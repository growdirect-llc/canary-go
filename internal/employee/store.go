// internal/employee/store.go
//
// pgx-backed employee store. Reads employee.employees and aggregates over
// detection.detections (cashier_employee_id) for the alert summary view.
//
//

package employee

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed employee access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var ErrNotFound = errors.New("employee: not found")

const employeeCols = `
	e.id, e.tenant_id, e.user_id, e.employee_code,
	e.first_name, e.last_name, e.display_name, e.email,
	e.hire_date, e.termination_date, e.employment_status, e.pay_type,
	e.created_at, e.updated_at`

func scanEmployee(row interface{ Scan(dest ...any) error }) (*EmployeeDTO, error) {
	var e EmployeeDTO
	return &e, row.Scan(
		&e.ID, &e.TenantID, &e.UserID, &e.EmployeeCode,
		&e.FirstName, &e.LastName, &e.DisplayName, &e.Email,
		&e.HireDate, &e.TerminationDate, &e.EmploymentStatus, &e.PayType,
		&e.CreatedAt, &e.UpdatedAt,
	)
}

// List returns employees matching the filters.
func (s *Store) List(ctx context.Context, f ListFilters) ([]EmployeeDTO, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `SELECT` + employeeCols + `
		FROM employee.employees e
		WHERE e.tenant_id = $1`

	if f.EmploymentStatus != "" {
		args = append(args, f.EmploymentStatus)
		q += fmt.Sprintf(" AND e.employment_status = $%d", len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		idx := len(args)
		q += fmt.Sprintf(
			" AND (e.display_name ILIKE $%d OR e.employee_code ILIKE $%d OR e.email ILIKE $%d)",
			idx, idx, idx,
		)
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY e.last_name, e.first_name LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("employee: list: %w", err)
	}
	defer rows.Close()
	out := make([]EmployeeDTO, 0, f.Limit)
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, fmt.Errorf("employee: list scan: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetByID returns a single employee.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*EmployeeDTO, error) {
	q := `SELECT` + employeeCols + `
		FROM employee.employees e
		WHERE e.tenant_id = $1 AND e.id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	e, err := scanEmployee(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("employee: get: %w", err)
	}
	return e, nil
}

// AlertSummaries returns detection counts grouped by cashier employee
// for the tenant. Ordered by total_alerts DESC.
func (s *Store) AlertSummaries(ctx context.Context, tenantID uuid.UUID) ([]AlertSummary, error) {
	const q = `
		SELECT
		    e.id, e.employee_code, e.display_name,
		    COUNT(d.id) AS total_alerts,
		    COUNT(d.id) FILTER (WHERE d.status = 'new')           AS new_alerts,
		    COUNT(d.id) FILTER (WHERE d.status = 'acknowledged')  AS acked_alerts,
		    COUNT(d.id) FILTER (WHERE d.severity = 'critical')    AS critical_count,
		    COUNT(d.id) FILTER (WHERE d.severity = 'high')        AS high_count
		FROM employee.employees e
		JOIN detection.detections d ON d.cashier_employee_id = e.id
		WHERE e.tenant_id = $1
		  AND d.tenant_id = $1
		GROUP BY e.id, e.employee_code, e.display_name
		ORDER BY total_alerts DESC`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("employee: alert summaries: %w", err)
	}
	defer rows.Close()
	var out []AlertSummary
	for rows.Next() {
		var a AlertSummary
		if err := rows.Scan(
			&a.EmployeeID, &a.EmployeeCode, &a.DisplayName,
			&a.TotalAlerts, &a.NewAlerts, &a.AckedAlerts,
			&a.CriticalCount, &a.HighCount,
		); err != nil {
			return nil, fmt.Errorf("employee: alert summaries scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
