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

# Transaction Stream Processor (TSP) — Pipeline Overview

> **Type:** App Service (Canary) — Pipeline Coordinator
> **Status:** Production Readiness Review — 2026-04-13
> **Patent:** Application #63/991,596 (hash-before-parse, chain hash, Merkle inscription)

**Owns:** TSP orchestration (Sub1–Sub4).
**Feeds:** Chirp engine, stock ledger.

> **Note:** This document covers the TSP pipeline overview. Each stage has its own engineering SDD:
> - Stage 1 (Hash & Seal): `tsp-seal.md`
> - Stage 2 (Parse & Route): `tsp-parse.md`
> - Stage 3 (Merkle Batcher): `tsp-merkle.md`
> - Stage 4 (Detection Engine): `tsp-detect.md`

---

## Purpose

The Transaction Stream Processor is Canary's core data ingestion pipeline. It receives POS events from multiple providers — Square via webhook push, NCR Counterpoint via poll-based REST ingestion — validates authenticity (HMAC for webhooks, credential-based for polls), computes content-addressable SHA-256 hashes from raw bytes before any parsing, publishes events to a Valkey stream, and fans them out to four independent consumer groups. Each consumer is documented in its own SDD.

### Multi-POS Ingress Model

TSP supports two ingress shapes that coexist on the same downstream pipeline:

| Ingress | Provider | Mechanism | SDD |
|---|---|---|---|
| Webhook push | Square | `POST /webhooks/square` → HMAC validate → stream publish | This document |
| Poll pull | NCR Counterpoint | `CounterpointPoller` → watermark cursor → stream publish | `ncr-counterpoint-tsp-adapter.md` |

Both ingress paths produce identically-shaped messages on the `canary:events` stream with a `source` field (`square` or `counterpoint`) that enables provider-keyed dispatch in Pipeline Stage 2. Pipeline Stage 1 (seal), Stage 3 (merkle), and Stage 4 (detect) are source-agnostic — they process sealed bytes regardless of origin. See `pos-adapter-substrate.md` for the adapter abstraction layer.

## Pipeline Stage Map

| Stage | Stream | Function |
|-------|--------|----------|
| Stage 1 — Hash & Seal | `canary:events` | Write-once evidence sealing with chain hashes |
| Stage 2 — Parse & Route | `canary:events` | CRDM record parsing, detection stream publishing |
| Stage 3 — Merkle Batcher | `canary:events` | Merkle tree batching for Bitcoin inscription |
| Stage 4 — Chirp Detection | `canary:detection` | Rule engine evaluation, alert generation |

Each stage runs as an independent worker process consuming from its own consumer group. Stages 1–3 read from `canary:events`; Stage 4 reads from `canary:detection` (published by Stage 2).

---

## Dependencies

| Dependency | Role | Required |
|------------|------|----------|
| PostgreSQL 17 (`canary` database) | `canary_sales`, `canary_app` schemas | Yes |
| Valkey 8 — dedup namespace | Idempotency keys (`SET NX`, 24h TTL) | Yes |
| Valkey 8 — webhook stream (`canary:events`) | Primary event stream | Yes |
| Valkey 8 — detection stream (`canary:detection`) | Detection routing for Stage 4 | Yes |
| Valkey 8 — dead letter stream (`canary:dead_letter`) | Poison message quarantine | Yes |
| Valkey 8 — batch accumulator (`canary:batch:current`) | Stage 3 Merkle accumulation | Yes |
| Valkey 8 — heartbeat namespace (`canary:heartbeat:*`) | Consumer liveness | Yes |
| Square Webhooks | Event source — POSTs to `/webhooks/square` | Yes (Square tenants) |
| Square Orders API | Synchronous enrichment for `order.*` events | Optional (graceful fallback) |
| NCR Counterpoint REST API | Event source — poll-based ingestion | Yes (Counterpoint tenants) |
| `SQUARE_WEBHOOK_SIGNATURE_KEY` | Per-subscription HMAC key from Square Developer Dashboard | Yes (prod) |
| `SQUARE_NOTIFICATION_URL` | Registered webhook URL — must match for HMAC computation | Yes (prod) |
| Chirp Rule Engine | Stage 4 depends on the Chirp evaluation service | Yes (Stage 4 only) |

---

## Data Flow & PII Map

### Architecture Diagram

```
Square Webhooks                    NCR Counterpoint REST API
      |                                    |
      v                                    v
[ POST /webhooks/square ]          [ CounterpointPoller ]
(webhook gateway handler)          (poll loop per tenant/entity)
      |                                    |
      | 1. HMAC-SHA256 validate            | 1. Credential-based auth
      | 2. SHA-256 hash raw bytes          | 2. SHA-256 hash raw bytes
      |    (patent-critical: hash          |    (same hash-before-parse)
      |     BEFORE parse)                  | 3. Watermark advance
      | 3. JSON parse + enrich             | 4. Idempotency check
      | 4. Stateless Chirp (Tier 1)        | 5. XADD -> canary:events
      | 5. Idempotency check               |    (source=counterpoint)
      |    (dedup namespace, TTL 24h)      |
      | 6. XADD -> canary:events           |
      |    (source=square)                 |
      | 7. INSERT ingestion_log            |
      |                                    |
      +------------------+-----------------+
                         |
                         v
            canary:events (Valkey Stream, source-attributed)
                         |
             +-----------+-----------+
             |           |           |
             v           v           v
         stage1-seal  stage2-parse  stage3-merkle
             |           |               |
             |           | XADD          |
             |           v               |
             |    canary:detection        |
             |           |               |
             v           v               v
       evidence_    CRDM tables    inscription_pool
       records      (canary_sales) + event_inscriptions
                         |
                   detection-engine
                     (Stage 4)
                         |
                         v
                  canary_app.alerts
```

