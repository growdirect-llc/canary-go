---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: pricing
port: 8094
mcp-server: canary-pricing
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Pricing — Price History and Promotion Engine

**Type:** Domain Service — Pricing Authority  
**Binary:** `cmd/pricing` → `:8094`  
**MCP server:** `canary-pricing` (8 tools)  
**Depends on:** `identity` (merchant and location existence), `raas` (`price_changed` events appended to chain)  
**Feeds:** `ildwac` (effective price at transaction time for margin computation), `hawk` (price anomaly signals), `fox` (ecom price display), `tsp-seal` (price snapshot at transaction moment)

Pricing is the single source of truth for what an item costs a customer at any given moment. This sounds obvious but is where most retail analytics break down — a markdown event, a promotional override, and a loyalty discount can all hit the same transaction, and if the system cannot reconstruct what price applied when, margin analysis is fiction. Canary's pricing module maintains a full history of every price that was ever active, with `effective_from`/`effective_to` timestamps, so every past transaction can be repriced accurately. The `effective_price_at` tool is the canonical query: given an item and a timestamp, return the price that was in effect. No other service is permitted to make this determination.

**Multi-tenant context.** Pricing tables (`prices`, `price_history`, `promotions`, `markdowns`, `price_zones`) live per-tenant in `tenant_{merchant_id}`. Every price decision is merchant-scoped; cross-tenant pricing analytics (industry markdown rate benchmarks) flow through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Pricing operates with all Optional Features (per `platform-overview.md`) disabled. The `effective_price_at` tool, price history, markdown lifecycle, and zone-based price authority all run on internal records. When `ILDWAC_ENABLED=true`, price events are joined with cost packets at margin computation time across the five-dimension provenance — when off, standard ILWAC margin computation applies. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, price change events are eligible for public anchoring (regulatory audit defense for advertised vs. rung price disputes); failures are non-blocking.

---

## Business

### The Price-at-Time Problem

A merchant runs a 20%-off weekend sale. On Monday, a customer returns a sweater purchased Friday. The POS now shows the non-sale price. The cashier, guessing, refunds the current price — which is 20% higher than what the customer paid. The merchant either over-refunds or creates a dispute. Canary prevents this by reconstructing the exact price in effect at the original transaction's timestamp, which feeds the returns module's refund calculation.

The same problem appears in margin analysis: if a buyer wants to know actual gross margin for a category last quarter, they need to know what price was in effect on each sale day — not today's price. Without a price history, this calculation is impossible without manual research.

### Price Layer Priority

Four price layers can be simultaneously active for any item. The highest-priority layer that matches wins.

```
Priority 1: promotional_price    — time-bounded promotion (sale event)
Priority 2: loyalty_price        — loyalty-program member price
Priority 3: markdown_price       — permanent, temporary, or clearance markdown
Priority 4: regular_price        — the base price; always present
```

At query time, `effective_price_at` checks each layer in priority order and returns the first active match. If no promotion or markdown is active, regular price is returned. This logic lives in the application layer, not the DB — the DB stores history; the application resolves priority.

### Price Change Categories

| `price_type` | Description | Duration |
|---|---|---|
| `regular` | Base retail price | Indefinite until changed |
| `markdown` | Permanent markdown — item reclassified to a lower price tier | Indefinite |
| `temporary` | Time-limited sale price | Has explicit `effective_to` |
| `clearance` | Terminal markdown — item liquidating | Indefinite; no re-price expected |
| `promotional` | Promotion-driven override; managed via `price_promotions` | Bounded by promotion `ends_at` |

### Business Rules

1. Price rows are never updated. A new price is a new row with a new `effective_from`. The prior row's `effective_to` is set to `effective_from` of the new row at INSERT time (application layer, not a trigger).
2. `effective_price_at` is the authoritative price resolver. No other service computes what price was in effect at a given time.
3. Price change events are appended to the RaaS chain as `pricing.price_changed` events. This is non-blocking: a price change is committed to the `item_prices` table before the RaaS append. If RaaS append fails, the price change is retained and the failed append is retried asynchronously.
4. Bulk markdown (`bulk_markdown`) applies the same markdown to a set of items in a single transaction. Partial failures roll back the entire batch — no half-applied markdowns.
5. A promotion with no `applies_to` criteria is rejected at creation. Every promotion must target something (SKU list, category, or location).

---

## Technical

### Service Boundaries

Pricing owns three table groups. No other service writes to these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Item Prices | `item_prices` | Append-only price history per item |
| Promotions | `price_promotions` | Time-bounded promotional price overrides |
| Change Events | `price_change_events` | Audit log linking price changes to RaaS chain sequences |

### Data Model

#### `item_prices`

