package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestAdminAudit_NoStore_RendersEmptyState(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Audit Log", "No audit rows yet"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAdminAudit_FilterParams(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, q := range []string{"", "?source=square", "?action=POST%20%2Fv1%2Fwebhook", "?limit=250"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/audit"+q, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("query=%q: expected 200 got %d", q, rr.Code)
		}
	}
}

func TestAdminISO27001_RendersControls(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/iso27001", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"ISO 27001", "Annex", "A.5", "A.8"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAdminUsers_RendersBlocked(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "GRO-769") {
		t.Errorf("expected dependency note on identity middleware")
	}
}

func TestAdminConfig_RendersSections(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Config Health", "N.1", "N.2", "N.3"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestReportTax_RendersBlocked(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/tax", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "tax_amount or tax_collections") {
		t.Errorf("expected schema-pending note")
	}
}
