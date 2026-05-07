---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: inventory
port: 9081
mcp-server: canary-inventory
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# IaaS — Inventory as a Service

**Type:** Infrastructure Service — Real-Time Inventory Position Engine  
**Binary:** `cmd/inventory` → `:9081`  
**MCP server:** `canary-inventory` (10 tools)  
**Depends on:** `identity` (merchant/location existence), `raas` (appends inventory events to chain)  
**Feeds:** `ecom-channel` (available_online position), `tsp` (transaction-time inventory decrement), `receiving` (receipt events), `fox` (fulfillment routing), `owl` (searchable inventory), `bull` (replenishment triggers)

IaaS is the shared inventory position engine for the Canary platform. It is not a module embedded in ecom-channel or store ops — it is infrastructure. Every service that needs to know "how many units are available, where, and for which channel" calls this service. The governing insight: a physical store location is simultaneously a retail store and a virtual warehouse. The same units on the backroom shelf serve walk-in customers, BOPIS holds, and online shoppers. Without a unified real-time position service, those channels compete against each other on stale counts and oversell becomes inevitable.

**Multi-tenant context.** IaaS tables (`inventory_positions`, `inventory_ledger`, `cart_reservations`, `bopis_holds`) live per-tenant in `tenant_{merchant_id}`. Every position read and write is scoped to a single merchant via `SET search_path`. Cross-tenant inventory analytics (platform-wide fill rate benchmarks, category turnover) flow through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** IaaS operates with all Optional Features (per `platform-overview.md`) disabled — position tracking, ledger writes, reservation semantics all work entirely on internal records. When `ILDWAC_ENABLED=true`, ledger entries carry the five-dimension provenance fields (`device_id`, `mcp_tool`, `pos_port`) that ILDWAC consumes for cost attribution — when off, those fields are nullable and the standard ILWAC cost surface (item × location × WAC) operates from the same ledger via the existing MAC infrastructure. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, inventory event chain hashes are eligible for public anchoring; failures are non-blocking.

---

## Business

### The Inventory Latency Problem

The traditional retail inventory problem is not accuracy — it is latency. Inventory counts are accurate at the moment they are taken and stale within the hour. A physical count says "200 units" at 6am; by noon, 47 have sold in-store, 12 are reserved in online carts, 8 are on BOPIS hold, and 3 are missing. The system still shows 200. An online customer buys what is not there.

IaaS solves this by making every inventory-moving event — POS sale, receiving dock scan, cart reservation, BOPIS pickup — a synchronous write to the ledger and the snapshot. Position is not a count; it is a running balance updated in real time by the events that change it. The storefront reads from Valkey (sub-millisecond); the Valkey entry is invalidated on every ledger write. The number the customer sees when they click "add to cart" is the number that matters — not the number from last night's batch job.

> **Solex is illustrative:** The cart-reservation and BOPIS-hold semantics that make this engine necessary are illustrated in `Solex/solex/services/inventory.py` and `cart.py`. Solex is single-channel and single-location — its inventory model is a simplified case, useful for grounding the operational invariants (reservation expiry, decrement on payment, restock on cancel) but not a literal port target. IaaS generalizes those patterns into the multi-channel, multi-location, real-time engine this SDD specifies. See ecom-channel.md → "Solex Port-Forward Inventory" for the full map.

### The Store-as-Warehouse Insight

A store location serving online orders is not a warehouse workaround — it is a distribution network. Five store locations in a metro area are five fulfillment nodes with sub-2-hour pickup capability that a centralized warehouse cannot match. BOPIS at 30% of online order volume (retail industry benchmark) is the best margin-per-order outcome in ecommerce: no last-mile shipping cost, store associate labor already accounted for, customer converts additional basket at pickup. But it only works if the inventory position is trusted at the moment of checkout. IaaS is what makes that trust possible.

### Business Rules

