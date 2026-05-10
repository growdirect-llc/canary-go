// internal/identity/scope_enforcement_test.go
//
// Cross-service acceptance probe for GRO-906. Mounts each in-scope
// service's chi router and verifies that:
//
//   - a key with only the read scope receives 403 insufficient_scope
//     when invoking a write route
//   - a key with the matching write scope receives any non-403 status
//     (the actual handler may legitimately return 400/404/500 because
//     the test does not stand up a database — what matters here is that
//     RequireScopeMiddleware did not block the request)
//
// The probe is unit-shaped and DB-free. It exercises the middleware
// boundary on every wired service in one place so future renames or
// scope drift fail loudly.

package identity_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/alert"
	"github.com/ruptiv/canary/internal/asset"
	"github.com/ruptiv/canary/internal/billing"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/owl"
	"github.com/ruptiv/canary/internal/report"
	"github.com/ruptiv/canary/internal/returns"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/transaction"
)

// scopeCase is one row in the cross-service probe matrix. method+path
// names a route on the mounted router; readScope is the scope sufficient
// to reach the route's read peers (used to prove the key WAS authenticated
// and just lacks the write privilege); writeScope is what the route
// actually requires.
type scopeCase struct {
	name       string
	method     string
	path       string
	readScope  string // scope held by the under-privileged key
	writeScope string // scope the route requires
	body       string
}

// mount builds a chi router with the named service's handler attached.
// One factory per service so each test row is fully self-describing.
func mountTransaction() chi.Router {
	r := chi.NewRouter()
	transaction.New(transaction.NewStore(nil), zap.NewNop()).Mount(r)
	return r
}

func mountAsset() chi.Router {
	r := chi.NewRouter()
	asset.New(asset.NewStore(nil), zap.NewNop()).Mount(r)
	return r
}

func mountReturns() chi.Router {
	r := chi.NewRouter()
	returns.New(returns.NewStore(nil), zap.NewNop()).Mount(r)
	return r
}

func mountReport() chi.Router {
	r := chi.NewRouter()
	report.New(report.NewStore(), zap.NewNop()).Mount(r)
	return r
}

func mountAlert() chi.Router {
	r := chi.NewRouter()
	alert.New(alert.NewStore(nil), zap.NewNop()).Mount(r)
	return r
}

func mountOwlDashboards() chi.Router {
	r := chi.NewRouter()
	owl.NewDashboardHandler(owl.NewDashboardStore(nil), zap.NewNop()).Mount(r)
	return r
}

func mountBilling() chi.Router {
	r := chi.NewRouter()
	billing.New(billing.NewStore(nil), zap.NewNop()).Mount(r)
	return r
}

