//go:build integration

// Integration smoke test for the inventory store. Exercises the real
// pgx stack against canary_go_test. Run with:
//
//   GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//   go test -tags=integration -v ./internal/inventory/...
//
// Wave 3 coordinator runs this post-merge — Wave 2 subagent does NOT
// run -tags=integration (per dispatch).
package inventory

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

func skipIfNoDB(t *testing.T) string {
	t.Helper()
	url := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return url
}

// seedFixtures creates an organization, tenant, merchant, item, and
// location in canary_go_test and returns IDs + cleanup.
func seedFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, itemID, locationID uuid.UUID, cleanup func()) {
	t.Helper()
	orgID := uuid.New()
	tenantID = uuid.New()
	merchantID := uuid.New()
	itemID = uuid.New()
	locationID = uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-761 Inventory Test Org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		"inv-test-"+tenantID.String()[:8],
		"GRO-761 Inventory Test Tenant",
		"tenant_inv_test_"+strings.ReplaceAll(tenantID.String()[:8], "-", "_"),
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID,
		"inv-test-merchant-"+merchantID.String()[:8],
		"GRO-761 Inventory Test Merchant",
	); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO m.items (id, tenant_id, sku, description)
		 VALUES ($1, $2, $3, $4)`,
		itemID, tenantID,
		"SKU-INV-TEST-"+itemID.String()[:8],
		"Inventory Test SKU",
	); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO l.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locationID, tenantID,
		"LOC-INV-TEST-"+locationID.String()[:8],
		"Inventory Test Location",
	); err != nil {
		t.Fatalf("seed location: %v", err)
	}

	cleanup = func() {
		_, _ = pool.Exec(ctx, `DELETE FROM i.inventory_movements WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM i.inventory_positions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM l.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM m.items WHERE id = $1`, itemID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return
}

func TestIntegration_AppendMovement_ChangesPosition(t *testing.T) {
	dbURL := skipIfNoDB(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	tenantID, itemID, locationID, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	store := NewStore(pool)
	now := time.Now().UTC()

	// Seed initial position via a goods_receipt of 100 units.
	_, pos1, err := store.AppendMovement(ctx, AppendMovementRequest{
		MerchantID:   tenantID,
		ItemID:       itemID,
		LocationID:   locationID,
		MovementType: "goods_receipt",
		Quantity:     "100",
		Attributes:   json.RawMessage(`{"seed":true}`),
	}, now)
	if err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
	if pos1.OnHandQuantity != "100.0000" {
		t.Errorf("after seed: on_hand=%q want 100.0000", pos1.OnHandQuantity)
	}

	// Sale of -5; expect on_hand to drop to 95.
	_, pos2, err := store.AppendMovement(ctx, AppendMovementRequest{
		MerchantID:   tenantID,
		ItemID:       itemID,
		LocationID:   locationID,
		MovementType: "sale",
		Quantity:     "-5",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("sale append: %v", err)
	}
	if pos2.OnHandQuantity != "95.0000" {
		t.Errorf("after sale: on_hand=%q want 95.0000", pos2.OnHandQuantity)
	}

	// Receive +10; expect on_hand to rise to 105.
	_, pos3, err := store.AppendMovement(ctx, AppendMovementRequest{
		MerchantID:   tenantID,
		ItemID:       itemID,
		LocationID:   locationID,
		MovementType: "goods_receipt",
		Quantity:     "10",
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("receipt append: %v", err)
	}
	if pos3.OnHandQuantity != "105.0000" {
		t.Errorf("after receipt: on_hand=%q want 105.0000", pos3.OnHandQuantity)
	}

	// Verify the position read returns the same value.
	posRead, err := store.GetPosition(ctx, tenantID, itemID, locationID)
	if err != nil {
		t.Fatalf("get position: %v", err)
	}
	if posRead.OnHandQuantity != "105.0000" {
		t.Errorf("read on_hand=%q want 105.0000", posRead.OnHandQuantity)
	}

	// Verify three movements are in the audit log.
	movs, err := store.ListMovements(ctx, tenantID, itemID, locationID, nil, nil, 50, 0)
	if err != nil {
		t.Fatalf("list movements: %v", err)
	}
	if len(movs) != 3 {
		t.Errorf("movements: got %d want 3", len(movs))
	}
}

func TestIntegration_HandlerEndToEnd(t *testing.T) {
	dbURL := skipIfNoDB(t)
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	tenantID, itemID, locationID, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	store := NewStore(pool)
	h := New(store, store, nil)
	r := chi.NewRouter()
	h.Mount(r)

	// POST a movement
	body, _ := json.Marshal(AppendMovementRequest{
		MerchantID:   tenantID,
		ItemID:       itemID,
		LocationID:   locationID,
		MovementType: "goods_receipt",
		Quantity:     "50",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/movements",
		strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post movement: got %d body=%s", rec.Code, rec.Body.String())
	}

	// GET the position
	req = httptest.NewRequest(http.MethodGet,
		"/v1/inventory/positions/"+itemID.String()+"/"+locationID.String(), nil)
	req.Header.Set(HeaderMerchant, tenantID.String())
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get position: got %d body=%s", rec.Code, rec.Body.String())
	}
	var pos PositionDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &pos)
	if pos.OnHandQuantity != "50.0000" {
		t.Errorf("on_hand: got %q", pos.OnHandQuantity)
	}
}
