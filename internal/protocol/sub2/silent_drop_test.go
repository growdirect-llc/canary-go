//go:build integration

// Silent-data-loss regression tests for sub2.PgxStore — patent
// Application 63/991,596 Node 4. Closes GRO-914.
//
// Two paths in (*PgxStore).Persist used to lose data without emitting
// a signal:
//
//  1. Tender drop: when the adapter doesn't set TenderTypeID and the
//     (tenant, source) default lookup against finance.tender_types
//     returns no rows, the tender row is dropped (the canonical header
//     is load-bearing; tenders are detail metadata). The drop is now
//     accompanied by a Warn log naming the transaction so an auditor
//     can grep prod logs for what was lost.
//
//  2. Cashier swallow: lookupEmployee used to return every pgx error
//     to the caller, which interpreted ANY failure as "leave cashier
//     nil." A transient connection blip would silently null the
//     cashier from a real sale. lookupEmployee now distinguishes
//     pgx.ErrNoRows (returns (uuid.Nil, nil)) from other errors
//     (propagated wrapped with "sub2: lookup employee:") so the
//     caller can fail loud and the message is redelivered.
//
// Run with:
//
//	DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable' \
//	  go test -tags=integration ./internal/protocol/sub2/... -run SilentDrop -count=1 -v
package sub2

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/ruptiv/canary/internal/db/types"
)

// silentDropPool returns a pool against DATABASE_URL or skips. Mirrors
// the existing transaction/fox cross-tenant test helpers.
func silentDropPool(t *testing.T) *pgxpool.Pool {
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

// seedSilentDropFixtures inserts org → tenant → merchant → location.
// Returns merchantID, tenantID, locationID, locationCode and registers
// row-level cleanup via t.Cleanup. Does NOT seed a finance.tender_types
// row — that's the missing-default condition the tender drop test
// exercises.
func seedSilentDropFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (
	merchantID, tenantID, locationID uuid.UUID, locationCode string,
) {
	t.Helper()
	orgID := uuid.New()
	tenantID = uuid.New()
	merchantID = uuid.New()
	locationID = uuid.New()
	short := tenantID.String()[:8]
	tenantCode := "sd-" + short
	schemaName := "tenant_sd_" + strings.ReplaceAll(short, "-", "")
	sourceMerchantID := "sd-source-" + short
	locationCode = "L-SD-" + short

	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec %q: %v", sql, err)
		}
	}

	exec(`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-914 silent-drop test org")
	exec(`INSERT INTO app.tenants (id, organization_id, tenant_code, name, status, schema_name)
	       VALUES ($1, $2, $3, $4, 'active', $5)`,
		tenantID, orgID, tenantCode, "GRO-914 silent-drop tenant", schemaName)
	exec(`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
	       VALUES ($1, $2, $3, $4, 'GRO-914 silent-drop merchant')`,
		merchantID, orgID, tenantID, sourceMerchantID)
	exec(`INSERT INTO location.locations (id, tenant_id, location_code, name, location_type)
	       VALUES ($1, $2, $3, 'GRO-914 silent-drop location', 'store')`,
		locationID, tenantID, locationCode)

	t.Cleanup(func() {
		// Dependency-ordered cleanup. transaction_tenders cascades
		// from transactions on DELETE so the explicit child wipe is
		// belt-and-suspenders.
		_, _ = pool.Exec(ctx, `DELETE FROM transaction.transaction_tenders WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM transaction.transactions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM finance.tender_types WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM employee.employees WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM location.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	})
	return
}

// canonicalForSilentDrop builds a minimal CanonicalEvent with one
// tender that has TenderTypeID == uuid.Nil — exercises the
// resolve-default branch.
func canonicalForSilentDrop(merchantID uuid.UUID, sourceCode, locationCode string) *CanonicalEvent {
	now := time.Now().UTC()
	return &CanonicalEvent{
		EventID:            uuid.New(),
		MerchantID:         merchantID,
		SourceCode:         sourceCode,
		SourceTxnID:        "TXN-SD-" + uuid.NewString()[:8],
		SourceLocationCode: locationCode,
		Transaction: types.Transaction{
			TransactionNumber: "TXN-SD-" + uuid.NewString()[:8],
			TransactionType:   "sale",
			BusinessDate:      now,
			StartedAt:         now,
			EndedAt:           now,
			Status:            "completed",
			ItemCount:         0,
			Subtotal:          "10.0000",
			TaxTotal:          "0.0000",
			DiscountTotal:     "0.0000",
			GrandTotal:        "10.0000",
			Currency:          "USD",
			Channel:           "pos",
		},
		Tenders: []types.TransactionTender{
			{
				TenderSequence: 1,
				TenderTypeID:   uuid.Nil, // adapter left this unset
				Amount:         "10.0000",
				Currency:       "USD",
				CashBackAmount: "0.0000",
				ChangeAmount:   "0.0000",
			},
		},
		ParsedAt: now,
	}
}

