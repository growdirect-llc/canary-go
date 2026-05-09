//go:build integration

// Cross-tenant negative-test suite for the inventory service. Closes
// the inventory slice of GRO-905 (services trust caller-supplied
// tenant_id) and the inventory slice of GRO-916 (cross-tenant negative
// test coverage).
//
// We assert at two layers:
//
//  1. Store layer — store.GetPosition with the wrong tenant returns
//     ErrPositionNotFound. No leaky 200, no distinguishable
//     ErrTenantMismatch sentinel that would let an attacker probe for
//     existence.
//  2. HTTP layer — GET /v1/inventory/positions/{item}/{location} with
//     tenant B's claims against tenant A's row returns 404. Tenant A
//     still gets 200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/inventory/...

package inventory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/testutil"
)

// xtDBPool returns a pool against DATABASE_URL or skips. We don't
// reuse the existing skipIfNoDB helper — that one reads the legacy
// GATEWAY_TEST_DATABASE_URL env var. These tests follow the make
// test-integration convention (DATABASE_URL).
func xtDBPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedPosition inserts a merchant + item + location + position row
// for the given tenant via direct SQL and registers cleanup. Returns
// the (item, location) pair the position is keyed on.
//
// Pattern mirrors seedFixtures in integration_test.go but is scoped
// to the testutil.SeedTenant flow used by cross-tenant tests.
func seedPosition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (itemID, locationID uuid.UUID) {
	t.Helper()
	merchantID := uuid.New()
	itemID = uuid.New()
	locationID = uuid.New()
	positionID := uuid.New()
	short := strings.ReplaceAll(itemID.String()[:8], "-", "")

	// SeedTenant already created the org+tenant; we need to look up
	// the org_id to satisfy the merchants FK.
	var orgID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT organization_id FROM app.tenants WHERE id = $1`, tenantID).
		Scan(&orgID); err != nil {
		t.Fatalf("lookup org: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID,
		"xt-merchant-"+short,
		"cross-tenant test merchant"); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO catalog.items (id, tenant_id, sku, description)
		 VALUES ($1, $2, $3, $4)`,
		itemID, tenantID, "XT-INV-SKU-"+short, "cross-tenant test item"); err != nil {
		t.Fatalf("seed item: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO location.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locationID, tenantID, "XT-INV-LOC-"+short, "cross-tenant test location"); err != nil {
		t.Fatalf("seed location: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO inventory.inventory_positions
		    (id, tenant_id, item_id, location_id, zone_id,
		     on_hand_quantity, reserved_quantity, on_order_quantity,
		     in_transit_quantity, status)
		 VALUES ($1, $2, $3, $4, NULL,
		         42::numeric, 0::numeric, 0::numeric,
		         0::numeric, 'active')`,
		positionID, tenantID, itemID, locationID); err != nil {
		t.Fatalf("seed position: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM inventory.inventory_movements WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM inventory.inventory_positions WHERE id = $1`, positionID)
		_, _ = pool.Exec(ctx, `DELETE FROM location.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.items WHERE id = $1`, itemID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
	})
	return itemID, locationID
}

// TestStore_GetPosition_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's position at the SQL layer.
func TestStore_GetPosition_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)

	itemID, locationID := seedPosition(t, ctx, pool, tenantA)
	store := NewStore(pool)

	if _, err := store.GetPosition(ctx, tenantB, itemID, locationID); !errors.Is(err, ErrPositionNotFound) {
		t.Errorf("tenantB GetPosition: want ErrPositionNotFound, got %v", err)
	}
	got, err := store.GetPosition(ctx, tenantA, itemID, locationID)
	if err != nil {
		t.Fatalf("tenantA GetPosition: %v", err)
	}
	if got.ItemID != itemID || got.LocationID != locationID {
		t.Errorf("got item=%v loc=%v, want %v / %v",
			got.ItemID, got.LocationID, itemID, locationID)
	}
}

// TestHandler_GetPosition_CrossTenant_404 — HTTP-level tenant scoping.
// Per the spec for GRO-905: tenant B authenticates, requests tenant
// A's position, must receive 404 (no existence leak — must not be
// 200, 403, or 500). Tenant A still gets 200.
func TestHandler_GetPosition_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)
	itemID, locationID := seedPosition(t, ctx, pool, tenantA)

	store := NewStore(pool)
	h := New(store, store, zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)

	url := "/v1/inventory/positions/" + itemID.String() + "/" + locationID.String()

	// tenantB tries to read tenantA's position → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet, url, nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet, url, nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}
