---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: receiving
port: 8092
mcp-server: canary-receiving
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Receiving

**Type:** Operational Module — Dock Control + Inventory Intake  
**Binary:** `cmd/receiving` → `:8092`  
**MCP server:** `canary-receiving` (8 tools)  
**Depends on:** `identity`, `raas`, `inventory-as-a-service`, `three-way-match`  
**Feeds:** `inventory-as-a-service` (quantity updates), `ildwac` (cost basis events), `hawk` (discrepancy alerts)

Receiving is where vendor promises meet physical reality. A PO is a contract; the ASN is the vendor's claim; the dock scan is the truth. The gap between them — short shipments, damaged goods, substitutions — is the primary source of inventory shrink that merchants can't see in their POS data. Without a disciplined receiving module, shrink from the receiving dock surfaces as "mystery shrink" in LP reports weeks later, after the vendor's dispute window has closed. Canary's receiving module closes that window at the dock, not in hindsight.

**Multi-tenant context.** Receiving tables (`purchase_orders`, `asn_documents`, `receipts`, `receipt_discrepancies`, `disposition_events`) live per-tenant in `tenant_{merchant_id}`. Every receiving event is scoped to a single merchant; cross-tenant analytics on receiving performance (vendor fill rate benchmarks across the platform) flow through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Receiving operates with all Optional Features (per `platform-overview.md`) disabled. The PO and receipt workflow runs entirely on internal records and the standard three-way match (PO + ASN + dock scan). When `L402_ENABLED=true`, OTB wallet debit on PO commitment is enforced per `l402-otb.md` — when off, OTB is tracked but not Lightning-settled. When `ILDWAC_ENABLED=true`, receipt events populate the five-dimension cost packet (Item × Location × Device × MCP × Port × WAC) in addition to standard ILWAC. Smart-contract receipt clearance (vendor subnet) requires `VENDOR_CONTRACTS_ENABLED=true`.

---

## Business

### The Receiving Problem

Most SMB retailers run receiving as an informal process: a manager signs the driver's delivery slip without counting, or counts cartons but not units, or counts units but never compares against the original PO quantity. The result is systematic under-receipt that accretes into unexplained shrink over weeks and months. The merchant has no vendor dispute record because the discrepancy was never captured.

The three-document chain — PO, ASN, dock scan — is the standard procurement control. Canary enforces it automatically. When the three quantities agree, the receipt is clean. When they diverge beyond tolerance, a discrepancy record is created, the inventory adjustment is held or flagged, and Hawk is alerted if the dollar value crosses the merchant's configured threshold. The vendor dispute window is typically 15–30 days from delivery; Canary ensures the evidence exists before that window closes.

### The Receiving Workflow

```
PO created (OTB commitment made) → L402 wallet debited
    │
    ▼
Vendor sends ASN (advance ship notice)
    │
    ▼
Dock associate scans items as they arrive
    │
    ▼
System compares: PO qty vs received qty vs ASN qty
    │
    ▼
Three-way match triggered (see three-way-match.md)
    │
    ▼
Inventory position updated (inventory-as-a-service.record_adjustment)
    │
    ▼
ILDWAC cost packet submitted (received_at PO cost, in satoshis)
    │
    ▼
RaaS chain event appended: "receiving.completed"
    │
    ▼
If discrepancy > tolerance: Hawk alert generated
```

The dock scan step is the only place a human touches this workflow. Everything downstream is automatic. This is intentional: the associate's job is to count accurately; the system's job is to do the rest.

### Business Rules

1. A PO cannot be received against without a registered namespace (identity check at creation time).
2. Items received in `damaged` condition are excluded from inventory adjustments and flagged in the discrepancy record.
3. Receiving completion requires two-party confirmation: dock associate scan + manager authorization (`complete_receiving`). This is the four-eyes control.
4. A PO with status `disputed` cannot be closed until all discrepancy records are resolved.
5. Unresolved discrepancies older than 30 days trigger automatic Hawk escalation — regardless of whether the merchant has manually reviewed them.
6. Partial receives are valid: a PO may move through `partially_received` as multiple shipments arrive against the same PO. The match runs only on `complete_receiving`.
7. PO cost data is financial record — 7-year retention minimum. No hard deletes on any receiving table.

---

## Technical

### Service Boundaries

Receiving owns the full lifecycle of a purchase order from creation through closure. It does not own inventory positions (those belong to inventory-as-a-service) or cost basis calculations (those belong to ILDWAC). Receiving is the orchestrator that calls both.

| Owned Tables | Purpose |
|---|---|
| `purchase_orders` | PO lifecycle |
| `po_line_items` | Per-item quantity and cost |
| `advance_ship_notices` | Vendor ASN registrations |
| `receiving_events` | Dock scan record (INSERT-only) |
| `receiving_discrepancies` | Gap analysis, dispute tracking |

