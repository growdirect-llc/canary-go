-- 12_party.sql — Party Substrate Identity Domain (§13)
-- Source: docs/sdds/go-handoff/canonical-data-model-party-edits.md §A-§F
-- Authority: GRO-734 + GRO-763 Phase B.5
-- Backup tier: Tier 2 for party.parties / households / memberships /
--              decisioning_facts; Tier 1 for party.identifiers (highest-
--              recovery-priority since identifier_value_hash is the
--              substrate that resolution depends on). Per OQ Resolution
--              Pack §A.1 OQ-3.5.

-- §A.1 — schema + parties node
CREATE SCHEMA IF NOT EXISTS party;

CREATE TABLE party.parties (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    party_code      text NOT NULL,
    party_type      text NOT NULL DEFAULT 'consumer',
    -- party_type values: consumer | customer | household_aggregate
    -- (vendor | auditor | investigator | mcp_agent reserved for taxonomy expansion)
    display_name    text NOT NULL,
    status          text NOT NULL DEFAULT 'active',
    -- status values: active | merged | suppressed | dissolved
    merged_into     uuid REFERENCES party.parties(id),
    confidence      text NOT NULL DEFAULT 'anonymous',
    -- confidence values: anonymous | weak | probable | strong
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, party_code)
);
CREATE INDEX idx_parties_tenant_active ON party.parties(tenant_id) WHERE status = 'active';
CREATE INDEX idx_parties_merged_into ON party.parties(merged_into) WHERE merged_into IS NOT NULL;
CREATE INDEX idx_parties_confidence ON party.parties(tenant_id, confidence) WHERE status = 'active';
CREATE INDEX idx_parties_last_seen ON party.parties(tenant_id, last_seen_at);

-- §A.2 — identifiers (every signal that ties to a party)
CREATE TABLE party.identifiers (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
    party_id               uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    identifier_type        text NOT NULL,
    identifier_value_hash  text NOT NULL,
    source_system          text NOT NULL,
    quality_score          numeric(3,2) NOT NULL,
    first_seen_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at           timestamptz NOT NULL DEFAULT now(),
    occurrence_count       bigint NOT NULL DEFAULT 1,
    attributes             jsonb NOT NULL DEFAULT '{}',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, identifier_type, identifier_value_hash)
);
CREATE INDEX idx_identifiers_party ON party.identifiers(tenant_id, party_id);
CREATE INDEX idx_identifiers_type_quality ON party.identifiers(tenant_id, identifier_type, quality_score DESC);
CREATE INDEX idx_identifiers_last_seen ON party.identifiers(tenant_id, last_seen_at);

-- §A.3 — resolution_events (append-only resolution decision log)
CREATE TABLE party.resolution_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
    party_id            uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    event_type          text NOT NULL,
    source_event_type   text,
    source_event_id     uuid,
    rule_id             text,
    confidence_before   text,
    confidence_after    text,
    evidence            jsonb NOT NULL DEFAULT '{}',
    actor               text NOT NULL DEFAULT 'system',
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_resevents_party_created ON party.resolution_events(party_id, created_at);
CREATE INDEX idx_resevents_tenant_event ON party.resolution_events(tenant_id, event_type, created_at);
CREATE INDEX idx_resevents_source ON party.resolution_events(source_event_type, source_event_id) WHERE source_event_id IS NOT NULL;

-- §A.4 — households (per-tenant household node)
CREATE TABLE party.households (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_code  text NOT NULL,
    display_name    text,
    status          text NOT NULL DEFAULT 'active',
    formed_at       timestamptz NOT NULL DEFAULT now(),
    dissolved_at    timestamptz,
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, household_code)
);
CREATE INDEX idx_households_tenant_active ON party.households(tenant_id) WHERE status = 'active';

-- §A.5 — household_memberships (many-to-many with effective dates)
CREATE TABLE party.household_memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_id    uuid NOT NULL REFERENCES party.households(id) ON DELETE RESTRICT,
    party_id        uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    member_role     text NOT NULL DEFAULT 'member',
    effective_start date NOT NULL DEFAULT CURRENT_DATE,
    effective_end   date,
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, household_id, party_id, effective_start),
    CONSTRAINT one_head_per_household
        EXCLUDE (household_id WITH =)
        WHERE (member_role = 'head' AND effective_end IS NULL)
);
CREATE INDEX idx_hhmem_party_current ON party.household_memberships(party_id) WHERE effective_end IS NULL;
CREATE INDEX idx_hhmem_household_current ON party.household_memberships(household_id) WHERE effective_end IS NULL;

