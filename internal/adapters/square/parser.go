// Package square implements the SourceAdapter for Square POS webhooks.
// It is the reference implementation of the adapter substrate — every
// other POS adapter (counterpoint, clover, future) follows this shape.
//
// Scope for Loop 2: parse payment.created / payment.updated /
// refund.created envelopes into a CanonicalEvent. Square's webhook
// payload is a JSON envelope shaped:
//
//	{
//	 "merchant_id": "MLXXXXXXXXX",
//	 "type": "payment.created",
//	 "event_id": "...",
//	 "created_at": "2026-05-02T18:00:00Z",
//	 "data": {
//	 "type": "payment",
//	 "id": "...",
//	 "object": { "payment": { ... } }
//	 }
//	}
//
// SDD-vague: pos-adapter-substrate.md does not enumerate every Square
// payload field. We map the fields we know about and round-trip the
// raw body through CanonicalEvent.Attributes for downstream re-parsing.
package square

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ruptiv/canary/internal/adapters"
	"github.com/ruptiv/canary/internal/db/types"
	"github.com/ruptiv/canary/internal/protocol/publisher"
	"github.com/ruptiv/canary/internal/protocol/sub2"
)

// SourceCode is the canonical identifier for this adapter. Must match
// app.source_systems.code (seeded by 99_seed.sql).
const SourceCode = "square"

// Adapter is the Square POS adapter.
type Adapter struct{}

// New constructs a Square adapter. No state — the adapter is a pure
// function over envelope bytes.
func New() *Adapter { return &Adapter{} }

// SourceCode satisfies adapters.SourceAdapter.
func (*Adapter) SourceCode() string { return SourceCode }

// Parse converts a Square webhook envelope into a CanonicalEvent.
//
// Behavior matrix:
//
// - "payment.created" / "payment.updated" → t.transactions header +
// a single t.transaction_tenders row. Line items are not present
// in the payment webhook (Square pushes them via order webhooks);
// we leave LineItems empty for
// - "refund.created" → t.transactions header with transaction_type=refund
// and a tender row for the refund amount.
// - Anything else → (nil, nil) discard.
// - Malformed JSON or missing required fields → (nil, ErrInvalidPayload).
func (*Adapter) Parse(env adapters.Envelope) (*sub2.CanonicalEvent, error) {
	var sq squareEnvelope
	if err := json.Unmarshal(env.Payload, &sq); err != nil {
		return nil, fmt.Errorf("%w: square unmarshal: %v", adapters.ErrInvalidPayload, err)
	}

	switch sq.Type {
	case "payment.created", "payment.updated":
		return parsePayment(env, sq, "sale")
	case "refund.created", "refund.updated":
		return parsePayment(env, sq, "refund")
	default:
		// Test pings, customer events, inventory events — discard cleanly.
		return nil, nil
	}
}

// squareEnvelope mirrors the Square webhook outer shape.
type squareEnvelope struct {
	MerchantID string          `json:"merchant_id"`
	Type       string          `json:"type"`
	EventID    string          `json:"event_id"`
	CreatedAt  time.Time       `json:"created_at"`
	Data       squareData      `json:"data"`
	RawObject  json.RawMessage `json:"-"`
}

type squareData struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Object squareObject   `json:"object"`
}

type squareObject struct {
	Payment *squarePayment `json:"payment,omitempty"`
	Refund  *squareRefund  `json:"refund,omitempty"`
}

