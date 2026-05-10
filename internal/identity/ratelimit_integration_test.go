//go:build integration

// End-to-end integration test for GRO-912. Drives APIKeyMiddleware with
// a real *pgxpool.Pool and a real *redis.Client, seeding a low-cap
// key and proving the per-key throttle returns 429 once the cap is
// crossed within a single minute.

package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/ruptiv/canary/internal/identity"
)

func intDBPool(t *testing.T) *pgxpool.Pool {
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

func intValkey(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		DB:       2,
		Password: "valkey_dev",
	})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skipf("valkey not available: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestAPIKeyMiddleware_PerKeyThrottle_429AtCap is the GRO-912
// acceptance probe. With rate_limit_rpm=2 on a freshly minted key,
// the first 2 requests succeed and the 3rd returns 429 with
// Retry-After.
func TestAPIKeyMiddleware_PerKeyThrottle_429AtCap(t *testing.T) {
	ctx := context.Background()
	pool := intDBPool(t)
	client := intValkey(t)

	// Mint a key with a known low cap.
	plaintext, keyID, err := identity.CreateAPIKeyRow(
		ctx, pool, nil, "gro912-throttle-test", []string{"identity:me"}, 2, nil,
	)
	if err != nil {
		t.Fatalf("CreateAPIKeyRow: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, keyID)
		// Clean Valkey counter so reruns of this test in the same
		// minute don't carry residue.
		minute := time.Now().UTC().Unix() / 60
		_ = client.Del(context.Background(),
			"apikey:rl:"+keyID.String()+":"+itoa(minute)).Err()
	})

	limiter := identity.NewRateLimiter(client, identity.DefaultRateLimitConfig())

	mw := identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
		Pool:     pool,
		Required: true,
		Limiter:  limiter,
	})

	var nextRan int
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextRan++
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(next)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set(identity.HeaderAPIKey, plaintext)
		req.RemoteAddr = "10.99.99.99:443"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// First 2 requests pass.
	for i := 1; i <= 2; i++ {
		rec := send()
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d (body=%s)", i, rec.Code, rec.Body.String())
		}
	}
	// Third request hits the throttle.
	rec := send()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("call 3: expected 429, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Errorf("call 3: missing Retry-After header")
	}
	if nextRan != 2 {
		t.Errorf("downstream handler ran %d times, want 2", nextRan)
	}
}

// TestAPIKeyMiddleware_BruteForceLockout proves that repeated invalid
// keys with the same prefix from the same IP trip the brute-force
// lockout, after which subsequent attempts get 429 immediately
// without spending the argon2id verify cost.
func TestAPIKeyMiddleware_BruteForceLockout(t *testing.T) {
	ctx := context.Background()
	pool := intDBPool(t)
	client := intValkey(t)

	// Seed one valid key so the prefix indexes have at least one row,
	// ensuring the verify path doesn't short-circuit on "no candidate."
	const validPlaintext = "cy_lockout1ABCDEFGHIJKLMNOPQRSTUVWXYZ234567abcdefghijk"
	const fakePlaintext = "cy_lockout1ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ" // same prefix as valid
	hash, err := identity.HashAPIKey(validPlaintext)
	if err != nil {
		t.Fatalf("HashAPIKey: %v", err)
	}
	prefix := validPlaintext[:11] // cy_ + 8 chars
	keyID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.api_keys (id, agent_name, key_hash, key_prefix, scopes, rate_limit_rpm)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		keyID, "gro912-bf-test", hash, prefix, []string{"identity:me"}, 600,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, keyID)
		_ = client.Del(context.Background(),
			"apikey:bf:fail:"+prefix+":10.77.77.77",
			"apikey:bf:lock:"+prefix+":10.77.77.77",
		).Err()
	})

	cfg := identity.RateLimitConfig{
		BruteForceWindow:     30 * time.Second,
		BruteForceThreshold:  3,
		BruteForceLockoutFor: 30 * time.Second,
	}
	limiter := identity.NewRateLimiter(client, cfg)

	mw := identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
		Pool:     pool,
		Required: true,
		Limiter:  limiter,
	})

	send := func(plaintext string) int {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set(identity.HeaderAPIKey, plaintext)
		req.RemoteAddr = "10.77.77.77:443"
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		return rec.Code
	}

	// First N invalid attempts — middleware returns 401 each time and
	// records a failure. The last one trips the lockout.
	for i := 1; i <= cfg.BruteForceThreshold; i++ {
		got := send(fakePlaintext)
		if got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i, got)
		}
	}

	// Subsequent attempts now return 429 immediately, even with the
	// VALID plaintext (because lockout is keyed on prefix+IP, not
	// validity).
	if got := send(validPlaintext); got != http.StatusTooManyRequests {
		t.Errorf("post-lockout valid key: got %d, want 429", got)
	}
}

func itoa(n int64) string {
	// minimal, allocation-free for the small range we care about.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
