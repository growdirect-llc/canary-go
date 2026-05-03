//go:build integration

// Integration test for the pricing service. Exercises the real stack:
// pgxpool → p.item_prices, p.promotions, p.promotion_rules, p.tax_classes,
// p.tax_rates → handler end-to-end. Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/pricing/...
//
// Wave 3 coordinator runs this post-merge — the dispatch tells subagents
// not to invoke -tags=integration during their wave.
package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

// pricingFixtures seeds a tenant + location + item + price + promotion +
// tax class + tax rate. Returns the IDs and a cleanup func.
type pricingFixtures struct {
	tenantID   uuid.UUID
	locationID uuid.UUID
	itemID     uuid.UUID
	promoID    uuid.UUID
	taxClassID uuid.UUID
	cleanup    func()
}

func seedPricingFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *pricingFixtures {
	t.Helper()

	orgID := uuid.New()
	tenantID := uuid.New()
	locationID := uuid.New()
	itemID := uuid.New()
	priceID := uuid.New()
	promoID := uuid.New()
	ruleID := uuid.New()
	taxClassID := uuid.New()
	taxRateID := uuid.New()
	suffix := tenantID.String()[:8]

	// Org + tenant
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "pricing-itest-"+suffix); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_name) VALUES ($1, $2, $3)`,
		tenantID, orgID, "pricing-itest-"+suffix); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Location — depends on l.locations schema (03_l_s_locations.sql)
	if _, err := pool.Exec(ctx,
		`INSERT INTO l.locations (id, tenant_id, code, name, location_type, status)
		 VALUES ($1, $2, $3, $4, 'store', 'active')`,
		locationID, tenantID, "STORE-"+suffix, "Test Store"); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	// Item with tax_class STD
	taxClassCode := "STD"
	if _, err := pool.Exec(ctx,
		`INSERT INTO m.items (id, tenant_id, sku, description, item_type, unit_of_measure,
		                       uom_quantity, default_currency, tax_class, status)
		 VALUES ($1, $2, $3, 'Test Widget', 'standard', 'EA', 1, 'USD', $4, 'active')`,
		itemID, tenantID, "WIDGET-"+suffix, taxClassCode); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	// Base price $20.00
	if _, err := pool.Exec(ctx,
		`INSERT INTO p.item_prices (id, tenant_id, item_id, channel, price_type, amount,
		                              currency, uom, effective_start, status)
		 VALUES ($1, $2, $3, 'all', 'regular', 20.00, 'USD', 'EA', now() - interval '1 hour', 'active')`,
		priceID, tenantID, itemID); err != nil {
		t.Fatalf("seed price: %v", err)
	}
	// Active 10%-off promotion targeting this item
	if _, err := pool.Exec(ctx,
		`INSERT INTO p.promotions (id, tenant_id, promotion_code, name, promotion_type,
		                            scope_type, effective_start, effective_end, active_days,
		                            stackable, status)
		 VALUES ($1, $2, $3, 'Ten Off', 'percent_off', 'item',
		         now() - interval '1 hour', now() + interval '1 day',
		         '{1,2,3,4,5,6,7}', false, 'active')`,
		promoID, tenantID, "ITEST-10OFF-"+suffix); err != nil {
		t.Fatalf("seed promo: %v", err)
	}
	trig, _ := json.Marshal(map[string][]string{"item_ids": {itemID.String()}})
	bene, _ := json.Marshal(map[string]string{"percent": "0.10"})
	if _, err := pool.Exec(ctx,
		`INSERT INTO p.promotion_rules (id, tenant_id, promotion_id, rule_order,
		                                 trigger_type, trigger_qualifier,
		                                 benefit_type, benefit_qualifier)
		 VALUES ($1, $2, $3, 1, 'buy_quantity', $4, 'percent_off', $5)`,
		ruleID, tenantID, promoID, trig, bene); err != nil {
		t.Fatalf("seed promo rule: %v", err)
	}
	// Tax class STD at 8.25%
	if _, err := pool.Exec(ctx,
		`INSERT INTO p.tax_classes (id, tenant_id, code, name, status)
		 VALUES ($1, $2, $3, 'Standard', 'active')`,
		taxClassID, tenantID, taxClassCode); err != nil {
		t.Fatalf("seed tax class: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO p.tax_rates (id, tenant_id, tax_class_id, rate_type, rate, effective_start)
		 VALUES ($1, $2, $3, 'percentage', 0.0825, CURRENT_DATE - 1)`,
		taxRateID, tenantID, taxClassID); err != nil {
		t.Fatalf("seed tax rate: %v", err)
	}

	cleanup := func() {
		// Best-effort, dependency order
		_, _ = pool.Exec(ctx, `DELETE FROM p.tax_rates WHERE id = $1`, taxRateID)
		_, _ = pool.Exec(ctx, `DELETE FROM p.tax_classes WHERE id = $1`, taxClassID)
		_, _ = pool.Exec(ctx, `DELETE FROM p.promotion_rules WHERE id = $1`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM p.promotions WHERE id = $1`, promoID)
		_, _ = pool.Exec(ctx, `DELETE FROM p.item_prices WHERE id = $1`, priceID)
		_, _ = pool.Exec(ctx, `DELETE FROM m.items WHERE id = $1`, itemID)
		_, _ = pool.Exec(ctx, `DELETE FROM l.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}

	return &pricingFixtures{
		tenantID: tenantID, locationID: locationID, itemID: itemID,
		promoID: promoID, taxClassID: taxClassID, cleanup: cleanup,
	}
}