1. Every inventory-moving event writes to the ledger before it is acknowledged to the caller. There is no fire-and-forget path for ledger writes.
2. The Valkey available-units counter is the oversell gate on the hot path. The DB constraint is the safety net. Both must be enforced — the Valkey layer handles 99%+ of oversell protection; the DB constraint catches drift.
3. Cart reservations expire after 15 minutes. Checkout-started reservations extend to 30 minutes, refreshed on each page load. BOPIS commitments have no TTL — they are hard holds until pickup or explicit cancellation.
4. Physical count adjustments require two-actor authorization. An associate counts; a manager confirms. This is the four-eyes principle for inventory and prevents associate-authorized shrink concealment.
5. Shrink writeoffs require an LP case `reference_id`. No unlinked writeoffs are accepted.
6. `floor_stock_min` and `safety_stock` per location per item are settings-managed (see `settings.md`). IaaS reads them; it does not own them.

### Inventory Ledger Reason Types

Every inventory change is a ledger entry. The ledger is append-only — like `raas_events`, there is no UPDATE or DELETE path. Current position is maintained in a snapshot table updated atomically with each ledger write.

| Reason | Direction | Trigger |
|--------|-----------|---------|
| `received` | +qty | Receiving dock scans ASN/PO completion |
| `sold_instore` | -qty | POS transaction commit |
| `sold_online` | -qty | Online order payment success |
| `returned_instore` | +qty | In-store return, item restocked |
| `returned_online` | +qty | Online return received and inspected |
| `reserved_cart` | -available (not -qty) | Item added to online cart (soft hold) |
| `reservation_released` | +available | Cart abandoned or TTL expired |
| `committed_bopis` | -available (hard hold) | Associate picks item for BOPIS hold |
| `fulfilled_bopis` | -qty (hard commit) | Customer picks up BOPIS order |
| `cancelled_bopis` | +available | BOPIS not picked up; hold released |
| `transfer_out` | -qty | Inventory moved to another location |
| `transfer_in` | +qty | Inventory received from another location |
| `adjustment_count` | +/- qty | Physical count reconciliation |
| `shrink_writeoff` | -qty | LP-confirmed shrink (theft, damage, expiry) |
| `floor_stock_min_change` | N/A | Floor stock minimum updated (metadata only) |

**Critical distinction:** `reserved_cart` and `committed_bopis` reduce `available` (the quantity available for new orders) without reducing `qty` (on-hand physical count). Only `sold_*`, `returned_*`, `fulfilled_bopis`, `transfer_*`, `adjustment_count`, and `shrink_writeoff` change physical `qty`.

### Sub-Location Model

Each location has four sub-locations. The distinction between physical and virtual drives the availability calculation.

| Sub-location | Type | Available online? | Available in-store? |
|-------------|------|-------------------|---------------------|
| `shelf` | Physical | Only above `floor_stock_min` | Yes |
| `backroom` | Physical | Yes (100%) | Yes |
| `hold` | Virtual | No (already committed) | No (reserved for pickup) |
| `in_transit` | Virtual | No | No (not yet physically present) |

**Availability formulas:**

```
available_online    = (shelf_qty - floor_stock_min) + backroom_qty - cart_reservations - bopis_commitments
available_instore   = shelf_qty + backroom_qty - bopis_commitments
total_on_hand       = shelf_qty + backroom_qty + hold_qty
```

### Multi-Tier Assortment Model

A store's relationship to the catalog is not binary. Each store-item pair has an assortment tier that governs what the storefront offers, how the order is fulfilled, and how IaaS computes availability. Three tiers, ordered by physical proximity:

| Tier | Stock pool | Fulfillment paths | Availability source |
|---|---|---|---|
| **Store assortment** | This store's on-hand (`shelf` + `backroom`) | BOPIS pickup, ship-from-store, in-store walk-in | `inventory_positions` for this `(store_id, item_id)` |
| **Warehouse assortment** | Centralized warehouse on-hand (separate `location_id` of type `warehouse`) | Ship-to-customer from warehouse, store-replenishment transfer | `inventory_positions` for the warehouse location |
| **Expanded assortment** | Not stocked anywhere — available via vendor drop-ship or special order | Vendor-direct ship, special order at the counter, in-store ring-up with vendor PO | Vendor capability registry (not a stock pool — a drop-ship promise) |

