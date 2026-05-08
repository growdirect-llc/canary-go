-- 02_m_items.sql — Item Domain (M / merchandising)
-- Source: docs/sdds/go-handoff/canonical-data-model.md §3 (lines 700-1075)
-- Schema: m
-- Depends on: 00_schemas.sql, 01_app_foundation.sql (app.tenants)
-- NOTE: SDD declares catalog.items BEFORE catalog.product_categories with an inline FK.
-- Postgres rejects forward references — catalog.items.category_id is created as
-- a plain uuid column here, with the FK added via ALTER TABLE at end of file.

-- catalog.items — master record for everything sold; ARTS Item ODM (Item/SKU/Style consolidated)
CREATE TABLE catalog.items (
  id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id                 uuid NOT NULL REFERENCES app.tenants(id),
  sku                       text NOT NULL,                              -- merchant's primary SKU
  description               text NOT NULL,                              -- shelf-name (full-text indexed)
  short_description         text,                                       -- receipt-name
  item_type                 text NOT NULL DEFAULT 'standard',           -- standard | service | giftcard | tare | pack | bundle
  category_id               uuid,                                       -- FK to catalog.product_categories(id) added at end of file
  unit_of_measure           text NOT NULL DEFAULT 'EA',                 -- EA | LB | KG | OZ | GAL | etc. (UN/ECE Recommendation 20)
  preferred_unit_of_measure text,                                       -- display/order default UOM when ≠ stocking; GRO-880 P1 #10 (CP IM_ITEM.PREF_UNIT)
  uom_quantity              numeric(10,4) NOT NULL DEFAULT 1,           -- employee.g., 0.5 LB per unit
  qty_decimals              smallint NOT NULL DEFAULT 0,                -- POS qty decimals (0 whole-unit, 2-4 weighed); GRO-880 P0 #4
  price_decimals            smallint NOT NULL DEFAULT 2,                -- POS price decimals; GRO-880 P0 #4
  default_price             numeric(12,4),                              -- catalog price; per-location overrides in pricing.item_prices
  default_cost              numeric(12,4),                              -- last-known cost; vendor-specific in catalog.item_vendors
  default_currency          text NOT NULL DEFAULT 'USD',                -- ISO 4217
  tax_class                 text,                                       -- tax classification key (lookup in pricing.tax_classes)
  food_stamp_eligible       boolean NOT NULL DEFAULT false,             -- US SNAP/EBT
  age_restriction           int,                                        -- minimum buyer age (alcohol, tobacco, Rx)
  weighable                 boolean NOT NULL DEFAULT false,             -- requires scale at POS
  is_discountable           boolean NOT NULL DEFAULT true,              -- discount-application flag; GRO-880 P1 #9
  tracking_method           text NOT NULL DEFAULT 'none',               -- none | serial | lot — read path uses to decide whether to look at catalog.item_serials; GRO-880 P0 #3
  mix_match_code            text,                                       -- mix-and-match group; items sharing this code qualify together for "buy any 3 for $10" deals; GRO-880 P1 #8
  attributes                jsonb NOT NULL DEFAULT '{}',                -- style variants (color, size), vertical fields (Rx NDC, food calories), CP ATTR_COD_1/2 + ADDL_DESCR_1/2/3, merchant-defined
  status                    text NOT NULL DEFAULT 'active',             -- active | discontinued | seasonal | hidden | draft | on_trial | phase_out | inactive — Canary lifecycle adds draft / on_trial / phase_out / inactive over the Counterpoint-source set; sync rounds Canary states to active/inactive at the boundary; GRO-880
  status_changed_at         timestamptz,                                -- last status transition; GRO-880 P1 #13
  last_received_at          timestamptz,                                -- last received-at-receiving; GRO-880 P1 #14
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sku)
);

CREATE INDEX idx_items_tenant ON catalog.items(tenant_id);
CREATE INDEX idx_items_category ON catalog.items(category_id);
CREATE INDEX idx_items_status ON catalog.items(status) WHERE status != 'active';
CREATE INDEX idx_items_description_trgm ON catalog.items USING gin(description gin_trgm_ops);
CREATE INDEX idx_items_attributes ON catalog.items USING gin(attributes);

