-- 033_signing_keys.down.sql

DROP INDEX IF EXISTS app.signing_keys_verify_set;
DROP INDEX IF EXISTS app.signing_keys_one_active;
DROP TABLE IF EXISTS app.signing_keys;
