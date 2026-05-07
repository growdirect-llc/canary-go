// internal/billing/store.go
//
// pgxpool-backed access to ledger.l402_otb_budgets +
// ledger.ildwac_positions. Spec: GRO-765 Phase A.

package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed access layer for Bull.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Sentinels mapped to HTTP status codes by the handler.
var (
	ErrNotFound       = errors.New("billing: not found")
	ErrValidation     = errors.New("billing: validation failed")
	ErrHardLimitHit   = errors.New("billing: hard limit reached")
	ErrConflict       = errors.New("billing: conflict")
)

// CreateBudget inserts a new OTB budget row.
func (s *Store) CreateBudget(ctx context.Context, req CreateBudgetRequest) (*OTBBudget, error) {
	if req.TenantID == uuid.Nil {
		return nil, fmt.Errorf("%w: tenant_id required", ErrValidation)
	}
	if req.ScopeType == "" {
		return nil, fmt.Errorf("%w: scope_type required", ErrValidation)
	}
	if req.BudgetSatoshis <= 0 {
		return nil, fmt.Errorf("%w: budget_satoshis must be positive", ErrValidation)
	}

	const q = `
		INSERT INTO ledger.l402_otb_budgets (
			tenant_id, budget_period, scope_type, scope_id,
			budget_satoshis, hard_limit, alert_threshold_pct, status
		) VALUES (
			$1, tstzrange($2, $3, '[)'), $4, $5,
			$6, $7, $8, 'active'
		)
		RETURNING ` + budgetSelectColumns
	row := s.pool.QueryRow(ctx, q,
		req.TenantID,
		req.BudgetPeriodStart,
		req.BudgetPeriodEnd,
		req.ScopeType,
		req.ScopeID,
		req.BudgetSatoshis,
		req.HardLimit,
		req.AlertThresholdPct,
	)
	return scanBudget(row)
}

// GetBudget returns a budget by id.
func (s *Store) GetBudget(ctx context.Context, tenantID, id uuid.UUID) (*OTBBudget, error) {
	const q = `SELECT ` + budgetSelectColumns +
		` FROM ledger.l402_otb_budgets WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	out, err := scanBudget(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("billing: get budget: %w", err)
	}
	return out, nil
}

// Budget status discriminators stored in ledger.l402_otb_budgets.status.
const (
	BudgetStatusActive   = "active"
	BudgetStatusLocked   = "locked"
	BudgetStatusDepleted = "depleted"
	BudgetStatusExpired  = "expired"
)

// UpdateBudgetStatus flips a budget's status. Used by the OTB report
// "lock period" action (W5 / GRO-824) — operator-initiated active ↔
// locked toggle. Returns ErrNotFound when no row matches.
func (s *Store) UpdateBudgetStatus(ctx context.Context, tenantID, id uuid.UUID, status string) (*OTBBudget, error) {
	const q = `
		UPDATE ledger.l402_otb_budgets
		   SET status = $1, updated_at = NOW()
		 WHERE tenant_id = $2 AND id = $3
		RETURNING ` + budgetSelectColumns
	row := s.pool.QueryRow(ctx, q, status, tenantID, id)
	out, err := scanBudget(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("billing: update budget status: %w", err)
	}
	return out, nil
}

// ListBudgets returns active budgets for a tenant, ordered by
// budget_period DESC.
func (s *Store) ListBudgets(ctx context.Context, tenantID uuid.UUID, status string) ([]OTBBudget, error) {
	args := []any{tenantID}
	q := `SELECT ` + budgetSelectColumns +
		` FROM ledger.l402_otb_budgets WHERE tenant_id = $1`
	if status != "" {
		args = append(args, status)
		q += " AND status = $2"
	}
	q += " ORDER BY lower(budget_period) DESC"
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("billing: list budgets: %w", err)
	}
	defer rows.Close()
	var out []OTBBudget
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Consume increments consumed_satoshis on a budget row. When
// hard_limit=true and the increment would exceed budget_satoshis, the
// row is left unchanged and ErrHardLimitHit is returned. Soft-limit
// budgets always permit the increment (alerting handled by the alert
// rail in Wave D).
func (s *Store) Consume(ctx context.Context, tenantID, budgetID uuid.UUID, satoshis int64) (*OTBBudget, error) {
	if satoshis < 0 {
		return nil, fmt.Errorf("%w: satoshis must be non-negative", ErrValidation)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("billing: consume begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// SELECT … FOR UPDATE to serialize concurrent consumes against the
	// same budget row.
	var (
		current        OTBBudget
		periodStart    time.Time
		periodEnd      *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, lower(budget_period), upper(budget_period),
		       scope_type, scope_id,
		       budget_satoshis, consumed_satoshis, remaining_satoshis,
		       hard_limit, alert_threshold_pct, status,
		       created_at, updated_at
		  FROM ledger.l402_otb_budgets
		 WHERE tenant_id = $1 AND id = $2
		   FOR UPDATE`,
		tenantID, budgetID,
	).Scan(
		&current.ID, &current.TenantID, &periodStart, &periodEnd,
		&current.ScopeType, &current.ScopeID,
		&current.BudgetSatoshis, &current.ConsumedSatoshis, &current.RemainingSatoshis,
		&current.HardLimit, &current.AlertThresholdPct, &current.Status,
		&current.CreatedAt, &current.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("billing: consume select: %w", err)
	}
	current.BudgetPeriodStart = periodStart
	current.BudgetPeriodEnd = periodEnd

	if current.HardLimit && current.ConsumedSatoshis+satoshis > current.BudgetSatoshis {
		return nil, fmt.Errorf("%w: budget=%d, consumed=%d, requested=%d",
			ErrHardLimitHit, current.BudgetSatoshis, current.ConsumedSatoshis, satoshis)
	}

	const updateQ = `
		UPDATE ledger.l402_otb_budgets
		   SET consumed_satoshis = consumed_satoshis + $2,
		       updated_at = now()
		 WHERE id = $1
		RETURNING ` + budgetSelectColumns
	row := tx.QueryRow(ctx, updateQ, budgetID, satoshis)
	out, err := scanBudget(row)
	if err != nil {
		return nil, fmt.Errorf("billing: consume update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: consume commit: %w", err)
	}
	return out, nil
}

