//go:build integration

// Integration smoke for Owl. Stands up the real chi handler over a
// real pgxpool, seeds a tenant + merchant + two locations + a handful
// of transactions + 1 detection + 1 case, calls
// GET /v1/owl/dashboard?merchant_id=...&period=mtd, and verifies the
// shape and the totals.
//
// Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/owl/...
//
// Wave 3 coordinator runs this post-merge. Wave 2 owl subagent does
// not run integration tests (port collisions in parallel; DB schema
// applied centrally).
package owl

import (
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
)

// envOrSkip returns the DATABASE_URL or skips the test.
func envOrSkip(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

// fixture seeds a complete dashboard scenario and returns the merchant
// id plus a cleanup func. Money values are chosen so the assertions
// can be exact rather than approximate.
type fixture struct {
	merchantID uuid.UUID
	tenantID   uuid.UUID
	locA       uuid.UUID
	locB       uuid.UUID
	itemX      uuid.UUID
	itemY      uuid.UUID
	cashier    uuid.UUID
	caseID     uuid.UUID
	cleanup    func()
}

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) fixture {
	t.Helper()

	// Synthetic IDs so we can clean up by exact match.
	orgID := uuid.New()
	tenantID := uuid.New()
	merchantID := uuid.New()
	locA := uuid.New()
	locB := uuid.New()
	itemX := uuid.New()
	itemY := uuid.New()
	cashierID := uuid.New()
	caseID := uuid.New()
	subjectID := uuid.New()
	ruleID := uuid.New()
	detectionID := uuid.New()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	mustExec := func(q string, args ...any) {
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", strings.SplitN(q, " ", 4)[0:3], err)
		}
	}

	mustExec(`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "Owl Integration Org")
	mustExec(`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
	          VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID, "owl-int-"+tenantID.String()[:8], "Owl Test Tenant", "owl_test_"+tenantID.String()[:8])
	mustExec(`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
	          VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID, "owl-int-"+merchantID.String()[:8], "Owl Test Merchant")
	mustExec(`INSERT INTO app.merchant_settings (merchant_id, timezone) VALUES ($1, 'UTC')`, merchantID)

	// Two canonical locations.
	mustExec(`INSERT INTO location.locations (id, tenant_id, location_code, name)
	          VALUES ($1, $2, '01', 'Main'), ($3, $2, '02', 'Annex')`,
		locA, tenantID, locB)

	// Cashier (canonical employee.employees).
	mustExec(`INSERT INTO employee.employees (id, tenant_id, employee_code, first_name, last_name, hire_date)
	          VALUES ($1, $2, 'E-101', 'Alex', 'Park', '2024-01-15')`,
		cashierID, tenantID)

	// Two items.
	mustExec(`INSERT INTO catalog.items (id, tenant_id, sku, description) VALUES ($1, $2, 'SKU-X', 'Widget'), ($3, $2, 'SKU-Y', 'Gadget')`,
		itemX, tenantID, itemY)

	// Subject + case (detection.cases needs a primary subject FK or NULL — use NULL).
	mustExec(`INSERT INTO detection.subjects (id, tenant_id, subject_code, subject_type, display_name)
	          VALUES ($1, $2, 'SUBJ-1', 'known_employee', 'Alex Park')`,
		subjectID, tenantID)
	mustExec(`INSERT INTO detection.cases (id, tenant_id, case_number, title, severity, status, primary_subject_id, primary_location_id, opened_at)
	          VALUES ($1, $2, 'CASE-001', 'Suspicious refund pattern', 'high', 'open', $3, $4, now())`,
		caseID, tenantID, subjectID, locA)

	// Detection rule + detection.
	mustExec(`INSERT INTO detection.detection_rules (id, tenant_id, rule_code, name, rule_category, rule_definition)
	          VALUES ($1, $2, 'RULE-1', 'Excessive voids', 'fraud', '{}')`,
		ruleID, tenantID)
	mustExec(`INSERT INTO detection.detections (id, tenant_id, rule_id, source_entity_type, source_entity_id, location_id, cashier_employee_id, severity)
	          VALUES ($1, $2, $3, 'transaction', gen_random_uuid(), $4, $5, 'high')`,
		detectionID, tenantID, ruleID, locA, cashierID)

	// Transactions: 3 sales at locA totalling $300, 2 sales at locB
	// totalling $50, 1 refund at locA for $20.
	mkTx := func(loc uuid.UUID, ttype string, total, discount float64) {
		txID := uuid.New()
		txNum := "T-" + txID.String()[:8]
		mustExec(`INSERT INTO transaction.transactions (id, tenant_id, transaction_number, transaction_type, location_id, cashier_employee_id, business_date, started_at, ended_at, status, subtotal, discount_total, grand_total)
		          VALUES ($1, $2, $3, $4, $5, $6, current_date, now(), now(), 'completed', $7, $8, $7)`,
			txID, tenantID, txNum, ttype, loc, cashierID, total, discount)
	}
	mkTx(locA, "sale", 100.00, 0)
	mkTx(locA, "sale", 100.00, 0)
	mkTx(locA, "sale", 100.00, 5.00)
	mkTx(locB, "sale", 25.00, 0)
	mkTx(locB, "sale", 25.00, 0)
	mkTx(locA, "refund", 20.00, 0)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	cleanup := func() {
		// FK-aware cleanup, best-effort.
		ctx2 := context.Background()
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.detections   WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.case_actions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.case_evidence WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.cases        WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.detection_rules WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM detection.subjects     WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM transaction.transaction_line_items WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM transaction.transactions WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM catalog.items        WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM employee.employees    WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM location.locations    WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM app.merchant_settings WHERE merchant_id = $1`, merchantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM app.merchants  WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM app.tenants    WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx2, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}

	return fixture{
		merchantID: merchantID,
		tenantID:   tenantID,
		locA:       locA,
		locB:       locB,
		itemX:      itemX,
		itemY:      itemY,
		cashier:    cashierID,
		caseID:     caseID,
		cleanup:    cleanup,
	}
}

