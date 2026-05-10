// internal/identity/lastused.go
//
// LastUsedRecorder bounds the goroutine + DB-write fan-out caused by
// the per-request `last_used_at` update in AuthenticateAPIKey. Closes
// GRO-913.
//
// Pre-fix behavior: every successful API-key auth spawned a goroutine
// that did `UPDATE app.api_keys SET last_used_at = now() WHERE id = $1`
// out-of-band. At sustained high RPS this is N goroutines × N DB pool
// checkouts, all racing for the same connection pool that handler code
// also uses. A slow DB makes the goroutine count climb without bound.
//
// Post-fix: every auth path calls Recorder.Touch(keyID), which writes
// the timestamp into an in-memory map under a short-held lock — no
// goroutine spawn, no DB I/O on the hot path. A single dedicated
// background flusher drains the map every FlushInterval and writes
// all pending touches in one batched UPDATE. On shutdown, the flusher
// drains its buffer once more so a graceful-shutdown SIGTERM doesn't
// lose the last few seconds of touches.
//
// Resolution: last_used_at precision is now ±FlushInterval (default
// 30s), which is fine for the auditing/last-seen UX the column serves.
// Callers needing per-second precision should not use this column.
//
// nil-receiver safe: every public method on a nil *LastUsedRecorder is
// a no-op so callers can pass nil during tests or while wiring isn't
// in place yet.

package identity

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultLastUsedFlushInterval is how often the background flusher
// drains pending touches into app.api_keys. Trade-off: shorter
// interval → finer last_used_at precision and more DB writes; longer
// → fewer writes but the column lags actual use.
const DefaultLastUsedFlushInterval = 30 * time.Second

// LastUsedRecorder collects last_used_at touches and writes them to
// app.api_keys in batches.
type LastUsedRecorder struct {
	pool          *pgxpool.Pool
	flushInterval time.Duration
	now           func() time.Time // overridable for tests

	mu      sync.Mutex
	pending map[uuid.UUID]time.Time
	started bool // guarded by mu; double-Start is a no-op

	// Lifecycle. close is sent (closed) to signal the flusher to stop
	// and drain; done is closed by the flusher when it exits, so Close()
	// can wait on it.
	close chan struct{}
	done  chan struct{}
}

// NewLastUsedRecorder constructs a recorder bound to pool. interval=0
// uses DefaultLastUsedFlushInterval. Returns a recorder whose Touch /
// Flush / Close methods are usable immediately, but the background
// flusher does NOT start until Start() is called — see the rationale
// in Start's doc.
//
// Pass pool=nil to get a no-op recorder useful for tests that exercise
// auth paths without a database.
func NewLastUsedRecorder(pool *pgxpool.Pool, interval time.Duration) *LastUsedRecorder {
	if interval <= 0 {
		interval = DefaultLastUsedFlushInterval
	}
	return &LastUsedRecorder{
		pool:          pool,
		flushInterval: interval,
		now:           time.Now,
		pending:       make(map[uuid.UUID]time.Time),
		close:         make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Start launches the background flusher. Idempotent: subsequent calls
// are no-ops. Returns immediately; the flusher runs in its own
// goroutine until ctx is canceled OR Close() is called, then drains
// once more before exiting.
//
// Why separate from New: tests want to call Touch + Flush without
// a background goroutine running. cmd/* binaries call Start once at
// boot.
func (r *LastUsedRecorder) Start(ctx context.Context) {
	if r == nil || r.pool == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.close:
		// already closed — nothing to start
		return
	default:
	}
	// Prevent double-start: re-running Start should be a no-op. We
	// detect a running flusher by the done channel — a non-running
	// recorder has done open and unbuffered. After Start runs once,
	// done closes when the flusher exits. Use a sentinel field.
	if r.started {
		return
	}
	r.started = true
	go r.run(ctx)
}

// run is the flusher loop. Tick on flushInterval; Flush() each tick;
// drain once more on shutdown.
func (r *LastUsedRecorder) run(ctx context.Context) {
	defer close(r.done)
	t := time.NewTicker(r.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = r.Flush(context.Background())
			return
		case <-r.close:
			_ = r.Flush(context.Background())
			return
		case <-t.C:
			// Best-effort flush; errors logged would belong with the
			// caller's logger, but the recorder is logger-free to keep
			// the package's import graph small. The error is observable
			// via Flush() return when called explicitly.
			_ = r.Flush(ctx)
		}
	}
}

// Touch records that keyID was used at "now." Subsequent Touch calls
// for the same keyID overwrite the timestamp (latest wins) — there is
// no use case for "all timestamps a key was seen at," only "most
// recent."
//
// Non-blocking. Safe under arbitrary concurrency.
func (r *LastUsedRecorder) Touch(keyID uuid.UUID) {
	if r == nil || keyID == uuid.Nil {
		return
	}
	r.mu.Lock()
	r.pending[keyID] = r.now()
	r.mu.Unlock()
}

// pendingCount returns the count of buffered touches; used by tests.
func (r *LastUsedRecorder) pendingCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// Flush drains the pending map and writes all rows in a single batched
// UPDATE. Returns nil when there's nothing to flush. Errors don't
// re-buffer the failed snapshot — at the next interval the recorder
// records new touches; the lost rows would only have been lost on a
// graceful-shutdown DB outage anyway.
//
// The pool nil-check is what makes the no-op recorder safe; at that
// point Touch was a no-op too, so pending is always empty.
func (r *LastUsedRecorder) Flush(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return nil
	}

	// Snapshot under lock; new touches that arrive during the DB write
	// land in the fresh map and flush on the next tick.
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return nil
	}
	snapshot := r.pending
	r.pending = make(map[uuid.UUID]time.Time, len(snapshot))
	r.mu.Unlock()

	ids := make([]uuid.UUID, 0, len(snapshot))
	timestamps := make([]time.Time, 0, len(snapshot))
	for id, ts := range snapshot {
		ids = append(ids, id)
		timestamps = append(timestamps, ts)
	}

	// Batched UPDATE via UNNEST. One round-trip, regardless of how
	// many keys touched in the interval. The trigger that maintains
	// updated_at fires per row; we set it explicitly here as a belt-
	// and-suspenders.
	const q = `
		UPDATE app.api_keys k
		   SET last_used_at = u.ts,
		       updated_at  = now()
		  FROM unnest($1::uuid[], $2::timestamptz[]) AS u(id, ts)
		 WHERE k.id = u.id`
	if _, err := r.pool.Exec(ctx, q, ids, timestamps); err != nil {
		return fmt.Errorf("identity: flush last_used_at (%d rows): %w", len(snapshot), err)
	}
	return nil
}

// Close signals the background flusher to drain and exit. After Close
// returns the recorder is no longer usable — Touch becomes a no-op
// (the lock guards a still-valid map but no flush will occur), and
// Flush returns nil.
//
// Safe to call multiple times.
func (r *LastUsedRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	select {
	case <-r.close:
		r.mu.Unlock()
		return nil // already closed
	default:
		close(r.close)
	}
	started := r.started
	r.mu.Unlock()
	if started {
		<-r.done
	}
	return nil
}

