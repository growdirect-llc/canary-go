---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
patent: "Application #63/991,596 (hash-before-parse, chain hash, Merkle inscription)"
---

# TSP Stage 4 — Chirp Detection (Exception Engine)

> **Type:** Pipeline Consumer Worker — Real-Time Exception Detection and Alert Generation
> **Parent SDD:** `tsp.md` (pipeline overview)
> **Consumer Group:** `detection-engine` on stream `canary:detection`
> **Related:** `hawk.md` (LP case management), `raas.md` (rule authoring)

---

## Governing Thesis

Stage 4 is where sealed, parsed POS events become actionable intelligence. The detection engine evaluates every transaction-class event against a configurable rule set and generates a chirp alert when a rule fires. The chirp is the product signal — the thing the merchant actually sees and acts on. Everything upstream (seal, parse, Merkle) exists to make the chirp trustworthy. Stage 4 is where trust converts to value.

The design bet is in-stream detection over batch detection. Latency is seconds, not hours. The trade-off is false positive rate on pattern rules that need shift-level context — mitigated by sliding windows in Valkey rather than waiting for shift-close. The merchant gets an alert while the shift is still live, not the next morning.

---

## Business Hat

### Why This Stage Exists

The median LP incident at an SMB retailer is not caught by exception reporting run overnight. It's caught by a cashier noticing something, or not caught at all. The goal of Stage 4 is to compress the detection window from 24–48 hours to under 60 seconds — without requiring the merchant to be watching a dashboard.

The rule types reflect the actual attack patterns: void abuse, refund fishing, discount stacking, cash drawer manipulation, gift card laundering. These are not hypothetical. They are the patterns that appear in every LP corpus from retail operators at the $5M–$50M revenue tier.

### Business Invariants

| Invariant | Why It Matters |
|-----------|---------------|
| Rules stored in DB, hot-reloaded every 60s | Rules can be tuned or added without a deployment; operators can respond to emerging patterns |
| Alert created per rule-event match | One event can fire multiple rules; each fires an independent alert |
| High-confidence detections auto-open Hawk LP cases | No manual triage required for high-confidence hits above score threshold |
| Dual DB sessions (read from canary_sales, write to canary_app) | Detection reads and alert writes must not share a transaction — separation of concerns and connection isolation |
| Missing CRDM record = silent ACK | Stage 4 does not retry missing records; if Stage 2 dropped the event, Stage 4 cannot recover it |

### Trade-Off: In-Stream vs. Batch Detection

In-stream detection (this approach) has lower latency but higher false positive rate on pattern rules that need a full shift's context. The mitigation is the sliding window: pattern rules evaluate against a Valkey-backed window of recent events for the same merchant/employee/register, not just the current event. The window size is configurable per rule.

Batch detection (nightly) has near-zero false positives on shift-level patterns but 12–16 hour detection latency. For cash drawer rules (which need shift close data), Stage 4 can trigger a second evaluation pass on `cash_drawer_shift.closed` events, which carry the full shift context.

The architecture supports both: in-stream for immediate signals, batch for shift-level confirmation. V1 implements in-stream only.

---

## Technical Hat

### Consumer Loop

Stage 4 supports multiple workers within the same consumer group for horizontal scaling. Each worker:

1. **PEL recovery:** Read pending entries from `canary:detection` with `XREADGROUP ... COUNT 100 ... STREAMS canary:detection 0` before switching to `>`.
2. Call `XREADGROUP GROUP detection-engine worker-<n> COUNT 10 BLOCK 5000 STREAMS canary:detection >`
3. For each message: process (see Processing Contract).
4. On success: `XACK canary:detection detection-engine <message-id>`.
5. On failure: do NOT ACK.
6. Consecutive error tracking: 10 consecutive errors → self-terminate.
7. Heartbeat write every iteration.

Note: Stage 4 reads from `canary:detection`, NOT `canary:events`. Stage 2 publishes the detection routing envelope; Stage 4 consumes it. Stage 4 never reads the raw event stream.

### Processing Contract (per message)

