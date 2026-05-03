// internal/alert/dto.go
//
// Wire types for the alert surface. Alerts are surfaced q.detections
// rows — Canary's detection = an alert to the operator. The store
// manages lifecycle transitions (new → acknowledged → dismissed/escalated).
//
// Spec: GRO-766 Phase A.

package alert

import (
	"time"

	"github.com/google/uuid"
)

// AlertDTO is the wire shape returned by alert endpoints.
type AlertDTO struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	RuleID             uuid.UUID  `json:"rule_id"`
	RuleCode           string     `json:"rule_code,omitempty"`
	RuleCategory       string     `json:"rule_category,omitempty"`
	DetectedAt         time.Time  `json:"detected_at"`
	SourceEntityType   string     `json:"source_entity_type"`
	SourceEntityID     uuid.UUID  `json:"source_entity_id"`
	LocationID         *uuid.UUID `json:"location_id,omitempty"`
	CashierEmployeeID  *uuid.UUID `json:"cashier_employee_id,omitempty"`
	CustomerID         *uuid.UUID `json:"customer_id,omitempty"`
	Severity           string     `json:"severity"`
	SignalStrength      *float64   `json:"signal_strength,omitempty"`
	Status             string     `json:"status"`
	CaseID             *uuid.UUID `json:"case_id,omitempty"`
	AcknowledgedAt     *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy     *uuid.UUID `json:"acknowledged_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// AlertStatsRow is one row of the stats aggregation.
type AlertStatsRow struct {
	RuleCategory string `json:"rule_category"`
	Severity     string `json:"severity"`
	Status       string `json:"status"`
	Count        int    `json:"count"`
}

// ListFilters parameterises the list endpoint.
type ListFilters struct {
	TenantID   uuid.UUID
	Severity   string
	Status     string
	RuleType   string // maps to rule_category on detection_rules
	LocationID *uuid.UUID
	Limit      int
	Offset     int
}

// AcknowledgeRequest is the body for POST /v1/alerts/{id}/acknowledge.
type AcknowledgeRequest struct {
	AcknowledgedBy uuid.UUID `json:"acknowledged_by"`
}

// ResolveRequest is the body for POST /v1/alerts/{id}/resolve.
type ResolveRequest struct {
	Disposition string `json:"disposition"` // dismissed | false_positive | escalated
	Note        string `json:"note,omitempty"`
}

// SuppressRequest is the body for POST /v1/alerts/{id}/suppress.
type SuppressRequest struct {
	DurationMinutes int    `json:"duration_minutes"` // 0 = indefinite
	Reason          string `json:"reason,omitempty"`
}
