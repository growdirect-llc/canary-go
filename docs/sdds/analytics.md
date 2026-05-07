---
spec-version: 1.1
updated: 2026-04-28
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: handoff-ready
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Analytics — KPI Dashboard + Velocity Engine

**Service type:** Domain service embedded in the Canary application process
**MCP server name:** `canary-analytics` (7 tools)

---

## Purpose

Analytics is Canary's read-heavy metrics domain. It owns KPI dashboard orchestration, heatmap band scoring, velocity anomaly detection, period aggregation (NRF 4-5-4 fiscal calendar), and risk entity ranking. The domain answers "how is this merchant doing?" by combining pre-computed period aggregates with two statistical scoring engines (heatmap + velocity).

No external API dependencies. Analytics reads entirely from tenant operational schemas and serves data to REST endpoints, MCP tools, and the Owl AI engine.

**Multi-tenant context.** Per-merchant rollups live in `tenant_{merchant_id}.metrics_*` tables. Analytics is also the **owner of the cross-tenant `analytics` schema** — it runs the scheduled jobs that populate cross-tenant materialized views (industry benchmarks, platform-wide KPI comparisons, anonymized peer rollups). The cross-tenant rollups never expose row-level tenant data; they expose aggregates only. Admin queries hit the `analytics` schema for benchmarks; merchant queries hit their own tenant schema for their KPIs. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Analytics operates with all Optional Features (per `platform-overview.md`) disabled. KPI computation, heatmap scoring, velocity detection all run on standard receipt-event data. When `ILDWAC_ENABLED=true`, additional analytics surfaces become available — cost-per-action by Device, MCP, and Port dimensions; device-level profitability; agent-attributed cost accounting. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, KPI rollup outputs are eligible for public anchoring as merchant performance attestations. Neither is required for the core KPI dashboard to function.

---

## Dependencies

| Dependency | Required | Notes |
|---|:---:|---|
| PostgreSQL (`canary` DB, `metrics` schema) | Yes | |
| PostgreSQL (`canary` DB, `app` schema — employees, locations, alerts) | Yes | Name lookups, alert counts |
| Valkey (DB 0) | No | No caching in prototype; required before GA (see P1-ANA-6) |
| Chirp (detection service) | No | Consumes Chirp output via alert_count columns; no direct call |
| Alert Lifecycle service | Yes | lifecycle_summary for dashboard alert context |

---

## Data Flow & PII Map

### What Enters (reads)

| Source | Data |
|---|---|
| `metrics.period_metrics` | Pre-aggregated KPIs per merchant × location × fiscal period |
| `metrics.employee_period_metrics` | Per-employee KPIs per fiscal period |
| `metrics.daily_metrics` | Daily fact rows (consumed by period aggregation) |
| `metrics.employee_daily_metrics` | Per-employee daily facts |
| `metrics.metric_baselines` | Statistical baselines (mean, stddev, sample_size) |
| `metrics.scorecard_thresholds` | Per-merchant band configuration |
| `app.employees` | Employee name lookups (sensitive — see PII note) |
| `app.locations` | Location name lookups |

### What Analytics Writes

Analytics owns writes to `period_metrics` and `employee_period_metrics` via the period aggregation pipeline. All other metrics tables are written by upstream services.

### PII Classification

| Field | Classification | Notes |
|---|:---:|---|
| `merchant_id` | internal | Tenant key |
| `employee_id` | internal | POS-native employee ID |
| `employee_name` | **sensitive** | Looked up for display in risk rankings; encrypt at rest (P0-ANA-1) |
| `location_id` | internal | |
| `location_name` | internal | |
| `entity_id` | internal | Employee, card, device, or location |
| `risk_score` | internal | 0.0–1.0 float |
| `contributing_factors` | internal | JSON array of scoring factors |

**PII exposure path:** Employee names are fetched from `app.employees` and returned in the `top-risks` and `drilldown` API responses. Name fields must be encrypted at rest in the `employees` table; decryption occurs only at the presentation layer for authenticated callers, with PII access logged.

---

## API Contract

### REST Endpoints

All endpoints require JWT authentication and are merchant-scoped via `merchant_id` extracted from the JWT. Callers must not pass a `merchant_id` parameter that differs from the JWT claim.

