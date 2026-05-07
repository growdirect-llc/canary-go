-- Reverse 032_api_key_prefix — drop the index + column.
-- GRO-860 / Sprint 2 T-L.

DROP INDEX IF EXISTS app.idx_api_keys_key_prefix;
ALTER TABLE app.api_keys DROP COLUMN IF EXISTS key_prefix;
