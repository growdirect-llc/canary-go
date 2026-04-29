-- internal/db/sqlc/identity.sql

-- name: GetMerchantByID :one
SELECT id, organization_id, source_merchant_id, merchant_name, currency, is_active,
       created_at, updated_at
FROM app.merchants
WHERE id = $1;

-- name: GetMerchantBySourceID :one
SELECT id, organization_id, source_merchant_id, merchant_name, currency, is_active,
       created_at, updated_at
FROM app.merchants
WHERE source_merchant_id = $1;

-- name: GetUserByEmail :one
SELECT id, merchant_id, username, email, display_name, is_active, last_login_at,
       created_at, updated_at
FROM app.users
WHERE merchant_id = $1 AND email = $2 AND db_status = 'active';

-- name: GetUserByID :one
SELECT id, merchant_id, username, email, display_name, is_active, last_login_at,
       created_at, updated_at
FROM app.users
WHERE id = $1 AND db_status = 'active';

-- name: GetUserRoles :many
SELECT r.role_name
FROM app.user_roles ur
JOIN app.roles r ON r.id = ur.role_id
WHERE ur.user_id = $1 AND ur.merchant_id = $2;

-- name: CreateOrganization :one
INSERT INTO app.organizations (org_name, subscription_tier)
VALUES ($1, $2)
RETURNING id, org_name, subscription_tier, is_active, created_at, updated_at;

-- name: CreateMerchant :one
INSERT INTO app.merchants (organization_id, source_merchant_id, merchant_name, currency)
VALUES ($1, $2, $3, $4)
RETURNING id, organization_id, source_merchant_id, merchant_name, currency, is_active,
          created_at, updated_at;

-- name: CreateUser :one
INSERT INTO app.users (merchant_id, username, email, display_name)
VALUES ($1, $2, $3, $4)
RETURNING id, merchant_id, username, email, display_name, is_active, created_at, updated_at;

-- name: UpdateUserLastLogin :exec
UPDATE app.users
SET last_login_at = now(), updated_at = now()
WHERE id = $1;