### What Enters

Square sends webhook POST requests containing transaction, order, payment, refund, cash drawer, gift card, loyalty, dispute, invoice, payout, inventory, timecard, device, and terminal event data. Payloads can contain:

- Merchant identifiers (Square merchant_id)
- Card payment data (last4, brand, fingerprint, BIN, expiry, entry method)
- Employee identifiers (team_member_id, names via Team Members API)
- Customer identifiers (customer_id, phone via loyalty)
- Device metadata (serial number, IP address, WiFi SSID)
- Transaction amounts and line item details

### What's Stored — PII Field Map

| Field | Table(s) | Classification | Encryption | Notes |
|-------|----------|---------------|------------|-------|
| `raw_payload` | `evidence_records` | **restricted** | **NONE (P0)** | Complete webhook JSON including all PII. Write-once, no updates/deletes. |
| `parsed_payload` | `evidence_records` | **restricted** | **NONE (P0)** | Parsed JSON copy of raw_payload. |
| `payload` | `transactions` | **restricted** | **NONE (P0)** | Full webhook payload stored as forensic copy. |
| `card_last4` | `transactions`, `transaction_tenders` | sensitive | NONE (P0) | Last 4 digits of card number. PCI-relevant. |
| `card_fingerprint` | `transactions` | sensitive | NONE (P0) | Square card fingerprint — pseudonymous but linkable. |
| `card_bin` | `transactions` | sensitive | NONE (P0) | First 6 digits — issuer BIN. |
| `card_exp_month/year` | `transactions` | sensitive | NONE (P0) | Card expiration date. |
| `employee_id` | Multiple CRDM tables | internal | NONE | Square team_member_id — maps to employee names. |
| `customer_id` | `transactions` | internal | NONE | Square customer_id — links to customer PII. |
| `phone_hash` | `loyalty_accounts` | sensitive | HMAC-SHA256 with `PHONE_HASH_KEY` (keyed one-way) | Phone number HMAC'd before storage. Plain SHA-256 was rejected — the phone-number domain is too low-entropy (NANP ≈ 10¹⁰) and would be brute-forceable from any read access to the column. Keyed hash blocks offline recovery while preserving dedup determinism. Key class defined in `go-security.md` → "PII Hashing Keys". |
| `ip_address` | `ingestion_log`, `devices` | sensitive | **NONE (P0)** | Source IP of webhook, device IP. |
| `wifi_network_name` | `devices` | internal | NONE | WiFi SSID — location-identifying. |
| `serial_number` | `devices` | internal | NONE | Device serial number. |
| `primary_recipient` | `invoices` | **sensitive** | **NONE (P0)** | JSONB — may contain customer name, email, phone, address. |
| `email` | `employees` (via Stage 2 upsert) | **sensitive** | **NONE (P0)** | Employee email stored plaintext. |
| `employee_name` | `employees` (via Stage 2 upsert) | **sensitive** | **NONE (P0)** | Employee display name stored plaintext. |
| `merchant_id` | All tables | internal | NONE | Tenant partition key. |
| `event_hash` | `ingestion_log`, `evidence_records`, `event_inscriptions` | public | NONE | SHA-256 content hash — not PII. |
| `chain_hash` | `evidence_records` | public | NONE | Derived hash — not PII. |
| `merkle_root` | `inscription_pool` | public | NONE | Aggregate hash — not PII. |

### What Exits

| Destination | Data | Classification |
|-------------|------|---------------|
| `canary:events` stream | 9-field queue message including `raw_payload` | restricted (contains full PII) |
| `canary:detection` stream | `{transaction_id, merchant_id, event_type, detection_type}` | internal (IDs only) |
| Receipt endpoints (`/receipt/*`) | Evidence receipt with chain hash, inscription proof | public (hashes only, no PII) |
| MCP tools (`/tsp/*`) | Stream health, ingestion stats, dead letter contents | mixed (DLQ contains raw payloads) |

---

## API Contract

### Webhook Endpoints (registered at `/webhooks`)

**`POST /webhooks/{pos-type}`**

Accepts POS-native webhook payloads. The `{pos-type}` path segment is a registered source identifier (e.g. `square`). Unknown sources return 404.

```
POST /webhooks/{pos-type}
Headers: X-Signature (HMAC-SHA256, Square format: X-Square-Hmacsha256-Signature)
Body: POS-native webhook payload (max 1 MB)
Response 200: {"received": true, "event_id": "<ulid>", "received_at": "<iso8601>"}
Response 200 (duplicate): {"received": true, "event_id": "<existing-ulid>", "duplicate": true}
Response 400: {"error": "invalid signature"}
Response 404: {"error": "unknown source"}
Response 409: {"error": "duplicate event", "event_id": "<existing-ulid>"}
Response 413: {"error": "payload too large"}
Response 503: {"error": "stream unavailable", "retry_after_seconds": 30}
```

