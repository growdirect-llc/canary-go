package chirp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the data-access surface chirp uses. Interface (not concrete
// pgx) so tests can stub.
//
// Loop 2 dispatch override: direct pgx + raw SQL. CanaryGo CLAUDE.md
// requires sqlc; the Loop 2 dispatch explicitly waives that for this
// wave. sqlc retrofit is Loop 3.
type Store interface {
	LoadRules(ctx context.Context, tenantID uuid.UUID, frequency string) ([]Rule, error)
	LoadTransaction(ctx context.Context, transactionID uuid.UUID) (*Transaction, error)
	LoadEvalContext(ctx context.Context, tx *Transaction) (*EvalContext, error)
	LoadCashierWindow(ctx context.Context, employeeID uuid.UUID, windowStart, windowEnd time.Time) ([]CashierAction, error)
	InsertDetection(ctx context.Context, d *Detection) error
	ListTransactionsSince(ctx context.Context, tenantID uuid.UUID, since time.Time) ([]uuid.UUID, error)
	ListRules(ctx context.Context, tenantID uuid.UUID) ([]Rule, error)
	ListDetections(ctx context.Context, q DetectionQuery) ([]Detection, error)
}

// DetectionQuery is the filter set for the GET /v1/chirp/detections endpoint.
type DetectionQuery struct {
	TenantID uuid.UUID
	From     *time.Time
	To       *time.Time
	Limit    int // pagination cap; defaults to 50 if zero
	Offset   int
}

// PgxStore is the production Store backed by a pgxpool.
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore wraps a pool.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// Verify PgxStore implements Store.
var _ Store = (*PgxStore)(nil)

// Default frequency-rule window for cashier/drawer event lookback when
// the rule doesn't specify. SDD-vague: chirp.md doesn't bind a window
// to "recent activity"; 60min is a defensible baseline.
const defaultCashierWindow = 60 * time.Minute

const sqlLoadRules = `
SELECT id, tenant_id, rule_code, name, description, rule_category,
       rule_definition, severity, status, evaluation_frequency,
       attributes, created_at, updated_at
FROM   detection.detection_rules
WHERE  tenant_id = $1
  AND  status = 'active'
  AND  ($2 = '' OR evaluation_frequency = $2)
ORDER BY rule_code
`

// LoadRules returns active rules for tenant. If frequency is empty,
// returns rules for every evaluation_frequency.
func (s *PgxStore) LoadRules(ctx context.Context, tenantID uuid.UUID, frequency string) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, sqlLoadRules, tenantID, frequency)
	if err != nil {
		return nil, fmt.Errorf("query detection_rules: %w", err)
	}
	defer rows.Close()

	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.RuleCode, &r.Name, &r.Description, &r.RuleCategory,
			&r.RuleDefinition, &r.Severity, &r.Status, &r.EvaluationFrequency,
			&r.Attributes, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan detection_rule: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRules returns ALL rules (any status, any frequency) for a tenant —
