// cmd/sub1-hash-seal/main.go
//
// Sub 1 — Hash & Seal worker. Node 3 of the Canary protocol pipeline
// (patent Application 63/991,596). Consumes canonical events from
// Valkey Streams (protocol:events) under consumer group
// "sub1-hash-seal" and writes a row into protocol.evidence — the L1
// Index of the Data Sovereignty Stack (write-once, hash-chained,
// per-merchant).
//
// Built in GRO-748.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/protocol/sub1"
)

const serviceName = "canary-sub1-hash-seal"

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

	consumer, _ := os.Hostname()
	if consumer == "" {
		consumer = "sub1-default"
	}

	w := sub1.NewWorker(sub1.WorkerConfig{
		Pool:     pool,
		Redis:    rdb,
		Stream:   sub1.Stream,
		Group:    sub1.ConsumerGroup,
		Consumer: consumer,
		Logger:   logger,
	})

	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("stream", sub1.Stream),
		zap.String("group", sub1.ConsumerGroup),
		zap.String("consumer", consumer),
	)

	if err := w.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("worker exited", zap.Error(err))
		os.Exit(1)
	}
	logger.Info("shutdown clean")
}
