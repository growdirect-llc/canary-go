-- 06_o_orders.sql — Orders Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §7 (lines 2226-2681)
-- Schema: o

-- o.purchase_orders — supplier-direction order header
CREATE TABLE o.purchase_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  po_number               text NOT NULL,                              -- merchant or system-assigned
  vendor_id               uuid NOT NULL REFERENCES m.vendors(id),
  destination_location_id uuid REFERENCES l.locations(id),            -- where goods will be received
  order_method            text NOT NULL DEFAULT 'replenishment',      -- replenishment | direct | dropship | warehouse_consolidation
  order_type              text NOT NULL DEFAULT 'standard',           -- standard | rush | pre_book | promotional | open
  status                  text NOT NULL DEFAULT 'draft',              -- draft | submitted | acknowledged | in_transit | partial_received | received | closed | cancelled
  ordered_at              timestamptz,                                -- when submitted to vendor
  expected_delivery_at    timestamptz,
  acknowledged_at         timestamptz,                                -- vendor acknowledgement (F013 PO Ack equivalent)
  cancelled_at            timestamptz,
  total_quantity          numeric(14,4),                              -- denormalized sum of line quantities
  total_cost              numeric(14,4),                              -- denormalized sum of line costs
  currency                text NOT NULL DEFAULT 'USD',                -- ISO 4217
  payment_terms           text,                                       -- inherits from vendor by default; override per-PO
  shipping_terms          text,                                       -- FOB origin | FOB destination | etc.
  approval_user_id        uuid REFERENCES app.users(id),
  approved_at             timestamptz,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, po_number)
);

CREATE INDEX idx_pos_tenant ON o.purchase_orders(tenant_id);
CREATE INDEX idx_pos_vendor ON o.purchase_orders(vendor_id);
CREATE INDEX idx_pos_destination ON o.purchase_orders(destination_location_id);
CREATE INDEX idx_pos_status ON o.purchase_orders(status) WHERE status NOT IN ('received', 'closed', 'cancelled');
CREATE INDEX idx_pos_expected ON o.purchase_orders(expected_delivery_at) WHERE status IN ('submitted', 'acknowledged', 'in_transit');

-- o.purchase_order_lines — supplier-direction order detail
CREATE TABLE o.purchase_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  po_id               uuid NOT NULL REFERENCES o.purchase_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES m.items(id),
  vendor_sku          text,                                       -- vendor's identifier (from m.item_vendors)
  ordered_quantity    numeric(14,4) NOT NULL,
  received_quantity   numeric(14,4) NOT NULL DEFAULT 0,           -- accumulates across partial receipts
  cancelled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  unit_cost           numeric(14,4) NOT NULL,
  total_cost          numeric(14,4) GENERATED ALWAYS AS (ordered_quantity * unit_cost) STORED,
  expected_delivery_at timestamptz,
  status              text NOT NULL DEFAULT 'open',                -- open | partial | received | cancelled | closed
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, po_id, line_number)
);

CREATE INDEX idx_po_lines_tenant ON o.purchase_order_lines(tenant_id);
CREATE INDEX idx_po_lines_po ON o.purchase_order_lines(po_id);
CREATE INDEX idx_po_lines_item ON o.purchase_order_lines(item_id);
CREATE INDEX idx_po_lines_open ON o.purchase_order_lines(po_id) WHERE status IN ('open', 'partial');

