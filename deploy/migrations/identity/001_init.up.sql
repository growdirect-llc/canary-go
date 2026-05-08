-- 001_init.up.sql — canary_identity_gcp schema bootstrap.
--
-- T-1.a / GRO-848. First migration in the identity DB's tree. Mirrors
-- deploy/schema/identity/00_init.sql for greenfield discipline:
-- declarative schema is source of truth, this migration applies the
-- same DDL incrementally on deployed databases.
--
-- See Brain/wiki/cards/platform-identity-database-boundary.md for why
-- identity owns its own DB.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

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
    user_type             TEXT        NOT NULL DEFAULT 'regular'
                                      CHECK (user_type IN
                                          ('read_only','regular','power','admin','system')),
    is_system             BOOLEAN     NOT NULL DEFAULT false,
    is_active             BOOLEAN     NOT NULL DEFAULT true,
    last_login_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_persons_org_email UNIQUE (org_id, email)
);
CREATE INDEX idx_persons_email ON public.persons (email);
CREATE INDEX idx_persons_org   ON public.persons (org_id);

CREATE TABLE public.person_credentials (
    person_id           UUID        PRIMARY KEY
                                    REFERENCES public.persons(id) ON DELETE CASCADE,
    password_hash       TEXT        NOT NULL,
    password_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mfa_secret          TEXT,
    mfa_enabled         BOOLEAN     NOT NULL DEFAULT false,
    failed_login_count  INT         NOT NULL DEFAULT 0,
    locked_until        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE public.refresh_tokens (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id       UUID        NOT NULL REFERENCES public.persons(id) ON DELETE CASCADE,
    family_id       UUID        NOT NULL,
    parent_id       UUID        REFERENCES public.refresh_tokens(id),
    token_hash      TEXT        NOT NULL UNIQUE,
    audiences       TEXT[]      NOT NULL,
    org_id          UUID        NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
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

COMMENT ON TABLE public.persons IS
    'Canonical identity record. Cross-product Person id; org_id is '
    'a value-only reference into whichever product DB owns the org.';
COMMENT ON TABLE public.person_credentials IS
    'Password (argon2id) + optional TOTP secret per Person. Owned '
    'exclusively by /auth/* handlers — never read elsewhere.';
COMMENT ON TABLE public.refresh_tokens IS
    'Refresh-token rotation chain with family_id reuse-detection. '
    'token_hash = sha256(secret); the secret only lives client-side.';
