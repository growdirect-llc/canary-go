// Package counterpoint implements the SourceAdapter for NCR
// Counterpoint. This is the keystone for Bart's wedge — proving the
// substrate generalizes beyond Square is the point of Loop 2.
//
// NCR Counterpoint exposes a REST API with a `/Documents` endpoint
// that returns sales documents. Each document carries a header plus
// an array of `Lines` (line items) and `Payments` (tenders). The
// payload shape is roughly:
//
//	{
//	  "DocumentNumber": "DOC-12345",
//	  "DocumentType": "TKT",
//	  "DocumentDate": "2026-05-02T18:00:00Z",
//	  "StoreNumber": "01",
//	  "TerminalNumber": "01-01",
//	  "CashierNumber": "5",
//	  "Total": 19.99,
//	  "TaxAmount": 1.50,
//	  "DiscountAmount": 0,
//	  "Currency": "USD",
//	  "Lines": [
//	    {"LineNumber": 1, "ItemNumber": "SKU-001", "Description": "Widget",
//	     "Quantity": 1, "Price": 18.49, "ExtendedPrice": 18.49}
//	  ],
//	  "Payments": [
//	    {"PaymentSequence": 1, "PaymentType": "VISA",
//	     "Amount": 19.99, "AuthorizationCode": "AUTH-001"}
//	  ]
//	}
//
// SDD-vague: pos-adapter-substrate.md doesn't enumerate the
// Counterpoint REST schema. The shape above is reconstructed from
// project_canary_native_labor_module_opportunity.md notes plus the
// memory_recall result for "NCR Counterpoint endpoint mapping". Treat
// this Loop 2 implementation as the substrate proof — Loop 3 hardens
// the field map against a real Counterpoint sandbox.
//
// SDD-missing: there is no Counterpoint webhook surface — Counterpoint
// is poll-driven (Bull adapter, Loop 4). Sub 2 will receive
// Counterpoint envelopes only after the Bull poller wraps each
// document in a publisher.Event and pushes it to protocol:events. For
// Loop 2 we treat the envelope.Payload as the raw Counterpoint
// document JSON, same as Square's payload-in-envelope convention.
package counterpoint

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/growdirect-llc/rapidpos/internal/adapters"
	"github.com/growdirect-llc/rapidpos/internal/db/types"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub2"
)

// SourceCode is the canonical identifier for this adapter.
const SourceCode = "counterpoint"

// Adapter is the NCR Counterpoint POS adapter.
type Adapter struct{}

// New constructs a Counterpoint adapter.
func New() *Adapter { return &Adapter{} }

// SourceCode satisfies adapters.SourceAdapter.
func (*Adapter) SourceCode() string { return SourceCode }

