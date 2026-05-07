-- 034_refresh_token_families.up.sql
--
-- T-1 / GRO-861 — refresh-token family tracking with reuse detection.
--
-- One row per refresh-token family. A family is created on /auth/login
-- and continues across /auth/refresh rotations. Each rotation updates
-- last_jti to the newly minted refresh-token jti.
--
-- Reuse detection: if /auth/refresh receives a token whose jti does
-- NOT match family.last_jti (and the family isn't already revoked),
-- the family is revoked family-wide. This is the standard
-- OAuth 2.1 / RFC 6819 §5.2.2.3 pattern: a stolen token + the legit
-- client both racing to refresh produces exactly one valid rotation
-- and one detected reuse, which kills the chain regardless of which
-- side won the race.

CREATE TABLE app.refresh_token_families (
    id              UUID PRIMARY KEY,
    subject         UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_jti        TEXT NOT NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT
);

CREATE INDEX refresh_token_families_subject
    ON app.refresh_token_families(subject)
    WHERE revoked_at IS NULL;

COMMENT ON TABLE app.refresh_token_families IS
    'Refresh-token family ledger. T-1 / GRO-861. Reuse-detection: '
    'a refresh request whose jti != last_jti revokes the whole '
    'family (OAuth 2.1 / RFC 6819 §5.2.2.3 pattern).';
