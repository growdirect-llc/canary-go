-- 07_p_f_pricing_finance.sql — Pricing + Financial Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §8 (lines 2682-3217)
-- Schemas: p (Pricing), f (Finance)

-- pricing.promotions — promotion header (item_prices forward-references this, so create first)
CREATE TABLE pricing.promotions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  promotion_code      text NOT NULL,
  name                text NOT NULL,
  description         text,
  promotion_type      text NOT NULL DEFAULT 'percent_off',           -- percent_off | amount_off | bogo | x_for_y | tier_threshold | bundle | fixed_price | loyalty_member_price
  scope_type          text NOT NULL DEFAULT 'item',                  -- item | category | brand | merchandise_total | tender | customer_segment
  effective_start     timestamptz NOT NULL,
  effective_end       timestamptz,
  active_days         int[] DEFAULT '{1,2,3,4,5,6,7}',               -- ISO day-of-week (1=Monday)
  active_hours        jsonb DEFAULT '{}',                            -- {"start": "08:00", "end": "20:00"}
  active_locations    uuid[],                                        -- NULL = all; array of location.locations.id
  active_channels     text[] DEFAULT '{}',                           -- {} = all
  customer_segments   text[],                                        -- target loyalty tiers / segments
  stackable           boolean NOT NULL DEFAULT false,                -- can stack with other promotions?
  exclusive_with      uuid[],                                        -- IDs of promotions that block this one
  max_uses_total      int,                                           -- across all customers
  max_uses_per_customer int,
  current_uses        int NOT NULL DEFAULT 0,
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'draft',                 -- draft | scheduled | active | paused | expired | cancelled
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, promotion_code)
);

CREATE INDEX idx_promo_tenant ON pricing.promotions(tenant_id);
CREATE INDEX idx_promo_active ON pricing.promotions(effective_start, effective_end) WHERE status = 'active';
CREATE INDEX idx_promo_status ON pricing.promotions(status) WHERE status NOT IN ('expired', 'cancelled');

-- pricing.item_prices — multi-scope pricing with temporal exclusion
CREATE TABLE pricing.item_prices (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  location_id         uuid REFERENCES location.locations(id),               -- NULL = default for all locations
  zone_id             uuid REFERENCES location.location_zones(id),          -- NULL = location-wide
  channel             text DEFAULT 'all',                            -- all | brick | web | bopis | marketplace
  price_type          text NOT NULL DEFAULT 'regular',               -- regular | clearance | member | wholesale | cost_plus
  amount              numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  uom                 text NOT NULL DEFAULT 'EA',                    -- price per EA, LB, KG, etc.
  effective_start     timestamptz NOT NULL DEFAULT now(),
  effective_end       timestamptz,                                    -- NULL = open-ended
  source_promotion_id uuid REFERENCES pricing.promotions(id),               -- if price came from a promotion
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  -- SDD-fix: original used COALESCE inside EXCLUDE — Postgres EXCLUDE doesn't
  -- accept function expressions. We split into two partial EXCLUDE constraints:
  -- one for rows where (location_id, zone_id) are both NOT NULL, one where
  -- both are NULL. Cross-NULL/non-NULL combinations are rare and accepted as
  -- distinct (consistent with NULL-distinct semantics elsewhere). Refine if
  -- a pricing edge-case surfaces.
  EXCLUDE USING gist (
    tenant_id WITH =, item_id WITH =,
    location_id WITH =, zone_id WITH =,
    channel WITH =, price_type WITH =,
    tstzrange(effective_start, effective_end, '[)') WITH &&
  ) WHERE (status = 'active' AND location_id IS NOT NULL AND zone_id IS NOT NULL),
  EXCLUDE USING gist (
    tenant_id WITH =, item_id WITH =,
    channel WITH =, price_type WITH =,
    tstzrange(effective_start, effective_end, '[)') WITH &&
  ) WHERE (status = 'active' AND location_id IS NULL AND zone_id IS NULL)
);

CREATE INDEX idx_iprice_tenant ON pricing.item_prices(tenant_id);
CREATE INDEX idx_iprice_item ON pricing.item_prices(item_id);
CREATE INDEX idx_iprice_location ON pricing.item_prices(location_id);
-- SDD-fix: dropped now() from predicate (STABLE, not IMMUTABLE — index predicates
-- must be IMMUTABLE). Active-prices-as-of-now check moved to query layer.
CREATE INDEX idx_iprice_active ON pricing.item_prices(item_id, location_id, channel) WHERE status = 'active';