**The store-level catalog is a projection.** Every store has a regular assortment (the SKUs it actively carries), a fulfillment-extended assortment (SKUs it does not stock but the warehouse does), and an expanded assortment (SKUs the merchant can special-order from a vendor on demand). The same item appears differently in each:

- A bottle of supplement carried locally → store assortment, on-shelf availability, BOPIS-in-an-hour
- A device the merchant stocks centrally but not at this location → warehouse assortment, ship-in-3-days, no BOPIS at this store
- A discontinued but vendor-orderable variant → expanded assortment, special-order-only, 5-10 day vendor lead time

**Fulfillment routing decision:** when an ecom order is placed against a specific store, IaaS evaluates options in tier order — store first (BOPIS/ship-from-store), then warehouse (ship-to-customer), then expanded (special order). The first tier that has the item in sufficient quantity wins. Ties (e.g., warehouse and store both have it; warehouse is closer to the customer's ship-to address) are broken by `fulfillment_routes` heuristics.

**Catalog data model implications:**

- Items carry assortment metadata per store: `(store_id, item_id, tier, active)` — owned by `cmd/item`, read by IaaS for assortment-aware availability
- The expanded assortment is keyed off vendor capability, not stock — `cmd/commercial` owns the vendor-can-drop-ship registry
- Special-order-only items have NULL on-hand at every location and are still purchasable; the order routes to vendor PO automatically

**Why this matters operationally:** the merchant wants to sell what the customer wants without losing margin to oversell or stocking out a slow-mover. The three-tier model keeps the store storefront expansive (broader catalog than the store physically carries) without breaking the trust model on the BOPIS path. A customer who picks BOPIS sees only store-assortment-on-hand items as one-hour-pickup; everything else routes to ship.

**Solex limitation:** Solex is single-store and treats inventory as a single pool keyed only by item. The multi-tier assortment model is a Canary-Go addition; do not look for it in Solex.

---

## Technical

### Service Boundaries

IaaS owns four mutually exclusive table groups. No other service writes to any of these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Snapshot | `inventory_positions` | Current on-hand and available-unit state per location/item/sub-location |
| Reservations | `inventory_reservations` | Soft holds (cart) and hard holds (BOPIS/ship-from-store) |
| Ledger | `inventory_ledger` | Append-only audit trail of every inventory-moving event |
| BOPIS | `bopis_holds` | BOPIS hold lifecycle (open → picked → ready → fulfilled/cancelled) |
| Routing Cache | `fulfillment_routes` | Precomputed best-source per item per channel (refreshed every 5 min) |

### Data Model

#### `inventory_positions`

```sql
-- Inventory snapshot (current state, updated atomically)
CREATE TABLE inventory_positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    location_id     UUID NOT NULL REFERENCES locations(id),
    item_id         UUID NOT NULL REFERENCES items(id),
    sub_location    TEXT NOT NULL DEFAULT 'backroom',  -- shelf | backroom | hold | in_transit
    qty             INT NOT NULL DEFAULT 0,
    floor_stock_min INT NOT NULL DEFAULT 0,  -- only meaningful for sub_location='shelf'
    safety_stock    INT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, location_id, item_id, sub_location),
    CONSTRAINT non_negative_qty CHECK (qty >= 0)
);

CREATE INDEX idx_inv_pos_merchant_item ON inventory_positions(merchant_id, item_id);
CREATE INDEX idx_inv_pos_location_item ON inventory_positions(location_id, item_id);
```

Device-level position is derived from `inventory_ledger` grouped by `device_id` — there is no `device_id` column on `inventory_positions` because position is maintained at the location/sub_location granularity. Device WAC computation (item × location × device) is owned by the ILDWAC service — see `ildwac.md`.

**Invariant:** `qty` never goes negative. The DB `CHECK` constraint is the final backstop. The Valkey atomic decrement is the first line of defense.

#### `inventory_reservations`

```sql
CREATE TABLE inventory_reservations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    location_id     UUID NOT NULL,
    item_id         UUID NOT NULL,
    qty             INT NOT NULL,
    reservation_type TEXT NOT NULL,  -- 'cart' | 'bopis' | 'ship_from_store'
    order_reference TEXT,            -- ecom order ID or cart session ID
    expires_at      TIMESTAMPTZ NOT NULL,
    released_at     TIMESTAMPTZ,     -- null until released
    fulfilled_at    TIMESTAMPTZ,     -- null until fulfilled
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_inv_res_location_item ON inventory_reservations(location_id, item_id)
    WHERE released_at IS NULL AND fulfilled_at IS NULL;
CREATE INDEX idx_inv_res_expires ON inventory_reservations(expires_at)
    WHERE released_at IS NULL AND fulfilled_at IS NULL;
```

#### `inventory_ledger`

The append-only audit trail. INSERT-only — no UPDATE or DELETE ever touches this table.

```sql
CREATE TABLE inventory_ledger (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    location_id     UUID NOT NULL,
    item_id         UUID NOT NULL,
    sub_location    TEXT NOT NULL,
    reason          TEXT NOT NULL,   -- typed enum (see Business section)
    qty_delta       INT NOT NULL,    -- positive = added, negative = removed
    reference_id    TEXT,            -- PO ID, order ID, case ID, count sheet ID
    reference_type  TEXT,            -- 'purchase_order' | 'ecom_order' | 'lp_case' | 'count_sheet' | 'bopis_hold'
    actor_id        TEXT NOT NULL,
    actor_type      TEXT NOT NULL,   -- 'human' | 'agent' | 'system'
    device_id       UUID REFERENCES inventory_devices(id),  -- null for location-level events
    mcp_tool        TEXT,           -- MCP tool name if agent-triggered: "append_event", "record_adjustment"
    pos_port        TEXT,           -- POS connector: "square" | "counterpoint" | "rapidpos" | "manual"
    raas_sequence   BIGINT,          -- chain sequence for this event (null for non-chain events)
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_inv_ledger_location_item ON inventory_ledger(location_id, item_id, occurred_at);
CREATE INDEX idx_inv_ledger_reason ON inventory_ledger(reason, occurred_at);
CREATE INDEX idx_inv_ledger_reference ON inventory_ledger(reference_type, reference_id);
CREATE INDEX idx_inv_ledger_device ON inventory_ledger(device_id, occurred_at) WHERE device_id IS NOT NULL;
```

#### `bopis_holds`

```sql
CREATE TABLE bopis_holds (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    location_id     UUID NOT NULL,  -- pickup location
    ecom_order_id   UUID NOT NULL REFERENCES ecom_orders(id),
    item_id         UUID NOT NULL,
    qty             INT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'open',  -- open | picked | ready | fulfilled | cancelled
    reservation_id  UUID REFERENCES inventory_reservations(id),
    picked_by       TEXT,           -- associate ID
    picked_at       TIMESTAMPTZ,
    ready_at        TIMESTAMPTZ,
    pickup_deadline TIMESTAMPTZ NOT NULL,  -- expires_at + pickup_window (default 48h)
    fulfilled_at    TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    cancel_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### `fulfillment_routes`

```sql
CREATE TABLE fulfillment_routes (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL,
    item_id               UUID NOT NULL,
    channel               TEXT NOT NULL,  -- 'online_ship' | 'bopis' | 'store_transfer'
    preferred_location_id UUID REFERENCES locations(id),
    available_qty         INT NOT NULL,
    shrink_risk_score     NUMERIC(5,2),
    computed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, item_id, channel)
);
```

#### `inventory_devices`

```sql
-- Stock-holding devices (sub-location granularity below shelf/backroom/hold)
CREATE TABLE inventory_devices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    location_id     UUID NOT NULL REFERENCES locations(id),
    sub_location    TEXT NOT NULL,  -- shelf | backroom | hold | in_transit
    device_type     TEXT NOT NULL,  -- bin | shelf | peg | barrel | chip | serial
    device_code     TEXT NOT NULL,  -- human-readable: "A3", "B2-TOP", "PEG-47"
    device_category TEXT NOT NULL DEFAULT 'cost_center',  -- cost_center | profit_center
    active          BOOLEAN NOT NULL DEFAULT true,
    l402_wallet_id  TEXT,           -- non-null if feature.l402_enforcement_enabled and profit_center
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(location_id, sub_location, device_code)
);

