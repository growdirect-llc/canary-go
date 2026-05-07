---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: item
port: 8090
mcp-server: canary-item
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Item

**Type:** Reference Data Service — Item Master  
**Binary:** `cmd/item` → `:8090`  
**MCP server:** `canary-item` (9 tools)  
**Depends on:** `identity`, `settings` (field registry)  
**Feeds:** `inventory-as-a-service` (item reference), `ecom-channel` (catalog sync), `ildwac` (item cost basis), `owl` (searchable item catalog)

The item master is the canonical source of truth for what a merchant sells. Without it, every system holds its own version of a product — the POS has one SKU, the website has another, the receiving dock has a third. Canary's item master is the single record that all modules reference. When an item changes — price, UPC, category — it changes once here and propagates everywhere. The item service is the least glamorous module in the spine and the one whose absence breaks everything else.

> **Solex is illustrative:** The catalog ingestion model in `Solex/` is one concrete realization of an item master — `solex/services/catalog_import.py` (loads `catalog/products.yaml`), `catalog.py`, `catalog_sync.py`, and the `admin_catalog` routes. The product YAML at `Solex/catalog/products.yaml` (4 categories, full SKU/slug/price/image/weight/inventory shape) shows what one item record looks like in practice. Read it for the operational shape, not as a copy target — Solex is single-tenant single-channel; `cmd/item` is the platform item master. See ecom-channel.md → "Solex Port-Forward Inventory" for the full map.

> **Assortment metadata is part of the item master.** Each item carries per-store assortment tier — `store`, `warehouse`, or `expanded` (special order) — that governs how IaaS computes availability and how ecom-channel routes fulfillment. This is the data backbone for the multi-tier assortment model: a store carries its regular SKUs (store tier), can ship from a central warehouse for items it does not stock (warehouse tier), and can special-order vendor items it does not carry at all (expanded tier). The item record owns the tier mapping; IaaS owns the stock; commercial owns the vendor-can-drop-ship promise. See inventory-as-a-service.md → "Multi-Tier Assortment Model" for the full contract.

**Multi-tenant context.** Item master tables (`items`, `item_attributes`, `item_assortment_tiers`, `item_substitutes`) live per-tenant in `tenant_{merchant_id}`. Every merchant has their own catalog; the same physical SKU sold by two merchants on the platform appears as separate item records. Cross-tenant catalog analytics (category penetration benchmarks) flow through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** The item master operates with all Optional Features (per `platform-overview.md`) disabled. Item lifecycle, assortment tier mapping, and downstream propagation work entirely on internal records. When `ILDWAC_ENABLED=true`, item activation creates the cost basis seed for the five-dimension provenance ledger — when off, the standard ILWAC cost basis applies via existing MAC infrastructure. When `VENDOR_CONTRACTS_ENABLED=true`, item activation deploys the agreed cost / allowance terms to the vendor's smart contract on the AVAX private subnet. Multi-tier assortment routing operates regardless of flag state — the data lives in the item record either way.

---

## Business

### The Item Data Problem

SMB merchants running Counterpoint or Square typically have item data spread across three to five places: the POS item database, a spreadsheet the buyer maintains, whatever the website platform imported last year, and the vendor's EDI catalog that lives in someone's email. None of these agree on price, cost, or active/discontinued status. Canary's item master does not replace the POS — it is a live-synchronized mirror that the platform's agent layer can query reliably. The POS remains the system of record for the cashier; the item master is the system of record for Canary's analytics and receiving modules.

### Key Concepts

**Item vs Variant.** A parent item (e.g., "Women's Trail Running Shoe") has child variants (size 7/8/9/10, color Slate/Navy). Each variant is its own item row with its own SKU, its own barcode, and its own inventory position. The parent item carries the shared attributes (department, class, description). Variants carry the differentiating attributes in `variant_attributes` JSONB.

**Category Hierarchy.** Department → Class → Subclass. Three levels, configurable per merchant. A sporting goods merchant may use Department: Footwear / Class: Athletic / Subclass: Trail Running. A gift store may use entirely different taxonomy. The hierarchy is stored in normalized tables; the item row carries foreign keys to all three levels.