-- pricing.promotion_rules — flexible trigger/benefit rules
CREATE TABLE pricing.promotion_rules (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  promotion_id        uuid NOT NULL REFERENCES pricing.promotions(id) ON DELETE CASCADE,
  rule_order          int NOT NULL DEFAULT 1,                        -- sequence (some rules check before others)
  trigger_type        text NOT NULL,                                  -- buy_quantity | spend_amount | own_loyalty_card | scan_coupon | match_basket
  trigger_qualifier   jsonb NOT NULL DEFAULT '{}',                    -- {item_ids: [], category_ids: [], min_quantity: 2, min_amount: 25.00}
  benefit_type        text NOT NULL,                                  -- amount_off | percent_off | fixed_price | free_item | tier_unlock
  benefit_qualifier   jsonb NOT NULL DEFAULT '{}',                    -- {amount: 5.00, percent: 0.20, fixed_price: 10.00, free_item_ids: []}
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_prules_tenant ON pricing.promotion_rules(tenant_id);
CREATE INDEX idx_prules_promo ON pricing.promotion_rules(promotion_id);

-- pricing.tax_classes — tax category master
CREATE TABLE pricing.tax_classes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  code            text NOT NULL,                                  -- "STD", "FOOD", "RX", "ALCOHOL", "SERVICE", "EXEMPT"
  name            text NOT NULL,
  description     text,
  is_default      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code),
  CONSTRAINT one_default_class EXCLUDE (tenant_id WITH =) WHERE (is_default = true AND status = 'active')
);

CREATE INDEX idx_tclasses_tenant ON pricing.tax_classes(tenant_id);

-- pricing.tax_rates — location × class effective-dated rates
CREATE TABLE pricing.tax_rates (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  tax_class_id    uuid NOT NULL REFERENCES pricing.tax_classes(id),
  location_id     uuid REFERENCES location.locations(id),                  -- NULL = applies tenant-wide (default)
  jurisdiction    text,                                              -- "CA", "CA-LA-County", "EU-DE", "EU-DE-Berlin" — for tax-engine integration
  rate_type       text NOT NULL DEFAULT 'percentage',                -- percentage | flat_amount | tiered
  rate            numeric(8,6) NOT NULL,                             -- 0.0825 for 8.25%; for tiered, JSONB schedule in attributes
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,
  attributes      jsonb NOT NULL DEFAULT '{}',                       -- VAT details, GST/HST distinction, multi-rate schedule
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE NULLS NOT DISTINCT (tenant_id, tax_class_id, location_id, effective_start)  -- SDD-fix: replaced COALESCE-in-UNIQUE
);

CREATE INDEX idx_trates_tenant ON pricing.tax_rates(tenant_id);
CREATE INDEX idx_trates_class ON pricing.tax_rates(tax_class_id);
CREATE INDEX idx_trates_location ON pricing.tax_rates(location_id);
-- SDD-fix: dropped CURRENT_DATE from predicate (not IMMUTABLE).
CREATE INDEX idx_trates_active ON pricing.tax_rates(tax_class_id, location_id) WHERE effective_end IS NULL;

-- finance.gl_accounts — chart of accounts (recursive parent); tender_types FKs this so create first
CREATE TABLE finance.gl_accounts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES finance.gl_accounts(id),
  code            text NOT NULL,                                  -- merchant chart of accounts code
  name            text NOT NULL,
  account_type    text NOT NULL,                                  -- asset | liability | equity | revenue | expense | contra
  account_subtype text,                                           -- current_asset | inventory | accounts_payable | sales | cogs | etc.
  is_postable     boolean NOT NULL DEFAULT true,                  -- false for parent rollups
  currency        text NOT NULL DEFAULT 'USD',
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_gl_tenant ON finance.gl_accounts(tenant_id);
CREATE INDEX idx_gl_parent ON finance.gl_accounts(parent_id);
CREATE INDEX idx_gl_type ON finance.gl_accounts(account_type);

