//go:build integration

// Integration smoke test for the protocol gateway. Exercises the real
// stack — pgxpool → protocol.source_secrets, redis → Valkey Streams +
// nonce store, handler wired end-to-end. Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/protocol/webhook/...
//
// Production p99 < 5ms verification happens against the GCP deployment,
// not against localhost. This test verifies the contract; the SLO test
// runs against the deployed gateway in the GCP-deployment dispatch.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	canaryhmac "github.com/growdirect-llc/rapidpos/internal/protocol/hmac"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/secrets"
)

const (
	testStream      = "protocol:events:integration"
	testNoncePrefix = "gateway:nonce:integration"
	testSourceCode  = "square"
	testSecretValue = "integration-test-secret-with-real-entropy"
)

func skipIfNoIntegration(t *testing.T) (dbURL, valkeyURL string) {
	t.Helper()
	dbURL = os.Getenv("GATEWAY_TEST_DATABASE_URL")
	valkeyURL = os.Getenv("GATEWAY_TEST_VALKEY_URL")
	if dbURL == "" || valkeyURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL and GATEWAY_TEST_VALKEY_URL to run integration tests")
	}
	return
}

// seedFixtures inserts an organization, merchant, and source_secret. Returns
// the merchant_id, the secret bytes, and a cleanup func.
func seedFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, []byte, func()) {
	t.Helper()

	orgID := uuid.New()
	merchantID := uuid.New()
	secretID := uuid.New()
	sourceMerchantID := "test-source-" + merchantID.String()[:8]

	// Organization
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-746 Integration Test Org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	// Merchant
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4)`,
		merchantID, orgID, sourceMerchantID, "GRO-746 Integration Test Merchant"); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	// Source secret. signature_algo + status NOT NULL added by Loop 1
	// declarative schema (deploy/schema/11_protocol.sql).
	if _, err := pool.Exec(ctx,
		`INSERT INTO protocol.source_secrets (id, merchant_id, source_code, secret, signature_algo, status)
		 VALUES ($1, $2, $3, $4, 'HMAC-SHA256', 'active')`,
		secretID, merchantID, testSourceCode, testSecretValue); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	cleanup := func() {
		// Delete in dependency order; ignore errors (best-effort cleanup).
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.source_secrets WHERE id = $1`, secretID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return merchantID, []byte(testSecretValue), cleanup
}

func TestIntegration_HappyPath_HandlerToValkey(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	opts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		t.Fatalf("parse valkey url: %v", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	// Sanity-ping infra
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("valkey ping: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}

	// Clear any prior test stream entries so this run is isolated
	_, _ = rdb.Del(ctx, testStream).Result()

	// Seed fixtures + ensure cleanup
	merchantID, secret, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	// Wire the production components
	resolver := secrets.NewPgxResolver(pool)
	pub := publisher.NewValkey(rdb, testStream)
	nonces := publisher.NewValkeyNonceStore(rdb, testNoncePrefix)
	h := New(resolver, pub, nonces, nil)

	r := chi.NewRouter()
	h.Mount(r)

	// Build a signed request
	payload := []byte(`{"event":"order.created","id":"o_integration","amount":12345}`)
	ts := time.Now().UTC()
	nonce := "integration-nonce-" + uuid.NewString()
	sigHex, _ := canaryhmac.Sign(secret, ts, nonce, payload)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/protocol/webhook/"+testSourceCode, bytes.NewReader(payload))
	req.Header.Set(HeaderMerchant, merchantID.String())
	req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(canaryhmac.HeaderNonce, nonce)
	req.Header.Set(canaryhmac.HeaderSignature, sigHex)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	// Time the round trip
	start := time.Now()
	r.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	t.Logf("handler latency: %s · event_id=%s · event_hash=%s",
		elapsed, resp.EventID, resp.EventHash)

	// Verify the event landed in Valkey Streams (read 1 entry)
	streams, err := rdb.XRead(ctx, &redis.XReadArgs{
		Streams: []string{testStream, "0"},
		Count:   1,
		Block:   2 * time.Second,
	}).Result()
	if err != nil {
		t.Fatalf("xread: %v", err)
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		t.Fatalf("no message landed in Valkey stream %s", testStream)
	}

	msg := streams[0].Messages[0]
	if got := msg.Values["event_id"]; got != resp.EventID {
		t.Errorf("stream event_id mismatch: got=%v response=%s", got, resp.EventID)
	}
	if got := msg.Values["event_hash"]; got != resp.EventHash {
		t.Errorf("stream event_hash mismatch: got=%v response=%s", got, resp.EventHash)
	}
	if got := msg.Values["merchant_id"]; got != merchantID.String() {
		t.Errorf("stream merchant_id mismatch: got=%v want=%s", got, merchantID)
	}
	if got := msg.Values["source_code"]; got != testSourceCode {
		t.Errorf("stream source_code: got %v want %s", got, testSourceCode)
	}

	// Stream cleanup so re-runs of this test stay isolated
	_, _ = rdb.Del(ctx, testStream).Result()
}

func TestIntegration_NonceReplay_RejectedAcrossProcesses(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	opts, _ := redis.ParseURL(valkeyURL)
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	streamName := testStream + ":replay"
	_, _ = rdb.Del(ctx, streamName).Result()

	merchantID, secret, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	resolver := secrets.NewPgxResolver(pool)
	pub := publisher.NewValkey(rdb, streamName)
	nonces := publisher.NewValkeyNonceStore(rdb, testNoncePrefix+":replay")
	h := New(resolver, pub, nonces, nil)
	r := chi.NewRouter()
	h.Mount(r)

	payload := []byte(`{"event":"replay.test"}`)
	ts := time.Now().UTC()
	nonce := "replay-nonce-" + uuid.NewString()
	sigHex, _ := canaryhmac.Sign(secret, ts, nonce, payload)

	build := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/protocol/webhook/"+testSourceCode, bytes.NewReader(payload))
		req.Header.Set(HeaderMerchant, merchantID.String())
		req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set(canaryhmac.HeaderNonce, nonce)
		req.Header.Set(canaryhmac.HeaderSignature, sigHex)
		return req
	}

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, build())
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: got %d want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, build())
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replay: got %d want 401", rec2.Code)
	}

	// Stream should have exactly 1 entry (the first call)
	streams, _ := rdb.XRange(ctx, streamName, "-", "+").Result()
	if len(streams) != 1 {
		t.Errorf("stream should have 1 entry, got %d", len(streams))
	}

	_, _ = rdb.Del(ctx, streamName).Result()
}

