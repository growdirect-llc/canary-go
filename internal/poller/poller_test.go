package poller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
)

// stubDocument returns a minimal Counterpoint document JSON with the
// given DocumentDate.
func stubDocument(docTime time.Time) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"DocumentNumber": "DOC-001",
		"DocumentType":   "TKT",
		"DocumentDate":   docTime.UTC().Format(time.RFC3339),
		"StoreNumber":    "01",
		"CashierNumber":  "5",
		"Total":          19.99,
		"Lines":          []any{},
		"Payments":       []any{},
	})
	return json.RawMessage(b)
}

func TestWrapDocument(t *testing.T) {
	p := &Poller{logger: zap.NewNop()}
	merchantID := uuid.New()
	docDate := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	raw := stubDocument(docDate)

	evt, gotDate, err := p.wrapDocument(merchantID, raw)
	if err != nil {
		t.Fatalf("wrapDocument: %v", err)
	}
	if evt.SourceCode != sourceCode {
		t.Errorf("source_code = %q, want %q", evt.SourceCode, sourceCode)
	}
	if evt.MerchantID != merchantID {
		t.Errorf("merchant_id mismatch")
	}
	if evt.EventHash == "" {
		t.Error("event_hash is empty")
	}
	if !gotDate.Equal(docDate) {
		t.Errorf("doc date = %v, want %v", gotDate, docDate)
	}
}

func TestFetchDocuments_ArrayResponse(t *testing.T) {
	docDate := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	docs := []json.RawMessage{stubDocument(docDate)}
	body, _ := json.Marshal(docs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Documents" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	p := &Poller{
		httpClient: srv.Client(),
		logger:     zap.NewNop(),
	}

	cred := fakeCredential(srv.URL)
	got, err := p.fetchDocuments(context.Background(), cred, time.Time{})
	if err != nil {
		t.Fatalf("fetchDocuments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d docs, want 1", len(got))
	}
}

func TestFetchDocuments_EnvelopeResponse(t *testing.T) {
	docDate := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	docs := []json.RawMessage{stubDocument(docDate)}
	body, _ := json.Marshal(map[string]any{"Documents": docs})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	p := &Poller{httpClient: srv.Client(), logger: zap.NewNop()}
	cred := fakeCredential(srv.URL)

	got, err := p.fetchDocuments(context.Background(), cred, time.Time{})
	if err != nil {
		t.Fatalf("fetchDocuments (envelope): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d docs, want 1", len(got))
	}
}

func TestFetchDocuments_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := &Poller{httpClient: srv.Client(), logger: zap.NewNop()}
	cred := fakeCredential(srv.URL)

	got, err := p.fetchDocuments(context.Background(), cred, time.Time{})
	if err != nil {
		t.Fatalf("fetchDocuments (no content): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d docs", len(got))
	}
}

// TestPollMerchant_PublishesEvents verifies the end-to-end path through
// pollMerchant using a mock publisher and a stub HTTP server.
// Requires Valkey on localhost:6379 (DB 2 — standard dev stack).
func TestPollMerchant_PublishesEvents(t *testing.T) {
	valkeyClient := redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		DB:       2,
		Password: "valkey_dev",
	})
	if err := valkeyClient.Ping(context.Background()).Err(); err != nil {
		t.Skipf("valkey not available: %v", err)
	}
	defer valkeyClient.Close()

	docDate := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	docs := []json.RawMessage{stubDocument(docDate)}
	body, _ := json.Marshal(docs)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	mockPub := publisher.NewMock()

	p := &Poller{
		httpClient: srv.Client(),
		pub:        mockPub,
		valkey:     valkeyClient,
		logger:     zap.NewNop(),
	}

	cred := fakeCredential(srv.URL)

	// Clean up cursor key before and after test.
	cursorKey := cursorPrefix + cred.MerchantID.String()
	valkeyClient.Del(context.Background(), cursorKey)
	defer valkeyClient.Del(context.Background(), cursorKey)

	if err := p.pollMerchant(context.Background(), cred); err != nil {
		t.Fatalf("pollMerchant: %v", err)
	}

	events := mockPub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(events))
	}
	if events[0].SourceCode != sourceCode {
		t.Errorf("source_code = %q, want %q", events[0].SourceCode, sourceCode)
	}
	if events[0].MerchantID != cred.MerchantID {
		t.Error("merchant_id mismatch in published event")
	}
}

// fakeCredential builds a test Credential pointing at the given base URL.
func fakeCredential(baseURL string) fakeAdapterCredential {
	return fakeAdapterCredential{
		MerchantID:      uuid.New(),
		APIKeyEncrypted: "test-api-key",
		EndpointURL:     baseURL,
	}
}

// fakeAdapterCredential mirrors adapters.Credential for test isolation.
// We use the real type via fakeCredential() above.
type fakeAdapterCredential = struct {
	MerchantID      uuid.UUID
	APIKeyEncrypted string
	EndpointURL     string
}

