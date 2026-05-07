// cmd/report/main.go
//
// Report — job-dispatch layer for async report generation.
// POST enqueues; GET polls. In-memory job store until app.report_jobs
// is migrated (Wave E schema).
// Tenant-scoped, API-key gated.
//
// Spec: GRO-766 Phase E.
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/obs"
	"github.com/ruptiv/canary/internal/report"
)

const serviceName = "canary-report"

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

	obsLogger := obs.NewLogger(serviceName)
	tracer, err := obs.NewTracer(ctx, serviceName)
	if err != nil {
		logger.Fatal("obs tracer", zap.Error(err))
	}
	defer func() { _ = tracer.Shutdown(context.Background()) }()
	logger = obsLogger

	// In-memory job store — replace with pgx store after app.report_jobs migration.
	store := report.NewStore()
	h := report.New(store, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(obs.Middleware(serviceName))
	r.Use(requestLogger(logger))

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
