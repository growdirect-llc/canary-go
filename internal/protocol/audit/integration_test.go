//go:build integration

// Integration test for the protocol audit middleware. Exercises the
// real PgxInserter against a live Postgres, wired in front of a stub
// handler that mints an event_id (mirroring the gateway's webhook
// handler). Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	go test -tags=integration -v ./internal/protocol/audit/...
//
// GRO-694.
package audit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_PgxInserter_RowLandsInAuditLog(t *testing.T) {
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}

	// Seed an organization + merchant so the FK on audit_log.merchant_id
	// resolves. (audit_log.merchant_id REFERENCES app.merchants(id).)
	orgID := uuid.New()
	merchantID := uuid.New()
	srcMerchant := "audit-test-" + merchantID.String()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-694 audit integration"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4)`,
		merchantID, orgID, srcMerchant, "GRO-694 audit"); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)

	// Wire middleware in front of a stub handler that mints an event_id
	// (mirrors webhook.Handler's production behavior).
	ins := NewPgxInserter(pool)
	r := chi.NewRouter()
	r.Use(Middleware(Config{
		Inserter:    ins,
		ServiceName: "canary-gateway-integration",
		ActorType:   "agent",
		Resource:    "protocol.event",
	}))

	mintedID := uuid.New()
	r.Post("/v1/protocol/webhook/{source}", func(w http.ResponseWriter, req *http.Request) {
		ctx := WithEventID(req.Context(), mintedID)
		ctx = WithSource(ctx, chi.URLParam(req, "source"))
		*req = *req.WithContext(ctx)
		w.WriteHeader(http.StatusOK)
	})

	body := []byte(`{"event":"audit.integration.probe"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/square", bytes.NewReader(body))
	req.Header.Set(HeaderMerchant, merchantID.String())
	req.Header.Set("User-Agent", "audit-integration/1.0")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}

	// Allow the async insert (fired with its own context) a beat to flush.
	deadline := time.Now().Add(3 * time.Second)
	var (
		gotEventID    uuid.UUID
		gotMerchantID uuid.UUID
		gotAction     string
		gotResource   string
		gotDigest     string
		gotSource     string
		gotReqID      string
		gotUserAgent  string
		gotStatusCode int
		gotLatencyMS  int
		gotActorType  string
		gotMCPServer  string
		gotToolName   string
	)
	for {
		err := pool.QueryRow(ctx,
			`SELECT event_id, merchant_id, action, resource, payload_digest,
			        source_code, request_id, user_agent, status_code, latency_ms,
			        actor_type, mcp_server, tool_name
			 FROM app.audit_log WHERE event_id = $1 LIMIT 1`,
			mintedID,
		).Scan(&gotEventID, &gotMerchantID, &gotAction, &gotResource, &gotDigest,
			&gotSource, &gotReqID, &gotUserAgent, &gotStatusCode, &gotLatencyMS,
			&gotActorType, &gotMCPServer, &gotToolName)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit row never landed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if gotMerchantID != merchantID {
		t.Errorf("merchant_id: got %s want %s", gotMerchantID, merchantID)
	}
	if gotEventID != mintedID {
		t.Errorf("event_id: got %s want %s", gotEventID, mintedID)
	}
	if gotAction != "POST /v1/protocol/webhook/square" {
		t.Errorf("action: got %q", gotAction)
	}
	if gotResource != "protocol.event" {
		t.Errorf("resource: got %q", gotResource)
	}
	if len(gotDigest) != 64 {
		t.Errorf("payload_digest len: got %d want 64", len(gotDigest))
	}
	if gotSource != "square" {
		t.Errorf("source_code: got %q", gotSource)
	}
	if gotReqID == "" {
		t.Errorf("request_id should be populated")
	}
	if gotUserAgent != "audit-integration/1.0" {
		t.Errorf("user_agent: got %q", gotUserAgent)
	}
	if gotStatusCode != http.StatusOK {
		t.Errorf("status_code: got %d", gotStatusCode)
	}
	if gotLatencyMS < 0 {
		t.Errorf("latency_ms negative")
	}
	if gotActorType != "agent" {
		t.Errorf("actor_type: got %q", gotActorType)
	}
	if gotMCPServer != "canary-gateway-integration" {
		t.Errorf("mcp_server: got %q", gotMCPServer)
	}
	if gotToolName != "/v1/protocol/webhook/square" {
		t.Errorf("tool_name: got %q", gotToolName)
	}

	t.Logf("audit row OK: event_id=%s digest=%s status=%d latency=%dms",
		gotEventID, gotDigest, gotStatusCode, gotLatencyMS)

	// Cleanup the row we wrote
	_, _ = pool.Exec(ctx, `DELETE FROM app.audit_log WHERE event_id = $1`, mintedID)
}
