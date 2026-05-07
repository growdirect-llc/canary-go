---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: internal package only (no HTTP service)
port: n/a
mcp-server: none
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Three-Way Match

**Type:** Internal Package — Procurement Integrity Control  
**Package:** `internal/threeway`  
**Binary:** None — linked into `cmd/receiving` only  
**MCP server:** None — called internally by `canary-receiving`  
**Depends on:** `purchase_orders`, `receiving_events`, `vendor_invoices` (all read-only from this package's perspective)  
**Feeds:** `receiving_discrepancies` (writes match results and discrepancy records), `raas` (chain event on match completion)

The three-way match is the oldest control in procurement: PO quantity, received quantity, and vendor invoice quantity must all agree before a payment is authorized. In retail at scale, this check is routinely skipped, batched, or approximated — and retailers pay for goods they didn't receive, or hold goods they didn't order against a vendor that won't take them back. Canary's three-way match runs automatically at receiving completion, inside the receiving transaction, before inventory is updated. The match result is the financial evidence. The chain record is the proof it ran.

---

## Business

### Why Three-Way Match Fails at SMB Scale

The institutional version of this control — PO to receipt to invoice, reviewed by AP before payment — requires an AP department. SMB retailers don't have one. The buyer places the PO. The dock associate signs the delivery slip. The invoice arrives by email two weeks later and gets paid without comparison. By the time anyone notices the received quantity was short, the vendor's dispute window has closed and the payment is gone.

Canary closes this gap by automating the comparison at the moment of receiving completion — not in a weekly AP review. The match runs when the associate scans the last item and the manager calls `complete_receiving`. If the quantities don't agree, a discrepancy record exists before the dock door closes. The vendor dispute window is still open. The evidence is in the chain.

A secondary problem: vendor invoices often arrive after receiving is complete. Canary handles this via deferred matching — the `invoice_only` status flag marks a PO as inventory-complete but payment-pending. When the invoice arrives and is registered, the deferred match runs automatically.

### Business Rules

1. The three-way match runs synchronously within the `complete_receiving` transaction. It is not a background job.
2. If no vendor invoice is present at the time of `complete_receiving`, the match proceeds with the PO and receiving quantities only (`invoice_only` status for invoice-related checks). Inventory is updated. The PO is flagged for invoice followup.
3. Tolerance is configurable per merchant via `merchant_settings.receiving_tolerance_pct`. Default: 2%. A received quantity within ±2% of the PO quantity is considered matched.
4. A `clean` match result approves the cost basis for inventory and ILDWAC automatically. No human review required.
5. Any `short_ship`, `over_ship`, or `invoice_discrepancy` result creates a `receiving_discrepancies` row. Payment is not blocked by this package — the downstream AP workflow (Phase 2) owns payment gating. This package creates the evidence record.
6. `total_discrepancy_sats` in the match result feeds the vendor dispute module. This value is the financial evidence of the shortfall.
7. Match results are financial evidence — 7-year retention. `three_way_match_results` is INSERT-only from the application layer.

### The Match Algorithm

```
match_result = RunMatch(po_line_items, receiving_events, vendor_invoice_lines)

For each item on the PO:
    po_qty       = po_line_items.ordered_qty
    received_qty = SUM(receiving_events.received_qty WHERE condition != 'damaged')
    invoice_qty  = vendor_invoice_lines.billed_qty (if invoice present, else NULL)

    variance_received = received_qty - po_qty
    variance_invoice  = (invoice_qty - received_qty) IF invoice_qty IS NOT NULL ELSE NULL

    tolerance = merchant_settings.receiving_tolerance_pct (default: 2%)

    IF ABS(variance_received / po_qty) <= tolerance:
        item_status = 'matched'
    ELIF variance_received < 0:
        item_status = 'short_ship'        -- received less than ordered
    ELIF variance_received > 0:
        item_status = 'over_ship'         -- received more than ordered

    IF variance_invoice IS NOT NULL AND ABS(variance_invoice / received_qty) > tolerance:
        item_status = 'invoice_discrepancy'  -- billed qty != received qty

overall_status:
    IF all items 'matched'      → 'clean'
    IF any items have variance  → 'discrepancy'
    IF no invoice present       → 'invoice_only'
```

The `invoice_discrepancy` check overrides `matched` — an item can be perfectly received against the PO but still have an invoice discrepancy if the vendor billed a different quantity than was received.

### Match Outcomes and Actions

| Status | Meaning | Auto-action |
|---|---|---|
| `clean` | All quantities within tolerance | Approve inventory update; approve ILDWAC cost packet; PO status → received |
| `short_ship` | Received less than ordered | Create discrepancy record; alert Hawk if discrepancy_cost_sats > merchant threshold |
| `over_ship` | Received more than ordered | Create discrepancy record; hold overage qty pending vendor instruction; alert Hawk |
| `invoice_discrepancy` | Billed qty != received qty | Create discrepancy record; flag PO for payment review; alert Hawk |
| `invoice_only` | No invoice registered yet | Update inventory now; flag PO for invoice followup; no discrepancy created |

---

## Technical

### Package Architecture

`internal/threeway` is a pure Go package with no HTTP layer and no independent process. It is compiled into `cmd/receiving` only. The decision to keep it internal rather than a standalone service reflects a deliberate trade-off: the match must run inside the receiving transaction to be atomic with the inventory update. A network call to a separate service within a database transaction introduces latency, connection overhead, and a distributed failure mode — all of which can leave the receiving transaction in an indeterminate state.

The cost: the three-way match logic is not independently deployable or independently callable. This is acceptable because the match has no callers other than `complete_receiving`. If a future phase introduces an AP review workflow that needs to re-run match logic against archived POs, that workflow can call `RunMatch` directly from its own binary after importing the `internal/threeway` package — Go's internal package system allows this.

```
cmd/receiving/
  main.go
  handlers/
    complete.go       ← calls internal/threeway.RunMatch()

internal/threeway/
  match.go            ← RunMatch() — the algorithm
  result.go           ← MatchResult, LineResult types
  persist.go          ← writes three_way_match_results and receiving_discrepancies
  match_test.go       ← table-driven tests with fixed test vectors
```

### Data Model

```sql
CREATE TABLE vendor_invoices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id          UUID NOT NULL REFERENCES purchase_orders(id),
    invoice_number TEXT NOT NULL,
    vendor_id      UUID NOT NULL,
    invoice_date   DATE NOT NULL,
    total_cost_sats BIGINT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending',
    -- status values: pending | matched | disputed | paid
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(po_id, invoice_number)
);

CREATE INDEX idx_vendor_invoices_po ON vendor_invoices(po_id);
CREATE INDEX idx_vendor_invoices_status ON vendor_invoices(status) WHERE status IN ('pending', 'disputed');

CREATE TABLE vendor_invoice_lines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      UUID NOT NULL REFERENCES vendor_invoices(id) ON DELETE CASCADE,
    item_id         UUID NOT NULL REFERENCES items(id),
    billed_qty      INT NOT NULL,
    unit_cost_sats  BIGINT NOT NULL,
    line_total_sats BIGINT NOT NULL,
    UNIQUE(invoice_id, item_id),
    CONSTRAINT line_total_check CHECK (line_total_sats = billed_qty * unit_cost_sats)
);

CREATE TABLE three_way_match_results (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id                  UUID NOT NULL REFERENCES purchase_orders(id),
    invoice_id             UUID REFERENCES vendor_invoices(id),
    matched_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    overall_status         TEXT NOT NULL,
    -- overall_status values: clean | discrepancy | invoice_only
    line_results           JSONB NOT NULL,
    -- [{item_id, po_qty, received_qty, invoice_qty, status, variance_pct}]
    total_discrepancy_sats BIGINT NOT NULL DEFAULT 0,
    raas_sequence          BIGINT
    -- populated after append_event confirms the chain position
);

-- INSERT-only. No UPDATE or DELETE path exists in application code.
CREATE INDEX idx_three_way_match_po ON three_way_match_results(po_id);
CREATE INDEX idx_three_way_match_status ON three_way_match_results(overall_status)
    WHERE overall_status != 'clean';
```

### Design Trade-offs

**`line_results` as JSONB vs a normalized line results table.** The match produces a result per item per PO — this could be stored in a `three_way_match_line_results` table with one row per item. The JSONB approach is chosen because: (1) the line results are always read together with the match result header — there is no query that fetches individual line results without the header; (2) the JSONB blob is set at INSERT time and never mutated; (3) at SMB PO scale (50–300 line items), the JSONB blob is small (< 10KB). The trade-off is that JSONB line results are not SQL-queryable without `jsonb_array_elements` unnesting. If a reporting requirement emerges for cross-PO line-level analysis (e.g., "show me all items with a short_ship discrepancy across all vendors"), a materialized view or a separate `match_line_summary` table can be added without changing the insert path.

**`line_total_check` constraint on vendor_invoice_lines.** The check constraint `line_total_sats = billed_qty * unit_cost_sats` enforces arithmetic integrity at the database level. Vendor-provided invoice data frequently contains rounding errors or deliberate manipulation. The DB constraint catches this at INSERT time and returns a clear error rather than silently propagating a bad total into the match result.

**Synchronous match vs async match.** The alternative to running the match synchronously inside the receiving transaction is to run it asynchronously — complete the receiving dock scan, update inventory immediately, and run the financial match in the background. This is faster for the dock associate (no waiting) but means inventory can be updated before the financial integrity check completes. For the SMB ICP, the delay from dock associate to match completion is under 3 seconds on a normal PO. The synchronous approach is the right call: it preserves the atomicity guarantee and the dock associate is already waiting for the manager to authorize completion anyway.

**Tolerance as a merchant setting vs a fixed constant.** The 2% default tolerance accommodates real-world measurement variance (counting errors, unit-of-measure rounding on bulk items). Making it configurable per merchant allows a high-value merchant (jewelry, electronics) to set 0% tolerance — any discrepancy triggers a discrepancy record — while a bulk goods merchant (feed, pet supply) can tolerate larger counting variance. The setting lives in `merchant_settings`; the match algorithm reads it at execution time, not at package initialization.

**Deferred invoice match.** When `complete_receiving` is called without a vendor invoice, the match runs on PO vs received quantities only and returns `invoice_only`. The `vendor_invoices` table receives a row when the merchant or an AP agent registers the invoice. A separate `RunDeferredMatch` function in `internal/threeway` re-runs the full match against the existing `three_way_match_results` row when the invoice arrives. This produces a new `three_way_match_results` row (INSERT-only — the original is not updated) with the invoice comparison populated. The chain event for the deferred match is a new `"receiving.invoice_matched"` event type.

### The Go Interface

```go
// Package threeway implements the three-way match algorithm.
// It is an internal package — only cmd/receiving may import it.
package threeway

// Input types — populated by the receiving handler from its DB queries

type POLineItem struct {
    ItemID        uuid.UUID
    OrderedQty    int
    UnitCostSats  int64
}

type ReceivingEvent struct {
    ItemID      uuid.UUID
    ReceivedQty int
    Condition   string // "acceptable" | "damaged" | "wrong_item"
}

type InvoiceLine struct {
    ItemID       uuid.UUID
    BilledQty    int
    UnitCostSats int64
}

// MatchInput is the full set of inputs for a match run.
type MatchInput struct {
    POID          uuid.UUID
    InvoiceID     *uuid.UUID  // nil if no invoice present
    POLines       []POLineItem
    Events        []ReceivingEvent
    InvoiceLines  []InvoiceLine  // empty if no invoice present
    TolerancePct  float64        // from merchant_settings; default 0.02
}

// LineResult is the per-item outcome.
type LineResult struct {
    ItemID        uuid.UUID
    POQty         int
    ReceivedQty   int
    InvoiceQty    *int    // nil if no invoice
    Status        string  // matched | short_ship | over_ship | invoice_discrepancy
    VariancePct   float64
    DiscrepancySats int64
}

// MatchResult is the outcome of a single RunMatch call.
type MatchResult struct {
    OverallStatus        string  // clean | discrepancy | invoice_only
    LineResults          []LineResult
    TotalDiscrepancySats int64
}

// RunMatch executes the algorithm and returns a MatchResult.
// It does NOT write to the database — that is Persist's job.
// This separation allows unit testing without a DB.
func RunMatch(input MatchInput) MatchResult

// Persist writes three_way_match_results and receiving_discrepancies rows,
// appends the RaaS chain event, and returns the match result ID and chain sequence.
// Must be called within the receiving transaction (tx is a pgx.Tx).
func Persist(ctx context.Context, tx pgx.Tx, result MatchResult, input MatchInput) (matchID uuid.UUID, raasSeq int64, err error)
```

The separation of `RunMatch` from `Persist` is critical for testability. `RunMatch` is a pure function — same inputs, same outputs, no side effects. All DB writes happen in `Persist`, which is called by the receiving handler after `RunMatch` returns. Test coverage for `RunMatch` uses table-driven tests with fixed fixtures; no test database required.

### Test Vectors

Fixed test vectors must be committed in `internal/threeway/match_test.go`. The minimum set:

| Scenario | PO qty | Received qty | Invoice qty | Expected status |
|---|---|---|---|---|
| Perfect match | 100 | 100 | 100 | clean |
| Within tolerance (received 99, tol 2%) | 100 | 99 | 99 | clean |
| Short ship — below tolerance | 100 | 85 | 85 | short_ship |
| Over ship | 100 | 110 | 110 | over_ship |
| Invoice discrepancy (received OK, billed wrong) | 100 | 100 | 90 | invoice_discrepancy |
| No invoice | 100 | 100 | nil | invoice_only |
| Damaged items excluded | 100 | 80 acceptable + 15 damaged | 95 | short_ship |
| Zero-tolerance merchant | 100 | 99 | 99 | short_ship (0% tol) |

---

## Ops

### Observability

`internal/threeway` emits structured log entries on every match run:

```json
{
  "level": "info",
  "event": "three_way_match.complete",
  "po_id": "...",
  "overall_status": "discrepancy",
  "line_count": 24,
  "discrepancy_line_count": 3,
  "total_discrepancy_sats": 48500,
  "duration_ms": 12
}
```

Latency is reported as `duration_ms` on the log line and as a histogram metric `threeway_match_duration_ms`. Alert if P99 exceeds 500ms — the match algorithm should complete in <50ms for any PO ≤ 1000 line items; latency above 500ms indicates a DB contention issue in `Persist`, not an algorithm issue.

### Failure Modes

| Failure | Behavior | Recovery |
|---|---|---|
| `RunMatch` panics | Caught by receiving handler's recover(); `complete_receiving` returns 500; PO status unchanged | Investigate: likely a nil pointer in invoice line or empty PO line slice |
| `Persist` fails (DB error) | `complete_receiving` rolls back; PO status unchanged | Caller retries `complete_receiving` |
| RaaS `append_event` returns 409 | `Persist` retries once with updated sequence; if second attempt fails, rolls back | Receiving handler returns 409 to caller |
| Discrepancy cost calculation overflow | Checked in `RunMatch`: if `discrepancy_cost_sats` would overflow int64, return error | Should never occur at SMB scale; log and alert if it does |

---

## Compliance

### Financial Evidence

`three_way_match_results` rows are financial evidence. Enforce INSERT-only at the DB level:

```sql
REVOKE UPDATE, DELETE ON three_way_match_results FROM canary_app;
REVOKE UPDATE, DELETE ON vendor_invoices FROM canary_app;
REVOKE UPDATE, DELETE ON vendor_invoice_lines FROM canary_app;
```

`vendor_invoices.status` must be updatable (pending → matched → disputed → paid). This is an exception to the INSERT-only rule — the status transition is audited via the RaaS chain event, which records the prior and new status in the event payload.

### Retention

| Table | Minimum Retention | Authority |
|---|---|---|
| `three_way_match_results` | 7 years | Financial records (IRS, SOX) |
| `vendor_invoices` | 7 years | AP records, vendor dispute evidence |
| `vendor_invoice_lines` | 7 years | Line-item evidence for dispute resolution |

### Vendor Dispute Evidence Chain

The `raas_sequence` on `three_way_match_results` is the chain anchor for vendor disputes. When a merchant disputes a short shipment, the dispute evidence package is:

1. `purchase_orders` row (what was ordered, at what cost)
2. `receiving_events` rows (what was scanned, by whom, at what time)
3. `three_way_match_results` row (the automated comparison result)
4. RaaS chain event at `raas_sequence` (tamper-evident proof that the match ran at that moment)

All four must be presented together. The `total_discrepancy_sats` value in the match result is the amount in dispute. `verify_chain` on the RaaS namespace confirms the chain is unbroken from PO creation through match completion.

---

## Related SDDs

- `receiving.md` — calls `internal/threeway.RunMatch()` and `Persist()` inside `complete_receiving`; owns the receiving transaction
- `vendor-invoices.md` — (Phase 2) AP workflow for invoice registration and deferred match triggering
- `raas.md` — `append_event` called with `"receiving.completed"` or `"receiving.invoice_matched"` on every match; chain record is the legal anchor
- `hawk.md` — receives discrepancy alerts when `total_discrepancy_sats` exceeds merchant threshold
- `item.md` — item_id references on invoice lines are validated against the item master
- `ildwac.md` — `total_cost_sats` on clean matches feeds the cost basis calculation for ILDWAC WAC updates
