-- 033_signing_keys.up.sql
--
-- T-2 / GRO-862 — JWKS keystore.
--
-- Holds the signing key set for JWT minting + JWKS publication.
-- Two-key rotation requires both `active` and `retiring` rows to
-- coexist for ≥24h; only `active` mints, both verify. `expired`
-- rows are kept for forensic lookup; the JWKS endpoint omits them.

CREATE TABLE app.signing_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kid             TEXT NOT NULL UNIQUE,
    -- Algorithm whitelist enforced at the application layer too
    -- (T-1's mint path rejects anything else); the CHECK is
    -- defense-in-depth so a manual INSERT can't smuggle "none" or
    -- HS256 in.
    alg             TEXT NOT NULL CHECK (alg IN ('RS256', 'ES256', 'EdDSA')),
    public_jwk      JSONB NOT NULL,
    -- Private key material is encrypted at rest by the application
    -- (Secret Manager backed envelope key in production, plaintext
    -- in dev). The keystore module never returns this column to
    -- callers other than the minter.
    private_key_pem TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'retiring', 'expired')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- When the row entered 'retiring'. NULL while active.
    retiring_at     TIMESTAMPTZ,
    -- When the row entered 'expired' (no longer serves verify). NULL
    -- while active or retiring. retired_at >= retiring_at + 24h is
    -- the operational invariant the rotation runbook enforces.
    retired_at      TIMESTAMPTZ,
    CONSTRAINT signing_keys_status_timestamps_consistent CHECK (
        (status = 'active'   AND retiring_at IS NULL AND retired_at IS NULL) OR
        (status = 'retiring' AND retiring_at IS NOT NULL AND retired_at IS NULL) OR
        (status = 'expired'  AND retiring_at IS NOT NULL AND retired_at IS NOT NULL)
    )
);

-- Hot path: keystore.Active() reads the single active row on every
-- mint. Rotation flips the previous row to 'retiring'; an exclusion
-- constraint on status='active' prevents two active rows
-- existing simultaneously (which would make "which one mints?"
-- ambiguous).
CREATE UNIQUE INDEX signing_keys_one_active
    ON app.signing_keys (status)
    WHERE status = 'active';

-- Verify path: keystore.VerifySet() reads all active+retiring rows
-- so the JWKS endpoint and the JWT verifier both see the rolling
-- window. Index lets the partial scan stay cheap.
CREATE INDEX signing_keys_verify_set
    ON app.signing_keys (status, kid)
    WHERE status IN ('active', 'retiring');

COMMENT ON TABLE app.signing_keys IS
    'JWT signing key set with two-key rotation. T-2 / GRO-862. '
    'One active row mints; active + retiring rows both verify; '
    'expired rows kept for forensic lookup but excluded from JWKS.';