The core table. INSERT-only in normal operation. `effective_to` is set by the application layer when a new price row closes the prior one.

```sql
CREATE TYPE price_type AS ENUM ('regular', 'markdown', 'clearance', 'temporary', 'promotional');

CREATE TABLE item_prices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id        UUID NOT NULL,               -- FK to items table (owned by inventory module)
    location_id    UUID,                        -- NULL = all locations for this merchant
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    price_type     price_type NOT NULL,
    price_sats     BIGINT NOT NULL CHECK (price_sats >= 0),
    effective_from TIMESTAMPTZ NOT NULL,
    effective_to   TIMESTAMPTZ,                 -- NULL = currently active
    created_by     TEXT NOT NULL,               -- associate_id or "system" or "agent:{module}"
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
    -- no updated_at: this table is INSERT-only
);

CREATE INDEX idx_item_prices_item_time ON item_prices(item_id, effective_from DESC);
CREATE INDEX idx_item_prices_item_active ON item_prices(item_id, price_type)
    WHERE effective_to IS NULL;   -- partial index: active prices only
CREATE INDEX idx_item_prices_merchant ON item_prices(merchant_id, effective_from DESC);
CREATE INDEX idx_item_prices_location ON item_prices(location_id, item_id, effective_from DESC)
    WHERE location_id IS NOT NULL;
```

**Closing prior active row:** when `set_price` is called, the handler executes in a transaction:

```sql
-- Step 1: close the currently active row for this item/location/type
UPDATE item_prices
SET effective_to = $new_effective_from
WHERE item_id = $item_id
  AND (location_id = $location_id OR (location_id IS NULL AND $location_id IS NULL))
  AND price_type = $price_type
  AND effective_to IS NULL;

-- Step 2: insert the new price row
INSERT INTO item_prices (...) VALUES (...);
```

Both steps are in one `pgx` transaction. If the UPDATE touches zero rows, the item has no prior active price of this type — the INSERT still proceeds (new item, or new price type for this item).

#### `price_promotions`

```sql
CREATE TABLE price_promotions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    name            TEXT NOT NULL,
    promotion_type  TEXT NOT NULL,   -- "pct_off" | "fixed_off" | "fixed_price" | "bogo"
    applies_to      JSONB NOT NULL,  -- {skus: [...]} | {category_id: "..."} | {location_id: "..."}
    discount_pct    NUMERIC(5,2),    -- used for pct_off; NULL for other types
    discount_sats   BIGINT,          -- used for fixed_off; NULL for other types
    fixed_price_sats BIGINT,         -- used for fixed_price; NULL for other types
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    CHECK (applies_to IS NOT NULL AND jsonb_typeof(applies_to) = 'object')
);

CREATE INDEX idx_price_promotions_merchant ON price_promotions(merchant_id);
CREATE INDEX idx_price_promotions_active_window ON price_promotions(merchant_id, active, starts_at, ends_at)
    WHERE active = true;
```

#### `price_change_events`

Audit link between a price change and the RaaS chain sequence assigned to it.

```sql
CREATE TABLE price_change_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id         UUID NOT NULL,
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    old_price_sats  BIGINT,          -- NULL for first price ever set on this item
    new_price_sats  BIGINT NOT NULL,
    change_type     price_type NOT NULL,
    effective_at    TIMESTAMPTZ NOT NULL,
    raas_sequence   BIGINT,          -- NULL if RaaS append is pending or failed
    raas_namespace  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_price_change_events_item ON price_change_events(item_id, effective_at DESC);
CREATE INDEX idx_price_change_events_raas ON price_change_events(raas_namespace, raas_sequence)
    WHERE raas_sequence IS NOT NULL;
CREATE INDEX idx_price_change_events_pending ON price_change_events(merchant_id, created_at)
    WHERE raas_sequence IS NULL;   -- partial index for retry sweep
```

**Retry sweep:** a background goroutine (one per service instance, not a separate worker) sweeps `price_change_events WHERE raas_sequence IS NULL AND created_at < NOW() - INTERVAL '30 seconds'` on a 60-second tick. Failed appends are retried up to 3 times before being escalated to the alert channel.

### Price Resolution Algorithm — `effective_price_at`

