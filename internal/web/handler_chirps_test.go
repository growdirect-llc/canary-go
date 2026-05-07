//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/chirp"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/web"
)

// TestChirpListPage_Renders verifies the chirps page handler mounts correctly,
// connects to the real store, and returns 200 with the expected heading.
// Empty results are fine — tenantIDFromCtx returns uuid.Nil.
func TestChirpListPage_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := chirp.NewPgxStore(pool)
	deps := web.Deps{ChirpStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/chirps", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Chirps") {
		t.Errorf("expected chirps heading in body")
	}
}

// TestChirpDetailPage_UnknownID_Returns404 verifies that a non-existent detection ID
// returns 404 rather than panicking or returning 500.
func TestChirpDetailPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := chirp.NewPgxStore(pool)
	deps := web.Deps{ChirpStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	nonexistentID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/chirps/"+nonexistentID, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

// TestRulesListPage_Renders verifies the rules list page handler mounts correctly,
// connects to the real store, and returns 200 with the expected heading.
// Empty results are fine — tenantIDFromCtx returns uuid.Nil.
func TestRulesListPage_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := chirp.NewPgxStore(pool)
	deps := web.Deps{ChirpStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/rules", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Detection Rules") {
		t.Errorf("expected 'Detection Rules' heading in body")
	}
}

// TestRuleDetailPage_UnknownID_Returns404 verifies that a non-existent rule ID
// returns 404.
func TestRuleDetailPage_UnknownID_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := chirp.NewPgxStore(pool)
	deps := web.Deps{ChirpStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/rules/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}
