package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/protocol/sub2"
)

// LookupShim adapts a *Registry into the sub2.AdapterLookup interface
// without sub2 needing to import this package. Construct with NewLookup;
// pass the result into sub2.NewWorker / sub2.NewDispatcher.
type LookupShim struct {
	reg *Registry
}

// NewLookup wraps a Registry for sub2 consumption.
func NewLookup(reg *Registry) *LookupShim { return &LookupShim{reg: reg} }

// Get satisfies sub2.AdapterLookup. The underlying SourceAdapter
// already implements sub2.Parser via its Parse(env) method, so we
// can return it directly.
func (l *LookupShim) Get(sourceCode string) (sub2.Parser, bool) {
	a, ok := l.reg.Get(sourceCode)
	if !ok {
		return nil, false
	}
	return a, true
}

// ErrNoDefaultTenderType is returned when no source-default tender_type
// exists for the given (tenant, source). Sub2 uses this signal to
// decide whether to skip the tender insert (preserving the canonical
// event without a tender row) or escalate.
var ErrNoDefaultTenderType = errors.New("no default tender_type for tenant+source")

// ResolveTenderType returns the tenant's source-default tender_type_id
// for the given source_code, looked up via the partial unique index
// uq_tender_source_default. The seed in deploy/schema/99_seed.sql
// installs one row per (tenant, source) — §B.2.
//
// Loop 3 Wave 1: simple per-call query. Loop 3 Wave 2 will add an
// LRU cache (the lookup is invariant once seeded; cache eviction only
// needed when an admin re-classifies a tender_type, which is rare).
//
// Returns ErrNoDefaultTenderType when no seeded row matches —
// callers should treat this as a non-fatal condition and skip the
// tender insert (preserving the canonical event header + line items
// without a tender row).
func ResolveTenderType(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, sourceCode string) (uuid.UUID, error) {
	const q = `SELECT id FROM finance.tender_types WHERE tenant_id = $1 AND source_code = $2 LIMIT 1`
	var id uuid.UUID
	err := pool.QueryRow(ctx, q, tenantID, sourceCode).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoDefaultTenderType
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("ResolveTenderType: %w", err)
	}
	return id, nil
}
