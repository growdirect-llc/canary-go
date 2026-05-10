package barcodelookup

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubSource is a configurable Source for testing the Resolver in
// isolation. delay simulates network latency; err short-circuits to a
// canned error.
type stubSource struct {
	name       string
	confidence float64
	fields     map[string]any
	err        error
	delay      time.Duration
}

func (s *stubSource) Name() string { return s.name }

func (s *stubSource) Lookup(ctx context.Context, barcode string) (Result, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	if s.err != nil {
		return Result{}, s.err
	}
	return Result{
		Source:     s.name,
		Confidence: s.confidence,
		Fields:     s.fields,
	}, nil
}

func TestResolver_SingleSourceHappy(t *testing.T) {
	t.Parallel()

	src := &stubSource{
		name:       "stub-a",
		confidence: 0.5,
		fields:     map[string]any{"name": "Widget"},
	}
	r := NewResolver([]Source{src})

	got, err := r.Lookup(context.Background(), "012345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != "stub-a" {
		t.Errorf("Source = %q, want %q", got.Source, "stub-a")
	}
	if got.Fields["name"] != "Widget" {
		t.Errorf("Fields[name] = %v, want Widget", got.Fields["name"])
	}
}

func TestResolver_HighestConfidenceWins(t *testing.T) {
	t.Parallel()

	low := &stubSource{name: "low", confidence: 0.3, fields: map[string]any{"name": "Low"}}
	mid := &stubSource{name: "mid", confidence: 0.6, fields: map[string]any{"name": "Mid"}}
	high := &stubSource{name: "high", confidence: 0.9, fields: map[string]any{"name": "High"}}

	r := NewResolver([]Source{low, mid, high})
	got, err := r.Lookup(context.Background(), "012345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != "high" {
		t.Errorf("Source = %q, want %q", got.Source, "high")
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", got.Confidence)
	}
}

func TestResolver_NotFoundIsNotAnError(t *testing.T) {
	t.Parallel()

	missing := &stubSource{name: "missing", err: ErrBarcodeNotFound}
	hit := &stubSource{name: "hit", confidence: 0.7, fields: map[string]any{"name": "Found"}}

	r := NewResolver([]Source{missing, hit})
	got, err := r.Lookup(context.Background(), "012345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != "hit" {
		t.Errorf("Source = %q, want %q", got.Source, "hit")
	}
}

func TestResolver_AllSourcesFail(t *testing.T) {
	t.Parallel()

	bad1 := &stubSource{name: "bad1", err: errors.New("boom")}
	bad2 := &stubSource{name: "bad2", err: ErrNotConfigured}
	bad3 := &stubSource{name: "bad3", err: ErrNotImplemented}

	r := NewResolver([]Source{bad1, bad2, bad3})
	_, err := r.Lookup(context.Background(), "012345")
	if !errors.Is(err, ErrAllSourcesFailed) {
		t.Fatalf("err = %v, want ErrAllSourcesFailed", err)
	}
}

func TestResolver_AllSourcesNotFound(t *testing.T) {
	t.Parallel()

	a := &stubSource{name: "a", err: ErrBarcodeNotFound}
	b := &stubSource{name: "b", err: ErrBarcodeNotFound}
	c := &stubSource{name: "c", err: ErrBarcodeNotFound}

	r := NewResolver([]Source{a, b, c})
	_, err := r.Lookup(context.Background(), "012345")
	if !errors.Is(err, ErrBarcodeNotFound) {
		t.Fatalf("err = %v, want ErrBarcodeNotFound", err)
	}
	if errors.Is(err, ErrAllSourcesFailed) {
		t.Fatalf("err = %v, must NOT be ErrAllSourcesFailed", err)
	}
}

func TestResolver_PerSourceTimeoutDropsSlowSource(t *testing.T) {
	t.Parallel()

	slow := &stubSource{
		name:       "slow",
		confidence: 0.99,
		fields:     map[string]any{"name": "Slow"},
		delay:      500 * time.Millisecond,
	}
	fast := &stubSource{
		name:       "fast",
		confidence: 0.5,
		fields:     map[string]any{"name": "Fast"},
	}

	r := NewResolver(
		[]Source{slow, fast},
		WithPerSourceTimeout(50*time.Millisecond),
		WithOverallTimeout(2*time.Second),
	)

	got, err := r.Lookup(context.Background(), "012345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The slow source's higher confidence would otherwise win, but the
	// per-source timeout drops it from the candidate set.
	if got.Source != "fast" {
		t.Errorf("Source = %q, want %q (slow source should have been dropped)", got.Source, "fast")
	}
}

func TestResolver_EmptySourcesList(t *testing.T) {
	t.Parallel()

	r := NewResolver([]Source{})
	_, err := r.Lookup(context.Background(), "012345")
	if !errors.Is(err, ErrAllSourcesFailed) {
		t.Fatalf("err = %v, want ErrAllSourcesFailed", err)
	}
}

func TestResolver_LatencyFilledByResolverWhenAdapterLeavesItZero(t *testing.T) {
	t.Parallel()

	src := &stubSource{
		name:       "stub",
		confidence: 0.5,
		fields:     map[string]any{"name": "Widget"},
		delay:      10 * time.Millisecond,
	}
	r := NewResolver([]Source{src})

	got, err := r.Lookup(context.Background(), "012345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0 (resolver should fill when adapter leaves zero)", got.Latency)
	}
}
