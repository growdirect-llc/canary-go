package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// stubStore implements Store with in-memory tables. Tests assemble it
// per-case so each scenario stands alone.
type stubStore struct {
	items     map[uuid.UUID]types.Item
	prices    map[uuid.UUID]types.ItemPrice  // key = item_id
	promos    []types.Promotion
	promoRule map[uuid.UUID][]types.PromotionRule
	taxClass  map[string]types.TaxClass // key = code
	taxRate   map[uuid.UUID]types.TaxRate // key = tax_class_id
}

func newStubStore() *stubStore {
	return &stubStore{
		items:     map[uuid.UUID]types.Item{},
		prices:    map[uuid.UUID]types.ItemPrice{},
		promoRule: map[uuid.UUID][]types.PromotionRule{},
		taxClass:  map[string]types.TaxClass{},
		taxRate:   map[uuid.UUID]types.TaxRate{},
	}
}

func (s *stubStore) GetItemPrice(_ context.Context, _ uuid.UUID, itemID uuid.UUID, _ *uuid.UUID, _ string, _ time.Time) (*types.ItemPrice, error) {
	p, ok := s.prices[itemID]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

func (s *stubStore) GetItem(_ context.Context, _ uuid.UUID, itemID uuid.UUID) (*types.Item, error) {
	it, ok := s.items[itemID]
	if !ok {
		return nil, ErrNotFound
	}
	return &it, nil
}

func (s *stubStore) ListActivePromotions(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ time.Time) ([]types.Promotion, error) {
	return s.promos, nil
}

func (s *stubStore) ListPromotionRules(_ context.Context, _ uuid.UUID, promoID uuid.UUID) ([]types.PromotionRule, error) {
	return s.promoRule[promoID], nil
}

func (s *stubStore) GetTaxClassByCode(_ context.Context, _ uuid.UUID, code string) (*types.TaxClass, error) {
	c, ok := s.taxClass[code]
	if !ok {
		return nil, ErrNotFound
	}
	return &c, nil
}

func (s *stubStore) GetTaxRate(_ context.Context, _ uuid.UUID, classID uuid.UUID, _ *uuid.UUID, _ time.Time) (*types.TaxRate, error) {
	r, ok := s.taxRate[classID]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (s *stubStore) ListTaxRates(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]TaxRateRow, error) {
	out := make([]TaxRateRow, 0, len(s.taxRate))
	for _, r := range s.taxRate {
		// reverse-lookup the class code
		code := ""
		for k, c := range s.taxClass {
			if c.ID == r.TaxClassID {
				code = k
				break
			}
		}
		out = append(out, TaxRateRow{TaxRate: r, TaxClassCode: code})
	}
	return out, nil
}

// --- helpers for building fixtures ---

func mkItem(s *stubStore, id uuid.UUID, taxClass *string) {
	s.items[id] = types.Item{
		ID:              id,
		TenantID:        uuid.New(),
		SKU:             "TEST-" + id.String()[:8],
		Description:     "Test item",
		ItemType:        "standard",
		UnitOfMeasure:   "EA",
		UOMQuantity:     "1",
		DefaultCurrency: "USD",
		TaxClass:        taxClass,
		Status:          "active",
		Attributes:      json.RawMessage("{}"),
	}
}

func mkPrice(s *stubStore, itemID uuid.UUID, amount string) {
	s.prices[itemID] = types.ItemPrice{
		ID:        uuid.New(),
		TenantID:  uuid.New(),
		ItemID:    itemID,
		Amount:    amount,
		Currency:  "USD",
		UOM:       "EA",
		PriceType: "regular",
		Status:    "active",
		Attributes: json.RawMessage("{}"),
		EffectiveStart: time.Now().Add(-time.Hour),
	}
}

func mkPromo(s *stubStore, code, benefitType, qualKey, qualVal string, stackable bool, itemIDs ...uuid.UUID) types.Promotion {
	p := types.Promotion{
		ID:             uuid.New(),
		TenantID:       uuid.New(),
		PromotionCode:  code,
		Name:           code,
		PromotionType:  benefitType,
		ScopeType:      "item",
		Stackable:      stackable,
		Status:         "active",
		EffectiveStart: time.Now().Add(-time.Hour),
		ActiveDays:     []int32{1, 2, 3, 4, 5, 6, 7},
		Attributes:     json.RawMessage("{}"),
		ActiveHours:    json.RawMessage("{}"),
	}
	s.promos = append(s.promos, p)

	// Build the trigger qualifier with item_ids list (or empty for "all").
	trig := map[string]any{}
	if len(itemIDs) > 0 {
		ids := make([]string, 0, len(itemIDs))
		for _, id := range itemIDs {
			ids = append(ids, id.String())
		}
		trig["item_ids"] = ids
	}
	trigJSON, _ := json.Marshal(trig)

	bene, _ := json.Marshal(map[string]string{qualKey: qualVal})

	s.promoRule[p.ID] = []types.PromotionRule{{
		ID:               uuid.New(),
		TenantID:         p.TenantID,
		PromotionID:      p.ID,
		RuleOrder:        1,
		TriggerType:      "buy_quantity",
		TriggerQualifier: trigJSON,
		BenefitType:      benefitType,
		BenefitQualifier: bene,
	}}
	return p
}

func mkTax(s *stubStore, code, rate string) uuid.UUID {
	classID := uuid.New()
	s.taxClass[code] = types.TaxClass{
		ID:       classID,
		TenantID: uuid.New(),
		Code:     code,
		Name:     code,
		Status:   "active",
		Attributes: json.RawMessage("{}"),
	}
	s.taxRate[classID] = types.TaxRate{
		ID:             uuid.New(),
		TenantID:       uuid.New(),
		TaxClassID:     classID,
		RateType:       "percentage",
		Rate:           rate,
		EffectiveStart: time.Now().Add(-24 * time.Hour),
		Attributes:     json.RawMessage("{}"),
	}
	return classID
}

func mkResolver(s *stubStore) *Resolver {
	return NewResolver(s, nil)
}

// --- tests ---

func TestResolve_BasePriceOnly_NoPromoNoTax(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "10.00")

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "2"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resp.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(resp.Lines))
	}
	line := resp.Lines[0]
	if line.BasePrice != "10.00" {
		t.Errorf("base price: want 10.00 got %s", line.BasePrice)
	}
	if line.UnitPriceAfterDiscount != "10.00" {
		t.Errorf("unit after: want 10.00 got %s", line.UnitPriceAfterDiscount)
	}
	if line.LineSubtotal != "20.00" {
		t.Errorf("subtotal: want 20.00 got %s", line.LineSubtotal)
	}
	if len(line.AppliedPromotions) != 0 {
		t.Errorf("no promos expected, got %d", len(line.AppliedPromotions))
	}
	if len(line.TaxLines) != 0 {
		t.Errorf("no tax expected, got %d", len(line.TaxLines))
	}
	if line.LineTotal != "20.00" {
		t.Errorf("line total: want 20.00 got %s", line.LineTotal)
	}
	if resp.CartTotal != "20.00" {
		t.Errorf("cart: want 20.00 got %s", resp.CartTotal)
	}
}

