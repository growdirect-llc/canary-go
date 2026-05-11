package web

import (
	"context"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
)

// BarcodeLookup is the web-layer seam over catalog barcode lookup.
// Gateway wires the concrete resolver; tests provide small stubs.
type BarcodeLookup interface {
	Lookup(ctx context.Context, barcode string) (barcodelookup.Result, error)
}
