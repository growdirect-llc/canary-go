// internal/returns/store.go
//
// pgx-backed returns store. Reads transaction.transactions WHERE transaction_type =
// 'return'. The FraudFlag write inserts a detection.detections row — it does NOT
// update the transaction; the detection drives downstream alert lifecycle.
//
//

package returns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed returns access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var ErrNotFound = errors.New("returns: not found")

// List returns return transactions matching filters.
func (s *Store) List(ctx context.Context, f ListFilters) ([]ReturnTransaction, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID, f.From, f.To}
	q := `
		SELECT
		    tx.id, tx.tenant_id, tx.transaction_number,
		    tx.parent_transaction_id, tx.location_id,
		    tx.cashier_employee_id, tx.customer_id,
		    tx.business_date, tx.ended_at, tx.status,
		    COALESCE(SUM(ABS(li.line_total)), 0) AS total_amount,
		    COUNT(li.id) AS line_count
		FROM transaction.transactions tx
		LEFT JOIN transaction.transaction_line_items li ON li.transaction_id = tx.id
		WHERE tx.tenant_id = $1
		  AND tx.transaction_type = 'return'
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date`

	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		q += fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}
	if f.CustomerID != nil {
		args = append(args, *f.CustomerID)
		q += fmt.Sprintf(" AND tx.customer_id = $%d", len(args))
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(
		` GROUP BY tx.id ORDER BY tx.ended_at DESC LIMIT $%d OFFSET $%d`,
		len(args)-1, len(args),
	)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("returns: list: %w", err)
	}
	defer rows.Close()
	out := make([]ReturnTransaction, 0, f.Limit)
	for rows.Next() {
		var r ReturnTransaction
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.TransactionNumber,
			&r.ParentTransactionID, &r.LocationID,
			&r.CashierEmployeeID, &r.CustomerID,
			&r.BusinessDate, &r.EndedAt, &r.Status,
			&r.TotalAmount, &r.LineCount,
		); err != nil {
			return nil, fmt.Errorf("returns: list scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetByID returns a return transaction with its line items.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*ReturnDetail, error) {
	const txQ = `
		SELECT
		    tx.id, tx.tenant_id, tx.transaction_number,
		    tx.parent_transaction_id, tx.location_id,
		    tx.cashier_employee_id, tx.customer_id,
		    tx.business_date, tx.ended_at, tx.status,
		    COALESCE(SUM(ABS(li.line_total)), 0) AS total_amount,
		    COUNT(li.id) AS line_count
		FROM transaction.transactions tx
		LEFT JOIN transaction.transaction_line_items li ON li.transaction_id = tx.id
		WHERE tx.tenant_id = $1 AND tx.id = $2 AND tx.transaction_type = 'return'
		GROUP BY tx.id`
	row := s.pool.QueryRow(ctx, txQ, tenantID, id)
	var d ReturnDetail
	if err := row.Scan(
		&d.ID, &d.TenantID, &d.TransactionNumber,
		&d.ParentTransactionID, &d.LocationID,
		&d.CashierEmployeeID, &d.CustomerID,
		&d.BusinessDate, &d.EndedAt, &d.Status,
		&d.TotalAmount, &d.LineCount,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("returns: get: %w", err)
	}

	const lineQ = `
		SELECT id, item_id, description, quantity, unit_price, line_total, return_reason
		FROM transaction.transaction_line_items
		WHERE transaction_id = $1
		ORDER BY line_number`
	lrows, err := s.pool.Query(ctx, lineQ, id)
	if err != nil {
		return nil, fmt.Errorf("returns: get lines: %w", err)
	}
	defer lrows.Close()
	for lrows.Next() {
		var l ReturnLine
		if err := lrows.Scan(
			&l.ID, &l.ItemID, &l.Description,
			&l.Quantity, &l.UnitPrice, &l.LineTotal, &l.ReturnReason,
		); err != nil {
			return nil, fmt.Errorf("returns: get lines scan: %w", err)
		}
		d.Lines = append(d.Lines, l)
	}
	return &d, lrows.Err()
}

// Summary returns aggregate return statistics for a date range.
func (s *Store) Summary(ctx context.Context, tenantID uuid.UUID, from, to time.Time, locationID *uuid.UUID) (*SummaryStats, error) {
	args := []any{tenantID, from, to}
	locClause := ""
	if locationID != nil {
		args = append(args, *locationID)
		locClause = fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}
	q := `
		SELECT
		    COUNT(DISTINCT tx.id) AS return_count,
		    COALESCE(SUM(ABS(li.line_total)), 0) AS total_amount,
		    COUNT(DISTINCT tx.customer_id) FILTER (WHERE tx.customer_id IS NOT NULL) AS unique_customers
		FROM transaction.transactions tx
		LEFT JOIN transaction.transaction_line_items li ON li.transaction_id = tx.id
		WHERE tx.tenant_id = $1
		  AND tx.transaction_type = 'return'
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date` + locClause
	row := s.pool.QueryRow(ctx, q, args...)
	ss := &SummaryStats{
		TenantID:   tenantID,
		LocationID: locationID,
		From:       from,
		To:         to,
	}
	if err := row.Scan(&ss.ReturnCount, &ss.TotalAmount, &ss.UniqueCustomers); err != nil {
		return nil, fmt.Errorf("returns: summary: %w", err)
	}
	if ss.ReturnCount > 0 {
		ss.AvgReturnAmt = ss.TotalAmount / float64(ss.ReturnCount)
	}
	return ss, nil
}

// FraudFlag inserts a detection.detections row pointing at the return transaction.
// The detection drives the alert lifecycle (ack/resolve/suppress) in the
// alert service.
func (s *Store) FraudFlag(ctx context.Context, tenantID, transactionID uuid.UUID, req FraudFlagRequest) (*FraudFlagResponse, error) {
	// Verify transaction exists and belongs to this tenant.
	const checkQ = `
		SELECT location_id, cashier_employee_id, customer_id
		FROM transaction.transactions
		WHERE tenant_id = $1 AND id = $2 AND transaction_type = 'return'`
	var locID uuid.UUID
	var cashierID, custID *uuid.UUID
	row := s.pool.QueryRow(ctx, checkQ, tenantID, transactionID)
	if err := row.Scan(&locID, &cashierID, &custID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("returns: fraud flag check: %w", err)
	}

	now := time.Now().UTC()
	const insertQ = `
		INSERT INTO detection.detections
		    (tenant_id, rule_id, source_entity_type, source_entity_id,
		     location_id, cashier_employee_id, customer_id,
		     severity, signal_strength, status, detected_at)
		VALUES ($1, $2, 'transaction', $3, $4, $5, $6, $7, 0.8, 'new', $8)
		RETURNING id`
	var detID uuid.UUID
	if err := s.pool.QueryRow(ctx, insertQ,
		tenantID, req.DetectionRuleID, transactionID,
		locID, cashierID, custID,
		req.Severity, now,
	).Scan(&detID); err != nil {
		return nil, fmt.Errorf("returns: fraud flag insert: %w", err)
	}
	return &FraudFlagResponse{
		DetectionID:   detID,
		TransactionID: transactionID,
		Severity:      req.Severity,
		CreatedAt:     now,
	}, nil
}

// parseDateRange falls back to last 30 days if inputs are absent.
func parseDateRange(fromStr, toStr string) (time.Time, time.Time) {
	const layout = "2006-01-02"
	to := time.Now().UTC().Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, -30)
	if t, err := time.Parse(layout, toStr); err == nil {
		to = t.UTC()
	}
	if t, err := time.Parse(layout, fromStr); err == nil {
		from = t.UTC()
	}
	return from, to
}
