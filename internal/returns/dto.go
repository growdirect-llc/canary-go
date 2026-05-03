// internal/returns/dto.go
//
// Wire types for the returns endpoints.
//
// Spec: GRO-766 Phase E.

package returns

import (
	"time"

	"github.com/google/uuid"
)

// ReturnTransaction is the wire shape for a return transaction row.
type ReturnTransaction struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	TransactionNumber   string     `json:"transaction_number"`
	ParentTransactionID *uuid.UUID `json:"parent_transaction_id,omitempty"`
	LocationID          uuid.UUID  `json:"location_id"`
	CashierEmployeeID   *uuid.UUID `json:"cashier_employee_id,omitempty"`
	CustomerID          *uuid.UUID `json:"customer_id,omitempty"`
	BusinessDate        time.Time  `json:"business_date"`
	EndedAt             time.Time  `json:"ended_at"`
	Status              string     `json:"status"`
	TotalAmount         float64    `json:"total_amount"`
	LineCount           int64      `json:"line_count"`
}

// ReturnLine is one line item within a return transaction.
type ReturnLine struct {
	ID           uuid.UUID  `json:"id"`
	ItemID       *uuid.UUID `json:"item_id,omitempty"`
	Description  string     `json:"description"`
	Quantity     float64    `json:"quantity"`
	UnitPrice    float64    `json:"unit_price"`
	LineTotal    float64    `json:"line_total"`
	ReturnReason *string    `json:"return_reason,omitempty"`
}

// ReturnDetail is a return transaction with its line items.
type ReturnDetail struct {
	ReturnTransaction
	Lines []ReturnLine `json:"lines"`
}

// FraudFlagRequest marks a return transaction as suspicious.
type FraudFlagRequest struct {
	DetectionRuleID uuid.UUID `json:"detection_rule_id"` // links to q.detection_rules
	Reason          string    `json:"reason"`
	Severity        string    `json:"severity"` // low | medium | high | critical
	FlaggedBy       uuid.UUID `json:"flagged_by"`
}

// FraudFlagResponse is the created detection row reference.
type FraudFlagResponse struct {
	DetectionID    uuid.UUID `json:"detection_id"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	Severity       string    `json:"severity"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListFilters controls the returns listing.
type ListFilters struct {
	TenantID   uuid.UUID
	LocationID *uuid.UUID
	From       time.Time
	To         time.Time
	CustomerID *uuid.UUID
	Limit      int
	Offset     int
}

// SummaryStats is the aggregate return stats for a date range.
type SummaryStats struct {
	TenantID      uuid.UUID  `json:"tenant_id"`
	LocationID    *uuid.UUID `json:"location_id,omitempty"`
	From          time.Time  `json:"from"`
	To            time.Time  `json:"to"`
	ReturnCount   int64      `json:"return_count"`
	TotalAmount   float64    `json:"total_amount"`
	AvgReturnAmt  float64    `json:"avg_return_amount"`
	UniqueCustomers int64    `json:"unique_customers"`
}