func TestIntegration_Dashboard_MTD(t *testing.T) {
	dbURL := envOrSkip(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	fx := seed(t, ctx, pool)
	defer fx.cleanup()

	store := NewPgxStore(pool)
	agg := NewAggregator(store)
	handler := New(agg, zap.NewNop())

	r := chi.NewRouter()
	handler.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// MTD: from 1st of current month → now. The seeded transactions all
	// have started_at = now() so they fall inside the window.
	resp, err := http.Get(srv.URL + "/v1/owl/dashboard?merchant_id=" + fx.merchantID.String() + "&period=mtd")
	if err != nil {
		t.Fatalf("get dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var dash Dashboard
	if err := json.NewDecoder(resp.Body).Decode(&dash); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Sales: 5 sale transactions, gross 350.00, 1 refund of 20.00,
	// discount 5.00, net = 350 - 20 - 5 = 325.00
	if dash.Sales.TransactionCount != 5 {
		t.Errorf("Sales.TransactionCount = %d, want 5", dash.Sales.TransactionCount)
	}
	if dash.Sales.RefundCount != 1 {
		t.Errorf("Sales.RefundCount = %d, want 1", dash.Sales.RefundCount)
	}
	// Strings come back from numeric — Postgres formats as "350.0000".
	// Use prefix match to dodge Postgres's trailing-zero formatting.
	if !strings.HasPrefix(dash.Sales.GrossSales, "350") {
		t.Errorf("Sales.GrossSales = %q, want prefix 350", dash.Sales.GrossSales)
	}

	// Two locations.
	if len(dash.ByLocation) != 2 {
		t.Errorf("ByLocation count = %d, want 2", len(dash.ByLocation))
	}

	// Cases: 1 open.
	if dash.Cases.OpenNow != 1 {
		t.Errorf("Cases.OpenNow = %d, want 1", dash.Cases.OpenNow)
	}

	// Detections: 1 detection / 5 sales = 200/1k.
	if dash.Detection.DetectionCount != 1 {
		t.Errorf("Detection.DetectionCount = %d, want 1", dash.Detection.DetectionCount)
	}
	if dash.Detection.TransactionCount != 5 {
		t.Errorf("Detection.TransactionCount = %d, want 5", dash.Detection.TransactionCount)
	}
	if dash.Detection.RatePer1KTransactions != 200.0 {
		t.Errorf("Detection.Rate = %f, want 200.0", dash.Detection.RatePer1KTransactions)
	}

	// Exposure: 1 cashier had 1 detection.
	if len(dash.Exposure) != 1 || dash.Exposure[0].DetectionCount != 1 {
		t.Errorf("Exposure = %+v", dash.Exposure)
	}
	if dash.Exposure[0].EmployeeCode != "E-101" {
		t.Errorf("Exposure[0].EmployeeCode = %q, want E-101", dash.Exposure[0].EmployeeCode)
	}

	// Period parsing.
	if dash.Period.Kind != PeriodMTD {
		t.Errorf("Period.Kind = %s, want mtd", dash.Period.Kind)
	}
	if dash.Period.Timezone != "UTC" {
		t.Errorf("Period.Timezone = %s, want UTC", dash.Period.Timezone)
	}
	if dash.Period.From.Day() != 1 {
		t.Errorf("Period.From day = %d, want 1", dash.Period.From.Day())
	}

	// generated_at is set to ~now.
	if time.Since(dash.GeneratedAt) > 10*time.Second {
		t.Errorf("GeneratedAt = %v, want recent", dash.GeneratedAt)
	}
}

func TestIntegration_Dashboard_MerchantNotFound(t *testing.T) {
	dbURL := envOrSkip(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	store := NewPgxStore(pool)
	agg := NewAggregator(store)
	handler := New(agg, zap.NewNop())

	r := chi.NewRouter()
	handler.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/owl/dashboard?merchant_id=" + uuid.New().String() + "&period=today")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
