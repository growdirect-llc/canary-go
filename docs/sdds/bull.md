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

# Bull — NCR Counterpoint Adapter (Reference Implementation of the POS Adapter Substrate) — Distribution Intelligence Layer

**Implements:** pos-adapter-substrate.
**Feeds:** TSP (polled CRDM, no native webhooks).

> **Role in the system:** Bull is Canary's native intelligence layer for Module D (Distribution). It implements the POS Adapter Substrate contract for NCR Counterpoint — a poll-only adapter using REST API key authentication — and adds the analysis layer that Counterpoint does not provide: transfer-loss reconciliation (D.4) and multi-store distribution recommendations (D.5).

**Multi-tenant context.** Bull operates per-tenant — every poll cycle, credential rotation, and watermark advance is scoped to a single merchant. Bull tables (`bull_api_credentials`, `bull_poll_watermarks`, `bull_merchant_config`, `bull_event_log`) live per-tenant in `tenant_{merchant_id}`. Cross-tenant Counterpoint observability (platform-wide adapter health) flows through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Bull operates with all Optional Features (per `platform-overview.md`) disabled. The poll loop, CRDM normalization, and event emission run entirely on internal records. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, Bull-emitted events flow into the standard receipt chain and are eligible for asynchronous public anchoring per `blockchain-anchor.md`. When `ILDWAC_ENABLED=true`, Bull-emitted receipt events populate the five-dimension provenance (Counterpoint as the Port dimension).

**Status:** Phase 3 — design complete. Implementation is **gated on Module D substrate prerequisites** (see Phase Gating). Schema is a stub; no migration exists yet.

---

## Architecture Position

```
Counterpoint REST API
        │
        │  HTTP polling (API key auth)
        ▼
CounterpointAdapter.Poll()      ← implements POSAdapter interface
        │
        │  PollResult → []CanonicalEvent
        ▼
canary:events (Valkey stream)
        │
        ├─ Sub1 (seal)
        ├─ Sub2 (parse) → XFER documents → Module D subscriber
        └─ Sub4 (detect) → Chirp Q-IS-03 (transfer-loss accumulation)
                                    ▲
                              Bull D.4 output
```

From Sub1 onward, no downstream component knows events came from Counterpoint. Bull's analysis layer consumes the parsed XFER data after the TSP pipeline has processed it.

---

## Counterpoint Adapter — Authentication Contract

NCR Counterpoint uses REST API key authentication. There is no OAuth flow, no token expiry, and no refresh token. Credentials are static until the operator rotates them.

### Credential Shape

Stored encrypted in `pos_tenant_credentials.credentials_enc` (see POS Adapter Substrate SDD for storage schema):

```json
{
  "host":          "https://pos.retailerstore.com:81",
  "username":      "MGMT",
  "password":      "...",
  "api_key":       "...",
  "company_alias": "MAINCO",
  "verify_ssl":    true
}
```

### HTTP Client Contract

Every outbound Counterpoint API request must include:

```
Authorization: Basic base64(username:password)
APIKey: <api_key>
Content-Type: application/json
Accept: application/json
```

`verify_ssl`: when `false`, the client skips TLS certificate verification. Permitted for on-premise installations with self-signed certificates. Log a warning at startup when SSL verification is disabled.

### Connection Test Contract

```
GET {host}/api/session
Headers: Authorization, APIKey

Expected: HTTP 200 with session object
Failure: Return typed error with user-visible message including host and reason.
         Examples:
           "Unable to connect to {host}: connection refused"
           "Authentication failed for user {username} on {host}"
           "SSL certificate verification failed for {host} — set verify_ssl=false for self-signed certificates"
```

### Multi-Company Support

A single merchant may operate multiple Counterpoint "companies" (separate database instances on the same server). Each company is identified by its `company_alias`. Each company gets its own row in `pos_tenant_credentials` and its own poll watermarks.

The adapter receives `company_alias` on every `Poll()` call and includes it in API requests:

```
GET {host}/api/Company/{company_alias}/Transaction?...
```

---

## Counterpoint Adapter — Webhook Contract

**Counterpoint has no native webhook system.** The adapter's `WebhookEventTypes()` returns an empty set. All ingestion is poll-based. There is no webhook ingress path for Counterpoint events.

---

## Counterpoint Adapter — Poll Surface

### Entity Types and Poll Intervals

