package workflow

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPgAdvisoryKeyDeterministic(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	k1a, k2a := pgAdvisoryKey(id)
	k1b, k2b := pgAdvisoryKey(id)
	if k1a != k1b || k2a != k2b {
		t.Errorf("hash not deterministic: %d/%d vs %d/%d", k1a, k2a, k1b, k2b)
	}
}

func TestPgAdvisoryKeyDistinctPerID(t *testing.T) {
	a := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	b := uuid.MustParse("11111111-2222-3333-4444-555555555556")
	k1a, k2a := pgAdvisoryKey(a)
	k1b, k2b := pgAdvisoryKey(b)
	if k1a == k1b && k2a == k2b {
		t.Errorf("different uuids hashed to identical keys: (%d,%d)", k1a, k2a)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := map[string]bool{
		StatusPending:   false,
		StatusRunning:   false,
		StatusSucceeded: true,
		StatusFailed:    true,
		StatusCancelled: true,
		"unknown":       false,
	}
	for status, want := range cases {
		if got := isTerminal(status); got != want {
			t.Errorf("isTerminal(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestErrorSentinels(t *testing.T) {
	// Make sure errors.Is works against wrapped sentinels — the
	// handler-level renderStoreError pattern relies on it.
	wrapped := errors.Join(ErrInvalidTransition, errors.New("downstream"))
	if !errors.Is(wrapped, ErrInvalidTransition) {
		t.Error("ErrInvalidTransition should match through errors.Join")
	}
}
