// cmd/chirp/main.go
//
// Chirp — Module Q rules engine. Loads detection rules from
// q.detection_rules, evaluates them against transaction events from
// schema t, writes matched detections to q.detections.
//
// Built in GRO-761 Loop 2 Wave 2.
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/chirp"
	"github.com/ruptiv/canary/internal/chirp/rules"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
)

const serviceName = "canary-chirp"

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

	store := chirp.NewPgxStore(pool)
	registry := chirp.NewRegistry()
	registerBaseline(registry, logger)

	engine := chirp.NewEngine(store, registry, logger)
	handler := chirp.NewHandler(engine, store, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg, registry))
	handler.Mount(r)

	addr := ":" + cfg.Port
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
		zap.Strings("rule_types", registry.RegisteredTypes()),
	)
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}

// registerBaseline wires the seven Loop 2 wave 2 evaluators.
func registerBaseline(r *chirp.Registry, logger *zap.Logger) {
	for _, e := range []chirp.RuleEvaluator{
		rules.VoidThreshold{},
		rules.NoSaleFrequency{},
		rules.RefundNoReceipt{},
		rules.ManagerOverrideFrequency{},
		rules.AfterHoursTransaction{},
		rules.LargeDiscountPct{},
		rules.CashDrawerVariance{},
	} {
		if r.Register(e) {
			logger.Warn("evaluator replaced", zap.String("rule_type", e.RuleType()))
		}
	}
}

func healthHandler(cfg *config.Config, registry *chirp.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         true,
			"service":    cfg.ServiceName,
			"version":    "1.0.0",
			"rule_types": registry.RegisteredTypes(),
			"checks":     map[string]string{},
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
