-- 99_seed.sql — dev fixture data
-- Stable UUIDs so tests can rely on them. Idempotent (ON CONFLICT DO NOTHING).
-- Loaded LAST, after all schema files. Use `make db-seed` to re-apply.

-- ─────────────────────────────────────────────────────────────────────
-- Source systems (must exist before merchant_sources or external_identities)
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO app.source_systems (code, display_name, category) VALUES
    ('square',       'Square',         'pos'),
    ('counterpoint', 'NCR Counterpoint', 'pos'),
    ('clover',       'Clover',         'pos')
ON CONFLICT (code) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- Roles
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO app.roles (id, role_name, description) VALUES
    ('00000000-0000-0000-0000-000000000001', 'admin',     'tenant-admin / can configure everything'),
    ('00000000-0000-0000-0000-000000000002', 'analyst',   'read everything, escalate cases, no config'),
    ('00000000-0000-0000-0000-000000000003', 'viewer',    'read-only dashboard'),
    ('00000000-0000-0000-0000-000000000004', 'auditor',   'read-only including evidence chain')
ON CONFLICT (role_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- Dev tenant — Acme Test Org / Acme Main Street Store
-- Stable UUIDs so test code can hard-code them.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO app.organizations (id, org_name, subscription_tier, is_active, db_status) VALUES
    ('11111111-0000-0000-0000-000000000001', 'Acme Test Org', 'starter', true, 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO app.tenants (id, organization_id, tenant_code, name, status, schema_name) VALUES
    ('22222222-0000-0000-0000-000000000001',
     '11111111-0000-0000-0000-000000000001',
     'acme-main',
     'Acme Main Street',
     'active',
     'tenant_acme_main')
ON CONFLICT (organization_id, tenant_code) DO NOTHING;

INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name, currency) VALUES
    ('33333333-0000-0000-0000-000000000001',
     '11111111-0000-0000-0000-000000000001',
     '22222222-0000-0000-0000-000000000001',
     'acme-main-square-001',
     'Acme Main Street (Square)',
     'USD')
ON CONFLICT (id) DO NOTHING;

INSERT INTO app.merchant_sources (merchant_id, source_code, status, raas_namespace) VALUES
    ('33333333-0000-0000-0000-000000000001', 'square', 'active', 'acme-main-square')
ON CONFLICT DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- Dev source secret for the gateway smoke test. The literal value is a
-- placeholder — `deploy/scripts/seed-dev-secrets.sh` substitutes a real
-- random 32-byte hex string at apply time and writes it to .env.local
-- so the gateway smoke-test caller can sign with the same value. Never
-- ship this seed file's placeholder verbatim; CI fails the build if the
-- placeholder appears in any file under deploy/ outside this comment.
-- GRO-859 / Sprint 2 T-W.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO protocol.source_secrets
    (id, merchant_id, source_code, secret, signature_algo, status, replay_window_seconds) VALUES
    ('44444444-0000-0000-0000-000000000001',
     '33333333-0000-0000-0000-000000000001',
     'square',
     '__SEED_HMAC_PLACEHOLDER__',
     'HMAC-SHA256',
     'active',
     300)
ON CONFLICT (id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- finance.tender_types defaults per source — loop3-wave1 (GRO-762 §B.2)
-- One default-tender row per (tenant, source) so Sub2 can resolve a
-- tender_type_id when the inbound POS payload doesn't carry one.
-- Tenants can add unlimited custom rows beyond these defaults
-- (source_code NULL); the partial unique index uq_tender_source_default
-- only constrains the seeded ones. Idempotent via ON CONFLICT on the
-- natural (tenant, code) key.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO finance.tender_types (id, tenant_id, source_code, code, name, tender_class, is_active, attributes)
SELECT
    gen_random_uuid(),
    t.id,
    src.code,
    upper(src.code) || '_DEFAULT',
    src.display_name || ' Default',
    'unknown',
    true,
    jsonb_build_object('seeded', true, 'source', 'loop3_wave1_default')
FROM app.tenants t
CROSS JOIN (VALUES
    ('square',       'Square'),
    ('counterpoint', 'NCR Counterpoint'),
    ('clover',       'Clover')
) AS src(code, display_name)
ON CONFLICT (tenant_id, code) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- app.webhook_backpressure_config — platform-wide defaults per source.
-- Per-tenant overrides land at runtime via admin API. (GRO-764 Phase A.1)
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO app.webhook_backpressure_config (merchant_id, source_code, max_rps, burst_capacity, enabled, attributes)
SELECT NULL, src.code, 100, 200, TRUE,
       jsonb_build_object('seeded', true, 'source', 'loop4_wave_b_default')
FROM (VALUES ('square'), ('counterpoint'), ('clover')) AS src(code)
WHERE NOT EXISTS (
    SELECT 1 FROM app.webhook_backpressure_config bp
    WHERE bp.merchant_id IS NULL AND bp.source_code = src.code
);

-- ─────────────────────────────────────────────────────────────────────
-- app.api_keys — dev seeds. Two rows: one platform-scope (gateway/sub*
-- internal calls), one tenant-scope (Acme Main Street external API).
-- key_hash is argon2id of a known dev plaintext — DO NOT ship beyond
-- the laptop. Plaintext is documented in the code seed comment for
-- internal/identity/apikey_test.go to use.
--
-- Dev plaintext (rotate before any external exposure):
--   platform: cy_dev_platform_key_DO_NOT_SHIP
--   tenant:   cy_dev_acme_tenant_key_DO_NOT_SHIP
--
-- The hash strings below are computed by the Go test seed (see
-- internal/identity/apikey_test.go::TestSeed). When the seed run
-- diverges (hash format change, new argon2id params), regenerate
-- via the test and replace these literals. GRO-763 Phase C.2.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO app.api_keys (id, tenant_id, agent_name, key_hash, scopes, rate_limit_rpm, status, attributes)
VALUES
    -- platform-scope key
    ('55555555-0000-0000-0000-000000000001',
     NULL,
     'gateway',
     'argon2id$DEV_PLATFORM_PLACEHOLDER',
     ARRAY['webhook:write', 'evidence:read', 'evidence:write']::text[],
     1200,
     'active',
     '{"seeded": true, "purpose": "dev-platform-scope", "do_not_ship": true}'::jsonb),
    -- tenant-scope key for Acme Main Street
    ('55555555-0000-0000-0000-000000000002',
     '22222222-0000-0000-0000-000000000001',
     'alx-dev',
     'argon2id$DEV_TENANT_PLACEHOLDER',
     ARRAY['evidence:read', 'transaction:read']::text[],
     600,
     'active',
     '{"seeded": true, "purpose": "dev-tenant-scope", "do_not_ship": true}'::jsonb)
ON CONFLICT (id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────
-- finance.markup_envelope_tiers defaults per archetype.
--
-- Pricing module reads tenant override
-- (app.tenants.attributes->>'markup_envelope_pct') first and falls back
-- to the active row here for the tenant's archetype. Idempotent: skip
-- if a non-expired row for the archetype already exists.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO finance.markup_envelope_tiers (archetype, markup_pct, attributes)
SELECT * FROM (VALUES
    ('small',  50.00, '{"seeded": true, "source": "OQ-2.1"}'::jsonb),
    ('medium', 30.00, '{"seeded": true, "source": "OQ-2.1"}'::jsonb),
    ('large',  15.00, '{"seeded": true, "source": "OQ-2.1"}'::jsonb)
) AS v(archetype, markup_pct, attributes)
WHERE NOT EXISTS (
    SELECT 1 FROM finance.markup_envelope_tiers
    WHERE archetype = v.archetype AND expires_at IS NULL
);

-- Convenience view: stable identifiers used throughout dev/test
COMMENT ON TABLE app.merchants IS 'Multi-POS merchants. Dev seed: id=33333333-0000-0000-0000-000000000001 (Acme Main Street, Square)';