-- catalog.product_categories — recursive merchandise hierarchy (ARTS MerchandiseHierarchy collapsed)
CREATE TABLE catalog.product_categories (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES catalog.product_categories(id),  -- NULL for root
  code            text NOT NULL,                              -- merchant or POS-native category code
  name            text NOT NULL,
  level           int NOT NULL,                               -- depth (0=root); denormalized for query speed
  path            ltree,                                      -- materialized path for subtree queries (Postgres ltree)
  attributes      jsonb NOT NULL DEFAULT '{}',                -- merchant-defined (employee.g., margin tier, demand class)
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_categories_tenant ON catalog.product_categories(tenant_id);
CREATE INDEX idx_categories_parent ON catalog.product_categories(parent_id);
CREATE INDEX idx_categories_path ON catalog.product_categories USING gist(path);

-- catalog.vendors — supplier master with dual lineage (financial + operational); ARTS Vendor (Party subtype)
CREATE TABLE catalog.vendors (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  vendor_code     text NOT NULL,                          -- merchant-assigned or POS-native
  name            text NOT NULL,                          -- legal/trading name
  short_name      text,
  vendor_type     text NOT NULL DEFAULT 'supplier',       -- supplier | manufacturer | distributor | broker | dropship
  primary_contact jsonb DEFAULT '{}',                     -- {name, email, phone, fax}
  address         jsonb DEFAULT '{}',                     -- {line1, line2, city, region, postal_code, country, timezone}
  payment_terms   text,                                   -- 'NET30' | 'COD' | 'PREPAY' | etc.
  currency        text DEFAULT 'USD',                     -- ISO 4217
  tax_id          text,                                   -- EIN/VAT/TIN — sensitive (PII tier 2)
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',         -- active | inactive | hold
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, vendor_code)
);

CREATE INDEX idx_vendors_tenant ON catalog.vendors(tenant_id);
CREATE INDEX idx_vendors_status ON catalog.vendors(status) WHERE status != 'active';
CREATE INDEX idx_vendors_name_trgm ON catalog.vendors USING gin(name gin_trgm_ops);

-- catalog.item_vendors — item-to-vendor association with cost / lead time; ARTS ItemVendor
CREATE TABLE catalog.item_vendors (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  vendor_id           uuid NOT NULL REFERENCES catalog.vendors(id) ON DELETE RESTRICT,
  vendor_sku             text,                              -- vendor's identifier for the item
  vendor_description     text,                              -- vendor's catalog description
  order_unit_of_measure  text,                              -- order UOM — drives PO unit conversions when ≠ item.unit_of_measure; GRO-880 P1 #11 (CP VendorItem.ORD_UNIT)
  unit_cost              numeric(12,4),                     -- vendor's per-unit cost
  case_pack_qty          int DEFAULT 1,                     -- units per case
  min_order_qty       int DEFAULT 1,
  lead_time_days      int,
  is_primary          boolean NOT NULL DEFAULT false,    -- the default vendor for this item
  country_of_origin   text,                              -- ISO 3166 alpha-2
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, vendor_id),
  CONSTRAINT one_primary_per_item EXCLUDE (item_id WITH =) WHERE (is_primary = true AND status = 'active')
);

CREATE INDEX idx_item_vendors_tenant ON catalog.item_vendors(tenant_id);
CREATE INDEX idx_item_vendors_item ON catalog.item_vendors(item_id);
CREATE INDEX idx_item_vendors_vendor ON catalog.item_vendors(vendor_id);
CREATE INDEX idx_item_vendors_primary ON catalog.item_vendors(item_id) WHERE is_primary = true;

-- catalog.item_barcodes — scan-key lookup (UPC/EAN/GTIN); ARTS Item Identification
CREATE TABLE catalog.item_barcodes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  item_id         uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  barcode         text NOT NULL,                       -- the scan-string (UPC-A 12, EAN-13, GTIN-14, ITF-14, GS1 DataBar)
  barcode_type    text NOT NULL DEFAULT 'GTIN',        -- GTIN | UPC_A | EAN_13 | ITF_14 | DATABAR | INTERNAL | PLU
  uom_quantity    numeric(10,4) NOT NULL DEFAULT 1,    -- units this barcode represents (case = 12, individual = 1)
  is_primary      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, barcode)
);

