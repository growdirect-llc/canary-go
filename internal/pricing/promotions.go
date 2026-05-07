package pricing

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// promoEvalContext is the per-line context passed to the eligibility
// evaluator. Built once per resolve call (location, customer, channel)
// and reused across every line.
type promoEvalContext struct {
	channel          string
	customerID       *uuid.UUID
	customerSegments []string
}

// evaluatePromotion decides whether a single promotion applies to a given
// line item, and computes the per-unit discount in cents.
//
// Returns (applies, perUnitDiscountCents, benefitType, err).
//
// SDD-vague: §8 lists eight promotion_type values
// (percent_off | amount_off | bogo | x_for_y | tier_threshold | bundle |
//  fixed_price | loyalty_member_price). Wave 2 implements three:
//   - percent_off  → benefit_qualifier.percent (e.g. "0.20")
//   - amount_off   → benefit_qualifier.amount  (e.g. "5.00")
//   - fixed_price  → benefit_qualifier.fixed_price (sets unit price)
// Others return (false, ...) — they need richer cart-context evaluation
// (BOGO needs to look at line collection; tier_threshold needs cart total)
// which is Wave 3+.
func evaluatePromotion(
	promo types.Promotion,
	rules []types.PromotionRule,
	itemID uuid.UUID,
	itemCategoryID *uuid.UUID,
	basePriceCents int64,
	qtyMicros int64,
	ctx promoEvalContext,
) (bool, int64, string, error) {
	// Channel filter: empty active_channels means "all channels".
	if len(promo.ActiveChannels) > 0 && !contains(promo.ActiveChannels, ctx.channel) {
		return false, 0, "", nil
	}

	// Customer-segment filter. customer_segments NULL or empty = no filter.
	if len(promo.CustomerSegments) > 0 {
		if !anyOverlap(promo.CustomerSegments, ctx.customerSegments) {
			return false, 0, "", nil
		}
	}

	// SDD-vague: active_hours JSONB has no firm schema.
	// LOOP2-decision: ignored in Wave 2 — pretend promos run 24h on
	// active_days.

	// Scope filter — the simplest case is scope_type='item' which means
	// "applies to specific items listed in the trigger_qualifier". For
	// scope_type='merchandise_total' or 'category' the rule list determines
	// applicability — Wave 2 supports 'item' fully, 'category' if the
	// category_id matches.
	switch promo.ScopeType {
	case "item":
		if !itemMatchesAnyRule(rules, itemID) {
			return false, 0, "", nil
		}
	case "category":
		if itemCategoryID == nil || !categoryMatchesAnyRule(rules, *itemCategoryID) {
			return false, 0, "", nil
		}
	default:
		// SDD-vague: merchandise_total / brand / tender / customer_segment
		// scopes need broader cart context. Skip in Wave 2.
		return false, 0, "", nil
	}

	// Trigger evaluation — Wave 2 supports buy_quantity (min_quantity).
	// Other triggers (spend_amount, scan_coupon, match_basket) need
	// cart-wide context.
	for _, r := range rules {
		switch r.TriggerType {
		case "buy_quantity":
			minQ, ok := jsonNumberMicros(r.TriggerQualifier, "min_quantity")
			if !ok {
				minQ = 1_000_000 // default 1 unit
			}
			if qtyMicros < minQ {
				return false, 0, "", nil
			}
		case "":
			// no trigger — always-on rule
		default:
			// SDD-vague: spend_amount requires cart total (computed in
			// resolver, not here). Wave 2 skips.
			continue
		}
	}

	// Benefit evaluation — pull the first benefit row.
	if len(rules) == 0 {
		return false, 0, "", nil
	}
	r := rules[0]
	switch r.BenefitType {
	case "percent_off":
		pct, ok := jsonString(r.BenefitQualifier, "percent")
		if !ok {
			return false, 0, "", nil
		}
		discount, err := percentOff(basePriceCents, pct)
		if err != nil {
			return false, 0, "", err
		}
		return true, discount, r.BenefitType, nil

	case "amount_off":
		amt, ok := jsonString(r.BenefitQualifier, "amount")
		if !ok {
			return false, 0, "", nil
		}
		discountCents, err := parseMajorToCents(amt)
		if err != nil {
			return false, 0, "", err
		}
		// Cap discount at base price to prevent negative unit price.
		if discountCents > basePriceCents {
			discountCents = basePriceCents
		}
		return true, discountCents, r.BenefitType, nil

	case "fixed_price":
		fp, ok := jsonString(r.BenefitQualifier, "fixed_price")
		if !ok {
			return false, 0, "", nil
		}
		fixedCents, err := parseMajorToCents(fp)
		if err != nil {
			return false, 0, "", err
		}
		// Discount = base - fixed (clamp at 0).
		discountCents := basePriceCents - fixedCents
		if discountCents < 0 {
			discountCents = 0
		}
		return true, discountCents, r.BenefitType, nil

	default:
		// SDD-vague: bogo / free_item / tier_unlock not implemented.
		return false, 0, "", nil
	}
}

