---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: GRO-617 — Canary Go rebuild
status: handoff-ready
updated: 2026-04-28
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Canary — Microservice Architecture

## Governing principle

Each microservice is an independently deployable Go binary. It owns its domain, exposes a REST API, reads and writes only its own tables, and communicates with peers via HTTP. No shared in-process state. No direct cross-service database reads. The pipeline is event-driven via Valkey streams; the rest of the system is request/response REST.

**Multi-tenant isolation is schema-per-tenant** — see `architecture.md` "Multi-Tenant Isolation" for the canonical model. Every service operates inside a tenant's schema via `SET search_path` from the JWT `merchant_id` claim. Cross-tenant data flow is forbidden except through the dedicated admin role with audit logging.

**Optional architectural features** — L402 enforcement, ILDWAC five-dimension cost model, blockchain anchoring, vendor smart contracts — are env-gated, default off. The service contracts in this SDD describe how services interact when those features are enabled; the contracts hold equally well when disabled (services skip the relevant calls). See `platform-overview.md` "Optional Features" for the canonical env-flag pattern. No service blocks store operations on any of those features being unavailable.

---

## Service map

```
                        ┌─────────────────────────────────────────┐
                        │           External POS systems           │
                        │    (Square webhooks / Counterpoint poll) │
                        └────────────────┬────────────────────────┘
                                         │
                              ┌──────────▼──────────┐
                              │   canary-gateway     │  :8080
                              │  Webhook receipt     │
                              │  HMAC verify         │
                              │  Stream write        │
                              └──────────┬──────────┘
                                         │ Valkey stream: canary:events
                              ┌──────────▼──────────┐
                              │    canary-tsp        │  :8081
                              │  4-stage pipeline    │
                              │  seal→parse→merkle   │
                              │  →detect trigger     │
                              └──────┬────────┬─────┘
                                     │        │ Valkey stream: canary:detection
                          writes     │        └─────────────┐
                          sales.*    │               ┌───────▼──────┐
                                     │               │ canary-chirp │  :8082
                                     │               │ Rule eval    │
                                     │               │ Alert create │
                                     │               └───────┬──────┘
                                     │                       │ REST POST /alerts
                                     │               ┌───────▼──────┐
                                     │               │ canary-alert │  :8083
                                     │               │ Alert CRUD   │
                                     │               │ Lifecycle SM │
                                     │               └───────┬──────┘
                                     │                       │ REST POST /cases
                                     │               ┌───────▼──────┐
                                     │               │  canary-fox  │  :8084
                                     │               │ Case mgmt    │
                                     │               │ Evidence     │
                                     │               │ Hash chain   │
                                     │               └──────────────┘
                                     │
              ┌──────────────────────┼──────────────────────┐
              │                      │                       │
    ┌─────────▼──────┐   ┌───────────▼──────┐   ┌──────────▼──────┐
    │  canary-owl    │   │canary-analytics  │   │canary-identity  │
    │  :8085         │   │  :8086           │   │  :8087          │
    │ pgvector search│   │ Metric rollups   │   │ Auth / tenants  │
    │ Risk Dictionary│   │ Risk scoring     │   │ OAuth / sessions│
    │ EJ Spine       │   │ Baselines        │   │                 │
    └────────────────┘   └──────────────────┘   └─────────────────┘

              POS Adapter layer (one binary per POS):
    ┌─────────────────┐   ┌────────────────────┐
    │  canary-hawk    │   │   canary-bull       │
    │  :8090          │   │   :8091             │
    │ Square adapter  │   │ Counterpoint adapter│
    │ OAuth + webhooks│   │ API key + polling   │
    └─────────────────┘   └────────────────────┘
```

---

## Services

### canary-gateway — Webhook Gateway
**Port:** 8080
**Role:** The only public-facing ingress point for POS events. Receives webhooks, verifies HMAC signatures, deduplicates, and writes to the ingestion stream. Does not touch the database directly.

**REST API:**
```
POST /webhooks/square          — Square webhook receiver
POST /webhooks/counterpoint    — Counterpoint event receiver (polling adapter pushes here)
GET  /health                   — Liveness probe
```

**Request contract (all webhook endpoints):**
```
Headers:
  X-Signature:   HMAC-SHA256 hex digest of request body
  X-Merchant-Id: POS-native merchant/location identifier
  Content-Type:  application/json

Response 200: {"received": true, "event_id": "<uuid>"}
Response 400: {"error": "invalid_signature"}
Response 409: {"error": "duplicate_event", "event_id": "<existing-uuid>"}
Response 503: {"error": "stream_unavailable"}
```

**Valkey writes:**
- Stream: `canary:events` — one message per verified event
- Dedup key: `canary:dedup:<sha256(body)>` — SET NX, TTL 24h

**Tables owned:** none (stateless)

**Dependencies:** Valkey (stream write only)

---

### canary-tsp — Transaction Stream Processor
**Port:** 8081
**Role:** Consumes `canary:events`, runs 4 pipeline stages (seal → parse → merkle → detect-trigger), writes to `sales.*` schema, publishes detection-ready events to `canary:detection`. This service is the data spine of the system.

**REST API:**
```
GET  /health          — Liveness
GET  /status          — Pipeline stage lag metrics
POST /replay/:event_id — Replay a specific event (ops use only, requires admin token)
```

**Pipeline stages:**

| Stage | Consumer group | Input | Output | Idempotent |
|---|---|---|---|---|
| 1 — Seal | `tsp:seal` | `canary:events` | `app.ingestion_log`, chain hash | YES — unique constraint on event_id |
| 2 — Parse | `tsp:parse` | `canary:events` (ACK after seal) | `sales.*` rows | YES — upsert on external_id + merchant_id |
| 3 — Merkle | `tsp:merkle` | batch accumulator | `app.merkle_batches` | YES — batch_id idempotency |
| 4 — Detect | `tsp:detect` | `canary:events` | `canary:detection` stream | YES — stream message dedup |

**Tables owned:** `sales.transactions`, `sales.refund_links`, `sales.line_items`, `sales.line_item_discounts`, `sales.cash_drawer_shifts`, `sales.cash_drawer_events`, `app.ingestion_log`, `app.merkle_batches`

**Dependencies:** PostgreSQL (sales schema), Valkey (streams), canary-identity (merchant context lookup on startup)

---

### canary-chirp — Detection Engine
**Port:** 8082
**Role:** Consumes `canary:detection` stream. Evaluates Chirp rules against transaction data. Creates alerts via canary-alert REST API. Stateless evaluation — all state is in the database.

**REST API:**
```
GET  /health                    — Liveness
GET  /rules                     — List all active rules and their thresholds
GET  /rules/:rule_id            — Get single rule definition
POST /rules/:rule_id/evaluate   — Force-evaluate a rule for a merchant (ops use)
```

**Rule evaluation contract:**
- Pulls threshold config from `app.merchant_rule_configs` (merchant-level override) → `app.location_rule_configs` (location-level override) → `app.detection_rules` (global default)
- Executes rule SQL contract against `sales.*` schema
- On violation: POST to canary-alert `/alerts`

**Tables owned:** `app.detection_rules`, `app.merchant_rule_configs`, `app.location_rule_configs`

**Dependencies:** PostgreSQL (sales schema reads, app schema reads), Valkey (canary:detection stream), canary-alert (POST /alerts)

---

### canary-alert — Alert Service
**Port:** 8083
**Role:** Alert lifecycle management. Owns alert creation, state transitions, history, and dismissal. The authoritative record of what Chirp detected.

**REST API:**
```
POST   /alerts                        — Create alert (called by canary-chirp)
GET    /alerts?merchant_id=&severity=&status=&from=&to=  — List alerts
GET    /alerts/:id                    — Get alert
PATCH  /alerts/:id/acknowledge        — Transition: OPEN → ACKNOWLEDGED
PATCH  /alerts/:id/investigate        — Transition: ACKNOWLEDGED → INVESTIGATING
PATCH  /alerts/:id/dismiss            — Transition: any → DISMISSED
PATCH  /alerts/:id/escalate           — Transition: any → ESCALATED (creates Fox case)
GET    /alerts/:id/history            — Alert state history

Request body for state transitions:
{
  "actor_id": "<user-uuid>",
  "note": "optional human note"
}

Response: full alert object with current state
```

**Alert state machine:**
```
OPEN → ACKNOWLEDGED → INVESTIGATING → DISMISSED
                   ↘              ↗
                    → ESCALATED (triggers Fox case creation)
```

**Tables owned:** `app.alerts`, `app.alert_history`

**Dependencies:** PostgreSQL, canary-fox (POST /cases on ESCALATED transition)

---

### canary-fox — Case Management
**Port:** 8084
**Role:** Investigation case lifecycle. Evidence hash chain. Append-only timeline. The legal record of what happened and what was done about it.

**REST API:**
```
POST   /cases                          — Open a case (called by canary-alert on escalation)
GET    /cases?merchant_id=&status=     — List cases
GET    /cases/:id                      — Get case with timeline
PATCH  /cases/:id/status               — Transition case status
POST   /cases/:id/evidence             — Add evidence record (append-only)
POST   /cases/:id/timeline             — Add timeline event
GET    /cases/:id/evidence/:record_id  — Get single evidence record
GET    /cases/:id/chain                — Verify hash chain integrity
```

**Hash chain invariant:**
- Every `evidence_records` INSERT computes `chain_hash = SHA256(record_payload || prev_chain_hash)`
- A DB trigger blocks all UPDATE and DELETE on `evidence_records` — the trigger DDL is in fox.md and must be deployed
- `GET /cases/:id/chain` recomputes and verifies the full chain on demand

**Tables owned:** `app.fox_cases`, `app.fox_timeline`, `app.fox_evidence`, `app.fox_subjects`

**Dependencies:** PostgreSQL

---

### canary-owl — Intelligence and Search
**Port:** 8085
**Role:** pgvector-powered semantic search over merchant data. Risk Dictionary lookup. EJ (Employee Journey) Spine entity resolution. All vector ops go through PostgreSQL/pgvector — no external vector database.

**REST API:**
```
POST   /search                  — Semantic search
       Body: {"query": "...", "merchant_id": "...", "limit": 10, "filters": {...}}
       Response: [{id, content, similarity, entity_type, metadata}]

GET    /risk-dictionary/:entity_id   — Risk profile for an entity
GET    /ej-spine/:employee_id        — Employee journey spine (full activity graph)
POST   /embed                        — Generate embedding for a text string
       Body: {"text": "..."}
       Response: {"embedding": [1024 floats]}

GET    /health
```

**Vector search SQL contract:**
```sql
SELECT
    id,
    content,
    entity_type,
    metadata,
    1 - (embedding <=> $1) AS similarity
FROM app.owl_chunks
WHERE merchant_id = $2
  AND entity_type = ANY($3)
ORDER BY embedding <=> $1
LIMIT $4
```

**Embedding generation:** POST to embedding service at `EMBEDDING_SERVICE_URL/api/embeddings`. Model: `qwen3-embedding:8b`, dimensions: 1024.

**Tables owned:** `app.owl_chunks`, `app.owl_sessions`, `app.risk_scores`, `app.risk_score_history`

**Dependencies:** PostgreSQL (pgvector), embedding service (HTTP)

---

### canary-analytics — Metrics and Risk Scoring
**Port:** 8086
**Role:** Scheduled metric rollups, entity risk scoring, baseline computation. Reads from `sales.*`, writes to `metrics.*`. No user-facing reads — serves as the metrics write engine. Dashboard queries go to the database directly or via canary-owl.

**REST API:**
```
GET  /health
POST /rollup/daily    — Trigger daily rollup (normally cron-driven)
POST /rollup/hourly   — Trigger hourly rollup
POST /score/:merchant_id  — Recompute risk scores for a merchant
GET  /status          — Last run times and row counts for each job
```

**Scheduled jobs:**

| Job | Trigger | Input | Output table |
|---|---|---|---|
| hourly-rollup | Every hour, :05 | `sales.transactions` last 2h | `metrics.hourly_rollups` |
| daily-rollup | 02:00 UTC | `sales.transactions` yesterday | `metrics.daily_rollups` |
| employee-rollup | 02:30 UTC | `sales.transactions` yesterday | `metrics.employee_rollups` |
| risk-scoring | 03:00 UTC | `metrics.*_rollups` | `metrics.entity_risk_scores` |
| baseline-update | Sunday 04:00 UTC | 90-day window | `metrics.metric_baselines` |

