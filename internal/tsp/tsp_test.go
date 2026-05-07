package tsp

import (
	"testing"
)

func TestDefaultExpectedPrevInteger(t *testing.T) {
	cases := []struct {
		current string
		want    *string
	}{
		{"100", strPtr("99")},
		{"1", strPtr("0")},
		{"0", nil},   // sequence id 0 has no predecessor
		{"abc", nil}, // non-integer
		{"", nil},
	}
	for _, c := range cases {
		got := defaultExpectedPrev(c.current)
		if (got == nil) != (c.want == nil) {
			t.Errorf("defaultExpectedPrev(%q)=%v want %v",
				c.current, prettyPtr(got), prettyPtr(c.want))
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("defaultExpectedPrev(%q)=%q want %q",
				c.current, *got, *c.want)
		}
	}
}

func TestNewSequenceLogUsesDefaultExpectedPrev(t *testing.T) {
	s := NewSequenceLog(nil)
	if s.expectedPrevFn == nil {
		t.Fatal("expected default expectedPrevFn to be set")
	}
	got := s.expectedPrevFn("42")
	if got == nil || *got != "41" {
		t.Errorf("default fn for '42' got %v, want '41'", prettyPtr(got))
	}
}

func TestWithExpectedPrevFnOverride(t *testing.T) {
	customCalled := 0
	custom := func(current string) *string {
		customCalled++
		v := "custom-prev"
		return &v
	}
	s := NewSequenceLog(nil, WithExpectedPrevFn(custom))
	got := s.expectedPrevFn("anything")
	if got == nil || *got != "custom-prev" {
		t.Errorf("custom fn: got %v, want 'custom-prev'", prettyPtr(got))
	}
	if customCalled != 1 {
		t.Errorf("custom fn calls=%d want 1", customCalled)
	}
}

func strPtr(s string) *string { return &s }
func prettyPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
