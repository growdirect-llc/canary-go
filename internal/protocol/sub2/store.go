package sub2

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// ErrTenantUnknown is returned by Store.Persist when the merchant in
// the envelope does not resolve to a tenant. Callers should treat this
// as a data-integrity error (a webhook arrived for a merchant we
// don't have set up); the dispatcher dead-letters it.
var ErrTenantUnknown = errors.New("sub2: tenant lookup failed for merchant")

// ErrLocationUnknown is returned when SourceLocationCode does not
// resolve to an location.locations row for the resolved tenant.
var ErrLocationUnknown = errors.New("sub2: location lookup failed")

// Store is the persistence interface the dispatcher uses. The pgx
// implementation is PgxStore; tests substitute a stub.
//
// Persist writes the canonical event to the t.* tables in a single
// pgx transaction. On UNIQUE-violation against
// (tenant_id, location_id, business_date, transaction_number) the
// store treats the event as already persisted and returns nil — Sub 2
// must be idempotent for the same reasons Sub 1 is (gateway retries,
// stream redeliveries, double-publishes from poll runs).
type Store interface {
	Persist(ctx context.Context, evt *CanonicalEvent) error
}

// PgxStore implements Store against a pgxpool.Pool. The pool is
// owned by the caller; Close is not part of this interface.
//
// logger is the structured logger used for soft-failure paths
// (currently: dropped tender rows when no default tender_type is
// seeded). Defaults to zap.NewNop when not set so callers using the
// existing NewPgxStore constructor don't break and tests don't have
// to thread a logger when they don't care about the warn path.
type PgxStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewPgxStore wires a Store against the given pool. The logger
// defaults to zap.NewNop; use WithLogger to attach one for
// observability of the soft-failure paths.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool, logger: zap.NewNop()}
}

// WithLogger returns the receiver with the given logger attached.
// Safe to call with a nil logger (falls back to NewNop). Returning the
// receiver lets callers keep the existing single-line construction,
// e.g. store := NewPgxStore(pool).WithLogger(log).
func (s *PgxStore) WithLogger(log *zap.Logger) *PgxStore {
	if log == nil {
		log = zap.NewNop()
	}
	s.logger = log
	return s
}

// pgUniqueViolation is the SQLSTATE returned by Postgres for unique
// constraint violations. Same constant as Sub 1; duplicated here to
// keep the package self-contained.
const pgUniqueViolation = "23505"

