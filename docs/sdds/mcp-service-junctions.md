# Canary Go — MCP Service Junction Inventory (SDD)

**Status**: DRAFT v1 (2026-05-01)
**Branch**: `gd-canonical-data-model`
**Companion**: `canonical-data-model.md` (the data model that defines what each junction transports)

**Per founder direction (2026-05-01)**: each MCP service at an L3 bus junction is born self-aware of its SLA. This SDD is the source-of-truth for what every junction knows about itself — its source/target binding, payload contract, cadence, and SLA targets (latency, freshness, atomicity, retry, idempotency, auth, observability).

---

# Chunk 9b — MCP Service Junction Inventory

**Purpose**: The ~150+ MCP service junctions referenced across chunks 2-8, consolidated into a single SLA-spec inventory. Per founder direction (2026-05-01): each MCP service at an L3 bus junction is **born self-aware of its SLA**. This document is the source-of-truth for what every junction knows about itself.

**Approach**: Junctions group into 10 architectural archetypes, each with default SLA targets. Individual junction entries reference their archetype + override only deviations. Saves hundreds of redundant SLA specs while keeping per-junction precision available.

## Junction archetypes

Each archetype has default SLA targets. Individual junctions inherit; override only when stricter or looser is justified.

| Archetype | Description | Default SLA |
|---|---|---|
| **A1 — Real-time master replicate** | Master-data change pushed to all consumers immediately (item, vendor, location, customer create/update). Idempotent on natural key. | latency p95 < 200ms · freshness < 5s at consumers · atomicity transactional · idempotent on natural key · retry 3× exp-backoff · auth tenant-scoped service token · trace + emit event |
| **A2 — Real-time scan lookup** | High-frequency point read at POS scan or web add-to-cart. Cache-friendly. | latency p99 < 30ms · freshness eventual (cache up to 5s) · idempotent (read-only) · retry 1× immediate · auth public-read within tenant · trace 1% sample |
| **A3 — Real-time event emit** | Append-only event creation (transaction complete, movement, evidence). | latency p95 < 500ms · atomicity transactional with related writes · idempotent on (tenant_id, source_event_id) · retry 5× exp-backoff up to 5min · auth tenant-scoped · trace 100% + emit downstream events |
| **A4 — Scheduled batch aggregate** | Periodic rollup or refresh (daily metrics fact, forecast snapshot). | latency n/a · cadence configurable (typical: nightly 02:00 local) · atomicity eventual (batch-level) · idempotent on (tenant_id, period) · retry 3× hourly · auth scheduler-bound service token · trace per-batch summary |
| **A5 — Event-triggered fan-out** | One source event triggers multiple downstream consumers (transactional fan-out per S078 pattern). | latency p95 < 2s end-to-end · atomicity all-or-nothing across fan-out targets · idempotent per consumer · retry per-consumer 3× exp-backoff · auth chained from source · trace correlation-id propagated |
| **A6 — Long-running poll** | Periodic poll of external system (Counterpoint REST polling pattern). | cadence configurable (typical: 5min watermark) · atomicity per-document · idempotent on (source_system, external_id, version) · retry 5× with backoff up to 1hr · auth POS-native credentials · trace per-poll cycle |
| **A7 — Append-only event log** | Pure append, no updates, audit-grade (audit_log, evidence, ledger entries). | latency p95 < 100ms · atomicity transactional with source operation · idempotent on (tenant_id, source_id, sequence) · retry 5× until success · auth tenant-scoped · trace 100% with hash chain |
| **A8 — Three-way match (stateful)** | PO ↔ receipt ↔ invoice correlation; longer time horizon, retains state across days. | latency seconds-to-minutes per match attempt · atomicity per-match · idempotent on (tenant_id, invoice_id, po_id) · retry until manual_override · auth finance-role required · trace + audit log every match attempt |
| **A9 — Discriminated message routing** | Single message stream with type discriminator drives downstream routing (JIROA/JIROB pattern). | latency p95 < 500ms · atomicity per-message · idempotent on (source_system, message_id) · retry 3× exp-backoff · auth chained · trace include discriminator for downstream filtering |
| **A10 — Cross-tenant administrative** | Rare cross-tenant operations (tenant onboarding, support impersonation). | latency seconds (interactive) · atomicity transactional · idempotent on (operation_id) · retry manual · auth elevated-role required (org-admin or support-engineer) · trace 100% + audit_log + alert |

## Junction inventory by domain