**Base path:** `/api/analytics`

| Method | Path | Description | Returns |
|---|---|---|---|
| GET | `/api/analytics/dashboard` | L1 period summary | health_score, scored KPI bands, anomaly count, top concern, alert summary |
| GET | `/api/analytics/trends?metric=REFUND_RATE` | L1 temporal sparkline | Array of period values for one metric |
| GET | `/api/analytics/top-risks` | L2-3 entity ranking | Top 5 employees + top 5 locations by risk score |
| GET | `/api/analytics/drilldown?entity_type=employee&entity_id=xxx` | L3-4 entity detail | All scored metrics for a single entity |
| GET | `/api/analytics/export` | All levels | **Not implemented** — return 501 or remove before GA |

**Error behavior:** Return 204 (No Content) when no period metrics exist for the merchant. Return 200 with payload when data exists. Never return `null` as a 200 body.

### MCP Tools (`canary-analytics`, 7 tools)

**Base path:** `/analytics`

| Tool | DB? | PII Access | Description |
|---|:---:|---|---|
| `get_dashboard` | Yes | merchant_id | Period summary — health score, scored KPI bands, anomaly count, top concern |
| `get_top_risks` | Yes | employee_name, employee_id | Top 5 employees and locations ranked by aggregate band penalties |
| `get_trends` | Yes | merchant_id | Sparkline data for one metric across N periods (1-13) |
| `get_drilldown` | Yes | employee_id or location_id | All scored metrics for a single employee or location |
| `get_period_metrics` | Yes | merchant_id | Raw KPI actuals for a fiscal period (unscored) |
| `score_metrics` | No | None | Pure: score actuals dict against baselines dict through heatmap engine |
| `detect_velocity` | No | None | Pure: z-score anomaly detection from current_value + historical_values array |

**MCP tenant isolation (P0-ANA-2):** MCP tool handlers must validate `merchant_id` against the authenticated context. Reject any request where the caller-supplied `merchant_id` differs from the JWT-bound `merchant_id` unless the caller holds admin privileges.

### Dashboard Response Shape

```json
{
  "fiscal_year": 2026,
  "fiscal_period": 3,
  "period_label": "P3 FY2026",
  "health_score": 75.0,
  "anomaly_count": 2,
  "top_concern": "Refund Rate: investigate (285% of baseline)",
  "alert_summary": {},
  "metrics": [
    {
      "name": "REFUND_RATE",
      "label": "Refund Rate",
      "actual": 4.2,
      "baseline": 1.5,
      "pct_of_baseline": 280.0,
      "band": "investigate",
      "color": "#ef4444",
      "format": "percent",
      "is_alert_worthy": true
    }
  ]
}
```

### Drill-Down Chain (5 levels)

| Level | Source Table | Description |
|---|---|---|
| L1 | `period_metrics` (aggregate) | Merchant-wide metrics for a fiscal period |
| L2 | `period_metrics` WHERE location_id | Same metrics split by location |
| L3 | `employee_period_metrics` WHERE location_id | Per-employee metrics within a location |
| L4 | `daily_metrics` / `employee_daily_metrics` | Per-day metrics for a specific entity |
| L5 | `transactions` WHERE date + entity | Individual transactions. Evidence ref: `raas:{merchant_id}:{source_table}:{source_id}` |

---

## Data Model

All Analytics-owned tables in the `metrics` schema (`canary_metrics`). 20 tables, star schema.

### Monetary Convention

All monetary columns store **integer cents** (e.g., `amount_cents BIGINT`). Convert to decimal at the presentation layer by dividing by 100. Never store fractional cents.

### Fact Tables (6)

**`daily_metrics`** — Grain: merchant × location × date. 18+ metric columns.
```sql
UNIQUE (merchant_id, location_id, metric_date)
INDEX (merchant_id, metric_date)
```

**`hourly_metrics`** — Grain: merchant × location × date × hour (0-23). Intraday pattern analysis and velocity profiling.

**`period_metrics`** — Grain: merchant × location × fiscal period. Aggregated from `daily_metrics` by the period aggregation job. Includes SRA columns.
```sql
UNIQUE (merchant_id, location_id, fiscal_year, fiscal_period)
INDEX (merchant_id, fiscal_year, fiscal_period)
```

