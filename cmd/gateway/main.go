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
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	alertPkg     "github.com/growdirect-llc/rapidpos/internal/alert"
	analyticsPkg "github.com/growdirect-llc/rapidpos/internal/analytics"
	assetPkg     "github.com/growdirect-llc/rapidpos/internal/asset"
	customerPkg  "github.com/growdirect-llc/rapidpos/internal/customer"
	employeePkg  "github.com/growdirect-llc/rapidpos/internal/employee"
	reportPkg    "github.com/growdirect-llc/rapidpos/internal/report"
	returnsPkg   "github.com/growdirect-llc/rapidpos/internal/returns"

	"github.com/growdirect-llc/rapidpos/internal/auth/lnurl"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/growdirect-llc/rapidpos/internal/identity"
	"github.com/growdirect-llc/rapidpos/internal/mcp"
	"github.com/growdirect-llc/rapidpos/internal/protocol/anchor"
	"github.com/growdirect-llc/rapidpos/internal/protocol/audit"
	"github.com/growdirect-llc/rapidpos/internal/protocol/evidence"
	"github.com/growdirect-llc/rapidpos/internal/protocol/namespace"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/secrets"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
	"github.com/growdirect-llc/rapidpos/internal/protocol/webhook"
	domainwebhook "github.com/growdirect-llc/rapidpos/internal/webhook"
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
	//
	// Secret backend is selected by the SECRET_BACKEND env var.
	//   - "pgx" (default): plaintext column lookup. Dev only.
	//   - "sm" : GCP Secret Manager-backed. Required for production.
	//
	// In "sm" mode we also need GCP_PROJECT_ID. If SmResolver fails
	// to construct (e.g., ADC not configured locally), we log a warn
	// and fall back to PgxResolver so a developer without GCP creds
	// can still boot the gateway. Production deployments fail fast
	// by setting SECRET_BACKEND_REQUIRE_SM=1.
	resolver := buildResolver(ctx, pool, logger)
	pub := publisher.NewValkey(rdb, streamName)
	nonceStore := publisher.NewValkeyNonceStore(rdb, noncePrefix)

	handler := webhook.New(resolver, pub, nonceStore, logger)
	evidenceHandler := evidence.New(pool, logger)
	anchorHandler := anchor.New(pool, logger)

	// .jeffe namespace registration — Node identity layer.
	// ORDINALSBOT_API_KEY env var selects real vs stub inscriber.
	// GRO-751.
	inscriber := sub3.NewOrdinalsBot(os.Getenv("ORDINALSBOT_API_KEY"), "signet")
	nsHandler := namespace.New(pool, inscriber, logger)

	// L402 sat-gated Validation API — revenue surface. GRO-752.
	// VALIDATOR_SECRET: 32-byte hex key for stub L402 HMAC. If absent,
	// a random key is generated at startup (stub mode, not production-safe).
	// VALIDATOR_SATOSHI_PRICE: sat price per proof (default 100).
	validateHandler := buildValidateHandler(pool, logger)

	// LNURL-auth login surface — Lightning wallet QR login. GRO-753.
	// LNURL_JWT_SECRET: 64-char hex key for HS256 session JWTs. If absent,
	// a random key is generated (ephemeral, dev only).
	// LNURL_STUB: set to "true" to skip secp256k1 signature verification
	// (CI/signet mode).
	lnurlHandler := buildLNURLHandler(pool, logger)

	// /v1/webhooks/* — admin endpoints under API-key auth.
	// GRO-764 Phase A.3 (folds part of GRO-642).
	dlq := domainwebhook.NewDLQ(pool)
	admin := newAdminHandlers(dlq, pub)

	// Build MCP tool registry over the 7 Wave D module stores. GRO-767.
	mcpRegistry := mcp.NewRegistry()
	mcp.RegisterAlertTools(mcpRegistry, alertPkg.NewStore(pool))
	mcp.RegisterAnalyticsTools(mcpRegistry, analyticsPkg.NewStore(pool))
	mcp.RegisterAssetTools(mcpRegistry, assetPkg.NewStore(pool))
	mcp.RegisterCustomerTools(mcpRegistry, customerPkg.NewStore(pool))
	mcp.RegisterEmployeeTools(mcpRegistry, employeePkg.NewStore(pool))
	mcp.RegisterReturnsTools(mcpRegistry, returnsPkg.NewStore(pool))
	mcp.RegisterReportTools(mcpRegistry, reportPkg.NewPgxStore(pool))
	mcpHandler := mcp.New(mcpRegistry)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg))

	// Bilateral verification APIs — read-only, mounted outside the
	// audit group. Reads don't need state-mutation audit semantics.
	// GRO-748 (evidence) · GRO-750 (anchor / Merkle proof).
	evidenceHandler.Mount(r)
	anchorHandler.Mount(r)

	// .jeffe namespace — POST (register) + GET (lookup).
	// Mounted outside the audit group; the POST writes one row but
	// carries its own payload_hash + inscription_id as the audit
	// trail. GRO-751.
	nsHandler.Mount(r)

	// L402 sat-gated verification — POST issues challenge, GET consumes.
	// Mounted outside audit group; the payment record IS the audit trail.
	// GRO-752.
	validateHandler.Mount(r)

	// LNURL-auth login — wallet QR challenge/response + JWT session.
	// Mounted outside audit group; Lightning wallet calls are read-only
	// from an audit perspective until the session is established. GRO-753.
	lnurlHandler.Mount(r)

	// Audit middleware records every state-mutating protocol invocation
	// into app.audit_log. Scoped to webhook routes so /health and
	// read-only /v1/protocol/evidence/* stay noise-free. GRO-694.
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

	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
		}))
		r.Use(auditMW)
		admin.Mount(r)
	})

	// POST /mcp — MCP JSON-RPC 2.0 endpoint. API-key auth, tenant-scoped.
	// 26 tools across 7 domain modules. GRO-767.
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
		}))
		mcpHandler.Mount(r)
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

