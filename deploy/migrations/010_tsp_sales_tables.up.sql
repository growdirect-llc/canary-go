-- 010_tsp_sales_tables.up.sql
CREATE TABLE IF NOT EXISTS sales.transactions (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID        NOT NULL REFERENCES app.merchants(id),
    external_id             TEXT        NOT NULL,
    location_id             UUID        REFERENCES app.locations(id),
    employee_id             UUID        REFERENCES app.employees(id),
    customer_id             UUID        REFERENCES app.customers(id),
    arts_business_date      DATE        NOT NULL,
    arts_workstation_id     TEXT,
    transaction_type        TEXT        NOT NULL DEFAULT 'SALE',
    total_cents             BIGINT      NOT NULL DEFAULT 0,
    subtotal_cents          BIGINT      NOT NULL DEFAULT 0,
    tax_cents               BIGINT      NOT NULL DEFAULT 0,
    tip_cents               BIGINT      NOT NULL DEFAULT 0,
    discount_cents          BIGINT      NOT NULL DEFAULT 0,
    tender_type             TEXT,
    card_fingerprint        TEXT,
    card_last4              TEXT,
    card_bin                TEXT,
    card_exp_month          SMALLINT,
    card_exp_year           SMALLINT,
    statement_description   TEXT,
    payload                 JSONB,
    source_code             TEXT        REFERENCES app.source_systems(code),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_transactions_external_id UNIQUE (merchant_id, external_id)
);

CREATE INDEX IF NOT EXISTS idx_transactions_merchant_id ON sales.transactions (merchant_id);
CREATE INDEX IF NOT EXISTS idx_transactions_merchant_date ON sales.transactions (merchant_id, arts_business_date);
CREATE INDEX IF NOT EXISTS idx_transactions_employee_id ON sales.transactions (employee_id);
CREATE INDEX IF NOT EXISTS idx_transactions_location_id ON sales.transactions (location_id);

CREATE TABLE IF NOT EXISTS sales.line_items (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    transaction_id  UUID        NOT NULL REFERENCES sales.transactions(id),
    product_id      UUID        REFERENCES app.products(id),
    external_id     TEXT,
    quantity        NUMERIC(10,4) NOT NULL DEFAULT 1,
    unit_price_cents BIGINT     NOT NULL DEFAULT 0,
    total_cents     BIGINT      NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_line_items_transaction_id ON sales.line_items (transaction_id);
CREATE INDEX IF NOT EXISTS idx_line_items_merchant_id ON sales.line_items (merchant_id);

CREATE TABLE IF NOT EXISTS sales.line_item_discounts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    line_item_id    UUID        NOT NULL REFERENCES sales.line_items(id),
    discount_name   TEXT,
    discount_cents  BIGINT      NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_line_item_discounts_line_item_id ON sales.line_item_discounts (line_item_id);

CREATE TABLE IF NOT EXISTS sales.refund_links (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    refund_transaction_id UUID      NOT NULL REFERENCES sales.transactions(id),
    original_transaction_id UUID    NOT NULL REFERENCES sales.transactions(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_refund_links_merchant_id ON sales.refund_links (merchant_id);
CREATE INDEX IF NOT EXISTS idx_refund_links_original ON sales.refund_links (original_transaction_id);

CREATE TABLE IF NOT EXISTS sales.cash_drawer_shifts (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    location_id         UUID        REFERENCES app.locations(id),
    employee_id         UUID        REFERENCES app.employees(id),
    external_id         TEXT        NOT NULL,
    opened_at           TIMESTAMPTZ NOT NULL,
    closed_at           TIMESTAMPTZ,
    expected_cents      BIGINT,
    actual_cents        BIGINT,
    discrepancy_cents   BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_cash_drawer_shifts_external UNIQUE (merchant_id, external_id)
);

CREATE INDEX IF NOT EXISTS idx_cash_drawer_shifts_merchant_id ON sales.cash_drawer_shifts (merchant_id);

CREATE TABLE IF NOT EXISTS sales.cash_drawer_events (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    shift_id        UUID        NOT NULL REFERENCES sales.cash_drawer_shifts(id),
    employee_id     UUID        REFERENCES app.employees(id),
    event_type      TEXT        NOT NULL,
    amount_cents    BIGINT,
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cash_drawer_events_shift_id ON sales.cash_drawer_events (shift_id);
