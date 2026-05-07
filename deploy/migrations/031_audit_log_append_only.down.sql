-- Reverse 031_audit_log_append_only — drop the triggers and the function.
-- Reversal of the append-only invariant should be exceptional; document
-- the operational reason in the rollback ticket if exercised.
--
-- GRO-851 / Sprint 2 T-F.

DROP TRIGGER IF EXISTS audit_log_no_truncate ON app.audit_log;
DROP TRIGGER IF EXISTS audit_log_no_delete   ON app.audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update   ON app.audit_log;
DROP FUNCTION IF EXISTS app.audit_log_block_mutation();
