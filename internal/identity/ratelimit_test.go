package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// valkeyTestClient connects to the dev Valkey on DB 2. Skip when not
// reachable so the suite stays runnable on a stripped-down dev box.
func valkeyTestClient(t *testing.T) *redis.Client {
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

// uniqueIP returns an IP suffix unique to t.Name() so each subtest
// has its own keyspace and parallel runs don't collide.
func uniqueIP(t *testing.T) string {
	t.Helper()
	return "10.0.0." + strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == '\\' {
			return '_'
		}
		return r
	}, t.Name())
}

func TestRateLimiter_NilSafe(t *testing.T) {
	// Every method on a nil *RateLimiter is a no-op.
	var l *RateLimiter

	if got, _ := l.IsLockedOut(context.Background(), "p", "i"); got.Locked {
		t.Errorf("nil receiver IsLockedOut should not lock")
	}
	if locked, _ := l.RecordFailure(context.Background(), "p", "i"); locked {
		t.Errorf("nil receiver RecordFailure should not lock")
	}
	if err := l.ClearFailures(context.Background(), "p", "i"); err != nil {
		t.Errorf("nil receiver ClearFailures should be a no-op, got %v", err)
	}
	if got, _ := l.AllowSuccess(context.Background(), uuid.New(), 100); !got.Allowed {
		t.Errorf("nil receiver AllowSuccess should allow")
	}
}

func TestRateLimiter_RecordFailure_LocksAtThreshold(t *testing.T) {
	client := valkeyTestClient(t)
	prefix := "cy_test01"
	ip := uniqueIP(t)
	cfg := RateLimitConfig{
		BruteForceWindow:     30 * time.Second,
		BruteForceThreshold:  3,
		BruteForceLockoutFor: 30 * time.Second,
	}
	l := NewRateLimiter(client, cfg)

	// Clean slate.
	_ = l.ClearFailures(context.Background(), prefix, ip)

	for i := 1; i < cfg.BruteForceThreshold; i++ {
		lockedNow, err := l.RecordFailure(context.Background(), prefix, ip)
		if err != nil {
			t.Fatalf("RecordFailure[%d]: %v", i, err)
		}
		if lockedNow {
			t.Fatalf("locked too early: i=%d, threshold=%d", i, cfg.BruteForceThreshold)
		}
		if got, _ := l.IsLockedOut(context.Background(), prefix, ip); got.Locked {
			t.Fatalf("IsLockedOut true before threshold: i=%d", i)
		}
	}

	// Threshold-th failure trips the lockout.
	lockedNow, err := l.RecordFailure(context.Background(), prefix, ip)
	if err != nil {
		t.Fatalf("RecordFailure[threshold]: %v", err)
	}
	if !lockedNow {
		t.Fatalf("expected lockedNow=true at threshold")
	}
	got, err := l.IsLockedOut(context.Background(), prefix, ip)
	if err != nil {
		t.Fatalf("IsLockedOut: %v", err)
	}
	if !got.Locked {
		t.Errorf("IsLockedOut should be true after threshold")
	}
	if got.RetryAfter <= 0 || got.RetryAfter > cfg.BruteForceLockoutFor+time.Second {
		t.Errorf("RetryAfter out of range: got %v, want (0, %v]", got.RetryAfter, cfg.BruteForceLockoutFor)
	}

	// Cleanup.
	_ = l.ClearFailures(context.Background(), prefix, ip)
}

func TestRateLimiter_ClearFailures_ResetsCounterAndLockout(t *testing.T) {
	client := valkeyTestClient(t)
	prefix := "cy_test02"
	ip := uniqueIP(t)
	cfg := RateLimitConfig{
		BruteForceWindow:     30 * time.Second,
		BruteForceThreshold:  2,
		BruteForceLockoutFor: 30 * time.Second,
	}
	l := NewRateLimiter(client, cfg)

	// Trip the lockout.
	for i := 0; i < cfg.BruteForceThreshold; i++ {
		_, _ = l.RecordFailure(context.Background(), prefix, ip)
	}
	if got, _ := l.IsLockedOut(context.Background(), prefix, ip); !got.Locked {
		t.Fatalf("setup: expected locked, got not")
	}

	if err := l.ClearFailures(context.Background(), prefix, ip); err != nil {
		t.Fatalf("ClearFailures: %v", err)
	}
	if got, _ := l.IsLockedOut(context.Background(), prefix, ip); got.Locked {
		t.Errorf("expected unlocked after ClearFailures, still locked")
	}
}

