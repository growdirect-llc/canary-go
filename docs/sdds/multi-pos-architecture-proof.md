---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: handoff-ready
updated: 2026-04-28
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Multi-POS Architecture — Abstraction Layer & Adapter Pattern

**Service Type:** App Service (Canary)
**Status:** Architecture validated, Phase 1 (Square) implemented
**Date:** 2026-04-13 (updated from 2026-03-22 original)

---

## Purpose

Canary is a horizontal data platform. The loss prevention engine operates on
canonical data models (CRDM) that are POS-agnostic by design. Adding a new
POS system requires adapter code — not architectural changes.

This SDD documents the abstraction layer that enables multi-POS support, the
adapter pattern each new POS must implement, and the current production state
of the architecture with code review findings.

**Multi-tenant context.** The CRDM lives in `public` schema as global reference; tenant-scoped event tables that adapter output lands into live in `tenant_{merchant_id}` per `architecture.md` "Multi-Tenant Isolation". Per-merchant adapter credentials are stored encrypted at rest in tenant schemas (per `go-security` AES-256-GCM standard).

**Optional Features posture.** The multi-POS abstraction is required core — it operates regardless of any Optional Feature flag state. The adapter pattern is foundational. POS port attribution is one of the five ILDWAC dimensions; when `ILDWAC_ENABLED=true`, the adapter populates the Port dimension automatically — when off, the dimension is recorded but not consumed by cost computation.

---

## Dependencies

| Dependency | Type | Required For |
|---|---|---|
| PostgreSQL 17 (`canary` database, `app` + `sales` schemas) | Database | Source registry, external identities, merchant sources, CRDM models |
| Valkey 8 (session store + dedup cache) | Cache/Stream | Webhook dedup, event stream (`canary:events`) |
| TSP Pipeline (Sub1-Sub4 consumers) | Internal service | Event processing after webhook receipt |
| Chirp Detection Engine | Internal service | Rule evaluation against parsed CRDM data |
| Source POS APIs (Square, Toast, Clover) | External API | Webhook delivery, OAuth/auth, data enrichment |

---

## Data Flow & PII Map

### What Enters

- **Webhook POST payloads** from POS systems via `POST /webhooks/<source>`.
  Currently Square only (`REGISTERED_SOURCES = {"square"}`). Raw bytes read
  before JSON parsing (patent-critical ordering).
- **HMAC signature headers** per source (`x-square-hmacsha256-signature` for
  Square). Validated before any payload processing.
- **OAuth tokens** during merchant onboarding (stored AES-256-GCM encrypted
  in `app.oauth_tokens`).

### What's Stored

| Table | Schema | PII Fields | Encryption Status | Classification |
|---|---|---|---|---|
| `source_systems` | app | None | N/A | public |
| `merchant_sources` | app | `external_merchant_id` | **Plaintext** | internal |
| `external_identities` | app | `external_id` (source-side entity IDs) | **Plaintext** | internal |
| `ingestion_log` | sales | `ip_address`, `user_agent` | **Plaintext** | sensitive |
| `transactions` | sales | `card_fingerprint`, `card_last4`, `card_bin`, `card_brand`, `card_exp_month`, `card_exp_year` | **Plaintext** | sensitive |
| `transactions` | sales | `payload` (full webhook JSON) | **Plaintext** | restricted |
| `dead_letter_queue` | sales | `payload` (full event payload for replay) | **Plaintext** | restricted |

### What Exits

- Parsed CRDM records written to `canary_sales` tables via Sub2 consumer
- Detection stream events published for Chirp evaluation via Sub4
- Stateless Tier 1 Chirp alerts written to `app.alerts` inline
- Ingestion log records for audit trail

---

## API Contract

### Webhook Entry Point

```
POST /webhooks/<source>
```

