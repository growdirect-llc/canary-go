// Package workflow provides the cross-cutting orchestration substrate
// for long-running, multi-step processes that span Canary modules
// (three-way-match, l402 charge cycle, evidence-anchor batches, etc.).
//
// Two persistence tables back the substrate:
//
//   app.workflow_definitions — immutable registry of (workflow_code,
//                              version) entries. Registered at service
//                              boot via RegisterDefinition.
//
//   app.workflow_executions  — append-only audit. KickOff creates a
//                              row; Advance and Complete mutate
//                              status / current_step / context.
//
// Cross-instance coordination uses pg_advisory_lock so multiple
// service replicas can't double-fire the same execution. The lock
// key is hash(execution.id) — see pgAdvisoryKey.
//
// Reference: PostgreSQL advisory lock semantics —
//   https://www.postgresql.org/docs/17/explicit-locking.html
//
// Loop 4 Wave A scaffold (GRO-763 Phase B.4). The first real workflow
// (three-way-match in Module D) lands in Wave B; this package gives
// it a stable substrate to compose against.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values for app.workflow_executions.status — must match the
// CHECK constraint in deploy/schema/01_app_foundation.sql.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Errors surfaced by Store. Handlers map these to HTTP status codes
// per the convention in docs/conventions.md.
var (
	ErrDefinitionNotFound = errors.New("workflow: definition not found")
	ErrExecutionNotFound  = errors.New("workflow: execution not found")
	ErrInvalidTransition  = errors.New("workflow: invalid status transition")
	ErrLockUnavailable    = errors.New("workflow: advisory lock unavailable")
)

// Definition represents a row in app.workflow_definitions.
type Definition struct {
	ID            uuid.UUID
	WorkflowCode  string
	DisplayName   string
	Version       int
	Status        string
	Attributes    json.RawMessage
	RegisteredAt  string // server-formatted RFC3339; raw for now
}

// Execution represents a row in app.workflow_executions.
type Execution struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	WorkflowID   uuid.UUID
	ExternalRef  *string
	Status       string
	CurrentStep  *string
	Context      json.RawMessage
	StartedAt    string
	FinishedAt   *string
	ErrorMessage *string
	Attributes   json.RawMessage
}

