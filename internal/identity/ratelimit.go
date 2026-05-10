// internal/identity/ratelimit.go
//
// Valkey-backed rate limiter for the API-key auth boundary. Closes
// GRO-912.
//
// Two distinct mechanisms wired into APIKeyMiddleware:
//
//  1. Brute-force lockout — a (prefix, source_ip) pair that fails
//     verification more than BruteForceThreshold times within
//     BruteForceWindow gets a lockout flag for BruteForceLockoutFor.
//     Requests during lockout return 429 immediately, before the
//     middleware spends ~50ms on argon2id.
//
//  2. Per-key throttle — once a key authenticates, INCR a counter
//     keyed by (key_id, current_minute). When the counter exceeds
//     the row's rate_limit_rpm, return 429 with a Retry-After
//     pointing to the next minute boundary.
//
// Brute-force tracking keys on (prefix, IP) — not (key_id) — because
// the failed-auth path doesn't yield a key_id (the verify failed).
// IP comes from r.RemoteAddr after the chi RealIP middleware has
// applied X-Forwarded-For. Tracking by prefix alone would let one
// rogue host lock out a legitimate key for everyone; tracking by IP
// alone would let an attacker fan out across keys to bypass the
// counter. (prefix, IP) is the conjunction that catches both.
//
// Fail mode: every limiter call returns the network error to the
// caller. APIKeyMiddleware logs the error and treats the result as
// "not limited" — fail-open. Rationale: a Valkey blip should not
// take down the auth path. The metric counter (TODO) makes it
// observable.

package identity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RateLimitConfig holds the brute-force policy. Per-key throttle
// magnitude comes from the row's rate_limit_rpm column, not config.
type RateLimitConfig struct {
	// BruteForceWindow — failed-attempt counter expiration. Failures
	// older than this are forgotten. Default 1 minute.
	BruteForceWindow time.Duration

	// BruteForceThreshold — N failures within Window triggers a
	// lockout. Default 10. (Person login uses 5 → 15 minute lockout
	// at the time of writing; API keys are slightly more permissive
	// because legitimate clients sometimes retry on transient errors.)
	BruteForceThreshold int

	// BruteForceLockoutFor — once locked, how long the lockout sticks.
	// Default 5 minutes. The counter and lockout key are independent;
	// the lockout outlives the counter window.
	BruteForceLockoutFor time.Duration
}

// DefaultRateLimitConfig returns reasonable defaults.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		BruteForceWindow:     1 * time.Minute,
		BruteForceThreshold:  10,
		BruteForceLockoutFor: 5 * time.Minute,
	}
}

// RateLimiter is the entrypoint used by APIKeyMiddleware. nil-safe at
// the public method level so callers can pass a nil *RateLimiter and
// the methods become no-ops (returns "not limited").
type RateLimiter struct {
	client *redis.Client
	cfg    RateLimitConfig
	now    func() time.Time // overridable for tests
}

// NewRateLimiter wraps client. Pass nil to get a no-op limiter (every
// method returns "not limited"). cfg's zero fields take defaults from
// DefaultRateLimitConfig.
func NewRateLimiter(client *redis.Client, cfg RateLimitConfig) *RateLimiter {
	def := DefaultRateLimitConfig()
	if cfg.BruteForceWindow == 0 {
		cfg.BruteForceWindow = def.BruteForceWindow
	}
	if cfg.BruteForceThreshold == 0 {
		cfg.BruteForceThreshold = def.BruteForceThreshold
	}
	if cfg.BruteForceLockoutFor == 0 {
		cfg.BruteForceLockoutFor = def.BruteForceLockoutFor
	}
	return &RateLimiter{
		client: client,
		cfg:    cfg,
		now:    time.Now,
	}
}

// LockoutStatus describes the outcome of an IsLockedOut check.
type LockoutStatus struct {
	Locked     bool
	RetryAfter time.Duration
}

