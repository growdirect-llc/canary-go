---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: commercial
port: 9089
mcp-server: canary-commercial
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Commercial — Vendor Relationship Layer

**Type:** Domain Service — Vendor Finance  
**Binary:** `cmd/commercial` → `:9089`  
**MCP server:** `canary-commercial` (7 tools)  
**Depends on:** `identity` (merchant + vendor existence), `raas` (chain events for contract milestones), `ildwac` (cost basis for rebate qualification)  
**Feeds:** `hawk` (chargeback dispute signals), `three-way-match` (invoice deduction reconciliation), `owl` (vendor performance analytics)

Commercial is the vendor relationship layer. It tracks contracts, payment terms, rebates, and chargebacks — the financial obligations that sit between a purchase order and a paid invoice. Without it, merchants have no systematic way to claim money they are owed (vendor rebates, markdown allowances, co-op advertising funds) or to contest money vendors are claiming (overcharges, unauthorized deductions). At $5–15M annual sales, unclaimed rebates and uncontested chargebacks represent 0.5–1.5% of revenue — real money for a thin-margin retailer who rarely has a dedicated AP clerk watching these exposures.

**Multi-tenant context.** Commercial tables (`vendors`, `vendor_contracts`, `chargebacks`, `rebate_accruals`, `coop_claims`) live per-tenant in `tenant_{merchant_id}`. Vendor records are merchant-scoped; the same vendor connected to multiple merchants on the platform appears as separate vendor records in each merchant's schema. Cross-tenant vendor performance benchmarking flows through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Commercial operates with all Optional Features (per `platform-overview.md`) disabled — chargeback workflows, rebate accruals, and contract tracking work entirely with internal database records. When `VENDOR_CONTRACTS_ENABLED=true`, vendor compliance terms are deployed as smart contracts on the AVAX private vendor subnet (per the chain-of-record split in `blockchain-anchor.md`); chargebacks become contract events with on-chain references. When the flag is off, contracts and disputes live in the database only — fully functional, just not externally verifiable on a vendor-facing chain.

---

## Business

### The Vendor Finance Problem

The average independent retailer in Canary's ICP has 30–80 active vendor relationships. Each relationship carries a contract with terms that vary: net-30 vs. net-60 payment windows, tiered volume rebates, seasonal markdown allowances, co-op advertising funds, and return-to-vendor (RTV) deduction rules. None of this lives in the POS. The merchant tracks it in a spreadsheet — or doesn't track it at all — and leaves money on the table at every billing cycle.

Canary Commercial solves this by making vendor terms machine-readable and tying them directly to purchase order and invoice data. The result: accruals computed automatically, claims generated at period close, and chargebacks surfaced before they age past the dispute window.

### The Chargeback Exposure

Vendors routinely deduct from invoice payments for reasons that range from legitimate (shortage claims, quality rejections) to aggressive (unauthorized deductions, miscalculated allowances). Disputes must be filed within the vendor's stated window — typically 30–90 days from invoice date. A merchant without a system to log and age these deductions will miss the window and absorb the loss. Commercial tracks every deduction from invoice date and alerts when the dispute window is approaching.

### Business Rules

1. A vendor cannot be created without an active `merchants` row. `merchant_id` is a foreign key; the identity service owns this constraint.
2. Contract terms are stored in `terms JSONB`. Schema validation runs at the application layer, not the DB. The JSONB column is intentionally flexible — contracts differ enough that a fixed column set would either overfit or leave too much in free text.
3. Rebate accruals are computed per contract period. A period is defined by `period_start` and `period_end` on `vendor_rebate_accruals`. Accruals are not auto-computed by a background job — they are triggered by the merchant via `accrue_rebate` or by a period-close agent. This is intentional: automatic accruals without review produce claims errors.
4. A deduction transitions through a strict status machine: `claimed` → `disputed` → `approved` | `denied`. A `claimed` deduction can be disputed within the vendor's dispute window. Once `approved` or `denied`, status is terminal.
5. Rebate accrual status machine: `pending` → `claimed` → `paid`. A rebate cannot be marked `paid` without a corresponding invoice reference.
6. Vendor balance summary (`vendor_balance_summary`) is a derived read — it does not write state. It is the primary surface for merchant dashboard display.

### Vendor Finance Lifecycle

