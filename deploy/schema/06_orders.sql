-- 06_o_orders.sql — Orders Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §7 (lines 2226-2681)
-- Schema: o

-- orders.purchase_orders — supplier-direction order header
CREATE TABLE orders.purchase_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  po_number               text NOT NULL,                              -- merchant or system-assigned
  vendor_id               uuid NOT NULL REFERENCES catalog.vendors(id),
  destination_location_id uuid REFERENCES location.locations(id),            -- where goods will be received
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

CREATE INDEX idx_pos_tenant ON orders.purchase_orders(tenant_id);
CREATE INDEX idx_pos_vendor ON orders.purchase_orders(vendor_id);
CREATE INDEX idx_pos_destination ON orders.purchase_orders(destination_location_id);
CREATE INDEX idx_pos_status ON orders.purchase_orders(status) WHERE status NOT IN ('received', 'closed', 'cancelled');
CREATE INDEX idx_pos_expected ON orders.purchase_orders(expected_delivery_at) WHERE status IN ('submitted', 'acknowledged', 'in_transit');

-- orders.purchase_order_lines — supplier-direction order detail
CREATE TABLE orders.purchase_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  po_id               uuid NOT NULL REFERENCES orders.purchase_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES catalog.items(id),
  vendor_sku          text,                                       -- vendor's identifier (from catalog.item_vendors)
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

CREATE INDEX idx_po_lines_tenant ON orders.purchase_order_lines(tenant_id);
CREATE INDEX idx_po_lines_po ON orders.purchase_order_lines(po_id);
CREATE INDEX idx_po_lines_item ON orders.purchase_order_lines(item_id);
CREATE INDEX idx_po_lines_open ON orders.purchase_order_lines(po_id) WHERE status IN ('open', 'partial');

-- orders.sales_orders — customer-direction order header
CREATE TABLE orders.sales_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  order_number            text NOT NULL,                              -- merchant or system-assigned
  customer_id             uuid REFERENCES customer.customers(id),            -- nullable for guest orders
  channel                 text NOT NULL DEFAULT 'web',                -- web | bopis | ship_to_store | special_order | phone | marketplace
  origin_location_id      uuid REFERENCES location.locations(id),            -- where the order will be fulfilled from (or null for assigned-later)
  destination_location_id uuid REFERENCES location.locations(id),            -- for BOPIS/ship-to-store; NULL for ship-to-customer-address
  destination_address_id  uuid REFERENCES customer.customer_addresses(id),   -- for shipped orders; NULL for in-store pickup
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

CREATE INDEX idx_so_tenant ON orders.sales_orders(tenant_id);
CREATE INDEX idx_so_customer ON orders.sales_orders(customer_id);
CREATE INDEX idx_so_origin ON orders.sales_orders(origin_location_id);
CREATE INDEX idx_so_destination ON orders.sales_orders(destination_location_id);
CREATE INDEX idx_so_status ON orders.sales_orders(status) WHERE status NOT IN ('completed', 'cancelled', 'returned');
CREATE INDEX idx_so_external_ids ON orders.sales_orders USING gin(external_ids);

-- orders.sales_order_lines — customer-direction order detail
CREATE TABLE orders.sales_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_id      uuid NOT NULL REFERENCES orders.sales_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES catalog.items(id),
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

CREATE INDEX idx_so_lines_tenant ON orders.sales_order_lines(tenant_id);
CREATE INDEX idx_so_lines_order ON orders.sales_order_lines(sales_order_id);
CREATE INDEX idx_so_lines_item ON orders.sales_order_lines(item_id);

-- orders.fulfillments — physical pick/pack/ship operation
CREATE TABLE orders.fulfillments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_number  text NOT NULL,
  source_location_id  uuid REFERENCES location.locations(id),                -- where stock is picked from
  fulfillment_method  text NOT NULL DEFAULT 'pick_and_ship',          -- pick_and_ship | bopis_pickup | curbside | dropship | direct_ship_from_warehouse
  status              text NOT NULL DEFAULT 'pending',                 -- pending | picking | packed | shipped | delivered | cancelled
  assigned_to         uuid REFERENCES employee.employees(id),
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

