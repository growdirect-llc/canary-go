// Code generated from deploy/schema/08_t_transactions.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Transaction mirrors t.transactions.
type Transaction struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	TransactionNumber   string          `db:"transaction_number"`
	TransactionType     string          `db:"transaction_type"`
	ParentTransactionID *uuid.UUID      `db:"parent_transaction_id"`
	LocationID          uuid.UUID       `db:"location_id"`
	POSTerminalID       *string         `db:"pos_terminal_id"`
	CashierEmployeeID   *uuid.UUID      `db:"cashier_employee_id"`
	CustomerID          *uuid.UUID      `db:"customer_id"`
	LoyaltyMembershipID *uuid.UUID      `db:"loyalty_membership_id"`
	BusinessDate        time.Time       `db:"business_date"`
	StartedAt           time.Time       `db:"started_at"`
	EndedAt             time.Time       `db:"ended_at"`
	Status              string          `db:"status"`
	TicketNumber        *int32          `db:"ticket_number"`
	ItemCount           int32           `db:"item_count"`
	Subtotal string `db:"subtotal"` // numeric — decimal.Decimal dep needed; using string for
	TaxTotal string `db:"tax_total"` // numeric — decimal.Decimal dep needed; using string for
	DiscountTotal string `db:"discount_total"` // numeric — decimal.Decimal dep needed; using string for
	GrandTotal string `db:"grand_total"` // numeric — decimal.Decimal dep needed; using string for
	Currency            string          `db:"currency"`
	Channel             string          `db:"channel"`
	POSSoftwareVersion  *string         `db:"pos_software_version"`
	IsTrainingMode      bool            `db:"is_training_mode"`
	IsOffline           bool            `db:"is_offline"`
	IsReentered         bool            `db:"is_reentered"`
	IsSuspended         bool            `db:"is_suspended"`
	VoidReason          *string         `db:"void_reason"`
	Attributes          json.RawMessage `db:"attributes"`
	ExternalIDs         json.RawMessage `db:"external_ids"`
	CreatedAt           time.Time       `db:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at"`
}

// TransactionLineItem mirrors t.transaction_line_items. No UpdatedAt (POSLog append-style).
type TransactionLineItem struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	TransactionID        uuid.UUID       `db:"transaction_id"`
	LineNumber           int32           `db:"line_number"`
	ItemID               *uuid.UUID      `db:"item_id"`
	BarcodeScanned       *string         `db:"barcode_scanned"`
	Description          string          `db:"description"`
	Quantity string `db:"quantity"` // numeric — decimal.Decimal dep needed; using string for
	UnitOfMeasure        string          `db:"unit_of_measure"`
	UnitPrice string `db:"unit_price"` // numeric — decimal.Decimal dep needed; using string for
	ListPrice *string `db:"list_price"` // numeric — decimal.Decimal dep needed; using string for
	UnitDiscount string `db:"unit_discount"` // numeric — decimal.Decimal dep needed; using string for
	UnitTax string `db:"unit_tax"` // numeric — decimal.Decimal dep needed; using string for
	ExtendedPrice string `db:"extended_price"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	ExtendedTax string `db:"extended_tax"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	LineTotal string `db:"line_total"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	CostBasis *string `db:"cost_basis"` // numeric — decimal.Decimal dep needed; using string for
	Margin string `db:"margin"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	CategoryID           *uuid.UUID      `db:"category_id"`
	ZoneID               *uuid.UUID      `db:"zone_id"`
	LotID                *uuid.UUID      `db:"lot_id"`
	InventoryMovementID  *uuid.UUID      `db:"inventory_movement_id"`
	IsVoid               bool            `db:"is_void"`
	VoidReason           *string         `db:"void_reason"`
	IsReturn             bool            `db:"is_return"`
	ReturnReason         *string         `db:"return_reason"`
	IsWeighable          bool            `db:"is_weighable"`
	IsFoodStampEligible  bool            `db:"is_food_stamp_eligible"`
	Attributes           json.RawMessage `db:"attributes"`
	CreatedAt            time.Time       `db:"created_at"`
}

// TransactionTender mirrors t.transaction_tenders. No UpdatedAt.
type TransactionTender struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           uuid.UUID       `db:"tenant_id"`
	TransactionID      uuid.UUID       `db:"transaction_id"`
	TenderSequence     int32           `db:"tender_sequence"`
	TenderTypeID       uuid.UUID       `db:"tender_type_id"`
	Amount string `db:"amount"` // numeric — decimal.Decimal dep needed; using string for
	Currency           string          `db:"currency"`
	CashBackAmount string `db:"cash_back_amount"` // numeric — decimal.Decimal dep needed; using string for
	ChangeAmount string `db:"change_amount"` // numeric — decimal.Decimal dep needed; using string for
	CardToken          *string         `db:"card_token"`
	CardLast4          *string         `db:"card_last_4"`
	CardBrand          *string         `db:"card_brand"`
	AuthorizationCode  *string         `db:"authorization_code"`
	ProcessorReference *string         `db:"processor_reference"`
	IsVoided           bool            `db:"is_voided"`
	IsRefund           bool            `db:"is_refund"`
	Contactless        bool            `db:"contactless"`
	Attributes         json.RawMessage `db:"attributes"`
	CreatedAt          time.Time       `db:"created_at"`
}