**Tables owned:** `metrics.*` (all metrics schema tables)

**Dependencies:** PostgreSQL (reads sales.*, writes metrics.*)

---

### canary-identity — Identity and Auth
**Port:** 8087
**Role:** Merchant registration, user management, OAuth flows, session tokens. The authentication authority for the entire system. Every other service validates tokens by calling canary-identity or by verifying a shared JWT secret.

**REST API:**
```
POST   /merchants                    — Register merchant
GET    /merchants/:id                — Get merchant
PATCH  /merchants/:id                — Update merchant

POST   /oauth/authorize              — Begin OAuth flow
GET    /oauth/callback               — OAuth callback
POST   /oauth/refresh                — Refresh access token
DELETE /oauth/disconnect             — Disconnect POS

POST   /sessions                     — Create session (login)
DELETE /sessions/:token              — Invalidate session (logout)
POST   /sessions/validate            — Validate session token
       Body: {"token": "..."}
       Response 200: {"valid": true, "merchant_id": "...", "user_id": "...", "roles": [...]}
       Response 401: {"valid": false}

POST   /users                        — Create user
GET    /users/:id
PATCH  /users/:id
GET    /health
```

**Session contract:**
- Sessions are JWT tokens, signed with `SESSION_SECRET`
- Payload: `{merchant_id, user_id, roles[], iat, exp}`
- TTL: 8 hours (configurable)
- Storage: Valkey SET with key `session:<token_hash>` for fast invalidation

**Tables owned:** `app.merchants`, `app.users`, `app.merchant_sources`, `app.merchant_settings`, `app.oauth_states`

**Dependencies:** PostgreSQL, Valkey (session store)

---

### canary-hawk — Square Adapter
**Port:** 8090
**Role:** Square POS adapter. Handles Square OAuth, webhook signature verification, and event normalization to the Canary CRDM. Pushes normalized events to canary-gateway.

**REST API:**
```
GET  /health
POST /webhooks/inbound       — Receives Square webhooks (registered with Square)
GET  /oauth/connect          — Begin Square OAuth
GET  /oauth/callback         — Square OAuth callback
POST /sync/pull              — Trigger manual pull sync (ops use)
GET  /sync/status            — Last sync watermarks
```

**Auth model:** Square OAuth 2.0. Tokens stored in `app.hawk_oauth_tokens`. Refresh handled automatically before expiry.

**Webhook verification:** `HMAC-SHA256` of request body using Square webhook signature key. Header: `X-Square-Hmacsha256-Signature`.

**Tables owned:** `app.hawk_oauth_tokens`, `app.hawk_webhook_subscriptions`, `app.hawk_sync_cursors`, `app.hawk_event_log`, and 4 additional Square-specific staging tables (see hawk.md).

**Dependencies:** Square API (external), canary-gateway (POST /webhooks/square), canary-identity (merchant lookup), PostgreSQL

---

### canary-bull — Counterpoint Adapter
**Port:** 8091
**Role:** NCR Counterpoint adapter. API key auth (no OAuth). Polling model — Counterpoint has no native webhooks. Polls Counterpoint REST API on configurable interval, normalizes to CRDM, pushes to canary-gateway.

**REST API:**
```
GET  /health
POST /merchants/:id/connect     — Register API key for a merchant
DELETE /merchants/:id/connect   — Remove credentials
GET  /merchants/:id/sync/status — Last poll watermarks and lag
POST /merchants/:id/sync/pull   — Force immediate poll
```

**Auth model:** API key per merchant, stored encrypted in `app.bull_api_credentials`. Header: `APIKey <key>` on all Counterpoint requests.

**Poll cycle:**
1. Read watermark from `app.bull_poll_watermarks`
2. GET Counterpoint `/Documents` (and other endpoints) with `ModifiedDate >= watermark`
3. Normalize each document to CRDM
4. POST to canary-gateway `/webhooks/counterpoint`
5. Update watermark

**Default poll interval:** 5 minutes (configurable per merchant via `app.bull_merchant_config`).

**Tables owned:** `app.bull_api_credentials`, `app.bull_poll_watermarks`, `app.bull_merchant_config`, `app.bull_event_log`

**Dependencies:** NCR Counterpoint REST API (external), canary-gateway (POST /webhooks/counterpoint), canary-identity (merchant lookup), PostgreSQL

---

### canary-asset — Asset Registry
**Port:** 8089
**Role:** Authoritative registry for every physical or digital resource the merchant tracks at the per-location level — registers, scanners, scales, tablets, signage devices, edge compute, network appliances. Owns asset lifecycle (active → idle → retired), location binding, depreciation tracking, and audit history. Cross-references `device-contracts` for SLA enforcement and `store-network-integrity` for cross-location anomaly detection on hardware fingerprints.

**REST API:**
```
TIER: Reference (cached lookups)
GET  /assets/:id                        — Get asset
GET  /assets/types                      — Enumerate asset type catalog
GET  /assets/:id/history                — Lifecycle audit trail (paginated)
GET  /health                            — Liveness

TIER: Change-feed (cursor-paginated tails)
GET  /assets?merchant_id=&type=&state=&location_id=&cursor=&limit=  — List with filters

TIER: Stream (lifecycle events)
POST /assets                            — Create asset (idempotency-key required)
PATCH /assets/:id                       — Update attributes
POST /assets/:id/bind                   — Bind to location_id
POST /assets/:id/unbind                 — Unbind from current location
PATCH /assets/:id/state                 — Transition lifecycle state
DELETE /assets/:id                      — Soft-delete (transitions to RETIRED)
```

**Request contracts:**
```
POST /assets
Headers:
  Authorization: Bearer <jwt>          — required, scope: asset:write
  Idempotency-Key: <uuid>              — required for stream-tier writes
Body:
  {
    "asset_type": "register|scanner|tablet|signage|edge|network|other",
    "vendor": "string",
    "model": "string",
    "serial_number": "string",
    "purchase_date": "YYYY-MM-DD",
    "depreciation_schedule": "straight-line-5y|...",
    "location_id": "uuid (optional — bind on create)",
    "metadata": {}
  }

Response 201: { "id": "uuid", "state": "ACTIVE", "created_at": "...", ... full asset }
Response 409: { "error": { "code": "duplicate_serial", "message": "..." } }
Response 422: { "error": { "code": "invalid_asset_type", "message": "..." } }

PATCH /assets/:id/state
Body: { "to_state": "ACTIVE|IDLE|RETIRED", "actor_id": "uuid", "note": "string?" }
Response 200: full asset object
Response 409: { "error": { "code": "invalid_transition", "from": "...", "to": "..." } }
```

**Lifecycle state machine:**
```
ACTIVE ⇄ IDLE → RETIRED (terminal)
       ↘     ↗
        Allowed transitions: ACTIVE→IDLE, IDLE→ACTIVE, ACTIVE→RETIRED, IDLE→RETIRED.
        RETIRED is terminal (DELETE soft-deletes by transitioning to RETIRED).
```

**Tier classifications:** Reads (`GET /assets/:id`, `GET /assets/types`) → Reference with 60s TTL. List (`GET /assets`) → Change-feed cursor pagination, max 200 per page. Mutations (`POST`, `PATCH`, `DELETE`) → Stream tier with idempotency-key.

**Tenant isolation:** `merchant_id` from JWT claim implicit on all writes. Reads accept explicit `?merchant_id=` for super-admin scope (RBAC-gated).

**Tables owned:** `app.assets`, `app.asset_lifecycle_events`, `app.asset_types`

**Dependencies:** PostgreSQL, canary-identity (JWT validation, merchant scope), canary-device-contracts (publishes lifecycle events for SLA cross-reference)

---

### canary-item — Item Master & Catalog
**Port:** 8090
**Role:** Read-side canonical for the merchandise catalog — item master, hierarchical categories, alternate identifiers (UPC/EAN/SKU), per-item attributes, multi-location item authorization, and pricing-tier linkage. **Not** authoritative for inventory positions (see canary-inventory and canary-inventory-as-a-service) or pricing rules (see canary-pricing). Bulk catalog refresh from POS adapters lands via the `/imports/items` bulk-window pattern.

**REST API:**
```
TIER: Reference
GET  /items/:id                          — Get item by canonical UUID
GET  /items/by-upc/:upc                  — Lookup by UPC
GET  /items/by-sku/:sku                  — Lookup by merchant-scoped SKU
GET  /items/:id/attributes               — Resolved attribute set
GET  /items/:id/categories               — Category memberships
GET  /categories                          — Category tree (full or rooted)
GET  /categories/:id                     — Single category node
GET  /categories/:id/items?cursor=&limit= — Paginated category contents
GET  /health

TIER: Change-feed
GET  /items?merchant_id=&category=&since=&cursor=&limit= — Catalog tail

TIER: Stream (mutations)
POST /items                              — Create (idempotency-key required)
PATCH /items/:id                         — Update attributes
PATCH /items/:id/attributes              — Patch attribute set
POST /items/:id/categories               — Add to category
DELETE /items/:id/categories/:cat_id     — Remove from category

TIER: Bulk window (catalog drops)
POST /imports/items                      — Submit catalog import job
GET  /imports/items/jobs/:id             — Job status (rows, errors, progress)
POST /imports/items/jobs/:id/finalize    — Commit imported batch
POST /imports/items/jobs/:id/cancel      — Abort in-progress import
```

**Request contracts:**
```
POST /imports/items
Headers:
  Authorization: Bearer <jwt>            — scope: item:bulk
  X-Source: counterpoint|square|shopify|csv|other
Body (multipart):
  file: <CSV or NDJSON file, ≤500MB>
  manifest: { "row_count_expected": N, "schema_version": "v1", "merchant_id": "uuid" }

Response 202: { "job_id": "uuid", "status": "QUEUED", "submitted_at": "..." }
Response 413: { "error": { "code": "payload_too_large" } }
Response 415: { "error": { "code": "unsupported_media_type" } }

GET /items/by-upc/:upc?merchant_id=
Response 200: { "id": "uuid", "upc": "...", "sku": "...", "name": "...", "categories": [...], "attributes": {...} }
Response 404: { "error": { "code": "not_found" } }
```

**Bulk import lifecycle:**
```
QUEUED → VALIDATING → READY → FINALIZED (terminal)
                        ↓
                      CANCELED (terminal)
                        ↓
                      ERROR (terminal — see error_log_url in job status)
```

**Tier classifications:** Single-item lookups → Reference with 5-minute TTL. List → Change-feed. Mutations → Stream. Catalog imports → Bulk window with explicit job lifecycle.

**Tenant isolation:** `merchant_id` mandatory on all reads (path or query). All UPC lookups are merchant-scoped — same UPC under different merchants returns different items.

**Tables owned:** `app.items`, `app.item_categories`, `app.item_attributes`, `app.barcodes`, `app.import_jobs` (item subset)

**Dependencies:** PostgreSQL, canary-identity, canary-pricing (read-only: tier linkage), canary-owl (publishes item updates for embedding refresh)

---

### canary-inventory — Stock Positions & Adjustments
**Port:** 8091
**Role:** Historical and eventual-consistency stock-position store. Answers "what is the stock level of item X at location Y as of time T" and tracks adjustment events as audit-ready records. Cycle-count workflows reconcile against POS counts on a daily-batch cadence. **The real-time position engine is canary-inventory-as-a-service (:9081)** — that service answers "right now" questions; this one answers "as of" questions and serves as the audit/replay record.

**REST API:**
```
TIER: Reference
GET  /inventory/positions?item_id=&location_id=&as_of=  — Time-travel lookup
GET  /inventory/positions/:id                            — Specific position record
GET  /inventory/positions/:id/history                    — Position change audit
GET  /inventory/adjustments/:id                          — Single adjustment
GET  /cycle-counts/:id                                   — Cycle count detail
GET  /health

TIER: Change-feed
GET  /inventory/adjustments?merchant_id=&location_id=&since=&cursor=&limit=
GET  /inventory/reconciliation?merchant_id=&location_id=&since=

TIER: Stream
POST /inventory/adjustments              — Submit adjustment (idempotency-key)
POST /cycle-counts                       — Open cycle count
PATCH /cycle-counts/:id/finalize         — Commit cycle count results

TIER: Daily batch
POST /reconciliation/run                 — Trigger merchant reconciliation (cron-driven)

TIER: Bulk window
POST /exports/inventory-positions        — Async snapshot export over date range
GET  /exports/inventory-positions/:job_id — Export job status + presigned URL on completion
```

