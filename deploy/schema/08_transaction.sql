-- 08_t_transactions.sql — POSLog + Sales Audit Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §9 (lines 3218-3735)
-- Schema: t

-- transaction.transactions — POS transaction header
CREATE TABLE transaction.transactions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_number      text NOT NULL,
  transaction_type        text NOT NULL DEFAULT 'sale',
  parent_transaction_id   uuid REFERENCES transaction.transactions(id),
  location_id             uuid NOT NULL REFERENCES location.locations(id),
  pos_terminal_id         text,
  cashier_employee_id     uuid REFERENCES employee.employees(id),
  customer_id             uuid REFERENCES customer.customers(id),
  loyalty_membership_id   uuid REFERENCES customer.loyalty_memberships(id),
  business_date           date NOT NULL,
  started_at              timestamptz NOT NULL,
  ended_at                timestamptz NOT NULL,
  status                  text NOT NULL DEFAULT 'completed',
  ticket_number           int,
  item_count              int NOT NULL DEFAULT 0,
  subtotal                numeric(14,4) NOT NULL DEFAULT 0,
  tax_total               numeric(14,4) NOT NULL DEFAULT 0,
  discount_total          numeric(14,4) NOT NULL DEFAULT 0,
  grand_total             numeric(14,4) NOT NULL DEFAULT 0,
  currency                text NOT NULL DEFAULT 'USD',
  channel                 text NOT NULL DEFAULT 'pos',
  pos_software_version    text,
  is_training_mode        boolean NOT NULL DEFAULT false,
  is_offline              boolean NOT NULL DEFAULT false,
  is_reentered            boolean NOT NULL DEFAULT false,
  is_suspended            boolean NOT NULL DEFAULT false,
  void_reason             text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  external_ids            jsonb DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, business_date, transaction_number)
);

CREATE INDEX idx_tx_tenant ON transaction.transactions(tenant_id);
CREATE INDEX idx_tx_location_date ON transaction.transactions(location_id, business_date);
CREATE INDEX idx_tx_cashier ON transaction.transactions(cashier_employee_id, business_date);
CREATE INDEX idx_tx_customer ON transaction.transactions(customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX idx_tx_loyalty ON transaction.transactions(loyalty_membership_id) WHERE loyalty_membership_id IS NOT NULL;
CREATE INDEX idx_tx_parent ON transaction.transactions(parent_transaction_id) WHERE parent_transaction_id IS NOT NULL;
CREATE INDEX idx_tx_started ON transaction.transactions(started_at);
CREATE INDEX idx_tx_status ON transaction.transactions(status) WHERE status != 'completed';
CREATE INDEX idx_tx_external_ids ON transaction.transactions USING gin(external_ids);

-- transaction.transaction_line_items — POSLog sale line item detail
CREATE TABLE transaction.transaction_line_items (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES transaction.transactions(id) ON DELETE CASCADE,
  line_number             int NOT NULL,
  item_id                 uuid REFERENCES catalog.items(id),
  barcode_scanned         text,
  description             text NOT NULL,
  quantity                numeric(14,4) NOT NULL,
  unit_of_measure         text NOT NULL DEFAULT 'EA',
  unit_price              numeric(14,4) NOT NULL,
  list_price              numeric(14,4),
  unit_discount           numeric(14,4) NOT NULL DEFAULT 0,
  unit_tax                numeric(14,4) NOT NULL DEFAULT 0,
  extended_price          numeric(14,4) GENERATED ALWAYS AS (quantity * (unit_price - unit_discount)) STORED,
  extended_tax            numeric(14,4) GENERATED ALWAYS AS (quantity * unit_tax) STORED,
  line_total              numeric(14,4) GENERATED ALWAYS AS ((quantity * (unit_price - unit_discount)) + (quantity * unit_tax)) STORED,
  cost_basis              numeric(14,4),
  margin                  numeric(14,4) GENERATED ALWAYS AS (((quantity * (unit_price - unit_discount)) - (quantity * COALESCE(cost_basis, 0)))) STORED,
  category_id             uuid REFERENCES catalog.product_categories(id),
  zone_id                 uuid REFERENCES location.location_zones(id),
  lot_id                  uuid REFERENCES inventory.inventory_lots(id),
  inventory_movement_id   uuid REFERENCES inventory.inventory_movements(id),
  is_void                 boolean NOT NULL DEFAULT false,
  void_reason             text,
  is_return               boolean NOT NULL DEFAULT false,
  return_reason           text,
  is_weighable            boolean NOT NULL DEFAULT false,
  is_food_stamp_eligible  boolean NOT NULL DEFAULT false,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, transaction_id, line_number)
);

