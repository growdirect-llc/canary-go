// Package rules holds the seven baseline detection-rule
// evaluators. Each evaluator is a pure function over an EvalContext —
// no DB access, no clock reads outside the EvalContext.
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ruptiv/canary/internal/chirp"
)

// VoidThresholdParams binds the rule_definition.parameters block for
// rule_type "void_threshold".
//
//	{ "rule_type": "void_threshold", "parameters": { "threshold_cents": 5000 } }
type VoidThresholdParams struct {
	// ThresholdCents — fire when sum(voided_line_total) on the
	// transaction meets or exceeds this. Default 5000 (= $50).
	ThresholdCents int64 `json:"threshold_cents"`
}

// VoidThreshold fires when the total value of voided line items on a
// single transaction crosses a threshold.
type VoidThreshold struct{}

// RuleType implements RuleEvaluator.
func (VoidThreshold) RuleType() string { return "void_threshold" }

// Evaluate implements RuleEvaluator.
func (VoidThreshold) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p VoidThresholdParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.ThresholdCents <= 0 {
		p.ThresholdCents = 5000
	}

	var voidedCents int64
	var voidedCount int
	for _, li := range ec.LineItems {
		if !li.IsVoid {
			continue
		}
		cents, err := numericStringToCents(li.LineTotal)
		if err != nil {
			return nil, fmt.Errorf("void_threshold: parse line %d line_total %q: %w", li.LineNumber, li.LineTotal, err)
		}
		// Use absolute value — voided lines may carry negative totals
		// in some POS conventions.
		if cents < 0 {
			cents = -cents
		}
		voidedCents += cents
		voidedCount++
	}

	if voidedCents < p.ThresholdCents {
		return nil, nil
	}

	evidence, _ := json.Marshal(map[string]any{
		"voided_total_cents": voidedCents,
		"voided_count":       voidedCount,
		"threshold_cents":    p.ThresholdCents,
		"transaction_number": ec.Transaction.TransactionNumber,
	})
	signal := strengthFromRatio(float64(voidedCents) / float64(p.ThresholdCents))
	return []chirp.MatchedDetection{{
		Severity:       rule.Severity,
		SignalStrength: &signal,
		Evidence:       evidence,
	}}, nil
}

// numericStringToCents converts a numeric(14,4)-shaped string ("12.3400")
// into an integer cents value. The pgx driver currently surfaces NUMERIC
// columns as strings (Loop 2 placeholder — Loop 3 swaps in shopspring/decimal).
//
// SDD-vague: chirp.md uses "_cents" everywhere; canonical schema uses
// numeric(14,4) units. We round half-away-from-zero at the cent.
func numericStringToCents(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	// Round half-away-from-zero.
	if f >= 0 {
		return int64(f*100 + 0.5), nil
	}
	return int64(f*100 - 0.5), nil
}

// strengthFromRatio returns a Detection.signal_strength string in
// [0.000, 1.000]. Interpolates linearly with a soft cap at 1.0; values
// well above threshold all round to 1.0.
func strengthFromRatio(ratio float64) string {
	if ratio <= 0 {
		return "0.0000"
	}
	// 1.0× threshold → 0.5; 2.0× → 1.0; clip beyond.
	v := 0.5 * ratio
	if v > 1.0 {
		v = 1.0
	}
	return strconv.FormatFloat(v, 'f', 4, 64)
}
