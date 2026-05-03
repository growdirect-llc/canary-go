// Package lnurl implements LNURL-auth (spec §9) for the Canary Protocol.
//
// Flow: QR encodes a k1 challenge → wallet signs with secp256k1 →
// server verifies → JWT session token issued. No passwords.
//
// Spec: https://github.com/lnurl/luds/blob/legacy/lnurl-auth.md
package lnurl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors returned by LNURLStore methods.
var (
	ErrNotFound    = errors.New("lnurl: not found")
	ErrAlreadyUsed = errors.New("lnurl: challenge already used")
	ErrExpired     = errors.New("lnurl: challenge expired")
)

// Challenge is the persistent row for a single QR scan.
type Challenge struct {
	K1        string
	LinkedID  *uuid.UUID
	Status    string
	CreatedAt time.Time
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// LNURLStore is the DB interface for the LNURL-auth package.
// *PgxStore satisfies it; tests supply a stubStore.
type LNURLStore interface {
	InsertChallenge(ctx context.Context, k1 string) error
	GetChallenge(ctx context.Context, k1 string) (*Challenge, error)
	MarkUsed(ctx context.Context, k1 string, ownerID uuid.UUID) error
	UpsertLinkedKey(ctx context.Context, linkingKey string, ownerID uuid.UUID) error
}

// PgxStore wraps a pgxpool with LNURL-auth DB operations.
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewStore constructs a PgxStore backed by the given pool.
func NewStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// InsertChallenge creates a new pending k1 challenge.
// The row expires in 5 minutes (set by column DEFAULT).
func (s *PgxStore) InsertChallenge(ctx context.Context, k1 string) error {
	const q = `
		INSERT INTO app.lnurl_auth_challenges (k1)
		VALUES ($1)
	`
	_, err := s.pool.Exec(ctx, q, k1)
	if err != nil {
		return fmt.Errorf("lnurl store insert_challenge: %w", err)
	}
	return nil
}

// GetChallenge fetches a challenge by its k1 value.
// Returns ErrNotFound if no row matches.
func (s *PgxStore) GetChallenge(ctx context.Context, k1 string) (*Challenge, error) {
	const q = `
		SELECT k1, linked_id, status, created_at, expires_at, updated_at
		FROM app.lnurl_auth_challenges
		WHERE k1 = $1
	`
	row := s.pool.QueryRow(ctx, q, k1)
	c, err := scanChallenge(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lnurl store get_challenge: %w", err)
	}
	return c, nil
}

// MarkUsed atomically sets status='used' for a pending, non-expired challenge
// and records the owner_id. If RowsAffected==0, it fetches the row to
// distinguish ErrNotFound, ErrAlreadyUsed, and ErrExpired.
func (s *PgxStore) MarkUsed(ctx context.Context, k1 string, ownerID uuid.UUID) error {
	const q = `
		UPDATE app.lnurl_auth_challenges
		   SET status = 'used',
		       linked_id = $2,
		       updated_at = now()
		 WHERE k1 = $1
		   AND status = 'pending'
		   AND expires_at > now()
	`
	ct, err := s.pool.Exec(ctx, q, k1, ownerID)
	if err != nil {
		return fmt.Errorf("lnurl store mark_used: %w", err)
	}
	if ct.RowsAffected() > 0 {
		return nil
	}

	// Distinguish failure modes.
	c, err := s.GetChallenge(ctx, k1)
	if err != nil {
		return err // ErrNotFound or DB error
	}
	switch c.Status {
	case "used":
		return ErrAlreadyUsed
	case "expired":
		return ErrExpired
	default:
		// Status is 'pending' but expires_at has passed.
		if time.Now().After(c.ExpiresAt) {
			return ErrExpired
		}
		return ErrAlreadyUsed
	}
}

// UpsertLinkedKey inserts a new linking_key → owner_id mapping, or
// updates last_auth_at if the key already exists.
func (s *PgxStore) UpsertLinkedKey(ctx context.Context, linkingKey string, ownerID uuid.UUID) error {
	const q = `
		INSERT INTO app.lnurl_linked_keys (linking_key, owner_id)
		VALUES ($1, $2)
		ON CONFLICT (linking_key) DO UPDATE
		    SET last_auth_at = now()
	`
	_, err := s.pool.Exec(ctx, q, linkingKey, ownerID)
	if err != nil {
		return fmt.Errorf("lnurl store upsert_linked_key: %w", err)
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

type scannable interface{ Scan(dest ...any) error }

func scanChallenge(row scannable) (*Challenge, error) {
	var c Challenge
	if err := row.Scan(
		&c.K1,
		&c.LinkedID,
		&c.Status,
		&c.CreatedAt,
		&c.ExpiresAt,
		&c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}
