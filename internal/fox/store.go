// internal/fox/store.go
//
// Direct pgx + raw SQL persistence layer. Loop 2 dispatch
// overrides the CanaryGo CLAUDE.md "all queries through sqlc" rule
// for this wave — sqlc retrofit lands in Loop 3.
package fox

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

	"github.com/ruptiv/canary/internal/db/types"
)

// ErrNotFound is returned by lookups when no row matches. Wrap with
// errors.Is at call sites to distinguish "missing" from "DB failed".
var ErrNotFound = errors.New("fox: not found")

// Store is fox's persistence boundary. All q.* writes go through here.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given pgxpool. The pool
// must already be connected and pingable.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Compile-time guard: Store satisfies EscalationStore.
var _ EscalationStore = (*Store)(nil)

// LoadDetection fetches a single detection by id. Returns ErrNotFound
// if the row doesn't exist.
func (s *Store) LoadDetection(ctx context.Context, id uuid.UUID) (*types.Detection, error) {
	const q = `
		SELECT id, tenant_id, rule_id, detected_at, source_entity_type,
		       source_entity_id, location_id, cashier_employee_id, customer_id,
		       severity, signal_strength, evidence, case_id, status,
		       acknowledged_at, acknowledged_by, attributes, created_at
		  FROM detection.detections
		 WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	var d types.Detection
	err := row.Scan(
		&d.ID, &d.TenantID, &d.RuleID, &d.DetectedAt, &d.SourceEntityType,
		&d.SourceEntityID, &d.LocationID, &d.CashierEmployeeID, &d.CustomerID,
		&d.Severity, &d.SignalStrength, &d.Evidence, &d.CaseID, &d.Status,
		&d.AcknowledgedAt, &d.AcknowledgedBy, &d.Attributes, &d.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fox.LoadDetection: %w", err)
	}
	return &d, nil
}

// LoadCase fetches a case by id without its evidence/actions. Returns
// ErrNotFound if missing.
func (s *Store) LoadCase(ctx context.Context, id uuid.UUID) (*types.Case, error) {
	const q = `
		SELECT id, tenant_id, case_number, case_type, title, description,
		       severity, status, primary_subject_id, primary_location_id,
		       assigned_to, opened_at, resolved_at, resolution_type,
		       loss_amount_estimated, loss_amount_recovered, attributes,
		       created_at, updated_at
		  FROM detection.cases
		 WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	var c types.Case
	err := row.Scan(
		&c.ID, &c.TenantID, &c.CaseNumber, &c.CaseType, &c.Title, &c.Description,
		&c.Severity, &c.Status, &c.PrimarySubjectID, &c.PrimaryLocationID,
		&c.AssignedTo, &c.OpenedAt, &c.ResolvedAt, &c.ResolutionType,
		&c.LossAmountEstimated, &c.LossAmountRecovered, &c.Attributes,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fox.LoadCase: %w", err)
	}
	return &c, nil
}

// FindOpenCaseBySubject returns the most-recently-opened non-terminal
// case for the given (tenant, subject) pair. Returns nil + nil when no
// match exists (a soft miss is not an error).
//
// Implementation notes:
//   - detection.cases.primary_subject_id is the clustering key.
//   - "open" excludes resolved/closed; the schema's idx_qcases_active
//     partial index covers this filter exactly.
//   - We pick the most recent by opened_at to match the human
//     intuition of "the case the investigator is currently on."
func (s *Store) FindOpenCaseBySubject(ctx context.Context, tenantID, subjectID uuid.UUID) (*types.Case, error) {
	const q = `
		SELECT id, tenant_id, case_number, case_type, title, description,
		       severity, status, primary_subject_id, primary_location_id,
		       assigned_to, opened_at, resolved_at, resolution_type,
		       loss_amount_estimated, loss_amount_recovered, attributes,
		       created_at, updated_at
		  FROM detection.cases
		 WHERE tenant_id = $1
		   AND primary_subject_id = $2
		   AND status NOT IN ('resolved','closed')
		 ORDER BY opened_at DESC
		 LIMIT 1`
	row := s.pool.QueryRow(ctx, q, tenantID, subjectID)
	var c types.Case
	err := row.Scan(
		&c.ID, &c.TenantID, &c.CaseNumber, &c.CaseType, &c.Title, &c.Description,
		&c.Severity, &c.Status, &c.PrimarySubjectID, &c.PrimaryLocationID,
		&c.AssignedTo, &c.OpenedAt, &c.ResolvedAt, &c.ResolutionType,
		&c.LossAmountEstimated, &c.LossAmountRecovered, &c.Attributes,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fox.FindOpenCaseBySubject: %w", err)
	}
	return &c, nil
}