**`employee_daily_metrics`** — Per-employee daily. Adds `employee_id`, `risk_score_snapshot`, off-clock flags, discount decomposition.

**`employee_period_metrics`** — Per-employee period. Adds `employee_id`, `off_clock_days`, `avg_risk_score`, `max_risk_score`, `sales_per_hour_cents`.

**`product_daily_metrics`** — Per-product daily: `catalog_object_id`, `item_name`, `units_sold`, `revenue_cents`.

### Dimension Tables (3)

**`dim_date`** — PK: `date_key` (DATE). Calendar + fiscal attributes. `fiscal_period` computed at query time via the fiscal calendar engine.

**`dim_location`** — SCD Type 2. Location attributes mirrored at measurement time.

**`dim_employee`** — SCD Type 2. Employee attributes mirrored at measurement time.

### Risk & Scoring Tables (4)

**`entity_risk_scores`** — Current risk per entity (employee/card/device/location). SCD Type 1 (overwritten on update).

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `merchant_id` | UUID NOT NULL | |
| `entity_id` | TEXT NOT NULL | POS-native entity identifier |
| `entity_type` | TEXT NOT NULL | `employee`, `card`, `device`, `location` |
| `risk_score` | FLOAT NOT NULL | 0.0–1.0 |
| `risk_category` | TEXT NOT NULL | `low`, `medium`, `high`, `critical` |
| `factors` | JSONB | Array of scoring factor objects |
| `scored_at` | TIMESTAMPTZ | |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

**`risk_score_history`** — Append-only timeline of risk scores for trend analysis.

**`scorecard_thresholds`** — Per-merchant, per-metric threshold configuration.
```sql
UNIQUE (merchant_id, metric_name)
```
21 default metrics seeded on merchant onboarding (idempotent). See Heatmap Scoring Engine for threshold semantics.

**`dashboard_config`** — Per-merchant dashboard configuration: layout, refresh interval, display toggles.
```sql
UNIQUE (merchant_id)
```

### Baseline Tables (2)

**`metric_baselines`** — Statistical baselines (mean, stddev, sample_size) per merchant per metric. Used by heatmap scoring.

**`velocity_baselines`** — Rate-of-change baselines for velocity engine. Keyed by merchant × metric × day_of_week × hour_of_day.

### ML Feature Store (3) — future use

**`transaction_features`**, **`feature_definitions`**, **`ml_models`** — Feature vectors and model registry. Not used by current analytics domain.

### Scorecards (2)

**`weekly_scorecard`**, **`monthly_scorecard`** — Aggregated KPI snapshots (JSONB `kpis` column).

---

## Scoring Algorithms

### Health Score

Health starts at 100 and subtracts penalties per non-normal band across the 14 summary metrics.

```
score = 100 - sum(band_penalties)
score = max(0, score)
```

| Band | Penalty | Color |
|---|:---:|---|
| `normal` | 0 | `#22c55e` (green) |
| `watch` | 5 | `#eab308` (yellow) |
| `review` | 15 | `#f97316` (orange) |
| `investigate` | 30 | `#ef4444` (red) |
| `insufficient_data` | 0 | `#94a3b8` (slate) |

Minimum transaction threshold: 20 transactions per period required for reliable scoring. Below this, return `insufficient_data` band for all metrics.

### Heatmap Scoring Engine

Classifies each metric into a band by comparing `actual` against `baseline`:

**higher_is_worse metrics (19 metrics):**

| pct_of_baseline | Band |
|---|---|
| < 100% | `normal` |
| 100–110% | `watch` |
| 110–120% | `review` |
| > 120% | `investigate` |

**lower_is_worse metrics (2 metrics: GROSS_SALES, TRANSACTION_COUNT):** Thresholds inverted — below 90% of baseline = `watch`, below 80% = `review`, below 70% = `investigate`.

**Zero baseline:** `investigate` if actual > 0 and metric is higher_is_worse; `normal` otherwise.

Score all metrics and sort results by severity (investigate first). `seed_default_thresholds()` creates the 21 default threshold rows on merchant onboarding (idempotent on conflict).

### Velocity Anomaly Detection

Poisson-process z-score anomaly detection. Operates on a sliding window of historical values.

