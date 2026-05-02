-- 012_fox_cases.up.sql
CREATE TABLE IF NOT EXISTS app.fox_cases (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    alert_id        UUID        REFERENCES app.alerts(id),
    status          TEXT        NOT NULL DEFAULT 'OPEN'
                                CHECK (status IN ('OPEN','ACTIVE','PENDING_REVIEW','CLOSED','ARCHIVED')),
    case_type       TEXT        NOT NULL,
    title           TEXT        NOT NULL,
    description     TEXT,
    assigned_to     UUID        REFERENCES app.users(id),
    opened_by       UUID        REFERENCES app.users(id),
    closed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fox_cases_merchant_id ON app.fox_cases (merchant_id);
CREATE INDEX IF NOT EXISTS idx_fox_cases_merchant_status ON app.fox_cases (merchant_id, status);

CREATE TABLE IF NOT EXISTS app.fox_subjects (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    case_id     UUID        NOT NULL REFERENCES app.fox_cases(id),
    name        TEXT        NOT NULL,
    entity_id   UUID,
    entity_type TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fox_subjects_case_id ON app.fox_subjects (case_id);

CREATE TABLE IF NOT EXISTS app.fox_timeline (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    case_id     UUID        NOT NULL REFERENCES app.fox_cases(id),
    actor_id    UUID        REFERENCES app.users(id),
    event_type  TEXT        NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fox_timeline_case_id ON app.fox_timeline (case_id);
