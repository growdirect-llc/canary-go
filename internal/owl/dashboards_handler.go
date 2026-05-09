// internal/owl/dashboards_handler.go
//
// HTTP layer for the Wave C dashboard endpoints.

package owl

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// DashboardHandler binds the Wave C analytics endpoints onto a chi
// router. Auth: caller carries an API key with scope analytics:read
// (party RFM) or analytics:admin (refresh / LP-rate aggregations).
// Tenant scope is derived from the API-key claims (see
// identity.APIKeyMiddleware in cmd/owl/main.go) — callers no longer
// pass tenant_id / merchant_id as a query parameter.
type DashboardHandler struct {
	Store  *DashboardStore
	Logger *zap.Logger
}

func NewDashboardHandler(s *DashboardStore, l *zap.Logger) *DashboardHandler {
	if l == nil {
		l = zap.NewNop()
	}
	return &DashboardHandler{Store: s, Logger: l}
}

func (h *DashboardHandler) Mount(r chi.Router) {
	r.Get("/v1/owl/parties", h.listParties)
	r.Get("/v1/owl/parties/{id}/rfm", h.getPartyRFM)
	r.Post("/v1/owl/parties/refresh", h.refreshDecisioningFacts)
	r.Get("/v1/owl/lp-rate", h.lpRate)
}

// requireTenant returns the authenticated tenant or writes 401 and
// returns false. Every owl dashboard endpoint is tenant-scoped — there
// is no platform-scope read path. CK2 (GRO-919) cleanup: replaces the
// caller-supplied ?tenant_id= pattern with claims-derived tenant.
func requireTenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	c, ok := identity.ClaimsFromContext(r.Context())
	if !ok || c.TenantID == uuid.Nil {
		writeDashErr(w, http.StatusUnauthorized, "unauthenticated", "missing tenant claim")
		return uuid.Nil, false
	}
	return c.TenantID, true
}

func (h *DashboardHandler) getPartyRFM(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeDashErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	out, err := h.Store.GetPartyRFM(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeDashErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("owl get rfm", zap.Error(err))
		writeDashErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeDashJSON(w, http.StatusOK, out)
}

func (h *DashboardHandler) listParties(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	out, err := h.Store.ListPartyRFM(r.Context(), tenantID, limit)
	if err != nil {
		h.Logger.Error("owl list rfm", zap.Error(err))
		writeDashErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeDashJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (h *DashboardHandler) refreshDecisioningFacts(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.RefreshDecisioningFacts(r.Context()); err != nil {
		h.Logger.Error("owl refresh", zap.Error(err))
		writeDashErr(w, http.StatusInternalServerError, "refresh_failed", err.Error())
		return
	}
	writeDashJSON(w, http.StatusOK, map[string]any{"refreshed": true})
}

func (h *DashboardHandler) lpRate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	windowStart, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		writeDashErr(w, http.StatusBadRequest, "malformed_from",
			"from (RFC3339) required")
		return
	}
	windowEnd, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		writeDashErr(w, http.StatusBadRequest, "malformed_to",
			"to (RFC3339) required")
		return
	}
	out, err := h.Store.LPRateRollup(r.Context(), tenantID, windowStart, windowEnd)
	if err != nil {
		h.Logger.Error("owl lp-rate", zap.Error(err))
		writeDashErr(w, http.StatusInternalServerError, "rollup_failed", err.Error())
		return
	}
	writeDashJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

// helpers

func writeDashErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

func writeDashJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
