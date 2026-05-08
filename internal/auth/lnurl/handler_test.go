package lnurl_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/auth/lnurl"
)

// ─── in-memory stub store ─────────────────────────────────────────────────────

// stubStore satisfies lnurl.LNURLStore without a DB.
type stubStore struct {
	mu         sync.Mutex
	challenges map[string]*lnurl.Challenge
	keys       map[string]uuid.UUID
}

func newStubStore() *stubStore {
	return &stubStore{
		challenges: make(map[string]*lnurl.Challenge),
		keys:       make(map[string]uuid.UUID),
	}
}

func (s *stubStore) InsertChallenge(_ context.Context, k1 string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.challenges[k1] = &lnurl.Challenge{
		K1:        k1,
		Status:    "pending",
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
		UpdatedAt: now,
	}
	return nil
}

func (s *stubStore) GetChallenge(_ context.Context, k1 string) (*lnurl.Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.challenges[k1]
	if !ok {
		return nil, lnurl.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *stubStore) MarkUsed(_ context.Context, k1 string, ownerID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.challenges[k1]
	if !ok {
		return lnurl.ErrNotFound
	}
	if c.Status == "used" {
		return lnurl.ErrAlreadyUsed
	}
	if c.Status == "expired" || time.Now().After(c.ExpiresAt) {
		return lnurl.ErrExpired
	}
	c.Status = "used"
	c.LinkedID = &ownerID
	return nil
}

func (s *stubStore) UpsertLinkedKey(_ context.Context, linkingKey string, ownerID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[linkingKey] = ownerID
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestHandler(store lnurl.LNURLStore) *lnurl.Handler {
	logger, _ := zap.NewDevelopment()
	return &lnurl.Handler{
		Store:  store,
		Secret: []byte("test-secret-32-bytes-padded-xxxx"),
		Stub:   true,
		Scheme: "http",
		Host:   "localhost:8080",
		Logger: logger,
	}
}

func buildRouter(h *lnurl.Handler) http.Handler {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestGetLNURL(t *testing.T) {
	store := newStubStore()
	h := newTestHandler(store)
	r := buildRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/lnurl", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["lnurl"] == "" {
		t.Error("lnurl field missing or empty")
	}
	if resp["k1"] == "" {
		t.Error("k1 field missing or empty")
	}
	if !strings.HasPrefix(resp["lnurl"], "lnurl") {
		t.Errorf("lnurl should start with 'lnurl', got %q", resp["lnurl"])
	}
}

func TestChallenge_NotFound(t *testing.T) {
	store := newStubStore()
	h := newTestHandler(store)
	r := buildRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/auth/lnurl/challenge?tag=login&k1=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCallback_StubMode(t *testing.T) {
	store := newStubStore()
	h := newTestHandler(store)
	r := buildRouter(h)

	// Step 1: get LNURL + k1.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/lnurl", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("getLNURL: want 200, got %d", rec.Code)
	}
	var lnurlResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&lnurlResp)
	k1 := lnurlResp["k1"]

	// Step 2: challenge handshake.
	req = httptest.NewRequest(http.MethodGet,
		"/v1/auth/lnurl/challenge?tag=login&k1="+k1, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Step 3: callback (stub mode — any sig/key bypasses verification).
	fakeSig := "304402200102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f200220" +
		"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	fakeKey := "02" + strings.Repeat("ab", 32)
	req = httptest.NewRequest(http.MethodGet,
		"/v1/auth/lnurl/callback?tag=login&k1="+k1+"&sig="+fakeSig+"&key="+fakeKey, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var cbResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&cbResp)
	if cbResp["status"] != "OK" {
		t.Errorf("callback status: want 'OK', got %q", cbResp["status"])
	}

	// Step 4: poll session — should return token now.
	req = httptest.NewRequest(http.MethodGet, "/v1/auth/session?k1="+k1, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pollSession: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var sessResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&sessResp)
	if sessResp["status"] != "ok" {
		t.Errorf("session status: want 'ok', got %q", sessResp["status"])
	}
	if sessResp["token"] == "" {
		t.Error("session token missing")
	}
}

func TestCallback_AlreadyUsed(t *testing.T) {
	store := newStubStore()
	h := newTestHandler(store)
	r := buildRouter(h)

	// Get a k1.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/lnurl", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var lnurlResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&lnurlResp)
	k1 := lnurlResp["k1"]

	fakeKey := "02" + strings.Repeat("cd", 32)
	fakeSig := "304402200102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f200220" +
		"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	callbackURL := "/v1/auth/lnurl/callback?tag=login&k1=" + k1 + "&sig=" + fakeSig + "&key=" + fakeKey

	// First callback — succeeds.
	req = httptest.NewRequest(http.MethodGet, callbackURL, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first callback: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Second callback — must return 409.
	req = httptest.NewRequest(http.MethodGet, callbackURL, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second callback: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPollSession_Pending(t *testing.T) {
	store := newStubStore()
	h := newTestHandler(store)
	r := buildRouter(h)

	// Get a k1 but do NOT call callback.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/lnurl", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var lnurlResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&lnurlResp)
	k1 := lnurlResp["k1"]

	req = httptest.NewRequest(http.MethodGet, "/v1/auth/session?k1="+k1, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var sessResp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&sessResp)
	if sessResp["status"] != "pending" {
		t.Errorf("want status=pending, got %q", sessResp["status"])
	}
}
