---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: active-build-spec
updated: 2026-04-30
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
related: [canary-go-cadence-ladder, canary-go-endpoint-library, microservice-architecture, agent-contracts, go-module-layout]
---

# Feed-Tier Contract — Buildable Spec

> **Purpose.** This SDD turns the [[canary-go-cadence-ladder|Cadence Ladder]] from descriptive wiki into buildable spec. It defines the exact YAML schemas, Go types, validation rules, and runtime parameterizations that make tier metadata load-bearing across the four layers (config / agent contract / runtime / surface). Engineers implement against this; ops people read this when troubleshooting tier mismatches; agents at boot time validate their contracts against the registry that this SDD specifies.

The cadence ladder card is the *what* and *why*. This SDD is the *how* — the concrete artifacts that make the model executable.

---

## Architecture Context

| Concept | Lives in | This SDD's role |
|---|---|---|
| Five-tier ladder | [[canary-go-cadence-ladder|Cadence Ladder]] wiki | Reference; tier enum aligned exactly |
| 3×5 grid (axis × tier) | [[canary-go-endpoint-library|Endpoint Library]] | Reference; library status flags consume the registry this SDD defines |
| Agent declarations | [[agent-card-format|Agent Card Format]] · `Brain/wiki/cards/*` | Layer 2 of this SDD reads agent cards at boot to validate consumes/produces |
| Service implementations | `cmd/<service>/` Go binaries | Each service registers its feeds via the registry API this SDD specifies |
| Bases health rollups | Brain wiki Bases queries | Layer 4 of this SDD specifies the `feed` card type Bases consumes |

### What the cadence ladder doesn't decide and this SDD does

- The exact Go type for `Tier` and its serialization
- The exact YAML schema for feed declarations, including which fields are required per tier
- Where the feed registry persists (Postgres `app.feed_registry`)
- The boot-time validation algorithm and the exit-code/log-message contract on failure
- The runtime interface every tier-handler implements (`TierRuntime`)
- The Bases card schema for surface-level rollups

---

## Layer 1 — Config Schema

### Feed declaration

Every feed Canary consumes or produces is declared in YAML and registered at deploy time. The declaration shape:

```yaml
# deploy/feeds/counterpoint.transactions.yaml
name: counterpoint.transactions
source: counterpoint
tier: change-feed                   # enum, required
axis: A                             # adapter|resource|agent — enum A/B/C, required
description: "Counterpoint transaction stream from VAR-deployed merchants"

sla:
  freshness_budget: 15m             # required for stream + change-feed
  max_lag: 30m                      # required for change-feed
  schedule: null                    # required for daily-batch + bulk-window
  ttl: null                         # required for reference
  window_start: null                # required for bulk-window
  window_end: null                  # required for bulk-window

recovery:
  mode: catch-up-from-watermark     # one of: replay-from-queue, catch-up-from-watermark,
                                    # rerun-job, reschedule-window, force-resync
  watermark_field: occurred_at      # required when mode = catch-up-from-watermark
  idempotency_key: external_id      # required for stream + change-feed

retention:
  hot: 7d                           # in fast storage
  warm: 90d                         # in slow storage
  cold: 7y                          # archived

alert_pattern: lag-exceeded         # one of: heartbeat-lost, lag-exceeded,
                                    # schedule-missed, window-missed, version-drift

producers:
  - service: bull
    binary: cmd/bull
consumers:
  - service: tsp
    via: webhook                    # how the consumer pulls — webhook|polling|sse|stomp
```

### Validation rules per tier

Each tier requires a specific subset of `sla.*` fields and rejects others. Validation is enforced at registry-load time; invalid feed declarations crash the registry loader with a non-zero exit code.

