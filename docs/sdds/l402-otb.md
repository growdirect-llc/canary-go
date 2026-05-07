---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: l402-otb
port: 9090
mcp-server: canary-otb
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# L402-OTB — Open-to-Buy Budget Enforcement

**Type:** Infrastructure Service — Financial Constraint Layer  
**Binary:** `cmd/l402-otb` → `:9090`  
**MCP server:** `canary-otb` (8 tools)  
**Depends on:** `identity` (merchant and location existence), `settings` (feature flags: `feature.l402_enforcement_enabled`), `ildwac` (WAC cost basis feeds OTB position calculation)  
**Feeds:** `purchasing` (OTB balance check before PO creation), `hawk` (budget exhaustion signals), `owl` (OTB trend analytics)

Open-to-buy (OTB) is the budget a merchant has available to purchase new inventory in a given period. Traditional OTB is a spreadsheet exercise done monthly by a buyer who may or may not be the same person writing the checks. Canary's L402-OTB module makes it a live constraint enforced at the PO creation moment — the wallet balance is the uncommitted OTB; each PO debits the wallet; returns and cancellations credit it back. The L402 Lightning wallet is the enforcement mechanism, but the mechanics are abstractions: what the merchant sees is a budget gauge, not a cryptographic protocol.

**Governing rule: L402 is opt-in architectural direction.** The entire L402 / Lightning settlement layer is gated by the system-wide `L402_ENABLED` flag (default `false`, see `platform-overview.md` "Optional Features"). When `L402_ENABLED=false`, this service runs in pure tracking mode — wallet balances are recorded, debits and credits post, but no Lightning settlement occurs and no PO is ever blocked. The schema (`otb_wallets`, `otb_transactions`, `otb_alerts`) is created either way; the writes happen either way; only the Lightning-settled enforcement layer is conditionally active.

**Even when `L402_ENABLED=true`, enforcement NEVER blocks store operations by default.** The per-merchant `feature.l402_enforcement_enabled` flag (managed by the `settings` service) controls per-node enforcement posture and defaults to `false`. A zero-balance wallet is an alert, not a gate. The one exception is `hard_enforce` mode, which requires explicit merchant opt-in and manager override capability at all times.

---

## Business

### Why OTB Fails Without a System

A buyer at a $10M retailer mentally tracks their OTB. They know roughly what they've committed, roughly what's arrived, and roughly what they have left to spend. "Roughly" is the problem. A PO submitted on a Friday afternoon that pushes the location over budget is not discovered until the invoice arrives six weeks later. By then, the cash is committed, the inventory is in transit, and the only option is to cut somewhere else or absorb the overrun. Canary solves this by making the OTB constraint visible and live — the buyer sees the remaining budget before committing the PO, not after.

### The Bitcoin Standard Encoding

OTB budget and wallet balances are stored in satoshis. This is not a Bitcoin payment architecture decision — it is a precision architecture decision. Satoshis (1/100,000,000 of a Bitcoin) provide integer arithmetic over monetary values at any scale without floating-point rounding errors. The `fx_rate_snapshot` on each wallet captures the BTC/USD rate at budget-setting time; fiat display is a conversion layer, not the stored value.

```
Fiat display = balance_sats / (100_000_000 / btc_usd_price_at_snapshot)
```

The conversion is for display only. All arithmetic — debits, credits, comparisons — runs on `BIGINT` satoshi values.

### OTB Formula

```
OTB_balance = planned_OTB_budget
            - committed_PO_value        (debits: POs in pending/approved state)
            + received_cancellations    (credits: cancelled POs)
            + realized_margin_above_plan (credits: margin performance credits, when enabled)

OTB_wallet_sats = OTB_balance expressed in satoshis at fx_rate_snapshot
```

The wallet tracks `balance_sats` (current available) and `committed_sats` (sum of all pending PO debits). `balance_sats + committed_sats` should equal `planned_budget_sats` modulo credits. The reconciliation check in `get_otb_balance` computes this invariant and alerts if it is violated.

### OTB Scope Hierarchy

