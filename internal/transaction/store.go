// internal/transaction/store.go
//
// pgxpool-backed access to t.transactions and child tables. The
// canonical schema's child tables (t.transaction_line_items, _tenders,
// _discounts) have richer shapes than the wire DTOs — line_total /
// extended_price / extended_tax are GENERATED columns, tender_type_id
// is required NOT NULL and FKs f.tender_types, discounts have a
// scope + discount_type + sequence model. The store mediates: callers
// pass the wire-shape CreateRequest, the store maps to canonical
// columns and writes.
//
// Spec: GRO-764 Phase B.1.

package transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Store is the pgx-backed access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Sentinels mapped to HTTP status codes by the handler.
var (
	ErrNotFound   = errors.New("transaction: not found")
	ErrConflict   = errors.New("transaction: conflict")
	ErrValidation = errors.New("transaction: validation failed")
)

// Create persists a header + all children inside one transaction.
// Computes header rollups from line items / discounts unless the
// caller pre-populated them. line_total at the line-item level is a
// GENERATED column; we write quantity + unit_price + unit_tax +
// unit_discount and let postgres compute it.
func (s *Store) Create(ctx context.Context, req CreateRequest) (*TransactionDTO, error) {
	if req.TenantID == uuid.Nil {
		return nil, fmt.Errorf("%w: tenant_id required", ErrValidation)
	}
	if req.LocationID == uuid.Nil {
		return nil, fmt.Errorf("%w: location_id required", ErrValidation)
	}
	if req.TransactionNumber == "" {
		return nil, fmt.Errorf("%w: transaction_number required", ErrValidation)
	}
	if req.TransactionType == "" {
		req.TransactionType = "sale"
	}
	if req.Status == "" {
		req.Status = "completed"
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	if req.Channel == "" {
		req.Channel = "pos"
	}

	// Compute rollups from line items + discounts.
	subtotal := decimal.Zero
	tax := decimal.Zero
	discount := decimal.Zero
	for _, li := range req.LineItems {
		subtotal = subtotal.Add(li.LineTotal)
		tax = tax.Add(li.TaxAmount)
	}
	for _, d := range req.Discounts {
		discount = discount.Add(d.Amount)
	}
	grandTotal := subtotal.Add(tax).Sub(discount)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("transaction: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertTxQ = `
		INSERT INTO t.transactions (
			tenant_id, transaction_number, transaction_type, parent_transaction_id,
			location_id, pos_terminal_id, cashier_employee_id, customer_id,
			loyalty_membership_id, business_date, started_at, ended_at, status,
			ticket_number, item_count, subtotal, tax_total, discount_total,
			grand_total, currency, channel, pos_software_version,
			is_training_mode, is_offline, attributes, external_ids
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date, $11, $12, $13,
			$14, $15, $16::numeric, $17::numeric, $18::numeric, $19::numeric,
			$20, $21, $22, $23, $24, COALESCE($25::jsonb, '{}'::jsonb), COALESCE($26::jsonb, '{}'::jsonb)
		)
		RETURNING ` + selectColumns

	row := tx.QueryRow(ctx, insertTxQ,
		req.TenantID, req.TransactionNumber, req.TransactionType, req.ParentTransactionID,
		req.LocationID, req.POSTerminalID, req.CashierEmployeeID, req.CustomerID,
		req.LoyaltyMembershipID, req.BusinessDate, req.StartedAt, req.EndedAt, req.Status,
		req.TicketNumber, len(req.LineItems), subtotal, tax, discount,
		grandTotal, req.Currency, req.Channel, req.POSSoftwareVersion,
		req.IsTrainingMode, req.IsOffline, jsonbBytes(req.Attributes), jsonbBytes(req.ExternalIDs),
	)
	out, err := scanTransaction(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return nil, fmt.Errorf("transaction: insert header: %w", err)
	}

	if err := insertChildren(ctx, tx, out.ID, out.TenantID, req); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("transaction: commit: %w", err)
	}

	out.LineItems = toLineItemDTOs(out.ID, req.LineItems)
	out.Tenders = toTenderDTOs(out.ID, req.Tenders, req.Currency)
	out.Discounts = toDiscountDTOs(out.ID, req.Discounts)
	return out, nil
}

func insertChildren(ctx context.Context, tx pgx.Tx, txID, tenantID uuid.UUID, req CreateRequest) error {
	// Line items: line_total is GENERATED; we write quantity +
	// unit_price + unit_tax. The wire DTO's LineTotal is recomputed
	// downstream by reading the persisted row.
	for _, li := range req.LineItems {
		// Derive unit_tax from the request's per-line TaxAmount /
		// Quantity when both are provided. Tax already at the line
		// level is fine — we just need to round-trip cleanly.
		unitTax := decimal.Zero
		if !li.Quantity.IsZero() {
			unitTax = li.TaxAmount.Div(li.Quantity)
		}
		const q = `
			INSERT INTO t.transaction_line_items (
				tenant_id, transaction_id, line_number, item_id, description,
				quantity, unit_of_measure, unit_price, unit_tax, attributes
			) VALUES (
				$1, $2, $3, $4, $5,
				$6::numeric, 'EA', $7::numeric, $8::numeric,
				COALESCE($9::jsonb, '{}'::jsonb)
			)`
		if _, err := tx.Exec(ctx, q,
			tenantID, txID, li.LineNumber, li.ItemID, li.Description,
			li.Quantity, li.UnitPrice, unitTax, jsonbBytes(li.Attributes),
		); err != nil {
			return fmt.Errorf("transaction: insert line item: %w", err)
		}
	}

	// Tenders: tender_type_id is NOT NULL; if not supplied, the
	// caller is responsible for resolving via the source's default
	// tender type (per Wave 1 f.tender_types seed). The store
	// returns ErrValidation when missing.
	for i, te := range req.Tenders {
		if te.TenderTypeID == nil {
			return fmt.Errorf("%w: tender %d missing tender_type_id", ErrValidation, i)
		}
		curr := te.Currency
		if curr == "" {
			curr = req.Currency
		}
		const q = `
			INSERT INTO t.transaction_tenders (
				tenant_id, transaction_id, tender_sequence, tender_type_id,
				amount, currency, processor_reference, attributes
			) VALUES (
				$1, $2, $3, $4, $5::numeric, $6, $7,
				COALESCE($8::jsonb, '{}'::jsonb)
			)`
		if _, err := tx.Exec(ctx, q,
			tenantID, txID, i+1, *te.TenderTypeID,
			te.Amount, curr, te.Reference, jsonbBytes(te.Attributes),
		); err != nil {
			return fmt.Errorf("transaction: insert tender: %w", err)
		}
	}

	// Discounts: scope = 'transaction' default; discount_type maps
	// to the wire DiscountCode for now. Promotion-driven discounts
	// (with source_promotion_id / promotion_rule_id) land when the
	// pricing module owns the resolution path (Wave C).
	for i, d := range req.Discounts {
		const q = `
			INSERT INTO t.transaction_discounts (
				tenant_id, transaction_id, discount_sequence, scope,
				discount_type, amount, reason_code, attributes
			) VALUES (
				$1, $2, $3, 'transaction', $4, $5::numeric, $6,
				COALESCE($7::jsonb, '{}'::jsonb)
			)`
		if _, err := tx.Exec(ctx, q,
			tenantID, txID, i+1, d.DiscountCode, d.Amount, d.Reason, jsonbBytes(d.Attributes),
		); err != nil {
			return fmt.Errorf("transaction: insert discount: %w", err)
		}
	}
	return nil
}

// GetByID returns a transaction with all child rows hydrated.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*TransactionDTO, error) {
	const q = `SELECT ` + selectColumns + ` FROM t.transactions WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	out, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("transaction: get by id: %w", err)
	}
	if err := s.hydrateChildren(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetByReceiptNumber looks up by (tenant, location, business_date,
// transaction_number). Used for POS scan + return-from-receipt.
func (s *Store) GetByReceiptNumber(ctx context.Context, tenantID, locationID uuid.UUID, businessDate, txNumber string) (*TransactionDTO, error) {
	const q = `SELECT ` + selectColumns + ` FROM t.transactions
	            WHERE tenant_id = $1 AND location_id = $2
	              AND business_date = $3::date AND transaction_number = $4`
	row := s.pool.QueryRow(ctx, q, tenantID, locationID, businessDate, txNumber)
	out, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("transaction: get by receipt: %w", err)
	}
	return out, nil
}

// List returns header rows matching the filters. Children are NOT
// hydrated to keep payloads bounded.
func (s *Store) List(ctx context.Context, f ListFilters) ([]TransactionDTO, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `SELECT ` + selectColumns + ` FROM t.transactions WHERE tenant_id = $1`
	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		q += fmt.Sprintf(" AND location_id = $%d", len(args))
	}
	if f.BusinessDateMin != nil {
		args = append(args, *f.BusinessDateMin)
		q += fmt.Sprintf(" AND business_date >= $%d::date", len(args))
	}
	if f.BusinessDateMax != nil {
		args = append(args, *f.BusinessDateMax)
		q += fmt.Sprintf(" AND business_date <= $%d::date", len(args))
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if f.CashierID != nil {
		args = append(args, *f.CashierID)
		q += fmt.Sprintf(" AND cashier_employee_id = $%d", len(args))
	}
	if f.CustomerID != nil {
		args = append(args, *f.CustomerID)
		q += fmt.Sprintf(" AND customer_id = $%d", len(args))
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("transaction: list: %w", err)
	}
	defer rows.Close()
	out := make([]TransactionDTO, 0, f.Limit)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("transaction: list scan: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Void flips parent.status='voided' and inserts a child transaction
// with type='void' that mirrors the parent's totals (negated).
func (s *Store) Void(ctx context.Context, tenantID, parentID uuid.UUID, req VoidRequest, now time.Time) (*TransactionDTO, error) {
	parent, err := s.GetByID(ctx, tenantID, parentID)
	if err != nil {
		return nil, err
	}
	if parent.Status == "voided" {
		return nil, fmt.Errorf("%w: already voided", ErrConflict)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("transaction: void begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE t.transactions SET status = 'voided', void_reason = $3, updated_at = now()
		  WHERE tenant_id = $1 AND id = $2`,
		tenantID, parentID, req.Reason,
	); err != nil {
		return nil, fmt.Errorf("transaction: update parent: %w", err)
	}

	const insertQ = `
		INSERT INTO t.transactions (
			tenant_id, transaction_number, transaction_type, parent_transaction_id,
			location_id, cashier_employee_id, business_date,
			started_at, ended_at, status, currency, channel,
			subtotal, tax_total, discount_total, grand_total, void_reason
		) VALUES (
			$1, $2, 'void', $3, $4, $5, $6::date, $7, $7, 'completed', $8, $9,
			(-1) * $10::numeric, (-1) * $11::numeric, (-1) * $12::numeric, (-1) * $13::numeric, $14
		)
		RETURNING ` + selectColumns

	row := tx.QueryRow(ctx, insertQ,
		tenantID, parent.TransactionNumber+"-VOID", parent.ID,
		parent.LocationID, req.CashierEmployeeID, parent.BusinessDate,
		now, parent.Currency, parent.Channel,
		parent.Subtotal, parent.TaxTotal, parent.DiscountTotal, parent.GrandTotal,
		req.Reason,
	)
	child, err := scanTransaction(row)
	if err != nil {
		return nil, fmt.Errorf("transaction: void insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("transaction: void commit: %w", err)
	}
	return child, nil
}

// Return creates a child transaction with type='return' that
// inherits party_id from the parent (Wave A canonical-data-model
// party-edits §C — soft FK).
func (s *Store) Return(ctx context.Context, tenantID, parentID uuid.UUID, req ReturnRequest, now time.Time) (*TransactionDTO, error) {
	parent, err := s.GetByID(ctx, tenantID, parentID)
	if err != nil {
		return nil, err
	}

	subtotal := decimal.Zero
	taxTotal := decimal.Zero
	for _, li := range req.LineItems {
		subtotal = subtotal.Add(li.LineTotal)
		taxTotal = taxTotal.Add(li.TaxAmount)
	}
	grand := subtotal.Add(taxTotal)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("transaction: return begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertQ = `
		INSERT INTO t.transactions (
			tenant_id, transaction_number, transaction_type, parent_transaction_id,
			location_id, cashier_employee_id, customer_id, business_date,
			started_at, ended_at, status, currency, channel,
			item_count, subtotal, tax_total, discount_total, grand_total, party_id
		) VALUES (
			$1, $2, 'return', $3, $4, $5, $6, $7::date, $8, $8, 'completed', $9, $10,
			$11, $12::numeric, $13::numeric, 0, $14::numeric, $15
		)
		RETURNING ` + selectColumns

	row := tx.QueryRow(ctx, insertQ,
		tenantID, parent.TransactionNumber+"-RTN", parent.ID,
		parent.LocationID, req.CashierEmployeeID, parent.CustomerID, parent.BusinessDate,
		now, parent.Currency, parent.Channel,
		len(req.LineItems), subtotal, taxTotal, grand,
		parent.PartyID, // Inherits party_id from parent — Wave A SDD §C
	)
	child, err := scanTransaction(row)
	if err != nil {
		return nil, fmt.Errorf("transaction: return insert: %w", err)
	}

	childReq := CreateRequest{
		TenantID:  tenantID,
		Currency:  parent.Currency,
		LineItems: req.LineItems,
		Tenders:   req.Tenders,
	}
	if err := insertChildren(ctx, tx, child.ID, tenantID, childReq); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("transaction: return commit: %w", err)
	}
	return child, nil
}

// hydrateChildren populates dto.LineItems / Tenders / Discounts. The
// schema's GENERATED line_total is read directly.
func (s *Store) hydrateChildren(ctx context.Context, dto *TransactionDTO) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, line_number, item_id, description,
		       quantity::numeric, unit_price::numeric, line_total::numeric,
		       extended_tax::numeric, created_at
		  FROM t.transaction_line_items
		 WHERE transaction_id = $1
		 ORDER BY line_number`, dto.ID)
	if err != nil {
		return fmt.Errorf("transaction: hydrate line items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var li LineItemDTO
		if err := rows.Scan(&li.ID, &li.TransactionID, &li.LineNumber, &li.ItemID,
			&li.Description, &li.Quantity, &li.UnitPrice, &li.LineTotal,
			&li.TaxAmount, &li.CreatedAt); err != nil {
			return err
		}
		dto.LineItems = append(dto.LineItems, li)
	}

	tRows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, tender_type_id,
		       amount::numeric, currency, processor_reference, created_at
		  FROM t.transaction_tenders
		 WHERE transaction_id = $1
		 ORDER BY tender_sequence`, dto.ID)
	if err != nil {
		return fmt.Errorf("transaction: hydrate tenders: %w", err)
	}
	defer tRows.Close()
	for tRows.Next() {
		var te TenderDTO
		var ttID uuid.UUID
		if err := tRows.Scan(&te.ID, &te.TransactionID, &ttID,
			&te.Amount, &te.Currency, &te.Reference, &te.CreatedAt); err != nil {
			return err
		}
		te.TenderTypeID = &ttID
		dto.Tenders = append(dto.Tenders, te)
	}

	dRows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, discount_type, amount::numeric, reason_code, created_at
		  FROM t.transaction_discounts
		 WHERE transaction_id = $1
		 ORDER BY discount_sequence`, dto.ID)
	if err != nil {
		return fmt.Errorf("transaction: hydrate discounts: %w", err)
	}
	defer dRows.Close()
	for dRows.Next() {
		var d DiscountDTO
		if err := dRows.Scan(&d.ID, &d.TransactionID, &d.DiscountCode,
			&d.Amount, &d.Reason, &d.CreatedAt); err != nil {
			return err
		}
		dto.Discounts = append(dto.Discounts, d)
	}
	return nil
}

// selectColumns is the canonical column list for t.transactions reads.
// Embedded as a constant so every read path uses the same set + order
// and scanTransaction lines up correctly.
const selectColumns = `id, tenant_id, transaction_number, transaction_type,
parent_transaction_id, location_id, pos_terminal_id,
cashier_employee_id, customer_id, loyalty_membership_id,
party_id, business_date::text, started_at, ended_at,
status, ticket_number, item_count,
subtotal::numeric, tax_total::numeric, discount_total::numeric, grand_total::numeric,
currency, channel, is_training_mode, is_offline,
void_reason, created_at, updated_at`

type scannable interface {
	Scan(dest ...any) error
}

func scanTransaction(r scannable) (*TransactionDTO, error) {
	var t TransactionDTO
	if err := r.Scan(
		&t.ID, &t.TenantID, &t.TransactionNumber, &t.TransactionType,
		&t.ParentTransactionID, &t.LocationID, &t.POSTerminalID,
		&t.CashierEmployeeID, &t.CustomerID, &t.LoyaltyMembershipID,
		&t.PartyID, &t.BusinessDate, &t.StartedAt, &t.EndedAt,
		&t.Status, &t.TicketNumber, &t.ItemCount,
		&t.Subtotal, &t.TaxTotal, &t.DiscountTotal, &t.GrandTotal,
		&t.Currency, &t.Channel, &t.IsTrainingMode, &t.IsOffline,
		&t.VoidReason, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

// jsonbBytes marshals a map[string]any to JSONB-friendly bytes;
// returns nil for nil input so the COALESCE in the SQL kicks in.
func jsonbBytes(m map[string]any) []byte {
	if m == nil {
		return nil
	}
	b, err := jsonMarshal(m)
	if err != nil {
		return nil
	}
	return b
}

// indirection keeps encoding/json import out of the hot path of every
// caller of this package — only the store layer needs it.
var jsonMarshal = func(v any) ([]byte, error) {
	return jsonMarshalImpl(v)
}

func toLineItemDTOs(txID uuid.UUID, items []LineItemRequest) []LineItemDTO {
	out := make([]LineItemDTO, 0, len(items))
	for _, li := range items {
		out = append(out, LineItemDTO{
			TransactionID: txID,
			LineNumber:    li.LineNumber,
			ItemID:        li.ItemID,
			Description:   li.Description,
			Quantity:      li.Quantity,
			UnitPrice:     li.UnitPrice,
			LineTotal:     li.LineTotal,
			TaxAmount:     li.TaxAmount,
		})
	}
	return out
}

func toTenderDTOs(txID uuid.UUID, items []TenderRequest, defaultCurrency string) []TenderDTO {
	out := make([]TenderDTO, 0, len(items))
	for _, te := range items {
		curr := te.Currency
		if curr == "" {
			curr = defaultCurrency
		}
		out = append(out, TenderDTO{
			TransactionID: txID,
			TenderTypeID:  te.TenderTypeID,
			TenderCode:    te.TenderCode,
			Amount:        te.Amount,
			Currency:      curr,
			Reference:     te.Reference,
		})
	}
	return out
}

func toDiscountDTOs(txID uuid.UUID, items []DiscountRequest) []DiscountDTO {
	out := make([]DiscountDTO, 0, len(items))
	for _, d := range items {
		out = append(out, DiscountDTO{
			TransactionID: txID,
			DiscountCode:  d.DiscountCode,
			Amount:        d.Amount,
			Reason:        d.Reason,
		})
	}
	return out
}
