package owl

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubStore implements Store with in-memory canned responses. Lets us
// verify the aggregator's plumbing — the order of calls, the
// propagation of merchant→tenant, the assembly of the Dashboard
// envelope — without standing up Postgres.
type stubStore struct {
	tenantID    uuid.UUID
	timezone    string
	resolveErr  error

	salesCalls    int
	unitsCalls    int
	revenueCalls  int
	unknownCalls  int
	locCalls      int
	casesCalls    int
	detCalls      int
	exposureCalls int

	sales     SalesSummary
	byUnits   []ItemMetric
	byRevenue []ItemMetric
	unknown   int64
	byLoc     []LocationMetric
	cases     CasesSummary
	detection DetectionRate
	exposure  []CashierExposure
}

func (s *stubStore) ResolveMerchant(ctx context.Context, mid uuid.UUID) (uuid.UUID, string, error) {
	if s.resolveErr != nil {
		return uuid.Nil, "", s.resolveErr
	}
	return s.tenantID, s.timezone, nil
}

func (s *stubStore) SalesSummary(_ context.Context, _ uuid.UUID, _ Period) (SalesSummary, error) {
	s.salesCalls++
	return s.sales, nil
}

func (s *stubStore) TopItemsByUnits(_ context.Context, _ uuid.UUID, _ Period, _ int) ([]ItemMetric, error) {
	s.unitsCalls++
	return s.byUnits, nil
}

func (s *stubStore) TopItemsByRevenue(_ context.Context, _ uuid.UUID, _ Period, _ int) ([]ItemMetric, error) {
	s.revenueCalls++
	return s.byRevenue, nil
}

func (s *stubStore) UnknownItemCount(_ context.Context, _ uuid.UUID, _ Period) (int64, error) {
	s.unknownCalls++
	return s.unknown, nil
}

func (s *stubStore) SalesByLocation(_ context.Context, _ uuid.UUID, _ Period) ([]LocationMetric, error) {
	s.locCalls++
	return s.byLoc, nil
}

func (s *stubStore) CasesSummary(_ context.Context, _ uuid.UUID, _ Period) (CasesSummary, error) {
	s.casesCalls++
	return s.cases, nil
}

func (s *stubStore) DetectionRate(_ context.Context, _ uuid.UUID, _ Period) (DetectionRate, error) {
	s.detCalls++
	return s.detection, nil
}

func (s *stubStore) CashierExposure(_ context.Context, _ uuid.UUID, _ Period, _ int) ([]CashierExposure, error) {
	s.exposureCalls++
	return s.exposure, nil
}

// ──────────────────────────────────────────────────────────────────────
// Period parsing
// ──────────────────────────────────────────────────────────────────────

func TestParsePeriod_Today_UTC(t *testing.T) {
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"today"}}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	want := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v", p.From, want)
	}
	if !p.To.Equal(now) {
		t.Errorf("To = %v, want %v", p.To, now)
	}
	if p.Kind != PeriodToday {
		t.Errorf("Kind = %s, want today", p.Kind)
	}
}

func TestParsePeriod_Today_LosAngeles(t *testing.T) {
	// 2026-05-02 06:00 UTC = 2026-05-01 23:00 PDT (DST). "Today" in
	// LA is still 2026-05-01.
	now := time.Date(2026, 5, 2, 6, 0, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"today"}}, "America/Los_Angeles", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	loc, _ := time.LoadLocation("America/Los_Angeles")
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, loc).UTC()
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v (LA midnight = 07:00 UTC during DST)", p.From, want)
	}
}

func TestParsePeriod_WTD_MondayAnchor(t *testing.T) {
	// 2026-05-02 is a Saturday. Week-to-date should anchor at Monday
	// 2026-04-27 00:00 UTC.
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"wtd"}}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	want := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v (Monday)", p.From, want)
	}
}

func TestParsePeriod_WTD_OnSunday(t *testing.T) {
	// 2026-05-03 is a Sunday. "WTD" should still anchor at Monday
	// 2026-04-27 — Sunday is the last day of the week, not a fresh one.
	now := time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"wtd"}}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	want := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v (Monday)", p.From, want)
	}
}

