package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/owl/dtotypes"
)

// CashierExposure ranks cashiers by detection count in the period.
//
// Joins detection.detections.cashier_employee_id → employee.employees(id). Cashiers
// without any detection in the period are omitted.
//
// SDD-vague: owl.md SDD says risk_score lives on app.employees (legacy
// Square table), not employee.employees (canonical). Both tables exist; the
// detection rows reference employee.employees per 09_q_canary_mechanics.sql:91
// (no FK declared in the schema, but the column comment says
// "FK to employee.employees(id)"). We honor the canonical join.
//
// SDD-missing: detection.detections.cashier_employee_id has no FK constraint
// declared in the schema (just a column comment). Loop 3 should add
// `REFERENCES employee.employees(id)` to enforce referential integrity.
// We use a LEFT JOIN to survive the meantime — orphan detections
// surface as "(unknown)" rows in the response.
func CashierExposure(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time, limit int) ([]dtotypes.CashierExposure, error) {
	const q = `
		SELECT
		  d.cashier_employee_id,
		  COALESCE(e.employee_code, '') AS employee_code,
		  COALESCE(NULLIF(e.display_name, ''),
		           NULLIF(TRIM(e.first_name || ' ' || e.last_name), ''),
		           '(unknown)')         AS display_name,
		  COUNT(*)                      AS detection_count
		FROM detection.detections d
		LEFT JOIN employee.employees e ON e.id = d.cashier_employee_id
		WHERE d.tenant_id = $1
		  AND d.detected_at >= $2
		  AND d.detected_at <  $3
		  AND d.cashier_employee_id IS NOT NULL
		  AND d.status NOT IN ('dismissed', 'duplicate')
		GROUP BY d.cashier_employee_id, e.employee_code, e.display_name, e.first_name, e.last_name
		ORDER BY COUNT(*) DESC
		LIMIT $4
	`
	rows, err := pool.Query(ctx, q, tenantID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("metrics.CashierExposure: %w", err)
	}
	defer rows.Close()

	out := make([]dtotypes.CashierExposure, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		var code, name string
		var n int64
		if err := rows.Scan(&id, &code, &name, &n); err != nil {
			return nil, fmt.Errorf("metrics.CashierExposure scan: %w", err)
		}
		out = append(out, dtotypes.CashierExposure{
			EmployeeID:     id,
			EmployeeCode:   code,
			DisplayName:    strings.TrimSpace(name),
			DetectionCount: n,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics.CashierExposure rows: %w", err)
	}
	return out, nil
}
