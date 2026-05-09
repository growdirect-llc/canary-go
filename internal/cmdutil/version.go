// Package cmdutil holds shared utilities for cmd/* binaries.
//
// Replaces the patterns that were copy-pasted across ~26 cmd binaries:
// logger init that swallowed errors, request-logging middleware, hardcoded
// version strings in health endpoints, and ad-hoc HTTP server lifecycle
// management without graceful shutdown.
//
// Each export is intentionally small and composable. cmd binaries import
// cmdutil at top-of-main, no further wiring required.
package cmdutil

import (
	"runtime/debug"
)

// version is set at link time via `-ldflags "-X github.com/ruptiv/canary/internal/cmdutil.version=v1.2.3"`.
// Empty means "use runtime/debug.BuildInfo or fall back to dev."
var version string

// Version returns the build version string for inclusion in /health
// responses. Resolution order:
//
//  1. ldflags-injected `version` package variable (production builds)
//  2. main module version from runtime/debug.BuildInfo (go install)
//  3. the literal "dev" (go run / go test)
//
// Exposed as a package var so tests can substitute a fixed value.
var Version = func() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
