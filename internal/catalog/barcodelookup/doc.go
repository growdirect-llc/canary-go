// Package barcodelookup wraps external barcode lookup providers behind a
// uniform Source contract and resolves a single best Result by fanning
// out across configured sources in parallel.
//
// # The contract
//
// Every adapter implements:
//
//	type Source interface {
//	    Name() string
//	    Lookup(ctx context.Context, barcode string) (Result, error)
//	}
//
// A Result carries:
//
//   - Source         human-readable label (e.g. "Open Food Facts")
//   - Confidence     0.0 – 1.0; higher wins in the resolver
//   - Fields         brand / name / image_url / allergens / dimensions / …
//   - PartialFields  required fields the source could not populate
//   - Latency        round-trip time to the source
//
// Confidence is calibrated per source so the Resolver can pick a winner
// when multiple sources answer:
//
//   - canarynetwork  0.99  (first-party data; Phase 3 stub)
//   - gs1            0.95  (authoritative when licensed; stub)
//   - openfoodfacts  0.85  (well-curated, food only)
//   - upcitemdb      0.65  (broad coverage, lower fidelity)
//
// # Resolver semantics
//
// Resolver.Lookup spawns one goroutine per Source, each with its own
// per-source deadline (default 2s). The whole call is bounded by an
// overall deadline (default 3s). It returns:
//
//   - The highest-confidence Result if at least one source produces one.
//   - ErrBarcodeNotFound if every source explicitly reports the barcode
//     unknown — distinct from "everything broke."
//   - ErrAllSourcesFailed if every source errored for a non-NotFound
//     reason (timeouts, transport failures, unconfigured stubs, …).
//
// A source returning ErrBarcodeNotFound is "no data, source healthy" and
// is silently excluded from the candidate set. A source returning any
// other error is logged at Warn but never aborts the resolver.
//
// # Configuring the resolver
//
//	r := barcodelookup.NewResolver(
//	    []barcodelookup.Source{
//	        openfoodfacts.New(http.DefaultClient),
//	        upcitemdb.New(http.DefaultClient),
//	    },
//	    barcodelookup.WithLogger(logger),
//	    barcodelookup.WithPerSourceTimeout(2*time.Second),
//	    barcodelookup.WithOverallTimeout(3*time.Second),
//	)
//	res, err := r.Lookup(ctx, barcode)
//
// # Out of scope (for this package)
//
// HTTP handlers, templates, and DB persistence — those land downstream
// of this package in internal/web and the relevant service stores. This
// package is pure Go and depends only on net/http, encoding/json, and
// zap for logging.
package barcodelookup
