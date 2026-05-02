// cmd/gateway/main.go
//
// API Gateway — Node 2 of the Canary protocol pipeline (patent
// Application 63/991,596). Receives webhook POSTs from source networks,
// validates HMAC-SHA256 signatures against per-(merchant, source)
// secrets, computes payload hashes, and publishes canonical events to
// Valkey Streams for the Triple Subscriber pipeline (GRO-747).
//
// Built in GRO-746.
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/growdirect-llc/rapidpos/internal/protocol/audit"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/secrets"
	"github.com/growdirect-llc/rapidpos/internal/protocol/webhook"
)

const (
	serviceName = "canary-gateway"

	// streamName is the Valkey Stream that the Triple Subscriber pipeline
	// reads from. Single stream, three independent consumer groups (one
	// per subscriber) — see GRO-747.
	streamName = "protocol:events"

	// noncePrefix namespaces nonce keys in Valkey so multiple gateway
	// instances or other services sharing the cluster don't collide.
	noncePrefix = "gateway:nonce"
)

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

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("parse valkey url", zap.Error(err))
	}
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	// Build the protocol-gateway dependency tree.
	resolver := secrets.NewPgxResolver(pool)
	pub := publisher.NewValkey(rdb, streamName)
	nonceStore := publisher.NewValkeyNonceStore(rdb, noncePrefix)

	handler := webhook.New(resolver, pub, nonceStore, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg))

	// Audit middleware records every protocol invocation into
	// app.audit_log. Scoped to protocol routes so /health stays
	// noise-free. GRO-694.
	auditMW := audit.Middleware(audit.Config{
		Inserter:    audit.NewPgxInserter(pool),
		Logger:      logger,
		ServiceName: serviceName,
		ActorType:   "agent",
		Resource:    "protocol.event",
	})
	r.Group(func(r chi.Router) {
		r.Use(auditMW)
		handler.Mount(r)
	})

	addr := ":" + cfg.Port
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", addr),
		zap.String("stream", streamName),
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

// requestLogger is a small middleware that emits a structured zap line
// per request without dragging in chi's verbose default logger.
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