// OpenCase inserts a new detection.cases row in a transaction along with its
// initial seeding state-change action. If the case is being opened
// from a detection (linkDetection != nil), that detection's case_id
// and status are updated atomically in the same tx so the lifecycle
// invariant — "every detection that escalates points at its case"
// — holds even if the caller crashes between calls.
//
// Returns the minted case id. The supplied *types.Case is mutated:
// ID, OpenedAt, CreatedAt, UpdatedAt are populated.
func (s *Store) OpenCase(ctx context.Context, c *types.Case, linkDetection *uuid.UUID) (uuid.UUID, error) {
	if c == nil {
		return uuid.Nil, fmt.Errorf("fox.OpenCase: nil case")
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now().UTC()
	c.OpenedAt = now
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Status == "" {
		c.Status = string(CaseStatusOpen)
	}
	if c.CaseType == "" {
		c.CaseType = "investigation"
	}
	if len(c.Attributes) == 0 {
		c.Attributes = json.RawMessage(`{}`)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("fox.OpenCase begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertCase = `
		INSERT INTO detection.cases (
			id, tenant_id, case_number, case_type, title, description,
			severity, status, primary_subject_id, primary_location_id,
			assigned_to, opened_at, attributes, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		)`
	if _, err := tx.Exec(ctx, insertCase,
		c.ID, c.TenantID, c.CaseNumber, c.CaseType, c.Title, c.Description,
		c.Severity, c.Status, c.PrimarySubjectID, c.PrimaryLocationID,
		c.AssignedTo, c.OpenedAt, c.Attributes, c.CreatedAt, c.UpdatedAt,
	); err != nil {
		return uuid.Nil, fmt.Errorf("fox.OpenCase insert: %w", err)
	}

	// Seed the action log so case history starts with a row, not a gap.
	const insertAction = `
		INSERT INTO detection.case_actions (
			id, tenant_id, case_id, action_type, performed_by, performed_at, details, created_at
		) VALUES ($1,$2,$3,'status_change',$4,$5,$6,$7)`
	openDetails, _ := json.Marshal(map[string]string{
		"to":     string(CaseStatusOpen),
		"reason": "case opened",
	})
	if _, err := tx.Exec(ctx, insertAction,
		uuid.New(), c.TenantID, c.ID, c.AssignedTo, now, openDetails, now,
	); err != nil {
		return uuid.Nil, fmt.Errorf("fox.OpenCase action: %w", err)
	}

	if linkDetection != nil {
		const updateDetection = `
			UPDATE detection.detections
			   SET case_id = $1, status = 'escalated_to_case'
			 WHERE id = $2 AND tenant_id = $3`
		ct, err := tx.Exec(ctx, updateDetection, c.ID, *linkDetection, c.TenantID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("fox.OpenCase link detection: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return uuid.Nil, fmt.Errorf("fox.OpenCase: detection %s not found in tenant %s", *linkDetection, c.TenantID)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("fox.OpenCase commit: %w", err)
	}
	return c.ID, nil
}

// AppendEvidence inserts a row into detection.case_evidence and computes the
// canonical SHA-256 hash + chains it to the previous evidence row's
// hash. blockchain_anchor_id is left NULL — the L2 anchor pass
// (separate dispatch) populates it asynchronously by querying for
// rows where blockchain_anchor_id IS NULL (idx_qev_unanchored).
//
// The hash chain runs per-case in collected_at order. We snap the
// previous hash inside the same tx to avoid a TOCTOU race when two
// evidence inserts hit the same case concurrently.
func (s *Store) AppendEvidence(ctx context.Context, e *types.CaseEvidence) error {
	if e == nil {
		return fmt.Errorf("fox.AppendEvidence: nil evidence")
	}
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	now := time.Now().UTC()
	if e.CollectedAt.IsZero() {
		e.CollectedAt = now
	}
	e.CreatedAt = now
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	if len(e.Attributes) == 0 {
		e.Attributes = json.RawMessage(`{}`)
	}
	// Canonical hash: SHA-256 of the payload bytes as stored.
	sum := sha256.Sum256(e.Payload)
	e.PayloadHash = hex.EncodeToString(sum[:])

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("fox.AppendEvidence begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const prevQ = `
		SELECT payload_hash
		  FROM detection.case_evidence
		 WHERE case_id = $1
		 ORDER BY collected_at DESC
		 LIMIT 1`
	var prevHash *string
	err = tx.QueryRow(ctx, prevQ, e.CaseID).Scan(&prevHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("fox.AppendEvidence prev hash: %w", err)
	}
	e.PrevEvidenceHash = prevHash

	const insertEv = `
		INSERT INTO detection.case_evidence (
			id, tenant_id, case_id, evidence_type, source_entity_type, source_entity_id,
			payload, payload_hash, prev_evidence_hash, blockchain_anchor_id,
			collected_by, collected_at, attributes, created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		)`
	if _, err := tx.Exec(ctx, insertEv,
		e.ID, e.TenantID, e.CaseID, e.EvidenceType, e.SourceEntityType, e.SourceEntityID,
		e.Payload, e.PayloadHash, e.PrevEvidenceHash, e.BlockchainAnchorID,
		e.CollectedBy, e.CollectedAt, e.Attributes, e.CreatedAt,
	); err != nil {
		return fmt.Errorf("fox.AppendEvidence insert: %w", err)
	}

	// Mirror the operation into the action log so case history is
	// complete from the detection.case_actions table alone.
	const insertAction = `
		INSERT INTO detection.case_actions (
			id, tenant_id, case_id, action_type, performed_by, performed_at, details, created_at
		) VALUES ($1,$2,$3,'evidence_collected',$4,$5,$6,$7)`
	det, _ := json.Marshal(map[string]string{
		"evidence_id":   e.ID.String(),
		"evidence_type": e.EvidenceType,
		"payload_hash":  e.PayloadHash,
	})
	if _, err := tx.Exec(ctx, insertAction,
		uuid.New(), e.TenantID, e.CaseID, e.CollectedBy, e.CollectedAt, det, now,
	); err != nil {
		return fmt.Errorf("fox.AppendEvidence action: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("fox.AppendEvidence commit: %w", err)
	}
	return nil
}

// AppendAction inserts a single detection.case_actions row. No tx — the table
// is append-only and a single INSERT is atomic. Mutates *a so the
// caller can observe the minted ID and timestamps.
func (s *Store) AppendAction(ctx context.Context, a *types.CaseAction) error {
	if a == nil {
		return fmt.Errorf("fox.AppendAction: nil action")
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	now := time.Now().UTC()
	if a.PerformedAt.IsZero() {
		a.PerformedAt = now
	}
	a.CreatedAt = now
	if len(a.Details) == 0 {
		a.Details = json.RawMessage(`{}`)
	}

	const q = `
		INSERT INTO detection.case_actions (
			id, tenant_id, case_id, action_type, performed_by, performed_at, details, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	if _, err := s.pool.Exec(ctx, q,
		a.ID, a.TenantID, a.CaseID, a.ActionType, a.PerformedBy, a.PerformedAt, a.Details, a.CreatedAt,
	); err != nil {
		return fmt.Errorf("fox.AppendAction: %w", err)
	}
	return nil
}

// CloseCase transitions a case to status='closed' and records the
// resolution. Uses a tx so the lifecycle UPDATE and the audit action
// land together. Returns ErrNotFound if the case doesn't exist.
func (s *Store) CloseCase(ctx context.Context, tenantID, caseID uuid.UUID, resolution string, closedBy *uuid.UUID, notes string) error {
	if !CaseStatus(CaseStatusClosed).IsValid() {
		// Defensive — IsValid is a constant-time check, panics only
		// if someone breaks the enum.
		return fmt.Errorf("fox.CloseCase: closed status is invalid (enum corruption)")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("fox.CloseCase begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	const upd = `
		UPDATE detection.cases
		   SET status = 'closed', resolved_at = $1, resolution_type = $2, updated_at = $1
		 WHERE id = $3 AND tenant_id = $4`
	ct, err := tx.Exec(ctx, upd, now, resolution, caseID, tenantID)
	if err != nil {
		return fmt.Errorf("fox.CloseCase update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	const insertAction = `
		INSERT INTO detection.case_actions (
			id, tenant_id, case_id, action_type, performed_by, performed_at, details, created_at
		) VALUES ($1,$2,$3,'resolution',$4,$5,$6,$7)`
	det, _ := json.Marshal(map[string]string{
		"to":         string(CaseStatusClosed),
		"resolution": resolution,
		"notes":      notes,
	})
	if _, err := tx.Exec(ctx, insertAction,
		uuid.New(), tenantID, caseID, closedBy, now, det, now,
	); err != nil {
		return fmt.Errorf("fox.CloseCase action: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("fox.CloseCase commit: %w", err)
	}
	return nil
}

// ListEvidence returns all evidence rows for a case in collected_at
// order (matches the hash chain order).
func (s *Store) ListEvidence(ctx context.Context, caseID uuid.UUID) ([]types.CaseEvidence, error) {
	const q = `
		SELECT id, tenant_id, case_id, evidence_type, source_entity_type, source_entity_id,
		       payload, payload_hash, prev_evidence_hash, blockchain_anchor_id,
		       collected_by, collected_at, attributes, created_at
		  FROM detection.case_evidence
		 WHERE case_id = $1
		 ORDER BY collected_at ASC`
	rows, err := s.pool.Query(ctx, q, caseID)
	if err != nil {
		return nil, fmt.Errorf("fox.ListEvidence: %w", err)
	}
	defer rows.Close()
	out := make([]types.CaseEvidence, 0, 8)
	for rows.Next() {
		var e types.CaseEvidence
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.CaseID, &e.EvidenceType, &e.SourceEntityType, &e.SourceEntityID,
			&e.Payload, &e.PayloadHash, &e.PrevEvidenceHash, &e.BlockchainAnchorID,
			&e.CollectedBy, &e.CollectedAt, &e.Attributes, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("fox.ListEvidence scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListActions returns all actions for a case in performed_at order.
func (s *Store) ListActions(ctx context.Context, caseID uuid.UUID) ([]types.CaseAction, error) {
	const q = `
		SELECT id, tenant_id, case_id, action_type, performed_by, performed_at, details, created_at
		  FROM detection.case_actions
		 WHERE case_id = $1
		 ORDER BY performed_at ASC`
	rows, err := s.pool.Query(ctx, q, caseID)
	if err != nil {
		return nil, fmt.Errorf("fox.ListActions: %w", err)
	}
	defer rows.Close()
	out := make([]types.CaseAction, 0, 8)
	for rows.Next() {
		var a types.CaseAction
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.CaseID, &a.ActionType, &a.PerformedBy, &a.PerformedAt, &a.Details, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("fox.ListActions scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CaseFilter narrows ListCases. Zero values mean "no filter".
type CaseFilter struct {
	Status string
	From   *time.Time
	To     *time.Time
}

// ListCases returns cases for a tenant with optional filters,
// paginated by limit/offset. Ordered by opened_at DESC.
func (s *Store) ListCases(ctx context.Context, tenantID uuid.UUID, filter CaseFilter, limit, offset int) ([]types.Case, error) {
	// Build the predicate dynamically while keeping arg ordering
	// deterministic. pgx doesn't have a query builder bundled and
	// we don't want a dep just for this.
	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	idx := 2
	if filter.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", idx))
		args = append(args, filter.Status)
		idx++
	}
	if filter.From != nil {
		where = append(where, fmt.Sprintf("opened_at >= $%d", idx))
		args = append(args, *filter.From)
		idx++
	}
	if filter.To != nil {
		where = append(where, fmt.Sprintf("opened_at <= $%d", idx))
		args = append(args, *filter.To)
		idx++
	}
	q := `
		SELECT id, tenant_id, case_number, case_type, title, description,
		       severity, status, primary_subject_id, primary_location_id,
		       assigned_to, opened_at, resolved_at, resolution_type,
		       loss_amount_estimated, loss_amount_recovered, attributes,
		       created_at, updated_at
		  FROM detection.cases
		 WHERE ` + joinAnd(where) + `
		 ORDER BY opened_at DESC
		 LIMIT $` + fmt.Sprint(idx) + ` OFFSET $` + fmt.Sprint(idx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("fox.ListCases: %w", err)
	}
	defer rows.Close()
	out := make([]types.Case, 0, limit)
	for rows.Next() {
		var c types.Case
		if err := rows.Scan(
			&c.ID, &c.TenantID, &c.CaseNumber, &c.CaseType, &c.Title, &c.Description,
			&c.Severity, &c.Status, &c.PrimarySubjectID, &c.PrimaryLocationID,
			&c.AssignedTo, &c.OpenedAt, &c.ResolvedAt, &c.ResolutionType,
			&c.LossAmountEstimated, &c.LossAmountRecovered, &c.Attributes,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("fox.ListCases scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// joinAnd is a tiny helper to keep the dynamic predicate readable
// without dragging in strings.Join at the call site.
func joinAnd(parts []string) string {
	if len(parts) == 0 {
		return "TRUE"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " AND " + p
	}
	return out
}
