-- deploy/migrations/021_protocol_namespace.down.sql
--
-- Reverses 021_protocol_namespace.up.sql. GRO-751.

DROP INDEX IF EXISTS protocol.idx_ns_reg_name;
DROP INDEX IF EXISTS protocol.idx_ns_reg_owner;
DROP TABLE IF EXISTS protocol.namespace_registrations;