The wallet can be scoped at three levels. A merchant may have wallets at multiple scopes simultaneously.

```
Merchant wallet
    └── Location wallet (location_A)
    └── Location wallet (location_B)
            └── Department wallet (dept_1 at location_B)
            └── Department wallet (dept_2 at location_B)
```

A PO debits the most specific matching wallet first. If no location-level wallet exists for the PO's location, the merchant-level wallet is debited. Scope resolution is deterministic; the purchasing service calls `debit_otb` with `scope` and `reference_id` explicitly — it does not guess.

### Enforcement Levels

The `feature.l402_enforcement_enabled` flag controls enforcement posture. This is a per-node (per-merchant-location) setting managed by the `settings` service.

| Flag Value | Behavior |
|------------|----------|
| `false` (default) | Track only. Wallet balance updated on every PO. No alerts. Silent accounting. |
| `tracking` | Track + alert at 20% and 10% remaining. No blocks. POs proceed regardless of balance. |
| `soft_enforce` | Track + alert + require manager acknowledgement on new POs when balance below 10%. Acknowledgement is a UI step; the PO is not blocked. |
| `hard_enforce` | Track + alert + block new POs when wallet is exhausted (`balance_sats <= 0`). Manager override required to proceed. |

**`hard_enforce` is the ONLY mode that can reject a PO.** Even then, manager override is always available — there is no mode where a merchant with manager credentials cannot create a PO. The platform does not own the merchant's buy decisions; it informs them.

### Business Rules

1. **L402 enforcement NEVER blocks store operations.** A zero-balance wallet with enforcement level `false` or `tracking` processes all POs without interruption. Even `hard_enforce` is overridable with manager credentials.
2. Wallet creation is idempotent per `(merchant_id, location_id, department_id, scope, period_start)`. A second call to `set_otb_budget` for the same scope and period updates the budget rather than creating a duplicate wallet.
3. `debit_otb` and `credit_otb` are strictly transactional — each call writes one `otb_transactions` row and updates `otb_wallets.balance_sats` atomically in one `pgx` transaction. The wallet balance is always consistent with the transaction log.
4. `otb_alerts` are generated by the service when the wallet crosses a threshold on debit. Alert generation is part of the `debit_otb` transaction — it does not require a separate scheduled job.
5. A wallet with `status = 'exhausted'` and enforcement level `hard_enforce` requires the purchasing service to call `acknowledge_alert` with a manager credential before the PO debit proceeds. The OTB service does not know about POs — it only knows about debits. The purchasing service is responsible for enforcing the gate.

---

## Technical

### Service Boundaries

L402-OTB owns three table groups. No other service writes to these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Wallets | `otb_wallets` | Budget position per merchant/location/department per period |
| Transactions | `otb_transactions` | Append-only debit/credit log |
| Alerts | `otb_alerts` | Threshold breach records and acknowledgement state |

### Data Model

#### `otb_wallets`

```sql
CREATE TYPE otb_scope AS ENUM ('merchant', 'location', 'department');
CREATE TYPE otb_wallet_status AS ENUM ('funded', 'warning', 'exhausted');

CREATE TABLE otb_wallets (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id          UUID NOT NULL REFERENCES merchants(id),
    location_id          UUID,                   -- NULL for merchant scope
    department_id        UUID,                   -- NULL unless scope = 'department'
    scope                otb_scope NOT NULL,
    balance_sats         BIGINT NOT NULL DEFAULT 0,
    committed_sats       BIGINT NOT NULL DEFAULT 0,  -- sum of pending PO debits
    planned_budget_sats  BIGINT NOT NULL,
    period_start         DATE NOT NULL,
    period_end           DATE NOT NULL,
    fx_rate_snapshot     NUMERIC(18,8) NOT NULL,     -- BTC/USD at budget-setting time
    status               otb_wallet_status NOT NULL DEFAULT 'funded',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_end > period_start),
    CHECK (balance_sats >= 0 OR scope = scope),   -- allow negative only in tracking mode
    UNIQUE(merchant_id, location_id, department_id, scope, period_start)
);

CREATE INDEX idx_otb_wallets_merchant ON otb_wallets(merchant_id);
CREATE INDEX idx_otb_wallets_merchant_scope ON otb_wallets(merchant_id, scope, period_start);
CREATE INDEX idx_otb_wallets_location ON otb_wallets(location_id, period_start)
    WHERE location_id IS NOT NULL;
CREATE INDEX idx_otb_wallets_status ON otb_wallets(merchant_id, status)
    WHERE status != 'funded';   -- partial index: only wallets with alerts active
```

