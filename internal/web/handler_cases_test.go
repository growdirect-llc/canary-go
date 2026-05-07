package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/casemgmt"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

// TestHawkListPage_Renders verifies the hawk case list page handler mounts
// correctly, connects to the real store, and returns 200 with the expected
// heading. Empty results are fine — tenantIDFromCtx returns uuid.Nil.
func TestHawkListPage_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := casemgmt.NewStore(pool)
	deps := web.Deps{CaseStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/cases/hawk", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Cases") {
		t.Errorf("expected 'Cases' heading in body")
	}
}

// TestHawkDetailPage_UnknownID_Returns404 verifies that a non-existent case ID
// returns 404.
func TestHawkDetailPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := casemgmt.NewStore(pool)
	deps := web.Deps{CaseStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/cases/hawk/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

// TestHawkEvidencePage_UnknownID_Returns404 verifies that the evidence page for
// a non-existent case ID returns 404.
func TestHawkEvidencePage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := casemgmt.NewStore(pool)
	deps := web.Deps{CaseStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/cases/hawk/"+uuid.New().String()+"/evidence", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}
