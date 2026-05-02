-- 003_identity_merchants.up.sql
CREATE TABLE IF NOT EXISTS app.merchants (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID        NOT NULL REFERENCES app.organizations(id),
    source_merchant_id  TEXT        NOT NULL UNIQUE,
    merchant_name       TEXT        NOT NULL,
    currency            CHAR(3)     NOT NULL DEFAULT 'USD',
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_merchants_organization_id ON app.merchants (organization_id);
CREATE INDEX IF NOT EXISTS idx_merchants_source_merchant_id ON app.merchants (source_merchant_id);

CREATE TABLE IF NOT EXISTS app.merchant_settings (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID        NOT NULL UNIQUE REFERENCES app.merchants(id),
    timezone                TEXT        NOT NULL DEFAULT 'UTC',
    language                TEXT        NOT NULL DEFAULT 'en',
    date_format             TEXT,
    calendar_type           TEXT        NOT NULL DEFAULT 'calendar_month'
                                        CHECK (calendar_type IN ('nrf_454','calendar_month')),
    fiscal_year_start_month SMALLINT,
    fiscal_week_start_day   SMALLINT,
    fiscal_pattern          TEXT,
    notif_email_enabled     BOOLEAN     NOT NULL DEFAULT true,
    notif_sms_enabled       BOOLEAN     NOT NULL DEFAULT false,
    notif_in_app_enabled    BOOLEAN     NOT NULL DEFAULT true,
    notif_quiet_hours_start SMALLINT,
    notif_quiet_hours_end   SMALLINT,
    notif_severity_threshold TEXT,
    notif_daily_limit       INTEGER,
    notif_phone             TEXT,
    theme                   TEXT,
    show_employee_names     BOOLEAN     NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_merchant_settings_merchant_id ON app.merchant_settings (merchant_id);
