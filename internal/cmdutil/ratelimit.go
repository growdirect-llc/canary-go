// internal/cmdutil/ratelimit.go
//
// MustValkeyRateLimiter — small helper used by every cmd/* binary
// that wires APIKeyMiddleware. Parses the Valkey URL, opens a client,
// constructs a RateLimiter with default policy, and returns both the
// limiter and a close function the caller is expected to defer.
//
// Centralizing the boilerplate here means the per-binary delta for
// GRO-912 is two lines:
//
//	limiter, closeLimiter := cmdutil.MustValkeyRateLimiter(cfg.ValkeyURL, logger)
//	defer closeLimiter()
//
// then `Limiter: limiter` in APIKeyMiddlewareOpts.

package cmdutil

import (
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// MustValkeyRateLimiter parses valkeyURL and returns a RateLimiter
// with default policy plus a close function. logger.Fatal is called
// if the URL is malformed — the caller is at startup and a misconfig
// is unrecoverable.
//
// On a logger.Fatal call, no close func is returned — the process
// exits before defer runs anyway.
func MustValkeyRateLimiter(valkeyURL string, logger *zap.Logger) (*identity.RateLimiter, func()) {
	opt, err := redis.ParseURL(valkeyURL)
	if err != nil {
		logger.Fatal("cmdutil: parse VALKEY_URL", zap.Error(err))
	}
	client := redis.NewClient(opt)
	limiter := identity.NewRateLimiter(client, identity.DefaultRateLimitConfig())
	return limiter, func() { _ = client.Close() }
}

// MustValkeyRateLimiterFromClient builds a RateLimiter on top of an
// already-constructed *redis.Client. Use this in binaries that already
// own a Valkey client for other reasons (bull, inventory, gateway,
// sub1, sub2, identity, edge) — the limiter shares the same connection
// pool rather than opening a second one.
//
// The caller retains responsibility for closing the underlying client.
func MustValkeyRateLimiterFromClient(client *redis.Client) *identity.RateLimiter {
	return identity.NewRateLimiter(client, identity.DefaultRateLimitConfig())
}
