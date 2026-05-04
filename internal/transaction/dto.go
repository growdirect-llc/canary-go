// Package transaction implements the canonical write path for
// transaction.transactions and its child tables (transaction.transaction_line_items,
// transaction.transaction_tenders, transaction.transaction_discounts).
//
// Spec: GRO-764 Phase B (folds GRO-647). The transaction module is
// the canonical example of "complex writes" per the sqlc rule
// reconciliation in docs/conventions.md — multi-statement
// transactional writes that span 4+ tables.
//
// This file: wire-shape DTOs. Conventions per docs/conventions.md.
package transaction

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CreateRequest is the inbound shape for POST /v1/transactions. Sub2
// (the POS adapter) translates source-vendor payloads into this
// canonical form before calling.
type CreateRequest struct {
	TenantID            uuid.UUID         `json:"tenant_id"`
	TransactionNumber   string            `json:"transaction_number"`
	TransactionType     string            `json:"transaction_type,omitempty"` // sale | return | void | exchange
	ParentTransactionID *uuid.UUID        `json:"parent_transaction_id,omitempty"`
	LocationID          uuid.UUID         `json:"location_id"`
	POSTerminalID       *string           `json:"pos_terminal_id,omitempty"`
	CashierEmployeeID   *uuid.UUID        `json:"cashier_employee_id,omitempty"`
	CustomerID          *uuid.UUID        `json:"customer_id,omitempty"`
	LoyaltyMembershipID *uuid.UUID        `json:"loyalty_membership_id,omitempty"`
	BusinessDate        string            `json:"business_date"` // YYYY-MM-DD
	StartedAt           time.Time         `json:"started_at"`
	EndedAt             time.Time         `json:"ended_at"`
	Status              string            `json:"status,omitempty"` // completed | suspended | voided
	TicketNumber        *int              `json:"ticket_number,omitempty"`
	Currency            string            `json:"currency,omitempty"`
	Channel             string            `json:"channel,omitempty"`
	POSSoftwareVersion  *string           `json:"pos_software_version,omitempty"`
	IsTrainingMode      bool              `json:"is_training_mode"`
	IsOffline           bool              `json:"is_offline"`
	Attributes          map[string]any    `json:"attributes,omitempty"`
	ExternalIDs         map[string]any    `json:"external_ids,omitempty"`
	LineItems           []LineItemRequest `json:"line_items"`
	Tenders             []TenderRequest   `json:"tenders"`
	Discounts           []DiscountRequest `json:"discounts,omitempty"`
}

// LineItemRequest is one row from CreateRequest.LineItems.
type LineItemRequest struct {
	LineNumber  int             `json:"line_number"`
	ItemID      *uuid.UUID      `json:"item_id,omitempty"`
	Description string          `json:"description"`
	Quantity    decimal.Decimal `json:"quantity"`
	UnitPrice   decimal.Decimal `json:"unit_price"`
	LineTotal   decimal.Decimal `json:"line_total"`
	TaxAmount   decimal.Decimal `json:"tax_amount,omitempty"`
	Attributes  map[string]any  `json:"attributes,omitempty"`
}

// TenderRequest is one row from CreateRequest.Tenders.
type TenderRequest struct {
	TenderTypeID *uuid.UUID      `json:"tender_type_id,omitempty"`
	TenderCode   string          `json:"tender_code"`
	Amount       decimal.Decimal `json:"amount"`
	Currency     string          `json:"currency,omitempty"`
	Reference    *string         `json:"reference,omitempty"`
	Attributes   map[string]any  `json:"attributes,omitempty"`
}

// DiscountRequest is one row from CreateRequest.Discounts.
type DiscountRequest struct {
	DiscountCode string          `json:"discount_code"`
	Amount       decimal.Decimal `json:"amount"`
	Reason       *string         `json:"reason,omitempty"`
	Attributes   map[string]any  `json:"attributes,omitempty"`
}

// VoidRequest is the shape for POST /v1/transactions/{id}/voids.
// Creates a child transaction with transaction_type='void' that
// references the parent.
type VoidRequest struct {
	Reason            string     `json:"reason"`
	CashierEmployeeID *uuid.UUID `json:"cashier_employee_id,omitempty"`
}

