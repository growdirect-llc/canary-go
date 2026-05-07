---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go | LND or core-lightning
status: active-build-spec
updated: 2026-05-01
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
related: [canary-go-satoshi-cost-model, canary-go-cadence-ladder, canary-go-endpoint-library, feed-tier-contract, ildwac, l402-otb, blockchain-anchor, raas]
---

# Satoshi Cost Rollup — Buildable Spec

> **Purpose.** This SDD turns the [[canary-go-satoshi-cost-model|satoshi cost model]] from architectural framework into engineer-codeable contract. It defines the Go types, database schema, calibration loader, metering pipeline, settlement choreography, and Lightning channel management that compose the satoshi-rollup pricing system. Engineers implement against this; ops calibrates against it; merchants verify their bills against artifacts it produces.
>
> The cost model card is the *what* and *why*. This SDD is the *how* — the concrete artifacts that make the model a running system.

---

## Architecture Context

| Concept | Lives in | This SDD's role |
|---|---|---|
| Cost model + tier weights | [[canary-go-satoshi-cost-model]] | Reference; `Tier` enum aligned with [[feed-tier-contract]] |
| Cadence tiers | [[canary-go-cadence-ladder]] | Each tier has a fixed `tier_weight` parameter |
| Per-feed registry | `feed-tier-contract.md` SDD | Cost calibration loader reads feed registry for per-feed tier |
| ILDWAC five-dim cost | [[canary-ildwac]] | Sixth dimension (cadence) plugs into existing model |
| L402-OTB Lightning gate | [[canary-l402-otb]] | Settlement primitive |
| Blockchain anchor | [[canary-blockchain-anchor]] | Period-end usage statement Merkle commit |
| RaaS chain anchors | [[canary-raas]] | Per-merchant cost-event chain |
| `SourceCode` on CRDM | `internal/crdm/transaction.go` | Channel rev-share aggregation key |

### What this SDD decides that the cost-model card doesn't

- The exact Go type for `MeteredEvent`, `SatCost`, `CalibrationParams`
- The deploy-time YAML schema for calibration parameters and channel rev-share
- Where the metering events persist (Postgres `metrics.cost_events`, partitioned by month)
- The SQL-level rollup query for periodic merchant statements
- The Lightning channel lifecycle (creation, capacity sizing, period rebalancing, close)
- The Merkle root algorithm for usage statements and the L2-commit handoff
- The integration boundary between cost metering and the agent contract (boot-time validation expanded)
- The implementation order — 12 ordered steps, each shippable as one PR

---

## Layer 1 — Calibration Schema

Per-tier weights, per-service unit costs, and per-MCP-tool decision costs all live in deploy-time YAML and load to a Postgres `cost_calibration` table at deploy. The runtime reads from the table, not the YAML, so calibration changes can roll forward without re-deploy.

### Calibration YAML

```yaml
# deploy/cost-calibration.yaml
version: 2026-05-01-q2
effective_at: 2026-05-01T00:00:00Z

tier_weights:
  stream:       10.0
  change-feed:   4.0
  daily-batch:   1.5
  bulk-window:   1.0
  reference:     0.2

service_unit_costs:
  # Ingestion
  gateway:                       2
  tsp:                           2
  # Detection + cases
  chirp:                        30
  alert:                         5
  fox:                          50
  # Master data + ops
  item:                          1
  customer:                      1
  inventory:                     2
  inventory-as-a-service:       15
  receiving:                     2
  transfer:                      2
  pricing:                       3
  employee:                      1
  returns:                       3
  asset:                         1
  report:                        per-MB
  # Intelligence
  owl:                          80
  analytics:                    20
  # Identity + RaaS
  identity:                      1
  raas:                          0.5
  # Adapters
  hawk:                          1
  bull:                          1
  ecom-channel:                  1
  # Moat services (patent-protected — premium)
  ildwac:                      150
  l402-otb:                     20
  blockchain-anchor:         5000   # includes amortized L2 commit
  device-contracts:             10
  field-capture:                 3
  store-network-integrity:     200
  store-brain:                  10
  ops-dashboard:                 5
  commercial:                   30
  compliance:                    5

decision_costs:
  # MCP tool unit costs in sats per call (cache-miss baseline; cache-hit = 0.1×)
  compliance.lookup:                  5
  compliance.create_block:           30
  compliance.audit_log:               5
  compliance.attest:               1000
  raas.build_key:                     0.5
  raas.ensure_namespace:              0.5
  raas.resolve_namespace:             1
  raas.verify_chain:                 10
  raas.anchor_hash:                  50      # plus blockchain-anchor amortized
  fox.add_evidence:                  50
  fox.verify_chain:                  10
  chirp.list_rules:                   1
  chirp.evaluate_rule:               30
  owl.search:                        80
  owl.embed:                         50
  owl.risk_lookup:                    5
  store_brain.who_is_here:            2
  store_brain.start_session:         10
  store_brain.check_permission:       2
  store_brain.heartbeat:              0.5
  store_brain.end_session:            5
  ops.health_rollup:                 10
  ops.device_status:                  2
  ops.adapter_lag:                    2
  ops.alert_distribution:             5
  ops.mcp_health:                     5
  ops.silence_device:                10

storage_tier_weights:
  hot:    1.0       # < 7 days, fast access
  warm:   0.3       # 8d - 90d
  cold:   0.05      # > 90d (object storage)

storage_unit_cost_per_record_per_day:
  default: 0.05      # sats / record / day baseline
  per_table_overrides:
    sales.transactions:        0.05
    fox.evidence_records:      0.10   # higher — append-only, hash-chained
    sales.line_items:          0.02
    metrics.cost_events:       0.02

base_platform_floor:
  default: 200000    # sats / merchant / month minimum
  enterprise_tier: 1000000

revshare:
  # V1.1 schema — keyed by partner_id, supports multi-VAR per source.
  # See canary-go-ncr-counterpoint-crosswalk for rationale.
  channel_partners:
    rapidpos:
      partner_id: "rapidpos-uuid"
      share: 0.20                       # 20% of satoshis flowing through this channel
      lightning_address: "rapidpos@payments.example"
      sources: ["counterpoint"]         # which source codes route through this partner
    ncr-direct:
      partner_id: null                  # null = direct-merchant relationship, no rev-share
      share: 0.0
      lightning_address: null
      sources: ["counterpoint"]
    square-direct:
      partner_id: null
      share: 0.0
      lightning_address: null
      sources: ["square"]
    shopify-direct:
      partner_id: null
      share: 0.0
      lightning_address: null
      sources: ["shopify"]
  microservices: []                     # populated when partner-contributed services register
```

