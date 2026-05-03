-- deploy/migrations/022_protocol_validation.up.sql
--
-- L402 verification token store — the revenue surface of the Canary
-- Protocol (patent Application 63/991,596). A caller submits an
-- event_hash, pays satoshis via L402 challenge, and receives a Merkle
-- proof that the event is anchored on Bitcoin. GRO-752.
--
-- NOTE: The dispatch spec referenced ledger.l402_verification_tokens but
-- no ledger schema exists in this codebase. Table lives in the protocol
-- schema to match the rest of the pipeline.

CREATE TABLE IF NOT EXISTS protocol.l402_verification_tokens (
    token_id        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_hash      text        NOT NULL,
    satoshi_price   bigint      NOT NULL DEFAULT 100,
    status          text        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','paid','consumed','expired')),
    preimage_hash   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    consumed_at     timestamptz
);

CREATE INDEX IF NOT EXISTS idx_l402_token_event
    ON protocol.l402_verification_tokens(event_hash);