CREATE INDEX idx_inv_devices_location ON inventory_devices(location_id) WHERE active = true;
```

Device types:
- `bin` — container bin (warehouse picking, stockroom bin locations)
- `shelf` — shelf section (specific section of a shelf run, assigned to one or more SKUs)
- `peg` — peg hook (one peg = one SKU, qty by count of units on hook)
- `barrel` — bulk display (loose items sold by count; qty is an approximation until next count)
- `chip` — chip/card display (gift cards, SIM cards, game tokens — face-value tracked)
- `serial` — serialized unit (each physical item tracked by serial number; qty is always 1)

Profit-center devices generate revenue from their position. They have their own WAC and optionally an L402 wallet for OTB tracking. Cost-center devices hold stock without directly generating revenue at the device level.

### Reservation Protocol — Oversell Protection

The core guarantee: the system never sells what it does not have. Enforced in two layers that operate independently. If the Valkey layer fails, the DB constraint catches it. If both drift simultaneously, the monitoring alert fires before the next order clears.

**Layer 1 — Valkey atomic decrement (hot path)**

```
Key: inv:available:{location_id}:{item_id}
Value: integer (available units, refreshed from DB every 60s)

Reserve:
  result = DECRBY inv:available:{location_id}:{item_id} {qty}
  if result < 0:
    INCRBY inv:available:{location_id}:{item_id} {qty}  -- rollback
    return INSUFFICIENT_STOCK error