```
Vendor contract executed
         │
         ▼
commercial.create_vendor
commercial.list_contracts (load existing terms)
         │
         ▼
Inventory received → POs created (purchasing module)
         │  qualified_purchases accumulate per contract period
         ▼
Period close approaches
         │
         ▼
commercial.accrue_rebate
         │  computes accrued_sats = qualified_purchases × rebate_rate_pct
         │  status: pending
         ▼
commercial.claim_rebate
         │  submits claim to vendor; status: pending → claimed
         ▼
Vendor pays or disputes
         │
         ├── Vendor pays → status: claimed → paid
         │
         └── Vendor deducts invoice
                  │
                  ▼
            commercial.record_deduction
                  │  status: claimed
                  ▼
            commercial.dispute_deduction
                  │  within dispute window; status: claimed → disputed
                  ▼
            Vendor resolves
                  │
                  ├── Vendor credits → status: disputed → approved
                  └── Vendor denies → status: disputed → denied
```

---

## Technical

### Service Boundaries

Commercial owns four table groups. No other service writes to these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Vendors | `vendors` | Vendor master — one row per merchant × vendor relationship |
| Contracts | `vendor_contracts` | Machine-readable contract terms by type |
| Deductions | `vendor_deductions` | Per-invoice deduction tracking and status |
| Rebate Accruals | `vendor_rebate_accruals` | Period-level rebate accrual and claim state |

### Data Model

#### `vendors`

```sql
CREATE TABLE vendors (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    name             TEXT NOT NULL,
    payment_terms_days INTEGER NOT NULL DEFAULT 30,
    contact          JSONB,           -- {name, email, phone, account_rep}
    active           BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, name)
);

CREATE INDEX idx_vendors_merchant ON vendors(merchant_id);
CREATE INDEX idx_vendors_merchant_active ON vendors(merchant_id, active);
```

**Notes:** `contact` is JSONB to accommodate the full range of vendor contact structures without premature normalization. Soft-delete via `active = false` — vendor financial history must be retained even after a vendor relationship ends.

#### `vendor_contracts`

```sql
CREATE TYPE vendor_contract_type AS ENUM (
    'rebate', 'allowance', 'coop', 'chargeback'
);

CREATE TABLE vendor_contracts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id        UUID NOT NULL REFERENCES vendors(id),
    contract_type    vendor_contract_type NOT NULL,
    terms            JSONB NOT NULL,       -- structured per contract_type; validated at app layer
    effective_from   DATE NOT NULL,
    effective_to     DATE,                 -- NULL = open-ended contract
    active           BOOLEAN NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_vendor_contracts_vendor ON vendor_contracts(vendor_id);
CREATE INDEX idx_vendor_contracts_vendor_type ON vendor_contracts(vendor_id, contract_type);
CREATE INDEX idx_vendor_contracts_active ON vendor_contracts(vendor_id, active, effective_from);
```

**JSONB `terms` schema by contract type:**

| `contract_type` | Required keys in `terms` |
|-----------------|--------------------------|
| `rebate` | `rebate_rate_pct NUMERIC`, `qualified_purchase_threshold_sats BIGINT`, `period_months INT` |
| `allowance` | `allowance_type TEXT`, `allowance_amount_sats BIGINT`, `qualifying_condition TEXT` |
| `coop` | `fund_pct_of_purchases NUMERIC`, `cap_sats BIGINT`, `eligible_media TEXT[]` |
| `chargeback` | `dispute_window_days INT`, `deduction_categories TEXT[]`, `escalation_contact TEXT` |

Application-layer validation rejects contracts with malformed or missing required keys before INSERT. This validation lives in `internal/commercial/contracts.go` — not in the DB.

#### `vendor_deductions`

```sql
CREATE TYPE deduction_status AS ENUM ('claimed', 'disputed', 'approved', 'denied');

CREATE TABLE vendor_deductions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id        UUID NOT NULL REFERENCES vendors(id),
    invoice_id       TEXT NOT NULL,        -- vendor's invoice reference; not a FK (external system)
    invoice_date     DATE NOT NULL,        -- dispute window measured from this date
    deduction_type   TEXT NOT NULL,        -- "shortage" | "quality" | "allowance" | "unauthorized"
    amount_sats      BIGINT NOT NULL CHECK (amount_sats > 0),
    status           deduction_status NOT NULL DEFAULT 'claimed',
    dispute_notes    TEXT,
    dispute_filed_at TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_vendor_deductions_vendor ON vendor_deductions(vendor_id);
CREATE INDEX idx_vendor_deductions_vendor_status ON vendor_deductions(vendor_id, status);
CREATE INDEX idx_vendor_deductions_invoice_date ON vendor_deductions(invoice_date)
    WHERE status = 'claimed';   -- partial index: only open deductions need date aging
```

