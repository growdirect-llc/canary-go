//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/inventory"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/web"
)

// TestTransferList_Renders_NoStore — list page renders empty when no store wired.
func TestTransferList_Renders_NoStore(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transfers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "No transfers found") {
		t.Errorf("expected empty-state copy")
	}
}

// TestTransferList_Renders_WithStore — list page renders with empty result
// against a real DB (no transfers seeded for the nil tenant).
func TestTransferList_Renders_WithStore(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{InventoryStore: inventory.NewStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transfers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestTransferDetail_BadID_Returns404
func TestTransferDetail_BadID_Returns404(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transfers/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

// TestTransferDetail_NotFound_Returns404 — wired store, missing id → 404
func TestTransferDetail_NotFound_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{InventoryStore: inventory.NewStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transfers/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

// TestTransferDetail_NoStore_RendersStub — stub view when store unavailable.
func TestTransferDetail_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	id := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/transfers/"+id, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 stub got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), id[:8]) {
		t.Errorf("body missing short id")
	}
}

// TestTransferVariance_BadID_Returns404
func TestTransferVariance_BadID_Returns404(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transfers/bad/variance", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

// TestReportDistribution_Renders — distribution report renders against real DB
// with no seeded transfers (empty lanes).
func TestReportDistribution_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{InventoryStore: inventory.NewStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/distribution", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestReportInventory_Renders — inventory report renders against real DB.
func TestReportInventory_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{InventoryStore: inventory.NewStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/inventory", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestReportDistribution_NoStore_RendersStub
func TestReportDistribution_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/distribution", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}

// TestReportInventory_NoStore_RendersStub
func TestReportInventory_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/inventory", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}
