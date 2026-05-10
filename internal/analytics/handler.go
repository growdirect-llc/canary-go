// internal/analytics/handler.go
//
// HTTP layer for analytics endpoints. All endpoints are tenant-scoped,
// read-only aggregations. No mutations.
//
//

package analytics

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// Handler binds analytics endpoints onto a chi router.
type Handler struct {
	Store  *Store
	Logger *zap.Logger
}

func New(store *Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{Store: store, Logger: logger}
}

// Mount registers all analytics routes on a chi router. All routes are
// reads (no write surface today); analytics:read enforced uniformly.
// analytics:write reserved for future drill-down save endpoints
// (GRO-906).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(identity.RequireScopeMiddleware(identity.ScopeAnalyticsRead))
		r.Get("/v1/analytics/sales", h.sales)
		r.Get("/v1/analytics/basket", h.basket)
		r.Get("/v1/analytics/cohort", h.cohort)
		r.Get("/v1/analytics/velocity", h.velocity)
		r.Get("/v1/analytics/shrink", h.shrink)
	})
}

func (h *Handler) sales(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	f := buildFilter(tenantID, r)
	result, err := h.Store.SalesSummary(r.Context(), f)
	if err != nil {
		h.Logger.Error("analytics sales", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) basket(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	f := buildFilter(tenantID, r)
	result, err := h.Store.BasketMetrics(r.Context(), f)
	if err != nil {
		h.Logger.Error("analytics basket", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) cohort(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	f := buildFilter(tenantID, r)
	rows, err := h.Store.CohortRows(r.Context(), f)
	if err != nil {
		h.Logger.Error("analytics cohort", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "count": len(rows)})
}

func (h *Handler) velocity(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	f := buildFilter(tenantID, r)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			f.Limit = n
		}
	}
	items, err := h.Store.VelocityItems(r.Context(), f)
	if err != nil {
		h.Logger.Error("analytics velocity", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) shrink(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	f := buildFilter(tenantID, r)
	result, err := h.Store.ShrinkSummary(r.Context(), f)
	if err != nil {
		h.Logger.Error("analytics shrink", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// helpers

func buildFilter(tenantID uuid.UUID, r *http.Request) DateRangeFilter {
	q := r.URL.Query()
	from, to := parseDateRange(q.Get("from"), q.Get("to"))
	f := DateRangeFilter{
		TenantID: tenantID,
		From:     from,
		To:       to,
		Limit:    50,
	}
	if v := q.Get("location_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			f.LocationID = &id
		}
	}
	return f
}

func tenantFromClaims(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
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