| Entity Type | Poll Interval | API Endpoint Pattern | Notes |
|---|---|---|---|
| `transaction` | 1 minute | `/api/Company/{alias}/Transaction` | Primary ingestion path |
| `customer` | 1 hour | `/api/Company/{alias}/Customer` | Reference data |
| `item` | 24 hours | `/api/Company/{alias}/InventoryItem` | Catalog |
| `item_categories` | 24 hours | `/api/Company/{alias}/Category` | Catalog |
| `store` | 24 hours | `/api/Company/{alias}/Store` | Location reference |
| `xfer_document` | 5 minutes | `/api/Company/{alias}/Document?DOC_TYP=XFER` | Transfer documents |
| `inventory_snapshot` | 4 hours | `/api/Company/{alias}/InventoryLocation` | SOH per location |

### Pagination Contract

Counterpoint REST API uses `$top` and `$skip` for pagination:

```
GET {host}/api/Company/{alias}/Transaction
  ?$filter=TKT_DT ge datetime'{since}'
  &$top=500
  &$skip={offset}
  &$orderby=TKT_DT asc

Response:
{
  "@odata.count": 1247,
  "value": [ ... up to 500 items ... ]
}
```

Poll until `len(value) < $top` (last page). Advance watermark to the maximum `TKT_DT` value in the returned results. If no results, watermark is unchanged (`new_watermark = nil`).

### Idempotency

`Poll()` must be idempotent. The Counterpoint REST API supports `$filter=TKT_DT ge datetime'{since}'`. Two calls with the same `since` return the same results (or a superset if new records appeared). The adapter must not de-duplicate — that responsibility belongs to Sub1 (seal) via the hash chain.

### Rate Limit Handling

Counterpoint on-premise installations have no documented rate limit. Hosted Counterpoint (NCR Cloud) may impose limits. Adapter behavior on HTTP 429:

1. Parse `Retry-After` header if present; sleep for that duration.
2. If no `Retry-After`: exponential backoff — 2s, 4s, 8s, up to 60s cap.
3. Return partial PollResult with events collected before the rate limit was hit.
4. Do NOT advance watermark past the last successfully fetched page.

---

## Counterpoint Adapter — Reference Data Seeding

`SeedData()` yields Fixture batches for a freshly connected tenant in activation order:

**Activation ordering (must be respected — reference data before transactions):**

| Order | Entity Type | Reason |
|---|---|---|
| 1 | `store` | Location IDs required by all other entities |
| 2 | `item_categories` | Category IDs required by items |
| 3 | `item` | Item IDs required by transactions |
| 4 | `customer` | Customer IDs required by transactions |
| 5 | `inventory_snapshot` | Initial SOH baseline for Bull D.4 |

Transactions are not seeded — they flow through the poll loop.

---

## [ARCHITECTURAL DIRECTION — not yet implemented] ILDWAC Dimension Mapping — Counterpoint Payload Fields

Bull (Counterpoint adapter) is responsible for populating the `pos_port` and `device_id` envelope fields defined in the POS Adapter Substrate SDD. These are the Port and Device dimensions of the IL(Device/MCP/Port/)WAC cost model.

### Counterpoint → ILDWAC Field Map

| ILDWAC Dimension | Envelope Field | Counterpoint Source Field | Notes |
|---|---|---|---|
| Port | `pos_port` | — (hardcoded) | Always `"counterpoint"` for all events from this adapter |
| Device | `device_id` | `WS_ID` (workstation identifier) | Counterpoint workstation/terminal ID; present on transaction and drawer events |

### Counterpoint Payload Field Details

| Entity Type | Device Field | Notes |
|---|---|---|
| `transaction` | `WS_ID` | Counterpoint workstation (register) identifier; set as `device_id` |
| `xfer_document` | `WS_ID` (transfer initiation workstation) | Set if present; null if document was created without a workstation context |
| `inventory_snapshot` | — | No device context — set `device_id = null` |
| `customer`, `item`, `store`, `item_categories` | — | Reference data; no device context — set `device_id = null` |

**Extraction rule:** Counterpoint `WS_ID` is a string workstation code (e.g., `"REG01"`, `"MGR"`). If absent or empty, set `device_id = null`.

### Poll-Based Device Attribution

Because Counterpoint is poll-only (no native webhooks), every event in a polled batch carries the workstation that originated the transaction in Counterpoint — not the device that called the Canary API. This is correct behavior: `device_id` tracks the POS hardware endpoint, not the polling agent. The polling agent dimension is captured separately via the MCP tool call that authorized the poll action.

