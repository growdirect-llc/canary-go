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

# Owl — AI Analysis Engine

**Service type:** MCP server embedded in Canary application process
**MCP server name:** `canary-owl`, version `0.1.0`

---

## Purpose

Owl is Canary's AI intelligence layer. It provides personality-routed chat, context-aware health-check reports, natural language data search, and an MCP tool registry that any client can discover and invoke. Every insight, report, and recommendation a merchant sees flows through Owl. When the LLM backend is unavailable, every path falls back to deterministic logic — Owl never returns an error to the merchant.

**Multi-tenant context.** Owl operates per-tenant — every chat session, semantic search query, and report generation is scoped to a single merchant via `SET search_path TO tenant_{merchant_id}, public`. Owl tables (`owl_chunks`, `owl_sessions`, `risk_scores`, `risk_score_history`) live in the tenant schema. The pgvector embeddings indexed by Owl never cross tenant boundaries — a query on Merchant A's namespace will not surface chunks from Merchant B regardless of similarity. Cross-tenant analytics (industry benchmarks, platform-wide patterns) flow through the `analytics` schema, populated by scheduled rollup jobs. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Owl's chat, search, and report generation operate with all Optional Features (per `platform-overview.md`) disabled. When `L402_ENABLED=true`, premium Owl tools (e.g., expanded analytical depth, cross-merchant benchmarks at the highest tier) may be gated as paid MCP calls — the core conversation flow does not depend on Lightning settlement. When `ILDWAC_ENABLED=true`, Owl's cost-attribution analysis surfaces additional dimensions (device, MCP, port) — until then, standard ILWAC (item × location × WAC) is the cost surface Owl reads.

---

## RaaS Namespace Integration

OWL's entity resolution — the EJ Spine that links cross-source entities (employees, cards, devices, locations) into a unified subject graph — uses the **RaaS namespace as the canonical merchant identifier** when resolving cross-source entities.

OWL does not call RaaS directly. It reads the `raas_namespace` field from `merchant_sources` (already populated by RaaS during merchant onboarding). The namespace token (`raas:{merchant_id}`) is the lens through which OWL resolves entity identities across POS sources.

| OWL Operation | RaaS Dependency |
|---|---|
| Entity resolution in semantic search | Reads `merchant_sources.raas_namespace` — no direct RaaS call |
| Evidence reference construction | Uses `raas:{merchant_id}:{source_table}:{source_id}` format (see Closed-Loop Action Flow) |
| Cross-source subject matching | Resolves entities via `raas_namespace` as the tenant token — not merchant name or internal UUID |

**Why this matters:** A merchant with both Square and Counterpoint connected has two `merchant_sources` rows sharing one `raas_namespace`. OWL's entity resolution unifies subjects across both sources using the namespace as the join key. Without the RaaS namespace, a Square employee and their Counterpoint counterpart appear as separate entities — deduplication is impossible.

The RaaS service (see `docs/sdds/canary/raas.md`) populates `raas_namespace` during onboarding and owns its lifecycle. OWL is a read-only consumer of this field.

---

## Dependencies

| Dependency | Required | Failure Impact |
|---|:---:|---|
| PostgreSQL (`canary` DB, `app` schema) | Yes | Sessions, findings, memory not persisted; reports still generated but ephemeral |
| Embedding service (HTTP API) | No | All paths degrade to deterministic fallback logic |
| Valkey (DB 0) | No | No caching; functionally identical, slightly slower |
| Chirp (detection service) | Yes | `score_payment` tool fails; heartbeat computation fails |
| Fox (case service) | Yes | `case_create` / `follow_up` actions fail |
| Memory Bus (port 8003, REST) | No | Window 0 (institutional knowledge) empty; prompts less context-rich |
| Dashboard service (internal REST) | No | Dashboard tool returns empty; dashboard context not injected into prompts |

---

## Data Flow & PII Map

### What Enters

| Source | Data | Transport |
|---|---|---|
| Mobile app / API clients | Merchant messages, alert lists, store stats | JSON via JWT-authenticated POST |
| PostgreSQL (`app.alerts`) | Active alerts with employee IDs, amounts, rule IDs | Auto-fetched when client doesn't provide |
| Memory Bus | Institutional knowledge (LP patterns, process expertise) | Semantic search results (pgvector) |
| Chirp service | Stateless rule evaluations on payment payloads | JSON result dict of fired rules |

