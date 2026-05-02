-- 018_protocol_source_secrets_sm_ref.down.sql
--
-- Reverts GRO-687. Drops the secret_sm_ref column and restores the
-- NOT NULL constraint on `secret`. Will fail if any rows have a
-- NULL `secret` — that's intentional, since dropping NOT NULL with
-- NULL data present would silently corrupt the v1 contract.

ALTER TABLE protocol.source_secrets
    DROP CONSTRAINT IF EXISTS chk_source_secrets_value_present;

ALTER TABLE protocol.source_secrets
    DROP COLUMN IF EXISTS secret_sm_ref;

ALTER TABLE protocol.source_secrets
    ALTER COLUMN secret SET NOT NULL;
