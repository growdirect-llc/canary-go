package pricing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// LOOP2-decision: dispatch overrides the CanaryGo CLAUDE.md "all queries
// through sqlc" rule for Loop 2. Direct pgx + raw SQL until Loop 3 sqlc
// retrofit. SQL strings are kept short and review-friendly.

// ErrNotFound — store-level miss for a single-row lookup.
var ErrNotFound = errors.New("pricing: not found")

// Store is the contract the resolver depends on. The pgx implementation
// satisfies it; tests use a stub.
type Store interface {
	GetItemPrice(ctx context.Context, tenantID, itemID uuid.UUID, locationID *uuid.UUID, channel string, asOf time.Time) (*types.ItemPrice, error)
	GetItem(ctx context.Context, tenantID, itemID uuid.UUID) (*types.Item, error)
	ListActivePromotions(ctx context.Context, tenantID uuid.UUID, locationID uuid.UUID, asOf time.Time) ([]types.Promotion, error)
	ListPromotionRules(ctx context.Context, tenantID, promotionID uuid.UUID) ([]types.PromotionRule, error)
	GetTaxClassByCode(ctx context.Context, tenantID uuid.UUID, code string) (*types.TaxClass, error)
	GetTaxRate(ctx context.Context, tenantID uuid.UUID, taxClassID uuid.UUID, locationID *uuid.UUID, asOf time.Time) (*types.TaxRate, error)
	ListTaxRates(ctx context.Context, tenantID uuid.UUID, locationID *uuid.UUID) ([]TaxRateRow, error)
}

// TaxRateRow joins p.tax_rates with p.tax_classes for the listing endpoint.
type TaxRateRow struct {
	types.TaxRate
	TaxClassCode string
}

// PgxStore is the live database-backed Store.
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore constructs a Store backed by the given pgx pool.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore { return &PgxStore{pool: pool} }

// GetItemPrice resolves the active base price for (tenant, item, location, channel)
// at the given timestamp. Falls back to NULL-location row if no
// location-specific price exists. Picks the most-specific matching row by
// effective_start DESC.
//
// SDD-vague: the canonical model talks about price_type ranking
// (clearance < member < regular?) without specifying. LOOP2-decision:
// pick price_type='regular' first, fall back to any price_type if
// regular isn't present. Member/clearance pricing is Wave 3+.
func (s *PgxStore) GetItemPrice(ctx context.Context, tenantID, itemID uuid.UUID, locationID *uuid.UUID, channel string, asOf time.Time) (*types.ItemPrice, error) {
	if channel == "" {
		channel = "all"
	}

	// Two-pass: location-specific first, then NULL-location fallback.
	// Each pass prefers price_type='regular' but accepts anything else if
	// regular is absent — ORDER BY (price_type='regular') DESC sorts
	// regular to the top.
	const q = `
		SELECT id, tenant_id, item_id, location_id, zone_id, channel,
		       price_type, amount::text, currency, uom,
		       effective_start, effective_end, source_promotion_id,
		       attributes, status, created_at, updated_at
		  FROM p.item_prices
		 WHERE tenant_id = $1
		   AND item_id   = $2
		   AND status    = 'active'
		   AND (channel  = $4 OR channel = 'all')
		   AND effective_start <= $5
		   AND (effective_end IS NULL OR effective_end > $5)
		   AND (
		         ($3::uuid IS NOT NULL AND location_id = $3)
		      OR ($3::uuid IS     NULL AND location_id IS NULL)
		       )
		 ORDER BY (price_type = 'regular') DESC,
		          (location_id IS NOT NULL) DESC,
		          effective_start DESC
		 LIMIT 1`

	row, err := s.fetchPrice(ctx, q, tenantID, itemID, locationID, channel, asOf)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Fallback: NULL-location row (tenant-default).
	return s.fetchPrice(ctx, q, tenantID, itemID, nil, channel, asOf)
}

func (s *PgxStore) fetchPrice(ctx context.Context, q string, tenantID, itemID uuid.UUID, locationID *uuid.UUID, channel string, asOf time.Time) (*types.ItemPrice, error) {
	var p types.ItemPrice
	err := s.pool.QueryRow(ctx, q, tenantID, itemID, locationID, channel, asOf).Scan(
		&p.ID, &p.TenantID, &p.ItemID, &p.LocationID, &p.ZoneID, &p.Channel,
		&p.PriceType, &p.Amount, &p.Currency, &p.UOM,
		&p.EffectiveStart, &p.EffectiveEnd, &p.SourcePromotionID,
		&p.Attributes, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricing: get_item_price: %w", err)
	}
	return &p, nil
}