CREATE INDEX idx_fulfill_tenant ON orders.fulfillments(tenant_id);
CREATE INDEX idx_fulfill_source ON orders.fulfillments(source_location_id);
CREATE INDEX idx_fulfill_assigned ON orders.fulfillments(assigned_to);
CREATE INDEX idx_fulfill_status ON orders.fulfillments(status) WHERE status NOT IN ('delivered', 'cancelled');
CREATE INDEX idx_fulfill_tracking ON orders.fulfillments(tracking_number) WHERE tracking_number IS NOT NULL;

-- orders.fulfillment_lines — fulfillment detail with three-quantity tracking
CREATE TABLE orders.fulfillment_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_id      uuid NOT NULL REFERENCES orders.fulfillments(id) ON DELETE CASCADE,
  sales_order_line_id uuid NOT NULL REFERENCES orders.sales_order_lines(id),
  item_id             uuid NOT NULL REFERENCES catalog.items(id),
  quantity            numeric(14,4) NOT NULL,
  picked_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  packed_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  shipped_quantity    numeric(14,4) NOT NULL DEFAULT 0,
  lot_id              uuid REFERENCES inventory.inventory_lots(id),           -- if lot-tracked
  inventory_movement_id uuid REFERENCES inventory.inventory_movements(id),    -- the movement that decremented stock
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ful_lines_tenant ON orders.fulfillment_lines(tenant_id);
CREATE INDEX idx_ful_lines_ful ON orders.fulfillment_lines(fulfillment_id);
CREATE INDEX idx_ful_lines_so_line ON orders.fulfillment_lines(sales_order_line_id);
CREATE INDEX idx_ful_lines_item ON orders.fulfillment_lines(item_id);

-- orders.allocations — inventory reservations for orders (soft/hard/committed)
CREATE TABLE orders.allocations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_line_id uuid NOT NULL REFERENCES orders.sales_order_lines(id) ON DELETE CASCADE,
  inventory_position_id uuid NOT NULL REFERENCES inventory.inventory_positions(id) ON DELETE RESTRICT,
  allocation_type     text NOT NULL DEFAULT 'soft',                  -- soft | hard | committed
  quantity            numeric(14,4) NOT NULL,
  allocated_at        timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz,                                    -- soft allocations expire (cart abandonment)
  consumed_by_movement_id uuid REFERENCES inventory.inventory_movements(id),  -- when picked, links to the actual decrement
  status              text NOT NULL DEFAULT 'active',                 -- active | consumed | expired | released | cancelled
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_alloc_tenant ON orders.allocations(tenant_id);
CREATE INDEX idx_alloc_so_line ON orders.allocations(sales_order_line_id);
CREATE INDEX idx_alloc_position ON orders.allocations(inventory_position_id);
CREATE INDEX idx_alloc_active ON orders.allocations(inventory_position_id) WHERE status = 'active';
CREATE INDEX idx_alloc_expiring ON orders.allocations(expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;

-- orders.shipping_documents — ASN (inbound) + BOL (outbound) unified
CREATE TABLE orders.shipping_documents (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_type       text NOT NULL,                              -- asn_inbound | bol_outbound | packing_list | manifest
  document_number     text NOT NULL,
  related_po_id       uuid REFERENCES orders.purchase_orders(id),      -- ASN inbound references PO
  related_fulfillment_id uuid REFERENCES orders.fulfillments(id),      -- BOL outbound references fulfillment
  vendor_id           uuid REFERENCES catalog.vendors(id),              -- ASN inbound: who is shipping to us
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

CREATE INDEX idx_shipdoc_tenant ON orders.shipping_documents(tenant_id);
CREATE INDEX idx_shipdoc_po ON orders.shipping_documents(related_po_id);
CREATE INDEX idx_shipdoc_ful ON orders.shipping_documents(related_fulfillment_id);
CREATE INDEX idx_shipdoc_carrier ON orders.shipping_documents(carrier, tracking_number);
CREATE INDEX idx_shipdoc_active ON orders.shipping_documents(status) WHERE status NOT IN ('delivered', 'cancelled');