### Validation rules

- Every service in `service_unit_costs` must exist in the service registry at deploy time
- Every MCP tool in `decision_costs` must exist in the MCP tool registry
- All cost values must be non-negative finite numbers
- `version` must be unique (no overwrites; new calibration is a new row)
- `effective_at` must be in the future or within 60s of `now`
- `revshare.channels.<source>.share` must be in `[0.0, 0.5]` (50% cap on channel rev-share)

### Persistence

```sql
-- deploy/migrations/cost-rollup/0001_calibration.up.sql

CREATE TABLE app.cost_calibration (
    version           TEXT PRIMARY KEY,
    effective_at      TIMESTAMPTZ NOT NULL,
    yaml_sha256       TEXT NOT NULL,
    tier_weights      JSONB NOT NULL,
    service_unit_costs JSONB NOT NULL,
    decision_costs    JSONB NOT NULL,
    storage_tier_weights JSONB NOT NULL,
    storage_unit_cost_per_record_per_day JSONB NOT NULL,
    base_platform_floor JSONB NOT NULL,
    revshare          JSONB NOT NULL,
    loaded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX cost_calibration_effective_idx
  ON app.cost_calibration (effective_at DESC);

-- Latest calibration view for runtime
CREATE OR REPLACE VIEW app.current_cost_calibration AS
  SELECT * FROM app.cost_calibration
  WHERE effective_at <= NOW()
  ORDER BY effective_at DESC LIMIT 1;
```

### Go types

```go
// internal/cost/calibration.go

package cost

type Calibration struct {
    Version                  string
    EffectiveAt              time.Time
    TierWeights              map[feed.Tier]float64
    ServiceUnitCosts         map[string]float64
    DecisionCosts            map[string]float64       // keyed by "<server>.<tool>"
    StorageTierWeights       map[string]float64
    StorageUnitCostPerRecord map[string]float64       // per-table; "default" key
    BasePlatformFloor        map[string]int64         // "default", "enterprise_tier"
    Revshare                 RevshareConfig
}

type RevshareConfig struct {
    ChannelPartners map[string]ChannelPartner   // keyed by var_of_record_label
    Microservices   map[string]ServiceShare     // keyed by service name
}

type ChannelPartner struct {
    PartnerID         *uuid.UUID                 // nil for direct-merchant relationships
    Share             float64
    LightningAddress  *string                    // nil for direct
    Sources           []string                   // which SourceCodes route through this channel
}

type ServiceShare struct {
    PartnerID         uuid.UUID
    Share             float64
    LightningAddress  string
}
```

---

## Layer 2 — Metering Pipeline

The metering pipeline writes one `cost_event` per measurable action. Every event flows through the same primitive: emit, accrue, settle. Six event types cover the cost model.

### Event types

