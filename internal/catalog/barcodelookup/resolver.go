package barcodelookup

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

// Default timeouts tuned for the Flow A scan path: the overall budget
// is 3s (the UI shows a spinner — past 3s users perceive the lookup as
// stalled), each source gets 2s of that budget so a slow source never
// monopolizes the budget while leaving headroom for the Resolver to
// collect and pick a winner.
const (
	defaultPerSourceTimeout = 2 * time.Second
	defaultOverallTimeout   = 3 * time.Second
)

// Resolver fans out a barcode lookup to all configured Sources in
// parallel and returns the highest-confidence Result.
type Resolver struct {
	sources          []Source
	perSourceTimeout time.Duration
	overallTimeout   time.Duration
	logger           *zap.Logger
}

// ResolverOption configures a Resolver via functional options.
type ResolverOption func(*Resolver)

// WithPerSourceTimeout overrides the default 2s per-source deadline.
// Each Source goroutine is given its own context.WithTimeout(ctx, d);
// hitting it drops that source from the candidate set without aborting
// the others.
func WithPerSourceTimeout(d time.Duration) ResolverOption {
	return func(r *Resolver) { r.perSourceTimeout = d }
}

// WithOverallTimeout overrides the default 3s overall deadline. The
// Resolver returns whatever Results have arrived by this deadline; any
// goroutines still in flight are abandoned.
func WithOverallTimeout(d time.Duration) ResolverOption {
	return func(r *Resolver) { r.overallTimeout = d }
}

// WithLogger overrides the default zap.NewNop() logger. Adapter errors
// (other than ErrBarcodeNotFound) are logged at Warn level with the
// source name and the underlying error.
func WithLogger(l *zap.Logger) ResolverOption {
	return func(r *Resolver) {
		if l != nil {
			r.logger = l
		}
	}
}

// NewResolver builds a Resolver over the supplied Sources. The slice
// may be empty; in that case Lookup returns ErrAllSourcesFailed
// immediately.
func NewResolver(sources []Source, opts ...ResolverOption) *Resolver {
	r := &Resolver{
		sources:          sources,
		perSourceTimeout: defaultPerSourceTimeout,
		overallTimeout:   defaultOverallTimeout,
		logger:           zap.NewNop(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// outcome is the per-source result of a single fan-out goroutine.
type outcome struct {
	source string
	result Result
	err    error
}

// Lookup fans out to every configured Source in parallel and returns
// the highest-confidence Result. See package doc for the full contract.
//
// Error semantics:
//
//   - len(sources) == 0           → ErrAllSourcesFailed
//   - all sources NotFound        → ErrBarcodeNotFound
//   - all sources errored (other) → ErrAllSourcesFailed
//   - mix of errors + a Result    → highest-confidence Result, nil err
//
// Both per-source and overall timeouts are honored: if a source exceeds
// its per-source deadline, it's dropped; if the overall deadline fires,
// whatever Results arrived already are considered.
func (r *Resolver) Lookup(ctx context.Context, barcode string) (Result, error) {
	if len(r.sources) == 0 {
		return Result{}, ErrAllSourcesFailed
	}

	overallCtx, cancel := context.WithTimeout(ctx, r.overallTimeout)
	defer cancel()

	results := make(chan outcome, len(r.sources))
	for _, src := range r.sources {
		go func(s Source) {
			start := time.Now()
			srcCtx, srcCancel := context.WithTimeout(overallCtx, r.perSourceTimeout)
			defer srcCancel()
			res, err := s.Lookup(srcCtx, barcode)
			if err == nil && res.Latency == 0 {
				res.Latency = time.Since(start)
			}
			results <- outcome{source: s.Name(), result: res, err: err}
		}(src)
	}

	var (
		best         Result
		haveResult   bool
		notFoundSeen int
		errored      int
	)

collect:
	for i := 0; i < len(r.sources); i++ {
		select {
		case o := <-results:
			switch {
			case o.err == nil:
				if !haveResult || o.result.Confidence > best.Confidence {
					best = o.result
					haveResult = true
				}
			case errors.Is(o.err, ErrBarcodeNotFound):
				notFoundSeen++
			default:
				errored++
				r.logger.Warn("barcodelookup source failed",
					zap.String("source", o.source),
					zap.String("barcode", barcode),
					zap.Error(o.err),
				)
			}
		case <-overallCtx.Done():
			// Overall deadline fired; abandon any in-flight sources and
			// decide on whatever we have.
			break collect
		}
	}

	if haveResult {
		return best, nil
	}
	// No usable Result. Distinguish "asked, nobody knows" from "asked,
	// everything broke." If at least one source explicitly reported the
	// barcode unknown and nothing produced a real Result, surface
	// ErrBarcodeNotFound so the UI can render a helpful "we couldn't
	// find this barcode" instead of a generic failure.
	if notFoundSeen > 0 && errored == 0 {
		return Result{}, ErrBarcodeNotFound
	}
	return Result{}, ErrAllSourcesFailed
}