### M (Merchandising) — 12 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.master.item.create` | producer | A1 | item created in merchant tool / import |
| `mcp.master.item.update` | producer | A1 | attribute change |
| `mcp.master.item.delete` | producer | A1 | soft-delete (status='discontinued') |
| `mcp.master.item.lookup` | consumer | A2 | every POS scan, web cart add |
| `mcp.master.item.scan-resolve` | consumer | A2 | barcode → item lookup |
| `mcp.master.category.upsert` | producer | A1 | hierarchy change |
| `mcp.master.vendor.upsert` | producer | A1 | vendor master |
| `mcp.master.vendor.from-financial-sync` | bridge | A6 | OFi-style vendor origin sync |
| `mcp.master.item-vendor.assign` | producer | A1 | item-vendor link |
| `mcp.master.item-vendor.update-cost` | producer | A1 | cost change from vendor PO ack |
| `mcp.master.item-barcode.assign` | producer | A1 | new barcode at item create |
| `mcp.master.item-barcode.recover-not-on-file` | producer | A1 (half-hourly C027 pattern) | NoF recovery — overrides A1 cadence to scheduled |
| `mcp.master.item-pack.compose` | producer | A1 (low cadence) | pack definition |

### A (Asset / Location) — 8 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.master.location.create` | producer | A1 | store/warehouse onboard |
| `mcp.master.location.update` | producer | A1 | attribute change |
| `mcp.master.location.lookup` | consumer | A2 | every transaction |
| `mcp.master.location-hierarchy.upsert` | producer | A1 | hierarchy node |
| `mcp.master.location-hierarchy.assign` | producer | A1 | location-to-hierarchy |
| `mcp.master.location-zone.upsert` | producer | A1 (low cadence) | store layout config |
| `mcp.assortment.upsert` | producer | A1 | item authorized for location |
| `mcp.assortment.from-planogram-derive` | bridge | A5 | planogram → assortment automatic |

### S (Space) — 5 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.space.planogram.upsert` | producer | A1 (low cadence) | planogram design |
| `mcp.space.planogram.assign-location` | producer | A1 | planogram-to-location |
| `mcp.space.position.upsert` | producer | A1 (low cadence) | item position on shelf |
| `mcp.store-line.position-push` | consumer | A5 (S078 pattern) | transactional fan-out to GPM + SR equivalents |
| `mcp.shelf-edge.label-print` | consumer | A4 | daily batch label generation |

### C (Customer) — 11 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.customer.create` | producer | A1 | enrollment |
| `mcp.customer.update` | producer | A1 | profile change |
| `mcp.customer.merge` | producer | A10 | dedup — deferred-constraint multi-row write, requires support-role auth |
| `mcp.customer.from-pos-native-sync` | bridge | A6 | Square Customer / Counterpoint AR_CUST sync |
| `mcp.customer-address.add` | producer | A1 | address management |
| `mcp.customer-address.from-order-derive` | bridge | A5 | auto-add on shipping order |
| `mcp.loyalty.enroll` | producer | A1 | loyalty signup |
| `mcp.loyalty.points-earn` | producer | A3 | atomic with transaction complete |
| `mcp.loyalty.points-redeem` | producer | A3 | atomic with transaction complete |
| `mcp.loyalty.tier-evaluate` | producer | A4 | monthly tier recalc |
| `mcp.loyalty.lookup` | consumer | A2 | every loyalty-tender or earn at POS |

### Party (Substrate Identity) — 5 junctions