**Critical invariant:** Never return 200 unless the event has been published to the Valkey stream. The stream is the source of truth, not the database.

**`GET /webhooks/health`** — Returns stream connectivity status
- Response 200: `{"status": "healthy", "queue_connected": true, "version": "1.0.0"}`
- Response 503: `{"status": "degraded", "queue_connected": false, "version": "1.0.0"}`

**`GET /webhooks/ready`** — Readiness probe (checks stream connection, signature key, notification URL)
- Response 200: `{"status": "ready"}`
- Response 503: `{"status": "not_ready", "errors": [...]}`

**`GET /webhooks/live`** — Liveness probe
- Response 200: `{"status": "alive"}`

### Receipt Endpoints (registered at `/receipt`)

**`GET /receipt/by-hash/{event_hash_hex}`** — Lookup sealed evidence by SHA-256 hex hash
- Auth: JWT
- Response 200: Evidence record with chain hash, Merkle proof path, inscription status
- Response 404: Not found

**`GET /receipt/by-event/{event_id}`** — Lookup by ULID event_id
- Auth: JWT
- Same response shape as by-hash

**`GET /receipt/health`** — Health check

### Management Tools (7 operations, restricted to authenticated admin sessions)

| Operation | Category | Write | Description |
|-----------|----------|:-----:|-------------|
| `get_stream_health` | health | No | Stream lengths, consumer groups, pending counts |
| `get_dead_letters` | health | No | List failed events from DLQ stream (default limit 20) |
| `replay_event` | management | **Yes** | Replay dead-letter entry back to main stream |
| `get_ingestion_stats` | analytics | No | Throughput, latency, error rates by merchant/window |
| `get_receipt` | evidence | No | Sealed evidence receipt with proof |
| `verify_merkle` | evidence | No | Verify Merkle inclusion proof |
| `process_dead_letters` | management | **Yes** | Process eligible DLQ entries with backoff (default batch 10) |

---

## [SPEC ADDITION — not in prototype] Message Envelope Schema

Every message published to `canary:events` must carry this 9-field envelope. All fields are strings (Valkey stream values are string-typed; binary fields are hex-encoded).

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `event_id` | ULID string | Yes | Canary-assigned identifier, generated at ingestion time |
| `merchant_id` | string | Yes | Tenant partition key (Square merchant_id or equivalent) |
| `source` | string | Yes | POS provider identifier: `square` or `counterpoint` |
| `source_event_id` | string | Yes | Provider-native event ID (Square event_id, Counterpoint doc key) |
| `event_type` | string | Yes | Provider-native event type string (e.g. `payment.created`) |
| `event_hash` | hex string (64 chars) | Yes | SHA-256 of the raw payload bytes, computed before parsing |
| `raw_payload` | UTF-8 string | Yes | Verbatim POS payload. Contains PII — see PII map above. |
| `received_at` | ISO 8601 string | Yes | Timestamp of gateway receipt |
| `parse_failed` | `"true"` or `"false"` | Yes | Whether JSON parsing succeeded. `"true"` means stages downstream should skip parse-dependent steps. |

Messages published to `canary:detection` carry a 5-field envelope:

| Field | Type | Required | Description |
|-------|------|:--------:|-------------|
| `transaction_id` | UUID string | Yes | PK of the CRDM record written by Stage 2 |
| `merchant_id` | string | Yes | Tenant partition key |
| `event_type` | string | Yes | Original provider event type |
| `event_id` | ULID string | Yes | Original gateway event_id |
| `detection_type` | string | Yes | Routing key: `transaction`, `cash_drawer`, `gift_card`, or `loyalty` |

---

## [ARCHITECTURAL DIRECTION — not yet implemented] ILDWAC Pass-Through Fields

TSP message envelopes carry two ILDWAC input dimensions as pass-through fields. These fields are set by the POS adapter (Hawk for Square, Bull for Counterpoint) and preserved unchanged through the TSP pipeline. TSP does not process, validate, or transform them — they are opaque strings forwarded to downstream consumers.

### Extended Envelope Fields (ILDWAC Dimensions)

| Field | Type | Set By | TSP Role | ILDWAC Dimension |
|---|---|---|---|---|
| `pos_port` | string | POS adapter | Pass-through | Port |
| `device_id` | string or null | POS adapter (from POS payload) | Pass-through | Device |

These fields extend the existing 9-field `canary:events` envelope. When present, they appear as additional string fields in the Valkey stream message. When `device_id` is null, it is omitted from the stream message or transmitted as the empty string `""` — downstream consumers must treat both as absent.

**TSP invariant:** TSP must not modify, normalize, or default these fields. The adapter is the authoritative source. A null `device_id` from the adapter means the POS did not report a device for this event — TSP must not substitute a value.

### Downstream Consumer Contract

