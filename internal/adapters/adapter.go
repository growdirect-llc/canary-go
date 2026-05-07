// Package adapters defines the SourceAdapter interface — the seam
// between source-specific webhook payloads and the canonical retail
// data model. Every POS integration (Square, NCR Counterpoint,
// Clover, future) implements this interface in its own subpackage:
//
//	internal/adapters/square/
//	internal/adapters/counterpoint/
//	internal/adapters/clover/
//
// The dispatcher (internal/protocol/sub2) holds a Registry and routes
// envelopes by SourceCode. No code outside an adapter subpackage may
// reference a source by name. If you find yourself writing
// `if env.SourceCode == "square"` outside internal/adapters/square/,
// stop — the substrate is supposed to make that impossible.
//
// Patent context: Application 63/991,596, Node 4 (Parse & Route).
package adapters

import (
	"errors"
	"fmt"
	"sync"

	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub2"
)

// Envelope is the canonical envelope read off the protocol:events
// stream. Aliases publisher.Event so adapter packages don't need a
// transitive import of the publisher.
type Envelope = publisher.Event

// SourceAdapter is the contract every POS adapter must satisfy. The
// substrate is intentionally minimal — Parse is the entire surface.
// Webhook authentication, retry/backoff, persistence, and dead-letter
// handling are the dispatcher's concerns, not the adapter's.
//
// SDD-vague: pos-adapter-substrate.md (lines 110-173) describes a
// far richer interface (WebhookEventTypes, PollIntervals, SeedData,
// AuthFlowHandler). Loop 2 implements only the Parse surface needed
// by Sub 2; auth/poll surfaces ship in Loops 3-4 with Hawk and Bull.
type SourceAdapter interface {
	// SourceCode returns the canonical source identifier
	// ("square", "counterpoint", "clover"). Must match the value in
	// app.source_systems.code and the SourceCode field on inbound
	// envelopes.
	SourceCode() string

	// Parse converts a raw envelope into a CanonicalEvent. Returns
	// (nil, nil) to discard the envelope cleanly (test pings,
	// unsupported but well-formed event types). Returns
	// (nil, ErrInvalidPayload) — or any error wrapping it — to
	// dead-letter the message. Any other error is treated as a
	// transient failure and the message is left un-ACKed for
	// redelivery.
	Parse(env Envelope) (*sub2.CanonicalEvent, error)
}

// Registry is the dispatcher-side adapter lookup. Construct with
// NewRegistry; populate at startup with Register; query with Get.
// Concurrent reads are safe; writes during steady-state are not
// expected and not optimized for.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]SourceAdapter
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]SourceAdapter)}
}

// Register adds a SourceAdapter to the registry. Returns an error if
// the source code is empty, the adapter is nil, or the source code
// is already registered. Re-registration is rejected loudly to surface
// startup wiring bugs immediately.
func (r *Registry) Register(a SourceAdapter) error {
	if a == nil {
		return errors.New("adapters: nil adapter")
	}
	code := a.SourceCode()
	if code == "" {
		return errors.New("adapters: empty source code")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[code]; exists {
		return fmt.Errorf("adapters: source code %q already registered", code)
	}
	r.adapters[code] = a
	return nil
}

// MustRegister panics on registration failure. Use only at startup.
func (r *Registry) MustRegister(a SourceAdapter) {
	if err := r.Register(a); err != nil {
		panic("adapters: " + err.Error())
	}
}

// Get returns the adapter for the given source code. The second
// return is false when no adapter is registered — callers should
// treat that as ErrUnknownSource.
func (r *Registry) Get(sourceCode string) (SourceAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[sourceCode]
	return a, ok
}

// Codes returns the registered source codes, alphabetized for stable
// log output. Used by the dispatcher's startup banner.
func (r *Registry) Codes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for code := range r.adapters {
		out = append(out, code)
	}
	// Tiny inline sort — no need for sort.Strings() given typical N=3-5.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ErrUnknownSource is returned when an envelope arrives for a source
// code the registry doesn't know. The dispatcher logs and ACKs these
// — they're upstream bugs, not stream issues, so retrying won't help.
var ErrUnknownSource = errors.New("adapters: unknown source code")

// ErrInvalidPayload is returned (or wrapped) when an adapter rejects a
// payload as malformed. The dispatcher dead-letters these.
var ErrInvalidPayload = errors.New("adapters: invalid payload")

// ErrNotImplemented is returned by stub adapters that register their
// source code but haven't shipped a real Parse yet (Clover today).
// The dispatcher treats it as a parse failure and dead-letters.
var ErrNotImplemented = errors.New("adapters: parser not implemented")
