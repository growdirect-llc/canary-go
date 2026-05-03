// internal/workflow/l402_charge_cycle.go
//
// L402 charge cycle workflow definition + step constants. Per memory
// `project_satoshi_cost_model` — usage rolls up into satoshi totals
// per merchant per cadence step, gets priced, generates an L402-
// compatible invoice, and gets charged. Failed charges retry per
// configurable policy.
//
// Spec: GRO-765 Phase A.1 (folds part of GRO-643).
//
// L402 reference (Lightning Service Authentication Token):
//   https://docs.lightning.engineering/the-lightning-network/l402

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// L402ChargeCycle identifies the workflow in app.workflow_definitions.
const (
	L402ChargeCycleCode    = "l402_charge_cycle"
	L402ChargeCycleVersion = 1
)

// Step constants for the charge cycle. Pass these to Advance / Complete
// rather than typing strings inline.
const (
	StepUsageCollected   = "usage_collected"
	StepSatoshiPriced    = "satoshi_priced"
	StepInvoiceGenerated = "invoice_generated"
	StepL402Charged      = "l402_charged"
	StepChargeFailed     = "charge_failed"
)

// RegisterL402ChargeCycle upserts the workflow definition. Idempotent
// — safe to call from cmd/bull at boot.
func RegisterL402ChargeCycle(ctx context.Context, store *Store) (*Definition, error) {
	attrs, err := json.Marshal(map[string]any{
		"description": "L402-gated billing cycle: collect usage from " +
			"ledger.ildwac_positions, price in satoshis, generate L402 " +
			"invoice, charge via Lightning. Anchored to " +
			"ledger.blockchain_anchors on success.",
		"steps": []string{
			StepUsageCollected, StepSatoshiPriced,
			StepInvoiceGenerated, StepL402Charged, StepChargeFailed,
		},
		"reference":   "https://docs.lightning.engineering/the-lightning-network/l402",
		"sponsor":     "GRO-643 / GRO-765 Phase A.1",
		"folded_from": "memory project_satoshi_cost_model",
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: l402-charge-cycle attrs: %w", err)
	}
	return store.RegisterDefinition(
		ctx,
		L402ChargeCycleCode,
		"L402 Charge Cycle (usage → satoshis → invoice → charged)",
		L402ChargeCycleVersion,
		attrs,
	)
}
