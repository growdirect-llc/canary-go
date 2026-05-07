// internal/analytics/store.go
//
// pgx-backed analytics store. All queries are read-only aggregations over
// the t.* and i.* schemas. No mutations.
//
//

package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the read-only analytics data layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// SalesSummary aggregates transaction-level revenue over a date range.
func (s *Store) SalesSummary(ctx context.Context, f DateRangeFilter) (*SalesSummary, error) {
	args := []any{f.TenantID, f.From, f.To}
	locClause := ""
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		locClause = fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}

	q := `
		SELECT
		    COUNT(*) FILTER (WHERE tx.transaction_type = 'sale')                       AS transaction_count,
		    COALESCE(SUM(li.line_total) FILTER (WHERE tx.transaction_type = 'sale'), 0) AS total_revenue,
		    COALESCE(SUM(li.extended_tax) FILTER (WHERE tx.transaction_type = 'sale'), 0) AS total_tax,
		    COALESCE(SUM(li.unit_discount * li.quantity) FILTER (WHERE tx.transaction_type = 'sale'), 0) AS total_discount,
		    COALESCE(SUM(li.quantity) FILTER (WHERE tx.transaction_type = 'sale' AND li.quantity > 0), 0) AS total_items,
		    COUNT(*) FILTER (WHERE tx.transaction_type = 'return')                     AS return_count,
		    COALESCE(SUM(ABS(li.line_total)) FILTER (WHERE tx.transaction_type = 'return'), 0) AS return_revenue
		FROM transaction.transactions tx
		JOIN transaction.transaction_line_items li ON li.transaction_id = tx.id
		WHERE tx.tenant_id = $1
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date
		  AND tx.status = 'completed'` + locClause

	row := s.pool.QueryRow(ctx, q, args...)
	var r SalesSummary
	r.TenantID = f.TenantID
	r.LocationID = f.LocationID
	r.From = f.From
	r.To = f.To
	err := row.Scan(
		&r.TransactionCount,
		&r.TotalRevenue,
		&r.TotalTax,
		&r.TotalDiscount,
		&r.TotalItems,
		&r.ReturnCount,
		&r.ReturnRevenue,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: sales summary: %w", err)
	}
	if r.TransactionCount > 0 {
		r.AvgTicket = r.TotalRevenue / float64(r.TransactionCount)
	}
	return &r, nil
}

// BasketMetrics computes per-ticket composition and tender mix.
func (s *Store) BasketMetrics(ctx context.Context, f DateRangeFilter) (*BasketMetrics, error) {
	args := []any{f.TenantID, f.From, f.To}
	locClause := ""
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		locClause = fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}

	// Avg items + avg ticket value
	headerQ := `
		SELECT
		    COALESCE(AVG(item_count), 0),
		    COALESCE(AVG(ticket_total), 0)
		FROM (
		    SELECT tx.id,
		           COUNT(li.id) AS item_count,
		           SUM(li.line_total) AS ticket_total
		    FROM transaction.transactions tx
		    JOIN transaction.transaction_line_items li ON li.transaction_id = tx.id
		    WHERE tx.tenant_id = $1
		      AND tx.business_date >= $2::date
		      AND tx.business_date <= $3::date
		      AND tx.transaction_type = 'sale'
		      AND tx.status = 'completed'` + locClause + `
		    GROUP BY tx.id
		) sub`
	row := s.pool.QueryRow(ctx, headerQ, args...)
	bm := &BasketMetrics{
		TenantID: f.TenantID,
		From:     f.From,
		To:       f.To,
	}
	if err := row.Scan(&bm.AvgItemsPerTicket, &bm.AvgTicketValue); err != nil {
		return nil, fmt.Errorf("analytics: basket header: %w", err)
	}

	// Tender mix — same date/location gate
	tenderArgs := []any{f.TenantID, f.From, f.To}
	tenderLoc := ""
	if f.LocationID != nil {
		tenderArgs = append(tenderArgs, *f.LocationID)
		tenderLoc = fmt.Sprintf(" AND tx.location_id = $%d", len(tenderArgs))
	}
	tenderQ := `
		SELECT
		    tt.tender_type,
		    COUNT(*) AS cnt,
		    COALESCE(SUM(tt.amount), 0) AS total_amount
		FROM transaction.transaction_tenders tt
		JOIN transaction.transactions tx ON tx.id = tt.transaction_id
		WHERE tx.tenant_id = $1
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date
		  AND tx.transaction_type = 'sale'
		  AND tx.status = 'completed'` + tenderLoc + `
		GROUP BY tt.tender_type
		ORDER BY total_amount DESC`
	rows, err := s.pool.Query(ctx, tenderQ, tenderArgs...)
	if err != nil {
		return nil, fmt.Errorf("analytics: basket tender: %w", err)
	}
	defer rows.Close()
	var grandTotal float64
	var shares []TenderShare
	for rows.Next() {
		var ts TenderShare
		if err := rows.Scan(&ts.TenderType, &ts.Count, &ts.TotalAmount); err != nil {
			return nil, fmt.Errorf("analytics: basket tender scan: %w", err)
		}
		grandTotal += ts.TotalAmount
		shares = append(shares, ts)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("analytics: basket tender rows: %w", rows.Err())
	}
	for i := range shares {
		if grandTotal > 0 {
			shares[i].SharePct = shares[i].TotalAmount / grandTotal * 100
		}
	}
	bm.TenderMix = shares
	return bm, nil
}

