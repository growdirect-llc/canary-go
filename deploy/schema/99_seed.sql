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
-- Dev source secret for the gateway smoke test (matches the merchant above)
-- secret value is a known constant for local-dev only; rotate before any
-- exposure beyond the laptop.
-- ─────────────────────────────────────────────────────────────────────
INSERT INTO protocol.source_secrets
    (id, merchant_id, source_code, secret, signature_algo, status, replay_window_seconds) VALUES
    ('44444444-0000-0000-0000-000000000001',
     '33333333-0000-0000-0000-000000000001',
     'square',
     'dev-only-do-not-ship-this-secret-1234567890abcdef',
     'HMAC-SHA256',
     'active',
     300)
ON CONFLICT (id) DO NOTHING;

-- Convenience view: stable identifiers used throughout dev/test
COMMENT ON TABLE app.merchants IS 'Multi-POS merchants. Dev seed: id=33333333-0000-0000-0000-000000000001 (Acme Main Street, Square)';
