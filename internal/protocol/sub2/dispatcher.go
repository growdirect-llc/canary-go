package sub2

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/protocol/publisher"
)

// AdapterLookup is the surface the dispatcher needs from the
// adapter registry. Defined here (rather than imported from the
// adapters package) to keep this package's tests free of any
// real adapter dependency.
type AdapterLookup interface {
	Get(sourceCode string) (Parser, bool)
}

// Parser is the dispatcher-side view of a SourceAdapter. The
// adapters package's SourceAdapter satisfies this implicitly;
// adapters.RegistryShim adapts the concrete Registry to AdapterLookup.
type Parser interface {
	Parse(env publisher.Event) (*CanonicalEvent, error)
}

// errUnknownSource and errParseFailed are the dispatcher-internal
// signals routed back to the worker. They mirror the semantics of
// adapters.ErrUnknownSource/ErrInvalidPayload but are owned here so
// sub2 has no import dependency on adapters.
var (
	errUnknownSource = errors.New("sub2: unknown source code")
	errParseDiscard  = errors.New("sub2: parser discarded envelope")
	errParseFailed   = errors.New("sub2: parse failed")
)

// Dispatcher orchestrates: registry lookup, parse, persist. It is the
// piece that's actually unit-testable without Postgres or Valkey —
// callers wire it into the worker which adds streams plumbing.
type Dispatcher struct {
	lookup AdapterLookup
	store  Store
}

// NewDispatcher returns a Dispatcher configured against the given
// registry and store.
func NewDispatcher(lookup AdapterLookup, store Store) *Dispatcher {
	return &Dispatcher{lookup: lookup, store: store}
}

// Dispatch processes one envelope end-to-end. Returns:
//
//   - nil on success or graceful discard (parser returned nil/nil).
//   - errUnknownSource when no adapter is registered for env.SourceCode
//     (worker treats this as an upstream bug — log + ack, do not retry).
//   - errParseFailed (wrapping the underlying error) when the adapter
//     rejected the payload (worker dead-letters and acks).
//   - any other error from Persist (worker leaves the message un-acked
//     so Valkey redelivers).
func (d *Dispatcher) Dispatch(ctx context.Context, env publisher.Event) error {
	parser, ok := d.lookup.Get(env.SourceCode)
	if !ok {
		return errUnknownSource
	}

	canonical, err := parser.Parse(env)
	if err != nil {
		return errors.Join(errParseFailed, err)
	}
	if canonical == nil {
		// Adapter chose to discard cleanly (test ping, unsupported but
		// well-formed event). Treat as success.
		return errParseDiscard
	}

	// Adapters don't always set envelope IDs — copy them over so the
	// canonical record is self-contained before persistence.
	if canonical.EventID == uuid.Nil {
		canonical.EventID = env.EventID
	}
	if canonical.MerchantID == uuid.Nil {
		canonical.MerchantID = env.MerchantID
	}
	if canonical.SourceCode == "" {
		canonical.SourceCode = env.SourceCode
	}

	return d.store.Persist(ctx, canonical)
}

// IsUnknownSource reports whether err signals an unknown-source
// dispatch failure. Exported for the worker's error classification.
func IsUnknownSource(err error) bool { return errors.Is(err, errUnknownSource) }

// IsParseDiscard reports whether the dispatcher discarded an envelope
// because the parser returned (nil, nil) — graceful "not interested".
func IsParseDiscard(err error) bool { return errors.Is(err, errParseDiscard) }

// IsParseFailed reports whether err signals a parser-rejected payload.
// Worker dead-letters these.
func IsParseFailed(err error) bool { return errors.Is(err, errParseFailed) }
