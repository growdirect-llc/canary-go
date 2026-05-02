-- 02_m_items.sql — Item Domain (M / merchandising)
-- Source: docs/sdds/go-handoff/canonical-data-model.md §3 (lines 700-1075)
-- Schema: m
-- Depends on: 00_schemas.sql, 01_app_foundation.sql (app.tenants)
-- NOTE: SDD declares m.items BEFORE m.product_categories with an inline FK.
-- Postgres rejects forward references — m.items.category_id is created as
-- a plain uuid column here, with the FK added via ALTER TABLE at end of file.

-- m.items — master record for everything sold; ARTS Item ODM (Item/SKU/Style consolidated)
CREATE TABLE m.items (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sku                 text NOT NULL,                                 -- merchant's primary SKU
  description         text NOT NULL,                                 -- shelf-name (full-text indexed)
  short_description   text,                                          -- receipt-name
  item_type           text NOT NULL DEFAULT 'standard',              -- standard | service | giftcard | tare | pack | bundle
  category_id         uuid,                                          -- FK to m.product_categories(id) added at end of file
  unit_of_measure     text NOT NULL DEFAULT 'EA',                    -- EA | LB | KG | OZ | GAL | etc. (UN/ECE Recommendation 20)
  uom_quantity        numeric(10,4) NOT NULL DEFAULT 1,              -- e.g., 0.5 LB per unit
  default_price       numeric(12,4),                                 -- catalog price; per-location overrides in p.item_prices
  default_cost        numeric(12,4),                                 -- last-known cost; vendor-specific in m.item_vendors
  default_currency    text NOT NULL DEFAULT 'USD',                   -- ISO 4217
  tax_class           text,                                          -- tax classification key (lookup in p.tax_classes)
  food_stamp_eligible boolean NOT NULL DEFAULT false,                -- US SNAP/EBT
  age_restriction     int,                                           -- minimum buyer age (alcohol, tobacco, Rx)
  weighable           boolean NOT NULL DEFAULT false,                -- requires scale at POS
  attributes          jsonb NOT NULL DEFAULT '{}',                   -- style variants (color, size), vertical fields (Rx NDC, food calories), merchant-defined
  status              text NOT NULL DEFAULT 'active',                -- active | discontinued | seasonal | hidden
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sku)
);

CREATE INDEX idx_items_tenant ON m.items(tenant_id);
CREATE INDEX idx_items_category ON m.items(category_id);
CREATE INDEX idx_items_status ON m.items(status) WHERE status != 'active';
CREATE INDEX idx_items_description_trgm ON m.items USING gin(description gin_trgm_ops);
CREATE INDEX idx_items_attributes ON m.items USING gin(attributes);

-- m.product_categories — recursive merchandise hierarchy (ARTS MerchandiseHierarchy collapsed)
CREATE TABLE m.product_categories (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES m.product_categories(id),  -- NULL for root
  code            text NOT NULL,                              -- merchant or POS-native category code
  name            text NOT NULL,
  level           int NOT NULL,                               -- depth (0=root); denormalized for query speed
  path            ltree,                                      -- materialized path for subtree queries (Postgres ltree)
  attributes      jsonb NOT NULL DEFAULT '{}',                -- merchant-defined (e.g., margin tier, demand class)
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_categories_tenant ON m.product_categories(tenant_id);
CREATE INDEX idx_categories_parent ON m.product_categories(parent_id);
CREATE INDEX idx_categories_path ON m.product_categories USING gist(path);

-- m.vendors — supplier master with dual lineage (financial + operational); ARTS Vendor (Party subtype)
CREATE TABLE m.vendors (
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

CREATE INDEX idx_vendors_tenant ON m.vendors(tenant_id);
CREATE INDEX idx_vendors_status ON m.vendors(status) WHERE status != 'active';
CREATE INDEX idx_vendors_name_trgm ON m.vendors USING gin(name gin_trgm_ops);

-- m.item_vendors — item-to-vendor association with cost / lead time; ARTS ItemVendor
CREATE TABLE m.item_vendors (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
  vendor_id           uuid NOT NULL REFERENCES m.vendors(id) ON DELETE RESTRICT,
  vendor_sku          text,                              -- vendor's identifier for the item
  vendor_description  text,                              -- vendor's catalog description
  unit_cost           numeric(12,4),                     -- vendor's per-unit cost
  case_pack_qty       int DEFAULT 1,                     -- units per case
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

CREATE INDEX idx_item_vendors_tenant ON m.item_vendors(tenant_id);
CREATE INDEX idx_item_vendors_item ON m.item_vendors(item_id);
CREATE INDEX idx_item_vendors_vendor ON m.item_vendors(vendor_id);
CREATE INDEX idx_item_vendors_primary ON m.item_vendors(item_id) WHERE is_primary = true;

-- m.item_barcodes — scan-key lookup (UPC/EAN/GTIN); ARTS Item Identification
CREATE TABLE m.item_barcodes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  item_id         uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
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

CREATE INDEX idx_barcodes_tenant ON m.item_barcodes(tenant_id);
CREATE INDEX idx_barcodes_item ON m.item_barcodes(item_id);
CREATE UNIQUE INDEX idx_barcodes_lookup ON m.item_barcodes(tenant_id, barcode) WHERE status = 'active';

-- m.item_packs — pack composition (optional; used by pack-aware merchants); ARTS Item Composition
CREATE TABLE m.item_packs (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  pack_item_id      uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,    -- the parent (case/bundle)
  component_item_id uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,   -- the child (each unit)
  quantity          numeric(10,4) NOT NULL,                                    -- e.g., 12 for "12-pack"
  pack_type         text NOT NULL DEFAULT 'case',                              -- case | bundle | kit | mix
  attributes        jsonb NOT NULL DEFAULT '{}',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, pack_item_id, component_item_id)
);

CREATE INDEX idx_packs_tenant ON m.item_packs(tenant_id);
CREATE INDEX idx_packs_pack ON m.item_packs(pack_item_id);
CREATE INDEX idx_packs_component ON m.item_packs(component_item_id);


-- ─────────────────────────────────────────────────────────────────────
-- Forward-declared FKs (Postgres requires referenced table to exist first)
-- ─────────────────────────────────────────────────────────────────────
ALTER TABLE m.items
  ADD CONSTRAINT fk_items_category
  FOREIGN KEY (category_id) REFERENCES m.product_categories(id);
