package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestAssetsList_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/assets", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Assets", "No assets yet"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAssetsList_FilterParams(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, q := range []string{"", "?status=active", "?status=discontinued&low_stock=1", "?limit=200"} {
		req := httptest.NewRequest(http.MethodGet, "/assets"+q, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("query=%q: expected 200 got %d", q, rr.Code)
		}
	}
}

func TestAssetDetail_NoStore_RendersStub(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/assets/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Positions by Location") {
		t.Errorf("expected positions section header")
	}
}

func TestAssetDetail_BadID_404(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/assets/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

func TestBillingOverview_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/billing/overview", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Billing", "Overview", "No active OTB budgets"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestBillingInvoices_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/billing/invoices", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Invoices", "Pending Invoice"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestBillingPaymentMethod_RendersUnconnected(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/billing/payment-method", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Payment Method", "No wallet bound"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}
