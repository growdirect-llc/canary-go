package sub3

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// leafHash produces a deterministic leaf hash for test inputs.
func leafHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// computeParent mirrors merkleParent — recomputed here so the test has
// no implicit dependency on the implementation's helper.
func computeParent(l, r string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s%s", l, r)
	return hex.EncodeToString(h.Sum(nil))
}

// ─── BuildMerkleTree ─────────────────────────────────────────────────────────

func TestBuildMerkleTree_Empty_ReturnsError(t *testing.T) {
	_, err := BuildMerkleTree(nil)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestBuildMerkleTree_OneLeaf_RootEqualsLeaf(t *testing.T) {
	leaf := leafHash("leaf-0")
	res, err := BuildMerkleTree([]string{leaf})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Root != leaf {
		t.Errorf("1-leaf root should equal leaf; root=%s leaf=%s", res.Root, leaf)
	}
	if len(res.Proofs) != 1 {
		t.Fatalf("expected 1 proof, got %d", len(res.Proofs))
	}
	if len(res.Proofs[0]) != 0 {
		t.Errorf("1-leaf proof should be empty, got %v", res.Proofs[0])
	}
}

func TestBuildMerkleTree_TwoLeaves_VerifyEach(t *testing.T) {
	l0 := leafHash("leaf-0")
	l1 := leafHash("leaf-1")
	res, err := BuildMerkleTree([]string{l0, l1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Root must be parent(l0, l1).
	wantRoot := computeParent(l0, l1)
	if res.Root != wantRoot {
		t.Errorf("root mismatch: got %s want %s", res.Root, wantRoot)
	}

	// Both proofs must verify.
	for i, leaf := range []string{l0, l1} {
		if !VerifyProof(res.Root, leaf, res.Proofs[i]) {
			t.Errorf("leaf %d proof failed", i)
		}
	}
}

func TestBuildMerkleTree_FourLeaves_VerifyEach(t *testing.T) {
	leaves := make([]string, 4)
	for i := range leaves {
		leaves[i] = leafHash(fmt.Sprintf("leaf-%d", i))
	}

	res, err := BuildMerkleTree(leaves)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Recompute expected root bottom-up.
	p01 := computeParent(leaves[0], leaves[1])
	p23 := computeParent(leaves[2], leaves[3])
	wantRoot := computeParent(p01, p23)

	if res.Root != wantRoot {
		t.Errorf("4-leaf root mismatch: got %s want %s", res.Root, wantRoot)
	}

	for i, leaf := range leaves {
		if !VerifyProof(res.Root, leaf, res.Proofs[i]) {
			t.Errorf("leaf %d proof failed", i)
		}
	}
}

func TestBuildMerkleTree_FiveLeaves_OddDuplicate_VerifyAll(t *testing.T) {
	// 5 leaves: the 5th is duplicated at the 3-node level so the tree
	// balances. This is the standard Bitcoin Merkle convention.
	leaves := make([]string, 5)
	for i := range leaves {
		leaves[i] = leafHash(fmt.Sprintf("leaf-%d", i))
	}

	res, err := BuildMerkleTree(leaves)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All 5 proofs must verify against the returned root.
	for i, leaf := range leaves {
		if !VerifyProof(res.Root, leaf, res.Proofs[i]) {
			t.Errorf("leaf %d proof failed (5-leaf odd tree)", i)
		}
	}
}

func TestBuildMerkleTree_EightLeaves_VerifyEach(t *testing.T) {
	leaves := make([]string, 8)
	for i := range leaves {
		leaves[i] = leafHash(fmt.Sprintf("leaf-%d", i))
	}
	res, err := BuildMerkleTree(leaves)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, leaf := range leaves {
		if !VerifyProof(res.Root, leaf, res.Proofs[i]) {
			t.Errorf("leaf %d proof failed (8-leaf tree)", i)
		}
	}
}

// ─── VerifyProof ─────────────────────────────────────────────────────────────

func TestVerifyProof_InvalidPosition_ReturnsFalse(t *testing.T) {
	leaf := leafHash("x")
	ok := VerifyProof("any-root", leaf, []ProofNode{{SiblingHash: leaf, Position: "neither"}})
	if ok {
		t.Fatal("expected false for invalid Position")
	}
}

func TestVerifyProof_EmptyProof_SingleLeaf(t *testing.T) {
	leaf := leafHash("single")
	// Single-leaf tree: root == leaf, proof is empty.
	if !VerifyProof(leaf, leaf, nil) {
		t.Fatal("empty proof against self should succeed")
	}
}

func TestVerifyProof_WrongRoot_ReturnsFalse(t *testing.T) {
	l0 := leafHash("leaf-0")
	l1 := leafHash("leaf-1")
	res, err := BuildMerkleTree([]string{l0, l1})
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the root.
	if VerifyProof("deadbeef", l0, res.Proofs[0]) {
		t.Fatal("expected false with wrong root")
	}
}
