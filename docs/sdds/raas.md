---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: raas
port: 8099
mcp-server: canary-raas
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# RaaS — Resolution as a Service

**Type:** Infrastructure Service — Identity Bridge + Evidentiary Backbone  
**Binary:** `cmd/raas` → `:8099`  
**MCP server:** `canary-raas` (9 tools)  
**Depends on:** `identity` (merchant existence), `webhook-pipeline` (calls this on every event)  
**Feeds:** `webhook-pipeline`, `owl`, `tsp`, `fox`, `hawk`, `returns`

RaaS is the namespace authority for the entire platform. It assigns every merchant a canonical identity token (`raas:{merchant_id}`) that persists regardless of which POS systems come and go, and maintains the append-only hash-chained event log that makes every retail event in the system tamper-evident and point-in-time reconstructible. Two function groups — namespace resolution and receipt chain — share one service because they share one invariant: the namespace is the key under which all receipts are chained.

**Required core, not an optional feature.** RaaS is foundational. The receipt chain — SHA-256 sealing, sequence integrity, append-only event log — operates **independently of L402, ILDWAC, blockchain anchoring, and vendor smart contracts**. With every Optional Feature env flag off (per `platform-overview.md` "Optional Features"), the chain still runs, still hashes, still sequences, still rejects out-of-order events, still produces verifiable receipts. The blockchain anchor (when `BLOCKCHAIN_ANCHOR_ENABLED=true`) takes the chain root asynchronously and inscribes it on a public L2 — anchor failures are non-blocking and the internal chain proceeds regardless. L402 (when `L402_ENABLED=true`) gates paid MCP tool calls on top of the chain — chain entry itself does not require Lightning settlement.

**Multi-tenant context.** RaaS namespace tables (`raas_namespaces`, `raas_source_registrations`) live in the global `public` schema — they are the routing layer that maps a merchant to their tenant schema. The receipt chain tables (`raas_events`, `raas_chain_state`) live per-tenant in `tenant_{merchant_id}` — every merchant's chain is isolated to their own schema. Cross-tenant chain queries are not permitted; cross-tenant analytics over the chain are produced via scheduled rollups into the `analytics` schema. See `architecture.md` "Multi-Tenant Isolation" for the canonical pattern.

---

## Business

### The Namespace Problem

A merchant with Square today and Counterpoint next year is one business. Without a persistent neutral identifier, every POS migration is a data rupture — history on the old system, new data on the new one, no coherent audit trail across the transition. The `raas:{merchant_id}` namespace is the bridge. It is created once at onboarding, is never deleted, and is the join key for every cross-source entity resolution in the platform.

This is also the commercial argument for Canary's multi-POS architecture: the retailer does not lose their history when they change POS. Their Canary record is continuous.

### The Receipt Chain Problem

A mutable database row is not evidence. A row that says "void transaction" is indistinguishable from a row that was fraudulently altered to say "void transaction." The receipt chain solves this by making every event append-only and cryptographically linked to the prior event. Corrections are new events; the original is never overwritten. An auditor — internal, regulatory, or judicial — can reconstruct the exact state of any record at any prior point in time, and verify that no event in the chain was altered after the fact.

### Business Rules

1. A merchant without a namespace cannot receive events. The webhook pipeline rejects events from unregistered merchants at the point of HMAC validation — not after queuing.
2. A namespace is never deleted. GDPR tombstoning deletes the event *content* linked to a namespace; the namespace row itself, its sequence counter, and its chain hashes are retained as proof that events existed and were deleted.
3. Every event in the chain must be acknowledged before the next is accepted. Out-of-sequence events are rejected, not queued for later.
4. The receipt chain is the authoritative source for `return_eligible()` decisions. No other service makes return eligibility determinations.

### Namespace Lifecycle

```
Merchant completes Square OAuth
         │
         ▼
Identity domain creates merchants row
         │
         ▼
RaaS.ensure_namespace(merchant_id)
         │  ← idempotent: creates if not exists, returns existing if present
         ▼
namespace_registrations row created
raas:{merchant_id} is now the canonical token
         │
         ▼
RaaS.register_source(merchant_id, provider, external_merchant_id)
         │
         ▼
merchant_sources row created, raas_namespace populated
         │
         ▼
Onboarding coordinator triggers initial sync
Events begin flowing; chain starts at sequence = 1
```

