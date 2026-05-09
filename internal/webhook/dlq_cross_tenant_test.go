//go:build integration

// Cross-tenant negative-test suite for the webhook DLQ store. Closes
// the store-level slice of GRO-910 (HIGH: webhook DLQ admin endpoints
// lack tenant scoping). The handler-level slice is asserted in
// cmd/gateway/admin_test.go (TestAdminGet_CrossTenant_Returns404,
// TestAdminReplay_CrossTenant_Returns404).
//
// Before this fix, internal/webhook/dlq.go Get / MarkReplayed /
// MarkRetryFailed / txGet all keyed on `WHERE id = $1`. Tenant
// isolation lived only at the handler layer (post-load comparison in
// cmd/gateway/admin.go). Any future caller that skipped that check
// could read or mutate another tenant's row. We now enforce
// `merchant_id = $N` at the SQL layer; cross-tenant probes return
// ErrDLQNotFound (no existence leak — same response shape as a real
// miss).
//
// Run via:
//
//	make test-integration
//
// or directly:
//
//	DATABASE_URL='postgres://...?sslmode=disable' \
//	  go test -tags=integration -run Cross ./internal/webhook/...

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dbPool returns a pool against DATABASE_URL or skips. Mirrors the
// pattern in internal/fox/cross_tenant_test.go.
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

// seedDLQRow inserts a pending DLQ row for the given merchant via the
// public Enqueue API and registers cleanup. Returns the inserted row.
func seedDLQRow(t *testing.T, ctx context.Context, q *DLQ, pool *pgxpool.Pool, merchantID uuid.UUID) *DLQRow {
	t.Helper()
	row, err := q.Enqueue(ctx, EnqueueParams{
		MerchantID:    merchantID,
		SourceCode:    "square",
		Payload:       json.RawMessage(`{"test":"cross-tenant"}`),
		FailureReason: "publish_failed",
		ErrorMessage:  "test seed",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM protocol.dlq WHERE id = $1`, row.ID)
	})
	return row
}

// TestDLQ_Get_TenantIsolation — store-level tenant scoping for Get +
// MarkReplayed. Tenant B asking for tenant A's DLQ row gets
// ErrDLQNotFound; same for MarkReplayed. Tenant A's read still
// succeeds, and no write was applied (status stayed 'pending').
func TestDLQ_Get_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := dbPool(t)
	q := NewDLQ(pool)

	tenantA := uuid.New()
	tenantB := uuid.New()
	rowA := seedDLQRow(t, ctx, q, pool, tenantA)

	// Cross-tenant read: must return ErrDLQNotFound, not the row.
	if _, err := q.Get(ctx, tenantB, rowA.ID); !errors.Is(err, ErrDLQNotFound) {
		t.Errorf("cross-tenant Get: got err=%v, want ErrDLQNotFound", err)
	}

	// Same-tenant read: must succeed.
	got, err := q.Get(ctx, tenantA, rowA.ID)
	if err != nil {
		t.Fatalf("same-tenant Get: %v", err)
	}
	if got.ID != rowA.ID {
		t.Errorf("same-tenant Get id: got %s, want %s", got.ID, rowA.ID)
	}
	if got.Status != "pending" {
		t.Errorf("same-tenant Get status (pre-write): got %q, want pending", got.Status)
	}

	// Cross-tenant write: MarkReplayed under tenant B must refuse and
	// must NOT mutate the row.
	if err := q.MarkReplayed(ctx, tenantB, rowA.ID); !errors.Is(err, ErrDLQNotFound) {
		t.Errorf("cross-tenant MarkReplayed: got err=%v, want ErrDLQNotFound", err)
	}

	// Re-read under tenant A: status must still be 'pending'. If the
	// SQL filter were missing, MarkReplayed would have flipped it to
	// 'replayed'.
	after, err := q.Get(ctx, tenantA, rowA.ID)
	if err != nil {
		t.Fatalf("post-write Get: %v", err)
	}
	if after.Status != "pending" {
		t.Errorf("post cross-tenant write status: got %q, want pending (cross-tenant write must not apply)",
			after.Status)
	}
}

// TestDLQ_MarkRetryFailed_TenantIsolation — store-level tenant scoping
// for the failed-replay path. Tenant B's MarkRetryFailed against
// tenant A's row returns ErrDLQNotFound. Tenant A's row remains
// untouched (retry_count still 0).
func TestDLQ_MarkRetryFailed_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := dbPool(t)
	q := NewDLQ(pool)

	tenantA := uuid.New()
	tenantB := uuid.New()
	rowA := seedDLQRow(t, ctx, q, pool, tenantA)

	if _, err := q.MarkRetryFailed(ctx, tenantB, rowA.ID, "some error"); !errors.Is(err, ErrDLQNotFound) {
		t.Errorf("cross-tenant MarkRetryFailed: got err=%v, want ErrDLQNotFound", err)
	}

	after, err := q.Get(ctx, tenantA, rowA.ID)
	if err != nil {
		t.Fatalf("post-write Get: %v", err)
	}
	if after.RetryCount != 0 {
		t.Errorf("retry_count after cross-tenant MarkRetryFailed: got %d, want 0",
			after.RetryCount)
	}
	if after.Status != "pending" {
		t.Errorf("status after cross-tenant MarkRetryFailed: got %q, want pending",
			after.Status)
	}
}
