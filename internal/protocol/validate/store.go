// Package validate implements the L402 sat-gated Validation API for the
// Canary Protocol (patent Application 63/991,596). A caller submits an
// event_hash, pays satoshis via an L402 challenge, and receives a
// Merkle proof confirming on-chain Bitcoin anchoring. GRO-752.
package validate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors returned by ValidationStore methods.
var (
	ErrNotFound        = errors.New("validate: not found")
	ErrAlreadyConsumed = errors.New("validate: token already consumed")
	ErrExpired         = errors.New("validate: token expired")
	ErrNotAnchored     = errors.New("validate: event not yet anchored")
)

// VerificationToken is the persistent L402 payment record.
type VerificationToken struct {
	TokenID      uuid.UUID
	EventHash    string
	SatoshiPrice int64
	Status       string
	PreimageHash *string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
}

// AnchorProof carries the Merkle inclusion proof returned to the caller
// after the L402 challenge is satisfied.
type AnchorProof struct {
	EventHash      string
	AnchorID       uuid.UUID
	MerkleRoot     string
	InscriptionID  *string
	BtcTxID        *string
	BtcBlockHeight *int64
	Network        string
	AnchorStatus   string
	LeafIndex      int
	MerkleProof    json.RawMessage // raw jsonb from DB
	AnchoredAt     time.Time
}

// ValidationStore is the interface callers use for all validation DB
// operations. *PgxStore satisfies it; tests can supply a stub.
type ValidationStore interface {
	InsertToken(ctx context.Context, eventHash string, satoshiPrice int64) (*VerificationToken, error)
	GetToken(ctx context.Context, tokenID uuid.UUID) (*VerificationToken, error)
	ConsumeToken(ctx context.Context, tokenID uuid.UUID) error
	GetAnchorProof(ctx context.Context, eventHash string) (*AnchorProof, error)
}

// PgxStore wraps a pgxpool with the validation DB operations.
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore constructs a PgxStore backed by the given pool.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// InsertToken creates a new pending verification token for the given
// event_hash. Returns the newly created token.
func (s *PgxStore) InsertToken(ctx context.Context, eventHash string, satoshiPrice int64) (*VerificationToken, error) {
	const q = `
		INSERT INTO protocol.l402_verification_tokens
			(event_hash, satoshi_price)
		VALUES ($1, $2)
		RETURNING token_id, event_hash, satoshi_price, status,
		          preimage_hash, created_at, expires_at, consumed_at
	`
	row := s.pool.QueryRow(ctx, q, eventHash, satoshiPrice)
	tok, err := scanToken(row)
	if err != nil {
		return nil, fmt.Errorf("validate store insert_token: %w", err)
	}
	return tok, nil
}

// GetToken fetches a verification token by its ID.
// Returns ErrNotFound if no matching row exists.
func (s *PgxStore) GetToken(ctx context.Context, tokenID uuid.UUID) (*VerificationToken, error) {
	const q = `
		SELECT token_id, event_hash, satoshi_price, status,
		       preimage_hash, created_at, expires_at, consumed_at
		FROM protocol.l402_verification_tokens
		WHERE token_id = $1
	`
	row := s.pool.QueryRow(ctx, q, tokenID)
	tok, err := scanToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("validate store get_token: %w", err)
	}
	return tok, nil
}

// ConsumeToken marks a pending token as consumed.
//
// The UPDATE is conditional on status='pending' AND expires_at > now().
// If RowsAffected is 0, it fetches the row to distinguish between
// ErrNotFound, ErrAlreadyConsumed, and ErrExpired.
func (s *PgxStore) ConsumeToken(ctx context.Context, tokenID uuid.UUID) error {
	const q = `
		UPDATE protocol.l402_verification_tokens
		SET status = 'consumed',
		    consumed_at = now()
		WHERE token_id = $1
		  AND status = 'pending'
		  AND expires_at > now()
	`
	ct, err := s.pool.Exec(ctx, q, tokenID)
	if err != nil {
		return fmt.Errorf("validate store consume_token: %w", err)
	}
	if ct.RowsAffected() > 0 {
		return nil
	}

	// Distinguish failure modes.
	tok, err := s.GetToken(ctx, tokenID)
	if err != nil {
		return err // ErrNotFound or DB error
	}
	switch tok.Status {
	case "consumed":
		return ErrAlreadyConsumed
	case "expired":
		return ErrExpired
	default:
		// Expired by time but status not yet flipped.
		if time.Now().After(tok.ExpiresAt) {
			return ErrExpired
		}
		return ErrAlreadyConsumed
	}
}

// GetAnchorProof returns the Merkle inclusion proof for the given
// event_hash.
//
//   - ErrNotFound: event_hash not in protocol.evidence at all.
//   - ErrNotAnchored: event exists in evidence but has no evidence_anchors row.
func (s *PgxStore) GetAnchorProof(ctx context.Context, eventHash string) (*AnchorProof, error) {
	const q = `
		SELECT
			e.event_hash,
			ea.anchor_id,
			a.merkle_root,
			a.inscription_id,
			a.btc_tx_id,
			a.btc_block_height,
			a.network,
			a.anchor_status,
			ea.leaf_index,
			ea.merkle_proof,
			a.anchored_at
		FROM protocol.evidence e
		LEFT JOIN protocol.evidence_anchors ea ON ea.event_hash = e.event_hash
		LEFT JOIN protocol.anchors a ON a.anchor_id = ea.anchor_id
		WHERE e.event_hash = $1
		ORDER BY a.anchored_at DESC
		LIMIT 1
	`
	var (
		proof    AnchorProof
		rawProof []byte
		// All anchor fields are nullable via LEFT JOIN.
		anchorID       *uuid.UUID
		merkleRoot     *string
		network        *string
		anchorStatus   *string
		leafIndex      *int
		anchoredAt     *time.Time
	)
	row := s.pool.QueryRow(ctx, q, eventHash)
	err := row.Scan(
		&proof.EventHash,
		&anchorID,
		&merkleRoot,
		&proof.InscriptionID,
		&proof.BtcTxID,
		&proof.BtcBlockHeight,
		&network,
		&anchorStatus,
		&leafIndex,
		&rawProof,
		&anchoredAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("validate store get_anchor_proof: %w", err)
	}

	// Event exists in evidence but no anchor row joined.
	if anchorID == nil {
		return nil, ErrNotAnchored
	}

	proof.AnchorID = *anchorID
	proof.MerkleRoot = *merkleRoot
	proof.Network = *network
	proof.AnchorStatus = *anchorStatus
	proof.LeafIndex = *leafIndex
	proof.AnchoredAt = *anchoredAt
	proof.MerkleProof = json.RawMessage(rawProof)
	return &proof, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func scanToken(row pgx.Row) (*VerificationToken, error) {
	var tok VerificationToken
	if err := row.Scan(
		&tok.TokenID,
		&tok.EventHash,
		&tok.SatoshiPrice,
		&tok.Status,
		&tok.PreimageHash,
		&tok.CreatedAt,
		&tok.ExpiresAt,
		&tok.ConsumedAt,
	); err != nil {
		return nil, err
	}
	return &tok, nil
}
