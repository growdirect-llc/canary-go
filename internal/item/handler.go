// internal/item/handler.go
//
// HTTP layer for the item service. Thin: parse + delegate + render.
// All business logic lives in store.go.

package item

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler binds the HTTP routes to the Store.
type Handler struct {
	Store  Store
	Logger *zap.Logger
}

// New constructs a Handler.
func New(store Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{Store: store, Logger: logger}
}

// Mount registers all item-service routes on a chi router.
//
// Endpoint inventory:
//
//	GET    /v1/items/{id}            — by UUID
//	GET    /v1/items?tenant_id=…&sku=…
//	GET    /v1/items?tenant_id=…[&category_id=…&vendor_id=…&status=…&limit=…&offset=…]
//	GET    /v1/items/by-barcode?tenant_id=…&barcode=…   ← keystone POS scan
//	POST   /v1/items                 — create
//	PATCH  /v1/items/{id}            — partial update
//	DELETE /v1/items/{id}            — soft delete
//	GET    /v1/categories?tenant_id=…
//	GET    /v1/vendors?tenant_id=…
//
// Note: the dispatch brief used "merchant_id" on the wire. The schema's
// m.* tables FK to app.tenants. We accept either query-string spelling
// (tenant_id preferred) so callers built against either spec keep
// working. Created items always echo tenant_id back.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/items/by-barcode", h.getByBarcode) // most specific first
	r.Get("/v1/items/{id}", h.getOrList)
	r.Get("/v1/items", h.getOrList)
	r.Post("/v1/items", h.create)
	r.Patch("/v1/items/{id}", h.update)
	r.Delete("/v1/items/{id}", h.delete)
	r.Get("/v1/categories", h.listCategories)
	r.Get("/v1/vendors", h.listVendors)
}

// getOrList dispatches /v1/items: with {id} → GetByID, with sku query →
// GetBySKU, otherwise List.
func (h *Handler) getOrList(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	if idParam != "" {
		h.getByID(w, r, idParam)
		return
	}
	if sku := r.URL.Query().Get("sku"); sku != "" {
		h.getBySKU(w, r, sku)
		return
	}
	h.list(w, r)
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request, idParam string) {
	id, err := uuid.Parse(idParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", "id must be a UUID")
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}

	out, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		h.renderStoreError(w, err, "get item by id")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getBySKU(w http.ResponseWriter, r *http.Request, sku string) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	out, err := h.Store.GetBySKU(r.Context(), tenantID, sku)
	if err != nil {
		h.renderStoreError(w, err, "get item by sku")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getByBarcode is the keystone POS-scan endpoint.
func (h *Handler) getByBarcode(w http.ResponseWriter, r *http.Request) {
	barcode := r.URL.Query().Get("barcode")
	if barcode == "" {
		writeError(w, http.StatusBadRequest, "missing_barcode", "barcode query parameter is required")
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}

	out, err := h.Store.GetByBarcode(r.Context(), tenantID, barcode)
	if err != nil {
		h.renderStoreError(w, err, "get item by barcode")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{TenantID: tenantID}

	if v := q.Get("category_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_category_id", "category_id must be a UUID")
			return
		}
		f.CategoryID = &id
	}
	if v := q.Get("vendor_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_vendor_id", "vendor_id must be a UUID")
			return
		}
		f.VendorID = &id
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	// Accept either ?size= (dispatch brief) or ?limit= (codebase pagination
	// helper). Same column under the hood.
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			f.Limit = n
		}
	} else if v := q.Get("size"); v != "" {
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
	} else if v := q.Get("page"); v != "" {
		// page is 1-indexed in the dispatch wording.
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Offset = (n - 1) * f.Limit
		}
	}

	items, err := h.Store.List(r.Context(), f)
	if err != nil {
		h.renderStoreError(w, err, "list items")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"limit":  f.Limit,
		"offset": f.Offset,
		"count":  len(items),
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req CreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	out, err := h.Store.Create(r.Context(), req)
	if err != nil {
		h.renderStoreError(w, err, "create item")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", "id must be a UUID")
		return
	}
	// PATCH needs a tenant — for now from query. Auth middleware will
	// inject it once item-service joins the JWT path.
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var patch PatchRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &patch); err != nil {
			writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
			return
		}
	}
	out, err := h.Store.Update(r.Context(), tenantID, id, patch)
	if err != nil {
		h.renderStoreError(w, err, "update item")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", "id must be a UUID")
		return
	}
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	if err := h.Store.Delete(r.Context(), tenantID, id); err != nil {
		h.renderStoreError(w, err, "delete item")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	cats, err := h.Store.ListCategories(r.Context(), tenantID)
	if err != nil {
		h.renderStoreError(w, err, "list categories")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats, "count": len(cats)})
}

func (h *Handler) listVendors(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromQuery(w, r)
	if !ok {
		return
	}
	vends, err := h.Store.ListVendors(r.Context(), tenantID)
	if err != nil {
		h.renderStoreError(w, err, "list vendors")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vendors": vends, "count": len(vends)})
}

// ─────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────

// tenantFromQuery extracts and parses the tenant identifier. Accepts
// either ?tenant_id= or ?merchant_id= (alias) for migration tolerance.
// Returns (uuid, true) on success; writes a 400 and returns (_, false)
// otherwise.
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
		writeError(w, http.StatusBadRequest, "malformed_tenant",
			"tenant_id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

// renderStoreError maps domain sentinels to HTTP status codes.
func (h *Handler) renderStoreError(w http.ResponseWriter, err error, op string) {
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

// silence unused import lint when fmt isn't used inline
var _ = fmt.Sprintf
