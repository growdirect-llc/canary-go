-- deploy/migrations/019_report_jobs.up.sql
--
-- Persistent report job table. Replaces the in-memory stub in
-- internal/report/store.go. Wave E — GRO-767.

CREATE TABLE IF NOT EXISTS app.report_jobs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL,
    report_type  TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending',
    format       TEXT        NOT NULL DEFAULT 'csv',
    date_from    TEXT,
    date_to      TEXT,
    location_id  UUID,
    download_url TEXT,
    error_msg    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_report_jobs_tenant_created
    ON app.report_jobs (tenant_id, created_at DESC);
