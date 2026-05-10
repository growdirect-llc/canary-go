package barcodelookup

import "context"

// canaryNetworkName is the stable label for the first-party
// Canary-network source.
const canaryNetworkName = "Canary Network"

// CanaryNetworkSource is a Phase 3 placeholder for the cross-tenant
// barcode index Canary plans to build. The shape of the call (gRPC vs
// REST, which service owns the index) is still being designed; until
// then Lookup returns ErrNotImplemented.
type CanaryNetworkSource struct{}

// NewCanaryNetwork returns the stub Canary-network Source.
func NewCanaryNetwork() *CanaryNetworkSource { return &CanaryNetworkSource{} }

// Name returns the stable source label.
func (s *CanaryNetworkSource) Name() string { return canaryNetworkName }

// Lookup always returns ErrNotImplemented. As with the GS1 stub, this
// is a non-NotFound error: the Resolver logs at Warn and excludes the
// source from the candidate set without aborting other sources.
func (s *CanaryNetworkSource) Lookup(ctx context.Context, barcode string) (Result, error) {
	_ = barcode
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{}, ErrNotImplemented
}
