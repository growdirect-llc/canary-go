// internal/workflow/investigation_lifecycle.go
//
// Investigation lifecycle workflow definition + step constants. Spec:
//
// Steps: opened → investigating → review_pending → closed{disposition}.
// SLA tracking happens at the q.cases column level (sla_* columns
// land in a future schema patch); this workflow gives the substrate
// callers wire against today.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// InvestigationLifecycle identifies the workflow.
const (
	InvestigationLifecycleCode    = "investigation_lifecycle"
	InvestigationLifecycleVersion = 1
)

// Step constants.
const (
	InvStepOpened         = "opened"
	InvStepInvestigating  = "investigating"
	InvStepReviewPending  = "review_pending"
	InvStepClosed         = "closed"
)

// RegisterInvestigationLifecycle upserts the workflow definition.
// Idempotent — safe to call from cmd/case at boot.
func RegisterInvestigationLifecycle(ctx context.Context, store *Store) (*Definition, error) {
	attrs, err := json.Marshal(map[string]any{
		"description": "Case investigation lifecycle. Steps: opened → " +
			"investigating → review_pending → closed{disposition}. " +
			"Disposition mirrors q.cases.resolution_type values.",
		"steps": []string{
			InvStepOpened, InvStepInvestigating,
			InvStepReviewPending, InvStepClosed,
		},
		"resolution_types": []string{
			"substantiated", "unsubstantiated", "recovered",
			"restitution", "termination", "no_action",
		},
		"sponsor":     "GRO-644 / GRO-765 Phase B.3",
		"folded_from": "Wave A canonical-data-model.md §10 q-schema",
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: investigation-lifecycle attrs: %w", err)
	}
	return store.RegisterDefinition(
		ctx,
		InvestigationLifecycleCode,
		"Investigation Lifecycle",
		InvestigationLifecycleVersion,
		attrs,
	)
}