| Tier | Required `sla.*` fields | Forbidden `sla.*` fields | Required `recovery.*` |
|---|---|---|---|
| `stream` | `freshness_budget` | `schedule`, `window_start`, `window_end`, `ttl` | `mode = replay-from-queue`, `idempotency_key` |
| `change-feed` | `freshness_budget`, `max_lag` | `schedule`, `window_start`, `window_end`, `ttl` | `mode = catch-up-from-watermark`, `watermark_field`, `idempotency_key` |
| `daily-batch` | `schedule` (cron string) | `freshness_budget`, `window_start`, `window_end` | `mode = rerun-job` |
| `bulk-window` | `window_start`, `window_end` (cron strings) | `freshness_budget`, `max_lag`, `schedule`, `ttl` | `mode = reschedule-window` |
| `reference` | `ttl` (duration) | `freshness_budget`, `max_lag`, `schedule`, `window_start`, `window_end` | `mode = force-resync` |

Validation also rejects `axis` mismatches: an axis-A feed must have non-empty `producers` and at least one `consumers` entry inside Canary; an axis-C feed must include MCP tool metadata; an axis-B feed must reference a service with an HTTP endpoint pattern.

### Go types

```go
// internal/feed/types.go

package feed

import (
    "time"
)

type Tier string

const (
    TierStream     Tier = "stream"
    TierChangeFeed Tier = "change-feed"
    TierDailyBatch Tier = "daily-batch"
    TierBulkWindow Tier = "bulk-window"
    TierReference  Tier = "reference"
)

type Axis string

const (
    AxisAdapter  Axis = "A"
    AxisResource Axis = "B"
    AxisAgent    Axis = "C"
)

type RecoveryMode string

const (
    RecoveryReplayFromQueue       RecoveryMode = "replay-from-queue"
    RecoveryCatchUpFromWatermark  RecoveryMode = "catch-up-from-watermark"
    RecoveryRerunJob              RecoveryMode = "rerun-job"
    RecoveryRescheduleWindow      RecoveryMode = "reschedule-window"
    RecoveryForceResync           RecoveryMode = "force-resync"
)

type AlertPattern string

const (
    AlertHeartbeatLost    AlertPattern = "heartbeat-lost"
    AlertLagExceeded      AlertPattern = "lag-exceeded"
    AlertScheduleMissed   AlertPattern = "schedule-missed"
    AlertWindowMissed     AlertPattern = "window-missed"
    AlertVersionDrift     AlertPattern = "version-drift"
)

type FeedConfig struct {
    Name        string         `yaml:"name" json:"name"`
    Source      string         `yaml:"source" json:"source"`
    Tier        Tier           `yaml:"tier" json:"tier"`
    Axis        Axis           `yaml:"axis" json:"axis"`
    Description string         `yaml:"description" json:"description"`
    SLA         SLAConfig      `yaml:"sla" json:"sla"`
    Recovery    RecoveryConfig `yaml:"recovery" json:"recovery"`
    Retention   Retention      `yaml:"retention" json:"retention"`
    AlertPattern AlertPattern  `yaml:"alert_pattern" json:"alert_pattern"`
    Producers   []FeedEndpoint `yaml:"producers" json:"producers"`
    Consumers   []FeedEndpoint `yaml:"consumers" json:"consumers"`
}

type SLAConfig struct {
    FreshnessBudget *time.Duration `yaml:"freshness_budget,omitempty" json:"freshness_budget,omitempty"`
    MaxLag          *time.Duration `yaml:"max_lag,omitempty" json:"max_lag,omitempty"`
    Schedule        *string        `yaml:"schedule,omitempty" json:"schedule,omitempty"`         // cron
    TTL             *time.Duration `yaml:"ttl,omitempty" json:"ttl,omitempty"`
    WindowStart     *string        `yaml:"window_start,omitempty" json:"window_start,omitempty"` // cron
    WindowEnd       *string        `yaml:"window_end,omitempty" json:"window_end,omitempty"`     // cron
}

type RecoveryConfig struct {
    Mode             RecoveryMode `yaml:"mode" json:"mode"`
    WatermarkField   string       `yaml:"watermark_field,omitempty" json:"watermark_field,omitempty"`
    IdempotencyKey   string       `yaml:"idempotency_key,omitempty" json:"idempotency_key,omitempty"`
}

type Retention struct {
    Hot  time.Duration `yaml:"hot" json:"hot"`
    Warm time.Duration `yaml:"warm" json:"warm"`
    Cold time.Duration `yaml:"cold" json:"cold"`
}

type FeedEndpoint struct {
    Service string `yaml:"service" json:"service"`
    Binary  string `yaml:"binary,omitempty" json:"binary,omitempty"`
    Via     string `yaml:"via,omitempty" json:"via,omitempty"` // webhook|polling|sse|stomp|mcp
}
```

