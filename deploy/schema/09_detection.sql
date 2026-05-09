-- 09_q_canary_mechanics.sql — Canary platform Q (Loss Prevention) schema
-- Source: docs/sdds/go-handoff/canonical-data-model.md §10 (lines 3736-3955)
-- Schema: q
--
-- Folds current Canary `app.fox_*` (7 tables) + `app.hawk_*` (8 tables) into
-- 6 canonical entities via case_type discriminator. Append-only evidence
-- chain anchors to ledger.blockchain_anchors (third accountability rail).

-- detection.detection_rules — JSONB rule definitions, multi-frequency evaluation
CREATE TABLE detection.detection_rules (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID        NOT NULL REFERENCES app.tenants(id),
    rule_code             TEXT        NOT NULL,
    name                  TEXT        NOT NULL,
    description           TEXT,
    rule_category         TEXT        NOT NULL,           -- shrink | fraud | discount_abuse | tender_pattern | scan_avoidance | refund_pattern | drawer_variance
    rule_definition       JSONB       NOT NULL,           -- the actual rule logic
    severity              TEXT        NOT NULL DEFAULT 'medium',     -- low | medium | high | critical
    status                TEXT        NOT NULL DEFAULT 'active',     -- active | paused | retired
    evaluation_frequency  TEXT        NOT NULL DEFAULT 'on_event',   -- on_event | hourly | daily | weekly
    attributes            JSONB       NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, rule_code)
);
CREATE INDEX idx_qrules_tenant   ON detection.detection_rules(tenant_id);
CREATE INDEX idx_qrules_category ON detection.detection_rules(rule_category);
CREATE INDEX idx_qrules_active   ON detection.detection_rules(tenant_id, evaluation_frequency) WHERE status = 'active';

-- detection.subjects — Party-like, may be unknown / suspected (cross-FK to employee/customer/vendor when known)
CREATE TABLE detection.subjects (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES app.tenants(id),
    subject_code        TEXT        NOT NULL,
    subject_type        TEXT        NOT NULL,    -- known_employee | known_customer | known_vendor | suspected_individual | unknown_person | external_party
    display_name        TEXT        NOT NULL,
    related_employee_id UUID,                    -- FK to employee.employees(id) when present (canonical §5)
    related_customer_id UUID,                    -- FK to customer.customers(id) when present (canonical §5)
    related_vendor_id   UUID,                    -- FK to catalog.vendors(id) when present (canonical §3)
    description         TEXT,
    identifiers         JSONB       DEFAULT '{}',  -- PII tier 2-3, encrypted at rest
    attributes          JSONB       NOT NULL DEFAULT '{}',
    status              TEXT        NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, subject_code)
);
CREATE INDEX idx_qsub_tenant   ON detection.subjects(tenant_id);
CREATE INDEX idx_qsub_employee ON detection.subjects(related_employee_id) WHERE related_employee_id IS NOT NULL;
CREATE INDEX idx_qsub_customer ON detection.subjects(related_customer_id) WHERE related_customer_id IS NOT NULL;
CREATE INDEX idx_qsub_type     ON detection.subjects(subject_type);

-- detection.cases — unified investigation/incident/dispute/compliance (folds fox + hawk)
CREATE TABLE detection.cases (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID        NOT NULL REFERENCES app.tenants(id),
    case_number           TEXT        NOT NULL,
    case_type             TEXT        NOT NULL DEFAULT 'investigation',  -- investigation | incident | dispute | compliance_review
    title                 TEXT        NOT NULL,
    description           TEXT,
    severity              TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'open',           -- open | active | pending_action | resolved | closed | reopened
    primary_subject_id    UUID        REFERENCES detection.subjects(id),
    primary_location_id   UUID,                                          -- FK to location.locations(id) (canonical §4)
    assigned_to           UUID        REFERENCES app.users(id),
    opened_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at           TIMESTAMPTZ,
    resolution_type       TEXT,                                          -- substantiated | unsubstantiated | recovered | restitution | termination | no_action
    loss_amount_estimated NUMERIC(14,4),
    loss_amount_recovered NUMERIC(14,4),
    attributes            JSONB       NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, case_number)
);
CREATE INDEX idx_qcases_tenant    ON detection.cases(tenant_id);
CREATE INDEX idx_qcases_subject   ON detection.cases(primary_subject_id);
CREATE INDEX idx_qcases_location  ON detection.cases(primary_location_id);
CREATE INDEX idx_qcases_assigned  ON detection.cases(assigned_to);
CREATE INDEX idx_qcases_active    ON detection.cases(tenant_id, status) WHERE status NOT IN ('resolved', 'closed');

