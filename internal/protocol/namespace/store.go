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
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
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
			 network, reg_status, payload_hash, registered_at, expires_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
		reg.Network, reg.RegStatus, reg.PayloadHash, reg.RegisteredAt, reg.ExpiresAt,
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
		       network, reg_status, payload_hash, registered_at, expires_at
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

// GetByOwner returns all registrations for a given (owner_id, owner_type) pair.
func (s *Store) GetByOwner(ctx context.Context, ownerID uuid.UUID, ownerType string) ([]Registration, error) {
	const q = `
		SELECT reg_id, name, owner_id, owner_type, raas_uuid,
		       COALESCE(inscription_id, ''), COALESCE(btc_tx_id, ''),
		       COALESCE(btc_block_height, 0),
		       network, reg_status, payload_hash, registered_at, expires_at
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
// OrdinalsBot webhook confirms the inscription.
func (s *Store) UpdateInscription(ctx context.Context, regID uuid.UUID,
	inscriptionID, btcTxID string, blockHeight int64, status string) error {
	const q = `
		UPDATE protocol.namespace_registrations
		SET inscription_id = $2,
		    btc_tx_id = $3,
		    btc_block_height = $4,
		    reg_status = $5
		WHERE reg_id = $1
	`
	_, err := s.pool.Exec(ctx, q, regID, inscriptionID, btcTxID, blockHeight, status)
	if err != nil {
		return fmt.Errorf("namespace store update_inscription: %w", err)
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
