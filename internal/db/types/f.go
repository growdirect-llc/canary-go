// Code generated from deploy/schema/07_p_f_pricing_finance.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// GLAccount mirrors f.gl_accounts.
type GLAccount struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	ParentID       *uuid.UUID      `db:"parent_id"`
	Code           string          `db:"code"`
	Name           string          `db:"name"`
	AccountType    string          `db:"account_type"`
	AccountSubtype *string         `db:"account_subtype"`
	IsPostable     bool            `db:"is_postable"`
	Currency       string          `db:"currency"`
	Attributes     json.RawMessage `db:"attributes"`
	Status         string          `db:"status"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// TenderType mirrors f.tender_types.
type TenderType struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	Code           string          `db:"code"`
	Name           string          `db:"name"`
	TenderClass    string          `db:"tender_class"`
	IsActive       bool            `db:"is_active"`
	IsChangeGiving bool            `db:"is_change_giving"`
	IsRefundable   bool            `db:"is_refundable"`
	OpenDrawer     bool            `db:"open_drawer"`
	GLAccountID    *uuid.UUID      `db:"gl_account_id"`
	RoundingRule   *string         `db:"rounding_rule"`
	Attributes     json.RawMessage `db:"attributes"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// SupplierInvoice mirrors f.supplier_invoices.
type SupplierInvoice struct {
	ID                       uuid.UUID       `db:"id"`
	TenantID                 uuid.UUID       `db:"tenant_id"`
	InvoiceNumber            string          `db:"invoice_number"`
	VendorID                 uuid.UUID       `db:"vendor_id"`
	InvoiceDate              time.Time       `db:"invoice_date"`
	DueDate                  *time.Time      `db:"due_date"`
	RelatedPOID              *uuid.UUID      `db:"related_po_id"`
	RelatedReceiptDocumentID *uuid.UUID      `db:"related_receipt_document_id"`
	Status                   string          `db:"status"`
	Subtotal string `db:"subtotal"` // numeric — decimal.Decimal dep needed; using string for
	TaxTotal string `db:"tax_total"` // numeric — decimal.Decimal dep needed; using string for
	ShippingTotal string `db:"shipping_total"` // numeric — decimal.Decimal dep needed; using string for
	DiscountTotal string `db:"discount_total"` // numeric — decimal.Decimal dep needed; using string for
	GrandTotal string `db:"grand_total"` // numeric — decimal.Decimal dep needed; using string for
	Currency                 string          `db:"currency"`
	MatchStatus              string          `db:"match_status"`
	MatchVariance *string `db:"match_variance"` // numeric — decimal.Decimal dep needed; using string for
	ApprovalUserID           *uuid.UUID      `db:"approval_user_id"`
	ApprovedAt               *time.Time      `db:"approved_at"`
	Attributes               json.RawMessage `db:"attributes"`
	CreatedAt                time.Time       `db:"created_at"`
	UpdatedAt                time.Time       `db:"updated_at"`
}

// SupplierInvoiceLine mirrors f.supplier_invoice_lines.
type SupplierInvoiceLine struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	InvoiceID            uuid.UUID       `db:"invoice_id"`
	LineNumber           int32           `db:"line_number"`
	RelatedPOLineID      *uuid.UUID      `db:"related_po_line_id"`
	RelatedReceiptLineID *uuid.UUID      `db:"related_receipt_line_id"`
	ItemID               *uuid.UUID      `db:"item_id"`
	Description          string          `db:"description"`
	Quantity *string `db:"quantity"` // numeric — decimal.Decimal dep needed; using string for
	UnitCost *string `db:"unit_cost"` // numeric — decimal.Decimal dep needed; using string for
	LineTotal string `db:"line_total"` // numeric — decimal.Decimal dep needed; using string for
	TaxAmount string `db:"tax_amount"` // numeric — decimal.Decimal dep needed; using string for
	GLAccountID          *uuid.UUID      `db:"gl_account_id"`
	MatchVariance *string `db:"match_variance"` // numeric — decimal.Decimal dep needed; using string for
	Attributes           json.RawMessage `db:"attributes"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}

// Payment mirrors f.payments.
type Payment struct {
	ID              uuid.UUID       `db:"id"`
	TenantID        uuid.UUID       `db:"tenant_id"`
	PaymentNumber   string          `db:"payment_number"`
	VendorID        uuid.UUID       `db:"vendor_id"`
	PaymentMethod   string          `db:"payment_method"`
	PaymentDate     time.Time       `db:"payment_date"`
	Amount string `db:"amount"` // numeric — decimal.Decimal dep needed; using string for
	Currency        string          `db:"currency"`
	BankAccountID   *uuid.UUID      `db:"bank_account_id"`
	ReferenceNumber *string         `db:"reference_number"`
	Status          string          `db:"status"`
	ClearedAt       *time.Time      `db:"cleared_at"`
	Attributes      json.RawMessage `db:"attributes"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

// PaymentInvoiceApplication mirrors f.payment_invoice_applications.
type PaymentInvoiceApplication struct {
	ID            uuid.UUID `db:"id"`
	TenantID      uuid.UUID `db:"tenant_id"`
	PaymentID     uuid.UUID `db:"payment_id"`
	InvoiceID     uuid.UUID `db:"invoice_id"`
	AmountApplied string `db:"amount_applied"` // numeric — decimal.Decimal dep needed; using string for
	CreatedAt     time.Time `db:"created_at"`
}
