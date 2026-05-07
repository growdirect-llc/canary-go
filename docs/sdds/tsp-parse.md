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

# TSP Stage 2 — Parse & Route

> **Type:** Pipeline Consumer Worker — CRDM Record Creation and Detection Routing
> **Parent SDD:** `tsp.md` (pipeline overview)
> **Consumer Group:** `sub2-parse` on stream `canary:events`
> **Related:** `pos-adapter-substrate.md` (adapter abstraction), `ncr-counterpoint-tsp-adapter.md` (Counterpoint adapter)

---

## Governing Thesis

Stage 2 is the translation layer. It takes a sealed, source-attributed stream message and produces two things: a structured CRDM record in the canonical data model, and — for transaction-class events — a routing signal to Stage 4. The source-agnostic principle is the key design decision: the same CRDM schema receives Square payment events and NCR Counterpoint sales records. Downstream detection, analytics, and reporting never know or care which POS produced the data.

The architectural trade-off is parse-then-route rather than route-then-parse. Parse once, produce structured output, route the output. This couples parsing to routing in a single stage but eliminates double-parsing and keeps Stage 4 free of POS-specific logic.

---

## Business Hat

### Why This Stage Exists

The CRDM (Canonical Retail Data Model) is the business asset. It is the schema that makes multi-POS comparison, cross-store analytics, and POS-agnostic detection possible. Without Stage 2, every downstream component would need to understand Square JSON, NCR XML, and every future POS format. With Stage 2, they understand one schema.

The routing function — publishing to `canary:detection` — is what gives Stage 4 its low latency. Detection happens in seconds because Stage 2 publishes a lightweight routing envelope immediately after writing the CRDM record. Stage 4 does not poll the database; it reacts to the stream.

### Business Invariants

| Invariant | Why It Matters |
|-----------|---------------|
| Parse-then-route (never route-then-parse) | Single parse pass; no double-parsing; canonical output is the routing unit |
| Source-agnostic CRDM output | Multi-POS analytics and detection work without modification |
| Idempotent writes on `payload_hash` | Duplicate stream messages never produce duplicate CRDM records |
| `parse_failed=true` events ACKed without writing | Unparseable events don't block the pipeline; they remain in evidence_records from Stage 1 |

---

## Technical Hat

### Consumer Loop

Stage 2 supports multiple workers within the same consumer group for horizontal scaling. Each worker:

1. Call `XREADGROUP GROUP sub2-parse worker-<n> COUNT 10 BLOCK 5000 STREAMS canary:events >`
2. For each message: process (see Processing Contract).
3. On success: `XACK canary:events sub2-parse <message-id>`.
4. On unrecoverable failure (10 consecutive): route to dead letter, ACK, reset counter.
5. Heartbeat write every iteration.

**PEL recovery at startup:** Read pending entries from the PEL with `XREADGROUP ... STREAMS canary:events 0` before switching to `>`.

### Processing Contract (per message)

```
1. CHECK parse_failed FIELD.
   → If "true": ACK immediately, no DB writes, no detection publish. Done.

2. DEDUPE CHECK — check Valkey for key dedup:parsed:{source}:{source_event_id}.
   → If key exists: ACK (duplicate), skip. Log warning with event_id.
   → Fail-open: if Valkey unavailable, proceed to step 3 (DB unique constraint is backstop).

3. DISPATCH to source parser:
   → source = "square":        dispatch to Square parser suite
   → source = "counterpoint":  dispatch to NCR parser suite
   → unknown source:           route to dead letter, ACK

4. ROUTE by event_type to specific parser function (see Parser Dispatch Table).
   → Log-only routes: ACK immediately, no DB writes.
   → Unknown event_type for known source: log warning, ACK (graceful skip — new event types
     from the POS should not block the pipeline).

5. PARSE — call parser (pure function: raw JSON/CSV/XML → flat field struct, no side effects).
   → Parse error: do NOT ACK. If same message fails 10 consecutive times, route to dead letter.

6. BUILD CRDM models:
   → Payments/refunds:   Transaction + optional RefundLink
   → Orders:            Transaction + TransactionLineItems + TransactionTenders
   → Standard:          Single model per event_type

7. WRITE to canary_sales (appropriate table per event_type).
   → ON CONFLICT (merchant_id, external_id) DO NOTHING on all CRDM tables (idempotency).
   → Database write failure: do NOT ACK.

8. UPDATE ingestion_log.status from "accepted" → "parsed".
   → Record not found: ACK silently (stale reference; do not block pipeline).

9. SET dedup key in Valkey: dedup:parsed:{source}:{source_event_id} with TTL 24h.

10. IF event_type has detection_type: XADD canary:detection with 5-field envelope.
    → Publish failure: log error, do NOT let it block the CRDM write ACK.
    → The detection publish is best-effort at the stream level; the CRDM record is
      the durable state. Stage 4 could replay from the DB in an emergency.
```