```
1. VALIDATE — all 5 detection envelope fields present.
   → Missing fields: route to dead letter, ACK.

2. ROUTE by detection_type:
   → "transaction":   fetch transaction record from canary_sales
   → "cash_drawer":   fetch cash_drawer_shift or cash_drawer_event record
   → "gift_card":     fetch gift_card_activity record
   → "loyalty":       fetch loyalty_event record

3. FETCH CRDM RECORD from canary_sales (read-only session).
   → Record not found: ACK silently. Log warning with event_id and detection_type.
     Do not retry — if Stage 2 dropped the record, it will not appear.

4. LOAD RULES from Valkey rule cache.
   → Cache key: rules:{detection_type}:{merchant_id}
   → Cache miss: load from DB (detection_rules table), populate cache with 60s TTL.
   → If no rules configured for this merchant/type: ACK. No alerts generated.

5. EVALUATE each active rule against the CRDM record.
   → Rule evaluation is pure: (rule, record, context) → (fired bool, score float64, evidence map)
   → Context for pattern rules: sliding window from Valkey (see Sliding Window below).

6. FOR EACH FIRED RULE:
   a. Write alert to canary_app.alerts (write session, separate from read session).
      → ON CONFLICT (merchant_id, event_id, rule_id) DO NOTHING (idempotency).
   b. If alert.confidence_score >= HAWK_AUTO_OPEN_THRESHOLD (default 0.8):
      Publish to canary-chirp stream for Hawk LP case auto-open.

7. UPDATE sliding window in Valkey (best-effort; failure does not block ACK).

8. ACK the detection stream message.
```

### Rule Engine

Rules are stored in `canary_app.detection_rules` and loaded into Valkey per `(detection_type, merchant_id)` with a 60-second TTL. The rule engine evaluates each active rule as a pure function.

**Rule types:**

| Rule Type | Description | Sliding Window |
|-----------|-------------|----------------|
| `threshold` | Metric exceeds a configured value in a window (e.g., void rate > 5% in 1 hour) | Yes — Valkey sorted set keyed by merchant + employee + register |
| `pattern` | Same actor, same action, N times in a window (e.g., 3 refunds > $50 by same cashier in 30 min) | Yes |
| `cross_reference` | Transaction attribute not present in an expected reference (e.g., tender type not matching receiving chain) | No |
| `absolute` | Single-event rule with no window (e.g., any transaction voided after tender) | No |

**Scoring:** Each rule produces a `confidence_score` in [0.0, 1.0]. The score reflects how strongly the evidence supports the rule fire. Threshold rules typically score 0.5–0.7; pattern rules with N > 1 occurrences can score up to 0.9; cross-reference rules score 0.8–1.0 (binary — either the cross-reference exists or it does not).

### Sliding Window (Valkey)

Pattern and threshold rules use a sliding window backed by a Valkey sorted set. The window accumulates event-level signals, not full records.

```
Window key: detect:window:{detection_type}:{merchant_id}:{employee_id}:{rule_id}
Window value: ZADD ... <unix_timestamp> <event_id_or_amount>
Window TTL: max(rule.window_seconds) + 300s buffer
```

On each evaluation:
1. `ZADD` the current event to the window.
2. `ZREMRANGEBYSCORE` to trim entries older than `rule.window_seconds`.
3. `ZCARD` (count) or `ZRANGEBYSCORE` (sum) to compute the metric.
4. Compare metric to rule threshold.

The window update in step 7 of the processing contract is best-effort: if Valkey is unavailable, the rule evaluation still completes (with whatever stale window state was available), and the ACK proceeds. Window accuracy degrades gracefully; it does not block alert generation.

### Hawk Integration

High-confidence alerts (score >= `HAWK_AUTO_OPEN_THRESHOLD`) are published to the `canary-chirp` Valkey stream. The Hawk LP case manager consumes this stream and auto-opens an LP case. The publish is asynchronous and non-blocking.

```
XADD canary-chirp * \
  alert_id    <uuid> \
  merchant_id <id> \
  rule_id     <uuid> \
  event_id    <ulid> \
  score       0.87 \
  detection_type transaction
```

If the `canary-chirp` publish fails, the alert record in `canary_app.alerts` is still written. Hawk can poll for unprocessed high-confidence alerts via the `get_unprocessed_hawk_candidates` MCP tool as a recovery path.

### Dual Database Session Requirement

Stage 4 requires two independent PostgreSQL connections:

1. **Read session** — connects to `canary_sales` schema. Used for fetching CRDM records in step 3. **Read-only.** Must not be used for any writes.
2. **Write session** — connects to `canary_app` schema. Used for writing alerts in step 6. **Isolated from the read session.** The two sessions must not share a transaction.

This isolation prevents detection reads from being blocked by alert writes and vice versa. It also ensures that a failed alert write does not roll back a CRDM read or corrupt the detection state.

