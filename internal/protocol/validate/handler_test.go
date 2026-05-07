package validate_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
)

// ─── stub store ──────────────────────────────────────────────────────────────

type stubStore struct {
	tokens  map[uuid.UUID]*validate.VerificationToken
	proofs  map[string]*validate.AnchorProof
	counter int64
}

func newStubStore() *stubStore {
	return &stubStore{
		tokens: make(map[uuid.UUID]*validate.VerificationToken),
		proofs: make(map[string]*validate.AnchorProof),
	}
}

func (s *stubStore) InsertToken(_ context.Context, eventHash string, satoshiPrice int64) (*validate.VerificationToken, error) {
	tok := &validate.VerificationToken{
		TokenID:      uuid.New(),
		EventHash:    eventHash,
		SatoshiPrice: satoshiPrice,
		Status:       "pending",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}
	s.tokens[tok.TokenID] = tok
	return tok, nil
}

func (s *stubStore) GetToken(_ context.Context, tokenID uuid.UUID) (*validate.VerificationToken, error) {
	tok, ok := s.tokens[tokenID]
	if !ok {
		return nil, validate.ErrNotFound
	}
	return tok, nil
}

func (s *stubStore) ConsumeToken(_ context.Context, tokenID uuid.UUID) error {
	tok, ok := s.tokens[tokenID]
	if !ok {
		return validate.ErrNotFound
	}
	if tok.Status == "consumed" {
		return validate.ErrAlreadyConsumed
	}
	if tok.Status == "expired" || time.Now().After(tok.ExpiresAt) {
		return validate.ErrExpired
	}
	tok.Status = "consumed"
	now := time.Now()
	tok.ConsumedAt = &now
	return nil
}

func (s *stubStore) GetAnchorProof(_ context.Context, eventHash string) (*validate.AnchorProof, error) {
	proof, ok := s.proofs[eventHash]
	if !ok {
		return nil, validate.ErrNotAnchored
	}
	return proof, nil
}

// addAnchoredEvent seeds the stub with a fully anchored event + valid proof.
func (s *stubStore) addAnchoredEvent(eventHash string) {
	// Build a single-leaf Merkle tree so VerifyProof passes.
	result, _ := sub3.BuildMerkleTree([]string{eventHash})

	raw, _ := json.Marshal(result.Proofs[0])
	network := "signet"
	status := "anchored"
	s.proofs[eventHash] = &validate.AnchorProof{
		EventHash:    eventHash,
		AnchorID:     uuid.New(),
		MerkleRoot:   result.Root,
		Network:      network,
		AnchorStatus: status,
		LeafIndex:    0,
		MerkleProof:  raw,
		AnchoredAt:   time.Now(),
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func newHandler(store validate.ValidationStore) (*validate.Handler, *StubL402Wrapper) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	l402 := &validate.StubL402{Secret: secret}
	h := &validate.Handler{
		Store:        store,
		L402:         l402,
		Logger:       zap.NewNop(),
		SatoshiPrice: 100,
	}
	return h, &StubL402Wrapper{inner: l402}
}

// StubL402Wrapper exposes L402 for tests that need to build auth headers.
type StubL402Wrapper struct {
	inner *validate.StubL402
}

func (w *StubL402Wrapper) IssueChallenge(tokenID uuid.UUID, satoshis int64) (string, string) {
	return w.inner.IssueChallenge(tokenID, satoshis)
}

func newRouter(h *validate.Handler) *chi.Mux {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// knownEventHash is a valid 64-char lowercase hex string (SHA-256 of "test-event-hash-canonical").
const knownEventHash = "fe9bc84c36fc931583324c1b3fe4bb2132c6184d4201088e1c13074159140891"

// ─── tests ───────────────────────────────────────────────────────────────────

// TestIssueChallenge_NotAnchored: POST with an event_hash that exists in the
// store but has no anchor → 200 with verified:false.
func TestIssueChallenge_NotAnchored(t *testing.T) {
	store := newStubStore()
	// Do NOT add an anchor — GetAnchorProof returns ErrNotAnchored.
	h, _ := newHandler(store)
	r := newRouter(h)

	body, _ := json.Marshal(map[string]string{"event_hash": knownEventHash})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/verify",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal("decode response:", err)
	}
	if verified, _ := resp["verified"].(bool); verified {
		t.Error("expected verified:false for unanchored event")
	}
	if status, _ := resp["anchor_status"].(string); status != "pending" {
		t.Errorf("expected anchor_status=pending, got %q", status)
	}
}

// TestIssueChallenge_NoAuth_Returns402: POST without Authorization header
// and event IS anchored → 402 with WWW-Authenticate header.
func TestIssueChallenge_NoAuth_Returns402(t *testing.T) {
	store := newStubStore()
	store.addAnchoredEvent(knownEventHash)
	h, _ := newHandler(store)
	r := newRouter(h)

	body, _ := json.Marshal(map[string]string{"event_hash": knownEventHash})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/verify",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d: %s", w.Code, w.Body.String())
	}
	wwwAuth := w.Header().Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Error("expected WWW-Authenticate header on 402 response")
	}
	if len(wwwAuth) < 4 || wwwAuth[:4] != "L402" {
		t.Errorf("WWW-Authenticate should start with L402, got %q", wwwAuth)
	}
}

