// internal/billing/handler.go
//
// HTTP layer for Bull. Conventions per docs/conventions.md.

package billing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

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

// Mount registers routes on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/billing/otb", h.createBudget)
	r.Get("/v1/billing/otb", h.listBudgets)
	r.Get("/v1/billing/otb/{id}", h.getBudget)
	r.Post("/v1/billing/otb/{id}/consume", h.consume)
	r.Get("/v1/billing/cost-rollup", h.costRollup)
}

func (h *Handler) createBudget(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req CreateBudgetRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.CreateBudget(r.Context(), req)
	if err != nil {
		h.renderStoreErr(w, err, "create budget")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) listBudgets(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	out, err := h.Store.ListBudgets(r.Context(), tenantID, status)
	if err != nil {
		h.renderStoreErr(w, err, "list budgets")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"count": len(out),
	})
}

func (h *Handler) getBudget(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	out, err := h.Store.GetBudget(r.Context(), tenantID, id)
	if err != nil {
		h.renderStoreErr(w, err, "get budget")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) consume(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
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
	var req ConsumeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	if req.Satoshis <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_satoshis", "satoshis must be positive")
		return
	}
	out, err := h.Store.Consume(r.Context(), tenantID, id, req.Satoshis)
	if err != nil {
		if errors.Is(err, ErrHardLimitHit) {
			// Return 402 Payment Required — semantics-appropriate for
			// L402-gated budgets.
			writeError(w, http.StatusPaymentRequired, "hard_limit_hit", err.Error())
			return
		}
		h.renderStoreErr(w, err, "consume budget")
		return
	}
	writeJSON(w, http.StatusOK, ConsumeResponse{
		BudgetID:          out.ID,
		ConsumedSatoshis:  out.ConsumedSatoshis,
		RemainingSatoshis: out.RemainingSatoshis,
	})
}

func (h *Handler) costRollup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	startStr := q.Get("period_start")
	endStr := q.Get("period_end")
	if startStr == "" || endStr == "" {
		writeError(w, http.StatusBadRequest, "missing_period",
			"period_start + period_end (RFC3339) required")
		return
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_period_start", err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_period_end", err.Error())
		return
	}
	out, err := h.Store.CostRollup(r.Context(), CostRollupRequest{
		TenantID:    tenantID,
		PeriodStart: start,
		PeriodEnd:   end,
		CadenceStep: q.Get("cadence_step"),
	})
	if err != nil {
		h.renderStoreErr(w, err, "cost rollup")
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
	case errors.Is(err, ErrHardLimitHit):
		writeError(w, http.StatusPaymentRequired, "hard_limit_hit", err.Error())
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