The stock ledger (Module V, ILDWAC recalculation engine) is the primary consumer of these fields. Future consumers include the cost attribution service. Both expect `pos_port` and `device_id` to be available in the `canary:events` stream message if the originating adapter populated them.

Stage 1 (Hash & Seal), Stage 2 (Parse & Route), Stage 3 (Merkle Batcher), and Stage 4 (Chirp Detection) do not consume these fields — they pass through the envelope unchanged.

### MCP Dimension — Not Captured Here

The MCP dimension of ILDWAC (which MCP tool call authorized a cost-affecting action) is NOT captured in TSP or the webhook pipeline. It is captured when an MCP tool call authorizes an action — recorded by the MCP authorization middleware at invocation time. The pipeline carries `pos_port` and `device_id` only.

---

## [ARCHITECTURAL DIRECTION — not yet implemented] Pipeline Stage Smart Contracts

TSP's four pipeline stages (Sub1–Sub4) each carry an implicit contract governing their interaction with the rest of the system. These contracts are the precondition for the agent-attribution model: every cost-affecting action that passes through the pipeline has a defined input schema, output schema, idempotency guarantee, and failure mode.

### Stage Contract Summary

| Stage | Input Schema | Output Schema | Idempotency Guarantee | Failure Mode |
|---|---|---|---|---|
| Sub1 — Hash & Seal | 9-field `canary:events` envelope | One row in `evidence_records` | `ON CONFLICT (merchant_id, event_id) DO NOTHING` | No-ACK → PEL redelivery; 10 consecutive errors → self-terminate |
| Sub2 — Parse & Route | 9-field `canary:events` envelope | One+ rows in `canary_sales`; optional detection event | `ON CONFLICT (merchant_id, external_id) DO NOTHING` on CRDM tables | No-ACK; stale ref → silent ACK |
| Sub3 — Merkle Batcher | `event_hash` from `canary:events` | One row `inscription_pool` + N rows `event_inscriptions` | Existence check on `batch_id` before INSERT | No-ACK on DB failure; all pending IDs stay in PEL |
| Sub4 — Chirp Detection | 5-field `canary:detection` envelope | Zero or more rows in `canary_app.alerts` | `ON CONFLICT (merchant_id, event_id, rule_id) DO NOTHING` | No-ACK; missing record → silent ACK |

The full agent-contract pattern for these stage interactions — including which agent nodes are authorized to trigger replays, how the Controller escalates on stage failure, and how MCP tool calls are attributed to pipeline outputs — is documented in `agent-contracts.md` (to be authored separately).

---

## [SPEC ADDITION — not in prototype] Idempotency Key Strategy

Duplicate events must be suppressed at the gateway before they enter the stream. The strategy is two-layer:

**Layer 1 — Fast dedup (Valkey SET NX):**
- Key pattern: `dedup:{source}:{source_event_id}`
- TTL: 24 hours
- Behavior: If key exists, the event is a duplicate. Return 200 with `"duplicate": true`. Square treats 2xx as successful delivery and stops retrying.
- Fail-open: If the dedup store is unavailable, allow the event through. Layer 2 provides backstop.
- If no `source_event_id` is extractable (parse failure), skip dedup entirely. Accept and hash.

**Layer 2 — Database backstop (unique constraint):**
- `evidence_records` has a unique constraint on `(merchant_id, event_id)`.
- On `ON CONFLICT DO NOTHING`, Stage 1 ACKs the message silently without error.
- This backstop handles the window between dedup cache expiry (24h) and any edge cases where Layer 1 fails open.

**Idempotency scope:** Both layers together guarantee at-most-once writes to `evidence_records`. Stage 2 (parse) uses `ON CONFLICT DO NOTHING` on CRDM tables. Duplicate ACK on unique constraint violation is always correct behavior.

---

## [SPEC ADDITION — not in prototype] Backpressure and Flow Control

The webhook gateway does not implement backpressure against the stream. This is intentional: the gateway's only hard dependency is the Valkey stream; it must return quickly to Square or risk triggering Square's retry mechanism.

The flow control contract is:

1. **Gateway → stream:** Fire-and-forget XADD. If stream is unavailable, return 503. Square will retry for up to 72 hours.
2. **Stream → consumers:** Each consumer reads at its own pace via XREADGROUP with a configurable block timeout. Consumers are independently scaled.
3. **Stream bounds:** The `canary:events` stream should be configured with a MAXLEN (~10,000, approximate trimming). This prevents unbounded memory growth if consumers fall behind. Consumers that lag beyond MAXLEN lose access to unprocessed messages — the stream is not a durable queue; the database is.
4. **Consumer health signal:** Each consumer writes a heartbeat key with a 120-second TTL on every iteration. If a heartbeat is older than 60 seconds, the consumer is considered stalled.
5. **Backpressure observable:** Monitor `canary:events` stream length. If length exceeds 10,000, consumers are falling behind. Alert threshold: >10,000 messages pending.

---

## [SPEC ADDITION — not in prototype] Consumer Group Semantics

Each pipeline stage operates as an independent consumer group on the same stream. This fan-out pattern means every event is processed by all four stages.

