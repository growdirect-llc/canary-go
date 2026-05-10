// internal/cmdutil/lastused.go
//
// MustLastUsedRecorder — boot helper used by every cmd/* binary that
// constructs an APIKeyMiddleware. Builds the package-level
// identity.LastUsedRecorder, starts its background flusher, and
// returns a close function the caller is expected to defer.
//
// Centralizing the boilerplate means the per-binary delta for GRO-913
// is two lines:
//
//	closeRecorder := cmdutil.MustLastUsedRecorder(ctx, pool)
//	defer closeRecorder()
//
// The recorder installs into the identity package as a process-level
// singleton (identity.SetLastUsedRecorder), so AuthenticateAPIKey
// records touches without any further plumbing through middleware
// opts. Each binary still constructs its own recorder so the lifecycle
// + DB pool are owned at the binary boundary.

package cmdutil

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/identity"
)

// MustLastUsedRecorder builds + installs the process-level
// last_used_at recorder, starts its background flusher bound to ctx,
// and returns a close function the caller should defer. interval=0
// uses identity.DefaultLastUsedFlushInterval (30s).
//
// Pass pool=nil to install a no-op recorder (Touch becomes a no-op).
// Useful for in-memory test binaries that don't connect to Postgres.
func MustLastUsedRecorder(ctx context.Context, pool *pgxpool.Pool) func() {
	return MustLastUsedRecorderWithInterval(ctx, pool, 0)
}

// MustLastUsedRecorderWithInterval is the explicit-interval variant.
// Lower intervals make last_used_at more current at the cost of more
// DB writes. Most binaries should use the default; only call this if
// the precision/cost trade-off needs tuning.
func MustLastUsedRecorderWithInterval(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) func() {
	r := identity.NewLastUsedRecorder(pool, interval)
	identity.SetLastUsedRecorder(r)
	r.Start(ctx)
	return func() {
		_ = r.Close()
		identity.SetLastUsedRecorder(nil)
	}
}
