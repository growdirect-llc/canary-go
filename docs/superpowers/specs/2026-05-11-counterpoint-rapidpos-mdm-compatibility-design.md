---
title: Counterpoint RapidPOS MDM Compatibility Design
date: 2026-05-11
status: draft
owners: product, engineering, adapters
related:
  - docs/superpowers/specs/2026-05-11-gro-901-item-scan-flow-design.md
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
  - deploy/schema/02_catalog_items.sql
  - deploy/migrations/035_catalog_evolution.up.sql
  - internal/adapters/counterpoint/parser.go
  - internal/item/store.go
external-references:
  - /Users/gclyle/GrowDirect/Brain/wiki/ncr-counterpoint-functional-decomposition.md
  - /Users/gclyle/GrowDirect/Brain/wiki/ncr-counterpoint-api-reference.md
  - /Users/gclyle/GrowDirect/Brain/wiki/canary-location-item-data-model.md
  - /Users/gclyle/GrowDirect/Brain/wiki/rapid-pos-counterpoint-user-pain-points.md
---

# Counterpoint RapidPOS MDM Compatibility Design

## Decision

Canary's item/catalog domain must be shaped as a Counterpoint/RapidPOS-compatible master data management layer, not only as a local CRUD form set. The current `catalog.items`, `catalog.product_categories`, `catalog.vendors`, `catalog.item_vendors`, `catalog.item_barcodes`, `catalog.item_packs`, and `catalog.item_serials` tables are the starting point, but compatibility requires a broader item-master contract, source-system crosswalks, and an evented pub/sub boundary that can later be split into an MCP/PIM service.

The initial implementation remains inside Canary Go. The design boundary is intentionally service-shaped: item-master mutations publish canonical events, adapters subscribe or project those events, and future `MCP_PIM` extraction should not require rewriting Counterpoint/RapidPOS adapter semantics.

## Why This Matters

Rapid Garden POS is NCR Counterpoint deployed and customized by Rapid POS. From an item-master perspective, that means Canary must match the capabilities operators already expect from Counterpoint item maintenance and Quick Items while preserving a cleaner cloud-native model.

Counterpoint's item surface includes quick item creation, full item maintenance, categories/sub-categories, item attributes and profile codes, prompt codes, stocking and alternate units, unit-specific barcodes, quantity and price decimals, tax/tare/mix-match/label codes, primary vendor and vendor item number, last cost, regular and location-specific price, grid/matrix variants, serial/lot tracking, images, URL fields, ecommerce categories, inventory locations, inventory by location, barcode management, and label/tag workflows.

GRO-901 is only the scan-to-create quick item path. It must preserve the item-master data it captures, but it does not need to implement the whole Counterpoint item master in one pass.

## Compatibility Scope

### In Scope

- Item identity: SKU/item number, description, short description, status, item type, lifecycle dates.
- Barcode identity: multiple barcodes per item, barcode type, unit quantity, primary barcode, duplicate detection.
- Category hierarchy: Counterpoint item categories and sub-categories mapped to Canary product categories.
- Supplier/vendor master and vendor-item relationships: primary vendor, vendor SKU, vendor description, order UOM, unit cost, case pack, minimum order quantity, lead time.
- Units and packs: stocking UOM, preferred/order UOM, UOM quantity, case pack, item packs, barcode UOM quantity.
- Pricing master data: regular/default price, cost, location-specific prices, and observed pricing-rule outcomes.
- Compliance and POS flags: tax class, SNAP/food stamp eligibility, age restriction, weighable/tare, discountable, tracking method.
- Custom fields: Counterpoint attributes, profile codes, prompt codes, vertical fields, and merchant-defined mappings.
- Variants and traceability: matrix/grid variants, serial tracking, lot tracking, parent/child or style/color/size structures.
- Catalog media and commerce: images, image filenames, item URL, ecommerce flags/categories.
- Inventory master bridge: stores, inventory locations, item inventory rollups, inventory-by-location counts.
- Source identity: Counterpoint item numbers, company alias, vendor refs, category refs, barcode refs, external update timestamps.
- Evented item-master boundary for future MCP/PIM extraction.

### Out of Scope for GRO-901

- Counterpoint outbound write-back.
- Full grid/matrix item maintenance.
- Label and shelf-tag printing.
- Location-specific pricing maintenance.
- Ecommerce catalog sync.
- Serial/lot assignment workflows.
- Full pricing-rule authoring or replication.
- Phase 10 RBAC.

## Current Canary Fit

### Already Modeled In Schema

`catalog.items` already carries the core SKU record: SKU, description, item type, category, UOM, UOM quantity, quantity decimals, price decimals, default price, default cost, currency, tax class, food stamp eligibility, age restriction, weighable, discountable, tracking method, mix-match code, attributes, status, status dates, and last received date.

