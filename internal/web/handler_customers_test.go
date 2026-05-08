//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/customer"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/web"
)

// TestCustomerListPage_Renders verifies the customers list page returns 200
// with the expected heading when no search query is provided (empty state).
func TestCustomerListPage_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<h1>Customers</h1>") {
		t.Errorf("expected customers heading in body; got:\n%s", rr.Body.String()[:min(500, rr.Body.Len())])
	}
}

// TestCustomerListPage_SearchQuery verifies the page renders 200 with a search
// query parameter. The nil tenant means results will be empty, but the handler
// must not panic and the search form should reflect the query value.
func TestCustomerListPage_SearchQuery(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers?q=john", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "john") {
		t.Errorf("expected query value 'john' reflected in body")
	}
}

// TestCustomerListPage_NoStore verifies the customers list page renders even
// when no CustomerStore is wired (nil-store fallback path).
func TestCustomerListPage_NoStore(t *testing.T) {
	deps := web.Deps{}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}

// TestCustomerDetailPage_UnknownID_Returns404 verifies that a well-formed UUID
// that doesn't exist in the DB returns 404.
func TestCustomerDetailPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

// TestCustomerDetailPage_BadID_Returns404 verifies that a malformed (non-UUID)
// path parameter returns 404 rather than 500.
func TestCustomerDetailPage_BadID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

// TestCustomerRiskPage_UnknownID_Returns404 verifies the risk sub-page also 404s.
func TestCustomerRiskPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers/"+uuid.New().String()+"/risk", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

// TestCustomerContextPage_UnknownID_Returns404 verifies the context sub-page also 404s.
func TestCustomerContextPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := customer.NewStore(pool)
	deps := web.Deps{CustomerStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/customers/"+uuid.New().String()+"/context", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
