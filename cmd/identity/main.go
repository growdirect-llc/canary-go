// cmd/identity/main.go
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/cmdutil"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Two pools: canary_gcp for legacy app.api_keys + app.signing_keys
	// + app.refresh_token_families; canary_identity_gcp for
	// public.persons + public.person_credentials. The dual-pool
	// wiring is the bridge until Sprint 4 (GRO-895) consolidates.
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect (canary_gcp)", zap.Error(err))
	}
	defer pool.Close()

	// last_used_at aggregating writer (GRO-913). Replaces the
	// per-request goroutine fan-out in identity.AuthenticateAPIKey
	// with a single process-level batched flusher.
	closeRecorder := cmdutil.MustLastUsedRecorder(ctx, pool)
	defer closeRecorder()

	identityPool, err := db.Connect(ctx, cfg.IdentityDatabaseURL)
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

	handler := NewServer(pool, identityPool, rdb, cfg, logger)
	addr := ":" + cfg.Port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
	logger.Info("starting", zap.String("service", cfg.ServiceName), zap.String("addr", ln.Addr().String()))
	srv := &http.Server{Handler: handler}
	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server", zap.Error(err))
	}
}
