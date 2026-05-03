// internal/inventory/handler.go
//
// HTTP handlers for the inventory service. Mounts five routes under /v1:
//
//   GET  /v1/inventory/positions/{item_id}/{location_id}
//   GET  /v1/inventory/positions
//   POST /v1/inventory/movements
//   GET  /v1/inventory/movements
//
// /health is mounted at the cmd/inventory/main.go level alongside chi
// middleware, so it doesn't appear here.
package inventory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// defaultPageSize matches the dispatch (50/page default).
	defaultPageSize = 50
	// maxPageSize keeps a single response from blowing the wire.
	maxPageSize = 200
)

// Handler is the HTTP layer for the inventory package. Inject the read
// and write surfaces (both satisfied by *Store in production).
type Handler struct {
	Reader PositionReader
	Writer MovementWriter
	Logger *zap.Logger
	Now    func() time.Time
}

// New returns a Handler with sensible defaults.
func New(r PositionReader, w MovementWriter, l *zap.Logger) *Handler {
	if l == nil {
		l = zap.NewNop()
	}
	return &Handler{
		Reader: r,
		Writer: w,
		Logger: l,
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// Mount registers all inventory routes on the given chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/inventory/positions/{item_id}/{location_id}", h.getPosition)
	r.Get("/v1/inventory/positions", h.listPositions)
	r.Post("/v1/inventory/movements", h.appendMovement)
	r.Get("/v1/inventory/movements", h.listMovements)
}

// getPosition handles GET /v1/inventory/positions/{item_id}/{location_id}.
// The merchant_id (tenant_id) comes from the X-Canary-Merchant header, the
// same convention used by the protocol gateway.
func (h *Handler) getPosition(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_merchant", err.Error())
		return
	}

	itemID, err := uuid.Parse(chi.URLParam(r, "item_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_item_id", "item_id must be a UUID")
		return
	}
	locationID, err := uuid.Parse(chi.URLParam(r, "location_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_location_id", "location_id must be a UUID")
		return
	}

	pos, err := h.Reader.GetPosition(r.Context(), tenantID, itemID, locationID)
	if err != nil {
		if errors.Is(err, ErrPositionNotFound) {
			writeError(w, http.StatusNotFound, "position_not_found", "")
			return
		}
		h.Logger.Error("get position", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, pos)
}

// listPositions handles GET /v1/inventory/positions with optional
// location_id and item_id filters. Pagination is page (1-based) + size.
func (h *Handler) listPositions(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_merchant", err.Error())
		return
	}

	var locationID, itemID *uuid.UUID
	if v := r.URL.Query().Get("location_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_location_id", "location_id must be a UUID")
			return
		}
		locationID = &id
	}
	if v := r.URL.Query().Get("item_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_item_id", "item_id must be a UUID")
			return
		}
		itemID = &id
	}

	page, size := pageSize(r)
	offset := (page - 1) * size

	rows, err := h.Reader.ListPositions(r.Context(), tenantID, locationID, itemID, size, offset)
	if err != nil {
		h.Logger.Error("list positions", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, PositionListResponse{
		Items: rows,
		Page:  page,
		Size:  size,
	})
}

// appendMovement handles POST /v1/inventory/movements. Validates the
// payload, then delegates to the Store. The response carries both the
// new movement and the freshly-updated position.
func (h *Handler) appendMovement(w http.ResponseWriter, r *http.Request) {
	var req AppendMovementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	clean, err := ValidateAppendRequest(req)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidMovementType):
			writeError(w, http.StatusBadRequest, "invalid_movement_type", err.Error())
		case errors.Is(err, ErrInvalidQuantity):
			writeError(w, http.StatusBadRequest, "invalid_quantity", err.Error())
		case errors.Is(err, ErrMissingField):
			writeError(w, http.StatusBadRequest, "missing_field", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		}
		return
	}

	mov, pos, err := h.Writer.AppendMovement(r.Context(), clean, h.Now())
	if err != nil {
		h.Logger.Error("append movement", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, AppendMovementResponse{
		Movement: *mov,
		Position: *pos,
	})
}

// listMovements handles GET /v1/inventory/movements with required
// item_id + location_id, optional from/to (RFC3339), and pagination.
func (h *Handler) listMovements(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_merchant", err.Error())
		return
	}

	itemID, err := uuid.Parse(r.URL.Query().Get("item_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_item_id", "item_id query param must be a UUID")
		return
	}
	locationID, err := uuid.Parse(r.URL.Query().Get("location_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_location_id", "location_id query param must be a UUID")
		return
	}

	var from, to *time.Time
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_from", "from must be RFC3339")
			return
		}
		from = &t
	}
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_to", "to must be RFC3339")
			return
		}
		to = &t
	}

	page, size := pageSize(r)
	offset := (page - 1) * size

	rows, err := h.Writer.ListMovements(r.Context(), tenantID, itemID, locationID, from, to, size, offset)
	if err != nil {
		h.Logger.Error("list movements", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, MovementListResponse{
		Items: rows,
		Page:  page,
		Size:  size,
	})
}

// HeaderMerchant carries the tenant scope on read endpoints. Kept
// consistent with the protocol gateway (internal/protocol/webhook).
const HeaderMerchant = "X-Canary-Merchant"

func tenantFromHeader(r *http.Request) (uuid.UUID, error) {
	v := r.Header.Get(HeaderMerchant)
	if v == "" {
		return uuid.Nil, errors.New("X-Canary-Merchant header is required")
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, errors.New("X-Canary-Merchant must be a UUID")
	}
	return id, nil
}

// pageSize parses ?page=N&size=M with safe defaults.
func pageSize(r *http.Request) (int, int) {
	page := 1
	size := defaultPageSize
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}
	if v := r.URL.Query().Get("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= maxPageSize {
			size = n
		}
	}
	return page, size
}

// errorBody is the JSON shape returned on every non-2xx response. Mirrors
// the protocol gateway's error contract for cross-service consistency.
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
