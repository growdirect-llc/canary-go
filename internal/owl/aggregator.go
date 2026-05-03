package owl

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DefaultTopItemsLimit is what /v1/owl/dashboard returns when no
// override is provided. 10 is the merchant-comprehensible number;
// anything wider is an analyst tool, not a dashboard tile.
const DefaultTopItemsLimit = 10

// DefaultExposureLimit is the cashier-exposure top-N. 5 because the
// metric is a risk concentration signal — if the top 5 cashiers
// account for the bulk of detections, that's what matters.
const DefaultExposureLimit = 5

// Aggregator coordinates per-metric reads into a single Dashboard.
//
// One coordinator method (Aggregate) and several specialized methods
// (Sales, TopItems, ...) so the handler tier can serve both the wide
// /v1/owl/dashboard endpoint and the narrow per-section endpoints
// without duplicating period parsing.
type Aggregator struct {
	store Store
	now   func() time.Time // injected for tests
}

// NewAggregator wires an Aggregator over a Store.
func NewAggregator(store Store) *Aggregator {
	return &Aggregator{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// WithNow overrides the time source — for tests only.
func (a *Aggregator) WithNow(now func() time.Time) *Aggregator {
	a.now = now
	return a
}

// Aggregate is the top-level dashboard composition. Each metric is a
// separate query — there is no super-CTE trying to do everything at
// once. That's deliberate: the queries have different time anchors
// (started_at vs detected_at vs opened_at) and different join shapes,
// and Postgres plans them better as independent statements.
//
// Loop 3 candidate optimization: run the metric queries in parallel
// goroutines. Holding off in Loop 2 — sequential is easier to debug
// when something is wrong, and the tile count is bounded.
func (a *Aggregator) Aggregate(ctx context.Context, merchantID uuid.UUID, p Period) (Dashboard, error) {
	tenantID, _, err := a.store.ResolveMerchant(ctx, merchantID)
	if err != nil {
		return Dashboard{}, err
	}
	return a.aggregateForTenant(ctx, merchantID, tenantID, p, DefaultTopItemsLimit, DefaultExposureLimit)
}

// aggregateForTenant is the inner version that doesn't re-resolve.
// Used both by Aggregate and by tests that want to skip the resolve
// hop and pin a tenant directly.
func (a *Aggregator) aggregateForTenant(ctx context.Context, merchantID, tenantID uuid.UUID, p Period, topItemsLimit, exposureLimit int) (Dashboard, error) {
	out := Dashboard{
		MerchantID:  merchantID,
		TenantID:    tenantID,
		Period:      p,
		GeneratedAt: a.now(),
	}

	sales, err := a.store.SalesSummary(ctx, tenantID, p)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: sales: %w", err)
	}
	out.Sales = sales

	byUnits, err := a.store.TopItemsByUnits(ctx, tenantID, p, topItemsLimit)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: top items by units: %w", err)
	}
	byRev, err := a.store.TopItemsByRevenue(ctx, tenantID, p, topItemsLimit)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: top items by revenue: %w", err)
	}
	unknown, err := a.store.UnknownItemCount(ctx, tenantID, p)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: unknown items: %w", err)
	}
	out.TopItems = TopItems{
		Limit:        topItemsLimit,
		ByUnits:      byUnits,
		ByRevenue:    byRev,
		UnknownItems: unknown,
	}

	byLoc, err := a.store.SalesByLocation(ctx, tenantID, p)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: by location: %w", err)
	}
	out.ByLocation = byLoc

	cases, err := a.store.CasesSummary(ctx, tenantID, p)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: cases: %w", err)
	}
	out.Cases = cases

	det, err := a.store.DetectionRate(ctx, tenantID, p)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: detection rate: %w", err)
	}
	out.Detection = det

	exp, err := a.store.CashierExposure(ctx, tenantID, p, exposureLimit)
	if err != nil {
		return Dashboard{}, fmt.Errorf("aggregator: exposure: %w", err)
	}
	out.Exposure = exp

	return out, nil
}

// Sales is the narrow accessor for the sales-only endpoint. Resolves
// merchant inline.
func (a *Aggregator) Sales(ctx context.Context, merchantID uuid.UUID, p Period) (uuid.UUID, SalesSummary, error) {
	tenantID, _, err := a.store.ResolveMerchant(ctx, merchantID)
	if err != nil {
		return uuid.Nil, SalesSummary{}, err
	}
	s, err := a.store.SalesSummary(ctx, tenantID, p)
	if err != nil {
		return tenantID, SalesSummary{}, fmt.Errorf("aggregator: sales: %w", err)
	}
	return tenantID, s, nil
}

// TopItems is the narrow accessor for the top-items endpoint.
func (a *Aggregator) TopItems(ctx context.Context, merchantID uuid.UUID, p Period, by TopItemsBy, limit int) (uuid.UUID, []ItemMetric, error) {
	tenantID, _, err := a.store.ResolveMerchant(ctx, merchantID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	switch by {
	case TopItemsByRevenue:
		out, err := a.store.TopItemsByRevenue(ctx, tenantID, p, limit)
		return tenantID, out, err
	case TopItemsByUnits, "":
		out, err := a.store.TopItemsByUnits(ctx, tenantID, p, limit)
		return tenantID, out, err
	default:
		return tenantID, nil, fmt.Errorf("owl: unknown by=%q (want units|revenue)", by)
	}
}

// Cases is the narrow accessor for the cases-only endpoint.
func (a *Aggregator) Cases(ctx context.Context, merchantID uuid.UUID, p Period) (uuid.UUID, CasesSummary, error) {
	tenantID, _, err := a.store.ResolveMerchant(ctx, merchantID)
	if err != nil {
		return uuid.Nil, CasesSummary{}, err
	}
	c, err := a.store.CasesSummary(ctx, tenantID, p)
	if err != nil {
		return tenantID, CasesSummary{}, fmt.Errorf("aggregator: cases: %w", err)
	}
	return tenantID, c, nil
}

// Exposure is the narrow accessor for the exposure-only endpoint.
func (a *Aggregator) Exposure(ctx context.Context, merchantID uuid.UUID, p Period, limit int) (uuid.UUID, []CashierExposure, error) {
	tenantID, _, err := a.store.ResolveMerchant(ctx, merchantID)
	if err != nil {
		return uuid.Nil, nil, err
	}
	out, err := a.store.CashierExposure(ctx, tenantID, p, limit)
	if err != nil {
		return tenantID, nil, fmt.Errorf("aggregator: exposure: %w", err)
	}
	return tenantID, out, nil
}

// ResolveMerchantTimezone is a convenience for handlers that need the
// merchant's IANA timezone before parsing the period query string.
func (a *Aggregator) ResolveMerchantTimezone(ctx context.Context, merchantID uuid.UUID) (uuid.UUID, string, error) {
	return a.store.ResolveMerchant(ctx, merchantID)
}
