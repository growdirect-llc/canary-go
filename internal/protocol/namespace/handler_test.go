package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// ─── stubStore ───────────────────────────────────────────────────────────────

// stubStore satisfies NamespaceStore so tests can run without a DB.
type stubStore struct {
	regs map[string]*Registration
}

func newStubStore() *stubStore {
	return &stubStore{regs: make(map[string]*Registration)}
}

func (s *stubStore) Insert(_ context.Context, reg Registration) error {
	if _, exists := s.regs[reg.Name]; exists {
		return ErrNameTaken
	}
	clone := reg
	s.regs[reg.Name] = &clone
	return nil
}

func (s *stubStore) GetByName(_ context.Context, name string) (*Registration, error) {
	reg, ok := s.regs[name]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return reg, nil
}

func (s *stubStore) GetByOwner(_ context.Context, ownerID uuid.UUID, ownerType string) ([]Registration, error) {
	var out []Registration
	for _, reg := range s.regs {
		if reg.OwnerID == ownerID && reg.OwnerType == ownerType {
			out = append(out, *reg)
		}
	}
	return out, nil
}

func (s *stubStore) UpdateInscription(_ context.Context, regID uuid.UUID,
	inscriptionID, btcTxID string, blockHeight int64, status string) error {
	for _, reg := range s.regs {
		if reg.RegID == regID {
			reg.InscriptionID = inscriptionID
			reg.BtcTxID = btcTxID
			reg.BtcBlockHeight = blockHeight
			reg.RegStatus = status
			return nil
		}
	}
	return ErrNotFound
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// handlerWithStub returns an http.Handler for the two namespace routes
// backed by a stubStore, bypassing the real *Store.
func handlerWithStub(stub *stubStore) http.Handler {
	h := &Handler{
		store:     stub,
		inscriber: &sub3.StubInscriber{},
		logger:    nil,
	}
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// ─── POST tests ──────────────────────────────────────────────────────────────

func TestHandler_POST_201_ValidRegistration(t *testing.T) {
	t.Parallel()
	stub := newStubStore()
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
	stub := newStubStore()
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
	stub := newStubStore()
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
	stub := newStubStore()
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
	stub := newStubStore()
	srv := handlerWithStub(stub)

	getReq := httptest.NewRequest(http.MethodGet, "/v1/protocol/namespace/does-not-exist.jeffe", nil)
	getW := httptest.NewRecorder()
	srv.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body=%s", getW.Code, getW.Body.String())
	}
}
