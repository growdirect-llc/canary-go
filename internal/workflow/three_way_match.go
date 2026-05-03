// internal/workflow/three_way_match.go
//
// Three-way-match workflow definition + step constants. The first
// real workflow registered against the Wave A app.workflow_definitions
// substrate. Spec: GRO-764 Phase B.4 (folds part of GRO-647).
//
// The workflow advances a (po_line, receipt_line, supplier_invoice_line)
// triple through these steps:
//
//   pending_match     → variance_check
//   variance_check    → approved | flagged_for_review
//   flagged_for_review → approved (operator override) | succeeded |
//                        failed (operator decline)
//
// Variance threshold is configurable per merchant via
// app.merchant_settings.three_way_match_variance_pct (default 5.00).
// Lines with variance ≤ threshold auto-approve; lines above flag for
// review. The actual evaluation logic lands when supplier_invoice
// path is wired (Wave C); this file ships the registration + step
// constants so callers can compose against them today.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// ThreeWayMatch identifies the three-way-match workflow in
// app.workflow_definitions. Stable across versions — the version
// number bumps when the step graph changes meaningfully.
const (
	ThreeWayMatchCode    = "three_way_match"
	ThreeWayMatchVersion = 1
)

// Three-way-match step constants. Pass these to Advance / Complete
// rather than typing strings inline.
const (
	StepPendingMatch     = "pending_match"
	StepVarianceCheck    = "variance_check"
	StepApproved         = "approved"
	StepFlaggedForReview = "flagged_for_review"
)

// RegisterThreeWayMatch upserts the three-way-match definition in
// app.workflow_definitions. Idempotent — safe to call from every
// service binary at boot. Returns the persisted definition.
//
// Sponsor: GRO-647 (Module T epic) + GRO-764 Phase B.4. Variance
// threshold per merchant: app.merchant_settings.three_way_match_variance_pct.
func RegisterThreeWayMatch(ctx context.Context, store *Store) (*Definition, error) {
	attrs, err := json.Marshal(map[string]any{
		"description": "PO line ↔ receipt line ↔ supplier-invoice line match per " +
			"f.supplier_invoice_lines.related_po_line_id + related_receipt_line_id.",
		"steps":                          []string{StepPendingMatch, StepVarianceCheck, StepApproved, StepFlaggedForReview},
		"variance_pct_setting":           "app.merchant_settings.three_way_match_variance_pct",
		"default_variance_pct":           5.00,
		"sponsor":                        "GRO-647 / GRO-764 Phase B.4",
		"folded_from":                    "Wave A canonical-data-model.md §F three-way-match",
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: three-way-match attrs: %w", err)
	}
	return store.RegisterDefinition(
		ctx,
		ThreeWayMatchCode,
		"Three-Way Match (PO / Receipt / Invoice)",
		ThreeWayMatchVersion,
		attrs,
	)
}