`catalog.item_barcodes` already supports multiple scan keys per item with barcode value, barcode type, UOM quantity, primary flag, attributes, status, and tenant-scoped uniqueness.

`catalog.vendors` and `catalog.item_vendors` already support supplier master data, primary vendor links, vendor SKU, vendor description, order UOM, unit cost, case pack, minimum order quantity, lead time, country of origin, attributes, and status.

`catalog.item_packs` supports pack, bundle, kit, and mix composition at a schema level.

`catalog.item_serials` supports serial-tracked inventory units and can back Counterpoint serial endpoints.

### Partially Wired In Application Code

`internal/item.Store.Create` creates items and barcodes in one transaction. GRO-901 now passes inferred barcode type and optional primary vendor/case-pack data into the create request.

Manual and scan item setup capture supplier and case pack. Before this patch these fields were UI-only; the compatibility path is to preserve them in `catalog.item_vendors` when supplier is selected and in item attributes as scan provenance.

Category and vendor dropdowns exist. Category selection writes `category_id`; vendor selection now writes a primary item-vendor link through `CreateRequest.VendorLinks`.

### Needs Schema Or Service Design Follow-Up

Counterpoint/RapidPOS parity still needs explicit treatment for label code, tare weight code, prompt codes, item profile code families, matrix/grid variant identity, ecommerce category mappings, item images, location-specific price records, and source-system crosswalks. Some can live in `attributes` short-term, but long-term compatibility should avoid trapping known first-class MDM fields in opaque JSON.

## Canonical MDM Model

### Item Identity

Canary `catalog.items.sku` maps to Counterpoint item number / primary SKU. Barcode is not SKU; it is an item identification record. A scanned barcode may prefill SKU in quick-create flows, but the operator can change SKU before save.

Canary must preserve:

- Canary item ID.
- Tenant ID.
- Merchant SKU / item number.
- Source system code, e.g. `counterpoint`.
- Counterpoint company alias.
- Counterpoint item number.
- Source update timestamp or revision when available.

Design requirement: add a source crosswalk table or shared integration identity pattern before outbound write-back:

```sql
catalog.item_source_refs
  tenant_id
  item_id
  source_code
  source_company_alias
  source_item_ref
  source_updated_at
  attributes
  UNIQUE (tenant_id, source_code, source_company_alias, source_item_ref)
```

### Categories

Canary categories should preserve Counterpoint category codes and hierarchy. Counterpoint category/sub-category levels map to `catalog.product_categories.parent_id`, `code`, `name`, `level`, and `path`.

Compatibility rules:

- Category code is unique per tenant.
- Category hierarchy is adapter-owned on inbound sync unless Canary is configured as system of record.
- Quick item creation can use a selected category ID or store lookup category suggestions as provenance only.

### Vendors And Vendor Items

Counterpoint primary vendor, vendor item number, vendor description, order UOM, unit cost, and case pack map to `catalog.item_vendors`.

Compatibility rules:

- One active primary vendor per item.
- Vendor links must be tenant-scoped and only reference active vendors.
- Case pack belongs on the vendor item relationship when the pack is supplier/order-specific.
- Pack composition belongs in `catalog.item_packs` when the pack is itself a sellable or stockable item.

GRO-901 minimum compatibility: when an operator selects Supplier and enters Case pack, create one primary vendor link with unit cost and case pack.

### Units, Packs, And Barcodes

Counterpoint supports stocking units, alternate units, associated units, and unit-specific barcodes. Canary needs to distinguish:

- Item stocking UOM: `catalog.items.unit_of_measure`.
- Preferred/order UOM: `catalog.items.preferred_unit_of_measure` and `catalog.item_vendors.order_unit_of_measure`.
- Barcode UOM quantity: `catalog.item_barcodes.uom_quantity`.
- Vendor case pack: `catalog.item_vendors.case_pack_qty`.
- Sellable pack/bundle/kit composition: `catalog.item_packs`.

Barcode type should be preserved as `UPC_A`, `EAN_13`, `GTIN`, `ITF_14`, `DATABAR`, `PLU`, or `INTERNAL`. Length-only inference is acceptable for quick-create defaults; adapter ingest should preserve source type when available.

### Pricing Master Data

Canary should not try to replicate the full Counterpoint pricing engine in the catalog slice. The compatibility split is:

- Master/default item price: `catalog.items.default_price`.
- Master/default item cost: `catalog.items.default_cost`.
- Vendor cost: `catalog.item_vendors.unit_cost`.
- Location/customer/promotion price outputs: `pricing.observed_price_rules` until explicit pricing-maintenance screens exist.
- Future location-specific prices: use pricing-owned tables, not item attributes.

