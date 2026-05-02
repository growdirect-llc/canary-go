-- 017_protocol_evidence.up.sql
--
-- Patent reference: Application 63/991,596, Node 3 / Sub 1 (Hash & Seal).
-- Data Sovereignty Stack reference: L1 Index — the immutable evidence
-- layer that L2 Structured Store and the Application Layer above it
-- are rebuildable from.
--
-- Sub 1 reads canonical events off Valkey Streams (protocol:events,
-- consumer group "sub1-hash-seal") and writes a row here for each one.
-- The chain is per-merchant: chain_hash links events for the same
-- merchant_id in ingest order. Different merchants are independent
-- chains (FIG. 4 of the application).
--
-- Mutability: append-only. UPDATE and DELETE are rejected by the
-- triggers below — defense in depth so that even a compromised
-- application role cannot rewrite history. The accountability rail
-- is the database, not the application.
--
-- Idempotency: the unique constraint on event_hash plus the natural
-- primary key on event_id together let Sub 1 replay the same Streams
-- entry safely after a crash. A duplicate insert returns a unique-
-- violation that the worker observes and ACKs without rewriting state.

CREATE TABLE IF NOT EXISTS protocol.evidence (
    event_id        UUID         PRIMARY KEY,
    event_hash      TEXT         NOT NULL UNIQUE,
    chain_hash      TEXT         NOT NULL,
    prev_chain_hash TEXT,
    source_code     TEXT         NOT NULL REFERENCES app.source_systems(code),
    merchant_id     UUID         NOT NULL REFERENCES app.merchants(id),
    raw_payload     JSONB        NOT NULL,
    ingested_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Per-merchant chain lookup: Sub 1 reads the most recent prev_chain_hash
-- for a given merchant before computing the next chain_hash.
CREATE INDEX IF NOT EXISTS evidence_merchant_chain_idx
    ON protocol.evidence (merchant_id, ingested_at DESC);

-- Bilateral verification: GET /v1/protocol/evidence/{event_hash}
-- The UNIQUE constraint above already covers this lookup, but spelling
-- it out keeps EXPLAIN plans obvious.
CREATE INDEX IF NOT EXISTS evidence_event_hash_idx
    ON protocol.evidence (event_hash);

-- DB-level append-only enforcement (defense in depth).
CREATE OR REPLACE FUNCTION protocol.evidence_block_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'protocol.evidence is append-only — % blocked', TG_OP
        USING ERRCODE = 'feature_not_supported';
END;
$$;

CREATE TRIGGER evidence_no_update
    BEFORE UPDATE ON protocol.evidence
    FOR EACH ROW EXECUTE FUNCTION protocol.evidence_block_mutation();

CREATE TRIGGER evidence_no_delete
    BEFORE DELETE ON protocol.evidence
    FOR EACH ROW EXECUTE FUNCTION protocol.evidence_block_mutation();

-- TRUNCATE bypasses row-level triggers on its own; we need a statement
-- trigger to block it as well. Without this, TRUNCATE would silently
-- erase the L1 evidence layer.
CREATE TRIGGER evidence_no_truncate
    BEFORE TRUNCATE ON protocol.evidence
    FOR EACH STATEMENT EXECUTE FUNCTION protocol.evidence_block_mutation();
