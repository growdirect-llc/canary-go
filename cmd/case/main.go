// cmd/case/main.go
//
// Case API binary — owns the q.cases / q.case_actions / q.case_evidence
// HTTP surface.
//
// Registers the investigation_lifecycle workflow at boot. Endpoints
// are gated by the API-key middleware (Wave A C.4).
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/casemgmt"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/workflow"
)

const serviceName = "canary-case"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	// Register investigation_lifecycle workflow at boot. Idempotent.
	wfStore := workflow.NewStore(pool)
	def, err := workflow.RegisterInvestigationLifecycle(context.Background(), wfStore)
	if err != nil {
		logger.Fatal("register investigation lifecycle", zap.Error(err))
	}
	logger.Info("workflow registered",
		zap.String("workflow_code", def.WorkflowCode),
		zap.Int("version", def.Version),
	)

	store := casemgmt.NewStore(pool)
	h := casemgmt.New(store, logger)

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
		})
	}
}
