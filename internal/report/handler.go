// internal/report/handler.go
//
// HTTP layer for report generation endpoints.
//
// Routes:
//   POST /v1/reports          — enqueue a new report job
//   GET  /v1/reports          — list jobs for the tenant
//   GET  /v1/reports/{job_id} — poll job status / get download URL
//
// Spec: GRO-766 Phase E.

package report

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/identity"
)

// Handler binds report endpoints onto a chi router.
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
	r.Post("/v1/reports", h.create)
	r.Get("/v1/reports", h.list)
	r.Get("/v1/reports/{job_id}", h.get)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	var req ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.ReportType == "" {
		writeErr(w, http.StatusBadRequest, "missing_report_type", "report_type required")
		return
	}
	switch req.ReportType {
	case ReportTypeSalesSummary, ReportTypeReturnDetail, ReportTypeShrink:
		// valid
	default:
		writeErr(w, http.StatusBadRequest, "invalid_report_type",
			"report_type must be sales_summary | return_detail | shrink")
		return
	}
	job, err := h.Store.Create(r.Context(), tenantID, req)
	if err != nil {
		h.Logger.Error("report create", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	jobs, err := h.Store.List(r.Context(), tenantID)
	if err != nil {
		h.Logger.Error("report list", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs, "count": len(jobs)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(chi.URLParam(r, "job_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_job_id", err.Error())
		return
	}
	job, err := h.Store.GetByID(r.Context(), tenantID, jobID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("report get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, job)
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
