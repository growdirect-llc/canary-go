// Package refreshfamily manages the refresh-token family ledger
// per OAuth 2.1 / RFC 6819 §5.2.2.3 — reuse detection with
// family-wide revocation on mismatched jti.
//
// Flow:
//
//   1. /auth/login: Create(ctx, familyID, subject, refreshJTI)
//      — establishes a fresh family on the new refresh token.
//   2. /auth/refresh: ValidateAndRotate(ctx, familyID, presentedJTI,
//      newJTI) — atomic SELECT FOR UPDATE check that the presented
//      jti matches family.last_jti, then updates last_jti to newJTI
//      in the same transaction. If presentedJTI != last_jti the
//      family is revoked and reuse-detection error returns —
//      regardless of which side of the race "won."
//   3. /auth/logout (T-1 follow-on): Revoke(ctx, familyID, reason)
//      explicitly bans the family.
//
// T-1 / GRO-861.
package refreshfamily

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors callers errors.Is against.
var (
	ErrFamilyNotFound = errors.New("refreshfamily: family not found")
	ErrFamilyRevoked  = errors.New("refreshfamily: family revoked")
	// ErrReuseDetected indicates a refresh token whose jti does not
	// match the family's last_jti was presented. Family has been
	// revoked as part of handling this error — no further refreshes
	// against this family will succeed.
	ErrReuseDetected = errors.New("refreshfamily: refresh-token reuse detected; family revoked")
)

// Store is the family-ledger client.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a brand-new family. Called from /auth/login (or
// other initial-mint paths like SSO JIT) right after MintPair.
// jti is the just-minted refresh-token jti.
func (s *Store) Create(ctx context.Context, familyID, subject uuid.UUID, jti string) error {
	const q = `
		INSERT INTO app.refresh_token_families (id, subject, last_jti)
		VALUES ($1, $2, $3)
	`
	_, err := s.pool.Exec(ctx, q, familyID, subject, jti)
	if err != nil {
		return fmt.Errorf("refreshfamily create: %w", err)
	}
	return nil
}

// ValidateAndRotate is the hot path of /auth/refresh. Inside a
// single transaction with SELECT FOR UPDATE:
//
//   1. Lock the family row (or return ErrFamilyNotFound).
//   2. Reject if revoked (ErrFamilyRevoked).
//   3. If presentedJTI != family.last_jti:
//        - Set revoked_at = NOW(), revoked_reason = "reuse"
//        - Return ErrReuseDetected
//   4. Else update last_jti = newJTI, last_used_at = NOW(), commit.
//
// The SELECT FOR UPDATE serializes concurrent refresh attempts on
// the same family — exactly one wins, all others either succeed in
// sequence (if they present the rotated jti, which they wouldn't —
// the rotated jti is freshly minted; nobody else has it) or trigger
// reuse detection.
func (s *Store) ValidateAndRotate(ctx context.Context, familyID uuid.UUID, presentedJTI, newJTI string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("refreshfamily begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lastJTI string
	var revokedAt *string // we don't read the timestamp's value, just whether it's set
	row := tx.QueryRow(ctx, `
		SELECT last_jti, revoked_at::text
		FROM app.refresh_token_families
		WHERE id = $1
		FOR UPDATE
	`, familyID)
	if err := row.Scan(&lastJTI, &revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFamilyNotFound
		}
		return fmt.Errorf("refreshfamily select: %w", err)
	}

	if revokedAt != nil {
		return ErrFamilyRevoked
	}

	if presentedJTI != lastJTI {
		// Reuse detected — revoke + return. Commit so the revoke
		// is durable even though we're returning an error.
		if _, err := tx.Exec(ctx, `
			UPDATE app.refresh_token_families
			SET revoked_at = NOW(), revoked_reason = 'reuse'
			WHERE id = $1
		`, familyID); err != nil {
			return fmt.Errorf("refreshfamily revoke-on-reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("refreshfamily commit revoke: %w", err)
		}
		return ErrReuseDetected
	}

	if _, err := tx.Exec(ctx, `
		UPDATE app.refresh_token_families
		SET last_jti = $2, last_used_at = NOW()
		WHERE id = $1
	`, familyID, newJTI); err != nil {
		return fmt.Errorf("refreshfamily rotate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("refreshfamily commit rotate: %w", err)
	}
	return nil
}

// Revoke explicitly bans a family — logout, admin action, or
// security-incident response. Idempotent (revoking an already-
// revoked family is a no-op). reason is recorded for forensics.
func (s *Store) Revoke(ctx context.Context, familyID uuid.UUID, reason string) error {
	const q = `
		UPDATE app.refresh_token_families
		SET revoked_at = COALESCE(revoked_at, NOW()),
		    revoked_reason = COALESCE(revoked_reason, $2)
		WHERE id = $1
	`
	_, err := s.pool.Exec(ctx, q, familyID, reason)
	if err != nil {
		return fmt.Errorf("refreshfamily revoke: %w", err)
	}
	return nil
}
