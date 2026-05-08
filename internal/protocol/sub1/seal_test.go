package sub1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

// ---------------------------------------------------------------------------
// Stub DB
// ---------------------------------------------------------------------------

// stubDB simulates protocol.evidence well enough to test the chain
// logic without Postgres. Append-only: rows go in via Exec; the most
// recent row per merchant is returned by QueryRow.
type stubDB struct {
	// rows in append order
	rows []evidenceRow
	// hashes seen — duplicates simulate the UNIQUE constraint
	seen map[string]struct{}
	// when forceErr is non-nil, Exec returns it (used to simulate non-
	// duplicate errors)
	forceErr error
}

type evidenceRow struct {
	eventID    uuid.UUID
	eventHash  string
	chainHash  string
	prevHash   string
	sourceCode string
	merchantID uuid.UUID
	rawPayload []byte
	ingestedAt time.Time
}

func newStubDB() *stubDB {
	return &stubDB{seen: make(map[string]struct{})}
}

func (s *stubDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	merchantID, _ := args[0].(uuid.UUID)
	// Most recent first
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.rows[i].merchantID == merchantID {
			return stubRow{val: s.rows[i].chainHash}
		}
	}
	return stubRow{noRows: true}
}

func (s *stubDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if s.forceErr != nil {
		return pgconn.CommandTag{}, s.forceErr
	}
	row := evidenceRow{
		eventID:    args[0].(uuid.UUID),
		eventHash:  args[1].(string),
		chainHash:  args[2].(string),
		sourceCode: args[4].(string),
		merchantID: args[5].(uuid.UUID),
		rawPayload: args[6].([]byte),
		ingestedAt: args[7].(time.Time),
	}
	if pv, ok := args[3].(string); ok {
		row.prevHash = pv
	}
	if _, dup := s.seen[row.eventHash]; dup {
		return pgconn.CommandTag{}, &pgconn.PgError{Code: pgUniqueViolation}
	}
	s.seen[row.eventHash] = struct{}{}
	s.rows = append(s.rows, row)
	return pgconn.CommandTag{}, nil
}

type stubRow struct {
	val    string
	noRows bool
}

func (r stubRow) Scan(dest ...any) error {
	if r.noRows {
		return pgx.ErrNoRows
	}
	*dest[0].(*string) = r.val
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newEvent(merchantID uuid.UUID, eventHash string, ts time.Time) publisher.Event {
	return publisher.Event{
		EventID:    uuid.New(),
		EventHash:  eventHash,
		SourceCode: "square",
		MerchantID: merchantID,
		Timestamp:  ts,
		IngestedAt: ts,
		Payload:    json.RawMessage(`{"event":"test"}`),
		Nonce:      uuid.NewString(),
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestComputeChainHash_Deterministic(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2026-05-02T18:00:00.123456Z")
	a := ComputeChainHash("eh-1", "", ts)
	b := ComputeChainHash("eh-1", "", ts)
	if a != b {
		t.Fatalf("non-deterministic: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-hex-char SHA-256, got %d chars", len(a))
	}
}

func TestComputeChainHash_PrevHashChangesOutput(t *testing.T) {
	ts := time.Now().UTC()
	first := ComputeChainHash("eh-1", "", ts)
	second := ComputeChainHash("eh-1", first, ts)
	if first == second {
		t.Fatal("chain_hash with non-empty prev must differ from first")
	}
}

func TestComputeChainHash_DifferentEventHashes_DifferentDigests(t *testing.T) {
	ts := time.Now().UTC()
	a := ComputeChainHash("eh-1", "", ts)
	b := ComputeChainHash("eh-2", "", ts)
	if a == b {
		t.Fatal("different event_hash should produce different chain_hash")
	}
}

func TestWriteEvidence_FirstEvent_PrevIsEmpty(t *testing.T) {
	ctx := context.Background()
	db := newStubDB()
	merchantID := uuid.New()
	ts := time.Now().UTC()

	evt := newEvent(merchantID, "eh-1", ts)
	got, err := WriteEvidence(ctx, db, evt)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	want := ComputeChainHash("eh-1", "", ts)
	if got != want {
		t.Fatalf("chain_hash mismatch: got=%s want=%s", got, want)
	}
	if len(db.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(db.rows))
	}
	if db.rows[0].prevHash != "" {
		t.Fatalf("first event prev_hash should be empty, got %q", db.rows[0].prevHash)
	}
}

func TestWriteEvidence_ChainsAcrossEventsForSameMerchant(t *testing.T) {
	ctx := context.Background()
	db := newStubDB()
	merchantID := uuid.New()

	ts1 := time.Now().UTC()
	ts2 := ts1.Add(50 * time.Millisecond)
	ts3 := ts2.Add(50 * time.Millisecond)

	c1, err := WriteEvidence(ctx, db, newEvent(merchantID, "eh-1", ts1))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}
	c2, err := WriteEvidence(ctx, db, newEvent(merchantID, "eh-2", ts2))
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}
	c3, err := WriteEvidence(ctx, db, newEvent(merchantID, "eh-3", ts3))
	if err != nil {
		t.Fatalf("write 3: %v", err)
	}

	if db.rows[1].prevHash != c1 {
		t.Errorf("row 2 prev=%s want=%s", db.rows[1].prevHash, c1)
	}
	if db.rows[2].prevHash != c2 {
		t.Errorf("row 3 prev=%s want=%s", db.rows[2].prevHash, c2)
	}

	// Recompute and verify continuity end-to-end
	wantC2 := ComputeChainHash("eh-2", c1, ts2)
	wantC3 := ComputeChainHash("eh-3", c2, ts3)
	if c2 != wantC2 || c3 != wantC3 {
		t.Errorf("chain continuity broken: c2=%s want=%s ; c3=%s want=%s",
			c2, wantC2, c3, wantC3)
	}
	t.Logf("chain (same merchant): %s -> %s -> %s", c1, c2, c3)
}

