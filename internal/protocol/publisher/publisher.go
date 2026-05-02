// Package publisher publishes validated webhook events onto Valkey
// Streams (the "events" stream consumed by the Triple Subscriber
// pipeline — GRO-747). It also exposes a Valkey-backed NonceStore
// that satisfies internal/protocol/hmac.NonceStore.
//
// The Publisher interface lets the webhook handler stay infra-free
// in tests while real production runs against a Valkey cluster.
package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Event is the canonical envelope written to the Valkey stream.
// Each subscriber (Sub 1 / Sub 2 / Sub 3) consumes the same events
// stream with its own consumer group; Event therefore needs every
// piece of context any subscriber will need.
//
// Patent reference: Application 63/991,596, Node 2 → Queue.
type Event struct {
	EventID    uuid.UUID       `json:"event_id"`
	EventHash  string          `json:"event_hash"`   // sha256 hex of raw payload
	SourceCode string          `json:"source_code"`  // 'square', 'shopify', etc.
	MerchantID uuid.UUID       `json:"merchant_id"`  // tenant scope
	Timestamp  time.Time       `json:"timestamp"`    // signed timestamp from header
	IngestedAt time.Time       `json:"ingested_at"`  // gateway clock at accept
	Payload    json.RawMessage `json:"payload"`      // raw webhook body
	Nonce      string          `json:"nonce,omitempty"`
}

// Publisher emits events into the substrate's queue.
type Publisher interface {
	Publish(ctx context.Context, evt Event) error
}

// ---------------------------------------------------------------------------
// Valkey Streams implementation
// ---------------------------------------------------------------------------

// ValkeyPublisher writes events to a Valkey Streams entry. Streams is
// the right primitive: durable, ordered within partition (by merchant_id
// when partitioning is configured downstream), and supports independent
// consumer groups per subscriber — the Triple Subscriber Pattern.
type ValkeyPublisher struct {
	client *redis.Client
	stream string
}

// NewValkey constructs a Publisher against an existing redis client.
// The stream name is typically "protocol:events".
func NewValkey(client *redis.Client, stream string) *ValkeyPublisher {
	return &ValkeyPublisher{client: client, stream: stream}
}

// Publish writes one event to the configured Valkey stream. Returns the
// upstream error if the XADD fails — the gateway maps that to 5xx so
// the source network retries.
func (p *ValkeyPublisher) Publish(ctx context.Context, evt Event) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("publisher: marshal event: %w", err)
	}
	args := &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]any{
			"event_id":    evt.EventID.String(),
			"event_hash":  evt.EventHash,
			"source_code": evt.SourceCode,
			"merchant_id": evt.MerchantID.String(),
			"event":       string(body),
		},
	}
	if _, err := p.client.XAdd(ctx, args).Result(); err != nil {
		return fmt.Errorf("publisher: xadd: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// In-memory mock for unit tests
// ---------------------------------------------------------------------------

// Mock captures published events for assertion in tests.
type Mock struct {
	mu     sync.Mutex
	Events []Event
	// FailWith, when non-nil, is returned from every Publish call.
	FailWith error
}

// NewMock builds a Mock publisher.
func NewMock() *Mock { return &Mock{} }

// Publish records the event (or returns FailWith).
func (p *Mock) Publish(_ context.Context, evt Event) error {
	if p.FailWith != nil {
		return p.FailWith
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, evt)
	return nil
}

// Snapshot returns a copy of the captured events.
func (p *Mock) Snapshot() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Event, len(p.Events))
	copy(out, p.Events)
	return out
}

// ---------------------------------------------------------------------------
// Valkey-backed NonceStore (satisfies hmac.NonceStore)
// ---------------------------------------------------------------------------

// ValkeyNonceStore records nonces in Valkey using SET-NX-EX. The TTL
// equals the verifier's replay window, so once the window passes the
// nonce automatically becomes reusable (which is fine — the timestamp
// check would have rejected a replay anyway).
type ValkeyNonceStore struct {
	client *redis.Client
	prefix string
}

// NewValkeyNonceStore builds a nonce store. prefix is prepended to
// every key so multiple gateways can share a Valkey instance without
// collisions ("gateway:nonce" is reasonable).
func NewValkeyNonceStore(client *redis.Client, prefix string) *ValkeyNonceStore {
	if prefix == "" {
		prefix = "gateway:nonce"
	}
	return &ValkeyNonceStore{client: client, prefix: prefix}
}

// SeenOnce returns true on the first sighting of nonce within the TTL.
// Atomic via SET NX EX — no race between concurrent webhook deliveries.
func (s *ValkeyNonceStore) SeenOnce(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	if nonce == "" {
		return false, errors.New("publisher: empty nonce")
	}
	key := s.prefix + ":" + nonce
	ok, err := s.client.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("publisher: nonce setnx: %w", err)
	}
	return ok, nil
}
