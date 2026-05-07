-- 009_identity_sessions_audit.up.sql
CREATE TABLE IF NOT EXISTS app.audit_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        REFERENCES app.merchants(id),
    user_id     UUID        REFERENCES app.users(id),
    action      TEXT        NOT NULL,
    resource    TEXT,
    resource_id UUID,
    ip_address  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_merchant_id ON app.audit_log (merchant_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_user_id ON app.audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON app.audit_log (created_at);

CREATE TABLE IF NOT EXISTS app.interest_signups (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email      TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
