package adapters

import (
	"strings"
	"testing"

	"github.com/ruptiv/canary/internal/protocol/publisher"
	"github.com/ruptiv/canary/internal/protocol/sub2"
)

// fakeAdapter is a minimal SourceAdapter for registry tests.
type fakeAdapter struct {
	code string
}

func (f *fakeAdapter) SourceCode() string { return f.code }
func (f *fakeAdapter) Parse(_ publisher.Event) (*sub2.CanonicalEvent, error) {
	return nil, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &fakeAdapter{code: "square"}
	if err := r.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Get("square")
	if !ok {
		t.Fatal("Get returned ok=false for registered code")
	}
	if got.SourceCode() != "square" {
		t.Errorf("Get returned wrong adapter: %q", got.SourceCode())
	}
}

func TestRegistry_DuplicateRegistration_Errors(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeAdapter{code: "square"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := r.Register(&fakeAdapter{code: "square"})
	if err == nil {
		t.Fatal("duplicate registration must fail")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

func TestRegistry_NilAdapter_Errors(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("nil adapter must fail registration")
	}
}

func TestRegistry_EmptyCode_Errors(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeAdapter{code: ""}); err == nil {
		t.Fatal("empty source code must fail registration")
	}
}

func TestRegistry_GetUnknown_ReturnsFalse(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get on empty registry should return ok=false")
	}
}

func TestRegistry_Codes_SortedAlphabetically(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&fakeAdapter{code: "square"})
	r.MustRegister(&fakeAdapter{code: "counterpoint"})
	r.MustRegister(&fakeAdapter{code: "clover"})

	codes := r.Codes()
	want := []string{"clover", "counterpoint", "square"}
	if len(codes) != len(want) {
		t.Fatalf("codes len = %d, want %d", len(codes), len(want))
	}
	for i, c := range codes {
		if c != want[i] {
			t.Errorf("codes[%d] = %q, want %q", i, c, want[i])
		}
	}
}

func TestLookupShim_DelegatesToRegistry(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(&fakeAdapter{code: "square"})
	shim := NewLookup(r)

	parser, ok := shim.Get("square")
	if !ok {
		t.Fatal("LookupShim.Get returned ok=false for registered code")
	}
	if parser == nil {
		t.Fatal("LookupShim.Get returned nil parser")
	}

	if _, ok := shim.Get("missing"); ok {
		t.Error("LookupShim.Get should return ok=false for unregistered code")
	}
}