CREATE INDEX idx_lines_tenant ON transaction.transaction_line_items(tenant_id);
CREATE INDEX idx_lines_tx ON transaction.transaction_line_items(transaction_id);
CREATE INDEX idx_lines_item ON transaction.transaction_line_items(item_id);
CREATE INDEX idx_lines_category ON transaction.transaction_line_items(category_id);
CREATE INDEX idx_lines_zone ON transaction.transaction_line_items(zone_id);
CREATE INDEX idx_lines_returns ON transaction.transaction_line_items(transaction_id) WHERE is_return = true;
CREATE INDEX idx_lines_voids ON transaction.transaction_line_items(transaction_id) WHERE is_void = true;
CREATE INDEX idx_lines_unknown ON transaction.transaction_line_items(barcode_scanned) WHERE item_id IS NULL;

-- transaction.transaction_tenders — multi-tender payment detail (tokenized card data only)
CREATE TABLE transaction.transaction_tenders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES transaction.transactions(id) ON DELETE CASCADE,
  tender_sequence         int NOT NULL,
  tender_type_id          uuid NOT NULL REFERENCES finance.tender_types(id),
  amount                  numeric(14,4) NOT NULL,
  currency                text NOT NULL DEFAULT 'USD',
  cash_back_amount        numeric(14,4) NOT NULL DEFAULT 0,
  change_amount           numeric(14,4) NOT NULL DEFAULT 0,
  card_token              text,
  card_last_4             text,
  card_brand              text,
  authorization_code      text,
  processor_reference     text,
  is_voided               boolean NOT NULL DEFAULT false,
  is_refund               boolean NOT NULL DEFAULT false,
  contactless             boolean NOT NULL DEFAULT false,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, transaction_id, tender_sequence)
);

CREATE INDEX idx_tend_tenant ON transaction.transaction_tenders(tenant_id);
CREATE INDEX idx_tend_tx ON transaction.transaction_tenders(transaction_id);
CREATE INDEX idx_tend_type ON transaction.transaction_tenders(tender_type_id);
CREATE INDEX idx_tend_card ON transaction.transaction_tenders(card_last_4) WHERE card_last_4 IS NOT NULL;
CREATE INDEX idx_tend_processor ON transaction.transaction_tenders(processor_reference) WHERE processor_reference IS NOT NULL;

