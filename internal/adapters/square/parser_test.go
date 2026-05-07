package square

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/adapters"
	"github.com/growdirect-llc/rapidpos/internal/protocol/publisher"
)

const samplePaymentCreated = `{
  "merchant_id": "ML123ABC",
  "type": "payment.created",
  "event_id": "evt-001",
  "created_at": "2026-05-02T18:00:00Z",
  "data": {
    "type": "payment",
    "id": "pay-001",
    "object": {
      "payment": {
        "id": "pay-001",
        "order_id": "ord-001",
        "location_id": "L-MAIN",
        "status": "COMPLETED",
        "amount_money": {"amount": 1999, "currency": "USD"},
        "created_at": "2026-05-02T18:00:00Z",
        "card_details": {
          "status": "CAPTURED",
          "auth_code": "AUTH-001",
          "entry_method": "CONTACTLESS",
          "card": {"card_brand": "VISA", "last_4": "4242"}
        },
        "employee_id": "emp-005"
      }
    }
  }
}`

const sampleRefundCreated = `{
  "merchant_id": "ML123ABC",
  "type": "refund.created",
  "data": {
    "type": "refund",
    "id": "rf-001",
    "object": {
      "refund": {
        "id": "rf-001",
        "payment_id": "pay-001",
        "order_id": "ord-001",
        "location_id": "L-MAIN",
        "status": "COMPLETED",
        "amount_money": {"amount": 500, "currency": "USD"},
        "reason": "customer_requested",
        "created_at": "2026-05-02T19:00:00Z"
      }
    }
  }
}`

func newEnvelope(payload string) publisher.Event {
	return publisher.Event{
		EventID:    uuid.New(),
		EventHash:  "test-hash",
		SourceCode: "square",
		MerchantID: uuid.New(),
		Timestamp:  time.Now().UTC(),
		IngestedAt: time.Now().UTC(),
		Payload:    json.RawMessage(payload),
		Nonce:      uuid.NewString(),
	}
}

func TestSquareParse_PaymentCreated_HappyPath(t *testing.T) {
	a := New()
	env := newEnvelope(samplePaymentCreated)

	canonical, err := a.Parse(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical == nil {
		t.Fatal("parse returned nil canonical event")
	}

	if canonical.SourceCode != "square" {
		t.Errorf("SourceCode = %q want square", canonical.SourceCode)
	}
	if canonical.SourceTxnID != "pay-001" {
		t.Errorf("SourceTxnID = %q want pay-001", canonical.SourceTxnID)
	}
	if canonical.SourceLocationCode != "L-MAIN" {
		t.Errorf("SourceLocationCode = %q want L-MAIN", canonical.SourceLocationCode)
	}
	if canonical.SourceCashierCode != "emp-005" {
		t.Errorf("SourceCashierCode = %q want emp-005", canonical.SourceCashierCode)
	}
	if canonical.Transaction.TransactionType != "sale" {
		t.Errorf("TransactionType = %q want sale", canonical.Transaction.TransactionType)
	}
	if canonical.Transaction.GrandTotal != "19.99" {
		t.Errorf("GrandTotal = %q want 19.99", canonical.Transaction.GrandTotal)
	}
	if canonical.Transaction.Currency != "USD" {
		t.Errorf("Currency = %q want USD", canonical.Transaction.Currency)
	}
	if canonical.Transaction.Status != "completed" {
		t.Errorf("Status = %q want completed", canonical.Transaction.Status)
	}
}

func TestSquareParse_RefundCreated_TxnTypeIsRefund(t *testing.T) {
	a := New()
	env := newEnvelope(sampleRefundCreated)

	canonical, err := a.Parse(env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical == nil {
		t.Fatal("parse returned nil canonical event")
	}
	if canonical.Transaction.TransactionType != "refund" {
		t.Errorf("TransactionType = %q want refund", canonical.Transaction.TransactionType)
	}
	if canonical.Transaction.GrandTotal != "5.00" {
		t.Errorf("GrandTotal = %q want 5.00", canonical.Transaction.GrandTotal)
	}
	if canonical.Transaction.VoidReason == nil || *canonical.Transaction.VoidReason != "customer_requested" {
		got := "<nil>"
		if canonical.Transaction.VoidReason != nil {
			got = *canonical.Transaction.VoidReason
		}
		t.Errorf("VoidReason = %q want customer_requested", got)
	}
}

func TestSquareParse_UnknownEventType_DiscardsCleanly(t *testing.T) {
	a := New()
	env := newEnvelope(`{"type":"customer.updated","data":{}}`)
	canonical, err := a.Parse(env)
	if err != nil {
		t.Fatalf("parse: unexpected err %v", err)
	}
	if canonical != nil {
		t.Errorf("unknown event type should discard (nil), got non-nil")
	}
}

func TestSquareParse_MalformedJSON_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	env := newEnvelope(`{not json`)
	_, err := a.Parse(env)
	if err == nil {
		t.Fatal("malformed json must error")
	}
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want errors.Is(err, ErrInvalidPayload); got %v", err)
	}
}

func TestSquareParse_PaymentMissingPaymentObject_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	env := newEnvelope(`{"type":"payment.created","data":{"object":{}}}`)
	_, err := a.Parse(env)
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload; got %v", err)
	}
}

func TestSquareParse_PaymentMissingLocationID_ReturnsErrInvalidPayload(t *testing.T) {
	a := New()
	env := newEnvelope(`{
	  "type":"payment.created",
	  "data":{"object":{"payment":{
	    "id":"pay-x",
	    "amount_money":{"amount":100,"currency":"USD"}
	  }}}
	}`)
	_, err := a.Parse(env)
	if !errors.Is(err, adapters.ErrInvalidPayload) {
		t.Errorf("want ErrInvalidPayload; got %v", err)
	}
}

func TestCentsToDecimalString(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{1234, "12.34"},
		{1000000, "10000.00"},
		{-150, "-1.50"},
	}
	for _, c := range cases {
		got := centsToDecimalString(c.in)
		if got != c.want {
			t.Errorf("centsToDecimalString(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSquareAdapter_SourceCodeIsSquare(t *testing.T) {
	a := New()
	if a.SourceCode() != "square" {
		t.Errorf("SourceCode = %q, want square", a.SourceCode())
	}
}