// buildResolver picks a secrets backend based on env vars and returns
// a Resolver. Default is PgxResolver for dev; production sets
// SECRET_BACKEND=sm + GCP_PROJECT_ID. If SECRET_BACKEND_REQUIRE_SM=1
// is set, a failure to construct SmResolver is fatal — used in
// production deployments to prevent silent fallback to plaintext.
func buildResolver(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) secrets.Resolver {
	backend := os.Getenv("SECRET_BACKEND")
	if backend == "" {
		backend = "pgx"
	}
	if backend != "sm" {
		logger.Info("secrets backend",
			zap.String("backend", "pgx"),
			zap.String("note", "plaintext column lookup; dev only"),
		)
		return secrets.NewPgxResolver(pool)
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		if os.Getenv("SECRET_BACKEND_REQUIRE_SM") == "1" {
			logger.Fatal("SECRET_BACKEND=sm requires GCP_PROJECT_ID")
		}
		logger.Warn("SECRET_BACKEND=sm but GCP_PROJECT_ID is empty; falling back to pgx")
		return secrets.NewPgxResolver(pool)
	}

	smResolver, err := secrets.NewSmResolver(ctx, pool, projectID, secrets.WithLogger(logger))
	if err != nil {
		if os.Getenv("SECRET_BACKEND_REQUIRE_SM") == "1" {
			logger.Fatal("SmResolver construct failed", zap.Error(err))
		}
		logger.Warn("SmResolver construct failed; falling back to pgx",
			zap.Error(err),
		)
		return secrets.NewPgxResolver(pool)
	}
	logger.Info("secrets backend",
		zap.String("backend", "sm"),
		zap.String("project_id", projectID),
	)
	return smResolver
}

// buildValidateHandler constructs the L402 validation handler from env vars.
//
// VALIDATOR_SECRET: 32-byte hex key. If absent or invalid, a random key
// is generated and a warning is logged (stub/dev mode only).
// VALIDATOR_SATOSHI_PRICE: satoshi price per proof verification (default 100).
func buildValidateHandler(pool *pgxpool.Pool, logger *zap.Logger) *validate.Handler {
	secret := make([]byte, 32)
	secretHex := os.Getenv("VALIDATOR_SECRET")
	if secretHex != "" {
		decoded, err := hex.DecodeString(secretHex)
		if err != nil || len(decoded) != 32 {
			logger.Warn("VALIDATOR_SECRET invalid; generating random key (stub mode)",
				zap.String("hint", "set VALIDATOR_SECRET to a 64-char hex string for production"))
		} else {
			copy(secret, decoded)
		}
	} else {
		_, _ = cryptoRand.Read(secret)
		logger.Warn("VALIDATOR_SECRET not set; using ephemeral random key (stub mode only)")
	}

	price := int64(100)
	if priceStr := os.Getenv("VALIDATOR_SATOSHI_PRICE"); priceStr != "" {
		var p int64
		if _, err := fmt.Sscanf(priceStr, "%d", &p); err == nil && p > 0 {
			price = p
		}
	}

	return &validate.Handler{
		Store:        validate.NewPgxStore(pool),
		L402:         &validate.StubL402{Secret: secret},
		Logger:       logger,
		SatoshiPrice: price,
	}
}

// buildLNURLHandler constructs the LNURL-auth handler from env vars.
//
// LNURL_JWT_SECRET: 64-char hex key for HS256 session JWTs. If absent or
// invalid, a random 32-byte key is generated (ephemeral, dev only).
// LNURL_STUB: "true" skips secp256k1 signature verification (CI/signet).
// LNURL_SCHEME: "http" or "https" (default "https").
// LNURL_HOST: hostname[:port] for callback URLs (default "localhost:8080").
func buildLNURLHandler(pool *pgxpool.Pool, logger *zap.Logger) *lnurl.Handler {
	secret := make([]byte, 32)
	secretHex := os.Getenv("LNURL_JWT_SECRET")
	if secretHex != "" {
		decoded, err := hex.DecodeString(secretHex)
		if err != nil || len(decoded) != 32 {
			logger.Warn("LNURL_JWT_SECRET invalid; generating ephemeral random key",
				zap.String("hint", "set LNURL_JWT_SECRET to a 64-char hex string for production"))
		} else {
			copy(secret, decoded)
		}
	} else {
		_, _ = cryptoRand.Read(secret)
		logger.Warn("LNURL_JWT_SECRET not set; using ephemeral random key (dev only)")
	}

	stub := os.Getenv("LNURL_STUB") == "true"

	scheme := os.Getenv("LNURL_SCHEME")
	if scheme == "" {
		scheme = "https"
	}
	host := os.Getenv("LNURL_HOST")
	if host == "" {
		host = "localhost:8080"
	}

	return lnurl.NewHandler(pool, secret, stub, scheme, host, logger)
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