// CohortRows returns monthly customer new/returning breakdowns.
func (s *Store) CohortRows(ctx context.Context, f DateRangeFilter) ([]CohortRow, error) {
	args := []any{f.TenantID, f.From, f.To}
	q := `
		WITH tx_customers AS (
		    SELECT DISTINCT
		        TO_CHAR(DATE_TRUNC('month', tx.business_date), 'YYYY-MM') AS period,
		        tx.customer_id,
		        MIN(first_seen.first_date) AS first_date
		    FROM transaction.transactions tx
		    JOIN (
		        SELECT customer_id, MIN(business_date) AS first_date
		        FROM transaction.transactions
		        WHERE tenant_id = $1
		          AND customer_id IS NOT NULL
		        GROUP BY customer_id
		    ) first_seen ON first_seen.customer_id = tx.customer_id
		    WHERE tx.tenant_id = $1
		      AND tx.business_date >= $2::date
		      AND tx.business_date <= $3::date
		      AND tx.customer_id IS NOT NULL
		      AND tx.transaction_type = 'sale'
		      AND tx.status = 'completed'
		    GROUP BY period, tx.customer_id, first_seen.first_date
		)
		SELECT
		    period,
		    COUNT(*) FILTER (WHERE DATE_TRUNC('month', first_date) = DATE_TRUNC('month', period::date)) AS new_customers,
		    COUNT(*) FILTER (WHERE DATE_TRUNC('month', first_date) < DATE_TRUNC('month', period::date))  AS return_customers,
		    COUNT(*) AS total_customers
		FROM tx_customers
		GROUP BY period
		ORDER BY period`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: cohort: %w", err)
	}
	defer rows.Close()
	var out []CohortRow
	for rows.Next() {
		var r CohortRow
		if err := rows.Scan(&r.Period, &r.NewCustomers, &r.ReturnCustomers, &r.TotalCustomers); err != nil {
			return nil, fmt.Errorf("analytics: cohort scan: %w", err)
		}
		if r.TotalCustomers > 0 {
			r.RetentionRate = float64(r.ReturnCustomers) / float64(r.TotalCustomers) * 100
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VelocityItems returns top-N items by quantity sold in the window.
func (s *Store) VelocityItems(ctx context.Context, f DateRangeFilter) ([]VelocityItem, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{f.TenantID, f.From, f.To}
	locClause := ""
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		locClause = fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}
	args = append(args, limit)
	q := `
		SELECT
		    li.item_id,
		    MAX(li.description) AS description,
		    MAX(li.category_id) AS category_id,
		    SUM(li.quantity)    AS total_qty,
		    SUM(li.line_total)  AS total_rev,
		    COUNT(DISTINCT li.transaction_id) AS tx_count
		FROM transaction.transaction_line_items li
		JOIN transaction.transactions tx ON tx.id = li.transaction_id
		WHERE tx.tenant_id = $1
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date
		  AND tx.transaction_type = 'sale'
		  AND tx.status = 'completed'
		  AND li.item_id IS NOT NULL
		  AND li.quantity > 0` + locClause + `
		GROUP BY li.item_id
		ORDER BY total_qty DESC
		LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: velocity: %w", err)
	}
	defer rows.Close()
	var out []VelocityItem
	for rows.Next() {
		var v VelocityItem
		if err := rows.Scan(&v.ItemID, &v.Description, &v.CategoryID, &v.TotalQty, &v.TotalRev, &v.TxCount); err != nil {
			return nil, fmt.Errorf("analytics: velocity scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ShrinkSummary tallies return/void/unknown-scan indicators.
func (s *Store) ShrinkSummary(ctx context.Context, f DateRangeFilter) (*ShrinkSummary, error) {
	args := []any{f.TenantID, f.From, f.To}
	locClause := ""
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		locClause = fmt.Sprintf(" AND tx.location_id = $%d", len(args))
	}
	q := `
		SELECT
		    COUNT(*) FILTER (WHERE tx.transaction_type = 'return')                              AS return_count,
		    COALESCE(SUM(ABS(li.line_total)) FILTER (WHERE tx.transaction_type = 'return'), 0)  AS return_revenue,
		    COUNT(*) FILTER (WHERE li.is_void = true)                                            AS void_count,
		    COALESCE(SUM(ABS(li.line_total)) FILTER (WHERE li.is_void = true), 0)               AS void_revenue,
		    COUNT(*) FILTER (WHERE li.item_id IS NULL)                                           AS unknown_scan_count,
		    COALESCE(SUM(li.line_total) FILTER (WHERE tx.transaction_type = 'sale' AND NOT li.is_void), 0) AS gross_revenue
		FROM transaction.transaction_line_items li
		JOIN transaction.transactions tx ON tx.id = li.transaction_id
		WHERE tx.tenant_id = $1
		  AND tx.business_date >= $2::date
		  AND tx.business_date <= $3::date
		  AND tx.status = 'completed'` + locClause
	row := s.pool.QueryRow(ctx, q, args...)
	ss := &ShrinkSummary{
		TenantID:   f.TenantID,
		LocationID: f.LocationID,
		From:       f.From,
		To:         f.To,
	}
	var grossRevenue float64
	err := row.Scan(
		&ss.ReturnCount,
		&ss.ReturnRevenue,
		&ss.VoidCount,
		&ss.VoidRevenue,
		&ss.UnknownScanCount,
		&grossRevenue,
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: shrink: %w", err)
	}
	if grossRevenue > 0 {
		ss.ShrinkRate = (ss.ReturnRevenue + ss.VoidRevenue) / grossRevenue * 100
	}
	return ss, nil
}

// parseDateRange parses ?from= and ?to= query params. Falls back to
// last 30 days if absent or malformed.
func parseDateRange(fromStr, toStr string) (time.Time, time.Time) {
	layout := "2006-01-02"
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
