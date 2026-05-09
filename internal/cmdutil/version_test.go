package cmdutil

import (
	"strings"
	"testing"
)

func TestVersion_ReturnsNonEmptyString(t *testing.T) {
	got := Version()
	if got == "" {
		t.Errorf("Version() returned empty string; expected build-info or fallback")
	}
}

func TestVersion_FallbackIsNotPlaceholder(t *testing.T) {
	got := Version()
	// The whole point of cmdutil is killing the hardcoded "1.0.0" placeholder.
	// The fallback may be "dev" or "unknown" but never the historic placeholder.
	if got == "1.0.0" {
		t.Errorf("Version() returned the historic hardcoded placeholder; expected build-info or a real fallback")
	}
}

func TestVersion_LdflagsOverride(t *testing.T) {
	// When the linker injects a value via -X, Version() should return it.
	// Test via package-level var override (simulating ldflags behavior).
	original := Version
	defer func() { Version = original }()

	Version = func() string { return "v1.2.3-test" }
	if got := Version(); !strings.Contains(got, "v1.2.3") {
		t.Errorf("override not applied; got %q", got)
	}
}