-- o.sales_orders — customer-direction order header
CREATE TABLE o.sales_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  order_number            text NOT NULL,                              -- merchant or system-assigned
  customer_id             uuid REFERENCES c.customers(id),            -- nullable for guest orders
  channel                 text NOT NULL DEFAULT 'web',                -- web | bopis | ship_to_store | special_order | phone | marketplace
  origin_location_id      uuid REFERENCES l.locations(id),            -- where the order will be fulfilled from (or null for assigned-later)
  destination_location_id uuid REFERENCES l.locations(id),            -- for BOPIS/ship-to-store; NULL for ship-to-customer-address
  destination_address_id  uuid REFERENCES c.customer_addresses(id),   -- for shipped orders; NULL for in-store pickup
  status                  text NOT NULL DEFAULT 'pending',            -- pending | confirmed | allocated | picking | packed | shipped | delivered | completed | cancelled | returned
  ordered_at              timestamptz NOT NULL DEFAULT now(),
  promised_at             timestamptz,                                -- promised delivery / pickup time
  fulfilled_at            timestamptz,
  cancelled_at            timestamptz,
  subtotal                numeric(14,4),
  tax_total               numeric(14,4),
  shipping_total          numeric(14,4),
  discount_total          numeric(14,4),
  grand_total             numeric(14,4),
  currency                text NOT NULL DEFAULT 'USD',
  payment_status          text NOT NULL DEFAULT 'pending',            -- pending | authorized | captured | refunded | failed
  attributes              jsonb NOT NULL DEFAULT '{}',                -- gift_message, delivery_instructions, special_handling
  external_ids            jsonb DEFAULT '{}',                         -- shopify_id, square_order_id, marketplace_order_id
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, order_number)
);

CREATE INDEX idx_so_tenant ON o.sales_orders(tenant_id);
CREATE INDEX idx_so_customer ON o.sales_orders(customer_id);
CREATE INDEX idx_so_origin ON o.sales_orders(origin_location_id);
CREATE INDEX idx_so_destination ON o.sales_orders(destination_location_id);
CREATE INDEX idx_so_status ON o.sales_orders(status) WHERE status NOT IN ('completed', 'cancelled', 'returned');
CREATE INDEX idx_so_external_ids ON o.sales_orders USING gin(external_ids);

-- o.sales_order_lines — customer-direction order detail
CREATE TABLE o.sales_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_id      uuid NOT NULL REFERENCES o.sales_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES m.items(id),
  ordered_quantity    numeric(14,4) NOT NULL,
  fulfilled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  cancelled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  refunded_quantity   numeric(14,4) NOT NULL DEFAULT 0,
  unit_price          numeric(14,4) NOT NULL,
  unit_discount       numeric(14,4) NOT NULL DEFAULT 0,
  unit_tax            numeric(14,4) NOT NULL DEFAULT 0,
  line_total          numeric(14,4) GENERATED ALWAYS AS ((ordered_quantity * (unit_price - unit_discount)) + (ordered_quantity * unit_tax)) STORED,
  status              text NOT NULL DEFAULT 'open',
  attributes          jsonb NOT NULL DEFAULT '{}',                  -- {customizations, gift_wrap, etc.}
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sales_order_id, line_number)
);

CREATE INDEX idx_so_lines_tenant ON o.sales_order_lines(tenant_id);
CREATE INDEX idx_so_lines_order ON o.sales_order_lines(sales_order_id);
CREATE INDEX idx_so_lines_item ON o.sales_order_lines(item_id);

-- o.fulfillments — physical pick/pack/ship operation
CREATE TABLE o.fulfillments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_number  text NOT NULL,
  source_location_id  uuid REFERENCES l.locations(id),                -- where stock is picked from
  fulfillment_method  text NOT NULL DEFAULT 'pick_and_ship',          -- pick_and_ship | bopis_pickup | curbside | dropship | direct_ship_from_warehouse
  status              text NOT NULL DEFAULT 'pending',                 -- pending | picking | packed | shipped | delivered | cancelled
  assigned_to         uuid REFERENCES e.employees(id),
  picked_at           timestamptz,
  packed_at           timestamptz,
  shipped_at          timestamptz,
  delivered_at        timestamptz,
  carrier             text,                                            -- USPS | UPS | FedEx | DHL | merchant_delivery | customer_pickup
  tracking_number     text,
  tracking_url        text,
  attributes          jsonb NOT NULL DEFAULT '{}',                    -- driver_name, photo_proof_url, signature_url
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, fulfillment_number)
);

CREATE INDEX idx_fulfill_tenant ON o.fulfillments(tenant_id);
CREATE INDEX idx_fulfill_source ON o.fulfillments(source_location_id);
CREATE INDEX idx_fulfill_assigned ON o.fulfillments(assigned_to);
CREATE INDEX idx_fulfill_status ON o.fulfillments(status) WHERE status NOT IN ('delivered', 'cancelled');
CREATE INDEX idx_fulfill_tracking ON o.fulfillments(tracking_number) WHERE tracking_number IS NOT NULL;

