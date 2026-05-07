-- 032_api_key_prefix — index API keys by a deterministic prefix so the
-- argon2id verify loop runs at most once per authenticated request.
--
-- Pre-T-L: AuthenticateAPIKey scanned every active row and called
-- argon2id.Verify on each. argon2id is intentionally slow (~50ms per
-- call); the scan was O(n) argon2 calls per request, scaling badly
-- past low key counts.
--
-- Post-T-L: each new key carries a `key_prefix` column (e.g. "cy_AbCdEfGh")
-- — the first 11 plaintext characters. The plaintext prefix is
-- recoverable from any inbound bearer token, so the verify path filters
-- candidates by `WHERE key_prefix = $1` first, then runs argon2id verify
-- on the (at most 1, normally) remaining row.
--
-- Legacy rows with NULL key_prefix fall back to the full-scan path —
-- there's no way to backfill from `key_hash` alone since argon2id is
-- non-deterministic. New rows mint with the prefix populated; old rows
-- can be rotated as keys age.
--
-- GRO-860 / Sprint 2 T-L.

ALTER TABLE app.api_keys
  ADD COLUMN IF NOT EXISTS key_prefix TEXT;

-- Partial index: only present rows. NULL legacy rows are excluded.
CREATE INDEX IF NOT EXISTS idx_api_keys_key_prefix
  ON app.api_keys (key_prefix)
  WHERE key_prefix IS NOT NULL;