### Validation function

```go
// internal/feed/validate.go

package feed

import "fmt"

func (f FeedConfig) Validate() error {
    if f.Name == "" || f.Source == "" {
        return fmt.Errorf("feed: name and source are required")
    }
    if !validTier(f.Tier) {
        return fmt.Errorf("feed %s: invalid tier %q", f.Name, f.Tier)
    }
    if !validAxis(f.Axis) {
        return fmt.Errorf("feed %s: invalid axis %q", f.Name, f.Axis)
    }

    switch f.Tier {
    case TierStream:
        if f.SLA.FreshnessBudget == nil {
            return fmt.Errorf("feed %s: stream tier requires sla.freshness_budget", f.Name)
        }
        if f.SLA.Schedule != nil || f.SLA.WindowStart != nil || f.SLA.TTL != nil {
            return fmt.Errorf("feed %s: stream tier forbids sla.schedule, sla.window_*, sla.ttl", f.Name)
        }
        if f.Recovery.Mode != RecoveryReplayFromQueue {
            return fmt.Errorf("feed %s: stream tier requires recovery.mode = replay-from-queue", f.Name)
        }
        if f.Recovery.IdempotencyKey == "" {
            return fmt.Errorf("feed %s: stream tier requires recovery.idempotency_key", f.Name)
        }
    case TierChangeFeed:
        if f.SLA.FreshnessBudget == nil || f.SLA.MaxLag == nil {
            return fmt.Errorf("feed %s: change-feed tier requires sla.freshness_budget and sla.max_lag", f.Name)
        }
        if f.Recovery.Mode != RecoveryCatchUpFromWatermark {
            return fmt.Errorf("feed %s: change-feed tier requires recovery.mode = catch-up-from-watermark", f.Name)
        }
        if f.Recovery.WatermarkField == "" || f.Recovery.IdempotencyKey == "" {
            return fmt.Errorf("feed %s: change-feed tier requires recovery.watermark_field and recovery.idempotency_key", f.Name)
        }
    case TierDailyBatch:
        if f.SLA.Schedule == nil {
            return fmt.Errorf("feed %s: daily-batch tier requires sla.schedule (cron)", f.Name)
        }
        if f.Recovery.Mode != RecoveryRerunJob {
            return fmt.Errorf("feed %s: daily-batch tier requires recovery.mode = rerun-job", f.Name)
        }
    case TierBulkWindow:
        if f.SLA.WindowStart == nil || f.SLA.WindowEnd == nil {
            return fmt.Errorf("feed %s: bulk-window tier requires sla.window_start and sla.window_end (cron)", f.Name)
        }
        if f.Recovery.Mode != RecoveryRescheduleWindow {
            return fmt.Errorf("feed %s: bulk-window tier requires recovery.mode = reschedule-window", f.Name)
        }
    case TierReference:
        if f.SLA.TTL == nil {
            return fmt.Errorf("feed %s: reference tier requires sla.ttl", f.Name)
        }
        if f.Recovery.Mode != RecoveryForceResync {
            return fmt.Errorf("feed %s: reference tier requires recovery.mode = force-resync", f.Name)
        }
    }

    if f.Axis == AxisAdapter && (len(f.Producers) == 0 || len(f.Consumers) == 0) {
        return fmt.Errorf("feed %s: axis A (adapter) requires producers and consumers", f.Name)
    }
    return nil
}
```

### Persistence

