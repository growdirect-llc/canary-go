-- 011_tsp_ingestion.up.sql
CREATE TABLE IF NOT EXISTS app.ingestion_log (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    event_id        TEXT        NOT NULL UNIQUE,
    source_code     TEXT        REFERENCES app.source_systems(code),
    chain_hash      TEXT        NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    stage           TEXT        NOT NULL DEFAULT 'seal'
                                CHECK (stage IN ('seal','parse','merkle','detect'))
);

CREATE INDEX IF NOT EXISTS idx_ingestion_log_merchant_id ON app.ingestion_log (merchant_id);
CREATE INDEX IF NOT EXISTS idx_ingestion_log_event_id ON app.ingestion_log (event_id);

CREATE TABLE IF NOT EXISTS app.merkle_batches (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    batch_id    TEXT        NOT NULL UNIQUE,
    root_hash   TEXT        NOT NULL,
    event_count INTEGER     NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_merkle_batches_merchant_id ON app.merkle_batches (merchant_id);

CREATE TABLE IF NOT EXISTS app.detection_rules (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id           TEXT        NOT NULL UNIQUE,
    category          TEXT        NOT NULL,
    severity          TEXT        NOT NULL
                                  CHECK (severity IN ('critical','high','medium','low','info')),
    default_threshold NUMERIC,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_detection_rules_rule_id ON app.detection_rules (rule_id);

CREATE TABLE IF NOT EXISTS app.merchant_rule_config (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID        NOT NULL REFERENCES app.merchants(id),
    rule_id          UUID        NOT NULL REFERENCES app.detection_rules(id),
    is_enabled       BOOLEAN     NOT NULL DEFAULT true,
    custom_threshold NUMERIC,
    notify_enabled   BOOLEAN     NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by       UUID,
    modified_by      UUID,
    CONSTRAINT uq_merchant_rule_config_merchant_rule UNIQUE (merchant_id, rule_id)
);

CREATE INDEX IF NOT EXISTS idx_merchant_rule_config_merchant_id ON app.merchant_rule_config (merchant_id);

CREATE TABLE IF NOT EXISTS app.alerts (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID        NOT NULL REFERENCES app.merchants(id),
    rule_id      UUID        NOT NULL REFERENCES app.detection_rules(id),
    severity     TEXT        NOT NULL CHECK (severity IN ('critical','high','medium','low','info')),
    status       TEXT        NOT NULL DEFAULT 'OPEN'
                             CHECK (status IN ('OPEN','ACKNOWLEDGED','INVESTIGATING','ESCALATED','DISMISSED')),
    source_table TEXT        NOT NULL,
    source_id    UUID        NOT NULL,
    employee_id  UUID        REFERENCES app.employees(id),
    location_id  UUID        REFERENCES app.locations(id),
    impact_cents BIGINT      NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alerts_merchant_id ON app.alerts (merchant_id);
CREATE INDEX IF NOT EXISTS idx_alerts_merchant_status ON app.alerts (merchant_id, status);

CREATE TABLE IF NOT EXISTS app.alert_history (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id    UUID        NOT NULL REFERENCES app.alerts(id),
    actor_id    UUID        REFERENCES app.users(id),
    from_status TEXT,
    to_status   TEXT        NOT NULL,
    note        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_history_alert_id ON app.alert_history (alert_id);
