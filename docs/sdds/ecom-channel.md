---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Solex reference implementation + Canary Go platform architecture
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Ecom Channel — Ecommerce Channel Integration Service

> **Type:** Channel Integration — Order & Catalog Gateway
> **Binary:** `cmd/ecom-channel` → port **9080**
> **MCP server:** `canary-ecom` (7 tools)
> **Last reviewed:** 2026-04-29

**Owns:** Channel adapter lifecycle · order ingest · catalog sync · subscription management · channel webhook processing.
**Feeds:** RaaS (order events) · Owl (searchable order data) · Fox/Hawk (return fraud signals) · analytics.

**Reference implementation:** `/Users/gclyle/GrowDirect/Solex/` — a working Flask ecommerce app that proves every architectural claim in this spec. When in doubt, read the code.

---

## Hat 1 — Business Context

### The Gap That Breaks Retail Analytics

Legacy loss prevention systems are built around the POS terminal. Every transaction model, every shrink rule, every exception report assumes the transaction happened at a register. The moment a merchant adds an online channel, that assumption collapses — and it collapses silently. Ecom returns that bypass the in-store process do not appear in the POS event stream. Subscription gaps that indicate a payment failure show up nowhere. A price discrepancy between the Square Online catalog and the in-store POS looks like two different businesses to every system that processes them separately.

This is where retail analytics traditionally breaks down. Canary's ecom-channel service closes that gap by design.

The architecture makes a deliberate choice: ecommerce orders flow into the same RaaS event chain as POS transactions. Not a parallel stream. Not a separate database. The same chain. This means Canary can write detection rules that correlate across channels — an ecom refund against an in-store purchase, a catalog price discrepancy between Square Online and Counterpoint, an autoship cadence gap that precedes an in-store theft spike. None of those correlations are possible if the data lives in separate systems.

**Multi-tenant context.** Ecom-channel tables (`ecom_channels`, `ecom_orders`, `ecom_subscriptions`, `ecom_webhook_events`) live per-tenant in `tenant_{merchant_id}`. Each merchant configures their own channels (Square Online, Shopify, etc.) with merchant-scoped credentials. Cross-tenant ecom benchmarks (channel performance comparisons) flow through `analytics` schema rollups. Cryptographic erasure for `customer_email` and `customer_name` (per `platform-cryptographic-erasure`) operates per-merchant, with the per-subject DEK pattern keeping chain integrity intact across erasure events. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Ecom-channel operates with all Optional Features (per `platform-overview.md`) disabled. Order ingest, catalog sync, subscription lifecycle, and channel webhook processing all run on internal records. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, order placement events anchor to a public L2 — making cross-channel return correlation externally verifiable. When `L402_ENABLED=true`, premium channel features (real-time inventory broadcast, multi-channel reconciliation) may be paid MCP tools. The multi-tier assortment fulfillment routing (store / warehouse / expanded) operates regardless of flag state — it is required core for any merchant with multiple fulfillment surfaces.

### Five Capabilities, One Service

ecom-channel owns five distinct capabilities, each of which has real LP consequence:

**Channel adapter pattern.** Square Online is V1. Shopify and WooCommerce are V2+. The adapter interface abstracts provider-specific API calls behind a contract, so adding a new channel does not touch any core business logic. This is the architectural decision that keeps the service from becoming a Square-specific integration masquerading as a platform capability.

**Order ingest.** Every ecom order enters the RaaS chain as a typed event: `ecom.order.placed`, `ecom.order.fulfilled`, `ecom.order.refunded`. An ecom return that circumvents the in-store process is a return fraud vector. Without the order event in the chain, Hawk's return fraud detection is blind to it.

**Multi-tier fulfillment routing.** An ecom store is attached to a physical store location and inherits that store's regular assortment for BOPIS pickup, but it is not constrained to it. Three assortment tiers are surfaced through the storefront and routed at order time: store assortment (on-hand at the attached store, BOPIS-eligible), warehouse assortment (centralized stock, ship-to-customer from warehouse, no BOPIS), and expanded assortment (special-order via vendor drop-ship for items not stocked anywhere). The merchant offers a richer catalog than the store physically carries without breaking the BOPIS trust model — only store-tier items show "pickup in an hour"; warehouse-tier items show "ships in 3 days"; expanded-tier items show "special order, 5–10 days". Routing logic and availability formulas are owned by `cmd/inventory-as-a-service` (see "Multi-Tier Assortment Model"). ecom-channel surfaces tier-aware availability at cart, calls IaaS for the routing decision at checkout, and emits the routed fulfillment plan as part of `ecom.order.placed`.

**Catalog sync.** Canary's item master is the source of truth. Square Online is the downstream. When prices diverge — because someone edited the Square catalog directly without going through Canary — the discrepancy is a shrink opportunity. Catalog sync catches it. The pull direction (Square → Canary) is a soft alert path; the push direction (Canary → Square) is authoritative.

**Subscription / autoship management.** Recurring charge scheduling is more than a billing convenience. Autoship cadence is a predictive signal — a gap in expected recurring revenue often precedes or coincides with a supply chain anomaly or a fulfillment discrepancy. Canary feeds this signal into predictive analytics. The subscription status machine (active → paused → cancelled) and charge failure history are the inputs.

**Webhook processing.** Channel webhooks arrive here, not at the general webhook-pipeline service. ecom-channel owns its own webhook receiver for channel-sourced events (order updates, catalog changes, payment events from Square Online). The general webhook-pipeline handles POS adapter events (Square Point of Sale, NCR Counterpoint). ecom-channel handles ecommerce channel events. The distinction is maintained deliberately — the data shapes, signature methods, and processing semantics differ between POS and ecom channel providers.

