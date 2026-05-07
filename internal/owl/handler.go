package owl

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler serves Owl's read-only HTTP surface. Five endpoints:
//
//	GET /v1/owl/dashboard   — full dashboard envelope
//	GET /v1/owl/sales       — sales summary only
//	GET /v1/owl/top-items   — top items by units|revenue
//	GET /v1/owl/cases       — cases summary only
//	GET /v1/owl/exposure    — cashier exposure top-N
//
// Loop 2 dispatch deliberately leaves auth out of scope. The full
// owl.md SDD specifies JWT — that's a Loop 3 graft. Until then
// merchant_id comes off the query string.
type Handler struct {
	Aggregator *Aggregator
	Logger     *zap.Logger
	Now        func() time.Time
}

// New constructs a Handler with sensible defaults.
func New(agg *Aggregator, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		Aggregator: agg,
		Logger:     logger,
		Now:        func() time.Time { return time.Now().UTC() },
	}
}

// Mount registers Owl's routes on a chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/owl/dashboard", h.Dashboard)
	r.Get("/v1/owl/sales", h.Sales)
	r.Get("/v1/owl/top-items", h.TopItems)
	r.Get("/v1/owl/cases", h.Cases)
	r.Get("/v1/owl/exposure", h.Exposure)
}

// ──────────────────────────────────────────────────────────────────────
// Endpoints
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	merchantID, p, err := h.parseMerchantAndPeriod(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	dash, err := h.Aggregator.Aggregate(r.Context(), merchantID, p)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dash)
}

func (h *Handler) Sales(w http.ResponseWriter, r *http.Request) {
	merchantID, p, err := h.parseMerchantAndPeriod(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	tenantID, sales, err := h.Aggregator.Sales(r.Context(), merchantID, p)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"tenant_id":   tenantID,
		"period":      p,
		"sales":       sales,
	})
}

func (h *Handler) TopItems(w http.ResponseWriter, r *http.Request) {
	merchantID, p, err := h.parseMerchantAndPeriod(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	by := TopItemsBy(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("by"))))
	if by == "" {
		by = TopItemsByUnits
	}
	limit := parseLimit(r.URL.Query().Get("limit"), DefaultTopItemsLimit, 100)

	tenantID, items, err := h.Aggregator.TopItems(r.Context(), merchantID, p, by, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"tenant_id":   tenantID,
		"period":      p,
		"by":          by,
		"limit":       limit,
		"items":       items,
	})
}

func (h *Handler) Cases(w http.ResponseWriter, r *http.Request) {
	merchantID, p, err := h.parseMerchantAndPeriod(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	tenantID, cases, err := h.Aggregator.Cases(r.Context(), merchantID, p)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"tenant_id":   tenantID,
		"period":      p,
		"cases":       cases,
	})
}

func (h *Handler) Exposure(w http.ResponseWriter, r *http.Request) {
	merchantID, p, err := h.parseMerchantAndPeriod(r.Context(), r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), DefaultExposureLimit, 50)

	tenantID, exposure, err := h.Aggregator.Exposure(r.Context(), merchantID, p, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"tenant_id":   tenantID,
		"period":      p,
		"limit":       limit,
		"exposure":    exposure,
	})
}

// ──────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────

// parseMerchantAndPeriod extracts merchant_id from the query string,
// resolves the merchant's timezone, and parses the period window.
//
// Caller flow: most handlers want the merchant resolved AND the period
// computed in the same step — they pay one extra round-trip for the
// timezone lookup, but the alternative is asking the caller to pre-load
// the timezone, which leaks repository concerns into HTTP handlers.
func (h *Handler) parseMerchantAndPeriod(ctx context.Context, r *http.Request) (uuid.UUID, Period, error) {
	q := r.URL.Query()
	mIDStr := strings.TrimSpace(q.Get("merchant_id"))
	if mIDStr == "" {
		return uuid.Nil, Period{}, &httpError{status: http.StatusBadRequest, msg: "merchant_id is required"}
	}
	merchantID, err := uuid.Parse(mIDStr)
	if err != nil {
		return uuid.Nil, Period{}, &httpError{status: http.StatusBadRequest, msg: "merchant_id must be a valid UUID"}
	}
	_, tz, err := h.Aggregator.ResolveMerchantTimezone(ctx, merchantID)
	if err != nil {
		if errors.Is(err, ErrMerchantNotFound) {
			return uuid.Nil, Period{}, &httpError{status: http.StatusNotFound, msg: "merchant not found"}
		}
		return uuid.Nil, Period{}, err
	}
	p, err := ParsePeriod(q, tz, h.Now())
	if err != nil {
		return uuid.Nil, Period{}, &httpError{status: http.StatusBadRequest, msg: err.Error()}
	}
	return merchantID, p, nil
}

// parseLimit clamps an inbound `limit` query param to [1, max], with a
// fallback to def. Anything unparseable is treated as missing.
func parseLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// httpError carries a status + client-safe message.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

// writeError writes a JSON error response. 404 / 400 messages are
// returned verbatim; anything else is sanitized to "internal error"
// (per owl.md security model).
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrMerchantNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "merchant not found"})
		return
	}
	var he *httpError
	if errors.As(err, &he) {
		writeJSON(w, he.status, map[string]any{"ok": false, "error": he.msg})
		return
	}
	h.Logger.Error("owl handler", zap.Error(err))
	writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal error"})
}

// writeJSON is the standard JSON response writer.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