func TestIntegration_LatencyBaseline_SinglePath(t *testing.T) {
	dbURL, valkeyURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()
	opts, _ := redis.ParseURL(valkeyURL)
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	streamName := testStream + ":latency"
	_, _ = rdb.Del(ctx, streamName).Result()

	merchantID, secret, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	resolver := secrets.NewPgxResolver(pool)
	pub := publisher.NewValkey(rdb, streamName)
	nonces := publisher.NewValkeyNonceStore(rdb, testNoncePrefix+":latency")
	h := New(resolver, pub, nonces, nil)
	r := chi.NewRouter()
	h.Mount(r)

	const N = 50
	latencies := make([]time.Duration, 0, N)
	payload := []byte(`{"event":"latency.probe"}`)

	// Warm the connection pool + caches with 5 throwaway requests
	for i := 0; i < 5; i++ {
		ts := time.Now().UTC()
		nonce := "warm-" + strconv.Itoa(i) + "-" + uuid.NewString()
		sigHex, _ := canaryhmac.Sign(secret, ts, nonce, payload)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/protocol/webhook/"+testSourceCode, bytes.NewReader(payload))
		req.Header.Set(HeaderMerchant, merchantID.String())
		req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set(canaryhmac.HeaderNonce, nonce)
		req.Header.Set(canaryhmac.HeaderSignature, sigHex)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	// Measured run
	for i := 0; i < N; i++ {
		ts := time.Now().UTC()
		nonce := "lat-" + strconv.Itoa(i) + "-" + uuid.NewString()
		sigHex, _ := canaryhmac.Sign(secret, ts, nonce, payload)
		req := httptest.NewRequest(http.MethodPost,
			"/v1/protocol/webhook/"+testSourceCode, bytes.NewReader(payload))
		req.Header.Set(HeaderMerchant, merchantID.String())
		req.Header.Set(canaryhmac.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set(canaryhmac.HeaderNonce, nonce)
		req.Header.Set(canaryhmac.HeaderSignature, sigHex)

		start := time.Now()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: got %d", i, rec.Code)
		}
		latencies = append(latencies, elapsed)
	}

	// Compute simple stats — sort and pick percentiles
	sortDurations(latencies)
	p50 := latencies[len(latencies)/2]
	p95 := latencies[len(latencies)*95/100]
	p99 := latencies[len(latencies)*99/100]

	t.Logf("latency baseline (N=%d, sequential, localhost): p50=%s p95=%s p99=%s",
		N, p50, p95, p99)
	t.Logf("note: production p99<5ms SLO verification belongs to GCP deployment dispatch — this is a contract baseline only")

	_, _ = rdb.Del(ctx, streamName).Result()
}

// sortDurations is a tiny in-place insertion sort — N≤50 so no need
// to import sort.
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j-1] > d[j]; j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
}