**External Mappings.** A Counterpoint item has an `item_code`; a Square catalog item has a `catalog_object_id`. Both are stored in `item_external_ids` with a provider tag. This table is the bridge that allows `scan_item` in Receiving to accept barcodes from any POS source and resolve to a single canonical item_id. It is also the sync target for ecom-channel — when a Shopify product is linked, its variant ID is stored here.

**Soft Delete Only.** Items with any transaction, receiving, or inventory history must never be hard-deleted. `deactivate_item` sets `active = false`. Historical records that reference `item_id` remain intact and queryable. This is an unconditional rule — see Compliance.

### Business Rules

1. SKU is unique per merchant. Two merchants may share a SKU string; within a single merchant's namespace, SKUs must be unique.
2. A variant must reference a valid parent item. A parent item may have zero or more variants. A variant cannot itself be a parent.
3. `default_unit_cost_sats` and `default_retail_price_sats` are the item master values. Actual transaction prices and receiving costs may differ — those are stored on their respective records. The item master cost is the default for new POs and for ILDWAC when no PO cost is available.
4. `serialized = true` means each unit of this item carries a unique serial number (e.g., firearms, high-value electronics). Inventory for serialized items uses the serial device type in inventory-as-a-service, not the quantity device type.
5. The barcode lookup (`lookup_barcode`) is the hot path — it is called by the POS integration on every scan. It must return in under 50ms P99. It is Valkey-cached.
6. Any change to `default_retail_price_sats` triggers a downstream event to ecom-channel so the website price can be synchronized. Price changes are not silently absorbed.
7. `taxable` defaults to true. Merchants configure tax-exempt categories at the settings level; individual item overrides are possible but uncommon.

---

## Technical

### Service Boundaries

The item service owns all item reference data. It does not own inventory positions (inventory-as-a-service), transaction records (tsp), or category analytics (hawk/fox). Downstream services that need item data call this service's read endpoints or the MCP tools — they do not write to the items tables directly.

| Owned Tables | Purpose |
|---|---|
| `items` | Item master — root and variant items |
| `item_barcodes` | All scan codes for an item |
| `item_external_ids` | POS provider mappings (Square, Counterpoint, Shopify) |
| `item_departments` | Merchant-specific department taxonomy |
| `item_classes` | Class within department |
| `item_subclasses` | Subclass within class |

### Data Model

```sql
CREATE TABLE items (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id               UUID NOT NULL REFERENCES merchants(id),
    sku                       TEXT NOT NULL,
    name                      TEXT NOT NULL,
    description               TEXT,
    department_id             UUID REFERENCES item_departments(id),
    class_id                  UUID REFERENCES item_classes(id),
    subclass_id               UUID REFERENCES item_subclasses(id),
    parent_item_id            UUID REFERENCES items(id),
    -- NULL for root items; set for variants
    is_variant                BOOLEAN NOT NULL DEFAULT false,
    variant_attributes        JSONB,
    -- {size: "M", color: "Red"} — only set when is_variant = true
    default_unit_cost_sats    BIGINT NOT NULL DEFAULT 0,
    default_retail_price_sats BIGINT NOT NULL DEFAULT 0,
    taxable                   BOOLEAN NOT NULL DEFAULT true,
    serialized                BOOLEAN NOT NULL DEFAULT false,
    active                    BOOLEAN NOT NULL DEFAULT true,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, sku),
    CONSTRAINT variant_has_parent CHECK (
        (is_variant = false AND parent_item_id IS NULL)
        OR
        (is_variant = true AND parent_item_id IS NOT NULL)
    )
);

CREATE INDEX idx_items_merchant_active ON items(merchant_id, active);
CREATE INDEX idx_items_parent ON items(parent_item_id) WHERE parent_item_id IS NOT NULL;
CREATE INDEX idx_items_department ON items(department_id);
-- Full-text search index for search_items
CREATE INDEX idx_items_fts ON items USING GIN(to_tsvector('english', name || ' ' || COALESCE(description, '')));

CREATE TABLE item_barcodes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id       UUID NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    barcode       TEXT NOT NULL,
    barcode_type  TEXT NOT NULL DEFAULT 'UPC',
    -- barcode_type values: UPC | EAN | QR | internal
    is_primary    BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(item_id, barcode)
);

-- Barcode lookup is the hot path; index on barcode text for cross-item scan
CREATE INDEX idx_item_barcodes_barcode ON item_barcodes(barcode);
-- Constraint: only one primary barcode per item
CREATE UNIQUE INDEX idx_item_barcodes_primary ON item_barcodes(item_id) WHERE is_primary = true;

CREATE TABLE item_external_ids (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id     UUID NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    -- provider values: square | counterpoint | shopify
    external_id TEXT NOT NULL,
    UNIQUE(item_id, provider)
);

CREATE INDEX idx_item_external_ids_provider ON item_external_ids(provider, external_id);

CREATE TABLE item_departments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    code        TEXT NOT NULL,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, code)
);

CREATE TABLE item_classes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    department_id UUID NOT NULL REFERENCES item_departments(id) ON DELETE CASCADE,
    code          TEXT NOT NULL,
    name          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(department_id, code)
);

CREATE TABLE item_subclasses (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id  UUID NOT NULL REFERENCES item_classes(id) ON DELETE CASCADE,
    code      TEXT NOT NULL,
    name      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(class_id, code)
);
```

