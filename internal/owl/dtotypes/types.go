// Package dtotypes is the leaf-level home for Owl's response DTOs.
//
// Why a sub-package? Owl's metrics helpers (internal/owl/metrics) need
// these types to return query results, and the parent owl package
// imports metrics. A leaf package breaks the would-be cycle. The owl
// package re-exports the names via type aliases in dto.go so callers
// see a clean owl.Dashboard / owl.SalesSummary surface.
package dtotypes

import (
	"time"

	"github.com/google/uuid"
)

// ──────────────────────────────────────────────────────────────────────
// Period
// ──────────────────────────────────────────────────────────────────────

// PeriodKind identifies a canonical reporting window.
type PeriodKind string

const (
	PeriodToday  PeriodKind = "today"
	PeriodWTD    PeriodKind = "wtd"
	PeriodMTD    PeriodKind = "mtd"
	PeriodCustom PeriodKind = "range"
)

// Period is the resolved reporting window in UTC. From inclusive,
// To exclusive — same convention as Postgres `>= $from AND < $to`.
type Period struct {
	Kind     PeriodKind `json:"kind"`
	From     time.Time  `json:"from"`
	To       time.Time  `json:"to"`
	Timezone string     `json:"timezone"`
}

// ──────────────────────────────────────────────────────────────────────
// Sales summary
// ──────────────────────────────────────────────────────────────────────

// SalesSummary aggregates t.transactions over a period.
//
// Money fields stay decimal-as-string. Wave 1 types punted on
// shopspring/decimal; Owl follows that posture so float drift can't
// creep in between Postgres numeric and the dashboard JSON.
type SalesSummary struct {
	GrossSales       string `json:"gross_sales"`
	NetSales         string `json:"net_sales"`
	RefundTotal      string `json:"refund_total"`
	DiscountTotal    string `json:"discount_total"`
	TaxTotal         string `json:"tax_total"`
	TransactionCount int64  `json:"transaction_count"`
	RefundCount      int64  `json:"refund_count"`
	AverageTicket    string `json:"average_ticket"`
}

// ──────────────────────────────────────────────────────────────────────
// Top items
// ──────────────────────────────────────────────────────────────────────

// TopItemsBy is the sort key for top-items queries.
type TopItemsBy string

const (
	TopItemsByUnits   TopItemsBy = "units"
	TopItemsByRevenue TopItemsBy = "revenue"
)

// ItemMetric is a single row in a top-items list.
type ItemMetric struct {
	ItemID      uuid.UUID `json:"item_id"`
	SKU         string    `json:"sku"`
	Description string    `json:"description"`
	Units       string    `json:"units"`
	Revenue     string    `json:"revenue"`
}

// TopItems wraps both top-N lists.
type TopItems struct {
	Limit        int          `json:"limit"`
	ByUnits      []ItemMetric `json:"by_units"`
	ByRevenue    []ItemMetric `json:"by_revenue"`
	UnknownItems int64        `json:"unknown_items"`
}

// ──────────────────────────────────────────────────────────────────────
// Sales by location
// ──────────────────────────────────────────────────────────────────────

type LocationMetric struct {
	LocationID       uuid.UUID `json:"location_id"`
	LocationCode     string    `json:"location_code"`
	LocationName     string    `json:"location_name"`
	GrossSales       string    `json:"gross_sales"`
	NetSales         string    `json:"net_sales"`
	TransactionCount int64     `json:"transaction_count"`
}

// ──────────────────────────────────────────────────────────────────────
// Cases
// ──────────────────────────────────────────────────────────────────────

// CasesSummary aggregates q.cases over a period.
type CasesSummary struct {
	OpenNow        int64            `json:"open_now"`
	OpenedInPeriod int64            `json:"opened_in_period"`
	ClosedInPeriod int64            `json:"closed_in_period"`
	BySeverity     map[string]int64 `json:"by_severity"`
	ByCaseType     map[string]int64 `json:"by_case_type"`
}

// ──────────────────────────────────────────────────────────────────────
// Detection rate
// ──────────────────────────────────────────────────────────────────────

// DetectionRate is detections-per-1k-transactions in the period.
type DetectionRate struct {
	DetectionCount        int64            `json:"detection_count"`
	TransactionCount      int64            `json:"transaction_count"`
	RatePer1KTransactions float64          `json:"rate_per_1k_transactions"`
	BySeverity            map[string]int64 `json:"by_severity"`
}

// ──────────────────────────────────────────────────────────────────────
// Cashier exposure
// ──────────────────────────────────────────────────────────────────────

// CashierExposure ranks a cashier by detection count in the period.
type CashierExposure struct {
	EmployeeID     uuid.UUID `json:"employee_id"`
	EmployeeCode   string    `json:"employee_code"`
	DisplayName    string    `json:"display_name"`
	DetectionCount int64     `json:"detection_count"`
}

// ──────────────────────────────────────────────────────────────────────
// Dashboard envelope
// ──────────────────────────────────────────────────────────────────────

// Dashboard is the full /v1/owl/dashboard response.
type Dashboard struct {
	MerchantID  uuid.UUID         `json:"merchant_id"`
	TenantID    uuid.UUID         `json:"tenant_id"`
	Period      Period            `json:"period"`
	Sales       SalesSummary      `json:"sales"`
	TopItems    TopItems          `json:"top_items"`
	ByLocation  []LocationMetric  `json:"by_location"`
	Cases       CasesSummary      `json:"cases"`
	Detection   DetectionRate     `json:"detection"`
	Exposure    []CashierExposure `json:"exposure"`
	GeneratedAt time.Time         `json:"generated_at"`
}