func TestResolve_PercentOffPromotion(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "100.00")
	mkPromo(s, "TWENTY", "percent_off", "percent", "0.20", false, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "80.00" {
		t.Errorf("want 80.00 after 20%% off, got %s", line.UnitPriceAfterDiscount)
	}
	if len(line.AppliedPromotions) != 1 {
		t.Fatalf("want 1 promo, got %d", len(line.AppliedPromotions))
	}
	if line.AppliedPromotions[0].DiscountAmount != "20.00" {
		t.Errorf("discount: want 20.00 got %s", line.AppliedPromotions[0].DiscountAmount)
	}
}

func TestResolve_AmountOffPromotion(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "15.00")
	mkPromo(s, "FIVE_OFF", "amount_off", "amount", "5.00", false, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "3"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "10.00" {
		t.Errorf("unit after: want 10.00 got %s", line.UnitPriceAfterDiscount)
	}
	if line.LineSubtotal != "30.00" {
		t.Errorf("subtotal: want 30.00 got %s", line.LineSubtotal)
	}
}

func TestResolve_FixedPricePromotion(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "12.99")
	mkPromo(s, "DOLLAR_DEAL", "fixed_price", "fixed_price", "1.00", false, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "1.00" {
		t.Errorf("unit after: want 1.00 got %s", line.UnitPriceAfterDiscount)
	}
}

