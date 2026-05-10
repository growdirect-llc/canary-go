package pricing

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// requireTenant returns the authenticated claim's tenant id (GRO-928).
// Replaces every uuidQuery(r, "tenant_id") call site so the tenant is
// derived from the API-key claims, not request-supplied data. Writes
// 401 if claims are absent (defensive — the routes are wrapped in
// APIKeyMiddleware which returns 401 first, but the handler still
// guards in case wiring drifts). If the request also supplies a
// tenant_id query param, it MUST match the authenticated claim
// (returns 403 tenant_mismatch).
func (h *Handler) requireTenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok || claims.TenantID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "tenant claim required")
		return uuid.Nil, false
	}
	if v := r.URL.Query().Get("tenant_id"); v != "" {
		q, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", err.Error())
			return uuid.Nil, false
		}
		if q != claims.TenantID {
			writeError(w, http.StatusForbidden, "tenant_mismatch",
				"tenant_id query parameter does not match authenticated tenant")
			return uuid.Nil, false
		}
	}
	return claims.TenantID, true
}

// Handler exposes pricing over HTTP. Mounted by Mount(r).
type Handler struct {
	Resolver *Resolver
	Store    Store
	Logger   *zap.Logger
}

// New constructs the handler.
func New(resolver *Resolver, store Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{Resolver: resolver, Store: store, Logger: logger}
}

// Mount registers all pricing routes on the given chi router.
//
//   POST /v1/pricing/resolve
//   GET  /v1/pricing/items/{item_id}/base
//   GET  /v1/pricing/promotions
//   GET  /v1/pricing/tax-rates
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/pricing/resolve", h.handleResolve)
	r.Get("/v1/pricing/items/{item_id}/base", h.handleBasePrice)
	r.Get("/v1/pricing/promotions", h.handleListPromotions)
	r.Get("/v1/pricing/tax-rates", h.handleListTaxRates)
}

// --- POST /v1/pricing/resolve ---

func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request) {
	var req ResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// GRO-928: tenant comes from authenticated claims, not the request
	// body. If the body supplied a tenant_id and it doesn't match the
	// claim, return 403 — matches the inventory + item handler pattern.
	tenantID, ok := h.requireTenant(w, r)
	if !ok {
		return
	}
	if req.TenantID != uuid.Nil && req.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "tenant_mismatch",
			"body tenant_id does not match authenticated tenant")
		return
	}
	req.TenantID = tenantID
	resp, err := h.Resolver.Resolve(r.Context(), &req)
	if err != nil {
		// Resolver only returns input or DB errors. Map "not found"
		// types to 404; everything else 500.
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		h.Logger.Warn("resolve failed", zap.Error(err))
		// Some validation errors (no lines, missing IDs) come back as
		// plain errors.New — string-match the prefix for 400 vs 500.
		msg := err.Error()
		if isInputError(msg) {
			writeError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}
		writeError(w, http.StatusInternalServerError, "resolve_failed", msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// isInputError sniffs validation messages from resolver.go. Wave 2-pragmatic
// — Wave 3 should introduce a typed input-validation error.
func isInputError(msg string) bool {
	switch {
	case contains4xxPrefix(msg, "pricing: nil request"),
		contains4xxPrefix(msg, "pricing: at least one line required"),
		contains4xxPrefix(msg, "pricing: tenant_id required"),
		contains4xxPrefix(msg, "pricing: location_id required"),
		contains4xxPrefix(msg, "pricing: line item_id required"),
		contains4xxPrefix(msg, "pricing: parse quantity"),
		contains4xxPrefix(msg, "pricing: zero quantity"),
		contains4xxPrefix(msg, "pricing: no price found"):
		return true
	}
	return false
}

func contains4xxPrefix(msg, prefix string) bool {
	if len(msg) < len(prefix) {
		return false
	}
	return msg[:len(prefix)] == prefix
}

// --- GET /v1/pricing/items/{item_id}/base ---

func (h *Handler) handleBasePrice(w http.ResponseWriter, r *http.Request) {
	itemIDStr := chi.URLParam(r, "item_id")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_item_id", err.Error())
		return
	}
	tenantID, ok := h.requireTenant(w, r)
	if !ok {
		return
	}
	var locPtr *uuid.UUID
	if v := r.URL.Query().Get("location_id"); v != "" {
		loc, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_location_id", err.Error())
			return
		}
		locPtr = &loc
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "brick"
	}
	asOf, err := timeQuery(r, "as_of")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_as_of", err.Error())
		return
	}

	row, err := h.Store.GetItemPrice(r.Context(), tenantID, itemID, locPtr, channel, asOf)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_price", "no active price for this item/location")
			return
		}
		h.Logger.Warn("base price lookup failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "lookup_failed", "")
		return
	}

	writeJSON(w, http.StatusOK, BasePriceResponse{
		ItemID:         row.ItemID,
		TenantID:       row.TenantID,
		LocationID:     row.LocationID,
		Channel:        derefString(row.Channel, "all"),
		PriceType:      row.PriceType,
		Amount:         row.Amount,
		Currency:       row.Currency,
		UOM:            row.UOM,
		EffectiveStart: row.EffectiveStart,
		EffectiveEnd:   row.EffectiveEnd,
	})
}

