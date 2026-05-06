-- 029_pos_tenant_credentials_expires_at.up.sql
--
-- Adds expires_at as a plain column so the devops panel can display token
-- expiry without decrypting credentials_enc. Written by StoreToken on every
-- upsert alongside the encrypted blob.

ALTER TABLE app.pos_tenant_credentials
    ADD COLUMN IF NOT EXISTS expires_at timestamptz;
