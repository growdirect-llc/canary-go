package rules

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/ruptiv/canary/internal/chirp"
)

// LargeDiscountPctParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "large_discount_pct",
//	  "parameters": { "threshold_pct": 50 } }
type LargeDiscountPctParams struct {
	// ThresholdPct — fire when a single line's discount/price ratio
	// (as percent, 0-100) crosses this. Default 50.
	ThresholdPct float64 `json:"threshold_pct"`
}

// LargeDiscountPct fires per line item whose unit_discount/unit_price
// ratio crosses the threshold. One transaction can produce N matches.
type LargeDiscountPct struct{}

func (LargeDiscountPct) RuleType() string { return "large_discount_pct" }

func (LargeDiscountPct) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p LargeDiscountPctParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.ThresholdPct <= 0 {
		p.ThresholdPct = 50.0
	}

	var out []chirp.MatchedDetection
	for _, li := range ec.LineItems {
		if li.IsVoid {
			continue
		}
		price, err := strconv.ParseFloat(li.UnitPrice, 64)
		if err != nil || price <= 0 {
			continue
		}
		disc, err := strconv.ParseFloat(li.UnitDiscount, 64)
		if err != nil || disc <= 0 {
			continue
		}
		pct := (disc / price) * 100.0
		if pct < p.ThresholdPct {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{
			"line_number":   li.LineNumber,
			"item_id":       li.ItemID,
			"unit_price":    li.UnitPrice,
			"unit_discount": li.UnitDiscount,
			"discount_pct":  pct,
			"threshold_pct": p.ThresholdPct,
			"description":   li.Description,
		})
		signal := strengthFromRatio(pct / p.ThresholdPct)
		out = append(out, chirp.MatchedDetection{
			Severity:       rule.Severity,
			SignalStrength: &signal,
			Evidence:       evidence,
		})
	}
	return out, nil
}
