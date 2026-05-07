// internal/alert/store.go
//
// pgx-backed alert store. Reads and mutates detection.detections — the
// canonical alert surface. JOINs detection.detection_rules for the enriched
// wire shape (rule_code, rule_category).
//
// Spec: GRO-766 Phase A.1.

package alert

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed alert access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var (
	ErrNotFound   = errors.New("alert: not found")
	ErrConflict   = errors.New("alert: conflict")
	ErrValidation = errors.New("alert: validation failed")
)

const selectCols = `
	d.id, d.tenant_id, d.rule_id,
	r.rule_code, r.rule_category,
	d.detected_at, d.source_entity_type, d.source_entity_id,
	d.location_id, d.cashier_employee_id, d.customer_id,
	d.severity, d.signal_strength,
	d.status, d.case_id,
	d.acknowledged_at, d.acknowledged_by,
	d.created_at`

func scanAlert(row interface{ Scan(dest ...any) error }) (*AlertDTO, error) {
	var a AlertDTO
	var sig *float64
	return &a, row.Scan(
		&a.ID, &a.TenantID, &a.RuleID,
		&a.RuleCode, &a.RuleCategory,
		&a.DetectedAt, &a.SourceEntityType, &a.SourceEntityID,
		&a.LocationID, &a.CashierEmployeeID, &a.CustomerID,
		&a.Severity, &sig,
		&a.Status, &a.CaseID,
		&a.AcknowledgedAt, &a.AcknowledgedBy,
		&a.CreatedAt,
	)
}

// GetByID returns a single detection row enriched with rule metadata.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*AlertDTO, error) {
	q := `SELECT ` + selectCols + `
	        FROM detection.detections d
	        JOIN detection.detection_rules r ON r.id = d.rule_id
	       WHERE d.tenant_id = $1 AND d.id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	a, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("alert: get: %w", err)
	}
	return a, nil
}

// List returns detections matching the filters. Status 'dismissed' and
// 'duplicate' are excluded unless the caller explicitly filters on them.
func (s *Store) List(ctx context.Context, f ListFilters) ([]AlertDTO, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `SELECT ` + selectCols + `
	        FROM detection.detections d
	        JOIN detection.detection_rules r ON r.id = d.rule_id
	       WHERE d.tenant_id = $1`

	if f.Severity != "" {
		args = append(args, f.Severity)
		q += fmt.Sprintf(" AND d.severity = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND d.status = $%d", len(args))
	} else {
		q += " AND d.status NOT IN ('dismissed','duplicate')"
	}
	if f.RuleType != "" {
		args = append(args, f.RuleType)
		q += fmt.Sprintf(" AND r.rule_category = $%d", len(args))
	}
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		q += fmt.Sprintf(" AND d.location_id = $%d", len(args))
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY d.detected_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("alert: list: %w", err)
	}
	defer rows.Close()
	out := make([]AlertDTO, 0, f.Limit)
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("alert: list scan: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Acknowledge transitions a detection to acknowledged.
func (s *Store) Acknowledge(ctx context.Context, tenantID, id, byUserID uuid.UUID) (*AlertDTO, error) {
	now := time.Now().UTC()
	const q = `
		UPDATE detection.detections
		   SET status = 'acknowledged',
		       acknowledged_at = $3,
		       acknowledged_by = $4
		 WHERE tenant_id = $1 AND id = $2
		   AND status = 'new'`
	tag, err := s.pool.Exec(ctx, q, tenantID, id, now, byUserID)
	if err != nil {
		return nil, fmt.Errorf("alert: acknowledge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either not found or already acknowledged — check which.
		a, err2 := s.GetByID(ctx, tenantID, id)
		if err2 != nil {
			return nil, ErrNotFound
		}
		if a.Status != "new" {
			return nil, fmt.Errorf("%w: status is %q, must be 'new'", ErrConflict, a.Status)
		}
		return nil, ErrNotFound
	}
	return s.GetByID(ctx, tenantID, id)
}

// Resolve dismisses a detection with a disposition label.
func (s *Store) Resolve(ctx context.Context, tenantID, id uuid.UUID, req ResolveRequest) (*AlertDTO, error) {
	const q = `
		UPDATE detection.detections
		   SET status = 'dismissed',
		       attributes = attributes || jsonb_build_object(
		           'disposition', $3::text,
		           'resolve_note', $4::text
		       )
		 WHERE tenant_id = $1 AND id = $2
		   AND status NOT IN ('dismissed','duplicate')`
	tag, err := s.pool.Exec(ctx, q, tenantID, id, req.Disposition, req.Note)
	if err != nil {
		return nil, fmt.Errorf("alert: resolve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_, err2 := s.GetByID(ctx, tenantID, id)
		if errors.Is(err2, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: already resolved", ErrConflict)
	}
	return s.GetByID(ctx, tenantID, id)
}

// Suppress dismisses a detection with a suppression marker. Duration=0
// means indefinite suppression; >0 means mute until now+duration.
func (s *Store) Suppress(ctx context.Context, tenantID, id uuid.UUID, req SuppressRequest) (*AlertDTO, error) {
	suppressedUntil := (*time.Time)(nil)
	if req.DurationMinutes > 0 {
		t := time.Now().UTC().Add(time.Duration(req.DurationMinutes) * time.Minute)
		suppressedUntil = &t
	}
	const q = `
		UPDATE detection.detections
		   SET status = 'dismissed',
		       attributes = attributes || jsonb_build_object(
		           'suppressed', true,
		           'suppress_reason', $3::text,
		           'suppressed_until', $4::timestamptz
		       )
		 WHERE tenant_id = $1 AND id = $2
		   AND status NOT IN ('dismissed','duplicate')`
	tag, err := s.pool.Exec(ctx, q, tenantID, id, req.Reason, suppressedUntil)
	if err != nil {
		return nil, fmt.Errorf("alert: suppress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_, err2 := s.GetByID(ctx, tenantID, id)
		if errors.Is(err2, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: already dismissed", ErrConflict)
	}
	return s.GetByID(ctx, tenantID, id)
}

// Stats returns detection counts grouped by rule_category + severity + status.
func (s *Store) Stats(ctx context.Context, tenantID uuid.UUID) ([]AlertStatsRow, error) {
	const q = `
		SELECT r.rule_category, d.severity, d.status, COUNT(*) AS cnt
		  FROM detection.detections d
		  JOIN detection.detection_rules r ON r.id = d.rule_id
		 WHERE d.tenant_id = $1
		 GROUP BY r.rule_category, d.severity, d.status
		 ORDER BY r.rule_category, d.severity, d.status`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("alert: stats: %w", err)
	}
	defer rows.Close()
	var out []AlertStatsRow
	for rows.Next() {
		var r AlertStatsRow
		if err := rows.Scan(&r.RuleCategory, &r.Severity, &r.Status, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