### Design Trade-offs

**JSONB for variant_attributes vs a variants table.** The alternative is a dedicated `item_variants` table with typed columns for common variant dimensions (size, color, width). The JSONB approach is chosen here for flexibility — different merchants use radically different variant taxonomies (a shoe retailer uses size+width+color; a food merchant uses weight+pack_size+organic). A typed variants table would require either a very wide table with mostly NULL columns or a per-merchant schema, both of which create operational complexity. The trade-off is that variant_attributes is not queryable with standard SQL predicates without GIN indexing. The `search_items` tool handles variant search via the GIN full-text index on name, not on variant_attributes directly. If a specific merchant needs variant-attribute filtering (e.g., "show all items in size 9"), that is a Phase 2 feature backed by a GIN index on the JSONB column.

**CASCADE delete on item_barcodes and item_external_ids.** These tables use ON DELETE CASCADE from items(id). In practice, items are never hard-deleted (see Compliance). The cascade exists to handle the edge case where a test item or data quality correction requires a hard delete under DBA supervision. In production, the application layer enforces soft-delete only.

**variant_has_parent CHECK constraint.** The constraint enforces the invariant at the DB level: a non-variant item has no parent; a variant item must have a parent. This is the kind of rule that starts as application-layer validation and silently breaks when a migration or import script bypasses the app. The DB constraint is the right level for a data integrity rule of this consequence.

**Separate index for primary barcode.** `CREATE UNIQUE INDEX ... WHERE is_primary = true` is a partial unique index. It enforces that each item has at most one primary barcode while allowing multiple non-primary barcodes. This is cleaner than a trigger and more efficient than a unique constraint on a nullable column.

**Full-text search via GIN vs pgvector similarity.** `search_items` uses PostgreSQL GIN full-text search for name and description, not pgvector semantic similarity. The rationale: item search in a retail context is typically exact or prefix-based (cashier types "trail shoe 9W", not a semantic query). GIN FTS handles this with sub-10ms latency at SMB catalog scale (< 100K items). pgvector semantic search would be appropriate for a buyer-facing discovery surface, which is Owl's job — not this service's.

### Barcode Lookup — Hot Path

The dock scan, POS transaction, and returns flows all call `lookup_barcode`. This is the highest-frequency read in the item service.

```
lookup_barcode(barcode, merchant_id):

1. Check Valkey: GET item:barcode:{merchant_id}:{barcode}
   → HIT: return cached {item_id, item} (TTL 10 minutes)
   → MISS: continue

2. SELECT i.*, ib.barcode_type
   FROM items i
   JOIN item_barcodes ib ON ib.item_id = i.id
   WHERE ib.barcode = $1 AND i.merchant_id = $2 AND i.active = true
   LIMIT 1
   → FOUND: cache result in Valkey (EX 600); return 200
   → NOT FOUND: return 404 {found: false}

3. On 404: caller decides — POS may prompt cashier for manual entry
```