In Go, each worker maintains two `*pgxpool.Pool` instances — one per schema. They are initialized at startup and reused across iterations.

---

## Data Model

### `canary_app.detection_rules`

Rule configuration. Hot-reloaded into Valkey every 60 seconds.

```sql
CREATE TABLE canary_app.detection_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     TEXT NOT NULL,
    detection_type  TEXT NOT NULL,                  -- transaction | cash_drawer | gift_card | loyalty
    rule_type       TEXT NOT NULL,                  -- threshold | pattern | cross_reference | absolute
    name            TEXT NOT NULL,
    description     TEXT,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    config          JSONB NOT NULL,                 -- rule-specific parameters (thresholds, window sizes, etc.)
    hawk_threshold  NUMERIC,                        -- if score >= this, auto-open Hawk case (NULL = never auto-open)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_detection_rules_merchant_type ON canary_app.detection_rules (merchant_id, detection_type) WHERE is_active = true;
```

**Rule config examples:**

```json
// threshold rule: void rate
{
  "metric": "void_rate",
  "threshold": 0.05,
  "window_seconds": 3600,
  "min_transaction_count": 10
}

// pattern rule: same cashier, same amount, N refunds in window
{
  "action": "refund",
  "group_by": ["employee_id"],
  "count_threshold": 3,
  "amount_min_cents": 5000,
  "window_seconds": 1800
}

// cross_reference rule: transaction not in receiving chain
{
  "reference": "receiving_chain",
  "match_field": "external_id"
}
```

### `canary_app.alerts`

One row per rule-event match. The alert record is the output of Stage 4.

```sql
CREATE TABLE canary_app.alerts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         TEXT NOT NULL,
    rule_id             UUID NOT NULL REFERENCES canary_app.detection_rules(id),
    event_id            TEXT NOT NULL,                  -- ULID from gateway
    detection_type      TEXT NOT NULL,
    transaction_id      UUID,                           -- FK to canary_sales.transactions if applicable
    confidence_score    NUMERIC NOT NULL,               -- 0.0 to 1.0
    evidence            JSONB NOT NULL,                 -- structured evidence map from rule evaluation
    hawk_case_id        UUID,                           -- populated if Hawk auto-opened a case
    hawk_published_at   TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'open',   -- open | acknowledged | resolved | false_positive
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_alert_merchant_event_rule UNIQUE (merchant_id, event_id, rule_id)
);

CREATE INDEX idx_alerts_merchant_status ON canary_app.alerts (merchant_id, status, created_at DESC);
CREATE INDEX idx_alerts_merchant_score ON canary_app.alerts (merchant_id, confidence_score DESC) WHERE status = 'open';
CREATE INDEX idx_alerts_event_id ON canary_app.alerts (event_id);
```

---

## API Contract

Stage 4 does not expose HTTP endpoints. Alerts are surfaced through the Canary application UI and Hawk LP case manager, not through TSP service endpoints.

The following MCP tools surface Stage 4 output:

### MCP Tool Surface

| Tool | Write | Description |
|------|:-----:|-------------|
| `get_open_alerts` | No | List open alerts for a merchant, optionally filtered by detection_type or rule_id, sorted by confidence_score desc |
| `get_alert_detail` | No | Full alert record including evidence map and rule config snapshot |
| `acknowledge_alert` | Yes | Set alert status to `acknowledged` — requires authenticated session |
| `get_rule_config` | No | List active detection rules for a merchant |
| `reload_rules` | Yes | Force rule cache invalidation for a merchant (next evaluation reloads from DB) — admin only |
| `get_hawk_candidates` | No | List high-confidence alerts that have not yet been published to Hawk (for recovery) |

All write-capable MCP tools require an authenticated session (P0-TSP-09).

---

## SLA

| Metric | P50 | P99 | Hard Limit |
|--------|-----|-----|------------|
| Detection latency (stream message receipt → alert written) | 20ms | 100ms | 2000ms |
| CRDM record fetch | 3ms | 20ms | 200ms |
| Rule cache load (Valkey hit) | <1ms | 3ms | 20ms |
| Rule cache load (Valkey miss → DB load) | 5ms | 30ms | 200ms |
| Rule evaluation (per rule, pure function) | <1ms | 5ms | 20ms |
| Alert write (per alert) | 3ms | 15ms | 100ms |
| Hawk publish (XADD canary-chirp) | 1ms | 5ms | 50ms |
| Sliding window update (Valkey ZADD + ZREMRANGE) | <1ms | 3ms | 20ms |