| Event type | Emitted by | Captures |
|---|---|---|
| `event_processed` | Service that processed an inbound event | merchant, service, tier, count |
| `decision_made` | Service that handled an MCP tool call | merchant, tool, tier, output_size, cache_hit, passthrough_sats |
| `storage_accrued` | Daily reconciliation job | merchant, table, records, days, tier |
| `passthrough_incurred` | Service crossing external boundary | merchant, upstream, sats |
| `anchor_amortized` | blockchain-anchor commit | merchants[], share_per_merchant |
| `floor_applied` | Period-end reconciliation | merchant, floor_sats |

### Emit primitive

Every service that wants to meter a cost calls one function:

```go
// internal/cost/meter.go

package cost

type Meter struct {
    db    *pgx.Pool
    cache *redis.Client
}

func (m *Meter) Emit(ctx context.Context, e Event) error {
    // 1. Compute sat_cost from event + current calibration
    cal := m.currentCalibration(ctx)
    cost := computeSatCost(e, cal)

    // 2. Write to metrics.cost_events (partitioned by month)
    _, err := m.db.Exec(ctx, `
        INSERT INTO metrics.cost_events
            (merchant_id, service, event_type, tier, source_code,
             quantity, sat_cost, metadata, occurred_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
        e.MerchantID, e.Service, e.EventType, e.Tier, e.SourceCode,
        e.Quantity, cost, e.Metadata, e.OccurredAt,
    )
    if err != nil {
        return err
    }

    // 3. Increment per-merchant period accumulator (Valkey)
    accumKey := fmt.Sprintf("cost:accum:%s:%s", e.MerchantID, periodKey(e.OccurredAt))
    m.cache.IncrBy(ctx, accumKey, cost)

    // 4. If a budget gate exists for this merchant, decrement
    if budget := m.budgetFor(ctx, e.MerchantID); budget != nil {
        budget.Decrement(cost)
    }

    return nil
}
```

### Cost computation per event type

```go
func computeSatCost(e Event, cal *Calibration) int64 {
    switch e.EventType {
    case EventProcessed:
        return int64(
            float64(e.Quantity) *
            cal.TierWeights[e.Tier] *
            cal.ServiceUnitCosts[e.Service],
        )
    case DecisionMade:
        base := cal.DecisionCosts[e.ToolKey()]
        tierMult := cal.TierWeights[e.Tier]
        sizeFactor := outputSizeFactor(e.OutputSize)
        cacheFactor := 0.1
        if !e.CacheHit {
            cacheFactor = 1.0
        }
        return int64(base*tierMult*sizeFactor*cacheFactor) + e.PassthroughSats
    case StorageAccrued:
        return int64(
            float64(e.Quantity) *                                    // records
            float64(e.Days) *
            cal.StorageTierWeights[e.StorageTier] *
            cal.unitCostFor(e.Table),
        )
    case PassthroughIncurred:
        return e.PassthroughSats
    case AnchorAmortized:
        return e.SharePerMerchant
    case FloorApplied:
        return cal.BasePlatformFloor[e.MerchantTier]
    }
    return 0
}
```

### Persistence

```sql
-- deploy/migrations/cost-rollup/0002_cost_events.up.sql

CREATE TABLE metrics.cost_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    service         TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    tier            TEXT NOT NULL,
    source_code     TEXT,
    quantity        BIGINT NOT NULL,
    sat_cost        BIGINT NOT NULL,
    metadata        JSONB,
    occurred_at     TIMESTAMPTZ NOT NULL,
    settlement_id   UUID NULL                  -- linked once settled
) PARTITION BY RANGE (occurred_at);

-- One partition per month
CREATE TABLE metrics.cost_events_2026_05 PARTITION OF metrics.cost_events
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
-- ...

CREATE INDEX cost_events_merchant_period_idx
  ON metrics.cost_events (merchant_id, occurred_at DESC);

CREATE INDEX cost_events_source_idx
  ON metrics.cost_events (source_code, occurred_at DESC)
  WHERE source_code IS NOT NULL;

CREATE INDEX cost_events_unsettled_idx
  ON metrics.cost_events (merchant_id, occurred_at)
  WHERE settlement_id IS NULL;
```

### Where each service emits

Every service contracted in `microservice-architecture.md` emits cost events at a consistent point in its request lifecycle:

| Lifecycle point | Event type | Service example |
|---|---|---|
| After ingest write | `event_processed` | `tsp` after appending to ingestion log |
| Before MCP tool result return | `decision_made` | every `canary-*` MCP server |
| Daily reconciliation cron | `storage_accrued` | `analytics` per merchant per table |
| After upstream API call | `passthrough_incurred` | `bull` after Counterpoint call |
| After L2 commit | `anchor_amortized` | `blockchain-anchor` per included merchant |
| Period-end | `floor_applied` | settlement reconciliation job |

A small library at `internal/cost/middleware.go` provides Chi middleware that emits `decision_made` automatically for any service registering its router with the helper. Reduces per-service boilerplate.

---

## Layer 3 — Rollup + Statement Generation

At period end (configurable per merchant; default monthly), a reconciliation job generates a usage statement, computes Merkle root, commits to L2, and triggers Lightning settlement.

### Schema addition for VAR-of-record attribution

```sql
-- deploy/migrations/cost-rollup/0004_var_of_record.up.sql

