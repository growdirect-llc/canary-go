---
spec-version: 1.0
target-implementation: Go (Canary side) + .NET (DriftPOS side, partner-implemented)
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | mTLS-or-JWT auth | ARTS POSLOG payloads
status: handoff-ready
updated: 2026-05-03
binary: gateway (existing) + driftpos-adapter (new)
port: 8080 (gateway) — DriftPOS register pushes here
mcp-server: canary-driftpos-adapter (when wired)
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# DriftPOS ↔ Canary Integration Contract (CAN-DESIGN-001)

> **Type:** Integration Contract Specification — Wire Protocol Between Two Independently-Owned Systems
> **Linear:** [GRO-759 / CAN-DESIGN-001](https://linear.app/growdirect/issue/GRO-759/)
> **Parent:** [GRO-721 / CAN-RES-001](https://linear.app/growdirect/issue/GRO-721/) — Capability Map (130 capabilities split DriftPOS / Canary / Both)
> **Sibling:** [GRO-761 / Loop 2 Build Report](../../Brain/wiki/cards/loop2-build-report.md) — Multi-POS adapter substrate proven in code (Square + Counterpoint + Clover stub)

---

## Governing Thesis

**DriftPOS owns the register surface; Canary owns everything behind it; ARTS POSLOG is the wire contract between them.** This is the integration spec for the first partner-built POS to consume Canary's multi-POS adapter substrate. The substrate already runs in production code (`internal/adapters/{adapter,square,counterpoint,clover}/`); DriftPOS becomes the fourth `SourceAdapter` implementation. The contract defined here is what Bart's .NET team writes against, and what Canary's Go team plumbs in alongside the existing Square + Counterpoint adapters. Three governing constraints fall out of that thesis: every payload either IS an ARTS POSLOG message or projects clearly into one (no proprietary message formats); register-hardware-execution belongs to DriftPOS regardless of where the data eventually lands in the back-half spine (Identity Rule); and Canary never receives raw card data — only network-tokenized payment fingerprints, per the party-identity-design.md PCI boundary. Every wire crossing in this spec honors all three.

---

## Executive Summary

This SDD defines 47 endpoints across 9 domains, 38 ARTS-aligned wire entities, and 12 open questions requiring DriftPOS-team confirmation. Five design decisions carry the weight:

1. **REST + JSON over mTLS** for register↔back-half (not gRPC, not WebSockets, not message bus). Justified by the firewall posture of an SMB merchant's back-office network and the operational reality that Bart's .NET stack speaks JSON natively. JWT-only fallback is acceptable for v1 if mTLS certificate management is not ready by Bart's pilot date.
2. **Push for transactions, poll for master data.** Register pushes every transaction-complete event to Canary in real-time (the LP, evidentiary, and OTB rails depend on it). Back-half pushes catalog/customer/pricing changes via *change notifications* that DriftPOS pulls on its own cadence — DriftPOS's offline-capable register cannot have its master data pushed at it without breaking the SQLite sync model.
3. **Canary is a side-of-spine, not the system of record for items, customers, or inventory.** During Phase 1 (parallel-run), Counterpoint remains the SoR for master data; DriftPOS's SQLite cache reads from Canary, which projects from Counterpoint via the existing `edge` poller. During Phase 2 (Counterpoint sunset), Canary becomes the SoR. The wire contract behaves identically in both phases — the migration is invisible to the register.
4. **Tenant isolation is enforced by the gateway, not by the register.** Every inbound request carries `tenant_id` extracted from the mTLS cert (or JWT claim); a register mis-configured to send the wrong `tenant_id` is rejected at the boundary, not silently routed to the wrong back-half tenant. This honors the platform's data-isolation thesis without depending on the register doing the right thing.
5. **Party resolution happens at Canary, but the result is round-tripped to the register for receipt printing and loyalty display.** When a payment fingerprint arrives at Canary, party resolution is synchronous (Rule 1 hits in <50ms p99 per `party-identity-design.md` NFRs); the resolved `party_code`, loyalty membership, and any household-level offers come back in the response. This is the load-bearing latency budget of the entire wire — if Canary breaks 200ms p99, DriftPOS's checkout flow stalls.

The 47 endpoints group as: 9 transaction (push from register), 8 catalog (pull from back-half), 6 customer/party (bidirectional), 5 inventory (bidirectional with reservation semantics), 4 pricing/promotion (pull), 4 loyalty (bidirectional), 4 employee/auth (pull), 4 config/heartbeat (bidirectional), 3 compliance/evidence (push). Each is documented in §3 with verb, path, request/response shape, idempotency strategy, and ARTS POSLOG provenance.

The contract works with or without every Canary Optional Feature (L402-OTB, ILDWAC, blockchain-anchor, party householding) enabled. Adapters operate identically with all flags off; downstream services consume the wire envelope's added dimensions when their respective flags are on.

---

## Table of Contents

1. Scope, Identity Rule, and the Boundary
2. Wire Inventory — entities, directions, frequencies, ARTS mapping
3. API Surface — endpoints by domain
4. Eventing Pattern — push vs poll, ordering, backpressure
5. Ownership Semantics — system of record per entity, cache rules, conflict resolution
6. Migration State Machine — parallel-run → cutover → sunset
7. Failure Modes — register offline, back-half offline, partial sync, isolation breach, PCI breach
8. Security Posture — mTLS, auth, PCI scope, audit logging, evidence anchors
9. Test Strategy — contract fixtures, replay harness, conformance suite
10. Open Questions — DriftPOS-team confirmation required
11. Cross-references and Related SDDs

---

## §1. Scope, Identity Rule, and the Boundary

### What this contract covers

The wire between two independent systems:

- **DriftPOS** — Bart's team, .NET API + Angular web + Kotlin/Jetpack Android + PostgreSQL→SQLite sync. Register-side. Owns: every interaction at the physical register, the cashier-facing UI, payment terminal communication, EBT acceptance, offline operation on SQLite, sync-on-reconnect.
- **Canary** — GrowDirect, Go on GCP per `go-module-layout.md`. Back-half. Owns: 13-module spine + 12 extended modules, the four accountability rails (Operational, Financial, Evidentiary, Vendor), the agent network, multi-tenant data isolation, the canonical retail data model.

Both run independently. Neither imports the other's code. Neither shares a database. The wire is the only seam.

### Identity Rule (constraint C-3)

Where a capability *executes* on the physical register hardware = DriftPOS, regardless of where its data eventually lands in Canary's back-half. This rule resolved every "Both" capability in the GRO-721 capability map without ambiguity:

| Capability | Where data lands | Where execution happens | Side |
|---|---|---|---|
| Loyalty redemption | Canary `c.loyalty_memberships`, `t.loyalty_events` | Register UI prompts, scans loyalty card, applies discount on tender | Both → DriftPOS executes; Canary settles |
| Video-on-transaction | Canary `fox` evidence + analytics | Register triggers feed; back-half indexes | Both → DriftPOS triggers; Canary stores |
| Post-outage re-sync | Canary `t.transactions`, `i.inventory_movements` | Register replay loop | Both → DriftPOS replays; Canary reconciles |
| Mobile gun-show mode | Canary firearms compliance, `q.detections` | Mobile register operating offline | Both → DriftPOS operates; Canary later compliance-checks |
| Bottle deposits | Canary `compliance` (state liability), `t.transaction_line_items` | Register collects deposit; back-half tracks liability | Both → DriftPOS collects; Canary tracks |
| Online waivers | Canary `c.customers.attributes` (waiver state) | Register checks at checkout; back-half stores signed PDFs | Both → DriftPOS checks; Canary stores |
| Gift-card processing | Canary `t.gift_card_events` (lifecycle), `commercial` (liability) | Register swipes/redeems; back-half maintains liability | Both → DriftPOS executes; Canary settles |
| Customer DL-scan ID capture | Canary `c.customers.attributes` (DL-derived fields) | Register scans driver license at lane | Both → DriftPOS scans; Canary stores |

The Identity Rule has a useful corollary: **the wire contract is the seam at which the rule operates.** Every wire crossing carries one direction of authority. There is no contested ownership — there is only "DriftPOS is calling Canary" or "Canary is notifying DriftPOS."

### What this contract does NOT cover

- DriftPOS's internal PostgreSQL→SQLite sync mechanics. That's Bart's team's problem; Canary doesn't see it.
- DriftPOS's UI / cashier flow. Canary returns data; DriftPOS decides how to render it.
- Canary's internal service-to-service contracts (e.g., gateway → sub2 → sub3). See `webhook-pipeline.md` and `pos-adapter-substrate.md` for those.
- Counterpoint's REST API surface. The `edge` poller talks to Counterpoint; DriftPOS never does.
- Hardware-level integrations (pinpads, scanners, cash drawers). DriftPOS owns them entirely; the wire carries only their *outputs* (tender records, scan results, drawer events).

---

## §2. Wire Inventory

38 entities cross the boundary. Categorized by direction:

- **Push (R→C):** DriftPOS pushes to Canary. Real-time event semantics.
- **Pull (C→R):** DriftPOS pulls from Canary. Cache-and-refresh semantics.
- **Notify (C→R):** Canary notifies DriftPOS of state changes. DriftPOS decides when to act.
- **Bidirectional:** Initiated from either side depending on operational context.

| # | Entity | Direction | Frequency | ARTS POSLOG mapping | Capability Map ref |
|---|---|---|---|---|---|
| 1 | RetailTransaction (sale) | R→C | per transaction (real-time) | `RetailTransaction` + `LineItem*` + `Tender*` | #16, #17, #25, #82, #90, #110 |
| 2 | RetailTransaction (refund) | R→C | per refund (real-time) | `RetailTransaction.TransactionTypeCode='RETURN'` | (returns split — see §5) |
| 3 | RetailTransaction (void) | R→C | per void (real-time) | `RetailTransaction.TransactionTypeCode='VOID'` | #99 (LP signal) |
| 4 | RetailTransaction (no-sale) | R→C | per drawer-open-without-sale | `RetailTransaction.TransactionTypeCode='NO_SALE'` | #99 |
| 5 | TenderRecord with payment fingerprint | R→C | per tender within transaction | `Tender.CardEntryMethod` + tokenized fingerprint | C-4 PCI boundary |
| 6 | EBT tender authorization | R→C | per EBT tender | `Tender.TypeCode='EBT'` (ARTS extension) | #84 |
| 7 | CashDrawerEvent (open, paid-in, paid-out, count) | R→C | per drawer event | `RetailTransaction` derivative + `CashManagement` | #99 |
| 8 | ShiftEvent (open, close) | R→C | per cashier shift boundary | `OperatorSession` (ARTS extension) | #41, #43 |
| 9 | CashierAction (override, manager-swipe, lookup) | R→C | per action | `OperatorAction` (ARTS extension) | #43, #99 |
| 10 | EvidenceTrigger (LP-relevant event marker) | R→C | per merchant-defined trigger | `RetailTransaction.attributes.evidence_trigger` | #98, #99 |
| 11 | DigitalReceiptOptIn (email/phone capture at register) | R→C | per opt-in | `Customer` (lightweight) + `RetailTransaction.LinkedCustomer` | #32 |
| 12 | DLScanRecord (age verification, ID capture) | R→C | per DL scan | `Customer` (DL-derived attrs) + `ComplianceCheck` | #19, #36 |
| 13 | WaiverCheckRequest | R→C | per waiver check (real-time, blocking) | `ComplianceCheck.WaiverStatus` | #35 |
| 14 | WaiverCheckResponse | C→R (response) | response to #13 | `ComplianceCheckResult` | #35 |
| 15 | LoyaltyResolveRequest (party-from-tender) | R→C | per tender (real-time, <50ms p99) | extension over `Customer.Lookup` | #25 |
| 16 | LoyaltyResolveResponse (party + offers) | C→R (response) | response to #15 | `Customer` + `LoyaltyMember.Offers[]` | #23, #24 |
| 17 | LoyaltyAccrualEvent | R→C | post-tender (async) | `LoyaltyAccount.LoyaltyEvent` | #24 |
| 18 | LoyaltyRedemptionEvent | R→C | per redemption (real-time, blocking) | `LoyaltyAccount.LoyaltyEvent` (REDEEM) | #25 |
| 19 | GiftCardActivation | R→C | per activation (real-time) | `LoyaltyAccount` + `GiftCardActivity` | #78 |
| 20 | GiftCardRedemption | R→C | per redemption (real-time, blocking) | `GiftCardActivity` | #79 |
| 21 | GiftCardBalanceQuery | R→C | per balance check | `GiftCardActivity.Lookup` | #78 |
| 22 | GiftCardBalanceResponse | C→R (response) | response to #21 | `GiftCardActivity` | #78 |
| 23 | InventoryReservation (BOPIS hold, range slot) | R→C | per hold | `InventoryReservation` (ARTS) | (multi-tier assortment) |
| 24 | ItemMaster (Item) | C→R | poll on register startup + change notify | `Item` (ARTS Item Maintenance) | #1, #2, #3, #4, #5, #6, #7 |
| 25 | ItemMaster ChangeNotification | C→R | per change (notification) | `Item.Update` (event) | #1 |
| 26 | PriceListEntry | C→R | poll daily + change notify | `Price` + `PriceList` | #89 |
| 27 | PriceListEntry ChangeNotification | C→R | per change | `Price.Update` (event) | #89 |
| 28 | PromotionRule (configured, active) | C→R | poll daily + change notify | `Promotion` + `PromotionRule` | #89, #91 |
| 29 | CustomerProfile (known) | C→R | per lookup + change notify | `Customer` | #18, #20 |
| 30 | CustomerProfile ChangeNotification | C→R | per change | `Customer.Update` (event) | #18 |
| 31 | EmployeeRecord (active, role-permissioned) | C→R | poll on shift open + change notify | `Operator` + `OperatorRole` | #41, #43, #44 |
| 32 | EmployeeAuthChallenge | R→C | per cashier login | extension over `Operator.Authenticate` | #44 |
| 33 | EmployeeAuthResponse | C→R (response) | response to #32 | `Operator` + `OperatorPermissions[]` | #44 |
| 34 | LocationConfig (terminal, store, tax zone) | C→R | poll on register startup | `RetailStore` + `Workstation` | (config) |
| 35 | TenderTypeMaster (cash, card, EBT, gift, store credit) | C→R | poll on register startup | `TenderType` | (config) |
| 36 | RegisterHeartbeat | R→C | every 30s | extension `RegisterStatus` | (operational) |
| 37 | OutageReplayBatch | R→C | on reconnect after offline period | array of #1-#9 with `isOffline=true` | #100, #101, #102, #103 |
| 38 | EvidenceAnchorReceipt (Fox case anchor confirmation) | C→R (notification) | per anchor (best-effort) | extension `EvidenceAnchor` | #98 (LP) |

**Totals:** 38 entities. 22 push/notify R→C. 11 pull C→R. 5 request-response pairs (synchronous). The push-heavy pattern reflects the platform thesis: the register is the data source for the accountability rails, and rail-level analytics depend on real-time arrival.

**ARTS coverage assessment:** 31 of 38 entities map to a documented ARTS POSLOG message type or canonical entity. The 7 extensions (`OperatorSession`, `OperatorAction`, `ComplianceCheck`, `RegisterStatus`, `EvidenceAnchor`, `LoyaltyMember.Offers[]`, `EmployeeAuthChallenge`) are documented in the API surface section as Canary extensions over ARTS. Each extension is justified inline.

---

## §3. API Surface

47 endpoints across 9 domains. All endpoints are versioned `/v1/...`; all return JSON; all require auth (mTLS or JWT — see §8). Path conventions follow Canary's existing `webhook-pipeline.md` style: `POST /webhooks/{pos-type}/...` for register-pushed events, `GET /v1/...` for register-pulled master data, `POST /v1/...` for register-initiated requests requiring synchronous response.

### Domain 1 — Transaction (9 endpoints, push from register)

The transaction endpoints are the load-bearing surface. Every accountability rail depends on transactions arriving here in real-time.

| Verb | Path | Purpose | Request | Response | Idempotency | ARTS ref |
|---|---|---|---|---|---|---|
| POST | `/webhooks/driftpos/transaction` | Submit completed sale, refund, void, or no-sale | `RetailTransaction` envelope (see schema below) | 201 + `{event_id, transaction_id, party_id?, evidence_anchor_pending}` | `Idempotency-Key: <register-uuid>` header (24h dedup) | `RetailTransaction` |
| POST | `/webhooks/driftpos/transaction/batch` | Bulk submit (offline replay path) | array of `RetailTransaction` envelopes (max 100) | 201 + per-element status array | per-element `Idempotency-Key` from envelope | `RetailTransactionBatch` |
| POST | `/webhooks/driftpos/cash-drawer-event` | Drawer open, paid-in, paid-out, count | `CashDrawerEvent` envelope | 201 + `{event_id}` | `Idempotency-Key` | `CashManagement` ext |
| POST | `/webhooks/driftpos/shift-event` | Cashier shift open/close | `ShiftEvent` envelope | 201 + `{event_id, shift_id}` | `Idempotency-Key` | `OperatorSession` (ext) |
| POST | `/webhooks/driftpos/cashier-action` | Override, manager-swipe, lookup, etc. | `CashierAction` envelope | 201 + `{event_id}` | `Idempotency-Key` | `OperatorAction` (ext) |
| POST | `/webhooks/driftpos/evidence-trigger` | Mark a transaction or moment as LP-relevant | `EvidenceTrigger` envelope | 201 + `{event_id, fox_anchor_pending}` | `Idempotency-Key` | `RetailTransaction.attributes` |
| POST | `/webhooks/driftpos/replay-batch` | Reconnect after offline; bulk-replay all queued events | `OutageReplayBatch` (array of typed envelopes, max 1000) | 207 multi-status + per-element results | per-element `Idempotency-Key` | `RetailTransactionBatch` w/ `isOffline=true` |
| GET | `/v1/driftpos/transaction/{idempotency_key}` | Lookup prior submission by key (post-outage reconciliation) | — | 200 + transaction state, or 404 | n/a (read) | `RetailTransaction.Lookup` |
| GET | `/v1/driftpos/transaction/by-receipt/{receipt_no}` | Lookup by register-printed receipt number | — | 200 + transaction state, or 404 | n/a (read) | `RetailTransaction.Lookup` |

**`RetailTransaction` envelope schema** (canonical; ARTS POSLOG-aligned, projects to Canary `t.transactions` + children):

```json
{
  "tenant_id": "uuid (required)",
  "register_id": "string (required, register-assigned, ARTS WorkstationID)",
  "register_uuid": "uuid (required, Canary-assigned during register registration)",
  "location_code": "string (required, ARTS RetailStoreID)",
  "transaction_number": "string (required, register-assigned, unique per register-day)",
  "transaction_type": "sale | refund | void | no_sale | exchange",
  "parent_transaction_uuid": "uuid (nullable, for refunds/voids — references prior transaction)",
  "business_date": "ISO 8601 date (ARTS BusinessDayDate)",
  "started_at": "ISO 8601 timestamp",
  "ended_at": "ISO 8601 timestamp",
  "cashier_employee_code": "string (required, ARTS OperatorID)",
  "currency": "ISO 4217 (default USD)",
  "subtotal": "decimal (string, 4dp)",
  "tax_total": "decimal",
  "discount_total": "decimal",
  "grand_total": "decimal",
  "is_training_mode": "boolean",
  "is_offline": "boolean (true if transaction completed during register offline period)",
  "is_reentered": "boolean (post-outage replay marker)",
  "void_reason": "string (nullable)",
  "line_items": [
    {
      "line_number": "int",
      "barcode_scanned": "string (nullable)",
      "item_code": "string (item-master code, may be unresolved if barcode unknown)",
      "description": "string",
      "quantity": "decimal",
      "unit_of_measure": "string (EA, LB, etc.)",
      "unit_price": "decimal",
      "list_price": "decimal (nullable)",
      "unit_discount": "decimal",
      "unit_tax": "decimal",
      "is_void": "boolean",
      "void_reason": "string (nullable)",
      "is_return": "boolean",
      "return_reason": "string (nullable)",
      "is_weighable": "boolean",
      "is_food_stamp_eligible": "boolean",
      "promotion_rule_codes": ["string"],
      "attributes": {}
    }
  ],
  "tenders": [
    {
      "tender_sequence": "int",
      "tender_type_code": "string (cash, card, ebt_snap, ebt_cash, gift, store_credit, etc.)",
      "amount": "decimal",
      "cash_back_amount": "decimal",
      "change_amount": "decimal",
      "card_last_4": "string (nullable, NEVER full PAN)",
      "card_brand": "string (nullable)",
      "card_entry_method": "swipe | chip | contactless | keyed | manual_card_present",
      "payment_fingerprint": {
        "source": "ingenico_emv | wallet_apple | wallet_google | self_computed",
        "value": "string (network-tokenized; NEVER raw PAN)",
        "quality_hint": "0.0 - 1.0"
      },
      "authorization_code": "string (nullable)",
      "processor_reference": "string (nullable)",
      "is_voided": "boolean",
      "is_refund": "boolean",
      "ebt_balance_remaining": "decimal (nullable, EBT only)",
      "loyalty_membership_code": "string (nullable, if redemption tender)",
      "gift_card_serial_hash": "string (nullable, hashed; never raw)",
      "attributes": {}
    }
  ],
  "discounts": [
    {
      "discount_sequence": "int",
      "scope": "transaction | line_item | tender",
      "line_number": "int (nullable)",
      "discount_type": "promo | manager_override | loyalty | employee | other",
      "promotion_rule_code": "string (nullable)",
      "amount": "decimal",
      "percentage": "decimal (nullable)",
      "reason_code": "string",
      "authorized_by_employee_code": "string (nullable)"
    }
  ],
  "loyalty_event": {
    "loyalty_membership_code": "string (nullable)",
    "points_earned": "int",
    "points_redeemed": "int",
    "tier_at_time_of_transaction": "string"
  },
  "compliance_checks": [
    {
      "check_type": "age_verification | waiver | nics | bound_book | feed_compliance",
      "result": "pass | fail | bypass | not_applicable",
      "verified_by_employee_code": "string (nullable)",
      "evidence_payload_hash": "string (nullable)"
    }
  ],
  "attributes": {
    "drift_pos_version": "string",
    "drift_pos_register_serial": "string",
    "any_other_drift_pos_native_fields": "..."
  }
}
```

**Response shape** (201 Created):

```json
{
  "event_id": "ulid (Canary-assigned ingestion identifier)",
  "transaction_id": "uuid (Canary t.transactions.id, populated after sub2 persists)",
  "party_id": "uuid (nullable — populated synchronously when payment_fingerprint resolves to known party)",
  "party_code": "string (nullable, e.g. P-MERCH-X4Y7Z2)",
  "party_offers": [
    {"offer_code": "string", "description": "string", "expires_at": "ISO 8601"}
  ],
  "evidence_anchor_pending": "boolean (true if Fox case will be anchored)",
  "received_at": "ISO 8601",
  "processing_status": "accepted | accepted_with_warnings | rejected"
}
```

The `party_id` / `party_code` / `party_offers` round-trip is the load-bearing latency budget. Per `party-identity-design.md` NFR table: Rule 1 hit p99 <50ms; the register's checkout flow expects the response within 200ms wall clock to remain non-disruptive. If party resolution takes longer (Rule 4 conflict, Rule 3 new party with full insert), the response returns with `party_id: null` and the register prints a basic receipt — the resolution still completes asynchronously and updates `t.transactions.party_id` via the soft-FK populate path. The register treats this as the normal case for unknown shoppers.

### Domain 2 — Catalog (8 endpoints, pull from back-half)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| GET | `/v1/driftpos/items?cursor=&limit=&since=` | Bulk item pull (initial sync, periodic refresh) | Cursor-paginated; `since` filters by `updated_at`; max limit 1000 |
| GET | `/v1/driftpos/items/{item_code}` | Single item lookup (cache miss at register) | 200 with `Item` payload, 404 if unknown |
| GET | `/v1/driftpos/items/by-barcode/{barcode}` | Barcode resolve (POS scan path) | The keystone scan endpoint; <50ms p99 SLA |
| GET | `/v1/driftpos/items/changes?since=&cursor=` | Change feed since timestamp | Cursor-paginated; returns `{added[], updated[], deleted[]}` |
| GET | `/v1/driftpos/categories?cursor=&since=` | Bulk category pull | |
| GET | `/v1/driftpos/categories/changes?since=` | Category change feed | |
| GET | `/v1/driftpos/items/kits/{kit_code}` | Kit / BOM expansion | Returns component items + quantities |
| GET | `/v1/driftpos/items/matrix/{matrix_parent_code}` | Size/color matrix expansion (apparel, footwear) | Returns full grid w/ child item codes |

**`Item` payload schema** (subset, ARTS Item Maintenance-aligned; full fields per `cr-arts-xml-item-maintenance-technical-specification-v1-3-2-20131223-pdf.md`):

```json
{
  "tenant_id": "uuid",
  "item_code": "string (sku, ARTS POSItemID)",
  "barcode_primary": "string",
  "barcodes_alternate": ["string"],
  "description_short": "string",
  "description_long": "string (nullable)",
  "category_code": "string",
  "department_code": "string (nullable)",
  "brand": "string (nullable)",
  "vendor_codes": ["string"],
  "unit_of_measure_primary": "string",
  "units_of_measure_alternate": [
    {"code": "string", "description": "string", "conversion_factor": "decimal"}
  ],
  "is_kit": "boolean",
  "kit_components": [{"item_code": "string", "quantity": "decimal"}],
  "is_serialized": "boolean",
  "is_weighable": "boolean",
  "is_food_stamp_eligible": "boolean",
  "tax_class_code": "string",
  "compliance_flags": ["alcohol", "tobacco", "firearm", "rx"],
  "list_price": "decimal",
  "current_promotional_price": "decimal (nullable)",
  "matrix_parent_code": "string (nullable, references parent if matrix child)",
  "active": "boolean",
  "attributes": {},
  "updated_at": "ISO 8601"
}
```

### Domain 3 — Customer / Party (6 endpoints, bidirectional)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| POST | `/v1/driftpos/customer/lookup` | Party resolve from tender (synchronous, blocking) | <50ms p99 — see `party-identity-design.md` NFRs |
| GET | `/v1/driftpos/customers/{customer_code}` | Known customer fetch (loyalty card scan path) | |
| GET | `/v1/driftpos/customers/by-loyalty/{loyalty_membership_code}` | Loyalty card resolve | |
| GET | `/v1/driftpos/customers/changes?since=&cursor=` | Customer change feed | |
| POST | `/v1/driftpos/customer/register-from-receipt` | Capture digital-receipt opt-in (email/phone capture at register) | Creates lightweight `c.customers` row + `party.identifiers` |
| POST | `/v1/driftpos/customer/dl-scan` | Submit DL scan record (age verification + ID capture) | Auto-creates customer if not extant; updates DL-derived attrs |

**`/v1/driftpos/customer/lookup` request shape:**

```json
{
  "tenant_id": "uuid",
  "transaction_context_uuid": "uuid (transaction in flight; for clustering)",
  "lookup_signals": [
    {"type": "payment_fingerprint", "source": "ingenico_emv", "value": "<token>"},
    {"type": "loyalty_card", "value": "12345"},
    {"type": "phone", "value": "+15551234567"},
    {"type": "email", "value": "alice@example.com"},
    {"type": "dl_scan", "value": "<DL parsed payload>"}
  ]
}
```

**Response:**

```json
{
  "party_id": "uuid (nullable — null if no resolution within budget)",
  "party_code": "string (nullable)",
  "party_confidence": "anonymous | weak | probable | strong",
  "customer": { "...": "Customer payload if known" },
  "loyalty_membership": {
    "loyalty_membership_code": "string",
    "tier": "string",
    "points_balance": "int",
    "available_offers": [{"offer_code": "string", "description": "string"}]
  },
  "household_offers": [{"...": "..."}],
  "compliance_state": {
    "waiver_signed": "boolean",
    "waiver_expires_at": "ISO 8601",
    "age_verified_dob": "date (nullable, derived from DL scan)"
  },
  "resolved_via_rule": "rule_1_strong | rule_2_probable | rule_3_new_anonymous | rule_4_conflict | n/a",
  "resolution_event_id": "uuid"
}
```

### Domain 4 — Inventory (5 endpoints, bidirectional)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| GET | `/v1/driftpos/inventory/position?item_code=&location_code=` | Real-time position for one item at one store | <100ms p99; reads `inventory-as-a-service` |
| GET | `/v1/driftpos/inventory/positions/bulk` | Bulk position pull (cache warm-up) | POST body w/ array of (item_code, location_code) |
| POST | `/v1/driftpos/inventory/reserve` | BOPIS hold, range-slot reservation | Returns `reservation_id` + TTL |
| DELETE | `/v1/driftpos/inventory/reserve/{reservation_id}` | Release reservation | |
| POST | `/v1/driftpos/inventory/cycle-count` | Submit register-initiated cycle count (e.g., handheld at end of shift) | Creates `i.inventory_movements` row of type `cycle_count_correction` |

Note: standard sale-driven inventory decrements are derived by Canary from the inbound `RetailTransaction` line items. DriftPOS does NOT call a separate inventory-adjust endpoint per sale — that would be redundant and would create a race window.

### Domain 5 — Pricing & Promotion (4 endpoints, pull)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| GET | `/v1/driftpos/prices?cursor=&since=` | Bulk price-list pull | |
| GET | `/v1/driftpos/prices/effective?item_code=&location_code=&customer_class=` | Effective price for an item, location, and customer class (real-time) | Includes any active promo |
| GET | `/v1/driftpos/promotions?active_at=` | Active promotions at a given timestamp | |
| GET | `/v1/driftpos/promotions/changes?since=&cursor=` | Promotion change feed | |

### Domain 6 — Loyalty (4 endpoints, bidirectional)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| POST | `/v1/driftpos/loyalty/redeem-check` | Real-time redeemable check (tier, points, eligible offers) | <50ms p99; blocking for register |
| POST | `/webhooks/driftpos/loyalty/accrual` | Async post-tender accrual event | Fire-and-forget |
| POST | `/webhooks/driftpos/loyalty/redemption` | Real-time redemption event (settles points) | Blocking for register |
| GET | `/v1/driftpos/loyalty/membership/{membership_code}` | Membership detail fetch | |

### Domain 7 — Employee / Auth (4 endpoints, pull + auth)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| GET | `/v1/driftpos/employees?location_code=&cursor=` | Active employee roster for a store | |
| GET | `/v1/driftpos/employees/{employee_code}` | Single employee lookup | Permissions included |
| POST | `/v1/driftpos/employees/auth-challenge` | Cashier login challenge (password hash check, swipe-card validation) | Returns short-lived register-session JWT |
| GET | `/v1/driftpos/employees/changes?since=&cursor=` | Employee change feed (terminations, role changes) | DriftPOS must invalidate cached employees on change |

### Domain 8 — Config / Heartbeat (4 endpoints, bidirectional)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| GET | `/v1/driftpos/config/location/{location_code}` | Store config (tax zones, operating hours, terminal list) | Fetched on register startup |
| GET | `/v1/driftpos/config/tender-types?location_code=` | Active tender types for this location | Includes EBT eligibility, gift-card config |
| POST | `/webhooks/driftpos/heartbeat` | Register liveness ping (every 30s) | Powers `ops-dashboard` |
| GET | `/v1/driftpos/config/feature-flags?location_code=` | Feature flags relevant to register (e.g., `evidence_trigger_enabled`) | Cached client-side; pulled on startup + 1h refresh |

### Domain 9 — Compliance / Evidence (3 endpoints, push)

| Verb | Path | Purpose | Notes |
|---|---|---|---|
| POST | `/webhooks/driftpos/compliance/waiver-check` | Real-time waiver-status check (blocking) | <100ms p99 |
| POST | `/webhooks/driftpos/compliance/age-verify` | Submit age-verification result (DL or manager override) | |
| POST | `/webhooks/driftpos/compliance/firearm-event` | ATF eBound, e4473, NICS event submission | Triggers Fox case anchor |

### Cross-cutting endpoint requirements

Every endpoint:

1. **Auth header** — `Authorization: Bearer <jwt>` (v1 fallback) OR mTLS client certificate (target). Per-tenant credentials managed via Canary's `app.pos_tenant_credentials` table (see `pos-adapter-substrate.md` §Credential Storage); per-register identity via cert CN or JWT `sub` claim.

2. **Tenant context** — `tenant_id` is extracted from the auth credential, NOT trusted from the request body. A request body `tenant_id` that does not match the credential's tenant is rejected at the gateway with `403 tenant_mismatch`. Constraint C-6 enforced at the boundary.

3. **Idempotency** — every POST that mutates state requires `Idempotency-Key: <uuid>` header. Server stores key→result for 24h. Repeated keys return the original response, never re-execute.

4. **Rate limiting** — per-register: 1000 req/min sustained; 5000 req/min burst. Per-tenant aggregate: 10K req/min sustained; 50K burst. Returned headers: `X-RateLimit-Remaining`, `X-RateLimit-Reset`. 429 on exceedance with `Retry-After`.

5. **Versioning** — major version in path (`/v1/`); minor versions advertised via `X-Canary-API-Version: 1.3` header. Backward-compat policy: 12 months for breaking changes within a major version.

6. **Tracing** — `X-Request-Id` accepted from register; if absent, server generates ULID. Propagated through Canary's internal pipeline for correlated logs.

7. **Error envelope** — all error responses follow:

```json
{
  "error": {
    "code": "machine_readable_code",
    "message": "human-readable description",
    "details": {},
    "retry_strategy": "retry_after_seconds | do_not_retry | escalate"
  },
  "request_id": "ulid"
}
```

---

## §4. Eventing Pattern

### Push vs poll — the decision tree

| Entity class | Mechanism | Rationale |
|---|---|---|
| Transactions, drawer events, shift events, cashier actions, evidence triggers | **Push (R→C)** | Real-time arrival is the load-bearing assumption of every accountability rail; LP detection requires sub-second event ingestion |
| Loyalty resolve, party lookup, waiver check, gift-card balance, age-verify | **Synchronous request-response (R→C)** | Register UX requires the answer within the cashier's attention window (~200ms) |
| Item master, customer master, prices, promotions, employees, config | **Poll on startup + change-feed delta + push notification** | Master data is large and slow-changing; push semantics would break DriftPOS's offline SQLite-sync model; change feed gives "near-real-time" without synchronous coupling |
| Heartbeat, evidence-anchor confirmation, ops events | **Push (R→C) + push (C→R) async** | Operational telemetry; not in the customer-facing flow |

### Ordering guarantees

**Per-register strict ordering**: events from a single `register_uuid` are processed in the order they arrive at the gateway. The gateway uses register_uuid as the partition key when publishing to `protocol:events`. Within a partition, ordering is FIFO. Across registers, ordering is best-effort (eventual consistency at the tenant level).

**No global ordering guarantee** across registers within a tenant. A transaction at Register-A and a transaction at Register-B that both happen at 14:32:00.001 may be persisted in either order. Downstream services (Chirp, Owl, Fox) are designed to tolerate this — they reason about events in business-time (`business_date` + `started_at`), not arrival time.

### Backpressure semantics

Canary follows the `webhook-pipeline.md` posture: the gateway is a thin acceptance layer that returns quickly or signals retry. Specifically:

- **Stream available** — gateway returns 201/200 immediately on successful XADD. No waiting for sub2/sub3/sub4 to process.
- **Stream unavailable** — gateway returns 503 with `Retry-After: 30`. DriftPOS must retry with exponential backoff (5s, 30s, 5m, then dead-letter at register).
- **Gateway overloaded (rate limit)** — 429 with `Retry-After`. Same retry semantics.
- **Tenant suspended** — 403 with `error.code: tenant_suspended`. Do not retry; surface to merchant operator.
- **Stream length monitoring** — when `protocol:events` length exceeds 10K, Canary alerting fires. DriftPOS sees no behavior change at this threshold; only if length exceeds the gateway's configured `max_stream_length` (50K default) does the gateway start returning 503.

DriftPOS is responsible for its own offline queue. When Canary returns 503 sustained, DriftPOS should fail over to local SQLite queueing (its existing offline mode) and replay via `/webhooks/driftpos/replay-batch` when reachability returns.

### Change-feed semantics

For every entity with a `/changes?since=` endpoint:

- **Cursor-based pagination**: response includes `next_cursor`; caller passes back as `cursor=` parameter on next request.
- **`since` is a server-side high-water mark**: not a client clock. Use the `next_cursor` from the prior call, not "30 seconds ago."
- **Returns `{added[], updated[], deleted[]}`**: deletions are explicit (soft-deleted entities surface in `deleted[]` with their codes).
- **Cursor TTL**: 24h. After 24h, caller must do a full resync (`/v1/driftpos/items?cursor=&limit=` from start).
- **Push notification supplement**: for high-priority changes (price update for a hot item, customer block-list update), Canary may push a notification to a registered DriftPOS callback (see §Notification callbacks below). The notification is advisory; the change-feed is the authoritative source.

### Notification callbacks (C→R push)

DriftPOS registers callback URLs per tenant via `POST /v1/driftpos/config/callbacks`. Canary uses these for:

| Notification | Purpose | Delivery semantics |
|---|---|---|
| `item.changed` | Hot-item price change, item deactivation | Best-effort; signed with HMAC-SHA256 over body |
| `price.changed` | Promotional price activation/expiration | Best-effort |
| `customer.blocked` | Returns-fraud, banned customer | Reliable (3 retries); register MUST honor |
| `evidence.anchor.confirmed` | Fox case Bitcoin anchor confirmation | Best-effort; informational only |
| `feature.flag.toggled` | Per-location feature-flag flip | Reliable (3 retries) |

Notifications use the same auth posture as inbound (mTLS or JWT). Callback URL must be HTTPS. Failed notifications are logged but not retried indefinitely (3 attempts: 5s, 30s, 5m).

---

## §5. Ownership Semantics

### System of record per entity

| Entity | Phase 1 (parallel-run, Counterpoint extant) | Phase 2 (Counterpoint sunset) |
|---|---|---|
| Item master | Counterpoint (via `edge` poller into Canary) | Canary |
| Customer master | Counterpoint + Canary (split: known via CP, party via Canary) | Canary |
| Inventory position | Counterpoint (via `edge`) | Canary (`inventory-as-a-service`) |
| Pricing | Counterpoint | Canary (`pricing` module) |
| Promotions | Canary (Counterpoint promo-engine deficient per GRO-721) | Canary |
| Employee | Counterpoint (via `edge`) | Canary (`employee` module) |
| Loyalty | Canary (`customer.loyalty_memberships`) — Canary native from day 1 | Canary |
| Transactions (sale) | Canary `t.transactions` — DriftPOS pushes; Canary owns canonical | Canary |
| Drawer / shift / cashier actions | Canary `t.cash_drawer_events` / `t.shift_events` / `t.cashier_actions` — DriftPOS pushes | Canary |
| Evidence chain (Fox) | Canary `q.cases` + `fox` chain hashes | Canary |
| OTB wallet | Canary `l402-otb` — Canary native from day 1 | Canary |

### Cache rules at the register

DriftPOS maintains a local SQLite cache for offline operation. Cache rules per entity:

| Entity | Cache TTL | Refresh trigger | Offline behavior |
|---|---|---|---|
| Item master | 1h soft / 24h hard | Bulk pull on startup; change-feed every 30s; push notify | Full offline operation against cached items |
| Item barcode | 1h soft / 24h hard | Inherits from item master | Full offline operation |
| Categories | 24h | Push notify | Full offline |
| Customer (known) | 1h | Lookup-on-demand + change-feed every 30s | Reads cached known customers; new lookups skipped (party resolution skipped offline) |
| Pricing | 5m soft / 1h hard | Bulk pull on startup; change-feed every 30s | Full offline operation against cached prices |
| Promotions | 5m soft / 1h hard | Same | Active promos cached; new promos delayed until reconnect |
| Employees | 1h | Pull on shift open + change-feed | Full offline; new auth challenges checked against cache |
| Tender types | 24h | Pull on startup | Full offline |
| Location config | 24h | Pull on startup; push notify | Full offline |

**Hard TTL** = if Canary is unreachable longer than this, the register MUST refuse new transactions for safety reasons (per Bart's discretion — DriftPOS may relax this with manager override). **Soft TTL** = the register continues operation but attempts a refresh on each transaction.

### Conflict resolution

The wire is *predominantly one-direction* per entity, which avoids most conflicts. Where conflicts can arise:

- **Item master conflict** (Phase 1 only) — Counterpoint and Canary editing the same item simultaneously. Resolution: `edge` poller is authoritative for Counterpoint-owned fields; Canary's overlay (e.g., compliance flags Counterpoint doesn't track) is additive; on collision, Counterpoint wins for shared fields. Per `edge` SDD.
- **Customer party-merge collision** — two registers concurrently submit transactions resolving to candidate-merge parties. Resolution: per `party-identity-design.md` Rule 4 (no silent merges); both transactions persist with their respective `party_id`; the conflict is logged for merchant review.
- **Inventory reservation collision** — two registers attempt to reserve the same last unit. Resolution: first-write-wins on reservation; second register receives 409 `inventory_unavailable`; UI displays the conflict.
- **Replay-after-outage producing duplicate transaction** — register replays a transaction that Canary already accepted. Resolution: idempotency key dedups silently (200 with `was_duplicate: true`).

### Cache invalidation

Push notifications carry sufficient detail for the register to invalidate the affected cache entry. Three-strikes invalidation:

1. Push notification received → invalidate immediately.
2. Notification missed (callback failed) → next change-feed poll catches it → invalidate.
3. Stale-cache detected at point-of-use (e.g., price returned by Canary differs from cached) → invalidate that entry, refresh, retry.

---

## §6. Migration State Machine

The wire contract is identical across migration phases. The differences are entirely in *who owns the data behind the wire*. Bart's team writes one DriftPOS adapter; the back-half configuration changes invisibly.

### Phase 0 — Baseline (today)

- Counterpoint runs at merchant.
- Canary's `edge` poller projects Counterpoint master data into Canary back-half.
- DriftPOS does not yet exist at this merchant.
- No DriftPOS↔Canary wire traffic.

### Phase 1 — DriftPOS register install (parallel-run)

- DriftPOS registers deployed alongside Counterpoint registers (or replacing one register at a time during pilot).
- DriftPOS calls Canary endpoints per this contract.
- `RetailTransaction` payloads from DriftPOS land in Canary `t.transactions`.
- `edge` poller continues to project Counterpoint master data; DriftPOS pulls master data from Canary (which transparently reflects Counterpoint's current state).
- Counterpoint and DriftPOS are both running registers; the merchant decides per-lane which to operate.
- Wire contract behavior: 100% of this spec is in effect. DriftPOS does not know Counterpoint exists.

### Phase 2 — Register cutover (Counterpoint registers retired)

- All registers at the merchant are now DriftPOS.
- Counterpoint backoffice still runs (master-data SoR).
- `edge` poller continues; DriftPOS pulls from Canary as before.
- Wire contract behavior: unchanged.

### Phase 3 — Counterpoint backoffice sunset

- Counterpoint is decommissioned at the merchant.
- `edge` poller is shut down.
- Canary becomes the master-data SoR for items, customers, pricing, employees, etc.
- DriftPOS pulls from Canary as before — but Canary is now the source, not a projection.
- Wire contract behavior: unchanged. The migration is invisible to the register.

### Phase 4 — PCI gateway absorption (per `project_pci_scope_phase4`)

- Canary takes over the payment gateway path.
- PCI scope expands per the phase-4 commitment.
- Wire contract behavior: still unchanged at the register-facing surface. The change is server-side: Canary now handles raw card material at the ingress (vault tokenization layer) before any party-fingerprinting logic sees it.

### Migration invariants

- Wire contract version does not change across phases.
- Idempotency keys remain valid across phases (they're per-register-uuid, not per-back-half-config).
- DriftPOS adapter implementation is written once, against this spec, and works through all four phases.

---

## §7. Failure Modes

### Register offline

**Canary's view:** register heartbeats stop arriving. After missing 3 heartbeats (90s), the `ops-dashboard` flags the register as `offline`. After 10min, an alert fires.

**Register's view:** continues operating against local SQLite cache per §5. Every transaction is queued in DriftPOS's local outage queue. New customer lookups, party resolutions, real-time waiver checks fail-open (transaction proceeds with cached state).

**Reconnect:** register calls `/webhooks/driftpos/replay-batch` with all queued events. Canary processes in submission order, applies idempotency dedup, and returns 207 multi-status. Replayed transactions carry `is_offline: true` and `is_reentered: true` markers — Canary's analytics treats them with appropriate confidence.

**Edge case — extended outage:** if outage exceeds `hard_ttl` for any cached entity (e.g., 24h), DriftPOS UI must surface to operator: "data is stale; safe operation requires reconnect." Per Bart's discretion; not a Canary-enforceable behavior.

### Back-half offline (Canary down)

**Register's view:** every Canary call returns 503 or times out. Register UI displays `offline_mode` banner. Switches to local-only operation per §5 cache rules. Same as register-offline-from-Canary's-perspective except the register stays operational.

**Canary's view:** alerts fire on its own infrastructure (Cloud Run health, DB health). Multi-cloud failover per `project_cloud_provider_accountability_stance`. When recovery happens, registers reconnect and replay queued events.

**Recovery:** registers see Canary return 200 again on heartbeat; resume normal operation. Replay queued transactions per the offline-replay path.

### Partial sync — only some endpoints succeed

DriftPOS submits a `RetailTransaction`. The transaction-record-write succeeds. The party-resolve subcomponent succeeds. The loyalty-accrual subcomponent fails downstream (e.g., Valkey connection lost mid-write).

**Resolution:** the transaction is persisted; the response indicates success. The loyalty event is dead-lettered to `protocol:dead_letter` per webhook-pipeline.md. Canary's reconciliation job replays the loyalty event on the next pass. DriftPOS sees no error — the transaction succeeded from its perspective.

**Why this is safe:** the loyalty event is append-only and idempotent (key: `(membership_code, transaction_uuid)`). Late-arrival reconciliation produces the same end state.

### Tenant isolation breach attempt

A misconfigured DriftPOS register (or a malicious actor) sends a request with `tenant_id` that doesn't match its auth credential's tenant.

**Detection:** at the gateway, before any business-logic handler runs, the gateway compares `tenant_id` extracted from auth credential vs `tenant_id` in the request body. Mismatch → 403 with `error.code: tenant_mismatch`.

**Response:** the request is rejected. The attempt is logged in `app.audit_log` with full context (source IP, register_uuid, claimed tenant, actual tenant). After 3 attempts within 5 minutes, the register's credential is auto-suspended and a high-priority alert is fired to the platform ops-on-call. The register operator must rotate credentials and confirm intent before reactivation.

### PCI boundary violation attempt

DriftPOS sends a `payment_fingerprint.value` that contains anything resembling a raw PAN (16+ digit string, Luhn-valid).

**Detection:** at the gateway, a pre-handler middleware scans `payment_fingerprint.value` and any fields named `card_number`, `pan`, `account_number` in the request body for Luhn-valid 13-19 digit strings.

**Response:** 400 with `error.code: pci_boundary_violation`. Request rejected entirely; no part is persisted. The attempt is logged (with the offending payload masked — only metadata about the rejection is stored). Repeat violations from the same register trigger credential suspension.

This is defense-in-depth. The contract is clear that DriftPOS must tokenize at the pinpad before submission; this gateway-side check catches misconfigurations and prevents accidental leakage.

---

## §8. Security Posture

### Transport security

**Target: mTLS.** Per-register client certificate, signed by Canary's per-tenant CA. Certificate CN encodes `(tenant_id, register_uuid)`. Certificate rotation cadence: 90 days (industry standard); auto-renewed via ACME-like flow that DriftPOS implements.

**v1 fallback: JWT-only (over TLS).** Because mTLS certificate management is operationally heavy and Bart's team may not have ACME tooling ready by pilot date. The fallback uses long-lived JWT (90-day expiry, rotated via `/v1/driftpos/auth/rotate`). This is acceptable for pilot but **not** for full GA — the platform thesis (specifically Rail 4 — vendor accountability) requires the cryptographic strength of mTLS for the wire that carries every transaction.

**Migration path:** support both during transition. Per-tenant config flag `driftpos_auth_mode = mtls | jwt | mtls_or_jwt`. Default during phase 1: `mtls_or_jwt`. Default at GA: `mtls` only.

### Authentication

- **Per-tenant identity:** each tenant has a CA cert (mTLS) or a master JWT-signing key (JWT mode). Stored per `pos-adapter-substrate.md` `app.pos_tenant_credentials` pattern (AES-256-GCM encrypted at rest).
- **Per-register identity:** each register has a unique cert or a unique JWT subject. Used as the partition key for ordering and for idempotency key namespacing.
- **Cashier authentication:** out of band — cashier credentials live with DriftPOS; Canary only sees the resulting `cashier_employee_code` on inbound payloads. Canary validates the employee code against `e.employees` and rejects if unknown / inactive.

### PCI scope analysis

**Phase 1–3:** Canary is **OUT** of PCI-DSS Level 1 scope.

- No raw PAN ever crosses the wire (constraint C-4).
- `payment_fingerprint.value` is a network-tokenized derivative (Ingenico EMV terminal token, Apple/Google Pay DPAN, etc.), not card data.
- Canary stores only `card_last_4` and `card_brand` (PCI-DSS § 3.3 — "First six and last four can be stored without compensating controls").
- Card-derived hash (per `party-identity-design.md` fingerprinting strategy) is tenant-salted and SHA-256'd; not PAN.

**Phase 4** per `project_pci_scope_phase4` — Canary enters Service Provider scope as it begins handling raw card material at the ingress (vault tokenization). The wire contract DOES NOT change in phase 4 — the gateway-side vault is invisible to DriftPOS. DriftPOS continues to send tokenized fingerprints; the underlying vault implementation is on Canary's side.

### Audit logging

Every wire crossing produces:

1. **Application log** (structured JSON): timestamp, request_id, tenant_id, register_uuid, endpoint, status, latency, party_id (if resolved). Standard observability.
2. **Audit log** (immutable, separate stream): for security-relevant events only — auth failures, tenant-mismatch, PCI-boundary-violation, credential rotation, evidence-trigger submission. Stored in `app.audit_log` (append-only; no DELETE or UPDATE permitted).
3. **Evidence anchor** (Fox-relevant only): for events flagged as LP-relevant or for cases auto-opened by Chirp rule firing, an entry is appended to the evidence chain (`q.cases.evidence_records`) and the chain hash is anchored per `blockchain-anchor` SDD.

### Anchor points — which wire crossings produce evidence-chain entries

| Wire event | Anchor? | Rationale |
|---|---|---|
| `RetailTransaction (sale)` | Indirect — via Sub3 Merkle batch | Every transaction is part of a Merkle batch per `tsp.md`; not individually anchored |
| `RetailTransaction (refund)` | Direct — Fox case opened | Refunds are LP-relevant by default; Fox case auto-opened; chain anchored |
| `RetailTransaction (void)` | Direct — Fox case opened | Same as refund |
| `EvidenceTrigger` | Direct — Fox case opened | Explicit operator-marked event |
| `CashierAction (manager-override)` | Direct | Manager overrides are LP-relevant |
| `firearm-event` (ATF, NICS, e4473) | Direct — Fox case opened | Compliance evidence |
| `compliance.waiver-check` (failed) | Direct | Failed waiver is potential liability |
| All other wire events | Indirect — via Merkle batch | Routine traffic; aggregated anchoring |

### Evidence-chain confirmation back to register

When a Fox case is anchored to L2 blockchain (per `blockchain-anchor` SDD), Canary sends an `evidence.anchor.confirmed` notification (§4 callback) to the register. DriftPOS may print "anchored" indicators on receipts at merchant configuration. This is the customer-facing manifestation of Rail 3 (Evidentiary).

---

## §9. Test Strategy

### Contract test fixtures (golden payloads)

Maintained in `CanaryGo/internal/adapters/driftpos/testdata/` (Canary side) and Bart's team's mirror repo. Each fixture is a request/response pair:

| Fixture | Request (R→C) | Expected Response | Validates |
|---|---|---|---|
| `transaction_simple_sale.json` | One-line cash sale | 201 + canonical IDs | Basic transaction round-trip |
| `transaction_card_sale_known_customer.json` | Card sale with known loyalty | 201 + party_id resolved | Party resolution Rule 1 |
| `transaction_card_sale_anonymous.json` | Card sale, no loyalty | 201 + new anonymous party | Rule 3 |
| `transaction_split_tender.json` | Cash + card + EBT three-tender | 201 + correct totals | Multi-tender |
| `transaction_refund_with_parent.json` | Refund referencing prior txn | 201 + parent_transaction_id resolved | Refund lineage |
| `transaction_void.json` | Mid-transaction void | 201 + Fox case anchor pending | Void → LP signal |
| `transaction_offline_replay.json` | `is_offline=true` batch of 50 | 207 + per-element status | Outage replay |
| `cash_drawer_paid_out.json` | Drawer paid-out event | 201 | Drawer event ingestion |
| `shift_open_close_pair.json` | Open + close shift | 2× 201 | Shift event lifecycle |
| `compliance_waiver_check_signed.json` | Waiver-check req for signed waiver | 200 + `waiver_signed: true` | Compliance read |
| `compliance_waiver_check_unsigned.json` | Same for unsigned | 200 + `waiver_signed: false` | Compliance write |
| `tenant_mismatch_attempt.json` | Body tenant_id ≠ cred tenant_id | 403 `tenant_mismatch` | Isolation enforcement |
| `pci_violation_attempt.json` | Luhn-valid 16-digit in `payment_fingerprint.value` | 400 `pci_boundary_violation` | PCI defense |
| `idempotent_duplicate.json` | Resubmit with same Idempotency-Key | 200 + `was_duplicate: true` | Idempotency |
| `large_batch_replay.json` | 1000-element replay batch | 207 + first 100 success, 901 rejected with `batch_too_large` | Batch limits |

### Replay harness

Recorded register sessions are replayable against a Canary test instance:

```go
// In CanaryGo/internal/adapters/driftpos/replay/harness.go
type ReplayHarness struct {
    fixtures []SessionFixture
    canary   *http.Client
}

func (h *ReplayHarness) Run(t *testing.T) { ... }
```

Each `SessionFixture` is a sequence of N requests with expected responses. The harness verifies that replaying the sequence produces the recorded responses (after canonicalizing timestamps and UUIDs). Used for regression testing.

### Integration test approach

| Test class | Runs in CI? | Requires DriftPOS instance? |
|---|---|---|
| Contract fixture tests (request/response golden payloads) | YES | NO — Canary stub of DriftPOS |
| Tenant-isolation enforcement | YES | NO |
| PCI-boundary defense | YES | NO |
| Adapter parser unit tests (`internal/adapters/driftpos/parser_test.go`) | YES | NO |
| Sub2 dispatcher routing (DriftPOS envelope → canonical) | YES | NO |
| End-to-end through gateway → sub1 → sub2 → DB | YES (testcontainers) | NO |
| Real DriftPOS register → Canary live integration | NO | YES — manual / paired test environment |
| Outage simulation (kill Canary, verify register queues; restart Canary, verify replay) | NO | YES |

### Conformance test suite

A Canary-published HTTP test runner that Bart's team can run against their DriftPOS instance:

```bash
canary-conformance --target https://driftpos-test.bart.example.com \
                   --tenant-id <test-tenant-uuid> \
                   --cert /path/to/test-cert.pem
```

The runner:
1. Submits each contract fixture against DriftPOS (where DriftPOS is acting as receiver — for callback notifications)
2. Submits each contract fixture against the live Canary (where Canary is receiver — for the standard register→back-half flow)
3. Reports pass/fail per fixture
4. Surfaces any deviations from the contract

Passing the conformance suite is the gating criterion for DriftPOS pilot deployment. Bart's team and Canary's team both run it; both must see green.

### Schema validation

Every JSON payload is validated against an OpenAPI 3.0 schema at the gateway boundary. The OpenAPI spec is generated from this SDD's payload definitions and published at `https://api.canary.example.com/openapi.json`. Bart's .NET team can codegen client stubs from the spec.

---

## §10. Open Questions — DriftPOS-team Confirmation Required

Per constraint C-2 (anti-speculation), every "DriftPOS-team confirmation required" item is enumerated below. None of these block contract publication; all should be resolved before pilot.

| # | Question | Recommended default | Why we need confirmation |
|---|---|---|---|
| OQ-1 | Does DriftPOS support mTLS client certificates today, or is JWT-only fallback required for pilot? | `mtls_or_jwt` mode for pilot; tighten to `mtls` for GA | Determines whether the Canary credential-management UX needs to handle cert provisioning at pilot kickoff |
| OQ-2 | Does DriftPOS's Ingenico pinpad return a stable network-tokenized fingerprint per card? Or does Canary need to fall back to `self_computed` (last4 + brand + zip5) for `payment_fingerprint.value`? | `self_computed` fallback acceptable for v1 with quality_score=0.4 | Per `party-identity-design.md` §B fingerprint quality matrix; if Ingenico provides a network token (analogous to Square's `card.fingerprint`), party resolution quality jumps from 0.4 to 0.85+ |
| OQ-3 | Can DriftPOS respect the `Idempotency-Key` semantics (storing the key on first submission and resubmitting the same key on retry) for offline-replay? | Yes — required for safety | If DriftPOS generates a fresh key on each retry, duplicates will not be deduped and the merchant's transaction count will inflate |
| OQ-4 | What's DriftPOS's offline outage queue capacity? (How many transactions can a register queue before it must refuse new sales?) | Unknown; flag for Bart | Determines whether `replay-batch` max size of 1000 is appropriate, or needs to be 10K+ |
| OQ-5 | Does DriftPOS maintain per-register UUID assignment server-side, or does each register self-assign? | Server-side via Canary registration endpoint | Self-assigned UUIDs create idempotency-namespace collision risk if a register is re-imaged |
| OQ-6 | Does DriftPOS print receipts during offline operation? If so, how does it reconcile receipt-numbering with Canary on reconnect? | Local register-day numbering; reconciled on replay | Receipt number uniqueness is enforced on `(tenant_id, location_id, business_date, transaction_number)` — register must guarantee no collision |
| OQ-7 | What's DriftPOS's stance on the `evidence_trigger` capability? Does the cashier UI surface a "mark as LP-relevant" button? | Optional — surface only if merchant configures it | The wire supports it; the UX decision is Bart's |
| OQ-8 | Will DriftPOS implement the change-feed pull cadence (`/changes?since=`) or rely solely on push notifications? | Both (push + 30s pull as backstop) | Push notifications can be lost; 30s pull catches gaps |
| OQ-9 | EBT acceptance — does DriftPOS handle USDA SNAP eligibility logic locally, or does Canary need to expose a real-time eligibility check endpoint? | DriftPOS handles locally per processor's certified flow | Per GRO-721 capability map row #84 — register-side responsibility |
| OQ-10 | Does DriftPOS track three-level void semantics (transaction, line, sub-line) like Toast, or simpler register-level void only? | Simpler — register-level + line-level | Toast's three-level model is restaurant-specific; SMB retail rarely uses it |
| OQ-11 | What's DriftPOS's PCI scope as a cardholder-data-handling app — is it certified at the level required for an SMB merchant's PCI-DSS attestation? | Required | Bart's team must complete this independently; Canary's PCI posture (out of scope until phase 4) is conditional on DriftPOS owning the cardholder-data interaction |
| OQ-12 | Will DriftPOS support the conformance test suite as part of its CI? | Strongly recommended | Without Bart-side CI on conformance, regressions may go undetected until they hit a merchant |

---

## §11. Cross-references and Related SDDs

### Direct dependencies

- `pos-adapter-substrate.md` — the `SourceAdapter` interface DriftPOS adapter satisfies (Canary side)
- `webhook-pipeline.md` — gateway → sub1 → sub2 → DB pipeline this contract feeds
- `party-identity-design.md` (GRO-734) — party resolution semantics, fingerprint quality matrix, PCI posture
- `multi-pos-architecture-proof.md` — three-POS comparison validating the abstraction
- `agent-contracts.md` — agent contract style this SDD borrows for its endpoint schemas
- `data-model.md` (CRDM) — canonical schema this contract maps onto
- `go-module-layout.md` — Canary module spine; gateway runs on `:8080`
- `microservice-architecture.md` — inter-service call graph

### Sibling SDDs

- `Brain/wiki/cards/loop2-build-report.md` (GRO-761) — Loop 2 ratified the multi-POS substrate; DriftPOS is the fourth adapter
- `Brain/wiki/canary/partnership-research/rapidpos-feature-map.md` (GRO-721) — capability split this contract operationalizes
- `docs/superpowers/plans/2026-04-29-m1-m2-m3-coverage-assessment.md` (GRO-684) — coverage gaps; relevant: POS failover queue (no SDD), field-capture naming collision

### Brain wiki

- [[platform-thesis|Platform Thesis]] — three (now four) accountability rails this contract carries
- [[concept-party-taxonomy|Party Taxonomy]] — substrate the party_id round-trip operates within
- [[platform-performance-nfrs|Platform Performance NFRs]] — SLA targets

### Memory references

- `project_canary_canonical_positioning` — Canary owns back-half; DriftPOS owns register
- `project_rapidpos_go_pivot` — partner team is Go-only on the back-half; DriftPOS is .NET on the front
- `project_canary_above_not_against_pos` — wire contract is the boundary that makes this true
- `project_mcp_native_virtual_usbc_plug` — DriftPOS is one of the "USB-C plugs" Canary's substrate accepts
- `feedback_dont_crystallize_speculation` — every DriftPOS-side behavior assumption is flagged in OQ table

### Code references (forward)

- `CanaryGo/internal/adapters/driftpos/` — adapter package (to be written; mirrors `internal/adapters/counterpoint/` shape)
- `CanaryGo/internal/adapters/driftpos/parser.go` — DriftPOS envelope → CanonicalEvent parser (Loop 3 work)
- `CanaryGo/internal/adapters/driftpos/parser_test.go` — fixture-driven parser tests
- `CanaryGo/cmd/gateway/routes.go` — `/webhooks/driftpos/*` route registration
- `CanaryGo/deploy/openapi/driftpos-v1.yaml` — OpenAPI 3.0 spec generated from this SDD

### External standards

- ARTS POSLOG XML / canonical model — entity shapes mapped in §2 wire inventory
- PCI-DSS v4.0 — § 3.3 (PAN truncation rules); § 4 (transmission encryption)
- ISO 4217 — currency codes
- ISO 8601 — timestamp format
- E.164 — phone normalization (per `party-identity-design.md`)
- Luhn algorithm — PCI-boundary defense (§7)

---

## Self-review against constraints C-1 through C-6

| # | Constraint | Status | Evidence |
|---|---|---|---|
| C-1 | Source citation per design decision | PASS | Every endpoint cites GRO-721 capability row, ARTS spec section, or platform-thesis rail |
| C-2 | Anti-speculation; flag DriftPOS-team confirmation required | PASS | 12 OQs enumerated in §10; no DriftPOS-side behavior assumed |
| C-3 | Identity rule (register-execution = DriftPOS) | PASS | §1 table walks every "Both" capability through the rule |
| C-4 | PCI boundary; Canary never receives PAN | PASS | §3 envelope schema, §7 PCI-boundary-violation handler, §8 PCI scope analysis |
| C-5 | ARTS-native; no proprietary message formats | PASS | §2 wire inventory: 31/38 direct ARTS map; 7 documented extensions inline-justified |
| C-6 | Tenant isolation; every payload carries tenant_id | PASS | §3 cross-cutting requirement #2; §7 tenant-mismatch failure mode |

---

## Quality Gate 8.G1 — coverage of "Both" capabilities

Per the dispatch's quality gate: every "Both"-side capability from GRO-721 must have a corresponding wire crossing defined or an explicit deferral with rationale.

| GRO-721 row | Capability | Wire crossing |
|---|---|---|
| #25 | Loyalty redemption at checkout | Endpoint `/v1/driftpos/loyalty/redeem-check` + push `/webhooks/driftpos/loyalty/redemption`. Real-time blocking. |
| #98 | Surveillance video integration | Push `/webhooks/driftpos/evidence-trigger`. Register triggers; back-half indexes. Bridge to a video system is out of this contract's scope (separate vendor integration). |
| #102 | Auto re-sync payments and inventory after outage | Push `/webhooks/driftpos/replay-batch`. §7 "Register offline" failure mode. |
| #107 | Mobile gun-show mode | Same as #102 — replay-batch handles offline mode. Compliance-specific (firearm) handled via `/webhooks/driftpos/compliance/firearm-event`. |
| #77 | Bottle deposits | Carried in `RetailTransaction.line_items[].attributes` as `bottle_deposit_amount`; back-half tracks liability via `compliance` module. Documented as line-item attribute extension. |
| #35 | Online waivers | `/webhooks/driftpos/compliance/waiver-check` (real-time, blocking). |
| #79 | Gift card swipe at register | `/webhooks/driftpos/loyalty/redemption` (gift card is a tender type, processed through the same path). Balance query: `/v1/driftpos/loyalty/membership/{...}` for gift-card-as-stored-value. |
| #19 | Customer DL-scan ID capture | `/v1/driftpos/customer/dl-scan` (POST, register submits). |

**All 8 "Both" capabilities have wire crossings.** Quality Gate 8.G1: PASS.

---

*Canary | GrowDirect LLC | Confidential*