On success: INSERT inventory_reservations, set expires_at = now() + TTL
```

TTL schedule:

| Reservation type | TTL |
|-----------------|-----|
| Cart (browsing) | 15 minutes |
| Checkout started | 30 minutes (refreshed on each page load) |
| BOPIS committed | No TTL — hard hold until pickup or cancellation |

**Layer 2 — DB constraint (safety net)**

```sql
CREATE OR REPLACE FUNCTION check_non_negative_available()
RETURNS TRIGGER AS $$
BEGIN
    IF (NEW.qty - COALESCE(
        (SELECT SUM(qty) FROM inventory_reservations
         WHERE location_id = NEW.location_id
         AND item_id = NEW.item_id
         AND released_at IS NULL
         AND fulfilled_at IS NULL), 0)
    ) < 0 THEN
        RAISE EXCEPTION 'Inventory position would go negative for location % item %',
            NEW.location_id, NEW.item_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

The DB constraint should never fire in normal operation. If it fires, Valkey state has drifted — the drift detection alarm should already be alerting. A DB constraint violation returns 409 Conflict to the caller; it does not propagate as a 500.

### Fulfillment Routing

For online orders, IaaS computes the best fulfillment source given channel and customer location.

**Route query:**

```
RouteQuery{
    merchant_id, item_id, qty, channel, customer_location (lat/lng or zip)
}
```

**Route result:**

```
RouteResult{
    location_id, available_qty, estimated_pickup_mins (BOPIS),
    distance_km, shrink_risk_score (0.0–10.0),
    route_type (ship_from_store | bopis | warehouse)
}
```

**Routing priority:**

| Priority | Channel | Logic |
|----------|---------|-------|
| 1 | BOPIS | Customer-selected location; must have `available_qty >= requested`. If short, offer alternatives sorted by distance. |
| 2 | Ship-from-store | Location with highest `available_qty` that minimizes distance + shrink risk. `shrink_risk_score` sourced from fox module. |
| 3 | Warehouse | No store location can fulfill; route to dedicated warehouse location. |

