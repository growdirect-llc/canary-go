// internal/chirp/suppression.go
//
// Allow-list suppression for detection rules. When the chirp engine
// evaluates a rule and an evaluator returns a matched detection, the
// engine consults the allow-list (detection.allow_list filtered by
// rule_id) before persisting. If any active entry matches the
// detection's context (cashier, reason code, etc.), the detection is
// still inserted — but with status='dismissed' and the suppression
// reason captured in attributes jsonb so the audit trail is preserved.
//
// W3 dispatch: GRO-822. Closes the W1 / GRO-814 loop — allow-list
// admin entries actually change rule behavior.

package chirp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/lp"
)

// AllowListLookup is the narrow read surface the suppression check
// needs. *lp.AllowListStore satisfies it; tests can stub.
type AllowListLookup interface {
	ListByRuleID(ctx context.Context, tenantID, ruleID uuid.UUID, limit int) ([]lp.AllowListRow, error)
}

// SuppressionReason captures why a detection was suppressed. Persisted
// to detection.detections.attributes.suppression jsonb.
type SuppressionReason struct {
	EntryID uuid.UUID `json:"entry_id"`
	Kind    string    `json:"kind"`
	Reason  string    `json:"reason,omitempty"`
}

// CheckSuppression returns a non-nil SuppressionReason if any
// active allow-list entry for the rule matches the candidate detection.
// Active = expires_at NULL or in the future (already filtered by
// AllowListStore.ListByRuleID).
//
// Match logic per pattern.kind:
//   - dead_count: pattern.cashier_id == match.CashierEmployeeID
//   - discounts/voids/comps: pattern.reason_code == match.Evidence.reason_code
//   - other kinds: never match (must be implemented by the rule type)
//
// Returns (nil, nil) when no allow-list match is found — the detection
// fires normally.
func CheckSuppression(
	ctx context.Context,
	store AllowListLookup,
	rule *Rule,
	m MatchedDetection,
) (*SuppressionReason, error) {
	if store == nil || rule == nil {
		return nil, nil
	}
	rows, err := store.ListByRuleID(ctx, rule.TenantID, rule.ID, 200)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		row := rows[i]
		var pat map[string]any
		if err := json.Unmarshal(row.Pattern, &pat); err != nil {
			continue
		}
		kind, _ := pat["kind"].(string)
		if !matchesPattern(kind, pat, m) {
			continue
		}
		reason := ""
		if row.Reason != nil {
			reason = *row.Reason
		}
		return &SuppressionReason{
			EntryID: row.ID,
			Kind:    kind,
			Reason:  reason,
		}, nil
	}
	return nil, nil
}

// matchesPattern reports whether the allow-list pattern matches the
// detection's context.
func matchesPattern(kind string, pat map[string]any, m MatchedDetection) bool {
	switch kind {
	case lp.KindDeadCount:
		cashier, _ := pat["cashier_id"].(string)
		if cashier == "" || m.CashierEmployeeID == nil {
			return false
		}
		return m.CashierEmployeeID.String() == cashier
	case lp.KindDiscounts, lp.KindVoids, lp.KindComps:
		code, _ := pat["reason_code"].(string)
		if code == "" {
			return false
		}
		evCode := evidenceReasonCode(m.Evidence)
		return evCode != "" && evCode == code
	default:
		return false
	}
}

// evidenceReasonCode pulls reason_code out of the evidence jsonb.
// Returns empty string if absent or malformed.
func evidenceReasonCode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return ""
	}
	if code, ok := ev["reason_code"].(string); ok {
		return code
	}
	return ""
}