func TestParsePeriod_WTD_OnMonday(t *testing.T) {
	// 2026-05-04 is a Monday. WTD should anchor at the same day's
	// midnight, not roll back a week.
	now := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"wtd"}}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v (this Monday)", p.From, want)
	}
}

func TestParsePeriod_MTD(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{"period": {"mtd"}}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !p.From.Equal(want) {
		t.Errorf("From = %v, want %v (1st of month)", p.From, want)
	}
}

func TestParsePeriod_Range_Valid(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	q := url.Values{
		"period": {"range"},
		"from":   {"2026-04-01T00:00:00Z"},
		"to":     {"2026-05-01T00:00:00Z"},
	}
	p, err := ParsePeriod(q, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	if p.From.Day() != 1 || p.From.Month() != 4 {
		t.Errorf("From = %v, want 2026-04-01", p.From)
	}
	if p.To.Day() != 1 || p.To.Month() != 5 {
		t.Errorf("To = %v, want 2026-05-01", p.To)
	}
}

func TestParsePeriod_Range_MissingFromTo(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	_, err := ParsePeriod(url.Values{"period": {"range"}}, "UTC", now)
	if err == nil {
		t.Fatal("expected error when from/to omitted")
	}
}

func TestParsePeriod_Range_FromAfterTo(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	q := url.Values{
		"period": {"range"},
		"from":   {"2026-05-01T00:00:00Z"},
		"to":     {"2026-04-01T00:00:00Z"},
	}
	_, err := ParsePeriod(q, "UTC", now)
	if err == nil {
		t.Fatal("expected error when from >= to")
	}
}

func TestParsePeriod_DefaultsToToday(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	p, err := ParsePeriod(url.Values{}, "UTC", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	if p.Kind != PeriodToday {
		t.Errorf("Kind = %s, want today (default)", p.Kind)
	}
}

func TestParsePeriod_BadKind(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	_, err := ParsePeriod(url.Values{"period": {"yesterday"}}, "UTC", now)
	if err == nil {
		t.Fatal("expected error for unknown period")
	}
}

func TestParsePeriod_BadTimezone(t *testing.T) {
	now := time.Date(2026, 5, 17, 14, 30, 0, 0, time.UTC)
	_, err := ParsePeriod(url.Values{"period": {"today"}}, "Mars/Olympus", now)
	if err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

// ──────────────────────────────────────────────────────────────────────
// Aggregator wiring
// ──────────────────────────────────────────────────────────────────────

func TestAggregator_Aggregate_HappyPath(t *testing.T) {
	mid := uuid.New()
	tid := uuid.New()
	itemID := uuid.New()
	locID := uuid.New()
	empID := uuid.New()
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	store := &stubStore{
		tenantID: tid,
		timezone: "UTC",
		sales: SalesSummary{
			GrossSales:       "12500.00",
			NetSales:         "11800.00",
			RefundTotal:      "200.00",
			DiscountTotal:    "500.00",
			TaxTotal:         "1000.00",
			TransactionCount: 87,
			RefundCount:      2,
			AverageTicket:    "143.68",
		},
		byUnits: []ItemMetric{
			{ItemID: itemID, SKU: "SKU-1", Description: "Widget", Units: "23", Revenue: "230.00"},
		},
		byRevenue: []ItemMetric{
			{ItemID: itemID, SKU: "SKU-2", Description: "Premium Widget", Units: "5", Revenue: "1250.00"},
		},
		unknown: 3,
		byLoc: []LocationMetric{
			{LocationID: locID, LocationCode: "01", LocationName: "Main", GrossSales: "12500.00", NetSales: "11800.00", TransactionCount: 87},
		},
		cases: CasesSummary{
			OpenNow:        4,
			OpenedInPeriod: 2,
			ClosedInPeriod: 1,
			BySeverity:     map[string]int64{"high": 2, "medium": 2},
			ByCaseType:     map[string]int64{"investigation": 4},
		},
		detection: DetectionRate{
			DetectionCount:        12,
			TransactionCount:      87,
			RatePer1KTransactions: 137.93,
			BySeverity:            map[string]int64{"medium": 8, "high": 4},
		},
		exposure: []CashierExposure{
			{EmployeeID: empID, EmployeeCode: "E-101", DisplayName: "Alex Park", DetectionCount: 7},
		},
	}

	agg := NewAggregator(store).WithNow(func() time.Time { return now })
	dash, err := agg.Aggregate(context.Background(), mid, Period{Kind: PeriodMTD, From: now.AddDate(0, 0, -16), To: now, Timezone: "UTC"})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	// Identity
	if dash.MerchantID != mid {
		t.Errorf("MerchantID = %v, want %v", dash.MerchantID, mid)
	}
	if dash.TenantID != tid {
		t.Errorf("TenantID = %v, want %v", dash.TenantID, tid)
	}
	if !dash.GeneratedAt.Equal(now) {
		t.Errorf("GeneratedAt = %v, want %v (injected now)", dash.GeneratedAt, now)
	}

	// Each metric called exactly once.
	if store.salesCalls != 1 || store.unitsCalls != 1 || store.revenueCalls != 1 ||
		store.unknownCalls != 1 || store.locCalls != 1 || store.casesCalls != 1 ||
		store.detCalls != 1 || store.exposureCalls != 1 {
		t.Errorf("metric call counts wrong: %+v", store)
	}

	// Wiring sanity — the store data made it into the response.
	if dash.Sales.GrossSales != "12500.00" {
		t.Errorf("Sales.GrossSales = %s, want 12500.00", dash.Sales.GrossSales)
	}
	if dash.TopItems.Limit != DefaultTopItemsLimit {
		t.Errorf("TopItems.Limit = %d, want %d", dash.TopItems.Limit, DefaultTopItemsLimit)
	}
	if dash.TopItems.UnknownItems != 3 {
		t.Errorf("UnknownItems = %d, want 3", dash.TopItems.UnknownItems)
	}
	if len(dash.TopItems.ByUnits) != 1 || dash.TopItems.ByUnits[0].SKU != "SKU-1" {
		t.Errorf("ByUnits = %+v", dash.TopItems.ByUnits)
	}
	if len(dash.ByLocation) != 1 || dash.ByLocation[0].LocationCode != "01" {
		t.Errorf("ByLocation = %+v", dash.ByLocation)
	}
	if dash.Cases.OpenNow != 4 {
		t.Errorf("Cases.OpenNow = %d, want 4", dash.Cases.OpenNow)
	}
	if dash.Detection.DetectionCount != 12 {
		t.Errorf("Detection.DetectionCount = %d, want 12", dash.Detection.DetectionCount)
	}
	if len(dash.Exposure) != 1 || dash.Exposure[0].EmployeeCode != "E-101" {
		t.Errorf("Exposure = %+v", dash.Exposure)
	}
}

func TestAggregator_Aggregate_MerchantNotFound(t *testing.T) {
	store := &stubStore{resolveErr: ErrMerchantNotFound}
	agg := NewAggregator(store)

	_, err := agg.Aggregate(context.Background(), uuid.New(), Period{})
	if !errors.Is(err, ErrMerchantNotFound) {
		t.Fatalf("expected ErrMerchantNotFound, got %v", err)
	}
}

func TestAggregator_TopItems_DispatchesByKey(t *testing.T) {
	store := &stubStore{tenantID: uuid.New()}
	agg := NewAggregator(store)

	if _, _, err := agg.TopItems(context.Background(), uuid.New(), Period{}, TopItemsByUnits, 10); err != nil {
		t.Fatalf("TopItems units: %v", err)
	}
	if store.unitsCalls != 1 || store.revenueCalls != 0 {
		t.Errorf("by=units should hit unitsCalls only, got %+v", store)
	}

	if _, _, err := agg.TopItems(context.Background(), uuid.New(), Period{}, TopItemsByRevenue, 10); err != nil {
		t.Fatalf("TopItems revenue: %v", err)
	}
	if store.revenueCalls != 1 {
		t.Errorf("by=revenue should hit revenueCalls, got %+v", store)
	}

	if _, _, err := agg.TopItems(context.Background(), uuid.New(), Period{}, TopItemsBy("garbage"), 10); err == nil {
		t.Error("expected error on unknown by=")
	}
}

// Compile-time check: PgxStore satisfies Store. Asserts the concrete
// type still matches the interface as both evolve.
var _ Store = (*PgxStore)(nil)
