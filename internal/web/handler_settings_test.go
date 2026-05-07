package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	lpPkg "github.com/growdirect-llc/rapidpos/internal/lp"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

// TestSettingsDrawerPage_Renders verifies the store/drawer settings page
// returns 200 with no stores wired (inline lambda stub path).
func TestSettingsDrawerPage_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/store/drawer", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsAllowlistDeadCount_Renders verifies the allowlist/dead-count page
// returns 200 with LP stores wired.
func TestSettingsAllowlistDeadCount_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{
		SubstrateStore: lpPkg.NewSubstrateStore(pool),
		AllowListStore: lpPkg.NewAllowListStore(pool),
	}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/allowlist/dead-count", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsAllowlistDiscounts_Renders verifies the allowlist/discounts page.
func TestSettingsAllowlistDiscounts_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/allowlist/discounts", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsAllowlistVoids_Renders verifies the allowlist/voids page.
func TestSettingsAllowlistVoids_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/allowlist/voids", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsStoreVoidReasons_Renders verifies the store/void-reasons page.
func TestSettingsStoreVoidReasons_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/store/void-reasons", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsStoreCompReasons_Renders verifies the store/comp-reasons page.
func TestSettingsStoreCompReasons_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/store/comp-reasons", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSettingsTrainingMode_Renders verifies the training-mode page.
func TestSettingsTrainingMode_Renders(t *testing.T) {
	deps := web.Deps{}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/training-mode", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", rr.Code, rr.Body.String())
	}
}