| Parameter | Source | Description |
|---|---|---|
| `source` (path) | URL | POS identifier, must be in `REGISTERED_SOURCES` |
| Body | Raw bytes | Full webhook payload |
| `x-square-hmacsha256-signature` | Header | HMAC signature (Square) |

**Response:** `200 OK` with `event_id` on success, `401` on HMAC failure,
`404` on unknown source, `413` on payload > 1MB, `503` on queue unavailable.

### Health Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/webhooks/health` | GET | Valkey stream connectivity + version |
| `/webhooks/ready` | GET | Signature key loaded, notification URL set, queue reachable |
| `/webhooks/live` | GET | Process alive |

### Internal Interfaces

| Interface | Description |
|---|---|
| `webhook_dispatch.resolve_route(event_type)` | Maps event type to `EventRoute` (parser, CRDM model, detection type) |
| `webhook_dispatch.import_parser(parser_path)` | Dynamic dispatch of parser function from services/parsers registry |
| `stream_publisher.publish_event(...)` | Publishes to message queue stream `canary:events` |
| `enrichers.enrich_payload(source, payload, event_type)` | Source-specific payload enrichment before stream publish |
| `square_validator.validate_signature(raw_bytes, sig_header)` | HMAC-SHA256 timing-safe validation |

---

## Architecture — Abstraction Layer

### Layer Scorecard (Current State)

| Layer | Implementation | Multi-POS Ready? |
|---|---|---|
| **Source Registry** (`app.source_systems`) | Reference table: `code` PK, `display_name`, `category`, `is_active`. Designed for N sources. | **Ready** — INSERT a row per POS |
| **Merchant Connection** (`app.merchant_sources`) | `(merchant_id, source_code)` unique, FK to `source_systems.code`. Status lifecycle, RaaS namespace. | **Ready** — no schema changes |
| **Identity Resolution** (`app.external_identities`) | Source-agnostic bridge: `(merchant_id, source_code, entity_type, external_id)` unique. 5 entity types. | **Ready** — INSERT with new `source_code` |
| **Webhook Entry** (`/webhooks/<source>`) | Route parameterized on `<source>`, gated by `REGISTERED_SOURCES` set. `SOURCE_VALIDATORS` dict maps source to validator module. | **Ready** — add source to set + validator dict |
| **Signature Validation** (validators/) | Strategy pattern: validators registry describes interface; Square implementation provides HMAC-SHA256. Interface: `validate_signature()`, `extract_merchant_id()`, `extract_source_event_id()`, `extract_event_type()`. | **Ready** — add `validators/toast.go` following same interface |
| **Payload Enrichment** (enrichers/) | Strategy pattern: `_SOURCE_ENRICHERS` dict, Square enricher fetches full order objects for lightweight notifications. | **Ready** — add `enrichers/toast.go` |
| **Event Dispatch** (webhook_dispatch) | Flat `event_type -> EventRoute` lookup. ~70 exact-match routes + ~25 prefix routes. All Square event types. | **Needs generalization** — dispatch keys are bare event types, not `(source_code, event_type)` compound keys |
| **Parser Suite** (services/parsers/) | 17 `square_*` parser files. Pure functions: JSON in, flat dict out. Dynamic dispatch via `import_parser()`. | **Needs new code** — add `toast_*` parser files following same pattern |
| **CRDM Models** (Transaction, etc.) | Source-agnostic columns. `card_fingerprint` indexed. No `source_code` column on Transaction. | **Needs minor changes** — add `source_code` column |
| **Stateless Chirps** (Tier 1) | Hardcoded `if source == "square"` check in webhook handler. | **Needs generalization** — remove source guard |
| **Evidence Chain** (Sub1 seal + Sub3 Merkle) | Operates on raw event bytes and chain hashes. No POS-specific field dependency. | **Ready** — source-agnostic by design |

### POS Adapter Pattern

Each new POS requires five components — a repeatable sprint:

| Component | Responsibility | Location Pattern |
|---|---|---|
| **Auth Adapter** | Connect merchant, manage tokens | `services/<pos>_oauth.go` |
| **Signature Validator** | Verify inbound webhooks | `services/tsp/validators/<pos>.go` |
| **Payload Enricher** | Fetch full objects for lightweight notifications | `services/tsp/enrichers/<pos>.go` |
| **Event Registry** | Map POS events to CRDM parsers | Entries in `webhook_dispatch.go` (needs compound key) |
| **Parser Suite** | Transform POS JSON to CRDM flat structs | `services/parsers/<pos>_*.go` |

### Current Implementation (Square)

- **17 parser files** in `services/parsers/`: payment, order, dispute, loyalty, payout, auxiliary, card, invoice, terminal, bank account, transfer order, subscription, customer, location, device, gift card, team member.
- **~70 exact-match event routes** + ~25 prefix catch-all routes covering all ~145 Square webhook event types.
- **Validator module:** `tsp/validators/square.go` — HMAC-SHA256 with `SQUARE_WEBHOOK_SIGNATURE_KEY` and `SQUARE_NOTIFICATION_URL` env vars.
- **Enricher module:** `tsp/enrichers/square.go` — fetches full order objects for lightweight Square order webhooks.

---

## Toast and Clover Validation

### Toast (Restaurant POS)

Toast uses OAuth 2.0 client-credentials grant (machine-to-machine, no merchant
consent flow). Primary webhook is `order_updated` containing the full Order
object with Check-level granularity. ~8 event types total.

