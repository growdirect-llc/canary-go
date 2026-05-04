-- 10_ledger.sql — Cost-to-serve + accountability rails
-- Source: docs/sdds/go-handoff/canonical-data-model.md §10 (lines 3957-4118)
-- Schema: ledger
--
-- Three-rail accountability per platform thesis:
--   - financial: l402_otb_budgets (L402-gated open-to-buy)
--   - operational: ildwac_positions (satoshi cost-to-serve per cadence step)
--   - evidentiary: blockchain_anchors (L2 hash anchoring; cross-FK from detection.case_evidence)

-- ledger.stock_ledger_entries — financial valuation per inventory movement
CREATE TABLE ledger.stock_ledger_entries (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID        NOT NULL REFERENCES app.tenants(id),
    inventory_movement_id UUID        NOT NULL,    -- FK to inventory.inventory_movements (canonical §6)
    posted_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    item_id               UUID        NOT NULL,    -- FK to catalog.items (canonical §3)
    location_id           UUID        NOT NULL,    -- FK to location.locations (canonical §4)
    quantity_delta        NUMERIC(14,4) NOT NULL,
    cost_per_unit         NUMERIC(14,4) NOT NULL,
    cost_amount           NUMERIC(14,4) GENERATED ALWAYS AS (quantity_delta * cost_per_unit) STORED,
    cost_method           TEXT        NOT NULL DEFAULT 'weighted_average',  -- weighted_average | fifo | lifo | specific
    gl_account_id         UUID,                    -- FK to finance.gl_accounts (canonical §8)
    attributes            JSONB       NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sl_tenant       ON ledger.stock_ledger_entries(tenant_id);
CREATE INDEX idx_sl_movement     ON ledger.stock_ledger_entries(inventory_movement_id);
CREATE INDEX idx_sl_item_location ON ledger.stock_ledger_entries(item_id, location_id, posted_at);
CREATE INDEX idx_sl_gl           ON ledger.stock_ledger_entries(gl_account_id);

-- ledger.ildwac_positions — satoshi cost-to-serve per cadence step (GRO-732)
CREATE TABLE ledger.ildwac_positions (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              UUID        NOT NULL REFERENCES app.tenants(id),
    position_period        TSTZRANGE   NOT NULL,
    cadence_step           TEXT        NOT NULL,   -- minute | hour | day | week | month
    l_storage_satoshis     BIGINT      NOT NULL DEFAULT 0,
    w_workload_satoshis    BIGINT      NOT NULL DEFAULT 0,
    c_capture_satoshis     BIGINT      NOT NULL DEFAULT 0,
    total_satoshis         BIGINT      GENERATED ALWAYS AS (l_storage_satoshis + w_workload_satoshis + c_capture_satoshis) STORED,
    bytes_under_management BIGINT,
    workload_units         BIGINT,
    capture_tier           TEXT,                    -- low | medium | high | full
    invoiced_at            TIMESTAMPTZ,
    payment_proof          TEXT,                    -- L402 receipt / on-chain ref
    attributes             JSONB       NOT NULL DEFAULT '{}',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ildwac_tenant   ON ledger.ildwac_positions(tenant_id);
CREATE INDEX idx_ildwac_period   ON ledger.ildwac_positions USING gist(position_period);
CREATE INDEX idx_ildwac_cadence  ON ledger.ildwac_positions(cadence_step);
CREATE INDEX idx_ildwac_unbilled ON ledger.ildwac_positions(tenant_id) WHERE invoiced_at IS NULL;

-- ledger.rib_batches — Receipt-In-Batch cost averaging
CREATE TABLE ledger.rib_batches (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL REFERENCES app.tenants(id),
    item_id           UUID        NOT NULL,    -- FK catalog.items
    location_id       UUID,                    -- FK location.locations
    batch_period      TSTZRANGE   NOT NULL,
    total_quantity    NUMERIC(14,4) NOT NULL DEFAULT 0,
    total_cost        NUMERIC(14,4) NOT NULL DEFAULT 0,
    weighted_avg_cost NUMERIC(14,4) GENERATED ALWAYS AS (CASE WHEN total_quantity > 0 THEN total_cost / total_quantity ELSE 0 END) STORED,
    receipt_count     INT         NOT NULL DEFAULT 0,
    closed_at         TIMESTAMPTZ,
    attributes        JSONB       NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rib_tenant         ON ledger.rib_batches(tenant_id);
CREATE INDEX idx_rib_item_location  ON ledger.rib_batches(item_id, location_id);
CREATE INDEX idx_rib_period         ON ledger.rib_batches USING gist(batch_period);
CREATE INDEX idx_rib_open           ON ledger.rib_batches(tenant_id) WHERE closed_at IS NULL;

-- ledger.l402_otb_budgets — L402-gated open-to-buy (financial accountability rail)
CREATE TABLE ledger.l402_otb_budgets (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID        NOT NULL REFERENCES app.tenants(id),
    budget_period       TSTZRANGE   NOT NULL,
    scope_type          TEXT        NOT NULL,   -- tenant_total | category | location | service
    scope_id            UUID,
    budget_satoshis     BIGINT      NOT NULL,
    consumed_satoshis   BIGINT      NOT NULL DEFAULT 0,
    remaining_satoshis  BIGINT      GENERATED ALWAYS AS (budget_satoshis - consumed_satoshis) STORED,
    hard_limit          BOOLEAN     NOT NULL DEFAULT false,
    alert_threshold_pct NUMERIC(5,4) DEFAULT 0.80,
    status              TEXT        NOT NULL DEFAULT 'active',
    attributes          JSONB       NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_otb_tenant ON ledger.l402_otb_budgets(tenant_id);
CREATE INDEX idx_otb_period ON ledger.l402_otb_budgets USING gist(budget_period);
CREATE INDEX idx_otb_active ON ledger.l402_otb_budgets(tenant_id, scope_type) WHERE status = 'active';

-- ledger.blockchain_anchors — L2 hash anchoring (evidentiary accountability rail)
CREATE TABLE ledger.blockchain_anchors (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID        REFERENCES app.tenants(id),  -- nullable for cross-tenant batch anchors
    anchor_type          TEXT        NOT NULL,    -- evidence_batch | ildwac_position | gl_period | merkle_root
    payload_hash         TEXT        NOT NULL,
    merkle_root          TEXT,
    anchored_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    l2_chain             TEXT        NOT NULL DEFAULT 'lightning',  -- lightning | rgb | liquid | rsk
    l2_transaction_id    TEXT,
    l2_block_height      BIGINT,
    l2_proof             JSONB,
    related_entity_count INT,
    status               TEXT        NOT NULL DEFAULT 'pending',  -- pending | confirmed | failed
    attributes           JSONB       NOT NULL DEFAULT '{}',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_anchor_tenant       ON ledger.blockchain_anchors(tenant_id);
CREATE INDEX idx_anchor_type         ON ledger.blockchain_anchors(anchor_type);
CREATE INDEX idx_anchor_payload_hash ON ledger.blockchain_anchors(payload_hash);
CREATE INDEX idx_anchor_pending      ON ledger.blockchain_anchors(tenant_id) WHERE status = 'pending';

-- Backfill the detection.case_evidence FK to ledger.blockchain_anchors now that it exists.
ALTER TABLE detection.case_evidence
  ADD CONSTRAINT fk_case_evidence_anchor
  FOREIGN KEY (blockchain_anchor_id) REFERENCES ledger.blockchain_anchors(id);
