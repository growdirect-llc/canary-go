//go:build integration

// Integration test for the chirp rules engine. Exercises the real stack —
// pgxpool → transaction.transactions / detection.detection_rules / detection.detections — end-to-end.
//
// Run with:
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
//	go test -tags=integration -v ./internal/chirp/...
//
// Wave 3 coordinator runs this; do NOT run it in parallel subagent
// sessions or it will collide with other Loop 2 wave 2 fixtures.
package chirp_test

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

	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/chirp/rules"
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

// chirpFixtures seeds every row chirp needs to evaluate one transaction
// end-to-end and returns a cleanup func plus the seeded IDs the test
// will need.
type chirpFixtures struct {
	tenantID       uuid.UUID
	merchantID     uuid.UUID
	locationID     uuid.UUID
	employeeID     uuid.UUID
	ruleID         uuid.UUID
	transactionID  uuid.UUID
	cleanup        func()
}

func seedChirpFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *chirpFixtures {
	t.Helper()

	orgID := uuid.New()
	tenantID := uuid.New()
	merchantID := uuid.New()
	locationID := uuid.New()
	employeeID := uuid.New()
	ruleID := uuid.New()
	transactionID := uuid.New()
	tenantCode := "chirp-it-" + tenantID.String()[:8]

	mustExec := func(label, sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}

	mustExec("organization",
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "Chirp IT Org")

	mustExec("tenant",
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID, tenantCode, "Chirp IT Tenant", "chirp_it_"+tenantCode)

	mustExec("merchant",
		`INSERT INTO app.merchants (id, organization_id, tenant_id, source_merchant_id, merchant_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		merchantID, orgID, tenantID, "chirp-src-"+tenantCode, "Chirp IT Merchant")

	mustExec("location",
		`INSERT INTO location.locations (id, tenant_id, location_code, name, location_type, operating_hours)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		locationID, tenantID, "STORE-1", "Chirp IT Store", "store",
		`{"saturday":[{"open":"07:00","close":"22:00"}]}`)

	mustExec("employee",
		`INSERT INTO employee.employees (id, tenant_id, employee_code, first_name, last_name, display_name, hire_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		employeeID, tenantID, "EMP-1", "Test", "Cashier", "Test Cashier", "2025-01-01")

	// detection rule — void_threshold @ $10
	ruleDef := `{"rule_type":"void_threshold","parameters":{"threshold_cents":1000}}`
	mustExec("detection_rule",
		`INSERT INTO detection.detection_rules
		   (id, tenant_id, rule_code, name, rule_category, rule_definition, severity, status, evaluation_frequency)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)`,
		ruleID, tenantID, "C-IT-VOID", "IT void threshold", "shrink",
		ruleDef, "high", "active", "on_event")

	// transaction with one voided line over threshold
	now := time.Now().UTC()
	mustExec("transaction",
		`INSERT INTO transaction.transactions
		   (id, tenant_id, transaction_number, transaction_type, location_id,
		    cashier_employee_id, business_date, started_at, ended_at, status,
		    item_count, subtotal, tax_total, discount_total, grand_total)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		transactionID, tenantID, "IT-TX-1", "sale", locationID, employeeID,
		now.Format("2006-01-02"), now, now.Add(time.Minute), "completed",
		1, "50.0000", "0.0000", "0.0000", "50.0000")

	mustExec("line_item",
		`INSERT INTO transaction.transaction_line_items
		   (tenant_id, transaction_id, line_number, description, quantity,
		    unit_of_measure, unit_price, unit_discount, unit_tax, is_void, void_reason)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tenantID, transactionID, 1, "test item", "1.0000", "EA",
		"50.0000", "0.0000", "0.0000", true, "test")

	cleanup := func() {
		// Delete in dependency order; ignore errors (best-effort).
		_, _ = pool.Exec(ctx, `DELETE FROM detection.detections WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM transaction.transaction_line_items WHERE transaction_id = $1`, transactionID)
		_, _ = pool.Exec(ctx, `DELETE FROM transaction.transactions WHERE id = $1`, transactionID)
		_, _ = pool.Exec(ctx, `DELETE FROM detection.detection_rules WHERE id = $1`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM employee.employees WHERE id = $1`, employeeID)
		_, _ = pool.Exec(ctx, `DELETE FROM location.locations WHERE id = $1`, locationID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.merchants WHERE id = $1`, merchantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}

	return &chirpFixtures{
		tenantID:      tenantID,
		merchantID:    merchantID,
		locationID:    locationID,
		employeeID:    employeeID,
		ruleID:        ruleID,
		transactionID: transactionID,
		cleanup:       cleanup,
	}
}

func TestIntegration_EvaluateEndpoint(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	fx := seedChirpFixtures(t, ctx, pool)
	t.Cleanup(fx.cleanup)

	store := chirp.NewPgxStore(pool)
	registry := chirp.NewRegistry()
	registry.Register(rules.VoidThreshold{})
	engine := chirp.NewEngine(store, registry, zap.NewNop())
	handler := chirp.NewHandler(engine, store, zap.NewNop())

	r := chi.NewRouter()
	handler.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := `{"transaction_id":"` + fx.transactionID.String() + `"}`
	req, _ := http.NewRequest("POST", srv.URL+"/v1/chirp/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}

	var out struct {
		DetectionsCount int                `json:"detections_count"`
		Detections      []chirp.Detection  `json:"detections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.DetectionsCount != 1 {
		t.Fatalf("want 1 detection, got %d", out.DetectionsCount)
	}

	// Verify it actually landed in detection.detections.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM detection.detections WHERE tenant_id = $1 AND rule_id = $2`,
		fx.tenantID, fx.ruleID).Scan(&count); err != nil {
		t.Fatalf("count detections: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 row in detection.detections, got %d", count)
	}
}
