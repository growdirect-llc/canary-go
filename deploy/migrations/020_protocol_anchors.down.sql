-- deploy/migrations/020_protocol_anchors.down.sql
--
-- Rolls back migration 020: drops the Merkle anchor tables.
-- GRO-750.

DROP INDEX IF EXISTS idx_evidence_anchors_event_hash;
DROP TABLE IF EXISTS protocol.evidence_anchors;
DROP TABLE IF EXISTS protocol.anchors;
