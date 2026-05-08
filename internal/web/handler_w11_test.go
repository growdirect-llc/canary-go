package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestSuppliersList_NoStore_RendersEmptyState(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/suppliers", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Suppliers", "Add Supplier", "No suppliers yet"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSuppliersCreate_NoStore_Redirect(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	form := url.Values{"supplier_name": {"Acme"}}
	req := httptest.NewRequest(http.MethodPost, "/suppliers", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/suppliers?flash=no_store" {
		t.Errorf("unexpected redirect: %q", rr.Header().Get("Location"))
	}
}

func TestSupplierDetail_NoStore_RendersStub(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/suppliers/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Profile") {
		t.Errorf("expected profile section header")
	}
}

func TestSupplierScorecard_NoStore_RendersBlocked(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/suppliers/"+uuid.NewString()+"/scorecard", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Vendor Scorecard", "On-Time", "scorecard requires PO history"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestPOList_NoStore_RendersEmptyState(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/po", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Purchase Orders", "New PO", "No purchase orders yet"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestPOCreate_NoStore_Redirect(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	form := url.Values{"supplier_id": {uuid.NewString()}, "po_number": {"PO-001"}}
	req := httptest.NewRequest(http.MethodPost, "/po", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/po?flash=no_store" {
		t.Errorf("unexpected redirect: %q", rr.Header().Get("Location"))
	}
}

func TestPODetail_NoStore_RendersStub(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/po/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Lifecycle") {
		t.Errorf("expected lifecycle action section")
	}
}

func TestPOMatch_NoStore_RendersBlocked(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/po/"+uuid.NewString()+"/match", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Three-Way Match") {
		t.Errorf("expected three-way match heading")
	}
}

func TestPOStatus_NoStore_Redirect(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	id := uuid.NewString()
	form := url.Values{"status": {"submitted"}}
	req := httptest.NewRequest(http.MethodPost, "/po/"+id+"/status", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/po/"+id+"?flash=no_store" {
		t.Errorf("unexpected redirect: %q", rr.Header().Get("Location"))
	}
}
