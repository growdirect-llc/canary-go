// cmd/gateway/main.go
//
// API Gateway — Node 2 of the Canary protocol pipeline (patent
// Application 63/991,596). Receives webhook POSTs from source networks,
// validates HMAC-SHA256 signatures against per-(merchant, source)
// secrets, computes payload hashes, and publishes canonical events to
// Valkey Streams for the Triple Subscriber pipeline.
package main

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	alertPkg "github.com/ruptiv/canary/internal/alert"
	analyticsPkg "github.com/ruptiv/canary/internal/analytics"
	assetPkg "github.com/ruptiv/canary/internal/asset"
	billingPkg "github.com/ruptiv/canary/internal/billing"
	casemgmtPkg "github.com/ruptiv/canary/internal/casemgmt"
	chirpPkg "github.com/ruptiv/canary/internal/chirp"
	"github.com/ruptiv/canary/internal/cmdutil"
	customerPkg "github.com/ruptiv/canary/internal/customer"
	"github.com/ruptiv/canary/internal/devops"
	employeePkg "github.com/ruptiv/canary/internal/employee"
	hierarchyPkg "github.com/ruptiv/canary/internal/hierarchy"
	lpPkg "github.com/ruptiv/canary/internal/lp"
	owlPkg "github.com/ruptiv/canary/internal/owl"
	poPkg "github.com/ruptiv/canary/internal/po"
	reportPkg "github.com/ruptiv/canary/internal/report"
	returnsPkg "github.com/ruptiv/canary/internal/returns"
	supplierPkg "github.com/ruptiv/canary/internal/supplier"
	taskPkg "github.com/ruptiv/canary/internal/task"
	"github.com/ruptiv/canary/internal/web"
	webdevops "github.com/ruptiv/canary/internal/web/devops"
	workflowPkg "github.com/ruptiv/canary/internal/workflow"

	"github.com/ruptiv/canary/internal/auth/lnurl"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/identity/jwks"
	"github.com/ruptiv/canary/internal/identity/keystore"
	"github.com/ruptiv/canary/internal/manifest/routewalk"
	"github.com/ruptiv/canary/internal/mcp"
	"github.com/ruptiv/canary/internal/protocol/anchor"
	"github.com/ruptiv/canary/internal/protocol/audit"
	"github.com/ruptiv/canary/internal/protocol/evidence"
	"github.com/ruptiv/canary/internal/protocol/namespace"
	"github.com/ruptiv/canary/internal/protocol/publisher"
	"github.com/ruptiv/canary/internal/protocol/secrets"
	"github.com/ruptiv/canary/internal/protocol/sub3"
	"github.com/ruptiv/canary/internal/protocol/validate"
	"github.com/ruptiv/canary/internal/protocol/webhook"
	"github.com/ruptiv/canary/internal/squareauth"
	domainwebhook "github.com/ruptiv/canary/internal/webhook"
)