Routing results are cached in `fulfillment_routes`. Cache is refreshed every 5 minutes or on any ledger write that touches the affected item.

### API Contract

All routes require JWT auth. Adjustment routes additionally require `inventory_manager` or `admin` role claim in the JWT.

```
GET    /inventory/position/{location_id}/{item_id}   → real-time position (all sub-locations)
GET    /inventory/available/{location_id}/{item_id}  → available_online + available_instore
POST   /inventory/reserve                            → create soft reservation (cart/checkout)
DELETE /inventory/reserve/{reservation_id}           → release reservation (cart abandoned)
POST   /inventory/commit/{reservation_id}            → hard commit (payment success)
POST   /inventory/bopis                              → create BOPIS hold
PUT    /inventory/bopis/{hold_id}/status             → update BOPIS hold (picked/ready/fulfilled/cancelled)
POST   /inventory/adjust                             → manual adjustment (count reconciliation; requires inventory_manager)
GET    /inventory/route                              → fulfillment routing query
GET    /inventory/ledger/{location_id}/{item_id}     → ledger history with cursor pagination
GET    /inventory/healthz                            → 200 (shallow liveness)
GET    /inventory/readyz                             → 200 | 503 (DB + Valkey check)
```

### MCP Tool Surface — `canary-inventory` (10 tools)

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `get_position` | `location_id, item_id` | `{qty, available_online, available_instore, sub_locations}` | Real-time from snapshot |
| `get_positions_bulk` | `location_id, item_ids[]` | `[{item_id, position}]` | Batch read for storefront |
| `reserve_inventory` | `location_id, item_id, qty, type, ttl_minutes` | `{reservation_id, expires_at}` | Valkey-first; DB fallback |
| `release_reservation` | `reservation_id` | `{released: bool}` | Idempotent |
| `commit_reservation` | `reservation_id, order_reference` | `{ledger_id}` | Payment confirmed; converts soft hold to ledger entry |
| `create_bopis_hold` | `ecom_order_id, location_id, item_id, qty, pickup_window_hours` | `{hold_id, pickup_deadline}` | Creates hard hold + reservation row |
| `update_bopis_status` | `hold_id, status` | `{hold}` | picked / ready / fulfilled / cancelled |
| `route_fulfillment` | `merchant_id, item_id, qty, channel, customer_zip` | `{location_id, route_type, available_qty, shrink_risk}` | Routing cache consulted first |
| `record_adjustment` | `location_id, item_id, sub_location, qty_delta, reason, reference_id` | `{ledger_id}` | Writes ledger + updates snapshot atomically |
| `get_ledger` | `location_id, item_id, after_cursor?, limit` | `[{ledger_entry}]` | Cursor-paginated; max 500 entries per call |

### Go Implementation Notes

- `reserve_inventory` must use a Lua script for the Valkey DECRBY + rollback sequence. A single DECRBY followed by a conditional INCRBY is not atomic across two commands without a Lua transaction.
- `record_adjustment` and `commit_reservation` must use `pgx` transactions: the ledger INSERT and the snapshot UPDATE must be atomic. A partial write (ledger written, snapshot not yet updated) leaves position in an inconsistent state.
- The Valkey key `inv:available:{location_id}:{item_id}` is refreshed from DB every 60 seconds via a background goroutine per merchant. On cache miss (key not present), the goroutine triggers an immediate refresh rather than serving a stale 0.
- `get_positions_bulk` uses a single `IN (...)` query — do not issue N individual queries for a bulk storefront read.
- The `reason` field in `inventory_ledger` is a typed string constant in Go. Define a `LedgerReason` type and validate at the handler boundary — do not allow arbitrary string writes to the ledger.
- Two-actor authorization for `adjustment_count` entries: the handler requires a `confirmed_by` field in the request body distinct from `actor_id`. Both must be valid user IDs. The confirmation actor must hold `inventory_manager` or `admin` role.
- `shrink_writeoff` entries: validate that `reference_id` is non-empty and `reference_type = 'lp_case'` before writing. Return 422 if either is missing.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `get_position` (Valkey hit) | <5ms | <20ms | 100ms | Alert + fallback to DB |
| `get_positions_bulk` (≤50 items) | <20ms | <100ms | 500ms | Alert |
| `reserve_inventory` (Valkey path) | <10ms | <50ms | 200ms | Alert |
| `reserve_inventory` (DB fallback) | <50ms | <200ms | 1s | Alert |
| `record_adjustment` | <100ms | <500ms | 2s | Alert |
| `route_fulfillment` (cache hit) | <20ms | <100ms | 500ms | Alert |
| `route_fulfillment` (cache miss) | <200ms | <1s | 3s | Alert |