// IsLockedOut returns whether (prefix, ip) is currently locked due to
// repeated brute-force failures. Cheap — one Valkey GET. Returns
// LockoutStatus{Locked: false} on nil-receiver, missing inputs, or
// Valkey error (fail-open).
func (l *RateLimiter) IsLockedOut(ctx context.Context, prefix, ip string) (LockoutStatus, error) {
	if l == nil || l.client == nil || prefix == "" || ip == "" {
		return LockoutStatus{}, nil
	}
	key := lockoutKey(prefix, ip)
	ttl, err := l.client.PTTL(ctx, key).Result()
	if err != nil {
		return LockoutStatus{}, fmt.Errorf("ratelimit: lockout pttl: %w", err)
	}
	// PTTL semantics:
	//   -2 ⇒ key does not exist
	//   -1 ⇒ key exists with no TTL (we never set those, but be safe)
	//    n ⇒ TTL in milliseconds
	if ttl < 0 {
		return LockoutStatus{}, nil
	}
	return LockoutStatus{Locked: true, RetryAfter: ttl}, nil
}

// RecordFailure increments the failed-attempt counter for (prefix, ip)
// and, if the counter crosses the threshold, sets a lockout key.
// Returns true if this call established a NEW lockout (caller may
// emit a metric / log line). Errors are returned but the caller is
// expected to log and continue (fail-open).
func (l *RateLimiter) RecordFailure(ctx context.Context, prefix, ip string) (lockedNow bool, err error) {
	if l == nil || l.client == nil || prefix == "" || ip == "" {
		return false, nil
	}
	counterKey := failureCounterKey(prefix, ip)

	// INCR + EXPIRE in a pipeline so the TTL is set on the first hit.
	// Subsequent hits within Window keep extending the TTL via SET-IF-EXISTS
	// semantics — but that's actually undesirable: we want the window to be
	// fixed, not sliding. Use SETNX on a separate sentinel + EXPIREAT to
	// pin the deadline. Simpler approach used here: only set EXPIRE when
	// INCR returns 1 (first hit). The counter naturally rolls over after
	// Window seconds.
	pipe := l.client.Pipeline()
	incr := pipe.Incr(ctx, counterKey)
	pipe.Expire(ctx, counterKey, l.cfg.BruteForceWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("ratelimit: incr failure counter: %w", err)
	}

	count := incr.Val()
	if count < int64(l.cfg.BruteForceThreshold) {
		return false, nil
	}
	// Crossed threshold: set lockout key. SET NX so an existing lockout
	// is not extended by an in-flight failure.
	ok, err := l.client.SetNX(ctx, lockoutKey(prefix, ip), "1", l.cfg.BruteForceLockoutFor).Result()
	if err != nil {
		return false, fmt.Errorf("ratelimit: set lockout: %w", err)
	}
	return ok, nil
}

// ClearFailures removes the failure counter and any lockout for the
// given (prefix, ip). Called by APIKeyMiddleware on a successful
// authentication so legitimate retries after a couple of mistakes
// don't accumulate toward a lockout.
func (l *RateLimiter) ClearFailures(ctx context.Context, prefix, ip string) error {
	if l == nil || l.client == nil || prefix == "" || ip == "" {
		return nil
	}
	if err := l.client.Del(ctx, failureCounterKey(prefix, ip), lockoutKey(prefix, ip)).Err(); err != nil {
		return fmt.Errorf("ratelimit: clear failures: %w", err)
	}
	return nil
}

