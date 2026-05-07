-- 05_i_inventory.sql — Inventory + Distribution Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §6 (lines 1876-2225)
-- Schemas: i
-- NOTE: Tables reordered from SDD narrative order to satisfy forward-reference
-- constraints. Source order: positions → movements → documents → document_lines → lots.
-- Execution order required: documents → document_lines → lots → positions → movements.

-- inventory.inventory_documents — Header for receipts/transfers/counts/RTVs (single table, type-discriminated)
CREATE TABLE inventory.inventory_documents (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_type       text NOT NULL,                              -- goods_receipt | transfer_out | transfer_in | rtv | stock_count | adjustment_batch
  document_number     text NOT NULL,                              -- merchant-assigned (PO# for receipt, RTV# for return, etc.)
  source_location_id  uuid REFERENCES location.locations(id),            -- origin (transfers, RTVs); NULL for receipts
  destination_location_id uuid REFERENCES location.locations(id),         -- destination (receipts, transfers); NULL for RTVs
  vendor_id           uuid REFERENCES catalog.vendors(id),               -- for receipts and RTVs
  related_order_id    uuid,                                        -- orders.purchase_orders(id) when known — Chunk 5b
  status              text NOT NULL DEFAULT 'draft',               -- draft | in_progress | completed | cancelled | reconciled
  expected_at         timestamptz,
  completed_at        timestamptz,
  total_quantity      numeric(14,4),                               -- sum of line quantities (denormalized)
  total_cost          numeric(14,4),                               -- sum of line cost (denormalized)
  performed_by_user_id uuid REFERENCES app.users(id),
  attributes          jsonb NOT NULL DEFAULT '{}',                 -- carrier, BOL #, packing list URL, photo evidence URLs
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_type, document_number)
);

CREATE INDEX idx_idocs_tenant ON inventory.inventory_documents(tenant_id);
CREATE INDEX idx_idocs_type ON inventory.inventory_documents(document_type);
CREATE INDEX idx_idocs_status ON inventory.inventory_documents(status) WHERE status NOT IN ('completed', 'cancelled');
CREATE INDEX idx_idocs_destination ON inventory.inventory_documents(destination_location_id);
CREATE INDEX idx_idocs_source ON inventory.inventory_documents(source_location_id);
CREATE INDEX idx_idocs_vendor ON inventory.inventory_documents(vendor_id);
CREATE INDEX idx_idocs_related_order ON inventory.inventory_documents(related_order_id);

-- inventory.inventory_lots — Lot/serial/expiry tracking (optional; only used by lot-tracked merchants)
CREATE TABLE inventory.inventory_lots (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,
  lot_number          text NOT NULL,                              -- batch/lot/serial number
  lot_type            text NOT NULL DEFAULT 'batch',              -- batch | serial | expiry | catch_weight
  expiry_date         date,                                       -- for date-tracked items
  manufacture_date    date,
  received_at         timestamptz,
  vendor_id           uuid REFERENCES catalog.vendors(id),
  source_document_id  uuid REFERENCES inventory.inventory_documents(id),  -- the receipt that introduced this lot
  status              text NOT NULL DEFAULT 'active',             -- active | quarantine | recalled | exhausted | expired
  attributes          jsonb NOT NULL DEFAULT '{}',                -- catch-weight, country-of-origin, FDA NDC, etc.
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, lot_number)
);

CREATE INDEX idx_lots_tenant ON inventory.inventory_lots(tenant_id);
CREATE INDEX idx_lots_item ON inventory.inventory_lots(item_id);
CREATE INDEX idx_lots_expiry ON inventory.inventory_lots(expiry_date) WHERE expiry_date IS NOT NULL AND status = 'active';
CREATE INDEX idx_lots_status ON inventory.inventory_lots(status) WHERE status != 'active';

-- inventory.inventory_document_lines — Per-line detail with generated variance column
CREATE TABLE inventory.inventory_document_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_id         uuid NOT NULL REFERENCES inventory.inventory_documents(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,
  expected_quantity   numeric(14,4),                              -- planned (for receipts vs PO, transfers vs request)
  actual_quantity     numeric(14,4),                              -- physically counted/received
  variance_quantity   numeric(14,4) GENERATED ALWAYS AS (COALESCE(actual_quantity, 0) - COALESCE(expected_quantity, 0)) STORED,
  variance_reason     text,                                       -- damaged | short | over | wrong_item | quality_reject
  unit_cost           numeric(14,4),                              -- per-unit cost at receipt
  lot_id              uuid REFERENCES inventory.inventory_lots(id),
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_id, line_number)
);

CREATE INDEX idx_idoc_lines_tenant ON inventory.inventory_document_lines(tenant_id);
CREATE INDEX idx_idoc_lines_document ON inventory.inventory_document_lines(document_id);
CREATE INDEX idx_idoc_lines_item ON inventory.inventory_document_lines(item_id);
CREATE INDEX idx_idoc_lines_variance ON inventory.inventory_document_lines(document_id) WHERE variance_quantity != 0;