// Store is the pgxpool-backed persistence layer for the workflow
// substrate. The interface lets handlers / tests substitute stubs
// when needed.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store wrapping the given pgxpool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// RegisterDefinition upserts a (workflow_code, version) row in
// app.workflow_definitions. Idempotent — re-registering the same
// version is a no-op. Designed to be called from cmd/<service>/main.go
// at boot, before any KickOff calls.
func (s *Store) RegisterDefinition(
	ctx context.Context,
	workflowCode, displayName string,
	version int,
	attributes json.RawMessage,
) (*Definition, error) {
	if attributes == nil {
		attributes = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO app.workflow_definitions
		    (workflow_code, display_name, version, attributes)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (workflow_code, version) DO UPDATE
		    SET display_name = EXCLUDED.display_name,
		        attributes   = EXCLUDED.attributes
		RETURNING id, workflow_code, display_name, version, status,
		          attributes, registered_at::text`
	row := s.pool.QueryRow(ctx, q, workflowCode, displayName, version, attributes)
	var d Definition
	if err := row.Scan(
		&d.ID, &d.WorkflowCode, &d.DisplayName, &d.Version,
		&d.Status, &d.Attributes, &d.RegisteredAt,
	); err != nil {
		return nil, fmt.Errorf("workflow: register definition: %w", err)
	}
	return &d, nil
}

// GetDefinitionByCode resolves a (workflow_code, version) tuple to a
// Definition row. Used by callers that hold the code constants but
// need the workflow_id for KickOff. Returns ErrNotFound when no row
// matches. Wired W5 / GRO-824 for the receiving-close → three-way-match
// trigger.
func (s *Store) GetDefinitionByCode(ctx context.Context, code string, version int) (*Definition, error) {
	const q = `
		SELECT id, workflow_code, display_name, version, status,
		       attributes, registered_at::text
		  FROM app.workflow_definitions
		 WHERE workflow_code = $1 AND version = $2`
	row := s.pool.QueryRow(ctx, q, code, version)
	var d Definition
	if err := row.Scan(
		&d.ID, &d.WorkflowCode, &d.DisplayName, &d.Version,
		&d.Status, &d.Attributes, &d.RegisteredAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDefinitionNotFound
		}
		return nil, fmt.Errorf("workflow: get definition by code: %w", err)
	}
	return &d, nil
}

// KickOff inserts a new app.workflow_executions row in 'pending'
// status. external_ref is optional and used for caller-side
// correlation (e.g., the inbound webhook's idempotency key).
func (s *Store) KickOff(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	externalRef *string,
	initialContext json.RawMessage,
) (*Execution, error) {
	if initialContext == nil {
		initialContext = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO app.workflow_executions
		    (tenant_id, workflow_id, external_ref, status, context)
		VALUES ($1, $2, $3, 'pending', $4::jsonb)
		RETURNING id, tenant_id, workflow_id, external_ref, status,
		          current_step, context, started_at::text,
		          finished_at::text, error_message, attributes`
	row := s.pool.QueryRow(ctx, q, tenantID, workflowID, externalRef, initialContext)
	return scanExecution(row)
}

// Advance moves an execution forward — sets current_step, optionally
// merges new keys into context, and ensures status is 'running'.
// Acquires a pg_advisory_lock keyed on the execution id for the
// duration of the transaction so concurrent advances can't race.
//
// If the execution is already in a terminal status (succeeded /
// failed / cancelled), returns ErrInvalidTransition.
func (s *Store) Advance(
	ctx context.Context,
	executionID uuid.UUID,
	step string,
	contextMerge json.RawMessage,
) (*Execution, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("workflow: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := acquireLock(ctx, tx, executionID); err != nil {
		return nil, err
	}

	cur, err := txGetExecution(ctx, tx, executionID)
	if err != nil {
		return nil, err
	}
	if isTerminal(cur.Status) {
		return nil, fmt.Errorf("%w: cannot advance from %s", ErrInvalidTransition, cur.Status)
	}

	merge := json.RawMessage("{}")
	if contextMerge != nil {
		merge = contextMerge
	}

	const q = `
		UPDATE app.workflow_executions
		   SET status        = 'running',
		       current_step  = $2,
		       context       = context || $3::jsonb
		 WHERE id = $1
		RETURNING id, tenant_id, workflow_id, external_ref, status,
		          current_step, context, started_at::text,
		          finished_at::text, error_message, attributes`
	row := tx.QueryRow(ctx, q, executionID, step, merge)
	out, err := scanExecution(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("workflow: commit advance: %w", err)
	}
	return out, nil
}

// Complete marks an execution terminal — succeeded, failed, or
// cancelled. errorMessage is required when status is 'failed', ignored
// otherwise. Acquires the same advisory lock as Advance.
func (s *Store) Complete(
	ctx context.Context,
	executionID uuid.UUID,
	status string,
	errorMessage *string,
) (*Execution, error) {
	if !isTerminal(status) {
		return nil, fmt.Errorf("%w: %q is not terminal", ErrInvalidTransition, status)
	}
	if status == StatusFailed && (errorMessage == nil || *errorMessage == "") {
		return nil, fmt.Errorf("%w: failed status requires error_message", ErrInvalidTransition)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("workflow: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := acquireLock(ctx, tx, executionID); err != nil {
		return nil, err
	}

	cur, err := txGetExecution(ctx, tx, executionID)
	if err != nil {
		return nil, err
	}
	if isTerminal(cur.Status) {
		return nil, fmt.Errorf("%w: already %s", ErrInvalidTransition, cur.Status)
	}

	const q = `
		UPDATE app.workflow_executions
		   SET status        = $2,
		       finished_at   = now(),
		       error_message = $3
		 WHERE id = $1
		RETURNING id, tenant_id, workflow_id, external_ref, status,
		          current_step, context, started_at::text,
		          finished_at::text, error_message, attributes`
	row := tx.QueryRow(ctx, q, executionID, status, errorMessage)
	out, err := scanExecution(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("workflow: commit complete: %w", err)
	}
	return out, nil
}

// GetExecution returns a snapshot of the execution by id without
// taking the advisory lock. Read-only — callers expect Eventually
// Consistent reads.
// ListExecutionsFilter scopes a ListExecutions query.
type ListExecutionsFilter struct {
	TenantID   uuid.UUID
	WorkflowID *uuid.UUID
	Status     string
	Limit      int
}

// ListExecutions returns app.workflow_executions rows for the tenant,
// optionally filtered by workflow_id and status. Used by the portal
// /workflows surface (W4 / GRO-823) to show in-flight workflows.
// Ordered by started_at DESC.
func (s *Store) ListExecutions(ctx context.Context, f ListExecutionsFilter) ([]Execution, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{f.TenantID}
	q := `SELECT id, tenant_id, workflow_id, external_ref, status,
	             current_step, context, started_at::text,
	             finished_at::text, error_message, attributes
	      FROM app.workflow_executions
	      WHERE tenant_id = $1`
	if f.WorkflowID != nil {
		args = append(args, *f.WorkflowID)
		q += fmt.Sprintf(" AND workflow_id = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow: list executions: %w", err)
	}
	defer rows.Close()
	out := make([]Execution, 0, limit)
	for rows.Next() {
		exec, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow: list executions scan: %w", err)
		}
		out = append(out, *exec)
	}
	return out, rows.Err()
}

// ListDefinitions returns all registered workflow definitions.
// Used by the portal /workflows surface to label executions by code.
func (s *Store) ListDefinitions(ctx context.Context) ([]Definition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, workflow_code, display_name, version, status,
		       attributes, registered_at::text
		FROM app.workflow_definitions
		ORDER BY workflow_code, version DESC`)
	if err != nil {
		return nil, fmt.Errorf("workflow: list definitions: %w", err)
	}
	defer rows.Close()
	out := make([]Definition, 0, 16)
	for rows.Next() {
		var d Definition
		if err := rows.Scan(
			&d.ID, &d.WorkflowCode, &d.DisplayName, &d.Version,
			&d.Status, &d.Attributes, &d.RegisteredAt,
		); err != nil {
			return nil, fmt.Errorf("workflow: list definitions scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetExecution(ctx context.Context, executionID uuid.UUID) (*Execution, error) {
	const q = `
		SELECT id, tenant_id, workflow_id, external_ref, status,
		       current_step, context, started_at::text,
		       finished_at::text, error_message, attributes
		  FROM app.workflow_executions
		 WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, executionID)
	return scanExecution(row)
}

