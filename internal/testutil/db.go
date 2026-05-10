// internal/testutil/db.go
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/identity"
)

// MustConnect connects to the test database from DATABASE_URL env var.
// Calls t.Fatal if connection fails. Returns the pool; caller should defer pool.Close().
func MustConnect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("testutil: DATABASE_URL not set — run tests via 'make test'")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("testutil: connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("testutil: ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TruncateTables truncates the given schema-qualified tables within a transaction.
// Use in test setup to ensure a clean state. Does NOT truncate fox_evidence (append-only).
func TruncateTables(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	ctx := context.Background()
	for _, table := range tables {
		if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE"); err != nil {
			t.Fatalf("testutil: truncate %s: %v", table, err)
		}
	}
}

// SeedTenant inserts a fresh organization + tenant pair and returns the
// tenant id. Cleanup is delegated to test-level TRUNCATE; callers that
// need narrower cleanup should DELETE app.tenants + app.organizations
// themselves.
func SeedTenant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	pool := MustConnect(t)
	orgID := uuid.New()
	tenantID := uuid.New()
	short := tenantID.String()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "testutil seed org "+short); err != nil {
		t.Fatalf("testutil: seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID, "tu-"+short, "testutil tenant "+short,
		"tenant_testutil_"+short); err != nil {
		t.Fatalf("testutil: seed tenant: %v", err)
	}
	return tenantID
}

// WithAPIKeyClaims returns a context carrying a minimal API-key Claims
// record for tenantID with a generous scope set covering every read +
// write + admin scope across the platform. Use this for handler tests
// that exercise tenant-scoping without standing up the APIKeyMiddleware
// DB lookup; the broad scope grant means RequireScopeMiddleware never
// 403s these test requests, isolating the unit under test from
// scope-enforcement concerns.
//
// Tests that want to assert scope-enforcement behavior should call
// WithAPIKeyClaimsScoped (below) and pass the exact scope set under
// test (e.g. only the read scope when probing a 403 on a write route).
func WithAPIKeyClaims(ctx context.Context, tenantID uuid.UUID) context.Context {
	return WithAPIKeyClaimsScoped(ctx, tenantID, allTestScopes()...)
}

// WithAPIKeyClaimsScoped returns a context carrying API-key Claims for
// tenantID with exactly the named scopes. Use to exercise scope
// enforcement (e.g. inject a read-only key, hit a write route, assert
// 403 insufficient_scope).
func WithAPIKeyClaimsScoped(ctx context.Context, tenantID uuid.UUID, scopes ...string) context.Context {
	return identity.InjectClaims(ctx, identity.Claims{
		TenantID:   tenantID,
		AgentName:  "test-agent",
		AuthMethod: identity.AuthMethodAPIKey,
		Scopes:     append([]string(nil), scopes...),
	})
}

// allTestScopes returns the union of every scope constant defined in
// internal/identity/scopes.go. WithAPIKeyClaims grants this set so
// existing handler tests (predating GRO-906) keep passing once the
// scope middleware lands.
func allTestScopes() []string {
	return []string{
		// Per-resource read/write scopes added in GRO-906.
		identity.ScopeTransactionRead, identity.ScopeTransactionWrite,
		identity.ScopeCustomerRead, identity.ScopeCustomerWrite,
		identity.ScopeEmployeeRead, identity.ScopeEmployeeWrite,
		identity.ScopeAssetRead, identity.ScopeAssetWrite,
		identity.ScopeAnalyticsRead, identity.ScopeAnalyticsWrite,
		identity.ScopeReturnsRead, identity.ScopeReturnsWrite,
		identity.ScopeReportRead, identity.ScopeReportWrite,
		identity.ScopeOwlRead, identity.ScopeOwlWrite,
		identity.ScopeAlertRead, identity.ScopeAlertWrite,
		identity.ScopeTaskRead, identity.ScopeTaskWrite,
		identity.ScopeBillingRead, identity.ScopeBillingWrite,
		// Pre-existing scopes preserved verbatim.
		identity.ScopeCaseRead, identity.ScopeCaseWrite,
		identity.ScopeDLQRead, identity.ScopeDLQReplay,
		identity.ScopeEvidenceRead, identity.ScopeEvidenceWrite,
		identity.ScopeWebhookWrite, identity.ScopeWebhookBP, identity.ScopeWebhookIdem,
		identity.ScopeGatewayNonce, identity.ScopeProtocolEvents,
		identity.ScopeIdentityMe, identity.ScopeIdentityAdmin,
		identity.ScopeLedgerRead,
		identity.ScopeInventoryReplenish,
	}
}
