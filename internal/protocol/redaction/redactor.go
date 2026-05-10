// Package redaction provides ingestion-time PII / payment-data
// redaction for inbound POS payloads (GRO-952).
//
// The protocol pipeline writes inbound webhook payloads to
// protocol.evidence.raw_payload, and a schema trigger blocks
// update/delete/truncate on that table. That makes anything written
// there effectively immutable. If a POS adapter forwards customer
// PII (email, phone, address), payment data (card_number, CVV),
// employee PII (SSN, DOB), or PHI (in pharma/clinic contexts), it
// becomes undeletable — at odds with right-to-forget, GDPR/CCPA
// erasure, and legal-hold workflows.
//
// This package defines the redaction substrate. It does NOT yet
// wire into the ingestion path — the wiring requires coordinated
// schema changes (separating raw_payload into a hashed-but-not-stored
// commitment + a separately-managed redacted payload) and a
// migration plan that is staged in follow-on tickets. See the
// docs/decisions/gro-952-pii-redaction-policy.md ADR for the
// full multi-phase plan.
//
// What this package provides today:
//
//   - Policy: per-source-code allowlist of fields that may be kept
//     verbatim. Anything not allowlisted is either dropped (default)
//     or replaced with a redaction sentinel (configurable).
//   - Redactor: applies a Policy to a JSON payload.
//   - DefaultDenyPolicy: the conservative posture — drop all keys
//     unless explicitly allowlisted.
//   - SquarePolicy: a starter allowlist for the Square Webhooks
//     payload shape, listing the non-PII fields the protocol
//     actually consumes (event_id, type, merchant_id, location_id,
//     created_at).
//
// Acceptance for this substrate (probes in redactor_test.go):
//
//   - PII-shaped fields (email, phone, ssn, address.line1, customer.*)
//     never appear in the redacted output.
//   - Payment-shaped fields (card_number, cvv, ccv, account_number,
//     routing_number, pan) never appear.
//   - Allowlisted fields (event_id, merchant_id, type) survive.
//   - Nested objects / arrays are walked recursively; allowlisting
//     applies path-wise (parent.child) where configured.
package redaction

import (
	"encoding/json"
	"errors"
	"strings"
)

// RedactionToken is the sentinel that replaces a denied field when
// Policy.Mode is ModeReplace. Surfaces in logs and downstream tools
// as a clear marker that the original value was withheld; chosen so
// it's not a valid UUID, email, or phone number.
const RedactionToken = "[redacted]"

// Mode controls what happens to fields not in the allowlist.
type Mode int

const (
	// ModeDrop removes denied fields from the output entirely. Default
	// — keeps the redacted payload smallest and avoids any chance the
	// sentinel itself becomes signal in downstream tooling.
	ModeDrop Mode = iota
	// ModeReplace keeps denied keys but replaces their values with
	// RedactionToken. Useful when downstream consumers expect a
	// stable schema shape and need to see "this field existed but
	// was redacted".
	ModeReplace
)

// Policy configures the redactor for one source.
//
// Allowlist matches a key path: a top-level field is "field"; a
// nested field is "parent.child"; an arbitrary array element under a
// path is "parent.*.child" — the * matches any array index.
//
// Empty Allowlist means "deny everything" — useful as a fail-safe
// fallback for unknown sources.
type Policy struct {
	// SourceCode is informational; tags errors so a misconfigured
	// adapter is identifiable.
	SourceCode string
	// Mode selects drop vs replace behavior. Default ModeDrop.
	Mode Mode
	// Allowlist enumerates key paths permitted in the output. A path
	// matches the original payload's key chain joined with ".".
	// Wildcards "*" match any single segment (typically array
	// indices). Examples:
	//   "event_id"                — top-level field
	//   "merchant.id"             — nested field
	//   "items.*.sku"             — every items[N].sku
	Allowlist []string
}

// Redactor applies a Policy to JSON payload bytes.
type Redactor struct {
	policy Policy
}

// New constructs a Redactor for the supplied Policy.
func New(p Policy) *Redactor {
	return &Redactor{policy: p}
}

// ErrInvalidJSON is returned when the supplied payload is not valid
// JSON. Callers should treat this as a 400-class input error — a
// payload that doesn't parse cannot be safely redacted.
var ErrInvalidJSON = errors.New("redaction: payload is not valid JSON")