The feed registry is loaded at deploy time from `deploy/feeds/*.yaml` and persisted to `app.feed_registry` for runtime queries:

```sql
CREATE TABLE app.feed_registry (
    name              TEXT PRIMARY KEY,
    source            TEXT NOT NULL,
    tier              TEXT NOT NULL,
    axis              TEXT NOT NULL,
    description       TEXT NOT NULL,
    config            JSONB NOT NULL,            -- full FeedConfig
    yaml_sha256       TEXT NOT NULL,             -- detect drift between repo and registry
    registered_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_validated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX feed_registry_tier_idx ON app.feed_registry(tier);
CREATE INDEX feed_registry_axis_idx ON app.feed_registry(axis);
CREATE INDEX feed_registry_source_idx ON app.feed_registry(source);
```

The deploy job runs `feed-registry-load` which reads `deploy/feeds/*.yaml`, validates each, and upserts into `app.feed_registry`. Drift between disk and registry (e.g., a feed YAML deleted from disk but row still in registry) emits a warning at deploy and a daily reconciliation alert.

---

## Layer 2 — Agent Contract

Each agent (`cmd/<service>/`) declares which feeds it consumes and produces. Declarations live in `Brain/wiki/cards/<agent>.md` per [[agent-card-format]] frontmatter, plus a parallel YAML manifest at `cmd/<service>/feeds.yaml` for fast boot-time loading without parsing markdown.

### Agent feed manifest

```yaml
# cmd/chirp/feeds.yaml
agent: chirp
binary: cmd/chirp
consumes:
  - feed: canary.transactions.detection
    expected_tier: change-feed
    freshness_budget: 30s
  - feed: counterpoint.transactions
    expected_tier: change-feed
    freshness_budget: 5m
  - feed: items.master
    expected_tier: reference
    freshness_budget: 5m
produces:
  - feed: canary.alerts.events
    declared_tier: stream
```

### Boot-time validation

When `cmd/chirp` starts, it:

1. Loads its own `feeds.yaml`
2. For each `consumes[i]`: queries the feed registry for that feed name, asserts `registry.tier == consumes[i].expected_tier`. **Mismatch is a fatal error.**
3. For each `produces[i]`: asserts feed exists in registry and `registry.tier == produces[i].declared_tier`. **Mismatch is a fatal error.**
4. Logs each validated dependency with tier and freshness budget at INFO level.
5. If any error: process exits with code 78 (`EX_CONFIG`), one log line per error.

### Go interface

```go
// internal/feed/contract.go

package feed

type AgentContract struct {
    Agent    string             `yaml:"agent"`
    Binary   string             `yaml:"binary"`
    Consumes []FeedDependency   `yaml:"consumes"`
    Produces []FeedProduction   `yaml:"produces"`
}

type FeedDependency struct {
    Feed             string         `yaml:"feed"`
    ExpectedTier     Tier           `yaml:"expected_tier"`
    FreshnessBudget  time.Duration  `yaml:"freshness_budget"`
}

type FeedProduction struct {
    Feed         string `yaml:"feed"`
    DeclaredTier Tier   `yaml:"declared_tier"`
}

type RegistryReader interface {
    GetFeed(ctx context.Context, name string) (*FeedConfig, error)
}

func ValidateContract(ctx context.Context, c AgentContract, registry RegistryReader) []error {
    var errs []error
    for _, dep := range c.Consumes {
        feed, err := registry.GetFeed(ctx, dep.Feed)
        if err != nil {
            errs = append(errs, fmt.Errorf("agent %s: consumes feed %s: %w", c.Agent, dep.Feed, err))
            continue
        }
        if feed.Tier != dep.ExpectedTier {
            errs = append(errs, fmt.Errorf("agent %s: consumes feed %s at expected tier %s but registry has tier %s",
                c.Agent, dep.Feed, dep.ExpectedTier, feed.Tier))
        }
    }
    for _, prod := range c.Produces {
        feed, err := registry.GetFeed(ctx, prod.Feed)
        if err != nil {
            errs = append(errs, fmt.Errorf("agent %s: produces feed %s: %w", c.Agent, prod.Feed, err))
            continue
        }
        if feed.Tier != prod.DeclaredTier {
            errs = append(errs, fmt.Errorf("agent %s: produces feed %s at declared tier %s but registry has tier %s",
                c.Agent, prod.Feed, prod.DeclaredTier, feed.Tier))
        }
    }
    return errs
}
```

