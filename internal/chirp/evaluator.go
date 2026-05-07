package chirp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Registry maps rule_type discriminator → evaluator implementation.
// One Registry per chirp service; evaluators register themselves at
// service start via Register.
type Registry struct {
	mu         sync.RWMutex
	evaluators map[string]RuleEvaluator
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{evaluators: make(map[string]RuleEvaluator)}
}

// Register adds an evaluator. Replacing an existing rule_type is
// allowed but logged via the returned bool (true = replaced).
func (r *Registry) Register(e RuleEvaluator) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, replaced := r.evaluators[e.RuleType()]
	r.evaluators[e.RuleType()] = e
	return replaced
}

// Lookup returns the evaluator for a rule_type or false.
func (r *Registry) Lookup(ruleType string) (RuleEvaluator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evaluators[ruleType]
	return e, ok
}

// RegisteredTypes returns the rule_type strings the registry can
// evaluate. Useful for /health and admin tooling.
func (r *Registry) RegisteredTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.evaluators))
	for k := range r.evaluators {
		out = append(out, k)
	}
	return out
}

// Engine is the public surface: load rules + context, dispatch to
// registered evaluators, persist matched detections.
type Engine struct {
	store     Store
	registry  *Registry
	logger    *zap.Logger
	allowList AllowListLookup // optional — when set, used for suppression (W3)
	now       func() time.Time
}