### What's Stored (4 tables in `app` schema)

| Table | PII Classification | Encryption |
|---|:---:|:---:|
| `owl_sessions` | internal | plaintext |
| `owl_findings` | internal | plaintext |
| `owl_merchant_memory` | internal | plaintext |
| `owl_action_log` | internal | plaintext |

**PII notes:**
- `narrative_summary` and `running_summary` are LLM-generated and may reference employee names sourced from alert data.
- `finding_text` may include employee names from upstream Chirp alerts.
- `action_detail` stores JSON with alert_ids and drill filter parameters — no direct PII but links to PII-bearing records.
- No email, phone, SSN, or direct customer PII stored in Owl tables.
- Employee names transit LLM prompts but are not independently persisted by Owl.

### What Exits

| Destination | Data | Sensitivity |
|---|---|:---:|
| Mobile app / API clients | Chat responses, health reports, search results, dashboard data | internal |
| Fox case service | Alert IDs, transaction IDs, case metadata | internal |
| LLM inference service | System prompts with merchant context, employee-linked alert summaries | **sensitive** — prompts contain employee-attributed alert data; TLS required for remote inference |
| `owl_action_log` | Action records | internal |

---

## API Contract

**Base path:** `/owl`

### Public Endpoints (no auth)

| Method | Path | Description |
|---|---|---|
| GET | `/owl/manifest` | MCP protocol manifest with full tool list |
| GET | `/owl/tools` | All registered tools in MCP format |
| GET | `/owl/health` | Inference service status. Returns `{service, healthy, owl_status, model, tools}`. 200 if healthy, 503 if offline |

**Production note:** The health endpoint must not expose internal infrastructure addresses or model names in production responses. Redact those fields when `ENV=production`.

### JWT-Protected Endpoints

| Method | Path | Timeout | Description |
|---|---|:---:|---|
| POST | `/owl/tools/<name>` | varies | Generic MCP tool invocation. Params from JSON body, context from JWT |
| POST | `/owl/one-thing` | 15s | Single most important insight. Auto-fetches alerts from DB if not provided |
| POST | `/owl/heartbeat` | — | Store health score. Returns `{score, band, alert_counts, total_alerts}` |
| POST | `/owl/chat` | 15s | Full personality-routed chat. Closed-loop envelope response |
| GET | `/owl/personalities` | — | Lists Chirp JPT lenses for chat UI picker |
| POST | `/owl/health-check` | 120s | McKinsey-style health report. Seven-step pipeline |
| POST | `/owl/action` | — | Closed-loop action dispatcher. 11 action codes |

### Action Codes

`case_create`, `follow_up`, `resolve`, `dismiss` (reason required), `archive`, `share`, `checklist_start`, `template_save`, `drill_down`, `drill_row`.

---

## Internal Service Contracts

### LLM Inference Service

**Generate (with JSON output):**
```
POST {INFERENCE_BASE_URL}/api/generate
Content-Type: application/json

Request body:
{
  "model": "<configured model>",
  "prompt": "<user message>",
  "system": "<assembled system prompt>",
  "format": "json",
  "stream": false,
  "options": { "think": false }
}

Response body:
{
  "response": "<JSON string or plain text>",
  "done": true
}
```

**Health check:**
```
GET {INFERENCE_BASE_URL}/api/tags
Timeout: 3s
Status 200 → inference service is reachable
Any other → degraded, fall back to deterministic logic
```

Circuit breaker: cache inference service health status for 30 seconds. Open circuit after 3 consecutive failures, half-open after 60 seconds.

### Embedding Service

```
POST {EMBEDDING_BASE_URL}/api/embeddings
Content-Type: application/json

Request body:
{
  "model": "<configured embedding model>",
  "prompt": "<text to embed>"
}

Response body:
{
  "embedding": [float64, ...]   // 1024 dimensions
}
```

### Memory Bus Recall