**Baseline computation:**
```
mean = sum(values) / n
variance = sum((x - mean)^2) / (n - 1)   // sample variance
std_dev = sqrt(variance)
```
Reliable if `n >= 7`.

**Anomaly classification:**
```
z_score = (current_value - mean) / std_dev

spike:  z_score > sigma_threshold
drop:   z_score < -sigma_threshold
zero:   current_value == 0 AND mean > 0
none:   within normal range
```

Default `sigma_threshold = 2.0`.

**Confidence by sample count:**

| Sample count | Confidence |
|---|---|
| < 7 | 0.0 |
| 7–13 | 0.5 |
| 14–29 | 0.7 |
| 30–59 | 0.85 |
| ≥ 60 | 0.95 |

**Time-aware baselines (VelocityProfile):** Priority order: hourly > daily > overall. Use the highest-resolution baseline available for the entity being scored.

**Batch check:** `check_velocities()` evaluates multiple metrics against their baselines and returns only the anomalous results.

---

## Workflows

### Period Aggregation Pipeline

**Trigger:** Scheduled job — run once per day per merchant, or on-demand after large data imports.

**Steps:**
1. Read merchant fiscal calendar settings: `calendar_type`, `anchor_month`, `week_start_day`, `fiscal_pattern`.
2. For the target fiscal year: DELETE existing period rows for this merchant (full rebuild — not incremental).
3. Re-INSERT from `daily_metrics` using SUM aggregation by fiscal period.
4. Aggregate both merchant-level and employee-level facts.
5. Compute SRA v2 (4 components).
6. Return: `merchant_id`, `fiscal_year`, `periods_created`, `employee_periods_created`, `pattern`.

**Transaction boundary:** The aggregation job operates within a single transaction. The caller controls commit/rollback. Empty daily data returns `periods_created = 0`.

**SQL pattern (delete + re-insert):**
```sql
DELETE FROM metrics.period_metrics
WHERE merchant_id = $1 AND fiscal_year = $2;

INSERT INTO metrics.period_metrics (merchant_id, location_id, fiscal_year, fiscal_period, ...)
SELECT
  merchant_id,
  location_id,
  $2 AS fiscal_year,
  get_fiscal_period(metric_date, $anchor_month, $fiscal_pattern) AS fiscal_period,
  SUM(gross_sales_cents) AS gross_sales_cents,
  ...
FROM metrics.daily_metrics
WHERE merchant_id = $1
  AND metric_date BETWEEN $year_start AND $year_end
GROUP BY merchant_id, location_id, fiscal_period;
```

### Dashboard Build Pipeline

**Trigger:** `GET /api/analytics/dashboard` or `get_dashboard` MCP tool invocation.

```
Step 1: get_current_fiscal_period(merchant_id)
        → Most recent period_metrics row; estimate from calendar date if none

Step 2: fetch_period_actuals(merchant_id, fiscal_year, fiscal_period)
        → Aggregate across locations: SUM counts/amounts, MAX for unique entity counts
        → Compute derived metrics:
            REFUND_RATE = refund_count / transaction_count
            VOID_RATE = void_count / transaction_count
            AVG_TRANSACTION = gross_sales_cents / transaction_count
            SRA_PCT_SALES = sra_amount_cents / gross_sales_cents

Step 3: fetch_baselines(merchant_id)
        → {metric_name: baseline_value} dict

Step 4: fetch_thresholds(merchant_id)
        → {metric_name: {normal_pct, watch_pct, review_pct}} dict
        → Fall back to DEFAULT_THRESHOLDS (21 metrics) if none configured for merchant

Step 5: build_period_summary(actuals, baselines, thresholds, fiscal_year, fiscal_period)
        → score_all_metrics() — classify each metric, sort by severity
        → Filter to SUMMARY_METRICS (14 metrics)
        → compute_health_score() — 100 minus band penalties
        → lifecycle_summary() — alert context from Alert Lifecycle service
        → find_top_concern() — worst band, highest pct_of_baseline

Step 6: serialize → JSON-safe dict matching Dashboard Response Shape
```

**Caching (required before GA, P1-ANA-6):** Cache the complete dashboard response in Valkey with key `analytics:dashboard:{merchant_id}`, TTL 60 seconds. Invalidate on period aggregation completion.