`receiving_events` is append-only. A dock scan is an immutable fact. Corrections are new events, not updates to old ones. This mirrors the RaaS chain invariant — receiving events are financial evidence.

### Data Model

```sql
CREATE TABLE purchase_orders (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id          UUID NOT NULL REFERENCES merchants(id),
    location_id          UUID NOT NULL REFERENCES locations(id),
    vendor_id            UUID NOT NULL,
    po_number            TEXT NOT NULL UNIQUE,
    status               TEXT NOT NULL DEFAULT 'open',
    -- status values: open | partially_received | received | cancelled | disputed
    total_units          INT NOT NULL DEFAULT 0,
    total_cost_sats      BIGINT NOT NULL DEFAULT 0,
    issued_at            TIMESTAMPTZ NOT NULL,
    expected_delivery_at TIMESTAMPTZ,
    closed_at            TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_purchase_orders_merchant_status ON purchase_orders(merchant_id, status);
CREATE INDEX idx_purchase_orders_location ON purchase_orders(location_id);

CREATE TABLE po_line_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id           UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    item_id         UUID NOT NULL REFERENCES items(id),
    ordered_qty     INT NOT NULL,
    unit_cost_sats  BIGINT NOT NULL,
    received_qty    INT NOT NULL DEFAULT 0,
    UNIQUE(po_id, item_id)
);

CREATE TABLE advance_ship_notices (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id            UUID NOT NULL REFERENCES purchase_orders(id),
    vendor_reference TEXT,
    expected_at      TIMESTAMPTZ,
    received_at      TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'pending',
    -- status values: pending | partial | complete
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE receiving_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id          UUID NOT NULL REFERENCES purchase_orders(id),
    asn_id         UUID REFERENCES advance_ship_notices(id),
    location_id    UUID NOT NULL,
    item_id        UUID NOT NULL REFERENCES items(id),
    device_id      UUID REFERENCES inventory_devices(id),
    -- device_id is the receiving sensor (RFID gate, barcode scanner) or NULL for manual entry
    received_qty   INT NOT NULL,
    condition      TEXT NOT NULL DEFAULT 'acceptable',
    -- condition values: acceptable | damaged | wrong_item
    associate_id   TEXT NOT NULL,
    raas_sequence  BIGINT,
    -- populated after append_event confirms the chain position
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- INSERT-only. No UPDATE or DELETE path exists in application code.
CREATE INDEX idx_receiving_events_po ON receiving_events(po_id);
CREATE INDEX idx_receiving_events_item ON receiving_events(po_id, item_id);

CREATE TABLE receiving_discrepancies (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id                 UUID NOT NULL REFERENCES purchase_orders(id),
    item_id               UUID NOT NULL REFERENCES items(id),
    po_qty                INT NOT NULL,
    asn_qty               INT,
    -- NULL if no ASN was registered for this PO
    received_qty          INT NOT NULL,
    discrepancy_type      TEXT NOT NULL,
    -- type values: short_ship | over_ship | damaged | substitution
    discrepancy_qty       INT NOT NULL,
    discrepancy_cost_sats BIGINT NOT NULL,
    resolved_at           TIMESTAMPTZ,
    resolution            TEXT,
    -- resolution values: vendor_credit | reorder | accepted | disputed
    hawk_case_id          UUID
    -- populated if LP case opened via hawk service
);

CREATE INDEX idx_receiving_discrepancies_po ON receiving_discrepancies(po_id);
CREATE INDEX idx_receiving_discrepancies_unresolved
    ON receiving_discrepancies(po_id)
    WHERE resolved_at IS NULL;
```

### Design Trade-offs

**Dock-scan as INSERT-only vs mutable event log.** The receiving_events table could allow updates — useful for correcting a miscount before the match runs. The choice here is append-only, consistent with the evidentiary posture of the platform. A miscounted line item is corrected by a second scan event with a negative or corrective quantity; the original scan is preserved. This adds complexity to the aggregation query in `complete_receiving` but is non-negotiable for audit integrity. `scan_item` validates that cumulative received_qty does not exceed a sensible upper bound (3× ordered_qty) and returns a warning rather than an error if the associate is likely miscounting.

**Per-item UNIQUE constraint on po_line_items.** A PO can only have one line item per item_id. This prevents duplicate PO lines for the same SKU (a common data quality issue in manual PO entry) but requires the UI to aggregate quantities if the buyer wants to split a single item across multiple cost tiers. For the SMB ICP, split-cost PO lines are rare enough that the constraint is the right call — it catches data entry errors far more often than it blocks legitimate use.