### Health Endpoints

```
GET /inventory/healthz

Shallow liveness check — returns 200 if the process is up.
Never checks DB or Valkey. Returns 503 only on catastrophic internal panic.

Response 200:
{
  "status": "ok"
}
```

```
GET /inventory/readyz

Deep readiness check — verifies DB connection pool and Valkey reachability.

Response 200:
{
  "status": "ok",
  "valkey_ok": true,
  "db_ok": true,
  "active_reservations": 142,
  "valkey_cache_hit_rate_pct": 94.2
}

Response 503 if DB or Valkey unreachable.
```

GCP Cloud Run liveness probe: `GET /inventory/healthz`. GCP Cloud Run readiness probe: `GET /inventory/readyz` — 503 removes the instance from load balancer rotation without a cold-start restart.

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| Valkey unreachable | All reservation attempts fall through to DB path. Latency degrades to DB P99. Oversell protection shifts entirely to the DB constraint. | Log warning; alert if sustained > 30s. Continue operating at DB latency — do not reject orders. |
| DB unreachable | `get_position` returns Valkey-cached available units if key is warm. `record_adjustment` and `commit_reservation` return 503. | Auto-recovery via pgx connection pool. In-flight reservation attempts queue behind connection retry. |
| Valkey drift (key out of sync with DB) | DB constraint fires on snapshot UPDATE — returns 409 Conflict. | Alert on any DB constraint violation. DBA agent runs `SELECT SUM(qty)` reconciliation query. Valkey key is force-refreshed from DB. |
| BOPIS hold orphaned (ecom order cancelled) | `update_bopis_status(cancelled)` releases the reservation and writes `cancelled_bopis` to ledger. If ecom-channel calls before IaaS is notified, hold remains open until `pickup_deadline`. | Ecom-channel is responsible for calling `update_bopis_status` on order cancellation. IaaS does not poll for stale holds — it responds to events. |
| Fulfillment routing stale | Routing cache is refreshed every 5 minutes. Stale cache entry may route to a location that has since sold out. | `route_fulfillment` checks `available_qty` live against snapshot before returning cached result. If snapshot says zero, cache is invalidated and a fresh route is computed. |

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `inv:available:{location_id}:{item_id}` | 60s (refreshed by background goroutine) | Available-unit counter for reservation hot path |
| `inv:route:{merchant_id}:{item_id}:{channel}` | 300s | Fulfillment routing cache |
| `inv:pos:{location_id}:{item_id}` | 60s | Position snapshot cache (for `get_position` fast path) |

### Monitoring

Alert on:

- Any DB constraint violation (`check_non_negative_available` fires in production)
- `reserve_inventory` P99 > 200ms sustained for 2 minutes
- `record_adjustment` P99 > 1s sustained for 1 minute
- Valkey cache hit rate for `inv:available:*` below 80% for 5 minutes (indicates background refresh falling behind)
- Any `bopis_holds` row with `status = 'open'` and `pickup_deadline < now() - 1 hour` (missed pickup not yet cancelled)
- Any `inventory_ledger` row with `reason = 'shrink_writeoff'` and `reference_id IS NULL` (write should have been rejected at the handler — this indicates a code path bypass)

### Graceful Shutdown

