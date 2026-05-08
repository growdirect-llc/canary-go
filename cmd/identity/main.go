// cmd/identity/main.go
package main

import (
	"context"
	"net/http"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
)

func main() {
	cfg := config.Load("canary-identity")

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	if cfg.IdentityDatabaseURL == "" {
		logger.Fatal("IDENTITY_DATABASE_URL is required for the identity service " +
			"(see Brain/wiki/cards/platform-identity-database-boundary.md)")
	}

	// Two pools: canary_gcp for legacy app.api_keys + app.signing_keys
	// + app.refresh_token_families; canary_identity_gcp for
	// public.persons + public.person_credentials. The dual-pool
	// wiring is the bridge until Sprint 4 (GRO-895) consolidates.
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect (canary_gcp)", zap.Error(err))
	}
	defer pool.Close()

	identityPool, err := db.Connect(context.Background(), cfg.IdentityDatabaseURL)
	if err != nil {
		logger.Fatal("db connect (canary_identity_gcp)", zap.Error(err))
	}
	defer identityPool.Close()

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("parse valkey url", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	srv := NewServer(pool, identityPool, rdb, cfg, logger)
	addr := ":" + cfg.Port
	logger.Info("starting", zap.String("service", cfg.ServiceName), zap.String("addr", addr))
	if err := http.ListenAndServe(addr, srv); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}
