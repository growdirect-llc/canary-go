package adapters

import "github.com/growdirect-llc/rapidpos/internal/protocol/sub2"

// LookupShim adapts a *Registry into the sub2.AdapterLookup interface
// without sub2 needing to import this package. Construct with NewLookup;
// pass the result into sub2.NewWorker / sub2.NewDispatcher.
type LookupShim struct {
	reg *Registry
}

// NewLookup wraps a Registry for sub2 consumption.
func NewLookup(reg *Registry) *LookupShim { return &LookupShim{reg: reg} }

// Get satisfies sub2.AdapterLookup. The underlying SourceAdapter
// already implements sub2.Parser via its Parse(env) method, so we
// can return it directly.
func (l *LookupShim) Get(sourceCode string) (sub2.Parser, bool) {
	a, ok := l.reg.Get(sourceCode)
	if !ok {
		return nil, false
	}
	return a, true
}
