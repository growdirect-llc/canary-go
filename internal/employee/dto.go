// internal/employee/dto.go
//
// Wire types for employee endpoints.
//
// Spec: GRO-766 Phase D.

package employee

import (
	"time"

	"github.com/google/uuid"
)

// EmployeeDTO is the wire shape for e.employees.
// Sensitive HR fields (pay rates, SSN) are never stored or exposed here.
type EmployeeDTO struct {
	ID               uuid.UUID  `json:"id"`
	TenantID         uuid.UUID  `json:"tenant_id"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	EmployeeCode     string     `json:"employee_code"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	DisplayName      *string    `json:"display_name,omitempty"`
	Email            *string    `json:"email,omitempty"`
	HireDate         time.Time  `json:"hire_date"`
	TerminationDate  *time.Time `json:"termination_date,omitempty"`
	EmploymentStatus string     `json:"employment_status"`
	PayType          *string    `json:"pay_type,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AlertSummary is a per-employee detection / alert count summary.
type AlertSummary struct {
	EmployeeID    uuid.UUID `json:"employee_id"`
	EmployeeCode  string    `json:"employee_code"`
	DisplayName   *string   `json:"display_name,omitempty"`
	TotalAlerts   int64     `json:"total_alerts"`
	NewAlerts     int64     `json:"new_alerts"`
	AckedAlerts   int64     `json:"acknowledged_alerts"`
	CriticalCount int64     `json:"critical_count"`
	HighCount     int64     `json:"high_count"`
}

// ListFilters controls the employee listing.
type ListFilters struct {
	TenantID         uuid.UUID
	EmploymentStatus string
	Search           string // fuzzy on display_name / employee_code / email
	Limit            int
	Offset           int
}
