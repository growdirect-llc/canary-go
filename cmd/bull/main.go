// cmd/bull/main.go
//
// Bull — directed-work task queue + L402-gated billing (Module B).
//
// Task queue: POST /v1/tasks, GET /v1/tasks/next, PATCH status,
// exception, skip — three task types: receiving, replenishment, cycle_count.
//
// Replenishment trigger: background goroutine subscribes to
// inventory:replenish stream and creates replenishment tasks via Min/Max.
//
// Billing: L402 charge cycle + satoshi cost rollup under
// /v1/billing/* (API-key gated).
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

	"github.com/ruptiv/canary/internal/billing"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/replenishment"
	"github.com/ruptiv/canary/internal/task"
	"github.com/ruptiv/canary/internal/workflow"
)

const serviceName = "canary-bull"

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

	// Register L402 charge cycle workflow at boot. Idempotent.
	wfStore := workflow.NewStore(pool)
	def, err := workflow.RegisterL402ChargeCycle(ctx, wfStore)
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

	// Background replenishment trigger: inventory:replenish → Min/Max → task.
	trigger := replenishment.NewTrigger(pool, taskStore, valkeyClient, logger)
	go func() {
		if err := trigger.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("replenishment trigger exited", zap.Error(err))
		}
	}()

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", health(cfg))

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
