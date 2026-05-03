-- deploy/migrations/021_protocol_namespace.up.sql
--
-- .jeffe namespace registration table. Node identity layer of the
-- Canary Protocol (patent Application 63/991,596, FIG. 5).
-- Every entity — merchant, user, or agent — can claim a .jeffe name,
-- inscribed as a Bitcoin ordinal on signet. The inscription_id is the
-- on-chain proof of identity. GRO-751.

CREATE TABLE IF NOT EXISTS protocol.namespace_registrations (
    reg_id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text        NOT NULL UNIQUE,   -- e.g. "acme-hardware.jeffe"
    owner_id         uuid        NOT NULL,          -- merchant_id or user_id — opaque ref
    owner_type       text        NOT NULL           -- "merchant" | "user" | "agent"
                     CHECK (owner_type IN ('merchant','user','agent')),
    raas_uuid        uuid        NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    inscription_id   text,                          -- null until on-chain confirmed
    btc_tx_id        text,
    btc_block_height bigint,
    network          text        NOT NULL DEFAULT 'signet',
    reg_status       text        NOT NULL DEFAULT 'pending'
                     CHECK (reg_status IN ('pending','active','expired','revoked')),
    payload_hash     text        NOT NULL,          -- SHA-256 of the registration payload
    registered_at    timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz                    -- null = perpetual
);

CREATE INDEX IF NOT EXISTS idx_ns_reg_owner
    ON protocol.namespace_registrations(owner_id, owner_type);

CREATE INDEX IF NOT EXISTS idx_ns_reg_name
    ON protocol.namespace_registrations(name);
