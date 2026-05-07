// internal/owl/dashboards.go
//
// analytics surface — RFM rollups per party (consumer of the
// party.decisioning_facts materialized view from plus
// LP-rate metrics aggregating detection.detections / detection.cases.
// Phase C.
//
// ships internal/owl/ with metrics aggregation primitives;
// this file adds the Wave C dashboard endpoints. The existing owl
// metrics package stays — these dashboards sit alongside it.

package owl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PartyRFM is the wire-shape returned by the party RFM endpoint.
type PartyRFM struct {
	PartyID         uuid.UUID `json:"party_id"`
	TenantID        uuid.UUID `json:"tenant_id"`
	Confidence      string    `json:"confidence"`
	PartyValue string `json:"party_value"` // numeric — 12-month rolling spend
	PartyRecency int `json:"party_recency"` // days since last_seen_at
	PartyFrequency int `json:"party_frequency"` // 12-month transaction count
	PartyMonetary string `json:"party_monetary"` // 12-month average transaction
	PartyFraudRisk  string    `json:"party_fraud_risk"`
	PartyChurnRisk  string    `json:"party_churn_risk"`
	ComputedAt      time.Time `json:"computed_at"`
}

// LPRateMetric is one row of the LP-rate dashboard.
type LPRateMetric struct {
	TenantID        uuid.UUID `json:"tenant_id"`
	RuleType        string    `json:"rule_type"`
	WindowStart     time.Time `json:"window_start"`
	WindowEnd       time.Time `json:"window_end"`
	DetectionCount  int       `json:"detection_count"`
	CaseCount       int       `json:"case_count"`
	EscalationRate float64 `json:"escalation_rate"` // case_count / detection_count
}

// DashboardStore is the pgx-backed read layer for owl dashboards.
type DashboardStore struct {
	pool *pgxpool.Pool
}

func NewDashboardStore(pool *pgxpool.Pool) *DashboardStore {
	return &DashboardStore{pool: pool}
}

// Sentinels.
var (
	ErrNotFound = errors.New("owl: not found")
)

// GetPartyRFM reads one row from party.decisioning_facts. The MV is
// refreshed on the cadence configured in party-identity-design.md §E.
// Callers needing a fresh read trigger RefreshDecisioningFacts.
func (s *DashboardStore) GetPartyRFM(ctx context.Context, tenantID, partyID uuid.UUID) (*PartyRFM, error) {
	const q = `
		SELECT party_id, tenant_id, confidence,
		       party_value::text, party_recency,
		       party_frequency, party_monetary::text,
		       party_fraud_risk::text, party_churn_risk::text,
		       computed_at
		  FROM party.decisioning_facts
		 WHERE tenant_id = $1 AND party_id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, partyID)
	var r PartyRFM
	if err := row.Scan(
		&r.PartyID, &r.TenantID, &r.Confidence,
		&r.PartyValue, &r.PartyRecency,
		&r.PartyFrequency, &r.PartyMonetary,
		&r.PartyFraudRisk, &r.PartyChurnRisk, &r.ComputedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("owl: party rfm: %w", err)
	}
	return &r, nil
}

// ListPartyRFM returns the top-N parties by party_value (descending)
// for a tenant. Used by the operator dashboard to surface the highest-
// value parties.
func (s *DashboardStore) ListPartyRFM(ctx context.Context, tenantID uuid.UUID, limit int) ([]PartyRFM, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	const q = `
		SELECT party_id, tenant_id, confidence,
		       party_value::text, party_recency,
		       party_frequency, party_monetary::text,
		       party_fraud_risk::text, party_churn_risk::text,
		       computed_at
		  FROM party.decisioning_facts
		 WHERE tenant_id = $1
		 ORDER BY party_value DESC
		 LIMIT $2`
	rows, err := s.pool.Query(ctx, q, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("owl: list party rfm: %w", err)
	}
	defer rows.Close()
	out := make([]PartyRFM, 0, limit)
	for rows.Next() {
		var r PartyRFM
		if err := rows.Scan(
			&r.PartyID, &r.TenantID, &r.Confidence,
			&r.PartyValue, &r.PartyRecency,
			&r.PartyFrequency, &r.PartyMonetary,
			&r.PartyFraudRisk, &r.PartyChurnRisk, &r.ComputedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RefreshDecisioningFacts triggers REFRESH MATERIALIZED VIEW on
// party.decisioning_facts. CONCURRENTLY when supported (postgres
// requires a unique index — created idx_dfacts_party
// UNIQUE so CONCURRENTLY works). Idempotent at the DB level.
func (s *DashboardStore) RefreshDecisioningFacts(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx,
		`REFRESH MATERIALIZED VIEW CONCURRENTLY party.decisioning_facts`); err != nil {
		// Fall back to non-concurrent refresh if the concurrent path
		// failed (e.g., during the very first refresh when no rows
		// exist yet — postgres rejects CONCURRENTLY on empty MV).
		if _, err2 := s.pool.Exec(ctx, `REFRESH MATERIALIZED VIEW party.decisioning_facts`); err2 != nil {
			return fmt.Errorf("owl: refresh decisioning_facts: %w (fallback: %v)", err, err2)
		}
	}
	return nil
}

// LPRateRollup aggregates detection.detections + detection.cases for a tenant over a
// time window, grouped by rule_type. Used by the LP-rate dashboard.
func (s *DashboardStore) LPRateRollup(ctx context.Context, tenantID uuid.UUID, windowStart, windowEnd time.Time) ([]LPRateMetric, error) {
	const q = `
		WITH det AS (
		    SELECT r.rule_type, COUNT(*) AS detection_count
		      FROM detection.detections d
		      JOIN detection.detection_rules r ON r.id = d.rule_id
		     WHERE d.tenant_id = $1
		       AND d.detected_at >= $2 AND d.detected_at < $3
		     GROUP BY r.rule_type
		),
		cas AS (
		    SELECT r.rule_type, COUNT(DISTINCT c.id) AS case_count
		      FROM detection.cases c
		      JOIN detection.detections d ON d.tenant_id = c.tenant_id
		           AND (c.attributes->>'detection_id')::uuid = d.id
		      JOIN detection.detection_rules r ON r.id = d.rule_id
		     WHERE c.tenant_id = $1
		       AND c.opened_at >= $2 AND c.opened_at < $3
		     GROUP BY r.rule_type
		)
		SELECT COALESCE(det.rule_type, cas.rule_type)            AS rule_type,
		       COALESCE(det.detection_count, 0)                  AS detection_count,
		       COALESCE(cas.case_count, 0)                       AS case_count
		  FROM det FULL OUTER JOIN cas ON det.rule_type = cas.rule_type
		 ORDER BY detection_count DESC`
	rows, err := s.pool.Query(ctx, q, tenantID, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("owl: lp-rate rollup: %w", err)
	}
	defer rows.Close()
	out := []LPRateMetric{}
	for rows.Next() {
		m := LPRateMetric{
			TenantID:    tenantID,
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		}
		if err := rows.Scan(&m.RuleType, &m.DetectionCount, &m.CaseCount); err != nil {
			return nil, err
		}
		if m.DetectionCount > 0 {
			m.EscalationRate = float64(m.CaseCount) / float64(m.DetectionCount)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
