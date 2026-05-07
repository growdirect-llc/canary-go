---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis
status: handoff-ready
updated: 2026-04-29
binary: store-network-integrity
port: 9088
mcp-server: canary-sni
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Store Network Integrity

**Type:** Analytics Service — Multi-Store Cross-Location Anomaly Detection  
**Binary:** `cmd/store-network-integrity` → `:9088`  
**MCP server:** `canary-sni` (6 tools)  
**Depends on:** `tsp` (transaction data), `chirp` (alert generation), `hawk` (case linkage)  
**Feeds:** `hawk` (cases with `hawk_case_id`), `chirp` (cross-store alert signals)

Store Network Integrity (SNI) runs cross-location correlation jobs and publishes alerts when a single store deviates from network norms. Anomalies that are invisible at the individual store level become visible when stores are compared against each other.

---

## Business

### The Single-Store Blind Spot

Most loss prevention analytics are store-local: compare this week to last week at the same location. That catches temporal anomalies but misses network-level manipulation. A store manager who trains their team to process voids in a particular way will show elevated void rates week-over-week — but those rates will be elevated consistently, producing no alert. SNI exposes this by comparing the store to its peers.

The principle is directionally similar to network fraud detection in payments: a single transaction that looks normal in isolation becomes suspicious when correlated with the broader pattern. SNI applies the same logic to store operations.

### Detection Signals

| Signal | Threshold | What it catches |
|---|---|---|
| Void rate deviation | Store void rate > 2σ above network mean | Systematic register manipulation or management pressure on void approvals |
| Return rate deviation | Store return rate > 2σ above network mean | Sweethearting via returns, refund fraud, return policy exploitation |
| Transaction count anomaly | Store txn count drops > 30% vs same-day prior week | Under-reporting, system failure, or intentional transaction suppression |
| Inventory shrink concentration | Store shrink rate > 3× network average in same category | Theft concentration, receiving fraud, or inventory manipulation in specific category |
| Receiving discrepancy concentration | One location accounts for > 50% of all network discrepancies | Systematic receiving fraud or vendor collusion at one location |
| Price deviation | Store selling at prices > 5% below network average | Scan fraud, price override abuse, or unauthorized markdown |
| Cross-store return fraud | Same customer returning across multiple locations in same week | Organized return fraud using the same account across the network |

Thresholds are configurable via env vars with the defaults above. Each threshold has a corresponding `sni_config` row per merchant — merchants can tune sensitivity to their network characteristics.

---

## Data Model

All tables in the `app` schema.

### `sni_baselines`

Computed norms per metric per location. Recomputed nightly by the `baseline_compute` job.

```sql
CREATE TABLE app.sni_baselines (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    metric_type     TEXT        NOT NULL,
    -- 'void_rate' | 'return_rate' | 'txn_count' | 'shrink_rate' |
    -- 'receiving_discrepancy_share' | 'price_deviation'
    location_id     UUID        REFERENCES app.locations(id),
    -- null = network-level aggregate (used as the baseline for per-location comparison)
    mean            FLOAT8      NOT NULL,
    std_dev         FLOAT8      NOT NULL,
    sample_size     INT         NOT NULL,
    -- Number of data points used to compute this baseline
    sample_period   TEXT        NOT NULL,
    -- e.g., 'trailing_30d' | 'trailing_90d'
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sni_baselines_merchant_metric ON app.sni_baselines
    (merchant_id, metric_type, computed_at DESC);
CREATE INDEX idx_sni_baselines_location ON app.sni_baselines
    (merchant_id, location_id, metric_type, computed_at DESC)
    WHERE location_id IS NOT NULL;
```

### `sni_alerts`

Cross-location anomaly alerts. Linked to Hawk cases when the anomaly warrants investigation.