```
POST http://memory-bus:8003/recall
Content-Type: application/json

Request body:
{
  "query": "<text>",
  "limit": 5,
  "filter": { "lens": "jpt_detection" }
}

Response body:
{
  "results": [
    { "chunk_id": "...", "content": "...", "score": 0.92 }
  ]
}
```
Timeout: 5s. On failure, return empty results — prompt proceeds without institutional context.

---

## MCP Tool Registry

| Tool | Auth | PII Access | Timeout | Description |
|---|:---:|---|:---:|---|
| `the_one_thing` | JWT | Reads alerts (employee-linked) | 15s | Single most important insight. LLM with deterministic fallback. |
| `ask` | JWT | Reads merchant context | 30s | Freeform plain-English question. LLM required; returns structured error if offline. |
| `heartbeat` | JWT | Reads alerts (counts only) | — | Store health score 0-100. Pure deterministic computation. |
| `check_heartbeat` | JWT | Reads alerts, memory, previous findings | 120s | Full health check with memory context and optional report. |
| `score_payment` | JWT | Reads payment payload (card_last4, employee, amounts) | — | Stateless Chirp rule evaluation on a parsed payment. |
| `search` | JWT | Reads transactions, alerts, cases, employees | 10s | Natural language query translated to parameterized SQL. |
| `dashboard` | JWT | Reads merchant KPIs, employee risk scores | — | Health score, top concern, anomaly count, metric bands. Deterministic. |
| `knowledge_search` | JWT | None (institutional knowledge only) | 5s | Memory Bus semantic similarity recall. |

---

## Security Model

**Authentication:** All tool invocations require a valid JWT. The JWT contains `merchant_id` and `user_id`. Every database query issued by Owl filters by the `merchant_id` extracted from the JWT. There is no way to query across tenants from any Owl endpoint.

**Authorization:** No role-based access control within Owl. Any authenticated merchant user can invoke any tool.

**Search tool safety:** The search tool translates natural language to parameterized SQL. The query builder must: validate that the translated query only touches `app`, `sales`, and `metrics` schemas; enforce a row limit (500 max); apply a database-level statement timeout (5s). SQL injection risk is mitigated by parameterized queries — never string interpolation.

**Cross-app isolation:** Owl has no access to Cove, Angel, or platform databases. Blast radius on compromise is limited to the `canary` database.

**Audit logging gap (P1):** Tool invocations through the generic `/owl/tools/<name>` endpoint must be logged. Log: merchant_id, user_id, tool name, params hash (not plaintext), timestamp, result status.

**Error responses:** Never return raw exception messages to clients. Log full errors server-side; return generic `{"ok": false, "error": "internal error"}` to the client.

**Rate limits (required before GA):**

| Endpoint | Limit |
|---|---|
| POST `/owl/chat` | 60/min per merchant |
| POST `/owl/health-check` | 10/min per merchant |
| POST `/owl/one-thing` + `/owl/heartbeat` | 120/min per merchant |
| POST `/owl/action` | 60/min per merchant |
| GET `/owl/health` + public endpoints | 120/min global |

---

## Chirp JPT Lenses

Three personality lenses route merchant messages deterministically. No LLM is involved in routing.

| Lens | Domain | Default | Preferred Outputs | Routing Trigger |
|---|---|:---:|---|---|
| `jpt_detection` | Loss prevention | Yes | alert_set, number | Employee patterns, void/refund anomalies, coaching |
| `jpt_operations` | Operations | No | checklist, memo | Process optimization, scheduling, workflow |
| `jpt_analytics` | Analytics | No | number, memo | Benchmarks, trends, cohort comparisons |

**Routing algorithm:** Score each personality's keyword set against the message using `matches / sqrt(len(keywords))` normalization. Highest score wins. Ties go to `jpt_detection`. Explicit address prefix (e.g., `@lp`, `@ops`, `@analytics`) overrides scoring with a minimum confidence of 0.8.

---

## 4-Window Context Assembly

Every LLM prompt is assembled from four context windows, all best-effort. A window that fails returns empty string — the prompt proceeds.