-- VAR-of-record stamped at merchant onboarding via canary-identity.
-- Drives channel rev-share attribution per the V1.1 schema.
ALTER TABLE app.merchants
  ADD COLUMN var_of_record_label TEXT NULL;

CREATE INDEX merchants_var_of_record_idx
  ON app.merchants (var_of_record_label)
  WHERE var_of_record_label IS NOT NULL;
```

`var_of_record_label` is the key into `revshare.channel_partners`. Its value is captured at merchant onboarding (`POST /merchants` accepts `var_of_record_label` field). Examples: `"rapidpos"`, `"ncr-direct"`, `"square-direct"`, `"shopify-direct"`.

### Rollup query

```sql
-- internal/cost/queries/rollup.sql

-- name: PeriodRollup :many
WITH events AS (
    SELECT
        e.merchant_id, e.service, e.event_type, e.tier, e.source_code,
        m.var_of_record_label,                          -- channel attribution
        SUM(e.quantity) AS total_quantity,
        SUM(e.sat_cost) AS total_sats,
        COUNT(*) AS event_count
    FROM metrics.cost_events e
    JOIN app.merchants m ON e.merchant_id = m.id
    WHERE e.merchant_id = $1
      AND e.occurred_at >= $2
      AND e.occurred_at < $3
      AND e.settlement_id IS NULL
    GROUP BY e.merchant_id, e.service, e.event_type, e.tier, e.source_code,
             m.var_of_record_label
)
SELECT * FROM events ORDER BY service, event_type, tier;

-- name: ChannelRevshareRollup :many
-- Per-channel-partner rev-share for a period, keyed by var_of_record_label
SELECT
    m.var_of_record_label,
    e.source_code,
    SUM(e.sat_cost) AS total_sats,
    COUNT(*) AS event_count
FROM metrics.cost_events e
JOIN app.merchants m ON e.merchant_id = m.id
WHERE e.occurred_at >= $1
  AND e.occurred_at < $2
  AND m.var_of_record_label IS NOT NULL
GROUP BY m.var_of_record_label, e.source_code
ORDER BY total_sats DESC;
```

### Statement document

```go
// internal/cost/statement.go

type UsageStatement struct {
    ID              uuid.UUID
    MerchantID      uuid.UUID
    PeriodStart     time.Time
    PeriodEnd       time.Time
    GeneratedAt     time.Time
    CalibrationVer  string
    LineItems       []LineItem
    TotalSats       int64
    Floor           int64
    Adjustments     []Adjustment
    NetSats         int64
    FiatEquivalent  FiatQuote
    MerkleRoot      string
    AnchorCommitID  *uuid.UUID
    LightningInvoice string
    Status          string // DRAFT | ANCHORED | INVOICED | PAID | SETTLED
}

type LineItem struct {
    Service      string
    EventType    string
    Tier         string
    SourceCode   string
    EventCount   int64
    Quantity     int64
    SatCost      int64
    SourceEvents []uuid.UUID  // Merkle leaf inputs
}

type FiatQuote struct {
    Currency string
    Amount   decimal.Decimal
    Rate     decimal.Decimal  // sats/USD locked at quote time
    QuotedAt time.Time
}

type Adjustment struct {
    Kind     string  // "credit" | "debit" | "channel-revshare"
    SatDelta int64
    Reason   string
    LinkedID *uuid.UUID
}
```

### Merkle root algorithm

```go
// internal/cost/merkle.go

// MerkleRootForStatement computes the canonical Merkle root over all
// underlying cost_event UUIDs, sorted lexicographically. Each leaf is
// SHA256(event_id || sat_cost || occurred_at_unix_nanos). Internal
// nodes are SHA256(left || right). Single-element trees pad with the
// genesis sentinel hash.
func MerkleRootForStatement(events []CostEvent) string {
    leaves := make([][]byte, 0, len(events))
    for _, e := range events {
        leaves = append(leaves, leafHash(e))
    }
    sort.Slice(leaves, func(i, j int) bool {
        return bytes.Compare(leaves[i], leaves[j]) < 0
    })
    return hex.EncodeToString(merkleTree(leaves))
}
```

The merchant retains the full leaf set and reconstruction path for any line item. `GET /usage/statements/:id/proof?event_id=` returns the Merkle path. Verifying any event against the L2-committed root is one hash chain walk.

### Persistence

```sql
-- deploy/migrations/cost-rollup/0003_statements.up.sql