### Parser Dispatch Table

Square event types and their CRDM routes:

| Event Type | Parser | CRDM Table(s) | Detection Type |
|------------|--------|----------------|----------------|
| `payment.created` | `square_payment_parser` | `transactions`, `transaction_tenders` | `transaction` |
| `payment.updated` | `square_payment_parser` | `transactions` (upsert) | `transaction` |
| `refund.created` | `square_payment_parser` | `transactions`, `refund_links` | `transaction` |
| `order.created` | `square_order_parser` | `transactions`, `transaction_line_items`, `transaction_tenders` | `transaction` |
| `order.updated` | `square_order_parser` | `transactions` (upsert) | `transaction` |
| `cash_drawer_shift.closed` | `square_auxiliary_parsers` | `cash_drawer_shifts` | `cash_drawer` |
| `cash_drawer_event.created` | `square_auxiliary_parsers` | `cash_drawer_events` | `cash_drawer` |
| `gift_card.created` | `square_auxiliary_parsers` | `gift_cards` | `gift_card` |
| `gift_card_activity.created` | `square_auxiliary_parsers` | `gift_card_activities` | `gift_card` |
| `loyalty_event.accumulated` | `square_loyalty_parser` | `loyalty_events`, `loyalty_accounts` | `loyalty` |
| `payout.sent` | `square_payout_parser` | `payouts` | — |
| `dispute.created` | `square_dispute_parser` | `disputes` | — |
| `inventory.count.updated` | `square_auxiliary_parsers` | `inventory_counts` | — |
| `timecard.*` | `square_auxiliary_parsers` | `timecards` | — |
| `device.*` | `square_auxiliary_parsers` | `devices` | — |

NCR Counterpoint event types follow the same dispatch pattern but route to `ncr_parser` functions. See `ncr-counterpoint-tsp-adapter.md` for the full NCR parser suite.

**Log-only routes** (ACK, no DB write): `merchant.updated`, `location.updated`, `subscription.*`, `oauth.*`, and any Square event type not in the table above. These are tracked in `ingestion_log` only.

### Square Parser Suite

All parsers are pure functions: `(raw_payload []byte) → (CRDMModel, error)`. No database access, no network calls, no side effects.

| Parser | Input | Output | PII Fields Handled |
|--------|-------|--------|--------------------|
| `square_payment_parser` | Payment/refund webhook JSON | `Transaction`, optional `RefundLink` | `card_last4`, `card_fingerprint`, `card_bin`, `card_exp_month/year` (P0 encrypt) |
| `square_order_parser` | Order webhook JSON + enriched order data | `Transaction`, `TransactionLineItems[]`, `TransactionTenders[]` | `customer_id`, card fields |
| `square_loyalty_parser` | Loyalty event webhook JSON | `LoyaltyEvent`, `LoyaltyAccount` | `phone` → HMAC-SHA256(`PHONE_HASH_KEY`, normalized_phone) before storage |
| `square_payout_parser` | Payout webhook JSON | `Payout` | None |
| `square_dispute_parser` | Dispute webhook JSON | `Dispute` | None |
| `square_auxiliary_parsers` | Cash drawer / gift card / inventory / timecard / device JSON | Various | `employee_id`, `email`, `employee_name` (P0 encrypt) |

**Phone number hashing:** The loyalty parser hashes phone numbers with `HMAC-SHA256(PHONE_HASH_KEY, normalize(phone))` before writing to `loyalty_accounts.phone_hash`. The plaintext phone number is never stored. The hash is one-way against the keyed input space — used for deduplication and cross-reference, not for lookup of the original number.

