// internal/casemgmt/handler.go
//
// HTTP layer for the case-management API. Six endpoints per
// docs/conventions.md.

package casemgmt

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Handler struct {
	Store  *Store
	Logger *zap.Logger
}

func New(s *Store, l *zap.Logger) *Handler {
	if l == nil {
		l = zap.NewNop()
	}
	return &Handler{Store: s, Logger: l}
}

// Mount registers all routes on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/cases", h.create)
	r.Get("/v1/cases", h.list)
	r.Get("/v1/cases/{id}", h.get)
	r.Post("/v1/cases/{id}/actions", h.appendAction)
	r.Get("/v1/cases/{id}/actions", h.listActions)
	r.Post("/v1/cases/{id}/evidence", h.appendEvidence)
	r.Get("/v1/cases/{id}/evidence", h.listEvidence)
	r.Post("/v1/cases/{id}/close", h.closeCase)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req CreateCaseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.CreateCase(r.Context(), req)
	if err != nil {
		h.renderStoreErr(w, err, "create case")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{
		TenantID: tenantID,
		Status:   q.Get("status"),
		Severity: q.Get("severity"),
	}
	if v := q.Get("assigned_to"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_assigned_to", err.Error())
			return
		}
		f.AssignedTo = &id
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			f.Limit = n
		}
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}
	items, err := h.Store.ListCases(r.Context(), f)
	if err != nil {
		h.renderStoreErr(w, err, "list cases")
		return
	}
	writeJSON(w, http.StatusOK, ListResponse{
		Items: items, Limit: f.Limit, Offset: f.Offset, Count: len(items),
	})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	out, err := h.Store.GetCase(r.Context(), tenantID, id)
	if err != nil {
		h.renderStoreErr(w, err, "get case")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) appendAction(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req AppendActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.AppendAction(r.Context(), tenantID, caseID, req)
	if err != nil {
		h.renderStoreErr(w, err, "append action")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) listActions(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	out, err := h.Store.ListActions(r.Context(), caseID)
	if err != nil {
		h.renderStoreErr(w, err, "list actions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (h *Handler) appendEvidence(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req AppendEvidenceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.AppendEvidence(r.Context(), tenantID, caseID, req)
	if err != nil {
		h.renderStoreErr(w, err, "append evidence")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) listEvidence(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	out, err := h.Store.ListEvidence(r.Context(), caseID)
	if err != nil {
		h.renderStoreErr(w, err, "list evidence")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "count": len(out)})
}

func (h *Handler) closeCase(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<14))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req CloseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.CloseCase(r.Context(), tenantID, caseID, req)
	if err != nil {
		h.renderStoreErr(w, err, "close case")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// helpers

func tenantFromQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	v := r.URL.Query().Get("tenant_id")
	if v == "" {
		v = r.URL.Query().Get("merchant_id")
	}
	if v == "" {
		writeError(w, http.StatusBadRequest, "missing_tenant",
			"tenant_id (or merchant_id) query parameter is required")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_tenant", "tenant_id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) renderStoreErr(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		h.Logger.Error(op, zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal_error", "")
	}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