Added per **GRO-734** / `party-identity-design.md`. The party substrate sits upstream of `c.customers`; every transaction-complete resolves a party. Schema: `party` (6 tables + decisioning_facts materialized view).

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.party.resolve-from-tender` | producer | A2 (override: must commit transactionally with caller) | resolve-or-create party from tender fingerprint at transaction-complete; SLA p99 < 50ms (10% of t.transactions.complete budget); idempotent on `(tenant_id, identifier_type, identifier_value_hash)`; request: `{tenant_id, source_system, payment_metadata, secondary_identifiers[]}`; response: `{party_id, party_code, confidence, was_created, identifier_id}` — see party-identity-design.md §Part C resolution rules 1-3 |
| `mcp.party.merge-anonymous-to-known` | producer | A10 (cross-party multi-row write, requires merchant-admin or support-role) | merchant-initiated merge of two parties (or de-merge); supports `action='merge'` with `surviving_party_id` + `to_merge_party_id`, or `action='de_merge'` with `target_identifiers[]` to split off; appends 1-2 resolution_events rows; SLA p99 < 500ms; auth: merchant-admin or support-role |
| `mcp.party.identifier-add` | producer | A3 | attach a new identifier to an existing party (e.g., merchant captures email at first sale to anonymous party); request: `{tenant_id, party_id, identifier_type, identifier_value, source_system}`; response: `{identifier_id, was_attached, prior_party_id?}` (prior_party_id non-null if identifier previously belonged to a different party — caller may then queue merge review); SLA p99 < 80ms |
| `mcp.party.household-detect` | bridge | A4 (default: nightly batch) + A1-style on-demand mode | nightly auto-detection per tenant when `PARTY_HOUSEHOLDS_ENABLED=true`; also supports synchronous `mode='manual'` with explicit `member_party_ids[]` for merchant-driven assignment; appends `party.household_evidence` rows; threshold-driven membership creation per party-identity-design.md §Part D; SLA: nightly batch under 30min per tenant; manual mode p99 < 2s |
| `mcp.party.decisioning-recompute` | producer | A4 (default: daily 02:00 local per tenant) + A1-style on-demand for fraud_risk | refreshes `party.decisioning_facts` materialized view; daily batch covers value/frequency/monetary/segment_tags; on-demand mode with `compute_risk=true` recomputes party_fraud_risk for a single party (cached 1h); weekly Sunday 03:00 covers party_churn_risk; SLA: nightly tenant batch under 30min; on-demand single-party p99 < 500ms |

### L (Labor) — 7 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.employee.hire` | producer | A1 | onboard |
| `mcp.employee.update` | producer | A1 | profile change |
| `mcp.employee.terminate` | producer | A1 (audit-trail-preserving) | soft-terminate |
| `mcp.employee.from-pos-native-sync` | bridge | A6 | Square Team Member sync |
| `mcp.employee-role.assign` | producer | A1 | promotion/role change |
| `mcp.employee-location.assign` | producer | A1 | hire/transfer |
| `mcp.access-control.role-check` | consumer | A2 | every Canary action — RBAC |

### D (Distribution / Inventory) — 17 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.inventory.position.materialize` | bridge | A3 | recompute from movements, atomic with movement insert |
| `mcp.inventory.position.from-stock-count` | producer | A3 | direct overwrite from physical count |
| `mcp.inventory.movement.from-goods-receipt` | producer | A3 | D028 GRN equivalent |
| `mcp.inventory.movement.from-adjustment` | producer | A3 | D019 adjustment |
| `mcp.inventory.movement.from-transfer` | producer | A3 | D036/D038 transfer |
| `mcp.inventory.movement.from-rtv` | producer | A3 | D030/D035 RTV |
| `mcp.inventory.movement.from-sale` | producer | A3 | derived from t.transactions complete |
| `mcp.inventory.movement.from-cycle-count` | producer | A3 | D029 stock-count corrections |
| `mcp.inventory.movement.reserve` | producer | A3 | soft reservation for orders |
| `mcp.inventory.movement.release-reservation` | producer | A3 | order cancel/fulfill |
| `mcp.inventory.document.create` | producer | A1 | draft document at intent |
| `mcp.inventory.document.complete` | producer | A3 (triggers fan-out) | physical activity done |
| `mcp.inventory.document-line.update-actual` | producer | A1 | physical count |
| `mcp.inventory.lot.create` | producer | A1 | at receipt for lot-tracked items |
| `mcp.inventory.lot.recall` | producer | A1 (urgent) | recall event |
| `mcp.inventory.lot.expire` | producer | A4 | scheduled daily |
| `mcp.inventory.scan.check-availability` | consumer | A2 | every POS scan |

