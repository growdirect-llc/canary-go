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

# Alert Domain

## Purpose

The Alert domain is the terminal consumer of Canary's detection pipeline. Chirp writes alert rows; the Alert domain owns everything after: lifecycle state tracking, dollar-impact scoring, notification delivery, and agent tool access. Alerts are never forwarded to another domain — they are resolved, dismissed, escalated to Fox cases, or auto-archived by TTL.

**Core principle: APPEND-ONLY.** The `alerts` row is immutable after creation. All state changes are new `alert_history` rows. Current status is derived by reading the latest history entry per alert (no history row = status "new").

**Multi-tenant context.** Alert tables (`alerts`, `alert_history`, `notification_log`, `notification_schedule`) live per-tenant in `tenant_{merchant_id}`. Notifications are tenant-scoped — a notification preference is per merchant. Cross-tenant alert analytics (which rule families fire most across the platform) flow through `analytics` schema rollups, never direct cross-tenant queries. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Alert lifecycle operates independently of L402, ILDWAC, blockchain anchor, and vendor smart contracts (per `platform-overview.md` "Optional Features"). When `BLOCKCHAIN_ANCHOR_ENABLED=true`, alert state transitions are eligible for anchoring as an evidentiary signal — anchor failures are non-blocking. When `L402_ENABLED=true`, premium alert capabilities (e.g., expanded notification channels, real-time SMS) may be gated as paid MCP tools — the core alert lifecycle does not depend on Lightning settlement.

## Dependencies

| Dependency | Type | Required | Purpose |
|---|---|---|---|
| PostgreSQL (`canary` DB, `app` schema) | Database | Yes | alerts, alert_history, notification_log, notification_schedule tables |
| Valkey (DB 0) | Cache | Yes | Session backend for auth. Rate limiting counters for notification caps. |
| Chirp rule engine | Internal | Yes | Writes alert rows via the detection pipeline |
| Fox case service | Internal | Optional | `open-case` action creates Fox cases |
| Owl action dispatcher | Internal | Optional | Routes resolve/dismiss/case_create actions |
| Twilio (or equivalent SMS provider) | External | Optional | SMS delivery |
| Merchant settings | Internal | Yes | Notification preferences (opt-in, quiet hours, daily limit) |

## Alert Status State Machine

```
                    ┌─────────────────────────────────┐
                    │                                 ▼
new ──► investigating ──► resolved [TERMINAL]        archived [TERMINAL]
 │          │                                         ▲
 │          ├──► escalated ──► resolved [TERMINAL]    │
 │          │               ├──► case_opened [TERM.]  │
 │          │               └──► referred_to_le       │
 │          ├──► case_opened [TERMINAL]               │
 │          └──► dismissed [TERMINAL]                 │
 │                                                    │
 ├──► resolved [TERMINAL]                             │
 ├──► dismissed [TERMINAL]                            │
 └────────────────────────────────────────────────────┘
       (system:ttl auto-archival after 14 days if not terminal)
```

**Four terminal states:** `resolved`, `dismissed`, `case_opened`, `archived`.

No alert leaves the terminal state. The `dismiss` and `escalate` transitions must check for terminal status before writing — do not create duplicate history entries on already-terminal alerts.

### State Transition Rules

| From | To | Who | Requirements |
|---|---|---|---|
| new | investigating | owner/manager/admin | — |
| new | dismissed | owner/manager/admin | `reason` required |
| new | resolved | owner/manager/admin | — |
| new | case_opened | owner/operator/admin | Fox case created atomically |
| new | archived | system:ttl | Auto-archival after TTL (14 days default), not terminal AND older than TTL |
| investigating | escalated | owner/admin | — |
| investigating | resolved | owner/manager/admin | — |
| investigating | dismissed | owner/manager/admin | `reason` required |
| investigating | case_opened | owner/operator/admin | Fox case created atomically |
| escalated | resolved | owner/manager/admin | — |
| escalated | case_opened | owner/operator/admin | — |

## Data Model

All tables in the `app` schema of the `canary` database. The Alert domain owns 4 tables.

### `alerts`