func TestResolve_WithTax(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	std := "STD"
	mkItem(s, itemID, &std)
	mkPrice(s, itemID, "100.00")
	mkTax(s, "STD", "0.0825") // 8.25%

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.LineSubtotal != "100.00" {
		t.Errorf("subtotal: want 100.00 got %s", line.LineSubtotal)
	}
	if len(line.TaxLines) != 1 {
		t.Fatalf("want 1 tax line, got %d", len(line.TaxLines))
	}
	if line.TaxLines[0].TaxAmount != "8.25" {
		t.Errorf("tax: want 8.25 got %s", line.TaxLines[0].TaxAmount)
	}
	if line.LineTotal != "108.25" {
		t.Errorf("total: want 108.25 got %s", line.LineTotal)
	}
}

func TestResolve_MultiplePromotions_PickBest(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "100.00")
	// Two non-stackable: 10% off (= $10) vs $25 off → $25 wins.
	mkPromo(s, "TEN_PCT", "percent_off", "percent", "0.10", false, itemID)
	mkPromo(s, "TWENTY_FIVE", "amount_off", "amount", "25.00", false, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "75.00" {
		t.Errorf("want 75.00 (best of two non-stackable), got %s", line.UnitPriceAfterDiscount)
	}
	if len(line.AppliedPromotions) != 1 {
		t.Errorf("want exactly 1 applied promo, got %d", len(line.AppliedPromotions))
	}
	if line.AppliedPromotions[0].PromotionCode != "TWENTY_FIVE" {
		t.Errorf("want TWENTY_FIVE picked, got %s", line.AppliedPromotions[0].PromotionCode)
	}
}

func TestResolve_StackablePromotions(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "100.00")
	// Two stackable: 10% (=$10) + $5 → $15 off.
	mkPromo(s, "TEN_PCT", "percent_off", "percent", "0.10", true, itemID)
	mkPromo(s, "FIVE_OFF", "amount_off", "amount", "5.00", true, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "85.00" {
		t.Errorf("want 85.00 (both stack), got %s", line.UnitPriceAfterDiscount)
	}
	if len(line.AppliedPromotions) != 2 {
		t.Errorf("want 2 applied promos, got %d", len(line.AppliedPromotions))
	}
}

func TestResolve_StackableAndNonStackableMix(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "100.00")
	// Best non-stackable ($25) + one stackable ($5) = $30 off → $70.
	mkPromo(s, "BIG_NS", "amount_off", "amount", "25.00", false, itemID)
	mkPromo(s, "SMALL_NS", "amount_off", "amount", "10.00", false, itemID) // loses
	mkPromo(s, "STACK_FIVE", "amount_off", "amount", "5.00", true, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.UnitPriceAfterDiscount != "70.00" {
		t.Errorf("want 70.00, got %s", line.UnitPriceAfterDiscount)
	}
	if len(line.AppliedPromotions) != 2 {
		t.Errorf("want 2 applied (best non-stackable + one stackable), got %d", len(line.AppliedPromotions))
	}
}

func TestResolve_ZeroQuantity_Errors(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "10.00")

	r := mkResolver(s)
	_, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "0"}},
	})
	if err == nil {
		t.Fatal("want error for zero quantity")
	}
	if !strings.Contains(err.Error(), "zero quantity") {
		t.Errorf("want zero-quantity error, got %v", err)
	}
}

