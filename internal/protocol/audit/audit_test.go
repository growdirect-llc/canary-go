package audit

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// memInserter is a thread-safe in-memory Inserter used to inspect what
// the middleware would have written.
type memInserter struct {
	mu      sync.Mutex
	entries []Entry
	err     error // injected failure
	calls   int
}

func (m *memInserter) Insert(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return m.err
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *memInserter) last() Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return Entry{}
	}
	return m.entries[len(m.entries)-1]
}

// newTestRouter spins up a chi router with the audit middleware in front
// of a stub handler that mints an event_id (mirroring webhook.Handler's
// production behavior).
func newTestRouter(ins Inserter) (*chi.Mux, *uuid.UUID) {
	r := chi.NewRouter()
	cfg := Config{
		Inserter:    ins,
		ServiceName: "canary-gateway-test",
		ActorType:   "agent",
		Resource:    "protocol.event",
	}
	r.Use(Middleware(cfg))
	mintedID := uuid.New()
	r.Post("/v1/protocol/webhook/{source}", func(w http.ResponseWriter, req *http.Request) {
		// Stub the handler-side context bridge.
		ctx := WithEventID(req.Context(), mintedID)
		ctx = WithSource(ctx, chi.URLParam(req, "source"))
		*req = *req.WithContext(ctx)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return r, &mintedID
}

func TestMiddleware_SuccessPath_RecordsEntry(t *testing.T) {
	ins := &memInserter{}
	r, mintedID := newTestRouter(ins)

	merchantID := uuid.New()
	body := []byte(`{"event":"order.created","amount":100}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/square", bytes.NewReader(body))
	req.Header.Set(HeaderMerchant, merchantID.String())
	req.Header.Set("User-Agent", "audit-test-agent/1.0")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if ins.calls != 1 {
		t.Fatalf("inserter calls: got %d want 1", ins.calls)
	}

	got := ins.last()

	if got.MerchantID == nil || *got.MerchantID != merchantID {
		t.Errorf("merchant_id: got %v want %s", got.MerchantID, merchantID)
	}
	if got.Action != "POST /v1/protocol/webhook/square" {
		t.Errorf("action: got %q", got.Action)
	}
	if got.Resource != "protocol.event" {
		t.Errorf("resource: got %q", got.Resource)
	}
	if got.EventID == nil || *got.EventID != *mintedID {
		t.Errorf("event_id: got %v want %s", got.EventID, mintedID)
	}
	if got.SourceCode != "square" {
		t.Errorf("source_code: got %q", got.SourceCode)
	}
	if got.PayloadDigest == "" || len(got.PayloadDigest) != 64 {
		t.Errorf("payload_digest: got %q (len=%d) want 64-hex", got.PayloadDigest, len(got.PayloadDigest))
	}
	if got.UserAgent != "audit-test-agent/1.0" {
		t.Errorf("user_agent: got %q", got.UserAgent)
	}
	if got.RequestID == "" {
		t.Errorf("request_id should have been minted; got empty")
	}
	if got.StatusCode != http.StatusOK {
		t.Errorf("status_code: got %d", got.StatusCode)
	}
	if got.LatencyMS < 0 {
		t.Errorf("latency_ms negative: %d", got.LatencyMS)
	}
	if got.ActorType != "agent" {
		t.Errorf("actor_type: got %q", got.ActorType)
	}
	if got.MCPServer != "canary-gateway-test" {
		t.Errorf("mcp_server: got %q", got.MCPServer)
	}
	if got.ToolName != "/v1/protocol/webhook/square" {
		t.Errorf("tool_name: got %q", got.ToolName)
	}
	if rec.Header().Get(HeaderRequestID) == "" {
		t.Errorf("response should echo X-Request-ID")
	}
}

func TestMiddleware_RequestIDPassthroughWhenProvided(t *testing.T) {
	ins := &memInserter{}
	r, _ := newTestRouter(ins)

	merchantID := uuid.New()
	const explicitReqID = "client-supplied-req-id-7c1f"

	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/square", bytes.NewReader([]byte(`{"k":"v"}`)))
	req.Header.Set(HeaderMerchant, merchantID.String())
	req.Header.Set(HeaderRequestID, explicitReqID)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := ins.last().RequestID; got != explicitReqID {
		t.Errorf("entry request_id: got %q want %q", got, explicitReqID)
	}
	if got := rec.Header().Get(HeaderRequestID); got != explicitReqID {
		t.Errorf("response request_id: got %q want %q", got, explicitReqID)
	}
}

func TestMiddleware_InsertFailureDoesNotBlockResponse(t *testing.T) {
	ins := &memInserter{err: errors.New("simulated DB outage")}
	r, _ := newTestRouter(ins)

	merchantID := uuid.New()
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/square", bytes.NewReader([]byte(`{"k":"v"}`)))
	req.Header.Set(HeaderMerchant, merchantID.String())

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// The point: the handler still served 200 even though the
	// inserter blew up.
	if rec.Code != http.StatusOK {
		t.Errorf("response status: got %d want 200 (audit failure must not block)", rec.Code)
	}
	if ins.calls != 1 {
		t.Errorf("inserter should have been attempted exactly once, got %d", ins.calls)
	}
}

func TestMiddleware_NoMerchantHeader_StillRecordsRow(t *testing.T) {
	// Even on a 400 (missing merchant), the audit row should still land,
	// because the gateway needs a record of every probe / malformed call.
	ins := &memInserter{}
	r := chi.NewRouter()
	r.Use(Middleware(Config{Inserter: ins, ServiceName: "canary-gateway-test"}))
	r.Post("/v1/protocol/webhook/{source}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/square", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
	if ins.calls != 1 {
		t.Fatalf("inserter calls: got %d want 1", ins.calls)
	}
	got := ins.last()
	if got.MerchantID != nil {
		t.Errorf("merchant_id should be nil when header missing; got %v", got.MerchantID)
	}
	if got.StatusCode != http.StatusBadRequest {
		t.Errorf("status_code: got %d", got.StatusCode)
	}
}

func TestMiddleware_PayloadDigestStable(t *testing.T) {
	ins := &memInserter{}
	r, _ := newTestRouter(ins)

	body := []byte(`{"deterministic":"input"}`)
	merchantID := uuid.New()

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/protocol/webhook/square", bytes.NewReader(body))
		req.Header.Set(HeaderMerchant, merchantID.String())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: status %d", i, rec.Code)
		}
	}
	if ins.entries[0].PayloadDigest == "" {
		t.Fatal("digest empty")
	}
	if ins.entries[0].PayloadDigest != ins.entries[1].PayloadDigest {
		t.Errorf("digest unstable: %q vs %q",
			ins.entries[0].PayloadDigest, ins.entries[1].PayloadDigest)
	}
}

func TestClientIP_PrefersXForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	got := clientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("got %q want 203.0.113.5", got)
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:54321"
	if got := clientIP(r); got != "10.0.0.1:54321" {
		t.Errorf("got %q", got)
	}
}
