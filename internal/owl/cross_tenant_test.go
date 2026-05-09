//go:build integration

// Cross-tenant negative-test suite for owl. Closes the owl slice of
// CK2 (GRO-919): owl was missed by the GRO-905 / GRO-916 sweep but
// used the same caller-supplied ?tenant_id= pattern. Tenant scope is
// now derived from API-key claims; these tests assert the fix at two
// layers:
//
//  1. Store layer — DashboardStore.GetPartyRFM with the wrong tenant
//     returns ErrNotFound (no leaky 200 + body, no distinguishable
//     mismatch sentinel).
//  2. HTTP layer — GET /v1/owl/parties/{id}/rfm with tenant B's
//     claims against tenant A's party returns 404. Tenant A still
//     gets 200.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/owl/...

package owl

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
	"go.uber.org/zap"

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

// seedParty inserts a minimal active party.parties row for the given
// tenant and refreshes party.decisioning_facts so GetPartyRFM /
// ListPartyRFM see the row. Returns the party id.
func seedParty(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	partyID := uuid.New()
	short := partyID.String()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO party.parties (id, tenant_id, party_code, party_type,
		    display_name, status, confidence)
		 VALUES ($1, $2, $3, 'consumer', $4, 'active', 'anonymous')`,
		partyID, tenantID, "P-XT-"+short, "cross-tenant test "+short); err != nil {
		t.Fatalf("seed party: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM party.parties WHERE id = $1`, partyID)
	})
	// Refresh the MV so decisioning_facts picks up the new row. Caller
	// can refresh again after mutating fixtures; this seed is already
	// usable by GetPartyRFM after one refresh.
	if _, err := pool.Exec(ctx,
		`REFRESH MATERIALIZED VIEW party.decisioning_facts`); err != nil {
		t.Fatalf("refresh decisioning_facts: %v", err)
	}
	return partyID
}

// TestStore_GetPartyRFM_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's party at the SQL layer.
func TestStore_GetPartyRFM_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)

	partyID := seedParty(t, ctx, pool, tenantA)
	store := NewDashboardStore(pool)

	if _, err := store.GetPartyRFM(ctx, tenantB, partyID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenantB GetPartyRFM: want ErrNotFound, got %v", err)
	}
	got, err := store.GetPartyRFM(ctx, tenantA, partyID)
	if err != nil {
		t.Fatalf("tenantA GetPartyRFM: %v", err)
	}
	if got.PartyID != partyID {
		t.Errorf("got party_id=%v, want %v", got.PartyID, partyID)
	}
	if got.TenantID != tenantA {
		t.Errorf("got tenant_id=%v, want %v", got.TenantID, tenantA)
	}
}

// TestHandler_GetPartyRFM_CrossTenant_404 — HTTP-level tenant scoping.
// Tenant B authenticates, requests tenant A's party RFM via
// GET /v1/owl/parties/{id}/rfm, must receive 404 (no existence leak —
// must not be 200, 403, or 500). Tenant A still gets 200.
func TestHandler_GetPartyRFM_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)
	partyID := seedParty(t, ctx, pool, tenantA)

	h := NewDashboardHandler(NewDashboardStore(pool), zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)

	// tenantB tries to read tenantA's party → 404 (no existence leak).
	reqB := httptest.NewRequest(http.MethodGet,
		"/v1/owl/parties/"+partyID.String()+"/rfm", nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA can still read it.
	reqA := httptest.NewRequest(http.MethodGet,
		"/v1/owl/parties/"+partyID.String()+"/rfm", nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant GET: got %d, want 200; body=%s",
			recA.Code, recA.Body.String())
	}
}
