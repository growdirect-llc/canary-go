// internal/chirp/rules/markdown_abuse.go
//
// MarkdownAbuse — fires when a return transaction's line items have
// unit_price meaningfully below the parent sale's unit_price.
// Pattern: a cashier marks down an item, sells it (or a confederate
// does), then later returns it at the original price for cash. The
// signal: the same item has divergent unit_prices across a recent
// window, and a return touches the lower-priced version.
//
// Loop 2 doesn't expose the parent transaction's line items in the
// EvalContext, so the simpler heuristic in this Wave B baseline:
// flag returns where any line_item.unit_price is meaningfully below
// the catalog list_price (m.items.list_price equivalent — read from
// line_item.list_price column when populated). When list_price is
// not populated by the source, the rule short-circuits (no false
// positives).
//
// Spec: GRO-764 Phase C.1 +
// docs/sdds/go-handoff/chirp.md "markdown abuse" pattern.

package rules

import (
	"context"
	"encoding/json"

	"github.com/ruptiv/canary/internal/chirp"
)

// MarkdownAbuseParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "markdown_abuse",
//	  "parameters": { "min_markdown_pct": 30.0 } }
//
// Default: 30.0 — a return line whose unit_price is ≥ 30% below
// the line's list_price triggers the rule.
type MarkdownAbuseParams struct {
	MinMarkdownPct float64 `json:"min_markdown_pct"`
}

// MarkdownAbuse fires on transactions of type='return' whose line
// items show meaningful divergence between unit_price and list_price.
type MarkdownAbuse struct{}

func (MarkdownAbuse) RuleType() string { return "markdown_abuse" }

func (MarkdownAbuse) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p MarkdownAbuseParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}
	if p.MinMarkdownPct <= 0 {
		p.MinMarkdownPct = 30.0
	}

	tx := ec.Transaction
	if tx == nil || tx.TransactionType != "return" {
		return nil, nil
	}

	var matches []map[string]any
	for _, li := range ec.LineItems {
		if li.ListPrice == nil {
			continue
		}
		listCents, err := numericStringToCents(*li.ListPrice)
		if err != nil || listCents <= 0 {
			continue
		}
		paidCents, err := numericStringToCents(li.UnitPrice)
		if err != nil || paidCents <= 0 {
			continue
		}
		if paidCents >= listCents {
			continue
		}
		markdownPct := (float64(listCents-paidCents) / float64(listCents)) * 100.0
		if markdownPct < p.MinMarkdownPct {
			continue
		}
		matches = append(matches, map[string]any{
			"line_item_id":   li.ID,
			"line_number":    li.LineNumber,
			"item_id":        li.ItemID,
			"unit_price":     li.UnitPrice,
			"list_price":     *li.ListPrice,
			"markdown_pct":   markdownPct,
			"description":    li.Description,
		})
	}
	if len(matches) == 0 {
		return nil, nil
	}

	evidence, _ := json.Marshal(map[string]any{
		"transaction_id":            tx.ID,
		"transaction_type":          tx.TransactionType,
		"matched_lines":             matches,
		"min_markdown_pct_threshold": p.MinMarkdownPct,
		"detection_pattern":         "return_below_list_price",
	})
	// Signal strength scales with the number of matched lines vs total.
	signal := strengthFromRatio(float64(len(matches)) / float64(max(len(ec.LineItems), 1)))
	return []chirp.MatchedDetection{{
		Severity:          rule.Severity,
		SignalStrength:    &signal,
		Evidence:          evidence,
		CashierEmployeeID: tx.CashierEmployeeID,
		CustomerID:        tx.CustomerID,
	}}, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