**Note on `balance_sats` going negative:** In `tracking` and `soft_enforce` modes, POs proceed even when the wallet would go below zero. The `CHECK` constraint above is a placeholder — the actual enforcement is at the application layer based on the `l402_enforcement_enabled` flag value. Under `hard_enforce`, the application rejects the debit before it reaches the DB if it would produce a negative balance (without manager override). This check lives in the `debit_otb` handler, not in a DB constraint, because the enforcement level is a runtime configuration.

#### `otb_transactions`

Append-only. No UPDATE or DELETE.

```sql
CREATE TYPE otb_transaction_type AS ENUM ('debit', 'credit');
CREATE TYPE otb_reference_type AS ENUM ('purchase_order', 'cancellation', 'return', 'budget_update');

CREATE TABLE otb_transactions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id          UUID NOT NULL REFERENCES otb_wallets(id),
    transaction_type   otb_transaction_type NOT NULL,
    amount_sats        BIGINT NOT NULL CHECK (amount_sats > 0),
    reference_type     otb_reference_type NOT NULL,
    reference_id       UUID NOT NULL,            -- the PO, cancellation, or return ID
    balance_after_sats BIGINT NOT NULL,          -- snapshot of wallet balance after this transaction
    occurred_at        TIMESTAMPTZ NOT NULL DEFAULT now()
    -- no updated_at: INSERT-only
);

CREATE INDEX idx_otb_transactions_wallet ON otb_transactions(wallet_id, occurred_at DESC);
CREATE INDEX idx_otb_transactions_reference ON otb_transactions(reference_type, reference_id);
```

`balance_after_sats` is a running balance snapshot — each transaction records the wallet balance at the moment it posted. This makes balance reconstruction and audit straightforward without replaying the full transaction log.

#### `otb_alerts`

```sql
CREATE TYPE otb_alert_type AS ENUM ('below_20pct', 'below_10pct', 'exhausted', 'over_budget');

CREATE TABLE otb_alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id       UUID NOT NULL REFERENCES otb_wallets(id),
    alert_type      otb_alert_type NOT NULL,
    acknowledged    BOOLEAN NOT NULL DEFAULT false,
    acknowledged_by TEXT,                -- manager_id who acknowledged
    acknowledged_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(wallet_id, alert_type, acknowledged)  -- one open alert per type per wallet
);

CREATE INDEX idx_otb_alerts_wallet ON otb_alerts(wallet_id);
CREATE INDEX idx_otb_alerts_unacknowledged ON otb_alerts(wallet_id, acknowledged)
    WHERE acknowledged = false;
```

**Deduplication:** the `UNIQUE(wallet_id, alert_type, acknowledged)` constraint prevents duplicate open alerts for the same type. When a wallet crosses the 20% threshold, one `below_20pct` alert is created. If the next debit crosses 10%, a `below_10pct` alert is created. If the wallet is credited back above 10%, the `below_10pct` alert is automatically acknowledged by the `credit_otb` handler. This prevents alert noise on wallets that oscillate near a threshold.

### Threshold Computation — `debit_otb`

Alert thresholds are checked inside the `debit_otb` transaction, after the balance update:

```
pct_remaining = balance_after_sats / planned_budget_sats × 100

if pct_remaining <= 0 AND no open 'exhausted' alert:
    INSERT INTO otb_alerts (wallet_id, alert_type) VALUES ($wallet_id, 'exhausted')
    UPDATE otb_wallets SET status = 'exhausted'

elif pct_remaining <= 10 AND no open 'below_10pct' alert:
    INSERT INTO otb_alerts (wallet_id, alert_type) VALUES ($wallet_id, 'below_10pct')
    UPDATE otb_wallets SET status = 'warning'

elif pct_remaining <= 20 AND no open 'below_20pct' alert:
    INSERT INTO otb_alerts (wallet_id, alert_type) VALUES ($wallet_id, 'below_20pct')
    UPDATE otb_wallets SET status = 'warning'
```

The `ON CONFLICT DO NOTHING` clause on the alert INSERT handles the deduplication case: if an alert of this type already exists and is open, the INSERT is silently skipped.

### API Contract

All routes require JWT auth except `/otb/healthz` and `/otb/readyz`.

```
GET  /otb/healthz                              → 200
GET  /otb/readyz                               → 200 | 503
GET  /otb/balance/{wallet_id}                  → 200 {balance}
POST /otb/debit                                → 201 {transaction_id, balance_after_sats}
POST /otb/credit                               → 201 {transaction_id, balance_after_sats}
GET  /otb/history/{wallet_id}                  → 200 [{transaction}]
POST /otb/budget                               → 201 | 200 {wallet_id}  (upsert)
GET  /otb/alerts/{wallet_id}                   → 200 [{alert}]
POST /otb/alerts/{alert_id}/acknowledge        → 200 {alert}
GET  /otb/analytics/{merchant_id}              → 200 {otb_analytics}
```

### MCP Tool Surface — `canary-otb` (8 tools)

| Tool | Input | Output | SLA | Notes |
|------|-------|--------|-----|-------|
| `get_otb_balance` | `wallet_id` | `{balance_sats, committed_sats, planned_budget_sats, pct_remaining, status, fx_rate_snapshot}` | <50ms P99 | Valkey-cached with 30s TTL; invalidated on debit/credit |
| `debit_otb` | `wallet_id, amount_sats, reference_type, reference_id` | `{transaction_id, balance_after_sats, alerts_created}` | <500ms | Atomic: UPDATE wallet + INSERT transaction + INSERT alerts if thresholds crossed |
| `credit_otb` | `wallet_id, amount_sats, reference_type, reference_id` | `{transaction_id, balance_after_sats}` | <500ms | Same atomicity as debit; auto-acknowledges resolved alerts |
| `get_otb_history` | `wallet_id, limit?, before?` | `[]{transaction}` | <200ms | Cursor-based; max 200 rows per call |
| `set_otb_budget` | `merchant_id, location_id?, department_id?, scope, planned_budget_sats, period_start, period_end, fx_rate_snapshot` | `{wallet_id}` | <500ms | Upsert: creates or updates budget for the period; resets balance if new budget > old budget |
| `get_otb_alerts` | `wallet_id, acknowledged?` | `[]{alert}` | <100ms | Filter by acknowledged state; default returns unacknowledged only |
| `acknowledge_alert` | `alert_id, manager_id` | `{alert}` | <500ms | Required by purchasing service for `hard_enforce` gate; logs manager_id |
| `get_otb_analytics` | `merchant_id, from, to` | `{avg_utilization_pct, periods_over_budget, alert_frequency, top_locations}` | <2s | Aggregation over wallets + transactions |

### Trade-off: Live Balance vs. Recomputed Balance

**Option A (chosen): Stored running balance in `otb_wallets.balance_sats`, updated atomically with each transaction.** `get_otb_balance` reads the current balance directly without summing the transaction log. Fast reads (<50ms with cache), simple query, but requires transactional consistency on every debit/credit. The `balance_after_sats` snapshot on each transaction row provides an audit-ready running balance without requiring a replay.

**Option B: Compute balance as `SUM(credits) - SUM(debits)` over `otb_transactions`.** The stored balance is derived at read time; no update needed on the wallet row. Correct by construction (no possibility of the wallet row drifting from the transaction log), but expensive at read time for wallets with long histories. Rejected because `get_otb_balance` is called on every PO creation — it is a hot read path, and an aggregation query does not meet the <50ms SLA.