```
effective_price_at(item_id, location_id, timestamp):

1. Check for active promotion:
   SELECT p FROM price_promotions p
   WHERE p.merchant_id = $merchant_id
     AND p.active = true
     AND p.starts_at <= $timestamp
     AND p.ends_at > $timestamp
     AND (applies_to @> '{"skus": [$item_id]}'::jsonb
          OR applies_to @> '{"category_id": $category_id}'::jsonb)
   ORDER BY starts_at DESC LIMIT 1
   → FOUND: compute promotional_price from promotion terms, return with layer="promotional"

2. Check for active markdown at location (most specific wins):
   SELECT price_sats FROM item_prices
   WHERE item_id = $item_id
     AND location_id = $location_id
     AND price_type IN ('markdown', 'clearance', 'temporary')
     AND effective_from <= $timestamp
     AND (effective_to IS NULL OR effective_to > $timestamp)
   ORDER BY effective_from DESC LIMIT 1
   → FOUND: return with layer="markdown"

3. Check for active markdown at merchant level (location_id IS NULL):
   (same query, location_id IS NULL)
   → FOUND: return with layer="markdown"

4. Fall through to regular_price:
   SELECT price_sats FROM item_prices
   WHERE item_id = $item_id
     AND price_type = 'regular'
     AND effective_from <= $timestamp
     AND (effective_to IS NULL OR effective_to > $timestamp)
   ORDER BY effective_from DESC LIMIT 1
   → FOUND: return with layer="regular"
   → NOT FOUND: return 404 (item has no price history)
```

### API Contract

All routes require JWT auth except `/pricing/healthz` and `/pricing/readyz`.

```
GET  /pricing/healthz                         → 200
GET  /pricing/readyz                          → 200 | 503
GET  /pricing/price/{item_id}                 → 200 {effective_price}
POST /pricing/price                           → 201 {price_id}
POST /pricing/price/markdown                  → 201 {price_id}
POST /pricing/price/bulk-markdown             → 201 {applied: N, failed: 0}
POST /pricing/promotions                      → 201 {promotion_id}
GET  /pricing/price/{item_id}/history         → 200 [{price_row}]
GET  /pricing/price/{item_id}/at/{timestamp}  → 200 {effective_price}
GET  /pricing/analytics/{merchant_id}         → 200 {price_analytics}
```

### MCP Tool Surface — `canary-pricing` (8 tools)

| Tool | Input | Output | SLA | Notes |
|------|-------|--------|-----|-------|
| `get_price` | `item_id, location_id?` | `{price_sats, price_type, layer, effective_from}` | <50ms P99 | Current effective price; Valkey-cached |
| `set_price` | `item_id, location_id?, price_type, price_sats, effective_from, created_by` | `{price_id}` | <500ms | Closes prior active row + inserts new; appends `pricing.price_changed` to RaaS |
| `apply_markdown` | `item_id, location_id?, markdown_type, price_sats, effective_from, created_by` | `{price_id}` | <500ms | Shorthand for `set_price` with markdown price types |
| `create_promotion` | `merchant_id, name, promotion_type, applies_to, discount_*, starts_at, ends_at` | `{promotion_id}` | <500ms | Validates `applies_to` structure before INSERT |
| `get_price_history` | `item_id, location_id?, limit?, before?` | `[]{price_row}` | <200ms | Cursor-based; max 500 rows per call |
| `bulk_markdown` | `merchant_id, item_ids[], markdown_type, price_sats, effective_from, created_by` | `{applied: N}` | <5s for 500 items | Single pgx transaction; all-or-nothing |
| `effective_price_at` | `item_id, location_id?, timestamp` | `{price_sats, layer, source_id}` | <100ms P99 | Authoritative point-in-time price; used by returns and margin modules |
| `get_price_analytics` | `merchant_id, from, to, category_id?` | `{avg_markdown_depth, active_promotions, clearance_pct}` | <2s | Aggregation query; no Valkey cache |

### Trade-off: Full Price History vs. Current-Only Storage

**Option A (chosen): Append-only full price history.** Every price change creates a new row; prior rows are never deleted. Enables accurate historical margin analysis, correct return refund computation, and point-in-time price reconstruction for audits. For a $5M retailer with 1,000 SKUs and weekly price changes across 4 price types, this generates approximately 52,000 rows per year — trivially small in PostgreSQL terms. A 7-year retention window yields ~364,000 rows per merchant, well within single-table performance bounds with the partial index on `effective_to IS NULL`.

**Option B: Current-only with a separate change log.** Stores current prices in a single-row-per-item table (fast reads) and appends changes to a separate audit table. Faster reads on the happy path; more complex `effective_price_at` logic (must join current + change log). Rejected because the join complexity cancels the read performance gain at any meaningful history depth, and the abstraction leaks: callers must know which table to query.

**The cost of Option A:** `get_price` on the current-only hot path still needs a query that filters to `effective_to IS NULL`. The partial index handles this efficiently, but index maintenance adds ~10% overhead on price change writes. That cost is acceptable given the analytical payoff.

### Go Implementation Notes