| Window | Source | Token Budget | Fallback |
|:---:|---|:---:|---|
| 0 | Institutional Knowledge — Memory Bus semantic search filtered by JPT lens affinity | ~2,000 | Empty string |
| 1 | Running Summary — compressed merchant history from `owl_merchant_memory.running_summary` | ~1,000 | Cold start baseline message |
| 2 | Delta — improved/worsened/appeared/resolved since last session (deterministic engine) | ~1,000 | Empty (no prior session) |
| 3 | Heartbeat + Dashboard — current score, band, severity breakdown, top-5 active alerts; dashboard state (3b) when available | ~700 | Heartbeat only |

Total prompt budget: ~6,200 tokens with ~1,800 reserved for generation.

**Window 4 (Phase 4, not yet implemented):** Hawk card corpus recall from `hawk_cards` WHERE `invalidated_at IS NULL`, filtered by `incident_class` and `subject_types`. ~500 tokens per card. When no cards exist, window is empty — no degradation.

---

## Output Formatting (Closed-Loop Envelope)

Every Owl response wraps in one of four typed output envelopes. No orphan outputs.

| Output Type | Available Actions | Terminal State |
|---|---|---|
| `alert_set` | Open case, Follow up, Resolve, Dismiss | `resolved_or_case_opened` |
| `memo` | Archive, Share | `archived_or_shared` |
| `checklist` | Start checklist, Save as template | `completed_or_saved` |
| `number` | Dig deeper, Got it | `resolved_or_drilled` |

---

## Data Model

All tables in the `app` schema. Access pattern: append-only for sessions and findings (never updated after creation); merchant memory is upsert (one row per merchant).

### `owl_sessions`

One row per completed analysis run.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUID | PK | |
| `merchant_id` | UUID | NOT NULL, FK merchants.id | Tenant key |
| `session_type` | TEXT | NOT NULL | `health_check`, `chat`, `scheduled` |
| `heartbeat_score` | INTEGER | | 0-100 |
| `heartbeat_band` | TEXT | | |
| `total_alerts` | INTEGER | | |
| `alert_breakdown` | JSONB | | |
| `category_scores` | JSONB | | |
| `narrative_summary` | TEXT | | LLM-generated |
| `top_finding` | TEXT | | |
| `top_finding_category` | TEXT | | |
| `hc_session_id` | UUID | | |
| `previous_session_id` | UUID | FK owl_sessions.id | |
| `relevance_weight` | FLOAT | | Time-decay weight |
| `lp_assessment` | TEXT | | |
| `ops_assessment` | TEXT | | |
| `analytics_assessment` | TEXT | | |
| `positive_notes` | TEXT | | |
| `outlook` | TEXT | | |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |

Indexes: `(merchant_id, created_at)`, `(merchant_id, session_type)`.

**Time-decay weight formula:** `max(0.05, pow(2, -days_since_created / 14.0))` — exponential decay with 14-day half-life, floor 0.05.

### `owl_findings`

One row per Chirp category that fired within a session.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUID | PK | |
| `session_id` | UUID | NOT NULL, FK owl_sessions.id | |
| `merchant_id` | UUID | NOT NULL, FK merchants.id | |
| `category` | TEXT | NOT NULL | Chirp category string |
| `rule_ids` | JSONB | | Array of fired rule IDs |
| `severity` | TEXT | | |
| `alert_count` | INTEGER | | |
| `finding_text` | TEXT | | |
| `recommended_action` | TEXT | | |
| `delta_direction` | TEXT | | `improved`, `worsened`, `new`, `resolved` |
| `delta_detail` | JSONB | | |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |

Indexes: `(session_id)`, `(merchant_id, category)`.

### `owl_merchant_memory`

One row per merchant. Always reflects current state (upsert on write).

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUID | PK | |
| `merchant_id` | UUID | UNIQUE NOT NULL, FK merchants.id | |
| `latest_session_id` | UUID | FK owl_sessions.id | |
| `latest_heartbeat_score` | INTEGER | | |
| `latest_heartbeat_band` | TEXT | | |
| `session_count` | INTEGER | | |
| `score_trend` | JSONB | | Array of last 12 scores |
| `recurring_categories` | JSONB | | |
| `running_summary` | TEXT | | ~500 tokens, LLM-compressed history |
| `actions_taken` | JSONB | | Array of recent action records |
| `is_cold_start` | BOOLEAN | NOT NULL DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |

### `owl_action_log`

Tracks merchant actions on findings. Best-effort writes — failure is logged at WARNING level, not fatal.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | UUID | PK | |
| `merchant_id` | UUID | NOT NULL, FK merchants.id | |
| `finding_id` | UUID | FK owl_findings.id, nullable | |
| `session_id` | UUID | FK owl_sessions.id, nullable | |
| `action_type` | TEXT | NOT NULL | |
| `action_detail` | JSONB | | Alert IDs, drill filter params |
| `outcome` | TEXT | | |
| `created_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL DEFAULT now() | |

Indexes: `(merchant_id, created_at)`, `(finding_id)`.

---

## Key SQL Contracts

### Fetch merchant memory (upsert pattern)
```sql
-- Read
SELECT * FROM app.owl_merchant_memory
WHERE merchant_id = $1;

-- Upsert
INSERT INTO app.owl_merchant_memory (id, merchant_id, ...)
VALUES (gen_random_uuid(), $1, ...)
ON CONFLICT (merchant_id) DO UPDATE
SET running_summary = EXCLUDED.running_summary,
    score_trend = EXCLUDED.score_trend,
    updated_at = now();
```

### Fetch previous session for delta engine
```sql
SELECT id, category_scores, heartbeat_score
FROM app.owl_sessions
WHERE merchant_id = $1
  AND session_type = 'health_check'
ORDER BY created_at DESC
LIMIT 1;
```

### Fetch previous findings for delta comparison
```sql
SELECT category, alert_count, severity
FROM app.owl_findings
WHERE session_id = $1;
```

---

## Workflows

### Chat Flow

1. Client sends `POST /owl/chat` with JWT and message body.
2. Extract explicit address prefix via regex (e.g., `@lp`). If none, score intent keywords against all three personalities using `matches / sqrt(keywords)` normalization. Produce routed message with chosen lens.
3. Infer expected output type by counting signal words in message, falling back to personality's first preferred output.
4. Assemble 4-window context (all best-effort — empty string on any window failure).
5. Build system prompt: personality definition + heartbeat knowledge base + institutional context + merchant history + top 5 active alerts + stats.
6. POST to inference service `/api/generate` with `format: "json"`, `timeout: 15s`.
7. Parse LLM JSON response: extract `message`, `output_type`, `data`, `severity`. Wrap in closed-loop envelope.
8. On inference failure: produce deterministic fallback envelope with `source: "fallback"`.
9. Attach routing metadata to response.

### Closed-Loop Action Flow

Client taps action button → `POST /owl/action` with `action_code` + context.

- `case_create`: POST to Fox case service. Supports alerts, transactions, and drill paths. Evidence refs use format `raas:{merchant_id}:{source_table}:{source_id}`.
- `follow_up`: Check existing Fox case linkage; create or append evidence.
- `resolve` / `dismiss`: Write to alert history service.
- All actions: log to `owl_action_log` as best-effort (non-blocking).

### Health Check Report Flow

1. `POST /owl/health-check`. Fetch up to 50 alerts (auto if not provided by client).
2. Compute heartbeat score and band from alerts using deterministic algorithm (see Heartbeat).
3. Load merchant memory + previous findings (best-effort).
4. Assemble 4-window context.
5. POST to inference service with 120s timeout. On failure, generate template-based report.
6. Post-validate LLM output: correct hallucinated scores (accept if within ±15 of computed score), validate finding trends against delta engine output. Never trust raw LLM scores without verification.
7. Persist: insert `owl_sessions`, insert `owl_findings` (one per Chirp category, including "resolved" findings with delta direction), upsert `owl_merchant_memory`. Persistence failures are non-fatal — return report regardless.

### The One Thing Flow

Triggered on app load or pull-to-refresh.

- If inference available: build JSON prompt from up to 20 alerts + stats + context. Call inference with `jpt_detection` lens. Parse JSON response.
- If inference offline: fall back to composite ranking — severity 60%, impact_cents 40% when available.
- Dashboard override: if any metric is in "investigate" band AND alert severity < critical, dashboard concern takes priority over alert severity ranking.

### Delta Engine

Compare current alerts (grouped by Chirp category) against previous session's findings:

| Condition | Delta Direction |
|---|---|
| Alert count decreased | `improved` |
| Alert count increased | `worsened` |
| Same count, severity changed | follows severity direction |
| Category not in previous session | `new` |
| Category in previous but not current | `resolved` |

Categories firing 3+ consecutive sessions are flagged as "recurring" — elevated prominence in system prompt.

### Heartbeat Score Algorithm

```
score = 100
for each Chirp category group in active alerts:
  severity = worst severity across alerts in group
  score -= penalty(severity)

