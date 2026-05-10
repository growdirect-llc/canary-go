package barcodelookup

import (
	"context"
	"errors"
	"time"
)

// Result is the normalized output a Source produces for a barcode lookup.
//
// All fields except Source and Confidence are optional; PartialFields
// names any required fields the source could not populate so the UI can
// flag them visibly without rejecting the whole result.
type Result struct {
	// Source is a human-readable label, e.g. "Open Food Facts".
	Source string

	// Confidence is in [0.0, 1.0]; higher wins in the Resolver.
	Confidence float64

	// Fields holds the normalized payload — brand, name, description,
	// image_url, allergens, dimensions, etc. Keys are stable strings so
	// downstream callers (templates, item-store mappers) can rely on them.
	Fields map[string]any

	// PartialFields lists required fields the source couldn't fill. The
	// UI can render these as visible "missing" placeholders while still
	// using the rest of the Result.
	PartialFields []string

	// Latency is the round-trip time to the source. Adapters set this
	// directly; the Resolver fills it in if an adapter leaves it zero.
	Latency time.Duration
}

// Source is the contract every barcode-lookup adapter implements.
type Source interface {
	// Name returns a stable label for logs and metrics.
	Name() string

	// Lookup returns a Result for the given barcode or one of the
	// sentinel errors below. Implementations MUST honor ctx.
	Lookup(ctx context.Context, barcode string) (Result, error)
}

// Sentinel errors returned by adapters and the Resolver.
//
//   - ErrBarcodeNotFound  — source confirms the barcode is unknown
//     (e.g. 404 or empty result). Treated as "no data, source healthy"
//     by the Resolver and excluded from the candidate set without
//     polluting the failure path.
//   - ErrNotConfigured    — source needs credentials/config that aren't
//     wired (e.g. GS1 license).
//   - ErrNotImplemented   — placeholder adapter, not yet built.
//   - ErrAllSourcesFailed — every source errored for a non-NotFound
//     reason. Distinct from ErrBarcodeNotFound; this means "we asked
//     and everything broke," not "we asked and nobody knows."
var (
	ErrBarcodeNotFound  = errors.New("barcodelookup: barcode not found")
	ErrNotConfigured    = errors.New("barcodelookup: source not configured")
	ErrNotImplemented   = errors.New("barcodelookup: source not implemented")
	ErrAllSourcesFailed = errors.New("barcodelookup: all sources failed")
)
