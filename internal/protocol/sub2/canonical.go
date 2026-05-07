// Package sub2 implements the Parse & Route subscriber — Node 4 of the
// Canary protocol pipeline (patent Application 63/991,596). Sub 2 reads
// the same canonical events stream as Sub 1 (under its own consumer
// group), dispatches each event to the registered POS adapter, and
// persists the resulting CanonicalEvent into the t.* tables.
//
// The package itself is infra-light. Dispatcher orchestration lives in
// dispatcher.go; persistence in store.go; the streams worker in
// worker.go. Source-specific parsing lives entirely under
// internal/adapters/<source_code>/. If a code path here references a
// specific POS, it is wrong.
package sub2

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
)

// CanonicalEvent is the parsed-but-not-yet-persisted intermediate. It
// mirrors the t.* schema closely so the persistence layer can write
// fields without further translation, but it is constructed entirely
// in memory by the adapter (no DB lookups here). The persistence layer
// owns ID generation, FK resolution (location_code → l.locations.id,
// employee_code → e.employees.id), and the UPSERT semantics.
//
// MerchantID and SourceCode together identify the inbound webhook
// envelope. The persistence layer resolves MerchantID → TenantID via
// app.merchants.tenant_id (1:1 mapping per 99_seed.sql comment) and
// stamps tenant_id onto every t.* row.
type CanonicalEvent struct {
	// Envelope metadata — copied verbatim from the gateway publisher.Event.
	EventID    uuid.UUID `json:"event_id"`
	MerchantID uuid.UUID `json:"merchant_id"`
	SourceCode string    `json:"source_code"`

	// SourceTxnID is the POS-native transaction identifier. Used for
	// idempotency on the UPSERT against (tenant_id, location_id,
	// business_date, transaction_number).
	SourceTxnID string `json:"source_txn_id"`

	// SourceLocationCode is the POS-native store/location code. The
	// persistence layer resolves this to l.locations.id via
	// (tenant_id, location_code).
	SourceLocationCode string `json:"source_location_code"`

	// SourceCashierCode is the POS-native cashier/employee code, optional.
	// Resolved to e.employees.id via (tenant_id, employee_code) when
	// non-empty.
	SourceCashierCode string `json:"source_cashier_code,omitempty"`

	// Transaction is the canonical t.transactions header. The
	// persistence layer fills in ID, TenantID, LocationID,
	// CashierEmployeeID after FK resolution.
	Transaction types.Transaction `json:"transaction"`

	// LineItems are the t.transaction_line_items rows. The persistence
	// layer fills ID, TenantID, TransactionID after the parent insert.
	LineItems []types.TransactionLineItem `json:"line_items,omitempty"`

	// Tenders are the t.transaction_tenders rows. SDD-conflict: the
	// dispatch brief refers to "tender_lines"; the schema (08_t_transactions.sql)
	// uses transaction_tenders. Schema wins.
	Tenders []types.TransactionTender `json:"tenders,omitempty"`

	// Discounts are the t.transaction_discounts rows. SDD-conflict:
	// dispatch brief refers to "discount_lines"; schema uses
	// transaction_discounts. Schema wins.
	Discounts []types.TransactionDiscount `json:"discounts,omitempty"`

	// CashDrawer is an optional drawer event tied to this transaction
	// (paid-in/out, no-sale, drawer count). Nil when not applicable.
	CashDrawer *types.CashDrawerEvent `json:"cash_drawer,omitempty"`

	// CashierActions records operator actions performed during the
	// transaction (overrides, lookups, manager swipes).
	CashierActions []types.CashierAction `json:"cashier_actions,omitempty"`

	// LoyaltyEvents are append-only loyalty earn/redeem events tied to
	// this transaction.
	LoyaltyEvents []types.LoyaltyEvent `json:"loyalty_events,omitempty"`

	// GiftCardEvents are append-only gift card activity events tied to
	// this transaction.
	GiftCardEvents []types.GiftCardEvent `json:"gift_card_events,omitempty"`

	// ParsedAt is the wall-clock time the adapter produced this
	// canonical event. Useful for diagnosing parse latency. Not
	// persisted directly.
	ParsedAt time.Time `json:"parsed_at"`

	// Attributes carries any source-specific fields the adapter wants
	// to round-trip without dropping. Stored on t.transactions.attributes
	// as JSONB for downstream re-parsing.
	Attributes json.RawMessage `json:"attributes,omitempty"`
}
