// Package fox implements the case-management module for the Canary
// loss-prevention pipeline. It reads detections off q.detections,
// decides whether to escalate them into a q.cases row, accumulates
// evidence into q.case_evidence, and tracks investigator actions in
// q.case_actions. All three q.case_* descendant tables are append-only
// per the canonical schema (no updated_at columns) — fox issues only
// INSERTs against them; lifecycle state lives on q.cases itself.
//
// Built in GRO-761. Schema source of truth:
// deploy/schema/09_q_canary_mechanics.sql.
//
// SDD-conflict: the dispatch brief refers to "merchant_id" throughout,
// but the canonical schema uses tenant_id (q.cases.tenant_id →
// app.tenants(id)). One-merchant-per-tenant is the MVP posture per
// app.merchants.tenant_id (01_app_foundation.sql:59). Fox honors the
// schema — request bodies and query params named merchant_id are
// interpreted as tenant_id at the boundary.
package fox

import (
	"time"

	"github.com/google/uuid"
)

// CaseStatus enumerates the legal values for q.cases.status. The
// schema doesn't enforce a CHECK constraint on this column — we
// validate at the application layer instead.
type CaseStatus string

const (
	CaseStatusOpen          CaseStatus = "open"
	CaseStatusActive        CaseStatus = "active"
	CaseStatusPendingAction CaseStatus = "pending_action"
	CaseStatusResolved      CaseStatus = "resolved"
	CaseStatusClosed        CaseStatus = "closed"
	CaseStatusReopened      CaseStatus = "reopened"
)

// IsValid reports whether s is one of the recognized case statuses.
func (s CaseStatus) IsValid() bool {
	switch s {
	case CaseStatusOpen, CaseStatusActive, CaseStatusPendingAction,
		CaseStatusResolved, CaseStatusClosed, CaseStatusReopened:
		return true
	}
	return false
}

// IsTerminal reports whether s represents a closed-out lifecycle.
// Terminal cases are excluded from subject-clustering (fox won't
// re-attach new detections to a resolved case).
func (s CaseStatus) IsTerminal() bool {
	return s == CaseStatusResolved || s == CaseStatusClosed
}

// Severity enumerates the legal severity values shared between
// q.detection_rules.severity, q.detections.severity, and q.cases.severity.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// IsValid reports whether s is one of the recognized severities.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	}
	return false
}

// rank converts a severity to a comparable integer. Higher = more severe.
// Unknown severities rank as 0 so they cannot trip the escalation
// threshold by accident.
func (s Severity) rank() int {
	switch s {
	case SeverityLow:
		return 1
	case SeverityMedium:
		return 2
	case SeverityHigh:
		return 3
	case SeverityCritical:
		return 4
	}
	return 0
}

// AtLeast reports whether s is at least as severe as threshold.
func (s Severity) AtLeast(threshold Severity) bool {
	return s.rank() >= threshold.rank()
}

// EscalationDecision is the output of EvaluateForEscalation. Exactly
// one of the three booleans is true on a successful evaluation.
type EscalationDecision struct {
	// OpenNew indicates fox should open a brand-new case row and link
	// the detection to it. CaseID will be uuid.Nil; the caller mints
	// the new ID when it inserts.
	OpenNew bool

	// AttachToExisting indicates fox should attach the detection as
	// new evidence to an already-open case (subject-clustering hit).
	// CaseID is the existing case to attach to.
	AttachToExisting bool

	// NoAction indicates the detection is below the escalation
	// threshold and has no clustering match. Fox leaves it alone.
	NoAction bool

	// CaseID is meaningful only when AttachToExisting == true.
	CaseID uuid.UUID

	// Reason is a short human-readable label for the decision.
	// Useful in logs and audit trails.
	Reason string
}

// EscalationConfig governs the policy fox applies in
// EvaluateForEscalation. Defaults are conservative — medium and
// above escalates, lower severities only escalate via subject
// clustering.
type EscalationConfig struct {
	// MinSeverity is the lowest severity that triggers an automatic
	// "open new case" decision when no clustering match exists. A
	// detection below this threshold can still cause attach-to-existing
	// if it shares a subject with an open case.
	MinSeverity Severity
}

// DefaultEscalationConfig is the policy fox uses when none is
// supplied. Tuned by the canonical-data-model SDD §10 default
// of "medium" for q.detection_rules.severity.
func DefaultEscalationConfig() EscalationConfig {
	return EscalationConfig{MinSeverity: SeverityMedium}
}

// CaseNumber generates a human-readable case_number. Format:
// FOX-YYYYMMDD-<8-char-random>. Stable enough for a UNIQUE
// (tenant_id, case_number) constraint without coordination.
func CaseNumber(t time.Time) string {
	id := uuid.New().String()
	return "FOX-" + t.UTC().Format("20060102") + "-" + id[:8]
}
