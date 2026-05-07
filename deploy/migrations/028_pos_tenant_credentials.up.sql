-- 028_pos_tenant_credentials.up.sql
--
-- Canonical credential store for all POS adapter connections. One row per
-- (merchant, source_code, company_alias) triple. company_alias is nullable
-- (most merchants connect one account per source); NULLS NOT DISTINCT ensures
-- (merchant, 'square', NULL) conflicts with itself on upsert.
--
-- source_code values: 'square', 'counterpoint', 'clover' (future)
-- credentials_enc: AES-256-GCM ciphertext (prefix 'GCM:') or sandbox
--                  plaintext fallback (prefix 'PLAIN:').
--
-- Spec: pos-adapter-substrate.md · GRO-802 Day 2.

CREATE TABLE IF NOT EXISTS app.pos_tenant_credentials (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     uuid        NOT NULL,
    source_code     text        NOT NULL,
    company_alias   text,
    credentials_enc bytea       NOT NULL,
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'revoked', 'error')),
    last_tested_at  timestamptz,
    created_at      timestamptz NOT NULL DEFAULT NOW(),
    updated_at      timestamptz NOT NULL DEFAULT NOW(),
    UNIQUE NULLS NOT DISTINCT (merchant_id, source_code, company_alias)
);

CREATE INDEX IF NOT EXISTS idx_pos_creds_merchant
    ON app.pos_tenant_credentials (merchant_id);
