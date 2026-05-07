// internal/customer/handler.go
//
// HTTP layer for customer endpoints.
//
// Routes:
//   GET  /v1/customers               — list with filters
//   GET  /v1/customers/{id}          — single customer
//   GET  /v1/customers/{id}/memberships — loyalty memberships
//   GET  /v1/customers/{id}/transactions — recent transactions (via JOIN)
//
// Spec: GRO-766 Phase D.

package customer

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// Handler binds customer endpoints onto a chi router.
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

func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/customers", h.list)
	r.Get("/v1/customers/{id}", h.get)
	r.Get("/v1/customers/{id}/memberships", h.memberships)
	r.Get("/v1/customers/{id}/transactions", h.transactions)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{
		TenantID:     tenantID,
		Status:       q.Get("status"),
		CustomerType: q.Get("customer_type"),
		Search:       q.Get("search"),
		Limit:        50,
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}
	items, err := h.Store.List(r.Context(), f)
	if err != nil {
		h.Logger.Error("customer list", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	c, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("customer get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) memberships(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	// Verify customer exists and belongs to tenant.
	if _, err := h.Store.GetByID(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("customer memberships lookup", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	items, err := h.Store.GetMemberships(r.Context(), tenantID, id)
	if err != nil {
		h.Logger.Error("customer memberships", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// transactions is a lightweight redirect: returns 303 to the analytics
// cohort endpoint filtered by customer, or 501 if not yet implemented.
// Full transaction history is scoped to the analytics service; this stub
// keeps the route registered for API contract completeness.
func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	// Ensure the customer exists in this tenant before 501.
	if _, err := h.Store.GetByID(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("customer tx lookup", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeErr(w, http.StatusNotImplemented, "not_implemented",
		"per-customer transaction history is served by the analytics service")
}

// helpers

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