func TestRateLimiter_AllowSuccess_BelowLimit(t *testing.T) {
	client := valkeyTestClient(t)
	keyID := uuid.New()
	rpm := 5

	l := NewRateLimiter(client, DefaultRateLimitConfig())
	defer func() {
		_ = client.Del(context.Background(),
			perKeyCounterKey(keyID, time.Now())).Err()
	}()

	for i := 1; i <= rpm; i++ {
		st, err := l.AllowSuccess(context.Background(), keyID, rpm)
		if err != nil {
			t.Fatalf("AllowSuccess[%d]: %v", i, err)
		}
		if !st.Allowed {
			t.Errorf("call %d: expected allowed, got denied (limit=%d, remaining=%d)",
				i, st.Limit, st.Remaining)
		}
	}
}

func TestRateLimiter_AllowSuccess_DeniesOverLimit(t *testing.T) {
	client := valkeyTestClient(t)
	keyID := uuid.New()
	rpm := 3

	l := NewRateLimiter(client, DefaultRateLimitConfig())
	defer func() {
		_ = client.Del(context.Background(),
			perKeyCounterKey(keyID, time.Now())).Err()
	}()

	// Burn through the limit.
	for i := 0; i < rpm; i++ {
		st, err := l.AllowSuccess(context.Background(), keyID, rpm)
		if err != nil || !st.Allowed {
			t.Fatalf("warmup call %d should be allowed; err=%v allowed=%v", i, err, st.Allowed)
		}
	}

	// Next call must be denied.
	st, err := l.AllowSuccess(context.Background(), keyID, rpm)
	if err != nil {
		t.Fatalf("AllowSuccess (over limit): %v", err)
	}
	if st.Allowed {
		t.Errorf("expected denied at rpm+1; got allowed (remaining=%d)", st.Remaining)
	}
	if st.RetryAfter <= 0 || st.RetryAfter > time.Minute+time.Second {
		t.Errorf("RetryAfter out of range: %v", st.RetryAfter)
	}
}

func TestRateLimiter_AllowSuccess_ZeroRPM_Unlimited(t *testing.T) {
	// rate_limit_rpm = 0 ⇒ unlimited (the schema default for keys
	// minted without a cap). Should always allow.
	client := valkeyTestClient(t)
	keyID := uuid.New()
	l := NewRateLimiter(client, DefaultRateLimitConfig())

	for i := 0; i < 50; i++ {
		st, err := l.AllowSuccess(context.Background(), keyID, 0)
		if err != nil {
			t.Fatalf("AllowSuccess[%d]: %v", i, err)
		}
		if !st.Allowed {
			t.Errorf("rpm=0 should be unlimited; got denied at i=%d", i)
		}
	}
}

func TestSourceIP_StripsPort(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"10.0.0.5:54321", "10.0.0.5"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"10.0.0.5", "10.0.0.5"}, // bare IP, no port
		{"", ""},
		{"not-a-host", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.raw
		if got := SourceIP(req); got != c.want {
			t.Errorf("SourceIP(%q): got %q, want %q", c.raw, got, c.want)
		}
	}
}

// ─── GRO-954 LoginRateLimiter tests ───────────────────────────────────

// TestLoginRateLimiter_AccountLockoutAtThreshold drives the per-account
// counter up to the threshold and asserts:
//
//   - Check returns Locked=false until the threshold is crossed.
//   - The threshold-crossing call returns AccountLockedNow=true exactly
//     once (idempotent — subsequent failures don't re-trigger the log).
//   - Check then returns Locked=true with a positive RetryAfter ≤ the
//     configured PerAccountLockoutFor.
//
// This is the GRO-954 integration probe — drives the real Valkey
// pipeline, not just the handler stubs.
func TestLoginRateLimiter_AccountLockoutAtThreshold(t *testing.T) {
	c := valkeyTestClient(t)
	l := NewLoginRateLimiter(c, LoginLockoutConfig{
		PerAccountWindow:     1 * time.Minute,
		PerAccountThreshold:  3,
		PerAccountLockoutFor: 30 * time.Second,
		PerIPWindow:          1 * time.Minute,
		PerIPThreshold:       100, // high so account threshold trips first
		PerIPLockoutFor:      30 * time.Second,
	})

	email := "lockout-" + t.Name() + "@example.test"
	ip := uniqueIP(t)
	ctx := context.Background()

	// Clean residue from prior runs.
	_ = c.Del(ctx,
		loginAccountFailKey(normalizeEmail(email)),
		loginAccountLockoutKey(normalizeEmail(email)),
		loginIPFailKey(ip),
		loginIPLockoutKey(ip),
	).Err()
	t.Cleanup(func() {
		_ = c.Del(context.Background(),
			loginAccountFailKey(normalizeEmail(email)),
			loginAccountLockoutKey(normalizeEmail(email)),
			loginIPFailKey(ip),
			loginIPLockoutKey(ip),
		).Err()
	})

	// Before any failures: not locked.
	st, err := l.Check(ctx, email, ip)
	if err != nil {
		t.Fatalf("initial Check: %v", err)
	}
	if st.Locked {
		t.Fatalf("initial state should be unlocked")
	}

	// Drive failures up to the threshold. Only the threshold-crossing
	// call should return AccountLockedNow=true.
	var seenLockedNow int
	for i := 1; i <= 3; i++ {
		res, err := l.RecordFailure(ctx, email, ip)
		if err != nil {
			t.Fatalf("RecordFailure[%d]: %v", i, err)
		}
		if res.AccountLockedNow {
			seenLockedNow++
		}
		if res.IPLockedNow {
			t.Errorf("IPLockedNow at i=%d (per-IP threshold=100; should not trip)", i)
		}
	}
	if seenLockedNow != 1 {
		t.Errorf("AccountLockedNow returned true %d times, want exactly 1 (idempotent)", seenLockedNow)
	}

	// Now locked.
	st, err = l.Check(ctx, email, ip)
	if err != nil {
		t.Fatalf("post-threshold Check: %v", err)
	}
	if !st.Locked {
		t.Fatalf("Check should report Locked after threshold crossed")
	}
	if st.RetryAfter <= 0 || st.RetryAfter > 30*time.Second {
		t.Errorf("RetryAfter: got %v, want (0, 30s]", st.RetryAfter)
	}

	// Subsequent failures don't re-trigger AccountLockedNow.
	res, err := l.RecordFailure(ctx, email, ip)
	if err != nil {
		t.Fatalf("post-lockout RecordFailure: %v", err)
	}
	if res.AccountLockedNow {
		t.Errorf("AccountLockedNow returned true on already-locked bucket (not idempotent)")
	}
}

