-- 018_protocol_source_secrets_sm_ref.up.sql
--
-- GRO-687: route secret values through GCP Secret Manager.
--
-- The v1 design (migration 015) stored the per-source HMAC secret as
-- plaintext in protocol.source_secrets.secret. That made a single
-- accidental commit or DB dump a full-key compromise. This migration
-- adds a `secret_sm_ref` column carrying the full Secret Manager
-- resource path; runtime (SmResolver) reads metadata from this row
-- and the value from SM. Postgres no longer holds production secret
-- bytes.
--
-- Rollout posture (transitional):
--
--   * `secret` is made nullable. Dev environments using PgxResolver
--     keep populating it; prod environments using SmResolver leave
--     it NULL and populate `secret_sm_ref`.
--   * A row must have either `secret` or `secret_sm_ref` (or both
--     during a hot migration window). Enforced by CHECK constraint.
--   * No data backfill happens here — production seeds new rows via
--     the SM-aware seeding tool (separate dispatch). Existing dev
--     fixtures continue to work unchanged.
--
-- Rollout posture (steady state, after all environments cut over):
--
--   * The `secret` column will be dropped in a follow-up migration
--     once SmResolver is the only resolver in production. Tracked
--     in the SDD at docs/sdds/canary-go/secrets-manager-integration.md.
--
-- Resource path convention (also encoded in
-- secrets.BuildResourcePath):
--
--   projects/{PROJECT}/secrets/canary-source-{merchant_id}-{source_code}/versions/latest

ALTER TABLE protocol.source_secrets
    ALTER COLUMN secret DROP NOT NULL;

ALTER TABLE protocol.source_secrets
    ADD COLUMN IF NOT EXISTS secret_sm_ref TEXT;

-- Either secret (legacy/dev) or secret_sm_ref (prod via SM) must be
-- populated. Both is allowed during a rotation/cutover window.
ALTER TABLE protocol.source_secrets
    ADD CONSTRAINT chk_source_secrets_value_present
        CHECK (secret IS NOT NULL OR secret_sm_ref IS NOT NULL);

COMMENT ON COLUMN protocol.source_secrets.secret IS
    'Plaintext webhook secret. DEPRECATED for production use as of GRO-687; populate secret_sm_ref instead. Will be dropped in a follow-up migration once SmResolver is the only production backend.';

COMMENT ON COLUMN protocol.source_secrets.secret_sm_ref IS
    'Full GCP Secret Manager resource path (projects/{p}/secrets/canary-source-{merchant_id}-{source_code}/versions/latest). Read by SmResolver at lookup time; the actual secret bytes never sit in Postgres.';