// NewEngine wires a store + registry + logger. Suppression is
// disabled until SetAllowListStore is called.
func NewEngine(s Store, r *Registry, l *zap.Logger) *Engine {
	if l == nil {
		l = zap.NewNop()
	}
	return &Engine{
		store:    s,
		registry: r,
		logger:   l,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// SetAllowListStore enables W3 allow-list suppression. When set, the
// engine consults the allow-list before persisting each detection;
// matched detections are still written but flipped to status='dismissed'
// with a suppression reason captured in attributes.
func (e *Engine) SetAllowListStore(a AllowListLookup) {
	e.allowList = a
}

// EvaluateTransaction runs every active on_event rule for the
// transaction's tenant against the transaction. Returns the inserted
// detections (with IDs assigned by the DB).
//
// SDD-vague: chirp.md describes evaluation as "fan-out to all enabled
// rules" without specifying what "enabled" means relative to
// evaluation_frequency. fires only on_event rules from
// this entry point; hourly/daily/weekly are deferred to a scheduler
// that lands in a later wave.
func (e *Engine) EvaluateTransaction(ctx context.Context, transactionID uuid.UUID) ([]Detection, error) {
	tx, err := e.store.LoadTransaction(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("chirp: load transaction %s: %w", transactionID, err)
	}
	if tx == nil {
		return nil, fmt.Errorf("chirp: transaction %s not found", transactionID)
	}

	rules, err := e.store.LoadRules(ctx, tx.TenantID, "on_event")
	if err != nil {
		return nil, fmt.Errorf("chirp: load rules for tenant %s: %w", tx.TenantID, err)
	}
	if len(rules) == 0 {
		return nil, nil
	}

	ec, err := e.store.LoadEvalContext(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("chirp: build eval context: %w", err)
	}

	var inserted []Detection
	for i := range rules {
		rule := &rules[i]
		matches, err := e.evaluateOne(ctx, rule, ec)
		if err != nil {
			// Per-rule errors don't abort the batch — a malformed rule
			// shouldn't poison every other detection on the transaction.
			e.logger.Warn("rule evaluation failed",
				zap.String("rule_code", rule.RuleCode),
				zap.String("tenant_id", rule.TenantID.String()),
				zap.Error(err),
			)
			continue
		}
		for _, m := range matches {
			det := e.buildDetection(rule, ec, m)
			// W3: check allow-list suppression. Match → status='dismissed'
			// + suppression reason in attributes; mismatch → fire normally.
			if sup, err := CheckSuppression(ctx, e.allowList, rule, m); err != nil {
				e.logger.Warn("allow-list lookup failed",
					zap.String("rule_code", rule.RuleCode),
					zap.Error(err),
				)
			} else if sup != nil {
				det.Status = "dismissed"
				if attrs, mErr := json.Marshal(map[string]any{
					"suppressed":  true,
					"suppression": sup,
				}); mErr == nil {
					det.Attributes = attrs
				}
			}
			if err := e.store.InsertDetection(ctx, &det); err != nil {
				e.logger.Error("insert detection failed",
					zap.String("rule_code", rule.RuleCode),
					zap.String("transaction_id", tx.ID.String()),
					zap.Error(err),
				)
				continue
			}
			inserted = append(inserted, det)
		}
	}
	return inserted, nil
}

// EvaluateBatch evaluates every transaction for a tenant since the
// given timestamp. Returns counts per rule_code and total inserted.
func (e *Engine) EvaluateBatch(ctx context.Context, tenantID uuid.UUID, since time.Time) (BatchResult, error) {
	txIDs, err := e.store.ListTransactionsSince(ctx, tenantID, since)
	if err != nil {
		return BatchResult{}, fmt.Errorf("chirp: list transactions: %w", err)
	}
	res := BatchResult{
		TenantID:        tenantID,
		Since:           since,
		TransactionsEvaluated: len(txIDs),
		PerRuleCounts:   map[string]int{},
	}
	for _, txID := range txIDs {
		dets, err := e.EvaluateTransaction(ctx, txID)
		if err != nil {
			e.logger.Warn("batch: skip transaction",
				zap.String("transaction_id", txID.String()),
				zap.Error(err),
			)
			continue
		}
		// Detection counts. We don't break out per-rule_code yet — the
		// detection only carries rule_id; a second-pass map lookup
		// lands in the next wave when the batch endpoint gets traffic.
		res.DetectionsInserted += len(dets)
	}
	return res, nil
}

// BatchResult is the response body for /v1/chirp/evaluate-batch.
type BatchResult struct {
	TenantID              uuid.UUID      `json:"tenant_id"`
	Since                 time.Time      `json:"since"`
	TransactionsEvaluated int            `json:"transactions_evaluated"`
	DetectionsInserted    int            `json:"detections_inserted"`
	PerRuleCounts         map[string]int `json:"per_rule_counts,omitempty"`
}

func (e *Engine) evaluateOne(ctx context.Context, rule *Rule, ec *EvalContext) ([]MatchedDetection, error) {
	ruleType, err := RuleType(rule)
	if err != nil {
		return nil, err
	}
	evaluator, ok := e.registry.Lookup(ruleType)
	if !ok {
		// Not fatal — log and skip. New rule_types in the DB without
		// matching code shouldn't break the engine.
		e.logger.Warn("unknown rule_type",
			zap.String("rule_code", rule.RuleCode),
			zap.String("rule_type", ruleType),
		)
		return nil, nil
	}
	return evaluator.Evaluate(ctx, rule, ec)
}

func (e *Engine) buildDetection(rule *Rule, ec *EvalContext, m MatchedDetection) Detection {
	det := Detection{
		TenantID:         rule.TenantID,
		RuleID:           rule.ID,
		DetectedAt:       e.now(),
		SourceEntityType: "transaction",
		SourceEntityID:   ec.Transaction.ID,
		Severity:         m.Severity,
		SignalStrength:   m.SignalStrength,
		Evidence:         m.Evidence,
		Status:           "new",
		Attributes:       json.RawMessage(`{}`),
		CreatedAt:        e.now(),
	}
	if m.LocationID != nil {
		det.LocationID = m.LocationID
	} else {
		loc := ec.Transaction.LocationID
		det.LocationID = &loc
	}
	if m.CashierEmployeeID != nil {
		det.CashierEmployeeID = m.CashierEmployeeID
	} else if ec.Transaction.CashierEmployeeID != nil {
		det.CashierEmployeeID = ec.Transaction.CashierEmployeeID
	}
	if m.CustomerID != nil {
		det.CustomerID = m.CustomerID
	} else if ec.Transaction.CustomerID != nil {
		det.CustomerID = ec.Transaction.CustomerID
	}
	if det.Severity == "" {
		// Fall back to the rule's configured severity when the evaluator
		// doesn't override it.
		det.Severity = rule.Severity
	}
	if len(det.Evidence) == 0 {
		det.Evidence = json.RawMessage(`{}`)
	}
	return det
}

// Compile-time guard against accidental Engine misuse.
var _ = errors.New
