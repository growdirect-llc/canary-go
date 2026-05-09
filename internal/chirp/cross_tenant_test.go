//go:build integration

// Cross-tenant negative-test suite for chirp. Closes the chirp slice
// of GRO-905 (services trust caller-supplied tenant_id) and the chirp
// slice of GRO-916 (cross-tenant negative test coverage).
//
// We assert at two layers:
//
//  1. Store layer — store.ListRules with tenant B's id returns zero
//     rows when the only seeded rule belongs to tenant A.
//  2. HTTP layer — GET /v1/chirp/rules with tenant B's claims against
//     a tenant-A-only fixture returns 200 with count=0; tenant A
//     sees its rule.
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/chirp/...

package chirp

import (
	"bytes"
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
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/testutil"
)

// dbPool returns a pool against DATABASE_URL or skips. We don't reuse
// the existing skipIfNoIntegration helper because it reads the legacy
// GATEWAY_TEST_DATABASE_URL env var — these tests follow the
// make test-integration convention (DATABASE_URL).
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

// seedRule inserts a minimal detection.detection_rules row for the
// given tenant and registers cleanup. Returns the rule id.
func seedRule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	ruleID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO detection.detection_rules
		    (id, tenant_id, rule_code, name, rule_category, rule_definition, severity)
		 VALUES ($1, $2, $3, $4, 'shrink', '{}', 'high')`,
		ruleID, tenantID, "xt-"+ruleID.String()[:8], "cross-tenant test rule"); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM detection.detection_rules WHERE id = $1`, ruleID)
	})
	return ruleID
}

// seedLocation inserts a minimal location.locations row for the given
// tenant (transaction.transactions.location_id is a NOT NULL FK).
func seedLocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	locID := uuid.New()
	short := strings.ReplaceAll(locID.String()[:8], "-", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO location.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locID, tenantID, "LOC-XT-"+short, "chirp cross-tenant test location"); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM location.locations WHERE id = $1`, locID)
	})
	return locID
}

// seedTransaction inserts a minimal transaction.transactions header
// for the given tenant + location and registers cleanup.
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

// TestHandler_Evaluate_CrossTenant_404 — POST /v1/chirp/evaluate with
// a transaction belonging to tenantA, while authenticated as tenantB,
// must return 404 (no existence leak). The engine's
// EvaluateTransaction now refuses cross-tenant invocation; the
// handler maps ErrTransactionNotFound to 404.
func TestHandler_Evaluate_CrossTenant_404(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)

	locA := seedLocation(t, ctx, pool, tenantA)
	txID := seedTransaction(t, ctx, pool, tenantA, locA)

	store := NewPgxStore(pool)
	engine := NewEngine(store, NewRegistry(), zap.NewNop())
	h := NewHandler(engine, store, zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)

	body, _ := json.Marshal(map[string]string{"transaction_id": txID.String()})

	// tenantB submits tenantA's tx → 404, no existence leak.
	reqB := httptest.NewRequest(http.MethodPost, "/v1/chirp/evaluate", bytes.NewReader(body))
	reqB.Header.Set("Content-Type", "application/json")
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Errorf("cross-tenant evaluate: got %d, want 404; body=%s",
			recB.Code, recB.Body.String())
	}

	// tenantA submits its own tx → 200 (no rules registered, but the
	// route succeeds and returns an empty detection list).
	reqA := httptest.NewRequest(http.MethodPost, "/v1/chirp/evaluate", bytes.NewReader(body))
	reqA.Header.Set("Content-Type", "application/json")
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Errorf("same-tenant evaluate: got %d, want 200; body=%s",
			recA.Code, recA.Body.String())
	}
}

// TestStore_ListRules_TenantIsolation — store-level tenant scoping.
// Tenant B's id must not match tenant A's rule at the SQL layer.
func TestStore_ListRules_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)

	_ = seedRule(t, ctx, pool, tenantA)
	store := NewPgxStore(pool)

	gotB, err := store.ListRules(ctx, tenantB)
	if err != nil {
		t.Fatalf("tenantB ListRules: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("tenantB ListRules: got %d rules, want 0 (cross-tenant leak)", len(gotB))
	}

	gotA, err := store.ListRules(ctx, tenantA)
	if err != nil {
		t.Fatalf("tenantA ListRules: %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("tenantA ListRules: got %d rules, want 1", len(gotA))
	}
}

// TestHandler_ListRules_CrossTenant_Empty — HTTP-level tenant scoping.
// Per the spec for GRO-905: tenant B authenticates, hits
// GET /v1/chirp/rules, must receive 200 with count=0 — its claims
// are the source of truth, the rule belongs to tenant A. tenant A
// sees its rule.
func TestHandler_ListRules_CrossTenant_Empty(t *testing.T) {
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, ctx)
	tenantB := testutil.SeedTenant(t, ctx)
	pool := dbPool(t)
	_ = seedRule(t, ctx, pool, tenantA)

	store := NewPgxStore(pool)
	engine := NewEngine(store, NewRegistry(), zap.NewNop())
	h := NewHandler(engine, store, zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)

	type listResp struct {
		Count int `json:"count"`
	}

	// tenantB tries to list rules → 200 with count=0.
	reqB := httptest.NewRequest(http.MethodGet, "/v1/chirp/rules", nil)
	reqB = reqB.WithContext(testutil.WithAPIKeyClaims(reqB.Context(), tenantB))
	recB := httptest.NewRecorder()
	r.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("cross-tenant list: got %d, want 200; body=%s", recB.Code, recB.Body.String())
	}
	var bodyB listResp
	if err := json.Unmarshal(recB.Body.Bytes(), &bodyB); err != nil {
		t.Fatalf("decode tenantB body: %v", err)
	}
	if bodyB.Count != 0 {
		t.Errorf("cross-tenant list: got count=%d, want 0", bodyB.Count)
	}

	// tenantA can list its own rule.
	reqA := httptest.NewRequest(http.MethodGet, "/v1/chirp/rules", nil)
	reqA = reqA.WithContext(testutil.WithAPIKeyClaims(reqA.Context(), tenantA))
	recA := httptest.NewRecorder()
	r.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("same-tenant list: got %d, want 200; body=%s", recA.Code, recA.Body.String())
	}
	var bodyA listResp
	if err := json.Unmarshal(recA.Body.Bytes(), &bodyA); err != nil {
		t.Fatalf("decode tenantA body: %v", err)
	}
	if bodyA.Count != 1 {
		t.Errorf("same-tenant list: got count=%d, want 1", bodyA.Count)
	}
}
