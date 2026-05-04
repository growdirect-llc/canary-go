package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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
