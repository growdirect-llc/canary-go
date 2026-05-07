-- 031_audit_log_append_only — enforce append-only invariant on app.audit_log.
--
-- The CanaryGo CLAUDE.md "Key Invariants" section claims:
--   "4. Append-only evidence — fox.evidence_records has a DB trigger
--    blocking UPDATE/DELETE"
-- The protocol.evidence trigger (deploy/schema/11_protocol.sql:43-54) does
-- enforce this for protocol-edge evidence. The application-side audit log
-- at app.audit_log was not similarly protected — verified absent in the
-- 2026-05-07 security review (Sec H4) and during the IP-handover scan.
--
-- This migration mirrors the protocol.evidence trigger pattern onto
-- app.audit_log so the documentation invariant matches the schema reality.
-- Closes Sec H4 + SOC2 CC7.2 + PCI Req 10 + GDPR Article 30 audit-record
-- tamper-evidence requirements.
--
-- GRO-851 / Sprint 2 T-F.

CREATE OR REPLACE FUNCTION app.audit_log_block_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'app.audit_log is append-only — % blocked', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON app.audit_log
  FOR EACH ROW EXECUTE FUNCTION app.audit_log_block_mutation();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON app.audit_log
  FOR EACH ROW EXECUTE FUNCTION app.audit_log_block_mutation();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON app.audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION app.audit_log_block_mutation();