### Wiring into agent boot

```go
// cmd/chirp/main.go (excerpt)

func main() {
    ctx := context.Background()

    // 1. Standard service init: config, DB, valkey
    cfg := config.Load()
    db := database.MustConnect(ctx, cfg.PostgresURL)
    registry := feed.NewPostgresRegistry(db)

    // 2. Load agent contract
    contract, err := feed.LoadAgentContract("cmd/chirp/feeds.yaml")
    if err != nil {
        log.Fatalf("load agent contract: %v", err)
    }

    // 3. Validate against registry — fatal on mismatch
    if errs := feed.ValidateContract(ctx, contract, registry); len(errs) > 0 {
        for _, e := range errs {
            log.Printf("FATAL contract validation: %v", e)
        }
        os.Exit(78) // EX_CONFIG
    }

    // 4. Proceed with normal startup
    server := chirp.NewServer(cfg, db, registry)
    log.Fatal(server.Run(ctx))
}
```

The boot validation pattern is identical for every service. A small helper (`feed.MustValidateContract`) wraps steps 2–3 to keep main() short.

---

## Layer 3 — Runtime Behavior

Tier-specific runtime code lives in `internal/runtime/` with one file per tier. Each tier handler implements the `TierRuntime` interface; the runtime selects the handler based on the registered tier of the feed.

### `TierRuntime` interface

```go
// internal/runtime/tier_runtime.go

package runtime

import (
    "context"
    "time"

    "github.com/growdirect-llc/rapidpos/internal/feed"
)

type HealthState int

const (
    HealthGreen HealthState = iota
    HealthAmber
    HealthRed
)

type Alert struct {
    Pattern  feed.AlertPattern
    FeedName string
    Severity string
    Message  string
    Context  map[string]any
}

type TierRuntime interface {
    HealthCheck(ctx context.Context, f feed.FeedConfig, lastObserved time.Time) HealthState
    Retry(ctx context.Context, f feed.FeedConfig, err error) error
    AlertsFor(state HealthState, f feed.FeedConfig) []Alert
    Recover(ctx context.Context, f feed.FeedConfig) error
}

func ForTier(t feed.Tier) TierRuntime {
    switch t {
    case feed.TierStream:
        return &StreamTier{}
    case feed.TierChangeFeed:
        return &ChangeFeedTier{}
    case feed.TierDailyBatch:
        return &DailyBatchTier{}
    case feed.TierBulkWindow:
        return &BulkWindowTier{}
    case feed.TierReference:
        return &ReferenceTier{}
    }
    panic("unknown tier")
}
```

### Per-tier behavior table

| Behavior | Stream | Change-feed | Daily batch | Bulk window | Reference |
|---|---|---|---|---|---|
| Health input | last heartbeat timestamp | last watermark advance | last successful run | last window completion | last revalidation |
| Green if | now − last < freshness_budget | now − watermark < max_lag | last run within today's slot | window completed within window range | now − revalidated < ttl |
| Amber if | freshness_budget ≤ now − last < 2× | max_lag ≤ now − watermark < 2× | run started but not completed | window started but not completed | ttl ≤ now − revalidated < 2× |
| Red if | now − last ≥ 2× freshness_budget | now − watermark ≥ 2× max_lag | scheduled slot missed entirely | window slot missed entirely | now − revalidated ≥ 2× ttl |
| Retry policy | exponential backoff to circuit break | bounded retry up to max_lag | rerun on next slot | manual reschedule | revalidate via ETag/If-None-Match |
| Alert pattern | heartbeat-lost | lag-exceeded | schedule-missed | window-missed | version-drift |
| Recovery primitive | replay queue from last ack | catch up from `recovery.watermark_field` cursor | rerun the job for the slot | reschedule the window or run partial | force resync (full pull) |

