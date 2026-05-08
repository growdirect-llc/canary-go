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

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/protocol/sub3"
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
// backed by a stubStore, bypassing the real *Store. Existing tests
// don't exercise the T-C ownership check, so the wrapper injects
// platform-scope claims (TenantID == uuid.Nil) into every request —
// which the auth gate accepts. Tests that DO exercise the ownership
// check (TestHandler_POST_*_OwnerMismatch / _Unauthenticated) build
// requests directly without this wrapper.
func handlerWithStub(stub *stubStore) http.Handler {
	h := &Handler{
		store:     stub,
		inscriber: &sub3.StubInscriber{},
		logger:    nil,
	}
	r := chi.NewRouter()
	r.Use(injectPlatformClaims)
	h.Mount(r)
	return r
}

// injectPlatformClaims adds platform-scope identity.Claims to every
// request — used by handlerWithStub so the existing test suite
// doesn't have to thread auth context through every NewRequest call.
func injectPlatformClaims(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := identity.InjectClaims(r.Context(), identity.Claims{
			TenantID:   uuid.Nil,
			AuthMethod: identity.AuthMethodAPIKey,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

// ─── T-C ownership-proof tests ─────────────────────────────────────

// TestHandler_POST_401_NoClaims verifies that the register endpoint
// rejects requests with no identity.Claims — the production wiring
// places APIKeyMiddleware in front, so a missing-key 401 is what
// genuine unauthenticated traffic returns.
func TestHandler_POST_401_NoClaims(t *testing.T) {
	t.Parallel()
	stub := newStubStore()
	h := &Handler{store: stub, inscriber: &sub3.StubInscriber{}}
	r := chi.NewRouter()
	h.Mount(r) // no claims-injection middleware

	body, _ := json.Marshal(map[string]string{
		"name":       "ghost.jeffe",
		"owner_id":   uuid.New().String(),
		"owner_type": "merchant",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "unauthenticated" {
		t.Errorf("error code: got %q, want unauthenticated", resp["code"])
	}
}

// TestHandler_POST_403_OwnerMismatch verifies that a tenant-scoped
// API key cannot register a name claiming a different tenant's
// owner_id — prevents impersonation via spoofed registration.
func TestHandler_POST_403_OwnerMismatch(t *testing.T) {
	t.Parallel()
	stub := newStubStore()
	h := &Handler{store: stub, inscriber: &sub3.StubInscriber{}}
	r := chi.NewRouter()
	caller := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := identity.InjectClaims(req.Context(), identity.Claims{
				TenantID:   caller,
				AuthMethod: identity.AuthMethodAPIKey,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.Mount(r)

	other := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	body, _ := json.Marshal(map[string]string{
		"name":       "spoofed.jeffe",
		"owner_id":   other.String(),
		"owner_type": "merchant",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "owner_mismatch" {
		t.Errorf("error code: got %q, want owner_mismatch", resp["code"])
	}
	if _, exists := stub.regs["spoofed.jeffe"]; exists {
		t.Error("forbidden registration was nonetheless inserted into the store")
	}
}

// TestHandler_POST_201_OwnTenant verifies the happy path: tenant-
// scoped key registering a name with matching owner_id succeeds.
func TestHandler_POST_201_OwnTenant(t *testing.T) {
	t.Parallel()
	stub := newStubStore()
	h := &Handler{store: stub, inscriber: &sub3.StubInscriber{}}
	r := chi.NewRouter()
	caller := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := identity.InjectClaims(req.Context(), identity.Claims{
				TenantID:   caller,
				AuthMethod: identity.AuthMethodAPIKey,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.Mount(r)

	body, _ := json.Marshal(map[string]string{
		"name":       "owner.jeffe",
		"owner_id":   caller.String(),
		"owner_type": "merchant",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/namespace", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
}
