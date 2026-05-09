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
// record for tenantID. Use this to build test requests that exercise
// handler tenant-scoping without standing up the APIKeyMiddleware DB
// lookup. Mirrors identity.InjectAPIKeyClaims but takes the resolved
// tenant directly.
func WithAPIKeyClaims(ctx context.Context, tenantID uuid.UUID) context.Context {
	return identity.InjectClaims(ctx, identity.Claims{
		TenantID:   tenantID,
		AgentName:  "test-agent",
		AuthMethod: identity.AuthMethodAPIKey,
	})
}
