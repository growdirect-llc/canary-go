// Package changefeed implements the change-feed-tier middleware that
// surfaces cursor + watermark + lag semantics on REST endpoints in the
// /v1/alerts/*, /v1/owl/*, /v1/chirp/detections families.
//
// Per spec §"Per-tier infrastructure" change-feed row:
//   - Protocol: REST polling with cursor / sub with watermark
//   - Health:   lag exceeded / queue depth
//   - Cache:    short TTL (~5 min)
//   - Recovery: catch up from watermark
//
// The middleware:
//   - Parses ?cursor=<opaque> from the request (clients echo this back
//     to resume from where they left off).
//   - Reads the current Watermark for the request's stream key.
//   - Computes lag = watermark - cursor (clamped to ≥ 0).
//   - Emits X-Watermark, X-Lag, and (when over threshold) X-Lag-Exceeded
//     response headers so operators see staleness without per-handler
//     instrumentation.
//
// Stream key derivation lives in StreamKey — by default it's the URL
// path stripped of any path parameter component (so /v1/alerts/{id}
// shares a watermark with /v1/alerts). Callers can override.
//
// T3A.2 / GRO-899.
package changefeed

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Watermark is the per-stream high-water-mark provider. Implementations
// live alongside this file: Valkey-backed (production) and in-memory (tests).
type Watermark interface {
	// Get returns the current watermark for the given stream key. Watermarks
	// are unix-milli timestamps so the wire format stays portable.
	Get(ctx context.Context, key string) (int64, bool, error)
}

// StreamKeyFunc derives the stream key for a request. Default uses
// r.URL.Path. Callers can substitute (e.g. trim path parameters).
type StreamKeyFunc func(r *http.Request) string

// Middleware wraps a handler with cursor + watermark + lag tracking.
// Construct via New.
type Middleware struct {
	wm           Watermark
	lagThreshold time.Duration
	keyFn        StreamKeyFunc
	logger       *zap.Logger
}

// New returns a middleware ready to wrap any http.Handler. lagThreshold
// is the lag duration above which the X-Lag-Exceeded header fires. keyFn
// can be nil — defaults to r.URL.Path.
func New(wm Watermark, lagThreshold time.Duration, keyFn StreamKeyFunc, logger *zap.Logger) *Middleware {
	if keyFn == nil {
		keyFn = func(r *http.Request) string { return r.URL.Path }
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Middleware{
		wm:           wm,
		lagThreshold: lagThreshold,
		keyFn:        keyFn,
		logger:       logger,
	}
}

// Wrap returns a handler that adds X-Watermark, X-Lag, and (when over
// threshold) X-Lag-Exceeded response headers. The wrapped handler runs
// unchanged — this middleware is purely observational.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.keyFn(r)
		watermark, ok, err := m.wm.Get(r.Context(), key)
		if err != nil {
			// Watermark lookup failed — log and continue without the headers.
			// The handler still runs; we just don't add observational metadata.
			m.logger.Warn("changefeed: watermark lookup failed",
				zap.String("key", key), zap.Error(err))
			next.ServeHTTP(w, r)
			return
		}

		// Always emit X-Watermark on responses (even when no watermark exists
		// yet — value is 0, which the client treats as "no events yet").
		// Set BEFORE calling next so the header survives WriteHeader.
		w.Header().Set("X-Watermark", strconv.FormatInt(watermark, 10))

		// Cursor is optional. When absent, lag is 0 (client is caught up by
		// definition for a first poll). When present, lag = watermark - cursor
		// (in milliseconds, converted to time.Duration for the header).
		var lag time.Duration
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			if cursorMS, perr := strconv.ParseInt(cursor, 10, 64); perr == nil {
				if ok && watermark >= cursorMS {
					lag = time.Duration(watermark-cursorMS) * time.Millisecond
				}
			}
		}
		w.Header().Set("X-Lag", lag.String())

		// Health signal: when lag exceeds the configured threshold, surface
		// it in the response and log a warn. T3A.4 will collect these
		// signals into the per-tier health rollup on /devops/observability.
		if m.lagThreshold > 0 && lag > m.lagThreshold {
			w.Header().Set("X-Lag-Exceeded", "1")
			m.logger.Warn("changefeed: lag exceeded threshold",
				zap.String("key", key),
				zap.Duration("lag", lag),
				zap.Duration("threshold", m.lagThreshold),
			)
		}

		next.ServeHTTP(w, r)
	})
}

// Wire returns next unchanged when the flag is off; otherwise the
// middleware-wrapped handler. Same shape as refcache.Wire.
func Wire(enabled bool, mw *Middleware, next http.Handler) http.Handler {
	if !enabled || mw == nil {
		return next
	}
	return mw.Wrap(next)
}

// MemoryWatermark is an in-memory Watermark for tests. Concurrent-safe.
// Production code uses ValkeyWatermark.
type MemoryWatermark struct {
	mu    sync.Mutex
	marks map[string]int64
}

// NewMemoryWatermark builds an empty watermark store.
func NewMemoryWatermark() *MemoryWatermark {
	return &MemoryWatermark{marks: make(map[string]int64)}
}

func (m *MemoryWatermark) Get(_ context.Context, key string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.marks[key]
	return v, ok, nil
}

// Advance sets the watermark for a key. Tests use this to simulate
// upstream events landing.
func (m *MemoryWatermark) Advance(key string, watermark int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marks[key] = watermark
}

// ErrInvalidWatermark — exported so tests + production can match on it.
var ErrInvalidWatermark = errors.New("changefeed: invalid watermark value")