---

## Technical

### Service Boundaries

RaaS owns two mutually exclusive table groups. No other service writes to either group.

| Group | Tables | Purpose |
|-------|--------|---------|
| Namespace | `raas_namespaces`, `raas_source_registrations` | Merchant identity bridge |
| Receipt Chain | `raas_events`, `raas_chain_state` | Append-only event log |

Cross-reference: `merchant_sources.raas_namespace` is read by downstream services (Owl, webhook-pipeline) for cross-source resolution. Identity writes this column during onboarding — after RaaS `register_source` succeeds, the onboarding coordinator calls Identity with the `raas_namespace` value. RaaS never writes to `merchant_sources`.

### Data Model

#### `raas_namespaces`

```sql
CREATE TABLE raas_namespaces (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL UNIQUE REFERENCES merchants(id),
    namespace       TEXT NOT NULL UNIQUE,   -- "raas:{merchant_id}"
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- never has updated_at: this row is immutable after creation
    CONSTRAINT raas_namespace_format CHECK (namespace LIKE 'raas:%')
);

CREATE INDEX idx_raas_namespaces_merchant ON raas_namespaces(merchant_id);
CREATE INDEX idx_raas_namespaces_namespace ON raas_namespaces(namespace);
```

**Invariant:** one row per merchant, created once, never updated, never deleted.

#### `raas_source_registrations`

```sql
CREATE TABLE raas_source_registrations (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id          UUID NOT NULL REFERENCES merchants(id),
    namespace            TEXT NOT NULL REFERENCES raas_namespaces(namespace),
    provider             TEXT NOT NULL,          -- "square" | "counterpoint" | future
    external_merchant_id TEXT NOT NULL,          -- provider's native merchant ID
    registered_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deregistered_at      TIMESTAMPTZ,            -- soft delete only; namespace persists
    metadata             JSONB,                  -- webhook_signature_key, onboarding results
    UNIQUE(merchant_id, provider)
);
```

**Note on `metadata`:** `webhook_signature_key` stored here is a restricted credential. Encrypt with AES-256-GCM using the platform key. See `go-security.md`.

#### `raas_events`

The receipt chain. INSERT-only; no UPDATE or DELETE ever touches this table.

```sql
CREATE TABLE raas_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace       TEXT NOT NULL REFERENCES raas_namespaces(namespace),
    sequence_num    BIGINT NOT NULL,
    event_type      TEXT NOT NULL,       -- "transaction.created", "receiving.discrepancy", etc.
    source          TEXT NOT NULL,       -- "square" | "counterpoint" | "agent:{module}"
    actor_id        TEXT NOT NULL,       -- cashier ID, dock agent ID, MCP agent identity
    actor_type      TEXT NOT NULL,       -- "human" | "agent" | "system"
    occurred_at     TIMESTAMPTZ NOT NULL,
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload_hash    TEXT NOT NULL,       -- SHA-256 of canonical payload JSON
    chain_hash      TEXT NOT NULL,       -- SHA-256(payload_hash + occurred_at + sequence_num + prior_chain_hash)
    prior_hash      TEXT NOT NULL,       -- chain_hash of sequence_num - 1; "GENESIS" for sequence 1
    payload         JSONB NOT NULL,      -- event body (may be PII-classified; see Compliance)
    UNIQUE(namespace, sequence_num)
);

CREATE INDEX idx_raas_events_namespace_seq ON raas_events(namespace, sequence_num);
CREATE INDEX idx_raas_events_namespace_type ON raas_events(namespace, event_type);
CREATE INDEX idx_raas_events_namespace_time ON raas_events(namespace, occurred_at);
```

**INSERT trigger — sequence enforcement:**

