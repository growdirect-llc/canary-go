// Package casemgmt is the dedicated case-management module — owns
// q.cases / q.case_actions / q.case_evidence as a standalone API
// surface. Wave C Phase B deliverable per GRO-765.
//
// Naming note: package is `casemgmt` (not `case`) because `case` is a
// Go reserved keyword. The HTTP path remains /v1/cases.
//
// internal/fox/ continues to own the LP-specific escalation flow and
// composes against this package for case-side reads/writes. Splitting
// off case management gives Hawk and other future consumers a clean
// API to call without inheriting fox's escalation policy.
package casemgmt

import (
	"time"

	"github.com/google/uuid"
)

// Case is the wire shape returned by reads.
type Case struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	CaseNumber          string     `json:"case_number"`
	CaseType            string     `json:"case_type"`
	Title               string     `json:"title"`
	Description         *string    `json:"description,omitempty"`
	Severity            string     `json:"severity"`
	Status              string     `json:"status"`
	PrimarySubjectID    *uuid.UUID `json:"primary_subject_id,omitempty"`
	PrimaryLocationID   *uuid.UUID `json:"primary_location_id,omitempty"`
	AssignedTo          *uuid.UUID `json:"assigned_to,omitempty"`
	OpenedAt            time.Time  `json:"opened_at"`
	ResolvedAt          *time.Time `json:"resolved_at,omitempty"`
	ResolutionType      *string    `json:"resolution_type,omitempty"`
	LossEstimated       *string    `json:"loss_amount_estimated,omitempty"`
	LossRecovered       *string    `json:"loss_amount_recovered,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateCaseRequest is the wire shape for POST /v1/cases.
type CreateCaseRequest struct {
	TenantID          uuid.UUID  `json:"tenant_id"`
	CaseNumber        string     `json:"case_number"`
	CaseType          string     `json:"case_type,omitempty"` // investigation (default) | incident | dispute | compliance_review
	Title             string     `json:"title"`
	Description       *string    `json:"description,omitempty"`
	Severity          string     `json:"severity"`
	PrimarySubjectID  *uuid.UUID `json:"primary_subject_id,omitempty"`
	PrimaryLocationID *uuid.UUID `json:"primary_location_id,omitempty"`
	AssignedTo        *uuid.UUID `json:"assigned_to,omitempty"`
	LinkDetectionID   *uuid.UUID `json:"link_detection_id,omitempty"`
}

// AppendActionRequest is the wire shape for POST /v1/cases/{id}/actions.
type AppendActionRequest struct {
	ActionType  string         `json:"action_type"`
	PerformedBy *uuid.UUID     `json:"performed_by,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// AppendEvidenceRequest is the wire shape for POST /v1/cases/{id}/evidence.
type AppendEvidenceRequest struct {
	EvidenceType     string         `json:"evidence_type"`
	SourceEntityType *string        `json:"source_entity_type,omitempty"`
	SourceEntityID   *uuid.UUID     `json:"source_entity_id,omitempty"`
	Payload          map[string]any `json:"payload"`
	CollectedBy      *uuid.UUID     `json:"collected_by,omitempty"`
}

// CloseRequest is the wire shape for POST /v1/cases/{id}/close.
type CloseRequest struct {
	ResolutionType string     `json:"resolution_type"` // substantiated | unsubstantiated | recovered | restitution | termination | no_action
	Notes          string     `json:"notes,omitempty"`
	ClosedBy       *uuid.UUID `json:"closed_by,omitempty"`
}

// CaseAction is the wire shape for q.case_actions reads.
type CaseAction struct {
	ID          uuid.UUID      `json:"id"`
	CaseID      uuid.UUID      `json:"case_id"`
	ActionType  string         `json:"action_type"`
	PerformedBy *uuid.UUID     `json:"performed_by,omitempty"`
	PerformedAt time.Time      `json:"performed_at"`
	Details     map[string]any `json:"details,omitempty"`
}

// CaseEvidence is the wire shape for q.case_evidence reads.
type CaseEvidence struct {
	ID                 uuid.UUID  `json:"id"`
	CaseID             uuid.UUID  `json:"case_id"`
	EvidenceType       string     `json:"evidence_type"`
	SourceEntityType   *string    `json:"source_entity_type,omitempty"`
	SourceEntityID     *uuid.UUID `json:"source_entity_id,omitempty"`
	PayloadHash        string     `json:"payload_hash"`
	PrevEvidenceHash   *string    `json:"prev_evidence_hash,omitempty"`
	BlockchainAnchorID *uuid.UUID `json:"blockchain_anchor_id,omitempty"`
	CollectedBy        *uuid.UUID `json:"collected_by,omitempty"`
	CollectedAt        time.Time  `json:"collected_at"`
}

// ListFilters captures filters for /v1/cases list reads.
type ListFilters struct {
	TenantID   uuid.UUID
	Status     string
	AssignedTo *uuid.UUID
	Severity   string
	Limit      int
	Offset     int
}

// ListResponse is the wire envelope for list reads.
type ListResponse struct {
	Items  []Case `json:"items"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Count  int    `json:"count"`
}
