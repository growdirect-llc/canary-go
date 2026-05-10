//go:build integration

// Per-tenant verifiability test for Sub 3 — closes GRO-907 (Wave B.2).
//
// Before this fix, sub3 batched protocol.evidence rows from every
// merchant into a single Merkle tree and produced one Bitcoin
// inscription per cycle. Each tenant's evidence_anchor proof
// referenced sibling hashes from every other tenant — leaking the
// count and existence of other tenants' events and breaking the
// patent's per-merchant verifiability claim (Application 63/991,596,
// FIG. 4 — sub1 explicitly designs per-merchant chains for this).
//
// This test seeds two merchants, inserts disjoint evidence sets for
// each, calls WriteAnchor with a stub Inscriber, and asserts:
//
//   - Two *AnchorResult values returned (one per merchant)
//   - The Inscriber was called exactly twice with two distinct
//     Merkle roots
//   - tenantA's anchor's evidence_anchors row set is exactly
//     tenantA's events (no tenantB leakage), and vice versa
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run PerMerchant ./internal/protocol/sub3/...

package sub3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/testutil"
)

// xtSub3DBPool returns a pool against DATABASE_URL or skips. Mirrors
// the testutil/cross_tenant_test convention used elsewhere.
func xtSub3DBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedMerchantForTenant inserts an org + merchant tied to the supplied
// tenant id and returns the merchant id. testutil.SeedTenant already
// inserts an org + tenant; we add a merchant tied to the same org so
// app.merchants is consistent with the test fixture model.
//
// Cleanup deletes evidence rows (bypassing the append-only trigger via
// session_replication_role = 'replica', mirroring sub1's integration
// test) plus the merchant row. The tenant + org are cleaned up by
// downstream test harness TRUNCATE.
func seedMerchantForTenant(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
) uuid.UUID {
	t.Helper()

	// Look up the org id for this tenant — testutil.SeedTenant inserts
	// both as a pair, so the org exists.
	var orgID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT organization_id FROM app.tenants WHERE id = $1`, tenantID,
	).Scan(&orgID); err != nil {
		t.Fatalf("lookup org for tenant %s: %v", tenantID, err)
	}

	merchantID := uuid.New()
	short := merchantID.String()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants
		    (id, organization_id, tenant_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID, "sub3-pt-"+short, "sub3 per-tenant test "+short,
	); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	t.Cleanup(func() {
		// protocol.evidence is append-only; bypass the trigger inside
		// a one-off transaction so the DELETE succeeds. Same pattern as
		// the sub1 integration test.
		tx, err := pool.Begin(ctx)
		if err != nil {
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, _ = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
		_, _ = tx.Exec(ctx, `DELETE FROM protocol.evidence_anchors ea
		                     USING protocol.evidence e
		                     WHERE ea.event_hash = e.event_hash
		                       AND e.merchant_id = $1`, merchantID)
		_, _ = tx.Exec(ctx, `DELETE FROM protocol.evidence WHERE merchant_id = $1`, merchantID)
		_ = tx.Commit(ctx)

		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
	})

	return merchantID
}

// insertEvidenceRow inserts a single protocol.evidence row directly.
// We bypass sub1.WriteEvidence because this test is concerned with
// sub3's batching behavior, not chain-hash continuity. Each row's
// chain_hash is a unique deterministic fake derived from (merchant, i).
func insertEvidenceRow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	merchantID uuid.UUID,
	idx int,
	ingestedAt time.Time,
) string {
	t.Helper()

	eventID := uuid.New()
	// event_hash must be globally unique (UNIQUE constraint).
	rawEH := sha256.Sum256([]byte(fmt.Sprintf("ev-%s-%d", merchantID, idx)))
	eventHash := hex.EncodeToString(rawEH[:])
	rawCH := sha256.Sum256([]byte(fmt.Sprintf("ch-%s-%d", merchantID, idx)))
	chainHash := hex.EncodeToString(rawCH[:])

	if _, err := pool.Exec(ctx,
		`INSERT INTO protocol.evidence
		    (event_id, event_hash, chain_hash, source_code,
		     merchant_id, raw_payload, ingested_at)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)`,
		eventID, eventHash, chainHash, "sub3-per-tenant-test",
		merchantID, `{"test":"per-tenant"}`, ingestedAt,
	); err != nil {
		t.Fatalf("insert evidence (merchant=%s idx=%d): %v", merchantID, idx, err)
	}
	return eventHash
}

// captureInscriber records every Inscribe call so the test can assert
// on call count and the set of roots passed through.
type captureInscriber struct {
	mu    sync.Mutex
	calls []string // merkle roots in call order
}

func (c *captureInscriber) Inscribe(_ context.Context, merkleRoot string, _ string) (InscribeResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, merkleRoot)
	// Deterministic InscriptionID derived from the root, matching the
	// StubInscriber convention so the writeAnchorResults insert sees a
	// non-empty inscription_id and stamps anchor_status = 'inscribed'.
	h := sha256.Sum256([]byte(merkleRoot))
	short := fmt.Sprintf("%x", h[:8])
	return InscribeResult{
		InscriptionID: "stub:" + short,
		TxID:          "stub-tx-" + short,
		BlockHeight:   0,
	}, nil
}

func (c *captureInscriber) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

// TestWriteAnchor_PerMerchantSeparation seeds two merchants, inserts
// disjoint evidence rows for each, and proves WriteAnchor produces
// exactly one anchor per merchant with no leaf-set leakage between
// them. This is the GRO-907 regression test.
func TestWriteAnchor_PerMerchantSeparation(t *testing.T) {
	ctx := context.Background()
	pool := xtSub3DBPool(t)

	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)

	merchantA := seedMerchantForTenant(t, ctx, pool, tenantA)
	merchantB := seedMerchantForTenant(t, ctx, pool, tenantB)

	// Distinct counts so an off-by-one swap surfaces. Both above
	// minBatch=2 so both merchants must be inscribed.
	const (
		nA = 4
		nB = 3
	)

	base := time.Now().UTC().Add(-1 * time.Hour)

	// Insert in interleaved order to prove the SELECT … ORDER BY
	// (merchant_id, ingested_at) in lockAndFetchUnanchored — not the
	// physical insert order — is what groups the rows.
	hashesA := make([]string, 0, nA)
	hashesB := make([]string, 0, nB)
	for i := 0; i < nA || i < nB; i++ {
		if i < nA {
			h := insertEvidenceRow(t, ctx, pool, merchantA, i, base.Add(time.Duration(i)*time.Second))
			hashesA = append(hashesA, h)
		}
		if i < nB {
			h := insertEvidenceRow(t, ctx, pool, merchantB, i, base.Add(time.Duration(i)*time.Second+500*time.Millisecond))
			hashesB = append(hashesB, h)
		}
	}

	store := NewStore(pool, "signet")
	cap := &captureInscriber{}

	results, err := store.WriteAnchor(ctx, cap, 100, 2)
	if err != nil {
		t.Fatalf("WriteAnchor: %v", err)
	}

	// Filter the full result set to only the merchants this test seeded.
	// WriteAnchor scans every unanchored evidence row in the database, so
	// leftovers from a prior aborted run (where cleanup never fired) or a
	// concurrent integration test in another package can produce extra
	// anchors that aren't under test here. The contract under test is
	// per-merchant separation for *our two seeded merchants*, not "this
	// test owns the database."
	seeded := map[uuid.UUID]bool{merchantA: true, merchantB: true}
	results = filterAnchorsByMerchant(results, seeded)

	// ── Assertion: exactly two anchors returned for our seeded merchants ──
	if len(results) != 2 {
		t.Fatalf("expected 2 anchors for seeded merchants, got %d", len(results))
	}

	// ── Assertion: Inscriber received one call for each seeded merchant's
	//    Merkle root, and the two roots are distinct. We don't bound the
	//    total inscribe count: leftover unanchored evidence legitimately
	//    triggers additional Inscribe calls for non-test merchants, and
	//    that's outside this test's contract. ──────────────────────────────
	calls := cap.snapshot()
	for _, r := range results {
		if countOccurrences(calls, r.MerkleRoot) != 1 {
			t.Fatalf(
				"expected exactly one Inscribe call for merchant=%s root=%s; got %d (all calls=%v)",
				r.MerchantID, r.MerkleRoot, countOccurrences(calls, r.MerkleRoot), calls,
			)
		}
	}
	if results[0].MerkleRoot == results[1].MerkleRoot {
		t.Fatalf("two anchors but identical Merkle root: %s", results[0].MerkleRoot)
	}

	// ── Index results by merchant id for the leaf-set assertions ──────────
	resultByMerchant := map[uuid.UUID]*AnchorResult{}
	for _, r := range results {
		if r.MerchantID == uuid.Nil {
			t.Fatalf("AnchorResult missing MerchantID: %+v", r)
		}
		if _, dup := resultByMerchant[r.MerchantID]; dup {
			t.Fatalf("two anchors with same MerchantID=%s", r.MerchantID)
		}
		resultByMerchant[r.MerchantID] = r
	}
	rA, ok := resultByMerchant[merchantA]
	if !ok {
		t.Fatalf("no anchor for merchantA=%s; got merchants=%v", merchantA, resultByMerchant)
	}
	rB, ok := resultByMerchant[merchantB]
	if !ok {
		t.Fatalf("no anchor for merchantB=%s; got merchants=%v", merchantB, resultByMerchant)
	}

	if rA.EventCount != nA {
		t.Errorf("merchantA EventCount: got %d want %d", rA.EventCount, nA)
	}
	if rB.EventCount != nB {
		t.Errorf("merchantB EventCount: got %d want %d", rB.EventCount, nB)
	}
	if rA.MerkleRoot == rB.MerkleRoot {
		t.Errorf("anchors share a Merkle root: %s", rA.MerkleRoot)
	}

	// ── Assertion: each anchor's evidence_anchors rows reference only
	//    that merchant's events (no leakage either direction). ─────────────
	gotA := readAnchoredHashes(t, ctx, pool, rA.AnchorID)
	gotB := readAnchoredHashes(t, ctx, pool, rB.AnchorID)

	wantA := append([]string(nil), hashesA...)
	wantB := append([]string(nil), hashesB...)
	sort.Strings(wantA)
	sort.Strings(wantB)
	sort.Strings(gotA)
	sort.Strings(gotB)

	if !equalStringSlices(gotA, wantA) {
		t.Errorf("merchantA anchor leaf set mismatch:\n  got=%v\n  want=%v", gotA, wantA)
	}
	if !equalStringSlices(gotB, wantB) {
		t.Errorf("merchantB anchor leaf set mismatch:\n  got=%v\n  want=%v", gotB, wantB)
	}

	// ── Assertion: leaf sets are disjoint ─────────────────────────────────
	setA := map[string]struct{}{}
	for _, h := range gotA {
		setA[h] = struct{}{}
	}
	for _, h := range gotB {
		if _, overlap := setA[h]; overlap {
			t.Errorf("event_hash %s appears under both anchors — sets must be disjoint", h)
		}
	}
}

// readAnchoredHashes returns every event_hash bound to the given
// anchor via protocol.evidence_anchors.
func readAnchoredHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, anchorID uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT event_hash FROM protocol.evidence_anchors WHERE anchor_id = $1`,
		anchorID,
	)
	if err != nil {
		t.Fatalf("read evidence_anchors for %s: %v", anchorID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan evidence_anchors: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// filterAnchorsByMerchant returns only the AnchorResults whose MerchantID is
// in the provided set. Used to scope this test's assertions to the two
// merchants it seeded, ignoring any other anchors WriteAnchor produced from
// unanchored leftovers in the test DB.
func filterAnchorsByMerchant(results []*AnchorResult, want map[uuid.UUID]bool) []*AnchorResult {
	out := make([]*AnchorResult, 0, len(want))
	for _, r := range results {
		if want[r.MerchantID] {
			out = append(out, r)
		}
	}
	return out
}

// countOccurrences returns the number of elements in xs equal to want.
func countOccurrences(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}
