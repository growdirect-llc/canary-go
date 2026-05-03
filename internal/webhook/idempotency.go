// Package webhook is the domain-owned webhook pipeline that rides on
// top of internal/protocol/webhook (HMAC + envelope) and adds the
// load-bearing primitives a production deployment needs: idempotency,
// dead-letter queue, backpressure, and replay.
//
// Spec: GRO-764 Phase A.1 (folds GRO-642 — Webhook Pipeline & TSP epic).
// See docs/conventions.md for the package-layout pattern this file
// follows.
//
// This file: idempotency. The check is a Valkey SET-NX with TTL
// keyed on (source_code, source_event_id). If the same event arrives
// twice (re-delivered by the POS or replayed by ops), the second
// receipt is observed via the IsDuplicate path and short-circuits.
//
// References:
//   - https://valkey.io/commands/set/ (NX + EX options)
//   - The "stripe-style" idempotency key pattern: a caller-supplied
//     key combined with a deterministic short-circuit on repeat.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// IdempotencyTTL is the default lifetime for idempotency keys in
// Valkey. 24h matches the Stripe / Shopify webhook re-delivery
// windows; sources that re-deliver beyond 24h should be detected by
// the DLQ + replay path instead.
const IdempotencyTTL = 24 * time.Hour

// ErrDuplicateEvent is returned by Idempotency.Reserve when the
// (source_code, source_event_id) tuple was already accepted within
// the TTL window. The handler should return 200 with the originally
// minted event_id rather than re-running the pipeline.
var ErrDuplicateEvent = errors.New("webhook: duplicate event")

// Idempotency is a thin wrapper around go-redis exposing Reserve +
// Lookup operations. One per gateway; reuse across requests.
type Idempotency struct {
	rdb       *redis.Client
	ttl       time.Duration
	keyPrefix string
}

// NewIdempotency constructs an Idempotency keyed on the given prefix
// (default "webhook:idempotency"). Pass an explicit ttl to override
// the 24h default.
func NewIdempotency(rdb *redis.Client, opts ...IdempotencyOption) *Idempotency {
	idem := &Idempotency{
		rdb:       rdb,
		ttl:       IdempotencyTTL,
		keyPrefix: "webhook:idempotency",
	}
	for _, o := range opts {
		o(idem)
	}
	return idem
}

// IdempotencyOption configures an Idempotency at construction.
type IdempotencyOption func(*Idempotency)

// WithIdempotencyTTL overrides the default 24h key lifetime.
func WithIdempotencyTTL(d time.Duration) IdempotencyOption {
	return func(i *Idempotency) { i.ttl = d }
}

// WithIdempotencyKeyPrefix overrides the Valkey key prefix.
func WithIdempotencyKeyPrefix(p string) IdempotencyOption {
	return func(i *Idempotency) { i.keyPrefix = p }
}

// Reserve attempts to claim (sourceCode, sourceEventID) for the given
// canonical eventID. On success returns nil. On duplicate returns
// ErrDuplicateEvent — the handler should fetch the prior event_id via
// Lookup if needed and return the original response.
//
// Empty sourceEventID is treated as "no idempotency key supplied" —
// Reserve returns nil immediately without writing to Valkey. Callers
// that need stronger guarantees should fall back to canonical
// event_hash-based dedup at the seal step (sub1 already does this).
func (i *Idempotency) Reserve(ctx context.Context, sourceCode, sourceEventID, canonicalEventID string) error {
	if sourceEventID == "" {
		return nil
	}
	key := i.key(sourceCode, sourceEventID)
	ok, err := i.rdb.SetNX(ctx, key, canonicalEventID, i.ttl).Result()
	if err != nil {
		return fmt.Errorf("webhook: idempotency reserve: %w", err)
	}
	if !ok {
		return ErrDuplicateEvent
	}
	return nil
}

// Lookup returns the canonical event id previously associated with
// (sourceCode, sourceEventID). Returns "" + nil when no record
// exists (key expired or never reserved). Errors are surfaced
// directly.
func (i *Idempotency) Lookup(ctx context.Context, sourceCode, sourceEventID string) (string, error) {
	if sourceEventID == "" {
		return "", nil
	}
	v, err := i.rdb.Get(ctx, i.key(sourceCode, sourceEventID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", fmt.Errorf("webhook: idempotency lookup: %w", err)
	}
	return v, nil
}

// Forget removes the idempotency record. Useful for tests; rarely
// called in production paths.
func (i *Idempotency) Forget(ctx context.Context, sourceCode, sourceEventID string) error {
	if sourceEventID == "" {
		return nil
	}
	return i.rdb.Del(ctx, i.key(sourceCode, sourceEventID)).Err()
}

func (i *Idempotency) key(sourceCode, sourceEventID string) string {
	return fmt.Sprintf("%s:%s:%s", i.keyPrefix, sourceCode, sourceEventID)
}
