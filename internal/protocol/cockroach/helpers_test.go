//go:build integration

// Package cockroach_test contains integration tests for the Cockroach
// Principle — the antifragility property of the Canary Protocol
// (patent Application 63/991,596). Each test scenario destroys one
// storage tier and proves reconstruction is still possible.
package cockroach_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub1"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// testMerchantID is a fixed UUID used by all test helpers so evidence
// rows form a coherent per-merchant chain.
var testMerchantID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// writeTestEvents inserts n events into protocol.evidence using
// sub1.WriteEvidence, which computes the chain_hash correctly.
// Returns the event_hashes in insertion order.
func writeTestEvents(t *testing.T, pool *pgxpool.Pool, n int) []string {
	t.Helper()
	ctx := context.Background()

	hashes := make([]string, n)
	for i := 0; i < n; i++ {
		// Generate a random 32-byte payload to ensure a unique event_hash.
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatalf("writeTestEvents: rand.Read: %v", err)
		}
		// Compute event_hash = hex(sha256(raw bytes)).
		sum := sha256.Sum256(raw)
		eventHash := hex.EncodeToString(sum[:])

		payload := []byte(fmt.Sprintf("test-payload-%d", i))

		evt := publisher.Event{
			EventID:    uuid.New(),
			EventHash:  eventHash,
			SourceCode: "cockroach-test", // seeded in TestMain
			MerchantID: testMerchantID,
			IngestedAt: time.Now().UTC(),
			Payload:    payload,
		}

		_, err := sub1.WriteEvidence(ctx, pool, evt)
		if err != nil {
			t.Fatalf("writeTestEvents[%d]: WriteEvidence: %v", i, err)
		}
		hashes[i] = eventHash
	}
	return hashes
}