**Request contracts:**
```
POST /inventory/adjustments
Body:
  {
    "merchant_id": "uuid",
    "location_id": "uuid",
    "item_id": "uuid",
    "delta": -3,
    "reason_code": "shrink|damage|theft|miscount|transfer-out|other",
    "actor_id": "uuid",
    "evidence_record_id": "uuid?",   — optional Fox link
    "effective_at": "ISO8601"
  }
Response 201: { "id": "uuid", "new_position": <int>, "chain_hash": "..." }
Response 409: { "error": { "code": "duplicate_adjustment", "existing_id": "uuid" } }

GET /inventory/positions?item_id=&location_id=&as_of=2026-04-15T12:00:00Z
Response 200: { "item_id": ..., "location_id": ..., "position": <int>, "as_of": "...", "snapshot_id": "..." }
Response 404: no position record at requested time
```

**Time-travel semantics:** `as_of` parameter is interpreted in UTC. Position records are append-only; querying with `as_of` resolves to the most recent record at or before that timestamp. Timezone handling: clients send UTC; merchant-local rendering is the consumer's concern.

**Tier classifications:** Position queries → Reference with cache-bust on adjustment write. Adjustment list → Change-feed. Adjustment writes → Stream with idempotency. Reconciliation → Daily batch. Snapshot export → Bulk window async job.

**Tenant isolation:** All endpoints scope by `merchant_id`. Cross-merchant position queries return 403.

**Tables owned:** `app.inventory_positions`, `app.inventory_adjustments`, `app.cycle_counts`, `app.cycle_count_lines`, `app.reconciliation_runs`

**Dependencies:** PostgreSQL, canary-identity, canary-item (item lookup), canary-inventory-as-a-service (publishes position changes), canary-fox (evidence links on shrink/theft adjustments)

---

### canary-receiving — Purchase Orders & Vendor Master
**Port:** 8092
**Role:** Owns purchase-order lifecycle (DRAFT → SUBMITTED → IN_TRANSIT → RECEIVED → RECONCILED → CLOSED), receiving events including direct-store-delivery without prior PO, and vendor master data. Cross-references canary-commercial for vendor-invoice reconciliation; cross-references canary-inventory for receipt-driven position increments.

**REST API:**
```
TIER: Reference
GET  /purchase-orders/:id                       — Get PO with line items
GET  /vendors/:id                                — Vendor master record
GET  /receipts/:id                               — Single receipt
GET  /health

TIER: Change-feed
GET  /purchase-orders?status=&vendor_id=&from=&to=&cursor=&limit=
GET  /vendors?cursor=&since=&limit=

TIER: Stream
POST /purchase-orders                            — Create draft PO
PATCH /purchase-orders/:id                       — Update draft fields
POST /purchase-orders/:id/submit                 — Transition: DRAFT → SUBMITTED
POST /purchase-orders/:id/cancel                 — Transition: any → CANCELED
POST /receipts                                   — Receive against PO
POST /receipts/dsd                               — Direct-store-delivery (no PO)
POST /receipts/:id/finalize                      — Commit receipt
POST /vendors                                    — Create vendor
PATCH /vendors/:id                               — Update vendor

TIER: Bulk window
POST /imports/vendors                            — Vendor master refresh
POST /imports/purchase-orders                    — Bulk PO ingest from ERP
```

**PO state machine:**
```
DRAFT → SUBMITTED → IN_TRANSIT → RECEIVED → RECONCILED → CLOSED
   ↓        ↓           ↓
   └─→ CANCELED ←──────┘
```
Transitions are explicit endpoints, not generic state PATCH. RECONCILED is set by canary-commercial when invoice match completes. CLOSED is terminal.

**Request contracts:**
```
POST /receipts
Body:
  {
    "purchase_order_id": "uuid|null",  // null for DSD
    "vendor_id": "uuid",
    "location_id": "uuid",
    "received_at": "ISO8601",
    "lines": [
      { "item_id": "uuid", "quantity_received": 12, "quantity_expected": 12, "lot_number": "...", "notes": "..." }
    ],
    "evidence": { "packing_slip_url": "...", "photo_urls": [...] }
  }
Response 201: { "id": "uuid", "status": "DRAFT", "variance_lines": [...] }

POST /receipts/:id/finalize
Body: { "actor_id": "uuid", "force_close_po": bool }
Response 200: full receipt object
Response 409: { "error": { "code": "po_already_closed" } }
```

**Idempotency:** Receipt records key by `(vendor_id, vendor_invoice_number, location_id)` — duplicate receipts for the same vendor invoice at the same location return 409 with the existing receipt ID.

**Tier classifications:** Single-resource reads → Reference. List/filter → Change-feed. PO and receipt mutations → Stream. Bulk imports → Bulk window async jobs.

**Tenant isolation:** `merchant_id` from JWT. Cross-merchant PO/vendor access returns 403.

**Tables owned:** `app.purchase_orders`, `app.po_lines`, `app.receipts`, `app.receipt_lines`, `app.receipt_variances`, `app.vendors`

**Dependencies:** PostgreSQL, canary-identity, canary-item, canary-inventory (publishes receipt events for position increment), canary-commercial (RECONCILED transition, invoice match)

---

### canary-transfer — Inter-Location Transfers
**Port:** 8093
**Role:** Operational record for inter-location transfer requests, in-transit tracking, receipt confirmation at destination, and variance reporting on short-shipments, over-receipts, damages, and missing items. Loss detection and pattern analysis on transfer-loss events live in canary-bull (Distribution Intelligence). This service handles the legitimate operational record; canary-bull handles the forensic intelligence.

**REST API:**
```
TIER: Reference
GET  /transfers/:id                              — Transfer with line detail
GET  /transfers/:id/lines                        — Line-level detail
GET  /transfers/:id/events                       — Event history (audit)
GET  /transfers/:id/variance                     — Variance summary if any
GET  /health

TIER: Change-feed
GET  /transfers?from_location=&to_location=&status=&since=&cursor=&limit=

TIER: Stream
POST /transfers                                  — Create transfer request
PATCH /transfers/:id                             — Update fields (CREATED state only)
POST /transfers/:id/ship                         — Transition: CREATED → IN_TRANSIT
POST /transfers/:id/receive                      — Transition: IN_TRANSIT → RECEIVED
POST /transfers/:id/variance                     — Record short/over/damage/missing
POST /transfers/:id/cancel                       — Transition: any pre-RECEIVED → CANCELED
PATCH /transfers/:id/lines/:line_id              — Update single line (variance corrections)
```

**Transfer state machine:**
```
CREATED → IN_TRANSIT → RECEIVED → RECONCILED (terminal)
   ↓          ↓            ↓
   └─→ CANCELED            └─→ DISPUTED → RECONCILED
                                    ↓
                                ESCALATED-TO-FOX (creates Fox case via canary-alert)
```

**Variance event schema:**
```
POST /transfers/:id/variance
Body:
  {
    "type": "SHORT|OVER|DAMAGED|MISSING",
    "line_id": "uuid",
    "expected_quantity": 12,
    "actual_quantity": 10,
    "reason_code": "string",
    "evidence_record_id": "uuid?",
    "actor_id": "uuid"
  }
Response 201: { "id": "uuid", "transfer_status": "DISPUTED" if material_variance else "RECEIVED" }
```

**Tier classifications:** Single-transfer reads → Reference. List → Change-feed. Lifecycle transitions → Stream. No bulk-window endpoints by design (transfers are operational events, not import targets).

**Tenant isolation:** `merchant_id` from JWT. Both `from_location` and `to_location` must belong to the same merchant.

**Tables owned:** `app.transfers`, `app.transfer_lines`, `app.transfer_events`, `app.transfer_variances`

**Dependencies:** PostgreSQL, canary-identity, canary-item, canary-inventory (publishes transfer events for position decrement at source / increment at destination), canary-bull (subscribes to variance events for cross-location loss correlation)

---

