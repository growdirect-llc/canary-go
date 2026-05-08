// internal/db/types/decimal.go
//
// Canonical decimal type for Canary Go — alias for shopspring/decimal.
//
// shopspring/decimal is the open-source standard for Go decimal math
// (https://github.com/shopspring/decimal). The library handles arbitrary
// precision, deterministic rounding, and database scan/value
// implementations correctly — none of which is true of float64 or
// int64-cents-encoded-as-string, both of which earlier modules used.
//
// This package provides the canonical alias plus JSON marshal helpers
// and DB scan/value implementations. Modules that currently use
// string/int64 cents retrofit to this canonical Decimal type — see
// Brain/wiki/cards/loop3-decimal-standard.md
// for the per-module retrofit roadmap.
//
// Why a type alias instead of a wrapper struct: callers should be able
// to use decimal.NewFromInt, decimal.NewFromString, .Add, .Mul, etc.
// without juggling conversion helpers. Aliasing keeps the canonical
// import path obvious (internal/db/types.Decimal) while preserving
// the upstream library's full API.
package types

import (
	"github.com/shopspring/decimal"
)

// Decimal is the canonical decimal type for Canary Go. It is a direct
// type alias for shopspring/decimal.Decimal so callers get the upstream
// library's complete API (Add, Sub, Mul, Div, Cmp, Round, etc.) plus
// its JSON marshaling and database/sql Scan/Value implementations
// (which means pgx scan/value works automatically).
type Decimal = decimal.Decimal

// Re-exports so callers can use types.NewDecimalFromString without
// having to import shopspring/decimal directly. Reduces the
// dependency surface in non-substrate packages — they import only
// internal/db/types.

// NewDecimal constructs a Decimal from int64 + scale (number of
// decimal places). Example: NewDecimal(1234, 2) == 12.34.
func NewDecimal(value int64, exp int32) Decimal {
	return decimal.New(value, -exp)
}

// NewDecimalFromString parses a decimal string ("12.34", "0.00",
// "-1.5e3"). Returns an error on malformed input. Use this at
// system boundaries (POS adapter parsers, HTTP request parsing).
func NewDecimalFromString(s string) (Decimal, error) {
	return decimal.NewFromString(s)
}

// NewDecimalFromFloat constructs a Decimal from a float64. Use only
// when the upstream source is genuinely float (e.g., Counterpoint's
// wire format) — float64 cannot represent 0.10 exactly, so this
// loses precision relative to NewDecimalFromString.
func NewDecimalFromFloat(f float64) Decimal {
	return decimal.NewFromFloat(f)
}

// NewDecimalFromInt constructs a Decimal from int64. Lossless.
func NewDecimalFromInt(i int64) Decimal {
	return decimal.NewFromInt(i)
}

// ZeroDecimal is a pre-allocated zero — common case in initializers,
// avoids decimal.Zero ambiguity on cold reads.
var ZeroDecimal = decimal.Zero
