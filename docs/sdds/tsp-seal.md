---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
patent: "Application #63/991,596 (hash-before-parse, chain hash, Merkle inscription)"
---

# TSP Stage 1 — Hash & Seal

> **Type:** Pipeline Consumer Worker — Write-Once Evidence Sealing
> **Parent SDD:** `tsp.md` (pipeline overview)
> **Consumer Group:** `sub1-seal` on stream `canary:events`
> **Patent Scope:** This stage implements the two patent-covered primitives: hash-before-parse (SHA-256 computed from raw bytes prior to any parsing) and chain hash (SHA-256 of concatenated prior chain hash and current event hash). Both are patent-critical. Do not reorder or defer.

---

## Governing Thesis

Stage 1 is the evidence anchor for the entire Canary pipeline. Its single responsibility is to transform a raw, unverified stream message into a tamper-evident, write-once record that can serve as forensic evidence. Everything downstream — detection, Merkle inscription, receipts — derives its evidentiary weight from the seal computed here. If the seal is wrong, the chain is meaningless.

The architectural bet is simple: the hash is computed from the wire bytes, before parsing, so no downstream transformation can produce the same hash from different content. Tamper = hash mismatch. The sealed record is the fact; everything else is analysis.

---

## Business Hat

### Why This Stage Exists

Loss prevention at the SMB tier fails for one reason: there is no credible chain of custody from the POS event to the alert. Operators and employees both know that digital records can be edited. The sealed evidence record changes that calculus. The `event_hash` and `chain_hash` together prove: (a) this event arrived with this content, (b) it arrived in this sequence relative to other events for this merchant, and (c) the record has not been modified since sealing.

That proof is the product. The detection and analytics are the features. The seal is the moat.

### Business Invariants

| Invariant | Why It Matters |
|-----------|---------------|
| Hash computed from wire bytes, before parsing | Any altered payload produces a different hash — forgery is detectable without comparing to the original |
| Chain hash serialized per merchant | Sequence integrity: a gap or reorder in the chain is detectable |
| Write-once: no UPDATE, no DELETE | Evidence records must survive audit scrutiny; mutability destroys admissibility |
| Tamper-detected events quarantined, not discarded | The tamper record itself is evidence — silent discard destroys the audit trail |

### Compliance Value

The sealed `event_hash` + `chain_hash` is the anchor for the Merkle inscription in Stage 3. A single L2 blockchain transaction proves the existence and sequence of up to 1,000 events. This supports LP investigations, insurance claims, SOX compliance for publicly-held retailers, and any third-party audit requiring tamper-evident records.

---

## Technical Hat

### Consumer Loop

Stage 1 runs as a single-worker process (see Concurrency Constraint below). The loop:

1. Call `XREADGROUP GROUP sub1-seal worker-1 COUNT 1 BLOCK 5000 STREAMS canary:events >`
2. On timeout (no messages): write heartbeat, iterate.
3. On message received: process (see Processing Contract below).
4. On success: `XACK canary:events sub1-seal <message-id>`.
5. On failure: do NOT ACK. Message stays in PEL for redelivery.
6. Consecutive error tracking: if 10 consecutive errors, self-terminate. Process manager restarts.

**PEL recovery at startup:** Before switching to `>` (new messages), read and reprocess all pending entries from the PEL using `XREADGROUP ... COUNT 100 ... STREAMS canary:events 0`. This handles in-flight messages from a prior crash.

### Processing Contract (per message)

```
1. VALIDATE — all 9 envelope fields present and non-empty.
   → Missing required fields: route to dead_letter, ACK.

2. HASH VERIFY — recompute SHA-256(raw_payload_bytes).
   → Compare to event_hash field (hex decode first).
   → Mismatch: route to dead_letter with tamper_detected=true, ACK. Do not write to evidence_records.
   → Match: proceed.

3. ADVISORY LOCK — acquire PostgreSQL advisory lock keyed on merchant_id hash.
   → Serializes chain computation for this merchant across any future multi-worker scenarios.
   → Lock held only for steps 4–6.

4. QUERY PRIOR CHAIN — SELECT chain_hash FROM evidence_records
   WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT 1.
   → NULL result = genesis event.

5. COMPUTE CHAIN HASH:
   → Genesis:     chain_hash = SHA-256(event_hash_bytes)
   → Subsequent:  chain_hash = SHA-256(prev_chain_hash_bytes || event_hash_bytes)
   → Concatenation is raw byte concatenation (no delimiter, no encoding).

6. INSERT evidence_records (write-once; trigger blocks UPDATE and DELETE).
   → ON CONFLICT (merchant_id, event_id) DO NOTHING — duplicate = silent ACK.

7. RELEASE advisory lock.

8. PUBLISH to canary:events-sealed (optional downstream notification stream).
   → Best-effort; failure does not block ACK.
```