// TestConsumeToken_StubFlow: POST → get token_id → GET → 200 verified:true.
func TestConsumeToken_StubFlow(t *testing.T) {
	store := newStubStore()
	store.addAnchoredEvent(knownEventHash)
	h, _ := newHandler(store)
	r := newRouter(h)

	// Step 1: POST — should return 402 with token_id in body.
	body, _ := json.Marshal(map[string]string{"event_hash": knownEventHash})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/verify",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("step1 expected 402, got %d: %s", w.Code, w.Body.String())
	}

	var payResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&payResp); err != nil {
		t.Fatal("step1 decode:", err)
	}
	tokenIDStr, _ := payResp["token_id"].(string)
	if tokenIDStr == "" {
		t.Fatal("step1: no token_id in response")
	}

	// Step 2: GET /v1/protocol/verify/{token_id} — consume in stub mode.
	req2 := httptest.NewRequest(http.MethodGet,
		"/v1/protocol/verify/"+tokenIDStr, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("step2 expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var verResp validate.VerificationResponse
	if err := json.NewDecoder(w2.Body).Decode(&verResp); err != nil {
		t.Fatal("step2 decode:", err)
	}
	if !verResp.Verified {
		t.Error("expected verified:true for anchored + consumed token")
	}
	if verResp.EventHash != knownEventHash {
		t.Errorf("expected event_hash %s, got %s", knownEventHash, verResp.EventHash)
	}
	if verResp.SatoshiPrice != 100 {
		t.Errorf("expected satoshi_price 100, got %d", verResp.SatoshiPrice)
	}
}

// TestConsumeToken_AlreadyConsumed: consuming the same token twice → 410.
func TestConsumeToken_AlreadyConsumed(t *testing.T) {
	store := newStubStore()
	store.addAnchoredEvent(knownEventHash)
	h, _ := newHandler(store)
	r := newRouter(h)

	// Issue the challenge to get a token.
	body, _ := json.Marshal(map[string]string{"event_hash": knownEventHash})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/verify",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var payResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&payResp)
	tokenIDStr, _ := payResp["token_id"].(string)
	if tokenIDStr == "" {
		t.Fatal("AlreadyConsumed setup: no token_id in 402 response")
	}

	// First consume.
	req1 := httptest.NewRequest(http.MethodGet, "/v1/protocol/verify/"+tokenIDStr, nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first consume: expected 200, got %d", w1.Code)
	}

	// Second consume — should be 410.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/protocol/verify/"+tokenIDStr, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusGone {
		t.Fatalf("second consume: expected 410, got %d: %s", w2.Code, w2.Body.String())
	}
}

// TestIssueChallenge_InvalidHash: POST with bad event_hash → 400.
func TestIssueChallenge_InvalidHash(t *testing.T) {
	store := newStubStore()
	h, _ := newHandler(store)
	r := newRouter(h)

	body, _ := json.Marshal(map[string]string{"event_hash": "not-a-hash"})
	req := httptest.NewRequest(http.MethodPost, "/v1/protocol/verify",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
