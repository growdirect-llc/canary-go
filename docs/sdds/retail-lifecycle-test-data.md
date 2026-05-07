---
spec-version: 1.1
updated: 2026-04-29
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | math/rand (deterministic seed) | canary_go_test DB only
source: Solex scenario runner (Canary Python prototype) + platform test data requirements
status: handoff-ready
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Retail Lifecycle Test Dataset

**Service Type:** Test Fixture / Integration Testing Infrastructure
**Last Code Review:** N/A — new Go implementation
**Related:** `data-model.md` (full schema), `transactions.md` (event model), `ecom.md` (online channel), `lp-alerts.md` (exception detection), `case-management.md` (LP case lifecycle)

---

## Purpose

The Retail Lifecycle Test Dataset is the integration testing substrate for the Canary Go platform. It provides a comprehensive synthetic dataset covering the full retail business lifecycle — from top-down OTB planning through vendor receiving, in-store transactions, exception detection, case management, ecom channel activity, and period financial reporting — across three distinct merchant profiles.

The dataset has one job: make every Canary Go module testable against realistic, interconnected data without ever touching real merchant records. Realistic volume, realistic variance, and named scenarios that exercise the exact anomaly patterns Canary is built to detect.

**Critical constraint:** This dataset lives exclusively in `canary_go_test`. No real merchant data. No production database contact. Any seed script that could reach a production database is a defect, not a feature.

**Multi-tenant test scope.** The lifecycle dataset materializes three test merchants under the schema-per-tenant pattern (per `architecture.md` "Multi-Tenant Isolation"). Each merchant gets a dedicated `tenant_{merchant_uuid}_test` schema. The seeder runs the full operational migration set against each tenant schema and populates per-merchant operational tables. The `public` schema holds shared reference data (source systems, role definitions, detection rule library); the `audit` and `analytics` schemas exist as test fixtures with empty initial state. Cross-tenant test scenarios verify that the schema-per-tenant boundary holds — a query without `SET search_path` resolves no tenant rows, and cross-tenant data leakage in either direction is a test failure.

**Optional Features test coverage.** The dataset exercises every module both with Optional Features enabled and disabled (per `platform-overview.md` "Optional Features"). Each test merchant runs in a different feature-flag profile:

| Test merchant | L402 | ILDWAC | Anchor | Vendor contracts | Profile purpose |
|---|---|---|---|---|---|
| MerchantA (Square, single-location) | off | off | off | off | Required-core baseline — proves platform operates with all flags off |
| MerchantB (Counterpoint, 5-location) | off | on | off | off | ILDWAC five-dim provenance under multi-location load |
| MerchantC (Square + Shopify dual-channel) | on | on | on | off | Full Bitcoin-standard / anchor / Lightning operation; vendor contracts off |

Test scenarios assert the correct behavior in each profile — the same operational sequence (PO → ASN → receipt → MAC update → sale → return) produces internal-only records in MerchantA and chain-anchored, ILDWAC-attributed, L402-settled records in MerchantC.

---

## Dependencies

| Dependency | Type | Required | Notes |
|------------|------|----------|-------|
| PostgreSQL (`canary_go_test` DB) | Database | Yes | All test data; isolated from canary_go (production) |
| `math/rand` with fixed seed | Go stdlib | Yes | Deterministic data generation; reproducible across runs |
| `crypto/sha1` + `uuid.NewSHA1` | Go stdlib | Yes | Deterministic UUIDs from named constants |
| All 13 Canary Go modules | Integration targets | Yes | Dataset must exercise every module's data surface |

---

## Merchant Profiles

Three synthetic merchants cover the full ICP surface. All use deterministic UUIDs generated from `uuid.NewSHA1(CanaryTestNamespace, []byte(merchantKey))`.

| Merchant | Key | Profile | Annual Revenue | Locations | POS | Ecom |
|----------|-----|---------|---------------|-----------|-----|------|
| MERCHANT_A | `test-merchant-a` | Single-location specialty retailer (Solex archetype) | ~$2M | 1 | Square | Square Online |
| MERCHANT_B | `test-merchant-b` | Multi-location retailer | ~$12M | 5 | NCR Counterpoint | None |
| MERCHANT_C | `test-merchant-c` | Mid-market retailer (dual-channel) | ~$6M | 2 | Square | Shopify |

MERCHANT_A is the Solex reference implementation. Its item catalog, transaction patterns, and ecom behavior mirror the Solex pilot data used in Python prototype validation. Scenario SC-09 (POS migration) exercises MERCHANT_C switching from Square to NCR Counterpoint mid-period.

---

## Dataset Volume

90-day test period. All volumes are minimums; generators may produce ±5% for realism.