// Parse converts a Counterpoint document envelope into a CanonicalEvent.
//
// Document type mapping:
//
//   - "TKT"   (sales ticket) → transaction_type=sale
//   - "RET"   (return)        → transaction_type=refund
//   - "QTE"   (quote)         → discard (not a financial event)
//   - default                 → discard
//
// Loop 2 scope: header + line items + payments. Discounts and
// taxes per-line are not yet mapped — pulled into the parent
// totals only.
func (*Adapter) Parse(env adapters.Envelope) (*sub2.CanonicalEvent, error) {
	var doc cpDocument
	if err := json.Unmarshal(env.Payload, &doc); err != nil {
		return nil, fmt.Errorf("%w: counterpoint unmarshal: %v", adapters.ErrInvalidPayload, err)
	}

	var txnType string
	switch doc.DocumentType {
	case "TKT", "TICKET", "SALE":
		txnType = "sale"
	case "RET", "RETURN", "REFUND":
		txnType = "refund"
	default:
		// Quote, layaway, work-order — not a financial transaction.
		return nil, nil
	}

	if doc.DocumentNumber == "" {
		return nil, fmt.Errorf("%w: missing DocumentNumber", adapters.ErrInvalidPayload)
	}
	if doc.StoreNumber == "" {
		return nil, fmt.Errorf("%w: missing StoreNumber", adapters.ErrInvalidPayload)
	}

	eventTime := doc.DocumentDate
	if eventTime.IsZero() {
		eventTime = env.IngestedAt
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}

	currency := doc.Currency
	if currency == "" {
		currency = "USD"
	}

	canonical := &sub2.CanonicalEvent{
		EventID:            env.EventID,
		MerchantID:         env.MerchantID,
		SourceCode:         SourceCode,
		SourceTxnID:        doc.DocumentNumber,
		SourceLocationCode: doc.StoreNumber,
		SourceCashierCode:  doc.CashierNumber,
		ParsedAt:           time.Now().UTC(),
		Attributes:         env.Payload,
	}

	canonical.Transaction = types.Transaction{
		TransactionNumber: doc.DocumentNumber,
		TransactionType:   txnType,
		BusinessDate:      eventTime.UTC(),
		StartedAt:         eventTime.UTC(),
		EndedAt:           eventTime.UTC(),
		Status:            "completed",
		ItemCount:         int32(len(doc.Lines)),
		Subtotal:          decimalString(doc.Subtotal),
		TaxTotal:          decimalString(doc.TaxAmount),
		DiscountTotal:     decimalString(doc.DiscountAmount),
		GrandTotal:        decimalString(doc.Total),
		Currency:          currency,
		Channel:           "pos",
		POSTerminalID:     strPtrOrNil(doc.TerminalNumber),
		Attributes:        env.Payload,
		ExternalIDs:       buildExternalIDs(doc.DocumentNumber, doc.DocumentType),
	}

	for _, line := range doc.Lines {
		canonical.LineItems = append(canonical.LineItems, types.TransactionLineItem{
			LineNumber:     int32(line.LineNumber),
			BarcodeScanned: strPtrOrNil(line.ItemNumber),
			Description:    nonEmpty(line.Description, line.ItemNumber),
			Quantity:       decimalString(line.Quantity),
			UnitOfMeasure:  nonEmpty(line.UnitOfMeasure, "EA"),
			UnitPrice:      decimalString(line.Price),
			UnitDiscount:   decimalString(line.DiscountAmount),
			UnitTax:        decimalString(line.TaxAmount),
			IsReturn:       txnType == "refund",
		})
	}

	for _, pay := range doc.Payments {
		// Loop 3 Wave 1 (GRO-762 §B.2): leave TenderTypeID = uuid.Nil
		// — Sub2 store resolves the (tenant, source_code='counterpoint')
		// default from f.tender_types before insert. Adapters pkg
		// invariant preserved: parsers never invent FK IDs.
		canonical.Tenders = append(canonical.Tenders, types.TransactionTender{
			TenderSequence:    int32(pay.PaymentSequence),
			Amount:            decimalString(pay.Amount),
			Currency:          currency,
			AuthorizationCode: strPtrOrNil(pay.AuthorizationCode),
			CardBrand:         strPtrOrNil(pay.PaymentType),
		})
	}

	return canonical, nil
}

// cpDocument is the Loop 2 minimal shape of a Counterpoint sales
// document. Extended in Loop 3 with discounts, tax breakdowns, and
// loyalty fields.
type cpDocument struct {
	DocumentNumber string    `json:"DocumentNumber"`
	DocumentType   string    `json:"DocumentType"`
	DocumentDate   time.Time `json:"DocumentDate"`
	StoreNumber    string    `json:"StoreNumber"`
	TerminalNumber string    `json:"TerminalNumber"`
	CashierNumber  string    `json:"CashierNumber"`
	CustomerNumber string    `json:"CustomerNumber"`
	Currency       string    `json:"Currency"`
	Subtotal       float64   `json:"Subtotal"`
	TaxAmount      float64   `json:"TaxAmount"`
	DiscountAmount float64   `json:"DiscountAmount"`
	Total          float64   `json:"Total"`
	Lines          []cpLine  `json:"Lines"`
	Payments       []cpPay   `json:"Payments"`
}

type cpLine struct {
	LineNumber     int     `json:"LineNumber"`
	ItemNumber     string  `json:"ItemNumber"`
	Description    string  `json:"Description"`
	Quantity       float64 `json:"Quantity"`
	UnitOfMeasure  string  `json:"UnitOfMeasure"`
	Price          float64 `json:"Price"`
	ExtendedPrice  float64 `json:"ExtendedPrice"`
	DiscountAmount float64 `json:"DiscountAmount"`
	TaxAmount      float64 `json:"TaxAmount"`
}

type cpPay struct {
	PaymentSequence   int     `json:"PaymentSequence"`
	PaymentType       string  `json:"PaymentType"`
	Amount            float64 `json:"Amount"`
	AuthorizationCode string  `json:"AuthorizationCode"`
}

// decimalString renders a float64 with two decimal places — good
// enough for the schema's numeric(14,4) column. Loop 3 swaps for a
// proper decimal type once shopspring/decimal is approved.
func decimalString(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}

func buildExternalIDs(docNum, docType string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{
		"counterpoint_document_number": docNum,
		"counterpoint_document_type":   docType,
	})
	return b
}

func nonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Compile-time check.
var _ adapters.SourceAdapter = (*Adapter)(nil)
