package sub2

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
	"github.com/ruptiv/canary/internal/protocol/publisher"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// stubLookup is an AdapterLookup that returns parsers from a map keyed
// on source code.
type stubLookup struct {
	parsers map[string]Parser
}

func newStubLookup() *stubLookup { return &stubLookup{parsers: make(map[string]Parser)} }

func (l *stubLookup) Get(code string) (Parser, bool) {
	p, ok := l.parsers[code]
	return p, ok
}

// fnParser turns a function into a Parser — handy for table-driven tests.
type fnParser func(env publisher.Event) (*CanonicalEvent, error)

func (f fnParser) Parse(env publisher.Event) (*CanonicalEvent, error) { return f(env) }

// stubStore captures Persist calls and lets tests assert on them.
type stubStore struct {
	mu       sync.Mutex
	persists []*CanonicalEvent
	failWith error
}

func (s *stubStore) Persist(_ context.Context, evt *CanonicalEvent) error {
	if s.failWith != nil {
		return s.failWith
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Take a shallow copy so later mutations don't pollute the assertion.
	cp := *evt
	s.persists = append(s.persists, &cp)
	return nil
}

func (s *stubStore) snapshot() []*CanonicalEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CanonicalEvent, len(s.persists))
	copy(out, s.persists)
	return out
}

func newEnvelope(sourceCode string) publisher.Event {
	return publisher.Event{
		EventID:    uuid.New(),
		EventHash:  "test-hash",
		SourceCode: sourceCode,
		MerchantID: uuid.New(),
		Timestamp:  time.Now().UTC(),
		IngestedAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"hello":"world"}`),
		Nonce:      uuid.NewString(),
	}
}

func newCanonical(env publisher.Event) *CanonicalEvent {
	return &CanonicalEvent{
		SourceTxnID:        "TXN-001",
		SourceLocationCode: "L-001",
		Transaction: types.Transaction{
			TransactionNumber: "TXN-001",
			TransactionType:   "sale",
			BusinessDate:      env.IngestedAt,
			StartedAt:         env.IngestedAt,
			EndedAt:           env.IngestedAt,
			Status:            "completed",
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDispatcher_RoutesBySourceCode(t *testing.T) {
	lookup := newStubLookup()
	store := &stubStore{}

	squareCalls := 0
	cpCalls := 0
	lookup.parsers["square"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		squareCalls++
		return newCanonical(env), nil
	})
	lookup.parsers["counterpoint"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		cpCalls++
		return newCanonical(env), nil
	})

	d := NewDispatcher(lookup, store)
	ctx := context.Background()

	if err := d.Dispatch(ctx, newEnvelope("square")); err != nil {
		t.Fatalf("square dispatch: %v", err)
	}
	if err := d.Dispatch(ctx, newEnvelope("counterpoint")); err != nil {
		t.Fatalf("counterpoint dispatch: %v", err)
	}
	if err := d.Dispatch(ctx, newEnvelope("counterpoint")); err != nil {
		t.Fatalf("counterpoint dispatch 2: %v", err)
	}

	if squareCalls != 1 {
		t.Errorf("square parser calls = %d, want 1", squareCalls)
	}
	if cpCalls != 2 {
		t.Errorf("counterpoint parser calls = %d, want 2", cpCalls)
	}
	if got := len(store.snapshot()); got != 3 {
		t.Errorf("store persists = %d, want 3", got)
	}
}

func TestDispatcher_UnknownSource_ReturnsErrUnknownSource(t *testing.T) {
	lookup := newStubLookup() // no adapters registered
	store := &stubStore{}
	d := NewDispatcher(lookup, store)

	err := d.Dispatch(context.Background(), newEnvelope("nope"))
	if !IsUnknownSource(err) {
		t.Fatalf("want IsUnknownSource(err)==true, got err=%v", err)
	}
	if got := len(store.snapshot()); got != 0 {
		t.Errorf("unknown-source dispatch should not persist; got %d", got)
	}
}

func TestDispatcher_ParserNilNil_ReturnsErrParseDiscard(t *testing.T) {
	lookup := newStubLookup()
	store := &stubStore{}
	lookup.parsers["square"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		return nil, nil
	})
	d := NewDispatcher(lookup, store)

	err := d.Dispatch(context.Background(), newEnvelope("square"))
	if !IsParseDiscard(err) {
		t.Fatalf("want IsParseDiscard(err)==true, got err=%v", err)
	}
	if got := len(store.snapshot()); got != 0 {
		t.Errorf("discarded envelope should not persist; got %d", got)
	}
}

func TestDispatcher_ParserError_WrapsErrParseFailed(t *testing.T) {
	lookup := newStubLookup()
	store := &stubStore{}
	parserErr := errors.New("malformed payload")
	lookup.parsers["square"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		return nil, parserErr
	})
	d := NewDispatcher(lookup, store)

	err := d.Dispatch(context.Background(), newEnvelope("square"))
	if !IsParseFailed(err) {
		t.Fatalf("want IsParseFailed(err)==true, got err=%v", err)
	}
	if !errors.Is(err, parserErr) {
		t.Errorf("parser error should be in chain; got %v", err)
	}
}

func TestDispatcher_PersistError_PropagatesAsTransientError(t *testing.T) {
	lookup := newStubLookup()
	persistErr := errors.New("connection refused")
	store := &stubStore{failWith: persistErr}
	lookup.parsers["square"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		return newCanonical(env), nil
	})
	d := NewDispatcher(lookup, store)

	err := d.Dispatch(context.Background(), newEnvelope("square"))
	if err == nil {
		t.Fatal("expected persist error to propagate")
	}
	// MUST NOT be classified as parse-failed or unknown-source.
	if IsParseFailed(err) {
		t.Errorf("persist error misclassified as parse-failed")
	}
	if IsUnknownSource(err) {
		t.Errorf("persist error misclassified as unknown-source")
	}
	if IsParseDiscard(err) {
		t.Errorf("persist error misclassified as parse-discard")
	}
}

func TestDispatcher_StampsEnvelopeIDsOntoCanonical(t *testing.T) {
	lookup := newStubLookup()
	store := &stubStore{}
	// Parser leaves EventID/MerchantID/SourceCode unset — the
	// dispatcher must fill them from the envelope.
	lookup.parsers["square"] = fnParser(func(env publisher.Event) (*CanonicalEvent, error) {
		return &CanonicalEvent{
			SourceTxnID:        "TXN-X",
			SourceLocationCode: "L-X",
			Transaction:        types.Transaction{TransactionNumber: "TXN-X", TransactionType: "sale"},
		}, nil
	})
	d := NewDispatcher(lookup, store)

	env := newEnvelope("square")
	if err := d.Dispatch(context.Background(), env); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	got := store.snapshot()
	if len(got) != 1 {
		t.Fatalf("want 1 persist, got %d", len(got))
	}
	if got[0].EventID != env.EventID {
		t.Errorf("EventID stamping: got=%s want=%s", got[0].EventID, env.EventID)
	}
	if got[0].MerchantID != env.MerchantID {
		t.Errorf("MerchantID stamping: got=%s want=%s", got[0].MerchantID, env.MerchantID)
	}
	if got[0].SourceCode != env.SourceCode {
		t.Errorf("SourceCode stamping: got=%s want=%s", got[0].SourceCode, env.SourceCode)
	}
}
