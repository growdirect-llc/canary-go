//go:build integration

// Integration test for the Merkle anchor bilateral verification API.
// Exercises the full read path: a sealed event + anchor row in
// protocol.anchors/evidence_anchors is fetched via
// GET /v1/protocol/anchor/{event_hash}.
package anchor

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
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

func TestIntegration_Anchor_LookupHit(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Seed prerequisites: org → merchant → evidence row → anchor → evidence_anchor.
	orgID := uuid.New()
	merchantID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-750 Anchor API Test"); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4)`,
		merchantID, orgID, "src-"+merchantID.String()[:6], "anchor test merchant"); err != nil {
		t.Fatalf("merchant: %v", err)
	}

	eventID := uuid.New()
	eventHash := "anchev-" + uuid.NewString()
	chainHash := "ch-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO protocol.evidence
			(event_id, event_hash, chain_hash, prev_chain_hash,
			 source_code, merchant_id, raw_payload, ingested_at)
		VALUES ($1, $2, $3, NULL, 'square', $4, $5, now())
	`, eventID, eventHash, chainHash, merchantID, []byte(`{"t":"anchor-test"}`)); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	// Seed anchor.
	anchorID := uuid.New()
	merkleRoot := "mr-" + uuid.NewString()
	proofJSON := json.RawMessage(`[{"sibling_hash":"aabb","position":"right"}]`)
	if _, err := pool.Exec(ctx, `
		INSERT INTO protocol.anchors
			(anchor_id, merkle_root, inscription_id, btc_tx_id,
			 btc_block_height, network, event_count, anchor_status, anchored_at)
		VALUES ($1, $2, 'stub:abc123', 'stub-tx-abc', 0, 'signet', 1, 'inscribed', now())
	`, anchorID, merkleRoot); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO protocol.evidence_anchors (event_hash, anchor_id, leaf_index, merkle_proof)
		VALUES ($1, $2, 0, $3)
	`, eventHash, anchorID, proofJSON); err != nil {
		t.Fatalf("seed evidence_anchor: %v", err)
	}

	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.evidence_anchors WHERE event_hash = $1`, eventHash)
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.anchors WHERE anchor_id = $1`, anchorID)
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.evidence WHERE event_hash = $1`, eventHash)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}()

	h := New(pool, nil)
	r := chi.NewRouter()
	h.Mount(r)

	// HIT
	req := httptest.NewRequest(http.MethodGet, "/v1/protocol/anchor/"+eventHash, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("hit: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got AnchorRecord
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EventHash != eventHash {
		t.Errorf("event_hash mismatch: got=%s", got.EventHash)
	}
	if got.MerkleRoot != merkleRoot {
		t.Errorf("merkle_root mismatch: got=%s", got.MerkleRoot)
	}
	if got.AnchorStatus != "inscribed" {
		t.Errorf("status mismatch: got=%s", got.AnchorStatus)
	}

	// MISS
	miss := httptest.NewRequest(http.MethodGet,
		"/v1/protocol/anchor/missing-"+uuid.NewString(), nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, miss)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("miss: got %d want 404", rec2.Code)
	}
}