**Consumer group names:**
- Stage 1: `sub1-seal` on stream `canary:events`
- Stage 2: `sub2-parse` on stream `canary:events`
- Stage 3: `sub3-merkle` on stream `canary:events`
- Stage 4: `detection-engine` on stream `canary:detection`

**Startup behavior:**
- Consumer groups are created idempotently at application startup. If the group already exists (BUSYGROUP error from Valkey), ignore the error and proceed.
- Groups are created with `$` start position (new messages only), not `0` (replay all). Existing messages in the stream are not reprocessed on restart; in-flight messages in the PEL are.
- Stage 4 recovers pending messages from its PEL before switching to new messages.

**Concurrency within a consumer group:**
- Stage 1 is limited to exactly one worker due to its advisory-lock-based chain hash serialization per merchant. Multiple Stage 1 workers would deadlock or produce non-deterministic chain sequences.
- Stages 2 and 4 can support multiple workers within the same consumer group for horizontal scaling. Each worker claims messages independently via XREADGROUP.
- Stage 3 is designed for a single worker because the batch accumulator is a shared Valkey sorted set. Multiple workers would race on the flush condition.

**Message acknowledgment rules (all stages):**
- ACK on success only.
- On failure, do NOT ACK. The message re-enters the PEL and is redelivered on next XREADGROUP with `>`.
- On unrecoverable poison (hash mismatch, malformed envelope), route to `canary:dead_letter` stream and ACK the original. The message must not block the PEL indefinitely.
- On database unique constraint violation (duplicate), ACK silently — this is expected and correct.

---

## Pipeline Stage Specifications

### Stage 1 — Hash & Seal

**Input contract:** Message from `canary:events` consumer group `sub1-seal`. All 9 envelope fields must be present; malformed messages (missing required fields) are routed to the dead letter stream and ACKed.

**Processing contract:**
1. Validate all 9 required fields in the message envelope.
2. Recompute `SHA-256(raw_payload_bytes)`. Compare to the `event_hash` field from the envelope. Mismatch = tamper detection — route to dead letter stream as a tamper alert. Do not write to `evidence_records`.
3. Acquire an advisory lock per `merchant_id` (serializes chain hash computation for this merchant).
4. Query `evidence_records` for the most recent chain hash for this merchant.
5. Compute chain hash:
   - Genesis (first event for merchant): `chain_hash = SHA-256(event_hash_bytes)`
   - Subsequent: `chain_hash = SHA-256(prev_chain_hash_bytes || event_hash_bytes)`
6. Write to `evidence_records` (write-once; no UPDATE, no DELETE, enforced by trigger).
7. If `parse_failed = "false"`, parse `raw_payload` and populate `parsed_payload` column.

**Output contract:**
- One row written to `canary_sales.evidence_records` with fields: `event_id`, `merchant_id`, `source`, `source_event_id`, `event_type`, `event_hash` (BYTEA), `chain_hash` (BYTEA), `previous_chain_hash` (BYTEA, NULL for genesis), `raw_payload`, `parsed_payload` (NULL if parse_failed), `parse_failed`, `received_at`.

**Error contract:**
- Malformed envelope: route to `canary:dead_letter`, ACK.
- Hash mismatch (tamper): route to `canary:dead_letter` with `tamper_detected=true`, ACK.
- Database write failure: do NOT ACK. Message stays in PEL. Retry on next consumer iteration.
- Consecutive errors (10): consumer self-terminates. Process manager restarts it.

**Idempotency guarantee:** `evidence_records` has a unique constraint on `(merchant_id, event_id)`. Duplicate events from the stream produce an `ON CONFLICT DO NOTHING` and are ACKed silently.

---

### Stage 2 — Parse & Route

**Input contract:** Message from `canary:events` consumer group `sub2-parse`. All 9 envelope fields required. If `parse_failed = "true"`, skip parsing and ACK immediately.

**Processing contract:**
1. Route the event by `event_type` through the dispatch table to the appropriate parser function and CRDM model.
2. Log-only routes: ACK immediately without database writes.
3. Call the parser (pure function: raw JSON → flat field dict, no side effects).
4. Build CRDM model instances:
   - Refunds: `Transaction` + `RefundLink`
   - Orders: `Transaction` + `TransactionLineItems` + `TransactionTenders`
   - Standard: single model
5. Write to `canary_sales` (appropriate table per event type).
6. Update `ingestion_log.status` from `"accepted"` to `"parsed"`.
7. If the event type has a `detection_type`, publish a 5-field message to `canary:detection` for Stage 4.

**Square parser suite (pure functions, no side effects):**
- `square_payment_parser` — payments and refunds
- `square_order_parser` — orders, line items, and tenders
- `square_loyalty_parser` — loyalty accounts and events (phone number → `HMAC-SHA256(PHONE_HASH_KEY, normalize(phone))` before storage; see `go-security.md` → "PII Hashing Keys")
- `square_payout_parser` — payouts
- `square_dispute_parser` — disputes
- `square_auxiliary_parsers` — cash drawer shifts/events, timecards, inventory, gift cards

**Output contract:**
- One or more rows in the appropriate `canary_sales` table.
- `ingestion_log` status updated.
- If detection-eligible: one 5-field message published to `canary:detection`.