-- finance.tender_types — payment method master
-- source_code (loop3-wave1, GRO-762 §B.2): the POS adapter that
-- declares this tender (square|counterpoint|clover). NULL when the
-- tender is merchant-defined and not bound to one upstream source.
-- The Sub2 persistence path uses (tenant_id, source_code) to resolve
-- a default tender_type_id when the inbound payload doesn't carry one
-- (most adapters don't surface a stable tender-type identifier in
-- their wire envelope). Per OQ-2.3 + Loop 2 finding #7.
CREATE TABLE finance.tender_types (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  source_code     text,                                            -- POS adapter that owns this default; NULL = merchant-defined
  code            text NOT NULL,                                  -- CASH, VISA, MC, AMEX, EBT, GIFT, STORE_CREDIT, CHECK
  name            text NOT NULL,
  tender_class    text NOT NULL,                                  -- cash | credit_card | debit_card | gift_card | store_credit | check | electronic_check | ebt_snap | wic | crypto
  is_active       boolean NOT NULL DEFAULT true,
  is_change_giving boolean NOT NULL DEFAULT false,                 -- can give change as this tender (cash yes; gift card no)
  is_refundable   boolean NOT NULL DEFAULT true,
  open_drawer     boolean NOT NULL DEFAULT false,                  -- triggers cash drawer (cash, check)
  gl_account_id   uuid REFERENCES finance.gl_accounts(id),               -- accounting destination
  rounding_rule   text,                                             -- nearest_cent | nickel | etc. (cash-rounding for currencies that need it)
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

-- Partial unique index — at most one source-default tender per
-- (tenant, source). Lets Sub2 ResolveTenderType() be a deterministic
-- lookup, while leaving merchants free to add unlimited custom
-- tender_types with source_code IS NULL.
CREATE UNIQUE INDEX uq_tender_source_default ON finance.tender_types(tenant_id, source_code) WHERE source_code IS NOT NULL;

CREATE INDEX idx_tender_tenant ON finance.tender_types(tenant_id);
CREATE INDEX idx_tender_class ON finance.tender_types(tender_class);
CREATE INDEX idx_tender_source ON finance.tender_types(source_code) WHERE source_code IS NOT NULL;

-- finance.supplier_invoices — AP invoice with three-way match FKs
CREATE TABLE finance.supplier_invoices (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  invoice_number      text NOT NULL,                              -- vendor's invoice number
  vendor_id           uuid NOT NULL REFERENCES catalog.vendors(id),
  invoice_date        date NOT NULL,
  due_date            date,
  related_po_id       uuid REFERENCES orders.purchase_orders(id),      -- three-way match: invoice ↔ PO
  related_receipt_document_id uuid REFERENCES inventory.inventory_documents(id),  -- invoice ↔ receipt (third leg)
  status              text NOT NULL DEFAULT 'received',           -- received | matched | discrepancy | approved | paid | disputed | cancelled
  subtotal            numeric(14,4) NOT NULL,
  tax_total           numeric(14,4) NOT NULL DEFAULT 0,
  shipping_total      numeric(14,4) NOT NULL DEFAULT 0,
  discount_total      numeric(14,4) NOT NULL DEFAULT 0,
  grand_total         numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  match_status        text NOT NULL DEFAULT 'pending',            -- pending | matched | partial_match | mismatch | manual_override
  match_variance      numeric(14,4),                              -- variance vs PO + receipt
  approval_user_id    uuid REFERENCES app.users(id),
  approved_at         timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',                -- vendor_credit_note_ref, payment_terms_override, original_doc_url
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, vendor_id, invoice_number)
);

CREATE INDEX idx_sinv_tenant ON finance.supplier_invoices(tenant_id);
CREATE INDEX idx_sinv_vendor ON finance.supplier_invoices(vendor_id);
CREATE INDEX idx_sinv_po ON finance.supplier_invoices(related_po_id);
CREATE INDEX idx_sinv_receipt ON finance.supplier_invoices(related_receipt_document_id);
CREATE INDEX idx_sinv_status ON finance.supplier_invoices(status) WHERE status NOT IN ('paid', 'cancelled');
CREATE INDEX idx_sinv_due ON finance.supplier_invoices(due_date) WHERE status = 'approved';

-- finance.supplier_invoice_lines — invoice detail with line-level three-way match
CREATE TABLE finance.supplier_invoice_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  invoice_id          uuid NOT NULL REFERENCES finance.supplier_invoices(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  related_po_line_id  uuid REFERENCES orders.purchase_order_lines(id),     -- three-way match at line level
  related_receipt_line_id uuid REFERENCES inventory.inventory_document_lines(id),
  item_id             uuid REFERENCES catalog.items(id),                    -- nullable for non-merchandise lines (freight, fees)
  description         text NOT NULL,
  quantity            numeric(14,4),
  unit_cost           numeric(14,4),
  line_total          numeric(14,4) NOT NULL,
  tax_amount          numeric(14,4) NOT NULL DEFAULT 0,
  gl_account_id       uuid REFERENCES finance.gl_accounts(id),              -- override GL account for this line
  match_variance      numeric(14,4),                                  -- variance vs PO line / receipt line
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, invoice_id, line_number)
);

