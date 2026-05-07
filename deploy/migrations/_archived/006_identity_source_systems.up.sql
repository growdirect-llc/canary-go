-- 006_identity_source_systems.up.sql
CREATE TABLE IF NOT EXISTS app.source_systems (
    code         TEXT        PRIMARY KEY,
    display_name TEXT        NOT NULL,
    category     TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.merchant_sources (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    source_code     TEXT        NOT NULL REFERENCES app.source_systems(code),
    raas_namespace  TEXT,
    status          TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','disconnected')),
    metadata_json   JSONB,
    disconnected_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID,
    modified_by     UUID
);

CREATE INDEX IF NOT EXISTS idx_merchant_sources_merchant_id ON app.merchant_sources (merchant_id);

CREATE TABLE IF NOT EXISTS app.customers (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    square_customer_id  TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          UUID,
    modified_by         UUID,
    db_status           TEXT        NOT NULL DEFAULT 'active'
                                    CHECK (db_status IN ('draft','active','archived')),
    db_effective_from   TIMESTAMPTZ,
    db_effective_to     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_customers_merchant_id ON app.customers (merchant_id);
CREATE INDEX IF NOT EXISTS idx_customers_square_customer_id ON app.customers (merchant_id, square_customer_id);

CREATE TABLE IF NOT EXISTS app.products (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    square_item_id  TEXT        NOT NULL,
    product_name    TEXT        NOT NULL,
    sku             TEXT,
    upc             TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID,
    modified_by     UUID,
    db_status       TEXT        NOT NULL DEFAULT 'active'
                                CHECK (db_status IN ('draft','active','archived')),
    db_effective_from TIMESTAMPTZ,
    db_effective_to   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_products_merchant_id ON app.products (merchant_id);