Immutable after creation. Written by Chirp detection pipeline. No `updated_at` column — this table is never updated.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id (tenant isolation) |
| `rule_id` | VARCHAR(20) | No | FK → detection_rules.rule_id (e.g., "C-001") |
| `alert_type` | VARCHAR(50) | No | Rule category name |
| `severity` | VARCHAR(20) | No | critical / high / medium / low |
| `source_table` | VARCHAR(50) | No | Source data table name (e.g., "transactions") |
| `source_id` | VARCHAR(36) | No | Source record ID |
| `employee_id` | UUID | Yes | FK → employees.id (suspect linkage, nullable) |
| `location_id` | UUID | Yes | FK → locations.id (nullable) |
| `amount_cents` | INTEGER | Yes | Transaction amount in cents |
| `impact_cents` | INTEGER | Yes | Calculated dollar impact — set once at creation, never updated |
| `details` | TEXT | Yes | JSON string with rule-specific context (must be encrypted in production — see security findings) |
| `created_at` | TIMESTAMPTZ | No | Alert creation time (immutable) |
| `created_by` | VARCHAR(36) | Yes | "chirp" or user who manually triggered |

**Indexes:**
- `(merchant_id)`
- `(merchant_id, created_at)`
- `(merchant_id, rule_id)`
- `(merchant_id, employee_id)`
- `(merchant_id, location_id)`
- `(merchant_id, severity)`

### `alert_history`

Append-only status log. Every state change = new row. Status derived from `MAX(created_at)` per `alert_id`.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `alert_id` | UUID | No | FK → alerts.id |
| `status` | VARCHAR(50) | No | new / investigating / escalated / dismissed / case_opened / resolved / follow_up / archived |
| `changed_by` | VARCHAR(36) | Yes | User ID, "system:ttl" for auto-archival, "chirp" for system-generated |
| `notes` | TEXT | Yes | Free-text (required for dismiss, auto-generated for archival) |
| `created_at` | TIMESTAMPTZ | No | Status change time |

**Indexes:**
- `(alert_id)`
- `(alert_id, status)`

**Note on `changed_by`:** System actors ("system:ttl", "chirp") are not valid user UUIDs. Do not define a foreign key constraint on this column. Add a separate `actor_type` column (user / system / agent) to support structured audit queries.

### `notification_log`

Tracks every notification attempt regardless of outcome. Used for rate limiting and audit.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id |
| `alert_id` | UUID | Yes | FK → alerts.id (NULL for digest/summary notifications) |
| `channel` | VARCHAR(10) | No | email / sms / in_app / push |
| `status` | VARCHAR(12) | No | pending / sent / failed / batched / suppressed |
| `frequency_mode` | VARCHAR(10) | No | realtime / hourly / daily / weekly |
| `severity` | VARCHAR(10) | Yes | Alert severity at time of send |
| `recipient` | VARCHAR(255) | Yes | Phone number, email, or device token — **must be encrypted at rest** |
| `message_preview` | VARCHAR(255) | Yes | First 255 chars of notification body — **must be redacted to rule_id + severity only; no free-text details** |
| `failure_reason` | VARCHAR(255) | Yes | Reason for failure or suppression |
| `digest_batch_id` | UUID | Yes | Groups notifications in same digest window |
| `created_at` | TIMESTAMPTZ | No | Delivery attempt time |

**Indexes:**
- `(merchant_id, created_at)`
- `(merchant_id, status)`
- `(digest_batch_id)`

### `notification_schedule`

Per-merchant, per-category routing rules. Falls back to `_default` category if no specific match exists for the alert category.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id |
| `alert_category` | VARCHAR(30) | No | Alert category or "_default" |
| `channel_email` | BOOLEAN | No | Email notifications enabled |
| `channel_sms` | BOOLEAN | No | SMS notifications enabled |
| `channel_in_app` | BOOLEAN | No | In-app notifications enabled |
| `channel_push` | BOOLEAN | No | Push notifications enabled |
| `freq_critical` | VARCHAR(10) | No | Delivery frequency for critical severity |
| `freq_high` | VARCHAR(10) | No | Delivery frequency for high severity |
| `freq_medium` | VARCHAR(10) | No | Delivery frequency for medium severity |
| `freq_low` | VARCHAR(10) | No | Delivery frequency for low severity |
| `freq_info` | VARCHAR(10) | No | Delivery frequency for info severity |
| `hourly_cap` | INTEGER | No | Max notifications per hour (default 50) |
| `daily_cap` | INTEGER | No | Max notifications per day (default 100) |
| `impact_threshold_cents` | INTEGER | No | Minimum impact to trigger notification (0 = no filter) |
| `is_active` | BOOLEAN | No | Schedule active flag |

**Unique index:** `(merchant_id, alert_category)`

