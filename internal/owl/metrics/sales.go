// Package metrics holds the raw-SQL query helpers for Owl's read-only
// dashboard. Each file groups one metric family (sales, cases,
// exposure). Helpers take a pgxpool.Pool, a tenant_id, and a period
// window — returning DTO types from the parent owl package.
//
// SQL dialect: PostgreSQL 17. Raw SQL per override
// (CanaryGo CLAUDE.md mandates sqlc; dispatch suspends that rule
// because Wave 1 types are hand-written and the dashboard surface is
// still moving fast — sqlc retrofit is Loop 3).
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/owl/dtotypes"
)

// SalesSummary aggregates transaction.transactions over [from, to).
//
// Sales vs refunds: transaction.transactions.transaction_type carries 'sale'
// (default) or 'refund'. We treat refunds as their own population —
// counted separately, deducted from net.
//
// Money: numeric(14,4) → string. Postgres does the addition; we never
// touch a float on the way out.
//
// SDD-vague: ops-dashboard.md doesn't pin "net sales" formula. Owl
// uses: net = gross - refunds - discount_total. A merchant could
// reasonably want net = gross - refunds (because discount is already
// netted in line item extended_price). We pick the more conservative
// reading; comment on the field stays the source of truth.
func SalesSummary(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) (dtotypes.SalesSummary, error) {
	const q = `
		SELECT
		  COALESCE(SUM(grand_total)    FILTER (WHERE transaction_type = 'sale'),   0)::text AS gross_sales,
		  COALESCE(SUM(grand_total)    FILTER (WHERE transaction_type = 'refund'), 0)::text AS refund_total,
		  COALESCE(SUM(discount_total) FILTER (WHERE transaction_type = 'sale'),   0)::text AS discount_total,
		  COALESCE(SUM(tax_total)      FILTER (WHERE transaction_type = 'sale'),   0)::text AS tax_total,
		  COUNT(*)                     FILTER (WHERE transaction_type = 'sale')              AS sale_count,
		  COUNT(*)                     FILTER (WHERE transaction_type = 'refund')            AS refund_count,
		  COALESCE(
		    SUM(grand_total) FILTER (WHERE transaction_type = 'sale')
		      - SUM(grand_total) FILTER (WHERE transaction_type = 'refund')
		      - SUM(discount_total) FILTER (WHERE transaction_type = 'sale'),
		    0
		  )::text AS net_sales,
		  CASE
		    WHEN COUNT(*) FILTER (WHERE transaction_type = 'sale') > 0
		    THEN (SUM(grand_total) FILTER (WHERE transaction_type = 'sale')
		          / COUNT(*) FILTER (WHERE transaction_type = 'sale'))::text
		    ELSE '0'
		  END AS average_ticket
		FROM transaction.transactions
		WHERE tenant_id = $1
		  AND started_at >= $2
		  AND started_at <  $3
		  AND status = 'completed'
	`
	var s dtotypes.SalesSummary
	err := pool.QueryRow(ctx, q, tenantID, from, to).Scan(
		&s.GrossSales,
		&s.RefundTotal,
		&s.DiscountTotal,
		&s.TaxTotal,
		&s.TransactionCount,
		&s.RefundCount,
		&s.NetSales,
		&s.AverageTicket,
	)
	if err != nil {
		return dtotypes.SalesSummary{}, fmt.Errorf("metrics.SalesSummary: %w", err)
	}
	return s, nil
}

// TopItemsByUnits returns the top-N items ranked by sum(quantity)
// across non-void, non-return line items in the period.
//
// SDD-missing: transaction.transaction_line_items has no `is_void` index that
// pairs with the time filter on transaction.transactions. Postgres will use
// idx_lines_tx for the join + filter; for >1M-line periods this would
// want a `(tenant_id, created_at, is_void)` partial index. Flagging
// as a future-Loop tuning item, not a blocker.
//
// SDD-vague: items returned/voided don't reverse units sold. We
// honor `is_void = false AND is_return = false` so the rankings
// reflect real take-home velocity.
func TopItemsByUnits(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time, limit int) ([]dtotypes.ItemMetric, error) {
	const q = `
		SELECT
		  li.item_id,
		  i.sku,
		  i.description,
		  COALESCE(SUM(li.quantity),       0)::text AS units,
		  COALESCE(SUM(li.extended_price), 0)::text AS revenue
		FROM transaction.transaction_line_items li
		JOIN transaction.transactions       tx ON tx.id = li.transaction_id
		JOIN catalog.items              i  ON i.id  = li.item_id
		WHERE tx.tenant_id = $1
		  AND tx.started_at >= $2
		  AND tx.started_at <  $3
		  AND tx.status = 'completed'
		  AND tx.transaction_type = 'sale'
		  AND li.is_void   = false
		  AND li.is_return = false
		  AND li.item_id IS NOT NULL
		GROUP BY li.item_id, i.sku, i.description
		ORDER BY SUM(li.quantity) DESC NULLS LAST
		LIMIT $4
	`
	return scanItemMetrics(ctx, pool, q, tenantID, from, to, limit)
}

