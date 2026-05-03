package chirp

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler is the HTTP surface for the chirp service.
type Handler struct {
	engine *Engine
	store  Store
	logger *zap.Logger
}

// NewHandler wires an engine + store + logger.
func NewHandler(engine *Engine, store Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{engine: engine, store: store, logger: logger}
}

// Mount registers the chirp routes on a chi router. Path prefix is
// /v1/chirp/*; the caller decides where in the route tree they sit.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/chirp/evaluate", h.Evaluate)
	r.Post("/v1/chirp/evaluate-batch", h.EvaluateBatch)
	r.Get("/v1/chirp/rules", h.ListRules)
	r.Get("/v1/chirp/detections", h.ListDetections)
}

type evaluateRequest struct {
	TransactionID string `json:"transaction_id"`
}

// Evaluate handles POST /v1/chirp/evaluate.
func (h *Handler) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req evaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_request", err.Error())
		return
	}
	txID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_transaction_id", err.Error())
		return
	}
	dets, err := h.engine.EvaluateTransaction(r.Context(), txID)
	if err != nil {
		h.logger.Error("evaluate failed", zap.Error(err), zap.String("transaction_id", txID.String()))
		writeError(w, http.StatusInternalServerError, "evaluate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"transaction_id":     txID,
		"detections_count":   len(dets),
		"detections":         dets,
	})
}

type evaluateBatchRequest struct {
	MerchantID string `json:"merchant_id"`
	Since      string `json:"since"`
}

// EvaluateBatch handles POST /v1/chirp/evaluate-batch.
//
// SDD-conflict: dispatch uses "merchant_id" as the request key; the
// canonical schema treats tenant_id as the multi-tenant boundary.
// The wave 2 mapping is 1:1 (app.merchants.tenant_id), so we accept
// merchant_id in the request and pass it through as tenant_id.
func (h *Handler) EvaluateBatch(w http.ResponseWriter, r *http.Request) {
	var req evaluateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_request", err.Error())
		return
	}
	tenantID, err := uuid.Parse(req.MerchantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
		return
	}
	since, err := time.Parse(time.RFC3339, req.Since)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_since", "since must be RFC3339")
		return
	}
	res, err := h.engine.EvaluateBatch(r.Context(), tenantID, since)
	if err != nil {
		h.logger.Error("batch evaluate failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "batch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ListRules handles GET /v1/chirp/rules?merchant_id=...
func (h *Handler) ListRules(w http.ResponseWriter, r *http.Request) {
	merchantStr := r.URL.Query().Get("merchant_id")
	if merchantStr == "" {
		writeError(w, http.StatusBadRequest, "missing_merchant_id", "merchant_id is required")
		return
	}
	tenantID, err := uuid.Parse(merchantStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
		return
	}
	rules, err := h.store.ListRules(r.Context(), tenantID)
	if err != nil {
		h.logger.Error("list rules failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_rules_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": tenantID,
		"count":       len(rules),
		"rules":       rules,
	})
}

// ListDetections handles GET /v1/chirp/detections?merchant_id=...&from=...&to=...&limit=...&offset=...
func (h *Handler) ListDetections(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	merchantStr := q.Get("merchant_id")
	if merchantStr == "" {
		writeError(w, http.StatusBadRequest, "missing_merchant_id", "merchant_id is required")
		return
	}
	tenantID, err := uuid.Parse(merchantStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
		return
	}
	dq := DetectionQuery{TenantID: tenantID}

	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_from", "from must be RFC3339")
			return
		}
		dq.From = &t
	}
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_to", "to must be RFC3339")
			return
		}
		dq.To = &t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err == nil && n > 0 && n <= 500 {
			dq.Limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err == nil && n >= 0 {
			dq.Offset = n
		}
	}

	dets, err := h.store.ListDetections(r.Context(), dq)
	if err != nil {
		h.logger.Error("list detections failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_detections_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": tenantID,
		"count":       len(dets),
		"detections":  dets,
	})
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
