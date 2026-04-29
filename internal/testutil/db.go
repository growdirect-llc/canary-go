// internal/testutil/db.go
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