### Key Relationships

- `alerts.merchant_id` → `merchants.id` (tenant isolation)
- `alerts.employee_id` → `employees.id` (nullable, suspect linkage)
- `alerts.location_id` → `locations.id` (nullable)
- `alert_history.alert_id` → `alerts.id` (status chain)
- `fox_case_alerts.alert_id` → `alerts.id` (junction to Fox cases, owned by Fox domain)
- `notification_log.alert_id` → `alerts.id` (notification audit trail)

## Workflows

### Alert Creation Pipeline

```
1. Webhook arrives → Sub4 detection stage calls Chirp rule engine
2. Rule matches → calculate_impact(rule_id, amount_cents, severity)
   → produces impact_cents using category formula (see Impact Scoring)
3. INSERT alerts row (immutable) + INSERT alert_history(status="new") atomically
4. Risk aggregation hook: update_employee_risk() recalculates EntityRiskScore
5. dispatch_notification():
   - Check per-rule notify toggle (MerchantRuleConfig.notify_enabled)
   - Load MerchantSettings: severity threshold, quiet hours, daily limit
   - Filter: severity >= threshold? not quiet hours? under daily limit?
   - Route by frequency: realtime → immediate; hourly/daily/weekly → digest batch
   - Write NotificationLog for every attempt regardless of outcome
6. Alert appears in feed (REST /api/alerts/, Owl)
7. Merchant acts via REST or Owl action dispatcher (see State Machine)
8. Unactioned alerts: TTL auto-archival job runs daily per merchant
```

### Case Creation — Atomic Requirement

When an alert transitions to `case_opened`:
1. Create Fox case
2. Write `alert_history(status="case_opened")`

Both operations **must share the same database transaction and commit atomically**. If either fails, roll back both. Do not split across separate commits.

### TTL Auto-Archival

A scheduled job (background worker, not synchronous request path) runs daily per merchant:

```
1. Load all non-terminal alerts for merchant older than ttl_days (default 14)
   WHERE alert_id NOT IN (
       SELECT alert_id FROM alert_history
       WHERE status IN ('resolved', 'dismissed', 'case_opened', 'archived')
   )
   AND created_at < now() - (ttl_days * interval '1 day')
2. For each stale alert:
   INSERT alert_history(alert_id, status='archived', changed_by='system:ttl',
       notes='Auto-archived: unactioned for 14+ days')
3. Report: stale_count, archived_count
```

**Critical:** This must be a background job — not on-demand during request handling. The prototype has pure functions for identifying stale alerts but no scheduler to execute archival. The Go implementation must wire this to a background worker.

### Impact Scoring

Calculated once at alert creation, stored in `alerts.impact_cents` immutably. Never recalculated.

**Category formulas (by rule_id prefix):**

| Prefix | Category | Formula |
|---|---|---|
| C-1xx | Refund | `amount_cents * 1.0` |
| C-2xx | Void | `amount_cents * 1.0` |
| C-3xx | Cash variance | `abs(amount_cents)` |
| C-4xx | No-sale | `avg_txn_cents` (default $25 = 2500 cents) |
| C-5xx | Discount | `amount_cents * 1.5` |
| C-6xx | Time-based | `avg_txn_cents * 2.0` (default $25 = 2500 cents) |
| C-7xx | Custom amount | `amount_cents * 1.2` |
| C-8xx | Pattern | Severity fallback |

**Severity fallback** (when no amount available):

| Severity | Impact |
|---|---|
| critical | $500 (50000 cents) |
| high | $200 (20000 cents) |
| warning | $100 (10000 cents) |
| medium | $50 (5000 cents) |
| low | $10 (1000 cents) |
| info | $0 |

### Owl Priority Ranking

The Owl agent surface uses alert ranking to determine "The One Thing" for a merchant.

```
1. Exclude terminal alerts and alerts older than TTL (14 days)
2. age_decay_factor = 1.0 - (age_days / ttl_days), clamped [0.0, 1.0]
   (1.0 at creation, 0.5 at 7 days, 0.0 at 14 days)
3. severity_normalized: critical=1.0, high=0.75, medium=0.5, low=0.25, info=0.0
4. composite_score = (0.6 * severity_normalized) + (0.4 * impact_normalized)
   where impact_normalized = alert.impact_cents / max(impact_cents across active set)
5. Sort descending by composite_score
```

### Notification Delivery Pipeline