```sql
CREATE OR REPLACE FUNCTION raas_enforce_sequence()
RETURNS TRIGGER AS $$
DECLARE
    expected_seq BIGINT;
    expected_prior TEXT;
BEGIN
    -- Lock the chain state row for this namespace to serialize concurrent inserts.
    -- FOR UPDATE prevents two concurrent appends from racing on sequence assignment.
    SELECT last_sequence + 1, last_hash
    INTO expected_seq, expected_prior
    FROM raas_chain_state
    WHERE namespace = NEW.namespace
    FOR UPDATE;

    IF NOT FOUND THEN
        -- First event for this namespace: bootstrap the chain state row.
        expected_seq := 1;
        expected_prior := 'GENESIS';
        INSERT INTO raas_chain_state(namespace, last_sequence, last_hash, last_updated)
        VALUES (NEW.namespace, 0, 'GENESIS', now())
        ON CONFLICT (namespace) DO NOTHING;
        -- Re-acquire with lock after bootstrap.
        SELECT last_sequence + 1, last_hash INTO expected_seq, expected_prior
        FROM raas_chain_state WHERE namespace = NEW.namespace FOR UPDATE;
    END IF;

    IF NEW.sequence_num != expected_seq THEN
        RAISE EXCEPTION 'RaaS sequence violation: expected %, got % for namespace %',
            expected_seq, NEW.sequence_num, NEW.namespace;
    END IF;

    IF NEW.prior_hash != expected_prior THEN
        RAISE EXCEPTION 'RaaS chain violation: prior_hash mismatch at sequence % for namespace %',
            NEW.sequence_num, NEW.namespace;
    END IF;

    -- Advance the cached chain head atomically within the same transaction.
    UPDATE raas_chain_state
    SET last_sequence = NEW.sequence_num,
        last_hash     = NEW.chain_hash,
        last_updated  = now()
    WHERE namespace = NEW.namespace;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_raas_enforce_sequence
    BEFORE INSERT ON raas_events
    FOR EACH ROW EXECUTE FUNCTION raas_enforce_sequence();
```

The database enforces chain integrity — not the application layer. An application bug that produces the wrong sequence number or prior hash fails at the DB constraint, not silently.

The `FOR UPDATE` on `raas_chain_state` serializes all concurrent appends to the same namespace at the DB row level. Concurrent writers queue behind the lock rather than racing on a full-table MAX() scan. This is why `append_event` in Go must use a `pgx` transaction: the INSERT into `raas_events` and the `raas_chain_state` update (performed inside the trigger) must be atomic — a rolled-back INSERT leaves the chain state unchanged.

#### `raas_chain_state`

Cached chain head for each namespace. Updated atomically with every INSERT into `raas_events`. Avoids a MAX() query on the hot write path.

```sql
CREATE TABLE raas_chain_state (
    namespace       TEXT PRIMARY KEY REFERENCES raas_namespaces(namespace),
    last_sequence   BIGINT NOT NULL DEFAULT 0,
    last_hash       TEXT NOT NULL DEFAULT 'GENESIS',
    last_updated    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Hash Chain Algorithm

```
payload_hash  = SHA-256(canonical_json(payload))
chain_hash    = SHA-256(payload_hash + "|" + occurred_at.RFC3339Nano + "|" + strconv.FormatInt(sequence_num, 10) + "|" + prior_hash)
```

`canonical_json` means: keys sorted lexicographically, no insignificant whitespace, UTF-8 encoded. The Go implementation uses `encoding/json` with a sorted-key marshaler. This is not `json.Marshal` of a struct — field order in structs is not guaranteed stable across Go versions. Use a `map[string]any` sorted explicitly before marshaling, or a dedicated canonical JSON library.

**GENESIS sentinel:** The first event in any namespace has `prior_hash = "GENESIS"` (literal string). This is the chain anchor.

### Namespace Resolution — Hot Path

The webhook pipeline calls `GET /raas/resolve/{merchant_id}` on every inbound event. This is the hottest read path in the service.

```
Resolution logic:
1. Check Valkey: GET raas:resolve:{merchant_id}
   → HIT: return cached namespace (TTL 5 minutes)
   → MISS: continue

2. SELECT namespace FROM raas_namespaces WHERE merchant_id = $1
   → FOUND: cache in Valkey (SET raas:resolve:{merchant_id} {namespace} EX 300)
             return 200 {namespace, resolved: true}
   → NOT FOUND: return 404 {resolved: false}

3. On 404: webhook pipeline rejects the event. Do NOT dead-letter.
   Square will retry. The event remains rejected until the merchant onboards.
