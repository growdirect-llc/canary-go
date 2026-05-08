package rules

import (
	"context"
	"encoding/json"

	"github.com/ruptiv/canary/internal/chirp"
)

// RefundNoReceiptParams is empty — this rule has no tunable knobs in
// the baseline. It fires whenever a refund-type transaction
// has no parent_transaction_id link.
type RefundNoReceiptParams struct{}

// RefundNoReceipt fires when a refund transaction lacks a link to the
// original sale.
//
// SDD-conflict: chirp.md (legacy) refers to a sales.refund_links table
// that the canonical schema does not have. The canonical model
// captures the refund→original link via t.transactions.parent_transaction_id
// instead. We use that and document the divergence here.
type RefundNoReceipt struct{}

func (RefundNoReceipt) RuleType() string { return "refund_no_receipt" }

func (RefundNoReceipt) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	tx := ec.Transaction
	// Two trigger shapes:
	// 1. Header transaction_type indicates a refund.
	// 2. Any line item carries is_return=true.
	isRefundHeader := tx.TransactionType == "refund" || tx.TransactionType == "return"
	hasReturnLine := false
	for _, li := range ec.LineItems {
		if li.IsReturn {
			hasReturnLine = true
			break
		}
	}
	if !isRefundHeader && !hasReturnLine {
		return nil, nil
	}
	if tx.ParentTransactionID != nil {
		// Linked refund — not the pattern we're flagging.
		return nil, nil
	}
	evidence, _ := json.Marshal(map[string]any{
		"transaction_type":   tx.TransactionType,
		"has_return_line":    hasReturnLine,
		"grand_total":        tx.GrandTotal,
		"transaction_number": tx.TransactionNumber,
		"reason":             "no parent_transaction_id link",
	})
	signal := "0.7500"
	return []chirp.MatchedDetection{{
		Severity:       rule.Severity,
		SignalStrength: &signal,
		Evidence:       evidence,
	}}, nil
}