// anchorTestBatch builds a Merkle tree over the chain_hashes of the
// provided event_hashes, calls WriteAnchor via StubInscriber, and
// returns the AnchorResult. minBatch is set to 1 so single-event
// batches are allowed in tests.
//
// Before calling WriteAnchor it verifies that exactly len(hashes)
// unanchored rows exist for the provided hashes. If the count does not
// match — indicating dirty DB state from a prior test run — the test is
// skipped so it does not produce a misleading anchor over the wrong set.
func anchorTestBatch(t *testing.T, pool *pgxpool.Pool, hashes []string) *sub3.AnchorResult {
	t.Helper()
	ctx := context.Background()

	// Guard: count unanchored evidence rows for the specific hashes only.
	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM protocol.evidence e
		 WHERE e.event_hash = ANY($1)
		   AND NOT EXISTS (
		       SELECT 1 FROM protocol.evidence_anchors ea
		       WHERE ea.event_hash = e.event_hash
		   )`,
		hashes,
	).Scan(&count)
	if err != nil {
		t.Fatalf("anchorTestBatch: count unanchored: %v", err)
	}
	if count != len(hashes) {
		t.Skipf("anchorTestBatch: expected %d unanchored hashes, found %d — dirty DB state", len(hashes), count)
	}

	store := sub3.NewStore(pool, "signet")
	stub := &sub3.StubInscriber{}

	result, err := store.WriteAnchor(ctx, stub, 1000, 1)
	if err != nil {
		t.Fatalf("anchorTestBatch: WriteAnchor: %v", err)
	}
	if result == nil {
		t.Fatal("anchorTestBatch: WriteAnchor returned nil — no unanchored events found")
	}
	return result
}

// captureProofs reads the Merkle proof for each event_hash from
// protocol.evidence_anchors and returns them as a map keyed by
// event_hash. Call this BEFORE any truncate that would wipe
// evidence_anchors — it simulates the verifying party who received
// their proof via GET /v1/protocol/verify before the loss.
func captureProofs(t *testing.T, pool *pgxpool.Pool, hashes []string) map[string][]sub3.ProofNode {
	t.Helper()
	ctx := context.Background()

	proofs := make(map[string][]sub3.ProofNode, len(hashes))
	for _, h := range hashes {
		const q = `
			SELECT ea.merkle_proof
			FROM protocol.evidence_anchors ea
			WHERE ea.event_hash = $1
			LIMIT 1
		`
		var raw []byte
		err := pool.QueryRow(ctx, q, h).Scan(&raw)
		if err != nil {
			t.Fatalf("captureProofs(%s): %v", h, err)
		}
		var nodes []sub3.ProofNode
		if err := json.Unmarshal(raw, &nodes); err != nil {
			t.Fatalf("captureProofs(%s): unmarshal: %v", h, err)
		}
		proofs[h] = nodes
	}
	return proofs
}

// evidenceRow holds the columns needed to restore a protocol.evidence row.
type evidenceRow struct {
	EventID       uuid.UUID
	EventHash     string
	ChainHash     string
	PrevChainHash *string
	SourceCode    string
	MerchantID    uuid.UUID
	RawPayload    []byte
	IngestedAt    time.Time
}

// anchorRow holds the columns needed to restore a protocol.anchors row.
type anchorRow struct {
	AnchorID       uuid.UUID
	MerkleRoot     string
	InscriptionID  *string
	BtcTxID        *string
	BtcBlockHeight *int64
	Network        string
	EventCount     int
	AnchorStatus   string
	AnchoredAt     time.Time
}

// evidenceAnchorRow holds the columns needed to restore a
// protocol.evidence_anchors row.
type evidenceAnchorRow struct {
	EventHash   string
	AnchorID    uuid.UUID
	LeafIndex   int
	MerkleProof []byte
}

// destroyAllLocal saves protocol.evidence, protocol.evidence_anchors, and
// protocol.anchors — in that order, before any cascade can wipe them — then
// truncates all three tables. Returns a restore function suitable for
// t.Cleanup.
//
// Used by Scenario C only, where both the L1 and L2 tiers are destroyed.
// Saving evidence_anchors MUST happen before truncating evidence, because
// TRUNCATE evidence CASCADE wipes evidence_anchors.
func destroyAllLocal(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	ctx := context.Background()

	// 1. Save evidence_anchors first — CASCADE from evidence will wipe them.
	savedEA, err := fetchAllEvidenceAnchors(ctx, pool)
	if err != nil {
		t.Fatalf("destroyAllLocal: fetch evidence_anchors: %v", err)
	}

	// 2. Save anchors.
	savedAnchors, err := fetchAllAnchors(ctx, pool)
	if err != nil {
		t.Fatalf("destroyAllLocal: fetch anchors: %v", err)
	}

	// 3. Save evidence.
	savedEvidence, err := fetchAllEvidence(ctx, pool)
	if err != nil {
		t.Fatalf("destroyAllLocal: fetch evidence: %v", err)
	}

	// 4. Destroy: disable triggers, truncate evidence (cascades to evidence_anchors),
	//    then truncate anchors.
	disableAndTruncate(t, ctx, pool) // TRUNCATE evidence CASCADE + trigger management

	if _, err := pool.Exec(ctx, "TRUNCATE protocol.anchors CASCADE"); err != nil {
		t.Fatalf("destroyAllLocal: truncate anchors: %v", err)
	}

	// Return restore: evidence first (FK parent), then anchors, then evidence_anchors.
	return func() {
		restoreEvidence(t, ctx, pool, savedEvidence)
		restoreAnchors(t, ctx, pool, savedAnchors)
		restoreEvidenceAnchors(t, ctx, pool, savedEA)
	}
}

// truncateEvidence disables the append-only triggers on
// protocol.evidence, truncates (cascading to evidence_anchors), and
// returns a cleanup function that re-inserts both the evidence rows and
// the evidence_anchor rows.
//
// The caller MUST capture evidence_anchors rows BEFORE calling this
// function — CASCADE from evidence wipes them too.
//
// IMPORTANT: This is a destructive helper. Only call against canary_go_test.
func truncateEvidence(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	ctx := context.Background()

	// ── Save current evidence rows ────────────────────────────────────────
	savedEvidence, err := fetchAllEvidence(ctx, pool)
	if err != nil {
		t.Fatalf("truncateEvidence: fetch evidence: %v", err)
	}
	// ── Save current evidence_anchors rows ───────────────────────────────
	savedEA, err := fetchAllEvidenceAnchors(ctx, pool)
	if err != nil {
		t.Fatalf("truncateEvidence: fetch evidence_anchors: %v", err)
	}

	// ── Disable triggers, truncate, re-enable ────────────────────────────
	disableAndTruncate(t, ctx, pool)

	// ── Return cleanup that restores both tables ─────────────────────────
	return func() {
		restoreEvidence(t, ctx, pool, savedEvidence)
		restoreEvidenceAnchors(t, ctx, pool, savedEA)
	}
}

// truncateAnchors truncates protocol.anchors (cascades to evidence_anchors
// via the anchor_id FK) and returns a cleanup function.
// Call only when evidence_anchors have already been saved by captureProofs.
func truncateAnchors(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	ctx := context.Background()

	saved, err := fetchAllAnchors(ctx, pool)
	if err != nil {
		t.Fatalf("truncateAnchors: fetch: %v", err)
	}
	savedEA, err := fetchAllEvidenceAnchors(ctx, pool)
	if err != nil {
		t.Fatalf("truncateAnchors: fetch evidence_anchors: %v", err)
	}

	_, err = pool.Exec(ctx, "TRUNCATE protocol.anchors CASCADE")
	if err != nil {
		t.Fatalf("truncateAnchors: truncate: %v", err)
	}

	return func() {
		restoreAnchors(t, ctx, pool, saved)
		restoreEvidenceAnchors(t, ctx, pool, savedEA)
	}
}

// ── internal helpers ──────────────────────────────────────────────────────────

func disableAndTruncate(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	stmts := []string{
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_delete",
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_truncate",
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_update",
		"TRUNCATE protocol.evidence CASCADE",
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_delete",
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_truncate",
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_update",
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("disableAndTruncate: %q: %v", stmt, err)
		}
	}
}

func fetchAllEvidence(ctx context.Context, pool *pgxpool.Pool) ([]evidenceRow, error) {
	const q = `
		SELECT event_id, event_hash, chain_hash, prev_chain_hash,
		       source_code, merchant_id, raw_payload, ingested_at
		FROM protocol.evidence
		ORDER BY ingested_at
	`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []evidenceRow
	for rows.Next() {
		var r evidenceRow
		if err := rows.Scan(
			&r.EventID, &r.EventHash, &r.ChainHash, &r.PrevChainHash,
			&r.SourceCode, &r.MerchantID, &r.RawPayload, &r.IngestedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func fetchAllAnchors(ctx context.Context, pool *pgxpool.Pool) ([]anchorRow, error) {
	const q = `
		SELECT anchor_id, merkle_root, inscription_id, btc_tx_id,
		       btc_block_height, network, event_count, anchor_status, anchored_at
		FROM protocol.anchors
		ORDER BY anchored_at
	`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []anchorRow
	for rows.Next() {
		var r anchorRow
		if err := rows.Scan(
			&r.AnchorID, &r.MerkleRoot, &r.InscriptionID, &r.BtcTxID,
			&r.BtcBlockHeight, &r.Network, &r.EventCount, &r.AnchorStatus, &r.AnchoredAt,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func fetchAllEvidenceAnchors(ctx context.Context, pool *pgxpool.Pool) ([]evidenceAnchorRow, error) {
	const q = `
		SELECT event_hash, anchor_id, leaf_index, merkle_proof
		FROM protocol.evidence_anchors
		ORDER BY anchor_id, leaf_index
	`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []evidenceAnchorRow
	for rows.Next() {
		var r evidenceAnchorRow
		if err := rows.Scan(&r.EventHash, &r.AnchorID, &r.LeafIndex, &r.MerkleProof); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func restoreEvidence(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rows []evidenceRow) {
	t.Helper()
	// Must disable triggers to re-insert (the append-only trigger blocks nothing
	// on INSERT, only on UPDATE/DELETE/TRUNCATE — but disabling is safe insurance).
	const insert = `
		INSERT INTO protocol.evidence
			(event_id, event_hash, chain_hash, prev_chain_hash,
			 source_code, merchant_id, raw_payload, ingested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (event_hash) DO NOTHING
	`
	for _, r := range rows {
		if _, err := pool.Exec(ctx, insert,
			r.EventID, r.EventHash, r.ChainHash, r.PrevChainHash,
			r.SourceCode, r.MerchantID, r.RawPayload, r.IngestedAt,
		); err != nil {
			t.Errorf("restoreEvidence(%s): %v", r.EventHash, err)
		}
	}
}

func restoreAnchors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rows []anchorRow) {
	t.Helper()
	const insert = `
		INSERT INTO protocol.anchors
			(anchor_id, merkle_root, inscription_id, btc_tx_id,
			 btc_block_height, network, event_count, anchor_status, anchored_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (anchor_id) DO NOTHING
	`
	for _, r := range rows {
		if _, err := pool.Exec(ctx, insert,
			r.AnchorID, r.MerkleRoot, r.InscriptionID, r.BtcTxID,
			r.BtcBlockHeight, r.Network, r.EventCount, r.AnchorStatus, r.AnchoredAt,
		); err != nil {
			t.Errorf("restoreAnchors(%s): %v", r.AnchorID, err)
		}
	}
}

func restoreEvidenceAnchors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rows []evidenceAnchorRow) {
	t.Helper()
	const insert = `
		INSERT INTO protocol.evidence_anchors
			(event_hash, anchor_id, leaf_index, merkle_proof)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_hash, anchor_id) DO NOTHING
	`
	for _, r := range rows {
		if _, err := pool.Exec(ctx, insert,
			r.EventHash, r.AnchorID, r.LeafIndex, r.MerkleProof,
		); err != nil {
			t.Errorf("restoreEvidenceAnchors(%s/%s): %v", r.EventHash, r.AnchorID, err)
		}
	}
}