CREATE INDEX idx_barcodes_tenant ON catalog.item_barcodes(tenant_id);
CREATE INDEX idx_barcodes_item ON catalog.item_barcodes(item_id);
CREATE UNIQUE INDEX idx_barcodes_lookup ON catalog.item_barcodes(tenant_id, barcode) WHERE status = 'active';

-- catalog.item_packs — pack composition (optional; used by pack-aware merchants); ARTS Item Composition
CREATE TABLE catalog.item_packs (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  pack_item_id      uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,    -- the parent (case/bundle)
  component_item_id uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,   -- the child (each unit)
  quantity          numeric(10,4) NOT NULL,                                    -- employee.g., 12 for "12-pack"
  pack_type         text NOT NULL DEFAULT 'case',                              -- case | bundle | kit | mix
  attributes        jsonb NOT NULL DEFAULT '{}',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, pack_item_id, component_item_id)
);

CREATE INDEX idx_packs_tenant ON catalog.item_packs(tenant_id);
CREATE INDEX idx_packs_pack ON catalog.item_packs(pack_item_id);
CREATE INDEX idx_packs_component ON catalog.item_packs(component_item_id);


-- catalog.item_serials — serial-tracked inventory units; backs Counterpoint SN_SER (GRO-880 P1 #7 / GRO-884)
CREATE TABLE catalog.item_serials (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES app.tenants(id),
  item_id     uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  serial_no   text NOT NULL,
  -- nullable: not all serials are location-bound at a given time
  -- (e.g., in-transit between stores). FK to location.locations
  -- kept loose to avoid forward-reference pattern across schema files.
  location_id uuid,
  status      text NOT NULL DEFAULT 'in_stock',                -- in_stock | sold | rma | warranty | reserved
  received_at timestamptz,
  sold_at     timestamptz,
  cost        numeric(12,4),
  attributes  jsonb NOT NULL DEFAULT '{}',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, serial_no)
);

CREATE INDEX idx_item_serials_tenant_status ON catalog.item_serials(tenant_id, status);
CREATE INDEX idx_item_serials_item ON catalog.item_serials(item_id);


-- catalog.import_jobs — bulk-catalog-import lifecycle; backs Flow B per
-- canary-item-setup-screen-decomp.md (GRO-877 OQ #2 / GRO-884).
CREATE TABLE catalog.import_jobs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  -- nullable so generic imports without a specific vendor work
  supplier_id     uuid,
  status          text NOT NULL DEFAULT 'queued',                  -- queued | validating | ready | committing | finalized | cancelled
  file_uri        text,                                            -- where the upload landed (S3/GCS/local-volume)
  file_name       text,
  column_mapping  jsonb NOT NULL DEFAULT '{}',                     -- file-column → Canary-field map (B2)
  summary         jsonb NOT NULL DEFAULT '{}',                     -- per-row outcomes (B5)
  row_count       int NOT NULL DEFAULT 0,
  rows_imported   int NOT NULL DEFAULT 0,
  rows_skipped    int NOT NULL DEFAULT 0,
  started_by      uuid,                                            -- user who initiated
  created_at      timestamptz NOT NULL DEFAULT now(),
  finalized_at    timestamptz
);

CREATE INDEX idx_import_jobs_tenant_status ON catalog.import_jobs(tenant_id, status);
-- Hot path for the "resume in-flight job" UX on next visit to /suppliers/{id}/import.
CREATE INDEX idx_import_jobs_active
  ON catalog.import_jobs(tenant_id, created_at DESC)
  WHERE status IN ('queued', 'validating', 'ready', 'committing');


-- ─────────────────────────────────────────────────────────────────────
-- Forward-declared FKs (Postgres requires referenced table to exist first)
-- ─────────────────────────────────────────────────────────────────────
ALTER TABLE catalog.items
  ADD CONSTRAINT fk_items_category
  FOREIGN KEY (category_id) REFERENCES catalog.product_categories(id);