// TestLoginRateLimiter_ClearResetsCounter verifies a successful login's
// Clear() removes the per-account bucket so subsequent failures start
// from zero.
func TestLoginRateLimiter_ClearResetsCounter(t *testing.T) {
	c := valkeyTestClient(t)
	l := NewLoginRateLimiter(c, LoginLockoutConfig{
		PerAccountWindow:     1 * time.Minute,
		PerAccountThreshold:  3,
		PerAccountLockoutFor: 30 * time.Second,
	})

	email := "clear-" + t.Name() + "@example.test"
	ip := uniqueIP(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_ = c.Del(context.Background(),
			loginAccountFailKey(normalizeEmail(email)),
			loginAccountLockoutKey(normalizeEmail(email)),
			loginIPFailKey(ip),
			loginIPLockoutKey(ip),
		).Err()
	})

	// Two failures (below threshold).
	for i := 0; i < 2; i++ {
		if _, err := l.RecordFailure(ctx, email, ip); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}

	// Clear (simulates successful login).
	if err := l.Clear(ctx, email); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	// Two more failures should NOT trigger the lockout — the counter
	// reset, so we're back to N=2 of 3.
	for i := 0; i < 2; i++ {
		res, err := l.RecordFailure(ctx, email, ip)
		if err != nil {
			t.Fatalf("post-clear RecordFailure[%d]: %v", i, err)
		}
		if res.AccountLockedNow {
			t.Errorf("lockout fired at i=%d after Clear (should require 3 fresh failures)", i)
		}
	}
}

// TestLoginRateLimiter_NormalizeEmail verifies case-only and
// whitespace-only variants share the same bucket. Without this, an
// attacker could rotate "Foo@x.com" / "FOO@x.com" / " foo@x.com " to
// reset the counter on every attempt.
func TestLoginRateLimiter_NormalizeEmail(t *testing.T) {
	c := valkeyTestClient(t)
	l := NewLoginRateLimiter(c, LoginLockoutConfig{
		PerAccountWindow:     1 * time.Minute,
		PerAccountThreshold:  3,
		PerAccountLockoutFor: 30 * time.Second,
	})
	ctx := context.Background()
	base := "norm-" + t.Name() + "@example.test"
	normalized := normalizeEmail(base)
	// Pre-clear so a re-run within the lockout TTL still starts fresh.
	// Cleanup runs only on exit; previous runs' lockout keys would
	// otherwise make SetNX a no-op for the threshold-crossing call.
	_ = c.Del(ctx,
		loginAccountFailKey(normalized),
		loginAccountLockoutKey(normalized),
	).Err()
	t.Cleanup(func() {
		_ = c.Del(context.Background(),
			loginAccountFailKey(normalized),
			loginAccountLockoutKey(normalized),
		).Err()
	})

	variants := []string{
		base,
		strings.ToUpper(base),
		"   " + base + "   ",
	}
	var lockedNow bool
	for i, v := range variants {
		res, err := l.RecordFailure(ctx, v, "")
		if err != nil {
			t.Fatalf("RecordFailure[%d]: %v", i, err)
		}
		if res.AccountLockedNow {
			lockedNow = true
		}
	}
	if !lockedNow {
		t.Errorf("3 case/whitespace-variant failures should share a bucket and lock the account")
	}
}
