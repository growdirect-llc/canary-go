-- 008_identity_oauth_tokens.up.sql
-- Hawk (Square) OAuth credentials
CREATE TABLE IF NOT EXISTS app.hawk_oauth_tokens (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID        NOT NULL REFERENCES app.merchants(id),
    access_token_encrypted  TEXT        NOT NULL,
    refresh_token_encrypted TEXT,
    token_type              TEXT        NOT NULL DEFAULT 'bearer',
    expires_at              TIMESTAMPTZ NOT NULL,
    scopes                  TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by              UUID,
    modified_by             UUID
);

CREATE INDEX IF NOT EXISTS idx_hawk_oauth_tokens_merchant_id ON app.hawk_oauth_tokens (merchant_id);

-- Bull (NCR Counterpoint) API credentials
CREATE TABLE IF NOT EXISTS app.bull_api_credentials (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    api_key_encrypted   TEXT        NOT NULL,
    endpoint_url        TEXT        NOT NULL,
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          UUID,
    modified_by         UUID
);

CREATE INDEX IF NOT EXISTS idx_bull_api_credentials_merchant_id ON app.bull_api_credentials (merchant_id);

CREATE TABLE IF NOT EXISTS app.bull_poll_watermarks (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    endpoint_name   TEXT        NOT NULL,
    last_modified   TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_bull_poll_watermarks_merchant_endpoint
        UNIQUE (merchant_id, endpoint_name)
);

CREATE INDEX IF NOT EXISTS idx_bull_poll_watermarks_merchant_id ON app.bull_poll_watermarks (merchant_id);

CREATE TABLE IF NOT EXISTS app.bull_merchant_config (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL UNIQUE REFERENCES app.merchants(id),
    poll_interval_s INTEGER     NOT NULL DEFAULT 300,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.bull_event_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    event_type  TEXT        NOT NULL,
    payload     JSONB,
    processed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bull_event_log_merchant_id ON app.bull_event_log (merchant_id);
