//go:build integration

// Cross-tenant negative-test suite for the transaction service. Closes
// the transaction slice of GRO-905 (services trust caller-supplied
// tenant_id) and the transaction slice of GRO-916 (cross-tenant
// negative test coverage).
//
// We assert at two layers:
//
//  1. Store layer — store.GetByID with the wrong tenant returns
//     ErrNotFound (not a leaky 200 + body, not a distinguishable
//     ErrTenantMismatch sentinel that would let an attacker probe for
//     existence).
//  2. HTTP layer — GET /v1/transactions/{id} with tenant B's claims
//     against tenant A's transaction returns 404. tenant A still
//     gets 200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/transaction/...

package transaction

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/testutil"
)

// xtDBPool returns a pool against DATABASE_URL or skips. Mirrors the
// fox / item / inventory cross-tenant test helpers (DATABASE_URL is
// the make test-integration convention).
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

// seedLocation inserts a minimal location.locations row for the given
// tenant (transaction.transactions.location_id is a NOT NULL FK).
// Returns the location id.
func seedLocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	locID := uuid.New()
	short := strings.ReplaceAll(locID.String()[:8], "-", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO location.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locID, tenantID, "LOC-XT-"+short, "cross-tenant test location"); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM location.locations WHERE id = $1`, locID)
	})
	return locID
}

// seedTransaction inserts a minimal transaction.transactions header
// for the given tenant + location and registers cleanup. Returns the
// transaction id. Children are not seeded — the cross-tenant assertion
// only exercises the header read path.
func seedTransaction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, locationID uuid.UUID) uuid.UUID {
	t.Helper()
	txID := uuid.New()
	short := strings.ReplaceAll(txID.String()[:8], "-", "")
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO transaction.transactions
		    (id, tenant_id, transaction_number, location_id,
		     business_date, started_at, ended_at)
		 VALUES ($1, $2, $3, $4, $5::date, $6, $7)`,
		txID, tenantID, "T-XT-"+short, locationID,
		now.Format("2006-01-02"), now, now); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM transaction.transactions WHERE id = $1`, txID)
	})
	return txID
}

// TestStore_GetByID_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's transaction at the SQL layer.
func TestStore_GetByID_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)

	locA := seedLocation(t, ctx, pool, tenantA)
	txID := seedTransaction(t, ctx, pool, tenantA, locA)
	store := NewStore(pool)

	if _, err := store.GetByID(ctx, tenantB, txID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenantB GetByID: want ErrNotFound, got %v", err)
	}
	got, err := store.GetByID(ctx, tenantA, txID)
	if err != nil {
		t.Fatalf("tenantA GetByID: %v", err)
	}
	if got.ID != txID {
		t.Errorf("got id=%v, want %v", got.ID, txID)
	}
}

// TestHandler_GetByID_CrossTenant_404 — HTTP-level tenant scoping.
// Per the spec for GRO-905: tenant B authenticates, requests tenant
// A's transaction via GET /v1/transactions/{id}, must receive 404
// (no existence leak — must not be 200, 403, or 500). Tenant A still
// gets 200.
func TestHandler_GetByID_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := xtDBPool(t)
	locA := seedLocation(t, ctx, pool, tenantA)
	txID := seedTransaction(t, ctx, pool, tenantA, locA)

	h := New(NewStore(pool), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// tenantB tries to read tenantA's transaction → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet, "/v1/transactions/"+txID.String(), nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet, "/v1/transactions/"+txID.String(), nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
}