// TopItemsByRevenue ranks by sum(extended_price) instead.
func TopItemsByRevenue(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time, limit int) ([]dtotypes.ItemMetric, error) {
	const q = `
		SELECT
		  li.item_id,
		  i.sku,
		  i.description,
		  COALESCE(SUM(li.quantity),       0)::text AS units,
		  COALESCE(SUM(li.extended_price), 0)::text AS revenue
		FROM transaction.transaction_line_items li
		JOIN transaction.transactions       tx ON tx.id = li.transaction_id
		JOIN catalog.items              i  ON i.id  = li.item_id
		WHERE tx.tenant_id = $1
		  AND tx.started_at >= $2
		  AND tx.started_at <  $3
		  AND tx.status = 'completed'
		  AND tx.transaction_type = 'sale'
		  AND li.is_void   = false
		  AND li.is_return = false
		  AND li.item_id IS NOT NULL
		GROUP BY li.item_id, i.sku, i.description
		ORDER BY SUM(li.extended_price) DESC NULLS LAST
		LIMIT $4
	`
	return scanItemMetrics(ctx, pool, q, tenantID, from, to, limit)
}

// UnknownItemCount surfaces line items the POS sent without an item_id.
// In Counterpoint and Square these usually mean a barcode scan failed
// to resolve to a catalog row — a real schema-tier signal worth
// raising on the dashboard, not hiding.
//
// SDD-missing: there is no canonical place to surface "unknown items"
// to the merchant. The dashboard treats it as a top-level integer
// alongside top-items so it can't be ignored.
func UnknownItemCount(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) (int64, error) {
	const q = `
		SELECT COUNT(*)
		FROM transaction.transaction_line_items li
		JOIN transaction.transactions       tx ON tx.id = li.transaction_id
		WHERE tx.tenant_id = $1
		  AND tx.started_at >= $2
		  AND tx.started_at <  $3
		  AND tx.status = 'completed'
		  AND tx.transaction_type = 'sale'
		  AND li.is_void   = false
		  AND li.item_id IS NULL
	`
	var n int64
	if err := pool.QueryRow(ctx, q, tenantID, from, to).Scan(&n); err != nil {
		return 0, fmt.Errorf("metrics.UnknownItemCount: %w", err)
	}
	return n, nil
}

// SalesByLocation joins transaction.transactions to location.locations (canonical) for
// per-location revenue.
//
// SDD-conflict: owl.md SDD groups by app.locations (legacy Square
// table). The canonical schema has both app.locations and location.locations
// — transaction.transactions.location_id FKs to location.locations(id) per
// 08_t_transactions.sql:12. We honor the canonical join, not the SDD.
func SalesByLocation(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) ([]dtotypes.LocationMetric, error) {
	const q = `
		SELECT
		  l.id,
		  l.location_code,
		  l.name,
		  COALESCE(SUM(tx.grand_total)    FILTER (WHERE tx.transaction_type = 'sale'),   0)::text AS gross_sales,
		  COALESCE(
		    SUM(tx.grand_total) FILTER (WHERE tx.transaction_type = 'sale')
		      - COALESCE(SUM(tx.grand_total) FILTER (WHERE tx.transaction_type = 'refund'), 0)
		      - COALESCE(SUM(tx.discount_total) FILTER (WHERE tx.transaction_type = 'sale'), 0),
		    0
		  )::text AS net_sales,
		  COUNT(*) FILTER (WHERE tx.transaction_type = 'sale') AS transaction_count
		FROM transaction.transactions tx
		JOIN location.locations    l ON l.id = tx.location_id
		WHERE tx.tenant_id = $1
		  AND tx.started_at >= $2
		  AND tx.started_at <  $3
		  AND tx.status = 'completed'
		GROUP BY l.id, l.location_code, l.name
		ORDER BY SUM(tx.grand_total) FILTER (WHERE tx.transaction_type = 'sale') DESC NULLS LAST
	`
	rows, err := pool.Query(ctx, q, tenantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("metrics.SalesByLocation: %w", err)
	}
	defer rows.Close()

	out := make([]dtotypes.LocationMetric, 0, 8)
	for rows.Next() {
		var m dtotypes.LocationMetric
		if err := rows.Scan(
			&m.LocationID,
			&m.LocationCode,
			&m.LocationName,
			&m.GrossSales,
			&m.NetSales,
			&m.TransactionCount,
		); err != nil {
			return nil, fmt.Errorf("metrics.SalesByLocation scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics.SalesByLocation rows: %w", err)
	}
	return out, nil
}

// scanItemMetrics is the shared row-scan loop for the two top-items
// queries — same column layout, different ORDER BY.
func scanItemMetrics(ctx context.Context, pool *pgxpool.Pool, q string, tenantID uuid.UUID, from, to time.Time, limit int) ([]dtotypes.ItemMetric, error) {
	rows, err := pool.Query(ctx, q, tenantID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("metrics.TopItems: %w", err)
	}
	defer rows.Close()

	out := make([]dtotypes.ItemMetric, 0, limit)
	for rows.Next() {
		var m dtotypes.ItemMetric
		if err := rows.Scan(&m.ItemID, &m.SKU, &m.Description, &m.Units, &m.Revenue); err != nil {
			return nil, fmt.Errorf("metrics.TopItems scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics.TopItems rows: %w", err)
	}
	return out, nil
}
