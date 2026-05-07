package sub2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

// ConsumerGroup is the Sub 2 consumer group on the canonical events
// stream. Sub 1, Sub 2, Sub 3 each read the same protocol:events
// stream under independent consumer groups — Triple Subscriber Pattern,
// patent Application 63/991,596 Node 4.
const ConsumerGroup = "sub2-parse-route"

// Stream is the canonical events stream the gateway publishes to.
const Stream = "protocol:events"

// DLQStream is the dead-letter stream where parse failures and bad
// envelopes land for human inspection. A separate stream — not a
// separate group — keeps the original stream clean and lets ops
// replay or drop entries independently.
const DLQStream = "protocol:events:dlq"

// WorkerConfig wires one Sub 2 worker.
type WorkerConfig struct {
	Pool         *pgxpool.Pool
	Redis        *redis.Client
	Lookup       AdapterLookup
	Stream       string
	Group        string
	DLQStream    string
	Consumer     string
	BlockTimeout time.Duration
	Logger       *zap.Logger
}

// Worker drives the streams consumer loop.
type Worker struct {
	cfg        WorkerConfig
	dispatcher *Dispatcher
	log        *zap.Logger
}

// NewWorker wires a Worker with sensible defaults. Callers own the
// pool and redis client lifecycles.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Stream == "" {
		cfg.Stream = Stream
	}
	if cfg.Group == "" {
		cfg.Group = ConsumerGroup
	}
	if cfg.DLQStream == "" {
		cfg.DLQStream = DLQStream
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "sub2-default"
	}
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = 2 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	store := NewPgxStore(cfg.Pool)
	dispatcher := NewDispatcher(cfg.Lookup, store)
	return &Worker{
		cfg:        cfg,
		dispatcher: dispatcher,
		log:        log.With(zap.String("svc", "sub2-parse-route")),
	}
}

// EnsureGroup creates the consumer group if needed (BUSYGROUP is fine).
func (w *Worker) EnsureGroup(ctx context.Context) error {
	if err := w.cfg.Redis.XGroupCreateMkStream(ctx, w.cfg.Stream, w.cfg.Group, "$").Err(); err != nil {
		if isBusyGroup(err) {
			return nil
		}
		return fmt.Errorf("sub2: xgroup create: %w", err)
	}
	return nil
}

// Run blocks consuming events until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.EnsureGroup(ctx); err != nil {
		return err
	}
	w.log.Info("sub2 started",
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
			w.log.Warn("batch error", zap.Error(err))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

// ProcessBatch reads up to 10 messages and dispatches each. Exposed
// for integration tests; production code should call Run.
func (w *Worker) ProcessBatch(ctx context.Context) error {
	return w.processBatch(ctx)
}

// processBatch reads up to 10 messages and dispatches each.
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
			return nil
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

// handle is the per-message routing path:
//
//   - Malformed envelope (no "event" field, bad JSON) → DLQ + ack.
//   - Unknown source code → log + ack (upstream bug; retry won't help).
//   - Parse failure (errParseFailed) → DLQ + ack.
//   - Parse discard (parser returned nil/nil) → ack silently.
//   - Persist success → ack.
//   - Persist error → log + leave un-acked so Valkey redelivers.
func (w *Worker) handle(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["event"].(string)
	if !ok {
		w.log.Error("malformed message — missing 'event' field",
			zap.String("id", msg.ID))
		w.deadletter(ctx, msg, "missing-event-field", raw)
		w.ack(ctx, msg.ID)
		return
	}
	var env publisher.Event
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		w.log.Error("event unmarshal", zap.String("id", msg.ID), zap.Error(err))
		w.deadletter(ctx, msg, "unmarshal-failed", raw)
		w.ack(ctx, msg.ID)
		return
	}

	err := w.dispatcher.Dispatch(ctx, env)
	switch {
	case err == nil:
		w.log.Info("dispatched",
			zap.String("event_id", env.EventID.String()),
			zap.String("source_code", env.SourceCode),
			zap.String("merchant_id", env.MerchantID.String()),
		)
		w.ack(ctx, msg.ID)

	case IsParseDiscard(err):
		w.log.Debug("parser discarded envelope",
			zap.String("event_id", env.EventID.String()),
			zap.String("source_code", env.SourceCode),
		)
		w.ack(ctx, msg.ID)

	case IsUnknownSource(err):
		// Upstream wired a source the gateway will accept but we
		// don't have a parser for. Log loudly so it surfaces in
		// observability, then ack — the only fix is to register an
		// adapter, redelivery doesn't help.
		w.log.Warn("unknown source code — no adapter registered",
			zap.String("source_code", env.SourceCode),
			zap.String("event_id", env.EventID.String()),
		)
		w.ack(ctx, msg.ID)

	case IsParseFailed(err):
		w.log.Error("parse failed — dead-lettering",
			zap.String("event_id", env.EventID.String()),
			zap.String("source_code", env.SourceCode),
			zap.Error(err),
		)
		w.deadletter(ctx, msg, "parse-failed", raw)
		w.ack(ctx, msg.ID)

	default:
		// Persist failure or anything else — leave un-acked. Valkey's
		// pending-list logic will redeliver after the consumer's
		// pending timeout.
		w.log.Error("dispatch failed — leaving un-acked for retry",
			zap.String("event_id", env.EventID.String()),
			zap.String("source_code", env.SourceCode),
			zap.Error(err),
		)
	}
}

func (w *Worker) ack(ctx context.Context, id string) {
	if _, err := w.cfg.Redis.XAck(ctx, w.cfg.Stream, w.cfg.Group, id).Result(); err != nil {
		w.log.Warn("ack failed", zap.String("id", id), zap.Error(err))
	}
}

// deadletter publishes the original envelope plus a reason tag to the
// DLQ stream. Best-effort — failures are logged but don't block ack.
func (w *Worker) deadletter(ctx context.Context, msg redis.XMessage, reason, raw string) {
	args := &redis.XAddArgs{
		Stream: w.cfg.DLQStream,
		Values: map[string]any{
			"original_id": msg.ID,
			"reason":      reason,
			"event":       raw,
		},
	}
	if _, err := w.cfg.Redis.XAdd(ctx, args).Result(); err != nil {
		w.log.Warn("dlq publish failed",
			zap.String("id", msg.ID),
			zap.String("reason", reason),
			zap.Error(err),
		)
	}
}

// isBusyGroup detects "BUSYGROUP Consumer Group name already exists"
// — go-redis surfaces this as a plain error string, not a typed error.
func isBusyGroup(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "BUSYGROUP") || strings.Contains(s, "already exists")
}
