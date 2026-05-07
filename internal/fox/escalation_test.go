// internal/fox/escalation_test.go
package fox

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// stubStore implements EscalationStore for unit tests. It returns a
// pre-canned case for any subject in lookups, or nil if Lookups is
// empty.
type stubStore struct {
	Lookups   map[uuid.UUID]*types.Case
	LookupErr error
	Calls     int
}

func (s *stubStore) FindOpenCaseBySubject(_ context.Context, tenantID, subjectID uuid.UUID) (*types.Case, error) {
	s.Calls++
	if s.LookupErr != nil {
		return nil, s.LookupErr
	}
	c, ok := s.Lookups[subjectID]
	if !ok {
		return nil, nil
	}
	// Sanity: stubbed case must match tenant or the test is misconfigured.
	if c.TenantID != tenantID {
		return nil, nil
	}
	return c, nil
}

func newDetection(severity string, cashier *uuid.UUID, customer *uuid.UUID) *types.Detection {
	tenantID := uuid.New()
	return &types.Detection{
		ID:                uuid.New(),
		TenantID:          tenantID,
		RuleID:            uuid.New(),
		SourceEntityType:  "transaction",
		SourceEntityID:    uuid.New(),
		Severity:          severity,
		Status:            "new",
		CashierEmployeeID: cashier,
		CustomerID:        customer,
	}
}

