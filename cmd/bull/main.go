// cmd/bull/main.go
//
// Bull — directed-work task queue + L402-gated billing (Module B).
//
// Task queue (GRO-800): POST /v1/tasks, GET /v1/tasks/next, PATCH status,
// exception, skip — three task types: receiving, replenishment, cycle_count.
//
// Billing (GRO-765): L402 charge cycle + satoshi cost rollup under
// /v1/billing/* (API-key gated).
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/billing"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/growdirect-llc/rapidpos/internal/identity"
	"github.com/growdirect-llc/rapidpos/internal/task"
	"github.com/growdirect-llc/rapidpos/internal/workflow"
)

const serviceName = "canary-bull"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	// Register L402 charge cycle workflow at boot. Idempotent.
	wfStore := workflow.NewStore(pool)
	def, err := workflow.RegisterL402ChargeCycle(context.Background(), wfStore)
	if err != nil {
		logger.Fatal("register l402 charge cycle", zap.Error(err))
	}
	logger.Info("workflow registered",
		zap.String("workflow_code", def.WorkflowCode),
		zap.Int("version", def.Version),
		zap.String("workflow_id", def.ID.String()),
	)

	store := billing.NewStore(pool)
	h := billing.New(store, logger)

	taskStore := task.NewStore(pool)
	taskHandler := task.NewHandler(taskStore, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", health(cfg))

	// Task queue routes — internal service-to-service auth via
	// X-Canary-Internal header (same pattern as gateway internal routes).
	taskHandler.Mount(r)

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
		})
	}
}
