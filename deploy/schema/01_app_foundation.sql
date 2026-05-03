-- 01_app_foundation.sql — app schema cross-cutting foundation
-- Source: archived migrations 002-009 + 016 (audit extensions) + canonical §10 (app.tenants)
-- Schema: app
-- Everything in m, l, s, c, e, i, o, p, f, t, q, ledger schemas FKs to here.

-- ─────────────────────────────────────────────────────────────────────
-- app.organizations — top of tenant tree (preserved from current spec)
-- ─────────────────────────────────────────────────────────────────────
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

-- ─────────────────────────────────────────────────────────────────────
-- app.tenants — promoted per canonical §10
-- The single multi-tenant boundary that every canonical retail-spine
-- entity FKs to (m.*, l.*, s.*, c.*, e.*, i.*, o.*, p.*, f.*, t.*, q.*, ledger.*).
-- 1:1 with app.merchants for now (seeded in 99_seed.sql).
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.tenants (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES app.organizations(id),
    tenant_code     TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','onboarding','suspended','terminated','archived')),
    schema_name     TEXT        NOT NULL,
    region          TEXT        NOT NULL DEFAULT 'us-west',
    attributes      JSONB       NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, tenant_code),
    UNIQUE (schema_name)
);
CREATE INDEX IF NOT EXISTS idx_tenants_organization_id ON app.tenants (organization_id);

