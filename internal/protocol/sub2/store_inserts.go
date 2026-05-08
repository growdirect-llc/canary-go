package sub2

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/ruptiv/canary/internal/db/types"
)

// CLAUDE.md / CanaryGo says "no raw SQL strings outside sqlc" but the
// dispatch overrides this for Loop 2 — sqlc retrofit is Loop 3. All
// inserts here are parameterized and rely on the schema files in
// deploy/schema/ as source of truth.

// nullableJSON returns nil when raw is len 0 (so Postgres uses the
// column DEFAULT '{}'); otherwise returns raw. Without this, json.RawMessage
// nil values would be sent as a literal NULL and fail the NOT NULL
// constraints on .attributes columns.
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return []byte(raw)
}

func insertTransaction(ctx context.Context, tx pgx.Tx, t *types.Transaction) error {
	const q = `
		INSERT INTO transaction.transactions (
		  id, tenant_id, transaction_number, transaction_type,
		  parent_transaction_id, location_id, pos_terminal_id,
		  cashier_employee_id, customer_id, loyalty_membership_id,
		  business_date, started_at, ended_at, status, ticket_number,
		  item_count, subtotal, tax_total, discount_total, grand_total,
		  currency, channel, pos_software_version, is_training_mode,
		  is_offline, is_reentered, is_suspended, void_reason,
		  attributes, external_ids
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		  $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
		  $21, $22, $23, $24, $25, $26, $27, $28, $29, $30
		)
	`
	_, err := tx.Exec(ctx, q,
		t.ID, t.TenantID, t.TransactionNumber, t.TransactionType,
		t.ParentTransactionID, t.LocationID, t.POSTerminalID,
		t.CashierEmployeeID, t.CustomerID, t.LoyaltyMembershipID,
		t.BusinessDate, t.StartedAt, t.EndedAt, t.Status, t.TicketNumber,
		t.ItemCount, t.Subtotal, t.TaxTotal, t.DiscountTotal, t.GrandTotal,
		t.Currency, t.Channel, t.POSSoftwareVersion, t.IsTrainingMode,
		t.IsOffline, t.IsReentered, t.IsSuspended, t.VoidReason,
		nullableJSON(t.Attributes), nullableJSON(t.ExternalIDs),
	)
	return err
}

// insertLineItem omits the GENERATED columns (extended_price,
// extended_tax, line_total, margin) — Postgres computes those.
func insertLineItem(ctx context.Context, tx pgx.Tx, li *types.TransactionLineItem) error {
	const q = `
		INSERT INTO transaction.transaction_line_items (
		  id, tenant_id, transaction_id, line_number, item_id,
		  barcode_scanned, description, quantity, unit_of_measure,
		  unit_price, list_price, unit_discount, unit_tax,
		  cost_basis, category_id, zone_id, lot_id,
		  inventory_movement_id, is_void, void_reason, is_return,
		  return_reason, is_weighable, is_food_stamp_eligible,
		  attributes
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		  $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
		  $21, $22, $23, $24, $25
		)
	`
	_, err := tx.Exec(ctx, q,
		li.ID, li.TenantID, li.TransactionID, li.LineNumber, li.ItemID,
		li.BarcodeScanned, li.Description, li.Quantity, li.UnitOfMeasure,
		li.UnitPrice, li.ListPrice, li.UnitDiscount, li.UnitTax,
		li.CostBasis, li.CategoryID, li.ZoneID, li.LotID,
		li.InventoryMovementID, li.IsVoid, li.VoidReason, li.IsReturn,
		li.ReturnReason, li.IsWeighable, li.IsFoodStampEligible,
		nullableJSON(li.Attributes),
	)
	return err
}

