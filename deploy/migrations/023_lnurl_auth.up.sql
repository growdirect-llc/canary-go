-- 023_lnurl_auth.up.sql
--
-- LNURL-auth login surface for the Canary Protocol (GRO-753).
-- Implements LNURL-auth spec §9: k1 challenge/response with secp256k1
-- keys from a Lightning wallet. No passwords; sessions are JWT-gated.
--
-- Two tables:
--   lnurl_auth_challenges — one row per QR scan, TTL 5 minutes.
--   lnurl_linked_keys     — persistent wallet-key ↔ owner mapping.

CREATE TABLE IF NOT EXISTS app.lnurl_auth_challenges (
    k1              text        PRIMARY KEY,
    linked_id       uuid,
    status          text        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','used','expired')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT now() + interval '5 minutes',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.lnurl_linked_keys (
    linking_key     text        PRIMARY KEY,
    owner_id        uuid        NOT NULL,
    owner_type      text        NOT NULL DEFAULT 'merchant'
                    CHECK (owner_type IN ('merchant','user','agent')),
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_auth_at    timestamptz NOT NULL DEFAULT now()
);