**Consistency guarantee for Option A:** the wallet balance update and the transaction INSERT are in a single `pgx` transaction with `FOR UPDATE` on the wallet row to prevent concurrent debit races. Two concurrent debits to the same wallet serialize at the DB row lock. This is the same pattern as `raas_chain_state` — the row lock is the serialization point.

```sql
-- Inside debit_otb transaction
SELECT balance_sats, planned_budget_sats, status
FROM otb_wallets
WHERE id = $wallet_id
FOR UPDATE;                   -- serialize concurrent debits

-- Check enforcement level (fetched from settings service before opening transaction)
-- IF hard_enforce AND balance_sats - amount_sats < 0 AND no manager override: return 402

UPDATE otb_wallets
SET balance_sats = balance_sats - $amount_sats,
    committed_sats = committed_sats + $amount_sats,
    updated_at = now()
WHERE id = $wallet_id;

INSERT INTO otb_transactions (...) VALUES (...);
-- alert threshold checks follow
```

### Go Implementation Notes

- `debit_otb` must fetch the enforcement level from the `settings` service before opening the DB transaction. Cache this setting in Valkey with a 60s TTL — it changes rarely and does not need to be fetched on every request.
- `set_otb_budget` is an upsert: `INSERT ... ON CONFLICT (merchant_id, location_id, department_id, scope, period_start) DO UPDATE SET planned_budget_sats = EXCLUDED.planned_budget_sats, fx_rate_snapshot = EXCLUDED.fx_rate_snapshot, updated_at = now()`. When the budget is updated upward, `balance_sats` is increased by the delta. When updated downward, `balance_sats` is decreased — but if this would make balance negative, log a warning and set balance to 0 (the merchant has already over-committed).
- Satoshi arithmetic: all computations are `int64`. Never use `float64` for balance arithmetic. The `fx_rate_snapshot` is `NUMERIC(18,8)` for storage; convert to `float64` only for display layer computation, never for balance math.
- Fiat display conversion: `fiat_display = float64(balance_sats) / float64(100_000_000) * fx_rate_snapshot`. Round to 2 decimal places for USD display. This conversion is performed in the HTTP response handler, not stored.
- `credit_otb` auto-acknowledges open alerts when the post-credit balance crosses back above the alert threshold: if balance crosses back above 10%, acknowledge any open `below_10pct` alert; above 20%, acknowledge `below_20pct`. This prevents merchants from seeing stale alerts after a budget credit. The acknowledgement uses `acknowledged_by = "system"` to distinguish automatic from manager acknowledgements in the audit trail.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `get_otb_balance` (Valkey hit) | <5ms | <20ms | 50ms | Alert; check Valkey latency |
| `get_otb_balance` (DB fallback) | <20ms | <100ms | 300ms | Alert |
| `debit_otb` | <100ms | <500ms | 2s | Alert; check wallet row lock contention |
| `credit_otb` | <100ms | <500ms | 2s | Alert |
| `acknowledge_alert` | <100ms | <500ms | 2s | Alert |
| `get_otb_analytics` | <500ms | <2s | 10s | Alert |

### Health Endpoints

```
GET /otb/healthz
→ 200 { "status": "ok" }
Shallow liveness. No DB or Valkey check.

GET /otb/readyz
→ 200 { "status": "ok", "db_ok": true, "valkey_ok": true, "settings_reachable": true, "wallet_count": N }
→ 503 if DB unreachable.
Note: settings service unreachability does not cause 503 — enforcement level
is cached in Valkey and falls back to 'tracking' mode if settings is down.
Enforcement level fallback is logged and metered.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| DB unreachable | All write tools return 503. `get_otb_balance` falls back to Valkey for cached balances. | Auto-recovery via pgx pool. |
| Valkey unreachable | `get_otb_balance` falls back to DB. Write operations proceed (no Valkey dependency on writes). | Log warning; continue. |
| `settings` service unreachable | `debit_otb` falls back to cached enforcement level (Valkey, 60s TTL). If cache is also stale, defaults to `tracking` mode — PO proceeds. | Log and alert on enforcement level fallback events > 5/min. |
| Concurrent debit race | DB row lock on `otb_wallets` serializes concurrent debits. Second writer blocks until first commits. No data loss. | Monitor for elevated lock wait times on `otb_wallets`. |
| `hard_enforce` gate on exhausted wallet | `debit_otb` returns 402 Payment Required with `{wallet_id, balance_sats: 0, enforcement: "hard_enforce"}`. Purchasing service must surface this to the buyer and request manager override. | Purchasing calls `acknowledge_alert` with manager credentials, then retries `debit_otb` with `manager_override: true` flag in the request. |

### Graceful Shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
srv.Shutdown(ctx) // 30s drain
pool.Close()
valkey.Close()
```

