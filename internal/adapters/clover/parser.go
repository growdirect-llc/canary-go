// Package clover is a stub adapter that proves the substrate is open
// to a third source code. It registers as "clover" but its Parse
// method returns adapters.ErrNotImplemented — every envelope is
// dead-lettered until a real Clover parser ships.
//
// The point of including the stub in Loop 2: a green build with three
// registered adapters (square, counterpoint, clover) demonstrates the
// registry isn't accidentally Square+Counterpoint coupled. Loops 3-4
// flesh this out.
package clover

import (
	"github.com/growdirect-llc/rapidpos/internal/adapters"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub2"
)

// SourceCode is the canonical identifier for this adapter.
const SourceCode = "clover"

// Adapter is the Clover POS stub adapter.
type Adapter struct{}

// New constructs a Clover adapter.
func New() *Adapter { return &Adapter{} }

// SourceCode satisfies adapters.SourceAdapter.
func (*Adapter) SourceCode() string { return SourceCode }

// Parse always returns ErrNotImplemented. The dispatcher will
// dead-letter every Clover envelope until this method is implemented.
func (*Adapter) Parse(_ adapters.Envelope) (*sub2.CanonicalEvent, error) {
	return nil, adapters.ErrNotImplemented
}

// Compile-time check.
var _ adapters.SourceAdapter = (*Adapter)(nil)