// pickBestPromotions returns the set of promotions to actually apply to a
// line. Logic:
//   - Group all eligible promos into stackable + non-stackable.
//   - Pick the single non-stackable promo with the largest discount.
//   - Add every stackable promo on top.
//   - If only stackables exist, return them all.
//   - If only non-stackables, return the largest single one.
//
// SDD-vague: the schema has `stackable boolean` and `exclusive_with uuid[]`
// but no canonical statement about how to pick when both stackable and
// non-stackable promos compete. LOOP2-decision: largest-discount
// non-stackable wins, then stackables layer on. Document and revisit when
// merchant feedback arrives.
//
// Also: exclusive_with is read but not enforced in Wave 2 — would need
// pairwise compatibility check across the chosen set.
func pickBestPromotions(candidates []evaluatedPromo) []evaluatedPromo {
	if len(candidates) == 0 {
		return nil
	}
	var stackables []evaluatedPromo
	var bestNonStackable *evaluatedPromo
	for i := range candidates {
		c := &candidates[i]
		if c.promo.Stackable {
			stackables = append(stackables, *c)
			continue
		}
		if bestNonStackable == nil || c.discountCents > bestNonStackable.discountCents {
			bestNonStackable = c
		}
	}
	out := stackables
	if bestNonStackable != nil {
		out = append(out, *bestNonStackable)
	}
	return out
}

// evaluatedPromo carries the matched promo + its computed discount.
type evaluatedPromo struct {
	promo          types.Promotion
	discountCents  int64
	benefitType    string
}

// --- helpers ---

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func anyOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// itemMatchesAnyRule returns true if any rule's trigger_qualifier.item_ids
// list contains the given itemID, or any rule has no item_ids restriction
// (which means all items).
func itemMatchesAnyRule(rules []types.PromotionRule, itemID uuid.UUID) bool {
	if len(rules) == 0 {
		return false
	}
	for _, r := range rules {
		ids, present := jsonUUIDList(r.TriggerQualifier, "item_ids")
		if !present || len(ids) == 0 {
			// No restriction → matches all
			return true
		}
		for _, id := range ids {
			if id == itemID {
				return true
			}
		}
	}
	return false
}

func categoryMatchesAnyRule(rules []types.PromotionRule, categoryID uuid.UUID) bool {
	if len(rules) == 0 {
		return false
	}
	for _, r := range rules {
		ids, present := jsonUUIDList(r.TriggerQualifier, "category_ids")
		if !present || len(ids) == 0 {
			return true
		}
		for _, id := range ids {
			if id == categoryID {
				return true
			}
		}
	}
	return false
}

// jsonString pulls a string field out of a JSONB blob.
func jsonString(raw json.RawMessage, key string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		// Allow "amount": 5.00 (JSON number) — render as string for the
		// money parser.
		s := strings.TrimRight(strings.TrimRight(formatFloat(x), "0"), ".")
		if s == "" {
			s = "0"
		}
		return s, true
	}
	return "", false
}

// jsonNumberMicros extracts a numeric value (as quantity micros).
func jsonNumberMicros(raw json.RawMessage, key string) (int64, bool) {
	s, ok := jsonString(raw, key)
	if !ok {
		return 0, false
	}
	v, err := parseQuantityToMicros(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// jsonUUIDList extracts a string-array field and parses each element as
// UUID. Returns (list, true-if-key-present).
func jsonUUIDList(raw json.RawMessage, key string) ([]uuid.UUID, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, true // present but wrong type → treat as empty/match-all
	}
	var out []uuid.UUID
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			continue
		}
		id, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, true
}

// formatFloat renders a JSON-number cleanly. fmt %g works fine for our
// domain (no scientific output for prices under $1B).
func formatFloat(f float64) string {
	// Use %.6f then trim — keeps deterministic precision.
	return trimFloat6(f)
}

func trimFloat6(f float64) string {
	// Inline because the file uses fmt elsewhere via the strings import.
	return strings.TrimRight(strings.TrimRight(sprintfFloat(f), "0"), ".")
}

// sprintfFloat is a tiny helper so we don't import fmt here just for
// formatting JSON-number-as-string.
func sprintfFloat(f float64) string {
	// 6 decimal places is enough — qualifiers are money or percent, both
	// well within 6dp.
	const digits = 6
	whole := int64(f)
	frac := f - float64(whole)
	if frac < 0 {
		frac = -frac
	}
	scaled := int64(frac*1e6 + 0.5)
	if scaled == 0 {
		return itoa(whole)
	}
	return itoa(whole) + "." + leftPad6(scaled)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func leftPad6(n int64) string {
	s := itoa(n)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}