```
1. Alert written → dispatch_notification() called
2. Load NotificationSchedule for (merchant_id, alert.category)
   → Fallback to (merchant_id, '_default') if no category-specific rule
3. Filter checks (in order):
   a. Is per-rule notify_enabled? (check MerchantRuleConfig)
   b. Is severity >= schedule.severity_threshold?
   c. Is impact_cents >= schedule.impact_threshold_cents?
   d. Is current time inside quiet hours? (use merchant timezone — UTC is wrong)
   e. Is hourly_count < hourly_cap AND daily_count < daily_cap?
      → Rate counters maintained in Valkey with TTL-based windows
4. Route by frequency per alert severity:
   - realtime → deliver immediately
   - hourly/daily/weekly → add to digest batch (digest_batch_id)
5. Channel delivery:
   - sms: via configured SMS provider
   - email: via configured transactional email provider
   - in_app: write to notification queue
   - push: via FCM/APNs
6. Write NotificationLog row for every attempt (sent/failed/suppressed/batched)
```

**Quiet hours timezone invariant:** Quiet hours must be compared against the merchant's local time, not UTC. Resolve merchant timezone from `merchant_settings.timezone` before checking.

## REST API Contract

All routes require JWT authentication. Role-gated actions require owner/manager/admin.

### List Alerts

```
GET /api/alerts/
```

**Query parameters:**
- `severity` — filter by severity level
- `rule_id` — filter by rule ID
- `created_after` — ISO 8601 timestamp
- `created_before` — ISO 8601 timestamp
- `page` — page number (default 1)
- `limit` — page size (default 20)

**Response:**
```json
{
  "alerts": [
    {
      "id": "uuid",
      "rule_id": "C-001",
      "alert_type": "RAPID_REFUND",
      "severity": "high",
      "amount_cents": 5000,
      "impact_cents": 5000,
      "employee_id": "uuid",
      "location_id": "uuid",
      "created_at": "2026-01-15T10:30:00Z",
      "current_status": "new"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 143
  }
}
```

**Note on `current_status`:** Derived from the latest `alert_history` row by `created_at`. Do not use a per-alert subquery loop (N+1). Use a lateral join or window function:

```sql
SELECT a.*, ah.status AS current_status
FROM app.alerts a
LEFT JOIN LATERAL (
    SELECT status FROM app.alert_history
    WHERE alert_id = a.id
    ORDER BY created_at DESC
    LIMIT 1
) ah ON true
WHERE a.merchant_id = $1
  [AND filters...]
ORDER BY a.created_at DESC
LIMIT $page_size OFFSET $offset;
```

### Get Alert

```
GET /api/alerts/{alert_id}
```

Returns single alert with full `history` array sorted by `created_at ASC`.

**Response:**
```json
{
  "id": "uuid",
  "rule_id": "C-001",
  "severity": "high",
  "amount_cents": 5000,
  "impact_cents": 5000,
  "details": { ... },
  "employee_id": "uuid",
  "location_id": "uuid",
  "created_at": "2026-01-15T10:30:00Z",
  "history": [
    { "status": "new", "changed_by": null, "notes": null, "created_at": "2026-01-15T10:30:00Z" }
  ]
}
```

**Error:** 404 if alert not found or does not belong to merchant.

### Investigate Alert

```
PUT /api/alerts/{alert_id}/investigate
```

**Auth:** JWT + owner/manager/admin role

**Behavior:** Sets status to "investigating". Reject (400) if alert is already in a terminal or investigating state.

**Request body:** none required

**Response:** 200 with updated current_status, or 400 with error reason.

### Dismiss Alert

```
PUT /api/alerts/{alert_id}/dismiss
```

**Auth:** JWT + owner/manager/admin role

**Request body:**
```json
{ "reason": "Confirmed legitimate transaction — manager approved" }
```

`reason` is required — return 400 if absent.

**Behavior:** Write `alert_history(status="dismissed", notes=reason)`. Reject if already terminal.

### Escalate Alert

```
POST /api/alerts/{alert_id}/escalate
```

**Auth:** JWT + owner/admin role

**Behavior:** Write `alert_history(status="escalated")`. Reject if already terminal.

### Open Case

```
POST /api/alerts/{alert_id}/open-case
```

**Auth:** JWT + owner/operator/admin role

**Behavior:**
1. Create Fox case (via Fox service)
2. Write `alert_history(status="case_opened")`

Both steps must be **atomic** in a single transaction.

