//go:build integration

// Cross-tenant negative-test suite for casemgmt. Closes the casemgmt
// slice of CK2 (GRO-919): the GRO-905 / GRO-916 sweep missed casemgmt
// but it used the same caller-supplied ?tenant_id= pattern. Tenant
// scope is now derived from API-key claims; these tests assert the
// fix at two layers:
//
//  1. Store layer — Store.GetCase with the wrong tenant returns
//     ErrNotFound (no leaky 200 + body, no distinguishable mismatch
//     sentinel that would let an attacker probe for existence).
//  2. HTTP layer — GET /v1/cases/{id} with tenant B's claims against
//     tenant A's case returns 404. Tenant A still gets 200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/casemgmt/...

package casemgmt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/testutil"
)

// dbPool returns a pool against DATABASE_URL or skips. Mirrors the fox
// helper — these tests follow the make test-integration convention.
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

// seedCase inserts a minimal detection.cases row for the given tenant
// and registers cleanup. Returns the case id.
func seedCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	caseID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO detection.cases (id, tenant_id, case_number, case_type, title, severity, status)
		 VALUES ($1, $2, $3, 'investigation', 'cross-tenant test', 'high', 'open')`,
		caseID, tenantID, "C-XT-"+caseID.String()[:8]); err != nil {
		t.Fatalf("seed case: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detection.cases WHERE id = $1`, caseID)
	})
	return caseID
}

// TestStore_GetCase_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's case at the SQL layer.
func TestStore_GetCase_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)

	caseID := seedCase(t, ctx, pool, tenantA)
	store := NewStore(pool)

	if _, err := store.GetCase(ctx, tenantB, caseID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenantB GetCase: want ErrNotFound, got %v", err)
	}
	got, err := store.GetCase(ctx, tenantA, caseID)
	if err != nil {
		t.Fatalf("tenantA GetCase: %v", err)
	}
	if got.ID != caseID {
		t.Errorf("got id=%v, want %v", got.ID, caseID)
	}
	if got.TenantID != tenantA {
		t.Errorf("got tenant_id=%v, want %v", got.TenantID, tenantA)
	}
}

// TestHandler_GetCase_CrossTenant_404 — HTTP-level tenant scoping.
// Tenant B authenticates, requests tenant A's case via
// GET /v1/cases/{id}, must receive 404 (no existence leak — must not
// be 200, 403, or 500). Tenant A still gets 200.
func TestHandler_GetCase_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)
	caseID := seedCase(t, ctx, pool, tenantA)

	h := New(NewStore(pool), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// tenantB tries to read tenantA's case → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet, "/v1/cases/"+caseID.String(), nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet, "/v1/cases/"+caseID.String(), nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}