**Dispute window enforcement:** the application layer, not the DB, checks whether `NOW() - invoice_date > chargeback.terms.dispute_window_days`. A deduction past the window can still be disputed — the system logs the override and flags it to hawk. The DB does not hard-block past-window disputes because legal escalation occasionally overrides standard windows.

#### `vendor_rebate_accruals`

```sql
CREATE TYPE rebate_status AS ENUM ('pending', 'claimed', 'paid');

CREATE TABLE vendor_rebate_accruals (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor_id               UUID NOT NULL REFERENCES vendors(id),
    contract_id             UUID NOT NULL REFERENCES vendor_contracts(id),
    period_start            DATE NOT NULL,
    period_end              DATE NOT NULL,
    qualified_purchases_sats BIGINT NOT NULL DEFAULT 0,
    rebate_rate_pct         NUMERIC(6,4) NOT NULL,
    accrued_sats            BIGINT NOT NULL DEFAULT 0,  -- qualified_purchases × rebate_rate_pct
    status                  rebate_status NOT NULL DEFAULT 'pending',
    claim_reference         TEXT,           -- vendor's claim number after submission
    paid_at                 TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_end > period_start),
    UNIQUE(vendor_id, contract_id, period_start)
);

CREATE INDEX idx_rebate_accruals_vendor ON vendor_rebate_accruals(vendor_id);
CREATE INDEX idx_rebate_accruals_status ON vendor_rebate_accruals(vendor_id, status);
```

### API Contract

All routes require JWT auth except `/commercial/healthz` and `/commercial/readyz`.

```
GET  /commercial/healthz                         → 200
GET  /commercial/readyz                          → 200 | 503
GET  /commercial/vendors/{merchant_id}           → 200 [{vendor}]
GET  /commercial/vendors/{vendor_id}/contracts   → 200 [{contract}]
POST /commercial/deductions                      → 201 {deduction_id}
PUT  /commercial/deductions/{id}/dispute         → 200 {deduction}
POST /commercial/accruals                        → 201 {accrual_id}
POST /commercial/accruals/{id}/claim             → 200 {accrual}
GET  /commercial/vendors/{vendor_id}/balance     → 200 {balance_summary}
```

### MCP Tool Surface — `canary-commercial` (7 tools)

| Tool | Input | Output | SLA | Notes |
|------|-------|--------|-----|-------|
| `get_vendor` | `vendor_id` | `{vendor, active_contracts}` | <100ms | Joins vendors + vendor_contracts |
| `list_contracts` | `vendor_id, contract_type?` | `[]{contract}` | <200ms | Filtered by type if provided |
| `record_deduction` | `vendor_id, invoice_id, invoice_date, deduction_type, amount_sats` | `{deduction_id}` | <500ms | Initial status: `claimed` |
| `dispute_deduction` | `deduction_id, dispute_notes` | `{deduction}` | <500ms | Status: `claimed` → `disputed`; logs to hawk if past window |
| `accrue_rebate` | `vendor_id, contract_id, period_start, period_end, qualified_purchases_sats` | `{accrual_id, accrued_sats}` | <500ms | Computes `accrued_sats` server-side from contract rate |
| `claim_rebate` | `accrual_id, claim_reference?` | `{accrual}` | <500ms | Status: `pending` → `claimed` |
| `vendor_balance_summary` | `vendor_id` | `{open_deductions_sats, accrued_rebates_sats, claimed_rebates_sats, net_position_sats}` | <300ms | Derived read; no state change |

### Trade-off: JSONB Contract Terms vs. Normalized Columns