```sql
CREATE TABLE app.sni_alerts (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID        NOT NULL REFERENCES app.merchants(id),
    location_id      UUID        NOT NULL REFERENCES app.locations(id),
    alert_type       TEXT        NOT NULL,
    -- mirrors metric_type values above; plus 'cross_store_return_fraud'
    metric_value     FLOAT8      NOT NULL,
    -- The store's actual metric value at detection time
    baseline_value   FLOAT8      NOT NULL,
    -- The network mean used as the comparison baseline
    deviation_sigma  FLOAT8      NOT NULL,
    -- How many standard deviations above the mean (positive = above, negative = below)
    hawk_case_id     UUID        REFERENCES app.hawk_cases(id),
    -- Set when this alert is escalated to a Hawk investigation
    acknowledged     BOOL        NOT NULL DEFAULT false,
    acknowledged_by  UUID        REFERENCES app.users(id),
    acknowledged_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sni_alerts_merchant_unack ON app.sni_alerts
    (merchant_id, created_at DESC) WHERE acknowledged = false;
CREATE INDEX idx_sni_alerts_location ON app.sni_alerts
    (merchant_id, location_id, created_at DESC);
CREATE INDEX idx_sni_alerts_type ON app.sni_alerts
    (merchant_id, alert_type, created_at DESC);
```

---

## Jobs

Three scheduled jobs. All run via the platform's Bull job queue (Valkey-backed).

### `baseline_compute` — Nightly

Computes per-metric baselines for each merchant's location network.

**Schedule:** Nightly, 02:00 local time per merchant timezone  
**Inputs:** `sales.transactions`, `sales.line_items`, `app.inventory_adjustments`, `app.receiving_records`  
**Outputs:** `sni_baselines` rows (latest computation replaces previous for same `merchant_id + metric_type + location_id`)

**Computation approach:**
1. For each merchant with SNI enabled, fetch trailing 30-day data per location
2. Compute metric values per location per day (e.g., void count / total txn count = void rate)
3. Compute network mean and standard deviation across all locations for the same metric
4. Insert `sni_baselines` rows — one network-level row (null location) and one per-location row per metric

Minimum sample threshold: locations with fewer than 7 days of data in the period are excluded from baseline computation (insufficient sample). A WARNING is logged but no alert is raised.

### `deviation_scan` — Every 4 Hours During Business Hours

Compares current metrics to the most recent baselines.

**Schedule:** Every 4 hours, 06:00–22:00 local time per merchant timezone  
**Inputs:** `sales.transactions` (current period), `sni_baselines` (latest nightly computation)  
**Outputs:** `sni_alerts` rows for deviating locations

**Scan logic per metric:**
1. Compute current metric value for the location (rolling 24h window)
2. Load the most recent `sni_baselines` row for (merchant, metric_type, location)
3. Compute: `deviation_sigma = (current_value - baseline.mean) / baseline.std_dev`
4. Apply threshold (default: 2σ for rate metrics, 30% drop for count metrics, 50% share for concentration metrics)
5. If threshold exceeded and no open unacknowledged alert exists for this (location, alert_type): INSERT `sni_alerts`

Deduplication: only one open alert per (merchant, location, alert_type) at a time. A new alert is not inserted if an unacknowledged alert of the same type already exists for that location.

### `cross_store_fraud_scan` — Daily

Identifies customers making returns across multiple locations in the same week.

**Schedule:** Daily, 04:00 UTC  
**Inputs:** `sales.transactions` (transactions of type `return` or `refund`), `app.customers`  
**Outputs:** `sni_alerts` rows with `alert_type = 'cross_store_return_fraud'`

**Scan logic:**
1. Query returns in the trailing 7 days, grouped by `customer_id`
2. Identify customers with returns at ≥ 2 distinct `location_id` values in the period
3. Calculate total return value across the pattern
4. Insert `sni_alerts` if the pattern hasn't already been flagged this week for that customer

---

## MCP Tools (`canary-sni` — `/sni/*`)

6 tools registered with the MCP tool registry.

