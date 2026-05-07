package namespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by UpdateInscription when no row matches regID.
var ErrNotFound = errors.New("namespace: not found")

// Registration is the persistent record for a .jeffe namespace claim.
type Registration struct {
	RegID          uuid.UUID  `json:"reg_id"`
	Name           string     `json:"name"`
	OwnerID        uuid.UUID  `json:"owner_id"`
	OwnerType      string     `json:"owner_type"`
	RaaSUUID       uuid.UUID  `json:"raas_uuid"`
	InscriptionID  string     `json:"inscription_id,omitempty"`
	BtcTxID        string     `json:"btc_tx_id,omitempty"`
	BtcBlockHeight int64      `json:"btc_block_height,omitempty"`
	Network        string     `json:"network"`
	RegStatus      string     `json:"reg_status"`
	PayloadHash    string     `json:"payload_hash"`
	RegisteredAt   time.Time  `json:"registered_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// NamespaceStore is the interface callers use for all namespace DB operations.
// *Store satisfies it; tests can supply a stub.
type NamespaceStore interface {
	inserter // Insert(ctx, Registration) error
	GetByName(ctx context.Context, name string) (*Registration, error)
	GetByOwner(ctx context.Context, ownerID uuid.UUID, ownerType string) ([]Registration, error)
	UpdateInscription(ctx context.Context, regID uuid.UUID, inscriptionID, btcTxID string, blockHeight int64, status string) error
}

// Store wraps a pgxpool with the namespace DB operations.
// The zero value is not usable — construct via NewStore.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Insert persists a Registration. Returns ErrNameTaken if the name
// already exists (ON CONFLICT on the unique name index).
func (s *Store) Insert(ctx context.Context, reg Registration) error {
	const q = `
		INSERT INTO protocol.namespace_registrations
			(reg_id, name, owner_id, owner_type, raas_uuid,
			 inscription_id, btc_tx_id, btc_block_height,
			 network, reg_status, payload_hash, registered_at, updated_at, expires_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (name) DO NOTHING
	`
	nullableInscription := nullText(reg.InscriptionID)
	nullableTxID := nullText(reg.BtcTxID)
	var nullableBlock *int64
	if reg.BtcBlockHeight != 0 {
		h := reg.BtcBlockHeight
		nullableBlock = &h
	}

	ct, err := s.pool.Exec(ctx, q,
		reg.RegID, reg.Name, reg.OwnerID, reg.OwnerType, reg.RaaSUUID,
		nullableInscription, nullableTxID, nullableBlock,
		reg.Network, reg.RegStatus, reg.PayloadHash, reg.RegisteredAt, reg.UpdatedAt, reg.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("namespace store insert: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNameTaken
	}
	return nil
}

// GetByName retrieves a registration by its .jeffe name.
// Returns pgx.ErrNoRows (wrapped) if not found.
func (s *Store) GetByName(ctx context.Context, name string) (*Registration, error) {
	const q = `
		SELECT reg_id, name, owner_id, owner_type, raas_uuid,
		       COALESCE(inscription_id, ''), COALESCE(btc_tx_id, ''),
		       COALESCE(btc_block_height, 0),
		       network, reg_status, payload_hash, registered_at, updated_at, expires_at
		FROM protocol.namespace_registrations
		WHERE name = $1
	`
	row := s.pool.QueryRow(ctx, q, name)
	reg, err := scanRegistration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("namespace store get_by_name: %w", err)
	}
	return reg, nil
}

// ListRecent returns the most recently registered .jeffe namespace claims
// across all owners. Used by the protocol portal overview (W7).
// Cross-tenant — operators with portal access see all recent registrations.
// Ordered by registered_at DESC; default limit 50, max 200.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]Registration, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT reg_id, name, owner_id, owner_type, raas_uuid,
		       COALESCE(inscription_id, ''), COALESCE(btc_tx_id, ''),
		       COALESCE(btc_block_height, 0),
		       network, reg_status, payload_hash, registered_at, updated_at, expires_at
		FROM protocol.namespace_registrations
		ORDER BY registered_at DESC
		LIMIT $1
	`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("namespace store list_recent: %w", err)
	}
	defer rows.Close()
	out := make([]Registration, 0, limit)
	for rows.Next() {
		reg, err := scanRegistration(rows)
		if err != nil {
			return nil, fmt.Errorf("namespace store list_recent scan: %w", err)
		}
		out = append(out, *reg)
	}
	return out, rows.Err()
}

// GetByOwner returns all registrations for a given (owner_id, owner_type) pair.
func (s *Store) GetByOwner(ctx context.Context, ownerID uuid.UUID, ownerType string) ([]Registration, error) {
	const q = `
		SELECT reg_id, name, owner_id, owner_type, raas_uuid,
		       COALESCE(inscription_id, ''), COALESCE(btc_tx_id, ''),
		       COALESCE(btc_block_height, 0),
		       network, reg_status, payload_hash, registered_at, updated_at, expires_at
		FROM protocol.namespace_registrations
		WHERE owner_id = $1 AND owner_type = $2
		ORDER BY registered_at DESC
	`
	rows, err := s.pool.Query(ctx, q, ownerID, ownerType)
	if err != nil {
		return nil, fmt.Errorf("namespace store get_by_owner: %w", err)
	}
	defer rows.Close()

	var out []Registration
	for rows.Next() {
		reg, err := scanRegistration(rows)
		if err != nil {
			return nil, fmt.Errorf("namespace store scan: %w", err)
		}
		out = append(out, *reg)
	}
	return out, rows.Err()
}

// UpdateInscription writes confirmed on-chain data back after the
// OrdinalsBot webhook confirms the inscription. Returns ErrNotFound if
// no row matches regID.
func (s *Store) UpdateInscription(ctx context.Context, regID uuid.UUID,
	inscriptionID, btcTxID string, blockHeight int64, status string) error {
	const q = `
		UPDATE protocol.namespace_registrations
		SET inscription_id = $2,
		    btc_tx_id = $3,
		    btc_block_height = $4,
		    reg_status = $5,
		    updated_at = now()
		WHERE reg_id = $1
	`
	ct, err := s.pool.Exec(ctx, q, regID, inscriptionID, btcTxID, blockHeight, status)
	if err != nil {
		return fmt.Errorf("namespace store update_inscription: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// scanner is satisfied by both pgx.Row and pgx.Rows so scanRegistration
// can be used by both QueryRow and Query.
type scanner interface {
	Scan(dest ...any) error
}

func scanRegistration(row scanner) (*Registration, error) {
	var (
		reg       Registration
		expiresAt *time.Time
	)
	if err := row.Scan(
		&reg.RegID, &reg.Name, &reg.OwnerID, &reg.OwnerType, &reg.RaaSUUID,
		&reg.InscriptionID, &reg.BtcTxID, &reg.BtcBlockHeight,
		&reg.Network, &reg.RegStatus, &reg.PayloadHash, &reg.RegisteredAt,
		&reg.UpdatedAt,
		&expiresAt,
	); err != nil {
		return nil, err
	}
	reg.ExpiresAt = expiresAt
	return &reg, nil
}

// nullText returns nil if s is empty, otherwise &s. pgx uses *string
// for nullable text columns to avoid mis-storing empty-string for NULL.
func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
