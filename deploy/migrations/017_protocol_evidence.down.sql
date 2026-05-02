-- 017_protocol_evidence.down.sql
--
-- Reverse of 017_protocol_evidence.up.sql. The append-only triggers
-- block DELETE/TRUNCATE on populated tables, so a downgrade against
-- a real cluster requires DROP TABLE — which CASCADEs through the
-- triggers because the triggers belong to the table.

DROP TABLE IF EXISTS protocol.evidence CASCADE;

DROP FUNCTION IF EXISTS protocol.evidence_block_mutation();