Counterpoint public user pain points show pricing rules are powerful but hard to operate. Canary's first compatibility move is visibility and observed outcomes; authoring price rules is a later Pricing module problem.

### Compliance And POS Flags

Counterpoint item maintenance includes tax category, tare weight code, quantity decimals, price decimals, weighable/random-weight behavior, and restricted/age-sensitive item handling.

Canary already has `tax_class`, `food_stamp_eligible`, `age_restriction`, `weighable`, `qty_decimals`, `price_decimals`, `is_discountable`, `tracking_method`, and `attributes`. Follow-up fields or conventions are needed for:

- Tare weight code.
- Label code.
- Prompt codes shown at POS.
- Restricted item category mapping.
- Counterpoint custom attribute slot mapping.

### Custom Attributes And Profile Codes

Counterpoint has configurable item attributes, profile codes, date/numeric/code fields, and item prompt codes. Canary can store arbitrary source fields in `catalog.items.attributes`, but compatibility needs a mapping registry so attributes are not mystery JSON.

Design requirement:

```sql
catalog.item_attribute_mappings
  tenant_id
  source_code
  source_field
  canary_field
  data_type
  display_label
  required_for_writeback
  attributes
```

This lets a merchant map Counterpoint custom fields such as restricted item flags, plant lifecycle class, or vertical compliance markers without hard-coding every VAR customization.

### Variants, Matrix, Serial, And Lot

Counterpoint grid/matrix items sell as cell combinations such as color/size/dimension. Canary should model this explicitly before claiming parity.

Recommended path:

- Use `catalog.items.attributes` for passive source snapshots in early ingest.
- Add first-class variant/group modeling before UI maintenance or write-back.
- Use `catalog.item_serials` for serial tracking.
- Add lot tracking only when a real Counterpoint payload or merchant workflow requires it.
- Keep GRO-901 related-item creation as an escape hatch, not a hidden variant relationship.

### Images, Labels, And Ecommerce

Counterpoint exposes item image endpoints and ecommerce categories. It also supports label codes, shelf tags, barcode labels, and print queues.

Canary compatibility requires:

- Image metadata and source filenames.
- Item URL and ecommerce category mapping.
- Label code and tag/label job intent.
- Explicit non-goal boundary: GRO-901 does not print labels or write ecommerce mappings.

Short-term storage can use attributes. Long-term, catalog media and ecommerce projection should be separate tables or module-owned projections.

### Inventory Locations And Item Inventory

Items are master data; stock counts are not item rows. Canary must preserve the two-axis model from the location/item data design:

- Module N: stores, stations, devices.
- Module D: inventory locations.
- Module S: items and item inventory rollups.
- Bridge: inventory-by-location for fine-grained stock counts.

Counterpoint `Items_ByLocation`, `Inventory_ByLocation`, `InventoryCost`, and `InventoryLocations` should project into store-level item inventory and inventory-location detail. Quick item creation should not invent stock counts.

## Evented Item Boundary For MCP_PIM

### Decision

Item master changes must publish events through a stable outbox/topic contract. Canary can continue to store item master data in Postgres, but every create, update, status change, barcode change, category assignment, vendor link change, and pack change should be representable as an event.

This is the future split point for a standalone MCP/PIM service. Adapters should depend on the event contract and query/read models, not on incidental web-handler behavior.

### Event Families

Use a compact catalog event vocabulary:

- `catalog.item.created`
- `catalog.item.updated`
- `catalog.item.status_changed`
- `catalog.item.category_assigned`
- `catalog.item.barcode_added`
- `catalog.item.barcode_updated`
- `catalog.item.vendor_linked`
- `catalog.item.vendor_link_updated`
- `catalog.item.pack_defined`
- `catalog.item.source_ref_linked`

### Event Payload Principles

Each event carries:

- `event_id` for idempotency.
- `tenant_id`.
- `item_id`.
- `source_code` and source reference when adapter-originated.
- `occurred_at` and `schema_version`.
- `actor_type` and `actor_id` when operator-originated.
- Changed fields only plus enough identity to route the event.
- No raw unbounded source payload unless stored as a referenced blob or redacted attributes.

### Publisher Responsibilities

The item store or service layer owns publishing after successful transaction commit. The eventual implementation should use an outbox table written in the same transaction as the item mutation, then a publisher drains to the internal topic bus.

Do not publish directly from templates or web handlers. Web handlers ask the item domain to mutate state; the item domain records and publishes canonical facts.

### Subscriber Responsibilities

Counterpoint/RapidPOS adapter subscribers use catalog events to decide whether to write back, ignore, or queue a reconciliation task. Early phases may subscribe only for logging/projections. Write-back requires explicit tenant configuration and source ownership rules.

