//go:build integration

package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

// TestAlertListPage_Renders is a smoke test: verifies the alerts page handler
// mounts correctly, connects to the store, and renders without panicking.
// Empty results are fine — this is a nil-tenant smoke test (tenantIDFromCtx returns uuid.Nil).
func TestAlertListPage_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := alert.NewStore(pool)
	deps := web.Deps{AlertStore: store}
	h := web.New(deps, nil)

	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "<h1>Alerts</h1>") {
		t.Errorf("expected response body to contain %q", "<h1>Alerts</h1>")
	}
}

// TestAlertListPage_WithData seeds an org → tenant → rule → detection and verifies
// the page renders 200. The seeded tenant won't match the nil tenant from
// tenantIDFromCtx, so results will be empty — validates store connectivity, no panic.
func TestAlertListPage_WithData(t *testing.T) {
	ctx := context.Background()
	pool := testutil.MustConnect(t)
	orgID := uuid.New()
	tenantID := uuid.New()
	ruleID := uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "web-test-org-"+orgID.String()[:8]); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		"web-t-"+tenantID.String()[:8],
		"Web Alert Test Tenant",
		"web_t_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO detection.detection_rules
			(id, tenant_id, rule_code, rule_category, name, severity, rule_definition)
		 VALUES ($1, $2, 'Q.D.1', 'discount', 'Discount Cap', 'high', '{}')`,
		ruleID, tenantID); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO detection.detections
			(tenant_id, rule_id, source_entity_type, source_entity_id, severity, status)
		 VALUES ($1, $2, 'transaction', $3, 'high', 'new')`,
		tenantID, ruleID, uuid.New()); err != nil {
		t.Fatalf("seed detection: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM detection.detections WHERE tenant_id = $1`, tenantID)
		pool.Exec(ctx, `DELETE FROM detection.detection_rules WHERE id = $1`, ruleID)
		pool.Exec(ctx, `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		pool.Exec(ctx, `DELETE FROM app.organizations WHERE id = $1`, orgID)
	})

	store := alert.NewStore(pool)
	deps := web.Deps{AlertStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestAlertDetailPage_UnknownID_Returns404 verifies that a non-existent alert ID
// returns 404 rather than panicking or returning 500.
func TestAlertDetailPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := alert.NewStore(pool)
	deps := web.Deps{AlertStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	nonexistentID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/alerts/"+nonexistentID, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}
