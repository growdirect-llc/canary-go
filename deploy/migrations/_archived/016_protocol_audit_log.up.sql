-- 016_protocol_audit_log.up.sql
--
-- Extends app.audit_log (migration 009) with protocol-specific columns
-- so that the API Gateway audit middleware (GRO-694) and any downstream
-- protocol service can record state-mutating MCP/handler invocations
-- without forking into a parallel table.
--
-- Decision rule: every PII-bearing or state-mutating MCP/HTTP invocation
-- writes to app.audit_log. Protocol-specific fields are added as nullable
-- columns so existing app-tier callers (identity, sessions, etc.) keep
-- writing the legacy shape.
--
-- Patent reference: Application 63/991,596, Node 2 — every payload that
-- traverses the gateway leaves an evidentiary record. Same DMZ landing
-- zone for internal and external callers (memory:
-- project_canary_is_customer_of_protocol).

-- Per-row protocol context. All nullable to keep migration backward
-- compatible with existing app.audit_log rows.

ALTER TABLE app.audit_log
    ADD COLUMN IF NOT EXISTS event_id        UUID,
    ADD COLUMN IF NOT EXISTS payload_digest  TEXT,
    ADD COLUMN IF NOT EXISTS source_code     TEXT,
    ADD COLUMN IF NOT EXISTS request_id      TEXT,
    ADD COLUMN IF NOT EXISTS user_agent      TEXT,
    ADD COLUMN IF NOT EXISTS status_code     INT,
    ADD COLUMN IF NOT EXISTS latency_ms      INT,
    ADD COLUMN IF NOT EXISTS actor_type      TEXT,
    ADD COLUMN IF NOT EXISTS mcp_server      TEXT,
    ADD COLUMN IF NOT EXISTS tool_name       TEXT;

-- Indexes that the gateway middleware and downstream forensic queries
-- will hit. event_id is unique-ish per gateway call but a payload may
-- legitimately produce multiple rows (request + downstream side effects),
-- so we use a non-unique index.
CREATE INDEX IF NOT EXISTS idx_audit_log_event_id
    ON app.audit_log (event_id)
    WHERE event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_log_payload_digest
    ON app.audit_log (payload_digest)
    WHERE payload_digest IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_log_source_code
    ON app.audit_log (source_code)
    WHERE source_code IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_log_request_id
    ON app.audit_log (request_id)
    WHERE request_id IS NOT NULL;
