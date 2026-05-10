//go:build integration

// Integration test for GRO-913 — drives Touch + Flush against a real
// pgxpool.Pool and asserts last_used_at updates land.

package identity_test

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/identity"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	p, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// TestLastUsedRecorder_Flush_WritesRow seeds an api_keys row, touches
// it, flushes, and asserts last_used_at is set on the row.
func TestLastUsedRecorder_Flush_WritesRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	plaintext, keyID, err := identity.CreateAPIKeyRow(
		ctx, pool, nil, "gro913-flush-test", []string{"identity:me"}, 600, nil,
	)
	if err != nil {
		t.Fatalf("CreateAPIKeyRow: %v", err)
	}
	_ = plaintext // unused for this test
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, keyID)
	})

	r := identity.NewLastUsedRecorder(pool, time.Hour) // long interval — Flush manually
	defer r.Close()

	r.Touch(keyID)
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var lastUsedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_used_at FROM app.api_keys WHERE id = $1`, keyID,
	).Scan(&lastUsedAt); err != nil {
		t.Fatalf("query last_used_at: %v", err)
	}
	if lastUsedAt == nil {
		t.Fatalf("last_used_at is NULL after Flush; want non-nil")
	}
	if time.Since(*lastUsedAt) > 5*time.Second {
		t.Errorf("last_used_at too old: %v (now=%v)", lastUsedAt, time.Now())
	}
}

// TestLastUsedRecorder_BackgroundFlush exercises the Start() + ticker
// path. With a 100ms flush interval, a single Touch must propagate
// to the DB within ~250ms.
func TestLastUsedRecorder_BackgroundFlush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := intPool(t)

	_, keyID, err := identity.CreateAPIKeyRow(
		ctx, pool, nil, "gro913-bg-test", []string{"identity:me"}, 600, nil,
	)
	if err != nil {
		t.Fatalf("CreateAPIKeyRow: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, keyID)
	})

	r := identity.NewLastUsedRecorder(pool, 100*time.Millisecond)
	r.Start(ctx)
	defer func() { _ = r.Close() }()

	r.Touch(keyID)

	// Poll up to 1s for last_used_at to populate. Avoids a fixed sleep.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		var lastUsedAt *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT last_used_at FROM app.api_keys WHERE id = $1`, keyID,
		).Scan(&lastUsedAt); err == nil && lastUsedAt != nil {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("last_used_at never populated within 1s — background flush did not run")
}

// TestLastUsedRecorder_GoroutineBound is the GRO-913 acceptance probe:
// 500 sequential Touch calls must NOT spawn 500 goroutines (the prior
// fan-out behavior). We allow up to 10 goroutines of headroom for
// runtime + test machinery.
func TestLastUsedRecorder_GoroutineBound(t *testing.T) {
	pool := intPool(t)

	r := identity.NewLastUsedRecorder(pool, time.Hour) // interval=hour, so no flush during the burst
	defer r.Close()

	baseline := runtime.NumGoroutine()

	var wg sync.WaitGroup
	const burst = 500
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Touch(uuid.New())
		}()
	}
	wg.Wait()
	// Right after the burst — Touch is non-blocking and DOES NOT
	// spawn extra goroutines, so the count should be very close to
	// baseline. The test goroutines themselves are gone by now (we
	// waited on wg).
	delta := runtime.NumGoroutine() - baseline
	if delta > 10 {
		t.Errorf("goroutine fan-out detected: NumGoroutine grew by %d (baseline=%d) after %d Touch calls; want ≤10",
			delta, baseline, burst)
	}
}