func TestResolve_NoPriceFound_Errors(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	// no mkPrice — and no default_price on item

	r := mkResolver(s)
	_, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err == nil {
		t.Fatal("want error for missing price")
	}
	if !strings.Contains(err.Error(), "no price found") {
		t.Errorf("want missing-price error, got %v", err)
	}
}

func TestResolve_FractionalQuantity(t *testing.T) {
	// 0.5 lb of an item priced $4.00/lb → subtotal $2.00.
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "4.00")

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "0.5"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	line := resp.Lines[0]
	if line.LineSubtotal != "2.00" {
		t.Errorf("subtotal: want 2.00 got %s", line.LineSubtotal)
	}
}

func TestResolve_DiscountClampedAtBasePrice(t *testing.T) {
	// $5 base, $10 off promo → unit drops to $0, not negative.
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "5.00")
	mkPromo(s, "TOO_MUCH", "amount_off", "amount", "10.00", false, itemID)

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resp.Lines[0].UnitPriceAfterDiscount != "0.00" {
		t.Errorf("want 0.00 (clamped), got %s", resp.Lines[0].UnitPriceAfterDiscount)
	}
}

func TestResolve_MultiLine_CartTotal(t *testing.T) {
	s := newStubStore()
	a, b := uuid.New(), uuid.New()
	mkItem(s, a, nil)
	mkItem(s, b, nil)
	mkPrice(s, a, "3.50")
	mkPrice(s, b, "12.00")

	r := mkResolver(s)
	resp, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines: []RequestLine{
			{ItemID: a, Quantity: "2"}, // 7.00
			{ItemID: b, Quantity: "1"}, // 12.00
		},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resp.CartTotal != "19.00" {
		t.Errorf("cart: want 19.00 got %s", resp.CartTotal)
	}
}

func TestResolve_MissingTenant_Errors(t *testing.T) {
	s := newStubStore()
	itemID := uuid.New()
	mkItem(s, itemID, nil)
	mkPrice(s, itemID, "1.00")

	r := mkResolver(s)
	_, err := r.Resolve(context.Background(), &ResolveRequest{
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: itemID, Quantity: "1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "tenant_id required") {
		t.Errorf("want tenant_id error, got %v", err)
	}
}

func TestMoney_RoundingHalfUp(t *testing.T) {
	// 0.005 → 1 cent (half up).
	cents, err := parseMajorToCents("0.005")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cents != 1 {
		t.Errorf("0.005 → want 1 cent (half-up), got %d", cents)
	}
}

func TestMoney_TaxRoundingExact(t *testing.T) {
	// $33.33 × 8.25% = $2.749725 → rounds to $2.75 half-up
	cents, _ := parseMajorToCents("33.33")
	ppm, _ := parseRateToPpm("0.0825")
	tax := multiplyCentsByPpm(cents, ppm)
	if tax != 275 {
		t.Errorf("want 275 cents tax, got %d (=%s)", tax, formatCents(tax))
	}
}

// stubStoreErr always returns the configured error, used to exercise
// resolver error paths without setting up tables.
type stubStoreErr struct {
	stubStore
	err error
}

func (s *stubStoreErr) GetItem(ctx context.Context, t, id uuid.UUID) (*types.Item, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.stubStore.GetItem(ctx, t, id)
}

func TestResolve_StorePropagatesGenericError(t *testing.T) {
	s := &stubStoreErr{
		stubStore: *newStubStore(),
		err:       errors.New("boom"),
	}
	r := NewResolver(s, nil)
	_, err := r.Resolve(context.Background(), &ResolveRequest{
		TenantID:   uuid.New(),
		LocationID: uuid.New(),
		Lines:      []RequestLine{{ItemID: uuid.New(), Quantity: "1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("want propagated error, got %v", err)
	}
}