### Stream tier handler (illustrative)

```go
// internal/runtime/stream.go

package runtime

import (
    "context"
    "time"

    "github.com/growdirect-llc/rapidpos/internal/feed"
)

type StreamTier struct{}

func (s *StreamTier) HealthCheck(ctx context.Context, f feed.FeedConfig, lastObserved time.Time) HealthState {
    age := time.Since(lastObserved)
    budget := *f.SLA.FreshnessBudget
    switch {
    case age < budget:
        return HealthGreen
    case age < 2*budget:
        return HealthAmber
    default:
        return HealthRed
    }
}

func (s *StreamTier) Retry(ctx context.Context, f feed.FeedConfig, err error) error {
    // Exponential backoff with circuit break — implementation omitted for brevity.
    return nil
}

func (s *StreamTier) AlertsFor(state HealthState, f feed.FeedConfig) []Alert {
    if state == HealthGreen {
        return nil
    }
    sev := "warning"
    if state == HealthRed {
        sev = "critical"
    }
    return []Alert{{
        Pattern:  feed.AlertHeartbeatLost,
        FeedName: f.Name,
        Severity: sev,
        Message:  "stream feed heartbeat exceeded freshness budget",
        Context: map[string]any{
            "freshness_budget": f.SLA.FreshnessBudget.String(),
            "tier":             f.Tier,
            "axis":             f.Axis,
        },
    }}
}

func (s *StreamTier) Recover(ctx context.Context, f feed.FeedConfig) error {
    // Replay from last-ack queue position. Implementation reads
    // f.Recovery.IdempotencyKey to deduplicate during replay.
    return nil
}
```

The other four tier files (`changefeed.go`, `dailybatch.go`, `bulkwindow.go`, `reference.go`) follow the same shape with tier-specific algorithms. Each is one file, ≤200 lines.

### Where runtime fires from

A long-lived watcher in each service runs a per-feed health loop:

```go
// internal/runtime/watcher.go (excerpt)

func (w *Watcher) WatchFeed(ctx context.Context, f feed.FeedConfig) {
    handler := ForTier(f.Tier)
    ticker := time.NewTicker(w.checkInterval(f.Tier))
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            lastObserved := w.lastObservedTime(f)
            state := handler.HealthCheck(ctx, f, lastObserved)
            for _, alert := range handler.AlertsFor(state, f) {
                w.publishAlert(ctx, alert)
            }
            if state == HealthRed {
                if err := handler.Recover(ctx, f); err != nil {
                    log.Printf("recovery failed for feed %s: %v", f.Name, err)
                }
            }
        }
    }
}

func (w *Watcher) checkInterval(t feed.Tier) time.Duration {
    switch t {
    case feed.TierStream:      return 1 * time.Second
    case feed.TierChangeFeed:  return 1 * time.Minute
    case feed.TierDailyBatch:  return 5 * time.Minute
    case feed.TierBulkWindow:  return 1 * time.Hour
    case feed.TierReference:   return 1 * time.Hour
    }
    return 1 * time.Hour
}
```

The watcher is started by every service that produces or consumes feeds. The check interval is tier-determined — there's no value in checking a reference feed every second.

---

## Layer 4 — Surface

### Bases query (Brain wiki rollup)

Layer 4 makes tier health visible. The Brain wiki has a Bases query at `Brain/canary-go-feed-health.base`:

```yaml
# Brain/canary-go-feed-health.base
filters:
  type: feed
views:
  by-tier:
    type: cards
    group-by: tier
    sort:
      - tier asc
      - state asc
    columns:
      - name
      - source
      - axis
      - tier
      - state
      - last_observed
      - alert_pattern
  needs-attention:
    type: cards
    filter: state in [amber, red]
    group-by: tier
    sort: state desc, last_observed asc
```