### Solex Is Illustrative

Canary Go is a clean codebase. It pulls patterns from many sources — the Python Canary prototype (frozen at `v0-python-prototype`), the SDD corpus in this directory, the agent PMO architecture spec, patent #63/991,596 disclosures, and Solex. None of those is the source of truth on its own; the SDD library is. Each illustrative source informs a different region of the design.

Solex (`/Users/gclyle/GrowDirect/Solex/`) is the illustrative reference for ecom-channel — a production-grade Flask ecommerce implementation covering checkout, subscriptions, and catalog management against the Square API. It is one concrete realization of the patterns this SDD specifies, not a literal port target. Solex was built single-channel (Square only) and single-tenant; ecom-channel generalizes those patterns into the multi-channel, multi-tenant, agent-driven contract this document defines.

**How to use Solex:** when the SDD specifies what the platform needs, Solex shows what it can look like when the rubber meets the road. Read it for proven behavior — failure recovery sequencing, idempotency keys, webhook ordering, cart fingerprinting — patterns that are easier to grok from working code than from spec prose. Where the SDD and Solex diverge, the SDD wins; Solex was a useful narrowing, not a constraint on the platform.

#### Solex Port-Forward Inventory

The table below maps Solex sources to the Go modules they inform. It is a starting map for the build dispatch, not a literal copy-list — Solex is one example, not the only model.

| Solex source (Python) | Go target | Port-forward note |
|---|---|---|
| `solex/services/checkout.py` | `cmd/ecom-channel/checkout.go` | The Square-API-before-DB invariant, idempotency key shape, and orphan-recovery sequencing all originate here |
| `solex/services/cart.py` | `internal/ecom/cart` | Cart fingerprinting and line-item snapshot logic |
| `solex/services/catalog.py` + `catalog_sync.py` + `catalog_import.py` | `cmd/ecom-channel/catalog_*.go` | Canary-master conflict resolution, Square push direction, soft-alert pull direction |
| `solex/services/subscriptions.py` | `cmd/ecom-channel/subscriptions.go` | Subscription state machine (active → paused → cancelled), recurring charge cadence, predictive signal feed |
| `solex/services/refunds.py` + `returns.py` | `cmd/ecom-channel/returns_handoff.go` (delegates to `cmd/returns`) | Ecom returns route to the dedicated `returns` service after channel-side normalization |
| `solex/services/abandonment.py` | `cmd/ecom-channel/abandonment.go` | Abandoned-cart recovery — predictive signal, not just a marketing artifact |
| `solex/services/webhooks.py` | `cmd/ecom-channel/webhook.go` | Channel webhook receiver — owns its own signature verification, distinct from the POS webhook-pipeline |
| `solex/services/inventory.py` | RaaS query layer — call `cmd/inventory-as-a-service` | Local inventory views in Solex become IaaS reads in Go |
| `solex/services/fulfillment.py` | `cmd/ecom-channel/fulfillment.go` | Fulfillment state, BOPIS hold semantics, shipping handoff |
| `solex/services/shipping.py` + `tax.py` + `addresses.py` | `internal/ecom/shipping` etc. | Pure-function utilities; straight ports |
| `solex/services/square_client.py` | `internal/ecom/adapters/square` | Becomes the V1 concrete `ChannelAdapter` implementation |
| `solex/services/email.py` | Out of scope for ecom-channel | Email is a platform concern; route through the platform notifier service |
| `solex/services/auth.py` | Out of scope | Customer auth is owned by `identity` |
| `solex/services/scenarios/` | Test fixtures | Becomes integration test data under `cmd/ecom-channel/testdata/` |
| `solex/routes/admin_*.py` (8 admin routes) | `cmd/ops-dashboard` integration | Admin surfaces consolidate into ops-dashboard, not ecom-channel |
| `solex/routes/account_*.py` (5 account routes) | `cmd/ecom-channel/account_*.go` | Storefront-facing account surfaces stay with ecom-channel |
| `solex/routes/storefront.py` + `pages.py` + `cart.py` + `checkout.py` | Storefront templates remain Flask/HTML; Go serves the API | The Go ecom-channel is API-first; the storefront UI does not move into Go |
| `solex/models/*.py` (14 models) | `internal/ecom/models` (sqlc-generated) | SQLAlchemy models map to sqlc structs — schemas in `data-model.md` already reflect this |
| `alembic/versions/` | `deploy/migrations/ecom-channel/` | Convert Alembic to golang-migrate sequentially; do not collapse history |

**Build dispatch posture:** the engineer building `cmd/ecom-channel` reads Solex alongside the SDD — the SDD specifies the contract; Solex illustrates one proven realization. Where Solex got something right that the SDD leaves implicit (failure sequencing, idempotency shape, webhook ordering), port the behavior forward. Where the SDD calls for something Solex did not need (multi-channel adapter, multi-tenant isolation, RaaS chain integration), the SDD governs. No part of this is a literal port; Solex is illustrative.

#### Solex Asset Reuse Beyond ecom-channel

Solex is not a single-purpose source for ecom-channel. Several of its assets carry into other modules in the Canary Go spine — the build dispatches for those modules should treat Solex as illustrative for their domain too.

