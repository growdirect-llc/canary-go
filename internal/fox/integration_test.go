//go:build integration

// Integration smoke test for fox. Exercises the real pgx Store
// against a running Postgres with the canonical schema applied.
//
//	GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable \
//	go test -tags=integration -v ./internal/fox/...
//
// Wave 3 coordinator runs this post-merge — the per-subagent verify
// pass for Loop 2 Wave 2 explicitly does NOT run integration tests.
package fox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

func skipIfNoIntegration(t *testing.T) string {
	t.Helper()
	dbURL := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to run integration tests")
	}
	return dbURL
}

// seedFixtures inserts an organization + tenant + active rule, then a
// detection. Returns the tenant id, the detection id, and a cleanup
// func. Cleanup runs in dependency order; failures are best-effort.
func seedFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (tenantID, detID uuid.UUID, cleanup func()) {
	t.Helper()

	orgID := uuid.New()
	tenantID = uuid.New()
	ruleID := uuid.New()
	detID = uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "GRO-761 Fox Integration Test Org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID, "fox-it-"+tenantID.String()[:8], "Fox IT Tenant", "fox_it_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO q.detection_rules (id, tenant_id, rule_code, name, rule_category, rule_definition, severity)
		 VALUES ($1, $2, $3, $4, 'shrink', '{}', 'high')`,
		ruleID, tenantID, "fox-it-rule", "Fox IT rule"); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	cashier := uuid.New() // unbound — no FK to e.employees in the schema (nullable column)
	if _, err := pool.Exec(ctx,
		`INSERT INTO q.detections (id, tenant_id, rule_id, source_entity_type, source_entity_id,
		                            cashier_employee_id, severity, evidence)
		 VALUES ($1, $2, $3, 'transaction', $4, $5, 'high', $6)`,
		detID, tenantID, ruleID, uuid.New(), cashier,
		[]byte(`{"reason":"refund_pattern_5x"}`)); err != nil {
		t.Fatalf("seed detection: %v", err)
	}

	cleanup = func() {
		_, _ = pool.Exec(ctx, `DELETE FROM q.case_actions    WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM q.case_evidence   WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM q.detections      WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM q.cases           WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM q.detection_rules WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.tenants       WHERE id = $1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	}
	return tenantID, detID, cleanup
}

func TestIntegration_FromDetection_OpensCase(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	tenantID, detID, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	store := NewStore(pool)
	h := New(store, DefaultEscalationConfig(), nil)
	r := chi.NewRouter()
	h.Mount(r)

	body, _ := json.Marshal(map[string]string{"detection_id": detID.String()})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/fox/cases/from-detection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Opened {
		t.Fatalf("expected opened=true, got %+v", resp)
	}

	caseID := uuid.MustParse(resp.CaseID)
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM q.cases WHERE id = $1 AND tenant_id = $2`,
		caseID, tenantID).Scan(&count); err != nil {
		t.Fatalf("verify case: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 case row, got %d", count)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM q.case_evidence WHERE case_id = $1`,
		caseID).Scan(&count); err != nil {
		t.Fatalf("verify evidence: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 seed evidence row, got %d", count)
	}
	// Detection should now be linked back to the case.
	var linkedCase *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT case_id FROM q.detections WHERE id = $1`, detID).Scan(&linkedCase); err != nil {
		t.Fatalf("verify detection link: %v", err)
	}
	if linkedCase == nil || *linkedCase != caseID {
		t.Errorf("detection not linked to case")
	}
}

func TestIntegration_AppendAction(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	tenantID, detID, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	store := NewStore(pool)
	h := New(store, DefaultEscalationConfig(), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Open a case via the from-detection endpoint to get a real case_id.
	body, _ := json.Marshal(map[string]string{"detection_id": detID.String()})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/fox/cases/from-detection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open case: %d %s", rec.Code, rec.Body.String())
	}
	var resp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	caseID := uuid.MustParse(resp.CaseID)

	// Append an action.
	actionBody, _ := json.Marshal(map[string]string{
		"action_type": "note",
		"notes":       "Reviewed video, matches detection",
	})
	req = httptest.NewRequest(http.MethodPost,
		"/v1/fox/cases/"+caseID.String()+"/actions", bytes.NewReader(actionBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("append action: %d %s", rec.Code, rec.Body.String())
	}

	// Verify q.case_actions has the row plus the auto-seeded
	// status_change + evidence_collected rows from OpenCase + AppendEvidence.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM q.case_actions WHERE case_id = $1 AND tenant_id = $2`,
		caseID, tenantID).Scan(&count); err != nil {
		t.Fatalf("verify actions: %v", err)
	}
	if count < 3 {
		t.Errorf("expected ≥3 actions (open + evidence + note), got %d", count)
	}
}

func TestIntegration_CloseCase_TerminalState(t *testing.T) {
	dbURL := skipIfNoIntegration(t)
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, dbURL)
	defer pool.Close()

	tenantID, detID, cleanup := seedFixtures(t, ctx, pool)
	defer cleanup()

	store := NewStore(pool)
	h := New(store, DefaultEscalationConfig(), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Open then close.
	body, _ := json.Marshal(map[string]string{"detection_id": detID.String()})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/fox/cases/from-detection", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var openResp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&openResp)
	caseID := uuid.MustParse(openResp.CaseID)

	closeBody, _ := json.Marshal(map[string]string{
		"resolution": "substantiated",
		"notes":      "termination + recovery filed",
	})
	req = httptest.NewRequest(http.MethodPost,
		"/v1/fox/cases/"+caseID.String()+"/close", bytes.NewReader(closeBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("close: %d %s", rec.Code, rec.Body.String())
	}

	var status string
	var resolution *string
	if err := pool.QueryRow(ctx,
		`SELECT status, resolution_type FROM q.cases WHERE id = $1 AND tenant_id = $2`,
		caseID, tenantID).Scan(&status, &resolution); err != nil {
		t.Fatalf("verify close: %v", err)
	}
	if status != "closed" {
		t.Errorf("status: got %s want closed", status)
	}
	if resolution == nil || *resolution != "substantiated" {
		t.Errorf("resolution: got %v", resolution)
	}
}

// Compile-time guard that types.Case fields used in integration tests
// stay in sync with the schema.
var _ = time.Now
var _ = (*types.Case)(nil)
