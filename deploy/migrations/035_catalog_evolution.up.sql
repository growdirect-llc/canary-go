-- 035_catalog_evolution.up.sql
--
-- GRO-884 — catalog schema evolution.
--
-- Picks up GRO-880's P0 list (catalog data-model audit) plus
-- catalog.import_jobs (GRO-877 OQ #2 — Flow B prerequisite) plus
-- pricing.observed_price_rules (GRO-880 P0 #6 architectural call:
-- observe Counterpoint's pricing engine rather than replicate it).
--
-- Schema only. No Go code changes here — existing INSERT/SELECT
-- statements use explicit column lists, so adding columns doesn't
-- break the current adapter or DTOs. Per-feature consumption of the
-- new fields lands in subsequent dispatches.
--
-- Idempotent — uses IF NOT EXISTS / ADD COLUMN IF NOT EXISTS
-- consistently so a second `migrate-up` is a no-op.

-- ─── catalog.items — additive columns ─────────────────────────────────

ALTER TABLE catalog.items
    ADD COLUMN IF NOT EXISTS tracking_method TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS qty_decimals SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS price_decimals SMALLINT NOT NULL DEFAULT 2,
    ADD COLUMN IF NOT EXISTS mix_match_code TEXT,
    ADD COLUMN IF NOT EXISTS is_discountable BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS preferred_unit_of_measure TEXT,
    ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_received_at TIMESTAMPTZ;

COMMENT ON COLUMN catalog.items.tracking_method IS
    'none | serial | lot — read path uses this to decide whether to look at catalog.item_serials. GRO-880 P0 #3.';
COMMENT ON COLUMN catalog.items.qty_decimals IS
    'POS quantity decimals (0 for whole-unit; 2-4 for weighed). GRO-880 P0 #4.';
COMMENT ON COLUMN catalog.items.price_decimals IS
    'POS price decimals. GRO-880 P0 #4.';
COMMENT ON COLUMN catalog.items.mix_match_code IS
    'Mix-and-match group code — items sharing this code qualify together for "buy any 3 for $10" deals. GRO-880 P1 #8 — Counterpoint IM_ITEM.MIX_MATCH_COD.';
COMMENT ON COLUMN catalog.items.is_discountable IS
    'Item-level flag prohibiting discount application (receipt paper, warranty cards, etc.). GRO-880 P1 #9.';
COMMENT ON COLUMN catalog.items.preferred_unit_of_measure IS
    'Display/order default UOM when different from stocking unit (sell by ft, stock by case-of-100). GRO-880 P1 #10 — Counterpoint IM_ITEM.PREF_UNIT.';
COMMENT ON COLUMN catalog.items.status_changed_at IS
    'When status last changed — Counterpoint IM_ITEM.STAT_DAT. GRO-880 P1 #13.';
COMMENT ON COLUMN catalog.items.last_received_at IS
    'Last received-at receiving — useful for dead-stock detection. GRO-880 P1 #14.';

-- Status comment update — the column accepts the canary-side
-- lifecycle states (draft, on_trial, phase_out, inactive) added per
-- GRO-880 status-lifecycle alignment. No CHECK constraint to permit
-- the broader vocabulary; application layer enforces the enum.
COMMENT ON COLUMN catalog.items.status IS
    'active | discontinued | seasonal | hidden | draft | on_trial | phase_out | inactive — Canary lifecycle adds draft / on_trial / phase_out / inactive over the Counterpoint-source set; sync rounds Canary states to active/inactive at the Counterpoint boundary. GRO-880.';


-- ─── catalog.item_vendors — additive columns ─────────────────────────

ALTER TABLE catalog.item_vendors
    ADD COLUMN IF NOT EXISTS vendor_description TEXT,
    ADD COLUMN IF NOT EXISTS order_unit_of_measure TEXT;

COMMENT ON COLUMN catalog.item_vendors.vendor_description IS
    'Vendor''s description for this item (per their invoice / catalog). GRO-880 P1 #11 — Counterpoint VendorItem.VEND_DESCR.';
COMMENT ON COLUMN catalog.item_vendors.order_unit_of_measure IS
    'Order UOM — drives PO unit conversions when different from item.unit_of_measure. GRO-880 P1 #11 — Counterpoint VendorItem.ORD_UNIT.';


-- ─── catalog.item_serials — new table ────────────────────────────────

CREATE TABLE IF NOT EXISTS catalog.item_serials (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES app.tenants(id),
    item_id     UUID NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
    serial_no   TEXT NOT NULL,
    -- nullable: not all serials are location-bound at a given time
    -- (e.g., in-transit between stores). location_id references
    -- location.locations(id) when set; FK kept loose to avoid the
    -- forward-reference pattern across schema files.
    location_id UUID,
    status      TEXT NOT NULL DEFAULT 'in_stock',
    received_at TIMESTAMPTZ,
    sold_at     TIMESTAMPTZ,
    cost        NUMERIC(12,4),
    attributes  JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, item_id, serial_no)
);

CREATE INDEX IF NOT EXISTS idx_item_serials_tenant_status
    ON catalog.item_serials(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_item_serials_item
    ON catalog.item_serials(item_id);

COMMENT ON TABLE catalog.item_serials IS
    'Serial-tracked inventory units. One row per (tenant, item, serial_no). status ∈ {in_stock, sold, rma, warranty, reserved}. Backs Counterpoint SN_SER + canary serial-tracked workflows (high-value tools, electronics, regulated goods). GRO-880 P1 #7 / GRO-884.';


-- ─── catalog.import_jobs — new table ─────────────────────────────────

CREATE TABLE IF NOT EXISTS catalog.import_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES app.tenants(id),
    -- nullable so generic imports without a specific vendor work
    supplier_id     UUID,
    status          TEXT NOT NULL DEFAULT 'queued',
    file_uri        TEXT,
    file_name       TEXT,
    column_mapping  JSONB NOT NULL DEFAULT '{}',
    summary         JSONB NOT NULL DEFAULT '{}',
    row_count       INT NOT NULL DEFAULT 0,
    rows_imported   INT NOT NULL DEFAULT 0,
    rows_skipped    INT NOT NULL DEFAULT 0,
    started_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_import_jobs_tenant_status
    ON catalog.import_jobs(tenant_id, status);
-- Hot path for the "resume in-flight job" UX on next visit to
-- /suppliers/{id}/import: list the active jobs newest-first.
CREATE INDEX IF NOT EXISTS idx_import_jobs_active
    ON catalog.import_jobs(tenant_id, created_at DESC)
    WHERE status IN ('queued', 'validating', 'ready', 'committing');

COMMENT ON TABLE catalog.import_jobs IS
    'Bulk-catalog-import lifecycle ledger. status ∈ {queued, validating, ready, committing, finalized, cancelled}. Backs Flow B (supplier CSV import) per canary-item-setup-screen-decomp.md. Per-row tx commits update rows_imported / rows_skipped; abandoned jobs auto-cleanup at 7d. GRO-877 OQ #2 / GRO-884.';


-- ─── pricing.observed_price_rules — new table ────────────────────────

CREATE TABLE IF NOT EXISTS pricing.observed_price_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES app.tenants(id),
    item_id         UUID NOT NULL REFERENCES catalog.items(id) ON DELETE CASCADE,
    -- nullable — class is derivable from customer.CATEG_COD when present
    customer_class  TEXT,
    rule_code       TEXT NOT NULL,
    observed_price  NUMERIC(12,4) NOT NULL,
    observed_qty    NUMERIC(10,4) NOT NULL DEFAULT 1,
    -- back-link to PS_DOC_LIN-equivalent transaction line; nullable
    -- since some observations may come from non-transactional sources
    transaction_id  UUID,
    observed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attributes      JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_observed_prices_tenant_item
    ON pricing.observed_price_rules(tenant_id, item_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_observed_prices_rule
    ON pricing.observed_price_rules(rule_code, observed_at DESC);

COMMENT ON TABLE pricing.observed_price_rules IS
    'Observed pricing-rule outcomes from POS transactions. Counterpoint exposes only PRC_1 + REG_PRC + LST_COST in REST IM_ITEM; the 10-layer pricing precedence (location-specific / special / contract / promotional / BOGO / mix-match / break / minimum / customer-tier) surfaces only as outputs in PS_DOC_LIN_PRICE per transaction line. Canary observes these here rather than replicating the pricing engine. Populated by transaction adapter, NOT catalog adapter. GRO-880 P0 #6 / GRO-884.';
COMMENT ON COLUMN pricing.observed_price_rules.rule_code IS
    'POS-emitted rule code. Counterpoint values include REG, SPECIAL, CONTRACT, MIX_MATCH, BOGO, BREAK, MINIMUM, TIER. Square emits a different vocabulary; adapters normalize.';
