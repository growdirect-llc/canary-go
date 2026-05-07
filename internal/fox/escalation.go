// internal/fox/escalation.go
package fox

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
)

// EscalationStore is the narrow surface EvaluateForEscalation needs
// from the persistence layer. The full Store implements this; tests
// stub it directly.
type EscalationStore interface {
	// FindOpenCaseBySubject returns the most recent non-terminal case
	// for the given (tenant_id, subject_id) pair, or nil if none
	// exists. Returns nil + nil when the detection has no subject.
	FindOpenCaseBySubject(ctx context.Context, tenantID, subjectID uuid.UUID) (*types.Case, error)
}

// detectionSubject extracts the most-likely subject identifier from a
// detection. The schema doesn't carry q.detections.subject_id directly
// — subjects are inferred from the cashier_employee_id or customer_id
// fields and reconciled through q.subjects.related_employee_id /
// related_customer_id in a downstream resolver. For Loop 2 we treat
// cashier_employee_id as the primary clustering key (loss-prevention
// cases are overwhelmingly employee-driven), with customer_id as the
// fallback. Returns uuid.Nil when neither is present.
//
// SDD-vague: "if detection's subject_id matches an open case's
// subject_id" — the schema has no q.detections.subject_id. Picking
// cashier_employee_id as the primary clustering key per the rule
// catalog (q.detection_rules.rule_category leans heavily on cashier
// behavior: shrink, scan_avoidance, refund_pattern, drawer_variance).
func detectionSubject(d *types.Detection) uuid.UUID {
	if d.CashierEmployeeID != nil {
		return *d.CashierEmployeeID
	}
	if d.CustomerID != nil {
		return *d.CustomerID
	}
	return uuid.Nil
}

// EvaluateForEscalation applies the escalation policy to a single
// detection and returns an EscalationDecision the caller can act on.
//
// Policy:
//
//  1. If the detection already has a case_id, return NoAction. The
//     dedup is the schema's responsibility on second look — we just
//     don't double-escalate on our side.
//  2. Compute the detection's subject (cashier or customer). If a
//     subject exists, ask the store whether an open case exists for
//     that (tenant, subject). If yes → AttachToExisting.
//  3. Otherwise, compare severity to the escalation threshold. If
//     severity ≥ threshold → OpenNew. Else → NoAction.
//
// Returns an error only on store failures (e.g., DB outage). Policy
// outcomes are never errors.
func EvaluateForEscalation(ctx context.Context, store EscalationStore, d *types.Detection, cfg EscalationConfig) (EscalationDecision, error) {
	if d == nil {
		return EscalationDecision{}, fmt.Errorf("fox.EvaluateForEscalation: nil detection")
	}
	if d.CaseID != nil {
		return EscalationDecision{
			NoAction: true,
			Reason:   "detection already linked to a case",
		}, nil
	}

	subjectID := detectionSubject(d)
	if subjectID != uuid.Nil {
		existing, err := store.FindOpenCaseBySubject(ctx, d.TenantID, subjectID)
		if err != nil {
			return EscalationDecision{}, fmt.Errorf("fox.EvaluateForEscalation: lookup open case: %w", err)
		}
		if existing != nil && !CaseStatus(existing.Status).IsTerminal() {
			return EscalationDecision{
				AttachToExisting: true,
				CaseID:           existing.ID,
				Reason:           "subject clustering hit on open case",
			}, nil
		}
	}

	sev := Severity(d.Severity)
	if sev.AtLeast(cfg.MinSeverity) {
		return EscalationDecision{
			OpenNew: true,
			Reason:  fmt.Sprintf("severity %s ≥ threshold %s", sev, cfg.MinSeverity),
		}, nil
	}

	return EscalationDecision{
		NoAction: true,
		Reason:   fmt.Sprintf("severity %s below threshold %s, no subject clustering match", sev, cfg.MinSeverity),
	}, nil
}
