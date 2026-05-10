// cmd/fox/main.go
//
// Fox — case-management service for the Canary loss-prevention pipeline.
// Reads detections off q.detections, escalates to q.cases, tracks
// evidence in q.case_evidence and actions in q.case_actions. All q.case_*
// descendant tables are append-only per canonical schema.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/cmdutil"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/fox"
	"github.com/ruptiv/canary/internal/identity"
)

const serviceName = "canary-fox"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	store := fox.NewStore(pool)
	handler := fox.New(store, fox.DefaultEscalationConfig(), logger)

	limiter, closeLimiter := cmdutil.MustValkeyRateLimiter(cfg.ValkeyURL, logger)
	defer closeLimiter()

	closeRecorder := cmdutil.MustLastUsedRecorder(ctx, pool)
	defer closeRecorder()

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg))

	// Case routes require API-key auth — tenant is derived from the
	// resolved claims, never from request body / query input. Rate
	// limit per GRO-912.
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
			Limiter:  limiter,
		}))
		handler.Mount(r)
	})

	addr := ":" + cfg.Port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", ln.Addr().String()),
	)
	srv := cmdutil.NewServer(r)
	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server", zap.Error(err))
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

// requestLogger emits a structured zap line per request — same shape
// as the gateway uses, kept inline to avoid building a shared
// middleware package for a single use.
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
