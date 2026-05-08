-- canary_identity_gcp / canary_identity_gcp_test — declarative schema.
--
-- T-1.a / GRO-848. Identity service owns this database. Cross-product
-- consumers (canary, atlasview, future products) talk to identity over
-- HTTP only. See Brain/wiki/cards/platform-identity-database-boundary.md.
--
-- Edit this file to change the schema; run `make identity-db-reset`
-- (LOCAL ONLY) to drop + recreate. Migrations in
-- deploy/migrations/identity/ ship the same DDL incrementally for
-- deployed databases.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- ─────────────────────────────────────────────────────────────────────
-- public.persons — canonical identity record.
--
-- One row per human (or system principal) across the firm. Other
-- products carry person_id as a value, never an FK; the identity
-- service is the source of truth for resolution.
--
-- org_id is a value-only reference. The canonical Organization record
-- lives in whichever product DB owns that organization (canary_gcp.app.
-- organizations today; AtlasView's Postgres later). Cross-product sync
-- happens over HTTP, not at the DB.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE public.persons (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                UUID        NOT NULL,
    email                 CITEXT      NOT NULL,
    first_name            TEXT,
    last_name             TEXT,
    display_name          TEXT,
    phone                 TEXT,
    picture_url           TEXT,
    picture_thumbnail_url TEXT,
    -- AtlasView's user_type taxonomy (GRO-848 §2). 'system' marks
    -- non-human principals (service Persons, automation accounts).
    user_type             TEXT        NOT NULL DEFAULT 'regular'
                                      CHECK (user_type IN
                                          ('read_only','regular','power','admin','system')),
    is_system             BOOLEAN     NOT NULL DEFAULT false,
    is_active             BOOLEAN     NOT NULL DEFAULT true,
    last_login_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Email is unique within an org. A human in two orgs holds two
    -- Person rows; person_id is what stitches their identity at
    -- the application layer (not in this table).
    CONSTRAINT uq_persons_org_email UNIQUE (org_id, email)
);
CREATE INDEX idx_persons_email ON public.persons (email);
CREATE INDEX idx_persons_org   ON public.persons (org_id);

COMMENT ON TABLE public.persons IS
    'Canonical identity record. Cross-product Person id; org_id is '
    'a value-only reference into whichever product DB owns the org.';

-- ─────────────────────────────────────────────────────────────────────
-- public.person_credentials — password + MFA secrets.
--
-- One row per person. Owned by identity service only. No other
-- service reads this table; password verification is wrapped behind
-- /auth/login.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE public.person_credentials (
    person_id           UUID        PRIMARY KEY
                                    REFERENCES public.persons(id) ON DELETE CASCADE,
    -- argon2id-encoded; full encoded form including parameters and salt.
    -- Verifier reconstructs parameters from the encoding.
    password_hash       TEXT        NOT NULL,
    password_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- TOTP shared secret, encrypted at rest by application (envelope
    -- key in production). NULL while MFA is not enrolled.
    mfa_secret          TEXT,
    mfa_enabled         BOOLEAN     NOT NULL DEFAULT false,
    -- Lockout primitives — login increments on failure, resets on
    -- success. locked_until is the soft-lock window; if non-null and
    -- in the future, /auth/login refuses regardless of password.
    failed_login_count  INT         NOT NULL DEFAULT 0,
    locked_until        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE public.person_credentials IS
    'Password (argon2id) + optional TOTP secret per Person. Owned '
    'exclusively by /auth/* handlers — never read elsewhere.';

-- ─────────────────────────────────────────────────────────────────────
-- public.refresh_tokens — refresh-token rotation chain.
--
-- Each row is one refresh token. family_id ties a chain of rotations
-- together: on /auth/refresh the parent row's used_at is set and a
-- child row is inserted with the same family_id and parent_id pointing
-- back. Reuse-detection: presenting a token whose used_at is non-NULL
-- means a sibling rotation already happened — invalidate the entire
-- family by setting revoked_at on every row sharing family_id.
--
-- The opaque secret never lives in the DB; we store sha256(secret).
-- Clients hold the secret only.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE public.refresh_tokens (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id       UUID        NOT NULL REFERENCES public.persons(id) ON DELETE CASCADE,
    family_id       UUID        NOT NULL,
    parent_id       UUID        REFERENCES public.refresh_tokens(id),
    -- sha256(secret), hex-encoded. Index lookup on /auth/refresh.
    token_hash      TEXT        NOT NULL UNIQUE,
    -- Audiences this refresh is permitted to mint access tokens for.
    -- Subset of {atlasview, canary, ...}; minter narrows on each
    -- exchange.
    audiences       TEXT[]      NOT NULL,
    -- Org id at time of issue. Refresh is always within the same org;
    -- re-org switch requires a fresh login.
    org_id          UUID        NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    -- Set on successful exchange. A second presentation of a row with
    -- used_at set is the reuse signal that compromises the family.
    used_at         TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_refresh_tokens_person   ON public.refresh_tokens (person_id);
CREATE INDEX idx_refresh_tokens_family   ON public.refresh_tokens (family_id);
CREATE INDEX idx_refresh_tokens_lookup
    ON public.refresh_tokens (expires_at)
    WHERE revoked_at IS NULL AND used_at IS NULL;

COMMENT ON TABLE public.refresh_tokens IS
    'Refresh-token rotation chain with family_id reuse-detection. '
    'token_hash = sha256(secret); the secret only lives client-side.';
