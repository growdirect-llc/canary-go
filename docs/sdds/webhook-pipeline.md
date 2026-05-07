---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: handoff-ready
updated: 2026-04-28
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Webhook Pipeline

> **Type:** External Integration — Ingestion Gateway
> **Last reviewed:** 2026-04-13 (code review + ops upgrade)

**Owns:** Webhook receipt → HMAC verify → namespace resolve → queue.
**Feeds:** TSP.

---

## Purpose

The Webhook Pipeline is Canary's data ingestion front door. Every external event entering the system passes through this service: HMAC-validated, hashed, deduplicated, and published to a Valkey stream for downstream consumer processing. It is the only externally-accessible write endpoint in the Canary platform.

---

## RaaS Namespace Resolution

**RaaS namespace resolution is the first processing step after HMAC validation.**

After the signature is verified, the pipeline makes a synchronous REST call to the RaaS (Resolution as a Service) service to resolve the `raas:{merchant_id}` namespace for the incoming event. This namespace is the canonical identity token that ties the merchant across POS systems and is the routing key for the TSP stream.

### Resolution Contract

| Step | Description |
|---|---|
| 1. HMAC validation passes | Signature verified; raw bytes preserved |
| 2. `merchant_id` extracted from payload | Via JSON parse or source-specific extraction |
| 3. REST call to RaaS | `GET /raas/resolve/{merchant_id}` (or equivalent) |
| 4a. Resolution success | RaaS returns canonical namespace; pipeline continues |
| 4b. Resolution failure — merchant not onboarded | Pipeline rejects event with **404 Not Found**; event is NOT queued |

**Critical:** A 404 from RaaS means the merchant is not onboarded. The event is rejected, not dead-lettered and not queued. Square will retry, but the event will continue to be rejected until the merchant completes onboarding. This is correct behavior — events from unknown merchants must not enter the pipeline.

The namespace returned by RaaS is the canonical `raas:{merchant_id}` token. It is included in the stream envelope as the routing key for TSP consumers. See `docs/sdds/canary/raas.md` for the full RaaS service specification.

---

## [ARCHITECTURAL DIRECTION — not yet implemented] ILDWAC Dimension Capture

The webhook pipeline is where the `pos_port` and `device_id` ILDWAC dimensions enter the system for webhook-based POS integrations.

**`pos_port`** is derived from the `{pos-type}` path segment in `POST /webhooks/{pos-type}` — it is the registered adapter identifier (`"square"`, `"counterpoint"`, etc.).

**`device_id`** is extracted from the POS payload by the adapter's `ParseWebhook()` function and included in the emitted CanonicalEvent. See the Hawk SDD (Square mapping) and Bull SDD (Counterpoint mapping) for the specific field paths per event type.

**The MCP dimension of ILDWAC is NOT captured in the webhook pipeline.** It is captured when an MCP tool call authorizes a cost-affecting action. The pipeline only carries `pos_port` and `device_id` as pass-through fields in the stream envelope. The MCP authorization middleware records the tool call context independently, at invocation time, for later attribution to the stock ledger's ILDWAC vector.

---

## Dependencies

| Dependency | Type | Required | Notes |
|------------|------|:--------:|-------|
| PostgreSQL 17 (`canary` database) | Database | Yes | `canary_sales.ingestion_log` writes, `canary_app.merchants` lookups |
| Valkey 8 — dedup namespace | Cache | Yes (fail-open) | Idempotency keys (`SET NX`, 24h TTL). Fails open — database unique constraint is the backstop |
| Valkey 8 — webhook stream (`canary:events`) | Stream | Yes (hard) | Primary event stream. Unavailability = 503, event rejected |
| Square Webhooks API | External | Yes | Source of all webhook POSTs. Per-subscription HMAC key |
| Square Orders API | External | No | Enrichment only — `order.*` webhooks fetch full order data. Best-effort. |
| TSP Pipeline Stage Workers | Downstream | No (decoupled) | Read from Valkey stream independently. Webhook gateway does not wait for them. |

---

## Data Flow & PII Map

### What Enters

Square sends `POST /webhooks/square` with:
- Raw JSON body (max 1 MB) containing transaction, order, loyalty, payout, dispute, cash drawer, gift card, inventory, and timecard events
- `X-Square-Hmacsha256-Signature` header (Base64-encoded HMAC-SHA256)
- 27 registered webhook event types post-onboarding

### PII Fields in Webhook Payloads