### The Patent-Critical Primitive

The hash-before-parse invariant is enforced by the position of step 2 in the loop. The `raw_payload` in the stream message is the verbatim wire bytes as received by the gateway. Stage 1 does not call any parser before computing and verifying the hash. In Go, this means:

```go
// CORRECT — hash the raw string from the stream message
h := sha256.Sum256([]byte(msg.Values["raw_payload"].(string)))

// WRONG — never parse before hashing
var parsed map[string]any
json.Unmarshal(rawBytes, &parsed) // DO NOT DO THIS BEFORE HASHING
```

The patent claim covers this specific ordering. Any implementation that parses before hashing invalidates the claim.

### Chain Hash Algorithm

```go
func computeChainHash(eventHash []byte, priorChainHash []byte) []byte {
    h := sha256.New()
    if priorChainHash == nil {
        // Genesis: hash of event_hash only
        h.Write(eventHash)
    } else {
        // Subsequent: prior_chain_hash || event_hash
        h.Write(priorChainHash)
        h.Write(eventHash)
    }
    return h.Sum(nil)
}
```

This is identical to the chain hash algorithm in `raas.md`. Do not diverge. The algorithm version is implicitly 1; if the algorithm changes, it must be versioned and the old records left intact.

### HMAC Validation (Gateway, not Stage 1)

HMAC validation for Square webhook events occurs in the webhook gateway handler **before** the event is published to `canary:events`. By the time Stage 1 reads a message from the stream, HMAC validation has already passed. Stage 1 does not re-validate HMAC. The `event_hash` recomputation in step 2 above is a content integrity check (tamper detection), not an authentication check.

For completeness: the HMAC formula is `Base64(HMAC-SHA256(notification_url + raw_body_utf8, signature_key_utf8))`. Comparison is constant-time. A failed HMAC at the gateway means the event never reaches the stream and never reaches Stage 1.

### Concurrency Constraint

Stage 1 is limited to **exactly one worker**. The advisory lock per merchant serializes chain hash computation, but only within a single process. Two workers holding advisory locks for different merchants would not conflict — the lock is per-merchant — but two workers racing for the same merchant lock would deadlock or produce non-deterministic chain sequences if the lock implementation has any race. The safe design is one worker, full stop. Document this constraint explicitly in the process manager configuration.

If horizontal scaling is ever needed, the chain hash computation must be moved to a CAS (compare-and-swap) loop in PostgreSQL using a dedicated `chain_state` table, with the worker lock replaced by an optimistic row lock. That is a separate design, not an extension of this one.

---

## Data Model

### `canary_sales.evidence_records`

The write-once evidence table. A PostgreSQL trigger enforces the append-only invariant.