The Bases query reads `feed` cards (one per registered feed, generated at deploy time from `deploy/feeds/*.yaml`) and renders a five-tier rollup. Operators see five health indicators, not a binary green/red. **This is the structural commitment that prevents change-feed lag from hiding behind a green stream.**

### Generated feed cards

Deploy time, the registry-load job emits one card per feed:

```markdown
---
card-type: feed
card-id: counterpoint.transactions
card-version: 1
domain: lp
layer: infra
status: approved
type: feed
tier: change-feed
axis: A
source: counterpoint
state: green                  # filled by reconciler — not authored by hand
last_observed: 2026-04-30T...  # filled by reconciler
---

# Feed: counterpoint.transactions

## What this is
Counterpoint transaction stream from VAR-deployed merchants.

## Tier
change-feed (cadence: minutes-to-hours, max_lag: 30m, freshness_budget: 15m)

## Producers
- bull (cmd/bull) — polls Counterpoint REST every 60s

## Consumers
- tsp (cmd/tsp) — webhook receiver

## Recovery
catch-up-from-watermark on `occurred_at`, idempotency_key on `external_id`

## Alert pattern
lag-exceeded → ops queue
```

These cards regenerate on deploy from the registry. Hand edits are stomped — the generator owns this file space.

### Ops Dashboard SSE channel

`canary-ops-dashboard` (port :9084) exposes `GET /ops-dashboard/sse?scope=tier-rollup` that emits per-tier health events on state change:

```
event: tier.state.changed
data: {"tier":"change-feed","state":"amber","lagging_feeds":["counterpoint.transactions","square.payments"],"observed_at":"..."}
```

Five separate state machines run server-side, one per tier. The UI renders five indicators. The `canary-ops` MCP tool `ops.health_rollup` reads the same data.

### Endpoint Library Tier column (already shipped)

The [[canary-go-endpoint-library|Endpoint Library]] Tier column on every endpoint table is populated from the feed registry plus per-endpoint metadata in `microservice-architecture.md`. Layer 4 closes the loop: the library's Tier column is no longer aspirational documentation — it's data sourced from the registry this SDD specifies.

---

## Database Schema

```sql
-- deploy/migrations/feed-registry/0001_init.up.sql

CREATE TABLE app.feed_registry (
    name              TEXT PRIMARY KEY,
    source            TEXT NOT NULL,
    tier              TEXT NOT NULL CHECK (tier IN ('stream','change-feed','daily-batch','bulk-window','reference')),
    axis              TEXT NOT NULL CHECK (axis IN ('A','B','C')),
    description       TEXT NOT NULL,
    config            JSONB NOT NULL,
    yaml_sha256       TEXT NOT NULL,
    registered_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_validated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX feed_registry_tier_idx ON app.feed_registry(tier);
CREATE INDEX feed_registry_axis_idx ON app.feed_registry(axis);
CREATE INDEX feed_registry_source_idx ON app.feed_registry(source);

CREATE TABLE app.feed_observations (
    feed_name      TEXT NOT NULL REFERENCES app.feed_registry(name) ON DELETE CASCADE,
    observed_at    TIMESTAMPTZ NOT NULL,
    watermark      TIMESTAMPTZ NULL,
    state          TEXT NOT NULL CHECK (state IN ('green','amber','red')),
    metadata       JSONB,
    PRIMARY KEY (feed_name, observed_at)
);

CREATE INDEX feed_observations_recent_idx
  ON app.feed_observations(feed_name, observed_at DESC);

CREATE TABLE app.feed_alerts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    feed_name      TEXT NOT NULL REFERENCES app.feed_registry(name) ON DELETE CASCADE,
    pattern        TEXT NOT NULL,
    severity       TEXT NOT NULL,
    message        TEXT NOT NULL,
    context        JSONB,
    raised_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at    TIMESTAMPTZ NULL,
    resolved_by    UUID NULL
);

CREATE INDEX feed_alerts_open_idx ON app.feed_alerts(feed_name, raised_at DESC) WHERE resolved_at IS NULL;
```

