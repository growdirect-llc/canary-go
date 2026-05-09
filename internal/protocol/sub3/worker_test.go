package sub3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─── Stub store ──────────────────────────────────────────────────────────────

// stubStore implements AnchorStorer for unit tests. It simulates the
// WriteAnchor behavior without a real database: it calls the inscriber
// once per simulated merchant when rowCount >= minBatch, mirroring the
// real Store contract (one anchor per merchant per cycle, GRO-907).
//
// merchantCount controls how many distinct merchants the stub
// pretends to have rows for. Each merchant is treated as having
// rowCount rows. merchantCount=0 is normalized to 1 to keep the
// pre-GRO-907 single-merchant test cases working.
type stubStore struct {
	rowCount      int // rows per merchant
	merchantCount int // number of distinct merchants; 0 → 1
	inscriber     Inscriber
}

func (s *stubStore) WriteAnchor(ctx context.Context, inscriber Inscriber, batchSize, minBatch int) ([]*AnchorResult, error) {
	if s.rowCount < minBatch {
		// Below threshold — no-op, same as real Store.
		return nil, nil
	}
	merchants := s.merchantCount
	if merchants == 0 {
		merchants = 1
	}
	results := make([]*AnchorResult, 0, merchants)
	for m := 0; m < merchants; m++ {
		// Build a per-merchant Merkle tree over fake leaves. Vary the
		// leaf seed by merchant index so each merchant gets a distinct
		// root — matching the real Store's per-merchant separation.
		leaves := make([]string, s.rowCount)
		for i := range leaves {
			h := sha256.Sum256([]byte(fmt.Sprintf("fake-chain-hash-m%d-%d", m, i)))
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
		results = append(results, &AnchorResult{
			AnchorID:      uuid.New(),
			MerchantID:    uuid.New(),
			MerkleRoot:    mr.Root,
			InscriptionID: ir.InscriptionID,
			EventCount:    s.rowCount,
			AnchorStatus:  "inscribed",
			AnchoredAt:    testTime(),
		})
	}
	return results, nil
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

// TestWorker_MultiMerchant_OneInscribeEach verifies that when the stub
// reports multiple merchants over MinBatch the worker calls Inscribe
// once per merchant — the per-tenant verifiability invariant from
// GRO-907.
func TestWorker_MultiMerchant_OneInscribeEach(t *testing.T) {
	stub := &countingInscriber{}
	store := &stubStore{rowCount: 3, merchantCount: 3, inscriber: stub}

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
	if stub.calls != 3 {
		t.Fatalf("Inscribe called %d times, want 3 (one per merchant)", stub.calls)
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
