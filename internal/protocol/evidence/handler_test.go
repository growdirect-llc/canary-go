//go:build integration

// Integration test for the bilateral verification API. Exercises the
// full read path: a sealed event in protocol.evidence is fetched via
// GET /v1/protocol/evidence/{event_hash}.
package evidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/testutil"
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

func TestIntegration_Evidence_LookupHit(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Seed an org + merchant + evidence row directly. We don't go through
	// the worker here because the worker has its own integration test —
	// this test is about the read API.
	orgID := uuid.New()
	merchantID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-748 Evidence API Test"); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4)`,
		merchantID, orgID, "src-"+merchantID.String()[:6], "evidence test merchant"); err != nil {
		t.Fatalf("merchant: %v", err)
	}

	defer func() {
		_, _ = pool.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.evidence WHERE merchant_id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}()

	eventID := uuid.New()
	eventHash := "evhash-" + uuid.NewString()
	chainHash := "ch-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO protocol.evidence
			(event_id, event_hash, chain_hash, prev_chain_hash,
			 source_code, merchant_id, raw_payload, ingested_at)
		VALUES ($1, $2, $3, NULL, 'square', $4, $5, now())
	`, eventID, eventHash, chainHash, merchantID, []byte(`{"event":"test"}`)); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	h := New(pool, nil)
	r := chi.NewRouter()
	h.Mount(r)

	// HIT — owner caller (claims tenant matches the row's merchant) gets
	// RecordFull with merchant_id + raw_payload.
	req := httptest.NewRequest(http.MethodGet, "/v1/protocol/evidence/"+eventHash, nil)
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), merchantID))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hit: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var got RecordFull
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EventHash != eventHash || got.ChainHash != chainHash {
		t.Errorf("payload mismatch: got=%+v", got)
	}
	if got.MerchantID != merchantID {
		t.Errorf("merchant_id mismatch")
	}

	// MISS
	miss := httptest.NewRequest(http.MethodGet,
		"/v1/protocol/evidence/missing-"+uuid.NewString(), nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, miss)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("miss: got %d want 404", rec2.Code)
	}
}