---

## Fiscal Calendar Engine

Two calendar modes, configured per merchant in `MerchantSettings`.

| Calendar Type | Periods/Year | Period Labels |
|---|:---:|---|
| `nrf_454` (default) | 13 (52–53 weeks) | P1–P13, FY2026 |
| `calendar_month` | 12 | January 2026 |

**NRF 4-5-4 parameters:**

| Parameter | Default | Constraints |
|---|---|---|
| `anchor_month` | 2 (February) | 1–12 |
| `week_start_day` | 6 (Sunday) | 0–6 (0=Monday) |
| `fiscal_pattern` | [4, 5, 4] | Must sum to 13 |

Week 53 maps to P13 — the last period absorbs the extra week.

`backfill_dim_date()` populates the `dim_date` table in batches of 500 rows. Raise a validation error for out-of-range parameters.

---

## Error Handling

| Condition | Behavior |
|---|---|
| Empty period actuals | Return health_score=100.0, empty metrics list |
| Missing baselines | score_metric() treats baseline as 0.0; higher_is_worse metrics with actual>0 score as `investigate` |
| No thresholds configured | Fall back to DEFAULT_THRESHOLDS (21 metrics) |
| Invalid fiscal calendar | Validate at config write time; raise error with message (pattern must sum to 13, anchor_month 1-12, week_start_day 0-6) |
| Period aggregation partial failure | Caller rolls back transaction; return periods_created=0 |
| Unreliable velocity baseline (< 7 samples) | Return is_anomaly=false, confidence=0.0 |
| Zero std_dev with deviation | Return infinite z-score; classify as anomaly |
| Employee name lookup failure | Return employee_id in place of name; do not propagate the error |

---

## Operations

### Health Check

**MCP endpoint:** `GET /analytics/health` — returns service name, healthy status, tool count.

**REST:** No dedicated health endpoint. Canary application health covers Analytics by default.

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| PostgreSQL down | Dashboard returns no data | Return `null`/empty with 503 |
| No `period_metrics` rows | Dashboard empty state | Return 204 No Content |
| No baselines configured | All metrics score against zero baseline | Dashboard renders with `insufficient_data` bands |
| No thresholds configured | Default thresholds apply | Transparent to caller |
| Period aggregation partial failure | Caller controls rollback | `periods_created: 0` returned |

### Monitoring

**Alert on:**
- Dashboard API response time > 2s (multiple cross-schema joins)
- Period aggregation produces 0 periods for a merchant with daily data
- Velocity engine confidence consistently 0.0 across merchants (insufficient baseline data)
- Health score drops below 30 for multiple merchants simultaneously

**Normal operating range:**
- Health scores 50–100 for active merchants
- 1–3 velocity anomalies per period per merchant
- Period aggregation rebuilds are delete + re-insert (not incremental — this is expected)

### Configuration

| Setting | Purpose | Default |
|---|---|---|
| `calendar_type` | `nrf_454` or `calendar_month` per merchant | `nrf_454` |
| `anchor_month` | Fiscal year start month | 2 (February) |
| `week_start_day` | Week start day | 6 (Sunday) |
| `fiscal_pattern` | Period grouping | [4, 5, 4] |
| `refresh_interval_seconds` | Dashboard auto-refresh | 300 |
| `MIN_TRANSACTION_THRESHOLD` | Min txns for reliable scoring | 20 |
| `DEFAULT_SIGMA_THRESHOLD` | Velocity z-score threshold | 2.0 |
| `DEFAULT_WINDOW_DAYS` | Velocity baseline window | 30 |
| `MIN_SAMPLE_SIZE` | Min data points for velocity baseline | 7 |

### Data Retention (required before GA, P1-ANA-3)

- `daily_metrics` and `employee_daily_metrics`: archive records older than 24 months to cold storage.
- `period_metrics`: retain indefinitely (aggregates).
- `risk_score_history`: prune records older than 24 months.
- Recommendation: partition `daily_metrics` by month on `metric_date`.

---

## ILDWAC Cost Anomaly Metrics

> **Architectural direction — not current implementation.** The metrics described here depend on `ledger.ilwac_positions` and the five-dimension WAC recalculation engine, neither of which exists yet. This section defines the analytics contract for when those layers are in place. No GRO ticket for implementation exists at this time.

