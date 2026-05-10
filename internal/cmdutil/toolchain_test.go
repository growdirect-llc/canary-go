package cmdutil

import (
	"runtime"
	"strings"
	"testing"
)

// TestRuntimeGoVersion_AtLeastPatched is the GRO-934 acceptance probe.
// The compiled-in Go runtime version must be at least 1.26.3 because
// earlier patchlines carry reachable stdlib CVEs:
//
//   - GO-2026-4982 / GO-2026-4980 — html/template escape bypass (XSS).
//   - GO-2026-4918 — HTTP/2 transport infinite loop.
//   - GO-2026-4971 — net panic on Windows NUL handling.
//
// All four are fixed in Go 1.26.3.
//
// This guards against an accidental toolchain downgrade in go.mod or
// CI/builder image after the bump lands. CI should also run
// `make vulncheck`; this test is the in-suite signal.
func TestRuntimeGoVersion_AtLeastPatched(t *testing.T) {
	const minimum = "go1.26.3"

	got := runtime.Version()
	if !strings.HasPrefix(got, "go") {
		t.Fatalf("runtime.Version() returned unexpected shape %q (want go1.X.Y)", got)
	}

	// Lexicographic comparison works for go1.X.Y where X.Y has no
	// triple-digit components (true through Go 1.99). We extract the
	// "go1.X.Y" portion in case Version returns extras like
	// "go1.26.3rc1" or with a build suffix.
	gotVer := got
	if i := strings.IndexAny(got, " +-"); i >= 0 {
		gotVer = got[:i]
	}

	if gotVer < minimum {
		t.Fatalf("runtime Go version %q is older than required %q — this build is exposed to GO-2026-{4980,4982,4918,4971}; bump the toolchain in go.mod and CI builders", gotVer, minimum)
	}
}