| Solex asset | Beyond-ecom reuse | Target Go module |
|---|---|---|
| `solex/routes/admin_*.py` (8 admin routes — catalog, customers, inventory, orders, returns, subscriptions, utils, auth) | Merchant-facing operational console; the admin surface area Solex proves out is exactly the surface area `ops-dashboard` is meant to expose. The Flask admin pages illustrate the controls the Go ops-dashboard surfaces over REST + SSE. | `cmd/ops-dashboard` |
| `solex/services/catalog.py` + `catalog_import.py` + `catalog_sync.py` + `catalog/products.yaml` | Catalog ingestion and the YAML product shape inform the item master, not just ecom-channel | `cmd/item` (primary) + `cmd/ecom-channel` (consumer) |
| `solex/services/cart.py` + `checkout.py` | In-store ordering: kiosk flows, associate-assisted ordering, and BOPIS extensions all reuse the same cart-and-checkout invariants. The "online order" abstraction is one shape; an associate ringing up a special order is another shape with the same backing primitives. | `cmd/store-brain` (in-store ordering UX) + `cmd/ecom-channel` (online ordering) |
| `solex/services/inventory.py` | Cart reservation and BOPIS hold semantics inform IaaS regardless of whether the order originates online or in-store | `cmd/inventory-as-a-service` |
| `solex/services/subscriptions.py` | Recurring-charge cadence as a predictive signal — relevant to analytics and to customer-lifecycle agents, not just the online subscription flow | `cmd/ecom-channel` (primary) + `cmd/analytics` (predictive consumer) |
| `solex/templates/*` + `solex/static/*` | The customer-facing storefront UI stays in Flask — Go is API-first. But the visual primitives and Tailwind config inform how the ops-dashboard and merchant admin surfaces look in the Canary platform. | Reference for `cmd/ops-dashboard` UI |

**The catalog-in-store-orders thread:** Solex's catalog + cart + checkout were built for the online channel, but the same primitives power in-store ordering use cases — POS-side special orders, kiosk-assisted ordering, associate-driven order entry, BOPIS extension flows. The store-brain agent uses the same catalog read-path and the same inventory reservation contract; only the UX surface and the channel attribution differ. The build should treat "the catalog" and "the cart" as platform primitives shared across channels, not as ecom-only structures.

**The ops-dashboard / devops-console thread:** the Solex admin route surface is a working example of the merchant-facing operational console that `ops-dashboard` is designed to be. When the ops-dashboard build dispatch fires, those Flask admin pages are the closest illustrative reference — the controls the merchant operator needs over catalog, customers, inventory, orders, returns, and subscriptions are already enumerated and exercised there.

---

## Hat 2 — Technical Specification

### Channel Adapter Interface

All provider-specific logic is encapsulated behind a single Go interface. Every concrete adapter (Square Online, Shopify, WooCommerce) must satisfy this contract. No core business logic in ecom-channel may call provider APIs directly.

```go
// ChannelAdapter abstracts provider-specific ecommerce operations.
// Square Online is the V1 concrete implementation.
// The adapter is initialized from ecom_channels.credentials_ciphertext.
type ChannelAdapter interface {
    // Order operations
    FetchOrder(ctx context.Context, externalOrderID string) (*ChannelOrder, error)

    // Catalog operations
    PushItem(ctx context.Context, item *catalog.Item) (externalItemID string, err error)
    PullCatalog(ctx context.Context) ([]*ChannelProduct, error)

    // Subscription / payment operations
    ChargeCard(ctx context.Context, cardToken string, amountCents int, idempotencyKey string) (paymentID string, err error)

    // Webhook operations
    VerifyWebhookSignature(payload []byte, signature string, url string) bool
}
```

**Adapter registry:** adapters are registered at startup by provider string (`"square_online"`, `"shopify"`, `"woocommerce"`). Channel credentials are decrypted from `ecom_channels.credentials_ciphertext` at adapter instantiation, not at request time.

**Adapter isolation:** a failure in one adapter (e.g., Square API rate limit) must not affect order processing for merchants on a different channel. Each adapter instance is scoped to a `channel_id`.

### Checkout Invariant — Square API Before DB Transaction

This is the critical operational invariant, proven in Solex (`/Users/gclyle/GrowDirect/Solex/solex/services/checkout.py`):

> Square API calls happen **before** the DB transaction. If Square fails, nothing persists locally. If the DB fails after Square succeeds, the webhook orphan-recovery path handles refunds.

The Go implementation enforces this sequencing explicitly:

```
1. Call Square CreatePayment → receive square_payment_id
2. Open pgx transaction
3. INSERT ecom_orders (include square_payment_id in the row)
4. Call raas.AppendEvent(ecom.order.placed)
5. Commit transaction
```

**Failure matrix:**

| Step fails | State | Recovery |
|---|---|---|
| Step 1 (Square CreatePayment) | Nothing persists | Return error to caller; no cleanup needed |
| Step 2 (open transaction) | Square payment exists; no DB row | orphan_recovery job finds unmatched payment after 10 min TTL and issues refund |
| Step 3 (INSERT ecom_orders) | Square payment exists; no DB row | Same as step 2 |
| Step 4 (RaaS AppendEvent) | Square payment exists; DB row committed if step 5 follows — but if step 4 blocks step 5, transaction is rolled back | orphan_recovery handles the Square payment; RaaS event missed but order row absent — no phantom state |
| Step 5 (commit) | Square payment exists; partial local state possible | orphan_recovery compares Square payment list against ecom_orders; issues refund for unmatched payments older than 10 min |

**orphan_recovery job:** scheduled task, runs every 5 minutes. Calls `GET /v2/payments` on Square for each active merchant, compares against `ecom_orders.square_payment_id`. Any Square payment older than 10 minutes with no corresponding `ecom_orders` row triggers a refund via `POST /v2/refunds`. Result logged to `ecom_order_orphan_recovery_log` (not in the primary data model; append-only ops log).

