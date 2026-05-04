-- 024_detection_schema.up.sql
-- Create detection schema with detection_rules, detections, lp_substrate, and allow_list tables.
-- Spec: GRO-766 Phase A.1, GRO-767 Phase A.1.

CREATE SCHEMA IF NOT EXISTS detection;

-- detection_rules: canonical loss prevention rules keyed by rule_code
CREATE TABLE IF NOT EXISTS detection.detection_rules (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    rule_code       TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    description     TEXT,
    rule_category   TEXT        NOT NULL,
    rule_definition JSONB       NOT NULL,
    severity        TEXT        NOT NULL CHECK (severity IN ('critical','high','medium','low','info')),
    status          TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','deprecated')),
    evaluation_frequency TEXT,
    attributes      JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_detection_rules_tenant_id ON detection.detection_rules (tenant_id);
CREATE INDEX IF NOT EXISTS idx_detection_rules_rule_code ON detection.detection_rules (rule_code);
CREATE INDEX IF NOT EXISTS idx_detection_rules_category ON detection.detection_rules (rule_category);

-- detections: individual alert surface, enriched with rule metadata
CREATE TABLE IF NOT EXISTS detection.detections (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL,
    rule_id             UUID        NOT NULL REFERENCES detection.detection_rules(id),
    detected_at         TIMESTAMPTZ NOT NULL,
    source_entity_type  TEXT        NOT NULL,
    source_entity_id    UUID        NOT NULL,
    location_id         UUID,
    cashier_employee_id UUID,
    customer_id         UUID,
    severity            TEXT        NOT NULL CHECK (severity IN ('critical','high','medium','low','info')),
    signal_strength     NUMERIC,
    evidence            JSONB,
    case_id             UUID,
    status              TEXT        NOT NULL DEFAULT 'new' CHECK (status IN ('new','acknowledged','investigating','dismissed','duplicate')),
    acknowledged_at     TIMESTAMPTZ,
    acknowledged_by     UUID,
    attributes          JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_detections_tenant_id ON detection.detections (tenant_id);
CREATE INDEX IF NOT EXISTS idx_detections_rule_id ON detection.detections (rule_id);
CREATE INDEX IF NOT EXISTS idx_detections_status ON detection.detections (status);
CREATE INDEX IF NOT EXISTS idx_detections_detected_at ON detection.detections (detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_detections_tenant_status ON detection.detections (tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_detections_source ON detection.detections (source_entity_type, source_entity_id);

-- lp_substrate: substrate data for evaluation (events, transactions, personnel)
CREATE TABLE IF NOT EXISTS detection.lp_substrate (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    entity_type     TEXT        NOT NULL,
    entity_id       UUID        NOT NULL,
    location_id     UUID,
    payload         JSONB       NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_lp_substrate_tenant_id ON detection.lp_substrate (tenant_id);
CREATE INDEX IF NOT EXISTS idx_lp_substrate_entity ON detection.lp_substrate (entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_lp_substrate_location_id ON detection.lp_substrate (location_id);
CREATE INDEX IF NOT EXISTS idx_lp_substrate_received_at ON detection.lp_substrate (received_at DESC);

-- allow_list: suppression rules for false-positive filtering
CREATE TABLE IF NOT EXISTS detection.allow_list (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID        NOT NULL,
    rule_id         UUID        REFERENCES detection.detection_rules(id),
    pattern         JSONB       NOT NULL,
    reason          TEXT,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      UUID
);

CREATE INDEX IF NOT EXISTS idx_allow_list_tenant_id ON detection.allow_list (tenant_id);
CREATE INDEX IF NOT EXISTS idx_allow_list_rule_id ON detection.allow_list (rule_id);
CREATE INDEX IF NOT EXISTS idx_allow_list_expires_at ON detection.allow_list (expires_at);