// CostRollup aggregates ledger.ildwac_positions for a (tenant, period,
// cadence_step) into the satoshi cost rollup primitive.
func (s *Store) CostRollup(ctx context.Context, req CostRollupRequest) (*CostRollup, error) {
	args := []any{req.TenantID, req.PeriodStart, req.PeriodEnd}
	q := `
		SELECT
		  COALESCE(SUM(l_storage_satoshis), 0)   AS storage,
		  COALESCE(SUM(w_workload_satoshis), 0)  AS workload,
		  COALESCE(SUM(c_capture_satoshis), 0)   AS capture,
		  COALESCE(SUM(total_satoshis), 0)       AS total,
		  COALESCE(SUM(bytes_under_management), 0) AS bytes,
		  COALESCE(SUM(workload_units), 0)       AS units,
		  COUNT(*)                               AS positions,
		  COUNT(*) FILTER (WHERE invoiced_at IS NULL) AS unbilled,
		  MIN(lower(position_period)) FILTER (WHERE invoiced_at IS NULL) AS oldest_unbilled
		  FROM ledger.ildwac_positions
		 WHERE tenant_id = $1
		   AND lower(position_period) >= $2
		   AND lower(position_period) <  $3`
	if req.CadenceStep != "" {
		args = append(args, req.CadenceStep)
		q += " AND cadence_step = $4"
	}

	var out CostRollup
	out.TenantID = req.TenantID
	out.PeriodStart = req.PeriodStart
	out.PeriodEnd = req.PeriodEnd
	out.CadenceStep = req.CadenceStep
	if err := s.pool.QueryRow(ctx, q, args...).Scan(
		&out.StorageSatoshis, &out.WorkloadSatoshis, &out.CaptureSatoshis,
		&out.TotalSatoshis, &out.BytesUnderManagement, &out.WorkloadUnits,
		&out.PositionCount, &out.UnbilledCount, &out.OldestUnbilled,
	); err != nil {
		return nil, fmt.Errorf("billing: cost rollup: %w", err)
	}
	return &out, nil
}

// budgetSelectColumns is the canonical column list for OTB reads;
// keeps every read site lined up with scanBudget.
const budgetSelectColumns = `id, tenant_id,
lower(budget_period) AS period_start, upper(budget_period) AS period_end,
scope_type, scope_id,
budget_satoshis, consumed_satoshis, remaining_satoshis,
hard_limit, alert_threshold_pct, status,
created_at, updated_at`

type scannable interface {
	Scan(dest ...any) error
}

func scanBudget(r scannable) (*OTBBudget, error) {
	var b OTBBudget
	var periodEnd *time.Time
	if err := r.Scan(
		&b.ID, &b.TenantID, &b.BudgetPeriodStart, &periodEnd,
		&b.ScopeType, &b.ScopeID,
		&b.BudgetSatoshis, &b.ConsumedSatoshis, &b.RemainingSatoshis,
		&b.HardLimit, &b.AlertThresholdPct, &b.Status,
		&b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	b.BudgetPeriodEnd = periodEnd
	return &b, nil
}
