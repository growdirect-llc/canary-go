package sub3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ─── Stub store ──────────────────────────────────────────────────────────────

// stubStore implements AnchorStorer for unit tests. It simulates the
// WriteAnchor behavior without a real database: it calls the inscriber only
// when rowCount >= minBatch, mirroring the real Store contract.
type stubStore struct {
	rowCount  int // number of rows the stub pretends to have fetched
	inscriber Inscriber
}

func (s *stubStore) WriteAnchor(ctx context.Context, inscriber Inscriber, batchSize, minBatch int) (*AnchorResult, error) {
	if s.rowCount < minBatch {
		// Below threshold — no-op, same as real Store.
		return nil, nil
	}
	// Build a minimal Merkle tree over fake leaves.
	leaves := make([]string, s.rowCount)
	for i := range leaves {
		h := sha256.Sum256([]byte(fmt.Sprintf("fake-chain-hash-%d", i)))
		leaves[i] = hex.EncodeToString(h[:])
	}
	mr, err := BuildMerkleTree(leaves)
	if err != nil {
		return nil, err
	}
	ir, err := inscriber.Inscribe(ctx, mr.Root, "signet")
	if err != nil {
		return nil, err
	}
	return &AnchorResult{
		MerkleRoot:    mr.Root,
		InscriptionID: ir.InscriptionID,
		EventCount:    s.rowCount,
		AnchorStatus:  "inscribed",
		AnchoredAt:    testTime(),
	}, nil
}

func testTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestWorker_SkipBelowMinBatch verifies that when fewer than MinBatch rows
// are available the worker skips inscription. It exercises w.Tick with a
// stub store that reports 1 row (below MinBatch=2) and asserts the
// countingInscriber was never called.
func TestWorker_SkipBelowMinBatch(t *testing.T) {
	stub := &countingInscriber{}
	store := &stubStore{rowCount: 1, inscriber: stub}

	w := newWorkerWithStore(WorkerConfig{
		Inscriber: stub,
		MinBatch:  2,
		BatchSize: 50,
		Logger:    zap.NewNop(),
	}, store)

	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatalf("Tick returned unexpected error: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("Inscribe called %d times, want 0 (below MinBatch)", stub.calls)
	}
}

// TestWorker_ProcessBatch_StubInscriber verifies the full Merkle build +
// stub inscription path without a real DB.
func TestWorker_ProcessBatch_StubInscriber(t *testing.T) {
	rows := []EvidenceRow{
		{EventHash: "ev-0", ChainHash: makeLeaf(0)},
		{EventHash: "ev-1", ChainHash: makeLeaf(1)},
		{EventHash: "ev-2", ChainHash: makeLeaf(2)},
	}

	// Build leaves.
	leaves := make([]string, len(rows))
	for i, r := range rows {
		leaves[i] = r.ChainHash
	}

	res, err := BuildMerkleTree(leaves)
	if err != nil {
		t.Fatalf("BuildMerkleTree: %v", err)
	}

	// Verify all proofs against the returned root.
	for i, r := range rows {
		if !VerifyProof(res.Root, r.ChainHash, res.Proofs[i]) {
			t.Errorf("proof for event %s failed", r.EventHash)
		}
	}

	stub := &StubInscriber{}
	ctx := context.Background()
	ir, err := stub.Inscribe(ctx, res.Root, "signet")
	if err != nil {
		t.Fatalf("Inscribe: %v", err)
	}
	if ir.InscriptionID == "" {
		t.Fatal("empty inscription_id from stub")
	}
	t.Logf("root=%s inscription=%s", res.Root, ir.InscriptionID)
}

// TestWorker_Idempotent_ProofVerification runs VerifyProof twice for the
// same leaf and confirms the result is identical — simulating a
// re-verification pass after the DB round-trip.
func TestWorker_Idempotent_ProofVerification(t *testing.T) {
	leaves := []string{makeLeaf(0), makeLeaf(1), makeLeaf(2), makeLeaf(3)}
	res, err := BuildMerkleTree(leaves)
	if err != nil {
		t.Fatal(err)
	}

	for i, leaf := range leaves {
		first := VerifyProof(res.Root, leaf, res.Proofs[i])
		second := VerifyProof(res.Root, leaf, res.Proofs[i])
		if !first || !second {
			t.Errorf("leaf %d: first=%v second=%v", i, first, second)
		}
	}
}

// TestWorker_Defaults verifies that NewWorker applies all expected defaults.
func TestWorker_Defaults(t *testing.T) {
	w := NewWorker(WorkerConfig{
		Inscriber: &StubInscriber{},
		Logger:    zap.NewNop(),
	})
	if w.cfg.Network != "signet" {
		t.Errorf("default network: got %s want signet", w.cfg.Network)
	}
	if w.cfg.PollInterval != 10*time.Minute {
		t.Errorf("default poll: got %v want 10m", w.cfg.PollInterval)
	}
	if w.cfg.BatchSize != 50 {
		t.Errorf("default batch: got %d want 50", w.cfg.BatchSize)
	}
	if w.cfg.MinBatch != 2 {
		t.Errorf("default minBatch: got %d want 2", w.cfg.MinBatch)
	}
}

// TestWorker_AboveMinBatch_CallsInscriber verifies that when rows >= MinBatch
// the inscriber IS called via Tick.
func TestWorker_AboveMinBatch_CallsInscriber(t *testing.T) {
	stub := &countingInscriber{}
	store := &stubStore{rowCount: 3, inscriber: stub}

	w := newWorkerWithStore(WorkerConfig{
		Inscriber: stub,
		MinBatch:  2,
		BatchSize: 50,
		Logger:    zap.NewNop(),
	}, store)

	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatalf("Tick returned unexpected error: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("Inscribe called %d times, want 1", stub.calls)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// makeLeaf returns a deterministic leaf hash.
func makeLeaf(i int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("chain-hash-%d", i)))
	return hex.EncodeToString(h[:])
}

type countingInscriber struct {
	calls int
}

func (c *countingInscriber) Inscribe(_ context.Context, _ string, _ string) (InscribeResult, error) {
	c.calls++
	return InscribeResult{InscriptionID: "fake"}, nil
}
