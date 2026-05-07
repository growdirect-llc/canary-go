// internal/webhook/backpressure.go
//
// Per-(merchant, source) rate limiting backed by a Valkey rolling
// window counter. Configuration is loaded from
// app.webhook_backpressure_config at construction with periodic
// reload via TTL.
//
// The pattern:
//
//   key = "webhook:bp:{source_code}:{merchant_id|*}:{floor_minute}"
//   INCR + EXPIRE 60s
//   if count > max_rps * 60: return 429 with Retry-After
//
// Sliding-window approximation by minute is good enough for SMB
// scale; if we cross 10⁵ events/min sustained against a single
// merchant we re-tune via fixed-window reservoirs (deferred).
//
// Spec: GRO-764 Phase A.1.

package webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// BackpressureConfig is the per-(merchant, source) limit row.
type BackpressureConfig struct {
	MerchantID    *uuid.UUID
	SourceCode    string
	MaxRPS        int
	BurstCapacity int
	Enabled       bool
}

// Backpressure is the runtime rate limiter. Constructed once at
// gateway boot; methods are safe for concurrent use.
type Backpressure struct {
	rdb         *redis.Client
	pool        *pgxpool.Pool
	reloadEvery time.Duration

	mu        sync.RWMutex
	configs   map[bpKey]BackpressureConfig
	loadedAt  time.Time
	keyPrefix string
}

type bpKey struct {
	merchantID string // empty for platform-default
	sourceCode string
}

// NewBackpressure constructs a Backpressure with sensible defaults.
// reloadEvery defaults to 60 seconds. The first config load happens
// lazily on the first Allow call; pass a context to LoadConfigs at
// boot to surface load errors early.
func NewBackpressure(rdb *redis.Client, pool *pgxpool.Pool, opts ...BackpressureOption) *Backpressure {
	b := &Backpressure{
		rdb:         rdb,
		pool:        pool,
		reloadEvery: 60 * time.Second,
		configs:     map[bpKey]BackpressureConfig{},
		keyPrefix:   "webhook:bp",
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// BackpressureOption configures Backpressure at construction.
type BackpressureOption func(*Backpressure)

// WithBackpressureReloadInterval overrides the 60s default.
func WithBackpressureReloadInterval(d time.Duration) BackpressureOption {
	return func(b *Backpressure) { b.reloadEvery = d }
}

// LoadConfigs replaces the in-memory cache from
// app.webhook_backpressure_config. Safe to call concurrently with
// Allow; readers see the prior state until the swap completes.
func (b *Backpressure) LoadConfigs(ctx context.Context) error {
	const sql = `
		SELECT merchant_id, source_code, max_rps, burst_capacity, enabled
		  FROM app.webhook_backpressure_config
		 WHERE enabled = TRUE`
	rows, err := b.pool.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("webhook: bp load: %w", err)
	}
	defer rows.Close()

	next := map[bpKey]BackpressureConfig{}
	for rows.Next() {
		var c BackpressureConfig
		if err := rows.Scan(&c.MerchantID, &c.SourceCode, &c.MaxRPS, &c.BurstCapacity, &c.Enabled); err != nil {
			return fmt.Errorf("webhook: bp scan: %w", err)
		}
		k := bpKey{sourceCode: c.SourceCode}
		if c.MerchantID != nil {
			k.merchantID = c.MerchantID.String()
		}
		next[k] = c
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("webhook: bp iter: %w", err)
	}

	b.mu.Lock()
	b.configs = next
	b.loadedAt = time.Now()
	b.mu.Unlock()
	return nil
}

// Allow checks whether a request from (merchantID, sourceCode) fits
// within the configured per-minute budget.
//
// Returns:
//   - allowed = true when the request fits or no config applies
//   - allowed = false + retryAfter when the rolling-minute count
//     would exceed (maxRPS × 60) + burstCapacity
//
// Allow always increments the counter on the success path; callers
// that want a "preview" of remaining budget should use Peek.
func (b *Backpressure) Allow(ctx context.Context, merchantID uuid.UUID, sourceCode string) (bool, time.Duration, error) {
	b.maybeReload(ctx)

	cfg := b.configFor(merchantID, sourceCode)
	if !cfg.Enabled || cfg.MaxRPS <= 0 {
		return true, 0, nil
	}

	floor := time.Now().Truncate(time.Minute).Unix()
	key := fmt.Sprintf("%s:%s:%s:%d", b.keyPrefix, sourceCode, merchantID, floor)

	count, err := b.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("webhook: bp incr: %w", err)
	}
	if count == 1 {
		// First increment for this minute window — set TTL just past
		// the next minute so a small amount of overlap is fine.
		_ = b.rdb.Expire(ctx, key, 75*time.Second).Err()
	}

	limit := int64(cfg.MaxRPS*60 + cfg.BurstCapacity)
	if count > limit {
		// Compute Retry-After against the start of the next minute.
		nextMinute := time.Unix(floor+60, 0)
		return false, time.Until(nextMinute), nil
	}
	return true, 0, nil
}

// Peek returns the current counter value without incrementing.
func (b *Backpressure) Peek(ctx context.Context, merchantID uuid.UUID, sourceCode string) (int64, error) {
	floor := time.Now().Truncate(time.Minute).Unix()
	key := fmt.Sprintf("%s:%s:%s:%d", b.keyPrefix, sourceCode, merchantID, floor)
	v, err := b.rdb.Get(ctx, key).Int64()
	if err != nil {
		if err == redis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

// configFor resolves the effective config for a (merchantID,
// sourceCode) pair. Merchant-specific override wins; platform default
// (merchantID NULL) is the fallback. Returns a zero-value (Enabled:
// false) when neither exists — Allow treats that as no limit.
func (b *Backpressure) configFor(merchantID uuid.UUID, sourceCode string) BackpressureConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if cfg, ok := b.configs[bpKey{merchantID: merchantID.String(), sourceCode: sourceCode}]; ok {
		return cfg
	}
	if cfg, ok := b.configs[bpKey{sourceCode: sourceCode}]; ok {
		return cfg
	}
	return BackpressureConfig{}
}

// maybeReload triggers an async LoadConfigs when the cache TTL has
// expired. Best-effort — failures don't block the request path.
func (b *Backpressure) maybeReload(ctx context.Context) {
	b.mu.RLock()
	stale := time.Since(b.loadedAt) > b.reloadEvery
	b.mu.RUnlock()
	if !stale {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = b.LoadConfigs(c)
	}()
}