CREATE TABLE app.usage_statements (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL,
    period_start      TIMESTAMPTZ NOT NULL,
    period_end        TIMESTAMPTZ NOT NULL,
    generated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    calibration_ver   TEXT NOT NULL,
    total_sats        BIGINT NOT NULL,
    floor_sats        BIGINT NOT NULL,
    net_sats          BIGINT NOT NULL,
    fiat_currency     TEXT NOT NULL,
    fiat_amount       NUMERIC(20, 8) NOT NULL,
    fiat_rate         NUMERIC(20, 8) NOT NULL,
    fiat_quoted_at    TIMESTAMPTZ NOT NULL,
    merkle_root       TEXT NOT NULL,
    anchor_commit_id  UUID REFERENCES app.anchor_commits(id),
    lightning_invoice TEXT,
    status            TEXT NOT NULL CHECK (status IN ('DRAFT','ANCHORED','INVOICED','PAID','SETTLED')),
    UNIQUE (merchant_id, period_start, period_end)
);

CREATE TABLE app.usage_statement_line_items (
    statement_id      UUID REFERENCES app.usage_statements(id) ON DELETE CASCADE,
    line_no           INTEGER NOT NULL,
    service           TEXT NOT NULL,
    event_type        TEXT NOT NULL,
    tier              TEXT NOT NULL,
    source_code       TEXT,
    event_count       BIGINT NOT NULL,
    quantity          BIGINT NOT NULL,
    sat_cost          BIGINT NOT NULL,
    PRIMARY KEY (statement_id, line_no)
);

CREATE TABLE app.usage_statement_proofs (
    statement_id      UUID REFERENCES app.usage_statements(id) ON DELETE CASCADE,
    event_id          UUID NOT NULL,
    leaf_hash         TEXT NOT NULL,
    merkle_path       JSONB NOT NULL,                    -- ordered hash list
    PRIMARY KEY (statement_id, event_id)
);
```

---

## Layer 4 — Lightning Settlement

Three settlement modes per `canary-l402-otb`. The cost-rollup system is the upstream producer; L402-OTB is the gating + settlement engine.

### Channel topology

One Lightning channel per merchant per direction:

- **Inbound channel** (merchant → Canary): merchant funds an OTB allocation
- **Outbound channel** (Canary → channel partner): Canary settles channel rev-share to RapidPOS, NCR-VAR partners, etc.

Channel sizing rule:

```go
// internal/cost/channel.go

// SizeChannel computes target capacity for a merchant Lightning channel
// based on rolling 90-day cost-to-serve. Targets 2× the 90-day mean to
// allow burst headroom without forcing a re-anchor mid-period.
func SizeChannel(merchant uuid.UUID, history []PeriodTotal) int64 {
    if len(history) == 0 {
        return InitialChannelCapacity // bootstrap default
    }
    sum := int64(0)
    for _, p := range history {
        sum += p.NetSats
    }
    mean := sum / int64(len(history))
    target := mean * 2
    return clamp(target, MinChannelCapacity, MaxChannelCapacity)
}
```

Channels are rebalanced at period-end in the same workflow that anchors the usage statement — one atomic ops cycle per merchant per period.

### Period-end choreography

```
1. Reconciliation cron fires at period-end (e.g., 2026-06-01 00:00:00Z UTC for May)
2. For each merchant:
   a. Run PeriodRollup query → produce LineItems
   b. Compute MerkleRoot over underlying cost_events
   c. Quote fiat equivalent (lock sats/USD rate at this moment)
   d. INSERT app.usage_statements with status = DRAFT
   e. Submit Merkle root to canary-blockchain-anchor → returns anchor_commit_id
   f. Update statement: anchor_commit_id set, status = ANCHORED
   g. Generate Lightning invoice for net_sats
   h. Update statement: lightning_invoice set, status = INVOICED
   i. Notify merchant: "Your May statement is ready, $XXX equivalent, verify at <url>"
3. Merchant pays the Lightning invoice (or auto-pays if pre-authorized)
4. Watcher detects payment → status = PAID
5. Channel rev-share settlement:
   a. Aggregate net_sats grouped by source_code
   b. Apply RevshareConfig.Channels[source].share
   c. Generate Lightning payment to each channel partner's lightning_address
   d. Status = SETTLED on all involved statements
