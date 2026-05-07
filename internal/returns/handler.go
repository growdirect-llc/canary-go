// internal/returns/handler.go
//
// HTTP layer for the returns endpoints.
//
// Routes:
//   GET  /v1/returns              — list return transactions
//   GET  /v1/returns/summary      — aggregate stats
//   GET  /v1/returns/{id}         — return detail with line items
//   POST /v1/returns/{id}/flag    — create fraud-flag detection
//   GET  /v1/returns/{id}/lines   — line items only (convenience)
//
// Spec: GRO-766 Phase E.

package returns

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/identity"
)

// Handler binds returns endpoints onto a chi router.
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
	r.Get("/v1/returns", h.list)
	r.Get("/v1/returns/summary", h.summary)
	r.Get("/v1/returns/{id}", h.get)
	r.Post("/v1/returns/{id}/flag", h.flag)
	r.Get("/v1/returns/{id}/lines", h.lines)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, to := parseDateRange(q.Get("from"), q.Get("to"))
	f := ListFilters{
		TenantID: tenantID,
		From:     from,
		To:       to,
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
	if v := q.Get("customer_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_customer_id", err.Error())
			return
		}
		f.CustomerID = &id
	}
	items, err := h.Store.List(r.Context(), f)
	if err != nil {
		h.Logger.Error("returns list", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, to := parseDateRange(q.Get("from"), q.Get("to"))
	var locationID *uuid.UUID
	if v := q.Get("location_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_location_id", err.Error())
			return
		}
		locationID = &id
	}
	result, err := h.Store.Summary(r.Context(), tenantID, from, to, locationID)
	if err != nil {
		h.Logger.Error("returns summary", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	detail, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("returns get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) lines(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	detail, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("returns lines", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": detail.Lines, "count": len(detail.Lines)})
}

func (h *Handler) flag(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var req FraudFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.DetectionRuleID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "missing_detection_rule_id", "detection_rule_id required")
		return
	}
	if req.Severity == "" {
		req.Severity = "medium"
	}
	resp, err := h.Store.FraudFlag(r.Context(), tenantID, id, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("returns flag", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
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
