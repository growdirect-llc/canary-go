// Package secrets resolves per-source webhook secrets keyed by
// (merchant_id, source_code). Production lookups go through Postgres
// against protocol.source_secrets (migration 015). Tests use the
// in-memory implementation.
//
// SECURITY NOTE: secrets are stored as plaintext in v1. The migration
// to envelope-encrypted secrets via Secrets Manager is tracked in
// GRO-687 and changes the storage path here, not the Resolver
// interface.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Secret is the resolved per-source secret plus its policy.
type Secret struct {
	ID            uuid.UUID
	MerchantID    uuid.UUID
	SourceCode    string
	Secret        []byte
	SignatureAlgo string
	ReplayWindow  time.Duration
}

// ErrNotFound signals no active secret exists for the given key.
// The gateway maps this to 401 (don't reveal whether merchant or
// source_code was the unknown side).
var ErrNotFound = errors.New("secrets: no active secret for merchant+source")

// Resolver looks up a secret by merchant + source_code. Implementations
// must be safe for concurrent use.
type Resolver interface {
	Lookup(ctx context.Context, merchantID uuid.UUID, sourceCode string) (Secret, error)
}

// ---------------------------------------------------------------------------
// Postgres implementation
// ---------------------------------------------------------------------------

// PgxResolver queries protocol.source_secrets via a pgx pool. Single
// query per lookup; expectation is that callers add their own caching
// layer if hot-path lookup pressure warrants it.
type PgxResolver struct {
	pool *pgxpool.Pool
}

// NewPgxResolver wraps an existing pgx pool.
func NewPgxResolver(pool *pgxpool.Pool) *PgxResolver { return &PgxResolver{pool: pool} }

const lookupSQL = `
SELECT id, merchant_id, source_code, secret, signature_algo, replay_window_seconds
FROM protocol.source_secrets
WHERE merchant_id = $1 AND source_code = $2 AND status = 'active'
LIMIT 1
`

// Lookup returns the active secret for (merchantID, sourceCode) or
// ErrNotFound. Postgres returns at most one row — the partial unique
// index uq_protocol_source_secrets_active enforces single-active-secret
// per (merchant, source).
func (r *PgxResolver) Lookup(ctx context.Context, merchantID uuid.UUID, sourceCode string) (Secret, error) {
	var (
		s             Secret
		replaySeconds int
	)
	row := r.pool.QueryRow(ctx, lookupSQL, merchantID, sourceCode)
	if err := row.Scan(&s.ID, &s.MerchantID, &s.SourceCode, &s.Secret, &s.SignatureAlgo, &replaySeconds); err != nil {
		if err.Error() == "no rows in result set" || errors.Is(err, errNoRows) {
			return Secret{}, ErrNotFound
		}
		return Secret{}, fmt.Errorf("secrets: lookup: %w", err)
	}
	s.ReplayWindow = time.Duration(replaySeconds) * time.Second
	return s, nil
}

// errNoRows mirrors pgx.ErrNoRows without forcing the import where
// it's not needed; the Lookup method tolerates either form.
var errNoRows = errors.New("no rows in result set")

// ---------------------------------------------------------------------------
// In-memory implementation (tests + dev)
// ---------------------------------------------------------------------------

// Memory is a thread-safe in-memory Resolver. Useful for unit tests and
// local dev without a Postgres dependency.
type Memory struct {
	mu      sync.RWMutex
	secrets map[string]Secret // key = merchantID|sourceCode
}

// NewMemory returns an empty in-memory resolver.
func NewMemory() *Memory { return &Memory{secrets: make(map[string]Secret)} }

// Add registers a secret. Overwrites any prior entry for the same key.
func (m *Memory) Add(s Secret) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[memKey(s.MerchantID, s.SourceCode)] = s
}

// Lookup returns the configured secret or ErrNotFound.
func (m *Memory) Lookup(_ context.Context, merchantID uuid.UUID, sourceCode string) (Secret, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.secrets[memKey(merchantID, sourceCode)]; ok {
		return s, nil
	}
	return Secret{}, ErrNotFound
}

func memKey(merchantID uuid.UUID, sourceCode string) string {
	return merchantID.String() + "|" + sourceCode
}