In-flight `debit_otb` and `credit_otb` calls that are mid-transaction will roll back cleanly. The wallet row lock is released on rollback; no balance corruption is possible.

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `otb:balance:{wallet_id}` | 30s | Cached wallet balance for `get_otb_balance` hot path |
| `otb:enforcement:{merchant_id}:{location_id}` | 60s | Cached enforcement level from settings service |

Short TTL on balance cache (30s) is intentional: OTB balance is a financial instrument and must not be stale for too long. A 30s window is acceptable given that PO creation is a deliberate human action, not a sub-second automated flow.

### Monitoring

Alert on:
- `debit_otb` P99 > 500ms sustained for 2 minutes (wallet row contention)
- `feature.l402_enforcement_enabled` fallback rate > 5 events/minute (settings service degraded)
- `otb_wallets WHERE status = 'exhausted' AND merchant_id = X` count increasing without corresponding `credit_otb` activity — merchant may be stuck
- `otb_alerts WHERE acknowledged = false AND created_at < NOW() - INTERVAL '48 hours'` — unacknowledged alert queue is aging; merchant may not have visibility

---

## Compliance

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `acknowledged_by` | `otb_alerts` | Internal | Manager employee ID. Do not expose in merchant-facing analytics. |
| `reference_id` | `otb_transactions` | Operational | PO or return ID. Not PII, but links to financial instruments — retain per financial record rules. |

### Bitcoin Standard Note

The satoshi encoding is an intentional precision decision, documented here for auditors:

All monetary values in `otb_wallets` and `otb_transactions` are stored as `BIGINT` satoshis. The conversion factor is `1 BTC = 100,000,000 satoshis`. The `fx_rate_snapshot` captures the BTC/USD exchange rate at the time the budget was set. Fiat display values shown in the UI are computed at request time using this snapshot rate and are not stored. This means two merchants with the same `balance_sats` but different `fx_rate_snapshot` values will see different USD equivalents — this is correct behavior, not a bug.

Auditors comparing stored satoshi values to fiat financial records must apply the `fx_rate_snapshot` conversion to produce the fiat equivalent at budget-setting time.

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `otb_wallets` | 7 years after period end | Financial planning record |
| `otb_transactions` | 7 years | AP audit trail; links to PO financial records |
| `otb_alerts` | 7 years | Governance record: when was merchant notified of budget exhaustion |

---

## Related SDDs

- `identity.md` — `merchants` and `locations` FK chain
- `settings.md` — owns `feature.l402_enforcement_enabled` flag; queried by `debit_otb` to determine enforcement posture
- `ildwac.md` — WAC cost basis feeds the realized-margin-above-plan credit calculation; OTB position includes margin performance credits when this feature is enabled
- `purchasing.md` — calls `debit_otb` before PO commitment; calls `acknowledge_alert` with manager credentials when `hard_enforce` returns 402; calls `credit_otb` on PO cancellation
- `hawk.md` — receives budget exhaustion signals for merchant health alerting
- `owl.md` — consumes OTB analytics for merchant financial health reporting
- `raas.md` — `feature.l402_enforcement_enabled` flag relationship described in raas.md Blockchain Anchor section; same governing rule: no operation is ever blocked by L402 wallet state without explicit merchant opt-in
