package counterpoint

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/adapters"
	"github.com/ruptiv/canary/internal/protocol/publisher"
)

const sampleTicket = `{
  "DocumentNumber": "DOC-12345",
  "DocumentType": "TKT",
  "DocumentDate": "2026-05-02T18:00:00Z",
  "StoreNumber": "01",
  "TerminalNumber": "01-01",
  "CashierNumber": "5",
  "Currency": "USD",
  "Subtotal": 18.49,
  "TaxAmount": 1.50,
  "DiscountAmount": 0,
  "Total": 19.99,
  "Lines": [
    {"LineNumber": 1, "ItemNumber": "SKU-001", "Description": "Widget",
     "Quantity": 1, "UnitOfMeasure": "EA", "Price": 18.49,
     "ExtendedPrice": 18.49}
  ],
  "Payments": [
    {"PaymentSequence": 1, "PaymentType": "VISA",
     "Amount": 19.99, "AuthorizationCode": "AUTH-001"}
  ]
}`

const sampleReturn = `{
  "DocumentNumber": "RET-100",
  "DocumentType": "RET",
  "DocumentDate": "2026-05-03T12:00:00Z",
  "StoreNumber": "02",
  "Currency": "USD",
  "Subtotal": -10.00,
  "Total": -10.00,
  "Lines": [
    {"LineNumber": 1, "ItemNumber": "SKU-001", "Description": "Widget",
     "Quantity": -1, "Price": 10.00, "ExtendedPrice": -10.00}
  ],
  "Payments": []
}`

func newEnvelope(payload string) publisher.Event {
	return publisher.Event{
		EventID:    uuid.New(),
		EventHash:  "test-hash",
		SourceCode: "counterpoint",
		MerchantID: uuid.New(),
		Timestamp:  time.Now().UTC(),
		IngestedAt: time.Now().UTC(),
		Payload:    json.RawMessage(payload),
		Nonce:      uuid.NewString(),
	}
}

func TestCounterpointParse_TicketHappyPath(t *testing.T) {
	a := New()
	env := newEnvelope(sampleTicket)

	canonical, err := a.Parse(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical == nil {
		t.Fatal("ticket parse returned nil canonical")
	}
	if canonical.SourceCode != "counterpoint" {
		t.Errorf("SourceCode = %q want counterpoint", canonical.SourceCode)
	}
	if canonical.SourceTxnID != "DOC-12345" {
		t.Errorf("SourceTxnID = %q want DOC-12345", canonical.SourceTxnID)
	}
	if canonical.SourceLocationCode != "01" {
		t.Errorf("SourceLocationCode = %q want 01", canonical.SourceLocationCode)
	}
	if canonical.SourceCashierCode != "5" {
		t.Errorf("SourceCashierCode = %q want 5", canonical.SourceCashierCode)
	}
	if canonical.Transaction.TransactionType != "sale" {
		t.Errorf("TransactionType = %q want sale", canonical.Transaction.TransactionType)
	}
	if canonical.Transaction.GrandTotal != "19.9900" {
		t.Errorf("GrandTotal = %q want 19.9900", canonical.Transaction.GrandTotal)
	}
	if len(canonical.LineItems) != 1 {
		t.Fatalf("LineItems len = %d want 1", len(canonical.LineItems))
	}
	if canonical.LineItems[0].Description != "Widget" {
		t.Errorf("LineItem description = %q want Widget", canonical.LineItems[0].Description)
	}
	if len(canonical.Tenders) != 1 {
		t.Fatalf("Tenders len = %d want 1", len(canonical.Tenders))
	}
	if canonical.Tenders[0].Amount != "19.9900" {
		t.Errorf("Tender amount = %q want 19.9900", canonical.Tenders[0].Amount)
	}
}

func TestCounterpointParse_ReturnTxnTypeIsRefund(t *testing.T) {
	a := New()
	canonical, err := a.Parse(newEnvelope(sampleReturn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical == nil {
		t.Fatal("return parse returned nil canonical")
	}
	if canonical.Transaction.TransactionType != "refund" {
		t.Errorf("TransactionType = %q want refund", canonical.Transaction.TransactionType)
	}
}

func TestCounterpointParse_QuoteDocumentDiscarded(t *testing.T) {
	a := New()
	canonical, err := a.Parse(newEnvelope(`{"DocumentNumber":"Q-1","DocumentType":"QTE","StoreNumber":"01"}`))
	if err != nil {
		t.Fatalf("parse: unexpected err %v", err)
	}
	if canonical != nil {
		t.Errorf("QTE document type should discard cleanly")
	}
}

func TestCounterpointParse_MalformedJSON_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	_, err := a.Parse(newEnvelope(`{not json`))
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload; got %v", err)
	}
}

func TestCounterpointParse_MissingDocumentNumber_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	_, err := a.Parse(newEnvelope(`{"DocumentType":"TKT","StoreNumber":"01"}`))
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload; got %v", err)
	}
}

func TestCounterpointParse_MissingStoreNumber_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	_, err := a.Parse(newEnvelope(`{"DocumentNumber":"D1","DocumentType":"TKT"}`))
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload; got %v", err)
	}
}

func TestCounterpointParse_QuantityAboveInt32_ReturnsErrInvalidQuantity(t *testing.T) {
	payload := `{
	  "DocumentNumber": "DOC-INT32",
	  "DocumentType": "TKT",
	  "StoreNumber": "01",
	  "Lines": [
	    {"LineNumber": 1, "ItemNumber": "SKU-001", "Quantity": ` + strconv.FormatInt(int64(math.MaxInt32)+1, 10) + `}
	  ]
	}`

	a := New()
	_, err := a.Parse(newEnvelope(payload))
	if !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("want ErrInvalidQuantity; got %v", err)
	}
}

func TestCounterpointAdapter_SourceCodeIsCounterpoint(t *testing.T) {
	a := New()
	if a.SourceCode() != "counterpoint" {
		t.Errorf("SourceCode = %q, want counterpoint", a.SourceCode())
	}
}
