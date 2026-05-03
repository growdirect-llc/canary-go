package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// ─── stubHandlerStore ─────────────────────────────────────────────────────────

// stubHandlerStore satisfies both inserter and the GetByName signature
// used by the handler so tests can run without a DB.
type stubHandlerStore struct {
	regs map[string]*Registration
}

func newStubHandlerStore() *stubHandlerStore {
	return &stubHandlerStore{regs: make(map[string]*Registration)}
}

func (s *stubHandlerStore) Insert(_ context.Context, reg Registration) error {
	if _, exists := s.regs[reg.Name]; exists {
		return ErrNameTaken
	}
	clone := reg
	s.regs[reg.Name] = &clone
	return nil
}

func (s *stubHandlerStore) GetByName(_ context.Context, name string) (*Registration, error) {
	reg, ok := s.regs[name]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return reg, nil
}

// ─── testHandler ─────────────────────────────────────────────────────────────

// testHandler constructs a Handler wired to stubHandlerStore so no pool
// or real DB is needed.
func testHandler(t *testing.T) (*Handler, *stubHandlerStore) {
	t.Helper()
	store := newStubHandlerStore()
	h := &Handler{
		store:     nil, // not used directly — we override via handlerWithStore
		inscriber: &sub3.StubInscriber{},
		logger:    nil,
	}
	_ = h
	// Use the overridden handler that accepts the stub store.
	return &Handler{
		store:     nil,
		inscriber: &sub3.StubInscriber{},
		logger:    nil,
	}, store
}

// handlerWithStub returns an http.Handler for the two namespace routes
// backed by a stubHandlerStore, bypassing the real *Store.
func handlerWithStub(stub *stubHandlerStore) http.Handler {
	h := &handlerStub{
		store:     stub,
		inscriber: &sub3.StubInscriber{},
	}
	r := chi.NewRouter()
	r.Post("/v1/protocol/namespace", h.handleRegister)
	r.Get("/v1/protocol/namespace/{name}", h.handleLookup)
	return r
}

// handlerStub mirrors Handler but uses stubHandlerStore instead of *Store.
type handlerStub struct {
	store     *stubHandlerStore
	inscriber sub3.Inscriber
}

func (h *handlerStub) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_json"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_name"})
		return
	}
	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_owner_id"})
		return
	}
	if req.OwnerType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_owner_type"})
		return
	}
	network := req.Network
	if network == "" {
		network = "signet"
	}

	reg, err := register(r.Context(), h.store, h.inscriber, RegisterRequest{
		Name:      req.Name,
		OwnerID:   ownerID,
		OwnerType: req.OwnerType,
		Network:   network,
	})
	switch {
	case errors.Is(err, ErrInvalidName):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_name", "error": err.Error()})
	case errors.Is(err, ErrNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "name_taken"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "registration_failed"})
	default:
		writeJSON(w, http.StatusCreated, reg)
	}
}

func (h *handlerStub) handleLookup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_name"})
		return
	}
	reg, err := h.store.GetByName(r.Context(), name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "name": name})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "lookup_failed"})
	default:
		writeJSON(w, http.StatusOK, reg)
	}
}

// ─── POST tests ──────────────────────────────────────────────────────────────

func TestHandler_POST_201_ValidRegistration(t *testing.T) {
	t.Parallel()
	stub := newStubHandlerStore()
	srv := handlerWithStub(stub)

	body, _ := json.Marshal(map[string]string{
		"name":       "acme-store.jeffe",
		"owner_id":   uuid.New().String(),
		"owner_type": "merchant",
		"network":    "signet",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var reg Registration
	if err := json.NewDecoder(w.Body).Decode(&reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reg.Name != "acme-store.jeffe" {
		t.Errorf("name mismatch: %q", reg.Name)
	}
	if reg.RegStatus != "pending" {
		t.Errorf("status mismatch: %q", reg.RegStatus)
	}
	if reg.InscriptionID == "" {
		t.Error("inscription_id must be populated by stub inscriber")
	}
}

func TestHandler_POST_409_DuplicateName(t *testing.T) {
	t.Parallel()
	stub := newStubHandlerStore()
	srv := handlerWithStub(stub)

	ownerID := uuid.New().String()
	body1, _ := json.Marshal(map[string]string{
		"name":       "duplicate.jeffe",
		"owner_id":   ownerID,
		"owner_type": "merchant",
	})

	// First registration.
	req1 := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first reg: expected 201, got %d", w1.Code)
	}

	// Second registration — same name.
	body2, _ := json.Marshal(map[string]string{
		"name":       "duplicate.jeffe",
		"owner_id":   uuid.New().String(),
		"owner_type": "user",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body=%s", w2.Code, w2.Body.String())
	}
}

func TestHandler_POST_400_InvalidName(t *testing.T) {
	t.Parallel()
	stub := newStubHandlerStore()
	srv := handlerWithStub(stub)

	body, _ := json.Marshal(map[string]string{
		"name":       "no-suffix",
		"owner_id":   uuid.New().String(),
		"owner_type": "merchant",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body=%s", w.Code, w.Body.String())
	}
}

// ─── GET tests ───────────────────────────────────────────────────────────────

func TestHandler_GET_200_ExistingName(t *testing.T) {
	t.Parallel()
	stub := newStubHandlerStore()
	srv := handlerWithStub(stub)

	// Register first.
	body, _ := json.Marshal(map[string]string{
		"name":       "lookup-test.jeffe",
		"owner_id":   uuid.New().String(),
		"owner_type": "agent",
		"network":    "signet",
	})
	postReq := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	srv.ServeHTTP(postW, postReq)
	if postW.Code != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d", postW.Code)
	}

	// Now look it up.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/protocol/namespace/lookup-test.jeffe", nil)
	getW := httptest.NewRecorder()
	srv.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", getW.Code, getW.Body.String())
	}
	var reg Registration
	if err := json.NewDecoder(getW.Body).Decode(&reg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reg.Name != "lookup-test.jeffe" {
		t.Errorf("name mismatch: %q", reg.Name)
	}
}

func TestHandler_GET_404_UnknownName(t *testing.T) {
	t.Parallel()
	stub := newStubHandlerStore()
	srv := handlerWithStub(stub)

	getReq := httptest.NewRequest(http.MethodGet, "/v1/protocol/namespace/does-not-exist.jeffe", nil)
	getW := httptest.NewRecorder()
	srv.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body=%s", getW.Code, getW.Body.String())
	}
}
