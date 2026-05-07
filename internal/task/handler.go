// internal/task/handler.go — HTTP layer for the directed-task queue.
//
// Routes (all under /v1/tasks):
//   POST   /v1/tasks                    — create a task
//   GET    /v1/tasks/next               — claim next queued task for an employee
//   GET    /v1/tasks/{id}               — get a task by ID
//   PATCH  /v1/tasks/{id}/status        — advance task status
//   POST   /v1/tasks/{id}/exception     — log an exception
//   POST   /v1/tasks/{id}/skip          — skip with reason
package task

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler is the HTTP layer for the task package.
type Handler struct {
	store  *Store
	logger *zap.Logger
}

// NewHandler returns a Handler backed by store.
func NewHandler(store *Store, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{store: store, logger: logger}
}

// Mount registers all task routes on the given chi router.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/v1/tasks", h.createTask)
	r.Get("/v1/tasks/next", h.getNext)
	r.Get("/v1/tasks/{id}", h.getByID)
	r.Patch("/v1/tasks/{id}/status", h.updateStatus)
	r.Post("/v1/tasks/{id}/exception", h.logException)
	r.Post("/v1/tasks/{id}/skip", h.skip)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	clean, err := ValidateCreate(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	t, err := h.store.Create(r.Context(), clean)
	if err != nil {
		h.logger.Error("create task", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) getNext(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuidParam(r, "tenant_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_tenant_id", err.Error())
		return
	}
	employeeID, err := uuidParam(r, "employee_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_employee_id", err.Error())
		return
	}
	t, err := h.store.GetNext(r.Context(), tenantID, employeeID)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNoContent, "no_task", "no queued tasks")
		return
	}
	if err != nil {
		h.logger.Error("get next", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) getByID(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuidParam(r, "tenant_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_tenant_id", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return
	}
	t, err := h.store.GetByID(r.Context(), tenantID, id)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task_not_found", "")
		return
	}
	if err != nil {
		h.logger.Error("get by id", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuidParam(r, "tenant_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_tenant_id", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return
	}
	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	t, err := h.store.UpdateStatus(r.Context(), tenantID, id, req.Status)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task_not_found", "")
		return
	}
	if errors.Is(err, ErrInvalidTransition) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_transition", err.Error())
		return
	}
	if err != nil {
		h.logger.Error("update status", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) logException(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuidParam(r, "tenant_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_tenant_id", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return
	}
	var req ExceptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	ex, err := h.store.LogException(r.Context(), tenantID, id, req)
	if errors.Is(err, ErrInvalidExceptionCode) {
		writeErr(w, http.StatusBadRequest, "invalid_reason_code", err.Error())
		return
	}
	if err != nil {
		h.logger.Error("log exception", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, ex)
}

func (h *Handler) skip(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuidParam(r, "tenant_id", true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_tenant_id", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return
	}
	var req SkipRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Reason == "" {
		writeErr(w, http.StatusBadRequest, "missing_reason", "reason is required for skip")
		return
	}
	t, err := h.store.Skip(r.Context(), tenantID, id, req.Reason)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task_not_found", "")
		return
	}
	if errors.Is(err, ErrInvalidTransition) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_transition", err.Error())
		return
	}
	if err != nil {
		h.logger.Error("skip task", zap.Error(err))
		writeErr(w, http.StatusInternalServerError, "store_error", "")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// uuidParam reads a UUID from a query parameter. required=true 422s if absent.
func uuidParam(r *http.Request, name string, required bool) (uuid.UUID, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		if required {
			return uuid.Nil, errors.New(name + " query param is required")
		}
		return uuid.Nil, nil
	}
	return uuid.Parse(v)
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errBody{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