**Response:**
```json
{ "case_id": "uuid", "case_number": "CASE-2026-00042" }
```

### Alert Summary

```
GET /api/alerts/summary
```

Returns counts grouped by severity and status, plus time window counts (24h/7d/30d).

**Implementation note:** Use a single aggregate query, not a per-alert status loop.

```sql
SELECT
    a.severity,
    ah.status,
    count(a.id) AS count
FROM app.alerts a
JOIN LATERAL (
    SELECT status FROM app.alert_history
    WHERE alert_id = a.id
    ORDER BY created_at DESC LIMIT 1
) ah ON true
WHERE a.merchant_id = $1
GROUP BY a.severity, ah.status;
```

## MCP Tools (canary-alert server)

6 tools. 4 pure (no DB access), 2 DB-read.

| Tool | DB? | Input | Output |
|---|---|---|---|
| `lifecycle_summary` | No | `alerts[]`, `history_by_alert{}`, `ttl_days?` | Counts: active, stale, archived, resolved, dismissed, case_opened |
| `calculate_impact` | No | `rule_id`, `amount_cents?`, `severity?`, `avg_txn_cents?` | `impact_cents`, `impact_dollars`, `category`, `formula` |
| `rank_alerts` | No | `alerts[]`, `severity_weight?`, `impact_weight?` | Sorted alerts array, highest priority first |
| `get_impact_summary` | No | `alerts[]` | `total_impact_cents`, `count_with_impact`, `highest_impact_alert`, `impact_by_category` |
| `list_alerts` | Yes | `merchant_id`, `severity?`, `rule_id?`, `page?`, `limit?` | Paginated alert list with current_status |
| `get_alert` | Yes | `merchant_id`, `alert_id` | Single alert with full history and current_status |

**No `open_case` tool in the Alert MCP.** Case creation routes through the Owl action dispatcher (`POST /owl/action` with `action=case_create`), which orchestrates Fox case creation + AlertHistory writes in a single transaction.

### Owl Action Dispatcher (alert-related actions)

| Action Code | Resulting Status | Requirements |
|---|---|---|
| `resolve` | resolved [TERMINAL] | — |
| `dismiss` | dismissed [TERMINAL] | `reason` required |
| `case_create` | case_opened [TERMINAL] | Fox case created atomically |
| `follow_up` | follow_up | Creates Fox evidence references |

## Key Constants

| Constant | Value | Used By |
|---|---|---|
| DEFAULT_TTL_DAYS | 14 | Lifecycle staleness, archival threshold |
| ARCHIVE_ACTOR | "system:ttl" | Auto-archival `changed_by` value |
| DEFAULT_AVG_TXN_CENTS | 2500 ($25) | No-sale/off-clock impact fallback |
| severity_weight | 0.6 | Owl ranking composite |
| impact_weight | 0.4 | Owl ranking composite |
| hourly_cap | 50 | Notification rate limit (default) |
| daily_cap | 100 | Notification rate limit (default) |

## Health Check

```
GET /alert/health
→ { "service": "canary-alert", "healthy": true, "tools": 6 }
```

The health check must verify DB connectivity — not just return a static response. Return `healthy: false` with a reason if PostgreSQL is unreachable.

## Failure Modes

| Failure | Impact | Required Behavior |
|---|---|---|
| PostgreSQL down | All alert reads/writes fail | REST routes return 503. MCP tools return error payload. Alert creation in Chirp pipeline fails — webhook processing halts for that event. |
| Valkey down | Session auth (browser) fails; JWT auth still works | No rate limiting on notifications — skip cap check rather than blocking delivery. |
| SMS/email provider unavailable | Notifications not delivered | Log failure to notification_log. Alert creation unaffected. |
| Alert creation fails mid-transaction | Alert not created | Chirp pipeline rolls back. Transaction data preserved. |

## Agent-Driven Alert Triage

The domain agent for Loss Prevention (Module Q) is the first consumer of every new alert. Human operators receive escalations, not raw alert feeds. The alert state machine supports an `AGENT_REVIEWING` intermediate state to make the agent's triage window explicit and auditable.

### Agent Triage Posture

When an alert enters `new` status, the Q agent evaluates it automatically before any human notification is dispatched:

