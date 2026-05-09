//go:build integration

// Cross-tenant negative-test suite for the item service. Closes the
// item slice of GRO-905 (services trust caller-supplied tenant_id) and
// the item slice of GRO-916 (cross-tenant negative test coverage).
//
// We assert at two layers:
//
//  1. Store layer — store.GetByID with the wrong tenant returns
//     ErrNotFound (not a leaky 200 + body, not a distinguishable
//     ErrTenantMismatch sentinel that would let an attacker probe for
//     existence).
//  2. HTTP layer — GET /v1/items/{id} with tenant B's claims against
//     tenant A's item returns 404. tenant A still gets 200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/item/...

package item

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
// reuse the existing skipIfNoIntegration helper because it reads the
// legacy GATEWAY_TEST_DATABASE_URL env var — these tests follow the
// make test-integration convention (DATABASE_URL).
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

// seedItem inserts a minimal catalog.items row (plus the category
// row that catalog.items.category_id can FK to — null is allowed but
// we exercise the realistic path) for the given tenant and registers
// cleanup. Returns the item id.
func seedItem(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	categoryID := uuid.New()
	itemID := uuid.New()
	short := strings.ReplaceAll(itemID.String()[:8], "-", "")

	if _, err := pool.Exec(ctx,
		`INSERT INTO catalog.product_categories
		    (id, tenant_id, code, name, level, status)
		 VALUES ($1, $2, $3, $4, 0, 'active')`,
		categoryID, tenantID, "CAT-XT-"+short, "cross-tenant test cat"); err != nil {
		t.Fatalf("seed category: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO catalog.items
		    (id, tenant_id, sku, description, item_type, category_id,
		     unit_of_measure, uom_quantity, default_currency,
		     food_stamp_eligible, weighable, attributes, status)
		 VALUES ($1, $2, $3, $4, 'standard', $5,
		         'EA', 1, 'USD',
		         false, false, '{}', 'active')`,
		itemID, tenantID, "XT-SKU-"+short, "cross-tenant test item", categoryID); err != nil {
		t.Fatalf("seed item: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.item_barcodes WHERE item_id = $1`, itemID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.items WHERE id = $1`, itemID)
		_, _ = pool.Exec(ctx, `DELETE FROM catalog.product_categories WHERE id = $1`, categoryID)
	})
	return itemID
}

// TestStore_GetByID_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's item at the SQL layer.
func TestStore_GetByID_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)

	itemID := seedItem(t, ctx, pool, tenantA)
	store := NewPgxStore(pool)

	if _, err := store.GetByID(ctx, tenantB, itemID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenantB GetByID: want ErrNotFound, got %v", err)
	}
	got, err := store.GetByID(ctx, tenantA, itemID)
	if err != nil {
		t.Fatalf("tenantA GetByID: %v", err)
	}
	if got.ID != itemID {
		t.Errorf("got id=%v, want %v", got.ID, itemID)
	}
}

// TestHandler_GetByID_CrossTenant_404 — HTTP-level tenant scoping.
// Per the spec for GRO-905: tenant B authenticates, requests tenant
// A's item via GET /v1/items/{id}, must receive 404 (no existence
// leak — must not be 200, 403, or 500). Tenant A still gets 200.
func TestHandler_GetByID_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)
	itemID := seedItem(t, ctx, pool, tenantA)

	h := New(NewPgxStore(pool), zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)

	// tenantB tries to read tenantA's item → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet, "/v1/items/"+itemID.String(), nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet, "/v1/items/"+itemID.String(), nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}