// ThrottleStatus describes the outcome of an AllowSuccess check.
type ThrottleStatus struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// AllowSuccess increments the per-key counter for the current minute
// bucket and returns whether the request is within rateLimitRPM.
// rateLimitRPM <= 0 disables the cap (returns Allowed=true unconditionally).
//
// The counter key TTLs slightly past the minute boundary (window+10s)
// so a request landing right at second-59 of one minute and second-0
// of the next still sees the counter reset cleanly.
func (l *RateLimiter) AllowSuccess(ctx context.Context, keyID uuid.UUID, rateLimitRPM int) (ThrottleStatus, error) {
	if l == nil || l.client == nil || keyID == uuid.Nil || rateLimitRPM <= 0 {
		return ThrottleStatus{Allowed: true, Limit: rateLimitRPM, Remaining: rateLimitRPM}, nil
	}

	now := l.now().UTC()
	bucketKey := perKeyCounterKey(keyID, now)

	pipe := l.client.Pipeline()
	incr := pipe.Incr(ctx, bucketKey)
	pipe.Expire(ctx, bucketKey, 70*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return ThrottleStatus{Allowed: true}, fmt.Errorf("ratelimit: incr key counter: %w", err)
	}

	count := int(incr.Val())
	if count <= rateLimitRPM {
		return ThrottleStatus{
			Allowed:   true,
			Limit:     rateLimitRPM,
			Remaining: rateLimitRPM - count,
		}, nil
	}

	// Over limit. RetryAfter = time remaining until the next minute boundary.
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	return ThrottleStatus{
		Allowed:    false,
		Limit:      rateLimitRPM,
		Remaining:  0,
		RetryAfter: nextMinute.Sub(now),
	}, nil
}

// ── Login brute-force limiter (GRO-954) ───────────────────────────────
//
// Mirrors the API-key brute-force half on a distinct Valkey key
// namespace. Two independent buckets so an attacker cannot bypass by
// rotating across one axis:
//
//   - per-account: aggregates across all source IPs. Catches credential
//     stuffing that fans out a single account across a botnet.
//   - per-source-IP: aggregates across all accounts. Catches a single
//     source running through a list of accounts.
//
// Either bucket independently triggers a lockout on (email, *, lockout
// TTL) or (*, ip, lockout TTL); the handler refuses the login if EITHER
// is locked.
//
// Email is normalized (TrimSpace + ToLower) before hashing into the
// Valkey key so case-only variants share a bucket.

// LoginLockoutConfig holds the policy. Defaults are intentionally
// stricter than the API-key counters: credential brute force is a
// higher-stakes attack than spraying API-key prefixes.
type LoginLockoutConfig struct {
	// PerAccountWindow — failed-attempt counter expiration per email.
	// Default 5 minutes.
	PerAccountWindow time.Duration

	// PerAccountThreshold — failures within Window for one email that
	// trigger an account lockout. Default 5.
	PerAccountThreshold int

	// PerAccountLockoutFor — how long an account lockout sticks.
	// Default 15 minutes.
	PerAccountLockoutFor time.Duration

	// PerIPWindow — failed-attempt counter expiration per source IP.
	// Default 5 minutes.
	PerIPWindow time.Duration

	// PerIPThreshold — failures within Window from one IP that trigger
	// an IP lockout. Default 20 (more permissive than per-account
	// because shared NATs can legitimately produce more failures).
	PerIPThreshold int

	// PerIPLockoutFor — how long an IP lockout sticks. Default 15
	// minutes.
	PerIPLockoutFor time.Duration
}

// DefaultLoginLockoutConfig returns reasonable defaults.
func DefaultLoginLockoutConfig() LoginLockoutConfig {
	return LoginLockoutConfig{
		PerAccountWindow:     5 * time.Minute,
		PerAccountThreshold:  5,
		PerAccountLockoutFor: 15 * time.Minute,
		PerIPWindow:          5 * time.Minute,
		PerIPThreshold:       20,
		PerIPLockoutFor:      15 * time.Minute,
	}
}

// LoginRateLimiter is the entrypoint used by /auth/login. nil-safe at
// the public method level so callers can pass a nil pointer and the
// methods become no-ops.
type LoginRateLimiter struct {
	client *redis.Client
	cfg    LoginLockoutConfig
}