// Persist resolves FKs (merchant→tenant, location_code→location_id,
// employee_code→employee_id) then inserts the transaction.transactions header
// and every child row in a single transaction. Any sub-row failure
// rolls back the parent.
//
// The canonical event arrives with FKs unset — adapters don't have
// access to the tenant database. The store fills:
//
// - Transaction.TenantID from app.merchants.tenant_id
// - Transaction.LocationID from location.locations(tenant_id, location_code)
// - Transaction.CashierEmployeeID from employee.employees(tenant_id, employee_code)
//
// Then mints the parent ID, propagates it onto each child, and inserts.
func (s *PgxStore) Persist(ctx context.Context, evt *CanonicalEvent) error {
	tenantID, err := s.lookupTenant(ctx, evt.MerchantID)
	if err != nil {
		return err
	}

	locationID, err := s.lookupLocation(ctx, tenantID, evt.SourceLocationCode)
	if err != nil {
		return err
	}

	var cashierID *uuid.UUID
	if evt.SourceCashierCode != "" {
		id, lookupErr := s.lookupEmployee(ctx, tenantID, evt.SourceCashierCode)
		if lookupErr != nil {
			// DB error (not a missing row) — fail loud. The transaction
			// can be redelivered; nulling the cashier from a real sale
			// would be silent data loss. Per-row "not found" is handled
			// inside lookupEmployee as (uuid.Nil, nil) and falls into
			// the id == uuid.Nil branch below, leaving cashierID nil
			// per the canonical schema's NULL allowance.
			return fmt.Errorf("sub2: cashier lookup failed: %w", lookupErr)
		}
		if id != uuid.Nil {
			cashierID = &id
		}
		// id == uuid.Nil here means "row genuinely missing" — leave
		// cashierID nil per the canonical schema's NULL allowance.
	}

	// Stamp resolved FKs onto the canonical record.
	evt.Transaction.TenantID = tenantID
	evt.Transaction.LocationID = locationID
	evt.Transaction.CashierEmployeeID = cashierID

	// Mint parent ID up-front so children can reference it.
	if evt.Transaction.ID == uuid.Nil {
		evt.Transaction.ID = uuid.New()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sub2: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertTransaction(ctx, tx, &evt.Transaction); err != nil {
		// Idempotency: duplicate (tenant, location, business_date,
		// transaction_number) means we already wrote this — treat as
		// success. The downstream Sub 1 evidence row is what
		// guarantees we don't double-record; Sub 2's job is to make
		// the tenant DB eventually consistent.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return nil
		}
		return fmt.Errorf("sub2: insert transaction: %w", err)
	}

	for i := range evt.LineItems {
		evt.LineItems[i].TenantID = tenantID
		evt.LineItems[i].TransactionID = evt.Transaction.ID
		if evt.LineItems[i].ID == uuid.Nil {
			evt.LineItems[i].ID = uuid.New()
		}
		if err := insertLineItem(ctx, tx, &evt.LineItems[i]); err != nil {
			return fmt.Errorf("sub2: insert line item %d: %w", i, err)
		}
	}

	// Resolve the per-source default tender_type_id once for the
	// envelope.: adapters set
	// TenderTypeID = uuid.Nil because their wire envelopes don't
	// carry a stable tender-type identifier; the (tenant, source)
	// default seeded in finance.tender_types is the FK we resolve here.
	// Lookup is a read against a tiny reference table — outside the
	// transaction is fine.
	defaultTenderTypeID, tenderResolveErr := s.resolveTenderTypeID(ctx, tenantID, evt.SourceCode)

	for i := range evt.Tenders {
		evt.Tenders[i].TenantID = tenantID
		evt.Tenders[i].TransactionID = evt.Transaction.ID
		if evt.Tenders[i].ID == uuid.Nil {
			evt.Tenders[i].ID = uuid.New()
		}
		// Stamp the resolved default when adapter left it Nil; preserve
		// any tender_type_id the adapter explicitly set (future-proof
		// for adapters that get smarter). When no default exists, skip
		// the tender row rather than fail the whole transaction —
		// canonical event header + line items are the load-bearing
		// signal; tenders are detail metadata.
		if evt.Tenders[i].TenderTypeID == uuid.Nil {
			if tenderResolveErr != nil {
				// Soft data-loss path: the tender row gets dropped (the
				// canonical header + line items are load-bearing; tenders
				// are detail metadata). Warn with enough fields that an
				// auditor can grep prod logs to reconstruct exactly what
				// was dropped — tx id, source, the tender's index +
				// internal ID, the amount, and the underlying lookup
				// error.
				s.logger.Warn("sub2: dropping tender — no default tender_type for (tenant, source)",
					zap.String("tenant_id", tenantID.String()),
					zap.String("source_code", evt.SourceCode),
					zap.String("transaction_id", evt.Transaction.ID.String()),
					zap.String("source_txn_id", evt.SourceTxnID),
					zap.Int("tender_index", i),
					zap.String("tender_id", evt.Tenders[i].ID.String()),
					zap.String("amount", evt.Tenders[i].Amount),
					zap.String("currency", evt.Tenders[i].Currency),
					zap.Error(tenderResolveErr),
				)
				continue
			}
			evt.Tenders[i].TenderTypeID = defaultTenderTypeID
		}
		if err := insertTender(ctx, tx, &evt.Tenders[i]); err != nil {
			return fmt.Errorf("sub2: insert tender %d: %w", i, err)
		}
	}

	for i := range evt.Discounts {
		evt.Discounts[i].TenantID = tenantID
		evt.Discounts[i].TransactionID = evt.Transaction.ID
		if evt.Discounts[i].ID == uuid.Nil {
			evt.Discounts[i].ID = uuid.New()
		}
		if err := insertDiscount(ctx, tx, &evt.Discounts[i]); err != nil {
			return fmt.Errorf("sub2: insert discount %d: %w", i, err)
		}
	}

	if evt.CashDrawer != nil {
		evt.CashDrawer.TenantID = tenantID
		evt.CashDrawer.LocationID = locationID
		if evt.CashDrawer.ID == uuid.Nil {
			evt.CashDrawer.ID = uuid.New()
		}
		if err := insertCashDrawerEvent(ctx, tx, evt.CashDrawer); err != nil {
			return fmt.Errorf("sub2: insert cash drawer event: %w", err)
		}
	}

	for i := range evt.CashierActions {
		evt.CashierActions[i].TenantID = tenantID
		evt.CashierActions[i].LocationID = locationID
		txID := evt.Transaction.ID
		evt.CashierActions[i].TransactionID = &txID
		if evt.CashierActions[i].ID == uuid.Nil {
			evt.CashierActions[i].ID = uuid.New()
		}
		if err := insertCashierAction(ctx, tx, &evt.CashierActions[i]); err != nil {
			return fmt.Errorf("sub2: insert cashier action %d: %w", i, err)
		}
	}

	for i := range evt.LoyaltyEvents {
		evt.LoyaltyEvents[i].TenantID = tenantID
		txID := evt.Transaction.ID
		evt.LoyaltyEvents[i].TransactionID = &txID
		if evt.LoyaltyEvents[i].ID == uuid.Nil {
			evt.LoyaltyEvents[i].ID = uuid.New()
		}
		if err := insertLoyaltyEvent(ctx, tx, &evt.LoyaltyEvents[i]); err != nil {
			return fmt.Errorf("sub2: insert loyalty event %d: %w", i, err)
		}
	}

	for i := range evt.GiftCardEvents {
		evt.GiftCardEvents[i].TenantID = tenantID
		txID := evt.Transaction.ID
		evt.GiftCardEvents[i].TransactionID = &txID
		if evt.GiftCardEvents[i].ID == uuid.Nil {
			evt.GiftCardEvents[i].ID = uuid.New()
		}
		if err := insertGiftCardEvent(ctx, tx, &evt.GiftCardEvents[i]); err != nil {
			return fmt.Errorf("sub2: insert gift card event %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sub2: commit: %w", err)
	}
	return nil
}

// lookupTenant resolves the gateway envelope's MerchantID to the
// canonical TenantID via app.merchants.tenant_id. The 99_seed.sql
// comment guarantees a 1:1 mapping for MVP.
func (s *PgxStore) lookupTenant(ctx context.Context, merchantID uuid.UUID) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT tenant_id FROM app.merchants WHERE id = $1`,
		merchantID,
	).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: merchant=%s", ErrTenantUnknown, merchantID)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sub2: lookup tenant: %w", err)
	}
	if tenantID == uuid.Nil {
		// app.merchants.tenant_id is nullable per current schema; treat
		// missing as unknown.
		return uuid.Nil, fmt.Errorf("%w: merchant=%s tenant_id is null", ErrTenantUnknown, merchantID)
	}
	return tenantID, nil
}

// lookupLocation resolves a POS-native location code to an
// location.locations.id within the tenant.
func (s *PgxStore) lookupLocation(ctx context.Context, tenantID uuid.UUID, locationCode string) (uuid.UUID, error) {
	if locationCode == "" {
		return uuid.Nil, fmt.Errorf("%w: empty location_code", ErrLocationUnknown)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM location.locations WHERE tenant_id = $1 AND location_code = $2`,
		tenantID, locationCode,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("%w: tenant=%s code=%s", ErrLocationUnknown, tenantID, locationCode)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sub2: lookup location: %w", err)
	}
	return id, nil
}

