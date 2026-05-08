// internal/alert/handler.go
//
// HTTP layer for the alert endpoints.
//
//

package alert

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

// Handler binds alert endpoints onto a chi router.
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
	r.Get("/v1/alerts", h.list)
	r.Get("/v1/alerts/stats", h.stats)
	r.Get("/v1/alerts/{id}", h.get)
	r.Post("/v1/alerts/{id}/acknowledge", h.acknowledge)
	r.Post("/v1/alerts/{id}/resolve", h.resolve)
	r.Post("/v1/alerts/{id}/suppress", h.suppress)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{
		TenantID: tenantID,
		Severity: q.Get("severity"),
		Status:   q.Get("status"),
		RuleType: q.Get("rule_type"),
		Limit:    50,
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
	if v := q.Get("location_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_location_id", err.Error())
			return
		}
		f.LocationID = &id
	}
	items, err := h.Store.List(r.Context(), f)
	if err != nil {
		h.Logger.Error("alert list", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
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
	a, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("alert get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) acknowledge(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var req AcknowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AcknowledgedBy == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "acknowledged_by (UUID) required")
		return
	}
	a, err := h.Store.Acknowledge(r.Context(), tenantID, id, req.AcknowledgedBy)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if errors.Is(err, ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		h.Logger.Error("alert ack", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var req ResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.Disposition == "" {
		req.Disposition = "dismissed"
	}
	a, err := h.Store.Resolve(r.Context(), tenantID, id, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if errors.Is(err, ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		h.Logger.Error("alert resolve", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) suppress(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var req SuppressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	a, err := h.Store.Suppress(r.Context(), tenantID, id, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if errors.Is(err, ErrConflict) {
			writeErr(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		h.Logger.Error("alert suppress", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	rows, err := h.Store.Stats(r.Context(), tenantID)
	if err != nil {
		h.Logger.Error("alert stats", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "count": len(rows)})
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