| Tool | Required Params | Description |
|---|---|---|
| `get_network_health` | `merchant_id` | Summary view: number of locations, how many have open alerts, which metric types are currently flagged, most recent baseline computation time. |
| `get_location_deviation` | `merchant_id`, `location_id` | Current deviation values for all metric types at one location vs. network baseline. Shows sigma values and whether each metric is within threshold. |
| `get_sni_alerts` | `merchant_id` | List unacknowledged alerts. Optional filters: `location_id`, `alert_type`, `min_sigma`. Ordered by deviation magnitude descending. |
| `get_baseline_metrics` | `merchant_id`, `metric_type` | Return the current baselines for a given metric across all locations. Useful for understanding the network distribution before investigating an alert. |
| `acknowledge_sni_alert` | `alert_id`, `user_id` | Mark an alert as acknowledged. Optionally links to a `hawk_case_id` if the alert is being escalated. |
| `run_cross_store_fraud_scan` | `merchant_id` | Manually trigger the cross-store return fraud scan for a merchant (outside normal schedule). Returns new alerts created. |

---

## API Contract

### MCP Blueprint Endpoints

**Base path:** `/sni`

| Method | Path | Auth | Description |
|---|---|:---:|---|
| GET | `/sni/manifest` | No | Server manifest (name, version, tool count) |
| GET | `/sni/tools` | No | List all 6 tools with schemas |
| POST | `/sni/tools/<name>` | JWT | Invoke a tool by name |
| GET | `/sni/health` | No | Service health check |

**Health response:**
```json
{
  "service": "canary-sni",
  "healthy": true,
  "tools": 6,
  "last_baseline_compute": "2026-04-29T02:00:00Z",
  "open_alerts": 3,
  "next_deviation_scan": "2026-04-29T14:00:00Z"
}
```

---

## Operations

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| PostgreSQL down | All jobs and tools fail | Jobs log failure and retry on next scheduled run; tools return 503 |
| Insufficient baseline data | Deviation scan skips metric | WARNING logged; no alert generated for that metric at that location |
| Valkey down | Job queue unavailable | Jobs do not fire; log ERROR; requires manual `run_cross_store_fraud_scan` until queue recovers |
| Hawk unavailable during escalation | Alert created without case link | Alert row created with `hawk_case_id = null`; can be linked manually via `acknowledge_sni_alert` later |

### Configuration

| Env Var | Default | Description |
|---|---|---|
| `SNI_VOID_RATE_SIGMA_THRESHOLD` | `2.0` | Sigma above mean to trigger void rate alert |
| `SNI_RETURN_RATE_SIGMA_THRESHOLD` | `2.0` | Sigma above mean to trigger return rate alert |
| `SNI_TXN_COUNT_DROP_THRESHOLD` | `0.30` | Fractional drop vs prior week to trigger txn count alert |
| `SNI_SHRINK_RATE_MULTIPLE_THRESHOLD` | `3.0` | Multiple of network average to trigger shrink concentration alert |
| `SNI_RECEIVING_SHARE_THRESHOLD` | `0.50` | Share of network discrepancies to trigger receiving alert |
| `SNI_PRICE_DEVIATION_THRESHOLD` | `0.05` | Fraction below network average to trigger price deviation alert |
| `SNI_BASELINE_SAMPLE_PERIOD_DAYS` | `30` | Rolling window for baseline computation |
| `SNI_MIN_SAMPLE_DAYS` | `7` | Minimum days of data before a location is included in baselines |

---

## Relationship to Chirp

Chirp handles rule-based single-event detection (a single transaction triggers a rule). SNI handles pattern-based cross-location detection (aggregated metrics over time, compared across the network). They are complementary:

- A single suspicious void at one register → Chirp alert
- A store whose void rate is 2σ above the network → SNI alert
- Both can feed Hawk cases independently

SNI alerts are not routed through Chirp's rule engine. They are inserted directly into `sni_alerts` and surfaced via the SNI MCP tool surface.

---

## Related SDDs

- **chirp.md** — single-event detection; SNI is the cross-location complement
- **hawk.md (hawk-case-management.md)** — SNI alerts can be escalated into Hawk investigations via `hawk_case_id`
- **tsp.md** — transaction ingestion pipeline; SNI reads from `sales.transactions`
- **go-module-layout.md** — service structure, package conventions, deployment