// --- GET /v1/pricing/promotions ---

func (h *Handler) handleListPromotions(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.requireTenant(w, r)
	if !ok {
		return
	}
	locationID, err := uuidQuery(r, "location_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_location_id", err.Error())
		return
	}
	asOf, err := timeQuery(r, "active_at")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_active_at", err.Error())
		return
	}
	rows, err := h.Store.ListActivePromotions(r.Context(), tenantID, locationID, asOf)
	if err != nil {
		h.Logger.Warn("list promotions failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	out := PromotionsListResponse{Promotions: make([]PromotionSummary, 0, len(rows))}
	for _, p := range rows {
		out.Promotions = append(out.Promotions, PromotionSummary{
			ID:             p.ID,
			PromotionCode:  p.PromotionCode,
			Name:           p.Name,
			PromotionType:  p.PromotionType,
			ScopeType:      p.ScopeType,
			EffectiveStart: p.EffectiveStart,
			EffectiveEnd:   p.EffectiveEnd,
			Stackable:      p.Stackable,
			Status:         p.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- GET /v1/pricing/tax-rates ---

func (h *Handler) handleListTaxRates(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.requireTenant(w, r)
	if !ok {
		return
	}
	var locPtr *uuid.UUID
	if v := r.URL.Query().Get("location_id"); v != "" {
		loc, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_location_id", err.Error())
			return
		}
		locPtr = &loc
	}
	rows, err := h.Store.ListTaxRates(r.Context(), tenantID, locPtr)
	if err != nil {
		h.Logger.Warn("list tax rates failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	out := TaxRatesListResponse{TaxRates: make([]TaxRateSummary, 0, len(rows))}
	for _, r := range rows {
		out.TaxRates = append(out.TaxRates, TaxRateSummary{
			ID:             r.ID,
			TaxClassID:     r.TaxClassID,
			TaxClassCode:   r.TaxClassCode,
			LocationID:     r.LocationID,
			Jurisdiction:   r.Jurisdiction,
			RateType:       r.RateType,
			Rate:           r.Rate,
			EffectiveStart: r.EffectiveStart,
			EffectiveEnd:   r.EffectiveEnd,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- helpers ---

func uuidQuery(r *http.Request, key string) (uuid.UUID, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return uuid.Nil, errors.New(key + " required")
	}
	return uuid.Parse(v)
}

// timeQuery parses an optional RFC3339 query param. Returns time.Now().UTC()
// if absent.
func timeQuery(r *http.Request, key string) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func derefString(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// errorBody is the JSON shape returned on every non-2xx response.
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