**Card fingerprint gap:** Toast's `cardPaymentId` is unreliable for EMV/chip
transactions (frequently null). Best available: `last4Digits + cardType`
composite key (~50K collision space vs Square's cryptographic fingerprint).

**Structural advantages:** Split check awareness, three-level void tracking
(`voided` + `voidDate` + `voidBusinessDate` at order/check/selection),
employee directly on order (`server.guid`), native `businessDate` field.

### Toast Data Model Mapping

| Canary CRDM Field | Square Source | Toast Source | Status |
|---|---|---|---|
| `transaction.external_id` | `payment.id` | `order.guid` | Direct map |
| `transaction.card_fingerprint` | `card.fingerprint` | Composite: `last4 + cardType` | **Degraded** |
| `transaction.entry_method` | `card_details.entry_method` | `payment.cardEntryMode` | Direct map (enum remap) |
| `transaction.employee_id` | `tender.employee_id` | `order.server.guid` | Direct map |
| `transaction.device_id` | `tender.device_id` | `order.device` | Direct map |
| `transaction.location_id` | `payment.location_id` | Header: `Toast-Restaurant-External-ID` | Direct map |
| `transaction.customer_id` | `order.customer_id` | Only on takeout/delivery | **Gap** (no dine-in customer) |
| `transaction.business_date` | Computed | `order.businessDate` | Direct map (Toast native) |
| `tender.tender_type` | `tender.type` | `payment.type` | Enum remap |
| `tender.card_brand` | `card.card_brand` | `payment.cardType` | Direct map |
| `tender.card_last4` | `card.last_4` | `payment.last4Digits` | Direct map |
| `tender.amount_cents` | `tender.amount_money.amount` | `payment.amount` x 100 | Unit conversion |

### Clover (General Retail POS)

Clover uses OAuth 2.0 authorization-code grant (like Square). Sends
**notification-only webhooks** — payload contains object ID + event type, not
full object. Requires fetch-then-parse pattern.

**Card fingerprint gap:** No native fingerprint. Composite key:
`first6 + last4 + cardType` (~50B collision space — better than Toast because
BIN is exposed on card-present reads).

**Architectural wrinkle:** Notification-only webhooks require a fetch step
between webhook receipt and parser dispatch. The enricher pattern already
handles this — `enrichers/clover.go` would always fetch the full object.

---

## Card Fingerprint and Customer Identity

### The Card Fingerprint Gap

**Square** provides `card.fingerprint` — a cryptographic hash that uniquely identifies
a physical card across all transactions. Same card always produces the same hash. This
powers the C-005 (CARD_VELOCITY) rule and is the foundation of card risk scoring.

**Toast** provides `cardPaymentId` — described as a "unique non-sensitive card
identifier." However, this field is **unreliable for EMV (chip) and keyed entries** —
availability varies by firmware and payment processor, and is frequently null.

**Clover** provides no card fingerprint at all. The `cardTransaction` sub-object
(requires `?expand=cardTransaction`) exposes `first6 + last4 + cardType` as the
best composite identity.

### Source-Aware Confidence Tiers

The Chirp rule engine must be **source-aware** — same rule, different confidence
levels depending on what the POS provides:

| Tier | Criteria | Card Identity Method | Confidence |
|---|---|---|---|
| **Tier 1** | Cryptographic fingerprint available | `card.fingerprint` (Square) | High |
| **Tier 2** | Composite key with BIN | `first6 + last4 + brand` (Clover) | Medium-high |
| **Tier 2** | Composite key without BIN | `last4 + brand` (Toast) | Medium |
| **Tier 3** | Cash / no card data | N/A | N/A — rule skipped |

Alert records must carry a `detection_confidence` field so downstream consumers
(Hawk cases, dashboard, EJ spine) can weight alerts appropriately.

### Impact on Chirp Rules

| Rule | Square Behavior | Toast Behavior |
|---|---|---|
| **C-005 CARD_VELOCITY** (same card 5+ times/hour) | Exact match on `card_fingerprint`. High confidence. | Composite match on `last4 + brand`. Collision risk. **Degraded.** |
| **C-008 MANUAL_ENTRY_SPIKE** (5+ keyed-in cards/shift) | `entry_method = KEYED` | `cardEntryMode = KEYED`. **Works.** |
| **C-010 PARTIAL_AUTHORIZATION** | `approved_amount < requested_amount` | Same fields available. **Works.** |
| **Card risk scoring** | Fingerprint-based aggregation | Cannot implement at same confidence. **Degraded.** |
| **Refund-to-different-card** (future) | Fingerprint comparison | Cannot implement. **Blocked.** |

### Customer Identity Gap

**Square** provides a Customer API. Customer IDs appear on transactions.

**Toast** has no Customer API. Customer data only appears on takeout and delivery
orders. Dine-in orders carry no customer identity beyond `server` (employee) and
an optional freeform `tabName`. Customer-level LP patterns cannot be detected on
Toast dine-in transactions — a data availability limitation of the POS, not a
Canary architectural gap.

---

## Three-POS Comparison — Card Identity

| Capability | Square | Toast | Clover |
|---|---|---|---|
| Card fingerprint | Native `card.fingerprint` — cryptographic, deterministic | `cardPaymentId` — unreliable for EMV | None |
| BIN (first 6) | Available | Auth flow only, not on Orders API | Available via `?expand=cardTransaction` |
| Last 4 | Available | Available | Available |
| Card brand | Available | Available | Available |
| Entry method | Available | Available | Available |
| Composite key quality | Not needed (fingerprint) | `last4 + brand` (~50K space) | `first6 + last4 + brand` (~50B space) |
| Confidence tier | **Tier 1** (high) | **Tier 2** (medium) | **Tier 2** (medium-high) |

---

## Toast API Characteristics

### Authentication

| Aspect | Square | Toast |
|---|---|---|
| Grant type | Authorization-code (merchant consent) | Client-credentials (partner agreement) |
| Token endpoint | `/oauth2/token` | `/authentication/v1/authentication/login` |
| Restaurant scoping | `merchant_id` in token | `Toast-Restaurant-External-ID` header |
| Multi-location | Per-merchant token | JWT with partner GUID or management set |
| Onboarding | Merchant installs app, grants scopes | Toast approves partner, assigns restaurants |

### Webhooks

Toast webhooks deliver full object payloads. The primary LP-relevant webhook is
`order_updated`, which fires on every order lifecycle event (create, payment, void,
refund, discount, status change). The full Order object is included in the payload.

**Idempotency:** Each event has a GUID. Deduplicate on event GUID.
**Fallback:** Every webhook has a corresponding polling API. Toast recommends
periodic polling as backup.

### Rate Limits

| Scope | Limit |
|---|---|
| Default (most APIs) | 20 req/s AND 10,000 req/15 min |
| Orders `/ordersBulk` | 5 req/client/location/second |
| Menus `/menus` | 1 req/s/location |
| Historical queries | Max 1-month range, 5-10s spacing |

Rate limits are tighter than Square. Webhook-first architecture is mandatory.

---

## Operations

### Startup Sequence

1. HTTP service loads, registers webhook handler at `/webhooks` prefix
2. CSRF protection exempts webhook handler (HMAC is the auth mechanism)
3. Readiness probe (`/webhooks/ready`) checks: message queue stream ping, signature
   key loaded (`SQUARE_WEBHOOK_SIGNATURE_KEY`), notification URL configured
   (`SQUARE_NOTIFICATION_URL`)
4. If any check fails, readiness returns 503 — load balancer stops routing

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| Message queue stream unreachable | Webhooks cannot be queued | Returns 503 with `retry_after_seconds: 30`. Source POS retries with backoff. |
| Signature key not configured | Cannot validate any webhooks | Returns 503. Readiness probe fails. |
| JSON parse failure | Payload corrupted or non-JSON | Accepts and hashes raw bytes. Publishes with `parse_failed=true`. Sub1 stores, Sub2 skips structured parse. |
| Duplicate event | Idempotency | Returns 200 OK with `duplicate: true`. Checked via dedup cache (24h TTL). DB unique constraint is backstop. |
| DB write failure (ingestion_log) | Audit trail gap | Logs error but does NOT fail the webhook — stream is the source of truth. |
| Enrichment failure | Lightweight payload persists | Logs warning, continues with unenriched payload. Non-blocking. |
| Stateless chirp failure | Detection gap | Logs warning, continues. Tier 1 chirps are additive, never block pipeline. |

### Monitoring

| Metric | Alert Threshold | Source |
|---|---|---|
| Webhook acceptance rate | < 95% over 5 min | Ingestion log status counts |
| HMAC failure rate | > 5% over 1 min | Warning log entries (`HMAC verification failed`) |
| Stream publish failures | Any | Error log entries (`Failed to publish to stream`) |
| Dedup cache hit rate | Informational | Valkey key count |
| Parse failure rate | > 1% over 15 min | Ingestion log `parse_failed` flag |

### Configuration

| Env Var | Required | Default | Purpose |
|---|---|---|---|
| `SQUARE_WEBHOOK_SIGNATURE_KEY` | Yes | — | HMAC signing key for Square webhooks |
| `SQUARE_NOTIFICATION_URL` | Yes | — | URL Square posts to (used in HMAC computation) |
| `MAX_PAYLOAD_BYTES` | No | `1048576` (1MB) | Maximum webhook payload size |
| `VALKEY_URL` | Yes | `redis://localhost:6379/0` | Valkey connection string |
| `VALKEY_STREAM` | No | `canary:events` | Stream name for event publishing |
| `VALKEY_DEAD_LETTER_STREAM` | No | `canary:dead_letter` | Dead letter stream name |
| `CANARY_ENCRYPTION_KEY` | Yes | — | AES-256-GCM key for OAuth token encryption |

---

## Deployment

### Service Topology

The webhook endpoint runs inside the Canary HTTP service, not as a separate service.
Handler registered at `/webhooks` prefix. Port 5001.

### AWS Target

| Component | AWS Service | Notes |
|---|---|---|
| Webhook endpoint | ECS/Fargate (Canary HTTP service) | Behind ALB, path-based routing to `/webhooks/*` |
| Valkey stream | ElastiCache (Valkey mode) | Separate logical stores for sessions and dedup cache |
| PostgreSQL | RDS PostgreSQL 17 | `canary` database, `app` + `sales` schemas |
| Signature keys | Secrets Manager | `SQUARE_WEBHOOK_SIGNATURE_KEY`, future POS keys |
| Encryption key | Secrets Manager | `CANARY_ENCRYPTION_KEY` |

### Test Requirements

- Unit tests for event dispatch (all ~95 routes), merchant ID resolution, and usecase mapping
- Integration tests for merchant lifecycle
- Smoke tests for API endpoints and security headers
- Each new POS adapter needs its own validator test, parser tests, and integration test for the webhook-to-CRDM flow

---

## Code Review Findings

### P0 — Blocks Production

**P0-1: Raw webhook payloads stored with PII in plaintext.**
`Transaction.payload` (full JSON) and `DeadLetterQueue.payload` store complete
webhook payloads including card data (`card_fingerprint`, `card_last4`,
`card_bin`, `card_exp_month/year`), customer IDs, and employee IDs. No
field-level encryption or redaction before storage.
**Fix:** Redact PII fields from `payload` column before persistence. Apply
AES-256-GCM encryption to retained forensic payloads. Consider storing only
the fields needed for replay, not full payloads.

**P0-2: Card data fields stored in plaintext on Transaction model.**
`card_fingerprint`, `card_last4`, `card_bin`, `card_exp_month`, `card_exp_year`
are stored as plaintext strings in `sales.transactions`. These are PCI-adjacent
fields.
**Fix:** Encrypt `card_fingerprint`, `card_last4`, `card_bin` at rest using
AES-256-GCM. Hash `card_fingerprint` for index lookups (store both encrypted
value and HMAC hash for search).

**P0-3: Encryption key in environment file.**
`CANARY_ENCRYPTION_KEY` and `SQUARE_WEBHOOK_SIGNATURE_KEY` are stored in
environment files. Production deployment must use AWS Secrets Manager.
**Fix:** Add Secrets Manager retrieval at service startup.

**P0-4: IP address logged in plaintext.**
`ingestion_log.ip_address` stores raw source IP addresses. PII under GDPR
and CCPA.
**Fix:** Hash or mask IPs before storage. Use HMAC with a rotating salt for
IP-based rate limiting lookups without storing raw IPs.

### P1 — Before GA

**P1-1: No rate limiting on webhook endpoint.**
`POST /webhooks/<source>` has no rate limiting. A compromised or misbehaving
POS integration could flood the pipeline.
**Fix:** Add rate limiting on webhook endpoint. Suggested: 100 req/s per
source IP, 1000 req/s global.

**P1-2: Event dispatch not generalized for multi-POS.**
Dispatch uses flat `event_type` keys (e.g., `payment.completed`).
Adding Toast requires `(source_code, event_type)` compound keys to avoid
collision between POS systems that may use the same event type string.
**Fix:** Change `EVENT_ROUTES` key to `f"{source}:{event_type}"` or add
source-prefixed dispatch tables.

**P1-3: Stateless chirp evaluation hardcoded to Square.**
A source guard blocks Tier 1 detection on any non-Square source.
**Fix:** Remove source guard. Dispatch already resolves the parser; if the
parser exists for the source/event_type, run stateless chirps.

**P1-4: No `source_code` column on Transaction model.**
The `sales.transactions` table has no column to identify which POS system
originated the record. Multi-POS queries require joining through `ingestion_log`
or `external_identities`.
**Fix:** Add `source_code` column to Transaction model. Database migration.
Backfill existing rows with `'square'`.

**P1-5: No data retention policy for ingestion_log or dead_letter_queue.**
`ingestion_log` and `dead_letter_queue` grow unbounded. No automated purge.
**Fix:** Implement retention: ingestion_log > 90 days archived, DLQ resolved
entries > 30 days purged.

**P1-6: No confidence tier metadata on Chirp alerts.**
Alerts carry no `detection_confidence` field. When non-Square sources with
composite card identity feed the pipeline, downstream consumers (Hawk cases,
dashboard) cannot weight alerts appropriately.
**Fix:** Add `detection_confidence` field to Alert model. Source-aware Chirp
rule evaluation sets confidence based on card identity quality per source.

### P2 — Post-Launch

**P2-1: No key rotation procedure documented.**
Encryption keys and webhook signature keys have no rotation procedure. Adding
POS sources adds more keys to manage.
**Fix:** Document rotation procedure per key. Implement dual-key read
(decrypt with current, fallback to previous) for zero-downtime rotation.

**P2-2: Dedup cache creates a new connection per request.**
No connection pooling for the dedup cache Valkey database.
**Fix:** Use a module-level connection pool or share the connection via the
stream publisher pattern.

**P2-3: Merchant ID lookup creates a DB round-trip per webhook.**
Merchant ID resolution inside the webhook handler queries the database on
every incoming event.
**Fix:** Refactor merchant ID resolution to use a cached lookup (Valkey or
in-process LRU) instead of per-request DB query.

**P2-4: Parser dispatch is not cached.**
Parser function lookup performs a fresh import/resolution on every event.
**Fix:** Add module-level cache for resolved parser functions.

---

## Production Readiness Checklist

- [ ] PII encrypted at rest — **FAIL**: Card data fields, IP addresses, and full webhook payloads stored plaintext (P0-1, P0-2, P0-4)
- [ ] Secrets in AWS Secrets Manager — **FAIL**: Keys in environment files (P0-3)
- [x] Health check endpoints respond — `/webhooks/health`, `/webhooks/ready`, `/webhooks/live` all implemented
- [ ] Audit logging for sensitive operations — **PARTIAL**: ingestion_log records webhook receipt but no audit trail for key access, config changes, or source registration
- [ ] Data retention policy implemented — **FAIL**: No automated purge for ingestion_log or DLQ (P1-5)
- [ ] Rate limiting on webhook endpoint — **FAIL**: No rate limiting (P1-1)
- [x] Error responses don't leak internals — webhook responses return structured JSON with status codes, no stack traces or internal paths
- [ ] Multi-POS dispatch generalized — **FAIL**: Flat event type keys, no compound `(source, event_type)` dispatch (P1-2)
- [ ] Source-aware detection confidence — **FAIL**: No confidence tiers on alerts (P1-6)
- [x] Idempotency — Valkey dedup cache + DB unique constraint implemented
- [x] Signature validation — Timing-safe HMAC-SHA256 implemented for Square
- [x] Graceful degradation — JSON parse failure, enrichment failure, and stateless chirp failure all handled without blocking pipeline

---

## RaaS — Namespace Layer for Multi-POS Events

RaaS (Resolution as a Service) is the identity bridge that ties merchant data across POS systems. It is excluded from the Go corpus and will be rebuilt separately, but the Go multi-POS layer depends on its interface contract.

### Why multi-POS requires RaaS

When the same merchant has both a Square connection and a Counterpoint connection, events from both sources flow through the same `canary:events` stream. The CRDM records need to resolve to a single merchant identity — but Square uses its own merchant IDs and Counterpoint uses its own company alias. RaaS is the layer that normalizes both to `raas:{merchant_id}`.

Without RaaS, multi-POS queries require joining through `external_identities` on every read. With RaaS, the namespace is the resolution surface — all Valkey keys, all CRDM records, and all cross-service calls use `raas:{merchant_id}` as the canonical identifier.

### RaaS interface contract for the multi-POS adapter

| Call | When to call | What it returns |
|---|---|---|
| `resolve_namespace(merchant_id)` | On any cross-POS query that needs to confirm active sources | `{namespace, resolved}` — confirms merchant has at least one active source |
| `get_sources(merchant_id)` | When building a multi-POS aggregated query | `{sources[], count}` — list of active `source_code` values for this merchant |
| `register_source(merchant_id, source_code, external_merchant_id)` | During POS onboarding / connection | `{namespace, source_code, status}` |
| `build_key(merchant_id, parts[])` | Before writing any Valkey key | `{key}` — canonical `raas:{merchant_id}:{domain}:{key}` |

### Source-transparent consumption

Downstream consumers (Chirp, Owl, Fox) query via `raas:{merchant_id}` without knowing which POS populated the data. Source attribution lives on `CanonicalEvent.provider` for rule-applicability filtering (some Chirp rules are Counterpoint-only). The namespace is source-agnostic; the event record is source-annotated.

**Full RaaS SDD:** `docs/sdds/canary/raas.md` — 7 MCP tools, JWT-gated, REST internally.

---

## ILDWAC and the Port Dimension

The POS connector (Port) is a cost dimension in the extended ILDWAC model. Every event that flows through the multi-POS adapter layer carries a `source_code` that becomes the Port dimension when ILDWAC is implemented.

> **Status: architectural direction, not yet implemented.**

What this means for the adapter layer design decisions today:

- The `source_code` column on `sales.transactions` is not optional (P1-4 in the findings section). It is the Port dimension. It must be present before ILDWAC can be computed.
- Events from Square and events from Counterpoint will produce different provenance signatures even when the item, location, and cost are identical. This is the design intent — the cost carries its audit trail as a dimension, not as an attached note.
- The dedup cache key should be `(source_code, event_id)` — not bare `event_id` — to avoid collisions between POS systems that may generate non-unique event IDs in isolation.

When ILDWAC is formally designed (a separate GRO ticket), the `source_code` field already on the transaction record is the Port input. No schema migration will be needed if P1-4 is resolved now.

---

## Implementation Scope per POS

Each new POS is a sprint — five adapter components:

| Work Item | Complexity | One-Time? |
|---|---|---|
| Auth adapter (source-specific OAuth/credentials) | Medium | Per POS |
| Webhook signature validator | Low | Per POS |
| Event registry entries | Low | Per POS |
| Parser suite (source-specific field mappings) | Medium | Per POS |
| Payload enricher (for notification-only webhooks like Clover) | Low | Per POS |
| Generalize dispatch to `(source_code, event_type)` | Low | **One-time** |
| Add `source_code` to Transaction table | Low | **One-time** |
| Chirp confidence tiers | Medium | **One-time** |
| Remove `source == "square"` guard on stateless chirps | Low | **One-time** |

**Verdict:** The horizontal platform thesis is validated. Three structurally
different POS systems map to the same adapter pattern without architectural
changes. The one-time generalizations (dispatch compound key, `source_code`
column, confidence tiers) serve all future POS integrations.

---

## What Toast Does Better Than Square

Toast's data model has several advantages for loss prevention:

1. **Split check awareness.** Check-level model exposes voids, discounts, and payments per-check within a single order. Square merges at the order level.
2. **Three-level void tracking.** `voided` + `voidDate` + `voidBusinessDate` at order, check, AND selection levels. Square's void data is more limited.
3. **Employee on order.** `server.guid` directly on the order object. Square puts `employee_id` on tenders — requires chasing through payment objects.
4. **Business date as first-class field.** `businessDate` integer handles overnight restaurants correctly. Square requires computation from timestamp + timezone.
5. **Cash management API.** Dedicated API for cash drawer events — useful for cash-based fraud detection beyond what Square exposes.
6. **Security integration cookbook.** Toast publishes documentation for building LP integrations, recommending monitoring of discounts, voids, and refunds with timestamp correlation to video. LP is a validated use case on their platform.

---

*Canary | GrowDirect LLC | Confidential*
