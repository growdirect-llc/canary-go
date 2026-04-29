// cmd/identity/main.go
package main

import (
	"context"
	"net/http"

	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Load("canary-identity")

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("parse valkey url", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	srv := NewServer(pool, rdb, cfg)
	addr := ":" + cfg.Port
	logger.Info("starting", zap.String("service", cfg.ServiceName), zap.String("addr", addr))
	if err := http.ListenAndServe(addr, srv); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}
