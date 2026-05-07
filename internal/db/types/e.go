// Code generated from deploy/schema/04_c_e_customers_employees.sql for Loop 2.
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Employee mirrors e.employees.
type Employee struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	UserID           *uuid.UUID      `db:"user_id"`
	EmployeeCode     string          `db:"employee_code"`
	FirstName        string          `db:"first_name"`
	LastName         string          `db:"last_name"`
	DisplayName      *string         `db:"display_name"`
	Email            *string         `db:"email"`
	Phone            *string         `db:"phone"`
	HireDate         time.Time       `db:"hire_date"`
	TerminationDate  *time.Time      `db:"termination_date"`
	EmploymentStatus string          `db:"employment_status"`
	PayType          *string         `db:"pay_type"`
	Attributes       json.RawMessage `db:"attributes"`
	ExternalIDs      json.RawMessage `db:"external_ids"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// EmployeeRoleAssignment mirrors e.employee_role_assignments.
type EmployeeRoleAssignment struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	EmployeeID     uuid.UUID       `db:"employee_id"`
	RoleCode       string          `db:"role_code"`
	EffectiveStart time.Time       `db:"effective_start"`
	EffectiveEnd   *time.Time      `db:"effective_end"`
	Attributes     json.RawMessage `db:"attributes"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// EmployeeLocationAssignment mirrors e.employee_location_assignments.
type EmployeeLocationAssignment struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	EmployeeID     uuid.UUID       `db:"employee_id"`
	LocationID     uuid.UUID       `db:"location_id"`
	AssignmentType string          `db:"assignment_type"`
	EffectiveStart time.Time       `db:"effective_start"`
	EffectiveEnd   *time.Time      `db:"effective_end"`
	IsPrimary      bool            `db:"is_primary"`
	Attributes     json.RawMessage `db:"attributes"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}