const (
	serviceName = "canary-gateway"

	// streamName is the Valkey Stream that the Triple Subscriber pipeline
	// reads from. Single stream, three independent consumer groups (one
	// per subscriber) —
	streamName = "protocol:events"

	// noncePrefix namespaces nonce keys in Valkey so multiple gateway
	// instances or other services sharing the cluster don't collide.
	noncePrefix = "gateway:nonce"
)

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

	// JWT signing keystore + JWKS publication (T-2 / GRO-862).
	// Reads from app.signing_keys; bootstraps a dev key on cold
	// start when the table is empty. Production fatals on empty —
	// keys must be published via the rotation runbook, never
	// auto-generated.
	keyStore := keystore.New(pool)
	keystore.BootstrapDevKeyIfEmpty(ctx, keyStore, logger)
	jwksHandler := jwks.New(keyStore, logger)

	// Auth surfaces (/auth/login, /auth/refresh, /v1/me) live on the
	// identity binary, not the gateway, per the platform-identity-
	// database-boundary card. Gateway only publishes the JWKS public
	// key set; identity owns mint + verify.

	// .jeffe namespace registration — Node identity layer.
	// ORDINALSBOT_API_KEY env var selects real vs stub inscriber.
	inscriber := sub3.NewOrdinalsBot(os.Getenv("ORDINALSBOT_API_KEY"), "signet")
	nsHandler := namespace.New(pool, inscriber, logger)

	// L402 sat-gated Validation API — revenue surface.
	// VALIDATOR_SECRET: 32-byte hex key for stub L402 HMAC. If absent,
	// a random key is generated at startup (stub mode, not production-safe).
	// VALIDATOR_SATOSHI_PRICE: sat price per proof (default 100).
	validateHandler := buildValidateHandler(pool, logger)

	// LNURL-auth login surface — Lightning wallet QR login.
	// LNURL_JWT_SECRET: 64-char hex key for HS256 session JWTs. If absent,
	// a random key is generated (ephemeral, dev only).
	// LNURL_STUB: set to "true" to skip secp256k1 signature verification
	// (CI/signet mode).
	lnurlHandler := buildLNURLHandler(pool, logger)

	// Square OAuth demo flow. Anthropic-facing demo:
	// connect Square sandbox, see merchant data live. Routes /, /auth/square,
	// /auth/square/callback, /dashboard, /auth/square/disconnect.
	// Requires SQUARE_APPLICATION_ID, SQUARE_APPLICATION_SECRET, SQUARE_REDIRECT_URI.
	squareSvc := squareauth.New(pool, logger)

	// /v1/webhooks/* — admin endpoints under API-key auth.
	dlq := domainwebhook.NewDLQ(pool)
	admin := newAdminHandlers(dlq, pub)

	// Build MCP tool registry over the 7 Wave D module stores.
	mcpRegistry := mcp.NewRegistry()
	mcp.RegisterAlertTools(mcpRegistry, alertPkg.NewStore(pool))
	mcp.RegisterAnalyticsTools(mcpRegistry, analyticsPkg.NewStore(pool))
	mcp.RegisterAssetTools(mcpRegistry, assetPkg.NewStore(pool))
	mcp.RegisterCustomerTools(mcpRegistry, customerPkg.NewStore(pool))
	mcp.RegisterEmployeeTools(mcpRegistry, employeePkg.NewStore(pool))
	mcp.RegisterReturnsTools(mcpRegistry, returnsPkg.NewStore(pool))
	mcp.RegisterReportTools(mcpRegistry, reportPkg.NewPgxStore(pool))
	// GRO-936: every tools/call lands a row in app.audit_log via the
	// same PgxInserter the webhook + admin surfaces use. The adapter
	// maps mcp.AuditEvent → audit.Entry.
	mcpAuditRecorder := newMCPAuditRecorder(audit.NewPgxInserter(pool), serviceName)
	mcpHandler := mcp.NewWithAudit(mcpRegistry, mcpAuditRecorder, logger)

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/health", healthHandler(cfg))
	r.Get("/.well-known/mcp.json", discoveryHandler(cfg))

	// JWKS — public key set for JWT verification. T-2 / GRO-862.
	// Mounted at the IANA-registered /.well-known/jwks.json path
	// (RFC 5785 + RFC 7517) so AtlasView and other consumers can
	// auto-discover. Reads from the rotation-aware keystore.
	jwksHandler.Mount(r)

	// /v1/me + /auth/refresh + /auth/login moved to the identity
	// binary per the platform-identity-database-boundary card.

	// Bilateral verification APIs — read-only, mounted outside the
	// audit group. Reads don't need state-mutation audit semantics.
	//
	evidenceHandler.Mount(r)
	anchorHandler.Mount(r)

	// .jeffe namespace — GET (lookup) public, POST (register)
	// gated below behind APIKeyMiddleware (T-C / GRO-849: prevents
	// spoofed registrations claiming someone else's owner_id).
	// The POST writes one row but carries its own payload_hash +
	// inscription_id as the audit trail.
	nsHandler.MountPublic(r)

	// L402 sat-gated verification — POST issues challenge, GET consumes.
	// Mounted outside audit group; the payment record IS the audit trail.
	validateHandler.Mount(r)

	// LNURL-auth login — wallet QR challenge/response + JWT session.
	// Mounted outside audit group; Lightning wallet calls are read-only
	// from an audit perspective until the session is established.
	lnurlHandler.Mount(r)

	// Square OAuth demo. Mounted outside audit group; OAuth
	// state is the auth mechanism, no API-key gating.
	squareSvc.Mount(r)

	// Audit middleware records every state-mutating protocol invocation
	// into app.audit_log. Scoped to webhook routes so /health and
	// read-only /v1/protocol/evidence/* stay noise-free.
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

	limiter := cmdutil.MustValkeyRateLimiterFromClient(rdb)
	closeRecorder := cmdutil.MustLastUsedRecorder(ctx, pool)
	defer closeRecorder()
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
			Limiter:  limiter,
		}))
		r.Use(auditMW)
		admin.Mount(r)

		// T-C: POST /v1/protocol/namespace requires
		// API-key auth + tenant-match on owner_id. Mounted here so
		// it inherits the same APIKeyMiddleware + audit pair as the
		// admin endpoints.
		nsHandler.MountProtected(r)
	})

	// POST /mcp — MCP JSON-RPC 2.0 endpoint. API-key auth, tenant-scoped.
	// 26 tools across 7 domain modules.
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
			Limiter:  limiter,
		}))
		mcpHandler.Mount(r)
	})

	// /devops — pipeline monitor + API explorer. Dev-only (DEV_CONSOLE=1).
	devops.New(pool, rdb, logger, squareSvc).Mount(r)

	// /devops/<service> — sysadmin module shell.
	// Owns the catalog/manifest/observability/pipeline/qa-agent service
	// pages. Routes don't collide with the dev console's specific paths
	// (square / api / releases / static); Phase 3 folds those legacy
	// pages into the new shell.
	if sysadmin, err := webdevops.New(logger); err == nil {
		sysadmin.Mount(r)
	} else {
		logger.Warn("sysadmin module disabled", zap.Error(err))
	}

	// / — Canary application UI.
	webDeps := web.Deps{
		AlertStore:       alertPkg.NewStore(pool),
		CaseStore:        casemgmtPkg.NewStore(pool),
		ChirpStore:       chirpPkg.NewPgxStore(pool),
		CustomerStore:    customerPkg.NewStore(pool),
		SubstrateStore:   lpPkg.NewSubstrateStore(pool),
		AllowListStore:   lpPkg.NewAllowListStore(pool),
		OwlDashboard:     owlPkg.NewDashboardStore(pool),
		TaskStore:        taskPkg.NewStore(pool),
		BillingStore:     billingPkg.NewStore(pool),
		WorkflowStore:    workflowPkg.NewStore(pool),
		AssetStore:       assetPkg.NewStore(pool),
		AuditReader:      audit.NewPgxInserter(pool),
		HierarchyStore:   hierarchyPkg.NewStore(pool),
		SupplierStore:    supplierPkg.NewStore(pool),
		POStore:          poPkg.NewStore(pool),
		MerchantResolver: squareSvc.MerchantFromRequest,
	}
	// T-E: wrap the merchant UI in CSRF + body-size caps.
	// CSRF runs the gorilla synchronizer-token pattern (signed,
	// HttpOnly cookie + per-form hidden field). Body cap fails fast
	// at 64 KiB on POST/PUT/PATCH. Both apply to the entire web tree
	// — public routes (/, /connect, /welcome, /errors/*) get a CSRF
	// token planted on first GET so subsequent forms have one.
	csrfKey := buildCSRFKey(logger)
	csrfMW := csrf.Protect(
		csrfKey,
		csrf.Secure(os.Getenv("ENV") == "production"),
		csrf.SameSite(csrf.SameSiteLaxMode),
		csrf.HttpOnly(true),
		csrf.Path("/"),
		csrf.FieldName("csrf_token"),
		csrf.CookieName("__Host-csrf"),
	)
	r.Group(func(r chi.Router) {
		r.Use(web.MaxBytesMiddleware(64 * 1024))
		r.Use(csrfMW)
		web.New(webDeps, logger).Mount(r)
	})

	// MANIFEST_ROUTEWALK=1 — emit build/routes-seen.json then continue
	// boot. Pure observation; consumed by the manifest reconciler to
	// detect drift against manifest.yaml + openapi.yaml.
	if os.Getenv("MANIFEST_ROUTEWALK") == "1" {
		out := os.Getenv("MANIFEST_ROUTEWALK_OUT")
		if err := routewalk.Walk(r, serviceName, out); err != nil {
			logger.Warn("routewalk failed", zap.Error(err))
		} else {
			path := out
			if path == "" {
				path = routewalk.DefaultOutPath
			}
			logger.Info("routewalk emitted", zap.String("output", path))
		}
	}

	addr := ":" + cfg.Port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", ln.Addr().String()),
		zap.String("stream", streamName),
	)
	srv := &http.Server{Handler: r}
	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server", zap.Error(err))
	}
}

