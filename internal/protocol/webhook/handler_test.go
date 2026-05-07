package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	canaryhmac "github.com/growdirect-llc/rapidpos/internal/protocol/hmac"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/secrets"
)

// fixedClock returns a stable Now() for deterministic tests.
type fixedClock struct{ t time.Time }

func (f *fixedClock) Now() time.Time { return f.t }

// memNonceStore mirrors the one in hmac_test.go.
type memNonceStore struct{ seen map[string]time.Time }

func (m *memNonceStore) SeenOnce(_ context.Context, nonce string, ttl time.Duration) (bool, error) {
	if m.seen == nil {
		m.seen = make(map[string]time.Time)
	}
	now := time.Now()
	if at, ok := m.seen[nonce]; ok && now.Sub(at) < ttl {
		return false, nil
	}
	m.seen[nonce] = now
	return true, nil
}

// testFixture is a one-stop builder for handler tests.
type testFixture struct {
	merchantID uuid.UUID
	source     string
	secret     []byte
	resolver   *secrets.Memory
	pub        *publisher.Mock
	nonces     *memNonceStore
	now        time.Time
	router     *chi.Mux
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()

	merchantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa")
	source := "square"
	secret := []byte("test-hmac-secret-with-decent-entropy-12345")

	res := secrets.NewMemory()
	res.Add(secrets.Secret{
		MerchantID:    merchantID,
		SourceCode:    source,
		Secret:        secret,
		SignatureAlgo: "HMAC-SHA256",
		ReplayWindow:  5 * time.Minute,
	})

	pub := publisher.NewMock()
	nonces := &memNonceStore{}
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	h := New(res, pub, nonces, nil)
	h.Now = func() time.Time { return now }

	r := chi.NewRouter()
	h.Mount(r)

	return &testFixture{
		merchantID: merchantID,
		source:     source,
		secret:     secret,
		resolver:   res,
		pub:        pub,
		nonces:     nonces,
		now:        now,
		router:     r,
	}
}

// signedRequest builds a valid-but-customizable request. Override fields
// in the returned http.Request before sending to test failure modes.
func (f *testFixture) signedRequest(payload []byte, nonce string, ts time.Time) *http.Request {
	sigHex, _ := canaryhmac.Sign(f.secret, ts, nonce, payload)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+f.source, bytes.NewReader(payload))
	req.Header.Set(HeaderMerchant, f.merchantID.String())
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(canaryhmac.HeaderNonce, nonce)
	req.Header.Set(canaryhmac.HeaderSignature, sigHex)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestHandler_HappyPath_Returns200AndPublishes(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{"event":"order.created","id":"o_123","amount":4995}`)
	req := f.signedRequest(payload, "nonce-1", f.now)

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "accepted" {
		t.Errorf("status: got %q want accepted", resp.Status)
	}
	if _, err := uuid.Parse(resp.EventID); err != nil {
		t.Errorf("event_id not a uuid: %v", err)
	}
	if len(resp.EventHash) != 64 {
		t.Errorf("event_hash should be 64 hex chars, got %d", len(resp.EventHash))
	}

	events := f.pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("publisher: got %d events, want 1", len(events))
	}
	got := events[0]
	if got.SourceCode != "square" {
		t.Errorf("event source_code: got %q", got.SourceCode)
	}
	if got.MerchantID != f.merchantID {
		t.Errorf("event merchant_id: got %s want %s", got.MerchantID, f.merchantID)
	}
	if got.EventID.String() != resp.EventID {
		t.Errorf("event_id mismatch: response=%s event=%s", resp.EventID, got.EventID)
	}
	if string(got.Payload) != string(payload) {
		t.Errorf("payload not preserved verbatim")
	}
}

// ---------------------------------------------------------------------------
// Error paths
// ---------------------------------------------------------------------------

func TestHandler_BadHMAC_Returns401(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{"x":1}`)
	req := f.signedRequest(payload, "n", f.now)
	// Tamper the signature so verify fails.
	req.Header.Set(canaryhmac.HeaderSignature,
		strings.Repeat("0", 64))

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401; body=%s", rec.Code, rec.Body.String())
	}
	if len(f.pub.Snapshot()) != 0 {
		t.Errorf("publisher should not be called on auth failure")
	}
}

func TestHandler_TamperedPayload_Returns401(t *testing.T) {
	f := newFixture(t)
	original := []byte(`{"amount":100}`)
	tampered := []byte(`{"amount":999}`)
	// Sign the original, send the tampered version.
	sigHex, _ := canaryhmac.Sign(f.secret, f.now, "n", original)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+f.source, bytes.NewReader(tampered))
	req.Header.Set(HeaderMerchant, f.merchantID.String())
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(f.now.Unix(), 10))
	req.Header.Set(canaryhmac.HeaderNonce, "n")
	req.Header.Set(canaryhmac.HeaderSignature, sigHex)

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
}

func TestHandler_TimestampOutOfWindow_Returns401(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{}`)
	tooOld := f.now.Add(-30 * time.Minute)
	req := f.signedRequest(payload, "n", tooOld)

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
}

func TestHandler_NonceReplay_Returns401(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{"x":1}`)
	req1 := f.signedRequest(payload, "nonce-replay", f.now)
	req2 := f.signedRequest(payload, "nonce-replay", f.now)

	rec1 := httptest.NewRecorder()
	f.router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: got %d want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	f.router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replay: got %d want 401; body=%s", rec2.Code, rec2.Body.String())
	}
	// Only the first event should have been published.
	if got := len(f.pub.Snapshot()); got != 1 {
		t.Errorf("publisher: got %d events, want 1", got)
	}
}

func TestHandler_UnknownSource_Returns401(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{}`)
	req := f.signedRequest(payload, "n", f.now)
	// Re-route to an unregistered source code
	req = httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/unknown-source", bytes.NewReader(payload))
	req.Header.Set(HeaderMerchant, f.merchantID.String())
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(f.now.Unix(), 10))
	req.Header.Set(canaryhmac.HeaderNonce, "n")
	req.Header.Set(canaryhmac.HeaderSignature, strings.Repeat("0", 64))

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
}

func TestHandler_MissingMerchantHeader_Returns400(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+f.source, bytes.NewReader([]byte(`{}`)))
	// Do NOT set HeaderMerchant
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(f.now.Unix(), 10))

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_MalformedMerchantHeader_Returns400(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+f.source, bytes.NewReader([]byte(`{}`)))
	req.Header.Set(HeaderMerchant, "not-a-uuid")

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_MalformedSignature_Returns400(t *testing.T) {
	f := newFixture(t)
	payload := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+f.source, bytes.NewReader(payload))
	req.Header.Set(HeaderMerchant, f.merchantID.String())
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(f.now.Unix(), 10))
	req.Header.Set(canaryhmac.HeaderNonce, "n")
	req.Header.Set(canaryhmac.HeaderSignature, "ZZZ-not-hex")

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_PublisherFailure_Returns500AndDoesNotMaskError(t *testing.T) {
	f := newFixture(t)
	f.pub.FailWith = errInjected

	payload := []byte(`{"x":1}`)
	req := f.signedRequest(payload, "n", f.now)

	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
}

// errInjected is a sentinel for the publisher-failure test.
var errInjected = errInjectedT{}

type errInjectedT struct{}

func (errInjectedT) Error() string { return "injected publish failure" }