Cache invalidation: when `add_barcode`, `update_item`, or `deactivate_item` is called, the handler deletes `item:barcode:{merchant_id}:*` for the affected item's barcodes. Since each item typically has 1–3 barcodes, explicit key deletion is cheaper than a pattern-based flush.

### Price Change Events

When `update_item` modifies `default_retail_price_sats`:

```
1. Persist the update
2. Check item_external_ids for provider = 'shopify' or provider = 'square'
3. For each found external mapping: publish price_change event to ecom-channel
   via a non-blocking goroutine + Valkey retry queue
4. Return 200 immediately — do not wait for ecom-channel confirmation
```

The merchant's POS is the pricing authority; ecom-channel is a downstream consumer. A failed ecom-channel sync does not roll back the item master update.

### API Contract

All routes require JWT auth except `/item/healthz` and `/item/readyz`.

```
GET  /item/healthz                            → 200
GET  /item/readyz                             → 200 | 503
POST /item/items                              → 201 {item_id}
POST /item/items/{item_id}/variants           → 201 {item_id}
POST /item/items/{item_id}/barcodes           → 201 {barcode_id}
GET  /item/items/{item_id}                    → 200 {item, variants, barcodes, external_ids}
GET  /item/lookup?barcode=&merchant_id=       → 200 | 404
GET  /item/search?merchant_id=&q=             → 200 [{item}]
POST /item/items/{item_id}/external           → 200 {mapped: bool}
PUT  /item/items/{item_id}                    → 200 {updated_at}
PUT  /item/items/{item_id}/deactivate         → 200 {active: false}
```

### MCP Tool Surface — `canary-item` (9 tools)

| Tool | Input | Output | Notes |
|---|---|---|---|
| `create_item` | merchant_id, sku, name, department_id, price_sats, cost_sats, taxable?, serialized? | {item_id} | Creates root item; sets is_variant = false |
| `add_variant` | parent_item_id, sku, variant_attributes, price_sats, cost_sats? | {item_id} | Creates variant; inherits department/class/subclass from parent |
| `add_barcode` | item_id, barcode, barcode_type, is_primary? | {barcode_id} | Adds scan code; enforces single-primary constraint |
| `lookup_barcode` | barcode, merchant_id | {item_id, item} | POS scan lookup; Valkey-cached; primary hot path |
| `get_item` | item_id | {item, variants, barcodes, external_ids} | Full item record including all child variants |
| `search_items` | merchant_id, query | [{item}] | GIN full-text search on name + description; returns active items only |
| `sync_external` | item_id, provider, external_id | {mapped: bool} | Upsert provider mapping; idempotent |
| `update_item` | item_id, fields{} | {updated_at} | Price, cost, name, description changes; triggers ecom-channel event on price change |
| `deactivate_item` | item_id | {active: false} | Soft delete only; clears barcode cache; does not cascade to inventory or transactions |

### Go Implementation Notes

- `create_item` and `add_variant` should accept an optional `barcodes[]` array so callers can create an item and its primary barcode in a single call. Internally this is two INSERTs in one transaction, not two separate tool calls.
- `lookup_barcode` must use a read-only DB connection pool (replica) for cache-miss DB queries. This is the highest-read-rate path in the service; do not send it to the primary.
- `search_items` is capped at 100 results. Callers paginating a large catalog should use `list_items` (not specified here — a future Phase 2 cursor-based endpoint). The FTS query uses `plainto_tsquery` not `to_tsquery` — cashiers and buyers do not write boolean query syntax.
- `deactivate_item` must check for active inventory positions before deactivating. If the item has on-hand quantity > 0 in any location, the tool returns a warning (not an error) and proceeds — the merchant is responsible for clearing inventory, but the deactivation should not be blocked.
- `update_item` accepts a partial fields map, not a full item replacement. Use `sqlc` named partial update queries or build the SET clause dynamically. Do not allow callers to overwrite `merchant_id`, `is_variant`, or `parent_item_id` via this tool.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|---|---|---|---|---|
| `lookup_barcode` (Valkey hit) | <5ms | <20ms | 50ms | Alert + DB fallback |
| `lookup_barcode` (DB fallback) | <20ms | <80ms | 200ms | Alert |
| `search_items` | <50ms | <200ms | 1s | Alert |
| `create_item` | <200ms | <500ms | 2s | Alert |
| `get_item` | <50ms | <200ms | 1s | Alert |