// discoveryHandler serves GET /.well-known/mcp.json — a public document that
// lets any MCP client (Claude Code, a partner service, an agent) discover the
// tool surface, endpoint, and auth scheme without reading docs.
//
// PUBLIC_URL env var sets the base URL (e.g. https://demo.growdirect.io).
// If unset, the handler derives it from the incoming request Host header.
func discoveryHandler(cfg *config.Config) http.HandlerFunc {
	const (
		mcpVersion  = "2025-03-26"
		toolsCount  = 28
		openAPIRepo = "https://raw.githubusercontent.com/ruptiv/canary/main/services/canary-protocol/openapi/openapi.yaml"
	)
	modules := []string{"alert", "analytics", "asset", "customer", "employee", "returns", "report"}

	return func(w http.ResponseWriter, r *http.Request) {
		base := cfg.PublicURL
		if base == "" {
			scheme := "https"
			if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
				scheme = "http"
			}
			base = scheme + "://" + r.Host
		}

		doc := map[string]any{
			"mcp_version": mcpVersion,
			"name":        "Canary Retail Ops",
			"description": "Store operations platform for independent retailers. " +
				"28 tools across 7 domain modules (alert, analytics, asset, customer, employee, returns, report).",
			"endpoint":  base + "/mcp",
			"transport": "http-post",
			"auth": map[string]any{
				"type":        "api_key",
				"header":      identity.HeaderAPIKey,
				"description": "Tenant-scoped API key. Contact the platform operator to obtain one.",
				"example_request": map[string]any{
					"method": "POST",
					"path":   "/mcp",
					"headers": map[string]string{
						identity.HeaderAPIKey: "cy_<your-key>",
						"Content-Type":        "application/json",
					},
					"body": map[string]any{
						"jsonrpc": "2.0",
						"id":      1,
						"method":  "tools/list",
						"params":  map[string]any{},
					},
				},
			},
			"modules":     modules,
			"tools_count": toolsCount,
			"openapi":     openAPIRepo,
			"links": map[string]string{
				"vault": "https://canary.growdirect.io",
				"sdds":  "https://canary.growdirect.io/sdds/",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(doc)
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

// buildCSRFKey loads the CSRF auth key from CSRF_SECRET (32-byte hex).
// Production requires it; dev generates an ephemeral key + warns.
// gorilla/csrf signs the per-session token cookie with this key — if it
// changes between restarts, all in-flight tokens become invalid (users
// must re-fetch a form GET before POSTing). T-E.
func buildCSRFKey(logger *zap.Logger) []byte {
	isProd := os.Getenv("ENV") == "production"
	key := make([]byte, 32)
	keyHex := os.Getenv("CSRF_SECRET")
	if keyHex != "" {
		decoded, err := hex.DecodeString(keyHex)
		if err != nil || len(decoded) != 32 {
			if isProd {
				logger.Fatal("CSRF_SECRET invalid in production; must be 64-char hex (32 bytes)",
					zap.Error(err))
			}
			logger.Warn("CSRF_SECRET invalid; generating random key (dev fallback)")
		} else {
			copy(key, decoded)
			return key
		}
	}
	if isProd {
		logger.Fatal("CSRF_SECRET required in production (ENV=production); set to a 64-char hex string")
	}
	_, _ = cryptoRand.Read(key)
	logger.Warn("CSRF_SECRET not set; using ephemeral random key (dev only)")
	return key
}

// buildValidateHandler constructs the L402 validation handler from env vars.
//
// VALIDATOR_SECRET: 32-byte hex key. In dev (ENV != "production") an
// invalid or absent value generates an ephemeral random key with a
// warning. In production (ENV=production) the absence or invalidity
// is fatal — the L402 HMAC must be deterministic so peer verification
// can succeed across restarts.
// VALIDATOR_SATOSHI_PRICE: satoshi price per proof verification (default 100).
func buildValidateHandler(pool *pgxpool.Pool, logger *zap.Logger) *validate.Handler {
	isProd := os.Getenv("ENV") == "production"
	secret := make([]byte, 32)
	secretHex := os.Getenv("VALIDATOR_SECRET")
	if secretHex != "" {
		decoded, err := hex.DecodeString(secretHex)
		if err != nil || len(decoded) != 32 {
			if isProd {
				logger.Fatal("VALIDATOR_SECRET invalid in production; must be 64-char hex (32 bytes)",
					zap.Error(err))
			}
			logger.Warn("VALIDATOR_SECRET invalid; generating random key (stub mode)",
				zap.String("hint", "set VALIDATOR_SECRET to a 64-char hex string for production"))
		} else {
			copy(secret, decoded)
		}
	} else {
		if isProd {
			logger.Fatal("VALIDATOR_SECRET required in production (ENV=production); set to a 64-char hex string")
		}
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

	// GRO-932: GET /v1/protocol/verify/{token_id} requires an L402
	// Authorization header by default. Setting L402_ALLOW_UNAUTH_CONSUME=true
	// lets dev/CI environments keep using the stub flow (POST → 402 →
	// GET without an auth header); production is fatal-guarded so the
	// flag cannot ship live.
	allowUnauth := os.Getenv("L402_ALLOW_UNAUTH_CONSUME") == "true"
	if allowUnauth && isProd {
		logger.Fatal("L402_ALLOW_UNAUTH_CONSUME=true is forbidden in production",
			zap.String("hint", "leave the env var unset and pass an L402 Authorization header on GET /v1/protocol/verify/{token_id}"))
	}
	if allowUnauth {
		logger.Warn("L402 unauthenticated GET consume enabled (dev/CI only)")
	}

	return &validate.Handler{
		Store:                       validate.NewPgxStore(pool),
		L402:                        &validate.StubL402{Secret: secret},
		Logger:                      logger,
		SatoshiPrice:                price,
		AllowUnauthenticatedConsume: allowUnauth,
	}
}

// buildLNURLHandler constructs the LNURL-auth handler from env vars.
//
// LNURL_JWT_SECRET: 64-char hex key for HS256 session JWTs. Required
// in production (ENV=production). In dev, an invalid/absent value
// generates an ephemeral random 32-byte key with a warning.
// LNURL_STUB: "true" skips secp256k1 signature verification (CI/signet).
// FATAL in production — signature verification cannot be disabled
// against real wallet traffic.
// LNURL_SCHEME: "http" or "https" (default "https"). Must be "https"
// in production — http leaks the auth k1 nonce in transit.
// LNURL_HOST: hostname[:port] for callback URLs (default "localhost:8080").
func buildLNURLHandler(pool *pgxpool.Pool, logger *zap.Logger) *lnurl.Handler {
	isProd := os.Getenv("ENV") == "production"
	secret := make([]byte, 32)
	secretHex := os.Getenv("LNURL_JWT_SECRET")
	if secretHex != "" {
		decoded, err := hex.DecodeString(secretHex)
		if err != nil || len(decoded) != 32 {
			if isProd {
				logger.Fatal("LNURL_JWT_SECRET invalid in production; must be 64-char hex (32 bytes)",
					zap.Error(err))
			}
			logger.Warn("LNURL_JWT_SECRET invalid; generating ephemeral random key",
				zap.String("hint", "set LNURL_JWT_SECRET to a 64-char hex string for production"))
		} else {
			copy(secret, decoded)
		}
	} else {
		if isProd {
			logger.Fatal("LNURL_JWT_SECRET required in production (ENV=production); set to a 64-char hex string")
		}
		_, _ = cryptoRand.Read(secret)
		logger.Warn("LNURL_JWT_SECRET not set; using ephemeral random key (dev only)")
	}

	stub := os.Getenv("LNURL_STUB") == "true"
	if isProd && stub {
		logger.Fatal("LNURL_STUB=true is not permitted in production; signature verification cannot be disabled against real wallet traffic")
	}

	scheme := os.Getenv("LNURL_SCHEME")
	if scheme == "" {
		scheme = "https"
	}
	if isProd && scheme != "https" {
		logger.Fatal("LNURL_SCHEME must be \"https\" in production",
			zap.String("got", scheme))
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

// mcpAuditRecorder bridges mcp.AuditRecorder to the existing
// audit.Inserter so MCP rows land in the same app.audit_log table as
// the webhook + admin surfaces (GRO-936). The action column carries
// the MCP tool name so log queries can pivot on it without parsing
// the path; tool_name is also populated for explicit MCP queries.
type mcpAuditRecorder struct {
	inserter    audit.Inserter
	serviceName string
}

func newMCPAuditRecorder(inserter audit.Inserter, serviceName string) *mcpAuditRecorder {
	return &mcpAuditRecorder{inserter: inserter, serviceName: serviceName}
}

func (r *mcpAuditRecorder) Record(ctx context.Context, e mcp.AuditEvent) error {
	entry := audit.Entry{
		Action:        "POST /mcp " + e.ToolName,
		Resource:      "mcp.tool_call",
		PayloadDigest: e.ArgsDigest,
		RequestID:     e.RequestID,
		// Status string lives in mcp.AuditEvent because JSON-RPC
		// errors are not HTTP status codes; we surface "ok" or the
		// rpc error code in the user_agent column-adjacent slot via
		// SourceCode so dashboards can pivot without a join.
		SourceCode: e.Status,
		LatencyMS:  e.LatencyMS,
		ActorType:  "agent",
		MCPServer:  r.serviceName,
		ToolName:   e.ToolName,
	}
	if e.TenantID != uuid.Nil {
		mid := e.TenantID
		entry.MerchantID = &mid
	}
	if e.KeyID != uuid.Nil {
		kid := e.KeyID
		entry.ResourceID = &kid
	}
	return r.inserter.Insert(ctx, entry)
}