func insertTender(ctx context.Context, tx pgx.Tx, t *types.TransactionTender) error {
	const q = `
		INSERT INTO transaction.transaction_tenders (
		  id, tenant_id, transaction_id, tender_sequence, tender_type_id,
		  amount, currency, cash_back_amount, change_amount,
		  card_token, card_last_4, card_brand, authorization_code,
		  processor_reference, is_voided, is_refund, contactless,
		  attributes
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		  $11, $12, $13, $14, $15, $16, $17, $18
		)
	`
	_, err := tx.Exec(ctx, q,
		t.ID, t.TenantID, t.TransactionID, t.TenderSequence, t.TenderTypeID,
		t.Amount, t.Currency, t.CashBackAmount, t.ChangeAmount,
		t.CardToken, t.CardLast4, t.CardBrand, t.AuthorizationCode,
		t.ProcessorReference, t.IsVoided, t.IsRefund, t.Contactless,
		nullableJSON(t.Attributes),
	)
	return err
}

func insertDiscount(ctx context.Context, tx pgx.Tx, d *types.TransactionDiscount) error {
	const q = `
		INSERT INTO transaction.transaction_discounts (
		  id, tenant_id, transaction_id, discount_sequence, scope,
		  line_item_id, discount_type, source_promotion_id,
		  promotion_rule_id, amount, percentage, reason_code,
		  authorized_by_employee_id, attributes
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		  $11, $12, $13, $14
		)
	`
	_, err := tx.Exec(ctx, q,
		d.ID, d.TenantID, d.TransactionID, d.DiscountSequence, d.Scope,
		d.LineItemID, d.DiscountType, d.SourcePromotionID,
		d.PromotionRuleID, d.Amount, d.Percentage, d.ReasonCode,
		d.AuthorizedByEmployeeID, nullableJSON(d.Attributes),
	)
	return err
}

// insertCashDrawerEvent omits the GENERATED variance column.
func insertCashDrawerEvent(ctx context.Context, tx pgx.Tx, e *types.CashDrawerEvent) error {
	const q = `
		INSERT INTO transaction.cash_drawer_events (
		  id, tenant_id, location_id, pos_terminal_id, cashier_employee_id,
		  event_type, event_at, expected_amount, counted_amount,
		  reason, paid_in_out_amount, reference, attributes
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`
	_, err := tx.Exec(ctx, q,
		e.ID, e.TenantID, e.LocationID, e.POSTerminalID, e.CashierEmployeeID,
		e.EventType, e.EventAt, e.ExpectedAmount, e.CountedAmount,
		e.Reason, e.PaidInOutAmount, e.Reference, nullableJSON(e.Attributes),
	)
	return err
}

func insertCashierAction(ctx context.Context, tx pgx.Tx, a *types.CashierAction) error {
	const q = `
		INSERT INTO transaction.cashier_actions (
		  id, tenant_id, transaction_id, location_id,
		  cashier_employee_id, pos_terminal_id, action_type,
		  performed_at, authorized_by_employee_id, details, attributes
		) VALUES (
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
	`
	_, err := tx.Exec(ctx, q,
		a.ID, a.TenantID, a.TransactionID, a.LocationID,
		a.CashierEmployeeID, a.POSTerminalID, a.ActionType,
		a.PerformedAt, a.AuthorizedByEmployeeID,
		nullableJSON(a.Details), nullableJSON(a.Attributes),
	)
	return err
}

func insertLoyaltyEvent(ctx context.Context, tx pgx.Tx, l *types.LoyaltyEvent) error {
	const q = `
		INSERT INTO transaction.loyalty_events (
		  id, tenant_id, loyalty_membership_id, transaction_id,
		  event_type, points_delta, amount_basis, reason, attributes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.Exec(ctx, q,
		l.ID, l.TenantID, l.LoyaltyMembershipID, l.TransactionID,
		l.EventType, l.PointsDelta, l.AmountBasis, l.Reason,
		nullableJSON(l.Attributes),
	)
	return err
}

func insertGiftCardEvent(ctx context.Context, tx pgx.Tx, g *types.GiftCardEvent) error {
	const q = `
		INSERT INTO transaction.gift_card_events (
		  id, tenant_id, gift_card_id, transaction_id, event_type,
		  amount_delta, balance_after, authorization_code, attributes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := tx.Exec(ctx, q,
		g.ID, g.TenantID, g.GiftCardID, g.TransactionID, g.EventType,
		g.AmountDelta, g.BalanceAfter, g.AuthorizationCode,
		nullableJSON(g.Attributes),
	)
	return err
}