> **Why keyed (HMAC), not plain SHA-256.** The phone number domain is low-entropy (NANP ≈ 10¹⁰; global mobile < 10¹³). A plain SHA-256 over that domain is exhaustively brute-forceable on commodity GPU hardware in under an hour, which would render every `phone_hash` row trivially recoverable to anyone with read access to the table (DBA, replica reader, leaked backup, breached column). HMAC with a server-side secret blocks offline brute force without compromising determinism — the same phone always produces the same hash given the same key, preserving the dedup/cross-reference property. `PHONE_HASH_KEY` is loaded from Secrets Manager and rotated independently from `CANARY_ENCRYPTION_KEY`. Key class definition lives in `go-security.md` → "PII Hashing Keys".

**Phone normalization (input to HMAC):** strip non-digit characters, prepend `+` and country code if missing (default `+1` for US/CA merchants per merchant settings), reject inputs that do not parse to a valid E.164 number. Normalization is required so that `(415) 555-0100`, `415-555-0100`, and `+14155550100` all collapse to one hash. Normalization happens before HMAC, never after.

### Detection Routing Envelope

When a parsed event has a `detection_type`, Stage 2 publishes this 5-field envelope to `canary:detection`:

| Field | Value |
|-------|-------|
| `transaction_id` | UUID PK of the CRDM record written in step 7 |
| `merchant_id` | Tenant partition key |
| `event_type` | Original provider event type string |
| `event_id` | ULID from the gateway (original envelope field) |
| `detection_type` | `transaction` \| `cash_drawer` \| `gift_card` \| `loyalty` |

Stage 4 reads exclusively from `canary:detection`. It does not read from `canary:events`. The detection_type routing key tells Stage 4 which evaluation function to invoke without loading the full CRDM record unnecessarily.

### Source-Agnostic Principle

The `source` field in the `canary:events` envelope (`square` or `counterpoint`) is the only point where source-specific logic branches in Stage 2. After the parser dispatches to the appropriate parser function, the output is a Go struct implementing the `CRDMRecord` interface. The database write, idempotency check, and detection routing are identical regardless of source.

This is the payoff of the parse-then-route design. A third POS (e.g., Lightspeed) requires only: a new parser function, entries in the dispatch table, and mappings to CRDM fields. Detection, analytics, and receipt queries remain unchanged.

---

## Data Model

### CRDM Tables (`canary_sales` schema)

All CRDM tables share these structural invariants:
- UUID primary key
- `merchant_id TEXT NOT NULL` — tenant partition key
- `external_id TEXT NOT NULL` — provider-native ID for idempotency
- `UNIQUE (merchant_id, external_id)` — ON CONFLICT DO NOTHING
- `source TEXT NOT NULL` — `square` | `counterpoint`
- `created_at`, `updated_at` timestamps

#### `canary_sales.transactions`

```sql
CREATE TABLE canary_sales.transactions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         TEXT NOT NULL,
    external_id         TEXT NOT NULL,               -- provider-native transaction/payment ID
    source              TEXT NOT NULL,
    event_type          TEXT NOT NULL,
    location_id         TEXT,
    employee_id         TEXT,
    customer_id         TEXT,
    amount_cents        BIGINT,
    currency            TEXT,
    status              TEXT,
    card_last4          TEXT,                        -- P0: encrypt AES-256-GCM
    card_fingerprint    TEXT,                        -- P0: encrypt
    card_bin            TEXT,                        -- P0: encrypt
    card_exp_month      INT,                         -- P0: encrypt
    card_exp_year       INT,                         -- P0: encrypt
    card_brand          TEXT,
    card_entry_method   TEXT,
    is_refund           BOOLEAN NOT NULL DEFAULT false,
    payload             JSONB,                       -- P0: encrypt or remove (evidence_records holds original)
    occurred_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_transaction_merchant_ext UNIQUE (merchant_id, external_id)
);
```

#### `canary_sales.transaction_line_items`

