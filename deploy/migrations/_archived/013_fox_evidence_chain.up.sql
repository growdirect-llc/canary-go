-- 013_fox_evidence_chain.up.sql
-- Evidence records are append-only. The trigger below enforces this at the DB level.
CREATE TABLE IF NOT EXISTS app.fox_evidence (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    case_id         UUID        NOT NULL REFERENCES app.fox_cases(id),
    record_type     TEXT        NOT NULL,
    record_payload  JSONB       NOT NULL,
    chain_hash      TEXT        NOT NULL,
    uploaded_by     TEXT,
    file_path       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fox_evidence_case_id ON app.fox_evidence (case_id);
CREATE INDEX IF NOT EXISTS idx_fox_evidence_merchant_id ON app.fox_evidence (merchant_id);

CREATE TABLE IF NOT EXISTS app.fox_evidence_access_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    evidence_id UUID        NOT NULL REFERENCES app.fox_evidence(id),
    accessed_by TEXT        NOT NULL,
    ip_address  TEXT,
    accessed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fox_evidence_access_log_evidence_id ON app.fox_evidence_access_log (evidence_id);

-- Immutability trigger: UPDATE and DELETE on fox_evidence are prohibited.
CREATE OR REPLACE FUNCTION app.fox_evidence_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'fox_evidence is append-only — UPDATE and DELETE are prohibited';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_fox_evidence_immutable ON app.fox_evidence;
CREATE TRIGGER trg_fox_evidence_immutable
    BEFORE UPDATE OR DELETE ON app.fox_evidence
    FOR EACH ROW EXECUTE FUNCTION app.fox_evidence_immutable();
