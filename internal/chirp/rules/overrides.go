package rules

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/chirp"
)

// ManagerOverrideFrequencyParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "manager_override_frequency",
//	  "parameters": { "threshold_count": 3, "window_minutes": 60 } }
type ManagerOverrideFrequencyParams struct {
	ThresholdCount int `json:"threshold_count"`
	WindowMinutes  int `json:"window_minutes"`
}

// ManagerOverrideFrequency fires when the same manager_employee_id
// authorizes more than ThresholdCount overrides in the lookback
// window.
//
// We count cashier_actions with action_type="manager_override" or
// "price_override" or "refund_override" carrying an
// authorized_by_employee_id, grouped by that employee.
type ManagerOverrideFrequency struct{}

func (ManagerOverrideFrequency) RuleType() string { return "manager_override_frequency" }

func (ManagerOverrideFrequency) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p ManagerOverrideFrequencyParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.ThresholdCount <= 0 {
		p.ThresholdCount = 3
	}

	// Group by authorizer.
	counts := map[uuid.UUID]int{}
	for _, a := range ec.CashierActions {
		if a.AuthorizedByEmployeeID == nil {
			continue
		}
		switch a.ActionType {
		case "manager_override", "price_override", "refund_override", "void_override":
			counts[*a.AuthorizedByEmployeeID]++
		}
	}

	var out []chirp.MatchedDetection
	for managerID, n := range counts {
		if n < p.ThresholdCount {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{
			"manager_employee_id": managerID,
			"override_count":      n,
			"threshold_count":     p.ThresholdCount,
			"window_minutes":      p.WindowMinutes,
		})
		signal := strengthFromRatio(float64(n) / float64(p.ThresholdCount))
		mid := managerID
		out = append(out, chirp.MatchedDetection{
			Severity:          rule.Severity,
			SignalStrength:    &signal,
			Evidence:          evidence,
			CashierEmployeeID: &mid, // populates the indexed cashier_employee_id slot for fast lookup
		})
	}
	return out, nil
}
