-- deploy/migrations/020_protocol_anchors.up.sql
--
-- Merkle & Ordinal anchor tables (Node 5/6 of the Canary Protocol
-- pipeline, patent Application 63/991,596). Sub 3 writes here after
-- inscribing a Merkle root on Bitcoin via OrdinalsBot.
-- GRO-750.

-- One row per Bitcoin inscription (one per batch anchor run).
CREATE TABLE IF NOT EXISTS protocol.anchors (
    anchor_id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    merkle_root        text        NOT NULL,
    inscription_id     text,                             -- null until confirmed on-chain
    btc_tx_id          text,
    btc_block_height   bigint,
    network            text        NOT NULL DEFAULT 'signet',
    event_count        int         NOT NULL,
    anchor_status      text        NOT NULL DEFAULT 'pending'
                       CHECK (anchor_status IN ('pending','inscribed','confirmed','failed')),
    anchored_at        timestamptz NOT NULL DEFAULT now()
);

-- Per-event Merkle proof membership: which anchor proves which event.
CREATE TABLE IF NOT EXISTS protocol.evidence_anchors (
    event_hash         text        NOT NULL REFERENCES protocol.evidence(event_hash),
    anchor_id          uuid        NOT NULL REFERENCES protocol.anchors(anchor_id),
    leaf_index         int         NOT NULL,
    merkle_proof       jsonb       NOT NULL,  -- [{sibling_hash, position: "left"|"right"}, ...]
    PRIMARY KEY (event_hash, anchor_id)
);

-- Supports the bilateral verification query: given event_hash → anchor.
CREATE INDEX IF NOT EXISTS idx_evidence_anchors_event_hash
    ON protocol.evidence_anchors(event_hash);
