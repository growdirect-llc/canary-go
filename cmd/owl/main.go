// cmd/owl/main.go
//
// Owl — Canary's merchant intelligence aggregator. Read-only over the
// canonical retail spine (t.*, q.*, m.*, l.*, e.*, app.*) — turns
// sales, cases, and detections into the dashboard a merchant operator
// looks at first thing in the morning.
//
// Loop 2 dispatch (GRO-761 Wave 2). Owl's longer-arc role per
// docs/sdds/go-handoff/owl.md is the AI / MCP intelligence layer
// (chat, personalities, embeddings); that's deferred. Loop 2 ships
// the dashboard surface only — the SQL spine that the AI layer will
// eventually wrap.
//
// Service port: 8084 (per CanaryGo CLAUDE.md → go-module-layout.md).
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/growdirect-llc/rapidpos/internal/owl"
)

const serviceName = "canary-owl"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	store := owl.NewPgxStore(pool)
	aggregator := owl.NewAggregator(store)
	handler := owl.New(aggregator, logger)

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

// requestLogger emits a structured zap line per request — same pattern
// as cmd/gateway/main.go, copied so Owl doesn't grow a middleware
// dependency on the gateway package.
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
