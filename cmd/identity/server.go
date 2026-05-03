// cmd/identity/server.go
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// NewServer wires the Chi router and all routes. Accepts injected
// dependencies so tests can pass a test DB and Valkey client.
//
// The /v1/identity/* group is mounted under APIKeyMiddleware so
// every key lifecycle and whoami call is authenticated. /health and
// /sessions/validate stay open (legacy + readiness probe).
func NewServer(pool *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) http.Handler {
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

	// /v1/identity/* — API-key required group
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
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

func stub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not_implemented"}`))
}
