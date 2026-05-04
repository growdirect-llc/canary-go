-- 03_l_s_locations.sql — Location + Space Domain (A / Asset, S / Space)
-- Source: docs/sdds/go-handoff/canonical-data-model.md §4 (lines 1076-1475)
-- Schemas: l, s
-- Depends on: 00_schemas.sql, 01_app_foundation.sql (app.tenants, app.users), 02_m_items.sql (catalog.items, catalog.product_categories)

-- location.locations — store / warehouse / DC unified; ARTS Location V2 (RetailStore + Warehouse + DistributionCenter)
CREATE TABLE location.locations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  location_code       text NOT NULL,                              -- merchant or POS-native code (StoreNo equivalent)
  name                text NOT NULL,
  location_type       text NOT NULL DEFAULT 'store',              -- store | warehouse | distribution_center | dropship | virtual | popup
  parent_location_id  uuid REFERENCES location.locations(id),            -- employee.g., distribution center serves stores; nullable
  banner              text,                                        -- merchant banner if multi-banner
  status              text NOT NULL DEFAULT 'active',             -- active | inactive | closed | construction | pending_open
  open_date           date,
  close_date          date,
  remodel_date        date,
  square_footage      int,
  selling_area_sqft   int,
  storage_area_sqft   int,
  channel             text,                                        -- brick | online | hybrid | popup
  format              text,                                        -- supermarket | convenience | specialty | warehouse | etc.
  currency            text NOT NULL DEFAULT 'USD',
  language            text NOT NULL DEFAULT 'en-US',               -- BCP 47
  timezone            text NOT NULL DEFAULT 'America/Los_Angeles'  -- IANA tz identifier (RFC 6557)
                      CHECK (timezone ~ '^[A-Z][a-zA-Z_]+/[A-Z][a-zA-Z_]+(/[A-Z][a-zA-Z_]+)?$'),
  address             jsonb DEFAULT '{}',                          -- {line1, line2, city, region, postal_code, country, latitude, longitude, county}
  contact             jsonb DEFAULT '{}',                          -- {name, phone, email, manager_name}
  operating_hours     jsonb DEFAULT '{}',                          -- {monday: [{open: "07:00", close: "22:00"}], ...}
  attributes          jsonb NOT NULL DEFAULT '{}',                 -- merchant-defined (employee.g., DUNS, integrated POS ind, MSA)
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_code)
);

CREATE INDEX idx_locations_tenant ON location.locations(tenant_id);
CREATE INDEX idx_locations_parent ON location.locations(parent_location_id);
CREATE INDEX idx_locations_status ON location.locations(status) WHERE status != 'active';
CREATE INDEX idx_locations_type ON location.locations(location_type);
CREATE INDEX idx_locations_address_gin ON location.locations USING gin(address);

-- location.location_hierarchy — multi-hierarchy nodes (organizational / distribution / banner / tax_zone); ARTS LocationHierarchy
CREATE TABLE location.location_hierarchy (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES location.location_hierarchy(id),     -- NULL for root
  code            text NOT NULL,                                  -- employee.g., "WEST_REGION", "DIST_LA_NORTH"
  name            text NOT NULL,
  hierarchy_type  text NOT NULL DEFAULT 'organizational',         -- organizational | distribution | banner | tax_zone
  level           int NOT NULL,                                   -- denormalized depth
  path            ltree,                                          -- materialized path
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, hierarchy_type, code)
);

-- location.location_hierarchy_assignments — many-to-many (location belongs to multiple hierarchies)
CREATE TABLE location.location_hierarchy_assignments (
  location_id     uuid NOT NULL REFERENCES location.locations(id) ON DELETE CASCADE,
  hierarchy_id    uuid NOT NULL REFERENCES location.location_hierarchy(id) ON DELETE CASCADE,
  PRIMARY KEY (location_id, hierarchy_id)
);

CREATE INDEX idx_loc_hier_tenant ON location.location_hierarchy(tenant_id);
CREATE INDEX idx_loc_hier_parent ON location.location_hierarchy(parent_id);
CREATE INDEX idx_loc_hier_path ON location.location_hierarchy USING gist(path);
CREATE INDEX idx_loc_hier_assign_loc ON location.location_hierarchy_assignments(location_id);
CREATE INDEX idx_loc_hier_assign_hier ON location.location_hierarchy_assignments(hierarchy_id);

-- location.location_zones — within-store recursive zoning (floor / dept / aisle / section / endcap / bin / shelf / cooler)
CREATE TABLE location.location_zones (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  location_id     uuid NOT NULL REFERENCES location.locations(id) ON DELETE CASCADE,
  parent_zone_id  uuid REFERENCES location.location_zones(id),
  code            text NOT NULL,                          -- employee.g., "FLOOR_1", "GROCERY_AISLE_3", "ENDCAP_5"
  name            text NOT NULL,
  zone_type       text NOT NULL DEFAULT 'department',     -- floor | department | aisle | section | endcap | bin | shelf | cooler
  level           int NOT NULL,                            -- depth within location
  path            ltree,
  geometry        jsonb DEFAULT '{}',                      -- {coordinates, dimensions} for store-mapping later
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, code)
);