type squarePayment struct {
	ID          string         `json:"id"`
	OrderID     string         `json:"order_id"`
	LocationID  string         `json:"location_id"`
	Status      string         `json:"status"`
	AmountMoney squareMoney    `json:"amount_money"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	CardDetails *squareCard    `json:"card_details,omitempty"`
	CashDetails *squareCash    `json:"cash_details,omitempty"`
	EmployeeID  string         `json:"employee_id,omitempty"`
	TeamMemberID string        `json:"team_member_id,omitempty"`
	Note        string         `json:"note,omitempty"`
}

type squareRefund struct {
	ID          string      `json:"id"`
	PaymentID   string      `json:"payment_id"`
	OrderID     string      `json:"order_id"`
	LocationID  string      `json:"location_id"`
	Status      string      `json:"status"`
	AmountMoney squareMoney `json:"amount_money"`
	Reason      string      `json:"reason"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type squareMoney struct {
	Amount int64 `json:"amount"` // Square denominates in minor units (cents)
	Currency string `json:"currency"` // ISO 4217
}

type squareCard struct {
	Status         string         `json:"status"`
	Card           squareCardInfo `json:"card"`
	EntryMethod    string         `json:"entry_method"`
	AuthCode       string         `json:"auth_code"`
	StatementDesc  string         `json:"statement_description"`
}

type squareCardInfo struct {
	CardBrand string `json:"card_brand"`
	Last4     string `json:"last_4"`
}

type squareCash struct {
	BuyerSuppliedMoney squareMoney `json:"buyer_supplied_money"`
	ChangeBackMoney    squareMoney `json:"change_back_money"`
}

// parsePayment is the shared mapping used for payment.* and refund.*
// envelopes. txnType is "sale" or "refund" — the rest of the field
// translation is identical.
func parsePayment(env publisher.Event, sq squareEnvelope, txnType string) (*sub2.CanonicalEvent, error) {
	payment := sq.Data.Object.Payment
	refund := sq.Data.Object.Refund

	if txnType == "sale" && payment == nil {
		return nil, fmt.Errorf("%w: payment.* envelope missing data.object.payment", adapters.ErrInvalidPayload)
	}
	if txnType == "refund" && refund == nil {
		// SDD-vague: the Square API exposes refund.created webhooks
		// but the docs don't enumerate every payload variant. If a
		// refund envelope arrives without a refund object, treat it
		// as malformed — better to dead-letter and inspect than to
		// invent fields.
		return nil, fmt.Errorf("%w: refund.* envelope missing data.object.refund", adapters.ErrInvalidPayload)
	}

	// Compose the canonical record. Field translations:
	//
	// Square cents → schema numeric(14,4) string. We divide by 100 and
	// format with two decimal places; the schema is happy with strings
	// that pgx will route into the numeric column.
	//
	// SDD-conflict: pos-adapter-substrate.md (CRDM table) requires
	// amount_cents as an integer; the schema (08_t_transactions.sql)
	// uses numeric(14,4). Schema wins — we store dollars-with-decimals.
	var (
		money         squareMoney
		sourceTxnID   string
		locationID    string
		employeeCode  string
		eventTime     time.Time
		voidReason    string
		card          *squareCard
		cash          *squareCash
		parentTxnExt  string
	)
	if txnType == "sale" {
		money = payment.AmountMoney
		sourceTxnID = payment.ID
		locationID = payment.LocationID
		employeeCode = firstNonEmpty(payment.EmployeeID, payment.TeamMemberID)
		eventTime = payment.CreatedAt
		card = payment.CardDetails
		cash = payment.CashDetails
	} else {
		money = refund.AmountMoney
		sourceTxnID = refund.ID
		locationID = refund.LocationID
		employeeCode = ""
		eventTime = refund.CreatedAt
		voidReason = refund.Reason
		parentTxnExt = refund.PaymentID
	}

	if sourceTxnID == "" {
		return nil, fmt.Errorf("%w: missing payment/refund id", adapters.ErrInvalidPayload)
	}
	if locationID == "" {
		return nil, fmt.Errorf("%w: missing location_id", adapters.ErrInvalidPayload)
	}
	if eventTime.IsZero() {
		eventTime = sq.CreatedAt
	}
	if eventTime.IsZero() {
		eventTime = env.IngestedAt
	}

	currency := money.Currency
	if currency == "" {
		currency = "USD"
	}
	amount := centsToDecimalString(money.Amount)

	canonical := &sub2.CanonicalEvent{
		EventID:            env.EventID,
		MerchantID:         env.MerchantID,
		SourceCode:         SourceCode,
		SourceTxnID:        sourceTxnID,
		SourceLocationCode: locationID,
		SourceCashierCode:  employeeCode,
		ParsedAt:           time.Now().UTC(),
		Attributes: env.Payload, // round-trip the raw payload
	}

	canonical.Transaction = types.Transaction{
		TransactionNumber:   sourceTxnID,
		TransactionType:     txnType,
		BusinessDate:        eventTime.UTC(),
		StartedAt:           eventTime.UTC(),
		EndedAt:             eventTime.UTC(),
		Status:              "completed",
		ItemCount:           0,
		Subtotal:            amount,
		TaxTotal:            "0",
		DiscountTotal:       "0",
		GrandTotal:          amount,
		Currency:            currency,
		Channel:             "pos",
		IsTrainingMode:      false,
		IsOffline:           false,
		IsReentered:         false,
		IsSuspended:         false,
		Attributes:          env.Payload,
		ExternalIDs:         buildExternalIDs(sourceTxnID, parentTxnExt),
	}
	if voidReason != "" {
		canonical.Transaction.VoidReason = &voidReason
	}

	// Tender row for the payment.: emit
	// the tender with TenderTypeID = uuid.Nil; Sub2 store resolves the
	// (tenant, source_code) default tender_type_id from f.tender_types
	// before insert (see internal/protocol/sub2/store.go
	// resolveTenderTypeID). Adapters pkg invariant preserved: parsers
	// never invent FK IDs — Sub2 owns FK resolution. Wave 2 will refine
	// to brand-specific tender_type_ids using card.CardBrand.
	tender := types.TransactionTender{
		TenderSequence: 1,
		Amount:         amount,
		Currency:       currency,
		IsRefund:       txnType == "refund",
	}
	if card != nil {
		tender.CardBrand = strPtrOrNil(card.Card.CardBrand)
		tender.CardLast4 = strPtrOrNil(card.Card.Last4)
		tender.AuthorizationCode = strPtrOrNil(card.AuthCode)
		tender.Contactless = card.EntryMethod == "CONTACTLESS"
	}
	if cash != nil {
		tender.ChangeAmount = centsToDecimalString(cash.ChangeBackMoney.Amount)
	}
	canonical.Tenders = append(canonical.Tenders, tender)

	return canonical, nil
}

// buildExternalIDs packs the POS-native IDs into a JSONB blob the
// downstream layer can index. Lives on t.transactions.external_ids
// (which is GIN-indexed).
func buildExternalIDs(squareID, parentSquareID string) json.RawMessage {
	out := map[string]string{"square_payment_id": squareID}
	if parentSquareID != "" {
		out["square_parent_payment_id"] = parentSquareID
	}
	b, _ := json.Marshal(out)
	return b
}

// centsToDecimalString converts Square's minor-unit integer to a
// numeric-friendly string. 1234 → "12.34".
func centsToDecimalString(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	dollars := cents / 100
	rem := cents % 100
	return neg + strconv.FormatInt(dollars, 10) + "." + zeroPad2(rem)
}

func zeroPad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

func firstNonEmpty(vals ...string) string {
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

// Sentinel for staticcheck — keeps errors imported even if reorder elides it.
var _ = errors.Is
