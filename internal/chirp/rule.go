// Package chirp implements the Canary loss-prevention rules engine —
// Module Q in the canonical spine. It loads enabled detection rules
// from q.detection_rules, evaluates them against transaction events,
// and writes matched detections to q.detections.
//
// Loop 2 Wave 2 baseline: 7 of the legacy 37-rule catalog. The rest
// land in subsequent waves once the engine shape proves out under
// load.
package chirp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
)

// Rule type aliases — keeps call sites readable when the same struct
// is used as both the persisted row and the in-memory descriptor.
type (
	// Rule is the persisted descriptor loaded from q.detection_rules.
	Rule = types.DetectionRule
	// Detection is the row written back to q.detections.
	Detection = types.Detection
	// Transaction is the source-of-truth row from t.transactions.
	Transaction = types.Transaction
	// LineItem is one row from t.transaction_line_items.
	LineItem = types.TransactionLineItem
	// CashierAction is one row from t.cashier_actions (no_sale, drawer_open, manager_override...).
	CashierAction = types.CashierAction
	// CashDrawerEvent is one row from t.cash_drawer_events.
	CashDrawerEvent = types.CashDrawerEvent
	// Discount is one row from t.transaction_discounts.
	Discount = types.TransactionDiscount
)

// EvalContext carries everything an evaluator needs for a single
// transaction event. The Store loads it once per evaluate call;
// each evaluator picks the slices it cares about.
//
// SDD-vague: chirp.md describes evaluators as having access to "the
// transaction and recent activity windows" without bounding the
// window. Loop 2 wave 2 hard-codes 60-minute windows for frequency
// rules; rule_definition.window_minutes can override per rule.
type EvalContext struct {
	TenantID    uuid.UUID
	Transaction *Transaction
	LineItems   []LineItem
	Discounts   []Discount
	// CashierActions in the cashier's recent window (windowed in Store.LoadCashierWindow).
	CashierActions []CashierAction
	// DrawerEvents on this transaction's terminal in the recent window.
	DrawerEvents []CashDrawerEvent
	// LocationOperatingHours is the JSONB blob from l.locations.operating_hours.
	LocationOperatingHours json.RawMessage
	// LocationTimezone is the IANA timezone identifier from
	// l.locations.timezone (RFC 6557 / tzdata). Drives UTC → local
	// conversion in time-of-day rules. Empty string means the store
	// timezone could not be loaded — rules that depend on it should
	// skip the transaction silently rather than emit false positives.
	LocationTimezone string
}

// MatchedDetection is what an evaluator returns: a partially-populated
// Detection row plus rule context the registry needs to fill in
// rule_id / tenant_id / source_entity_* / detected_at.
type MatchedDetection struct {
	Severity       string
	SignalStrength *string         // 0.0-1.0 numeric as string for Loop 2
	Evidence       json.RawMessage // arbitrary JSON describing the signal
	// Optional overrides — when nil/zero the evaluator has nothing more
	// specific than the source transaction.
	LocationID        *uuid.UUID
	CashierEmployeeID *uuid.UUID
	CustomerID        *uuid.UUID
}

// RuleEvaluator is the contract every rule type implements. Pure
// function — no side effects, no DB access, no time-of-day branching
// outside the EvalContext. The registry handles persistence.
type RuleEvaluator interface {
	// RuleType returns the discriminator string the evaluator handles.
	// Matched against q.detection_rules.rule_definition.rule_type.
	RuleType() string

	// Evaluate runs the rule against ctx using parameters extracted
	// from the rule's rule_definition JSONB. Returns one detection
	// per match (most rules return 0 or 1; some line-item rules can
	// return many).
	Evaluate(ctx context.Context, rule *Rule, ec *EvalContext) ([]MatchedDetection, error)
}

// ruleParams pulls the parameters object out of a rule's rule_definition
// JSONB. Convention:
//
//	{
//	  "rule_type": "void_threshold",
//	  "parameters": { "threshold_cents": 5000 }
//	}
//
// SDD-conflict: chirp.md references a flat top-level parameter shape
// per rule (e.g., {"threshold_cents": 5000}). The canonical schema
// has no opinion. Wrapping in {parameters: {...}} keeps rule_type
// and the params co-located in one column without a second JSONB.
type ruleEnvelope struct {
	RuleType   string          `json:"rule_type"`
	Parameters json.RawMessage `json:"parameters"`
}

// RuleType extracts the discriminator string from a rule's
// rule_definition. Returns ErrRuleTypeMissing if the JSONB is empty
// or doesn't contain a rule_type key.
func RuleType(rule *Rule) (string, error) {
	if len(rule.RuleDefinition) == 0 {
		return "", ErrRuleTypeMissing
	}
	var env ruleEnvelope
	if err := json.Unmarshal(rule.RuleDefinition, &env); err != nil {
		return "", fmt.Errorf("chirp: parse rule_definition for %s: %w", rule.RuleCode, err)
	}
	if env.RuleType == "" {
		return "", ErrRuleTypeMissing
	}
	return env.RuleType, nil
}

// Params unmarshals a rule's parameters block into the supplied
// destination struct. Pass &myParams.
func Params(rule *Rule, dst any) error {
	var env ruleEnvelope
	if err := json.Unmarshal(rule.RuleDefinition, &env); err != nil {
		return fmt.Errorf("chirp: parse rule_definition for %s: %w", rule.RuleCode, err)
	}
	if len(env.Parameters) == 0 {
		// Empty parameters object is fine — evaluators that don't
		// take parameters (none currently) can still be loaded.
		return nil
	}
	if err := json.Unmarshal(env.Parameters, dst); err != nil {
		return fmt.Errorf("chirp: parse parameters for %s: %w", rule.RuleCode, err)
	}
	return nil
}

// ErrRuleTypeMissing means a q.detection_rules row has no
// rule_definition.rule_type discriminator. Treated as a config error
// rather than a runtime error.
var ErrRuleTypeMissing = errors.New("chirp: rule_definition.rule_type missing")

// ErrUnknownRuleType means a rule_type was loaded that no evaluator
// is registered for. Logged as a warning and skipped, not fatal —
// new rule types may land in the DB before the matching evaluator
// is deployed.
var ErrUnknownRuleType = errors.New("chirp: no evaluator registered for rule type")