**Option A (chosen): JSONB `terms` with application-layer validation.** Contract structures differ enough across vendor types that a fully normalized column set would either require nullable columns for every variant (bad: schema tells you nothing about what's required for a given type) or a separate table per contract type (bad: four-way JOIN on every contract read, schema migration required to add a new contract type). JSONB keeps the schema stable while the business adds contract types; the validation logic in `internal/commercial/contracts.go` is the schema enforcer.

**Option B: Separate table per contract type.** Cleanest type safety, worst query ergonomics. Rejected on the grounds that this module is likely to see new contract types (e.g., `rebate_tier`, `guaranteed_margin`) and each addition would require a migration and new JOIN path.

**The cost of Option A:** validation errors surface at runtime, not at schema creation. The test suite must cover the contract type validator exhaustively — it is the only thing standing between bad data and a bad claim.

### Go Implementation Notes

- `accrue_rebate` computes `accrued_sats` in the handler, not a DB function. The formula is `qualified_purchases_sats * rebate_rate_pct / 100`. Use integer arithmetic: `accrued_sats = int64(float64(qualified_purchases_sats) * (rebate_rate_pct / 100.0))`. Precision loss at this conversion is acceptable (< 1 sat rounding) for rebate values.
- `vendor_balance_summary` executes four aggregation queries in parallel goroutines and assembles the response. Do not serialize them — total query time is bounded by the slowest single query, not their sum.
- Deduction status transitions are enforced in the handler via an explicit state machine check before UPDATE. Do not rely on the ENUM constraint alone — ENUM prevents invalid state names, not invalid transitions.
- `dispute_deduction` must call `hawk.flag_signal` (via internal HTTP or gRPC) if the deduction's `invoice_date` is past the vendor's dispute window. This is a best-effort call: hawk failure does not fail the dispute filing. Log the hawk call failure and proceed.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `get_vendor` | <30ms | <100ms | 300ms | Alert |
| `vendor_balance_summary` | <100ms | <300ms | 1s | Alert; check parallel query execution |
| `record_deduction` | <100ms | <500ms | 2s | Alert |
| `accrue_rebate` | <100ms | <500ms | 2s | Alert |
| `dispute_deduction` | <100ms | <500ms | 2s | Alert; includes hawk signal call |

### Health Endpoints

```
GET /commercial/healthz
→ 200 { "status": "ok" }
Shallow liveness. No DB or Valkey check.

GET /commercial/readyz
→ 200 { "status": "ok", "db_ok": true, "vendor_count": N }
→ 503 if DB unreachable.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| DB unreachable | All write tools return 503. `vendor_balance_summary` returns 503. | Auto-recovery via pgx connection pool on DB reconnect. |
| hawk unreachable during `dispute_deduction` | Dispute is filed; hawk call failure is logged and metered. No retry. | Alert on hawk failure rate. Manual review queue for missed hawk signals. |
| Invalid JSONB contract terms | `list_contracts` validator returns 422 with field-level errors. | Fix the contract record; no DB rollback needed (INSERT was rejected). |
| Rebate status out of sequence | Handler returns 409 Conflict with current status. | Caller reads current state via `get_vendor` and re-evaluates. |

### Graceful Shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
srv.Shutdown(ctx) // default 30s drain
pool.Close()
```

In-flight deduction and accrual writes must complete. Partial writes will roll back cleanly — no deduction row with an ambiguous status will persist.

### Valkey Key Space

Commercial does not use Valkey for primary data paths. Vendor balance summary results may be cached at a higher layer (e.g., dashboard API), but the commercial service itself does not cache.

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| (none defined at service level) | — | Balance summary is computed live; no cache |

### Monitoring

Alert on:
- `dispute_deduction` hawk signal call failure rate > 5% over 5 minutes
- Any `vendor_balance_summary` P99 > 1s sustained for 2 minutes
- `vendor_rebate_accruals` with status `pending` and `period_end < NOW() - 30 days` — unclaimed rebates aging past the reasonable claim window (scheduled daily check, not a real-time alert)

---

## Compliance

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `contact` | `vendors` | Sensitive | Contains vendor contact names and emails. Do not expose raw in logs. Mask in error messages. |
| `invoice_id` | `vendor_deductions` | Sensitive | External invoice reference. Do not log in plaintext at DEBUG level. |
| `dispute_notes` | `vendor_deductions` | Internal | May contain legal strategy. Restrict API access to merchant-owner role. |

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `vendors` | 7 years after `active = false` | IRS vendor payment records |
| `vendor_contracts` | 7 years after `effective_to` | Contract audit trail |
| `vendor_deductions` | 7 years | AP dispute evidence |
| `vendor_rebate_accruals` | 7 years | Revenue recognition audit |

Rebate and deduction records are financial instruments. Deletion before the 7-year retention window requires Legal & Compliance review.

---

## Related SDDs

- `identity.md` — owns `merchants` table; vendor foreign key chain starts here
- `raas.md` — contract milestone events (contract executed, rebate claimed) appended to chain via `append_event`
- `ildwac.md` — WAC cost basis feeds qualified purchase computation for rebate accruals
- `three-way-match.md` — invoice reconciliation surfaces deductions for `record_deduction`
- `hawk.md` — receives chargeback dispute signals from `dispute_deduction`; past-window disputes escalate to LP queue
- `owl.md` — vendor performance analytics consume commercial data for trend analysis