**vendor_id as UUID without a vendors table.** The current schema references vendor_id as a UUID on purchase_orders without a foreign key to a vendors table. This is deliberate deferral — a full vendor master (contact info, payment terms, dispute history) is a Phase 2 addition. The receiving module needs vendor identity for grouping and filtering; it does not need vendor master data to function. When the vendor master lands, it adds `REFERENCES vendors(id)` to the FK and back-fills existing rows via a migration.

**Status machine on purchase_orders.** The status column is a TEXT enum with no DB-enforced transition rules. Application code in the `complete_receiving` handler enforces the valid transitions (`open` → `partially_received` → `received`, `open` → `cancelled`, `received` → `disputed`). A PostgreSQL CHECK constraint could enforce valid status values but not valid transitions. A full state machine in the DB would require a trigger, which is the approach used in RaaS for chain integrity. For PO status, the trade-off goes the other way: invalid transitions are an application bug, not an adversarial input. The trigger overhead is not justified.

### Receiving Completion — The Match Handoff

`complete_receiving` is the critical path. In order:

```
1. Lock the PO row FOR UPDATE — prevents concurrent completion attempts
2. Aggregate received_qty per item across all receiving_events WHERE condition != 'damaged'
3. Call three-way-match.RunMatch(po_id, line_items, receiving_summary, invoice_lines_if_present)
4. Persist three_way_match_results row (owned by three-way-match package)
5. For each matched item: call inventory-as-a-service.RecordAdjustment(item_id, location_id, +qty, cost_sats)
6. Submit ILDWAC cost packet: event_type="receiving.completed", item_ids, unit_cost_sats per item
7. Append RaaS chain event: event_type="receiving.completed", payload includes match result summary
8. For discrepancies: persist receiving_discrepancies rows; if discrepancy_cost_sats > threshold → call hawk.CreateCase
9. Update PO status → 'received' (or 'disputed' if any discrepancies unresolved)
10. Unlock
```

Steps 4–9 execute inside a single pgx transaction. If any step fails, the entire completion rolls back. The PO remains in its prior status. The caller retries.

The one exception: RaaS append_event (step 7) is called inside the transaction. If the RaaS service is unreachable, the transaction rolls back and completion fails with 503. This is the right trade-off — an incomplete match without a chain record is worse than a failed completion that the caller retries.

### API Contract

All routes require JWT auth except `/receiving/healthz` and `/receiving/readyz`.

```
GET  /receiving/healthz                    → 200
GET  /receiving/readyz                     → 200 | 503
POST /receiving/pos                        → 201 {po_id, po_number}
POST /receiving/asns                       → 201 {asn_id}
POST /receiving/events                     → 201 {event_id, discrepancy?}
POST /receiving/pos/{po_id}/complete       → 200 {match_result, discrepancies[]}
GET  /receiving/pos/{po_id}                → 200 {po, line_items, asn, events, discrepancies}
GET  /receiving/pos?merchant_id=&location_id= → 200 [{po}]
GET  /receiving/discrepancies?merchant_id= → 200 [{discrepancy}]
PUT  /receiving/discrepancies/{id}/resolve → 200 {resolved_at}
```

### MCP Tool Surface — `canary-receiving` (8 tools)

| Tool | Input | Output | Notes |
|---|---|---|---|
| `create_po` | merchant_id, location_id, vendor_id, line_items[] | {po_id, po_number} | Creates PO + debits L402 OTB commitment |
| `receive_asn` | po_id, vendor_reference, expected_at | {asn_id} | Register advance ship notice; PO status unaffected |
| `scan_item` | po_id, item_id, qty, condition, associate_id, device_id? | {event_id, discrepancy?} | Dock scan; appends receiving_event; triggers running discrepancy check |
| `complete_receiving` | po_id, manager_id | {match_result, discrepancies[]} | Triggers three-way match; requires manager_id for four-eyes |
| `get_po` | po_id | {po, line_items, asn, events, discrepancies} | Full PO state |
| `list_open_pos` | merchant_id, location_id? | [{po}] | POs with status open or partially_received |
| `get_discrepancies` | merchant_id, date_from?, date_to?, unresolved_only? | [{discrepancy}] | Discrepancy report; filters by date range or unresolved status |
| `resolve_discrepancy` | discrepancy_id, resolution, notes | {resolved_at} | Closes a discrepancy; fires Hawk case close if hawk_case_id is set |

### Go Implementation Notes

