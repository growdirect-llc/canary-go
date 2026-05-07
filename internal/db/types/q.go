// Code generated from deploy/schema/09_q_canary_mechanics.sql for Loop 2.
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// DetectionRule mirrors q.detection_rules.
type DetectionRule struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	RuleCode            string          `db:"rule_code"`
	Name                string          `db:"name"`
	Description         *string         `db:"description"`
	RuleCategory        string          `db:"rule_category"`
	RuleDefinition      json.RawMessage `db:"rule_definition"`
	Severity            string          `db:"severity"`
	Status              string          `db:"status"`
	EvaluationFrequency string          `db:"evaluation_frequency"`
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at"`
}

// Subject mirrors q.subjects.
type Subject struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	SubjectCode       string          `db:"subject_code"`
	SubjectType       string          `db:"subject_type"`
	DisplayName       string          `db:"display_name"`
	RelatedEmployeeID *uuid.UUID      `db:"related_employee_id"`
	RelatedCustomerID *uuid.UUID      `db:"related_customer_id"`
	RelatedVendorID   *uuid.UUID      `db:"related_vendor_id"`
	Description       *string         `db:"description"`
	Identifiers       json.RawMessage `db:"identifiers"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// Case mirrors q.cases.
type Case struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	CaseNumber          string          `db:"case_number"`
	CaseType            string          `db:"case_type"`
	Title               string          `db:"title"`
	Description         *string         `db:"description"`
	Severity            string          `db:"severity"`
	Status              string          `db:"status"`
	PrimarySubjectID    *uuid.UUID      `db:"primary_subject_id"`
	PrimaryLocationID   *uuid.UUID      `db:"primary_location_id"`
	AssignedTo          *uuid.UUID      `db:"assigned_to"`
	OpenedAt            time.Time       `db:"opened_at"`
	ResolvedAt          *time.Time      `db:"resolved_at"`
	ResolutionType      *string         `db:"resolution_type"`
	LossAmountEstimated *string         `db:"loss_amount_estimated"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	LossAmountRecovered *string         `db:"loss_amount_recovered"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at"`
}

// Detection mirrors q.detections. No UpdatedAt (append-only signal log).
type Detection struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	RuleID            uuid.UUID       `db:"rule_id"`
	DetectedAt        time.Time       `db:"detected_at"`
	SourceEntityType  string          `db:"source_entity_type"`
	SourceEntityID    uuid.UUID       `db:"source_entity_id"`
	LocationID        *uuid.UUID      `db:"location_id"`
	CashierEmployeeID *uuid.UUID      `db:"cashier_employee_id"`
	CustomerID        *uuid.UUID      `db:"customer_id"`
	Severity          string          `db:"severity"`
	SignalStrength    *string         `db:"signal_strength"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	Evidence          json.RawMessage `db:"evidence"`
	CaseID            *uuid.UUID      `db:"case_id"`
	Status            string          `db:"status"`
	AcknowledgedAt    *time.Time      `db:"acknowledged_at"`
	AcknowledgedBy    *uuid.UUID      `db:"acknowledged_by"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
}

// CaseEvidence mirrors q.case_evidence. No UpdatedAt (append-only with hash chain).
type CaseEvidence struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	CaseID              uuid.UUID       `db:"case_id"`
	EvidenceType        string          `db:"evidence_type"`
	SourceEntityType    *string         `db:"source_entity_type"`
	SourceEntityID      *uuid.UUID      `db:"source_entity_id"`
	Payload             json.RawMessage `db:"payload"`
	PayloadHash         string          `db:"payload_hash"`
	PrevEvidenceHash    *string         `db:"prev_evidence_hash"`
	BlockchainAnchorID  *uuid.UUID      `db:"blockchain_anchor_id"`
	CollectedBy         *uuid.UUID      `db:"collected_by"`
	CollectedAt         time.Time       `db:"collected_at"`
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
}

// CaseAction mirrors q.case_actions. No UpdatedAt (append-only state log).
type CaseAction struct {
	ID          uuid.UUID       `db:"id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	CaseID      uuid.UUID       `db:"case_id"`
	ActionType  string          `db:"action_type"`
	PerformedBy *uuid.UUID      `db:"performed_by"`
	PerformedAt time.Time       `db:"performed_at"`
	Details     json.RawMessage `db:"details"`
	CreatedAt   time.Time       `db:"created_at"`
}
