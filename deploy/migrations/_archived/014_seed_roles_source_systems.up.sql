-- 014_seed_roles_source_systems.up.sql
INSERT INTO app.roles (role_name, description)
VALUES
    ('admin',    'Platform administrator — full access'),
    ('owner',    'Merchant owner — full tenant access'),
    ('manager',  'Store manager — operational access'),
    ('operator', 'Store operator — transaction and alert access'),
    ('member',   'Team member — read-only operational'),
    ('viewer',   'Read-only viewer')
ON CONFLICT (role_name) DO NOTHING;

INSERT INTO app.source_systems (code, display_name, category)
VALUES
    ('square',       'Square',             'POS'),
    ('counterpoint', 'NCR Counterpoint',   'POS'),
    ('clover',       'Clover',             'POS')
ON CONFLICT (code) DO NOTHING;