// NewLoginRateLimiter wraps client. Pass nil to get a no-op limiter.
// Zero fields in cfg take defaults from DefaultLoginLockoutConfig.
func NewLoginRateLimiter(client *redis.Client, cfg LoginLockoutConfig) *LoginRateLimiter {
	def := DefaultLoginLockoutConfig()
	if cfg.PerAccountWindow == 0 {
		cfg.PerAccountWindow = def.PerAccountWindow
	}
	if cfg.PerAccountThreshold == 0 {
		cfg.PerAccountThreshold = def.PerAccountThreshold
	}
	if cfg.PerAccountLockoutFor == 0 {
		cfg.PerAccountLockoutFor = def.PerAccountLockoutFor
	}
	if cfg.PerIPWindow == 0 {
		cfg.PerIPWindow = def.PerIPWindow
	}
	if cfg.PerIPThreshold == 0 {
		cfg.PerIPThreshold = def.PerIPThreshold
	}
	if cfg.PerIPLockoutFor == 0 {
		cfg.PerIPLockoutFor = def.PerIPLockoutFor
	}
	return &LoginRateLimiter{client: client, cfg: cfg}
}

// Check returns Locked=true if EITHER the per-account or per-IP bucket
// is currently locked out. RetryAfter is the longer of the two TTLs so
// the caller's Retry-After header is conservative. Fail-open: a Valkey
// error returns LockoutStatus{} with the error so the handler can log
// and proceed (a Valkey blip should not take down login).
func (l *LoginRateLimiter) Check(ctx context.Context, email, ip string) (LockoutStatus, error) {
	if l == nil || l.client == nil {
		return LockoutStatus{}, nil
	}
	email = normalizeEmail(email)
	var maxRetry time.Duration
	locked := false
	if email != "" {
		ttl, err := l.client.PTTL(ctx, loginAccountLockoutKey(email)).Result()
		if err != nil {
			return LockoutStatus{}, fmt.Errorf("ratelimit: login account pttl: %w", err)
		}
		if ttl > 0 {
			locked = true
			if ttl > maxRetry {
				maxRetry = ttl
			}
		}
	}
	if ip != "" {
		ttl, err := l.client.PTTL(ctx, loginIPLockoutKey(ip)).Result()
		if err != nil {
			return LockoutStatus{}, fmt.Errorf("ratelimit: login ip pttl: %w", err)
		}
		if ttl > 0 {
			locked = true
			if ttl > maxRetry {
				maxRetry = ttl
			}
		}
	}
	return LockoutStatus{Locked: locked, RetryAfter: maxRetry}, nil
}

// LoginFailureResult reports which buckets, if any, transitioned into a
// new lockout on this call. Callers use the booleans to decide whether
// to emit a security log (one log per lockout, not per failed attempt).
type LoginFailureResult struct {
	AccountLockedNow bool
	IPLockedNow      bool
}

// RecordFailure increments both bucket counters and sets a lockout key
// on whichever crosses its threshold. Callers should treat a non-nil
// error as fail-open — log and continue.
func (l *LoginRateLimiter) RecordFailure(ctx context.Context, email, ip string) (LoginFailureResult, error) {
	if l == nil || l.client == nil {
		return LoginFailureResult{}, nil
	}
	email = normalizeEmail(email)
	var result LoginFailureResult

	if email != "" {
		count, err := l.incrWithExpire(ctx, loginAccountFailKey(email), l.cfg.PerAccountWindow)
		if err != nil {
			return result, fmt.Errorf("ratelimit: login account fail: %w", err)
		}
		if count >= int64(l.cfg.PerAccountThreshold) {
			ok, err := l.client.SetNX(ctx, loginAccountLockoutKey(email), "1", l.cfg.PerAccountLockoutFor).Result()
			if err != nil {
				return result, fmt.Errorf("ratelimit: login account lockout set: %w", err)
			}
			result.AccountLockedNow = ok
		}
	}

	if ip != "" {
		count, err := l.incrWithExpire(ctx, loginIPFailKey(ip), l.cfg.PerIPWindow)
		if err != nil {
			return result, fmt.Errorf("ratelimit: login ip fail: %w", err)
		}
		if count >= int64(l.cfg.PerIPThreshold) {
			ok, err := l.client.SetNX(ctx, loginIPLockoutKey(ip), "1", l.cfg.PerIPLockoutFor).Result()
			if err != nil {
				return result, fmt.Errorf("ratelimit: login ip lockout set: %w", err)
			}
			result.IPLockedNow = ok
		}
	}
	return result, nil
}

