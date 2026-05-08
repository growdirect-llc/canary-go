// internal/analytics/dto.go
//
// Wire types for analytics endpoints. All monetary values are numeric
// stored as float64 (PostgreSQL numeric → Go). Counts are int64.
//
//

package analytics

import (
	"time"

	"github.com/google/uuid"
)

// DateRangeFilter is common across all analytics queries.
type DateRangeFilter struct {
	TenantID   uuid.UUID
	LocationID *uuid.UUID
	From       time.Time
	To         time.Time
	Limit      int
}

// SalesSummary is the aggregate revenue view for a date range.
type SalesSummary struct {
	TenantID         uuid.UUID `json:"tenant_id"`
	LocationID       *uuid.UUID `json:"location_id,omitempty"`
	From             time.Time  `json:"from"`
	To               time.Time  `json:"to"`
	TransactionCount int64      `json:"transaction_count"`
	TotalRevenue     float64    `json:"total_revenue"`
	TotalTax         float64    `json:"total_tax"`
	TotalDiscount    float64    `json:"total_discount"`
	AvgTicket        float64    `json:"avg_ticket"`
	TotalItems       int64      `json:"total_items"`
	ReturnCount      int64      `json:"return_count"`
	ReturnRevenue    float64    `json:"return_revenue"`
}

// BasketMetrics covers ticket composition and tender distribution.
type BasketMetrics struct {
	TenantID          uuid.UUID     `json:"tenant_id"`
	From              time.Time     `json:"from"`
	To                time.Time     `json:"to"`
	AvgItemsPerTicket float64       `json:"avg_items_per_ticket"`
	AvgTicketValue    float64       `json:"avg_ticket_value"`
	TenderMix         []TenderShare `json:"tender_mix"`
}

// TenderShare is one row of the basket tender breakdown.
type TenderShare struct {
	TenderType  string  `json:"tender_type"`
	Count       int64   `json:"count"`
	TotalAmount float64 `json:"total_amount"`
	SharePct    float64 `json:"share_pct"`
}

// CohortRow is one period bucket in the customer cohort view.
type CohortRow struct {
	Period          string  `json:"period"` // YYYY-MM or YYYY-Www
	NewCustomers    int64   `json:"new_customers"`
	ReturnCustomers int64   `json:"return_customers"`
	TotalCustomers  int64   `json:"total_customers"`
	RetentionRate   float64 `json:"retention_rate"`
}

// VelocityItem is one item in the velocity ranking.
type VelocityItem struct {
	ItemID      uuid.UUID `json:"item_id"`
	Description string    `json:"description"`
	CategoryID  *uuid.UUID `json:"category_id,omitempty"`
	TotalQty    float64   `json:"total_qty"`
	TotalRev    float64   `json:"total_revenue"`
	TxCount     int64     `json:"transaction_count"`
}

// ShrinkSummary summarises inventory shrink indicators for a date range.
type ShrinkSummary struct {
	TenantID         uuid.UUID  `json:"tenant_id"`
	LocationID       *uuid.UUID `json:"location_id,omitempty"`
	From             time.Time  `json:"from"`
	To               time.Time  `json:"to"`
	ReturnCount      int64      `json:"return_count"`
	ReturnRevenue    float64    `json:"return_revenue"`
	VoidCount        int64      `json:"void_count"`
	VoidRevenue      float64    `json:"void_revenue"`
	UnknownScanCount int64      `json:"unknown_scan_count"`
	ShrinkRate       float64    `json:"shrink_rate"` // (returns+voids) / gross_revenue
}
