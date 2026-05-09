package cmdutil

import (
	"fmt"
	"log"

	"go.uber.org/zap"
)

// loggerFactory builds the underlying *zap.Logger. Exposed as a package
// var so tests can substitute a failing factory without monkey-patching
// the zap package.
var loggerFactory = func() (*zap.Logger, error) {
	return zap.NewProduction()
}

// fatalHandler terminates the process when MustLogger cannot construct
// a logger. The default uses log.Fatalf so the cmd binary fails loud
// at startup; tests substitute a recording handler.
var fatalHandler = func(err error) {
	log.Fatalf("cmdutil.MustLogger: failed to construct logger: %v", err)
}

// MustLogger constructs a production-mode *zap.Logger. On factory
// failure the configured fatalHandler is invoked (default: log.Fatalf).
//
// Replaces the `logger, _ := zap.NewProduction()` pattern that was
// copy-pasted across ~26 cmd binaries — that pattern silently
// continued with a nil logger on failure, segfaulting on the first
// log call.
//
// Usage:
//
//	func main() {
//	    logger := cmdutil.MustLogger()
//	    defer logger.Sync()
//	    // ...
//	}
func MustLogger() *zap.Logger {
	logger, err := loggerFactory()
	if err != nil {
		fatalHandler(fmt.Errorf("zap.NewProduction: %w", err))
		return nil
	}
	return logger
}
