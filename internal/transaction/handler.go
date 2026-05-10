// internal/transaction/handler.go
//
// HTTP layer for the transaction service. Conventions per
// docs/conventions.md: thin handler that parses + delegates +
// renders; business logic stays in store.go.

package transaction

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity"
)

// Handler is the chi-compatible handler factory.
type Handler struct {
	Store  *Store
	Logger *zap.Logger
	Now    func() time.Time
}

func New(store *Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		Store:  store,
		Logger: logger,
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// Mount registers all transaction routes on a chi router. Routes are
// split into per-scope subgroups so identity.RequireScopeMiddleware
// enforces least-privilege at the route boundary (GRO-906).
//
//   - GET routes require transaction:read
//   - POST routes (create / void / make-return) require transaction:write
//
// The caller (cmd/transaction/main.go) is expected to mount Mount inside
// an APIKeyMiddleware group so claims are populated before the scope
// check runs; missing claims yields 401 from the scope middleware.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(identity.RequireScopeMiddleware(identity.ScopeTransactionRead))
		r.Get("/v1/transactions/by-receipt-number", h.byReceipt) // most-specific first
		r.Get("/v1/transactions/{id}", h.getByID)
		r.Get("/v1/transactions", h.list)
	})
	r.Group(func(r chi.Router) {
		r.Use(identity.RequireScopeMiddleware(identity.ScopeTransactionWrite))
		r.Post("/v1/transactions", h.create)
		r.Post("/v1/transactions/{id}/voids", h.void)
		r.Post("/v1/transactions/{id}/returns", h.makeReturn)
	})
}

// requireTenant returns the authenticated tenant or writes 401 and
// returns false. Every transaction endpoint is tenant-scoped — there
// is no platform-scope read path.
func requireTenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	c, ok := identity.ClaimsFromContext(r.Context())
	if !ok || c.TenantID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "missing tenant claim")
		return uuid.Nil, false
	}
	return c.TenantID, true
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
	authTenant, ok := requireTenant(w, r)
	if !ok {
		return
	}
	// Body tenant_id is retained for wire compatibility — if set, it
	// must match the authenticated tenant or the request is rejected
	// with 403 tenant_mismatch. Defense in depth: overwrite the body
	// tenant with the auth claim before persisting so the body value
	// cannot escape into the store.
	if req.TenantID != uuid.Nil {
		if err := identity.AssertBodyTenantMatches(r.Context(), req.TenantID); err != nil {
			if errors.Is(err, identity.ErrTenantMismatch) {
				writeError(w, http.StatusForbidden, "tenant_mismatch",
					"tenant_id does not match authenticated tenant")
				return
			}
			h.Logger.Error("assert tenant", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "tenant_check_failed", "")
			return
		}
	}
	req.TenantID = authTenant
	out, err := h.Store.Create(r.Context(), req)
	if err != nil {
		h.renderStoreErr(w, err, "create transaction")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	out, err := h.Store.GetByID(r.Context(), tenantID, id)
	if err != nil {
		h.renderStoreErr(w, err, "get transaction")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) byReceipt(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	locStr := q.Get("location_id")
	bizDate := q.Get("business_date")
	txNum := q.Get("transaction_number")
	if locStr == "" || bizDate == "" || txNum == "" {
		writeError(w, http.StatusBadRequest, "missing_params",
			"location_id + business_date + transaction_number required")
		return
	}
	locID, err := uuid.Parse(locStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_location_id", err.Error())
		return
	}
	out, err := h.Store.GetByReceiptNumber(r.Context(), tenantID, locID, bizDate, txNum)
	if err != nil {
		h.renderStoreErr(w, err, "get by receipt")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := ListFilters{TenantID: tenantID}
	if v := q.Get("location_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_location_id", err.Error())
			return
		}
		f.LocationID = &id
	}
	if v := q.Get("from"); v != "" {
		f.BusinessDateMin = &v
	}
	if v := q.Get("to"); v != "" {
		f.BusinessDateMax = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	if v := q.Get("cashier_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_cashier_id", err.Error())
			return
		}
		f.CashierID = &id
	}
	if v := q.Get("customer_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_customer_id", err.Error())
			return
		}
		f.CustomerID = &id
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
	items, err := h.Store.List(r.Context(), f)
	if err != nil {
		h.renderStoreErr(w, err, "list transactions")
		return
	}
	writeJSON(w, http.StatusOK, ListResponse{
		Items:  items,
		Limit:  f.Limit,
		Offset: f.Offset,
		Count:  len(items),
	})
}

func (h *Handler) void(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req VoidRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "missing_reason", "reason is required")
		return
	}
	out, err := h.Store.Void(r.Context(), tenantID, id, req, h.Now())
	if err != nil {
		h.renderStoreErr(w, err, "void transaction")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) makeReturn(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req ReturnRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "missing_reason", "reason is required")
		return
	}
	out, err := h.Store.Return(r.Context(), tenantID, id, req, h.Now())
	if err != nil {
		h.renderStoreErr(w, err, "return transaction")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// helpers

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