// GetItem fetches the item row — needed for tax_class lookup.
func (s *PgxStore) GetItem(ctx context.Context, tenantID, itemID uuid.UUID) (*types.Item, error) {
	const q = `
		SELECT id, tenant_id, sku, description, short_description, item_type,
		       category_id, unit_of_measure, uom_quantity::text,
		       default_price::text, default_cost::text, default_currency,
		       tax_class, food_stamp_eligible, age_restriction, weighable,
		       attributes, status, created_at, updated_at
		  FROM m.items
		 WHERE tenant_id = $1 AND id = $2`
	var it types.Item
	err := s.pool.QueryRow(ctx, q, tenantID, itemID).Scan(
		&it.ID, &it.TenantID, &it.SKU, &it.Description, &it.ShortDescription, &it.ItemType,
		&it.CategoryID, &it.UnitOfMeasure, &it.UOMQuantity,
		&it.DefaultPrice, &it.DefaultCost, &it.DefaultCurrency,
		&it.TaxClass, &it.FoodStampEligible, &it.AgeRestriction, &it.Weighable,
		&it.Attributes, &it.Status, &it.CreatedAt, &it.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricing: get_item: %w", err)
	}
	return &it, nil
}

// ListActivePromotions returns all promotions where:
//   - status = 'active'
//   - effective window covers asOf
//   - active_locations is NULL or contains locationID
//   - active_days contains the ISO weekday
//
// SDD-vague: active_hours is a JSONB blob with an unspecified schema
// ({"start": "08:00", "end": "20:00"}). LOOP2-decision: ignore active_hours
// in Wave 2 — implement when promotion-rule library is firmed up.
// Document with // SDD-vague comment in promotions.go evaluator.
func (s *PgxStore) ListActivePromotions(ctx context.Context, tenantID uuid.UUID, locationID uuid.UUID, asOf time.Time) ([]types.Promotion, error) {
	// ISO weekday: Monday = 1 .. Sunday = 7. Go's time.Weekday is
	// Sunday=0..Saturday=6, so map.
	dow := int(asOf.Weekday())
	if dow == 0 {
		dow = 7
	}

	const q = `
		SELECT id, tenant_id, promotion_code, name, description,
		       promotion_type, scope_type, effective_start, effective_end,
		       active_days, active_hours, active_locations, active_channels,
		       customer_segments, stackable, exclusive_with,
		       max_uses_total, max_uses_per_customer, current_uses,
		       attributes, status, created_at, updated_at
		  FROM p.promotions
		 WHERE tenant_id = $1
		   AND status    = 'active'
		   AND effective_start <= $2
		   AND (effective_end IS NULL OR effective_end > $2)
		   AND (active_locations IS NULL OR $3 = ANY(active_locations))
		   AND (active_days     IS NULL OR $4 = ANY(active_days))
		 ORDER BY effective_start DESC`

	rows, err := s.pool.Query(ctx, q, tenantID, asOf, locationID, dow)
	if err != nil {
		return nil, fmt.Errorf("pricing: list_promotions: %w", err)
	}
	defer rows.Close()

	var out []types.Promotion
	for rows.Next() {
		var p types.Promotion
		if err := rows.Scan(
			&p.ID, &p.TenantID, &p.PromotionCode, &p.Name, &p.Description,
			&p.PromotionType, &p.ScopeType, &p.EffectiveStart, &p.EffectiveEnd,
			&p.ActiveDays, &p.ActiveHours, &p.ActiveLocations, &p.ActiveChannels,
			&p.CustomerSegments, &p.Stackable, &p.ExclusiveWith,
			&p.MaxUsesTotal, &p.MaxUsesPerCustomer, &p.CurrentUses,
			&p.Attributes, &p.Status, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("pricing: scan_promotion: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing: rows_promotions: %w", err)
	}
	return out, nil
}

// ListPromotionRules returns the rules for a single promotion in rule_order.
func (s *PgxStore) ListPromotionRules(ctx context.Context, tenantID, promotionID uuid.UUID) ([]types.PromotionRule, error) {
	const q = `
		SELECT id, tenant_id, promotion_id, rule_order, trigger_type,
		       trigger_qualifier, benefit_type, benefit_qualifier,
		       created_at, updated_at
		  FROM p.promotion_rules
		 WHERE tenant_id = $1 AND promotion_id = $2
		 ORDER BY rule_order ASC`
	rows, err := s.pool.Query(ctx, q, tenantID, promotionID)
	if err != nil {
		return nil, fmt.Errorf("pricing: list_promo_rules: %w", err)
	}
	defer rows.Close()
	var out []types.PromotionRule
	for rows.Next() {
		var r types.PromotionRule
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.PromotionID, &r.RuleOrder, &r.TriggerType,
			&r.TriggerQualifier, &r.BenefitType, &r.BenefitQualifier,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("pricing: scan_rule: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTaxClassByCode resolves an item's tax_class string to a TaxClass row.
func (s *PgxStore) GetTaxClassByCode(ctx context.Context, tenantID uuid.UUID, code string) (*types.TaxClass, error) {
	const q = `
		SELECT id, tenant_id, code, name, description, is_default,
		       attributes, status, created_at, updated_at
		  FROM p.tax_classes
		 WHERE tenant_id = $1 AND code = $2 AND status = 'active'
		 LIMIT 1`
	var c types.TaxClass
	err := s.pool.QueryRow(ctx, q, tenantID, code).Scan(
		&c.ID, &c.TenantID, &c.Code, &c.Name, &c.Description, &c.IsDefault,
		&c.Attributes, &c.Status, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricing: get_tax_class: %w", err)
	}
	return &c, nil
}

// GetTaxRate finds the active rate for (tenant, class, location) at asOf.
// Falls back to tenant-wide default (location_id NULL) if no
// location-specific row exists.
func (s *PgxStore) GetTaxRate(ctx context.Context, tenantID uuid.UUID, taxClassID uuid.UUID, locationID *uuid.UUID, asOf time.Time) (*types.TaxRate, error) {
	const q = `
		SELECT id, tenant_id, tax_class_id, location_id, jurisdiction,
		       rate_type, rate::text,
		       effective_start, effective_end, attributes, created_at, updated_at
		  FROM p.tax_rates
		 WHERE tenant_id    = $1
		   AND tax_class_id = $2
		   AND effective_start <= $4
		   AND (effective_end IS NULL OR effective_end > $4)
		   AND (
		         ($3::uuid IS NOT NULL AND location_id = $3)
		      OR ($3::uuid IS     NULL AND location_id IS NULL)
		       )
		 ORDER BY (location_id IS NOT NULL) DESC, effective_start DESC
		 LIMIT 1`

	row, err := s.fetchTaxRate(ctx, q, tenantID, taxClassID, locationID, asOf)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Fallback to tenant-wide default.
	return s.fetchTaxRate(ctx, q, tenantID, taxClassID, nil, asOf)
}

func (s *PgxStore) fetchTaxRate(ctx context.Context, q string, tenantID, taxClassID uuid.UUID, locationID *uuid.UUID, asOf time.Time) (*types.TaxRate, error) {
	var r types.TaxRate
	// SDD-bug: schema column is `effective_start date` (date not timestamptz).
	// We bind a time.Time which pgx coerces correctly. Documented for
	// future-self when promo timing crosses midnight in odd time zones.
	asOfDate := asOf
	err := s.pool.QueryRow(ctx, q, tenantID, taxClassID, locationID, asOfDate).Scan(
		&r.ID, &r.TenantID, &r.TaxClassID, &r.LocationID, &r.Jurisdiction,
		&r.RateType, &r.Rate,
		&r.EffectiveStart, &r.EffectiveEnd, &r.Attributes, &r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricing: get_tax_rate: %w", err)
	}
	return &r, nil
}

// ListTaxRates returns all currently-effective rates for the tenant +
// optional location filter, joined with tax_class code for friendly
// display.
func (s *PgxStore) ListTaxRates(ctx context.Context, tenantID uuid.UUID, locationID *uuid.UUID) ([]TaxRateRow, error) {
	const q = `
		SELECT r.id, r.tenant_id, r.tax_class_id, r.location_id, r.jurisdiction,
		       r.rate_type, r.rate::text,
		       r.effective_start, r.effective_end, r.attributes,
		       r.created_at, r.updated_at,
		       c.code
		  FROM p.tax_rates r
		  JOIN p.tax_classes c ON c.id = r.tax_class_id
		 WHERE r.tenant_id = $1
		   AND (r.effective_end IS NULL OR r.effective_end > CURRENT_DATE)
		   AND (
		         ($2::uuid IS NULL)
		      OR (r.location_id = $2)
		      OR (r.location_id IS NULL)
		       )
		 ORDER BY (r.location_id IS NOT NULL) DESC, c.code ASC`
	rows, err := s.pool.Query(ctx, q, tenantID, locationID)
	if err != nil {
		return nil, fmt.Errorf("pricing: list_tax_rates: %w", err)
	}
	defer rows.Close()
	var out []TaxRateRow
	for rows.Next() {
		var r TaxRateRow
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.TaxClassID, &r.LocationID, &r.Jurisdiction,
			&r.RateType, &r.Rate,
			&r.EffectiveStart, &r.EffectiveEnd, &r.Attributes,
			&r.CreatedAt, &r.UpdatedAt,
			&r.TaxClassCode,
		); err != nil {
			return nil, fmt.Errorf("pricing: scan_tax_rate: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