```go
// Standard pattern — see go-runtime.md for the shared shutdown.go helper
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

// Allow in-flight reservation commits to complete (atomic ledger + snapshot writes)
srv.Shutdown(ctx) // waits up to ShutdownTimeout (default 30s)
pool.Close()      // close pgx pool after HTTP server drains
```

In-flight `commit_reservation` and `record_adjustment` calls must complete before shutdown. A connection killed mid-transaction rolls back cleanly — the caller receives a 500 and must retry.

---

## Compliance

### Four-Eyes Principle for Adjustments

Physical count adjustment (`reason = adjustment_count`) requires two distinct actor IDs in the request:

- `actor_id`: the associate who performed the count
- `confirmed_by`: the manager who authorized it

Both must be valid user IDs. `confirmed_by` must hold `inventory_manager` or `admin` role in the JWT. The ledger entry records both fields. This prevents an associate from unilaterally adjusting inventory to conceal shrink.

### Shrink Writeoff Integrity

`shrink_writeoff` entries require a valid LP case `reference_id` and `reference_type = 'lp_case'`. The handler validates both before writing. Any `shrink_writeoff` without a linked LP case is rejected with 422. This rule exists because unlinked writeoffs are the primary mechanism for concealing associate-attributed theft — the LP case chain (see `hawk-case-management.md`) is the evidence anchor.

### Append-Only Invariant

The `inventory_ledger` table has no UPDATE or DELETE path in the application code. The sqlc-generated queries for this table are INSERT-only. Enforce at the database level:

```sql
REVOKE UPDATE, DELETE ON inventory_ledger FROM canary_app;
```

Corrections are new ledger entries (e.g., a correction adjustment entry). The original entry is never overwritten. This matches the `raas_events` invariant — the ledger is evidence.

### Evidence Chain

`inventory_ledger` entries where `reference_type = 'lp_case'` are evidence records. See `hawk-case-management.md` for evidence chain requirements. These entries are retained for the full 7-year financial record period regardless of the LP case status.

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `actor_id` | `inventory_ledger` | Internal identifier | Retained as audit field; not exposed in storefront APIs |
| `order_reference` | `inventory_reservations` | May contain PII (customer session ID or order ID) | Do not log raw; truncate in error messages |
| `picked_by` | `bopis_holds` | Employee identifier | Retained for associate accountability; do not expose externally |

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `inventory_ledger` | 7 years | Financial record retention (IRS, SOX) |
| `inventory_positions` | Current only | Operational snapshot; no retention requirement |
| `inventory_reservations` | 90 days after `released_at` or `fulfilled_at` | Operational audit window |
| `bopis_holds` | 90 days after `fulfilled_at` or `cancelled_at` | Operational audit window |
| `fulfillment_routes` | Current only | Computed cache; no retention requirement |

---

## Related SDDs

- `ecom-channel.md` — calls `reserve_inventory` on cart add, `commit_reservation` on payment success, `create_bopis_hold` for BOPIS orders
- `tsp.md` — calls `record_adjustment(reason=sold_instore)` on every POS transaction commit
- `receiving.md` — calls `record_adjustment(reason=received)` on PO completion at the receiving dock
- `fox.md` — receives fulfillment routing queries; `shrink_risk_score` in routing results is sourced from fox
- `hawk-case-management.md` — shrink writeoffs require an LP case reference; ledger entries where `reference_type = 'lp_case'` are governed by hawk evidence chain requirements
- `bull.md` — monitors inventory positions; triggers replenishment when position drops below `safety_stock`
- `owl.md` — reads inventory positions for search-layer inventory availability signals
- `raas.md` — IaaS appends inventory events to the RaaS receipt chain for inventory-moving operations that require evidentiary integrity
- `settings.md` — `floor_stock_min` and `safety_stock` per location per item are settings-managed; IaaS reads and applies them but does not own them
- `ildwac.md` — Device-level WAC computation. Reads `inventory_ledger` (with `device_id`, `mcp_tool`, `pos_port` dimensions) to maintain the five-dimension cost model. Serialized items (`device_type=serial`) get unit-level WAC. Profit-center devices contribute to OTB wallet balance.