```sql
CREATE TABLE canary_sales.transaction_line_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  UUID NOT NULL REFERENCES canary_sales.transactions(id),
    merchant_id     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    name            TEXT,
    quantity        NUMERIC,
    base_price_cents BIGINT,
    total_price_cents BIGINT,
    sku             TEXT,
    category_id     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_line_item_merchant_ext UNIQUE (merchant_id, external_id)
);
```

#### `canary_sales.transaction_tenders`

```sql
CREATE TABLE canary_sales.transaction_tenders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  UUID NOT NULL REFERENCES canary_sales.transactions(id),
    merchant_id     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    tender_type     TEXT,                            -- CARD | CASH | OTHER
    amount_cents    BIGINT,
    card_last4      TEXT,                            -- P0: encrypt
    card_brand      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_tender_merchant_ext UNIQUE (merchant_id, external_id)
);
```

#### `canary_sales.refund_links`

```sql
CREATE TABLE canary_sales.refund_links (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    refund_transaction_id UUID NOT NULL REFERENCES canary_sales.transactions(id),
    original_transaction_id UUID,                   -- NULL if original not yet ingested
    merchant_id         TEXT NOT NULL,
    external_refund_id  TEXT NOT NULL,
    external_original_id TEXT,
    amount_cents        BIGINT,
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_refund_link_merchant_ext UNIQUE (merchant_id, external_refund_id)
);
```

#### `canary_sales.cash_drawer_shifts` and `canary_sales.cash_drawer_events`

```sql
CREATE TABLE canary_sales.cash_drawer_shifts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    employee_id     TEXT,
    location_id     TEXT,
    opened_at       TIMESTAMPTZ,
    closed_at       TIMESTAMPTZ,
    expected_cash_cents BIGINT,
    actual_cash_cents   BIGINT,
    variance_cents  BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_drawer_shift_merchant_ext UNIQUE (merchant_id, external_id)
);
```

#### `canary_sales.loyalty_accounts` and `canary_sales.loyalty_events`

```sql
CREATE TABLE canary_sales.loyalty_accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     TEXT NOT NULL,
    external_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    phone_hash      BYTEA,                           -- HMAC-SHA256(PHONE_HASH_KEY, normalized_phone); 32 bytes; plaintext never stored
    points_balance  BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_loyalty_account_merchant_ext UNIQUE (merchant_id, external_id)
);
```

#### `canary_sales.ingestion_log`

```sql
CREATE TABLE canary_sales.ingestion_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id        TEXT NOT NULL,
    merchant_id     TEXT NOT NULL,
    source          TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'accepted',  -- accepted | parsed | failed | log_only
    error_message   TEXT,
    ip_address      TEXT,                              -- P0: hash or encrypt
    received_at     TIMESTAMPTZ NOT NULL,
    processed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ingestion_log_merchant_event ON canary_sales.ingestion_log (merchant_id, event_id);
```

---

## API Contract

Stage 2 does not expose HTTP endpoints directly. It is a pure stream consumer.

### MCP Tool Surface

Stage 2 contributes to the `get_ingestion_stats` management tool:

| Tool | Write | Description |
|------|:-----:|-------------|
| `get_ingestion_stats` | No | Throughput, parse success/failure rates, event_type distribution by merchant and time window |

Stats are derived from `canary_sales.ingestion_log` queries. No real-time stream queries needed.

---

## SLA

| Metric | P50 | P99 | Hard Limit |
|--------|-----|-----|------------|
| Message processing latency (stream read → DB write → detection publish → ACK) | 15ms | 80ms | 1000ms |
| Parser execution (pure function, no I/O) | <1ms | 5ms | 20ms |
| CRDM write (single table) | 3ms | 20ms | 200ms |
| CRDM write (order: 3 tables in one transaction) | 8ms | 40ms | 300ms |
| Detection publish (XADD canary:detection) | 1ms | 5ms | 50ms |
| Dedup key set (Valkey) | <1ms | 3ms | 30ms |

**Throughput:** Stage 2 supports multiple workers. Add workers until p99 latency stabilizes. Practical ceiling before DB contention: ~4 workers.

---

## Failure Modes