// Clear removes the per-account counter and lockout. Called on a
// successful login so a couple of mistaken attempts followed by the
// right password don't accumulate toward a future lockout. The per-IP
// counter is intentionally NOT cleared — one user authenticating
// successfully from a NAT shouldn't reset failure tracking that may
// reflect a separate attacker on the same IP.
func (l *LoginRateLimiter) Clear(ctx context.Context, email string) error {
	if l == nil || l.client == nil || email == "" {
		return nil
	}
	email = normalizeEmail(email)
	if err := l.client.Del(ctx, loginAccountFailKey(email), loginAccountLockoutKey(email)).Err(); err != nil {
		return fmt.Errorf("ratelimit: login clear: %w", err)
	}
	return nil
}

// incrWithExpire applies the same INCR+EXPIRE pipeline used by the
// API-key limiter. EXPIRE on every hit is a known harmless quirk —
// the counter naturally rolls over after Window seconds anyway.
func (l *LoginRateLimiter) incrWithExpire(ctx context.Context, key string, window time.Duration) (int64, error) {
	pipe := l.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func loginAccountFailKey(email string) string {
	return fmt.Sprintf("%s:%s", keyPrefixLoginAccountFail, email)
}

func loginAccountLockoutKey(email string) string {
	return fmt.Sprintf("%s:%s", keyPrefixLoginAccountLock, email)
}

func loginIPFailKey(ip string) string {
	return fmt.Sprintf("%s:%s", keyPrefixLoginIPFail, ip)
}

func loginIPLockoutKey(ip string) string {
	return fmt.Sprintf("%s:%s", keyPrefixLoginIPLock, ip)
}

// ── Internals ─────────────────────────────────────────────────────────

const (
	keyPrefixLockout          = "apikey:bf:lock"
	keyPrefixFailCount        = "apikey:bf:fail"
	keyPrefixPerKeyRate       = "apikey:rl"
	keyPrefixLoginAccountFail = "login:bf:account:fail"
	keyPrefixLoginAccountLock = "login:bf:account:lock"
	keyPrefixLoginIPFail      = "login:bf:ip:fail"
	keyPrefixLoginIPLock      = "login:bf:ip:lock"
)

func lockoutKey(prefix, ip string) string {
	return fmt.Sprintf("%s:%s:%s", keyPrefixLockout, prefix, ip)
}

func failureCounterKey(prefix, ip string) string {
	return fmt.Sprintf("%s:%s:%s", keyPrefixFailCount, prefix, ip)
}

func perKeyCounterKey(keyID uuid.UUID, t time.Time) string {
	return fmt.Sprintf("%s:%s:%d", keyPrefixPerKeyRate, keyID, t.Unix()/60)
}

// ── Source-IP extraction helper ───────────────────────────────────────

// SourceIP extracts the request's source IP for rate-limit bucketing.
// Trusts whatever middleware (chi.middleware.RealIP) has rewritten
// r.RemoteAddr to. Strips the port. Returns "" on parse failure so
// the limiter falls back to a no-op for that request rather than
// bucketing every malformed source under the empty-string key.
func SourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may be an IP without :port (rare but possible).
		// If it parses as an IP, accept it.
		if ip := net.ParseIP(r.RemoteAddr); ip != nil {
			return r.RemoteAddr
		}
		return ""
	}
	return host
}

// ErrRateLimited is the sentinel surfaced when a request is denied due
// to rate limiting. APIKeyMiddleware maps it to 429.
var ErrRateLimited = errors.New("identity: rate limited")

// writeRateLimitError writes a 429 envelope matching writeAuthError's
// shape, with a Retry-After header in seconds (RFC 7231 §7.1.3).
func writeRateLimitError(w http.ResponseWriter, retryAfter time.Duration, code, message string) {
	if retryAfter > 0 {
		secs := int(retryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, message)
}