| Field | Source | Classification | Storage | Notes |
|-------|--------|---------------|---------|-------|
| `raw_payload` (full JSON body) | Square POST | **restricted** | Plaintext in `evidence_records.raw_payload`, Valkey stream `canary:events` | Contains all fields below; no field-level redaction (P0) |
| `card_fingerprint` | `payment.card_details.card.fingerprint` | **sensitive** | Plaintext in `transactions.card_fingerprint` | Tokenized by Square but unique per card — linkable |
| `card_last4` | `payment.card_details.card.last_4` | **internal** | Plaintext in `transactions.card_last4`, `transaction_tenders.card_last4` | Last 4 digits of card |
| `card_bin` | `payment.card_details.card.bin` | **sensitive** | Plaintext in `transactions.card_bin` | First 6 digits — identifies issuing bank |
| `card_exp_month/year` | `payment.card_details.card.exp_month/year` | **sensitive** | Plaintext in `transactions` (P0 encrypt) | Card expiration. Classified sensitive — the combination of `card_last4` + expiry approaches PAN reconstruction (limited brute-force surface against an issuer's BIN range), so expiry is treated as PCI-adjacent and encrypted at rest. |
| `phone_number` | `loyalty_account.mapping.phone_number` | **sensitive** | **HMAC-SHA256 keyed hash** with `PHONE_HASH_KEY` in `loyalty_accounts.phone_hash` (Go build) | Prototype used plain SHA-256; Go build mandates HMAC because phone domain (NANP ≈ 10¹⁰) is brute-forceable for unkeyed hashes. See `go-security.md` → "PII Hashing Keys". |
| `employee_id` | Multiple event types | **internal** | Plaintext in multiple CRDM tables | Square team_member_id — identifies individual employees |
| `customer_id` | `payment.customer_id` | **internal** | Plaintext in `transactions.customer_id` | Square customer reference |
| `ip_address` | Request metadata | **sensitive** | Plaintext in `ingestion_log.ip_address` | Source IP of webhook POST (Square infrastructure IP) |
| `user_agent` | Request headers | **internal** | Plaintext in `ingestion_log.user_agent` | HTTP User-Agent of webhook sender |
| `merchant_id` | Payload root | **internal** | Plaintext across all tables | Square merchant identifier (tenant key) |
| `device_id` | `payment.device_details.device_id` | **internal** | Plaintext in `transactions.device_id` | POS terminal identifier |
| `location_id` | Multiple event types | **internal** | Plaintext across tables | Physical store location |

### What's Stored

| Store | Content | TTL |
|-------|---------|-----|
| Valkey dedup namespace | Keys `dedup:square:{source_event_id}` | 24h (auto-expire) |
| Valkey `canary:events` stream | 9-field envelope including `raw_payload` | Bounded by MAXLEN |
| `canary_sales.ingestion_log` | One row per accepted webhook (includes IP, user_agent) | No auto-purge (P1) |
| `canary_sales.evidence_records` | Write-once sealed copy (via Stage 1, includes `raw_payload`) | Permanent (legal hold) |
| `canary_app.webhook_events` | Raw payload storage, append-only | No auto-purge |
| `canary_app.schema_fingerprints` / `schema_drift_alerts` | Schema shape tracking (no PII) | No auto-purge |

### What Exits

- **To `canary:events` stream:** 9-field envelope with full `raw_payload` (consumed by Stage 1–3)
- **To `canary:detection` stream:** 5-field envelope (no PII, IDs and routing keys only — published by Stage 2)
- **HTTP response to Square:** Status + event_id only. No payload reflection.

---

## API Contract

### POST /webhooks/{pos-type}

Accepts POS-native webhook payloads. The `{pos-type}` path segment is a registered source identifier (only `square` is registered in the prototype; extensible per `pos-adapter-substrate.md`).

```
POST /webhooks/{pos-type}
Headers: X-Signature (Square uses X-Square-Hmacsha256-Signature)
Body: POS-native webhook payload (max 1 MB, configurable via MAX_PAYLOAD_BYTES)
Response 200: {"received": true, "event_id": "<ulid>", "received_at": "<iso8601>"}
Response 200 (duplicate): {"received": true, "event_id": "<existing-ulid>", "duplicate": true}
Response 400: {"error": "invalid signature"}
Response 404: {"error": "unknown source"}
Response 409: {"error": "duplicate event", "event_id": "<existing-ulid>"}
Response 413: {"error": "payload too large"}
Response 503: {"error": "stream unavailable", "retry_after_seconds": 30}
```

**Critical invariant:** Never return 200 unless the event has been published to the Valkey stream. The stream is the source of truth, not the database.

**Note on duplicate response codes:** The prototype returns 200 for duplicates with `"duplicate": true`. This is intentional — Square treats any 2xx as successful delivery and stops retrying. Implementors may choose 409 for cleaner semantics if the POS provider's retry behavior is confirmed to treat 4xx as do-not-retry.

### GET /webhooks/health

Returns stream connectivity status. Used by load balancers.

```
Response 200: {"status": "healthy", "queue_connected": true, "version": "1.0.0"}
Response 503: {"status": "degraded", "queue_connected": false, "version": "1.0.0"}
```

### GET /webhooks/ready

Readiness probe. Checks stream connection, signature key presence, and notification URL presence.

```
Response 200: {"status": "ready"}
Response 503: {"status": "not_ready", "errors": [...]}
```

Error values must be generic categories (`"queue_unavailable"`, `"signature_key_missing"`), not raw exception text.

### GET /webhooks/live

Process liveness probe. Always returns 200 if the service is running.

```
Response 200: {"status": "alive"}
```

### GET /receipt/by-hash/{event_hash_hex}

Lookup sealed evidence record by SHA-256 hex hash.

- Auth: JWT
- Response 200: Evidence record fields — `event_hash`, `chain_hash`, `chain_position`, Merkle proof path, inscription status
- Response 404: Not found

### GET /receipt/by-event/{event_id}

Same as by-hash, queried by ULID event_id.

---

## Stream Message Schemas

### canary:events (9-field envelope)

All values are strings (Valkey stream field values are string-typed; binary fields are hex-encoded).

| Field | Type | Description |
|-------|------|-------------|
| `event_id` | ULID | Canary-assigned identifier |
| `merchant_id` | string | Tenant partition key |
| `source` | string | POS provider: `square` or `counterpoint` |
| `source_event_id` | string | Provider-native event ID |
| `event_type` | string | Provider-native event type (e.g. `payment.created`) |
| `event_hash` | hex string (64 chars) | SHA-256 of raw payload bytes, computed before parsing |
| `raw_payload` | UTF-8 string | Verbatim POS payload (contains PII — see map above) |
| `received_at` | ISO 8601 | Timestamp of gateway receipt |
| `parse_failed` | `"true"` or `"false"` | Whether JSON parsing succeeded |

### canary:detection (5-field envelope)

| Field | Type | Description |
|-------|------|-------------|
| `transaction_id` | UUID | PK of the CRDM record written by Stage 2 |
| `merchant_id` | string | Tenant partition key |
| `event_type` | string | Original provider event type |
| `event_id` | ULID | Original gateway event_id |
| `detection_type` | string | Routing key: `transaction`, `cash_drawer`, `gift_card`, or `loyalty` |

---

## HMAC Signature Validation

**Method:** HMAC-SHA256 with timing-safe comparison.

**Formula:** `Base64(HMAC-SHA256(notification_url + raw_body_utf8, signature_key_utf8))`

**Header name (Square):** `X-Square-Hmacsha256-Signature`

**Validation sequence:**
1. Extract signature header (case-insensitive).
2. If header is absent or empty, return `false` immediately (treat as validation failure → 400).
3. Load signature key from configuration. If not configured, return 503.
4. Load notification URL from configuration. If not configured, return 503.
5. Compute `Base64(HMAC-SHA256(notification_url + raw_body_utf8, key_utf8))`.
6. Compare expected vs. received using a constant-time string comparison function.

**Key rotation:** Not implemented in prototype. Single static key per Square webhook subscription. Production implementation should support validating against current + previous key during a rotation window.

---

## Webhook Ingestion Sequence (10-step pipeline)

This is the complete ordered sequence for the gateway handler. The steps are non-negotiable; the patent-critical ordering (step 2 before step 3) must be preserved exactly.

```
Request arrives at POST /webhooks/{pos-type}

1. Read raw bytes from request body.
   BEFORE any JSON parsing. This is the patent-critical step.
   The hash is computed over these bytes in step 2.

2. Compute SHA-256 hash of raw bytes (event_hash).
   Hash is computed before parse. Do not parse first.

3. Validate source ∈ registered sources.
   → 404 if unknown source.

4. Check len(raw_bytes) <= MAX_PAYLOAD_BYTES.
   → 413 if oversized.

5. Verify HMAC-SHA256 signature (timing-safe).
   → 400 if invalid.
   → 503 if signature key or notification URL is not configured.

5a. RaaS namespace resolution.
    Parse merchant_id from raw bytes (lightweight extraction, not full parse).
    Call RaaS: GET /raas/resolve/{merchant_id}.
    → 404 if merchant not onboarded. Event rejected, not queued.
    → 503 if RaaS is unreachable (treat as hard failure; reject event).
    On success: canonical namespace attached to event envelope as routing key.

6. JSON parse raw bytes to extract merchant_id and event_type.
   If parse fails, set parse_failed=true. Continue — do not reject.

7. Dedup check: SET NX dedup:{source}:{source_event_id} with 24h TTL.
   If key already exists: return 200 with duplicate=true. Stop.
   If Valkey is unavailable: fail-open, continue.
   If no source_event_id extractable (parse failure): skip dedup. Continue.

7a. [Optional] Enrichment: for order.* events, fetch full order from Square Orders API.
    Best-effort. If API call fails, continue with original payload.

7b. [Optional] Tier 1 stateless Chirp evaluation (non-blocking, fire-and-forget).
    Failures are swallowed — webhook acceptance is not conditional on this.

8. Generate ULID as event_id.

9. XADD to canary:events stream with 9-field envelope.
   → 503 if stream is unavailable. This is the only hard failure after step 5.

10. INSERT ingestion_log (best-effort).
    Stream is the source of truth. DB failure here does not affect the 200 response.

11. Return 200 with event_id and received_at.
```

---

## [SPEC ADDITION — not in prototype] Idempotency Key Strategy

Duplicate suppression is two-layer:

**Layer 1 — Fast dedup (Valkey SET NX):**
- Key: `dedup:{source}:{source_event_id}`
- TTL: 24 hours
- Behavior: If key exists → duplicate. Return 200 with `"duplicate": true`. Square treats 2xx as successful, stops retrying.
- Fail-open: If Valkey is unavailable, allow the event through. Layer 2 provides backstop.
- No `source_event_id` (parse failure): skip dedup. Accept and hash the event.

**Layer 2 — Database backstop (unique constraint):**
- `canary_sales.evidence_records` has a unique constraint on `(merchant_id, event_id)`.
- Stage 1 handles `ON CONFLICT DO NOTHING` and ACKs silently.

**Dedup connection:** The dedup Valkey client must use a persistent connection pool, not a new connection per request. New-connection-per-request will exhaust file descriptors under sustained load.

---

## [SPEC ADDITION — not in prototype] Backpressure and Flow Control

The gateway does not implement backpressure against the pipeline. This is intentional — the gateway must return quickly or trigger Square's retry mechanism.

**Contract:**
1. Gateway → stream: fire-and-forget XADD. Unavailable stream → 503. Square retries for up to 72 hours.
2. Stream → stage workers: each worker reads at its own pace via XREADGROUP with configurable block timeout.
3. Stream bounds: `canary:events` should use `MAXLEN ~10,000` (approximate trimming). The stream is not a durable queue — the database is. Consumers that fall far enough behind lose access to old messages.
4. Consumer liveness: each stage worker writes a heartbeat key with a 120-second TTL on every iteration.
5. Observable: monitor stream length. Alert if `canary:events` length exceeds 10,000.

---

## [SPEC ADDITION — not in prototype] Consumer Group Semantics

Each pipeline stage is an independent consumer group on the same stream, creating a fan-out — every event is processed by all applicable stages.

**Consumer group names:**
- Stage 1: `sub1-seal` on `canary:events`
- Stage 2: `sub2-parse` on `canary:events`
- Stage 3: `sub3-merkle` on `canary:events`
- Stage 4: `detection-engine` on `canary:detection`

**Startup:** Consumer groups are created idempotently at service start. BUSYGROUP errors are ignored. Groups start at `$` (new messages only); PEL messages are reprocessed on restart.

**Concurrency:**
- Stage 1: exactly 1 worker (chain hash serialization constraint; multiple workers would produce non-deterministic chains).
- Stage 2 and 4: horizontally scalable within the consumer group.
- Stage 3: 1 worker (shared batch accumulator in Valkey).

**ACK rules (all stages):**
- ACK on success only.
- On failure: do NOT ACK. Message stays in PEL, redelivered on restart.
- On unrecoverable poison: route to `canary:dead_letter`, then ACK. The PEL must not grow indefinitely on poison messages.
- On unique constraint violation (duplicate): ACK silently.

---

## Degradation Behavior

| Failure | Behavior | HTTP Response |
|---------|----------|---------------|
| Signature key not configured | Reject all webhooks | 503 + `retry_after_seconds: 30` |
| Notification URL not configured | Reject all webhooks | 503 |
| Valkey stream unavailable | Reject event (never return 200 without stream write) | 503 + `retry_after_seconds: 30` |
| Valkey dedup cache unavailable | Fail open — allow event through | 200 (database constraint is backstop) |
| PostgreSQL unavailable (ingestion_log write) | Event still accepted — stream write is source of truth | 200 (log failure recorded to structured log) |
| JSON parse failure | Accept and hash raw bytes; `parse_failed=true` | 200 (Stage 1 stores raw, Stage 2 skips) |
| Square Orders API unavailable | Skip enrichment, use notification payload | 200 (best-effort enrichment) |
| Merchant lookup failure | Use Square merchant_id as-is | 200 (non-blocking) |
| Tier 1 stateless Chirp failure | Swallowed — webhook still accepted | 200 (additive, non-blocking) |

**Design principle:** The gateway returns 503 only when the Valkey stream is unavailable or HMAC validation cannot be performed. All other failures are non-blocking. Square retries 503 with backoff.

---

## Operations

### Startup Sequence

1. Service starts, initializes Valkey connection pool (dedup client + stream client as separate pools or logical clients).
2. Call `InitConsumerGroups()` to create all four consumer groups idempotently (BUSYGROUP errors ignored).
3. Stage workers start after the gateway is healthy (not a hard ordering requirement — BUSYGROUP guard makes each worker self-sufficient).
4. Stage 4 recovers PEL before processing new messages.

### Health Checks

| Endpoint | Interval | Healthy Condition |
|----------|----------|-------------------|
| `GET /webhooks/health` | 15s | Valkey stream reachable |
| `GET /webhooks/ready` | on-demand | Stream reachable + signature key present + notification URL present |
| `GET /webhooks/live` | 15s | Service process running |
| `canary:heartbeat:<stage-name>` | Per loop iteration (TTL 120s) | Age < 60s |

### Failure Modes

| Failure | Impact | Recovery |
|---------|--------|----------|
| Valkey stream down | All webhooks rejected with 503 | Square retries for 72 hours. No data loss if outage < 72h. |
| Valkey dedup cache down | Duplicates may slip through | `evidence_records` unique constraint prevents double-writes. Benign. |
| PostgreSQL down (ingestion_log) | Ingestion log gap | Events still flow to stream and pipeline. Consumers are source of truth. |
| Slow consumer processing | Stream accumulates | Consumers catch up independently. Alert if stream length > 10,000. |
| Consumer crash | Messages stay in PEL | Process manager restart reprocesses pending messages. |
| 10 consecutive consumer errors | Consumer self-terminates | Process manager brings it back. Requires investigation. |

### Monitoring

| Metric | Normal | Alert Threshold |
|--------|--------|-----------------|
| Webhook response time (p95) | < 50ms | > 500ms |
| 503 responses | 0 | > 5 in 5 minutes |
| 401 responses (HMAC failures) | 0 | > 10 in 1 hour |
| `canary:events` stream length | < 1,000 | > 10,000 |
| Dedup cache miss rate | < 1% | > 10% |
| Consumer heartbeat age | < 30s | > 60s |
| DLQ entries (unresolved) | 0 | > 50 |
| Schema drift alerts (unresolved) | 0 | Any new alert (Square API changed) |

---

## Schema Drift Detection

On each webhook: compute SHA-256 of the sorted set of field paths in the payload.
- Known hash: increment `occurrence_count` in `schema_fingerprints`.
- New hash: diff against the latest fingerprint for this `event_type`. Create a `SchemaDriftAlert` record with `new_fields` and `missing_fields` arrays.

Resolution path: update the parser for the new schema, mark `is_resolved = true` on the alert.

Tables:
- `canary_app.schema_fingerprints` — `(id, event_type, payload_hash UNIQUE, field_paths JSON, first_seen_at, last_seen_at, occurrence_count)`
- `canary_app.schema_drift_alerts` — `(id, event_type, new_fields JSON, missing_fields JSON, payload_hash, detected_at, is_resolved)`

---

## Data Model

### canary_app schema (3 tables for this service)

**webhook_events** — Raw payload storage, append-only.

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | |
| `merchant_id` | string | Tenant key |
| `event_id` | string UNIQUE | Square event_id |
| `event_type` | string | |
| `payload` | text | Raw JSON |
| `processed_at` | timestamp nullable | |
| `processing_status` | string | `pending` → `processed` or `failed` |
| `error_message` | text nullable | |

Indexes: `(merchant_id, event_type)`, `(merchant_id, processing_status)`

**schema_fingerprints** — Not tenant-scoped.

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | |
| `event_type` | string | |
| `payload_hash` | string UNIQUE | SHA-256 of sorted field path set |
| `field_paths` | JSON | All field names |
| `first_seen_at` | timestamp | |
| `last_seen_at` | timestamp | |
| `occurrence_count` | int | Default 1 |

**schema_drift_alerts**

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | |
| `event_type` | string | |
| `new_fields` | JSON | |
| `missing_fields` | JSON | |
| `payload_hash` | string | |
| `detected_at` | timestamp | |
| `is_resolved` | bool | Default false |

### canary_sales schema (19 tables, WRITE-ONCE IMMUTABLE)

All 19 `canary_sales` tables are append-only. No UPDATEs or DELETEs are permitted. Immutability is enforced by database triggers (SOX compliance).

**ingestion_log** — One row per accepted webhook.

| Column | Type | Notes |
|--------|------|-------|
| `merchant_id` | string | |
| `source` | string | |
| `event_id` | ULID | Canary-assigned |
| `source_event_id` | string | Provider-native |
| `event_type` | string | |
| `event_hash` | BYTEA | 32 bytes |
| `received_at` | timestamp | |
| `processed_at` | timestamp nullable | Set by Stage 2 |
| `status` | string | `accepted` → `parsed` |
| `ip_address` | string | Source IP (P0: not encrypted) |
| `user_agent` | string | |

**dead_letter_queue** — Events that exhausted retries (3 attempts: 5s, 30s, 5min backoff).

Fields: event identity, error context, attempt count, exhausted flag, full payload.

**evidence_records** — Write-once sealed evidence. Written by Stage 1.

| Column | Type | Notes |
|--------|------|-------|
| `id` | SERIAL PK | |
| `event_id` | ULID | |
| `merchant_id` | string | |
| `source` | string | |
| `source_event_id` | string | |
| `event_type` | string | |
| `event_hash` | BYTEA | 32 bytes |
| `chain_hash` | BYTEA | 32 bytes |
| `previous_chain_hash` | BYTEA nullable | NULL for genesis event |
| `raw_payload` | text | Verbatim payload (P0: not encrypted) |
| `parsed_payload` | JSONB nullable | NULL if `parse_failed` |
| `parse_failed` | bool | |
| `received_at` | timestamp | |

Unique: `(merchant_id, event_id)`. No UPDATEs. No DELETEs.

**inscription_pool** — Merkle batch records. Written by Stage 3.

| Column | Type | Notes |
|--------|------|-------|
| `batch_id` | ULID PK | |
| `merkle_root` | BYTEA | 32 bytes |
| `batch_event_count` | int | |
| `padded_leaf_count` | int | Nearest power of 2 |
| `tree_depth` | int | |
| `tree_algorithm_version` | int | Always 1 |
| `status` | string | `tree_built` → `confirmed` |
| `inscription_id` | string nullable | OrdinalsBot ID |
| `bitcoin_txid` | string nullable | |
| `bitcoin_block` | int nullable | |
| `block_explorer_url` | string nullable | |
| `fee_sats` | int nullable | |

**event_inscriptions** — Per-event Merkle proof paths. Written by Stage 3.

| Column | Type | Notes |
|--------|------|-------|
| `event_hash` | BYTEA | |
| `event_id` | ULID | |
| `merchant_id` | string | |
| `batch_id` | ULID FK | → inscription_pool |
| `leaf_index` | int | |
| `merkle_proof_path` | JSONB | `{siblings, root, leaf_hash, depth}` |

**transactions** — Core CRDM table. Source domains: payments, refunds, orders.

Key columns: `id` (UUID PK), `merchant_id`, `external_id` (Square ID), `transaction_type` (`SALE`/`RETURN`/`VOID`/`POST_VOID`/`NO_SALE`), `amount_cents`, `card_fingerprint`, `risk_level`, `entry_method`, `processing_fee_cents`, `employee_id`, `location_id`, `transaction_date`.

Conflict strategy: `ON CONFLICT (merchant_id, external_id) DO NOTHING`.

**transaction_line_items** — Order line items. FK to `transactions.id`.

Columns: `catalog_object_id`, `item_name`, `quantity`, `gross_sales_cents`, `discount_cents`, `tax_cents`, `is_voided`, `return_reason`.

**transaction_tenders** — Payment tenders. FK to `transactions.id`.

Columns: `tender_type` (`CARD`/`CASH`/etc.), `amount_cents`, `card_brand`, `card_last4`, `payment_id`.

**refund_links** — Junction: refund → original payment.

Columns: `refund_external_id`, `original_external_id`, `refund_amount_cents`, `reason`.

**cash_drawer_shifts** — Shift-level cash management.

Columns: `expected_cash_cents`, `actual_cash_cents`, `cash_variance_cents` (computed: actual − expected), employee attribution.

**cash_drawer_events** — Individual cash events (adds, removes, etc.).

**gift_card_activities** — Load/redeem activities.

Columns: `activity_type`, `balance_after_cents`, `amount_cents`.

**loyalty_accounts**

Columns: `square_loyalty_id`, `phone_hash` (BYTEA — `HMAC-SHA256(PHONE_HASH_KEY, normalize(phone))`), `points_balance`, `lifetime_points`.

**loyalty_events**

Columns: `event_type` (`ACCUMULATE`/`REDEEM`/`ADJUST`/`EXPIRE`), `points` (positive for accrual, negative for redemption/expiry).

**disputes**

Columns: `square_dispute_id`, `payment_id`, `order_id`, `reason`, `state`, `amount_cents`, `due_at`, `reported_at`.

**payouts**

Columns: `square_payout_id`, `status` (`SENT`/`PAID`/`FAILED`), `destination_type`, `amount_cents`, `arrival_date`, `failure_reason`.

**inventory_adjustments**

Columns: `adjustment_type` (`PHYSICAL_COUNT`/`ADJUSTMENT`), `quantity_change`.

**employee_timecards**

Columns: `clock_in`, `clock_out`, `breaks_json`, `hourly_rate_cents`.

**etl_batches** — Batch grouping for ingestion tracking.

Types: `WEBHOOK`, `INITIAL_SYNC`, `DAILY_REFRESH`, `BACKFILL`. Lifecycle: `running` → `completed` | `partial` | `failed`.

---

## Pipeline Stage Specifications

### Stage 1 — Hash & Seal

**Input:** Message from consumer group `sub1-seal` on `canary:events`. All 9 envelope fields required.

**Processing:**
1. Validate all 9 fields. Malformed envelope → dead letter + ACK.
2. Recompute `SHA-256(raw_payload_bytes)`. Compare to `event_hash` field. Mismatch → dead letter with `tamper_detected=true` + ACK.
3. Acquire advisory lock on `merchant_id` (serializes chain hash computation — exactly 1 worker allowed).
4. Query latest `chain_hash` for this merchant from `evidence_records`.
5. Compute: genesis = `SHA-256(event_hash_bytes)`; subsequent = `SHA-256(prev_chain_hash || event_hash_bytes)`.
6. INSERT `evidence_records` (write-once).
7. If `parse_failed = "false"`: parse `raw_payload`, populate `parsed_payload`.
8. XACK on success. ON CONFLICT (unique constraint) → ACK silently.

**Output:** One row in `canary_sales.evidence_records`.

**Error:** DB failure → no ACK (PEL redelivery). Consecutive errors (10) → self-terminate.

**Idempotency:** `ON CONFLICT (merchant_id, event_id) DO NOTHING`.

---

### Stage 2 — Parse & Route

**Input:** Message from consumer group `sub2-parse` on `canary:events`. If `parse_failed = "true"` → skip, ACK immediately.

**Processing:**
1. Route `event_type` through dispatch table → parser function + CRDM target + `detection_type`.
2. Log-only routes → ACK immediately, no writes.
3. Call parser (pure function: JSON dict in, flat field dict out, no side effects).
4. Build CRDM model instances:
   - Refunds: `Transaction` + `RefundLink`
   - Orders: `Transaction` + `TransactionLineItems` + `TransactionTenders`
   - Standard: single model
5. Write to appropriate `canary_sales` table(s).
6. Update `ingestion_log.status = "parsed"`.
7. If `detection_type` is set: XADD 5-field envelope to `canary:detection`.
8. XACK. ON CONFLICT → ACK silently.

**Square parser suite (pure functions):**
- `square_payment_parser` — payments and refunds
- `square_order_parser` — orders, line items, and tenders
- `square_loyalty_parser` — loyalty accounts and events (phone → `HMAC-SHA256(PHONE_HASH_KEY, normalize(phone))` before storage)
- `square_payout_parser` — payouts
- `square_dispute_parser` — disputes
- `square_auxiliary_parsers` — cash drawer shifts/events, timecards, inventory, gift cards

**Output:** One or more rows in `canary_sales`. `ingestion_log` status updated. Optionally one detection message published.

**Error:** DB failure → no ACK. Record not found on update → ACK (stale refs do not block pipeline).

**Idempotency:** `ON CONFLICT (merchant_id, external_id) DO NOTHING` on all CRDM tables.

---

### Stage 3 — Merkle Batcher

**Input:** Message from consumer group `sub3-merkle` on `canary:events`. Requires `event_hash` field. Does NOT ACK during accumulation.

**Processing:**
1. Add `event_hash` to Valkey sorted set `canary:batch:current` with score = Unix timestamp.
2. Track pending stream message IDs locally (not ACKed yet).
3. Flush when count ≥ 100 OR elapsed time since first message ≥ 600s:
   a. Read all entries from `canary:batch:current`, sorted hex ascending.
   b. Build Merkle tree:
      - Double-hash all leaves with SHA-256 (second-preimage protection).
      - Pad to nearest power of 2 by duplicating the last leaf.
      - Algorithm version: 1.
   c. In one database transaction: INSERT `inscription_pool` + all `event_inscriptions` records with proof paths.
   d. XACK all pending message IDs.
   e. Clear `canary:batch:current` accumulator.
4. Inscription: mocked until spend gate is approved (deterministic fake Bitcoin values).

**Merkle proof path per event:**
```json
{
  "leaf_hash": "<sha256-hex>",
  "leaf_index": 3,
  "depth": 4,
  "root": "<sha256-hex>",
  "siblings": ["<sha256-hex>", "<sha256-hex>", "<sha256-hex>", "<sha256-hex>"]
}
```

**Output:** One row in `inscription_pool` + N rows in `event_inscriptions` per batch.

**Idempotency:** Check for existing `batch_id` before inserting `inscription_pool`. If interrupted after DB write but before ACK, the retry must detect the existing record and skip re-insert.

---

### Stage 4 — Chirp Detection

**Input:** Message from consumer group `detection-engine` on `canary:detection`. All 5 detection envelope fields required.

**Processing:**
1. Route by `detection_type` to the Chirp Rule Engine:
   - `"transaction"` → evaluate transaction rules (read-only)
   - `"cash_drawer"` → evaluate cash drawer record
   - `"gift_card"` → evaluate gift card activity
   - `"loyalty"` → evaluate loyalty event
2. Reads from `canary_sales` (read-only session).
3. If rules fire → write alerts to `canary_app.alerts` (write session, separate transaction).
4. XACK. If referenced record not found → ACK (do not retry missing data).

**Dual-session requirement:** Two independent database connections. One reads `canary_sales`; one writes `canary_app`. They must not share a transaction.

**Output:** Zero or more rows in `canary_app.alerts`.

**Idempotency:** Alert creation keyed on `(merchant_id, event_id, rule_id)`. ON CONFLICT DO NOTHING.

---

### Error Handling (All Stages)

Consecutive error limit: 10 → stage self-terminates. Process manager must restart it automatically. Backoff formula: `min(2^(errors-1), 30)` seconds between retries. ACK only on success. Dead letter stream `canary:dead_letter` for poison messages. Consumer groups auto-created with BUSYGROUP guard at startup.

---

### Dead Letter Queue

**Retry strategy:** 5s → 30s → 5 minutes → exhausted (3 attempts max).

**Storage:** `canary_sales.dead_letter_queue` table with full error context (payload, error message, attempt count, last attempt timestamp).

**Management operations:** list, replay single, replay all, resolve (mark as handled). All write operations require authenticated admin session.

---

## Initial Data Sync

Post-OAuth, the onboarding coordinator registers 27 webhook event types, pulls 90-day history across 10 Square domains, and computes merchant baselines. Sync records are tagged `source_type = "BATCH"`. Error-isolated per domain. Gated by `CANARY_ONBOARDING_SYNC` flag.

---

## Configuration

| Variable | Required | Default | Purpose |
|----------|:--------:|---------|---------|
| `SQUARE_WEBHOOK_SIGNATURE_KEY` | Yes | — | Per-subscription HMAC key |
| `SQUARE_NOTIFICATION_URL` | Yes | — | URL Square posts to — part of HMAC computation |
| `MAX_PAYLOAD_BYTES` | No | 1048576 | Maximum acceptable payload size |
| `VALKEY_URL` | Yes | — | Valkey connection URL |
| `VALKEY_STREAM` | No | `canary:events` | Primary event stream name |
| `VALKEY_DEAD_LETTER_STREAM` | No | `canary:dead_letter` | Dead letter stream |
| `DETECTION_STREAM` | No | `canary:detection` | Detection routing stream |
| `SQUARE_ACCESS_TOKEN` | No | — | Required for order enrichment only |
| `SQUARE_ENVIRONMENT` | No | `sandbox` | Square API environment |
| `BATCH_COUNT_THRESHOLD` | No | 100 | Stage 3 flush count |
| `BATCH_TIME_THRESHOLD_SECONDS` | No | 600 | Stage 3 flush time |
| `MOCK_INSCRIPTION` | No | `true` | Stage 3 mock Bitcoin inscription |
| `CANARY_ENCRYPTION_KEY` | Yes (prod) | — | AES-256-GCM key (base64-encoded 32 bytes) |

---

## Deployment Target

- **Webhook gateway:** Load-balanced service, auto-scalable. ALB health check on `/webhooks/health`. TLS terminated at load balancer (Square requires HTTPS notification URLs in production).
- **Stage workers:** 1 process per stage, independently deployable and scalable (Stage 1 and 3 must not scale beyond 1 worker; see consumer group semantics above).
- **PostgreSQL:** RDS PostgreSQL 17 with Multi-AZ.
- **Valkey:** ElastiCache (Valkey-compatible). Separate logical namespaces for dedup and streams.
- **Secrets:** Secrets manager (AWS Secrets Manager or equivalent) for `SQUARE_WEBHOOK_SIGNATURE_KEY`, `SQUARE_NOTIFICATION_URL`, database credentials, `CANARY_ENCRYPTION_KEY`.

---

## Code Review Findings (Carry Forward to Go Implementation)

### P0 — Blocks Production

**P0-1: Signature key must not be loaded from environment files on disk**
- Load from secrets manager at startup. Cache in memory. Support rotation signal (dual-key validation during rotation window).

**P0-2: Raw webhook payloads stored with PII in plaintext**
- `raw_payload` is stored verbatim in both the Valkey stream and `evidence_records.raw_payload`. Payloads contain card fingerprints, BINs, employee IDs, phone numbers (pre-hash), customer IDs, and device IDs.
- Fix: AES-256-GCM encryption before storage. The hash-before-parse ordering (patent-critical) means `event_hash` is computed on unmodified bytes. `raw_payload` can be encrypted for at-rest storage without affecting hash integrity.

**P0-3: IP addresses logged in plaintext**
- `ingestion_log.ip_address` stores source IP in plaintext.
- Fix: Hash or mask before storage. Consider whether IP logging is needed at all (HMAC already authenticates the source).

### P1 — Before GA

**P1-1: No rate limiting on webhook endpoint**
- Apply rate limiting to `POST /webhooks/{pos-type}`. Suggested: 1,000 req/min per source IP. Must not block legitimate Square burst traffic (Black Friday volumes).

**P1-2: No audit logging for webhook operations**
- `ingestion_log` records accepted events, but there is no audit trail for: HMAC validation failures, dedup cache failures, enrichment API calls, or Tier 1 Chirp alert writes.
- Fix: Structured JSON audit log (distinct from application log) for all security-relevant operations.

**P1-3: No data retention policy**
- Define retention: `ingestion_log` 24 months, `evidence_records` permanent (legal hold), Valkey stream `MAXLEN ~100,000` for bounded memory. Implement automated cleanup.

**P1-4: Dedup client must use a persistent connection pool**
- New connection per request will exhaust file descriptors under sustained load. Use a persistent connection pool or a singleton client.

**P1-5: Error responses on /webhooks/ready must not expose internals**
- Return generic categories (`"queue_unavailable"`, `"signature_key_missing"`). Log full details server-side.

### P2 — Post-Launch

**P2-1: No key rotation procedure** — Document and implement dual-key HMAC validation during rotation window.

**P2-2: No observability instrumentation** — OTEL or equivalent: request duration, HMAC result, dedup hit rate, stream publish latency.

**P2-3: Order enrichment uses a global access token, not per-merchant OAuth token** — Production multi-tenant operation requires looking up the merchant's OAuth token from `merchant_credentials` based on `merchant_id`.

---

## Agent Attribution and the MCP Dimension

The webhook pipeline produces events that become cost-affecting actions in the stock ledger. Every such action will eventually carry an ILDWAC vector that includes the MCP dimension — the specific MCP tool call that authorized the action. This attribution is not captured in the pipeline itself.

The agent network (documented in `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md`) assigns 27 domain PMO agents to Canary's module spine. Each agent exposes its context and capabilities via MCP. When an agent node authorizes an inventory-affecting action — receiving goods, processing a return, executing a transfer — the MCP middleware records the tool call context: which agent, which server, which tool, which merchant. This becomes the MCP dimension in the ILDWAC vector, binding pipeline output to agent attribution. The full agent topology and contract model is documented in `agent-contracts.md` (to be authored separately).

---

## Production Readiness Checklist

- [ ] PII encrypted at rest (P0-2: `raw_payload`, card fields, IP address)
- [ ] Secrets loaded from secrets manager (P0-1)
- [x] Health check endpoints (`/webhooks/health`, `/webhooks/ready`, `/webhooks/live` — all specified)
- [ ] Audit logging for security-relevant operations (P1-2)
- [ ] Data retention policy (P1-3)
- [ ] Rate limiting on webhook endpoint (P1-1)
- [ ] Error responses do not expose internals (P1-5)
- [x] HMAC signature validation with timing-safe comparison (specified)
- [x] Idempotency via dedup cache + database unique constraint (specified)
- [x] Degradation behavior (503 for critical failures, fail-open for non-critical)
- [x] Consumer liveness via heartbeat (specified)
- [x] Dead letter queue with retry (specified)
- [x] Schema drift detection (specified)
- [ ] Key rotation procedure (P2-1)
- [ ] Observability instrumentation (P2-2)
- [ ] Persistent connection pool for dedup client (P1-4)

---

## Event Attribution Capabilities — Pipeline Contribution

The combination of capabilities assembled at the webhook pipeline entry point produces an event attribution depth that is structurally impossible with batch-polling legacy LP systems:

- **Real-time webhook capture** eliminates the polling gap — events arrive within seconds of the POS transaction, not at the next batch window.
- **HMAC-sealed payloads** authenticate the source at the byte level before any processing occurs.
- **Hash-before-parse ordering** (patent application #63/991,596) ensures the content hash is computed over unmodified bytes — the evidence is sealed before the system has touched the data.
- **Merkle inscription chain** anchors batches of sealed events to the Bitcoin blockchain, producing a tamper-evident public record of the event sequence.
- **ILDWAC dimension tagging at the pipeline entry point** — `pos_port` and `device_id` captured at the adapter boundary — means every cost-affecting event carries its provenance signature from the moment it enters the system.

Together these produce an evidentiary record where the cost of a unit of inventory is not a number — it is a vector: hashed, chain-linked, adapter-attributed, device-stamped, and agent-authorized. No legacy LP system has this. Batch polling systems cannot achieve it because the event metadata (device, connector, timing) is lost in aggregation.

---

*Canary | GrowDirect LLC | Confidential*