ILDWAC introduces a cost provenance dimension to the analytics domain. When the extended WAC model is operational, Analytics gains a new metric family: cost anomaly signals derived from WAC deviation across the five ILDWAC dimensions.

### Baseline WAC Computation

Analytics is responsible for computing and maintaining the baseline WAC against which ILDWAC dimension deviations are measured.

| Baseline | Grain | Table | Computation |
|---|---|---|---|
| Item-location WAC baseline | Per (item_id, location_id) | `metrics.metric_baselines` | Rolling weighted average across all (device, mcp_tool, pos_port) combinations; recomputed on each RIB batch commit |
| Device WAC baseline | Per (item_id, location_id, device_id) | `metrics.metric_baselines` | WAC restricted to events from that device; updated on each RIB batch that includes the device |
| Port WAC baseline | Per (item_id, location_id, pos_port) | `metrics.metric_baselines` | WAC restricted to events from that connector (Square, Counterpoint, Lightspeed) |
| MCP tool WAC baseline | Per (item_id, location_id, mcp_tool) | `metrics.metric_baselines` | WAC restricted to events authorized by that MCP tool call |

The item-location baseline is the reference value. Dimension-level baselines are computed for the top N most active (item, location) pairs per merchant (configurable, default N=50).

### Anomaly Detection by Dimension

The velocity anomaly engine (z-score / sigma threshold) is reused for ILDWAC deviation detection. The same `detect_velocity()` function applies; the input vector is a sequence of WAC values by RIB batch commit rather than transaction event.

| Signal | Detection Method | Chirp Rule | Severity |
|---|---|---|---|
| Device WAC outlier | Z-score of device WAC vs store-level WAC distribution | C-1101 DEVICE_WAC_OUTLIER | high |
| Port WAC mismatch | Percentage deviation between Square WAC and Counterpoint WAC for same (item, location) | C-1102 PORT_WAC_MISMATCH | high |
| MCP tool cost attribution gap | Presence of MCP authorization event without corresponding RIB batch | C-1103 MCP_COST_ATTRIBUTION_GAP | critical |

When a WAC deviation exceeds the configured sigma threshold for any dimension, Analytics writes a `COST_ANOMALY` metric event to `metrics.daily_metrics` (future column: `cost_anomaly_count`) and feeds the signal to Chirp for rule evaluation.

### Satoshi-Denominated Shrink Cost

Every inventory loss event reported through the Analytics domain is expressed in both satoshis and fiat cents. The satoshi value is the native unit; fiat is computed at presentation time using the exchange rate at the event timestamp.

**Reporting convention:**

```json
{
  "metric": "SHRINK_COST",
  "period": "P3 FY2026",
  "shrink_amount_cents": 185000,
  "shrink_amount_satoshis": 5462800,
  "fiat_exchange_rate_at_period_close": 0.03389,
  "satoshi_source": "ledger.ilwac_positions"
}
```

The `shrink_amount_satoshis` field is populated only when `ledger.ilwac_positions` is operational. Until then, the field is `null` and the fiat value remains the sole reporting unit. No existing metric is changed — this is an additive field.

**Dashboard impact:** The analytics dashboard gains a `satoshi_shrink_cost` card in the ILDWAC implementation pass. Until then, the dashboard renders fiat-only as today.

### ILDWAC Metric Feed to Chirp

The Analytics → Chirp signal path for cost anomalies:

1. RIB batch commits to `ledger.ilwac_positions` (future)
2. Analytics computes WAC deviation per dimension against baselines
3. If deviation exceeds threshold, Analytics writes anomaly signal to `metrics.daily_metrics.cost_anomaly_count`
4. Chirp batch sweep reads `cost_anomaly_count` > 0 and evaluates Category 11 rules (C-1101/C-1102/C-1103)
5. If rule fires, Chirp writes `COST_ANOMALY` alert to `app.alerts`
6. Analytics dashboard reflects the alert in the `anomaly_count` and `top_concern` fields

---

## Phase 3 — Bull Distribution Analytics

When Bull ships (see `bull.md`), Analytics gains distribution intelligence metrics. These are **gated on Module D substrate** — the tables must exist (migration detection, not configuration flag). Until D.3 transfer detection is operational, these metrics return empty.

