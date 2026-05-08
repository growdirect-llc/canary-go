// cmd/edge/main.go
//
// Canary Edge — on-premise agent that runs alongside NCR Counterpoint
// and SQL Server. No inbound HTTP port. Polls the Counterpoint REST API
// every 60 seconds and publishes sale document events to the Canary
// protocol pipeline via Valkey Streams (protocol:events).
//
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
	"github.com/ruptiv/canary/internal/poller"
	"github.com/ruptiv/canary/internal/protocol/publisher"
)

const serviceName = "canary-edge"

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	cfg := config.Load(serviceName)

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	opt, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("valkey url parse", zap.Error(err))
	}
	valkeyClient := redis.NewClient(opt)
	defer func() { _ = valkeyClient.Close() }()

	pub := publisher.NewValkey(valkeyClient, "protocol:events")

	p := poller.New(poller.Config{
		Pool:      pool,
		Valkey:    valkeyClient,
		Publisher: pub,
	}, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("canary-edge starting", zap.String("service", serviceName))

	if err := p.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("poller exited with error", zap.Error(err))
		os.Exit(1)
	}
}
