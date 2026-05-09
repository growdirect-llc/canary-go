//go:build integration

// Cross-tenant negative-test suite for billing. Closes the billing
// slice of CK2 (GRO-919): the GRO-905 / GRO-916 sweep missed billing
// but it used the same caller-supplied ?tenant_id= pattern. Tenant
// scope is now derived from API-key claims; these tests assert the
// fix at two layers:
//
//  1. Store layer — Store.GetBudget with the wrong tenant returns
//     ErrNotFound (no leaky 200 + body, no distinguishable mismatch
//     sentinel that would let an attacker probe for existence).
//  2. HTTP layer — GET /v1/billing/otb/{id} with tenant B's claims
//     against tenant A's budget returns 404. Tenant A still gets
//     200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/billing/...

package billing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/testutil"
)

// dbPool returns a pool against DATABASE_URL or skips. Mirrors the
// fox / casemgmt helpers — these tests follow the make
// test-integration convention.
func dbPool(t *testing.T) *pgxpool.Pool {
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

// seedBudget inserts a minimal ledger.l402_otb_budgets row for the
// given tenant and registers cleanup. Returns the budget id.
func seedBudget(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	budgetID := uuid.New()
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(30 * 24 * time.Hour)
	if _, err := pool.Exec(ctx,
		`INSERT INTO ledger.l402_otb_budgets
		   (id, tenant_id, budget_period, scope_type,
		    budget_satoshis, hard_limit, status)
		 VALUES ($1, $2, tstzrange($3, $4, '[)'), 'tenant_total',
		         100000, true, 'active')`,
		budgetID, tenantID, start, end); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger.l402_otb_budgets WHERE id = $1`, budgetID)
	})
	return budgetID
}

// TestStore_GetBudget_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's budget at the SQL layer.
func TestStore_GetBudget_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)

	budgetID := seedBudget(t, ctx, pool, tenantA)
	store := NewStore(pool)

	if _, err := store.GetBudget(ctx, tenantB, budgetID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenantB GetBudget: want ErrNotFound, got %v", err)
	}
	got, err := store.GetBudget(ctx, tenantA, budgetID)
	if err != nil {
		t.Fatalf("tenantA GetBudget: %v", err)
	}
	if got.ID != budgetID {
		t.Errorf("got id=%v, want %v", got.ID, budgetID)
	}
	if got.TenantID != tenantA {
		t.Errorf("got tenant_id=%v, want %v", got.TenantID, tenantA)
	}
}

// TestHandler_GetBudget_CrossTenant_404 — HTTP-level tenant scoping.
// Tenant B authenticates, requests tenant A's budget via
// GET /v1/billing/otb/{id}, must receive 404 (no existence leak — must
// not be 200, 403, or 500). Tenant A still gets 200.
func TestHandler_GetBudget_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)
	budgetID := seedBudget(t, ctx, pool, tenantA)

	h := New(NewStore(pool), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// tenantB tries to read tenantA's budget → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet, "/v1/billing/otb/"+budgetID.String(), nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet, "/v1/billing/otb/"+budgetID.String(), nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}
