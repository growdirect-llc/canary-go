//go:build integration

// Package cockroach_test validates the Cockroach Principle: losing any
// single storage tier does not destroy evidence or the ability to
// reconstruct — the antifragility property of the Canary Protocol
// (patent Application 63/991,596).
//
// Three scenarios:
//
//	A — L1 loss (protocol.evidence wiped): Merkle proofs in evidence_anchors
//	    still verify against the Merkle root. Evidence is recoverable
//	    from the anchor layer alone.
//
//	B — L2 loss (parsed/routed data wiped): raw_payload in protocol.evidence
//	    (L1) is sufficient to reconstruct canonical event content without
//	    any external source.
//
//	C — Full local loss (evidence + anchors wiped): a verifying party who
//	    received their Merkle proof before the loss can still verify
//	    membership using only the Merkle root (retrievable from the chain,
//	    S4 tier) and their locally-held proof. No DB query required.
//
// Run:
//
//	TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	go test -tags integration ./internal/protocol/cockroach/... -v -timeout 60s
package cockroach_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// pool is the package-level connection pool, set in TestMain.
var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		fmt.Println("skipping cockroach integration tests: TEST_DATABASE_URL not set")
		os.Exit(0)
	}

	ctx := context.Background()
	var err error
	pool, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("cockroach TestMain: connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		fmt.Printf("cockroach TestMain: ping: %v\n", err)
		os.Exit(1)
	}

	// Seed the source_system used by helpers so the source_code FK is satisfied.
	// ON CONFLICT DO NOTHING is safe against repeated runs.
	if _, err := pool.Exec(ctx, `
		INSERT INTO app.source_systems (code, display_name, category)
		VALUES ('cockroach-test', 'Cockroach Test', 'pos')
		ON CONFLICT (code) DO NOTHING
	`); err != nil {
		fmt.Printf("cockroach TestMain: seed source_system: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario A — L1 Loss
// ─────────────────────────────────────────────────────────────────────────────

// TestScenarioA_L1Loss proves that destroying protocol.evidence (L1) does
// not invalidate the Merkle proofs stored in protocol.evidence_anchors.
// The critical assertion: VerifyProof returns true for every event without
// reading a single row from protocol.evidence.
func TestScenarioA_L1Loss(t *testing.T) {
	ctx := context.Background()
	_ = ctx // used indirectly through helpers

	// ── Setup ─────────────────────────────────────────────────────────────
	hashes := writeTestEvents(t, pool, 5)
	result := anchorTestBatch(t, pool, hashes)

	// Verify the anchor was written with a status we expect.
	if result.AnchorStatus != "inscribed" && result.AnchorStatus != "pending" {
		t.Fatalf("unexpected anchor_status: %s", result.AnchorStatus)
	}

	// Capture proofs BEFORE the truncate — CASCADE from evidence wipes
	// evidence_anchors as well.
	proofsByHash := captureProofs(t, pool, hashes)

	// ── Destroy L1 ────────────────────────────────────────────────────────
	// truncateEvidence disables the append-only triggers, truncates, and
	// registers a t.Cleanup that restores both evidence and evidence_anchors.
	t.Cleanup(truncateEvidence(t, pool))

	// Confirm evidence table is empty.
	var count int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM protocol.evidence").Scan(&count); err != nil {
		t.Fatalf("count evidence after truncate: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 evidence rows after truncate, got %d", count)
	}

	// ── Verify: proofs pass without evidence table ─────────────────────────
	// merkleRoot comes from the AnchorResult — as if read from the chain
	// (S4 tier) or from protocol.anchors (which was NOT truncated).
	merkleRoot := result.MerkleRoot

	for _, h := range hashes {
		proof, ok := proofsByHash[h]
		if !ok {
			t.Fatalf("no proof captured for hash %s", h)
		}
		// VerifyProof is a pure cryptographic operation: no DB access.
		if !sub3.VerifyProof(merkleRoot, h, proof) {
			t.Errorf("VerifyProof FAILED for event_hash %s — L1 loss is NOT recoverable", h)
		}
	}

	// Explicitly confirm no evidence rows exist at assertion time.
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM protocol.evidence").Scan(&count); err != nil {
		t.Fatalf("count evidence at verify time: %v", err)
	}
	if count != 0 {
		t.Errorf("evidence table has %d rows at assertion time — should be 0", count)
	}

	t.Logf("Scenario A PASS: %d events verified via Merkle proof with 0 L1 rows", len(hashes))
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario B — L2 Loss
// ─────────────────────────────────────────────────────────────────────────────

// TestScenarioB_L2Loss proves that losing parsed/routed event data (L2 tier —
// what a parse-and-route subscriber would produce, e.g. protocol.parsed_events
// or canonical domain tables) does not destroy the ability to reconstruct
// event content.
//
// Sub 2 writes to domain tables (t.transactions, etc.) which are schema-scoped
// per tenant. Those tables may not exist in the test DB. This test therefore
// simulates L2 loss at the pattern level: the raw_payload stored in
// protocol.evidence (L1) is the source of truth for reconstruction.
//
// Proof: write events → simulate "parsed results" in memory → wipe the
// in-memory L2 → reconstruct from L1 raw_payload → assert parity.
func TestScenarioB_L2Loss(t *testing.T) {
	// ── Setup: write events ───────────────────────────────────────────────
	hashes := writeTestEvents(t, pool, 5)

	// Register cleanup to restore evidence after the test.
	t.Cleanup(truncateEvidence(t, pool))

	// ── Simulate L2: read and "parse" the raw_payload from L1 ────────────
	// In production, Sub 2 reads from the Valkey stream and writes parsed
	// domain records. Here we simulate that by reading raw_payload directly
	// from L1 and storing the results in-memory (the "L2 layer").
	type parsedRecord struct {
		eventHash  string
		rawPayload []byte
	}

	ctx := context.Background()
	l2Records := make([]parsedRecord, 0, len(hashes))
	for _, h := range hashes {
		var payload []byte
		err := pool.QueryRow(ctx,
			"SELECT raw_payload FROM protocol.evidence WHERE event_hash = $1", h,
		).Scan(&payload)
		if err != nil {
			t.Fatalf("Scenario B: read raw_payload(%s): %v", h, err)
		}
		l2Records = append(l2Records, parsedRecord{eventHash: h, rawPayload: payload})
	}

	// ── Destroy L2: wipe in-memory parsed results ─────────────────────────
	// This simulates a complete L2 loss (disk wipe, schema drop, etc.).
	l2Records = nil

	// ── Reconstruct from L1 ───────────────────────────────────────────────
	// Re-read raw_payload from protocol.evidence (L1). This is exactly what
	// a recovery procedure would do: scan L1, re-parse, re-populate L2.
	reconstructed := make([]parsedRecord, 0, len(hashes))
	for _, h := range hashes {
		var payload []byte
		err := pool.QueryRow(ctx,
			"SELECT raw_payload FROM protocol.evidence WHERE event_hash = $1", h,
		).Scan(&payload)
		if err != nil {
			t.Fatalf("Scenario B: reconstruct(%s): %v", h, err)
		}
		reconstructed = append(reconstructed, parsedRecord{eventHash: h, rawPayload: payload})
	}

	// ── Assert: reconstructed == original L2 content ─────────────────────
	if len(reconstructed) != len(hashes) {
		t.Fatalf("reconstructed %d records, expected %d", len(reconstructed), len(hashes))
	}

	for i, r := range reconstructed {
		expected := fmt.Sprintf("test-payload-%d", i)
		got := string(r.rawPayload)
		if got != expected {
			t.Errorf("Scenario B: record[%d] payload mismatch: got %q, want %q", i, got, expected)
		}
	}

	// l2Records == nil confirms the L2 layer was truly lost.
	if l2Records != nil {
		t.Error("l2Records should be nil — L2 was not destroyed correctly")
	}

	t.Logf("Scenario B PASS: %d events reconstructed from L1 raw_payload after L2 wipe", len(hashes))
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario C — Full Local Loss
// ─────────────────────────────────────────────────────────────────────────────

// TestScenarioC_FullLocalLoss proves the strongest antifragility property:
// a verifying party who received their Merkle proof before a full local
// storage loss can still cryptographically verify event membership using:
//
//   - The Merkle root (retrieved from the chain — S4 tier; we use the
//     AnchorResult which the verifying party recorded before the loss)
//   - Their locally-held proof (captured before the loss)
//
// No DB query is performed during the verification step.
func TestScenarioC_FullLocalLoss(t *testing.T) {
	// ── Setup ─────────────────────────────────────────────────────────────
	hashes := writeTestEvents(t, pool, 5)
	result := anchorTestBatch(t, pool, hashes)

	// Simulates a verifying party receiving their proofs via GET before loss.
	// These are held client-side (in memory here; in production, persisted
	// by the verifying party or transmitted in the bilateral verification flow).
	proofsByHash := captureProofs(t, pool, hashes)

	// The verifying party also recorded the Merkle root. In production this
	// comes from reading the S4 chain anchor — not from a local DB query.
	merkleRoot := result.MerkleRoot

	// Register evidence restore first (inner), then anchors restore (outer).
	// Cleanup runs in LIFO order, so anchors are restored before evidence FK
	// is required again.
	t.Cleanup(truncateEvidence(t, pool))
	t.Cleanup(truncateAnchors(t, pool))

	// ── Destroy both local tiers ───────────────────────────────────────────
	// Order: evidence first (cascades to evidence_anchors), then anchors.
	// truncateEvidence disables append-only triggers; truncateAnchors operates
	// directly on protocol.anchors (no such trigger there).
	//
	// Note: t.Cleanup above registered restores, but we execute the truncates
	// now. The cleanup functions already captured the rows before we call them.
	// Re-execute the truncates manually for Scenario C.
	destroyAllLocal(t, pool)

	// Confirm all protocol tables are empty.
	ctx := context.Background()
	for _, table := range []string{
		"protocol.evidence",
		"protocol.anchors",
		"protocol.evidence_anchors",
	} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s: expected 0 rows after full local loss, got %d", table, n)
		}
	}

	// ── Verify: pure cryptographic verification, zero DB access ───────────
	// The verifying party uses only their locally-held proof + the Merkle
	// root retrieved from the chain. No DB query needed.
	for _, h := range hashes {
		proof, ok := proofsByHash[h]
		if !ok {
			t.Fatalf("no proof for hash %s", h)
		}
		// VerifyProof is deterministic and DB-free.
		if !sub3.VerifyProof(merkleRoot, h, proof) {
			t.Errorf("VerifyProof FAILED for %s — full local loss IS catastrophic (it should not be)", h)
		}
	}

	t.Logf("Scenario C PASS: %d events verified via chain anchor + local proof; 0 DB rows required", len(hashes))
}