6. UPDATE metrics.cost_events SET settlement_id = statement.id WHERE merchant_id = X AND ...
```

### L2 anchor handoff

`canary-blockchain-anchor` receives the Merkle root with `anchor_class = 'usage_statement'`:

```go
anchorReq := blockchain.AnchorRequest{
    MerchantID:    merchant.ID,
    PayloadHash:   merkleRoot,
    PrevHash:      lastUsageStatementHash(merchant.ID),
    AnchorClass:   "usage_statement",
    Metadata: map[string]any{
        "statement_id":     statement.ID,
        "period_start":     statement.PeriodStart,
        "period_end":       statement.PeriodEnd,
        "calibration_ver":  statement.CalibrationVer,
        "total_sats":       statement.NetSats,
    },
}
resp, err := blockchainClient.Anchor(ctx, anchorReq)
```

The anchor batches across all merchants for cost amortization (per the bulk-window tier mapping). One L2 commit per merchant per period would be cost-prohibitive; one batched commit per period across all merchants amortizes the cost trivially. The merchant's verification path retrieves their merkle path within the batch's commit.

### Invariants

- A statement cannot transition from `INVOICED` → `PAID` without a confirmed Lightning payment at or above `net_sats`
- A statement cannot transition from `PAID` → `SETTLED` until all channel rev-shares are dispatched
- `cost_events.settlement_id` is set only when the encompassing statement reaches `SETTLED`
- A statement's `merkle_root` and `anchor_commit_id` are immutable once set (DB constraint, not just policy)

---

## Layer 5 — Diagnostic API

The diagnostic is a discovery instrument. The platform exposes an endpoint that takes the five inputs and returns a forecast plus a side-by-side comparison.

### REST contract

```
POST /usage/forecast
Auth: optional JWT (anon allowed for public marketing site)
Body:
  {
    "transactions_per_month": 20000,
    "active_locations": 3,
    "pos_sources": ["counterpoint", "shopify"],
    "agent_decisions_per_day": 5000,
    "retention_days": 365,
    "compliance_complexity": 1,
    "comparison_baseline": "square|lightspeed|toast|counterpoint-license|none"
  }
Response 200:
  {
    "forecast": {
      "monthly_sats_low":  55000000,
      "monthly_sats_high": 70000000,
      "monthly_sats_mid":  62500000,
      "fiat_at_quote": {
        "currency": "USD",
        "low": 33.00, "mid": 37.50, "high": 42.00,
        "rate_sats_per_usd": 1666666,
        "quoted_at": "2026-05-01T..."
      },
      "breakdown": {
        "ingestion_processing":   3000000,
        "detection_cases":       14400000,
        "storage":                  365000,
        "agent_decisions":        37500000,
        "anchor_amortized":          20000,
        "platform_floor":           200000,
        "location_overhead":        150000,
        "adapter_overhead":         200000
      }
    },
    "comparison": {
      "baseline": "square",
      "monthly_fiat": 240.00,
      "calculation": "$60 base + $20 × 3 locations + ~2.6% transaction fee on $X",
      "delta_vs_canary": "Canary lands ~85% cheaper",
      "value_capture_note": "Square takes percentage of transaction value; Canary does not"
    },
    "verifiable_path": "On Canary, your monthly bill is independently verifiable against an immutable Bitcoin L2 timestamp. No competitor offers this."
  }
```

### Forecast formula

```go
// internal/cost/forecast.go

func Forecast(in DiagnosticInput, cal *Calibration) Forecast {
    // Ingestion + processing
    ingest := float64(in.TxnsPerMonth) * cal.ServiceUnitCosts["tsp"] * cal.TierWeights[feed.TierStream]

    // Detection (assume 30% trigger rate per cadence-ladder profiling)
    detectionEvents := float64(in.TxnsPerMonth) * 0.30
    detection := detectionEvents * cal.ServiceUnitCosts["chirp"] * cal.TierWeights[feed.TierChangeFeed]

    // Cases (5% escalate to Fox)
    cases := float64(in.TxnsPerMonth) * 0.05 * cal.ServiceUnitCosts["fox"] * cal.TierWeights[feed.TierStream]

    // Anchor amortized share (1 commit / week × 4 weeks × per-merchant share)
    anchorShare := 4 * (cal.ServiceUnitCosts["blockchain-anchor"] / 100)  // assume 100 merchants per batch

    // Storage
    storage := float64(in.TxnsPerMonth) * float64(in.RetentionDays) * cal.unitCostFor("default") *
               weightedStorageTier(in.RetentionDays)

    // Agent decisions
    decisions := float64(in.AgentDecisionsPerDay) * 30.0 * avgDecisionCost(cal)

    // Multi-location overhead
    locOverhead := float64(in.ActiveLocations) * 50000

    // Adapter overhead
    adapterOverhead := float64(len(in.PosSources)) * 100000

    // Compliance multiplier
    compliance := float64(in.TxnsPerMonth) * float64(in.ComplianceComplexity) * cal.ServiceUnitCosts["compliance"]

    // Floor
    floor := cal.BasePlatformFloor["default"]

    mid := int64(ingest + detection + cases + anchorShare + storage +
                 decisions + locOverhead + adapterOverhead + compliance) +
           floor

    return Forecast{
        MonthlySatsLow:  int64(float64(mid) * 0.85),
        MonthlySatsMid:  mid,
        MonthlySatsHigh: int64(float64(mid) * 1.15),
        // ... breakdown + fiat quote
    }
}
```

The ±15% bands reflect calibration variance — a merchant's actual usage drifts within this range based on tier mix, cache-hit rates, and external passthrough.

### Comparison baselines

```go
// internal/cost/comparison.go