CREATE INDEX idx_zones_tenant ON location.location_zones(tenant_id);
CREATE INDEX idx_zones_location ON location.location_zones(location_id);
CREATE INDEX idx_zones_parent ON location.location_zones(parent_zone_id);
CREATE INDEX idx_zones_path ON location.location_zones USING gist(path);

-- space.planograms — planogram master plan; ARTS Planogram V2
CREATE TABLE space.planograms (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  planogram_code      text NOT NULL,                      -- merchant-assigned (employee.g., "GROCERY_GR_VALID_24Q1")
  name                text NOT NULL,
  category_id         uuid REFERENCES catalog.product_categories(id),  -- the merchandise category this plans
  effective_start     date,
  effective_end       date,                               -- NULL for indefinite
  layout_dimensions   jsonb DEFAULT '{}',                 -- {width_cm, height_cm, depth_cm, shelf_count, etc.}
  status              text NOT NULL DEFAULT 'draft',      -- draft | approved | active | retired
  approved_by         uuid REFERENCES app.users(id),
  approved_at         timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, planogram_code)
);

-- space.planogram_assignments — planogram-to-location binding (location-wide or zone-specific)
-- SDD-fix: original used COALESCE inside PRIMARY KEY (Postgres rejects). Replaced
-- with a synthetic id PK + UNIQUE NULLS NOT DISTINCT (Postgres 15+) which gives
-- the same uniqueness semantics: (planogram, location, zone=NULL) collides with
-- another (planogram, location, zone=NULL).
CREATE TABLE space.planogram_assignments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  planogram_id    uuid NOT NULL REFERENCES space.planograms(id) ON DELETE CASCADE,
  location_id     uuid NOT NULL REFERENCES location.locations(id) ON DELETE CASCADE,
  zone_id         uuid REFERENCES location.location_zones(id),
  assigned_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE NULLS NOT DISTINCT (planogram_id, location_id, zone_id)
);

CREATE INDEX idx_planograms_tenant ON space.planograms(tenant_id);
CREATE INDEX idx_planograms_category ON space.planograms(category_id);
CREATE INDEX idx_planograms_status ON space.planograms(status);
CREATE INDEX idx_planogram_assign_loc ON space.planogram_assignments(location_id);

-- space.planogram_positions — item placement detail per planogram (shelf / position / facings / capacity); ARTS Planogram V2 Position/Facing
CREATE TABLE space.planogram_positions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  planogram_id        uuid NOT NULL REFERENCES space.planograms(id) ON DELETE CASCADE,
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,
  shelf_number        int,                                -- 1=top, N=bottom
  position_on_shelf   int,                                -- 1=left, N=right
  facings             int NOT NULL DEFAULT 1,             -- horizontal facings count
  capacity_units      int,                                -- max units this position holds
  orientation         text DEFAULT 'face_forward',        -- face_forward | sideways | hanging | etc.
  geometry            jsonb DEFAULT '{}',                 -- {x_cm, y_cm, width_cm, height_cm}
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, planogram_id, item_id, shelf_number, position_on_shelf)
);

CREATE INDEX idx_positions_tenant ON space.planogram_positions(tenant_id);
CREATE INDEX idx_positions_planogram ON space.planogram_positions(planogram_id);
CREATE INDEX idx_positions_item ON space.planogram_positions(item_id);

-- location.location_assortment — item-location authorization with multi-tier (store_carry / warehouse_only / expanded_storefront / dropship / deleted); ARTS Item-Location Range
CREATE TABLE location.location_assortment (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  location_id         uuid NOT NULL REFERENCES location.locations(id) ON DELETE CASCADE,
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  zone_id             uuid REFERENCES location.location_zones(id),
  assortment_tier     text NOT NULL DEFAULT 'store_carry',  -- store_carry | warehouse_only | expanded_storefront | dropship | deleted
  effective_start     date,
  effective_end       date,
  source_planogram_id uuid REFERENCES space.planograms(id),     -- if assortment driven by planogram
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE NULLS NOT DISTINCT (tenant_id, location_id, item_id, zone_id)  -- SDD-fix: replaced COALESCE-in-UNIQUE
);

CREATE INDEX idx_assortment_tenant ON location.location_assortment(tenant_id);
CREATE INDEX idx_assortment_location ON location.location_assortment(location_id);
CREATE INDEX idx_assortment_item ON location.location_assortment(item_id);
CREATE INDEX idx_assortment_active ON location.location_assortment(location_id, status) WHERE status = 'active';