**End-to-end detection latency** (from POS event to alert generated): includes Stage 2 parse (~80ms) + `canary:detection` stream latency (<5ms) + Stage 4 detection (~50ms). Total: ~135ms P50, ~400ms P99 under normal load.

---

## Failure Modes

| Failure | Detection | Impact | Recovery |
|---------|-----------|--------|----------|
| Missing CRDM record | DB query returns no rows | Silent ACK; log warning | Stage 4 does not retry; record was never written by Stage 2 |
| DB read failure (canary_sales) | pgx error | No ACK; PEL redelivery | Auto-reconnect; retry on recovery |
| DB write failure (canary_app.alerts) | pgx error | No ACK; PEL redelivery | Auto-reconnect; retry. Idempotent on re-evaluation |
| Duplicate alert (unique constraint) | ON CONFLICT DO NOTHING | Silent ACK | Expected and correct |
| Rule cache miss + DB load failure | pgx error | No rules evaluated; ACK | Log error; alert on repeated cache miss + DB failure |
| Sliding window update failure | Valkey error | Rule evaluated with stale window; best-effort | Degrade gracefully; log warning |
| Hawk publish failure | Valkey XADD error | Alert written but Hawk not notified | `get_hawk_candidates` MCP tool surfaces unprocessed high-confidence alerts for manual recovery |
| 10 consecutive processing errors | Internal counter | Worker self-terminates | Process manager restart; investigate root cause |
| Rule config error (invalid JSON) | Struct validation | Rule skipped; log error | Fix rule config in DB; force cache reload |

---

## Compliance

### PII Handling

Stage 4 reads PII-bearing CRDM records from `canary_sales` (specifically `transactions`, which contains encrypted card fields and employee IDs). The rule evaluation functions operate on these fields — they may compare `employee_id` values or `amount_cents`, but they never store PII in the alert record.

The `evidence` JSONB field in `canary_app.alerts` must contain only:
- Non-PII identifiers (transaction_id, employee_id as opaque string, rule-computed metrics)
- Aggregated metrics (void count, refund total)
- No card numbers, no cardholder names, no customer PII

Any evidence field that would contain PII must be replaced with a reference ID that the application layer can resolve at display time with appropriate access controls.

### Patent Scope

Stage 4 does not implement any patent-covered primitives. Detection logic does not touch hashing, chain computation, or Merkle construction. The patent scope is entirely in Stages 1 and 3.

### Audit Logging

Alert creation and status changes must be audit-logged: `{actor_id, alert_id, action, previous_status, new_status, timestamp}`. Alert acknowledgment and resolution are operator actions that may be cited in HR or legal proceedings. The audit log is the record of who knew what and when.

### Retention

Alert records: 7 years (SOX, LP investigation lifecycle). Sliding window data (Valkey): TTL-governed, no long-term retention required.

---

## Configuration

| Variable | Default | Stage 4 Usage |
|----------|---------|---------------|
| `STAGE4_BLOCK_MS` | `5000` | XREADGROUP blocking timeout |
| `DETECTION_STREAM` | `canary:detection` | Source stream |
| `HAWK_AUTO_OPEN_THRESHOLD` | `0.8` | Confidence score threshold for Hawk auto-open |
| `RULE_CACHE_TTL_SECONDS` | `60` | Valkey rule cache TTL |
| `DATABASE_URL` | (required) | PostgreSQL connection (both sessions use same URL, different pool configs) |
| `CANARY_SALES_DB_POOL_MAX` | `5` | Max connections for canary_sales read pool |
| `CANARY_APP_DB_POOL_MAX` | `5` | Max connections for canary_app write pool |

---

## Open Items (Carry Forward to Go)

| # | Priority | Item |
|---|---------|------|
| P0-TSP-09 | P0 | Require authenticated session for all write-capable MCP tools |
| P1-TSP-01 | P1 | Audit log for alert status changes (acknowledge, resolve, false_positive) |
| P1-TSP-02 | P1 | 7-year retention for `canary_app.alerts` |
| P1-TSP-05 | P1 | Structured JSON logging with `event_id`, `merchant_id`, `rule_id`, `stage` |
| — | P1 | Implement recovery path for Hawk publish failures (`get_hawk_candidates` polling) |
| P2-TSP-04 | P2 | Document horizontal scaling profile and Valkey sliding window contention under multi-worker load |
| — | P2 | OTEL spans for end-to-end detection latency (gateway receipt → alert creation) |

---

*Canary | GrowDirect LLC | Confidential*
