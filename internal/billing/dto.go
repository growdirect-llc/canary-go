// Package billing implements Bull (Module B) — L402-gated open-to-buy
// budgeting + satoshi cost rollup over ledger.ildwac_positions.
//
// Spec: GRO-765 Phase A (folds GRO-643). The accountability rail this
// module enforces:
//
//   1. Every cadence step (minute/hour/day/week/month) records an
//      ildwac_position row capturing storage + workload + capture
//      satoshis spent.
//   2. The L402 charge cycle workflow rolls those up into invoices.
//   3. OTB budgets gate spend at the merchant level — hard_limit=true
//      budgets refuse usage past their cap.
//
// Per memory `project_satoshi_cost_model`: 5-input diagnostic
// (cadence ladder × ILDWAC × L402-OTB × blockchain-anchor → satoshi
// total) replaces seat pricing.
package billing

import (
	"time"

	"github.com/google/uuid"
)

// OTBBudget is the wire shape for ledger.l402_otb_budgets reads.
type OTBBudget struct {
	ID                 uuid.UUID  `json:"id"`
	TenantID           uuid.UUID  `json:"tenant_id"`
	BudgetPeriodStart  time.Time  `json:"budget_period_start"`
	BudgetPeriodEnd    *time.Time `json:"budget_period_end,omitempty"`
	ScopeType          string     `json:"scope_type"` // tenant_total | category | location | service
	ScopeID            *uuid.UUID `json:"scope_id,omitempty"`
	BudgetSatoshis     int64      `json:"budget_satoshis"`
	ConsumedSatoshis   int64      `json:"consumed_satoshis"`
	RemainingSatoshis  int64      `json:"remaining_satoshis"`
	HardLimit          bool       `json:"hard_limit"`
	AlertThresholdPct  *float64   `json:"alert_threshold_pct,omitempty"`
	Status             string     `json:"status"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// CreateBudgetRequest is the wire shape for POST /v1/billing/otb.
type CreateBudgetRequest struct {
	TenantID          uuid.UUID  `json:"tenant_id"`
	BudgetPeriodStart time.Time  `json:"budget_period_start"`
	BudgetPeriodEnd   *time.Time `json:"budget_period_end,omitempty"`
	ScopeType         string     `json:"scope_type"`
	ScopeID           *uuid.UUID `json:"scope_id,omitempty"`
	BudgetSatoshis    int64      `json:"budget_satoshis"`
	HardLimit         bool       `json:"hard_limit"`
	AlertThresholdPct *float64   `json:"alert_threshold_pct,omitempty"`
}

// ConsumeRequest is the wire shape for POST /v1/billing/otb/{id}/consume.
type ConsumeRequest struct {
	Satoshis int64  `json:"satoshis"`
	Reason   string `json:"reason,omitempty"`
}

// ConsumeResponse confirms the consumed amount and returns the
// post-consume remaining balance.
type ConsumeResponse struct {
	BudgetID          uuid.UUID `json:"budget_id"`
	ConsumedSatoshis  int64     `json:"consumed_satoshis"`
	RemainingSatoshis int64     `json:"remaining_satoshis"`
	HardLimitHit      bool      `json:"hard_limit_hit,omitempty"`
}

// CostRollupRequest is the wire shape for GET /v1/billing/cost-rollup
// — the satoshi cost-aggregation primitive per memory
// `project_satoshi_cost_model`.
type CostRollupRequest struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	CadenceStep string    `json:"cadence_step,omitempty"` // minute | hour | day | week | month; empty = all
}

// CostRollup is the response shape — totals across the 3 ILDWAC
// dimensions plus aggregated period totals.
type CostRollup struct {
	TenantID            uuid.UUID `json:"tenant_id"`
	PeriodStart         time.Time `json:"period_start"`
	PeriodEnd           time.Time `json:"period_end"`
	CadenceStep         string    `json:"cadence_step,omitempty"`
	StorageSatoshis     int64     `json:"storage_satoshis"`
	WorkloadSatoshis    int64     `json:"workload_satoshis"`
	CaptureSatoshis     int64     `json:"capture_satoshis"`
	TotalSatoshis       int64     `json:"total_satoshis"`
	BytesUnderManagement int64    `json:"bytes_under_management"`
	WorkloadUnits       int64     `json:"workload_units"`
	PositionCount       int       `json:"position_count"`
	UnbilledCount       int       `json:"unbilled_count"`
	OldestUnbilled      *time.Time `json:"oldest_unbilled,omitempty"`
}