**Error contract:**
- Parse failure: log error, do NOT ACK. If the same message fails 10 consecutive times, route to dead letter.
- Database write failure: do NOT ACK.
- Record not found on update: ACK — stale references do not block the pipeline.
- Duplicate write (unique constraint): ACK silently.

**Idempotency guarantee:** All CRDM tables use `ON CONFLICT (merchant_id, external_id) DO NOTHING`.

---

### Stage 3 — Merkle Batcher

**Input contract:** Message from `canary:events` consumer group `sub3-merkle`. Requires `event_hash` field. Does NOT ACK during accumulation.

**Processing contract:**
1. Add `event_hash` to the Valkey sorted set `canary:batch:current` with score = current Unix timestamp.
2. Track the stream message ID in a local pending set (not ACKed yet).
3. Flush conditions — either triggers a flush:
   - Accumulated count >= 100 (`BATCH_COUNT_THRESHOLD`)
   - Elapsed time since first message >= 600 seconds (`BATCH_TIME_THRESHOLD_SECONDS`)
4. On flush:
   a. Read all entries from `canary:batch:current`, sorted by hex value ascending.
   b. Build Merkle tree:
      - Double-hash all leaf values using SHA-256 (second-preimage attack prevention).
      - Pad to nearest power of 2 by duplicating the last leaf.
      - Tree algorithm version: 1 (deterministic, stored in `inscription_pool.tree_algorithm_version`).
   c. In a single database transaction: INSERT `inscription_pool` record + all `event_inscriptions` records with Merkle proof paths.
   d. ACK all pending stream message IDs.
   e. Clear `canary:batch:current` accumulator.
5. Inscription: currently mocked (deterministic fake Bitcoin values for `inscription_id`, `bitcoin_txid`, `bitcoin_block`). Real OrdinalsBot API integration is gated by a spend approval.

**Output contract:**
- One row in `canary_sales.inscription_pool` per batch.
- One row in `canary_sales.event_inscriptions` per event in the batch, including `merkle_proof_path` (JSONB: siblings, root, leaf_hash, depth) and `leaf_index`.

**Merkle proof path format:**
```json
{
  "leaf_hash": "<sha256-hex>",
  "leaf_index": 3,
  "depth": 4,
  "root": "<sha256-hex>",
  "siblings": ["<sha256-hex>", "<sha256-hex>", "<sha256-hex>", "<sha256-hex>"]
}
```

**Error contract:**
- Database transaction failure: no ACK. All pending messages stay in PEL. Retry on next iteration.
- Accumulator write failure: no ACK.

**Idempotency guarantee:** `inscription_pool` batch_id is a ULID generated at flush time. If flush is interrupted after DB write but before ACK, the next iteration will attempt to re-flush. Implement an existence check before inserting to `inscription_pool` to handle this case.

---

### Stage 4 — Chirp Detection

**Input contract:** Message from `canary:detection` consumer group `detection-engine`. Requires all 5 detection envelope fields.

**Processing contract:**
1. Route by `detection_type` to the appropriate Chirp Rule Engine evaluation function:
   - `"transaction"` → evaluate against transaction rules (read-only, no side effects)
   - `"cash_drawer"` → evaluate cash drawer shift record
   - `"gift_card"` → evaluate gift card activity record
   - `"loyalty"` → evaluate loyalty event record
2. Rule evaluation reads from `canary_sales` (read-only session).
3. If any rules fire, write resulting alerts to `canary_app.alerts` (write session, separate from read session).

**Dual-database session requirement:** Stage 4 requires two independent database connections — one to `canary_sales` for reads (append-only, detection input) and one to `canary_app` for writes (alerts, cases). These must not share a transaction.

**Output contract:**
- Zero or more rows in `canary_app.alerts` per evaluated event.