| Event Type | MERCHANT_A | MERCHANT_B | MERCHANT_C | Total |
|-----------|------------|------------|------------|-------|
| Transactions | 2,700 | 18,000 | 8,100 | 28,800 |
| Line items | 8,100 | 72,000 | 32,400 | 112,500 |
| Returns | 270 | 900 | 540 | 1,710 |
| Voids | 135 | 540 | 270 | 945 |
| Receiving events | 36 | 180 | 90 | 306 |
| Ecom orders | 810 | 0 | 2,700 | 3,510 |
| LP exceptions flagged | 27 | 108 | 54 | 189 |
| Cases opened | 5 | 20 | 10 | 35 |

Rationale: MERCHANT_B volumes are 6.7x MERCHANT_A by location count (5 stores). LP exception rate is approximately 1% of transactions — consistent with industry benchmarks for a mid-shrink-risk retailer.

---

## Retail Lifecycle Coverage

The dataset must cover this lifecycle in dependency order. Each stage feeds the next; an agent or module that skips a stage will find missing FK references.

### 1. Planning

| Data | Generator | Notes |
|------|-----------|-------|
| OTB plan (budget by dept/class/month) | `purchase_orders.go` | 3 departments × 4 classes × 3 months |
| Assortment plan | `items.go` | Item-level planned units by class |
| Initial PO creation | `purchase_orders.go` | POs reference OTB budget |

### 2. Vendor Receiving

| Data | Generator | Notes |
|------|-----------|-------|
| ASN (advance ship notice) | `purchase_orders.go` | 95% of POs have matching ASN |
| Receiving event | `purchase_orders.go` | Date offset from PO creation; realistic lead times |
| Three-way match | `purchase_orders.go` | PO qty vs received qty vs invoice qty; SC-10 injects discrepancy |

### 3. Inventory

| Data | Generator | Notes |
|------|-----------|-------|
| Item master | `items.go` | 25 / 120 / 60 SKUs per merchant; all monetary values in cents |
| Location assignment | `locations.go` | SKUs assigned to locations via `item_locations` join |
| On-hand counts | `items.go` | Set post-receiving; decremented by transactions |

### 4. Store Operations

| Data | Generator | Notes |
|------|-----------|-------|
| Cashier assignments | `employees.go` | Cashier + manager + LP officer per location |
| Shift schedules | `employees.go` | Business-hours distribution: 70% of transactions 10am–7pm local |
| Register open/close | `events.go` | RaaS chain events; cash_drawer_open and cash_drawer_close events |

### 5. Transactions

| Data | Generator | Notes |
|------|-----------|-------|
| Sales (cash, card, split tender) | `transactions.go` | Tender mix: 40% card, 35% cash, 25% split |
| Voids | `transactions.go` | Tagged with `scenario_tag = 'void'`; SC-02 injects sweethearting voids |
| Returns | `transactions.go` | Tagged; SC-03 injects no-receipt returns |
| Exchanges | `transactions.go` | Return + sale in same transaction |
| Gift cards | `transactions.go` | Activation and redemption events |

All transactions tagged with `scenario_id` when they are part of a named scenario. Clean baseline transactions carry `scenario_id = NULL`.

### 6. Exception Detection

| Data | Generator | Notes |
|------|-----------|-------|
| LP exceptions | `transactions.go` | Generated by scenario scripts; reference the anomalous transaction IDs |
| Alert rules evaluated | Canary LP module | Detection runs against seeded data; expected alerts documented per scenario |

### 7. Case Management

| Data | Generator | Notes |
|------|-----------|-------|
| LP cases opened | `scenarios/` | One case opened per scenario that warrants it (SC-02, SC-03, SC-05, SC-07) |
| Evidence records | `scenarios/` | References exception IDs and transaction IDs |
| Case resolution | `scenarios/` | Mix of open and resolved cases; tests full lifecycle |

### 8. Ecom Channel (MERCHANT_A, MERCHANT_C only)

| Data | Generator | Notes |
|------|-----------|-------|
| Online orders | `ecom_orders.go` | Same SKU catalog as store; MERCHANT_A uses Square Online, MERCHANT_C uses Shopify |
| Fulfillment events | `ecom_orders.go` | Shipment confirmation with tracking number stub |
| Ecom returns | `ecom_orders.go` | Standard ecom returns + SC-07 (ecom-to-store return fraud) |
| Subscriptions + autoship | `ecom_orders.go` | 15% of MERCHANT_A ecom orders; SC-08 injects billing failure |

### 9. Financial Reporting

| Data | Generator | Notes |
|------|-----------|-------|
| Daily sales summary | Derived | Aggregated from transaction seeds; not independently generated |
| Weekly shrink report | Derived | Inventory delta vs sales; SC-06 injects multi-location shrink spike |
| Period P&L | Derived | Revenue - COGS - shrink; tests financial reporting module |

---

## Scenario Set