func TestIntegration_Resolve_HappyPath(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("postgres ping: %v", err)
	}

	fx := seedPricingFixtures(t, ctx, pool)
	defer fx.cleanup()

	store := NewPgxStore(pool)
	resolver := NewResolver(store, zap.NewNop())
	handler := New(resolver, store, zap.NewNop())

	r := chi.NewRouter()
	handler.Mount(r)

	// $20.00 × 1, 10% off → $18.00 subtotal, 8.25% tax → $1.49 tax,
	// line total $19.49.
	body, _ := json.Marshal(map[string]any{
		"tenant_id":   fx.tenantID.String(),
		"location_id": fx.locationID.String(),
		"lines": []map[string]any{
			{"item_id": fx.itemID.String(), "quantity": "1"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/pricing/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	start := time.Now()
	r.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp ResolveResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	t.Logf("resolve latency: %s", elapsed)

	if len(resp.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(resp.Lines))
	}
	line := resp.Lines[0]
	if line.BasePrice != "20.00" {
		t.Errorf("base: want 20.00 got %s", line.BasePrice)
	}
	if line.UnitPriceAfterDiscount != "18.00" {
		t.Errorf("unit after promo: want 18.00 got %s", line.UnitPriceAfterDiscount)
	}
	if len(line.AppliedPromotions) != 1 {
		t.Errorf("want 1 promo applied, got %d", len(line.AppliedPromotions))
	}
	if len(line.TaxLines) != 1 {
		t.Errorf("want 1 tax line, got %d", len(line.TaxLines))
	}
	if line.TaxLines[0].TaxAmount != "1.49" {
		t.Errorf("tax: want 1.49 (8.25%% of 18.00) got %s", line.TaxLines[0].TaxAmount)
	}
	if line.LineTotal != "19.49" {
		t.Errorf("line total: want 19.49 got %s", line.LineTotal)
	}
	if resp.CartTotal != "19.49" {
		t.Errorf("cart: want 19.49 got %s", resp.CartTotal)
	}
}

func TestIntegration_BasePriceLookup(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	fx := seedPricingFixtures(t, ctx, pool)
	defer fx.cleanup()

	store := NewPgxStore(pool)
	handler := New(NewResolver(store, nil), store, zap.NewNop())
	r := chi.NewRouter()
	handler.Mount(r)

	url := "/v1/pricing/items/" + fx.itemID.String() + "/base?tenant_id=" + fx.tenantID.String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp BasePriceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Amount != "20.0000" && resp.Amount != "20.00" {
		// pgx renders numeric(14,4) — accept either trimmed or full form.
		t.Errorf("amount: got %q want 20.0000 or 20.00", resp.Amount)
	}
}

func TestIntegration_ListPromotions(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	fx := seedPricingFixtures(t, ctx, pool)
	defer fx.cleanup()

	store := NewPgxStore(pool)
	handler := New(NewResolver(store, nil), store, zap.NewNop())
	r := chi.NewRouter()
	handler.Mount(r)

	url := "/v1/pricing/promotions?tenant_id=" + fx.tenantID.String() +
		"&location_id=" + fx.locationID.String() +
		"&active_at=" + time.Now().UTC().Format(time.RFC3339)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp PromotionsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Promotions) < 1 {
		t.Fatalf("want >= 1 promo, got %d", len(resp.Promotions))
	}
}

func TestIntegration_ListTaxRates(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	fx := seedPricingFixtures(t, ctx, pool)
	defer fx.cleanup()

	store := NewPgxStore(pool)
	handler := New(NewResolver(store, nil), store, zap.NewNop())
	r := chi.NewRouter()
	handler.Mount(r)

	url := "/v1/pricing/tax-rates?tenant_id=" + fx.tenantID.String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp TaxRatesListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.TaxRates) < 1 {
		t.Fatalf("want >= 1 tax rate, got %d", len(resp.TaxRates))
	}
}

// strconv import-keeper — silences unused imports if a future edit drops
// an Itoa call.
var _ = strconv.Itoa