**Scheduled rollup jobs to add (Phase 3):**

| Job | Source | Output | Frequency |
|---|---|---|---|
| Transfer-loss rate by route | `bull_transfer_variances` | Per (from_location, to_location) aggregation | Daily, rolling 90d |
| Unattributed inventory movements | `bull_unattributed_movements` | Per (location, item) | Daily |
| Recommendation acceptance rate | `bull_distribution_recommendations` | Per merchant, per buyer | Weekly |
| Transfer cost savings | `bull_distribution_recommendations` | Per accepted recommendation | On event |

---

## Production Readiness Checklist

- [ ] PII encrypted at rest — employee names in `app.employees` (P0-ANA-1)
- [ ] MCP tenant isolation enforced — merchant_id validation against JWT context (P0-ANA-2)
- [ ] DB session lifecycle — accept session/connection as parameter from caller, not created internally (P0-ANA-3)
- [ ] Secrets via secrets manager — not environment variable files
- [ ] REST health endpoint — add `GET /api/analytics/health`
- [ ] Audit logging — `get_top_risks` and `get_drilldown` access logged with timestamp, caller, entity accessed (P1-ANA-1)
- [ ] Rate limiting — `/dashboard` 30/min, `/trends` 60/min, `/top-risks` 30/min, `/drilldown` 60/min (P1-ANA-2)
- [ ] Data retention policy — daily metrics partitioned and archived (P1-ANA-3)
- [ ] Dashboard 204 on no data — not 200/null (P1-ANA-4)
- [ ] Export endpoint — implement CSV export or remove route before GA (P1-ANA-5)
- [ ] Dashboard caching in Valkey — 60s TTL, invalidate on aggregation (P1-ANA-6)
- [ ] Name lookup batching — single IN query for up to 10 entities, not N+1 (P2-ANA-4)
- [ ] Trend aggregation — GROUP BY or pass location_id for multi-location merchants (P2-ANA-1)

---

## Why This Resolution Level Is New

Legacy LP analytics tools operate on daily batch extracts, fiat-only cost models, mutable evidence stores, and flat entity identifiers. The granularity they can achieve is bounded by the coarsest element in that stack — the daily batch.

The combination of capabilities in this system produces a structurally different resolution level:

| Capability | This System | Legacy LP Tools |
|---|---|---|
| Event capture | Per-event webhook (no polling delay) | Daily batch extract; events arrive 12–24h after they occur |
| Cost provenance | Five-dimension ILDWAC: Item × Location × Device × MCP × Port × WAC in satoshis | Single fiat WAC per item-location; no device, channel, or agent attribution |
| Entity resolution | pgvector semantic matching (OWL EJ Spine) across POS sources via RaaS namespace | Flat employee ID match within a single POS; cross-source deduplication is manual |
| Evidence integrity | Append-only merkle evidence chain (Fox) with PostgreSQL-level INSERT-ONLY triggers | Mutable database records; evidence can be modified or deleted after the fact |

This is not a feature comparison. It is a structural difference in what the system can see.

**Per-event capture** means the analytics layer receives every transaction, void, refund, and drawer event within seconds of it occurring — not summarized at end of day. Velocity anomalies that develop and resolve within a shift are visible. In a batch system, they are invisible.

**Five-dimension cost provenance** means a WAC deviation is traceable to the specific device, connector, and MCP tool that produced it. A legacy WAC is a single number with no audit trail. The ILDWAC vector is auditable at the event level — the hash chain behind each RIB batch ensures the provenance is tamper-evident.

**Semantic entity resolution** means an employee recognized under two different identifiers in Square and Counterpoint is treated as one subject across both sources. Legacy systems match on exact ID — the same person appearing under different logins in different systems is invisible as a unified pattern.

**Append-only evidence** means the evidence record cannot be altered after the fact. In a mutable evidence store, records can be edited, deleted, or backdated. The Fox hash chain makes post-hoc tampering detectable and blocks it at the database trigger level.

The resolution level that results from this combination is not achievable by adding features to a legacy LP tool. It requires that every layer of the stack — capture, cost model, entity resolution, evidence integrity — be designed for this resolution from the start.