func TestEvaluate_AlreadyLinked_NoAction(t *testing.T) {
	d := newDetection("high", nil, nil)
	caseID := uuid.New()
	d.CaseID = &caseID
	dec, err := EvaluateForEscalation(context.Background(), &stubStore{}, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dec.NoAction {
		t.Fatalf("expected NoAction, got %+v", dec)
	}
}

func TestEvaluate_HighSeverity_NoSubject_OpensNew(t *testing.T) {
	d := newDetection("high", nil, nil)
	dec, err := EvaluateForEscalation(context.Background(), &stubStore{}, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dec.OpenNew {
		t.Fatalf("expected OpenNew, got %+v", dec)
	}
}

func TestEvaluate_LowSeverity_NoSubject_NoAction(t *testing.T) {
	d := newDetection("low", nil, nil)
	dec, err := EvaluateForEscalation(context.Background(), &stubStore{}, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dec.NoAction {
		t.Fatalf("expected NoAction, got %+v", dec)
	}
}

func TestEvaluate_LowSeverity_WithClusterHit_AttachToExisting(t *testing.T) {
	cashier := uuid.New()
	d := newDetection("low", &cashier, nil)
	existing := &types.Case{
		ID:               uuid.New(),
		TenantID:         d.TenantID,
		Status:           "open",
		PrimarySubjectID: &cashier,
	}
	store := &stubStore{Lookups: map[uuid.UUID]*types.Case{cashier: existing}}
	dec, err := EvaluateForEscalation(context.Background(), store, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dec.AttachToExisting {
		t.Fatalf("expected AttachToExisting, got %+v", dec)
	}
	if dec.CaseID != existing.ID {
		t.Fatalf("CaseID: got %s want %s", dec.CaseID, existing.ID)
	}
}

func TestEvaluate_HighSeverity_WithClusterHit_AttachWins(t *testing.T) {
	// Subject clustering takes precedence over severity-driven
	// new-case opening: the investigator is already on this subject,
	// piling new cases on top would fragment the investigation.
	cashier := uuid.New()
	d := newDetection("critical", &cashier, nil)
	existing := &types.Case{
		ID:               uuid.New(),
		TenantID:         d.TenantID,
		Status:           "active",
		PrimarySubjectID: &cashier,
	}
	store := &stubStore{Lookups: map[uuid.UUID]*types.Case{cashier: existing}}
	dec, err := EvaluateForEscalation(context.Background(), store, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !dec.AttachToExisting {
		t.Fatalf("expected AttachToExisting, got %+v", dec)
	}
}

func TestEvaluate_TerminalCase_NotReused(t *testing.T) {
	// A resolved/closed case must not absorb fresh detections — the
	// case needs to be reopened deliberately, not silently grown.
	cashier := uuid.New()
	d := newDetection("medium", &cashier, nil)
	closed := &types.Case{
		ID:               uuid.New(),
		TenantID:         d.TenantID,
		Status:           "closed",
		PrimarySubjectID: &cashier,
	}
	store := &stubStore{Lookups: map[uuid.UUID]*types.Case{cashier: closed}}
	dec, err := EvaluateForEscalation(context.Background(), store, d, DefaultEscalationConfig())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dec.AttachToExisting {
		t.Fatalf("expected new case (terminal cluster ignored), got attach")
	}
	if !dec.OpenNew {
		t.Fatalf("expected OpenNew, got %+v", dec)
	}
}

func TestEvaluate_StoreError_Propagates(t *testing.T) {
	cashier := uuid.New()
	d := newDetection("medium", &cashier, nil)
	store := &stubStore{LookupErr: errors.New("db down")}
	_, err := EvaluateForEscalation(context.Background(), store, d, DefaultEscalationConfig())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestEvaluate_NilDetection_Errors(t *testing.T) {
	_, err := EvaluateForEscalation(context.Background(), &stubStore{}, nil, DefaultEscalationConfig())
	if err == nil {
		t.Fatalf("expected error on nil detection")
	}
}

func TestSeverity_AtLeast(t *testing.T) {
	cases := []struct {
		got, threshold Severity
		want           bool
	}{
		{SeverityLow, SeverityLow, true},
		{SeverityLow, SeverityMedium, false},
		{SeverityMedium, SeverityMedium, true},
		{SeverityHigh, SeverityMedium, true},
		{SeverityCritical, SeverityHigh, true},
		{SeverityCritical, SeverityCritical, true},
		{Severity("garbage"), SeverityLow, false},
	}
	for _, c := range cases {
		if got := c.got.AtLeast(c.threshold); got != c.want {
			t.Errorf("%s.AtLeast(%s) = %v, want %v", c.got, c.threshold, got, c.want)
		}
	}
}

func TestCaseStatus_IsTerminal(t *testing.T) {
	if !CaseStatusClosed.IsTerminal() {
		t.Error("closed should be terminal")
	}
	if !CaseStatusResolved.IsTerminal() {
		t.Error("resolved should be terminal")
	}
	if CaseStatusOpen.IsTerminal() {
		t.Error("open should not be terminal")
	}
	if CaseStatusReopened.IsTerminal() {
		t.Error("reopened should not be terminal")
	}
}

func TestCaseNumber_FormatStable(t *testing.T) {
	t1 := newDetection("low", nil, nil).DetectedAt
	_ = t1
	// Just exercise the formatting path; the exact value depends on uuid.New().
	cn := CaseNumber(t1)
	if len(cn) < len("FOX-YYYYMMDD-XXXXXXXX") {
		t.Errorf("case number too short: %q", cn)
	}
	if cn[:4] != "FOX-" {
		t.Errorf("case number missing prefix: %q", cn)
	}
}

func TestSubjectFromDetection_PrefersCashier(t *testing.T) {
	cashier := uuid.New()
	customer := uuid.New()
	d := &types.Detection{CashierEmployeeID: &cashier, CustomerID: &customer}
	got := detectionSubject(d)
	if got != cashier {
		t.Errorf("got %s, want cashier %s", got, cashier)
	}
}

func TestSubjectFromDetection_FallsBackToCustomer(t *testing.T) {
	customer := uuid.New()
	d := &types.Detection{CustomerID: &customer}
	got := detectionSubject(d)
	if got != customer {
		t.Errorf("got %s, want customer %s", got, customer)
	}
}

func TestSubjectFromDetection_NilWhenAbsent(t *testing.T) {
	d := &types.Detection{}
	got := detectionSubject(d)
	if got != uuid.Nil {
		t.Errorf("got %s, want Nil", got)
	}
}
