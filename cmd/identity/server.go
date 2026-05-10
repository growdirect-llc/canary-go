// cmd/identity/server.go
package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/cmdutil"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/identity/auth"
	"github.com/ruptiv/canary/internal/identity/jwks"
	"github.com/ruptiv/canary/internal/identity/keystore"
	"github.com/ruptiv/canary/internal/identity/me"
	"github.com/ruptiv/canary/internal/identity/mint"
	"github.com/ruptiv/canary/internal/identity/refreshfamily"
	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// NewServer wires the Chi router and all routes. Accepts injected
// dependencies so tests can pass test DBs and Valkey client.
//
//	pool         — canary_gcp (legacy app.api_keys, app.signing_keys,
//	               app.refresh_token_families)
//	identityPool — canary_identity_gcp (public.persons, public.person_credentials)
//
// The /v1/identity/* group is mounted under APIKeyMiddleware so
// every key lifecycle and whoami call is authenticated. /health and
// /sessions/validate stay open (legacy + readiness probe).
//
// Auth surfaces (/auth/login, /auth/refresh, /v1/me, JWKS) live on
// the identity binary per the platform-identity-database-boundary
// card; gateway only publishes the public JWKS document.
func NewServer(
	pool *pgxpool.Pool,
	identityPool *pgxpool.Pool,
	rdb *redis.Client,
	cfg *config.Config,
	logger *zap.Logger,
) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	h := &handlers{
		pool: pool,
		rdb:  rdb,
		cfg:  cfg,
		jwt:  identity.NewJWTValidator(),
	}

	r.Get("/health", h.health)
	r.Post("/sessions/validate", h.sessionsValidate)

	// JWKS — public key set at /.well-known/jwks.json. Bootstraps a
	// dev key on cold start when app.signing_keys is empty
	// (production fatals — keys must come from the rotation runbook).
	keyStore := keystore.New(pool)
	keystore.BootstrapDevKeyIfEmpty(context.Background(), keyStore, logger)
	jwks.New(keyStore, logger).Mount(r)

	// Mint pipeline. Audience separation is the safety property:
	// access tokens carry aud ∈ {canary, atlasview}; refresh tokens
	// carry aud="refresh". A captured access token can never
	// substitute as a refresh, and vice versa.
	tokenMinter := mint.New(keyStore, mint.Config{
		Issuer:   issuerString(cfg),
		Audience: []string{"canary", "atlasview"},
	})

	// Family ledger lives in canary_gcp.app.refresh_token_families
	// (migration 034). Will move to canary_identity_gcp in Sprint 4
	// per GRO-895.
	refreshFamilyStore := refreshfamily.New(pool)

	// /auth/login — credential exchange (T-1.a / GRO-848).
	personStore := auth.NewPersonStore(identityPool)
	auth.NewLoginHandler(personStore, tokenMinter, refreshFamilyStore, logger).Mount(r)

	// /auth/refresh — token rotation with reuse detection (T-1.b).
	refreshVerifier := tokenverify.New(keyStore, issuerString(cfg), "refresh")
	auth.NewRefreshHandler(refreshVerifier, tokenMinter, refreshFamilyStore, logger).Mount(r)

	// /v1/me — WhoAmI (T-3 / GRO-848 §3). Verifier accepts the
	// canary access audience; resolver pulls the full Person record
	// from canary_identity_gcp.public.persons so AtlasView's contract
	// shape is fully populated.
	accessVerifier := tokenverify.New(keyStore, issuerString(cfg), "canary")
	resolver := newPersonResolverAdapter(personStore)
	me.NewWithResolver(accessVerifier, resolver, logger).Mount(r)

	// /v1/identity/* — API-key required group + rate limit (GRO-912).
	limiter := cmdutil.MustValkeyRateLimiterFromClient(rdb)
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
			Limiter:  limiter,
		}))
		r.Post("/v1/identity/keys", h.keysCreate)
		r.Get("/v1/identity/keys", h.keysList)
		r.Post("/v1/identity/keys/{id}/revoke", h.keysRevoke)
		r.Get("/v1/identity/whoami", h.whoami)
	})

	// Stubs — wired so callers don't get 404; returns 501 until M2.
	r.Post("/merchants", stub)
	r.Get("/merchants/{id}", stub)
	r.Patch("/merchants/{id}", stub)
	r.Post("/oauth/authorize", stub)
	r.Get("/oauth/callback", stub)
	r.Post("/oauth/refresh", stub)
	r.Delete("/oauth/disconnect", stub)
	r.Post("/sessions", stub)
	r.Delete("/sessions/{token}", stub)
	r.Post("/users", stub)
	r.Get("/users/{id}", stub)
	r.Patch("/users/{id}", stub)

	return r
}

// issuerString returns the JWT iss claim value used by both minter
// and verifiers. PUBLIC_URL is the canonical config; falls back to
// "canary" (matching origin/main's default audience name) so
// existing /auth/refresh contract tests keep working.
func issuerString(cfg *config.Config) string {
	if v := os.Getenv("IDENTITY_ISSUER"); v != "" {
		return v
	}
	if cfg.PublicURL != "" {
		return cfg.PublicURL
	}
	return "canary"
}

// personResolverAdapter bridges auth.PersonStore (returns *auth.Person)
// to me.PersonResolver (expects *me.PersonRecord). Lives at the
// wiring layer because the two packages don't depend on each other.
type personResolverAdapter struct {
	persons *auth.PersonStore
}

func newPersonResolverAdapter(persons *auth.PersonStore) *personResolverAdapter {
	return &personResolverAdapter{persons: persons}
}

func (a *personResolverAdapter) ResolveByID(ctx context.Context, id string) (*me.PersonRecord, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, me.ErrPersonNotFound
	}
	p, err := a.persons.LookupByID(ctx, uid)
	if err != nil {
		if errors.Is(err, auth.ErrPersonNotFound) {
			return nil, me.ErrPersonNotFound
		}
		return nil, err
	}
	return &me.PersonRecord{
		ID:        p.ID.String(),
		Email:     p.Email,
		Name:      p.DisplayName,
		FirstName: p.FirstName,
		LastName:  p.LastName,
		Phone:     p.Phone,
		System:    p.IsSystem,
	}, nil
}

func stub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not_implemented"}`))
}
