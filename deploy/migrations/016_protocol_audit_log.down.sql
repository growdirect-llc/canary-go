-- 016_protocol_audit_log.down.sql
--
-- Reverses 016_protocol_audit_log.up.sql cleanly. Drops the protocol
-- context indexes first, then the columns. Existing pre-protocol rows
-- in app.audit_log are unaffected because every added column is
-- nullable.

DROP INDEX IF EXISTS app.idx_audit_log_request_id;
DROP INDEX IF EXISTS app.idx_audit_log_source_code;
DROP INDEX IF EXISTS app.idx_audit_log_payload_digest;
DROP INDEX IF EXISTS app.idx_audit_log_event_id;

ALTER TABLE app.audit_log
    DROP COLUMN IF EXISTS tool_name,
    DROP COLUMN IF EXISTS mcp_server,
    DROP COLUMN IF EXISTS actor_type,
    DROP COLUMN IF EXISTS latency_ms,
    DROP COLUMN IF EXISTS status_code,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS request_id,
    DROP COLUMN IF EXISTS source_code,
    DROP COLUMN IF EXISTS payload_digest,
    DROP COLUMN IF EXISTS event_id;
