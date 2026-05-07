// cmd/inventory/main.go
//
// Inventory service. SOH read/write + sale event consumer.
//
// Two goroutines run concurrently:
// 1. HTTP server — position reads, movement appends, cycle-count adjustments
// 2. SaleConsumer — polls transaction_line_items for unlinked sale lines,
// applies inventory movements, emits replenish signals to Valkey
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/inventory"
)

const serviceName = "canary-inventory"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
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

	store := inventory.NewStore(pool)
	handler := inventory.New(store, store, logger)

	// Background sale consumer: unlinked transaction lines → SOH movements.
	consumer := inventory.NewSaleConsumer(pool, store, valkeyClient, logger, 0)
	go func() {
		if err := consumer.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("sale consumer exited", zap.Error(err))
		}
	}()

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg))
	handler.Mount(r)

	addr := ":" + cfg.Port
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
	)
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}

func healthHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"service": cfg.ServiceName,
			"version": "1.0.0",
			"checks":  map[string]string{},
		})
	}
}

// requestLogger emits a structured zap line per request without dragging
// in chi's verbose default logger. Mirrors cmd/gateway/main.go.
func requestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
			)
		})
	}
}