Subscribers must be idempotent by `event_id` and source crosswalk. If the source system is still authoritative for a field, the subscriber must not push Canary-originated changes for that field.

### Extraction Path

Phase 1 keeps event code in Canary Go. Phase 2 introduces an outbox and stable event DTOs. Phase 3 exposes MCP tools over item/PIM read and command surfaces. Phase 4 can split the implementation into `MCP_PIM` with the same event contract, keeping Counterpoint/RapidPOS adapters stable.

## Inbound Sync And Outbound Write-Back

### Inbound Sync

Counterpoint remains a source for existing merchant catalog data during onboarding and periodic sync. Inbound item sync should:

1. Resolve source item ref to Canary item ID.
2. Upsert item master fields.
3. Upsert categories, barcodes, vendor links, serials, images, and ecommerce refs as available.
4. Preserve source timestamps and unknown fields in structured attributes.
5. Publish catalog events for downstream projections.

### Outbound Write-Back

Outbound write-back is not enabled by GRO-901. Before write-back, Canary needs:

- Source crosswalks.
- Field ownership rules per tenant/source.
- Idempotency keys.
- Conflict policy for source-edited records.
- Adapter capability matrix for Counterpoint REST vs UI/direct DB-only surfaces.
- Operator audit trail.

Counterpoint REST exposes item reads and related catalog endpoints, but not the full UI item maintenance surface. Some write-back may be impossible or require a customer-approved edge/direct database path. That must be explicit per field.

## Validation And Conflict Rules

- Tenant is always derived from authenticated context, never trusted from browser forms.
- SKU uniqueness is `(tenant_id, sku)`.
- Barcode uniqueness is `(tenant_id, barcode)` for active barcodes.
- Vendor links require same-tenant active vendor records.
- Only one active primary vendor per item.
- Barcode type should be preserved or inferred; never silently collapse barcode to SKU.
- Case pack must be a positive integer when supplied.
- Source refs are unique by tenant/source/company/source item ref.
- Source-owned fields require conflict checks before Canary-originated writes.

## Phase Backlog

### Phase A: GRO-901 Compatibility Patch

- Preserve inferred barcode type in scan-created barcode rows.
- Preserve selected supplier and case pack as a primary `catalog.item_vendors` link.
- Preserve lookup and operational provenance in item attributes.
- Keep no-draft, final-save-only behavior.

### Phase B: Source Crosswalk And MDM Mapping

- Add `catalog.item_source_refs`.
- Add item/category/vendor source-ref ingestion rules.
- Add item attribute mapping registry for Counterpoint custom/profile fields.
- Add tests for source-ref idempotency and duplicate source records.

### Phase C: Evented Catalog Boundary

- Add catalog outbox table or reuse platform outbox if standardized.
- Publish item, barcode, vendor-link, category, pack, and source-ref events.
- Add idempotent event consumers for projections.
- Document MCP/PIM event contract.

### Phase D: Counterpoint Catalog Ingest Parity

- Ingest `GET_Item`, `GET_Items`, `GET_ItemCategories`, `GET_VendorItem`, `GET_Item_Images`, `GET_EC`, and inventory-location catalog endpoints.
- Preserve custom/profile fields through mapping registry.
- Populate inventory-location and item-inventory bridges from Counterpoint inventory endpoints.

### Phase E: Operator MDM Screens

- Extend item maintenance beyond quick-create: vendor tab, barcodes tab, UOM/pack tab, compliance/POS flags, source refs, and provenance.
- Keep screens compact and operational, not marketing-style.
- Add field-level source ownership indicators before write-back.

### Phase F: Controlled Write-Back / MCP_PIM

- Add field ownership config.
- Add outbound adapter queue with retry and idempotency.
- Expose MCP tools for item lookup, item create/update commands, barcode assignment, vendor link updates, and source reconciliation.
- Split item/PIM service only after event contract and read models are stable.

## Acceptance Criteria

- GRO-901 creates scan items without losing supplier, case pack, or barcode type.
- Canary has a written MDM compatibility target for Counterpoint/RapidPOS.
- The spec distinguishes current schema support, application wiring, and future schema/service work.
- The spec names the pub/sub boundary needed for eventual MCP_PIM extraction.
- Future tickets can be cut directly from the phase backlog without rediscovering the Counterpoint surface.

## Self-Review

- Placeholder scan: no TBD/TODO placeholders remain.
- Scope check: this spec defines MDM compatibility and phased implementation; it does not attempt to implement full write-back in GRO-901.
- Consistency check: GRO-901 remains a quick item scan flow; full Counterpoint/RapidPOS parity is a separate MDM backlog.
- Ambiguity check: source-system ownership, event boundary, and out-of-scope areas are explicitly called out.