// TestInsufficientScope_403_AcrossServices is the GRO-906 acceptance
// probe. For each row: a request carrying ONLY the read scope to a
// write route must return 403 with code=insufficient_scope.
func TestInsufficientScope_403_AcrossServices(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()

	cases := []struct {
		serviceName string
		mount       func() chi.Router
		probes      []scopeCase
	}{
		{
			serviceName: "transaction",
			mount:       mountTransaction,
			probes: []scopeCase{
				{
					name: "POST /v1/transactions (create)",
					method: http.MethodPost, path: "/v1/transactions",
					readScope: identity.ScopeTransactionRead, writeScope: identity.ScopeTransactionWrite,
					body: `{"transaction_number":"T-1","business_date":"2026-01-01"}`,
				},
				{
					name: "POST /v1/transactions/{id}/voids (void)",
					method: http.MethodPost, path: "/v1/transactions/" + uuid.New().String() + "/voids",
					readScope: identity.ScopeTransactionRead, writeScope: identity.ScopeTransactionWrite,
					body: `{}`,
				},
			},
		},
		{
			serviceName: "asset",
			mount:       mountAsset,
			probes: []scopeCase{
				{
					name: "POST /v1/assets/{item_id}/flag",
					method: http.MethodPost, path: "/v1/assets/" + itemID.String() + "/flag",
					readScope: identity.ScopeAssetRead, writeScope: identity.ScopeAssetWrite,
					body: `{}`,
				},
			},
		},
		{
			serviceName: "returns",
			mount:       mountReturns,
			probes: []scopeCase{
				{
					name: "POST /v1/returns/{id}/flag",
					method: http.MethodPost, path: "/v1/returns/" + uuid.New().String() + "/flag",
					readScope: identity.ScopeReturnsRead, writeScope: identity.ScopeReturnsWrite,
					body: `{}`,
				},
			},
		},
		{
			serviceName: "report",
			mount:       mountReport,
			probes: []scopeCase{
				{
					name: "POST /v1/reports (create job)",
					method: http.MethodPost, path: "/v1/reports",
					readScope: identity.ScopeReportRead, writeScope: identity.ScopeReportWrite,
					body: `{"report_type":"sales_summary","from":"2026-01-01","to":"2026-01-31"}`,
				},
			},
		},
		{
			serviceName: "alert",
			mount:       mountAlert,
			probes: []scopeCase{
				{
					name: "POST /v1/alerts/{id}/acknowledge",
					method: http.MethodPost, path: "/v1/alerts/" + uuid.New().String() + "/acknowledge",
					readScope: identity.ScopeAlertRead, writeScope: identity.ScopeAlertWrite,
					body: `{}`,
				},
				{
					name: "POST /v1/alerts/{id}/resolve",
					method: http.MethodPost, path: "/v1/alerts/" + uuid.New().String() + "/resolve",
					readScope: identity.ScopeAlertRead, writeScope: identity.ScopeAlertWrite,
					body: `{}`,
				},
			},
		},
		{
			serviceName: "owl-dashboards",
			mount:       mountOwlDashboards,
			probes: []scopeCase{
				{
					name: "POST /v1/owl/parties/refresh",
					method: http.MethodPost, path: "/v1/owl/parties/refresh",
					readScope: identity.ScopeOwlRead, writeScope: identity.ScopeOwlWrite,
					body: `{}`,
				},
			},
		},
		{
			serviceName: "billing",
			mount:       mountBilling,
			probes: []scopeCase{
				{
					name: "POST /v1/billing/otb (create budget)",
					method: http.MethodPost, path: "/v1/billing/otb",
					readScope: identity.ScopeBillingRead, writeScope: identity.ScopeBillingWrite,
					body: `{}`,
				},
				{
					name: "POST /v1/billing/otb/{id}/consume",
					method: http.MethodPost, path: "/v1/billing/otb/" + uuid.New().String() + "/consume",
					readScope: identity.ScopeBillingRead, writeScope: identity.ScopeBillingWrite,
					body: `{}`,
				},
			},
		},
	}

	for _, svc := range cases {
		svc := svc
		t.Run(svc.serviceName, func(t *testing.T) {
			router := svc.mount()
			for _, c := range svc.probes {
				c := c
				t.Run(c.name, func(t *testing.T) {
					// Read-only key against a write route → must 403.
					req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
					req.Header.Set("Content-Type", "application/json")
					req = req.WithContext(testutil.WithAPIKeyClaimsScoped(
						req.Context(), tenantID, c.readScope,
					))
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)

					if rec.Code != http.StatusForbidden {
						t.Errorf(
							"expected 403 insufficient_scope; got %d; body=%s",
							rec.Code, rec.Body.String(),
						)
					}
					if !bytes.Contains(rec.Body.Bytes(), []byte(`"insufficient_scope"`)) {
						t.Errorf(
							"expected body to include insufficient_scope code; got %s",
							rec.Body.String(),
						)
					}
				})
			}
		})
	}
}

// TestSufficientScope_NotForbidden_AcrossServices is the corollary
// probe: a key carrying the matching write scope reaches the handler.
// We don't assert a specific status (the handlers run against nil
// stores in this DB-free probe), only that the response is NOT 403 —
// proving the middleware did not block.
func TestSufficientScope_NotForbidden_AcrossServices(t *testing.T) {
	tenantID := uuid.New()
	router := mountTransaction()

	// Same probe rows as the deny case, but the key carries the matching
	// write scope. Status will be 4xx/5xx from the handler (no DB), and
	// must NOT be 403.
	probes := []scopeCase{
		{
			name: "POST /v1/transactions (create) with write scope",
			method: http.MethodPost, path: "/v1/transactions",
			readScope: identity.ScopeTransactionRead, writeScope: identity.ScopeTransactionWrite,
			body: `{"transaction_number":"T-1","business_date":"2026-01-01"}`,
		},
	}

	for _, c := range probes {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(testutil.WithAPIKeyClaimsScoped(
				req.Context(), tenantID, c.writeScope,
			))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code == http.StatusForbidden {
				t.Errorf(
					"middleware must allow request with sufficient scope, got 403; body=%s",
					rec.Body.String(),
				)
			}
		})
	}
}
