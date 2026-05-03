package sub3

import (
	"context"
	"encoding/json"
	"testing"
)

// TestStubInscriber_Deterministic verifies the stub always returns the
// same inscription_id for the same merkle root, and that the result
// parses cleanly.
func TestStubInscriber_Deterministic(t *testing.T) {
	s := &StubInscriber{}
	ctx := context.Background()

	root := "abc123deadbeef"
	r1, err := s.Inscribe(ctx, root, "signet")
	if err != nil {
		t.Fatalf("inscribe: %v", err)
	}
	r2, err := s.Inscribe(ctx, root, "signet")
	if err != nil {
		t.Fatalf("inscribe2: %v", err)
	}
	if r1.InscriptionID != r2.InscriptionID {
		t.Errorf("non-deterministic: %s != %s", r1.InscriptionID, r2.InscriptionID)
	}
	if len(r1.InscriptionID) == 0 {
		t.Fatal("empty inscription_id")
	}
	t.Logf("stub inscription_id: %s", r1.InscriptionID)
}

// TestStubInscriber_DifferentRoots_DifferentIDs ensures different Merkle
// roots produce different stub identifiers.
func TestStubInscriber_DifferentRoots_DifferentIDs(t *testing.T) {
	s := &StubInscriber{}
	ctx := context.Background()

	r1, _ := s.Inscribe(ctx, "root-aaa", "signet")
	r2, _ := s.Inscribe(ctx, "root-bbb", "signet")
	if r1.InscriptionID == r2.InscriptionID {
		t.Fatal("different roots should produce different stub IDs")
	}
}

// TestProofNodeMarshal verifies that ProofNode round-trips through JSON
// correctly, because the DB stores merkle_proof as JSONB.
func TestProofNodeMarshal(t *testing.T) {
	proof := []ProofNode{
		{SiblingHash: "aabbcc", Position: "left"},
		{SiblingHash: "ddeeff", Position: "right"},
	}
	b, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got []ProofNode
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != len(proof) {
		t.Fatalf("length mismatch: %d != %d", len(got), len(proof))
	}
	for i, p := range proof {
		if got[i].SiblingHash != p.SiblingHash || got[i].Position != p.Position {
			t.Errorf("proof[%d] mismatch: got=%+v want=%+v", i, got[i], p)
		}
	}
}