### Why This Matters

A transaction processed on workstation `REG01` at store `STR001` through the Counterpoint connector produces a different ILDWAC provenance signature than the same transaction processed via Square. The cost model records the Port (`"counterpoint"`) and Device (`"REG01"`) independently, enabling cross-channel cost attribution at the workstation level. See `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md`.

---

## Transaction Normalization — CRDM Mapping

How Counterpoint-native fields map to the Canonical Retail Data Model:

| CRDM Field | Counterpoint Source Field | Notes |
|---|---|---|
| `external_id` | `TKT_NO` | Transaction ticket number |
| `merchant_id` | (from credential tenant_id) | Injected by adapter |
| `source_merchant_id` | `company_alias` | Counterpoint company identifier |
| `location_id` | `STR_ID` | Store ID |
| `amount_cents` | `TOT_AMT * 100` | Counterpoint stores decimal dollars; convert to integer cents |
| `currency` | `CURR_COD` or "USD" | Default USD if not present |
| `occurred_at` | `TKT_DT` | POS-reported transaction timestamp |
| `transaction_type` | `TKT_TYP` | Map: `S`→`sale`, `R`→`refund`, `V`→`void` |
| `tender_type` | `TND_COD` | Map codes to: `card`, `cash`, `other`, `split` |
| `card_last4` | `CC_LAST4` | Nil if cash |
| `employee_id` | `DR_CLK_ID` | Drawer clerk ID |
| `employee_name` | `DR_CLK_NAM` | Clerk display name |
| `item_count` | `LIN_CNT` | Number of line items |
| `discount_amount_cents` | `TOT_DISC * 100` | Total discount; convert to cents |
| `parent_transaction_id` | `ORIG_TKT_NO` | For refunds/returns |

**Monetary conversion invariant:** All `*_AMT` and `*_DISC` fields from Counterpoint are decimal dollars. Multiply by 100 and round to integer before storing in CRDM cents fields.

---

## Bull — Distribution Intelligence (Module D Native Layer)

Bull is to Module D what Chirp is to Module Q — the Canary-native analysis engine built on substrate that Counterpoint provides but does not analyze.

**Bull does not build Counterpoint's transaction ingestion.** That is the Counterpoint adapter above. Bull is the layer that runs after XFER documents have been ingested and parsed — it reconciles transfer losses and generates rebalancing recommendations.

---

## Phase Gating

Bull is **not buildable** until all of the following are operational:

| Prerequisite | Status |
|---|---|
| T.1 Counterpoint adapter ingress (poll-based) — the adapter above | Phase 1 — in progress |
| T.3.2 DOC_TYP type-routing (XFER → Module D subscriber) | Phase 1 — in progress |
| T.4.6 Transfer event publication to Module D | Phase 1 — in progress |
| D.1 Inventory snapshot ingestion | Phase 2 — planned |
| D.2 Per-location item attribution | Phase 2 — planned |
| D.3 Transfer detection (XFER Documents) | Phase 2 — planned |

**Critical dependency chain:** Counterpoint adapter → T.3.2 XFER routing → T.4.6 event publication → D.3 transfer detection → Bull (D.4 + D.5).

---

## Expected Interfaces

### D.4 — Transfer-Loss Reconciliation

| Direction | Description |
|---|---|
| **Input:** D.3 XFER-RECVR document pairs | Matched transfer-initiation and receiving documents |
| **Input:** D.1 inventory snapshots (pre/post transfer) | SOH deltas around transfer events |
| **Output:** TRANSFER-VARIANCE records | Per (XFER, item) variance with match-confidence flag |
| **Output:** Systematic loss patterns | Per-route aggregated variance trends |
| **Output:** Q-IS-03 accumulation feed | Transfer-loss detections routed to Chirp's Q-IS-03 rule |
| **Output:** UNATTRIBUTED-MOVEMENT events | SOH deltas not explained by any Document |

**Garden-center operating context (informs variance allow-list):**
- End-of-season consolidation produces large transfers with expected live-goods spoilage. D.4.6 allow-list prevents these from flooding Q-IS-03.
- Some L&G operators move stock between locations without creating Counterpoint XFER Documents. These appear as UNATTRIBUTED-MOVEMENT events — a high-value signal, not an edge case.
- Plants that die in transit are a cost, not theft. Bull must distinguish spoilage-in-transit from pilferage-in-transit using seasonal baselines.