-- §A.6 — household_evidence (append-only evidence log)
CREATE TABLE party.household_evidence (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    membership_id   uuid NOT NULL REFERENCES party.household_memberships(id) ON DELETE RESTRICT,
    evidence_type   text NOT NULL,
    evidence_payload jsonb NOT NULL DEFAULT '{}',
    source_event_id  uuid,
    confidence       numeric(3,2) NOT NULL,
    collected_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_hhev_membership ON party.household_evidence(membership_id);
CREATE INDEX idx_hhev_tenant_collected ON party.household_evidence(tenant_id, collected_at);

-- §A.7 — decisioning_facts (materialized view; refresh cadence per
-- party-identity-design.md §E). The party_id column on transaction.transactions
-- is added by §C below — but the view definition references it. Since
-- this file loads §A first then §C as ALTER TABLE, the view must be
-- created AFTER §C. We move the view creation to the end of this file.

-- §B — customer.customers.party_id (HARD FK; one customer row resolves to
-- exactly one party, but a party may carry multiple customers — one
-- per POS-source identity).
ALTER TABLE customer.customers
    ADD COLUMN party_id uuid REFERENCES party.parties(id);
CREATE INDEX idx_customers_party ON customer.customers(party_id) WHERE party_id IS NOT NULL;

-- §C — transaction.transactions.party_id (SOFT FK; high-volume write path,
-- party_module guarantees row immutability so DB-level FK is not
-- needed. Application contract: party.GetByID + merged_into
-- forwarding). Soft-FK to party.parties(id).
ALTER TABLE transaction.transactions
    ADD COLUMN party_id uuid;
COMMENT ON COLUMN transaction.transactions.party_id IS 'soft-FK to party.parties(id) — see canonical-data-model-party-edits.md §C';
CREATE INDEX idx_tx_party ON transaction.transactions(party_id) WHERE party_id IS NOT NULL;

-- §D — detection.subjects.party_id (SOFT FK). Subsumes the soft-FK pattern
-- on related_employee_id / related_customer_id / related_vendor_id;
-- those columns stay during Phases 1-5 for read-path compatibility.
ALTER TABLE detection.subjects
    ADD COLUMN party_id uuid;
COMMENT ON COLUMN detection.subjects.party_id IS 'soft-FK to party.parties(id) — see canonical-data-model-party-edits.md §D';
CREATE INDEX idx_qsub_party ON detection.subjects(party_id) WHERE party_id IS NOT NULL;

-- §E — orders.sales_orders.party_id (SOFT FK). Guest orders may resolve
-- only at first-payment-attached event. customer_id stays in place
-- for operational reads (display, address).
ALTER TABLE orders.sales_orders
    ADD COLUMN party_id uuid;
COMMENT ON COLUMN orders.sales_orders.party_id IS 'soft-FK to party.parties(id) — see canonical-data-model-party-edits.md §E';
CREATE INDEX idx_so_party ON orders.sales_orders(party_id) WHERE party_id IS NOT NULL;

-- §F — detection.detections.party_id (SOFT FK). Recommended (not strictly
-- required) — direct column gives party.decisioning_facts.party_fraud_risk
-- recompute a single GROUP BY party_id rather than multi-step joins.
ALTER TABLE detection.detections
    ADD COLUMN party_id uuid;
COMMENT ON COLUMN detection.detections.party_id IS 'soft-FK to party.parties(id) — perf optimization for party_fraud_risk recompute — see canonical-data-model-party-edits.md §F';
CREATE INDEX idx_qdet_party ON detection.detections(party_id) WHERE party_id IS NOT NULL;

-- §A.7 (deferred — needs transaction.transactions.party_id from §C)
-- decisioning_facts: 12-month rolling RFM rollup per active party.
-- Refresh cadence per party-identity-design.md §E (nightly initially;
-- streaming refresh post-Phase-3).
CREATE MATERIALIZED VIEW party.decisioning_facts AS
SELECT
    pa.id                                              AS party_id,
    pa.tenant_id                                       AS tenant_id,
    pa.confidence                                      AS confidence,
    COALESCE(SUM(tx.grand_total) FILTER (
        WHERE tx.business_date >= CURRENT_DATE - INTERVAL '12 months'
    ), 0)::numeric(14,4)                              AS party_value,
    COALESCE(EXTRACT(DAY FROM now() - pa.last_seen_at)::int, 999) AS party_recency,
    COUNT(tx.id) FILTER (
        WHERE tx.business_date >= CURRENT_DATE - INTERVAL '12 months'
    )                                                 AS party_frequency,
    COALESCE(AVG(tx.grand_total) FILTER (
        WHERE tx.business_date >= CURRENT_DATE - INTERVAL '12 months'
    ), 0)::numeric(14,4)                              AS party_monetary,
    ARRAY[]::text[]                                   AS party_segment_tags,
    0.0::numeric(5,4)                                 AS party_fraud_risk,
    0.0::numeric(5,4)                                 AS party_churn_risk,
    now()                                             AS computed_at
FROM party.parties pa
LEFT JOIN transaction.transactions tx
    ON tx.party_id = pa.id AND tx.tenant_id = pa.tenant_id
WHERE pa.status = 'active'
GROUP BY pa.id, pa.tenant_id, pa.confidence, pa.last_seen_at;

CREATE UNIQUE INDEX idx_dfacts_party ON party.decisioning_facts(party_id);
CREATE INDEX idx_dfacts_tenant_value ON party.decisioning_facts(tenant_id, party_value DESC);
CREATE INDEX idx_dfacts_tenant_recency ON party.decisioning_facts(tenant_id, party_recency);
CREATE INDEX idx_dfacts_tenant_segments ON party.decisioning_facts USING gin(party_segment_tags);

-- Append-only enforcement on party.resolution_events and
-- party.household_evidence is via role-level REVOKE on production
-- deployment (canary_app role bootstrap). Skipped in dev/test schema —
-- the application layer treats these tables as append-only.