-- inventory.inventory_positions — Current SOH per item × location (with reserved/on-order/in-transit)
CREATE TABLE inventory.inventory_positions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  item_id                 uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,
  location_id             uuid NOT NULL REFERENCES location.locations(id) ON DELETE RESTRICT,
  zone_id                 uuid REFERENCES location.location_zones(id),    -- bin-level if zone-tracked; NULL = location-aggregate
  on_hand_quantity        numeric(14,4) NOT NULL DEFAULT 0,
  reserved_quantity       numeric(14,4) NOT NULL DEFAULT 0,        -- allocated but not yet picked (orders awaiting fulfillment)
  on_order_quantity       numeric(14,4) NOT NULL DEFAULT 0,        -- POs placed, not yet received
  in_transit_quantity     numeric(14,4) NOT NULL DEFAULT 0,        -- transfers in-flight
  last_movement_at        timestamptz,
  last_count_at           timestamptz,                              -- last stock-count timestamp (for cycle-count cadence)
  cost_basis              numeric(14,4),                            -- weighted-average cost (financial; cross-references ledger.stock_ledger_entries)
  attributes              jsonb NOT NULL DEFAULT '{}',
  status                  text NOT NULL DEFAULT 'active',           -- active | discontinued | bin_relocated
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE NULLS NOT DISTINCT (tenant_id, item_id, location_id, zone_id)  -- SDD-fix: replaced COALESCE-in-UNIQUE
);

CREATE INDEX idx_positions_tenant ON inventory.inventory_positions(tenant_id);
CREATE INDEX idx_positions_item ON inventory.inventory_positions(item_id);
CREATE INDEX idx_positions_location ON inventory.inventory_positions(location_id);
CREATE INDEX idx_positions_low_stock ON inventory.inventory_positions(tenant_id, location_id, item_id) WHERE on_hand_quantity <= 0;
-- SDD-fix: original predicate used `last_movement_at - interval '30 days'` which Postgres
-- treats as STABLE (not IMMUTABLE) so it can't appear in an index predicate. Reduced to
-- the IS NULL clause; the staleness-window check can be done at query time.
CREATE INDEX idx_positions_unsynced ON inventory.inventory_positions(last_movement_at) WHERE last_count_at IS NULL;

-- inventory.inventory_movements — Append-only event log per atomic SOH change (all movement types unified)
CREATE TABLE inventory.inventory_movements (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  item_id                 uuid NOT NULL REFERENCES catalog.items(id) ON DELETE RESTRICT,
  location_id             uuid NOT NULL REFERENCES location.locations(id) ON DELETE RESTRICT,
  zone_id                 uuid REFERENCES location.location_zones(id),
  lot_id                  uuid REFERENCES inventory.inventory_lots(id),
  movement_type           text NOT NULL,                            -- goods_receipt | adjustment | transfer_in | transfer_out | rtv | sale | return | write_off | cycle_count_correction | reservation | release_reservation
  quantity_delta          numeric(14,4) NOT NULL,                   -- signed; positive = increase, negative = decrease
  movement_at             timestamptz NOT NULL DEFAULT now(),
  source_document_id      uuid REFERENCES inventory.inventory_documents(id),  -- nullable for direct movements (manual adjustment)
  source_document_line_id uuid REFERENCES inventory.inventory_document_lines(id),
  source_transaction_id   uuid,                                       -- transaction.transactions(id) — sale-derived movements
  reason_code             text,                                       -- damaged | theft | spoilage | recount_corrected | etc.
  reference                text,                                       -- merchant or external reference (PO #, RTV #, etc.)
  performed_by_user_id    uuid REFERENCES app.users(id),
  performed_by_employee_id uuid REFERENCES employee.employees(id),
  cost_basis              numeric(14,4),                              -- cost at time of movement (snapshot)
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- NOTE: no updated_at — this is append-only
);

CREATE INDEX idx_movements_tenant ON inventory.inventory_movements(tenant_id);
CREATE INDEX idx_movements_item ON inventory.inventory_movements(item_id);
CREATE INDEX idx_movements_location ON inventory.inventory_movements(location_id);
CREATE INDEX idx_movements_at ON inventory.inventory_movements(movement_at);
CREATE INDEX idx_movements_type ON inventory.inventory_movements(movement_type);
CREATE INDEX idx_movements_document ON inventory.inventory_movements(source_document_id);
CREATE INDEX idx_movements_transaction ON inventory.inventory_movements(source_transaction_id) WHERE source_transaction_id IS NOT NULL;
CREATE INDEX idx_movements_position_recompute ON inventory.inventory_movements(tenant_id, item_id, location_id, COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid), movement_at DESC);
