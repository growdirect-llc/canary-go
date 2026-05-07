-- deploy/migrations/022_protocol_validation.down.sql

DROP INDEX IF EXISTS protocol.idx_l402_token_event;
DROP TABLE IF EXISTS protocol.l402_verification_tokens;