- `get_price` caches the current effective price in Valkey: `SET pricing:{item_id}:{location_id|"merchant"} {price_sats}:{price_type} EX 300`. Key is invalidated on `set_price`, `apply_markdown`, and `bulk_markdown` for every affected item. Cache invalidation is performed inside the same `pgx` transaction via a post-commit hook: invalidate only after the DB write commits.
- `effective_price_at` does NOT use the Valkey cache — it is a historical query, and historical prices are immutable. Query directly from DB.
- `bulk_markdown` uses a single `pgx` transaction. Build the UPDATE + INSERT pairs for all items, execute inside `tx.Exec` calls within a loop, then commit. If any item fails validation (e.g., `price_sats <= 0`), return 422 before opening the transaction — validate first, write second.
- The `applies_to` JSONB containment check in `effective_price_at` uses the `@>` operator. Ensure the `price_promotions` table has a GIN index if promotion volume grows: `CREATE INDEX idx_price_promotions_applies_to_gin ON price_promotions USING GIN (applies_to)` — add as a later migration when promotion count per merchant exceeds ~100.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `get_price` (Valkey hit) | <5ms | <20ms | 50ms | Alert; check Valkey latency |
| `get_price` (DB fallback) | <20ms | <100ms | 300ms | Alert |
| `effective_price_at` | <30ms | <100ms | 500ms | Alert; this is on the returns hot path |
| `set_price` | <100ms | <500ms | 2s | Alert |
| `bulk_markdown` (500 items) | <1s | <5s | 15s | Alert; check transaction lock contention |

### Health Endpoints

```
GET /pricing/healthz
→ 200 { "status": "ok" }
Shallow liveness. No DB or Valkey check.

GET /pricing/readyz
→ 200 { "status": "ok", "db_ok": true, "valkey_ok": true, "active_promotions": N }
→ 503 if DB or Valkey unreachable.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| DB unreachable | `get_price` falls back to Valkey for cached items; returns 503 for cache misses. All write tools return 503. | Auto-recovery via pgx pool reconnect. |
| Valkey unreachable | `get_price` falls back to DB. All reads degrade to DB latency. Log warning; continue. | No alarm needed unless DB latency also spikes. |
| RaaS append failure on `set_price` | Price change is committed to DB. RaaS append is retried by the background sweep. `set_price` returns 201 — the caller's price change is accepted. | Background sweep retries up to 3x. Alert on persistent RaaS failures. |
| `bulk_markdown` partial failure | Entire transaction rolls back. Returns 422 with the first failing item. | Caller corrects the failing item and resubmits. |
| `effective_price_at` no rows | Returns 404 with `{priced: false}`. | Expected for newly onboarded items with no price history. Caller must handle. |

### Graceful Shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
srv.Shutdown(ctx) // 30s drain
pool.Close()
valkey.Close()
```

The background RaaS retry goroutine must be stopped before shutdown: pass the context into the goroutine's select loop so it exits cleanly when the shutdown context is cancelled.

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `pricing:{item_id}:merchant` | 300s | Merchant-level current price cache |
| `pricing:{item_id}:{location_id}` | 300s | Location-specific current price cache |

No caching for `effective_price_at` — historical queries are immutable but not hot enough to justify cache management complexity.

### Monitoring

Alert on:
- `effective_price_at` P99 > 500ms sustained for 1 minute (returns module dependency)
- `set_price` RaaS append failure rate > 5% (background sweep is falling behind)
- Any `bulk_markdown` transaction holding a lock for > 10s
- `price_change_events WHERE raas_sequence IS NULL AND created_at < NOW() - INTERVAL '10 minutes'` — pending append count > 50 (sweep is not keeping up)

---

## Compliance

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `created_by` | `item_prices` | Internal | Associate ID. Do not expose to external API consumers. Redact in merchant-facing exports. |

Pricing data is not directly PII-classified. Price history is financial operational data — 7-year retention applies under IRS and SOX financial record rules.

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `item_prices` | 7 years | Financial record; supports margin audit and dispute resolution |
| `price_promotions` | 7 years after `ends_at` | Promotional pricing audit trail |
| `price_change_events` | 7 years | Ties price changes to the RaaS evidentiary chain |

---

## Related SDDs

- `raas.md` — receives `pricing.price_changed` events via `append_event`; chain hash links price changes to the evidentiary record
- `identity.md` — owns `merchants` and `locations` tables referenced here
- `ildwac.md` — calls `effective_price_at` to compute margin at transaction time
- `returns.md` — calls `effective_price_at` to determine the correct refund price at original purchase timestamp
- `fox.md` — consumes current price for ecom display; calls `get_price`
- `hawk.md` — receives price anomaly signals (e.g., clearance items being re-priced upward) from pricing event stream
- `tsp-seal.md` — seals the price snapshot at transaction moment; calls `effective_price_at` as part of the seal computation
