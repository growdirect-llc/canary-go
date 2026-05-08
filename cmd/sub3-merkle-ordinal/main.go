// cmd/sub3-merkle-ordinal/main.go
//
// Sub 3 — Merkle & Ordinal anchor worker. Node 5/6 of the Canary
// protocol pipeline (patent Application 63/991,596). Polls
// protocol.evidence for unanchored rows, builds a binary Merkle tree
// over their chain_hash values, inscribes the Merkle root on Bitcoin
// via OrdinalsBot (or a stub in dev), and records the proof paths in
// protocol.anchors + protocol.evidence_anchors.
//
//
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/protocol/sub3"
)

const serviceName = "canary-sub3-merkle-ordinal"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	// OrdinalsBot config — empty API key falls back to StubInscriber.
	apiKey := os.Getenv("ORDINALSBOT_API_KEY")
	network := getEnvOr("ORDINALSBOT_NETWORK", "signet")
	inscriber := sub3.NewOrdinalsBot(apiKey, network)
	if apiKey == "" {
		logger.Info("ORDINALSBOT_API_KEY not set — using StubInscriber (dev mode)")
	}

	// Poll interval and batch settings.
	pollInterval := parseDuration("SUB3_POLL_INTERVAL", 10*time.Minute)
	batchSize := parseInt("SUB3_BATCH_SIZE", 50)
	minBatch := parseInt("SUB3_MIN_BATCH", 2)

	w := sub3.NewWorker(sub3.WorkerConfig{
		Pool:         pool,
		Inscriber:    inscriber,
		Network:      network,
		PollInterval: pollInterval,
		BatchSize:    batchSize,
		MinBatch:     minBatch,
		Logger:       logger,
	})

	workerErr := make(chan error, 1)
	go func() {
		workerErr <- w.Run(ctx)
	}()

	// HTTP health surface — Sub 3 has no public API; the poller is the
	// entire surface. Health endpoint lets container orchestrators probe.
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", healthHandler(cfg, apiKey != ""))

	addr := ":" + getEnvOr("SUB3_PORT", "8095")
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
		zap.String("network", network),
		zap.Duration("poll_interval", pollInterval),
		zap.Int("batch_size", batchSize),
		zap.Int("min_batch", minBatch),
		zap.Bool("real_inscriber", apiKey != ""),
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	httpErr := make(chan error, 1)
	go func() {
		httpErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		if err := <-workerErr; err != nil && err != context.Canceled {
			logger.Error("worker exited with error", zap.Error(err))
			os.Exit(1)
		}
		logger.Info("shutdown clean")
	case err := <-workerErr:
		if err != nil && err != context.Canceled {
			logger.Error("worker exited", zap.Error(err))
			os.Exit(1)
		}
	case err := <-httpErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http server exited", zap.Error(err))
			os.Exit(1)
		}
	}
}

func healthHandler(cfg *config.Config, realInscriber bool) http.HandlerFunc {
	mode := "stub"
	if realInscriber {
		mode = "ordinalsbot"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"service":   cfg.ServiceName,
			"version":   "1.0.0",
			"inscriber": mode,
		})
	}
}

func getEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func parseInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
