package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/owl/dtotypes"
)

// detection.cases.status takes one of: open | active | pending_action |
// resolved | closed | reopened (per 09_q_canary_mechanics.sql:62).
// "Open" for Owl's purposes = anything not resolved/closed — the
// terminal-state list is hardcoded into the SQL below for clarity.

// CasesSummary computes open/opened/closed counts plus open-now
// breakdowns in two short queries.
//
// SDD-vague: owl.md SDD doesn't define "closed in period". We use
// resolved_at — the only timestamp on detection.cases that fires once and
// stays put. status='closed' could be reverted by a 'reopened' action;
// resolved_at is set when the case actually wraps.
//
// SDD-missing: there's no `closed_at` separate from `resolved_at`.
// closed-without-resolution would not show in this metric. Flag for
// later loop — add `closed_at` to detection.cases or treat status='closed'
// as a separate counter.
func CasesSummary(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) (dtotypes.CasesSummary, error) {
	const counts = `
		SELECT
		  COUNT(*) FILTER (WHERE status NOT IN ('resolved', 'closed'))                       AS open_now,
		  COUNT(*) FILTER (WHERE opened_at   >= $2 AND opened_at   < $3)                     AS opened_in_period,
		  COUNT(*) FILTER (WHERE resolved_at >= $2 AND resolved_at < $3)                     AS closed_in_period
		FROM detection.cases
		WHERE tenant_id = $1
	`
	out := dtotypes.CasesSummary{
		BySeverity: map[string]int64{},
		ByCaseType: map[string]int64{},
	}
	if err := pool.QueryRow(ctx, counts, tenantID, from, to).Scan(
		&out.OpenNow,
		&out.OpenedInPeriod,
		&out.ClosedInPeriod,
	); err != nil {
		return dtotypes.CasesSummary{}, fmt.Errorf("metrics.CasesSummary counts: %w", err)
	}

	// Open-now breakdown by severity. Two passes is cheaper than
	// pivoting on unknown-cardinality enums in one query.
	const sev = `
		SELECT severity, COUNT(*)
		FROM detection.cases
		WHERE tenant_id = $1
		  AND status NOT IN ('resolved', 'closed')
		GROUP BY severity
	`
	if err := scanCounts(ctx, pool, sev, tenantID, out.BySeverity); err != nil {
		return dtotypes.CasesSummary{}, fmt.Errorf("metrics.CasesSummary by severity: %w", err)
	}

	const types = `
		SELECT case_type, COUNT(*)
		FROM detection.cases
		WHERE tenant_id = $1
		  AND status NOT IN ('resolved', 'closed')
		GROUP BY case_type
	`
	if err := scanCounts(ctx, pool, types, tenantID, out.ByCaseType); err != nil {
		return dtotypes.CasesSummary{}, fmt.Errorf("metrics.CasesSummary by case_type: %w", err)
	}

	return out, nil
}

// DetectionRate is detections-per-1k-transactions in the period.
//
// detection.detections.detected_at is append-only and timestamped, so this is
// a clean single query. Wave 1 finding confirmed.
//
// SDD-conflict: the owl.md SDD defines no "detection rate" metric at
// all — it's a chat-tier system. The dispatch defines this metric.
// We're surfacing one of the cleanest queries in the canonical
// schema; it should anchor every dashboard.
func DetectionRate(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) (dtotypes.DetectionRate, error) {
	const q = `
		WITH d AS (
		  SELECT severity, COUNT(*) AS n
		  FROM detection.detections
		  WHERE tenant_id = $1
		    AND detected_at >= $2
		    AND detected_at <  $3
		    AND status NOT IN ('dismissed', 'duplicate')
		  GROUP BY severity
		),
		t AS (
		  SELECT COUNT(*) AS n
		  FROM transaction.transactions
		  WHERE tenant_id = $1
		    AND started_at >= $2
		    AND started_at <  $3
		    AND status = 'completed'
		    AND transaction_type = 'sale'
		)
		SELECT
		  (SELECT COALESCE(SUM(n), 0) FROM d) AS detection_count,
		  (SELECT n FROM t)                   AS transaction_count
	`
	out := dtotypes.DetectionRate{
		BySeverity: map[string]int64{},
	}
	if err := pool.QueryRow(ctx, q, tenantID, from, to).Scan(
		&out.DetectionCount,
		&out.TransactionCount,
	); err != nil {
		return dtotypes.DetectionRate{}, fmt.Errorf("metrics.DetectionRate counts: %w", err)
	}
	if out.TransactionCount > 0 {
		out.RatePer1KTransactions = float64(out.DetectionCount) / float64(out.TransactionCount) * 1000.0
	}

	const sev = `
		SELECT severity, COUNT(*)
		FROM detection.detections
		WHERE tenant_id = $1
		  AND detected_at >= $2
		  AND detected_at <  $3
		  AND status NOT IN ('dismissed', 'duplicate')
		GROUP BY severity
	`
	rows, err := pool.Query(ctx, sev, tenantID, from, to)
	if err != nil {
		return dtotypes.DetectionRate{}, fmt.Errorf("metrics.DetectionRate by severity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sevName string
		var n int64
		if err := rows.Scan(&sevName, &n); err != nil {
			return dtotypes.DetectionRate{}, fmt.Errorf("metrics.DetectionRate scan: %w", err)
		}
		out.BySeverity[sevName] = n
	}
	if err := rows.Err(); err != nil {
		return dtotypes.DetectionRate{}, fmt.Errorf("metrics.DetectionRate rows: %w", err)
	}

	return out, nil
}

// scanCounts populates a (text → int64) map from a 2-column query.
func scanCounts(ctx context.Context, pool *pgxpool.Pool, q string, tenantID uuid.UUID, into map[string]int64) error {
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		into[k] = n
	}
	return rows.Err()
}
