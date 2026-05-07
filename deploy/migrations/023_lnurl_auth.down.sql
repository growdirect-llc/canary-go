-- 023_lnurl_auth.down.sql
--
-- Drops LNURL-auth tables. Challenges must go first (no FK, but logical
-- dependency: challenges reference linked_keys via linked_id).
-- NOTE: DROP INDEX does not accept schema-qualified names in PostgreSQL.

DROP TABLE IF EXISTS app.lnurl_auth_challenges;
DROP TABLE IF EXISTS app.lnurl_linked_keys;