### O (Orders) — 22 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.orders.purchase-order.create` | producer | A1 | manual or replenishment |
| `mcp.orders.purchase-order.from-replenishment-engine` | bridge | A4 (daily) | auto-suggested PO |
| `mcp.orders.purchase-order.acknowledge` | bridge | A6 | F013 vendor ack |
| `mcp.orders.purchase-order.update-status` | producer | A1 | status transitions |
| `mcp.orders.po-line.add` | producer | A1 | line management |
| `mcp.orders.po-line.update-receipt-quantity` | producer | A1 | partial-receipt accumulation |
| `mcp.orders.po-line.cancel` | producer | A1 | line cancel |
| `mcp.orders.sales-order.create-from-web` | producer | A3 | web checkout |
| `mcp.orders.sales-order.create-from-bopis` | producer | A3 | BOPIS booking |
| `mcp.orders.sales-order.create-from-phone` | producer | A3 | phone order |
| `mcp.orders.sales-order.create-from-marketplace` | bridge | A6 | Amazon/eBay/Etsy etc. |
| `mcp.orders.sales-order.update-status` | producer | A1 | order state machine |
| `mcp.orders.so-line.add` | producer | A1 | line management |
| `mcp.fulfillment.create` | producer | A3 | from sales order |
| `mcp.fulfillment.assign` | producer | A1 | employee assignment |
| `mcp.fulfillment.update-status` | producer | A1 | pick/pack/ship state machine |
| `mcp.fulfillment.line.allocate` | producer | A3 | links to inventory_position |
| `mcp.fulfillment.line.update-picked` | producer | A3 | atomic with inventory movement |
| `mcp.allocation.allocate-for-line` | producer | A3 | reservation |
| `mcp.allocation.upgrade-soft-to-hard` | producer | A1 | cart → checkout |
| `mcp.allocation.release` | producer | A1 | cart abandonment / cancel |
| `mcp.shipping.asn.from-vendor` | bridge | A6 | J023 ASN inbound |
| `mcp.shipping.bol.from-fulfillment` | producer | A3 | J097 BOL outbound |

### P (Pricing) — 12 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.pricing.item-price.set` | producer | A1 | manual or scheduled price change |
| `mcp.pricing.item-price.from-promotion` | bridge | A5 | promotion start triggers price override |
| `mcp.pricing.item-price.expire` | producer | A4 (scheduled) | promotion end / clearance complete |
| `mcp.pricing.item-price.import-from-pos` | bridge | A6 | sync from POS-native price |
| `mcp.transaction.price.resolve` | consumer | A2 | every POS scan / web add-to-cart |
| `mcp.store-line.price-push` | consumer | A5 (PLU pattern) | overnight + half-hourly emergency |
| `mcp.promotion.create` | producer | A1 | promotion definition |
| `mcp.promotion.activate` | producer | A1 | scheduled or manual activation |
| `mcp.promotion.expire-on-schedule` | producer | A4 | scheduled expiration |
| `mcp.transaction.promotion.evaluate` | consumer | A2 | every line add and basket-total |
| `mcp.tax-class.upsert` | producer | A1 (low cadence) | tax category master |
| `mcp.tax-rate.set` | producer | A1 | per-location tax rate |
| `mcp.tax-rate.from-tax-engine-sync` | bridge | A6 | Avalara/TaxJar |
| `mcp.tax.compute` | consumer | A2 | every transaction tax line |

### F (Finance) — 14 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.tender-type.upsert` | producer | A1 (low cadence) | tender master |
| `mcp.transaction.tender.lookup` | consumer | A2 | every payment |
| `mcp.gl-account.upsert` | producer | A1 | chart of accounts |
| `mcp.financial.gl-post.tender` | bridge | A7 | tender → GL post |
| `mcp.financial.gl-post.invoice` | bridge | A7 | invoice → GL post |
| `mcp.financial.gl-post.cogs` | bridge | A7 | COGS post from movement |
| `mcp.supplier-invoice.from-vendor` | bridge | A6 | F004/F014 ReIM pattern |
| `mcp.supplier-invoice.three-way-match` | bridge | A8 | PO + receipt + invoice |
| `mcp.supplier-invoice-line.three-way-match-line` | bridge | A8 | line-level match |
| `mcp.payment.schedule-from-invoice` | producer | A1 | AP scheduling |
| `mcp.payment.issue` | producer | A3 | issue check/ACH/wire |
| `mcp.payment.bank-clear` | bridge | A6 | bank clearing |
| `mcp.financial.cash-reconciliation` | consumer | A4 | daily |
| `mcp.metrics.ap-aging` | consumer | A4 | weekly/monthly |