### D.5 — Multi-Store Distribution Recommendations

| Direction | Description |
|---|---|
| **Input:** D.1 per-location SOH | Current stock position per (item, location) |
| **Input:** O.2 ROP + safety stock targets | Demand-derived reorder points |
| **Output:** Excess/deficit matching | Location pairs where transfer is cheaper than new PO |
| **Output:** Transfer recommendations with OTB context | Scored by transfer-cost vs replenishment-cost |
| **Output:** Buyer review queue | Same approval UX pattern as O.4 PO recommendations |

---

## Schema (Stub — not yet in migration)

Bull tables will live in the `app` schema alongside Hawk. Schema will be added in a dedicated migration when Phase 3 development begins.

### `bull_transfer_variances`

Per-XFER-item variance records.

```sql
-- STUB — final column set subject to change during Phase 3 design
CREATE TABLE app.bull_transfer_variances (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID         NOT NULL REFERENCES app.merchants(id),
    xfer_document_id    TEXT         NOT NULL,  -- Counterpoint XFER document number
    recvr_document_id   TEXT,                   -- Matching RECVR document; NULL if unmatched
    item_id             TEXT         NOT NULL,  -- Counterpoint item number
    from_location_id    TEXT         NOT NULL,
    to_location_id      TEXT         NOT NULL,
    shipped_qty         NUMERIC      NOT NULL,
    received_qty        NUMERIC,                -- NULL if RECVR not yet received
    variance_qty        NUMERIC,                -- shipped - received; NULL if unresolved
    match_confidence    FLOAT,                  -- 0.0-1.0; NULL if unmatched
    variance_class      TEXT,
    -- 'within_tolerance' | 'spoilage' | 'pilferage' | 'unattributed' | 'pending'
    allow_listed        BOOLEAN      NOT NULL DEFAULT false,
    -- true = end-of-season consolidation allow-list; suppress Q-IS-03 feed
    transferred_at      TIMESTAMPTZ,
    received_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_bull_variances_merchant    ON app.bull_transfer_variances (merchant_id);
CREATE INDEX idx_bull_variances_route       ON app.bull_transfer_variances (from_location_id, to_location_id);
CREATE INDEX idx_bull_variances_item        ON app.bull_transfer_variances (merchant_id, item_id);
```

### `bull_unattributed_movements`

SOH deltas not explained by any Document.

```sql
-- STUB
CREATE TABLE app.bull_unattributed_movements (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID         NOT NULL REFERENCES app.merchants(id),
    location_id     TEXT         NOT NULL,
    item_id         TEXT         NOT NULL,
    delta_qty       NUMERIC      NOT NULL,
    observed_at     TIMESTAMPTZ  NOT NULL,
    snapshot_before NUMERIC,
    snapshot_after  NUMERIC,
    resolution      TEXT,
    -- NULL | 'documented_post_hoc' | 'spoilage' | 'investigation_opened'
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_bull_unattributed_merchant ON app.bull_unattributed_movements (merchant_id, observed_at DESC);
CREATE INDEX idx_bull_unattributed_location ON app.bull_unattributed_movements (location_id, item_id);
```

### `bull_distribution_recommendations`

Rebalancing suggestions with scoring.

```sql
-- STUB
CREATE TABLE app.bull_distribution_recommendations (
    id                      UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID         NOT NULL REFERENCES app.merchants(id),
    from_location_id        TEXT         NOT NULL,  -- excess location
    to_location_id          TEXT         NOT NULL,  -- deficit location
    item_id                 TEXT         NOT NULL,
    recommended_qty         NUMERIC      NOT NULL,
    transfer_cost_cents     BIGINT,                 -- configured per route
    replenishment_cost_cents BIGINT,                -- cost of new PO equivalent
    net_savings_cents       BIGINT,                 -- replenishment - transfer
    status                  TEXT         NOT NULL DEFAULT 'pending',
    -- 'pending' | 'accepted' | 'modified' | 'rejected' | 'expired'
    buyer_id                UUID         REFERENCES app.users(id),
    reviewed_at             TIMESTAMPTZ,
    accepted_qty            NUMERIC,                -- may differ from recommended
    notes                   TEXT,
    expires_at              TIMESTAMPTZ,
    created_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_bull_recs_merchant ON app.bull_distribution_recommendations (merchant_id, status);
CREATE INDEX idx_bull_recs_pending  ON app.bull_distribution_recommendations (merchant_id)
    WHERE status = 'pending';
```