```

**Cache invalidation:** None needed. Namespace is immutable once created. TTL of 5 minutes is a safety net, not a freshness requirement.

### API Contract

All routes require JWT auth except `/raas/healthz` and `/raas/readyz`.

```
GET  /raas/healthz                    → 200 (process alive; shallow check only)
GET  /raas/readyz                     → 200 | 503 (DB + Valkey check)
GET  /raas/resolve/{merchant_id}      → 200 | 404
POST /raas/namespaces                 → 201 (onboarding: create namespace)
POST /raas/sources                    → 201 (register a POS source)
POST /raas/events                     → 201 (append event to chain)
GET  /raas/chain/{namespace}/verify   → 200 {valid: bool, checked: N, first_bad_seq: N|null}
```

### MCP Tool Surface — `canary-raas` (9 tools)

| Tool | Input | Output | SLA | Notes |
|------|-------|--------|-----|-------|
| `resolve_namespace` | `merchant_id` | `{namespace, resolved}` | <50ms P99 | Valkey-cached; DB fallback |
| `ensure_namespace` | `merchant_id` | `{namespace}` | <10ms | Idempotent upsert; INSERT ON CONFLICT DO NOTHING; returns namespace string |
| `register_source` | `merchant_id, provider, external_merchant_id` | `{source_id}` | <500ms | Writes `raas_source_registrations` only. Onboarding coordinator relays `raas_namespace` back to Identity for `merchant_sources` update. |
| `append_event` | `namespace, event_type, source, actor_id, actor_type, occurred_at, payload` | `{sequence_num, chain_hash}` | <2s P99 | Enforced by DB trigger |
| `receipt_hash` | `namespace, sequence_num` | `{chain_hash, payload_hash, occurred_at}` | <100ms | Read from `raas_events` |
| `verify_chain` | `namespace, from_seq, to_seq` | `{valid, checked, first_bad_seq}` | <5s for 10k events | Used by external auditors |
| `return_eligible` | `namespace, transaction_id` | `{eligible, reason, expires_at}` | <200ms | Reads event chain for return window |
| `event_stream` | `namespace, after_seq, limit` | `[]{event}` | <500ms | Cursor-based; max 1000 events per call |
| `receiver_attribution` | `namespace, sequence_num` | `{actor_id, actor_type, occurred_at}` | <100ms | Returns who processed the event |

### Go Implementation Notes

- Use `pgx` transactions for `append_event`: INSERT into `raas_events` + UPDATE `raas_chain_state` must be atomic.
- The sequence enforcement trigger fires inside the transaction — if it raises, the transaction rolls back. The Go handler returns 409 Conflict on a sequence violation (not 500).
- `verify_chain` streams rows via a server-side cursor (`pgx.Rows`), not a bulk fetch. Computing chain integrity for large namespaces must not OOM the service.
- The canonical JSON marshaler for `payload_hash` computation must be tested with a fixed test vector. Put the test vector in `internal/testutil/raas_chain_test_vectors.go`.
- `ensure_namespace` is idempotent — multiple calls with the same `merchant_id` are safe. Under the hood it does `INSERT INTO raas_namespaces ON CONFLICT DO NOTHING`, then SELECT. `POST /raas/namespaces` delegates to the same internal function. Both require the `onboarding` role claim in the JWT.

### Blockchain Anchor — Feature Flag Governance

The public blockchain namespace anchor (L2 inscription of chain hashes via `blockchain-anchor.md`) is a V1+ optional overlay. The core namespace resolution and receipt chain functions operate entirely without it.

**Governing rule:** Core retail operations — transaction sync, EJ Spine events, receiving, sales reporting — are NEVER blocked by blockchain availability, L402 wallet state, or external chain connectivity. The blockchain is a consumer of RaaS chain state, not a dependency.

**Feature flag:** `feature.blockchain_anchor_enabled` (boolean, platform-level, default: `false`). When false, `append_event` completes without publishing to blockchain-anchor. When true, `append_event` fires a non-blocking goroutine that POSTs to the blockchain-anchor service; failure is logged and alerted but does not fail the append.

**L402 OTB per-node:** L402 wallet enforcement is tracking-by-default, enforcement opt-in per node. A node with an L402 wallet below threshold is flagged in reporting — it does not block the register, the transaction, or the event chain. The `feature.l402_enforcement_enabled` flag (node-level, default: `false`) controls whether threshold breaches actively block operations.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `resolve_namespace` (Valkey hit) | <5ms | <20ms | 100ms | Alert + fallback to DB |
| `resolve_namespace` (DB fallback) | <20ms | <100ms | 500ms | Alert |
| `append_event` | <200ms | <2s | 5s | Reject; do not queue |
| `verify_chain` (1k events) | <500ms | <2s | 10s | Alert |
| `verify_chain` (10k events) | <2s | <5s | 30s | Alert |

The `append_event` 2s P99 SLA is a hard platform NFR (see `platform-performance-nfrs`). An event that cannot be appended within 5s is rejected — the caller must retry. There is no dead-letter queue for chain events.

### Health Endpoints

```
GET /raas/healthz