| Failure | Detection | Impact | Recovery |
|---------|-----------|--------|----------|
| `parse_failed=true` in envelope | Field check | ACK immediately; no CRDM write | Expected path; logged in ingestion_log as `log_only` |
| Unknown event_type (known source) | Dispatch table miss | ACK; log warning | New event types from POS never block pipeline |
| Parse error (malformed payload) | Parser returns error | No ACK; retry up to 10 | After 10 failures, DLQ + ACK |
| DB write failure | pgx error | No ACK | PEL redelivery on recovery |
| Duplicate (unique constraint) | ON CONFLICT | Silent ACK | Expected and correct |
| Detection publish failure | Valkey XADD error | Log error; ACK CRDM write anyway | Detection recoverable from DB; don't block the write |
| Dedup store unavailable | Valkey error | Fail-open; proceed | DB unique constraint is backstop |
| `ingestion_log` update miss | Record not found | Silent ACK | Stale reference; pipeline continues |

---

## Compliance

### PII Handling

| Field | Classification | Required Action |
|-------|---------------|----------------|
| `card_last4`, `card_fingerprint`, `card_bin`, `card_exp_*` | Sensitive (PCI) | AES-256-GCM encrypt before INSERT (P0-TSP-03) |
| `payload` (transaction forensic copy) | Restricted | Encrypt or remove — evidence_records already holds the sealed original (P0-TSP-02) |
| `employee_name`, `email` | Sensitive | AES-256-GCM encrypt (P0-TSP-04) |
| `ip_address` (ingestion_log) | Sensitive | HMAC hash with rotating key (P0-TSP-05) |
| `phone` (loyalty) | Sensitive | HMAC-SHA256 with `PHONE_HASH_KEY` — keyed one-way hash; plaintext never stored. Plain SHA-256 is prohibited (low-entropy domain) — see `go-security.md` → "PII Hashing Keys". **Value-vs-handler note:** this row classifies the parser's handling of plaintext phone numbers as sensitive because the cleartext flows through the parser before being hashed. The resulting `loyalty_accounts.phone_hash` value at rest is classified internal in `data-model.md` because the keyed HMAC is irreversible against the input space. The two classifications describe the in-flight handler and the at-rest value respectively, and are not in conflict. |
| `primary_recipient` (invoices JSONB) | Sensitive | Encrypt entire JSONB blob (P0-TSP-06) |

### Patent Scope

Stage 2 does not implement any patent-covered primitives. The hash-before-parse invariant belongs to Stage 1. Stage 2 receives an already-hashed, already-sealed event. The `event_hash` field in the stream message is read-only to Stage 2 — it uses it for dedup key construction but does not recompute it.

### Append-Only for CRDM

CRDM tables are not declared write-once (unlike `evidence_records`), but UPDATE operations should be limited to status fields and performed idempotently. The general posture is append-preferring: new events produce new records; corrections produce correction records, not overwrites of the original.

---

## Configuration

| Variable | Default | Stage 2 Usage |
|----------|---------|---------------|
| `STAGE2_BLOCK_MS` | `5000` | XREADGROUP blocking timeout |
| `VALKEY_STREAM` | `canary:events` | Source stream |
| `DETECTION_STREAM` | `canary:detection` | Detection routing stream |
| `DATABASE_URL` | (required) | PostgreSQL connection |
| `CANARY_ENCRYPTION_KEY` | (required prod) | AES-256-GCM key for PII field encryption |

---

## Open Items (Carry Forward to Go)

| # | Priority | Item |
|---|---------|------|
| P0-TSP-02 | P0 | Encrypt or remove `transactions.payload` — evidence_records holds the sealed original |
| P0-TSP-03 | P0 | Field-level AES-256-GCM on card data fields |
| P0-TSP-04 | P0 | Encrypt `employee_name` and `email` |
| P0-TSP-05 | P0 | Hash or encrypt `ip_address` in ingestion_log |
| P0-TSP-06 | P0 | Encrypt invoice `primary_recipient` JSONB |
| P1-TSP-02 | P1 | 24-month retention for CRDM tables, 12-month for ingestion_log |
| P1-TSP-05 | P1 | Structured JSON logging with `event_id`, `merchant_id`, `stage` |
| P2-TSP-04 | P2 | Document horizontal scaling ceiling and DB contention profile for 4+ workers |

---

*Canary | GrowDirect LLC | Confidential*
