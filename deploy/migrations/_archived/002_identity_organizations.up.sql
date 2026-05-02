-- 002_identity_organizations.up.sql
CREATE TABLE IF NOT EXISTS app.organizations (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_name            TEXT        NOT NULL,
    billing_email       TEXT,
    subscription_tier   TEXT        NOT NULL DEFAULT 'starter'
                                    CHECK (subscription_tier IN ('starter','professional','enterprise')),
    billing_provider    TEXT        CHECK (billing_provider IN ('square','manual','none')),
    billing_external_id TEXT,
    billing_status      TEXT        CHECK (billing_status IN ('trialing','active','past_due','canceled','comped')),
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          UUID,
    modified_by         UUID,
    db_status           TEXT        NOT NULL DEFAULT 'active'
                                    CHECK (db_status IN ('draft','active','archived')),
    db_effective_from   TIMESTAMPTZ,
    db_effective_to     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_organizations_is_active ON app.organizations (is_active);