**Error contract:**
- Record not found (detection envelope references a CDM record that Stage 2 hasn't written yet, or was dropped): ACK. Do not retry missing data — the record will not appear.
- Rule evaluation error: log, do NOT ACK. Retry up to 10 consecutive errors before consumer self-terminates.
- Alert write failure: do NOT ACK.

**Idempotency guarantee:** Alert creation is keyed on `(merchant_id, event_id, rule_id)`. Duplicate detection messages produce `ON CONFLICT DO NOTHING`.

---

## Operations

### Startup Sequence

1. PostgreSQL and Valkey must be running and reachable.
2. The webhook gateway service starts, initializes consumer groups on `canary:events` (groups: `sub1-seal`, `sub2-parse`, `sub3-merkle`) and `canary:detection` (group: `detection-engine`). Consumer group creation is idempotent — BUSYGROUP errors are ignored.
3. Pipeline stage workers start after the gateway is healthy. Each worker defensively creates its own consumer group (BUSYGROUP guard).
4. Stage 4 recovers pending messages from its PEL before processing new messages.

### Health Checks

| Service | Mechanism | Interval | Healthy Condition |
|---------|-----------|----------|-------------------|
| Webhook gateway | `GET /webhooks/health` | 15s | Valkey stream reachable |
| Webhook readiness | `GET /webhooks/ready` | on-demand | Valkey + signature key + notification URL configured |
| Receipt service | `GET /receipt/health` | 15s | Returns 200 |
| Each stage worker | Valkey heartbeat `canary:heartbeat:<stage-name>` (TTL 120s) | Every loop iteration | Heartbeat younger than 60s |

### Failure Modes

| Failure | Impact | Recovery |
|---------|--------|----------|
| Valkey down | Gateway returns 503, stage workers block indefinitely | Workers reconnect via exponential backoff. Square retries webhooks for 72h. |
| PostgreSQL down | Stage workers fail on DB writes, do NOT ACK messages | Messages stay in PEL, redelivered when DB recovers. |
| Single stage worker crash | Other stages unaffected (independent groups) | Process manager restarts worker. PEL messages redelivered. |
| 10 consecutive errors | Stage worker self-terminates | Process manager restart required. Requires investigation. |
| HMAC key misconfigured | All webhooks rejected with 503 | Fix env var, restart gateway. |
| Square Orders API down | Order enrichment skipped, unenriched payload published | Graceful degradation — Stage 2 processes without line items. |

### Monitoring

| Metric | Normal | Alert Threshold |
|--------|--------|-----------------|
| Webhook response time (p95) | < 50ms | > 500ms |
| 503 responses | 0 | > 5 in 5 minutes |
| 401 responses | 0 | > 10 in 1 hour |
| `canary:events` stream length | < 1000 | > 10,000 |
| Consumer heartbeat age | < 30s | > 60s |
| DLQ stream length | 0 | > 0 |
| Pending message count (PEL) | near 0 | growing trend |
| Ingestion error rate | < 5% | > 5% |

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `VALKEY_URL` | (required) | Valkey connection URL |
| `VALKEY_STREAM` | `canary:events` | Primary event stream name |
| `VALKEY_DEAD_LETTER_STREAM` | `canary:dead_letter` | Dead letter stream |
| `DETECTION_STREAM` | `canary:detection` | Detection routing stream for Stage 4 |
| `SQUARE_WEBHOOK_SIGNATURE_KEY` | (required) | Per-subscription HMAC key |
| `SQUARE_NOTIFICATION_URL` | (required) | Registered webhook URL |
| `SQUARE_ACCESS_TOKEN` | (optional) | For order enrichment |
| `SQUARE_ENVIRONMENT` | `sandbox` | Controls Square API endpoint |
| `MAX_PAYLOAD_BYTES` | `1048576` | Max webhook payload size (1 MB) |
| `DATABASE_URL` | (required) | PostgreSQL connection string |
| `STAGE1_BLOCK_MS` | `5000` | Stage 1 XREADGROUP blocking timeout |
| `STAGE2_BLOCK_MS` | `5000` | Stage 2 XREADGROUP blocking timeout |
| `STAGE3_BLOCK_MS` | `2000` | Stage 3 XREADGROUP blocking timeout |
| `STAGE4_BLOCK_MS` | `5000` | Stage 4 XREADGROUP blocking timeout |
| `BATCH_COUNT_THRESHOLD` | `100` | Stage 3 Merkle batch flush count |
| `BATCH_TIME_THRESHOLD_SECONDS` | `600` | Stage 3 Merkle batch flush time (10 min) |
| `MOCK_INSCRIPTION` | `true` | Stage 3 mock OrdinalsBot responses |
| `CANARY_ENCRYPTION_KEY` | (required prod) | AES-256-GCM key for token encryption |

**Socket timeout rule:** Valkey stream client `socket_timeout` must exceed the maximum `XREADGROUP block_ms` by a safe margin (e.g., block_ms = 5000, socket_timeout = 30000).

---

## Shared Service Layer — Functional Contracts

### Stream Publisher
Singleton Valkey client for the event stream. Exposes:
- `InitConsumerGroups()` — creates all consumer groups idempotently (BUSYGROUP safe)
- `PublishEvent(envelope)` — XADD to `canary:events` (9-field envelope)
- `PublishDetectionEvent(envelope)` — XADD to `canary:detection` (5-field envelope)

### HMAC Validator (Square)
Validates incoming Square webhooks. Formula: `Base64(HMAC-SHA256(notification_url + raw_body_utf8, signature_key_utf8))`. Comparison must be timing-safe (constant-time comparison). Returns `false` immediately if the signature header is absent or empty. Returns error if the signature key or notification URL is not configured.

### Order Enricher (Square)
Fetches full order data from the Square Orders API synchronously for `order.*` event types. Falls back gracefully if the access token is absent or the API call fails — the unenriched payload is published to the stream.

### Consumer Heartbeat
Each stage worker writes a liveness key `canary:heartbeat:<stage-name>` with TTL 120 seconds on every loop iteration. Write is best-effort — errors are swallowed. A heartbeat is considered live if its age is less than 60 seconds.

### DLQ Processor
Processes dead-lettered events from `canary_sales.dead_letter_queue`. Exponential backoff: 5s, 30s, 5 minutes (3 retries maximum). After 3 retries, marks entry as exhausted. Exhausted entries remain in the table with full error context for manual investigation.

### Merkle Tree
Deterministic construction (algorithm version 1). Steps:
1. Sort leaf hashes by hex value ascending.
2. Pad to the nearest power of 2 by duplicating the last leaf.
3. Double-hash each leaf using SHA-256 (second-preimage attack prevention).
4. Build tree iteratively, hashing adjacent pairs.
5. Output: Merkle root (BYTEA) + per-leaf proof paths (sibling array + depth + root).

---

## Code Review Findings (Carry Forward to Go Implementation)

The following findings from the Python prototype are production blockers. The Go implementation must resolve them before go-live.

### P0 — Blocks Production

| # | Finding | Required Fix |
|---|---------|-------------|
| P0-TSP-01 | **Raw payloads stored plaintext with full PII** | Encrypt `raw_payload` and `parsed_payload` with AES-256-GCM before INSERT into `evidence_records`. Decrypt on read in evidence audit workflows only. |
| P0-TSP-02 | **Transaction.payload stored plaintext** | Encrypt with AES-256-GCM or remove the forensic copy (evidence_records already holds the original). |
| P0-TSP-03 | **Card data fields not encrypted** | Field-level AES-256-GCM encryption on `card_last4`, `card_fingerprint`, `card_bin`, `card_exp_month`, `card_exp_year`. |
| P0-TSP-04 | **Employee PII stored plaintext** | Encrypt `employee_name` and `email`. Consider hashing email for lookup, encrypting display value. |
| P0-TSP-05 | **IP addresses stored plaintext** | Hash or encrypt. For audit purposes, use HMAC with a rotating key. |
| P0-TSP-06 | **Invoice recipient PII in JSONB** | Encrypt the entire JSONB blob or extract and encrypt individual PII fields. |
| P0-TSP-07 | **Encryption key in environment** | Load from secrets manager (AWS Secrets Manager or equivalent) at startup. Do not read from disk. |
| P0-TSP-08 | **HMAC signature key in environment** | Load from secrets manager. |
| P0-TSP-09 | **DLQ replay operation has no auth** | Require authenticated admin session with admin role for any replay or DLQ write operation. |
| P0-TSP-10 | **Raw payload in Valkey stream (in-transit PII)** | Enable Valkey AUTH + TLS in production. Consider encrypting `raw_payload` before XADD. |

### P1 — Before GA

| # | Finding | Required Fix |
|---|---------|-------------|
| P1-TSP-01 | **No audit logging for evidence access** | Add audit log entries for receipt lookups: who, when, which event_hash. |
| P1-TSP-02 | **No data retention policy** | Retention windows: evidence 7 years (SOX), CRDM 24 months, ingestion_log 12 months, DLQ 90 days. Implement automated archival. |
| P1-TSP-03 | **No rate limiting on webhook endpoint** | Rate limit `POST /webhooks/{pos-type}`. Suggested: 1000 req/min per source IP. |
| P1-TSP-04 | **Stage worker memory not bounded** | Set explicit memory limits per worker process (256 MB is reasonable). |
| P1-TSP-05 | **No structured logging** | JSON-structured logs with `event_id`, `merchant_id`, `stage` as fields in every log line. |
| P1-TSP-06 | **Dual DLQ mechanisms not synchronized** | Valkey stream `canary:dead_letter` and `dead_letter_queue` PostgreSQL table are independent. Unify or use DB table as source of truth with stream as notification channel. |
| P1-TSP-07 | **Error responses leak internals** | Sanitize error responses. Log full errors server-side, return generic error categories to callers. |

### P2 — Post-Launch

| # | Finding | Recommended Fix |
|---|---------|----------------|
| P2-TSP-01 | **No key rotation procedure** | Implement dual-key validation during rotation window. |
| P2-TSP-02 | **Stream unbounded growth** | XADD with `MAXLEN ~10000` (approximate trimming). |
| P2-TSP-03 | **Mock inscription is default** | Complete OrdinalsBot integration when spend gate approved. |
| P2-TSP-04 | **Consumer scaling limited to 1** | Stage 2 and Stage 4 can scale horizontally. Stage 1 is constrained to 1 worker by chain hash serialization. Document explicitly. |
| P2-TSP-05 | **No metrics/tracing** | OTEL spans for gateway and each stage. Export pipeline latency, throughput, error rate. |

---

## Production Readiness Checklist

- [ ] PII encrypted at rest (P0-TSP-01 through P0-TSP-06)
- [ ] Secrets loaded from secrets manager at startup (P0-TSP-07, P0-TSP-08)
- [ ] Health check endpoints operational (`/webhooks/health`, `/webhooks/ready`, `/webhooks/live`)
- [ ] Stage worker heartbeats operational (Valkey heartbeat per stage)
- [ ] Audit logging for sensitive operations (P1-TSP-01)
- [ ] Data retention policy implemented (P1-TSP-02)
- [ ] Rate limiting on public endpoints (P1-TSP-03)
- [ ] Error responses do not leak internals (P1-TSP-07)
- [ ] Stage worker memory limits configured (P1-TSP-04)
- [ ] Structured JSON logging (P1-TSP-05)
- [ ] Valkey AUTH + TLS in production (P0-TSP-10)
- [ ] Management operations require authenticated admin session (P0-TSP-09)
- [ ] Stream MAXLEN configured (P2-TSP-02)

---

*Canary | GrowDirect LLC | Confidential*