Shallow liveness check — returns 200 if the process is up.
Never checks DB or Valkey. Returns 503 only on catastrophic internal panic.

Response 200:
{
  "status": "ok"
}
```

```
GET /raas/readyz

Deep readiness check — verifies DB connection pool and Valkey reachability.

Response 200:
{
  "status": "ok",
  "namespace_count": 42,
  "chain_lag_ms": 12,        // time since last event appended across all namespaces
  "valkey_ok": true,
  "db_ok": true
}

Response 503 if DB or Valkey unreachable.
```

GCP Cloud Run liveness probe: `GET /raas/healthz` — shallow check; a liveness failure restarts the container. Reserve for true deadlock/panic scenarios — never check DB or Valkey here.

GCP Cloud Run readiness probe: `GET /raas/readyz` — checks DB connection pool and Valkey reachability. Returns 503 if either is unreachable. A readiness failure removes the instance from the load balancer without killing the process — it can recover automatically when the dependency comes back, with no cold start.

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| DB unreachable | `resolve_namespace` falls back to Valkey only for cached namespaces; returns 503 for cache misses. `append_event` returns 503. | Auto-recovery on DB reconnect via pgx connection pool. |
| Valkey unreachable | `resolve_namespace` falls back to DB. All reads degrade to DB latency. | Log warning; continue operating. No alarm needed unless DB also shows elevated latency. |
| Sequence violation (trigger fires) | `append_event` returns 409 Conflict. Chain state is unchanged. | Caller investigates: fetch `last_sequence` from `raas_chain_state`, recompute next sequence, retry. |
| Chain hash mismatch on verify | `verify_chain` returns `{valid: false, first_bad_seq: N}`. | Immediate alert. Do not auto-remediate. Escalate to DBA agent for forensic review. |

### Graceful Shutdown

```go
// Signal handling — standard pattern across all Canary services
// See go-runtime.md for the shared shutdown.go helper
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

