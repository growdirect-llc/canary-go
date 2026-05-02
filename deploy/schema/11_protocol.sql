-- 11_protocol.sql — API Gateway protocol layer (Patent Application 63/991,596 Node 2/3)
-- Source: archived migrations 015 (source_secrets), 017 (evidence write-once), 018 (sm_ref)
-- Schema: protocol
--
-- The protocol schema captures per-source HMAC secrets used by the gateway
-- to authenticate webhooks, plus the L1 evidence chain (Sub 1 / Hash & Seal).
-- The gateway audit trail itself lives in app.audit_log (see 01_app_foundation.sql).

-- protocol.source_secrets — per-(merchant, source) HMAC secret metadata + Secret Manager reference
CREATE TABLE protocol.source_secrets (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID        NOT NULL REFERENCES app.merchants(id),
    source_code           TEXT        NOT NULL REFERENCES app.source_systems(code),
    secret                TEXT,                                                       -- nullable: dev-mode plaintext OR null when secret_sm_ref set
    signature_algo        TEXT        NOT NULL CHECK (signature_algo = 'HMAC-SHA256'),
    status                TEXT        NOT NULL CHECK (status IN ('active','rotated','revoked')),
    replay_window_seconds INT         NOT NULL DEFAULT 300
                                       CHECK (replay_window_seconds BETWEEN 30 AND 3600),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at            TIMESTAMPTZ,
    secret_sm_ref         TEXT,                                                       -- GCP Secret Manager: projects/{p}/secrets/canary-source-{merchant}-{source}/versions/latest
    CONSTRAINT chk_source_secrets_value_present
      CHECK (secret IS NOT NULL OR secret_sm_ref IS NOT NULL)
);
CREATE INDEX idx_source_secrets_merchant ON protocol.source_secrets(merchant_id, source_code);
CREATE INDEX idx_source_secrets_active   ON protocol.source_secrets(merchant_id) WHERE status = 'active';

-- protocol.evidence — write-once L1 evidence chain (Sub 1 / Node 3 of patent)
CREATE TABLE protocol.evidence (
    event_id        UUID        PRIMARY KEY,
    event_hash      TEXT        NOT NULL UNIQUE,
    chain_hash      TEXT        NOT NULL,
    prev_chain_hash TEXT,
    source_code     TEXT        NOT NULL,
    merchant_id     UUID        NOT NULL,                                  -- soft FK; merchant deletion shouldn't cascade evidence
    raw_payload     JSONB       NOT NULL,
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX evidence_merchant_chain_idx ON protocol.evidence (merchant_id, ingested_at DESC);

-- DB-level append-only enforcement (defense in depth)
CREATE OR REPLACE FUNCTION protocol.evidence_block_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'protocol.evidence is append-only — % blocked', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER evidence_no_update BEFORE UPDATE ON protocol.evidence
  FOR EACH ROW EXECUTE FUNCTION protocol.evidence_block_mutation();
CREATE TRIGGER evidence_no_delete BEFORE DELETE ON protocol.evidence
  FOR EACH ROW EXECUTE FUNCTION protocol.evidence_block_mutation();
CREATE TRIGGER evidence_no_truncate BEFORE TRUNCATE ON protocol.evidence
  FOR EACH STATEMENT EXECUTE FUNCTION protocol.evidence_block_mutation();