penalties:
  critical  → -30
  high      → -20
  medium    → -10
  low       → -5

score = max(0, score)

bands:
  85-100  → healthy
  65-84   → watch
  40-64   → review
  0-39    → investigate
```

---

## Operations

### Service Startup

Owl registers its route group during Canary HTTP server startup. All 8 MCP tools register with the tool registry. Inference service connectivity is checked lazily on first tool invocation, not at startup.

### Health Check Response

```json
{
  "service": "canary-owl",
  "healthy": true,
  "owl_status": "online",
  "model": "<configured model — redacted in production>",
  "tools": 8
}
```
Returns 200 if inference service is reachable (GET `/api/tags`, 3s timeout). Returns 503 if offline.

### Failure Modes

| Component | Failure | Degradation |
|---|---|---|
| Inference service down | Health check returns offline | All tools degrade to deterministic logic: severity-based ranking for The One Thing, template-based reports, `source: "fallback"` envelopes |
| Inference timeout (15s chat, 120s report) | LLM response lost | Same deterministic fallback |
| PostgreSQL down | DB queries fail | Reports generated and returned; persistence failure is non-fatal (logged WARNING, not raised) |
| Memory Bus down | Window 0 empty | 5s timeout, empty string returned, prompt proceeds |
| Dashboard service down | Window 3b empty | Context assembly continues with heartbeat-only |

### Configuration

| Config Key | Description | Default |
|---|---|---|
| `INFERENCE_BASE_URL` | LLM inference service base URL | — |
| `INFERENCE_MODEL` | Model identifier | — |
| `EMBEDDING_BASE_URL` | Embedding service base URL | — |
| `EMBEDDING_MODEL` | Embedding model identifier | — |
| `JWT_SECRET` | JWT validation secret | — |
| `MEMORY_BUS_URL` | Memory Bus service base URL | `http://memory-bus:8003` |

### Monitoring

| Metric | Alert Threshold | Notes |
|---|:---:|---|
| `/owl/health` status | 503 for >5 min | Inference is down — all AI features degraded |
| Report generation time | >120s | Inference overloaded |
| Post-validation score corrections | Any | Frequent corrections indicate prompt quality regression |
| `owl_sessions` insert failures | Any ERROR | Database connectivity issue |
| Action dispatch failures | Any 5xx | Fox/Alert service issue |

### Data Retention (required before GA)

- `owl_sessions`: archive records older than 12 months.
- `owl_action_log`: purge records older than 24 months.
- `owl_findings`: retain until parent session is archived.
- `owl_merchant_memory.score_trend`: rolling window of last 12 entries (capped in application logic).

---

## Production Readiness Checklist

- [ ] PII — Employee names sent to inference service must use employee IDs when inference endpoint is remote (not local network)
- [ ] Secrets — JWT secret, DB credentials via secrets manager (not env vars)
- [x] Health check endpoint — `GET /owl/health` returns 200/503
- [ ] Audit logging — all tool invocations logged (merchant_id, user_id, tool, params hash, status)
- [ ] Data retention — automated purge for sessions, findings, action logs
- [ ] Rate limiting — per-endpoint limits on all Owl routes
- [ ] Error sanitization — action dispatcher must not return raw exceptions to client
- [x] Graceful degradation — all paths fall back to deterministic logic when inference is offline
- [x] Tenant isolation — all queries scoped by JWT-extracted merchant_id
- [x] Post-validation of LLM output — score correction applied before persistence
- [x] Non-fatal persistence — report generation continues even if DB write fails
- [ ] Circuit breaker — Inference health cached 30s, circuit opens after 3 failures