```sql
CREATE TABLE canary_sales.evidence_records (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        TEXT NOT NULL,                      -- ULID from gateway
    merchant_id     TEXT NOT NULL,
    source          TEXT NOT NULL,                      -- 'square' | 'counterpoint'
    source_event_id TEXT NOT NULL,                      -- provider-native event ID
    event_type      TEXT NOT NULL,
    event_hash      BYTEA NOT NULL,                     -- SHA-256 of raw_payload bytes
    chain_hash      BYTEA NOT NULL,                     -- chain hash (see algorithm above)
    previous_chain_hash BYTEA,                          -- NULL for genesis events
    raw_payload     TEXT NOT NULL,                      -- verbatim wire payload (PII — P0 encrypt)
    parsed_payload  JSONB,                              -- NULL if parse_failed=true
    parse_failed    BOOLEAN NOT NULL DEFAULT false,
    received_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_evidence_merchant_event UNIQUE (merchant_id, event_id)
);

-- Write-once enforcement trigger
CREATE OR REPLACE FUNCTION canary_sales.evidence_records_immutable()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'evidence_records is append-only: UPDATE not permitted';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evidence_records is append-only: DELETE not permitted';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER evidence_records_immutability
    BEFORE UPDATE OR DELETE ON canary_sales.evidence_records
    FOR EACH ROW EXECUTE FUNCTION canary_sales.evidence_records_immutable();

-- Indexes
CREATE INDEX idx_evidence_merchant_received ON canary_sales.evidence_records (merchant_id, received_at DESC);
CREATE INDEX idx_evidence_hash ON canary_sales.evidence_records USING hash (event_hash);
```

**P0 encryption note:** `raw_payload` and `parsed_payload` must be encrypted with AES-256-GCM before INSERT (finding P0-TSP-01). The `event_hash` and `chain_hash` fields are hashes, not PII — they are stored and indexed unencrypted. The encryption key is loaded from secrets manager at startup (finding P0-TSP-07). Decryption occurs only in evidence audit workflows, not in the hot path.

### `canary_sales.dead_letter_queue`

Dead-lettered events from Stage 1 (and all stages) land here and in the `canary:dead_letter` Valkey stream.