// the GET /v1/chirp/rules surface, useful for admins debugging why a
// rule isn't firing.
func (s *PgxStore) ListRules(ctx context.Context, tenantID uuid.UUID) ([]Rule, error) {
	const q = `
SELECT id, tenant_id, rule_code, name, description, rule_category,
       rule_definition, severity, status, evaluation_frequency,
       attributes, created_at, updated_at
FROM   detection.detection_rules
WHERE  tenant_id = $1
ORDER BY rule_code
`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query detection_rules (all): %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.RuleCode, &r.Name, &r.Description, &r.RuleCategory,
			&r.RuleDefinition, &r.Severity, &r.Status, &r.EvaluationFrequency,
			&r.Attributes, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan detection_rule (all): %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const sqlLoadTransaction = `
SELECT id, tenant_id, transaction_number, transaction_type,
       parent_transaction_id, location_id, pos_terminal_id,
       cashier_employee_id, customer_id, loyalty_membership_id,
       business_date, started_at, ended_at, status, ticket_number,
       item_count, subtotal, tax_total, discount_total, grand_total,
       currency, channel, pos_software_version, is_training_mode,
       is_offline, is_reentered, is_suspended, void_reason,
       attributes, external_ids, created_at, updated_at
FROM   transaction.transactions
WHERE  id = $1
`

// LoadTransaction returns the transaction or (nil, nil) if not found.
func (s *PgxStore) LoadTransaction(ctx context.Context, transactionID uuid.UUID) (*Transaction, error) {
	var tx Transaction
	err := s.pool.QueryRow(ctx, sqlLoadTransaction, transactionID).Scan(
		&tx.ID, &tx.TenantID, &tx.TransactionNumber, &tx.TransactionType,
		&tx.ParentTransactionID, &tx.LocationID, &tx.POSTerminalID,
		&tx.CashierEmployeeID, &tx.CustomerID, &tx.LoyaltyMembershipID,
		&tx.BusinessDate, &tx.StartedAt, &tx.EndedAt, &tx.Status, &tx.TicketNumber,
		&tx.ItemCount, &tx.Subtotal, &tx.TaxTotal, &tx.DiscountTotal, &tx.GrandTotal,
		&tx.Currency, &tx.Channel, &tx.POSSoftwareVersion, &tx.IsTrainingMode,
		&tx.IsOffline, &tx.IsReentered, &tx.IsSuspended, &tx.VoidReason,
		&tx.Attributes, &tx.ExternalIDs, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query transaction: %w", err)
	}
	return &tx, nil
}

const sqlLoadLineItems = `
SELECT id, tenant_id, transaction_id, line_number, item_id,
       barcode_scanned, description, quantity, unit_of_measure,
       unit_price, list_price, unit_discount, unit_tax,
       extended_price, extended_tax, line_total, cost_basis, margin,
       category_id, zone_id, lot_id, inventory_movement_id,
       is_void, void_reason, is_return, return_reason,
       is_weighable, is_food_stamp_eligible, attributes, created_at
FROM   transaction.transaction_line_items
WHERE  transaction_id = $1
ORDER BY line_number
`

func (s *PgxStore) loadLineItems(ctx context.Context, txID uuid.UUID) ([]LineItem, error) {
	rows, err := s.pool.Query(ctx, sqlLoadLineItems, txID)
	if err != nil {
		return nil, fmt.Errorf("query line_items: %w", err)
	}
	defer rows.Close()
	var out []LineItem
	for rows.Next() {
		var li LineItem
		if err := rows.Scan(
			&li.ID, &li.TenantID, &li.TransactionID, &li.LineNumber, &li.ItemID,
			&li.BarcodeScanned, &li.Description, &li.Quantity, &li.UnitOfMeasure,
			&li.UnitPrice, &li.ListPrice, &li.UnitDiscount, &li.UnitTax,
			&li.ExtendedPrice, &li.ExtendedTax, &li.LineTotal, &li.CostBasis, &li.Margin,
			&li.CategoryID, &li.ZoneID, &li.LotID, &li.InventoryMovementID,
			&li.IsVoid, &li.VoidReason, &li.IsReturn, &li.ReturnReason,
			&li.IsWeighable, &li.IsFoodStampEligible, &li.Attributes, &li.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan line_item: %w", err)
		}
		out = append(out, li)
	}
	return out, rows.Err()
}

const sqlLoadDiscounts = `
SELECT id, tenant_id, transaction_id, discount_sequence, scope,
       line_item_id, discount_type, source_promotion_id, promotion_rule_id,
       amount, percentage, reason_code, authorized_by_employee_id,
       attributes, created_at
FROM   transaction.transaction_discounts
WHERE  transaction_id = $1
ORDER BY discount_sequence
`

func (s *PgxStore) loadDiscounts(ctx context.Context, txID uuid.UUID) ([]Discount, error) {
	rows, err := s.pool.Query(ctx, sqlLoadDiscounts, txID)
	if err != nil {
		return nil, fmt.Errorf("query discounts: %w", err)
	}
	defer rows.Close()
	var out []Discount
	for rows.Next() {
		var d Discount
		if err := rows.Scan(
			&d.ID, &d.TenantID, &d.TransactionID, &d.DiscountSequence, &d.Scope,
			&d.LineItemID, &d.DiscountType, &d.SourcePromotionID, &d.PromotionRuleID,
			&d.Amount, &d.Percentage, &d.ReasonCode, &d.AuthorizedByEmployeeID,
			&d.Attributes, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan discount: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

const sqlLoadCashierActions = `
SELECT id, tenant_id, transaction_id, location_id, cashier_employee_id,
       pos_terminal_id, action_type, performed_at,
       authorized_by_employee_id, details, attributes, created_at
FROM   transaction.cashier_actions
WHERE  cashier_employee_id = $1
  AND  performed_at >= $2
  AND  performed_at <= $3
ORDER BY performed_at
`

// LoadCashierWindow returns cashier_actions in [windowStart, windowEnd]
// for an employee. Used by frequency rules (no_sale_frequency,
// manager_override_frequency).
func (s *PgxStore) LoadCashierWindow(
	ctx context.Context,
	employeeID uuid.UUID,
	windowStart, windowEnd time.Time,
) ([]CashierAction, error) {
	rows, err := s.pool.Query(ctx, sqlLoadCashierActions, employeeID, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("query cashier_actions: %w", err)
	}
	defer rows.Close()
	var out []CashierAction
	for rows.Next() {
		var a CashierAction
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.TransactionID, &a.LocationID, &a.CashierEmployeeID,
			&a.POSTerminalID, &a.ActionType, &a.PerformedAt,
			&a.AuthorizedByEmployeeID, &a.Details, &a.Attributes, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cashier_action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const sqlLoadDrawerEvents = `
SELECT id, tenant_id, location_id, pos_terminal_id, cashier_employee_id,
       event_type, event_at, expected_amount, counted_amount, variance,
       reason, paid_in_out_amount, reference, attributes, created_at
FROM   transaction.cash_drawer_events
WHERE  location_id = $1
  AND  ($2::text IS NULL OR pos_terminal_id = $2)
  AND  event_at >= $3
  AND  event_at <= $4
ORDER BY event_at
`

func (s *PgxStore) loadDrawerEvents(
	ctx context.Context,
	locationID uuid.UUID,
	posTerminalID *string,
	windowStart, windowEnd time.Time,
) ([]CashDrawerEvent, error) {
	var terminalArg interface{}
	if posTerminalID != nil {
		terminalArg = *posTerminalID
	}
	rows, err := s.pool.Query(ctx, sqlLoadDrawerEvents, locationID, terminalArg, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("query cash_drawer_events: %w", err)
	}
	defer rows.Close()
	var out []CashDrawerEvent
	for rows.Next() {
		var e CashDrawerEvent
		if err := rows.Scan(
			&e.ID, &e.TenantID, &e.LocationID, &e.POSTerminalID, &e.CashierEmployeeID,
			&e.EventType, &e.EventAt, &e.ExpectedAmount, &e.CountedAmount, &e.Variance,
			&e.Reason, &e.PaidInOutAmount, &e.Reference, &e.Attributes, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cash_drawer_event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const sqlLoadLocationConfig = `SELECT operating_hours, timezone FROM location.locations WHERE id = $1`

// loadLocationConfig fetches the operating_hours JSONB blob and the
// IANA timezone identifier (location.locations.timezone, RFC 6557) for a
// location in one round-trip. Both are evaluator inputs; loading
// together saves a query per transaction.
func (s *PgxStore) loadLocationConfig(ctx context.Context, locationID uuid.UUID) (json.RawMessage, string, error) {
	var hours json.RawMessage
	var tz string
	err := s.pool.QueryRow(ctx, sqlLoadLocationConfig, locationID).Scan(&hours, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("query location config: %w", err)
	}
	return hours, tz, nil
}

// LoadEvalContext gathers everything an evaluator might need for a
// single transaction. Eager loading keeps each rule pure.
func (s *PgxStore) LoadEvalContext(ctx context.Context, tx *Transaction) (*EvalContext, error) {
	ec := &EvalContext{
		TenantID:    tx.TenantID,
		Transaction: tx,
	}

	lis, err := s.loadLineItems(ctx, tx.ID)
	if err != nil {
		return nil, err
	}
	ec.LineItems = lis

	disc, err := s.loadDiscounts(ctx, tx.ID)
	if err != nil {
		return nil, err
	}
	ec.Discounts = disc

	if tx.CashierEmployeeID != nil {
		windowEnd := tx.EndedAt
		windowStart := windowEnd.Add(-defaultCashierWindow)
		actions, err := s.LoadCashierWindow(ctx, *tx.CashierEmployeeID, windowStart, windowEnd)
		if err != nil {
			return nil, err
		}
		ec.CashierActions = actions
	}

	{
		windowEnd := tx.EndedAt
		windowStart := windowEnd.Add(-defaultCashierWindow)
		drawer, err := s.loadDrawerEvents(ctx, tx.LocationID, tx.POSTerminalID, windowStart, windowEnd)
		if err != nil {
			return nil, err
		}
		ec.DrawerEvents = drawer
	}

	hours, tz, err := s.loadLocationConfig(ctx, tx.LocationID)
	if err != nil {
		return nil, err
	}
	ec.LocationOperatingHours = hours
	ec.LocationTimezone = tz

	return ec, nil
}

const sqlInsertDetection = `
INSERT INTO detection.detections (
    tenant_id, rule_id, detected_at, source_entity_type, source_entity_id,
    location_id, cashier_employee_id, customer_id, severity, signal_strength,
    evidence, status, attributes
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13
)
RETURNING id, created_at
`

// InsertDetection writes a Detection and fills in the DB-assigned ID +
// created_at on success.
func (s *PgxStore) InsertDetection(ctx context.Context, d *Detection) error {
	if len(d.Evidence) == 0 {
		d.Evidence = json.RawMessage(`{}`)
	}
	if len(d.Attributes) == 0 {
		d.Attributes = json.RawMessage(`{}`)
	}
	err := s.pool.QueryRow(ctx, sqlInsertDetection,
		d.TenantID, d.RuleID, d.DetectedAt, d.SourceEntityType, d.SourceEntityID,
		d.LocationID, d.CashierEmployeeID, d.CustomerID, d.Severity, d.SignalStrength,
		d.Evidence, d.Status, d.Attributes,
	).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert detection: %w", err)
	}
	return nil
}

const sqlListTransactionsSince = `
SELECT id
FROM   transaction.transactions
WHERE  tenant_id = $1
  AND  created_at >= $2
ORDER BY created_at
`

func (s *PgxStore) ListTransactionsSince(ctx context.Context, tenantID uuid.UUID, since time.Time) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, sqlListTransactionsSince, tenantID, since)
	if err != nil {
		return nil, fmt.Errorf("query transactions since: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan transaction id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const sqlListDetections = `
SELECT id, tenant_id, rule_id, detected_at, source_entity_type, source_entity_id,
       location_id, cashier_employee_id, customer_id, severity, signal_strength,
       evidence, case_id, status, acknowledged_at, acknowledged_by,
       attributes, created_at
FROM   detection.detections
WHERE  tenant_id = $1
  AND  ($2::timestamptz IS NULL OR detected_at >= $2)
  AND  ($3::timestamptz IS NULL OR detected_at <= $3)
ORDER BY detected_at DESC
LIMIT $4 OFFSET $5
`

func (s *PgxStore) ListDetections(ctx context.Context, q DetectionQuery) ([]Detection, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, sqlListDetections, q.TenantID, q.From, q.To, limit, q.Offset)
	if err != nil {
		return nil, fmt.Errorf("query detections: %w", err)
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		var d Detection
		if err := rows.Scan(
			&d.ID, &d.TenantID, &d.RuleID, &d.DetectedAt, &d.SourceEntityType, &d.SourceEntityID,
			&d.LocationID, &d.CashierEmployeeID, &d.CustomerID, &d.Severity, &d.SignalStrength,
			&d.Evidence, &d.CaseID, &d.Status, &d.AcknowledgedAt, &d.AcknowledgedBy,
			&d.Attributes, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan detection: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
