// internal/adapters/credentials.go
//
// CredentialStore resolves per-merchant POS API credentials from
// app.bull_api_credentials. Used by Bull (outbound poll) and any adapter
// that needs to make authenticated calls to a source-system API.
//
// Schema note: app.bull_api_credentials currently stores one active
// row per merchant (no source_code column). When a merchant connects a
// second POS source (e.g. both Square and Counterpoint), the schema will
// need a source_code FK added and a UNIQUE(merchant_id, source_code)
// constraint. That extension ships with the multi-source polling feature
// (deferred past Loop 4 Wave D); for now GetActive picks the single
// active row.
//
// Spec: GRO-765 Phase D carry-forward B.2.

package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Credential holds the decrypted-on-caller-side API key and endpoint
// for a merchant's POS integration. The store returns the encrypted
// value verbatim; decryption is the caller's responsibility (Bull uses
// KMS; test seeds store plaintext for integration tests).
type Credential struct {
	MerchantID       uuid.UUID
	APIKeyEncrypted  string
	EndpointURL      string
}

// ErrNoCredential is returned when no active credential row exists for
// the given merchant. Callers should treat this as a configuration gap
// (merchant not yet provisioned) rather than a transient error.
var ErrNoCredential = errors.New("adapters: no active credential for merchant")

// CredentialStore is the read layer over app.bull_api_credentials.
type CredentialStore struct {
	pool *pgxpool.Pool
}

// NewCredentialStore constructs a CredentialStore.
func NewCredentialStore(pool *pgxpool.Pool) *CredentialStore {
	return &CredentialStore{pool: pool}
}

// GetActive returns the single active credential row for the given
// merchant. Returns ErrNoCredential when no active row exists.
func (s *CredentialStore) GetActive(ctx context.Context, merchantID uuid.UUID) (*Credential, error) {
	const q = `
		SELECT merchant_id, api_key_encrypted, endpoint_url
		  FROM app.bull_api_credentials
		 WHERE merchant_id = $1 AND is_active = true
		 LIMIT 1`
	row := s.pool.QueryRow(ctx, q, merchantID)
	var c Credential
	if err := row.Scan(&c.MerchantID, &c.APIKeyEncrypted, &c.EndpointURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoCredential
		}
		return nil, fmt.Errorf("adapters: get credential: %w", err)
	}
	return &c, nil
}

// ListActive returns all active credentials for a set of merchants.
// Used by Bull's batch-poll scheduler when iterating the active-merchant
// roster at poll-cycle start.
func (s *CredentialStore) ListActive(ctx context.Context, merchantIDs []uuid.UUID) ([]Credential, error) {
	if len(merchantIDs) == 0 {
		return nil, nil
	}
	// pgx accepts []uuid.UUID for ANY($1) via pgx's array handling.
	const q = `
		SELECT merchant_id, api_key_encrypted, endpoint_url
		  FROM app.bull_api_credentials
		 WHERE merchant_id = ANY($1) AND is_active = true
		 ORDER BY merchant_id`
	rows, err := s.pool.Query(ctx, q, merchantIDs)
	if err != nil {
		return nil, fmt.Errorf("adapters: list credentials: %w", err)
	}
	defer rows.Close()
	out := make([]Credential, 0, len(merchantIDs))
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.MerchantID, &c.APIKeyEncrypted, &c.EndpointURL); err != nil {
			return nil, fmt.Errorf("adapters: list credentials scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
