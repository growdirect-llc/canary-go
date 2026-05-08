// Valkey-backed Cache implementation. Production constructor for the
// reference-tier middleware.
package refcache

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// ValkeyCache stores cache entries in Valkey via the standard go-redis
// client used by the rest of the gateway (cmd/gateway/main.go line 103-122).
//
// Keep the surface narrow: just Get and Set with TTL — same shape as
// publisher.Publisher to keep the package infra-free for tests.
type ValkeyCache struct {
	rdb *redis.Client
}

// NewValkeyCache wraps an existing go-redis client. The client's lifecycle
// (Close, ping) stays with the gateway; this struct only borrows it.
func NewValkeyCache(rdb *redis.Client) *ValkeyCache {
	return &ValkeyCache{rdb: rdb}
}

func (v *ValkeyCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	res, err := v.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return res, true, nil
}

func (v *ValkeyCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("refcache: ttl must be positive")
	}
	return v.rdb.Set(ctx, key, value, ttl).Err()
}

// Wire returns next unchanged when the flag is off; otherwise returns the
// middleware-wrapped handler. Callers compose this in main.go where they
// know whether the feature flag is on. Two flags' worth of plumbing in
// gateway/main.go is replaced with one Wire call per protected route group.
func Wire(enabled bool, mw *Middleware, next interface{ ServeHTTP(http.ResponseWriter, *http.Request) }) http.Handler {
	if !enabled || mw == nil {
		return asHandler(next)
	}
	return mw.Wrap(asHandler(next))
}

// asHandler upgrades the narrow ServeHTTP() interface to a full http.Handler.
// Lets Wire accept either a *chi.Mux, a http.Handler, or a HandlerFunc.
func asHandler(h interface{ ServeHTTP(http.ResponseWriter, *http.Request) }) http.Handler {
	if hh, ok := h.(http.Handler); ok {
		return hh
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) })
}
