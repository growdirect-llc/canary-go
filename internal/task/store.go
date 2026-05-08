// internal/task/store.go — pgx-backed task queue store.
package task

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed access layer for the task package.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new directed task and returns it.
func (s *Store) Create(ctx context.Context, req CreateTaskRequest) (*TaskDTO, error) {
	const q = `
		INSERT INTO app.directed_tasks
		    (tenant_id, task_type, priority, item_id, location_id,
		     zone_id, quantity, source_location_id,
		     estimated_seconds, source_ref, attributes)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9, $10,
		        COALESCE($11, '{}'::jsonb))
		RETURNING id, tenant_id, task_type, priority, status,
		          item_id, location_id, zone_id, quantity::text,
		          source_location_id, assignee_id, assigned_at,
		          started_at, completed_at, verified_at,
		          estimated_seconds, skip_reason, source_ref,
		          attributes, created_at, updated_at`

	row := s.pool.QueryRow(ctx, q,
		req.TenantID, req.TaskType, req.Priority,
		req.ItemID, req.LocationID, req.ZoneID, req.Quantity,
		req.SourceLocationID, req.EstimatedSeconds,
		req.SourceRef, nullableJSON(req.Attributes),
	)
	return scanTask(row)
}

// GetByID returns a single task. Returns ErrNotFound if absent.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*TaskDTO, error) {
	const q = taskSelectBase + `
		 WHERE t.id = $1 AND t.tenant_id = $2`
	row := s.pool.QueryRow(ctx, q, id, tenantID)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// GetNext atomically claims the highest-priority queued task for the given