### Health Endpoints

```
GET /item/healthz  → 200 {"status": "ok"}  (shallow liveness)

GET /item/readyz   → 200 | 503
{
  "status": "ok",
  "db_ok": true,
  "valkey_ok": true,
  "item_count": 48291
}
```

`item_count` in the readyz response is a cached counter (updated every 5 minutes), not a live COUNT(*). Live count queries on large catalogs are expensive and the readyz check is called frequently.

### Failure Modes

| Failure | Behavior | Recovery |
|---|---|---|
| DB unreachable | Reads served from Valkey cache for cached lookups; other reads return 503 | Auto-recover on pgx pool reconnect |
| Valkey unreachable | All reads fall through to DB; `lookup_barcode` P99 degrades to ~80ms | Log warning; continue operating |
| ecom-channel unreachable | Price change event queued in Valkey; update_item returns 200 | Async retry worker drains queue |
| Duplicate barcode on add_barcode | Returns 409 Conflict with existing item_id | Caller decides: update existing or reject |

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|---|---|---|
| `item:barcode:{merchant_id}:{barcode}` | 600s | Barcode lookup cache |
| `item:ecom:pending` | None | Price change event retry queue (list) |
| `item:count:{merchant_id}` | 300s | Cached item count for readyz |

### Monitoring

Alert on:
- `lookup_barcode` P99 > 50ms sustained for 2 minutes (barcode cache miss rate spike)
- `item:ecom:pending` queue depth > 100 (ecom-channel sync backlog)
- Any `create_item` that generates a duplicate SKU error spike (bulk import gone wrong)

---

## Compliance

### PII Classification

The item master contains no direct PII. `associate_id` fields appear in downstream records (receiving_events) that reference item_id, but not in the item tables themselves.

| Field | Classification | Treatment |
|---|---|---|
| `default_unit_cost_sats` | Confidential (commercial pricing) | Exclude from public-facing catalog exports |
| `variant_attributes` | Non-sensitive | No special treatment |

### Soft Delete Invariant

`items` rows with any foreign key reference from transaction, receiving, inventory, or chain records must never be hard-deleted. Enforce at the application layer and document in the DBA runbook:

```sql
-- Do NOT run this without verifying zero FK references
-- Use deactivate_item instead
DELETE FROM items WHERE id = $1;  -- BLOCKED: use active = false
```

The cascade from items to item_barcodes and item_external_ids exists for test data cleanup under DBA supervision only. Any migration that adds a hard-delete path to items must be reviewed by the Compliance agent.

### Retention

| Table | Minimum Retention | Authority |
|---|---|---|
| `items` | Indefinite (while referenced) | Data integrity |
| `item_barcodes` | Same as parent item | Data integrity |
| `item_external_ids` | Same as parent item | Cross-system traceability |
| `item_departments/classes/subclasses` | Indefinite | Reporting taxonomy |

---

## Related SDDs

- `inventory-as-a-service.md` — references item_id for all quantity positions; item existence validated at position creation
- `receiving.md` — `scan_item` calls `lookup_barcode` on every dock scan; `create_po` validates item_ids on line items
- `ecom-channel.md` — receives price change and catalog sync events; uses `item_external_ids` for provider mapping
- `ildwac.md` — uses `default_unit_cost_sats` as fallback when no PO-specific cost is available
- `owl.md` — indexes item master for semantic search and cross-source entity resolution
- `three-way-match.md` — resolves item_id on vendor invoice lines against the item master
- `settings.md` — provides the field registry for merchant-specific item field configurations
