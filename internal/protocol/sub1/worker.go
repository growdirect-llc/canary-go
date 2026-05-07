package sub1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
)

// ConsumerGroup is the Valkey Streams consumer group name for Sub 1.
// Each subscriber in the Triple Subscriber pipeline (Sub 1 / Sub 2 /
// Sub 3) reads the same stream under a distinct consumer group, which
// is what gives the protocol its three-rail accountability without
// duplicating the queue.
const ConsumerGroup = "sub1-hash-seal"

// Stream is the canonical stream the gateway publishes to.
const Stream = "protocol:events"

// WorkerConfig configures one Sub 1 instance.
type WorkerConfig struct {
	Pool         *pgxpool.Pool
	Redis        *redis.Client
	Stream       string
	Group        string
	Consumer     string        // unique within the group
	BlockTimeout time.Duration // XReadGroup BLOCK; defaults to 2s
	Logger       *zap.Logger
}

// Worker is a Sub 1 consumer. Construct with NewWorker; drive with Run.
type Worker struct {
	cfg WorkerConfig
	log *zap.Logger
}

// NewWorker wires a worker. Callers are responsible for closing the
// underlying pool and redis client.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Stream == "" {
		cfg.Stream = Stream
	}
	if cfg.Group == "" {
		cfg.Group = ConsumerGroup
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "sub1-default"
	}
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = 2 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{cfg: cfg, log: log.With(zap.String("svc", "sub1-hash-seal"))}
}

// EnsureGroup creates the consumer group if it doesn't already exist.
// "BUSYGROUP Consumer Group name already exists" is treated as success
// — idempotent setup is the goal.
func (w *Worker) EnsureGroup(ctx context.Context) error {
	// MKSTREAM lets the group be created before any events have arrived.
	if err := w.cfg.Redis.XGroupCreateMkStream(ctx, w.cfg.Stream, w.cfg.Group, "$").Err(); err != nil {
		// go-redis surfaces BUSYGROUP as a plain error string match.
		if isBusyGroup(err) {
			return nil
		}
		return fmt.Errorf("sub1: xgroup create: %w", err)
	}
	return nil
}

// Run blocks consuming events until ctx is cancelled. Each event is
// deserialized, sealed via WriteEvidence, then ACKed regardless of
// whether the seal was a fresh insert or a duplicate (idempotency
// success path).
//
// Real exec errors (non-duplicate) leave the message un-ACKed, which
// is what we want — Valkey will redeliver after the consumer's pending
// timeout, and the next attempt either succeeds or the duplicate path
// catches it.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.EnsureGroup(ctx); err != nil {
		return err
	}
	w.log.Info("sub1 started",
		zap.String("stream", w.cfg.Stream),
		zap.String("group", w.cfg.Group),
		zap.String("consumer", w.cfg.Consumer),
	)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.processBatch(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			// Surface and continue — the worker is supposed to be
			// resilient to transient infra failures.
			w.log.Warn("batch error", zap.Error(err))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

// processBatch reads up to 10 messages, processes each, and returns.
// Exposed (non-export) for direct invocation from tests.
func (w *Worker) processBatch(ctx context.Context) error {
	res, err := w.cfg.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    w.cfg.Group,
		Consumer: w.cfg.Consumer,
		Streams:  []string{w.cfg.Stream, ">"},
		Count:    10,
		Block:    w.cfg.BlockTimeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil // BLOCK timeout, normal idle path
		}
		return fmt.Errorf("xreadgroup: %w", err)
	}

	for _, stream := range res {
		for _, msg := range stream.Messages {
			w.handle(ctx, msg)
		}
	}
	return nil
}

// handle processes one message: parse → seal → ack. Errors are logged;
// the message is only ACKed on success or duplicate.
func (w *Worker) handle(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["event"].(string)
	if !ok {
		w.log.Error("malformed message — missing 'event' field",
			zap.String("id", msg.ID))
		// ACK so we don't loop forever on a poison entry.
		_, _ = w.cfg.Redis.XAck(ctx, w.cfg.Stream, w.cfg.Group, msg.ID).Result()
		return
	}

	var evt publisher.Event
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		w.log.Error("event unmarshal", zap.String("id", msg.ID), zap.Error(err))
		_, _ = w.cfg.Redis.XAck(ctx, w.cfg.Stream, w.cfg.Group, msg.ID).Result()
		return
	}

	chainHash, err := WriteEvidence(ctx, w.cfg.Pool, evt)
	switch {
	case errors.Is(err, ErrDuplicateEvent):
		w.log.Debug("duplicate event_hash — already sealed",
			zap.String("event_hash", evt.EventHash),
			zap.String("event_id", evt.EventID.String()))
	case err != nil:
		w.log.Error("seal failed",
			zap.String("event_hash", evt.EventHash),
			zap.Error(err))
		// Do NOT ack — let Valkey redeliver.
		return
	default:
		w.log.Info("sealed",
			zap.String("event_id", evt.EventID.String()),
			zap.String("event_hash", evt.EventHash),
			zap.String("chain_hash", chainHash),
			zap.String("merchant_id", evt.MerchantID.String()),
		)
	}

	if _, err := w.cfg.Redis.XAck(ctx, w.cfg.Stream, w.cfg.Group, msg.ID).Result(); err != nil {
		w.log.Warn("ack failed", zap.String("id", msg.ID), zap.Error(err))
	}
}

// isBusyGroup detects the BUSYGROUP error returned by XGROUP CREATE
// when the group already exists. go-redis returns it as a plain
// error string, not a typed error.
func isBusyGroup(err error) bool {
	return err != nil && err.Error() != "" &&
		(containsAll(err.Error(), "BUSYGROUP") ||
			containsAll(err.Error(), "already exists"))
}

func containsAll(haystack string, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
