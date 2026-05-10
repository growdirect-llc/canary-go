package barcodelookup

import "context"

// gs1Name is the stable label for the GS1 source.
const gs1Name = "GS1"

// GS1Source is a placeholder for the GS1 GEPIR / Verified by GS1 API.
// A real implementation requires a license + member-area credentials,
// which is out of scope for Wave C.1. Until then Lookup always returns
// ErrNotConfigured so the Resolver treats it as "not configured" rather
// than a transport failure.
type GS1Source struct{}

// NewGS1 returns the stub GS1 Source. No HTTP client is required
// because the adapter never makes a request.
func NewGS1() *GS1Source { return &GS1Source{} }

// Name returns the stable source label.
func (s *GS1Source) Name() string { return gs1Name }

// Lookup always returns ErrNotConfigured. This is a non-NotFound error
// so the Resolver logs it at Warn and excludes it from the candidate
// set; it does NOT prevent other sources from succeeding.
func (s *GS1Source) Lookup(ctx context.Context, barcode string) (Result, error) {
	_ = barcode
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{}, ErrNotConfigured
}