// TransactionDiscount mirrors t.transaction_discounts. No UpdatedAt.
type TransactionDiscount struct {
	ID                      uuid.UUID       `db:"id"`
	TenantID                uuid.UUID       `db:"tenant_id"`
	TransactionID           uuid.UUID       `db:"transaction_id"`
	DiscountSequence        int32           `db:"discount_sequence"`
	Scope                   string          `db:"scope"`
	LineItemID              *uuid.UUID      `db:"line_item_id"`
	DiscountType            string          `db:"discount_type"`
	SourcePromotionID       *uuid.UUID      `db:"source_promotion_id"`
	PromotionRuleID         *uuid.UUID      `db:"promotion_rule_id"`
	Amount string `db:"amount"` // numeric — decimal.Decimal dep needed; using string for
	Percentage *string `db:"percentage"` // numeric — decimal.Decimal dep needed; using string for
	ReasonCode              *string         `db:"reason_code"`
	AuthorizedByEmployeeID  *uuid.UUID      `db:"authorized_by_employee_id"`
	Attributes              json.RawMessage `db:"attributes"`
	CreatedAt               time.Time       `db:"created_at"`
}

// CashierAction mirrors t.cashier_actions. No UpdatedAt.
type CashierAction struct {
	ID                     uuid.UUID       `db:"id"`
	TenantID               uuid.UUID       `db:"tenant_id"`
	TransactionID          *uuid.UUID      `db:"transaction_id"`
	LocationID             uuid.UUID       `db:"location_id"`
	CashierEmployeeID      uuid.UUID       `db:"cashier_employee_id"`
	POSTerminalID          *string         `db:"pos_terminal_id"`
	ActionType             string          `db:"action_type"`
	PerformedAt            time.Time       `db:"performed_at"`
	AuthorizedByEmployeeID *uuid.UUID      `db:"authorized_by_employee_id"`
	Details                json.RawMessage `db:"details"`
	Attributes             json.RawMessage `db:"attributes"`
	CreatedAt              time.Time       `db:"created_at"`
}

// CashDrawerEvent mirrors t.cash_drawer_events. No UpdatedAt.
type CashDrawerEvent struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	LocationID        uuid.UUID       `db:"location_id"`
	POSTerminalID     string          `db:"pos_terminal_id"`
	CashierEmployeeID *uuid.UUID      `db:"cashier_employee_id"`
	EventType         string          `db:"event_type"`
	EventAt           time.Time       `db:"event_at"`
	ExpectedAmount *string `db:"expected_amount"` // numeric — decimal.Decimal dep needed; using string for
	CountedAmount *string `db:"counted_amount"` // numeric — decimal.Decimal dep needed; using string for
	Variance *string `db:"variance"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	Reason            *string         `db:"reason"`
	PaidInOutAmount *string `db:"paid_in_out_amount"` // numeric — decimal.Decimal dep needed; using string for
	Reference         *string         `db:"reference"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
}

// ShiftEvent mirrors t.shift_events.
type ShiftEvent struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	LocationID           uuid.UUID       `db:"location_id"`
	POSTerminalID        string          `db:"pos_terminal_id"`
	CashierEmployeeID    uuid.UUID       `db:"cashier_employee_id"`
	ShiftStart           time.Time       `db:"shift_start"`
	ShiftEnd             *time.Time      `db:"shift_end"`
	TransactionCount     int32           `db:"transaction_count"`
	TotalSales *string `db:"total_sales"` // numeric — decimal.Decimal dep needed; using string for
	StartingDrawerAmount *string `db:"starting_drawer_amount"` // numeric — decimal.Decimal dep needed; using string for
	EndingDrawerAmount *string `db:"ending_drawer_amount"` // numeric — decimal.Decimal dep needed; using string for
	Attributes           json.RawMessage `db:"attributes"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}

// LoyaltyEvent mirrors t.loyalty_events. No UpdatedAt (append-only).
type LoyaltyEvent struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	LoyaltyMembershipID uuid.UUID       `db:"loyalty_membership_id"`
	TransactionID       *uuid.UUID      `db:"transaction_id"`
	EventType           string          `db:"event_type"`
	PointsDelta         int64           `db:"points_delta"`
	AmountBasis *string `db:"amount_basis"` // numeric — decimal.Decimal dep needed; using string for
	Reason              *string         `db:"reason"`
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
}

// GiftCardEvent mirrors t.gift_card_events. No UpdatedAt (append-only).
type GiftCardEvent struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	GiftCardID        uuid.UUID       `db:"gift_card_id"`
	TransactionID     *uuid.UUID      `db:"transaction_id"`
	EventType         string          `db:"event_type"`
	AmountDelta string `db:"amount_delta"` // numeric — decimal.Decimal dep needed; using string for
	BalanceAfter string `db:"balance_after"` // numeric — decimal.Decimal dep needed; using string for
	AuthorizationCode *string         `db:"authorization_code"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
}
