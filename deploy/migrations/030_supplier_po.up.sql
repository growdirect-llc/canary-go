-- 030_supplier_po.up.sql
--
-- W11 / GRO-830 — Supplier + Purchase Order lifecycle.
--
-- Three new tables under the app schema (no separate procurement schema
-- today; matches the placement convention used by app.directed_tasks,
-- app.locations, app.merchants, etc.). Columns are intentionally minimal
-- — enough to support list/detail/lifecycle flows and feed the existing
-- three-way-match workflow that W5 wired off receiving close.
--
-- Out of scope per dispatch: EDI integration, vendor self-service,
-- RFQ flows.

CREATE TABLE IF NOT EXISTS app.suppliers (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID        NOT NULL REFERENCES app.merchants(id),
    supplier_name     TEXT        NOT NULL,
    contact_email     TEXT,
    contact_phone     TEXT,
    payment_terms     TEXT,
    compliance_status TEXT        NOT NULL DEFAULT 'active'
                                  CHECK (compliance_status IN ('active','review','blocked')),
    attributes        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    db_status         TEXT        NOT NULL DEFAULT 'active'
                                  CHECK (db_status IN ('draft','active','archived'))
);

CREATE INDEX IF NOT EXISTS idx_suppliers_merchant_id ON app.suppliers (merchant_id);
CREATE INDEX IF NOT EXISTS idx_suppliers_compliance ON app.suppliers (merchant_id, compliance_status);

CREATE TABLE IF NOT EXISTS app.purchase_orders (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID        NOT NULL REFERENCES app.merchants(id),
    supplier_id   UUID        NOT NULL REFERENCES app.suppliers(id),
    po_number     TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'draft'
                              CHECK (status IN ('draft','submitted','received','closed','cancelled')),
    expected_at   TIMESTAMPTZ,
    total_cost    NUMERIC(14,4),
    attributes    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at  TIMESTAMPTZ,
    closed_at     TIMESTAMPTZ,
    CONSTRAINT uq_po_merchant_number UNIQUE (merchant_id, po_number)
);

CREATE INDEX IF NOT EXISTS idx_po_merchant_id ON app.purchase_orders (merchant_id);
CREATE INDEX IF NOT EXISTS idx_po_supplier_id ON app.purchase_orders (supplier_id);
CREATE INDEX IF NOT EXISTS idx_po_status ON app.purchase_orders (merchant_id, status);

CREATE TABLE IF NOT EXISTS app.purchase_order_lines (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID        NOT NULL REFERENCES app.merchants(id),
    po_id         UUID        NOT NULL REFERENCES app.purchase_orders(id) ON DELETE CASCADE,
    line_number   INT         NOT NULL,
    item_id       UUID,
    description   TEXT,
    ordered_qty   NUMERIC(14,4) NOT NULL,
    received_qty  NUMERIC(14,4) NOT NULL DEFAULT 0,
    unit_cost     NUMERIC(14,4),
    attributes    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_po_line UNIQUE (po_id, line_number)
);

CREATE INDEX IF NOT EXISTS idx_po_lines_po_id ON app.purchase_order_lines (po_id);
CREATE INDEX IF NOT EXISTS idx_po_lines_item_id ON app.purchase_order_lines (item_id) WHERE item_id IS NOT NULL;
