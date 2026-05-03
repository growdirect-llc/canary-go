package sub3

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// WorkerConfig wires one Sub 3 batch-anchor instance.
type WorkerConfig struct {
	Pool         *pgxpool.Pool
	Inscriber    Inscriber
	Network      string        // "signet" or "mainnet"; default "signet"
	PollInterval time.Duration // default 10 minutes
	BatchSize    int           // default 50
	MinBatch     int           // default 2; skip if fewer events available
	Logger       *zap.Logger
}

// Worker drives the Sub 3 Merkle-anchor batch loop.
type Worker struct {
	cfg   WorkerConfig
	log   *zap.Logger
	store AnchorStorer
}

// NewWorker wires a Worker with defaults applied. Callers own pool and
// inscriber lifecycles.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Network == "" {
		cfg.Network = "signet"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Minute
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 50
	}
	if cfg.MinBatch == 0 {
		cfg.MinBatch = 2
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{
		cfg:   cfg,
		log:   log.With(zap.String("svc", "sub3-merkle-ordinal")),
		store: NewStore(cfg.Pool, cfg.Network),
	}
}

// newWorkerWithStore is used by tests to inject a stub AnchorStorer.
func newWorkerWithStore(cfg WorkerConfig, store AnchorStorer) *Worker {
	if cfg.Network == "" {
		cfg.Network = "signet"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Minute
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 50
	}
	if cfg.MinBatch == 0 {
		cfg.MinBatch = 2
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{
		cfg:   cfg,
		log:   log.With(zap.String("svc", "sub3-merkle-ordinal")),
		store: store,
	}
}

// Run blocks on the polling loop until ctx is cancelled. On each tick it
// attempts one batch anchor cycle. Errors are logged and the loop continues.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("sub3 started",
		zap.Duration("poll_interval", w.cfg.PollInterval),
		zap.Int("batch_size", w.cfg.BatchSize),
		zap.Int("min_batch", w.cfg.MinBatch),
		zap.String("network", w.cfg.Network),
	)

	// Run once immediately on startup, then on the ticker.
	if err := w.tick(ctx); err != nil && !isCtxErr(err) {
		w.log.Warn("initial tick failed", zap.Error(err))
	}

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.tick(ctx); err != nil {
				if isCtxErr(err) {
					return err
				}
				w.log.Warn("tick error", zap.Error(err))
			}
		}
	}
}

// Tick is exported for testing — runs exactly one batch cycle.
func (w *Worker) Tick(ctx context.Context) error {
	return w.tick(ctx)
}

// tick is one anchor cycle. It delegates the fetch → inscribe → write
// sequence to store.WriteAnchor, which uses two transactions:
//
//   - Tx 1: lock and fetch unanchored rows → commit (lock released)
//   - External: Inscribe (network call, outside any transaction)
//   - Tx 2: write anchor + evidence_anchors → commit
//
// Failed inscriptions are recorded in protocol.anchors with
// anchor_status = 'failed' for audit; evidence_anchors rows are not
// written so those events remain available for the next retry cycle.
//
// Returns nil when the batch is below minBatch (no-op).
func (w *Worker) tick(ctx context.Context) error {
	result, err := w.store.WriteAnchor(ctx, w.cfg.Inscriber, w.cfg.BatchSize, w.cfg.MinBatch)
	if err != nil {
		w.log.Warn("anchor cycle failed", zap.Error(err))
		return err
	}

	if result == nil {
		// Below minBatch or no unanchored events — clean no-op.
		w.log.Debug("below minBatch threshold or no unanchored events — skipping")
		return nil
	}

	w.log.Info("anchor written",
		zap.String("anchor_id", result.AnchorID.String()),
		zap.String("merkle_root", result.MerkleRoot),
		zap.String("inscription_id", result.InscriptionID),
		zap.String("status", result.AnchorStatus),
		zap.Int("events", result.EventCount),
	)
	return nil
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