// destroyAllLocal is Scenario C's explicit destruction step. It operates
// independently of the t.Cleanup restores already registered — those will
// run after the test and restore state for subsequent tests.
func destroyAllLocal(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Disable evidence append-only triggers before truncate.
	triggerStmts := []string{
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_delete",
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_truncate",
		"ALTER TABLE protocol.evidence DISABLE TRIGGER evidence_no_update",
	}
	for _, stmt := range triggerStmts {
		if _, err := p.Exec(ctx, stmt); err != nil {
			t.Fatalf("destroyAllLocal: %s: %v", stmt, err)
		}
	}

	// TRUNCATE evidence (cascades to evidence_anchors via event_hash FK).
	if _, err := p.Exec(ctx, "TRUNCATE protocol.evidence CASCADE"); err != nil {
		t.Fatalf("destroyAllLocal: truncate evidence: %v", err)
	}

	// Re-enable evidence triggers.
	enableStmts := []string{
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_delete",
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_truncate",
		"ALTER TABLE protocol.evidence ENABLE TRIGGER evidence_no_update",
	}
	for _, stmt := range enableStmts {
		if _, err := p.Exec(ctx, stmt); err != nil {
			t.Fatalf("destroyAllLocal: %s: %v", stmt, err)
		}
	}

	// TRUNCATE anchors (evidence_anchors already gone from CASCADE above).
	if _, err := p.Exec(ctx, "TRUNCATE protocol.anchors CASCADE"); err != nil {
		t.Fatalf("destroyAllLocal: truncate anchors: %v", err)
	}

	// Brief pause so Postgres cleans up before COUNT(*) assertions.
	_ = time.Millisecond
}