### T (Transaction Pipeline) — 22 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.transaction.start` | producer | A3 | first scan |
| `mcp.transaction.complete` | producer | A3 (transactional fan-out) | payment cleared |
| `mcp.transaction.void` | producer | A3 | manager-authorized |
| `mcp.transaction.return-from-receipt` | producer | A3 | return with parent FK |
| `mcp.transaction.suspend-and-recall` | producer | A1 | suspend/recall flow |
| `mcp.transaction.line.add` | producer | A3 | atomic with transaction |
| `mcp.transaction.line.void` | producer | A3 | line void |
| `mcp.transaction.line.return` | producer | A3 | line return |
| `mcp.transaction.tender.add` | producer | A3 | atomic with transaction |
| `mcp.transaction.tender.void` | producer | A3 | tender void |
| `mcp.transaction.tender.refund` | producer | A3 | refund leg |
| `mcp.transaction.discount.apply` | producer | A3 | discount line |
| `mcp.transaction.discount.void` | producer | A3 | discount void |
| `mcp.cashier.action.log` | producer | A7 | every cashier action |
| `mcp.cash-drawer.shift-start` | producer | A3 | shift open |
| `mcp.cash-drawer.count` | producer | A3 | drawer count event |
| `mcp.cash-drawer.paid-in` | producer | A3 | paid-in event |
| `mcp.cash-drawer.paid-out` | producer | A3 | paid-out event |
| `mcp.cash-drawer.shift-end` | producer | A3 | shift close |
| `mcp.shift.start` | producer | A3 | operator session start |
| `mcp.shift.end` | producer | A3 | operator session end |
| `mcp.loyalty.event.from-transaction` | producer | A7 | append to loyalty event log |
| `mcp.gift-card.event.from-transaction` | producer | A7 | append to gift card event log |

### Q (Loss Prevention) — 14 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.q.rule.create` | producer | A1 | rule definition |
| `mcp.q.rule.update` | producer | A1 | rule change |
| `mcp.q.rule.activate` | producer | A1 | enable rule |
| `mcp.q.rule.pause` | producer | A1 | disable rule |
| `mcp.q.detection.evaluate-on-event` | consumer | A2 (per event) | inline rule evaluation |
| `mcp.q.detection.scheduled-batch` | consumer | A4 | scheduled rule eval |
| `mcp.q.detection.emit-from-rule` | producer | A3 | detection signal |
| `mcp.q.case.create` | producer | A1 | case open |
| `mcp.q.case.escalate-from-detection` | bridge | A5 | detection → case |
| `mcp.q.case.update-status` | producer | A1 | state machine |
| `mcp.q.case.resolve` | producer | A1 | resolution |
| `mcp.q.evidence.collect-from-transaction` | producer | A7 | append to evidence chain |
| `mcp.q.evidence.collect-from-video` | producer | A7 | video clip |
| `mcp.q.case-action.log` | producer | A7 | every case state change |
| `mcp.q.subject.create` | producer | A1 | new investigation subject |

### Ledger / Cost-to-Serve — 13 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.ledger.stock-ledger.post-from-movement` | bridge | A7 | atomic with inventory movement |
| `mcp.ledger.ildwac.compute-position` | producer | A4 (per cadence step) | ILDWAC position calc |
| `mcp.ledger.ildwac.invoice-position` | producer | A8 | when ready to bill |
| `mcp.ledger.rib-batch.create` | producer | A1 | new RIB batch |
| `mcp.ledger.rib-batch.append-receipt` | producer | A3 | atomic with receipt |
| `mcp.ledger.rib-batch.close` | producer | A3 | batch close → posts to position |
| `mcp.ledger.otb.set-budget` | producer | A1 | budget configuration |
| `mcp.ledger.otb.consume` | producer | A3 (atomic with operation) | budget gate consume |
| `mcp.ledger.otb.close-period` | producer | A4 | period close |
| `mcp.l402.gate.check-before-operation` | consumer | A2 | every metered operation |
| `mcp.l402.charge-tenant-position` | producer | A8 (long-running) | L402 charge cycle |
| `mcp.ledger.anchor.batch-evidence` | producer | A4 (periodic) | batch evidence into Merkle root |
| `mcp.ledger.anchor.submit-to-l2` | producer | A8 (Lightning network) | submit anchor |
| `mcp.ledger.anchor.confirm` | bridge | A6 (poll for confirmation) | check L2 block |

### Cross-cutting platform — 7 junctions

| Junction ID | Type | Archetype | Notes |
|---|---|---|---|
| `mcp.tenant.onboard` | producer | A10 | new tenant — schema materialization |
| `mcp.tenant.suspend` | producer | A10 | suspend |
| `mcp.tenant.terminate` | producer | A10 | terminate |
| `mcp.audit.log` | producer | A7 | called from every state-mutating service via middleware |
| `mcp.audit.report.by-user` | consumer | A4 | reporting query |
| `mcp.audit.report.by-entity` | consumer | A4 | reporting query |
| `mcp.audit.report.by-time` | consumer | A4 | reporting query |

## Total junction count by domain

