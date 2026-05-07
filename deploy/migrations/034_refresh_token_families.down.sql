-- 034_refresh_token_families.down.sql

DROP INDEX IF EXISTS app.refresh_token_families_subject;
DROP TABLE IF EXISTS app.refresh_token_families;