```sql
CREATE TABLE canary_sales.dead_letter_queue (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_message_id TEXT NOT NULL,
    stage           TEXT NOT NULL,                      -- 'seal' | 'parse' | 'merkle' | 'detect'
    event_id        TEXT,
    merchant_id     TEXT,
    raw_envelope    JSONB NOT NULL,                     -- full stream message for replay
    failure_reason  TEXT NOT NULL,
    tamper_detected BOOLEAN NOT NULL DEFAULT false,
    retry_count     INT NOT NULL DEFAULT 0,
    last_retry_at   TIMESTAMPTZ,
    exhausted       BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

---

## API Contract

Stage 1 does not expose HTTP endpoints directly. The receipt and management endpoints in the parent `tsp.md` service expose Stage 1 artifacts:

### `GET /receipt/by-hash/{event_hash_hex}`

Returns the sealed evidence record for a given SHA-256 hex hash. Requires JWT auth.

```
Response 200:
{
  "event_id": "<ulid>",
  "merchant_id": "<id>",
  "source": "square",
  "event_type": "payment.created",
  "event_hash": "<sha256-hex>",
  "chain_hash": "<sha256-hex>",
  "previous_chain_hash": "<sha256-hex | null>",
  "received_at": "<iso8601>",
  "inscription_status": "pending | inscribed | mocked",
  "merkle_proof": { ... }  // populated after Stage 3 flush
}
```

**Audit logging requirement (P1-TSP-01):** Every receipt lookup must write an audit log entry: `{accessor_user_id, event_hash, accessed_at, ip_address}`. This is non-negotiable for any installation used in an active LP investigation.

### MCP Tool Surface

| Tool | Write | Description |
|------|:-----:|-------------|
| `get_receipt` | No | Retrieve sealed evidence receipt by hash or event_id |
| `verify_merkle` | No | Verify Merkle inclusion proof for a sealed event |
| `get_dead_letters` | No | List dead-lettered events (includes tamper alerts) |
| `replay_event` | Yes | Replay a DLQ entry back to `canary:events` — admin only |

All write-capable MCP tools require an authenticated admin session (P0-TSP-09).

---

## SLA

| Metric | P50 | P99 | Hard Limit |
|--------|-----|-----|------------|
| Message processing latency (stream read → DB write → ACK) | 8ms | 40ms | 500ms |
| Advisory lock acquisition (per merchant) | <1ms | 10ms | 100ms |
| SHA-256 recomputation (1 MB payload) | <1ms | 2ms | 10ms |
| Dead-letter routing | 5ms | 20ms | 100ms |
| Heartbeat write | <1ms | 5ms | 30ms |

**Throughput:** Stage 1 is serialized per merchant (one worker, advisory lock). Throughput scales with the number of merchants, not with payload volume. Expected steady-state: 50–200 events/minute per merchant; single-worker capacity comfortably exceeds 10,000 events/minute across all merchants.

---

## Failure Modes

| Failure | Detection | Impact | Recovery |
|---------|-----------|--------|----------|
| Hash mismatch (tamper) | Step 2 comparison | Event quarantined to DLQ with `tamper_detected=true` | Manual investigation required; auto-alert on any tamper detection |
| Malformed envelope (missing fields) | Step 1 validation | Event quarantined to DLQ | No impact on pipeline; DLQ length metric alerts |
| PostgreSQL unavailable | DB write exception | No ACK; message stays in PEL | Auto-reconnect; PEL redelivered on recovery |
| Advisory lock timeout | Lock acquisition timeout | No ACK; retry on next iteration | Increase lock timeout; investigate DB contention |
| Valkey unavailable | XREADGROUP exception | Worker blocks; no progress | Worker reconnects via exponential backoff |
| Duplicate event (unique constraint) | `ON CONFLICT DO NOTHING` | Silent ACK; no double-write | Expected and correct behavior |
| 10 consecutive errors | Internal counter | Worker self-terminates | Process manager restart; alerts on repeated restarts |
| Encryption key unavailable (P0) | Startup check | Worker refuses to start | Fix secrets manager access; do not run unencrypted |

---

## Compliance

### Patent Scope

Patent Application #63/991,596 covers two primitives, both implemented in this stage:

1. **Hash-before-parse:** SHA-256 computed from raw bytes before any parsing. Enforced by the order of operations in the processing contract above.
2. **Chain hash:** SHA-256 of the concatenation of the prior chain hash and the current event hash, serialized per merchant. Enforced by the advisory lock and the chain hash algorithm.

Any modification to the hashing algorithm, the ordering of operations, or the concatenation formula must be reviewed for patent implications before implementation.

### PII Handling

| Field | Classification | Required Action |
|-------|---------------|----------------|
| `raw_payload` | Restricted | AES-256-GCM encrypt before INSERT (P0-TSP-01) |
| `parsed_payload` | Restricted | AES-256-GCM encrypt before INSERT (P0-TSP-01) |
| `event_hash`, `chain_hash` | Public (hash) | No encryption required |
| `merchant_id` | Internal | No encryption required |

The `canary:events` Valkey stream carries `raw_payload` in transit. Enable Valkey AUTH + TLS in production (P0-TSP-10).

### Append-Only Invariant

The PostgreSQL trigger on `evidence_records` enforces the write-once constraint at the database layer. Application-level code must never attempt UPDATE or DELETE on this table — but the trigger is the backstop. Evidence records must survive for 7 years under SOX retention requirements (P1-TSP-02).

---

## Configuration

| Variable | Default | Stage 1 Usage |
|----------|---------|---------------|
| `STAGE1_BLOCK_MS` | `5000` | XREADGROUP blocking timeout |
| `VALKEY_STREAM` | `canary:events` | Source stream |
| `VALKEY_DEAD_LETTER_STREAM` | `canary:dead_letter` | DLQ stream |
| `DATABASE_URL` | (required) | PostgreSQL connection |
| `CANARY_ENCRYPTION_KEY` | (required prod) | AES-256-GCM key for payload encryption |

---

## Open Items (Carry Forward to Go)

| # | Priority | Item |
|---|---------|------|
| P0-TSP-01 | P0 | Encrypt `raw_payload` and `parsed_payload` before INSERT |
| P0-TSP-07 | P0 | Load encryption key from secrets manager at startup |
| P0-TSP-10 | P0 | Enable Valkey AUTH + TLS |
| P0-TSP-09 | P0 | Require authenticated admin session for DLQ replay |
| P1-TSP-01 | P1 | Audit log for every receipt lookup |
| P1-TSP-02 | P1 | 7-year retention policy for `evidence_records` |
| P1-TSP-05 | P1 | Structured JSON logging with `event_id`, `merchant_id`, `stage` in every line |
| P1-TSP-06 | P1 | Unify DLQ: Valkey stream + PostgreSQL table as dual-write, DB as source of truth |

---

*Canary | GrowDirect LLC | Confidential*