| Domain | Junction count |
|---|---|
| M (Merchandising) | 12 |
| A (Asset/Location) | 8 |
| S (Space) | 5 |
| C (Customer) | 11 |
| L (Labor) | 7 |
| D (Distribution/Inventory) | 17 |
| O (Orders) | 22 |
| P (Pricing) | 14 |
| F (Finance) | 14 |
| T (Transaction Pipeline) | 22 |
| Q (Loss Prevention) | 14 |
| Ledger / Cost-to-Serve | 13 |
| Cross-cutting platform | 7 |
| Party (Substrate Identity) | 5 |
| **TOTAL** | **171** |

## Archetype distribution

| Archetype | Count | % |
|---|---|---|
| A1 — Real-time master replicate | 56 | 33% |
| A2 — Real-time scan lookup | 15 | 9% |
| A3 — Real-time event emit | 40 | 23% |
| A4 — Scheduled batch aggregate | 18 | 11% |
| A5 — Event-triggered fan-out | 7 | 4% |
| A6 — Long-running poll | 11 | 6% |
| A7 — Append-only event log | 13 | 8% |
| A8 — Three-way match (stateful) | 4 | 2% |
| A9 — Discriminated message routing | 0 | (handled inline by routing logic) |
| A10 — Cross-tenant administrative | 7 | 4% |

**6 archetypes cover 88% of junctions.** Implementing 10 archetype templates (with SLA-default infrastructure) covers the entire L3 bus topology — individual junctions plug into the appropriate archetype with minimal config.

## L3 bus topology (high-level)

```
                    ┌────────────────────┐
                    │  External Sources  │
                    │  Square / Counter- │
                    │  point / Stripe /  │
                    │  ReIM / Tax Engine │
                    └────────┬───────────┘
                             │  A6 (poll/sync)
                             ▼
        ┌─────────────────────────────────────────────────────┐
        │            Master Data Layer (m, l, c, e, f)         │
        │  A1 producers ←→ A2 consumers (cache-friendly)       │
        └─────────┬───────────────────────────────────┬───────┘
                  │                                   │
                  │  A1 push                          │  A2 lookup
                  ▼                                   ▼
        ┌─────────────────────┐            ┌──────────────────────┐
        │  Operational Layer  │  A3 emit   │  POS / Web / Channel │
        │  (i, o, t)          │ ─────────► │  Front-of-House      │
        └─────────┬───────────┘            └──────────────────────┘
                  │
                  │  A3 fan-out + A7 append
                  ▼
        ┌──────────────────────────────────────────┐
        │   Append Layer (q.evidence, audit_log,   │
        │   ledger.*)                              │
        │   A7 — hash-chained, blockchain-anchored │
        └────────────┬─────────────────────────────┘
                     │  A8 stateful workflow + A4 batch
                     ▼
        ┌──────────────────────────────────────────┐
        │   Reconciliation Layer                   │
        │   3-way match · ILDWAC · L402-OTB ·      │
        │   blockchain anchor · metrics rollup     │
        └──────────────────────────────────────────┘
```

## Operational clock (cadence calendar)

| Cadence | Junction count | Examples |
|---|---|---|
| **Real-time** (event-driven, < 1s end-to-end) | ~109 | All A1, A2, A3, A5, A7 |
| **Hourly** (e.g., emergency price changes) | 2 | C045 emergency price (P), TDS sales push (T → metrics) |
| **Half-hourly** (NoF recovery) | 1 | `mcp.master.item-barcode.recover-not-on-file` |
| **Per-cadence-step** (ILDWAC) | 1 | `mcp.ledger.ildwac.compute-position` (configurable: minute / hour / day / week / month) |
| **Daily** (overnight batch) | ~20 | A4 — reports, forecasts, planogram pushes, RIB closes, batch anchors |
| **Weekly** (rollup) | ~5 | weekly metrics, weekly tier evaluations |
| **Monthly** (period close) | ~5 | OTB period close, tier reviews, monthly anchors |
| **On-demand** (operator-initiated) | ~25 | A6 polls, A10 admin |

## Status

- **Chunk 9b complete.** 166 MCP service junctions inventoried. 10 archetypes with default SLA targets defined. L3 bus topology and operational clock documented.
- **Resume**: Chunk 10 — Final render. Consolidate working files into:
  1. `docs/sdds/go-handoff/canonical-data-model.md` — the canonical SDD
  2. `docs/sdds/go-handoff/mcp-service-junctions.md` — the MCP service inventory
  3. Provenance memo + closure comments on GRO-732