### `bull_transfer_costs`

Per-route configurable transfer costs (Counterpoint does not store these).

```sql
-- STUB
CREATE TABLE app.bull_transfer_costs (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID         NOT NULL REFERENCES app.merchants(id),
    from_location_id  TEXT         NOT NULL,
    to_location_id    TEXT         NOT NULL,
    cost_cents        BIGINT       NOT NULL,  -- flat cost per transfer event
    cost_per_unit_cents BIGINT,               -- optional per-unit component
    effective_from    DATE         NOT NULL DEFAULT CURRENT_DATE,
    effective_until   DATE,                   -- NULL = indefinite
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),

    UNIQUE (merchant_id, from_location_id, to_location_id, effective_from)
);
```

---

## MCP Tools (Planned — `canary-bull` server)

Not implemented until Phase 3.

| Tool | Description |
|---|---|
| `get_transfer_variance` | Per-XFER variance detail with match confidence and variance classification |
| `list_transfer_losses` | Aggregated loss by route, item, or time window |
| `get_distribution_recommendations` | Excess/deficit matches ranked by net savings |
| `approve_transfer_recommendation` | Buyer accepts/modifies/rejects a rebalancing suggestion |
| `get_unattributed_movements` | SOH deltas not explained by known Documents |

---

## Cross-Module Contracts

| Contract ID | Consumer | What Bull promises |
|---|---|---|
| D.6.3 TRANSFER-VARIANCE | Module Q (Q-IS-03), Module F (cost reconciliation) | Variance per XFER-RECVR pair with match-confidence flag |
| D.6.4 In-transit hold | Module O (on-order), Module M (OTB) | In-transit stock is NOT counted as available-for-sale |
| D.6.5 UNATTRIBUTED-MOVEMENT | Module Q, Module F | Every unexplained SOH delta is named, not dropped |
| D.6.9 In-transit timeout | Operations | 72-hour (configurable) timeout alert on unconfirmed transfers |

---

## Analytics Integration (Phase 3)

When Bull tables exist (migration detection, not configuration flag), the Analytics period aggregation pipeline adds Bull-specific rollup jobs:

| Metric | Source | Aggregation | Frequency |
|---|---|---|---|
| Transfer-loss rate by route | `bull_transfer_variances` | Per (from_location, to_location), rolling 90d | Daily |
| Unattributed inventory movements | `bull_unattributed_movements` | Per (location, item) | Daily |
| Recommendation acceptance rate | `bull_distribution_recommendations` | Per merchant, per buyer | Weekly |
| Transfer cost savings | `bull_distribution_recommendations` | Per accepted recommendation | On event |

These metrics are gated on D.3 (transfer detection) being operational. Until then, the rollup jobs return empty results.

---

## Open Assumptions

| ID | Assumption | Resolution Path |
|---|---|---|
| ASSUMPTION-D-03 | Transfer completion confirmed via RECVR Document | Sandbox workflow test |
| ASSUMPTION-D-04 | `DOC_TYP=XFER` is the correct code for store-to-store transfers | Sandbox workflow test |
| ASSUMPTION-D-06 | Transfer cost per route is not stored in Counterpoint — must be configured in `bull_transfer_costs` | Customer interview |
| ASSUMPTION-D-09 | Undocumented transfers are a known L&G operator practice | Customer interview |

---

## Production Readiness Checklist

- [x] Design complete — D.4 and D.5 L2/L3 processes defined
- [x] Cross-module contracts documented — D.6.3–D.6.9
- [x] Counterpoint adapter auth contract specified — REST API key, no OAuth
- [x] Counterpoint poll surface specified — 7 entity types with intervals
- [x] CRDM mapping specified — Counterpoint → canonical fields
- [x] Assumption markers identified — 4 blocking, all resolution-pathed
- [ ] Schema migration — gated on Phase 3 start
- [ ] Counterpoint adapter implementation — Poll(), ParseWebhook() (empty), SeedData()
- [ ] Module D service layer (D.1, D.2, D.3) — gated on Phase 2
- [ ] Bull service layer (D.4, D.5) — gated on Phase 3
- [ ] MCP tools — gated on Phase 3
- [ ] Analytics rollup jobs — gated on bull tables existing
- [ ] Contract tests against T.4.6 XFER routing — gated on T adapter shipping