// ReturnRequest is the shape for POST /v1/transactions/{id}/returns.
// Creates a child transaction with transaction_type='return' that
// inherits party_id from the parent (per Wave A canonical-data-model
// party-edits §C — soft FK).
type ReturnRequest struct {
	Reason            string             `json:"reason"`
	CashierEmployeeID *uuid.UUID         `json:"cashier_employee_id,omitempty"`
	LineItems         []LineItemRequest  `json:"line_items"`
	Tenders           []TenderRequest    `json:"tenders"`
}

// TransactionDTO is the wire shape returned by reads. Aggregates the
// canonical row plus child counts (full child rows fetched only on
// the by-id endpoint).
type TransactionDTO struct {
	ID                  uuid.UUID       `json:"id"`
	TenantID            uuid.UUID       `json:"tenant_id"`
	TransactionNumber   string          `json:"transaction_number"`
	TransactionType     string          `json:"transaction_type"`
	ParentTransactionID *uuid.UUID      `json:"parent_transaction_id,omitempty"`
	LocationID          uuid.UUID       `json:"location_id"`
	POSTerminalID       *string         `json:"pos_terminal_id,omitempty"`
	CashierEmployeeID   *uuid.UUID      `json:"cashier_employee_id,omitempty"`
	CustomerID          *uuid.UUID      `json:"customer_id,omitempty"`
	LoyaltyMembershipID *uuid.UUID      `json:"loyalty_membership_id,omitempty"`
	PartyID             *uuid.UUID      `json:"party_id,omitempty"`
	BusinessDate        string          `json:"business_date"`
	StartedAt           time.Time       `json:"started_at"`
	EndedAt             time.Time       `json:"ended_at"`
	Status              string          `json:"status"`
	TicketNumber        *int            `json:"ticket_number,omitempty"`
	ItemCount           int             `json:"item_count"`
	Subtotal            decimal.Decimal `json:"subtotal"`
	TaxTotal            decimal.Decimal `json:"tax_total"`
	DiscountTotal       decimal.Decimal `json:"discount_total"`
	GrandTotal          decimal.Decimal `json:"grand_total"`
	Currency            string          `json:"currency"`
	Channel             string          `json:"channel"`
	IsTrainingMode      bool            `json:"is_training_mode"`
	IsOffline           bool            `json:"is_offline"`
	VoidReason          *string         `json:"void_reason,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`

	// Hydrated only on by-id reads (List omits these to keep payloads bounded).
	LineItems []LineItemDTO `json:"line_items,omitempty"`
	Tenders   []TenderDTO   `json:"tenders,omitempty"`
	Discounts []DiscountDTO `json:"discounts,omitempty"`
}

// LineItemDTO is the persisted shape returned by GetByID.
type LineItemDTO struct {
	ID            uuid.UUID       `json:"id"`
	TransactionID uuid.UUID       `json:"transaction_id"`
	LineNumber    int             `json:"line_number"`
	ItemID        *uuid.UUID      `json:"item_id,omitempty"`
	Description   string          `json:"description"`
	Quantity      decimal.Decimal `json:"quantity"`
	UnitPrice     decimal.Decimal `json:"unit_price"`
	LineTotal     decimal.Decimal `json:"line_total"`
	TaxAmount     decimal.Decimal `json:"tax_amount"`
	CreatedAt     time.Time       `json:"created_at"`
}

// TenderDTO is the persisted shape returned by GetByID.
type TenderDTO struct {
	ID            uuid.UUID       `json:"id"`
	TransactionID uuid.UUID       `json:"transaction_id"`
	TenderTypeID  *uuid.UUID      `json:"tender_type_id,omitempty"`
	TenderCode    string          `json:"tender_code"`
	Amount        decimal.Decimal `json:"amount"`
	Currency      string          `json:"currency"`
	Reference     *string         `json:"reference,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// DiscountDTO is the persisted shape returned by GetByID.
type DiscountDTO struct {
	ID            uuid.UUID       `json:"id"`
	TransactionID uuid.UUID       `json:"transaction_id"`
	DiscountCode  string          `json:"discount_code"`
	Amount        decimal.Decimal `json:"amount"`
	Reason        *string         `json:"reason,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ListFilters captures the optional filters for the /v1/transactions
// list endpoint.
type ListFilters struct {
	TenantID        uuid.UUID
	LocationID      *uuid.UUID
	BusinessDateMin *string
	BusinessDateMax *string
	Status          *string
	CashierID       *uuid.UUID
	CustomerID      *uuid.UUID
	Limit           int
	Offset          int
}

// ListResponse is the wire envelope for list reads.
type ListResponse struct {
	Items  []TransactionDTO `json:"items"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
	Count  int              `json:"count"`
}
