package chirp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/chirp"
	"github.com/ruptiv/canary/internal/db/types"
	"github.com/ruptiv/canary/internal/lp"
)

// stubAllowList satisfies chirp.AllowListLookup for tests.
type stubAllowList struct {
	rows []lp.AllowListRow
	err  error
}

func (s *stubAllowList) ListByRuleID(_ context.Context, _, _ uuid.UUID, _ int) ([]lp.AllowListRow, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func mustPattern(t *testing.T, kind string, fields map[string]any) json.RawMessage {
	t.Helper()
	p, err := lp.NewPattern(lp.PatternTypeAllowlist, kind, fields)
	if err != nil {
		t.Fatalf("new pattern: %v", err)
	}
	return p
}

func newRule() *chirp.Rule {
	return &types.DetectionRule{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		RuleCode: "Q-DC-01",
		Severity: "high",
	}
}

func TestCheckSuppression_NoStore_ReturnsNil(t *testing.T) {
	rule := newRule()
	sup, err := chirp.CheckSuppression(context.Background(), nil, rule, chirp.MatchedDetection{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup != nil {
		t.Errorf("expected nil suppression with nil store")
	}
}

func TestCheckSuppression_DeadCount_Match(t *testing.T) {
	rule := newRule()
	cashier := uuid.New()
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, lp.KindDeadCount, map[string]any{"cashier_id": cashier.String()}),
	}}}
	m := chirp.MatchedDetection{CashierEmployeeID: &cashier}

	sup, err := chirp.CheckSuppression(context.Background(), store, rule, m)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup == nil {
		t.Fatal("expected suppression match")
	}
	if sup.Kind != lp.KindDeadCount {
		t.Errorf("kind = %q want %q", sup.Kind, lp.KindDeadCount)
	}
}

func TestCheckSuppression_DeadCount_NoMatch(t *testing.T) {
	rule := newRule()
	cashier := uuid.New()
	other := uuid.New()
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, lp.KindDeadCount, map[string]any{"cashier_id": other.String()}),
	}}}
	m := chirp.MatchedDetection{CashierEmployeeID: &cashier}

	sup, err := chirp.CheckSuppression(context.Background(), store, rule, m)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup != nil {
		t.Errorf("expected no match — different cashier, got %+v", sup)
	}
}

func TestCheckSuppression_DiscountReasonCode_Match(t *testing.T) {
	rule := newRule()
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, lp.KindDiscounts, map[string]any{"reason_code": "EMPLOYEE"}),
	}}}
	m := chirp.MatchedDetection{
		Evidence: json.RawMessage(`{"reason_code":"EMPLOYEE","amount":5}`),
	}

	sup, err := chirp.CheckSuppression(context.Background(), store, rule, m)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup == nil {
		t.Fatal("expected suppression match")
	}
}

func TestCheckSuppression_VoidReasonCode_NoMatchOnDifferentCode(t *testing.T) {
	rule := newRule()
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, lp.KindVoids, map[string]any{"reason_code": "ADM-VOID"}),
	}}}
	m := chirp.MatchedDetection{
		Evidence: json.RawMessage(`{"reason_code":"OTHER"}`),
	}

	sup, err := chirp.CheckSuppression(context.Background(), store, rule, m)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup != nil {
		t.Errorf("expected no match on different reason code")
	}
}

func TestCheckSuppression_StoreError_Propagates(t *testing.T) {
	rule := newRule()
	want := errors.New("db failure")
	store := &stubAllowList{err: want}
	_, err := chirp.CheckSuppression(context.Background(), store, rule, chirp.MatchedDetection{})
	if !errors.Is(err, want) {
		t.Errorf("expected store error, got %v", err)
	}
}

func TestCheckSuppression_ReasonField_PropagatesToSuppressionReason(t *testing.T) {
	rule := newRule()
	cashier := uuid.New()
	reason := "manager override"
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, lp.KindDeadCount, map[string]any{"cashier_id": cashier.String()}),
		Reason:  &reason,
	}}}
	m := chirp.MatchedDetection{CashierEmployeeID: &cashier}

	sup, _ := chirp.CheckSuppression(context.Background(), store, rule, m)
	if sup == nil || sup.Reason != reason {
		t.Errorf("expected reason %q, got %+v", reason, sup)
	}
}

func TestCheckSuppression_UnsupportedKind_NoMatch(t *testing.T) {
	rule := newRule()
	store := &stubAllowList{rows: []lp.AllowListRow{{
		ID:      uuid.New(),
		Pattern: mustPattern(t, "unknown_kind", map[string]any{"x": "y"}),
	}}}
	sup, err := chirp.CheckSuppression(context.Background(), store, rule, chirp.MatchedDetection{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sup != nil {
		t.Errorf("unsupported kind should not match")
	}
}

// Compile-time guard: time package referenced to keep import deterministic
var _ = time.Now
