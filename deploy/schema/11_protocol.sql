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

-- protocol.anchors / protocol.evidence_anchors — Sub 3 Merkle anchoring.
-- Lifted verbatim from migration 020_protocol_anchors.up.sql so a fresh
-- make db-reset-test produces the same shape as a deployed DB after
-- make migrate-up (CLAUDE.md two-tier rule).
CREATE TABLE protocol.anchors (
    anchor_id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merkle_root      TEXT        NOT NULL,
    inscription_id   TEXT,                              -- null until confirmed on-chain
    btc_tx_id        TEXT,
    btc_block_height BIGINT,
    network          TEXT        NOT NULL DEFAULT 'signet',
    event_count      INT         NOT NULL,
    anchor_status    TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (anchor_status IN ('pending','inscribed','confirmed','failed')),
    anchored_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE protocol.evidence_anchors (
    event_hash   TEXT  NOT NULL REFERENCES protocol.evidence(event_hash),
    anchor_id    UUID  NOT NULL REFERENCES protocol.anchors(anchor_id),
    leaf_index   INT   NOT NULL,
    merkle_proof JSONB NOT NULL,                          -- [{sibling_hash, position: "left"|"right"}, ...]
    PRIMARY KEY (event_hash, anchor_id)
);
CREATE INDEX idx_evidence_anchors_event_hash ON protocol.evidence_anchors(event_hash);

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

-- ─────────────────────────────────────────────────────────────────────
-- protocol.dlq — Dead-letter queue for inbound webhook payloads that
-- failed to persist or publish downstream. Spec: GRO-764 Phase A.1
-- (folds GRO-642).
--
-- Rows are written when the gateway accepts a webhook (HMAC verified)
-- but the downstream pipeline rejects it — Valkey publish failure,
-- sub1 seal failure, sub2 parse failure, or any other recoverable
-- error. The replay endpoint (POST /v1/webhooks/replay/{id}) re-fires
-- the payload through the same pipeline.
--
-- Backoff schedule (next_retry_at): 1m → 5m → 30m → 2h → manual.
-- After 4 automatic retries, status flips to 'abandoned' and the row
-- requires explicit operator action via the replay endpoint.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS protocol.dlq (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID        NOT NULL,                  -- soft FK; merchant deletion shouldn't cascade DLQ rows
    source_code         TEXT        NOT NULL REFERENCES app.source_systems(code),
    source_event_id     TEXT,                                  -- caller-supplied idempotency key when present
    event_id            UUID,                                  -- canonical event id when minted before failure
    payload             JSONB       NOT NULL,                  -- raw inbound body
    headers             JSONB       NOT NULL DEFAULT '{}',     -- inbound headers (for HMAC replay)
    failure_reason      TEXT        NOT NULL,                  -- short code: publish_failed | seal_failed | parse_failed | etc.
    error_message       TEXT,                                  -- detail; may be redacted in non-debug mode
    retry_count         INT         NOT NULL DEFAULT 0,
    next_retry_at       TIMESTAMPTZ,                           -- NULL when status='abandoned' or 'replayed'
    status              TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending', 'replayed', 'abandoned')),
    last_replay_at      TIMESTAMPTZ,
    last_replay_outcome TEXT,                                  -- success | failure
    attributes          JSONB       NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dlq_status_retry ON protocol.dlq(status, next_retry_at)
  WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_dlq_merchant_source ON protocol.dlq(merchant_id, source_code, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dlq_source_event ON protocol.dlq(source_code, source_event_id)
  WHERE source_event_id IS NOT NULL;

-- ─────────────────────────────────────────────────────────────────────
-- protocol.tsp_sequence_log — TSP sequence-id tracking per source for
-- replay-by-sequence + gap detection. Tier-1 feeds (real-time POS
-- webhooks) record here when source provides a sequence id; the
-- recording path checks the previous row and flags gap_detected when
-- expected sequencing is broken.
-- ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS protocol.tsp_sequence_log (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID        NOT NULL,                    -- soft FK
    source_code       TEXT        NOT NULL REFERENCES app.source_systems(code),
    sequence_id       TEXT        NOT NULL,                    -- source-supplied sequence
    event_id          UUID        NOT NULL,                    -- canonical event id
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    gap_detected      BOOLEAN     NOT NULL DEFAULT FALSE,
    expected_prev_seq TEXT,                                    -- populated when gap_detected
    UNIQUE (merchant_id, source_code, sequence_id)
);
CREATE INDEX IF NOT EXISTS idx_tsp_seq_merchant_source ON protocol.tsp_sequence_log(merchant_id, source_code, received_at DESC);
CREATE INDEX IF NOT EXISTS idx_tsp_seq_gaps ON protocol.tsp_sequence_log(merchant_id, source_code) WHERE gap_detected = TRUE;
