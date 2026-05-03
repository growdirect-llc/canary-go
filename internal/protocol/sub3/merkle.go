// Package sub3 implements the Merkle & Ordinal anchor worker — Node 5/6
// of the Canary Protocol pipeline (patent Application 63/991,596).
//
// Responsibilities:
//
//   - Poll protocol.evidence for rows not yet anchored
//   - Build a binary Merkle tree over the chain_hash values of the batch
//   - Inscribe the Merkle root on Bitcoin (signet by default) via OrdinalsBot
//   - Record the inscription in protocol.anchors
//   - Record per-event Merkle proof paths in protocol.evidence_anchors
//   - Expose GET /v1/protocol/anchor/{event_hash} for bilateral verification
//
// The Merkle tree uses SHA-256 of the concatenation of the decoded byte
// arrays of the left and right child hashes. Odd-length levels duplicate
// the last node before hashing upward (standard binary Merkle convention).
package sub3

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ProofNode is one step in a Merkle inclusion proof.
//
// To verify: hash the target leaf together with the sibling. When
// Position is "left", the sibling is on the left — i.e., the combined
// input to SHA-256 is sibling_hash || current_hash. When Position is
// "right", the sibling is on the right.
type ProofNode struct {
	SiblingHash string `json:"sibling_hash"`
	Position    string `json:"position"` // "left" or "right"
}

// MerkleResult holds the output of BuildMerkleTree.
type MerkleResult struct {
	// Root is the hex-encoded Merkle root SHA-256 hash.
	Root string
	// Proofs[i] is the inclusion proof for leaf i (the leaf at
	// leaves[i]). Each proof is a slice of ProofNode values from the
	// leaf level up to (but not including) the root.
	Proofs [][]ProofNode
}

// BuildMerkleTree constructs a binary Merkle tree bottom-up over the
// supplied leaf hashes (hex strings). Returns the root and a proof path
// for every leaf.
//
// Rules:
//   - 1-leaf tree: root == leaf hash; proof is empty.
//   - Odd-length levels: the last node is duplicated before hashing.
//   - All internal nodes are SHA-256(decoded(left) || decoded(right)) in hex.
//
// Returns an error if leaves is empty.
func BuildMerkleTree(leaves []string) (MerkleResult, error) {
	if len(leaves) == 0 {
		return MerkleResult{}, errors.New("sub3: BuildMerkleTree: no leaves supplied")
	}

	n := len(leaves)

	// proofs[i] accumulates the ProofNode list for leaf i as we climb.
	proofs := make([][]ProofNode, n)
	for i := range proofs {
		proofs[i] = []ProofNode{}
	}

	// Each leaf maps to its own index at the start.
	// As we merge upward, parent nodes represent a range of leaf indices.
	// We track, for each position in the current level, the list of
	// original leaf indices that flow through it.
	type levelNode struct {
		hash     string
		leafIdxs []int
	}

	currentLevel := make([]levelNode, n)
	for i, h := range leaves {
		currentLevel[i] = levelNode{hash: h, leafIdxs: []int{i}}
	}

	for len(currentLevel) > 1 {
		nextLevel := []levelNode{}

		for i := 0; i < len(currentLevel); i += 2 {
			left := currentLevel[i]

			var right levelNode
			isDuplicate := false
			if i+1 < len(currentLevel) {
				right = currentLevel[i+1]
			} else {
				// Odd: duplicate left node.
				right = levelNode{hash: left.hash, leafIdxs: append([]int{}, left.leafIdxs...)}
				isDuplicate = true
			}

			parentHash, err := merkleParent(left.hash, right.hash)
			if err != nil {
				return MerkleResult{}, err
			}

			// Add proof nodes for all leaves on the LEFT side.
			// Their sibling is the RIGHT node, positioned "right".
			for _, li := range left.leafIdxs {
				proofs[li] = append(proofs[li], ProofNode{
					SiblingHash: right.hash,
					Position:    "right",
				})
			}

			// Add proof nodes for all leaves on the RIGHT side.
			// Their sibling is the LEFT node, positioned "left".
			// In the duplicate case, the right side contains the same
			// leaf indices as the left side — those leaves need to know
			// their sibling is the duplicate of themselves (left.hash),
			// positioned "left". This IS a valid proof step: given the
			// leaf hash L, sibling L (position=left) → SHA256(L||L) = parent.
			if !isDuplicate {
				for _, li := range right.leafIdxs {
					proofs[li] = append(proofs[li], ProofNode{
						SiblingHash: left.hash,
						Position:    "left",
					})
				}
			}
			// In the duplicate case the leaves in left.leafIdxs already
			// received their proof node above (position="right" with
			// sibling = right.hash = left.hash). That single step is
			// correct: SHA256(leaf || leaf) = parentHash. No second
			// proof node is needed for those leaves.
			//
			// Also, the parent's leafIdxs must NOT include duplicates:
			// the right side is a structural duplicate, not a new leaf.
			var parentLeafIdxs []int
			if isDuplicate {
				parentLeafIdxs = append([]int{}, left.leafIdxs...)
			} else {
				parentLeafIdxs = append(append([]int{}, left.leafIdxs...), right.leafIdxs...)
			}
			nextLevel = append(nextLevel, levelNode{hash: parentHash, leafIdxs: parentLeafIdxs})
		}

		currentLevel = nextLevel
	}

	return MerkleResult{
		Root:   currentLevel[0].hash,
		Proofs: proofs,
	}, nil
}

// VerifyProof verifies that leafHash is a member of a Merkle tree with
// the given root, using the supplied proof path. Returns true iff the
// proof is valid.
func VerifyProof(root, leafHash string, proof []ProofNode) bool {
	current := leafHash
	for _, node := range proof {
		var err error
		switch node.Position {
		case "left":
			current, err = merkleParent(node.SiblingHash, current)
		case "right":
			current, err = merkleParent(current, node.SiblingHash)
		default:
			return false
		}
		if err != nil {
			return false
		}
	}
	return current == root
}

// merkleParent returns SHA-256(decoded(left) || decoded(right)) encoded as
// lowercase hex. Each input is a hex-encoded byte array; the bytes are decoded
// and concatenated before hashing. This produces SHA-256 of the concatenated
// decoded byte arrays — not of the hex string characters.
func merkleParent(left, right string) (string, error) {
	lb, err := hex.DecodeString(left)
	if err != nil {
		return "", fmt.Errorf("merkle: decode left: %w", err)
	}
	rb, err := hex.DecodeString(right)
	if err != nil {
		return "", fmt.Errorf("merkle: decode right: %w", err)
	}
	combined := append(lb, rb...)
	h := sha256.Sum256(combined)
	return hex.EncodeToString(h[:]), nil
}