### canary-pricing — Price Rules & Promotions
**Port:** 8094
**Role:** Rule-side store for pricing — price rules, promotional campaigns, markdown lifecycles, and historical price audit. **Does not execute basket calculation** (that's the POS today; canary-pricing in Phase 4 may add a basket-eval engine). Promotions cover rule definition, eligibility criteria, time bounds, and stack-ability with other promotions. The effective-price lookup endpoint (`/pricing/effective`) is the canonical resolver: given item/location/time/customer-segment, return the price the cashier should charge.

**REST API:**
```
TIER: Reference
GET  /pricing/effective?item_id=&location_id=&as_of=&customer_segment=  — Resolved price
GET  /price-rules/:id                            — Single rule
GET  /promotions/:id                             — Single promotion
GET  /markdowns/:id                              — Single markdown
GET  /pricing/history?item_id=&location_id=&from=&to=&cursor=  — Audit trail
GET  /health

TIER: Change-feed
GET  /price-rules?merchant_id=&active=&since=&cursor=&limit=
GET  /promotions?status=&since=&cursor=&limit=
GET  /markdowns?status=&cursor=&limit=

TIER: Stream
POST /price-rules                                — Create rule
PATCH /price-rules/:id                           — Update rule
POST /promotions                                 — Create promo campaign
PATCH /promotions/:id/activate                   — Transition: DRAFT → ACTIVE
PATCH /promotions/:id/deactivate                 — Transition: ACTIVE → INACTIVE
POST /markdowns                                  — Propose markdown
PATCH /markdowns/:id/approve                     — Transition: PROPOSED → APPROVED
PATCH /markdowns/:id/cancel                      — Transition: any pre-APPLIED → CANCELED

TIER: Bulk window
POST /imports/price-rules                        — Rule batch import
POST /imports/promotions                         — Campaign batch import
```

**Effective-price resolution precedence (highest first):**
```
1. Active markdown with item_id + location_id match
2. Active promotion with item_id + location_id match (most-specific wins; ties broken by largest discount)
3. Item-level price rule with location_id match
4. Item-level price rule without location_id (merchant-wide default)
5. Item base price (from canary-item attributes)
```

**Promotion stacking semantics:** Each promotion declares `stackable: true|false` and an optional `stack_group: "string"`. Promotions in the same stack group cannot combine; promotions in different stack groups can combine if all involved promos are `stackable: true`. The resolver evaluates stacks server-side and returns the final price plus the contributing rules in `applied_rules: [...]` for auditability.

**Markdown approval workflow:**
```
PROPOSED → APPROVED → SCHEDULED → APPLIED → EXPIRED (terminal)
   ↓         ↓          ↓
   └─→ CANCELED ←──────┘
```

**Tier classifications:** Effective-price lookup → Reference with short TTL (60s, invalidated on rule activation). Single-rule reads → Reference. Lists → Change-feed. Mutations → Stream. Imports → Bulk window async.

**Tenant isolation:** `merchant_id` from JWT. Effective-price lookups require explicit `merchant_id` in query for super-admin scope.

**Tables owned:** `app.price_rules`, `app.promotions`, `app.promotion_rules`, `app.markdowns`, `app.markdown_approvals`, `app.price_history`

**Dependencies:** PostgreSQL, canary-identity, canary-item (item base price), canary-inventory (markdown trigger from low-velocity items), canary-analytics (publishes effective-price changes for downstream metric attribution)

---

### canary-employee — Employee Records & Roles
**Port:** 8095
**Role:** Employee master records, role assignments, position bindings, and schedule references. The canonical record for who works for the merchant; roster data used by canary-fox (case subjects), canary-owl (EJ Spine attribution), and canary-chirp (employee-scoped detection rules). Does **not** execute payroll or time-and-attendance — those are downstream merchant systems. Employee-Journey (EJ) Spine querying is a canary-owl concern; this service owns the master record only.

**REST API:**
```
TIER: Reference
GET  /employees/:id                              — Get employee
GET  /employees/by-pos-id/:source/:pos_id        — Lookup by POS-native ID
GET  /employees/:id/roles                        — Resolved role assignments
GET  /employees/:id/locations                    — Locations the employee works
GET  /roles                                      — Role catalog
GET  /health

TIER: Change-feed
GET  /employees?merchant_id=&location_id=&status=&since=&cursor=&limit=

TIER: Stream
POST /employees                                  — Create employee
PATCH /employees/:id                             — Update fields
POST /employees/:id/roles                        — Add role assignment
DELETE /employees/:id/roles/:role_id             — Remove role assignment
PATCH /employees/:id/status                      — Transition: ACTIVE → INACTIVE → TERMINATED

TIER: Bulk window
POST /imports/employees                          — Roster import (HRIS sync)
GET  /imports/employees/jobs/:id                 — Import job status
```

**Employee state machine:**
```
ACTIVE → INACTIVE → TERMINATED (terminal)
   ↓         ↓
   └────→ TERMINATED
```
INACTIVE is for leave/suspension. TERMINATED is permanent and triggers EJ Spine archival in canary-owl.

**Request contracts:**
```
POST /employees
Body:
  {
    "external_id": "POS-native employee ID",
    "source": "counterpoint|square|adp|other",
    "first_name": "string",
    "last_name": "string",
    "email": "string?",
    "hire_date": "YYYY-MM-DD",
    "location_ids": ["uuid", ...],
    "roles": [{ "role_id": "uuid", "effective_at": "ISO8601" }],
    "metadata": {}
  }
Response 201: { "id": "uuid", "status": "ACTIVE", ... }
Response 409: { "error": { "code": "duplicate_external_id" } }

PATCH /employees/:id/status
Body: { "to_status": "ACTIVE|INACTIVE|TERMINATED", "actor_id": "uuid", "reason": "string" }
Response 200: full employee object (with terminated_at set if TERMINATED)
```

**Tier classifications:** Lookups → Reference (5-min TTL). List → Change-feed. Mutations → Stream. Roster import → Bulk window async.

**Tenant isolation:** `merchant_id` mandatory. PII handling: email/phone fields require `employee:read-pii` JWT scope; default reads return masked values.

**Tables owned:** `app.employees`, `app.employee_roles`, `app.employee_locations`, `app.roles`

**Dependencies:** PostgreSQL, canary-identity, canary-owl (publishes employee status changes for EJ Spine), canary-fox (subject lookup on case open)

---

### canary-customer — Customer Master & Loyalty
**Port:** 8096
**Role:** Customer master records, loyalty program membership, purchase history references, and consent state for marketing/data-handling. Owns the canonical customer record per merchant (customers are merchant-scoped — same person across two merchants is two records by design, with cross-merchant linkage as a separate concern handled by canary-owl). Loyalty point balances and tier state are tracked here; loyalty rule definitions and earn/burn execution may live downstream in the merchant's marketing platform.

**REST API:**
```
TIER: Reference
GET  /customers/:id                              — Get customer (PII-gated)
GET  /customers/by-pos-id/:source/:pos_id        — POS-native ID lookup
GET  /customers/:id/loyalty                      — Loyalty state (balance, tier, anniversary)
GET  /customers/:id/consents                     — Consent record (GDPR/CCPA-aligned)
GET  /customers/:id/transactions?cursor=&since=  — Purchase history pointer (cursor into canary-tsp)
GET  /health

TIER: Change-feed
GET  /customers?merchant_id=&loyalty_tier=&since=&cursor=&limit=

TIER: Stream
POST /customers                                  — Create
PATCH /customers/:id                             — Update fields
POST /customers/:id/loyalty/adjust               — Manual loyalty adjustment
PATCH /customers/:id/consents                    — Update consent state
POST /customers/:id/anonymize                    — GDPR/CCPA right-to-erasure flow

TIER: Bulk window
POST /imports/customers                          — Customer master refresh
POST /exports/customers                          — GDPR/CCPA data-export request
```

**Right-to-erasure flow:** `POST /customers/:id/anonymize` is a documented GDPR/CCPA path — it does NOT delete the row; it scrubs PII fields, retains transaction-link tokens, and writes an erasure receipt to `app.customer_erasure_log`. Hard delete is operationally prohibited because of the [[fox|append-only Fox evidence chain]] — historical transactions remain hash-chained even when customer PII is scrubbed.

**Request contracts:**
```
POST /customers
Headers:
  Authorization: Bearer <jwt>            — scope: customer:write
  X-Consent-Capture-Source: "in-store|web|kiosk|other"
Body:
  {
    "external_id": "string",
    "source": "string",
    "first_name": "string",
    "last_name": "string",
    "email": "string?",
    "phone": "string?",
    "loyalty_enrollment": { "enrolled": bool, "tier": "string?" },
    "consents": { "marketing": bool, "data_sharing": bool, "captured_at": "ISO8601" }
  }
Response 201: full customer object
Response 422: { "error": { "code": "consent_required" } } if loyalty enrollment without consent

POST /customers/:id/anonymize
Body: { "actor_id": "uuid", "request_source": "self_service|gdpr|ccpa|other", "verification_evidence_id": "uuid" }
Response 200: { "id": "uuid", "anonymized_at": "...", "erasure_receipt_id": "uuid" }
```

**Tier classifications:** Lookups → Reference (PII-gated, 60s TTL). List → Change-feed. Mutations → Stream. Master refresh and GDPR export → Bulk window.

**Tenant isolation:** Strict merchant scoping. PII fields require `customer:read-pii` JWT scope; default reads return masked values. Loyalty balance is non-PII and returned in default scope.

**Tables owned:** `app.customers`, `app.customer_consents`, `app.customer_loyalty`, `app.customer_erasure_log`

**Dependencies:** PostgreSQL, canary-identity, canary-tsp (transaction-history pointers), canary-owl (cross-merchant linkage as separate concern)

---

### canary-returns — Return Management
**Port:** 8097
**Role:** Return-to-merchant workflow — return authorization, reason classification, refund flow, and exchange handling. Owns the return record from initiation through resolution; cross-references canary-tsp for the original transaction, canary-inventory for restocking, canary-fox for fraud-flagged returns, and canary-customer for return history aggregation. Does **not** execute payment refunds (that's the POS or payment processor); records the disposition.

**REST API:**
```
TIER: Reference
GET  /returns/:id                                — Get return
GET  /returns/:id/timeline                       — Lifecycle event log
GET  /reasons                                    — Reason code catalog
GET  /health

TIER: Change-feed
GET  /returns?merchant_id=&location_id=&status=&from=&to=&cursor=&limit=

TIER: Stream
POST /returns                                    — Initiate return
PATCH /returns/:id/authorize                     — Transition: REQUESTED → AUTHORIZED
PATCH /returns/:id/decline                       — Transition: REQUESTED → DECLINED
POST /returns/:id/restock                        — Mark items returned to inventory
POST /returns/:id/refund                         — Record refund completion (POS-side)
POST /returns/:id/escalate                       — Flag for canary-fox case (fraud suspicion)
PATCH /returns/:id/close                         — Transition: any → CLOSED (terminal)

TIER: Bulk window
POST /exports/returns                            — Reporting export
```

**Return state machine:**
```
REQUESTED → AUTHORIZED → RESTOCKED → REFUNDED → CLOSED (terminal)
    ↓           ↓           ↓                   ↑
    └─→ DECLINED ─────────────────────────────┘
                ↓
            ESCALATED (creates Fox case, blocks CLOSED until Fox resolves)
```

**Request contracts:**
```
POST /returns
Body:
  {
    "merchant_id": "uuid",
    "location_id": "uuid",
    "original_transaction_id": "uuid",
    "customer_id": "uuid?",
    "lines": [
      { "line_item_id": "uuid", "quantity": 1, "reason_code": "string", "condition": "new|used|damaged" }
    ],
    "actor_id": "uuid",
    "evidence_record_id": "uuid?"
  }
Response 201: { "id": "uuid", "status": "REQUESTED", "auto_authorized": bool }

POST /returns/:id/escalate
Body: { "trigger_rule_id": "uuid?", "reason": "string", "actor_id": "uuid" }
Response 200: { "return_status": "ESCALATED", "fox_case_id": "uuid" }
```

**Auto-authorization rules:** Returns within N days, with original receipt, item in `new` condition, and total below merchant-configured threshold auto-authorize. Above threshold or outside policy require explicit `PATCH /returns/:id/authorize`.

**Tier classifications:** Single-return reads → Reference. List → Change-feed. State transitions → Stream. Reporting export → Bulk window.

**Tenant isolation:** Strict merchant scoping. Cross-location returns within a merchant are allowed; cross-merchant returns are not.

**Tables owned:** `app.returns`, `app.return_lines`, `app.return_events`, `app.return_reasons`

**Dependencies:** PostgreSQL, canary-identity, canary-tsp (original transaction), canary-inventory (restock event), canary-customer (return history), canary-fox (case creation on escalation), canary-alert (alerts on auto-decline patterns)

---

### canary-report — Standard Reports & Scheduled Exports
**Port:** 8098
**Role:** Generates and serves merchant-facing reports — sales summaries, shrink reports, employee scorecards, vendor reconciliation, compliance attestations. Reports are pre-defined templates, not ad-hoc query (that's canary-owl's job). Schedule management lets merchants subscribe to recurring runs; each run produces an artifact (PDF, CSV, JSON) staged in tenant-isolated object storage with presigned URL delivery.

**REST API:**
```
TIER: Reference
GET  /reports                                    — Report catalog (template list)
GET  /reports/:report_id                         — Template definition (params, format)
GET  /reports/:report_id/runs                    — Recent runs for this template
GET  /reports/:report_id/runs/:run_id            — Specific run with artifact URL
GET  /schedules/:schedule_id                     — Schedule detail
GET  /health

TIER: Change-feed
GET  /reports/runs?merchant_id=&status=&since=&cursor=&limit=  — Cross-template run tail

TIER: Stream
POST /reports/:report_id/runs                    — Trigger immediate run

TIER: Daily batch
POST /schedules                                  — Create scheduled subscription
PATCH /schedules/:schedule_id                    — Update schedule
DELETE /schedules/:schedule_id                   — Cancel schedule
(Internal: cron-driven evaluator fires scheduled runs at the configured cadence)

TIER: Bulk window
POST /exports/:export_kind                       — Generic data export (transactions, items, customers, alerts, cases)
GET  /exports/:job_id                            — Export job status + presigned URL
```

**Report run lifecycle:**
```
QUEUED → RUNNING → COMPLETED → DELIVERED (terminal)
            ↓          ↓
            └─→ FAILED ─→ NOTIFIED (terminal — error_log_url available)
```

**Request contracts:**
```
POST /reports/:report_id/runs
Body:
  {
    "merchant_id": "uuid",
    "params": { ... template-specific },
    "format": "pdf|csv|json|xlsx",
    "delivery": { "method": "presigned_url|email", "address": "..." }
  }
Response 202: { "run_id": "uuid", "status": "QUEUED", "estimated_completion": "ISO8601" }

POST /schedules
Body:
  {
    "report_id": "uuid",
    "params": {...},
    "cron": "0 9 * * 1-5",        // weekday mornings
    "format": "pdf",
    "delivery": {...},
    "active": true
  }
Response 201: full schedule object

POST /exports/transactions
Body:
  {
    "merchant_id": "uuid",
    "from": "ISO8601",
    "to": "ISO8601",
    "filters": { "location_id": "...", "min_amount": ... },
    "format": "csv|ndjson|parquet"
  }
Response 202: { "job_id": "uuid", "status": "QUEUED" }
```

**Tier classifications:** Catalog and run lookups → Reference. Run tail → Change-feed. Immediate runs → Stream. Schedule CRUD → Daily batch (cron-driven). Bulk exports → Bulk window async with presigned URL delivery.

**Tenant isolation:** All artifacts are tenant-scoped in object storage paths (`s3://canary-reports-prod/<merchant_id>/...`). Presigned URLs expire 24h.

**Tables owned:** `app.report_templates`, `app.report_runs`, `app.report_schedules`, `app.export_jobs`

**Dependencies:** PostgreSQL, canary-identity, canary-tsp / canary-alert / canary-fox (data sources for reports), object storage (S3-compatible), canary-analytics (precomputed aggregates)

---

### canary-raas — Resolution as a Service
**Port:** 8099
**Role:** Namespace resolution and chain-hash backbone. Every Canary service that constructs a Valkey key, resolves a tenant namespace, or anchors a hash to the chain calls canary-raas — never building keys independently. The 7-tool MCP surface is the agent-facing equivalent. RaaS is the **load-bearing primitive** that prevents key-construction divergence across 32 services and prevents tenant-isolation bugs at the cache layer.

**REST API:**
```
TIER: Reference (most operations are deterministic computation)
GET  /raas/resolve/:merchant_id                  — Resolve full namespace state
POST /raas/build-key                             — Construct Valkey key
POST /raas/ensure-namespace                      — Pure string construction (no DB check)
POST /raas/verify-chain                          — Verify a hash chain segment
GET  /raas/active-sources/:merchant_id           — List active POS sources for merchant
GET  /raas/key-prefix/:merchant_id/:domain       — Get the canonical key prefix
GET  /health

TIER: Stream (rare — chain anchoring is per-event)
POST /raas/anchor                                — Submit hash for chain anchoring
```

**Request contracts:**
```
POST /raas/build-key
Body: { "merchant_id": "uuid", "domain": "alerts|cases|sessions|...", "parts": ["string", ...] }
Response 200: { "key": "raas:{merchant_id}:{domain}:{joined-parts}", "ttl_recommendation": <int>? }

POST /raas/anchor
Body:
  {
    "merchant_id": "uuid",
    "payload_hash": "sha256-hex",
    "prev_hash": "sha256-hex|null (genesis)",
    "anchor_class": "alert|case|evidence|other",
    "metadata": {}
  }
Response 201: { "anchor_id": "uuid", "chain_position": <int>, "anchored_at": "..." }
```

### canary-raas — MCP surface (Axis C)

JWT-gated MCP tools. Tenant scope enforced via JWT claim.

| Tool | Tier | Purpose | Input | Output |
|---|---|---|---|---|
| `raas.resolve_namespace` | Reference | Resolve full namespace state for merchant | `{merchant_id}` | `{namespace, active_sources, key_prefix}` |
| `raas.build_key` | Reference | Deterministic key construction | `{merchant_id, domain, parts[]}` | `{key, ttl_recommendation}` |
| `raas.ensure_namespace` | Reference | Pure-string namespace construction | `{merchant_id, domain}` | `{namespace}` |
| `raas.verify_chain` | Reference | Chain integrity check | `{anchor_id, depth}` | `{valid, broken_at?}` |
| `raas.list_active_sources` | Reference | Enumerate active POS sources | `{merchant_id}` | `{sources[]}` |
| `raas.anchor_hash` | Stream | Submit a hash for chain anchoring | `{merchant_id, payload_hash, prev_hash, anchor_class}` | `{anchor_id, chain_position}` |
| `raas.get_chain_head` | Reference | Latest chain position for merchant + class | `{merchant_id, anchor_class}` | `{anchor_id, hash, position}` |

**Tier classifications:** Six of seven tools are Reference (deterministic, cacheable). One (`anchor_hash`) is Stream — chain-write events.

**Key construction rule (load-bearing across all services):** No service constructs Valkey keys directly. Every key goes through `raas.build_key` or `raas.ensure_namespace`. Pattern: `raas:{merchant_id}:{domain}:{key}`. Violations are caught by lint rule and code review; they would silently break tenant isolation at the cache layer.

**Tenant isolation:** All endpoints scope by `merchant_id`. Anchor operations are merchant-isolated chains — no cross-merchant chain segments by design.

**Tables owned:** `app.raas_namespaces`, `app.raas_anchors`, `app.raas_active_sources`

**Dependencies:** PostgreSQL, canary-identity. **Inverse dependency:** every other service depends on canary-raas for key construction.

---

### canary-ecom-channel — Ecommerce Channel Adapter
**Port:** 9080
**Role:** Adapter for ecommerce channels (Square Online in V1; Shopify and WooCommerce in V2+). Treats ecom orders as transactions on a parallel channel — same ARTS POSLog normalization, separate channel attribution. Cross-references canary-tsp for transaction landing and canary-inventory-as-a-service for real-time stock reservation against ecom carts. The omnichannel-attribution case ("did this customer buy in-store after browsing online") is handled in canary-owl, not here.

**REST API:**
```
TIER: Stream (Adapter Substrate, Axis A)
POST /webhooks/square-online                     — Square Online webhook receiver
POST /webhooks/shopify                           — Shopify webhook receiver (V2)
POST /webhooks/woocommerce                       — WooCommerce webhook receiver (V2)

TIER: Change-feed
GET  /sync/status                                — Per-merchant sync watermarks
POST /sync/pull                                  — Manual pull-sync (ops use)

TIER: Stream
POST /merchants/:id/connect                      — Register OAuth/API key
DELETE /merchants/:id/connect                    — Disconnect channel
GET  /oauth/connect                              — Begin OAuth flow
GET  /oauth/callback                             — OAuth callback receiver

TIER: Reference
GET  /merchants/:id/channels                     — Connected channels for merchant
GET  /merchants/:id/inventory-reservations       — Active cart reservations
GET  /health

TIER: Bulk window
POST /imports/historical-orders                  — Backfill of historical ecom orders
```

**Channel-to-ARTS mapping:** Each channel adapter translates its native order shape to ARTS POSLog (`TransactionHeader` + `LineItem` + `Tender`). Channel attribution is preserved in `transaction_metadata.channel: "square-online|shopify|woocommerce|other"`. Order status maps to ARTS `transaction_status: COMPLETED|REFUNDED|VOIDED|PARTIAL`.

**Inventory reservation flow:**
```
Cart-add → POST /reservations → IaaS reserves stock → 15-min TTL
      ↓
   On checkout: PATCH /reservations/:id/commit → IaaS converts to position decrement → tsp emits transaction
   On abandonment: TTL expires → IaaS releases reservation → no position change
```

**Tier classifications:** Webhook receivers → Stream (Axis A adapter substrate). Sync ops → Change-feed. OAuth + connection mutations → Stream. Status reads → Reference. Historical backfill → Bulk window.

**Tenant isolation:** `merchant_id` from JWT or implicit via webhook signature verification.

**Tables owned:** `app.ecom_connections`, `app.ecom_oauth_state`, `app.ecom_sync_watermarks`, `app.ecom_reservations`

**Dependencies:** PostgreSQL, canary-identity, canary-tsp (transaction landing), canary-inventory-as-a-service (reservation engine), external ecom platforms (Square Online, Shopify, WooCommerce)

---

### canary-inventory-as-a-service — Real-Time Inventory Engine
**Port:** 9081
**Role:** Real-time inventory position engine. Answers "right now, what is the stock at location X for item Y?" — superset of the canary-inventory historical store. Holds in-memory live positions per (merchant, location, item), backed by Postgres for durability and pgvector for similarity-based item lookups (substitutes, near-matches). SSE stream emits position changes for ops dashboards and ecom cart reservations.

**REST API:**
```
TIER: Stream (real-time position changes)
GET  /inventory/sse?merchant_id=&location_id=    — Server-Sent Events: position changes
GET  /inventory/changes?merchant_id=&since=&cursor=  — Polling fallback for SSE clients

TIER: Reference (live position snapshot)
GET  /inventory/live?item_id=&location_id=       — Current real-time position
GET  /inventory/live/batch?item_ids[]=&location_id=  — Batch live lookup (max 100)
GET  /inventory/similar?item_id=&location_id=&limit=  — pgvector similarity search for substitutes
GET  /health

TIER: Stream (reservation engine)
POST /reservations                               — Reserve stock (cart hold, transfer hold, etc.)
PATCH /reservations/:id/commit                   — Convert to position decrement
PATCH /reservations/:id/release                  — Release back to live position
GET  /reservations/:id                           — Reservation state

TIER: Change-feed
GET  /inventory/velocity?location_id=&period=    — Per-item velocity over rolling window
```

**Reservation state machine:**
```
ACTIVE (TTL countdown) → COMMITTED (terminal)
        ↓                  ↑
        ├── EXPIRED ────────┘ (auto-released after TTL)
        └── RELEASED (terminal — explicit release)
```

**Live-position consistency model:** IaaS holds position state in Valkey (per-merchant DB index). On every adjustment from canary-inventory or every transaction from canary-tsp, IaaS updates the live position and emits an SSE event. On startup, IaaS rebuilds live state from canary-inventory's most recent snapshot + replay of post-snapshot adjustments.

**Tier classifications:** SSE stream → Stream (Axis B + C). Snapshot reads → Reference (sub-second TTL). Reservations → Stream. Velocity windows → Change-feed.

**Tenant isolation:** Strict merchant scoping at the SSE channel level — clients can only subscribe to their merchant's stream.

**Tables owned:** `app.iaas_reservations`, `metrics.inventory_velocity` (read-only from analytics; write-back of computed rolling windows)

**Dependencies:** PostgreSQL, Valkey (live state), canary-inventory (durable adjustments), canary-tsp (transaction-driven decrement), canary-item (item metadata for similarity), canary-owl (pgvector embeddings for similarity)

---

### canary-ildwac — Provenance-Weighted Cost Model
**Port:** 9082
**Role:** Item × Location × Device × MCP × Port × Weighted-Average-Cost (ILDWAC). Patent-protected (#63/991,596). Computes a five-dimension cost model where every cost lineage is traceable to its provenance (which device/port/MCP touched the goods at each step). The reference state is slow-moving (cost basis per item × location); recomputes on receiving events are change-feed (recompute-on-write). Stream-tier ILDWAC operations would burn the model — never expose ILDWAC as real-time. **Do not change this tier mapping.**

**REST API:**
```
TIER: Reference (cost-model state)
GET  /ildwac/cost-model/:item_id?location_id=&as_of=  — Resolved WAC at point in time
GET  /ildwac/lineage?item_id=&from=&to=               — Cost lineage trace
GET  /ildwac/devices/:device_id/contributions         — Device-attributed cost contributions
GET  /ildwac/snapshot/:snapshot_id                    — Frozen model snapshot (audit)
GET  /health

TIER: Change-feed (recomputes on receiving events)
POST /ildwac/recompute                                — Trigger recompute for merchant×items
GET  /ildwac/recomputes?status=&since=&cursor=        — Recompute job tail
GET  /ildwac/recomputes/:job_id                       — Single recompute job

TIER: Bulk window (model snapshots for accounting periods)
POST /ildwac/snapshots                                — Generate period snapshot (monthly close, etc.)
```

**Five-dimension cost attribution:**
```
WAC[item, location, device, mcp, port] = Σ (cost_in × quantity_in) / Σ quantity_in
                                          for all events on that (device, mcp, port) lineage

Aggregations:
  WAC[item, location] = location-level rolled up across devices
  WAC[item] = merchant-level rolled up across locations
```

**Recompute trigger conditions:** New receipt event from canary-receiving with `evidence_record_id` populated. Manual recompute via `POST /ildwac/recompute` (ops use). Period close from canary-report.

**Patent-protected behavior:** Provenance weighting (the way device/MCP/port lineage influences cost weighting) is patent-protected territory — algorithm details live in `docs/sdds/go-handoff/ildwac.md` and are CRB-curated only as the contract surface, not the mechanism. External-facing partner docs document the API; internal SDDs document the algorithm.

**Tier classifications:** Cost-model lookups → Reference (24h cache, invalidated on recompute). Lineage trace → Reference. Recompute trigger → Change-feed. Period snapshots → Bulk window.

**Tenant isolation:** Strict merchant scoping. Cross-merchant cost queries return 403.

**Tables owned:** `app.ildwac_cost_state`, `app.ildwac_lineage`, `app.ildwac_recompute_jobs`, `app.ildwac_snapshots`

**Dependencies:** PostgreSQL, canary-identity, canary-receiving (receipt events), canary-inventory (position context), canary-blockchain-anchor (snapshot hash anchoring)

---

### canary-device-contracts — Smart Contract Enforcement
**Port:** 9083
**Role:** Smart-contract-style enforcement of cost/profit-center device SLAs. A device-contract declares the expected behavior of an asset (uptime, throughput, error budget) and surfaces breach events when actual behavior drifts. Pairs with canary-asset for the device record and with canary-ildwac for cost attribution at breach time.

**REST API:**
```
TIER: Reference
GET  /device-contracts/:device_id                      — Active contract for device
GET  /device-contracts/:device_id/history              — Version history
GET  /device-contracts/templates                       — Contract templates
GET  /device-contracts/breaches/:breach_id             — Single breach event
GET  /health

TIER: Change-feed
GET  /device-contracts?merchant_id=&device_type=&status=&since=&cursor=&limit=
GET  /device-contracts/breaches?since=&cursor=&limit=  — Breach event tail

TIER: Stream
POST /device-contracts                                  — Declare contract for device
PATCH /device-contracts/:id                             — Update contract
POST /device-contracts/:device_id/breach                — Record breach event
PATCH /device-contracts/:id/retire                      — Retire contract (device decommissioned)
```

**Contract declaration:**
```
POST /device-contracts
Body:
  {
    "device_id": "uuid",
    "merchant_id": "uuid",
    "metrics": {
      "uptime_target_pct": 99.5,
      "throughput_min_per_hour": 100,
      "error_rate_max_pct": 1.0
    },
    "evaluation_window": "rolling-7d|rolling-24h|calendar-day",
    "breach_actions": ["alert", "case", "escalate-to-vendor"],
    "effective_from": "ISO8601",
    "effective_to": "ISO8601?"
  }
Response 201: full contract object
```

**Breach event flow:** Continuous evaluator (separate worker, not REST-driven) reads device telemetry, computes against active contracts, and `POST /device-contracts/:device_id/breach` when a metric falls outside contract bounds. Breach events fan out to `breach_actions` — typically alert creation in canary-alert and optional Fox case opening.

**Tier classifications:** Single-contract reads → Reference. List + breach tail → Change-feed. Mutations and breach recording → Stream.

**Tenant isolation:** `merchant_id` from JWT. Devices and contracts scope strictly to merchant.

**Tables owned:** `app.device_contracts`, `app.device_contract_versions`, `app.device_contract_breaches`

**Dependencies:** PostgreSQL, canary-identity, canary-asset (device record), canary-alert (breach → alert), canary-fox (breach → case for severe), canary-ildwac (cost attribution at breach time)

---

### canary-ops-dashboard — Store NOC & Health Grid
**Port:** 9084
**Role:** The store-network operations console. Live device-health grid, MCP-server observability, alert state distribution, and adapter-sync watermarks. Two surfaces: a REST API for state queries and an SSE channel for live updates. Also exposes an MCP server (`canary-ops`) so AI assistants (in-store, founder-side) can query operational state on demand. The Bases-rendered Brain wiki health rollups (5 per cadence tier) consume from the same data.

**REST API:**
```
TIER: Stream (live operational telemetry)
GET  /ops-dashboard/sse?merchant_id=&scope=devices|alerts|adapters|all  — SSE channel
WebSocket variant: GET /ops-dashboard/ws  (for clients that prefer WS over SSE)

TIER: Reference (snapshot state)
GET  /ops-dashboard/devices?merchant_id=&location_id=&status=  — Device-health grid snapshot
GET  /ops-dashboard/devices/:device_id                          — Single device telemetry
GET  /ops-dashboard/mcp-grid?merchant_id=                       — MCP server observability matrix
GET  /ops-dashboard/adapters?merchant_id=                       — Adapter sync watermarks
GET  /ops-dashboard/health-rollup?tier=stream|change-feed|...   — Per-tier health rollup
GET  /health

TIER: Change-feed
GET  /ops-dashboard/events?merchant_id=&since=&cursor=&limit=   — Operational event log

TIER: Stream (operator actions)
POST /ops-dashboard/devices/:device_id/silence                  — Silence a noisy device
DELETE /ops-dashboard/devices/:device_id/silence                 — Unsilence
```

### canary-ops-dashboard — MCP surface (Axis C)

| Tool | Tier | Purpose | Input | Output |
|---|---|---|---|---|
| `ops.device_status` | Reference | Get device health | `{device_id}` | `{device, status, last_seen, error_budget_remaining}` |
| `ops.adapter_lag` | Reference | Adapter sync lag for merchant | `{merchant_id, source?}` | `{adapters[], max_lag_seconds}` |
| `ops.alert_distribution` | Reference | Alert counts by severity/status | `{merchant_id, since?}` | `{by_severity, by_status, total}` |
| `ops.mcp_health` | Reference | MCP server observability | `{merchant_id}` | `{servers[], unhealthy_count}` |
| `ops.health_rollup` | Reference | Per-tier health summary | `{merchant_id, tier?}` | `{tiers[], overall_state}` |
| `ops.silence_device` | Stream | Silence a noisy device | `{device_id, duration, reason}` | `{silenced_until}` |

**SSE event taxonomy:**
```
device.online   { device_id, location_id, at }
device.offline  { device_id, location_id, at, last_heartbeat }
device.degraded { device_id, metric, observed, expected }
adapter.lag     { source, merchant_id, lag_seconds }
mcp.unhealthy   { server, error, last_success }
alert.created   { alert_id, severity, merchant_id }
```

**Tier classifications:** SSE → Stream (Axis B + C). State snapshots → Reference. Event log → Change-feed. Operator actions → Stream.

**Tenant isolation:** SSE channels are merchant-scoped at subscribe time; cross-merchant scopes require `super_admin` JWT claim.

**Tables owned:** `app.device_telemetry`, `app.adapter_watermarks`, `app.ops_event_log`, `app.device_silences`

**Dependencies:** PostgreSQL, canary-identity, every other service (consumes their `/health` and `/status` endpoints), canary-store-brain (presence data for in-store device attribution)

---

### canary-store-brain — In-Store Context Manager
**Port:** 9085
**Role:** The in-store AI context manager. Resolves which assistant agents are present in a location, which user sessions are active, and which MCP tools each agent has permission to call. Acts as the policy-decision-point for in-store agent activity — every in-store MCP tool call traverses store-brain for permission gating. SSE channel emits presence events (agent connect/disconnect, session start/end) for ops dashboards.

**REST API:**
```
TIER: Stream
GET  /store-brain/sse?merchant_id=&location_id=  — Presence + session events
POST /store-brain/sessions                       — Start an agent session
DELETE /store-brain/sessions/:session_id         — End a session
POST /store-brain/sessions/:session_id/heartbeat — Keep-alive (every 30s)

TIER: Reference
GET  /store-brain/presence/:location_id          — Current agent presence
GET  /store-brain/sessions/:session_id           — Session detail
GET  /store-brain/permissions?session_id=&tool=  — Permission resolution
GET  /health

TIER: Change-feed
GET  /store-brain/sessions?merchant_id=&since=&cursor=  — Session history tail

TIER: Stream (policy mutations)
POST /store-brain/policies                        — Define permission policy
PATCH /store-brain/policies/:id                   — Update policy
POST /store-brain/sessions/:session_id/grant      — Grant additional tool to active session
POST /store-brain/sessions/:session_id/revoke     — Revoke tool from active session
```

### canary-store-brain — MCP surface (Axis C)

| Tool | Tier | Purpose | Input | Output |
|---|---|---|---|---|
| `store_brain.who_is_here` | Reference | Active agents at location | `{location_id}` | `{agents[], sessions_count}` |
| `store_brain.start_session` | Stream | Begin agent session | `{merchant_id, location_id, agent_role}` | `{session_id, allowed_tools[]}` |
| `store_brain.check_permission` | Reference | Verify tool permission | `{session_id, tool}` | `{allowed, reason?}` |
| `store_brain.heartbeat` | Stream | Session keep-alive | `{session_id}` | `{ttl_remaining}` |
| `store_brain.end_session` | Stream | End session | `{session_id}` | `{ended_at, duration}` |

**Permission resolution model:**
```
1. Lookup merchant policy (default permission set per agent_role)
2. Lookup location overrides
3. Lookup session-specific grants/revocations
4. Final answer: union of permitted tools, with deny-overrides
```

**Tier classifications:** SSE + session lifecycle → Stream. Snapshot reads → Reference. Session history → Change-feed.

**Tenant isolation:** Strict merchant + location scoping. Every session is bound to a single (merchant_id, location_id) tuple.

**Tables owned:** `app.store_brain_sessions`, `app.store_brain_session_events`, `app.store_brain_policies`, `app.store_brain_permission_grants`

**Dependencies:** PostgreSQL, canary-identity, canary-raas (session-key construction), every MCP-bearing service (consults canary-store-brain for permission gating)

---

### canary-blockchain-anchor — Bitcoin L2 Hash Anchoring
**Port:** 9086
**Role:** External-verifiability layer for Canary's evidentiary rail. Patent-protected (#63/991,596). Batches event hashes from canary-fox (evidence chain) and canary-raas (chain anchors), commits Merkle roots to a Bitcoin L2, and serves verification proofs to partners and regulators on demand. **Tier: Bulk window only** — per-event Bitcoin L2 commits are cost-prohibitive; batched hourly or daily commits are the canonical pattern. **Do not change this tier mapping** — it is enforced by the economics of L2 settlement.

**REST API:**
```
TIER: Bulk window (batched commits)
POST /blockchain-anchor/commit                    — Trigger immediate batch commit (ops)
GET  /blockchain-anchor/commits?since=&cursor=    — Commit history
GET  /blockchain-anchor/commits/:commit_id        — Single commit detail (txid, merkle root, hash count)

TIER: Reference (proof verification)
GET  /blockchain-anchor/proofs/:hash              — Get inclusion proof for a hash
POST /blockchain-anchor/verify                    — Verify a proof against on-chain state
GET  /blockchain-anchor/status                    — Last commit time, pending hash count, L2 health
GET  /health

TIER: Stream (rare — pending pool inspection)
GET  /blockchain-anchor/pending?merchant_id=      — Hashes queued for next commit
```

**Commit lifecycle:**
```
PENDING (in batch accumulator) → READY (batch full / time-window elapsed) → COMMITTING (L2 tx submitted) → CONFIRMED (block included) → ARCHIVED (terminal)
                                                                                       ↓
                                                                                    FAILED → RETRY
```

**Inclusion-proof contract:**
```
GET /blockchain-anchor/proofs/:hash
Response 200:
  {
    "hash": "sha256-hex",
    "merkle_root": "sha256-hex",
    "merkle_path": ["hash1", "hash2", ...],  // path to root
    "commit": {
      "id": "uuid",
      "btc_tx_id": "hex",
      "block_height": 850123,
      "block_time": "ISO8601",
      "l2_network": "liquid|rsk|stacks|other"
    }
  }
Response 404: hash not yet anchored or not found
```

**Patent-protected behavior:** The merkle-batching strategy and L2 selection economics are patent-protected territory. External partner docs cover the proof-verification contract; internal SDDs cover the batching algorithm.

**Tier classifications:** Commit operations → Bulk window. Proof retrieval and verification → Reference. Pending pool inspection → Stream (low-traffic ops use).

**Tenant isolation:** Hashes are anchored across all merchants in the same batch (cost-amortization), but proofs are merchant-scoped — a merchant can only verify proofs for hashes their services emitted.

**Tables owned:** `app.anchor_batches`, `app.anchor_pending_hashes`, `app.anchor_commits`, `app.anchor_l2_credentials`

**Dependencies:** PostgreSQL, canary-identity, canary-fox (evidence-chain hashes), canary-raas (chain-anchor hashes), Bitcoin L2 RPC (Liquid/RSK/Stacks/other)

---

### canary-field-capture — Semantic Field Mapping
**Port:** 9087
**Role:** pgvector-backed registry of semantic field mappings — when a POS adapter or import job encounters a non-standard field name (e.g., `cust_no` vs `customer_id` vs `acctNum`), canary-field-capture resolves it to the canonical Canary field via embedding similarity plus learned per-source mappings. Cross-references canary-owl for the embedding stack.

**REST API:**
```
TIER: Reference
GET  /field-capture/lookup?source=&field=         — Resolve raw field to canonical
GET  /field-capture/canonical/:canonical_id        — Canonical field definition
GET  /field-capture/mappings?source=&since=        — Learned mappings for source
GET  /health

TIER: Stream
POST /field-capture/register                       — Register new canonical field
POST /field-capture/learn                          — Learn a new mapping (raw → canonical)
PATCH /field-capture/mappings/:id                  — Override or correct learned mapping
DELETE /field-capture/mappings/:id                 — Remove learned mapping

TIER: Bulk window
POST /imports/field-mappings                       — Bulk import learned mappings (e.g., per-merchant migration)
```

**Field resolution flow:**
```
Adapter sees raw field "cust_no" from "counterpoint" source
  → POST /field-capture/lookup { source: "counterpoint", field: "cust_no" }
  → service computes embedding for "cust_no"
  → checks (source, field) → canonical mapping cache
  → if not cached, runs pgvector kNN against canonical field embeddings
  → returns canonical_id with confidence score; caches result
Adapter logs the resolution for audit and continues processing
```

**Tier classifications:** Lookups and canonical reads → Reference (cached resolutions). Mapping mutations → Stream. Bulk migration → Bulk window.

**Tenant isolation:** Canonical fields are merchant-agnostic (shared catalog). Learned mappings are merchant + source scoped to prevent cross-merchant pollution of the learning signal.

**Tables owned:** `app.field_canonical`, `app.field_mappings`, `app.field_mapping_audits`

**Dependencies:** PostgreSQL with pgvector, canary-identity, canary-owl (embedding generation), all POS adapters (consumers)

---

### canary-store-network-integrity — Cross-Location Anomaly Detection
**Port:** 9088
**Role:** Multi-store correlation and cross-location anomaly detection. Where canary-chirp evaluates rules within a single location's transaction stream, store-network-integrity correlates patterns across locations — collusion rings, transfer-loss conspiracies, schedule-coordinated shrink, regional fraud campaigns. Pairs with canary-bull for transfer-side intelligence and canary-fox for case escalation.

**REST API:**
```
TIER: Change-feed (anomaly tail)
GET  /store-network-integrity/anomalies?merchant_id=&severity=&since=&cursor=&limit=

TIER: Reference
GET  /store-network-integrity/anomalies/:id        — Single anomaly detail
GET  /store-network-integrity/cross-location/:case_id  — Cross-location correlation graph
GET  /store-network-integrity/networks?merchant_id=&since=  — Detected pattern networks
GET  /health

TIER: Stream
POST /store-network-integrity/anomalies/:id/escalate  — Open Fox case from anomaly
POST /store-network-integrity/anomalies/:id/dismiss   — False positive flag

TIER: Daily batch
POST /correlations/run                             — Trigger correlation evaluator (cron-driven)
```

**Anomaly schema:**
```
GET /store-network-integrity/anomalies/:id
Response 200:
  {
    "id": "uuid",
    "severity": "low|medium|high|critical",
    "pattern_class": "transfer-loss-conspiracy|schedule-coordinated-shrink|regional-fraud|...",
    "merchant_id": "uuid",
    "locations": [{ "location_id": "uuid", "role": "source|destination|peripheral" }, ...],
    "subjects": [{ "type": "employee|customer|vendor", "id": "uuid", "role": "..." }],
    "evidence": [{ "kind": "transaction|transfer|alert|case", "id": "uuid" }, ...],
    "confidence": 0.87,
    "detected_at": "ISO8601",
    "correlation_window": { "from": "...", "to": "..." }
  }
```

**Tier classifications:** Anomaly tail → Change-feed. Single-anomaly reads → Reference. Escalation/dismiss → Stream. Correlation evaluator → Daily batch (cron-driven; correlation windows of hours-to-days fit best at this tier).

**Tenant isolation:** Strict merchant scoping. Cross-merchant anomaly detection is an explicit super-admin operation requiring documented authorization.

**Tables owned:** `app.network_anomalies`, `app.network_correlations`, `app.network_pattern_classes`

**Dependencies:** PostgreSQL with pgvector, canary-identity, canary-tsp, canary-bull, canary-transfer, canary-alert, canary-fox

---

### canary-commercial — Vendor Finance & Reconciliation
**Port:** 9089
**Role:** Vendor relationship layer — invoice reconciliation, rebate accruals, chargebacks, and vendor-side deductions. Cross-references canary-receiving for the receipt side; closes the loop between physical goods received and financial obligations to vendors. The accountability rail here is reconciliation evidence — every chargeback records the underlying receipt or transfer variance as evidence.

**REST API:**
```
TIER: Reference
GET  /commercial/invoices/:id                      — Single invoice
GET  /commercial/rebates?vendor_id=&period=        — Rebate accrual state
GET  /commercial/chargebacks/:id                   — Chargeback detail
GET  /health

TIER: Change-feed
GET  /commercial/invoices?vendor_id=&status=&since=&cursor=&limit=
GET  /commercial/chargebacks?vendor_id=&status=&since=&cursor=&limit=
GET  /commercial/rebates/accruals?period=&since=&cursor=

TIER: Stream
POST /commercial/invoices                          — Submit invoice for reconciliation
POST /commercial/invoices/:id/match                — Trigger PO/receipt match
POST /commercial/chargebacks                       — Issue chargeback
PATCH /commercial/chargebacks/:id/status           — Transition chargeback state
POST /commercial/rebates/accrue                    — Record rebate accrual event

TIER: Daily batch
POST /commercial/reconciliation/run                — Daily merchant-wide reconciliation
GET  /commercial/reconciliation/runs/:id           — Reconciliation run detail

TIER: Bulk window
POST /imports/invoices                             — Vendor invoice batch import (EDI 810)
POST /exports/reconciliation                       — Export period reconciliation report
```

**Invoice match lifecycle:**
```
SUBMITTED → MATCHING → MATCHED → APPROVED → PAID (terminal)
                ↓          ↓
                └─→ DISPUTED → CHARGEBACK_ISSUED → RESOLVED (terminal)
```

**Chargeback flow:** When invoice match finds a variance (price mismatch, quantity mismatch, undelivered line), `POST /commercial/chargebacks` records the disposition with link to the receipt evidence (`receipt_id`, `transfer_id`) and updates canary-receiving's PO state to RECONCILED with chargeback note.

**Tier classifications:** Single reads → Reference. List/tail → Change-feed. Mutations → Stream. Reconciliation runs → Daily batch. Invoice imports + exports → Bulk window.

**Tenant isolation:** Strict merchant scoping. Vendor records are merchant-scoped (same vendor under different merchants is two records).

**Tables owned:** `app.vendor_invoices`, `app.invoice_lines`, `app.chargebacks`, `app.rebate_accruals`, `app.reconciliation_runs`

**Dependencies:** PostgreSQL, canary-identity, canary-receiving (PO + receipt linkage), canary-fox (evidence on disputed invoices), canary-l402-otb (chargeback impact on OTB budget)

---

### canary-l402-otb — L402-Gated Open-To-Buy
**Port:** 9090
**Role:** Patent-protected (#63/991,596) Lightning-gated open-to-buy budget enforcement. Allocates merchant-wide and category-level OTB budgets; reconciles against actuals on a change-feed cadence (15-minute default). The "gate" endpoint enforces budget compliance for any procurement action that touches OTB — POs, transfers, restocks, vendor commitments. **Tier: change-feed for reconciliation, stream only for the gate decision.** Do not change this mapping — periodic reconciliation prevents over-fitting; per-event reconciliation would burn the model.

**REST API:**
```
TIER: Reference
GET  /l402-otb/budget?merchant_id=&category=&period=  — Current budget state
GET  /l402-otb/allocations/:id                         — Single allocation record
GET  /l402-otb/policies                                — Allocation policies
GET  /health

TIER: Change-feed
GET  /l402-otb/reconciliations?since=&cursor=          — Reconciliation history
POST /l402-otb/reconcile                               — Trigger reconciliation (default 15-min cron)

TIER: Stream (gate decisions)
POST /l402-otb/gate                                    — Gate a procurement action

TIER: Stream (policy mutations)
POST /l402-otb/allocations                             — Create allocation
PATCH /l402-otb/allocations/:id                        — Adjust allocation
POST /l402-otb/policies                                — Define allocation policy

TIER: Bulk window
POST /exports/otb-state                                — Period-end budget state export
```

**Gate decision contract:**
```
POST /l402-otb/gate
Body:
  {
    "merchant_id": "uuid",
    "actor_id": "uuid",
    "action_kind": "po_submit|transfer_request|vendor_commit|other",
    "amount_cents": 12345,
    "category": "string?",
    "context": { "po_id": "uuid?", "transfer_id": "uuid?" }
  }
Response 200: { "allowed": true, "remaining_budget_cents": 567890, "decision_id": "uuid" }
Response 402: { "allowed": false, "reason": "budget_exhausted", "remaining_budget_cents": 0, "l402_invoice": "<lightning-invoice>" }
Response 422: { "error": { "code": "policy_violation", "policy_id": "uuid" } }
```

The 402 response carries an L402 Lightning invoice — clients that pay the invoice can continue the action, providing a built-in escalation path for legitimate over-budget operations. This is the patent-protected mechanism.

**Tier classifications:** Budget reads → Reference (60s TTL, invalidated on reconcile). Reconciliation → Change-feed. Gate decisions → Stream. Policy mutations → Stream. Period exports → Bulk window.

**Tenant isolation:** Strict merchant scoping. Budgets are per-merchant; cross-merchant queries return 403.

**Tables owned:** `app.otb_budgets`, `app.otb_allocations`, `app.otb_policies`, `app.otb_gate_decisions`, `app.otb_reconciliations`, `app.otb_l402_invoices`

**Dependencies:** PostgreSQL, canary-identity, canary-receiving (PO submit gates here), canary-transfer (transfer-cost gates here), canary-commercial (chargeback impact on budget), Lightning node (L402 invoice issuance)

---

### canary-compliance — Compliance Engine & Item Authorization
**Port:** 9091
**Role:** Cross-cutting compliance enforcement — item authorization (which items can be sold at which locations), regulatory zone management (state/county tax, age-verification, license requirements), and operational blocks (item × time-of-day restrictions, employee-role restrictions). Reference-tier service: lookups are slow-moving and heavily cached. The 7-tool MCP surface is where AI assistants consult compliance state at decision time.

**REST API:**
```
TIER: Reference (slow-moving lookups, heavy caching)
GET  /compliance/lookup?item_id=&location_id=&customer_id=&time=  — Resolved compliance state
GET  /compliance/zones/:id                          — Regulatory zone detail
GET  /compliance/items/:item_id/authorization       — Authorization state by location
GET  /compliance/policies                           — Policy catalog
GET  /compliance/blocks/:id                         — Single block detail
GET  /health

TIER: Change-feed (rare — policy updates)
GET  /compliance/audit-log?merchant_id=&since=&cursor=

TIER: Stream
POST /compliance/blocks                             — Create operational block
PATCH /compliance/blocks/:id                        — Update block
DELETE /compliance/blocks/:id                       — Remove block (soft-delete with audit)
POST /compliance/zones                              — Define regulatory zone
POST /compliance/items/:item_id/authorize           — Authorize item for location set
DELETE /compliance/items/:item_id/authorization     — Revoke authorization

TIER: Bulk window
POST /imports/regulatory-zones                      — Bulk regulatory updates (e.g., new state tax rules)
POST /imports/item-authorizations                   — Catalog-wide authorization sweep
POST /exports/compliance-attestation                — Period attestation report
```

### canary-compliance — MCP surface (Axis C)

| Tool | Tier | Purpose | Input | Output |
|---|---|---|---|---|
| `compliance.lookup` | Reference | Full compliance check | `{item_id, location_id, customer_id?, time?}` | `{allowed, blocks[], required_verifications[], applicable_policies[]}` |
| `compliance.list_zones` | Reference | Zones for a merchant | `{merchant_id, region?}` | `{zones[]}` |
| `compliance.zone_detail` | Reference | Single zone | `{zone_id}` | `{zone, applicable_rules[]}` |
| `compliance.item_authorization` | Reference | Item authorization across locations | `{item_id, merchant_id}` | `{authorized_locations[], unauthorized_locations[]}` |
| `compliance.create_block` | Stream | Add operational block | `{merchant_id, item_id, scope, reason}` | `{block_id, effective_at}` |
| `compliance.audit_log` | Reference | Recent compliance decisions | `{merchant_id, since, limit}` | `{events[]}` |
| `compliance.attest` | Stream | Generate attestation for period | `{merchant_id, period_start, period_end}` | `{attestation_id, artifact_url}` |

**Compliance lookup precedence:**
```
1. Operational block (item × location × time × employee role) — most specific wins
2. Item authorization at location (item-not-authorized → block sale)
3. Regulatory zone rules (age-verify, license-required, item-restricted)
4. Default: allowed
```

**Tier classifications:** Lookups → Reference (24h TTL, invalidated on policy change). Single-resource reads → Reference. Audit log → Change-feed. Mutations → Stream. Bulk imports/exports → Bulk window.

**Tenant isolation:** Compliance policies and blocks are merchant-scoped. Regulatory zones can be shared across merchants (e.g., California sales tax) but applicability is merchant-scoped.

**Tables owned:** `app.compliance_zones`, `app.compliance_zone_rules`, `app.compliance_item_authorizations`, `app.compliance_blocks`, `app.compliance_audit_log`, `app.compliance_attestations`

**Dependencies:** PostgreSQL, canary-identity, canary-item (item lookup), canary-employee (role checks), canary-customer (age/verification claims), canary-fox (evidence on attestation), canary-raas (key construction for cache)

---

## Business Process → Go Subsystem Mapping

This table traces the chain from business process through detection rule to Go microservice to primary database tables. Every engineer implementing a service should be able to trace their module back to the retail operation it represents. Every agent reasoning about a business failure should be able to trace forward to which service owns the data.

This mapping is the authoritative process decomposition for the Canary Go build. The module dependency graph determines build order: a module that feeds into another must be in Support mode before the downstream module advances to Service Introduction.

| Business Process Domain | Detection / Business Rule | Go Microservice | Primary Tables | Feeds Into |
|---|---|---|---|---|
| **Transaction processing — sale, tender, refund** | Chirp rules: C-001 to C-037 (Square era), 25+ Counterpoint rules | `canary-tsp` (ingest) → `canary-chirp` (evaluate) | `sales.transactions`, `sales.refund_links`, `app.detection_rules` | `canary-alert`, `canary-fox` |
| **Device identity — which terminal processed the event** | Device attribution on every transaction; N module feeds T | `canary-tsp` (device field normalization) | `sales.transactions.device_id`, `app.devices` (Module N scope) | `canary-chirp` (rule evaluation), ILDWAC Device dimension |
| **Loss prevention case — evidence assembly, investigation** | Fox evidence chain; Hawk incident workflow | `canary-fox`, `canary-hawk` | `app.fox_cases`, `app.fox_evidence`, `app.fox_timeline`, `app.hawk_cases` | `canary-owl` (analysis), Civil Services (Legal & Compliance gate) |
| **Alert lifecycle — detection to resolution** | Alert state machine: OPEN → ACKNOWLEDGED → INVESTIGATING → ESCALATED / DISMISSED | `canary-alert` | `app.alerts`, `app.alert_history` | `canary-fox` (on ESCALATED) |
| **Inventory receiving — PO, vendor, cost posting** | Receiving discrepancy rules; ILDWAC WAC update trigger | `canary-receiving` (Module V scope) | `sales.transactions` (receipt type), Module V tables | Module F (Finance), ILDWAC recalculation |
| **Stock management — on-hand, adjustments, cycle counts** | Inventory variance rules; shrink rate baseline | `canary-inventory` | `app.inventory_positions`, `app.inventory_adjustments` | Module Q (LP), Module O (Forecast) |
| **Pricing and promotion — rules, markdowns, exceptions** | Price override rules; unauthorized markdown detection | `canary-pricing` | `app.price_rules`, `app.promotion_events` | Module T (transaction validation), Module M (Merchandising) |
| **Customer identity — loyalty, purchase history** | Customer velocity rules; multi-card profiling | `canary-customer` | `app.customers`, `app.loyalty_accounts` | Module T (transaction enrichment), Module C scope |
| **Employee records — roles, schedules, access** | Employee attribution on transactions; labor ratio | `canary-employee` | `app.employees`, `app.schedules` | Module T (employee attribution), Module L (Labor) |
| **Returns and refunds — authorization, fraud detection** | Return fraud rules; refund-to-different-card | `canary-returns` | `app.return_authorizations`, `sales.refund_links` | Module Q (LP), Module F (Finance) |
| **Inter-store transfers — distribution, reconciliation** | Transfer-loss reconciliation; distribution recs | `canary-transfer` + `canary-bull` | `app.transfer_orders`, `app.transfer_lines` | Module D (Distribution), Module F (Finance) |
| **Semantic search and risk scoring** | Owl pgvector search; EJ Spine entity resolution; risk dictionary | `canary-owl` | `app.owl_chunks`, `app.risk_scores` | All consuming services (read-only) |
| **Metric rollups and risk baselines** | Hourly / daily rollups; entity risk scores; baseline computation | `canary-analytics` | `metrics.*` (all metrics schema tables) | Owl (analysis), Chirp (threshold calibration) |
| **Merchant identity and POS connections** | Namespace resolution; source registration; onboarding | RaaS (separate rebuild, REST interface) | `app.namespace_registrations`, `app.merchant_sources` | All services (Valkey key construction, merchant context) |

### Module dependency graph — build order

The graph below governs Service Introduction sequencing. A module cannot advance to Service Introduction until all modules it depends on are in Support mode.

| Module | Receives data from | Feeds data into |
|---|---|---|
| T — Transaction Pipeline | N (device identity), P (price values) | Q, R, F, A |
| N — Device | — | T |
| Q — Loss Prevention | T, A | Fox, Owl |
| C — Customer | T | P (loyalty earn rules) |
| P — Pricing & Promotion | R, C | T, C |
| M — Merchandising | S, P | D, F |
| S — Space, Range & Display | — | C, J |
| D — Distribution | C, J | A, F |
| O — Orders | S, D | C, D |
| F — Finance | T, C, D, A | — |
| A — Asset Management | T, D, Q | F |
| L — Labor | — | W |
| E — Execution | L | All modules (execution dispatch) |

Foundation dependency: all 13 modules depend on CRDM/Data Model, Identity/Auth, and Multi-POS Substrate. CRDM is the schema authority — cross-module schema changes require CRDM agent sign-off before consuming modules advance.

---

## Agent Smart Contracts — Interface Pattern

Every Go microservice boundary maps to an agent-to-module interface contract. The pattern is named here because it governs how domain agents extend, modify, and hand off modules across the Service Introduction lifecycle.

Each agent-to-module contract defines four elements:

| Contract element | Description |
|---|---|
| **Inputs** | Events, API calls, or escalations the service/agent accepts |
| **Outputs** | Artifacts produced: alerts, case records, metric writes, REST responses |
| **SLA** | Response time commitment per lifecycle phase |
| **Escalation path** | What happens when the service cannot resolve — always routes to Controller before surfacing to founder |

Full contract definitions will be written in `agent-contracts.md` (separate SDD, forthcoming). This pattern applies to every service in the map above.

---

## Inter-service communication

### Synchronous (REST)

| Caller | Callee | Call |
|---|---|---|
| canary-chirp | canary-alert | POST /alerts |
| canary-alert | canary-fox | POST /cases (on escalation) |
| canary-hawk | canary-gateway | POST /webhooks/square |
| canary-bull | canary-gateway | POST /webhooks/counterpoint |
| All services | canary-identity | POST /sessions/validate |

### Asynchronous (Valkey streams)

| Stream | Producer | Consumer |
|---|---|---|
| `canary:events` | canary-gateway | canary-tsp |
| `canary:detection` | canary-tsp (stage 4) | canary-chirp |

---

## Authentication between services

All internal REST calls carry a service token in the `Authorization` header:

```
Authorization: Bearer <service-jwt>
```

Service JWTs are signed with `INTERNAL_SERVICE_SECRET` (shared secret, not the user session secret). Each service validates inbound calls from other services using this key. canary-identity validates user-facing session tokens using `SESSION_SECRET`.

External-facing endpoints (canary-gateway webhooks, canary-identity OAuth) use POS-specific auth (HMAC, OAuth).

---

## Database ownership

Each service owns its tables exclusively. No service may write to another service's tables.

| Service | Schema | Tables |
|---|---|---|
| canary-tsp | sales.* | transactions, refund_links, line_items, line_item_discounts, cash_drawer_shifts, cash_drawer_events |
| canary-tsp | app | ingestion_log, merkle_batches |
| canary-chirp | app | detection_rules, merchant_rule_configs, location_rule_configs |
| canary-alert | app | alerts, alert_history |
| canary-fox | app | fox_cases, fox_timeline, fox_evidence, fox_subjects |
| canary-owl | app | owl_chunks, owl_sessions, risk_scores, risk_score_history |
| canary-analytics | metrics.* | all metrics schema tables |
| canary-identity | app | merchants, users, merchant_sources, merchant_settings, oauth_states |
| canary-hawk | app | hawk_* tables (8) |
| canary-bull | app | bull_* tables (4) |

Cross-service reads (e.g., chirp reading sales.transactions) are permitted read-only and do not violate ownership — ownership means write authority, not read exclusivity.

---

## Health check contract

Every service exposes `GET /health` returning:

```json
{
  "ok": true,
  "service": "canary-chirp",
  "version": "1.0.0",
  "checks": {
    "database": "ok",
    "valkey": "ok"
  }
}
```

On any dependency failure, return HTTP 503 with `"ok": false` and the failing check identified.

---

## Deployment topology

Each service is a standalone Go binary. Deployment target: containerized (one container per service). All services share one PostgreSQL instance and one Valkey instance in the initial deployment. Database connection pools are per-service.

**Recommended connection pool sizing (initial):**

| Service | Max connections |
|---|---|
| canary-tsp | 20 (pipeline throughput) |
| canary-analytics | 5 (scheduled jobs only) |
| canary-owl | 10 (vector queries) |
| All others | 10 |

**Port allocation summary:**

| Service | Port |
|---|---|
| canary-gateway | 8080 |
| canary-tsp | 8081 |
| canary-chirp | 8082 |
| canary-alert | 8083 |
| canary-fox | 8084 |
| canary-owl | 8085 |
| canary-analytics | 8086 |
| canary-identity | 8087 |
| canary-hawk | 8090 |
| canary-bull | 8091 |

---

## Environment variables (required by all services)

```
DATABASE_URL          postgresql://...    # Cloud SQL via private IP only — never public
                                          # Use Cloud SQL Auth Proxy or PgBouncer sidecar
VALKEY_URL            rediss://...        # TLS required in production (rediss://, not redis://)
INTERNAL_SERVICE_SECRET  <from Secret Manager>  # service-to-service JWT signing
JWT_SECRET            <from Secret Manager>     # platform JWT signing per go-security
CANARY_ENCRYPTION_KEY <from Secret Manager>     # AES-256-GCM, 32 bytes, per go-security
PHONE_HASH_KEY        <from Secret Manager>     # HMAC-SHA256 for PII lookup
EMAIL_HASH_KEY        <from Secret Manager>     # HMAC-SHA256 for PII lookup
LOG_LEVEL             info

# Optional Features — all default false (per platform-overview.md "Optional Features")
L402_ENABLED                  false
ILDWAC_ENABLED                false
BLOCKCHAIN_ANCHOR_ENABLED     false
VENDOR_CONTRACTS_ENABLED      false
BITCOIN_STANDARD_ENABLED      false
```

All production secrets are sourced from GCP Secret Manager via the runtime — never from `.env` files in production. Development secrets live in `.env` files that are git-ignored.

Service-specific vars are documented in each service's section above and in the individual SDDs.

---

## Production posture (GCP-native)

Per `platform-stack-commitment` and the Wave 2 dispatch, the production deployment posture is GCP-native:

| Component | Service | Notes |
|---|---|---|
| Compute | Cloud Run | Autoscaling per service; no fixed instance count |
| DB | Cloud SQL Postgres 17 (HA, regional) | Primary + 2 read replicas; PITR 7-day; cross-region replica for DR |
| Connection pooling | PgBouncer | Transaction-mode; deployed as a Cloud Run sidecar OR a dedicated service per tier |
| Cache | Memorystore (Valkey) | Private IP only; AUTH + TLS in production |
| Async | Pub/Sub + Cloud Tasks + Eventarc | Replaces Valkey streams for V2 multi-region |
| Secrets | GCP Secret Manager | All env-var secrets sourced at startup |
| Observability | Cloud Logging + Monitoring + Trace | Per `go-observability` |
| Edge | Cloud Load Balancing + Cloud Armor | Only public ingress point; internal services on private IP |

**SLA tiers** (per Wave 2 dispatch):
- Platform overall: 99.95% uptime, RPO 5 min, RTO 30 min
- POS write path (`canary-tsp`, `canary-gateway`): 99.95%, mission-critical
- Customer-facing API: 99.9%
- Internal services (analytics, reporting): 99.5%
- Audit log ingestion: 99.99%

---

## Related

- `architecture.md` — platform topology, multi-tenant isolation model, GCP target architecture
- `platform-overview.md` — Optional Features canonical section, product context, agent network
- `go-runtime.md` — service lifecycle, middleware stack, actor type discrimination
- `go-module-layout.md` — port registry, package layout, binary naming
- `go-security.md` — JWT validation, AES-256-GCM, HMAC, PII hashing
- `go-observability.md` — slog, Prometheus, OTel hooks
- `go-errors.md` — error taxonomy
- `data-model.md` — schema-per-tenant data model, full DDL
- `pos-adapter-substrate.md` — multi-POS abstraction this topology depends on
- `agent-contracts.md` — agent-to-module smart contract pattern (forthcoming)
- `raas.md` — namespace resolution and Valkey key construction
- `identity.md` — federation modes, membership boundary, JWT issuance
