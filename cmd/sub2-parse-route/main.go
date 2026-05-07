// cmd/sub2-parse-route/main.go
//
// Sub 2 — Parse & Route worker. Node 4 of the Canary protocol pipeline
// (patent Application 63/991,596). Consumes canonical events from
// Valkey Streams (protocol:events) under consumer group
// "sub2-parse-route", dispatches each envelope to the registered
// SourceAdapter, and persists the resulting CanonicalEvent into the
// t.* tables.
//
// Sub 2 is a dispatcher, not a Square parser. The substrate generalizes
// across POS sources (Square, NCR Counterpoint, Clover, future) via
// the SourceAdapter interface in internal/adapters.
//
// Built in GRO-761 (Loop 2 Wave 2).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/adapters"
	"github.com/ruptiv/canary/internal/adapters/clover"
	"github.com/ruptiv/canary/internal/adapters/counterpoint"
	"github.com/ruptiv/canary/internal/adapters/square"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/protocol/sub2"
)

const serviceName = "canary-sub2-parse-route"

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

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("parse valkey url", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	// Registry — every adapter wires here. New POS source = new line
	// here, no other code changes.
	reg := adapters.NewRegistry()
	reg.MustRegister(square.New())
	reg.MustRegister(counterpoint.New())
	reg.MustRegister(clover.New())
	logger.Info("adapters registered", zap.Strings("source_codes", reg.Codes()))

	consumer, _ := os.Hostname()
	if consumer == "" {
		consumer = "sub2-default"
	}

	w := sub2.NewWorker(sub2.WorkerConfig{
		Pool:     pool,
		Redis:    rdb,
		Lookup:   adapters.NewLookup(reg),
		Stream:   sub2.Stream,
		Group:    sub2.ConsumerGroup,
		Consumer: consumer,
		Logger:   logger,
	})

	// Run the consumer in a goroutine so the HTTP server can serve
	// /health concurrently. Signal cancellation drains both.
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- w.Run(ctx)
	}()

	// Minimal HTTP surface — health check only. Sub 2 has no public
	// API; the streams worker is the entire surface.
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", healthHandler(cfg, reg))

	addr := ":" + cfg.Port
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
		zap.String("stream", sub2.Stream),
		zap.String("group", sub2.ConsumerGroup),
		zap.String("consumer", consumer),
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

func healthHandler(cfg *config.Config, reg *adapters.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"service":  cfg.ServiceName,
			"version":  "1.0.0",
			"adapters": reg.Codes(),
		})
	}
}
