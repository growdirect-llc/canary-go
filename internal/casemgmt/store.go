// internal/casemgmt/store.go
//
// pgxpool-backed access to detection.cases / detection.case_actions / detection.case_evidence.
//

package casemgmt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Sentinels mapped to HTTP status codes by the handler.
var (
	ErrNotFound   = errors.New("casemgmt: not found")
	ErrConflict   = errors.New("casemgmt: conflict")
	ErrValidation = errors.New("casemgmt: validation failed")
)

// CreateCase inserts a new detection.cases row + an optional initial
// detection link as a single transaction.
func (s *Store) CreateCase(ctx context.Context, req CreateCaseRequest) (*Case, error) {
	if req.TenantID == uuid.Nil {
		return nil, fmt.Errorf("%w: tenant_id required", ErrValidation)
	}
	if req.Title == "" {
		return nil, fmt.Errorf("%w: title required", ErrValidation)
	}
	if req.Severity == "" {
		return nil, fmt.Errorf("%w: severity required", ErrValidation)
	}
	if req.CaseType == "" {
		req.CaseType = "investigation"
	}
	if req.CaseNumber == "" {
		// Generate a stable case_number when caller didn't supply one.
		req.CaseNumber = fmt.Sprintf("CASE-%d", time.Now().UnixNano())
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("casemgmt: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertQ = `
		INSERT INTO detection.cases (
			tenant_id, case_number, case_type, title, description,
			severity, status, primary_subject_id, primary_location_id,
			assigned_to
		) VALUES (
			$1, $2, $3, $4, $5, $6, 'open', $7, $8, $9
		)
		RETURNING ` + caseSelectColumns
	row := tx.QueryRow(ctx, insertQ,
		req.TenantID, req.CaseNumber, req.CaseType, req.Title, req.Description,
		req.Severity, req.PrimarySubjectID, req.PrimaryLocationID, req.AssignedTo,
	)
	out, err := scanCase(row)
	if err != nil {
		return nil, fmt.Errorf("casemgmt: insert case: %w", err)
	}

	// Initial action: status_change to 'open'.
	if _, err := tx.Exec(ctx, `
		INSERT INTO detection.case_actions (tenant_id, case_id, action_type, performed_by, details)
		VALUES ($1, $2, 'status_change', $3, $4::jsonb)`,
		req.TenantID, out.ID, req.AssignedTo, []byte(`{"to":"open","origin":"create"}`),
	); err != nil {
		return nil, fmt.Errorf("casemgmt: insert initial action: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("casemgmt: commit: %w", err)
	}
	return out, nil
}

// GetCase returns a case by id.
func (s *Store) GetCase(ctx context.Context, tenantID, id uuid.UUID) (*Case, error) {
	const q = `SELECT ` + caseSelectColumns +
		` FROM detection.cases WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	out, err := scanCase(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("casemgmt: get case: %w", err)
	}
	return out, nil
}

// ListCases returns cases per filters, ordered by opened_at DESC.
func (s *Store) ListCases(ctx context.Context, f ListFilters) ([]Case, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `SELECT ` + caseSelectColumns + ` FROM detection.cases WHERE tenant_id = $1`
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if f.AssignedTo != nil {
		args = append(args, *f.AssignedTo)
		q += fmt.Sprintf(" AND assigned_to = $%d", len(args))
	}
	if f.Severity != "" {
		args = append(args, f.Severity)
		q += fmt.Sprintf(" AND severity = $%d", len(args))
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY opened_at DESC LIMIT $%d OFFSET $%d",
		len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("casemgmt: list: %w", err)
	}
	defer rows.Close()
	out := make([]Case, 0, f.Limit)
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AppendAction appends a row to detection.case_actions.
func (s *Store) AppendAction(ctx context.Context, tenantID, caseID uuid.UUID, req AppendActionRequest) (*CaseAction, error) {
	if req.ActionType == "" {
		return nil, fmt.Errorf("%w: action_type required", ErrValidation)
	}
	details, err := json.Marshal(orEmpty(req.Details))
	if err != nil {
		return nil, fmt.Errorf("casemgmt: marshal details: %w", err)
	}
	const q = `
		INSERT INTO detection.case_actions (tenant_id, case_id, action_type, performed_by, details)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING id, case_id, action_type, performed_by, performed_at, details`
	row := s.pool.QueryRow(ctx, q, tenantID, caseID, req.ActionType, req.PerformedBy, details)
	var a CaseAction
	var raw []byte
	if err := row.Scan(&a.ID, &a.CaseID, &a.ActionType, &a.PerformedBy, &a.PerformedAt, &raw); err != nil {
		return nil, fmt.Errorf("casemgmt: insert action: %w", err)
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &a.Details)
	}
	return &a, nil
}

// AppendEvidence appends a row to detection.case_evidence with hash chaining
// (prev_evidence_hash). Hashes are SHA-256 hex of the canonical JSON
// payload for cheap content addressing.
func (s *Store) AppendEvidence(ctx context.Context, tenantID, caseID uuid.UUID, req AppendEvidenceRequest) (*CaseEvidence, error) {
	if req.EvidenceType == "" {
		return nil, fmt.Errorf("%w: evidence_type required", ErrValidation)
	}
	payloadBytes, err := json.Marshal(orEmpty(req.Payload))
	if err != nil {
		return nil, fmt.Errorf("casemgmt: marshal payload: %w", err)
	}
	hashBytes := sha256.Sum256(payloadBytes)
	hash := hex.EncodeToString(hashBytes[:])

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("casemgmt: evidence begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Get the prior evidence hash for chaining.
	var prevHash *string
	if err := tx.QueryRow(ctx, `
		SELECT payload_hash FROM detection.case_evidence
		 WHERE case_id = $1
		 ORDER BY collected_at DESC, id DESC
		 LIMIT 1`, caseID).Scan(&prevHash); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("casemgmt: prev hash lookup: %w", err)
		}
		// First evidence row — prev_evidence_hash stays NULL.
	}

	const insertQ = `
		INSERT INTO detection.case_evidence (
			tenant_id, case_id, evidence_type, source_entity_type,
			source_entity_id, payload, payload_hash, prev_evidence_hash,
			collected_by
		) VALUES (
			$1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9
		)
		RETURNING id, case_id, evidence_type, source_entity_type,
		          source_entity_id, payload_hash, prev_evidence_hash,
		          blockchain_anchor_id, collected_by, collected_at`
	row := tx.QueryRow(ctx, insertQ,
		tenantID, caseID, req.EvidenceType,
		req.SourceEntityType, req.SourceEntityID,
		payloadBytes, hash, prevHash, req.CollectedBy,
	)
	var e CaseEvidence
	if err := row.Scan(&e.ID, &e.CaseID, &e.EvidenceType,
		&e.SourceEntityType, &e.SourceEntityID, &e.PayloadHash,
		&e.PrevEvidenceHash, &e.BlockchainAnchorID, &e.CollectedBy, &e.CollectedAt); err != nil {
		return nil, fmt.Errorf("casemgmt: insert evidence: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("casemgmt: evidence commit: %w", err)
	}
	return &e, nil
}

// CloseCase moves a case to status='closed' with the given resolution.
// Idempotent — re-closing a closed case is a no-op (no error). Always
// appends a 'resolution' action so the audit trail captures the close.
func (s *Store) CloseCase(ctx context.Context, tenantID, caseID uuid.UUID, req CloseRequest) (*Case, error) {
	if req.ResolutionType == "" {
		return nil, fmt.Errorf("%w: resolution_type required", ErrValidation)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("casemgmt: close begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const updateQ = `
		UPDATE detection.cases
		   SET status = 'closed', resolved_at = now(),
		       resolution_type = $3, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2
		RETURNING ` + caseSelectColumns
	row := tx.QueryRow(ctx, updateQ, tenantID, caseID, req.ResolutionType)
	out, err := scanCase(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("casemgmt: close update: %w", err)
	}

	details, _ := json.Marshal(map[string]any{
		"resolution_type": req.ResolutionType,
		"notes":           req.Notes,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO detection.case_actions (tenant_id, case_id, action_type, performed_by, details)
		VALUES ($1, $2, 'resolution', $3, $4::jsonb)`,
		tenantID, caseID, req.ClosedBy, details,
	); err != nil {
		return nil, fmt.Errorf("casemgmt: close action: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("casemgmt: close commit: %w", err)
	}
	return out, nil
}

// ListActions returns actions for a case, oldest first (audit trail).
func (s *Store) ListActions(ctx context.Context, caseID uuid.UUID) ([]CaseAction, error) {
	const q = `
		SELECT id, case_id, action_type, performed_by, performed_at, details
		  FROM detection.case_actions
		 WHERE case_id = $1
		 ORDER BY performed_at ASC, id ASC`
	rows, err := s.pool.Query(ctx, q, caseID)
	if err != nil {
		return nil, fmt.Errorf("casemgmt: list actions: %w", err)
	}
	defer rows.Close()
	out := []CaseAction{}
	for rows.Next() {
		var a CaseAction
		var raw []byte
		if err := rows.Scan(&a.ID, &a.CaseID, &a.ActionType, &a.PerformedBy, &a.PerformedAt, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Details)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListEvidence returns evidence for a case, oldest first.
func (s *Store) ListEvidence(ctx context.Context, caseID uuid.UUID) ([]CaseEvidence, error) {
	const q = `
		SELECT id, case_id, evidence_type, source_entity_type,
		       source_entity_id, payload_hash, prev_evidence_hash,
		       blockchain_anchor_id, collected_by, collected_at
		  FROM detection.case_evidence
		 WHERE case_id = $1
		 ORDER BY collected_at ASC, id ASC`
	rows, err := s.pool.Query(ctx, q, caseID)
	if err != nil {
		return nil, fmt.Errorf("casemgmt: list evidence: %w", err)
	}
	defer rows.Close()
	out := []CaseEvidence{}
	for rows.Next() {
		var e CaseEvidence
		if err := rows.Scan(&e.ID, &e.CaseID, &e.EvidenceType,
			&e.SourceEntityType, &e.SourceEntityID, &e.PayloadHash,
			&e.PrevEvidenceHash, &e.BlockchainAnchorID, &e.CollectedBy, &e.CollectedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// caseSelectColumns is the canonical column list for detection.cases reads.
const caseSelectColumns = `id, tenant_id, case_number, case_type,
title, description, severity, status,
primary_subject_id, primary_location_id, assigned_to,
opened_at, resolved_at, resolution_type,
loss_amount_estimated::text, loss_amount_recovered::text,
created_at, updated_at`

type scannable interface {
	Scan(dest ...any) error
}

func scanCase(r scannable) (*Case, error) {
	var c Case
	if err := r.Scan(
		&c.ID, &c.TenantID, &c.CaseNumber, &c.CaseType,
		&c.Title, &c.Description, &c.Severity, &c.Status,
		&c.PrimarySubjectID, &c.PrimaryLocationID, &c.AssignedTo,
		&c.OpenedAt, &c.ResolvedAt, &c.ResolutionType,
		&c.LossEstimated, &c.LossRecovered,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
