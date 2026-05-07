-- 004_identity_users_roles.up.sql
CREATE TABLE IF NOT EXISTS app.roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_name   TEXT        NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_roles_role_name ON app.roles (role_name);

CREATE TABLE IF NOT EXISTS app.users (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    username        TEXT        NOT NULL,
    email           TEXT        NOT NULL,
    display_name    TEXT,
    is_active       BOOLEAN     NOT NULL DEFAULT true,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID,
    modified_by     UUID,
    db_status       TEXT        NOT NULL DEFAULT 'active'
                                CHECK (db_status IN ('draft','active','archived')),
    db_effective_from TIMESTAMPTZ,
    db_effective_to   TIMESTAMPTZ,
    CONSTRAINT uq_users_merchant_email UNIQUE (merchant_id, email)
);

CREATE INDEX IF NOT EXISTS idx_users_merchant_id ON app.users (merchant_id);
CREATE INDEX IF NOT EXISTS idx_users_email ON app.users (email);

CREATE TABLE IF NOT EXISTS app.user_roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    user_id     UUID        NOT NULL REFERENCES app.users(id),
    role_id     UUID        NOT NULL REFERENCES app.roles(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID,
    CONSTRAINT uq_user_roles_user_role UNIQUE (merchant_id, user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_merchant_id ON app.user_roles (merchant_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON app.user_roles (user_id);