var SeatBaselines = map[string]BaselineCalc{
    "square": {
        BaseFee:      6000,                          // cents/mo
        PerLocation:  2000,
        TxnPercent:   2.6,
        Notes:        "Square takes percentage of transaction value",
    },
    "lightspeed": {
        BaseFee:      9900,
        PerLocation:  9900,
        TxnPercent:   0.0,
        Notes:        "Plus per-employee seats",
    },
    "toast": {
        BaseFee:      6900,
        PerLocation:  6900,
        TxnPercent:   2.99,
    },
    "counterpoint-license": {
        BaseFee:      0,
        PerLocation:  150000,                        // licensed terminals + VAR support
        Notes:        "Plus VAR contract; varies wildly",
    },
}
```

The seat baselines are deploy-time configuration, refreshed quarterly from public pricing pages. **The diagnostic shows the buyer the math**, not just the answer.

---

## Layer 6 — Agent Cost Awareness

The agent layer (Axis C) consumes the cost model to make economically rational decisions.

### MCP tool: `cost.preview_call`

```
Tool: cost.preview_call
Tier: Reference
Purpose: Preview the satoshi cost of an MCP tool call before executing it.

Input:
  {
    "merchant_id": "uuid",
    "target_tool": "owl.search",
    "estimated_output_size": 50,
    "expected_cache_hit": false
  }

Output:
  {
    "estimated_sats": 800,
    "fiat_estimate": "$0.0005",
    "merchant_budget_remaining": 4500000,
    "would_exceed_budget": false
  }
```

Agents query `cost.preview_call` before expensive operations. A search expecting 1,000 results that costs 8,000 sats may be redundant if a 50-result search at 800 sats serves the same purpose. **The platform's incentive aligns with the merchant's** — fewer wasteful calls, lower bill, same outcomes.

### MCP tool: `cost.merchant_summary`

```
Tool: cost.merchant_summary
Tier: Reference
Purpose: Current period accumulator + budget remaining.

Input: {"merchant_id": "uuid"}
Output:
  {
    "period_start": "2026-05-01T...",
    "current_accum_sats": 12500000,
    "budget_remaining_sats": 87500000,
    "projected_monthly_sats": 65000000,
    "top_consumers": [
      {"service": "chirp", "sats": 4200000, "pct": 33.6},
      {"service": "owl",   "sats": 2100000, "pct": 16.8}
    ]
  }
```

In-store agents and ops dashboards consume this for real-time consumption visualization.

### MCP tool: `cost.statement_proof`

```
Tool: cost.statement_proof
Tier: Reference
Purpose: Retrieve Merkle proof for a specific cost_event against its statement.

Input: {"statement_id": "uuid", "event_id": "uuid"}
Output:
  {
    "statement_id": "uuid",
    "event_id": "uuid",
    "leaf_hash": "sha256-hex",
    "merkle_path": ["hash1", "hash2", ...],
    "merkle_root": "sha256-hex",
    "anchor_commit": {
      "btc_tx_id": "hex",
      "block_height": 850123,
      "block_time": "2026-06-01T..."
    },
    "verify_url": "https://anchor.growdirect.io/verify?root=..."
  }