Each scenario is a named, reproducible injection applied on top of the baseline dataset. Scenarios are idempotent: `TestDataScenario(id)` can be called multiple times without duplicating data.

| Scenario ID | Name | Modules Exercised | Expected Alert / Outcome |
|-------------|------|-------------------|--------------------------|
| SC-01 | Clean day | Transactions, LP alerts | Zero alerts; baseline comparison anchor |
| SC-02 | Cashier sweethearting | Transactions, LP alerts, Case management | Sweethearting alert; case opened for cashier |
| SC-03 | Return fraud | Transactions, LP alerts, Case management | High-value return without matching sale; alert + case |
| SC-04 | Receiving short ship | Receiving, Inventory, Three-way match | Three-way match discrepancy; vendor exception raised |
| SC-05 | Cash drawer variance | Store ops, LP alerts | End-of-day cash count differs from register total; alert |
| SC-06 | Multi-location shrink spike | Inventory, LP alerts, Financial reporting | Elevated shrink in same department across all locations |
| SC-07 | Ecom-to-store return fraud | Ecom, Transactions, LP alerts, Case management | Online purchase returned in-store for cash; cross-channel alert |
| SC-08 | Subscription billing failure | Ecom, Inventory | Autoship charge declined; inventory pre-committed but not shipped |
| SC-09 | POS migration | Transactions, Identity, Data model | Mid-period POS switch (Square → NCR Counterpoint); verify RaaS chain continuity |
| SC-10 | Three-way match discrepancy | Receiving, Inventory | PO qty = 100, received = 95, invoice = 100; discrepancy of 5 units |

SC-01 is the baseline verification scenario. Before running any other scenario, run SC-01 and assert zero alerts. If SC-01 produces alerts, the base data is contaminated.

---

## Seed Data File Structure

```
CanaryGo/internal/testutil/
  seeds/
    merchants.go         — 3 merchant records with deterministic UUIDs
    items.go             — 25/120/60 SKU catalogs per merchant; all prices in cents
    locations.go         — 1/5/2 location records per merchant
    employees.go         — cashier + manager + LP officer per location
    purchase_orders.go   — 90-day PO schedule + ASNs + receiving events
    transactions.go      — transaction generator with scenario_tag support
    events.go            — RaaS chain events for all transactional data
    ecom_orders.go       — ecom channel orders (MERCHANT_A, MERCHANT_C)
  scenarios/
    sc01_clean_day.go
    sc02_sweethearting.go
    sc03_return_fraud.go
    sc04_receiving_short_ship.go
    sc05_cash_drawer_variance.go
    sc06_multi_location_shrink.go
    sc07_ecom_store_return_fraud.go
    sc08_subscription_billing_failure.go
    sc09_pos_migration.go
    sc10_three_way_match_discrepancy.go
  testutil.go            — TestDataReset() + TestDataScenario(id string)
```

`TestDataReset()` drops and rebuilds all test data in `canary_go_test` only. It is a hard error if `canary_go_test` is not the active database — the function reads `DATABASE_URL` and asserts the database name contains `_test` before proceeding.

`TestDataScenario(id string)` applies a named scenario on top of the base dataset. It is idempotent. It calls the scenario file's `Apply(*sql.Tx)` function within a transaction.

---

## Data Generation Rules

These rules are invariants, not suggestions. Any seed script that violates them is a defect.

| Rule | Requirement | Rationale |
|------|-------------|-----------|
| Deterministic random | `math/rand` initialized with fixed seed `42` | Test output must be identical across runs and environments |
| Deterministic UUIDs | `uuid.NewSHA1(CanaryTestNamespace, []byte(key))` or pre-defined constants; never `uuid.New()` | UUIDs hardcoded in assertions must not drift between runs |
| Monetary values | All values in cents (int64); no floats | Consistent with production data model; prevents rounding bugs |
| Timestamps | All in UTC; 70% of transactions between 10am–7pm local time | Realistic business-hours distribution for anomaly detection baselines |
| Customer emails | `@test.growdirect.io` domain only | Prevents accidental email delivery on test data |
| External IDs | All Square payment IDs, Shopify order IDs, NCR transaction IDs prefixed with `test_` | Prevents any accidental production API call on test data |
| Database isolation | `canary_go_test` only; `TestDataReset()` asserts DB name contains `_test` | Hard guard against production data contamination |
| No real merchant data | Synthetic data only; no PII, no real business names | Privacy and compliance |

---

## Module Coverage Matrix

The dataset must provide sufficient data to exercise every Canary Go module's core functionality. Missing coverage is a gap that must be filled before the module goes to GA.