-- detection.detections — append-only signal log (referenced from cases via case_id)
CREATE TABLE detection.detections (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID         NOT NULL REFERENCES app.tenants(id),
    rule_id             UUID         NOT NULL REFERENCES detection.detection_rules(id),
    detected_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    source_entity_type  TEXT         NOT NULL,
    source_entity_id    UUID         NOT NULL,
    location_id         UUID,                                            -- FK to location.locations(id)
    cashier_employee_id UUID,                                            -- FK to employee.employees(id)
    customer_id         UUID,                                            -- FK to customer.customers(id)
    severity            TEXT         NOT NULL,
    signal_strength     NUMERIC(5,4),                                    -- 0.0-1.0 confidence
    evidence            JSONB        NOT NULL DEFAULT '{}',
    case_id             UUID         REFERENCES detection.cases(id),
    status              TEXT         NOT NULL DEFAULT 'new',             -- new | acknowledged | escalated_to_case | dismissed | duplicate
    acknowledged_at     TIMESTAMPTZ,
    acknowledged_by     UUID         REFERENCES app.users(id),
    attributes          JSONB        NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_qdet_tenant     ON detection.detections(tenant_id);
CREATE INDEX idx_qdet_rule       ON detection.detections(rule_id, detected_at);
CREATE INDEX idx_qdet_source     ON detection.detections(source_entity_type, source_entity_id);
CREATE INDEX idx_qdet_location   ON detection.detections(location_id, detected_at);
CREATE INDEX idx_qdet_cashier    ON detection.detections(cashier_employee_id, detected_at) WHERE cashier_employee_id IS NOT NULL;
CREATE INDEX idx_qdet_case       ON detection.detections(case_id) WHERE case_id IS NOT NULL;
CREATE INDEX idx_qdet_unresolved ON detection.detections(tenant_id, status) WHERE status NOT IN ('dismissed','duplicate');

-- detection.case_evidence — append-only with hash chain + L2 blockchain anchor FK
CREATE TABLE detection.case_evidence (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID        NOT NULL REFERENCES app.tenants(id),
    case_id              UUID        NOT NULL REFERENCES detection.cases(id) ON DELETE RESTRICT,
    evidence_type        TEXT        NOT NULL,    -- transaction_snapshot | video_clip | photo | document | witness_statement | system_log | scan_replay
    source_entity_type   TEXT,
    source_entity_id     UUID,
    payload              JSONB       NOT NULL DEFAULT '{}',
    payload_hash         TEXT        NOT NULL,    -- SHA-256 canonical-JSON
    prev_evidence_hash   TEXT,
    blockchain_anchor_id UUID,                    -- FK to ledger.blockchain_anchors(id) — added in 10_ledger.sql
    collected_by         UUID        REFERENCES app.users(id),
    collected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    attributes           JSONB       NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_qev_tenant     ON detection.case_evidence(tenant_id);
CREATE INDEX idx_qev_case       ON detection.case_evidence(case_id, collected_at);
CREATE INDEX idx_qev_hash       ON detection.case_evidence(payload_hash);
CREATE INDEX idx_qev_unanchored ON detection.case_evidence(tenant_id) WHERE blockchain_anchor_id IS NULL;

-- detection.case_actions — append-only state log
CREATE TABLE detection.case_actions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL REFERENCES app.tenants(id),
    case_id      UUID        NOT NULL REFERENCES detection.cases(id) ON DELETE CASCADE,
    action_type  TEXT        NOT NULL,           -- note | status_change | assignment_change | evidence_collected | external_notification | resolution
    performed_by UUID        REFERENCES app.users(id),
    performed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    details      JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_qact_tenant ON detection.case_actions(tenant_id);
CREATE INDEX idx_qact_case   ON detection.case_actions(case_id, performed_at);
CREATE INDEX idx_qact_type   ON detection.case_actions(action_type);

-- detection.lp_substrate — event-ingestion staging for the LP pipeline.
-- Columns mirror migration 024_detection_schema.up.sql; lifted here so
-- a fresh `make db-reset-test` produces the same shape as a deployed
-- DB after `make migrate-up` (CLAUDE.md two-tier rule: schema and
-- migrations must agree at HEAD).
CREATE TABLE detection.lp_substrate (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL,
    entity_type  TEXT        NOT NULL,
    entity_id    UUID        NOT NULL,
    location_id  UUID,
    payload      JSONB       NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_lp_substrate_tenant_id   ON detection.lp_substrate(tenant_id);
CREATE INDEX idx_lp_substrate_entity      ON detection.lp_substrate(entity_type, entity_id);
CREATE INDEX idx_lp_substrate_location_id ON detection.lp_substrate(location_id);
CREATE INDEX idx_lp_substrate_received_at ON detection.lp_substrate(received_at DESC);

-- detection.allow_list — tenant-scoped suppression entries backing the
-- 10 settings-page surfaces in W1. pattern jsonb carries a type+kind
-- discriminator; see internal/lp/substrate.go for the application
-- semantics.
CREATE TABLE detection.allow_list (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID        NOT NULL,
    rule_id    UUID        REFERENCES detection.detection_rules(id),
    pattern    JSONB       NOT NULL,
    reason     TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID
);
CREATE INDEX idx_allow_list_tenant_id  ON detection.allow_list(tenant_id);
CREATE INDEX idx_allow_list_rule_id    ON detection.allow_list(rule_id);
CREATE INDEX idx_allow_list_expires_at ON detection.allow_list(expires_at);