// ─────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────

// pgAdvisoryKey hashes the execution id into the pair of int4 keys
// that pg_advisory_xact_lock expects. FNV-1a 64-bit, split into hi/lo.
func pgAdvisoryKey(id uuid.UUID) (int32, int32) {
	h := fnv.New64a()
	h.Write(id[:])
	sum := h.Sum64()
	return int32(sum >> 32), int32(sum & 0xFFFFFFFF)
}

// acquireLock takes a transaction-scoped advisory lock on (k1, k2)
// derived from executionID. Released automatically when tx commits or
// rolls back. pg_try_advisory_xact_lock returns false if another tx
// holds it — we surface ErrLockUnavailable to let the caller back off.
func acquireLock(ctx context.Context, tx pgx.Tx, executionID uuid.UUID) error {
	k1, k2 := pgAdvisoryKey(executionID)
	var ok bool
	err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, $2)`, k1, k2).Scan(&ok)
	if err != nil {
		return fmt.Errorf("workflow: pg_try_advisory_xact_lock: %w", err)
	}
	if !ok {
		return ErrLockUnavailable
	}
	return nil
}

func txGetExecution(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Execution, error) {
	const q = `
		SELECT id, tenant_id, workflow_id, external_ref, status,
		       current_step, context, started_at::text,
		       finished_at::text, error_message, attributes
		  FROM app.workflow_executions
		 WHERE id = $1
		   FOR UPDATE`
	row := tx.QueryRow(ctx, q, id)
	return scanExecution(row)
}

type scannable interface{ Scan(...any) error }

func scanExecution(r scannable) (*Execution, error) {
	var e Execution
	if err := r.Scan(
		&e.ID, &e.TenantID, &e.WorkflowID, &e.ExternalRef, &e.Status,
		&e.CurrentStep, &e.Context, &e.StartedAt,
		&e.FinishedAt, &e.ErrorMessage, &e.Attributes,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrExecutionNotFound
		}
		return nil, fmt.Errorf("workflow: scan execution: %w", err)
	}
	return &e, nil
}

func isTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	}
	return false
}