| Module | MERCHANT_A | MERCHANT_B | MERCHANT_C | Scenarios |
|--------|-----------|-----------|-----------|-----------|
| Identity | Org + merchant provisioning | 5-location hierarchy | Dual-location | SC-09 (POS switch) |
| Transactions | Square POS events | NCR Counterpoint events | Square + mid-period NCR | SC-01 through SC-03 |
| Receiving | Single-location PO lifecycle | Multi-location PO distribution | 2-location | SC-04, SC-10 |
| Inventory | 25-SKU catalog | 120-SKU catalog | 60-SKU catalog | SC-06, SC-08 |
| Store Ops | Single-register shifts | 5-location shift matrix | 2-location | SC-05 |
| LP Alerts | Low-volume baseline | High-volume detection | Mid-volume | SC-02, SC-03, SC-05, SC-06, SC-07 |
| Case Management | 5 cases | 20 cases | 10 cases | SC-02, SC-03, SC-07 |
| Ecom | Square Online + subscriptions | None | Shopify + returns | SC-07, SC-08 |
| Financial Reporting | Period P&L | Multi-location P&L | Dual-channel P&L | SC-06 |
| MCP Tools | Agent query surface | Agent query surface | Agent query surface | All scenarios |
| RaaS Chain | Full chain from PO to case | Full chain | Full chain | SC-09 (chain continuity) |

---

## Operations

### Running Tests Against the Dataset

```bash
# Reset and seed all test data
go test ./internal/testutil/... -run TestDataReset

# Apply a named scenario
go test ./internal/testutil/... -run TestScenario/SC-02

# Integration test against full lifecycle (all modules)
go test ./... -tags integration -count=1
```

### Rebuilding After Schema Changes

When any schema migration touches a table that seed data writes:
1. Run `TestDataReset()` — drops and rebuilds all test data.
2. Run `factory list --status=ingested` — verify no test documents leaked into production tables.
3. Run SC-01 — assert zero alerts on clean baseline.
4. Run the full scenario suite — verify expected alert counts match.

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | Yes | Must point to `canary_go_test`; `TestDataReset()` hard-fails otherwise |
| `CANARY_ENV` | Yes | Must be `testing` for seed scripts to run |
| `CANARY_TEST_NAMESPACE_UUID` | No | Custom UUID namespace for deterministic IDs (default: `a1b2c3d4-...` — defined as constant in `testutil.go`) |

---

## Compliance

| Requirement | Enforcement |
|-------------|-------------|
| No real merchant data | `CANARY_ENV=testing` required; seed scripts refuse to run in production or development modes |
| No real PII | All customer emails `@test.growdirect.io`; all external IDs prefixed `test_` |
| Database isolation | `TestDataReset()` asserts database name contains `_test` before any DDL |
| Deterministic generation | Fixed random seed; deterministic UUIDs; documented in each generator file's package comment |
| No production API calls | `test_` prefix on all external IDs prevents Square, Shopify, and NCR APIs from processing test data as real events |

---

## Open Items

### P1 — Before GA

**P1-1: SC-09 (POS migration) chain continuity test not fully specified**

The RaaS event chain must maintain continuity when a merchant switches POS providers mid-period. The expected event structure for the handoff period (Square events → gap → NCR Counterpoint events with matching inventory state) is not yet documented. Spec the expected chain before implementing SC-09.

**P1-2: Ecom subscription lifecycle for MERCHANT_C (Shopify) not covered**

MERCHANT_C uses Shopify, which has a different subscription model than Square Online. SC-08 currently only exercises MERCHANT_A's Square Online autoship billing failure. Add a Shopify-equivalent scenario for MERCHANT_C.

**P1-3: Financial reporting derived data is not independently seeded**

The current design derives daily sales summary and period P&L from transaction seeds. If the financial reporting module has its own tables, those tables need seeded rows — not just derivation at query time. Verify with the financial reporting module SDD.

### P2 — Post-Launch

**P2-1: No performance baseline for LP alert detection at MERCHANT_B volumes**

MERCHANT_B generates 18,000 transactions over 90 days — the highest volume in the dataset. No benchmark exists for how long alert detection should take at this volume. Establish a p99 target before GA and assert it in the integration test suite.

**P2-2: Scenario suite has no negative tests**

Every scenario asserts that a specific alert is raised. There are no scenarios asserting that a specific alert is NOT raised (false positive tests). Add at least one scenario per alert type that should not trigger.

---

## Production Readiness Checklist

- [ ] All 13 modules have coverage rows in the Module Coverage Matrix
- [ ] SC-01 (clean day) produces zero alerts on fresh dataset
- [ ] All 10 scenarios produce documented expected outcomes
- [ ] `TestDataReset()` hard-fails on non-test database
- [ ] All external IDs prefixed `test_`; all customer emails `@test.growdirect.io`
- [ ] Deterministic UUIDs — no `uuid.New()` in seed files
- [ ] SC-09 chain continuity spec complete (P1-1)
- [ ] Performance baseline for MERCHANT_B volumes documented (P2-1)
- [ ] Integration test suite runs in CI without hitting production database