| Triage Outcome | Agent Action | Resulting State |
|---|---|---|
| Noise — rule fired but pattern is known-benign for this merchant | Dismiss with reason | `dismissed` (terminal) |
| Signal — pattern is anomalous, isolated, no prior case | Mark for human review | `HUMAN_ESCALATED` |
| Pattern — same employee/location flagged across multiple recent alerts | Aggregate and open Fox case | `case_opened` (terminal) |
| Outlier — novel pattern, insufficient evidence to classify | Hold in AGENT_REVIEWING; request additional data | `AGENT_REVIEWING` |

### State Machine Extension

The existing state machine gains one intermediate state:

```
new ──► AGENT_REVIEWING ──► investigating ──► resolved [TERMINAL]
  │          │                    │
  │          ├──► dismissed [TERMINAL]
  │          ├──► case_opened [TERMINAL]
  │          └──► HUMAN_ESCALATED ──► investigating ──► resolved [TERMINAL]
  │                                              └──► case_opened [TERMINAL]
  └── (existing transitions unchanged)
```

**`AGENT_REVIEWING`**: The Q agent has claimed the alert. No human notification is dispatched while in this state. The agent must resolve or escalate within its SLA window (see agent-contracts.md for the full contract schema — to be authored separately).

**`HUMAN_ESCALATED`**: The agent has determined that human judgment is required. The standard notification pipeline fires. The `changed_by` field must record the agent's identifier (e.g., `agent:q-lp`) as the actor, not "system".

### State Machine Implementation Notes

- Add `AGENT_REVIEWING` and `HUMAN_ESCALATED` to the `alert_history.status` enum.
- The `changed_by` field on `alert_history` rows written by the agent must use the pattern `agent:<module>-<role>` (e.g., `agent:q-lp`). This is distinct from the existing `system:ttl` actor pattern.
- Add `actor_type` column to `alert_history` with values: `user` / `system` / `agent`. The agent actor type enables audit queries that distinguish human-touched from agent-touched alerts.
- Agent triage SLA: the Q agent must resolve `AGENT_REVIEWING` alerts within 15 minutes. If the agent does not act within 15 minutes, the scheduler promotes the alert to `HUMAN_ESCALATED` automatically.

### Notification Gating

While an alert is in `AGENT_REVIEWING`, the notification pipeline is suppressed. When the agent escalates to `HUMAN_ESCALATED`, notification dispatch resumes using the standard `dispatch_notification()` path with the alert's original severity and category. No special notification path for agent-escalated alerts — the existing schedule rules apply.

---

## Monitoring

| Metric | Alert Threshold |
|---|---|
| Alert creation rate | > 1000/hour suggests webhook flood or rule misconfiguration |
| Notification suppression rate | > 90% suppressed suggests misconfigured preferences |
| Summary endpoint latency | > 1s indicates N+1 query problem |
| MCP tool error rate | Any errors |

## Known Security and Quality Findings (Prototype)

| ID | Severity | Finding |
|---|---|---|
| P0-1 | Critical | `notification_log.recipient` stores phone numbers and emails in plaintext. Encrypt using AES-256-GCM, decrypt only at delivery time. |
| P0-2 | Critical | `notification_log.message_preview` may contain PII from `details`. Store only rule_id + severity — no free-text alert context. |
| P0-3 | Critical | `alerts.details` stores rule-specific JSON as plaintext. Encrypt at rest or redact PII fields before storage. |
| P1-1 | High | No TTL archival scheduler exists. Pure functions exist but nothing invokes them. The Go implementation must wire a background job. |
| P1-2 | High | N+1 query in summary and list endpoints. Use lateral join or window function — not per-alert status subquery loop. |
| P1-3 | High | No audit context on status transitions. Add `actor_type`, source endpoint, and IP (hashed) to `alert_history`. |
| P1-4 | High | No rate limiting on REST endpoints. Apply: 60 req/min reads, 10 req/min writes. |
| P1-5 | High | No data retention policy. Define: alerts 24 months, notification_log 12 months. Implement scheduled purge. |
| P1-6 | High | Prototype has two parallel notification systems. Consolidate to a single dispatch path in Go implementation. |
| P1-7 | High | Quiet hours check uses UTC instead of merchant local time. Always resolve merchant timezone before checking. |
| P2-1 | Medium | `alert_history.changed_by` must not have a FK constraint — system actors ("system:ttl", "chirp") are not user UUIDs. |
| P2-2 | Medium | `dismiss` and `escalate` routes do not check for terminal status before writing. All transition endpoints must check. |
| P2-3 | Medium | `open-case` prototype creates case and history in separate commits. Must be a single atomic transaction. |
