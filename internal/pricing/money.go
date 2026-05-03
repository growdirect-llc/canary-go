package pricing

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// LOOP2-decision: every monetary value moves through int64 cents internally.
// Wave 1 froze numeric DB columns as Go strings (no decimal.Decimal dep
// allowed per dispatch). At read time we parse "12.3400" → 1234 cents.
// At math time we work in cents. At render time we emit "12.34". Tax
// rates are 6-decimal-place fractions (e.g. "0.082500"); they multiply
// cents and we divide-with-rounding back to cents.
//
// LOOP2-decision: rounding mode is HALF-UP (banker's "round-half-to-even"
// would be defensible, but most retail systems and Counterpoint use
// HALF-UP; we match the obvious POS convention).
//
// LOOP2-decision: no support for currencies with sub-cent precision (BHD,
// JOD, etc.) in Wave 2. USD/CAD/EUR/GBP all work; minor-unit divisor is
// fixed at 100. Multi-currency support enters in a later wave when the
// ledger module lands. Document with // SDD-vague: where the schema has
// `currency` column but resolver assumes 2dp.

// ErrAmountFormat is returned when a money string cannot be parsed.
var ErrAmountFormat = errors.New("pricing: invalid money format")

// parseMajorToCents takes "12.34" or "12.3400" or "12" or "0.50" and
// returns the value in cents (1234, 1234, 1200, 50). Negative not
// supported (no negative prices in the model). Sub-cent fractions get
// rounded HALF-UP at parse time.
func parseMajorToCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: empty", ErrAmountFormat)
	}
	if s[0] == '-' {
		return 0, fmt.Errorf("%w: negative not supported", ErrAmountFormat)
	}

	// Use big.Rat for exact decimal parsing, then round to nearest cent.
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return 0, fmt.Errorf("%w: %q", ErrAmountFormat, s)
	}
	// Multiply by 100 → cents-as-rational.
	r.Mul(r, big.NewRat(100, 1))

	// Round HALF-UP: floor(r + 1/2).
	r.Add(r, big.NewRat(1, 2))
	num := new(big.Int).Quo(r.Num(), r.Denom())
	if !num.IsInt64() {
		return 0, fmt.Errorf("%w: out of range", ErrAmountFormat)
	}
	return num.Int64(), nil
}

// parseQuantityToMicros parses a quantity string into a fixed-point
// micro-unit (qty × 1_000_000). Quantity often is fractional ("0.5" lb,
// "2.25" yards), so we need more precision than cents. Returns the
// quantity in millionths.
//
// LOOP2-decision: 6-decimal precision matches the schema's numeric(10,4)
// for uom_quantity and numeric(8,6) for tax rate. Sufficient overhead.
func parseQuantityToMicros(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: empty quantity", ErrAmountFormat)
	}
	if s[0] == '-' {
		return 0, fmt.Errorf("%w: negative quantity", ErrAmountFormat)
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return 0, fmt.Errorf("%w: %q", ErrAmountFormat, s)
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	r.Add(r, big.NewRat(1, 2))
	num := new(big.Int).Quo(r.Num(), r.Denom())
	if !num.IsInt64() {
		return 0, fmt.Errorf("%w: quantity out of range", ErrAmountFormat)
	}
	return num.Int64(), nil
}

// parseRateToPpm parses a tax rate ("0.0825") into parts-per-million
// (82500). Schema is numeric(8,6) so 6dp is exact.
func parseRateToPpm(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%w: empty rate", ErrAmountFormat)
	}
	if s[0] == '-' {
		return 0, fmt.Errorf("%w: negative rate", ErrAmountFormat)
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return 0, fmt.Errorf("%w: %q", ErrAmountFormat, s)
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	r.Add(r, big.NewRat(1, 2))
	num := new(big.Int).Quo(r.Num(), r.Denom())
	if !num.IsInt64() {
		return 0, fmt.Errorf("%w: rate out of range", ErrAmountFormat)
	}
	return num.Int64(), nil
}

// formatCents renders an int64-cents amount as a major-unit string with
// exactly two decimal places ("1234" → "12.34").
func formatCents(cents int64) string {
	if cents < 0 {
		return "-" + formatCents(-cents)
	}
	whole := cents / 100
	frac := cents % 100
	return fmt.Sprintf("%d.%02d", whole, frac)
}

// formatMicros renders a quantity in millionths back to a major-unit
// string, trimming trailing zeros but keeping at least one decimal.
// 1500000 → "1.5", 1000000 → "1", 2250000 → "2.25".
func formatMicros(micros int64) string {
	if micros < 0 {
		return "-" + formatMicros(-micros)
	}
	whole := micros / 1_000_000
	frac := micros % 1_000_000
	if frac == 0 {
		return fmt.Sprintf("%d", whole)
	}
	// Six-digit fractional part, then trim trailing zeros.
	s := fmt.Sprintf("%d.%06d", whole, frac)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// formatRatePpm renders parts-per-million back to a 6dp string ("82500"
// → "0.082500"). Always emits 6dp for round-tripping schema values.
func formatRatePpm(ppm int64) string {
	if ppm < 0 {
		return "-" + formatRatePpm(-ppm)
	}
	whole := ppm / 1_000_000
	frac := ppm % 1_000_000
	return fmt.Sprintf("%d.%06d", whole, frac)
}

// multiplyCentsByMicros computes (cents * qtyMicros / 1_000_000) with
// HALF-UP rounding back to cents. Used for unit-price × quantity →
// line-subtotal.
func multiplyCentsByMicros(cents, qtyMicros int64) int64 {
	c := big.NewInt(cents)
	q := big.NewInt(qtyMicros)
	prod := new(big.Int).Mul(c, q)
	// HALF-UP: add 500_000 then integer-divide by 1_000_000.
	prod.Add(prod, big.NewInt(500_000))
	prod.Quo(prod, big.NewInt(1_000_000))
	if !prod.IsInt64() {
		// Saturating overflow guard — return max int64 to stop math.
		// Real overflow on a single line means a misuse; the integration
		// tests catch a cart over the safe range.
		return 1<<62
	}
	return prod.Int64()
}

// multiplyCentsByPpm computes (cents * ratePpm / 1_000_000) with HALF-UP
// rounding. Used for tax (subtotal × rate → tax_amount).
func multiplyCentsByPpm(cents, ratePpm int64) int64 {
	c := big.NewInt(cents)
	r := big.NewInt(ratePpm)
	prod := new(big.Int).Mul(c, r)
	prod.Add(prod, big.NewInt(500_000))
	prod.Quo(prod, big.NewInt(1_000_000))
	if !prod.IsInt64() {
		return 1<<62
	}
	return prod.Int64()
}

// percentOff applies a "percent" qualifier (a fraction in major-unit form
// like "0.20" = 20% off) to a cents amount, returning the discount in
// cents. percent="0.20" against cents=1000 returns 200.
//
// LOOP2-decision: percent is parsed as a rate (parts-per-million-style)
// then multiplied. Same HALF-UP rounding.
func percentOff(cents int64, percentStr string) (int64, error) {
	ppm, err := parseRateToPpm(percentStr)
	if err != nil {
		return 0, err
	}
	return multiplyCentsByPpm(cents, ppm), nil
}