// employee: SELECT + UPDATE in one transaction with SKIP LOCKED.
// Returns ErrNotFound when no queued task exists.
func (s *Store) GetNext(ctx context.Context, tenantID, employeeID uuid.UUID) (*TaskDTO, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("task: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Claim the best queued task, skipping any rows locked by another worker.
	const claimQ = `
		SELECT id FROM app.directed_tasks
		 WHERE tenant_id = $1 AND status = 'queued'
		 ORDER BY priority ASC, created_at ASC
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`
	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, claimQ, tenantID).Scan(&taskID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task: get next claim: %w", err)
	}

	now := time.Now().UTC()
	const updateQ = `
		UPDATE app.directed_tasks
		   SET status = 'assigned', assignee_id = $1,
		       assigned_at = $2, updated_at = $2
		 WHERE id = $3
		RETURNING ` + taskReturnCols
	row := tx.QueryRow(ctx, updateQ, employeeID, now, taskID)
	t, err := scanTask(row)
	if err != nil {
		return nil, fmt.Errorf("task: assign: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("task: commit: %w", err)
	}
	return t, nil
}

// UpdateStatus advances the task to a new status after validating the
// transition. Sets the appropriate timestamp (started_at, completed_at,
// verified_at) based on the target status.
func (s *Store) UpdateStatus(ctx context.Context, tenantID, taskID uuid.UUID, to string) (*TaskDTO, error) {
	// Load the current status first.
	current, err := s.GetByID(ctx, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(current.Status, to); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := `UPDATE app.directed_tasks SET status = $1, updated_at = $2`
	args := []any{to, now}
	idx := 3
	switch to {
	case StatusInProgress:
		q += fmt.Sprintf(", started_at = $%d", idx)
		args = append(args, now)
		idx++
	case StatusComplete:
		q += fmt.Sprintf(", completed_at = $%d", idx)
		args = append(args, now)
		idx++
	case StatusVerified:
		q += fmt.Sprintf(", verified_at = $%d", idx)
		args = append(args, now)
		idx++
	}
	q += fmt.Sprintf(" WHERE id = $%d AND tenant_id = $%d RETURNING "+taskReturnCols, idx, idx+1)
	args = append(args, taskID, tenantID)

	row := s.pool.QueryRow(ctx, q, args...)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// Skip marks a task as skipped and records the reason.
func (s *Store) Skip(ctx context.Context, tenantID, taskID uuid.UUID, reason string) (*TaskDTO, error) {
	current, err := s.GetByID(ctx, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(current.Status, StatusSkipped); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	const q = `
		UPDATE app.directed_tasks
		   SET status = 'skipped', skip_reason = $1, updated_at = $2
		 WHERE id = $3 AND tenant_id = $4
		RETURNING ` + taskReturnCols
	row := s.pool.QueryRow(ctx, q, reason, now, taskID, tenantID)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// LogException inserts a task_exceptions row. The task status is unchanged —
// exceptions are observations, not state transitions.
func (s *Store) LogException(ctx context.Context, tenantID, taskID uuid.UUID, req ExceptionRequest) (*ExceptionDTO, error) {
	if _, ok := validExceptionCodes[req.ReasonCode]; !ok {
		return nil, ErrInvalidExceptionCode
	}
	const q = `
		INSERT INTO app.task_exceptions
		    (task_id, tenant_id, reason_code, note, reported_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, task_id, tenant_id, reason_code, note, reported_by, created_at`
	row := s.pool.QueryRow(ctx, q, taskID, tenantID, req.ReasonCode, req.Note, req.ReportedBy)
	var ex ExceptionDTO
	if err := row.Scan(&ex.ID, &ex.TaskID, &ex.TenantID, &ex.ReasonCode,
		&ex.Note, &ex.ReportedBy, &ex.CreatedAt); err != nil {
		return nil, fmt.Errorf("task: log exception: %w", err)
	}
	return &ex, nil
}

// OpenReplenishmentExists returns true when an open (queued/assigned/in_progress)
// replenishment task already exists for this (tenant, item, location) tuple.
// Used for deduplication by the replenishment trigger.
func (s *Store) OpenReplenishmentExists(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (bool, error) {
	const q = `
		SELECT EXISTS (
		  SELECT 1 FROM app.directed_tasks
		   WHERE tenant_id = $1
		     AND item_id = $2
		     AND location_id = $3
		     AND task_type = 'replenishment'
		     AND status IN ('queued','assigned','in_progress')
		)`
	var exists bool
	if err := s.pool.QueryRow(ctx, q, tenantID, itemID, locationID).Scan(&exists); err != nil {
		return false, fmt.Errorf("task: check open replenishment: %w", err)
	}
	return exists, nil
}

// ListByTenant returns tasks for a tenant. status filter:
//   - "" or "all"  → no status filter
//   - "open"       → queued + assigned + in_progress
//   - any single status string → exact match
// Limit is clamped to [1, 500] with a default of 100.
// Wired W5 for the /tasks portal page.
func (s *Store) ListByTenant(ctx context.Context, tenantID uuid.UUID, status string, limit int) ([]TaskDTO, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{tenantID}
	q := taskSelectBase + ` WHERE t.tenant_id = $1`
	switch status {
	case "", "all":
		// no extra clause
	case "open":
		q += ` AND t.status IN ('queued','assigned','in_progress')`
	default:
		args = append(args, status)
		q += ` AND t.status = $2`
	}
	q += ` ORDER BY t.priority ASC, t.created_at ASC LIMIT ` + strconv.Itoa(limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task: list by tenant: %w", err)
	}
	defer rows.Close()
	out := make([]TaskDTO, 0, limit)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// taskSelectBase is the shared SELECT prefix for task reads.
const taskSelectBase = `
	SELECT t.id, t.tenant_id, t.task_type, t.priority, t.status,
	       t.item_id, t.location_id, t.zone_id, t.quantity::text,
	       t.source_location_id, t.assignee_id, t.assigned_at,
	       t.started_at, t.completed_at, t.verified_at,
	       t.estimated_seconds, t.skip_reason, t.source_ref,
	       t.attributes, t.created_at, t.updated_at
	  FROM app.directed_tasks t`

// taskReturnCols matches the RETURNING columns in UPDATE queries.
const taskReturnCols = `
	id, tenant_id, task_type, priority, status,
	item_id, location_id, zone_id, quantity::text,
	source_location_id, assignee_id, assigned_at,
	started_at, completed_at, verified_at,
	estimated_seconds, skip_reason, source_ref,
	attributes, created_at, updated_at`

func scanTask(r interface{ Scan(...any) error }) (*TaskDTO, error) {
	var t TaskDTO
	if err := r.Scan(
		&t.ID, &t.TenantID, &t.TaskType, &t.Priority, &t.Status,
		&t.ItemID, &t.LocationID, &t.ZoneID, &t.Quantity,
		&t.SourceLocationID, &t.AssigneeID, &t.AssignedAt,
		&t.StartedAt, &t.CompletedAt, &t.VerifiedAt,
		&t.EstimatedSeconds, &t.SkipReason, &t.SourceRef,
		&t.Attributes, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
