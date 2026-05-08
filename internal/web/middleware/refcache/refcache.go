// Package refcache implements a Valkey read-through TTL cache middleware
// for reference-tier endpoints (/v1/m/* items, /v1/l/* locations).
//
// Reference data is high-volume read with low write rate; the cadence-ladder
// reference row in the design spec calls for "Long TTL (~60s hot)". This
// middleware caches successful GET responses in Valkey for the configured
// TTL and replays them verbatim on subsequent matching requests.
//
// Cache scope is per (URL + Authorization-digest + Accept) so per-tenant
// data stays isolated and JSON/YAML negotiation works.
//
// Flag-gated: gateway main wires this only when cfg.TierReferenceCache is
// true. Default off until the deploy proves stable for one full week per
// the spec migration path.
//
// T3A.1 / GRO-894.
package refcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Cache is the storage interface the middleware uses. Implementations live
// alongside this file: Valkey-backed (production) and in-memory (tests).
//
// Mirrors the publisher.Publisher / publisher.Mock pattern so the middleware
// stays infra-free.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// Entry is the on-the-wire shape of a cached response — status code +
// captured headers + body. JSON-encoded into the Cache value.
type Entry struct {
	Status int                 `json:"s"`
	Header map[string][]string `json:"h"`
	Body   []byte              `json:"b"`
}

// Middleware holds the cache + TTL config + key prefix. Construct via New.
type Middleware struct {
	cache  Cache
	ttl    time.Duration
	prefix string
}

// New returns a middleware ready to wrap any http.Handler. Prefix lets
// multiple services share a Valkey instance without colliding cache keys.
func New(c Cache, ttl time.Duration, prefix string) *Middleware {
	if prefix == "" {
		prefix = "refcache:"
	}
	return &Middleware{cache: c, ttl: ttl, prefix: prefix}
}

// Wrap returns a handler that caches GET responses through Valkey. Non-GET
// requests pass through untouched. Cache hits short-circuit before next is
// called and emit X-Cache: HIT; misses populate the cache and emit MISS.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only GET (and HEAD by extension — but we only cache GET responses).
		if r.Method != http.MethodGet {
			w.Header().Set("X-Cache", "BYPASS")
			next.ServeHTTP(w, r)
			return
		}

		key := m.prefix + cacheKey(r)
		ctx := r.Context()

		if data, ok, err := m.cache.Get(ctx, key); err == nil && ok {
			var e Entry
			if jerr := json.Unmarshal(data, &e); jerr == nil {
				for k, vv := range e.Header {
					for _, v := range vv {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set("X-Cache", "HIT")
				if e.Status == 0 {
					e.Status = http.StatusOK
				}
				w.WriteHeader(e.Status)
				_, _ = w.Write(e.Body)
				return
			}
			// Malformed cache entry — fall through to handler. Treat as miss.
		}

		// Miss: capture downstream response, populate cache on 200.
		cap := newCapturingWriter(w)
		next.ServeHTTP(cap, r)
		// Always emit X-Cache: MISS so the caller knows what tier they hit.
		w.Header().Set("X-Cache", "MISS")

		// Only cache 200 OK to avoid pinning errors.
		if cap.status == http.StatusOK || cap.status == 0 {
			payload := Entry{
				Status: 200,
				Header: cap.capturedHeaders(),
				Body:   cap.body.Bytes(),
			}
			if data, jerr := json.Marshal(payload); jerr == nil {
				_ = m.cache.Set(ctx, key, data, m.ttl)
			}
		}
	})
}

// cacheKey hashes the request's identifying surface — URL + Authorization
// digest + Accept — into a stable cache key. Authorization is digested
// (not stored verbatim) so cache contents don't leak credentials if the
// Valkey instance is compromised.
func cacheKey(r *http.Request) string {
	h := sha256.New()
	h.Write([]byte(r.URL.RequestURI()))
	h.Write([]byte{0x1f}) // unit separator — disambiguates multi-field hash
	if auth := r.Header.Get("Authorization"); auth != "" {
		ah := sha256.Sum256([]byte(auth))
		h.Write(ah[:])
	}
	h.Write([]byte{0x1f})
	if accept := r.Header.Get("Accept"); accept != "" {
		h.Write([]byte(accept))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// capturingWriter wraps an http.ResponseWriter and copies the status code,
// headers, and body through to the original while also buffering them for
// cache population.
type capturingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
	hdrSnapshot http.Header
}

func newCapturingWriter(w http.ResponseWriter) *capturingWriter {
	return &capturingWriter{ResponseWriter: w}
}

func (c *capturingWriter) WriteHeader(code int) {
	if c.wroteHeader {
		return
	}
	c.status = code
	c.wroteHeader = true
	// Snapshot the headers BEFORE we add X-Cache: MISS to the response, so
	// the cached entry doesn't bake "X-Cache: MISS" in (next call would
	// then return MISS as a HIT, which is just confusing).
	c.hdrSnapshot = cloneHeader(c.ResponseWriter.Header())
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	c.body.Write(p)
	return c.ResponseWriter.Write(p)
}

func (c *capturingWriter) capturedHeaders() map[string][]string {
	if c.hdrSnapshot == nil {
		// No WriteHeader called — fall back to a snapshot now (handler
		// only Write()'d, which implies 200).
		c.hdrSnapshot = cloneHeader(c.ResponseWriter.Header())
	}
	out := make(map[string][]string, len(c.hdrSnapshot))
	for k, vv := range c.hdrSnapshot {
		// Don't cache hop-by-hop or per-request headers.
		if isExcludedHeader(k) {
			continue
		}
		cp := make([]string, len(vv))
		copy(cp, vv)
		out[k] = cp
	}
	return out
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vv := range h {
		cp := make([]string, len(vv))
		copy(cp, vv)
		out[k] = cp
	}
	return out
}

// isExcludedHeader returns true for headers we don't want to replay from
// the cache (per-request markers, hop-by-hop). Keep this list short.
func isExcludedHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "X-Cache", "Date", "Set-Cookie", "Connection", "Keep-Alive",
		"Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

// MemoryCache is an in-memory Cache implementation for tests. Concurrent-safe.
// Production code uses ValkeyCache.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	now     func() time.Time // overridable for deterministic TTL tests
}

type memoryEntry struct {
	value   []byte
	expires time.Time
}

// NewMemoryCache builds an empty MemoryCache. now defaults to time.Now if nil.
func NewMemoryCache(now func() time.Time) *MemoryCache {
	if now == nil {
		now = time.Now
	}
	return &MemoryCache{entries: make(map[string]memoryEntry), now: now}
}

func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false, nil
	}
	if c.now().After(e.expires) {
		delete(c.entries, key)
		return nil, false, nil
	}
	return e.value, true, nil
}

func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("refcache: ttl must be positive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memoryEntry{
		value:   append([]byte(nil), value...),
		expires: c.now().Add(ttl),
	}
	return nil
}

// Len returns the number of live entries — handy for test assertions.
func (c *MemoryCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
