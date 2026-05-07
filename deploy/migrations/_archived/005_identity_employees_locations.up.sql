-- 005_identity_employees_locations.up.sql
CREATE TABLE IF NOT EXISTS app.employees (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    square_employee_id  TEXT        NOT NULL,
    employee_name       TEXT        NOT NULL,
    email               TEXT,
    risk_score          NUMERIC(4,3) NOT NULL DEFAULT 0.0
                                    CHECK (risk_score BETWEEN 0.0 AND 1.0),
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          UUID,
    modified_by         UUID,
    db_status           TEXT        NOT NULL DEFAULT 'active'
                                    CHECK (db_status IN ('draft','active','archived')),
    db_effective_from   TIMESTAMPTZ,
    db_effective_to     TIMESTAMPTZ,
    CONSTRAINT uq_employees_merchant_square_id UNIQUE (merchant_id, square_employee_id)
);

CREATE INDEX IF NOT EXISTS idx_employees_merchant_id ON app.employees (merchant_id);
CREATE INDEX IF NOT EXISTS idx_employees_square_employee_id ON app.employees (merchant_id, square_employee_id);

CREATE TABLE IF NOT EXISTS app.locations (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL REFERENCES app.merchants(id),
    square_location_id  TEXT        NOT NULL,
    location_name       TEXT        NOT NULL,
    address_line1       TEXT,
    address_line2       TEXT,
    city                TEXT,
    state               TEXT,
    postal_code         TEXT,
    coordinates         JSONB,
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          UUID,
    modified_by         UUID,
    db_status           TEXT        NOT NULL DEFAULT 'active'
                                    CHECK (db_status IN ('draft','active','archived')),
    db_effective_from   TIMESTAMPTZ,
    db_effective_to     TIMESTAMPTZ,
    CONSTRAINT uq_locations_merchant_square_id UNIQUE (merchant_id, square_location_id)
);

CREATE INDEX IF NOT EXISTS idx_locations_merchant_id ON app.locations (merchant_id);
CREATE INDEX IF NOT EXISTS idx_locations_square_location_id ON app.locations (merchant_id, square_location_id);

CREATE TABLE IF NOT EXISTS app.location_hierarchy (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    name        TEXT        NOT NULL,
    level       SMALLINT    NOT NULL,
    parent_id   UUID        REFERENCES app.location_hierarchy(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID,
    db_status   TEXT        NOT NULL DEFAULT 'active'
                            CHECK (db_status IN ('draft','active','archived')),
    db_effective_from TIMESTAMPTZ,
    db_effective_to   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_location_hierarchy_merchant_id ON app.location_hierarchy (merchant_id);
CREATE INDEX IF NOT EXISTS idx_location_hierarchy_parent_id ON app.location_hierarchy (parent_id);

CREATE TABLE IF NOT EXISTS app.user_employee_links (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    user_id     UUID        NOT NULL REFERENCES app.users(id),
    employee_id UUID        NOT NULL REFERENCES app.employees(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID
);

CREATE INDEX IF NOT EXISTS idx_user_employee_links_merchant_id ON app.user_employee_links (merchant_id);

CREATE TABLE IF NOT EXISTS app.employee_location_assignments (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    employee_id UUID        NOT NULL REFERENCES app.employees(id),
    location_id UUID        NOT NULL REFERENCES app.locations(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID
);

CREATE INDEX IF NOT EXISTS idx_emp_loc_assignments_merchant_id ON app.employee_location_assignments (merchant_id);
CREATE INDEX IF NOT EXISTS idx_emp_loc_assignments_employee_id ON app.employee_location_assignments (employee_id);
