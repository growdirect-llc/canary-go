package rules

import (
	"context"
	"encoding/json"

	"github.com/growdirect-llc/rapidpos/internal/chirp"
)

// NoSaleFrequencyParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "no_sale_frequency",
//	  "parameters": { "threshold_count": 5, "window_minutes": 60 } }
//
// The frequency window is whatever Store.LoadEvalContext seeded into
// EvalContext.CashierActions — defaulting to 60 minutes. The
// window_minutes parameter is currently informational; tightening it
// requires a per-rule re-fetch and is deferred to the next wave.
type NoSaleFrequencyParams struct {
	ThresholdCount int `json:"threshold_count"`
	WindowMinutes  int `json:"window_minutes"`
}

// NoSaleFrequency fires when a cashier opens the drawer with no sale
// more than ThresholdCount times in the lookback window.
type NoSaleFrequency struct{}

func (NoSaleFrequency) RuleType() string { return "no_sale_frequency" }

func (NoSaleFrequency) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p NoSaleFrequencyParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.ThresholdCount <= 0 {
		p.ThresholdCount = 5
	}

	if ec.Transaction.CashierEmployeeID == nil {
		return nil, nil
	}

	var noSaleCount int
	for _, a := range ec.CashierActions {
		// Action types are POS-vendor strings — accept either of the
		// two conventions we've seen in the legacy fox catalog.
		if a.ActionType == "no_sale" || a.ActionType == "drawer_open_no_sale" {
			noSaleCount++
		}
	}
	if noSaleCount < p.ThresholdCount {
		return nil, nil
	}
	evidence, _ := json.Marshal(map[string]any{
		"no_sale_count":   noSaleCount,
		"threshold_count": p.ThresholdCount,
		"window_minutes":  p.WindowMinutes,
	})
	signal := strengthFromRatio(float64(noSaleCount) / float64(p.ThresholdCount))
	return []chirp.MatchedDetection{{
		Severity:          rule.Severity,
		SignalStrength:    &signal,
		Evidence:          evidence,
		CashierEmployeeID: ec.Transaction.CashierEmployeeID,
	}}, nil
}