**Idempotency key for Square CreatePayment:** `SHA-256(merchant_id + cart_fingerprint + customer_email + total_cents)`, hex-encoded, truncated to 45 characters (Square's max). This ensures retried checkouts do not double-charge.

### Data Model

Seven tables in the `canary` database, `ecom` schema. All primary keys are UUIDs with `gen_random_uuid()` default. All tables carry `created_at` and `updated_at` timestamps.

---

#### `ecom_channels` — Channel registrations per merchant

| Column | Type | Notes |
|---|---|---|
| `channel_id` | UUID PK | |
| `merchant_id` | UUID NOT NULL | FK → `app.merchants.id` |
| `provider` | TEXT NOT NULL | `"square_online"` \| `"shopify"` \| `"woocommerce"` |
| `external_channel_id` | TEXT NOT NULL | Provider-native channel/shop ID |
| `credentials_ciphertext` | JSONB NOT NULL | AES-256-GCM encrypted API credentials |
| `sync_enabled` | BOOL NOT NULL DEFAULT true | Master switch for catalog sync |
| `webhook_url` | TEXT | Registered webhook endpoint URL for this channel |
| `created_at` | TIMESTAMPTZ NOT NULL | |
| `last_synced_at` | TIMESTAMPTZ | Updated on each successful catalog sync |

Unique: `(merchant_id, provider, external_channel_id)`.

---

#### `ecom_orders` — Ecommerce orders ingested from channel

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `merchant_id` | UUID NOT NULL | FK → `app.merchants.id` |
| `channel_id` | UUID NOT NULL | FK → `ecom_channels.channel_id` |
| `external_order_id` | TEXT NOT NULL | Provider-native order ID |
| `square_payment_id` | TEXT | Populated at checkout; used by orphan_recovery |
| `raas_sequence_num` | BIGINT | The RaaS chain event sequence number for `ecom.order.placed` |
| `status` | TEXT NOT NULL | Canary status: `pending` \| `paid` \| `fulfilled` \| `refunded` \| `cancelled` |
| `channel_status` | TEXT | Raw status string from provider — preserved verbatim |
| `customer_email` | TEXT | PII — encrypted at rest (AES-256-GCM) |
| `customer_name` | TEXT | PII — encrypted at rest |
| `line_items` | JSONB | Snapshot of items at order time |
| `subtotal_cents` | INT NOT NULL | |
| `tax_cents` | INT NOT NULL DEFAULT 0 | |
| `shipping_cents` | INT NOT NULL DEFAULT 0 | |
| `total_cents` | INT NOT NULL | |
| `currency` | TEXT NOT NULL DEFAULT 'USD' | |
| `placed_at` | TIMESTAMPTZ NOT NULL | When the customer placed the order |
| `fulfilled_at` | TIMESTAMPTZ | Set when `ecom.order.fulfilled` event appended |
| `tracking_number` | TEXT | Set at fulfillment; may be null |
| `raw_payload` | JSONB | Verbatim provider payload — classified per event type; redact cardholder data before logging |
| `created_at` | TIMESTAMPTZ NOT NULL | |
| `updated_at` | TIMESTAMPTZ NOT NULL | |

Unique: `(merchant_id, external_order_id)`. Indexes: `(merchant_id, status)`, `(merchant_id, placed_at)`, `(channel_id, status)`.

---

#### `ecom_order_items` — Line items per order

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `order_id` | UUID NOT NULL | FK → `ecom_orders.id` |
| `external_item_id` | TEXT | Provider-native item/variation ID |
| `canary_item_id` | UUID | FK → `app.items.id` — nullable; resolved by catalog sync |
| `sku` | TEXT | |
| `name_snapshot` | TEXT NOT NULL | Item name at order time — not FK to live catalog |
| `qty` | INT NOT NULL | |
| `price_snapshot_cents` | INT NOT NULL | Unit price at order time |
| `line_total_cents` | INT NOT NULL | `qty * price_snapshot_cents` |

---

#### `ecom_subscriptions` — Recurring autoship subscriptions

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `merchant_id` | UUID NOT NULL | FK → `app.merchants.id` |
| `channel_id` | UUID NOT NULL | FK → `ecom_channels.channel_id` |
| `external_subscription_id` | TEXT | Provider-native subscription ID, if applicable |
| `customer_email` | TEXT NOT NULL | PII — encrypted at rest |
| `canary_item_id` | UUID NOT NULL | FK → `app.items.id` — what is being autoshippped |
| `qty` | INT NOT NULL DEFAULT 1 | |
| `cadence_days` | INT NOT NULL | Days between charges (e.g., 30, 60, 90) |
| `card_token_ciphertext` | TEXT NOT NULL | PCI DSS — card-on-file token, AES-256-GCM; key rotated on card update |
| `status` | TEXT NOT NULL | `active` \| `paused` \| `cancelled` |
| `next_charge_at` | TIMESTAMPTZ | Next scheduled charge time; null if paused or cancelled |
| `paused_until` | TIMESTAMPTZ | Set when status=paused with a resume date |
| `created_at` | TIMESTAMPTZ NOT NULL | |
| `updated_at` | TIMESTAMPTZ NOT NULL | |

Index: `(merchant_id, status)`, `(next_charge_at)` partial where `status = 'active'`.

**Status machine:**

```
active ──pause──→ paused ──resume──→ active
active ─────────────────────────────cancel──→ cancelled
paused ──────────────────────────────cancel──→ cancelled
paused (paused_until reached) ──auto-resume──→ active
```

Transitions are enforced at the service layer. Direct DB status updates are not permitted.

---

#### `ecom_subscription_charges` — Charge history per subscription

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `subscription_id` | UUID NOT NULL | FK → `ecom_subscriptions.id` |
| `external_payment_id` | TEXT | Provider payment ID; null if charge not yet attempted |
| `amount_cents` | INT NOT NULL | |
| `status` | TEXT NOT NULL | `pending` \| `succeeded` \| `failed` |
| `attempted_at` | TIMESTAMPTZ NOT NULL | |
| `error` | TEXT | Provider error message on failure; null on success |

Charge failure policy: three consecutive failures with exponential backoff (1 day → 3 days → 7 days) trigger status transition to `paused`. The merchant is notified via the Owl notification surface.

---

#### `ecom_webhook_events` — Deduplicated inbound webhook log

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `channel_id` | UUID NOT NULL | FK → `ecom_channels.channel_id` |
| `external_event_id` | TEXT NOT NULL | Provider-native event ID |
| `event_type` | TEXT NOT NULL | Provider event type string |
| `received_at` | TIMESTAMPTZ NOT NULL | |
| `processed_at` | TIMESTAMPTZ | Set when dispatched to handler |
| `error` | TEXT | Set if handler returned an error |
| `raw_payload` | JSONB | Verbatim payload — classify per event type; redact cardholder data before storage |

Unique: `(channel_id, external_event_id)`. This is the idempotency backstop — duplicate webhook deliveries silently no-op on conflict.

---

#### `ecom_catalog_sync_log` — Audit trail for catalog sync operations

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | |
| `merchant_id` | UUID NOT NULL | |
| `channel_id` | UUID NOT NULL | FK → `ecom_channels.channel_id` |
| `canary_item_id` | UUID NOT NULL | FK → `app.items.id` |
| `direction` | TEXT NOT NULL | `"push"` (Canary → channel) \| `"pull"` (channel → Canary) |
| `status` | TEXT NOT NULL | `succeeded` \| `failed` \| `skipped` |
| `synced_at` | TIMESTAMPTZ NOT NULL | |
| `error` | TEXT | Provider error on failure; null on success |

---

### RaaS Event Schema

ecom-channel appends three event types to the RaaS chain. All use the `ecom` namespace.

| Event type | Trigger | Key fields |
|---|---|---|
| `ecom.order.placed` | Checkout completes — after DB commit | `order_id`, `channel_id`, `merchant_id`, `total_cents`, `item_count`, `customer_email_hash` |
| `ecom.order.fulfilled` | Fulfillment confirmed (tracking number set) | `order_id`, `tracking_number`, `fulfilled_at` |
| `ecom.order.refunded` | Refund processed | `order_id`, `refund_amount_cents`, `refund_reason` |

**`customer_email_hash`** is `HMAC-SHA256(EMAIL_HASH_KEY, normalize(email))` — included in the chain event for cross-channel correlation without embedding raw PII in the immutable chain. Plain SHA-256 is prohibited: the email plaintext domain is enumerable for any specific customer base (an attacker who has a list of candidate emails can hash each one and match against `customer_email_hash`), and the immutable-chain placement makes any post-hoc rehashing impossible. The keyed hash blocks offline brute force without compromising the cross-channel determinism the chain relies on.

**Email normalization (input to HMAC):** lowercase, strip whitespace, no Punycode rewrites. Normalization is required so that `Customer@example.com` and `customer@example.com` collapse to one hash for correlation purposes. Done before HMAC, never after.

**Key rotation in an immutable chain.** `EMAIL_HASH_KEY` rotation produces a new hash for the same email going forward — past chain events are sealed and cannot be re-hashed. Cross-channel correlation across a key-rotation boundary requires a key-version index alongside `customer_email_hash` in the chain payload (`{key_version: 2, hash: <bytes>}`). The `EMAIL_HASH_KEY` rotation procedure must keep all prior key versions available for verification queries — old keys are never deleted, only retired from new-write use. Key class definition lives in `go-security.md` → "PII Hashing Keys".

RaaS sequence numbers are written back to `ecom_orders.raas_sequence_num` after the event is appended. This provides a direct reference from any order row to its position in the evidentiary chain.

---

### API Contract

**Base path:** `/ecom`

#### POST /ecom/orders/checkout

Initiate a checkout. Follows the Square-first invariant: payment is captured before the DB write.

```
Request: {
  channel_id: UUID,
  line_items: [{external_item_id, qty, price_cents, name, sku}],
  customer: {email, name},
  shipping_address: {...},
  billing_address: {...},
  payment_token: string,  // Square nonce or card-on-file token
  idempotency_key: string
}
Response 201: {order_id, status: "paid", total_cents, raas_sequence_num}
Response 402: {error: "payment_declined", decline_code: string}
Response 422: {error: "validation_error", fields: [...]}
Response 503: {error: "provider_unavailable", retry_after_seconds: 30}
```

#### PATCH /ecom/orders/{order_id}/fulfill

Mark an order as fulfilled. Appends `ecom.order.fulfilled` to RaaS.

```
Request: {tracking_number?: string, fulfilled_at?: ISO8601}
Response 200: {order_id, status: "fulfilled", raas_sequence_num}
Response 404: {error: "order_not_found"}
Response 409: {error: "already_fulfilled"}
```

#### POST /ecom/orders/{order_id}/refund

Issue a refund. Calls provider refund API then appends `ecom.order.refunded`.

```
Request: {amount_cents: int, reason: string}
Response 200: {order_id, refund_id, status: "refunded"}
Response 402: {error: "refund_declined"}
Response 409: {error: "already_refunded"}
```

#### POST /ecom/catalog/sync

Trigger a catalog sync (Canary item master → channel). Returns immediately; sync runs async.

```
Request: {channel_id: UUID, item_ids?: [UUID]}  // omit item_ids for full sync
Response 202: {job_id, channel_id, item_count}
```

#### GET /ecom/catalog/sync/{job_id}

Poll sync status.

```
Response 200: {job_id, status: "running"|"completed"|"failed", pushed: N, failed: N, errors: [...]}
```

#### POST /ecom/webhooks/{provider}

Receive inbound channel webhooks. Analogous to `/webhooks/{pos-type}` in webhook-pipeline, but scoped to ecommerce channel providers.

```
Headers: provider-specific signature header (X-Square-Hmacsha256-Signature for Square Online)
Body: provider-native event payload
Response 200: {received: true, event_id: UUID}
Response 200 (duplicate): {received: true, event_id: UUID, duplicate: true}
Response 400: {error: "invalid_signature"}
Response 404: {error: "unknown_provider"}
```

**Critical invariant (identical to webhook-pipeline):** Never return 200 unless the event has been written to `ecom_webhook_events` and dispatched to the appropriate handler (or queued for async dispatch). The deduplication unique constraint on `(channel_id, external_event_id)` is the idempotency backstop.

#### GET /ecom/health

Service health check.

```
Response 200: {status: "healthy", db_connected: true, version: "1.0.0"}
Response 503: {status: "degraded", db_connected: false}
```

---

### Webhook Processing Sequence

Inbound channel webhooks follow a condensed version of the webhook-pipeline 10-step sequence, adapted for ecom channel events:

```
1. Read raw bytes from request body.
2. Resolve channel from {provider} path segment + payload merchant_id.
   → 404 if unknown provider or merchant not onboarded.
3. Verify signature via adapter.VerifyWebhookSignature().
   → 400 if invalid.
4. Extract external_event_id from payload.
5. INSERT ecom_webhook_events ON CONFLICT (channel_id, external_event_id) DO NOTHING.
   If conflict: return 200 with duplicate=true. Stop.
6. Route event_type to handler:
   - order.* → order handler (update ecom_orders, append RaaS event)
   - catalog.* → catalog handler (pull updated item, update Canary item master)
   - payment.* → payment/subscription handler
   - unrecognized types: log and ACK (200), do not error
7. On handler success: UPDATE ecom_webhook_events SET processed_at = now().
8. On handler error: UPDATE ecom_webhook_events SET error = <message>.
   Return 200 — channel providers treat 4xx/5xx as delivery failure and retry.
   Handler errors are an internal concern; the webhook was received.
9. Return 200 {received: true, event_id}.
```

**Separation from webhook-pipeline:** ecom-channel maintains its own webhook receiver rather than routing through the general webhook-pipeline service. The webhook-pipeline is designed for POS adapter events (Square Point of Sale, NCR Counterpoint) and the TSP/Stage processing chain. ecom channel events have different shape, different handler semantics (no Merkle batching, no CRDM tables), and should not share the POS event stream. The two receivers operate independently on different paths and different event streams.

---

### Catalog Conflict Resolution

Canary is the master. Square Online is the downstream.

| Scenario | Resolution |
|---|---|
| Canary item price changes | Push to Square catalog (PushItem). Sync log records direction=push. |
| Square catalog price changes (detected via pull webhook or periodic pull) | Create `ecom_catalog_sync_log` record with direction=pull, status=skipped. Emit alert to Owl. No Canary record updated. |
| Square catalog item not found in Canary (new item created directly in Square) | Pull webhook triggers: insert to `ecom_catalog_sync_log` direction=pull, status=failed. Alert to merchant. Not auto-imported. |
| Canary item has no `external_item_id` mapping | PushItem creates the Square catalog object. `ecom_order_items.canary_item_id` is populated retroactively by the catalog sync pass. |

---

### MCP Tools — `canary-ecom`

| Tool | Input | Output | Notes |
|---|---|---|---|
| `get_ecom_orders` | `merchant_id`, `status?`, `date_from?`, `date_to?` | `[{order}]` | Query `ecom_orders` with filters. Max 200 rows per call. |
| `get_order_detail` | `order_id` | `{order, items[], channel_status}` | Full order with line items resolved. |
| `sync_catalog` | `merchant_id`, `channel_id` | `{pushed: N, failed: N}` | Synchronous for ≤50 items; async job reference for larger sets. |
| `get_subscription` | `subscription_id` | `{subscription, charges[]}` | Full subscription state with charge history. |
| `pause_subscription` | `subscription_id`, `until?` | `{subscription}` | Sets status=paused; clears `next_charge_at`. |
| `resume_subscription` | `subscription_id` | `{subscription}` | Clears `paused_until`; schedules next charge. |
| `get_channel_health` | `channel_id` | `{last_sync, pending_orders, webhook_lag_ms}` | Channel connectivity and backlog status. |

**Tool authorization:** all 7 tools require JWT authentication scoped to the `merchant_id` of the resource being accessed. Cross-merchant access returns 403.

---

### Dependencies

| Dependency | Required | Purpose |
|---|:---:|---|
| PostgreSQL 17 (`canary` DB, `ecom` schema) | Yes | All ecom_ tables |
| RaaS service | Yes | Namespace resolution on merchant lookup; event append for order lifecycle |
| Identity service | Yes | Merchant credential and onboarding status lookup |
| Valkey (DB 0) | Yes | Session backend; idempotency key cache for checkout |
| Square API | Yes (V1 adapter) | Payment capture, order fetch, catalog push/pull |
| Owl (notification surface) | Soft | Subscription charge failure notifications; catalog conflict alerts |
| Fox/Hawk | Soft | Return fraud signal relay; ecom orders feed Hawk's return matching |

---

### Configuration

| Variable | Required | Default | Purpose |
|---|:---:|---|---|
| `ECOM_SQUARE_APPLICATION_ID` | Yes | — | Square application ID for OAuth |
| `ECOM_SQUARE_ENVIRONMENT` | No | `sandbox` | `sandbox` \| `production` |
| `ECOM_ENCRYPTION_KEY` | Yes (prod) | — | AES-256-GCM master key (base64-encoded 32 bytes) — for credentials_ciphertext, customer PII, and card tokens |
| `ECOM_WEBHOOK_URL_BASE` | Yes | — | Base URL for channel webhook registration (e.g., `https://canary.example.com/ecom/webhooks`) |
| `ECOM_ORPHAN_RECOVERY_INTERVAL_SECONDS` | No | `300` | Frequency of orphan_recovery job |
| `ECOM_ORPHAN_RECOVERY_TTL_MINUTES` | No | `10` | Age at which an unmatched Square payment triggers a refund |
| `ECOM_CATALOG_SYNC_BATCH_SIZE` | No | `50` | Items per sync batch before switching to async |
| `RAAS_BASE_URL` | Yes | — | RaaS service endpoint |
| `IDENTITY_BASE_URL` | Yes | — | Identity service endpoint |
| `DATABASE_URL` | Yes | — | PostgreSQL DSN |
| `VALKEY_URL` | Yes | — | Valkey connection URL |

---

## Hat 3 — Operations

### Startup Sequence

1. Validate required environment variables. Log and exit if any `Yes`-required variable is absent.
2. Initialize pgx connection pool. Run `SELECT 1` health check. Exit on failure.
3. Register channel adapter implementations (Square Online) in the adapter registry.
4. Start orphan_recovery goroutine on `ECOM_ORPHAN_RECOVERY_INTERVAL_SECONDS` ticker.
5. Start subscription charge scheduler goroutine — polls `ecom_subscriptions` where `status = 'active' AND next_charge_at <= now()` on a 60-second tick.
6. Start HTTP server on port 9080. Serve `/ecom/health` before all other routes initialize.

### Health Checks

| Endpoint | Interval | Healthy Condition |
|---|---|---|
| `GET /ecom/health` | 15s | DB connection reachable; adapter registry populated |
| `GET /ecom/health` | 15s (readiness variant) | All required env vars present; DB connected |

### Failure Modes

| Failure | Impact | Recovery |
|---|---|---|
| Square API unavailable (checkout) | Checkouts fail with 503 | Return `retry_after_seconds`; Square-first invariant means no phantom DB rows |
| Square API unavailable (orphan_recovery) | Recovery job skips the cycle | Next cycle will catch up; no data loss — Square holds the payment |
| DB write fails after Square payment | Orphan payment created | orphan_recovery issues refund within `ECOM_ORPHAN_RECOVERY_TTL_MINUTES` |
| RaaS unavailable (event append) | Order committed locally; RaaS event missed | `raas_sequence_num` left null; reprocessing job (future) replays unlinked orders |
| Subscription charge fails | Charge failure recorded; retry scheduled | Three consecutive failures → status=paused; merchant alerted |
| Catalog sync partial failure | Sync log records failed items | Admin can trigger resync for specific item_ids |
| Inbound webhook duplicate | Deduplicated silently on `ecom_webhook_events` unique constraint | No impact |
| Provider API rate limit | Catalog sync and order fetch throttled | Retry with exponential backoff; sync jobs respect provider rate limits |

### Monitoring

| Metric | Normal | Alert Threshold |
|---|---|---|
| Checkout response time (p95) | < 500ms | > 2000ms (Square API latency included) |
| Checkout 402 (payment declined) rate | < 5% | > 15% in 1 hour |
| Checkout 503 (provider unavailable) rate | 0 | > 3 in 5 minutes |
| orphan_recovery payments found | 0 | > 5 in 1 hour |
| Subscription charge failure rate | < 2% | > 10% in 1 day |
| Catalog sync failure rate | < 1% | > 10% per sync job |
| Inbound webhook processing lag | < 5s | > 30s |
| `ecom_webhook_events` unprocessed (error IS NOT NULL) | 0 | > 20 |

### Deployment Target

- **Service:** single binary, horizontally scalable. No shared in-process state — all coordination through PostgreSQL and Valkey.
- **Orphan recovery and subscription scheduler:** goroutines within the service binary. On multi-replica deployments, use advisory lock (`pg_try_advisory_lock`) per job run to prevent duplicate execution.
- **PostgreSQL:** same `canary` database, `ecom` schema. RDS PostgreSQL 17 with Multi-AZ in production.
- **Secrets:** `ECOM_ENCRYPTION_KEY`, `ECOM_SQUARE_APPLICATION_ID`, `DATABASE_URL`, `VALKEY_URL` sourced from secrets manager at startup. Never from files on disk.

---

## Hat 4 — Compliance

### PII Classification and Encryption

| Field | Table | Classification | Treatment |
|---|---|---|---|
| `customer_email` | `ecom_orders`, `ecom_subscriptions` | **Sensitive PII** | AES-256-GCM encrypted at rest; key from `ECOM_ENCRYPTION_KEY` |
| `customer_name` | `ecom_orders` | **Sensitive PII** | AES-256-GCM encrypted at rest |
| `card_token_ciphertext` | `ecom_subscriptions` | **PCI DSS** | AES-256-GCM encrypted; key rotated on card update via key-versioning envelope |
| `credentials_ciphertext` | `ecom_channels` | **Sensitive** | AES-256-GCM encrypted; contains OAuth tokens and API keys |
| `raw_payload` | `ecom_webhook_events`, `ecom_orders` | **Restricted — classify per event type** | Some event types (payment.*) may contain cardholder data; redact BIN, last4, expiry before storage; hash card fingerprints |
| `customer_email_hash` | RaaS chain events | **Internal** | `HMAC-SHA256(EMAIL_HASH_KEY, normalize(email))` (keyed) — included in chain for correlation without raw PII in immutable record. Plain SHA-256 prohibited (enumerable domain). Chain payload carries `{key_version, hash}` so rotation does not break historical verification. See `go-security.md` → "PII Hashing Keys". |

### PCI DSS Scope

`ecom_subscriptions.card_token_ciphertext` is the primary PCI DSS scoping concern. The token is a Square card-on-file reference, not a raw PAN — Square is the card vault. Canary's scope is limited to the encrypted token storage.

**Key rotation on card update:** When a customer updates their card, the new card token from Square replaces `card_token_ciphertext`. The old token is overwritten; no historical card token is retained. Key versioning in the AES-256-GCM envelope supports rotation of the master key without re-encrypting all rows in a single atomic operation.

**PCI controls:**
- `card_token_ciphertext` is never logged, never included in API responses, never written to `raw_payload` columns.
- Access to `ecom_subscriptions` requires merchant-scoped JWT. No service account has unscoped access to the subscriptions table.
- Audit log of every card charge is maintained in `ecom_subscription_charges`.

### GDPR — Right to Erasure

Customer PII exists in `ecom_orders.customer_email` and `ecom_orders.customer_name`, and in `ecom_subscriptions.customer_email`.

**Erasure pattern — cryptographic erasure:**

The RaaS chain event for the order is immutable and cannot be deleted. The order row itself remains for business and LP analytics. The erasure is applied to the PII columns:

```sql
UPDATE ecom_orders
SET customer_email = NULL,
    customer_name  = NULL,
    updated_at     = now()
WHERE merchant_id = $1
  AND customer_email_hash = $2;  -- hash used to locate without decrypting

UPDATE ecom_subscriptions
SET customer_email       = NULL,
    card_token_ciphertext = NULL,
    status               = 'cancelled',
    updated_at            = now()
WHERE merchant_id = $1
  AND customer_email_hash = $2;
```

The encryption key for the erased rows is destroyed (key-versioning envelope: the per-row DEK is overwritten with random bytes). The ciphertext that remains in the column is cryptographically irrecoverable — this is the erasure. The `customer_email_hash` in chain events is a one-way hash and does not constitute personal data under GDPR.

Erasure requests are recorded in `app.gdpr_erasure_log` (common table owned by the identity service), not in ecom-specific tables.

### Audit and Retention

| Data | Retention | Authority |
|---|---|---|
| `ecom_orders` | 7 years (financial records) | Tax compliance |
| `ecom_order_items` | 7 years | Same |
| `ecom_subscription_charges` | 7 years | Same |
| `ecom_subscriptions` | Until cancellation + 3 years | Business and legal |
| `ecom_webhook_events` | 90 days | Ops debugging |
| `ecom_catalog_sync_log` | 12 months | Audit trail |
| Customer PII in orders/subscriptions | Until GDPR erasure request or 7-year retention window | Whichever is earlier |

### SOX / Evidence Chain

`ecom_orders.raas_sequence_num` links every ecommerce order to a position in the RaaS evidentiary chain, which itself is Merkle-batched and Bitcoin-inscribed by the webhook-pipeline's Stage 3 process. This means ecom order events participate in the same tamper-evident record as POS transactions — a material fact for any financial audit that spans channels.

No `ecom` schema table is write-once (ecom orders are updated as they fulfill and refund). The immutable evidentiary record for an order is the RaaS chain event, not the `ecom_orders` row. The `raas_sequence_num` reference is the binding.

---

### Production Readiness Checklist

- [ ] PII encrypted at rest (`customer_email`, `customer_name`, `card_token_ciphertext`, `credentials_ciphertext`)
- [ ] `raw_payload` redaction for cardholder data fields (BIN, last4, expiry) before storage
- [ ] Secrets sourced from secrets manager at startup (never from disk)
- [ ] Orphan recovery job running; advisory lock prevents duplicate execution on multi-replica deployments
- [ ] Subscription charge scheduler running with advisory lock
- [ ] Key rotation procedure documented and tested for `ECOM_ENCRYPTION_KEY`
- [ ] PCI audit trail: `ecom_subscription_charges` complete for all charge attempts
- [ ] GDPR erasure path tested: PII nulled, DEK destroyed, `gdpr_erasure_log` written
- [ ] Channel adapter signature verification tested for Square Online (timing-safe comparison)
- [ ] Checkout idempotency key tested: retry with same key does not double-charge
- [ ] Health check returning correct status before serving traffic
- [ ] `ecom_webhook_events` unique constraint tested: duplicate delivery returns 200 with `duplicate: true`
- [ ] Catalog conflict alert path tested: Square-only price change generates Owl alert
- [ ] `raas_sequence_num` back-fill verified: order row updated after RaaS append

---

*Canary | GrowDirect LLC | Confidential*
