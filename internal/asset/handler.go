// internal/asset/handler.go
//
// HTTP layer for asset (inventory) endpoints.
//
//

package asset

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// Handler binds asset endpoints onto a chi router.
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

// Mount registers all asset routes on a chi router. GETs require
// asset:read; POST /flag requires asset:write (GRO-906).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(identity.RequireScopeMiddleware(identity.ScopeAssetRead))
		r.Get("/v1/assets", h.list)
		r.Get("/v1/assets/shrink", h.shrink)
		r.Get("/v1/assets/{item_id}", h.get)
	})
	r.Group(func(r chi.Router) {
		r.Use(identity.RequireScopeMiddleware(identity.ScopeAssetWrite))
		r.Post("/v1/assets/{item_id}/flag", h.flag)
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{
		TenantID: tenantID,
		Status:   q.Get("status"),
		LowStock: q.Get("low_stock") == "true",
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
		h.Logger.Error("asset list", zap.Error(err))
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
	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_item_id", err.Error())
		return
	}
	detail, err := h.Store.GetItem(r.Context(), tenantID, itemID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		h.Logger.Error("asset get", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) shrink(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, to := dateRange(q.Get("from"), q.Get("to"))
	var locationID *uuid.UUID
	if v := q.Get("location_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_location_id", err.Error())
			return
		}
		locationID = &id
	}
	rows, err := h.Store.ShrinkMovements(r.Context(), tenantID, from, to, locationID)
	if err != nil {
		h.Logger.Error("asset shrink", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "count": len(rows)})
}

func (h *Handler) flag(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromClaims(w, r)
	if !ok {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_item_id", err.Error())
		return
	}
	var req FlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.LocationID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "missing_location_id", "location_id required")
		return
	}
	if req.ReasonCode == "" {
		writeErr(w, http.StatusBadRequest, "missing_reason_code", "reason_code required")
		return
	}
	resp, err := h.Store.Flag(r.Context(), tenantID, itemID, req)
	if err != nil {
		h.Logger.Error("asset flag", zap.Error(err))
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

// dateRange parses ?from= and ?to= as YYYY-MM-DD. Defaults to last 30 days.
func dateRange(fromStr, toStr string) (time.Time, time.Time) {
	layout := "2006-01-02"
	to := time.Now().UTC().Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, -30)
	if t, err := time.Parse(layout, toStr); err == nil {
		to = t.UTC()
	}
	if t, err := time.Parse(layout, fromStr); err == nil {
		from = t.UTC()
	}
	return from, to
}