-- o.fulfillment_lines — fulfillment detail with three-quantity tracking
CREATE TABLE o.fulfillment_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_id      uuid NOT NULL REFERENCES o.fulfillments(id) ON DELETE CASCADE,
  sales_order_line_id uuid NOT NULL REFERENCES o.sales_order_lines(id),
  item_id             uuid NOT NULL REFERENCES m.items(id),
  quantity            numeric(14,4) NOT NULL,
  picked_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  packed_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  shipped_quantity    numeric(14,4) NOT NULL DEFAULT 0,
  lot_id              uuid REFERENCES i.inventory_lots(id),           -- if lot-tracked
  inventory_movement_id uuid REFERENCES i.inventory_movements(id),    -- the movement that decremented stock
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ful_lines_tenant ON o.fulfillment_lines(tenant_id);
CREATE INDEX idx_ful_lines_ful ON o.fulfillment_lines(fulfillment_id);
CREATE INDEX idx_ful_lines_so_line ON o.fulfillment_lines(sales_order_line_id);
CREATE INDEX idx_ful_lines_item ON o.fulfillment_lines(item_id);

-- o.allocations — inventory reservations for orders (soft/hard/committed)
CREATE TABLE o.allocations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_line_id uuid NOT NULL REFERENCES o.sales_order_lines(id) ON DELETE CASCADE,
  inventory_position_id uuid NOT NULL REFERENCES i.inventory_positions(id) ON DELETE RESTRICT,
  allocation_type     text NOT NULL DEFAULT 'soft',                  -- soft | hard | committed
  quantity            numeric(14,4) NOT NULL,
  allocated_at        timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz,                                    -- soft allocations expire (cart abandonment)
  consumed_by_movement_id uuid REFERENCES i.inventory_movements(id),  -- when picked, links to the actual decrement
  status              text NOT NULL DEFAULT 'active',                 -- active | consumed | expired | released | cancelled
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_alloc_tenant ON o.allocations(tenant_id);
CREATE INDEX idx_alloc_so_line ON o.allocations(sales_order_line_id);
CREATE INDEX idx_alloc_position ON o.allocations(inventory_position_id);
CREATE INDEX idx_alloc_active ON o.allocations(inventory_position_id) WHERE status = 'active';
CREATE INDEX idx_alloc_expiring ON o.allocations(expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;

-- o.shipping_documents — ASN (inbound) + BOL (outbound) unified
CREATE TABLE o.shipping_documents (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_type       text NOT NULL,                              -- asn_inbound | bol_outbound | packing_list | manifest
  document_number     text NOT NULL,
  related_po_id       uuid REFERENCES o.purchase_orders(id),      -- ASN inbound references PO
  related_fulfillment_id uuid REFERENCES o.fulfillments(id),      -- BOL outbound references fulfillment
  vendor_id           uuid REFERENCES m.vendors(id),              -- ASN inbound: who is shipping to us
  carrier             text,
  tracking_number     text,
  expected_arrival_at timestamptz,                                -- ASN
  shipped_at          timestamptz,                                -- BOL
  total_quantity      numeric(14,4),                              -- denormalized
  total_weight        numeric(14,4),
  total_volume        numeric(14,4),
  attributes          jsonb NOT NULL DEFAULT '{}',                -- pallet count, container number, customs declaration
  status              text NOT NULL DEFAULT 'pending',            -- pending | acknowledged | in_transit | delivered | cancelled
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_type, document_number)
);

CREATE INDEX idx_shipdoc_tenant ON o.shipping_documents(tenant_id);
CREATE INDEX idx_shipdoc_po ON o.shipping_documents(related_po_id);
CREATE INDEX idx_shipdoc_ful ON o.shipping_documents(related_fulfillment_id);
CREATE INDEX idx_shipdoc_carrier ON o.shipping_documents(carrier, tracking_number);
CREATE INDEX idx_shipdoc_active ON o.shipping_documents(status) WHERE status NOT IN ('delivered', 'cancelled');
