// internal/employee/handler.go
//
// HTTP layer for employee endpoints.
//
// Routes:
//   GET /v1/employees                  — list with filters
//   GET /v1/employees/{id}             — single employee
//   GET /v1/employees/{id}/alerts      — detection alert summary for employee
//   GET /v1/employees/alert-summary    — ranked alert summary across all employees
//
// Spec: GRO-766 Phase D.

package employee

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

// Handler binds employee endpoints onto a chi router.
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
	r.Get("/v1/employees", h.list)
	r.Get("/v1/employees/alert-summary", h.alertSummary)
	r.Get("/v1/employees/{id}", h.get)
	r.Get("/v1/employees/{id}/alerts", h.employeeAlerts)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{
		TenantID:         tenantID,
		EmploymentStatus: q.Get("employment_status"),
		Search:           q.Get("search"),
		Limit:            50,
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
		h.Logger.Error("employee list", zap.Error(err))
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
	e, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("employee get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) alertSummary(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	rows, err := h.Store.AlertSummaries(r.Context(), tenantID)
	if err != nil {
		h.Logger.Error("employee alert summary", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "count": len(rows)})
}

// employeeAlerts returns the alert summary for a single employee.
// Delegates to AlertSummaries and filters client-side — avoids a second
// query shape; the full table is small.
func (h *Handler) employeeAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	// Verify the employee exists.
	if _, err := h.Store.GetByID(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("employee alerts lookup", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	all, err := h.Store.AlertSummaries(r.Context(), tenantID)
	if err != nil {
		h.Logger.Error("employee alerts summary", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	for _, a := range all {
		if a.EmployeeID == id {
			writeJSON(w, http.StatusOK, a)
			return
		}
	}
	// No detections for this employee yet — return zero-value summary.
	e, _ := h.Store.GetByID(r.Context(), tenantID, id)
	writeJSON(w, http.StatusOK, AlertSummary{
		EmployeeID:   e.ID,
		EmployeeCode: e.EmployeeCode,
		DisplayName:  e.DisplayName,
	})
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
