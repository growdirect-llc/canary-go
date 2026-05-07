//go:build integration

package lp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/lp"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
)

// seedTenant creates an org and a tenant for use in test fixtures.
// Returns the tenant ID so callers can scope their writes.
func seedTenant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	pool := testutil.MustConnect(t)
	orgID := uuid.New()
	tenantID := uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "lp-test-org-"+orgID.String()[:8]); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		"lp-t-"+tenantID.String()[:8],
		"LP Test Tenant",
		"lp_t_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return tenantID
}

func TestAllowListStore_CreateAndList(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	pattern, err := lp.NewPattern(lp.PatternTypeAllowlist, lp.KindDeadCount, map[string]any{
		"cashier_id": "C-0042",
		"store":      "STR-01",
	})
	if err != nil {
		t.Fatalf("new pattern: %v", err)
	}
	reason := "manager override"

	created, err := store.Create(ctx, lp.CreateInput{
		TenantID: tenantID,
		Pattern:  pattern,
		Reason:   &reason,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("created row missing id")
	}
	if created.TenantID != tenantID {
		t.Errorf("tenant_id = %v, want %v", created.TenantID, tenantID)
	}

	rows, err := store.ListByPattern(ctx, tenantID, lp.PatternTypeAllowlist, lp.KindDeadCount, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].ID != created.ID {
		t.Errorf("listed id = %v, want %v", rows[0].ID, created.ID)
	}
	got, err := lp.DecodePattern(rows[0].Pattern)
	if err != nil {
		t.Fatalf("decode pattern: %v", err)
	}
	if got["cashier_id"] != "C-0042" {
		t.Errorf("pattern.cashier_id = %v, want C-0042", got["cashier_id"])
	}
}

func TestAllowListStore_ListByPattern_FiltersByTypeAndKind(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	// Insert one entry per kind under the same tenant.
	kinds := []struct {
		patternType, kind string
	}{
		{lp.PatternTypeAllowlist, lp.KindDeadCount},
		{lp.PatternTypeAllowlist, lp.KindDiscounts},
		{lp.PatternTypeThreshold, lp.KindDrawer},
		{lp.PatternTypeVocab, lp.KindVoidReason},
	}
	for _, k := range kinds {
		p, _ := lp.NewPattern(k.patternType, k.kind, map[string]any{"marker": k.kind})
		if _, err := store.Create(ctx, lp.CreateInput{TenantID: tenantID, Pattern: p}); err != nil {
			t.Fatalf("seed %s/%s: %v", k.patternType, k.kind, err)
		}
	}

	// Filter to the dead_count entry only.
	rows, err := store.ListByPattern(ctx, tenantID, lp.PatternTypeAllowlist, lp.KindDeadCount, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 dead_count row, got %d", len(rows))
	}
	pat, _ := lp.DecodePattern(rows[0].Pattern)
	if pat["marker"] != "dead_count" {
		t.Errorf("got pattern marker %v, want dead_count", pat["marker"])
	}

	// Filter to the drawer threshold.
	rows, err = store.ListByPattern(ctx, tenantID, lp.PatternTypeThreshold, lp.KindDrawer, 50)
	if err != nil {
		t.Fatalf("list threshold: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 drawer row, got %d", len(rows))
	}
}

func TestAllowListStore_Update_PartialFields(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	pattern, _ := lp.NewPattern(lp.PatternTypeAllowlist, lp.KindVoids, map[string]any{
		"reason_code": "ADM-VOID",
	})
	original := "initial"
	created, err := store.Create(ctx, lp.CreateInput{
		TenantID: tenantID,
		Pattern:  pattern,
		Reason:   &original,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update only the reason field.
	updated := "updated reason"
	row, err := store.Update(ctx, lp.UpdateInput{
		ID:       created.ID,
		TenantID: tenantID,
		Reason:   &updated,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if row.Reason == nil || *row.Reason != updated {
		t.Errorf("reason = %v, want %q", row.Reason, updated)
	}
	// Pattern unchanged.
	pat, _ := lp.DecodePattern(row.Pattern)
	if pat["reason_code"] != "ADM-VOID" {
		t.Errorf("pattern.reason_code mutated unexpectedly: %v", pat["reason_code"])
	}
}

func TestAllowListStore_Update_NotFound(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	reason := "x"
	_, err := store.Update(ctx, lp.UpdateInput{
		ID:       uuid.New(),
		TenantID: tenantID,
		Reason:   &reason,
	})
	if !errors.Is(err, lp.ErrAllowListNotFound) {
		t.Errorf("expected ErrAllowListNotFound, got %v", err)
	}
}

func TestAllowListStore_Delete(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	pattern, _ := lp.NewPattern(lp.PatternTypeVocab, lp.KindCompReason, map[string]any{
		"reason_code": "COMP-MGR",
	})
	created, err := store.Create(ctx, lp.CreateInput{TenantID: tenantID, Pattern: pattern})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Delete(ctx, tenantID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Second delete returns not-found.
	if err := store.Delete(ctx, tenantID, created.ID); !errors.Is(err, lp.ErrAllowListNotFound) {
		t.Errorf("second delete: expected ErrAllowListNotFound, got %v", err)
	}

	// Get also returns not-found.
	if _, err := store.Get(ctx, tenantID, created.ID); !errors.Is(err, lp.ErrAllowListNotFound) {
		t.Errorf("post-delete get: expected ErrAllowListNotFound, got %v", err)
	}
}

func TestAllowListStore_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	tenantA := seedTenant(t, ctx)
	tenantB := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	// Tenant A creates an entry.
	pattern, _ := lp.NewPattern(lp.PatternTypeAllowlist, lp.KindDeadCount, map[string]any{
		"cashier_id": "C-LEAK",
	})
	created, err := store.Create(ctx, lp.CreateInput{TenantID: tenantA, Pattern: pattern})
	if err != nil {
		t.Fatalf("create tenant A: %v", err)
	}

	// Tenant B should not see it via ListByPattern.
	rows, err := store.ListByPattern(ctx, tenantB, lp.PatternTypeAllowlist, lp.KindDeadCount, 50)
	if err != nil {
		t.Fatalf("list tenant B: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("tenant B saw %d rows from tenant A — isolation broken", len(rows))
	}

	// Tenant B cannot Get tenant A's row even with the leaked ID.
	if _, err := store.Get(ctx, tenantB, created.ID); !errors.Is(err, lp.ErrAllowListNotFound) {
		t.Errorf("tenant B Get of tenant A id: expected ErrAllowListNotFound, got %v", err)
	}

	// Tenant B cannot Delete tenant A's row.
	if err := store.Delete(ctx, tenantB, created.ID); !errors.Is(err, lp.ErrAllowListNotFound) {
		t.Errorf("tenant B Delete of tenant A id: expected ErrAllowListNotFound, got %v", err)
	}

	// Tenant A still owns the row.
	got, err := store.Get(ctx, tenantA, created.ID)
	if err != nil {
		t.Fatalf("tenant A get post-leak attempts: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("tenant A row id = %v, want %v", got.ID, created.ID)
	}
}

func TestAllowListStore_ExpiresAt(t *testing.T) {
	ctx := context.Background()
	tenantID := seedTenant(t, ctx)
	store := lp.NewAllowListStore(testutil.MustConnect(t))

	pattern, _ := lp.NewPattern(lp.PatternTypeAllowlist, lp.KindDiscounts, map[string]any{
		"reason_code": "EMPLOYEE",
	})
	expired := time.Now().Add(-1 * time.Hour)
	if _, err := store.Create(ctx, lp.CreateInput{
		TenantID:  tenantID,
		Pattern:   pattern,
		ExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	future := time.Now().Add(1 * time.Hour)
	if _, err := store.Create(ctx, lp.CreateInput{
		TenantID:  tenantID,
		Pattern:   pattern,
		ExpiresAt: &future,
	}); err != nil {
		t.Fatalf("create active: %v", err)
	}

	rows, err := store.ListByPattern(ctx, tenantID, lp.PatternTypeAllowlist, lp.KindDiscounts, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 active row (expired filtered), got %d", len(rows))
	}
}

func TestNewPattern_DropsReservedKeys(t *testing.T) {
	raw, err := lp.NewPattern(lp.PatternTypeAllowlist, lp.KindDeadCount, map[string]any{
		"type":       "should-be-dropped",
		"kind":       "should-be-dropped",
		"cashier_id": "C-0001",
	})
	if err != nil {
		t.Fatalf("new pattern: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["type"] != lp.PatternTypeAllowlist {
		t.Errorf("type = %v, want %v", m["type"], lp.PatternTypeAllowlist)
	}
	if m["kind"] != lp.KindDeadCount {
		t.Errorf("kind = %v, want %v", m["kind"], lp.KindDeadCount)
	}
	if m["cashier_id"] != "C-0001" {
		t.Errorf("cashier_id = %v", m["cashier_id"])
	}
}
