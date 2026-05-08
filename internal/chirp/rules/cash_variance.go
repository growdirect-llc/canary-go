package rules

import (
	"context"
	"encoding/json"
	"math"

	"github.com/ruptiv/canary/internal/chirp"
)

// CashDrawerVarianceParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "cash_drawer_variance",
//	  "parameters": { "threshold_cents": 500 } }
//
// 500 cents = $5.00 default. Uses the GENERATED variance column on
// t.cash_drawer_events; we never recompute it client-side.
type CashDrawerVarianceParams struct {
	ThresholdCents int64 `json:"threshold_cents"`
}

// CashDrawerVariance fires when any drawer event in the lookback
// window has |variance| crossing the threshold.
type CashDrawerVariance struct{}

func (CashDrawerVariance) RuleType() string { return "cash_drawer_variance" }

func (CashDrawerVariance) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p CashDrawerVarianceParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.ThresholdCents <= 0 {
		p.ThresholdCents = 500 // $5
	}

	var out []chirp.MatchedDetection
	for _, e := range ec.DrawerEvents {
		if e.Variance == nil {
			continue
		}
		cents, err := numericStringToCents(*e.Variance)
		if err != nil {
			continue
		}
		abs := cents
		if abs < 0 {
			abs = -abs
		}
		if abs < p.ThresholdCents {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{
			"drawer_event_id":  e.ID,
			"event_type":       e.EventType,
			"variance_cents":   cents,
			"threshold_cents":  p.ThresholdCents,
			"event_at":         e.EventAt,
			"pos_terminal_id":  e.POSTerminalID,
		})
		signal := strengthFromRatio(math.Abs(float64(cents)) / float64(p.ThresholdCents))
		out = append(out, chirp.MatchedDetection{
			Severity:          rule.Severity,
			SignalStrength:    &signal,
			Evidence:          evidence,
			CashierEmployeeID: e.CashierEmployeeID,
		})
	}
	return out, nil
}