```

Merchants (or their auditors) verify any line item against the L2 commit independently of Canary. **This is the killer instrument** — the platform offers an audit primitive no competitor can replicate.

---

## Layer 7 — REST Endpoint Surface

New endpoints added to the [[canary-go-endpoint-library|Endpoint Library]]:

| Pattern | Tier | Service | Purpose |
|---|---|---|---|
| `POST /usage/forecast` | Reference | `report` (or new `forecast`) | Diagnostic input → forecast |
| `GET /usage/statements?merchant_id=&since=` | Change-feed | `report` | Statement list |
| `GET /usage/statements/:id` | Reference | `report` | Single statement |
| `GET /usage/statements/:id/proof?event_id=` | Reference | `report` | Merkle proof for verification |
| `GET /usage/sse?merchant_id=` | Stream | `ops-dashboard` | Live consumption events |
| `GET /usage/current?merchant_id=` | Reference | `ops-dashboard` | Period-to-date accumulator |
| `POST /usage/statements/:id/pay` | Stream | `l402-otb` | Submit Lightning payment |
| `GET /revshare/channels?period=` | Reference | `commercial` | Channel rev-share settlement record |

Two new MCP tool surfaces under existing servers:

- `canary-cost` (new MCP server — joins `report` or stands alone): 4 tools (`cost.preview_call`, `cost.merchant_summary`, `cost.statement_proof`, `cost.forecast`)
- Extension to `canary-l402-otb` MCP: pay-statement tool

These slot into the endpoint library's Axis C section at the next library audit pass.

---

## Implementation Order

12 ordered steps, each shippable as one PR:

1. **`internal/cost/types.go`** — Calibration, Event, Forecast, Statement Go types.
2. **`deploy/migrations/cost-rollup/0001..0003`** — calibration, cost_events, statements DB schemas.
3. **`internal/cost/calibration.go`** — calibration loader (YAML → DB → in-memory cache).
4. **`cmd/cost-calibration-load/main.go`** — deploy-time entrypoint that loads `deploy/cost-calibration.yaml`.
5. **`internal/cost/meter.go`** — `Meter.Emit()` primitive + Chi middleware for auto-decision-cost emission.
6. **Wire into all 32 services** — each service emits `event_processed` and/or `decision_made` at its lifecycle point. PR per service or one big sweep.
7. **`internal/cost/rollup.go`** + sqlc queries — period rollup query, statement assembly.
8. **`internal/cost/merkle.go`** — Merkle root algorithm, leaf hash, proof generation.
9. **`cmd/cost-reconcile/main.go`** — period-end cron job: rollup → statement → anchor → invoice.
10. **`internal/cost/lightning.go`** — Lightning channel sizing, per-merchant channel manager, payment watcher.
11. **`internal/cost/forecast.go`** + `POST /usage/forecast` endpoint — diagnostic API.
12. **MCP tool registrations** — `canary-cost.*` tools, `cost.preview_call` integrated into agent contracts.

Steps 1-5 are foundation. Step 6 fans out across services. Steps 7-10 are the reconciliation pipeline. Steps 11-12 close the loop with the diagnostic + agent surface.

---

## Out of Scope

- **Multi-currency settlement.** USD-equivalent quote at quote time is supported; multi-currency Lightning settlement (e.g., euro-fiat-equivalent) is a Phase 2 concern.
- **Dynamic per-merchant calibration.** All merchants share the same calibration parameters. Per-merchant pricing tiers (enterprise / partner / freemium) live in `BasePlatformFloor` overrides only — no per-merchant per-service unit cost differentiation in V1.
- **L402 protocol-level settlement of channel rev-share.** V1 settles channel rev-share via direct Lightning payments to partner addresses. L402-mediated rev-share routing is a Phase 2 concern.
- **Tax / VAT computation.** This SDD covers cost-to-serve and settlement. Tax handling is an integration concern with the merchant's accounting system, not a Canary primitive.
- **Refunds + disputes.** V1 supports Adjustments via the `adjustments` field on Statement, but the dispute workflow (merchant flags a line item, ops reviews, refunds settle) is Phase 2.

---

## Pitfalls

- **Don't bypass the calibration version pin.** Statements are bound to a calibration version; rebuilding a statement against a newer calibration would change historical totals. Calibration changes roll forward only.
- **Don't conflate `metrics.cost_events` with `metrics.transactions`.** Cost events meter platform consumption; transaction records are POS data. They live in different schemas for a reason.
- **Don't skip the Merkle proof generation.** Every statement must persist enough leaf data to reconstruct the proof on demand. Without that, the L2 anchor is performance theater.
- **Don't expose the calibration matrix to merchants.** They see their bill and the verification path. The per-tool decision cost matrix is commercially sensitive — calibrate it carefully and expose only the line items.
- **Don't let Lightning channel state diverge from cost_event accumulators.** Channel state is the legal record of what's been paid; cost_events is the legal record of what's been consumed. They reconcile at period-end, but mid-period drift is allowed and expected. Don't try to keep them in lockstep — that's the broken loop.

---

## Cross-References

- [[canary-go-satoshi-cost-model]] — descriptive spine card this SDD makes buildable
- [[canary-go-cadence-ladder]] — tier weights flow from here
- [[canary-go-endpoint-library]] — `/usage/*` endpoints land here
- `docs/sdds/go-handoff/feed-tier-contract.md` — tier registry referenced for cost calibration
- [[canary-ildwac]] — provenance-weighted cost model (cadence is sixth dimension)
- [[canary-l402-otb]] — Lightning gate + budget primitive (settlement upstream)
- [[canary-blockchain-anchor]] — L2 commit for usage statement Merkle roots
- [[canary-raas]] — per-merchant chain anchor (statement chain)
- Memory: `project_ilwac_bitcoin_standard`, `project_pci_scope_phase4`, `project_data_hosting_compliance_phase4`
