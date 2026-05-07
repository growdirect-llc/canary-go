//go:build integration

// Integration smoke for the item service. Hits a real Postgres with the
// canonical schema applied. Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/item/...
//
// The test seeds an organization → tenant → merchant → category → vendor
// → item → 2 barcodes (UPC primary, EAN secondary), exercises every
// public path, then cleans up. Idempotent with cleanup so reruns work.
package item

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	upcBarcode = "012345678905"
	eanBarcode = "5012345678900"
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	url := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return url
}

// seed inserts the org / tenant / merchant / category / vendor / item /
// 2 barcodes used by every test below. Returns the tenant_id, item_id,
// and a cleanup func.
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (
	tenantID, itemID, categoryID, vendorID uuid.UUID, cleanup func(),
) {
	t.Helper()

	orgID := uuid.New()
	tenantID = uuid.New()
	merchantID := uuid.New()
	categoryID = uuid.New()
	vendorID = uuid.New()
	itemID = uuid.New()
	upcID := uuid.New()
	eanID := uuid.New()

	mustExec := func(q string, args ...any) {
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed exec %q: %v", q, err)
		}
	}

	mustExec(`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-761 Item Integration Org")

	mustExec(`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		"item-int-"+tenantID.String()[:8],
		"Item Integration Tenant",
		"tenant_item_int_"+strings.ReplaceAll(tenantID.String()[:8], "-", ""))

	mustExec(`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
		VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID,
		"item-int-merch-"+merchantID.String()[:8],
		"Item Integration Merchant")

	mustExec(`INSERT INTO catalog.product_categories
		(id, tenant_id, code, name, level, status)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		categoryID, tenantID, "CAT-001", "Test Category", 0, "active")

	mustExec(`INSERT INTO catalog.vendors
		(id, tenant_id, vendor_code, name, vendor_type, status)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		vendorID, tenantID, "VND-001", "Test Vendor Inc", "supplier", "active")

	mustExec(`INSERT INTO catalog.items
		(id, tenant_id, sku, description, short_description, item_type, category_id,
		 unit_of_measure, uom_quantity, default_price, default_cost, default_currency,
		 food_stamp_eligible, weighable, attributes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10::numeric, $11::numeric,
		        $12, $13, $14, $15, $16)`,
		itemID, tenantID, "INT-SKU-001", "Integration Test Widget",
		"Int Widget", "standard", categoryID, "EA", "1", "9.99", "5.50", "USD",
		false, false, []byte(`{"shelf":"A1"}`), "active")

	mustExec(`INSERT INTO catalog.item_vendors
		(tenant_id, item_id, vendor_id, vendor_sku, unit_cost, is_primary, status)
		VALUES ($1, $2, $3, $4, $5::numeric, $6, $7)`,
		tenantID, itemID, vendorID, "VND-INT-001", "5.50", true, "active")

	mustExec(`INSERT INTO catalog.item_barcodes
		(id, tenant_id, item_id, barcode, barcode_type, uom_quantity, is_primary, status)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8)`,
		upcID, tenantID, itemID, upcBarcode, "UPC_A", "1", true, "active")

	mustExec(`INSERT INTO catalog.item_barcodes
		(id, tenant_id, item_id, barcode, barcode_type, uom_quantity, is_primary, status)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8)`,
		eanID, tenantID, itemID, eanBarcode, "EAN_13", "1", false, "active")

	cleanup = func() {
		// Tear down in dependency order. Best-effort — ignore errors so
		// a partial failure in one test doesn't poison the next.
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.item_barcodes WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.item_vendors WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.items WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.vendors WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.product_categories WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return
}

// newRouter builds a router wired to PgxStore.
func newRouter(t *testing.T, pool *pgxpool.Pool) *chi.Mux {
	t.Helper()
	store := NewPgxStore(pool)
	h := New(store, nil)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func TestIntegration_FullCRUDPath(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pg ping: %v", err)
	}

	tenantID, itemID, categoryID, vendorID, cleanup := seed(t, ctx, pool)
	defer cleanup()

	r := newRouter(t, pool)

	// 1. GET by ID
	t.Run("GET by id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items/"+itemID.String()+"?tenant_id="+tenantID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"sku":"INT-SKU-001"`) {
			t.Errorf("missing sku; body=%s", rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), upcBarcode) {
			t.Errorf("missing upc barcode in body; got=%s", rec.Body.String())
		}
	})

	// 2. GET by SKU
	t.Run("GET by sku", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items?tenant_id="+tenantID.String()+"&sku=INT-SKU-001", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
	})

	// 3. GET by barcode (UPC primary)
	t.Run("GET by barcode UPC", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items/by-barcode?tenant_id="+tenantID.String()+"&barcode="+upcBarcode, nil)
		rec := httptest.NewRecorder()
		start := time.Now()
		r.ServeHTTP(rec, req)
		elapsed := time.Since(start)
		t.Logf("by-barcode UPC: %s", elapsed)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
	})

	// 4. GET by barcode (EAN secondary) — must resolve to same item
	t.Run("GET by barcode EAN", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items/by-barcode?tenant_id="+tenantID.String()+"&barcode="+eanBarcode, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), itemID.String()) {
			t.Errorf("EAN didn't resolve to seeded item; body=%s", rec.Body.String())
		}
	})

	// 5. POST — create a new item with one barcode
	var createdID uuid.UUID
	t.Run("POST create", func(t *testing.T) {
		body := `{
			"tenant_id": "` + tenantID.String() + `",
			"sku": "INT-SKU-NEW",
			"description": "New Integration Item",
			"category_id": "` + categoryID.String() + `",
			"default_price": "12.50",
			"barcodes": [{"value": "999000000001", "type": "INTERNAL", "is_primary": true}]
		}`
		req := httptest.NewRequest(http.MethodPost, "/v1/items",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		// Pluck the id out of the body for the next subtests + cleanup
		var resp Item
		if err := decodeBody(rec, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		createdID = resp.ID
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM catalog.item_barcodes WHERE item_id = $1`, createdID)
			_, _ = pool.Exec(ctx, `DELETE FROM catalog.items WHERE id = $1`, createdID)
		})
		if resp.SKU != "INT-SKU-NEW" {
			t.Errorf("sku: got %q", resp.SKU)
		}
		if len(resp.Barcodes) != 1 {
			t.Errorf("barcodes: got %d want 1", len(resp.Barcodes))
		}
	})

	// 6. PATCH — update the description on the originally-seeded item
	t.Run("PATCH update", func(t *testing.T) {
		body := `{"description": "PATCHED Description"}`
		req := httptest.NewRequest(http.MethodPatch,
			"/v1/items/"+itemID.String()+"?tenant_id="+tenantID.String(),
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"description":"PATCHED Description"`) {
			t.Errorf("description not updated; body=%s", rec.Body.String())
		}
	})

	// 7. List with category filter
	t.Run("GET list with category filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items?tenant_id="+tenantID.String()+"&category_id="+categoryID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		// Should include both seeded item and createdID
		if !strings.Contains(rec.Body.String(), itemID.String()) {
			t.Errorf("seeded item missing from list; body=%s", rec.Body.String())
		}
	})

	// 8. List with vendor filter (only seeded item is linked to vendor)
	t.Run("GET list with vendor filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/items?tenant_id="+tenantID.String()+"&vendor_id="+vendorID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), itemID.String()) {
			t.Errorf("vendor-linked item missing; body=%s", rec.Body.String())
		}
	})

	// 9. List categories
	t.Run("GET list categories", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/categories?tenant_id="+tenantID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"CAT-001"`) {
			t.Errorf("category missing; body=%s", rec.Body.String())
		}
	})

	// 10. List vendors
	t.Run("GET list vendors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/vendors?tenant_id="+tenantID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"vendor_code":"VND-001"`) {
			t.Errorf("vendor missing; body=%s", rec.Body.String())
		}
	})

	// 11. DELETE — soft-delete the seeded item
	t.Run("DELETE soft", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete,
			"/v1/items/"+itemID.String()+"?tenant_id="+tenantID.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
		}
		// Verify status flipped, row not gone
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM catalog.items WHERE id = $1`, itemID).Scan(&status); err != nil {
			t.Fatalf("status check: %v", err)
		}
		if status != "inactive" {
			t.Errorf("status: got %q want inactive", status)
		}
	})
}

// decodeBody is a tiny helper to read a JSON response body.
func decodeBody(rec *httptest.ResponseRecorder, v any) error {
	return json.NewDecoder(rec.Body).Decode(v)
}
