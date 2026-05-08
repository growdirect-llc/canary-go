// cmd/hawk/main.go
//
// Hawk — case-side reader binary.
//
// wires Hawk against the casemgmt store directly so a
// read-only operator dashboard can list cases / inspect actions /
// inspect evidence without depending on cmd/case being deployed.
// Mutating endpoints (open / close / append) are intentionally
// excluded — Hawk is the consumer; cmd/case is the writer.
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/casemgmt"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
)

const serviceName = "canary-hawk"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	store := casemgmt.NewStore(pool)
	hawk := &hawkHandler{store: store, logger: logger}

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", health(cfg))

	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
		}))
		hawk.Mount(r)
	})

	addr := ":" + cfg.Port
	logger.Info("starting", zap.String("service", serviceName), zap.String("addr", addr))
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
}

// hawkHandler exposes the read-only subset of casemgmt for operator
// dashboards. Auth: case:read scope on the API key.
type hawkHandler struct {
	store  *casemgmt.Store
	logger *zap.Logger
}

func (h *hawkHandler) Mount(r chi.Router) {
	r.Get("/v1/hawk/cases", h.listCases)
	r.Get("/v1/hawk/cases/{id}", h.getCase)
	r.Get("/v1/hawk/cases/{id}/actions", h.listActions)
	r.Get("/v1/hawk/cases/{id}/evidence", h.listEvidence)
}

func (h *hawkHandler) listCases(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "case:read") {
		writeErr(w, http.StatusForbidden, "forbidden", "case:read scope required")
		return
	}
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := casemgmt.ListFilters{
		TenantID: tenantID,
		Status:   q.Get("status"),
		Severity: q.Get("severity"),
	}
	if v := q.Get("assigned_to"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_assigned_to", err.Error())
			return
		}
		f.AssignedTo = &id
	}
	items, err := h.store.ListCases(r.Context(), f)
	if err != nil {
		h.logger.Error("hawk list cases", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *hawkHandler) getCase(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "case:read") {
		writeErr(w, http.StatusForbidden, "forbidden", "case:read scope required")
		return
	}
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	c, err := h.store.GetCase(r.Context(), tenantID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *hawkHandler) listActions(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "case:read") {
		writeErr(w, http.StatusForbidden, "forbidden", "case:read scope required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	out, err := h.store.ListActions(r.Context(), id)
	if err != nil {
		h.logger.Error("hawk list actions", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (h *hawkHandler) listEvidence(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "case:read") {
		writeErr(w, http.StatusForbidden, "forbidden", "case:read scope required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	out, err := h.store.ListEvidence(r.Context(), id)
	if err != nil {
		h.logger.Error("hawk list evidence", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func tenantFromContext(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok || claims.TenantID == uuid.Nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "tenant-scoped key required")
		return uuid.Nil, false
	}
	return claims.TenantID, true
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
