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
	cfg WorkerConfig
	log *zap.Logger
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
		cfg: cfg,
		log: log.With(zap.String("svc", "sub3-merkle-ordinal")),
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

// tick is one anchor cycle: fetch → skip-if-too-small → Merkle → inscribe → write.
func (w *Worker) tick(ctx context.Context) error {
	rows, err := LockAndFetchUnanchored(ctx, w.cfg.Pool, w.cfg.BatchSize)
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		w.log.Debug("no unanchored events — skipping")
		return nil
	}

	if len(rows) < w.cfg.MinBatch {
		w.log.Debug("below minBatch threshold — skipping single-event anchor",
			zap.Int("count", len(rows)),
			zap.Int("min_batch", w.cfg.MinBatch),
		)
		return nil
	}

	// Extract chain_hash values as Merkle leaves.
	leaves := make([]string, len(rows))
	for i, r := range rows {
		leaves[i] = r.ChainHash
	}

	merkleResult, err := BuildMerkleTree(leaves)
	if err != nil {
		return err
	}

	w.log.Info("merkle tree built",
		zap.String("root", merkleResult.Root),
		zap.Int("leaves", len(leaves)),
	)

	inscribeResult, err := w.cfg.Inscriber.Inscribe(ctx, merkleResult.Root, w.cfg.Network)
	if err != nil {
		// Log and write a failed anchor record so we have an audit trail.
		w.log.Warn("inscription failed — writing failed anchor",
			zap.String("merkle_root", merkleResult.Root),
			zap.Error(err),
		)
		// We do NOT write evidence_anchors on failure — those rows must
		// only appear when the inscription succeeded (or at least was
		// submitted). A future poll cycle will retry the same events.
		return err
	}

	anchor, err := WriteAnchor(ctx, w.cfg.Pool, merkleResult, rows, inscribeResult, w.cfg.Network)
	if err != nil {
		return err
	}

	w.log.Info("anchor written",
		zap.String("anchor_id", anchor.AnchorID.String()),
		zap.String("merkle_root", anchor.MerkleRoot),
		zap.String("inscription_id", anchor.InscriptionID),
		zap.String("status", anchor.AnchorStatus),
		zap.Int("events", anchor.EventCount),
	)
	return nil
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
