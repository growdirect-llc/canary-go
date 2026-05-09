// Package sub1 implements the Hash & Seal subscriber — Node 3 / Sub 1
// of the Canary protocol pipeline (patent Application 63/991,596).
//
// Responsibilities:
//
//   - Consume canonical events from Valkey Streams (protocol:events)
//   - Compute a per-merchant chain_hash linking each event to the
//     previous one for the same merchant_id
//   - Insert into protocol.evidence (write-once, JSONB)
//   - Be idempotent: a duplicate event_hash is detected via the UNIQUE
//     constraint and is treated as a no-op
//
// The Sub 1 worker is the runtime around this package; the package
// itself is infra-light so unit tests can exercise the chain logic
// without Postgres.
package sub1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

// ErrDuplicateEvent is returned by WriteEvidence when the event_hash
// has already been recorded. Callers (the worker) treat this as a
// successful no-op and ACK the source message.
var ErrDuplicateEvent = errors.New("sub1: duplicate event_hash — already sealed")

// pgUniqueViolation is the SQLSTATE for unique_violation. We compare
// against this to decide whether an INSERT failure is a duplicate
// (idempotent retry) or a real error.
const pgUniqueViolation = "23505"

// DB is the minimal Postgres surface Sub 1 needs. Using an interface
// keeps the seal package unit-testable with a stub. *pgxpool.Pool,
// *pgx.Conn, and pgx.Tx all satisfy it — pgx.Tx's Begin opens a
// nested savepoint, which is fine for our purposes (the inner WriteEvidence
// transaction can ride on top of an outer one if a caller ever wraps).
type DB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ComputeChainHash computes the per-merchant chain hash:
//
//	chain_hash = SHA-256(event_hash || prev_chain_hash || timestamp)
//
// The timestamp is encoded as RFC3339Nano (UTC) to keep the digest
// deterministic across machines and to preserve sub-second ordering.
// prevChainHash is "" for the first event in a merchant's chain.
//
// This is the precise hash the patent (Application 63/991,596, FIG. 4)
// describes. Per-merchant chains, not a single global chain — that's
// what gives different tenants independent verifiability and lets us
// shard the L1 store later without disturbing chain semantics.
func ComputeChainHash(eventHash, prevChainHash string, ts time.Time) string {
	h := sha256.New()
	h.Write([]byte(eventHash))
	h.Write([]byte(prevChainHash))
	h.Write([]byte(ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))
}

// LookupPrevChainHash returns the chain_hash of the most recent event
// for the given merchant, or "" if none exists. The query is bounded
// by the per-(merchant_id, ingested_at DESC) index defined in
// migration 017.
func LookupPrevChainHash(ctx context.Context, db DB, merchantID uuid.UUID) (string, error) {
	const q = `
		SELECT chain_hash
		FROM protocol.evidence
		WHERE merchant_id = $1
		ORDER BY ingested_at DESC
		LIMIT 1
	`
	var prev string
	err := db.QueryRow(ctx, q, merchantID).Scan(&prev)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("sub1: lookup prev_chain_hash: %w", err)
	}
	return prev, nil
}

// WriteEvidence seals one canonical event. It computes the chain_hash
// from the merchant's most recent prev_chain_hash and inserts the row
// into protocol.evidence. Returns ErrDuplicateEvent if the event_hash
// is already present (idempotent retry path).
//
// The lookup-then-insert sequence is wrapped in a transaction that
// first takes a Postgres advisory transaction lock keyed on the
// merchant_id. Without it, two workers (or two retries) processing
// different events for the same merchant could both read the same
// prev_chain_hash, compute valid-but-divergent chain_hash values, and
// both insert — producing a fork in what the patent (Application
// 63/991,596, FIG. 4) requires to be a linear per-merchant chain.
//
// Same merchant_id → same hashtext → same lock; concurrent sealers
// for one merchant serialize. Different merchants run fully parallel.
// pg_advisory_xact_lock releases automatically on commit/rollback.
//
// The event_hash UNIQUE constraint still fires inside the transaction,
// so an idempotent retry of an already-sealed event surfaces as
// ErrDuplicateEvent before the commit — the duplicate row is rolled
// back, and chain integrity is unaffected.
func WriteEvidence(ctx context.Context, db DB, evt publisher.Event) (sealedChainHash string, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("sub1: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Per-merchant advisory lock. Released on commit/rollback.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1::text))`,
		evt.MerchantID.String(),
	); err != nil {
		return "", fmt.Errorf("sub1: advisory lock: %w", err)
	}

	prev, err := LookupPrevChainHash(ctx, tx, evt.MerchantID)
	if err != nil {
		return "", err
	}

	// Use the gateway's IngestedAt so the chain timestamp matches the
	// canonical envelope — not the Sub 1 clock at write time.
	ts := evt.IngestedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	chainHash := ComputeChainHash(evt.EventHash, prev, ts)

	const insert = `
		INSERT INTO protocol.evidence
			(event_id, event_hash, chain_hash, prev_chain_hash,
			 source_code, merchant_id, raw_payload, ingested_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8)
	`
	if _, err = tx.Exec(ctx, insert,
		evt.EventID,
		evt.EventHash,
		chainHash,
		prev,
		evt.SourceCode,
		evt.MerchantID,
		[]byte(evt.Payload),
		ts,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return chainHash, ErrDuplicateEvent
		}
		return "", fmt.Errorf("sub1: insert evidence: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("sub1: commit: %w", err)
	}
	return chainHash, nil
}