// Redact walks the payload and returns a copy with non-allowlisted
// fields removed (Mode=Drop) or replaced with RedactionToken
// (Mode=Replace). Returns ErrInvalidJSON if the payload doesn't
// parse.
//
// Object key iteration order in the result is not stable — Go's map
// iteration is randomized. Callers MUST NOT rely on byte-equality
// with the input even when no fields are redacted.
func (r *Redactor) Redact(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return payload, nil
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, ErrInvalidJSON
	}
	out := r.walk(v, "")
	return json.Marshal(out)
}

func (r *Redactor) walk(v any, path string) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, child := range typed {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if r.allowed(childPath) {
				out[k] = r.walk(child, childPath)
			} else if r.policy.Mode == ModeReplace {
				out[k] = RedactionToken
			}
			// ModeDrop: skip the key entirely.
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			// Array elements share their parent's allow-status; the
			// allowlist matches against parent.*.field-style paths so
			// the element's own children get individually checked.
			out[i] = r.walk(child, path+".*")
		}
		return out
	default:
		// Scalar or null — return unchanged. The allow-decision happened
		// at the parent map's key.
		return v
	}
}

// allowed reports whether path may appear in the redacted output.
// Three cases qualify:
//
//  1. Exact / wildcard match against an allowlist entry — leaf is
//     explicitly listed.
//  2. The path is an ancestor of an allowlist entry — must walk into
//     it to reach the listed descendant. e.g. "data" is allowed when
//     the policy lists "data.id".
//  3. The path is a descendant of an allowlist entry that ends in a
//     wildcard or names a parent that the policy intends to keep
//     wholesale. We do NOT cover (3) here — the package contract says
//     allowlist entries should name leaves, not subtrees, so a parent
//     entry doesn't implicitly allow children. This keeps the
//     conservative-by-default posture.
func (r *Redactor) allowed(path string) bool {
	for _, allow := range r.policy.Allowlist {
		if pathMatches(allow, path) {
			return true
		}
		// Ancestor of an allowed leaf: walk in so descendants can be
		// reached. We compare segment-aware so "data" doesn't
		// accidentally match an allowlist entry of "data_id".
		if isAncestorOf(path, allow) {
			return true
		}
	}
	return false
}

// isAncestorOf reports whether path is an ancestor of allow — i.e.
// allow starts with path followed by a dot. Wildcard-aware so
// "items.*" is a recognized ancestor of "items.*.sku".
func isAncestorOf(path, allow string) bool {
	if path == "" {
		return true
	}
	pSegs := strings.Split(path, ".")
	aSegs := strings.Split(allow, ".")
	if len(pSegs) >= len(aSegs) {
		return false
	}
	for i := range pSegs {
		if pSegs[i] != "*" && aSegs[i] != "*" && pSegs[i] != aSegs[i] {
			return false
		}
	}
	return true
}

// pathMatches reports whether pattern matches path, with "*"
// matching any single path segment. Both inputs are dot-delimited.
func pathMatches(pattern, path string) bool {
	pSegs := strings.Split(pattern, ".")
	xSegs := strings.Split(path, ".")
	if len(pSegs) != len(xSegs) {
		return false
	}
	for i := range pSegs {
		if pSegs[i] != "*" && pSegs[i] != xSegs[i] {
			return false
		}
	}
	return true
}

// DefaultDenyPolicy is the conservative posture: drop everything.
// Used when an unknown source delivers a payload — the protocol
// would rather lose all signal than persist unclassified PII.
var DefaultDenyPolicy = Policy{
	SourceCode: "unknown",
	Mode:       ModeDrop,
	Allowlist:  nil,
}

// SquarePolicy is a starter allowlist for Square webhook payloads.
// Lists only the fields the canary protocol downstream actually
// consumes — event id, event type, merchant id, location id,
// timestamps. Order details, customer profiles, and payment fields
// are intentionally absent: they MUST be derived (if needed) from
// authenticated Square API calls keyed by event_id, not from the
// webhook body.
//
// This list is informed by the canary spec's Square evidence
// contract and is NOT exhaustive — engineers wiring redaction
// across Square event types should extend it with explicit review.
var SquarePolicy = Policy{
	SourceCode: "square",
	Mode:       ModeDrop,
	Allowlist: []string{
		"event_id",
		"merchant_id",
		"location_id",
		"type",
		"created_at",
		"data.id",
		"data.type",
		"data.object.id",
		// ^ Square wraps the event entity inside data.object — we keep
		// only its id so downstream can re-fetch by id without
		// persisting any of the wrapped PII.
	},
}
