// internal/chirp/rules/sweetheart.go
//
// SweetheartDeal — fires when a transaction has a discount whose
// authorized_by_employee_id matches the cashier_employee_id (cashier
// authorized their own discount), AND the discount amount exceeds a
// configurable percentage of the transaction subtotal. Classic
// LP pattern: cashier giving sweetheart prices to friends/family.
//
// Spec: GRO-764 Phase C.1 (folds part of GRO-651) +
// docs/sdds/go-handoff/chirp.md "sweetheart" pattern.

package rules

import (
	"context"
	"encoding/json"

	"github.com/growdirect-llc/rapidpos/internal/chirp"
)

// SweetheartDealParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "sweetheart_deal",
//	  "parameters": { "min_discount_pct": 20.0 } }
//
// Default: 20.0 — a discount ≥ 20% of subtotal authorized by the
// transacting cashier triggers the rule.
type SweetheartDealParams struct {
	MinDiscountPct float64 `json:"min_discount_pct"`
}

// SweetheartDeal fires when transaction.cashier_employee_id ==
// any discount.authorized_by_employee_id AND discount magnitude
// crosses MinDiscountPct of subtotal.
type SweetheartDeal struct{}

func (SweetheartDeal) RuleType() string { return "sweetheart_deal" }

func (SweetheartDeal) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p SweetheartDealParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.MinDiscountPct <= 0 {
		p.MinDiscountPct = 20.0
	}

	tx := ec.Transaction
	if tx == nil || tx.CashierEmployeeID == nil {
		return nil, nil
	}

	subtotalCents, err := numericStringToCents(tx.Subtotal)
	if err != nil || subtotalCents <= 0 {
		return nil, nil
	}

	for _, d := range ec.Discounts {
		if d.AuthorizedByEmployeeID == nil {
			continue
		}
		if *d.AuthorizedByEmployeeID != *tx.CashierEmployeeID {
			continue
		}
		discCents, err := numericStringToCents(d.Amount)
		if err != nil {
			continue
		}
		// d.Amount is positive in the schema; compute fraction of subtotal.
		pct := (float64(discCents) / float64(subtotalCents)) * 100.0
		if pct < p.MinDiscountPct {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{
			"transaction_id":               tx.ID,
			"discount_id":                  d.ID,
			"discount_type":                d.DiscountType,
			"discount_amount":              d.Amount,
			"subtotal":                     tx.Subtotal,
			"discount_pct":                 pct,
			"min_discount_pct_threshold":   p.MinDiscountPct,
			"cashier_employee_id":          *tx.CashierEmployeeID,
			"authorized_by_employee_id":    *d.AuthorizedByEmployeeID,
			"detection_pattern":            "self_authorized_discount",
		})
		signal := strengthFromRatio(pct / p.MinDiscountPct)
		return []chirp.MatchedDetection{{
			Severity:          rule.Severity,
			SignalStrength:    &signal,
			Evidence:          evidence,
			CashierEmployeeID: tx.CashierEmployeeID,
			CustomerID:        tx.CustomerID,
		}}, nil
	}
	return nil, nil
}