// TestWriteCanonical_DroppedTender_LogsWarn proves the Warn-and-continue
// path: when no default tender_type is seeded for (tenant, source), the
// tender row is dropped (canonical header still commits) AND a Warn
// entry is emitted carrying the tx id, source, tender index, and the
// underlying lookup error.
func TestWriteCanonical_DroppedTender_LogsWarn(t *testing.T) {
	pool := silentDropPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	merchantID, tenantID, _, locationCode := seedSilentDropFixtures(t, ctx, pool)

	// Capture all entries at Warn level and above.
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	store := NewPgxStore(pool).WithLogger(logger)

	evt := canonicalForSilentDrop(merchantID, "sd-source-"+tenantID.String()[:8], locationCode)
	if err := store.Persist(ctx, evt); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Header committed despite the tender drop.
	var headerCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transaction.transactions WHERE id = $1`, evt.Transaction.ID,
	).Scan(&headerCount); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if headerCount != 1 {
		t.Fatalf("transaction header should commit; count=%d", headerCount)
	}

	// Tender did NOT land — no default tender_type was seeded.
	var tenderCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transaction.transaction_tenders WHERE transaction_id = $1`,
		evt.Transaction.ID,
	).Scan(&tenderCount); err != nil {
		t.Fatalf("count tenders: %v", err)
	}
	if tenderCount != 0 {
		t.Fatalf("tender should be dropped when no default seeded; got %d row(s)", tenderCount)
	}

	// Exactly one Warn entry for the dropped tender, with the expected
	// fields. Filter to dropping-tender messages so seed/cleanup noise
	// from elsewhere doesn't pollute the count.
	var dropEntries []observer.LoggedEntry
	for _, e := range recorded.All() {
		if strings.HasPrefix(e.Message, "sub2: dropping tender") {
			dropEntries = append(dropEntries, e)
		}
	}
	if len(dropEntries) != 1 {
		t.Fatalf("want 1 dropping-tender Warn entry, got %d (all entries: %+v)",
			len(dropEntries), recorded.All())
	}
	entry := dropEntries[0]
	if entry.Level != zap.WarnLevel {
		t.Errorf("entry level = %v, want WarnLevel", entry.Level)
	}
	fields := entry.ContextMap()
	if got := fields["transaction_id"]; got != evt.Transaction.ID.String() {
		t.Errorf("transaction_id field = %v, want %s", got, evt.Transaction.ID)
	}
	if got := fields["tenant_id"]; got != tenantID.String() {
		t.Errorf("tenant_id field = %v, want %s", got, tenantID)
	}
	if got := fields["source_code"]; got != evt.SourceCode {
		t.Errorf("source_code field = %v, want %s", got, evt.SourceCode)
	}
	if got := fields["tender_index"]; got != int64(0) {
		t.Errorf("tender_index field = %v, want 0", got)
	}
	if got := fields["amount"]; got != "10.0000" {
		t.Errorf("amount field = %v, want 10.0000", got)
	}
	// The underlying error from resolveTenderTypeID is logged via
	// zap.Error which surfaces under the "error" key.
	if got, ok := fields["error"].(string); !ok || got == "" {
		t.Errorf("error field missing or empty: %v", fields["error"])
	} else if !strings.Contains(got, "no default tender_type") {
		t.Errorf("error field = %q, want substring 'no default tender_type'", got)
	}
}

// TestLookupEmployee_NoRowsReturnsNilNil proves the contract:
// pgx.ErrNoRows from the underlying QueryRow surfaces as (uuid.Nil,
// nil) — the canonical "row genuinely missing → null cashier" path.
func TestLookupEmployee_NoRowsReturnsNilNil(t *testing.T) {
	pool := silentDropPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, tenantID, _, _ := seedSilentDropFixtures(t, ctx, pool)

	store := NewPgxStore(pool)
	id, err := store.lookupEmployee(ctx, tenantID, "definitely-not-seeded-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("lookupEmployee: want nil err for missing row, got %v", err)
	}
	if id != uuid.Nil {
		t.Errorf("lookupEmployee: want uuid.Nil for missing row, got %s", id)
	}
}

// TestLookupEmployee_PropagatesNonNoRowsError proves the contract: any
// non-ErrNoRows pgx error from the underlying QueryRow is wrapped with
// "sub2: lookup employee:" and propagated. We trigger this by closing
// the pool before calling — the resulting "closed pool" error is NOT
// pgx.ErrNoRows, so it must propagate.
//
// Smaller blast radius than introducing a queryrower interface just for
// this test: we own the pool and can poison it.
func TestLookupEmployee_PropagatesNonNoRowsError(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Dedicated pool we can close mid-test without affecting other
	// tests in the package.
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	pool.Close() // poison

	store := NewPgxStore(pool)
	id, err := store.lookupEmployee(ctx, uuid.New(), "any-code")
	if err == nil {
		t.Fatal("lookupEmployee: want non-nil error after pool close, got nil")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("lookupEmployee: pool-closed error misclassified as ErrNoRows: %v", err)
	}
	if id != uuid.Nil {
		t.Errorf("lookupEmployee: want uuid.Nil on error, got %s", id)
	}
	if !strings.Contains(err.Error(), "sub2: lookup employee:") {
		t.Errorf("lookupEmployee: want error wrapped with 'sub2: lookup employee:', got %q", err.Error())
	}
}