// lookupEmployee resolves a POS-native employee code to
// employee.employees.id. Returns (uuid.Nil, nil) when the row genuinely
// doesn't exist — caller decides whether NULL cashier is acceptable.
// Other DB errors (transient connection failures, schema mismatches)
// propagate so the caller can fail loud rather than silently null the
// field.
func (s *PgxStore) lookupEmployee(ctx context.Context, tenantID uuid.UUID, employeeCode string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM employee.employees WHERE tenant_id = $1 AND employee_code = $2`,
		tenantID, employeeCode,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sub2: lookup employee: %w", err)
	}
	return id, nil
}

// resolveTenderTypeID looks up the (tenant, source_code) default
// tender_type_id from the partial unique index uq_tender_source_default
// (deploy/schema/07_p_f_pricing_finance.sql). Mirrors
// adapters.ResolveTenderType but kept inline here to avoid an import
// cycle (internal/adapters already imports sub2). will
// add an LRU cache; Wave 1 keeps it simple.
func (s *PgxStore) resolveTenderTypeID(ctx context.Context, tenantID uuid.UUID, sourceCode string) (uuid.UUID, error) {
	if sourceCode == "" {
		return uuid.Nil, fmt.Errorf("sub2: resolveTenderTypeID: empty source_code")
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM finance.tender_types WHERE tenant_id = $1 AND source_code = $2 LIMIT 1`,
		tenantID, sourceCode,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("sub2: no default tender_type seeded for tenant=%s source=%s", tenantID, sourceCode)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("sub2: resolveTenderTypeID: %w", err)
	}
	return id, nil
}