// Give in-flight append_event requests time to commit
srv.Shutdown(ctx) // waits up to ShutdownTimeout (default 30s)
pool.Close()      // close pgx pool after HTTP server drains
```

In-flight `append_event` calls must complete before shutdown. The sequence trigger means a partial write is impossible (transaction either commits or rolls back), but a connection killed mid-flight will roll back cleanly — the caller gets a 500 and must retry.

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `raas:resolve:{merchant_id}` | 300s | Namespace resolution cache |
| `raas:chain:{namespace}:head` | None | Not used — chain state is in DB (`raas_chain_state`) |

Do not cache chain state in Valkey. The DB trigger is the authority; a stale Valkey entry could allow a caller to submit an event with an incorrect sequence number that passes the cache check but fails the DB trigger, generating unnecessary 409s.

### Monitoring

Alert on:
- `resolve_namespace` P99 > 100ms sustained for 2 minutes
- `append_event` P99 > 2s sustained for 1 minute  
- Any `verify_chain` result with `valid: false`
- `raas_chain_state.last_updated` for any namespace older than 1 hour during business hours (stale chain — events may have stopped flowing)

---

## Compliance

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `payload` | `raas_events` | Varies by `event_type` | Classify per event type. Transaction payloads may contain cardholder data (PCI DSS scope). Employee payloads contain names. Apply field-level encryption for PCI-scoped fields. |
| `external_merchant_id` | `raas_source_registrations` | Sensitive | Encrypt at rest (AES-256-GCM). This is Square's or NCR's native merchant identifier — do not expose in logs or error messages. |
| `metadata.webhook_signature_key` | `raas_source_registrations` | Restricted | Encrypt at rest. Key rotation procedure: deregister old key via Square API, register new key, update row. Old key is retained in `metadata` history array for 30 days for replay validation. |

### Append-Only Invariant — Audit Posture

The `raas_events` table has no UPDATE or DELETE path in the application code. The sqlc-generated queries for this table are INSERT-only. Enforce at the database level:

```sql
-- Revoke UPDATE and DELETE from the application role
REVOKE UPDATE, DELETE ON raas_events FROM canary_app;
```

The DBA agent owns this grant. Any migration that attempts to add an UPDATE or DELETE path to `raas_events` must be reviewed by the Legal & Compliance agent before execution.

### GDPR Tombstoning

GDPR right-to-erasure applies to event payload content, not to the chain structure. RaaS implements cryptographic erasure — `raas_events` is never updated.

**Encryption at ingestion:** every `append_event` call encrypts the payload with a per-subject AES-256-GCM key before writing. Keys are stored in a separate table:

```sql
CREATE TABLE raas_subject_keys (
    namespace           TEXT NOT NULL REFERENCES raas_namespaces(namespace),
    subject_id          TEXT NOT NULL,   -- pseudonymous subject identifier (hashed)
    encryption_key_ciphertext BYTEA,     -- AES-256-GCM key, itself encrypted with platform key; NULL after erasure
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    erased_at           TIMESTAMPTZ,     -- set on erasure; NULL until then
    PRIMARY KEY (namespace, subject_id)
);
```

**Erasure procedure:**

1. Identify the `subject_id` and the namespace.
2. Execute: `UPDATE raas_subject_keys SET encryption_key_ciphertext = NULL, erased_at = now() WHERE namespace = $1 AND subject_id = $2`.
3. The payload rows in `raas_events` remain intact — their ciphertext is permanently unreadable without the key. `payload_hash` values remain valid (they were computed over the ciphertext, not the plaintext). Chain hashes are unaffected. Chain integrity is preserved.
4. Append a tombstone event to the chain — no UPDATE, just a new INSERT:
   - `event_type = "gdpr.tombstone"`
   - payload: `{"subject_id": "<pseudonymous>", "erased_at": "...", "affected_event_count": N}`
   - This event is the permanent proof that erasure occurred.
5. The blockchain anchor service publishes the tombstone event hash to the L2 anchor. See `blockchain-anchor.md`.

**`verify_chain` behaviour after erasure:** the verifier will encounter payload_hash values it cannot re-derive (ciphertext payloads it cannot decrypt). The tombstone event marks the affected namespace and subject. Verifiers must be tombstone-aware: when a `gdpr.tombstone` event is encountered for a subject, skip payload_hash re-derivation for that subject's sequence range — the chain hash sequence itself remains intact and verifiable. Hash mismatches for tombstoned subjects are expected and documentable, not evidence of tampering.

**No UPDATE to `raas_events` ever occurs.** The append-only invariant is absolute.

### Evidence Chain Certification

`verify_chain` is the external auditor interface. Any party — court, insurer, regulator, enterprise buyer — can run `verify_chain` against a namespace and receive a cryptographic proof that the event sequence is unbroken and unaltered. The output is intended to be reproducible: given the same namespace and sequence range, the result is deterministic.

Patent application #63/991,596 covers the hash-before-parse, chain hash, and Merkle inscription elements of this architecture. The `chain_hash` computation in `raas_events` is within the scope of this patent.

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `raas_namespaces` | Indefinite | Namespace is permanent |
| `raas_events` | 7 years | Financial record retention (IRS, SOX) |
| `raas_source_registrations` | 7 years after deregistration | Audit trail |
| `raas_chain_state` | Current only | Operational; no retention requirement |

---

## Related SDDs

- `webhook-pipeline.md` — calls `resolve_namespace` on every inbound event; 404 = reject event
- `identity.md` — creates the `merchants` row that RaaS namespaces reference
- `tsp-seal.md` — Sub1 hash sealing happens upstream of the RaaS chain; the sealed hash feeds `append_event`
- `blockchain-anchor.md` — anchors RaaS chain hashes to L2; non-blocking consumer of this service
- `field-capture.md` — field capture events are appended to the RaaS chain via `append_event`
- `three-way-match.md` — three-way match completion appends a chain event for the matched record
- `returns.md` — `return_eligible` is the authoritative return window check
- `owl.md` — reads `raas_namespace` from `merchant_sources` for cross-source entity resolution