CREATE INDEX idx_sinvline_tenant ON finance.supplier_invoice_lines(tenant_id);
CREATE INDEX idx_sinvline_invoice ON finance.supplier_invoice_lines(invoice_id);
CREATE INDEX idx_sinvline_po_line ON finance.supplier_invoice_lines(related_po_line_id);
CREATE INDEX idx_sinvline_receipt_line ON finance.supplier_invoice_lines(related_receipt_line_id);

-- finance.payments — AP payment header
CREATE TABLE finance.payments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  payment_number      text NOT NULL,
  vendor_id           uuid NOT NULL REFERENCES catalog.vendors(id),
  payment_method      text NOT NULL,                              -- check | ach | wire | credit_card | virtual_card
  payment_date        date NOT NULL,
  amount              numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  bank_account_id     uuid,                                       -- references app.bank_accounts in current Canary spec
  reference_number    text,                                       -- check #, wire ref, ACH trace
  status              text NOT NULL DEFAULT 'scheduled',          -- scheduled | issued | cleared | voided | bounced
  cleared_at          timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, payment_number)
);

-- finance.payment_invoice_applications — many-to-many payment ↔ invoice
CREATE TABLE finance.payment_invoice_applications (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  payment_id          uuid NOT NULL REFERENCES finance.payments(id) ON DELETE CASCADE,
  invoice_id          uuid NOT NULL REFERENCES finance.supplier_invoices(id),
  amount_applied      numeric(14,4) NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_pay_tenant ON finance.payments(tenant_id);
CREATE INDEX idx_pay_vendor ON finance.payments(vendor_id);
CREATE INDEX idx_pay_status ON finance.payments(status) WHERE status IN ('scheduled', 'issued');
CREATE INDEX idx_pay_invapp_payment ON finance.payment_invoice_applications(payment_id);
CREATE INDEX idx_pay_invapp_invoice ON finance.payment_invoice_applications(invoice_id);

-- finance.markup_envelope_tiers — platform-wide cost-plus markup defaults per
-- merchant archetype. Per-tenant override path is
-- app.tenants.attributes->>'markup_envelope_pct'; the pricing module
-- reads tenant override first, falls back to the active row here for
-- the tenant's archetype.
CREATE TABLE finance.markup_envelope_tiers (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  archetype       text NOT NULL,                              -- small | medium | large
  markup_pct      numeric(5,2) NOT NULL,                      -- 50.00 / 30.00 / 15.00 per archetype
  effective_at    timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz,                                -- NULL = open-ended
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK (archetype IN ('small','medium','large')),
  CHECK (markup_pct >= 0 AND markup_pct <= 100)
);

-- One active row per archetype at any given time (the partial unique
-- index enforces "at most one open-ended row per archetype"). When a
-- new tier replaces the prior, the application sets expires_at on the
-- old row in the same transaction as the insert.
CREATE UNIQUE INDEX uq_markup_envelope_active
  ON finance.markup_envelope_tiers(archetype)
  WHERE expires_at IS NULL;


-- ─────────────────────────────────────────────────────────────────────
-- Observed-price-rule ledger (GRO-880 P0 #6 / GRO-884)
-- ─────────────────────────────────────────────────────────────────────
-- Counterpoint exposes only PRC_1 + REG_PRC + LST_COST in REST IM_ITEM;
-- the 10-layer pricing precedence (location-specific / special /
-- contract / promotional / BOGO / mix-match / break / minimum /
-- customer-tier) surfaces only as outputs in PS_DOC_LIN_PRICE per
-- transaction line. Canary observes these here rather than replicating
-- the pricing engine. Populated by transaction adapter, NOT catalog
-- adapter.

CREATE TABLE pricing.observed_price_rules (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  item_id         uuid NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
  -- nullable — class is derivable from customer.CATEG_COD when present
  customer_class  text,
  rule_code       text NOT NULL,                                   -- POS-emitted: REG | SPECIAL | CONTRACT | MIX_MATCH | BOGO | BREAK | MINIMUM | TIER (Counterpoint vocabulary; Square-source maps to similar)
  observed_price  numeric(12,4) NOT NULL,
  observed_qty    numeric(10,4) NOT NULL DEFAULT 1,
  -- back-link to PS_DOC_LIN-equivalent transaction line; nullable
  -- since some observations may come from non-transactional sources
  transaction_id  uuid,
  observed_at     timestamptz NOT NULL DEFAULT now(),
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_observed_prices_tenant_item
  ON pricing.observed_price_rules(tenant_id, item_id, observed_at DESC);
CREATE INDEX idx_observed_prices_rule
  ON pricing.observed_price_rules(rule_code, observed_at DESC);
