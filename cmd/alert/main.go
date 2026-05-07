// cmd/alert/main.go
//
// Alert — detection lifecycle surface. Exposes q.detections as
// operator-facing alerts with lifecycle transitions (ack / resolve /
// suppress) and rule-category stats.
//
// Spec: GRO-766 Phase A.1.
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/alert"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
)

const serviceName = "canary-alert"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	store := alert.NewStore(pool)
	h := alert.New(store, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", health(cfg))

	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
		}))
		h.Mount(r)
	})

	addr := ":" + cfg.Port
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
	)
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}

func health(cfg *config.Config) http.HandlerFunc {
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