`app.feed_registry` is loaded at deploy. `app.feed_observations` is written by the per-service `Watcher`. `app.feed_alerts` is written by `AlertsFor` outputs and read by canary-alert for downstream routing.

---

## Implementation Order

1. **`internal/feed/types.go`** — Tier, Axis, RecoveryMode, AlertPattern, FeedConfig, AgentContract structs (Layer 1 + 2 type definitions).
2. **`internal/feed/validate.go`** — `Validate()` on FeedConfig and `ValidateContract()` on AgentContract (Layer 1 + 2 enforcement).
3. **`deploy/migrations/feed-registry/`** — Three migrations (registry, observations, alerts).
4. **`internal/feed/registry.go`** — Postgres-backed `RegistryReader` and writer for deploy-time loading.
5. **`cmd/feed-registry-load/main.go`** — Deploy-time entrypoint that walks `deploy/feeds/*.yaml`, validates, upserts to registry.
6. **`internal/runtime/tier_runtime.go`** + 5 tier files — TierRuntime interface and implementations (Layer 3).
7. **`internal/runtime/watcher.go`** — Per-feed health loop, used by every service.
8. **Wire into all 32 services** — Each service's `main.go` validates its agent contract at boot before serving traffic. PR per service or one big PR — choose during build.
9. **`Brain/canary-go-feed-health.base`** + feed-card generator — Layer 4 wiki surface.
10. **`canary-ops-dashboard` SSE channel** — `/ops-dashboard/sse?scope=tier-rollup` emits per-tier state changes.

Each step is independent enough to ship as one PR. Steps 1–4 are foundational and unblock everything else; steps 5–8 fan out across services in parallel.

---

## Out of Scope

- Per-feed implementations (each adapter and service specifies its own feed YAMLs as part of its own dispatch).
- Migration of existing services to declarative feed config — separate dispatch per service. Until migrated, services continue to operate without registry-backed validation; validation kicks in once the feed YAML lands.
- Cross-merchant feed registry sharding — single registry per deploy is sufficient for current scale.
- Alert routing strategy — `app.feed_alerts` is consumed by canary-alert which owns routing; this SDD writes to the table only.
- Bases query rendering details — Layer 4 specifies the query; the rendering is per [[obsidian-method|kepano method]] vendored under `Brain/external-skills/`.

---

## Pitfalls

- **Don't bypass tier validation in dev.** Tempting to skip the boot-time check for fast iteration. Skip it once and you get the silent 2 a.m. drift the entire ladder is designed to prevent. If you need to iterate fast, add a feed YAML — don't disable the validator.
- **Don't reinvent recovery modes.** Five modes cover the five tiers; adding a sixth means either inventing a sixth tier (probably wrong) or a service-specific shortcut (always wrong).
- **Don't write feed cards by hand.** Generated from registry on deploy. Hand edits get stomped. Edit the YAML, redeploy.
- **Don't make the watcher check interval shorter than the tier's natural cadence.** Polling a reference feed every second wastes CPU and noises up logs without surfacing a faster signal.

---

## Cross-References

- [[canary-go-cadence-ladder|Cadence Ladder]] — descriptive wiki this SDD makes buildable
- [[canary-go-endpoint-library|Endpoint Library]] — Tier column on every endpoint sourced from this registry
- [[microservice-architecture]] — per-service endpoint contracts that map onto feeds
- [[agent-contracts]] — agent-to-module smart contract pattern (Layer 2 builds on this)
- [[go-module-layout]] — file paths under `cmd/`, `internal/`, `deploy/`
- [[agent-card-format]] — feed-card generator emits cards that conform to this format
- Memory: `project_canary_canonical_positioning`, `project_engine_map_and_main_street_archetype`, `feedback_no_volatile_data_in_wiki`, `feedback_no_hand_rolling_outside_core_ip`
