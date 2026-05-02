-- 015_protocol_source_secrets.up.sql
--
-- Adds the protocol schema and the source_secrets table used by the
-- API Gateway (Node 2 of the patent pipeline, GRO-746) to verify
-- HMAC-SHA256 signatures on inbound webhooks.
--
-- Patent reference: Application 63/991,596, Node 2.
-- The patent architecture's protocol.* schema lives here.
--
-- NOTE on secret storage:
--   The `secret` column is plaintext in v1. Production hardening — moving
--   secrets behind a Secrets Manager with envelope encryption — is tracked
--   in GRO-687. Until that lands, do not store production webhook secrets
--   in this column on a shared cluster.

CREATE SCHEMA IF NOT EXISTS protocol;

CREATE TABLE IF NOT EXISTS protocol.source_secrets (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID         NOT NULL REFERENCES app.merchants(id),
    source_code           TEXT         NOT NULL REFERENCES app.source_systems(code),
    secret                TEXT         NOT NULL,
    signature_algo        TEXT         NOT NULL DEFAULT 'HMAC-SHA256'
                                       CHECK (signature_algo = 'HMAC-SHA256'),
    status                TEXT         NOT NULL DEFAULT 'active'
                                       CHECK (status IN ('active','rotated','revoked')),
    replay_window_seconds INT          NOT NULL DEFAULT 300
                                       CHECK (replay_window_seconds BETWEEN 30 AND 3600),
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT now(),
    rotated_at            TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_protocol_source_secrets_active
    ON protocol.source_secrets (merchant_id, source_code)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_protocol_source_secrets_merchant
    ON protocol.source_secrets (merchant_id);