-- ─────────────────────────────────────────────────────────────────────
-- app.merchants — preserved (one-merchant-per-tenant for MVP; seed maps 1:1)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.merchants (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID        NOT NULL REFERENCES app.organizations(id),
    tenant_id           UUID        REFERENCES app.tenants(id),  -- nullable during transition; backfill in seed
    source_merchant_id  TEXT        NOT NULL UNIQUE,
    merchant_name       TEXT        NOT NULL,
    currency            CHAR(3)     NOT NULL DEFAULT 'USD',
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_merchants_organization_id ON app.merchants (organization_id);
CREATE INDEX IF NOT EXISTS idx_merchants_tenant_id ON app.merchants (tenant_id);
CREATE INDEX IF NOT EXISTS idx_merchants_source_merchant_id ON app.merchants (source_merchant_id);

CREATE TABLE IF NOT EXISTS app.merchant_settings (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id              UUID        NOT NULL UNIQUE REFERENCES app.merchants(id),
    timezone                 TEXT        NOT NULL DEFAULT 'UTC',
    language                 TEXT        NOT NULL DEFAULT 'en',
    date_format              TEXT,
    calendar_type            TEXT        NOT NULL DEFAULT 'calendar_month'
                                         CHECK (calendar_type IN ('nrf_454','calendar_month')),
    fiscal_year_start_month  SMALLINT,
    fiscal_week_start_day    SMALLINT,
    fiscal_pattern           TEXT,
    notif_email_enabled      BOOLEAN     NOT NULL DEFAULT true,
    notif_sms_enabled        BOOLEAN     NOT NULL DEFAULT false,
    notif_in_app_enabled     BOOLEAN     NOT NULL DEFAULT true,
    notif_quiet_hours_start  SMALLINT,
    notif_quiet_hours_end    SMALLINT,
    notif_severity_threshold TEXT,
    notif_daily_limit        INTEGER,
    notif_phone              TEXT,
    theme                    TEXT,
    show_employee_names      BOOLEAN     NOT NULL DEFAULT false,
    -- de_merge_audit_visibility — controls who can read the audit
    -- trail of de-duplicated party / customer merges. 'lp_only' (the
    -- default) restricts visibility to LP-tier roles + auditor; the
    -- party-module de-merge UX returns 403 to other internal roles.
    -- 'all_internal' opens the trail to every authenticated tenant
    -- user. Per OQ Resolution Pack §A.1 OQ-1.4 (founder-approved
    -- 2026-05-03 per GRO-762). Handler-level enforcement lands when
    -- the de-merge UX ships in a later loop.
    de_merge_audit_visibility TEXT       NOT NULL DEFAULT 'lp_only'
                                         CHECK (de_merge_audit_visibility IN ('lp_only', 'all_internal')),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────
-- app.roles + app.users + app.user_roles
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_name   TEXT        NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.users (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID        NOT NULL REFERENCES app.merchants(id),
    username          TEXT        NOT NULL,
    email             TEXT        NOT NULL,
    display_name      TEXT,
    is_active         BOOLEAN     NOT NULL DEFAULT true,
    last_login_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        UUID,
    modified_by       UUID,
    db_status         TEXT        NOT NULL DEFAULT 'active'
                                  CHECK (db_status IN ('draft','active','archived')),
    db_effective_from TIMESTAMPTZ,
    db_effective_to   TIMESTAMPTZ,
    CONSTRAINT uq_users_merchant_email UNIQUE (merchant_id, email)
);
CREATE INDEX IF NOT EXISTS idx_users_merchant_id ON app.users (merchant_id);

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

-- ─────────────────────────────────────────────────────────────────────
-- app.locations + app.employees + links + assignments
-- (Current Canary tables. Canonical §4 adds parallel l.locations for
-- richer ARTS-anchored modeling; both coexist for now.)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.locations (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id        UUID        NOT NULL REFERENCES app.merchants(id),
    square_location_id TEXT        NOT NULL,
    location_name      TEXT        NOT NULL,
    address_line1      TEXT,
    address_line2      TEXT,
    city               TEXT,
    state              TEXT,
    postal_code        TEXT,
    coordinates        JSONB,
    is_active          BOOLEAN     NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by         UUID,
    modified_by        UUID,
    db_status          TEXT        NOT NULL DEFAULT 'active'
                                   CHECK (db_status IN ('draft','active','archived')),
    db_effective_from  TIMESTAMPTZ,
    db_effective_to    TIMESTAMPTZ,
    CONSTRAINT uq_locations_merchant_square_id UNIQUE (merchant_id, square_location_id)
);
CREATE INDEX IF NOT EXISTS idx_locations_merchant_id ON app.locations (merchant_id);

CREATE TABLE IF NOT EXISTS app.employees (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id        UUID         NOT NULL REFERENCES app.merchants(id),
    square_employee_id TEXT         NOT NULL,
    employee_name      TEXT         NOT NULL,
    email              TEXT,
    risk_score         NUMERIC(4,3) NOT NULL DEFAULT 0.0
                                    CHECK (risk_score BETWEEN 0.0 AND 1.0),
    is_active          BOOLEAN      NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_by         UUID,
    modified_by        UUID,
    db_status          TEXT         NOT NULL DEFAULT 'active'
                                    CHECK (db_status IN ('draft','active','archived')),
    db_effective_from  TIMESTAMPTZ,
    db_effective_to    TIMESTAMPTZ,
    CONSTRAINT uq_employees_merchant_square_id UNIQUE (merchant_id, square_employee_id)
);
CREATE INDEX IF NOT EXISTS idx_employees_merchant_id ON app.employees (merchant_id);

CREATE TABLE IF NOT EXISTS app.location_hierarchy (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID        NOT NULL REFERENCES app.merchants(id),
    name              TEXT        NOT NULL,
    level             SMALLINT    NOT NULL,
    parent_id         UUID        REFERENCES app.location_hierarchy(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        UUID,
    modified_by       UUID,
    db_status         TEXT        NOT NULL DEFAULT 'active'
                                  CHECK (db_status IN ('draft','active','archived')),
    db_effective_from TIMESTAMPTZ,
    db_effective_to   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_location_hierarchy_merchant_id ON app.location_hierarchy (merchant_id);

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

-- ─────────────────────────────────────────────────────────────────────
-- app.source_systems + merchant_sources + external_identities
-- (Federation layer — maps canonical entity IDs to source-system IDs)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.source_systems (
    code         TEXT        PRIMARY KEY,
    display_name TEXT        NOT NULL,
    category     TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.merchant_sources (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    source_code     TEXT        NOT NULL REFERENCES app.source_systems(code),
    raas_namespace  TEXT,
    status          TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','disconnected')),
    metadata_json   JSONB,
    disconnected_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID,
    modified_by     UUID
);
CREATE INDEX IF NOT EXISTS idx_merchant_sources_merchant_id ON app.merchant_sources (merchant_id);

CREATE TABLE IF NOT EXISTS app.external_identities (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    entity_type TEXT        NOT NULL
                            CHECK (entity_type IN ('employee','location','device','product','customer','tenant','order')),
    entity_id   UUID        NOT NULL,
    source_code TEXT        NOT NULL REFERENCES app.source_systems(code),
    external_id TEXT        NOT NULL,
    is_primary  BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID,
    CONSTRAINT uq_ext_id_merchant_source_entity
        UNIQUE (merchant_id, source_code, entity_type, external_id),
    CONSTRAINT uq_ext_id_merchant_entity_source
        UNIQUE (merchant_id, entity_type, entity_id, source_code)
);
CREATE INDEX IF NOT EXISTS idx_ext_id_reverse
    ON app.external_identities (merchant_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_ext_id_merchant ON app.external_identities (merchant_id);

-- ─────────────────────────────────────────────────────────────────────
-- app.audit_log — extended per archived 016 for protocol gateway middleware
-- (Canonical §10 extension. Used by gateway audit middleware + any
-- state-mutating MCP server.)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.audit_log (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        REFERENCES app.merchants(id),
    user_id         UUID        REFERENCES app.users(id),
    action          TEXT        NOT NULL,
    resource        TEXT,
    resource_id     UUID,
    ip_address      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Protocol extensions (gateway audit middleware, GRO-694 / patent Node 2)
    event_id        UUID,
    payload_digest  TEXT,
    source_code     TEXT,
    request_id      TEXT,
    user_agent      TEXT,
    status_code     INT,
    latency_ms      INT,
    actor_type      TEXT,
    mcp_server      TEXT,
    tool_name       TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_log_merchant_id   ON app.audit_log (merchant_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_user_id        ON app.audit_log (user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at     ON app.audit_log (created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_event_id       ON app.audit_log (event_id)       WHERE event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_log_payload_digest ON app.audit_log (payload_digest) WHERE payload_digest IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_log_source_code    ON app.audit_log (source_code)    WHERE source_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_log_request_id     ON app.audit_log (request_id)     WHERE request_id IS NOT NULL;

-- ─────────────────────────────────────────────────────────────────────
-- app.interest_signups — minor utility (preserved from archived 009)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.interest_signups (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email      TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────
-- Hawk OAuth + Bull API credentials (per-source connector state)
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.hawk_oauth_tokens (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID        NOT NULL REFERENCES app.merchants(id),
    access_token_encrypted  TEXT        NOT NULL,
    refresh_token_encrypted TEXT,
    token_type              TEXT        NOT NULL DEFAULT 'bearer',
    expires_at              TIMESTAMPTZ NOT NULL,
    scopes                  TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by              UUID,
    modified_by             UUID
);

CREATE TABLE IF NOT EXISTS app.bull_api_credentials (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID        NOT NULL REFERENCES app.merchants(id),
    api_key_encrypted TEXT        NOT NULL,
    endpoint_url      TEXT        NOT NULL,
    is_active         BOOLEAN     NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        UUID,
    modified_by       UUID
);

CREATE TABLE IF NOT EXISTS app.bull_poll_watermarks (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID        NOT NULL REFERENCES app.merchants(id),
    endpoint_name TEXT        NOT NULL,
    last_modified TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_run_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_bull_poll_watermarks_merchant_endpoint
        UNIQUE (merchant_id, endpoint_name)
);

CREATE TABLE IF NOT EXISTS app.bull_merchant_config (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL UNIQUE REFERENCES app.merchants(id),
    poll_interval_s INTEGER     NOT NULL DEFAULT 300,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app.bull_event_log (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID        NOT NULL REFERENCES app.merchants(id),
    event_type   TEXT        NOT NULL,
    payload      JSONB,
    processed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────
-- Workflow substrate — per OQ Resolution Pack §A.1 OQ-3.2
-- (founder-approved 2026-05-03 per GRO-762).
--
-- Two-table substrate for cross-cutting orchestration: long-running
-- multi-step processes that span modules (three-way-match, l402
-- charge cycle, evidence-anchor batches, etc.). Coordination uses
-- PostgreSQL pg_advisory_lock so multiple service instances don't
-- step on each other's executions:
--
--   https://www.postgresql.org/docs/17/explicit-locking.html
--
-- workflow_definitions is registered by code at service boot; rows
-- are immutable per (workflow_code, version). workflow_executions is
-- append-only audit — each kick-off creates one row that's mutated
-- by Advance / Complete to track current_step, status, finished_at.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS app.workflow_definitions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_code   TEXT        NOT NULL,                          -- 'three_way_match' | 'l402_charge_cycle' | etc.
    display_name    TEXT        NOT NULL,
    version         INT         NOT NULL DEFAULT 1,
    status          TEXT        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'deprecated')),
    attributes      JSONB       NOT NULL DEFAULT '{}',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workflow_code, version)
);

CREATE TABLE IF NOT EXISTS app.workflow_executions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL REFERENCES app.tenants(id),
    workflow_id     UUID        NOT NULL REFERENCES app.workflow_definitions(id),
    external_ref    TEXT,                                           -- caller-supplied correlation id
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    current_step    TEXT,                                           -- caller-defined step identifier
    context         JSONB       NOT NULL DEFAULT '{}',              -- accumulated state across steps
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    error_message   TEXT,
    attributes      JSONB       NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_workflow_exec_tenant_status
    ON app.workflow_executions(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_workflow_exec_workflow
    ON app.workflow_executions(workflow_id);
CREATE INDEX IF NOT EXISTS idx_workflow_exec_external_ref
    ON app.workflow_executions(tenant_id, external_ref)
    WHERE external_ref IS NOT NULL;