func TestWriteEvidence_DuplicateEventHash_ReturnsErrDuplicate(t *testing.T) {
	ctx := context.Background()
	db := newStubDB()
	merchantID := uuid.New()
	ts := time.Now().UTC()

	first := newEvent(merchantID, "eh-dup", ts)
	if _, err := WriteEvidence(ctx, db, first); err != nil {
		t.Fatalf("first: %v", err)
	}

	dup := newEvent(merchantID, "eh-dup", ts.Add(time.Second))
	_, err := WriteEvidence(ctx, db, dup)
	if !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("expected ErrDuplicateEvent, got: %v", err)
	}
	if len(db.rows) != 1 {
		t.Fatalf("duplicate should not add a row; got %d rows", len(db.rows))
	}
}

func TestWriteEvidence_DifferentMerchants_IndependentChains(t *testing.T) {
	ctx := context.Background()
	db := newStubDB()

	mA := uuid.New()
	mB := uuid.New()
	ts := time.Now().UTC()

	cA1, err := WriteEvidence(ctx, db, newEvent(mA, "A-1", ts))
	if err != nil {
		t.Fatalf("A1: %v", err)
	}
	cB1, err := WriteEvidence(ctx, db, newEvent(mB, "B-1", ts.Add(10*time.Millisecond)))
	if err != nil {
		t.Fatalf("B1: %v", err)
	}
	cA2, err := WriteEvidence(ctx, db, newEvent(mA, "A-2", ts.Add(20*time.Millisecond)))
	if err != nil {
		t.Fatalf("A2: %v", err)
	}

	// Merchant A: A2.prev == A1.chain
	if db.rows[2].prevHash != cA1 {
		t.Errorf("merchant A: A-2 prev=%s want=%s", db.rows[2].prevHash, cA1)
	}
	// Merchant B's first event has no prev — must be empty even though
	// merchant A had already written.
	if db.rows[1].prevHash != "" {
		t.Errorf("merchant B: B-1 prev should be empty, got %q", db.rows[1].prevHash)
	}
	// A2 must NOT chain off B1
	if cA2 == ComputeChainHash("A-2", cB1, db.rows[2].ingestedAt) {
		t.Errorf("merchant A chained off merchant B — chains are not independent")
	}
}

func TestWriteEvidence_NonDuplicateExecError_PropagatesWrapped(t *testing.T) {
	ctx := context.Background()
	db := newStubDB()
	db.forceErr = errors.New("boom")
	merchantID := uuid.New()

	_, err := WriteEvidence(ctx, db, newEvent(merchantID, "eh-x", time.Now().UTC()))
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrDuplicateEvent) {
		t.Fatal("non-duplicate exec error should not be reported as duplicate")
	}
}