- `scan_item` must be idempotent on `event_id` — the MCP caller may retry on network timeout. Use `INSERT ... ON CONFLICT (id) DO NOTHING` keyed on a caller-supplied idempotency_key rather than the server-generated UUID.
- `complete_receiving` acquires a `SELECT ... FOR UPDATE` on the PO row before any downstream calls. Concurrent completion attempts (MCP agent + human UI) must queue, not race.
- The three-way-match call in `complete_receiving` is a synchronous in-process call to `internal/threeway.RunMatch()` — not an HTTP call. The three-way-match package is an internal library, not a separate service. See `three-way-match.md`.
- ILDWAC cost packets are submitted via a non-blocking goroutine after the transaction commits. If ILDWAC is unavailable, the cost packet is written to a `ildwac_pending` retry queue in Valkey. The receiving transaction does not fail due to ILDWAC unavailability.
- Hawk alerting follows the same pattern: non-blocking goroutine, retry queue in Valkey if Hawk is unreachable.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|---|---|---|---|---|
| `create_po` | <300ms | <1s | 3s | Alert |
| `scan_item` | <200ms | <500ms | 2s | Alert + return partial result |
| `complete_receiving` | <1s | <3s | 10s | Alert; do not retry automatically |
| `get_po` | <100ms | <500ms | 2s | Alert |

`complete_receiving` is deliberately slow-tolerant — it is a manager-initiated action, not a real-time register operation. The 10s hard limit reflects the full chain of calls (three-way match + inventory update + ILDWAC packet + RaaS append).

### Health Endpoints

```
GET /receiving/healthz  → 200 {"status": "ok"}  (shallow liveness)

GET /receiving/readyz   → 200 | 503
{
  "status": "ok",
  "db_ok": true,
  "valkey_ok": true,
  "open_po_count": 14,
  "unresolved_discrepancy_count": 3
}
```

### Failure Modes

| Failure | Behavior | Recovery |
|---|---|---|
| DB unreachable | All writes return 503; reads served from cache where available | Auto-recover on pgx pool reconnect |
| inventory-as-a-service unreachable | `complete_receiving` rolls back; PO remains in prior status | Caller retries; no partial state |
| RaaS unreachable | `complete_receiving` rolls back | Caller retries |
| ILDWAC unreachable | Transaction commits; cost packet queued in Valkey for retry | Async retry worker drains queue |
| Hawk unreachable | Transaction commits; alert queued in Valkey for retry | Async retry worker drains queue |
| Sequence violation on RaaS append | `complete_receiving` returns 409; PO remains open | Receiving service fetches current chain head, recomputes sequence, retries once automatically |

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|---|---|---|
| `receiving:po:{po_id}:lock` | 30s | Distributed lock for complete_receiving |
| `receiving:ildwac:pending` | None | Cost packet retry queue (list) |
| `receiving:hawk:pending` | None | Alert retry queue (list) |

### Monitoring

Alert on:
- Unresolved discrepancies older than 28 days (2-day lead time before auto-escalation at day 30)
- `complete_receiving` P99 > 3s sustained for 2 minutes
- `receiving:ildwac:pending` queue depth > 50 (cost data backlog)
- Any PO in `disputed` status for more than 72 hours without a Hawk case ID

---

## Compliance

### PII Classification

| Field | Table | Classification | Treatment |
|---|---|---|---|
| `associate_id` | `receiving_events` | Internal PII | Store as employee ID reference; do not log in plain text |
| `vendor_id` | `purchase_orders` | Sensitive (commercial relationship) | Exclude from external-facing reports |
| `total_cost_sats` | `purchase_orders` | Financial record | 7-year retention; encrypt in transit |
| `discrepancy_cost_sats` | `receiving_discrepancies` | Financial record | 7-year retention; vendor dispute evidence |

### Append-Only Invariant

`receiving_events` is INSERT-only. Enforce at the database level:

```sql
REVOKE UPDATE, DELETE ON receiving_events FROM canary_app;
```

### Retention

| Table | Minimum Retention | Authority |
|---|---|---|
| `purchase_orders` | 7 years | Financial records (IRS, SOX) |
| `po_line_items` | 7 years | Financial records |
| `receiving_events` | 7 years | Financial evidence (vendor disputes) |
| `receiving_discrepancies` | 7 years | Vendor dispute evidence |
| `advance_ship_notices` | 7 years | Procurement records |

### Four-Eyes Control

`complete_receiving` requires `manager_id` in the request body. The handler validates that `manager_id` differs from the `associate_id` on any `receiving_events` rows for this PO. A manager cannot self-authorize completion of a receiving session they personally scanned. This check is enforced in application code, not DB constraints — the associate and manager identity tokens are from the JWT claims on their respective sessions.

---

## Related SDDs

- `three-way-match.md` — called synchronously by `complete_receiving`; owns `three_way_match_results` and `vendor_invoices`
- `inventory-as-a-service.md` — `RecordAdjustment` called on completion; owns inventory positions
- `ildwac.md` — receives cost basis packets on each completed receipt
- `hawk.md` — receives discrepancy alerts when dollar threshold exceeded or 30-day aging breached
- `raas.md` — `append_event` called with `"receiving.completed"` on every match; chain record is the evidentiary anchor
- `identity.md` — merchant and location existence validated at PO creation