-- transaction.transaction_discounts — discount events scoped at transaction, line, or tender
CREATE TABLE transaction.transaction_discounts (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES transaction.transactions(id) ON DELETE CASCADE,
  discount_sequence       int NOT NULL,
  scope                   text NOT NULL,
  line_item_id            uuid REFERENCES transaction.transaction_line_items(id) ON DELETE CASCADE,
  discount_type           text NOT NULL,
  source_promotion_id     uuid REFERENCES pricing.promotions(id),
  promotion_rule_id       uuid REFERENCES pricing.promotion_rules(id),
  amount                  numeric(14,4) NOT NULL,
  percentage              numeric(5,4),
  reason_code             text,
  authorized_by_employee_id uuid REFERENCES employee.employees(id),
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_disc_tenant ON transaction.transaction_discounts(tenant_id);
CREATE INDEX idx_disc_tx ON transaction.transaction_discounts(transaction_id);
CREATE INDEX idx_disc_line ON transaction.transaction_discounts(line_item_id);
CREATE INDEX idx_disc_promo ON transaction.transaction_discounts(source_promotion_id);
CREATE INDEX idx_disc_type ON transaction.transaction_discounts(discount_type);
CREATE INDEX idx_disc_authorizer ON transaction.transaction_discounts(authorized_by_employee_id) WHERE authorized_by_employee_id IS NOT NULL;

-- transaction.cashier_actions — operator action log (overrides, drawer opens, lookups)
CREATE TABLE transaction.cashier_actions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid REFERENCES transaction.transactions(id),
  location_id             uuid NOT NULL REFERENCES location.locations(id),
  cashier_employee_id     uuid NOT NULL REFERENCES employee.employees(id),
  pos_terminal_id         text,
  action_type             text NOT NULL,
  performed_at            timestamptz NOT NULL DEFAULT now(),
  authorized_by_employee_id uuid REFERENCES employee.employees(id),
  details                 jsonb NOT NULL DEFAULT '{}',
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_actions_tenant ON transaction.cashier_actions(tenant_id);
CREATE INDEX idx_actions_tx ON transaction.cashier_actions(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_actions_cashier ON transaction.cashier_actions(cashier_employee_id, performed_at);
CREATE INDEX idx_actions_type ON transaction.cashier_actions(action_type);
CREATE INDEX idx_actions_authorizer ON transaction.cashier_actions(authorized_by_employee_id) WHERE authorized_by_employee_id IS NOT NULL;

-- transaction.cash_drawer_events — drawer counts, paid-in/out, and variance tracking
CREATE TABLE transaction.cash_drawer_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  location_id             uuid NOT NULL REFERENCES location.locations(id),
  pos_terminal_id         text NOT NULL,
  cashier_employee_id     uuid REFERENCES employee.employees(id),
  event_type              text NOT NULL,
  event_at                timestamptz NOT NULL DEFAULT now(),
  expected_amount         numeric(14,4),
  counted_amount          numeric(14,4),
  variance                numeric(14,4) GENERATED ALWAYS AS (COALESCE(counted_amount, 0) - COALESCE(expected_amount, 0)) STORED,
  reason                  text,
  paid_in_out_amount      numeric(14,4),
  reference               text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_drawer_tenant ON transaction.cash_drawer_events(tenant_id);
CREATE INDEX idx_drawer_location_terminal ON transaction.cash_drawer_events(location_id, pos_terminal_id, event_at);
CREATE INDEX idx_drawer_cashier ON transaction.cash_drawer_events(cashier_employee_id, event_at);
CREATE INDEX idx_drawer_variance ON transaction.cash_drawer_events(location_id) WHERE variance IS NOT NULL AND variance != 0;

-- transaction.shift_events — operator session with denormalized totals
CREATE TABLE transaction.shift_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  location_id             uuid NOT NULL REFERENCES location.locations(id),
  pos_terminal_id         text NOT NULL,
  cashier_employee_id     uuid NOT NULL REFERENCES employee.employees(id),
  shift_start             timestamptz NOT NULL,
  shift_end               timestamptz,
  transaction_count       int NOT NULL DEFAULT 0,
  total_sales             numeric(14,4),
  starting_drawer_amount  numeric(14,4),
  ending_drawer_amount    numeric(14,4),
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, pos_terminal_id, cashier_employee_id, shift_start)
);

CREATE INDEX idx_shifts_tenant ON transaction.shift_events(tenant_id);
CREATE INDEX idx_shifts_location ON transaction.shift_events(location_id);
CREATE INDEX idx_shifts_cashier ON transaction.shift_events(cashier_employee_id);
CREATE INDEX idx_shifts_active ON transaction.shift_events(location_id) WHERE shift_end IS NULL;

-- transaction.loyalty_events — append-only loyalty earn/redeem/adjust log
CREATE TABLE transaction.loyalty_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  loyalty_membership_id   uuid NOT NULL REFERENCES customer.loyalty_memberships(id),
  transaction_id          uuid REFERENCES transaction.transactions(id),
  event_type              text NOT NULL,
  points_delta            bigint NOT NULL,
  amount_basis            numeric(14,4),
  reason                  text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_loyalty_evt_tenant ON transaction.loyalty_events(tenant_id);
CREATE INDEX idx_loyalty_evt_member ON transaction.loyalty_events(loyalty_membership_id, created_at);
CREATE INDEX idx_loyalty_evt_tx ON transaction.loyalty_events(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_loyalty_evt_type ON transaction.loyalty_events(event_type);

-- transaction.gift_card_events — append-only gift card activity log
CREATE TABLE transaction.gift_card_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  gift_card_id            uuid NOT NULL,
  transaction_id          uuid REFERENCES transaction.transactions(id),
  event_type              text NOT NULL,
  amount_delta            numeric(14,4) NOT NULL,
  balance_after           numeric(14,4) NOT NULL,
  authorization_code      text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_gc_tenant ON transaction.gift_card_events(tenant_id);
CREATE INDEX idx_gc_card ON transaction.gift_card_events(gift_card_id, created_at);
CREATE INDEX idx_gc_tx ON transaction.gift_card_events(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_gc_type ON transaction.gift_card_events(event_type);
