package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestW16_ExceptionDetail_NoStore_RendersStub(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/exceptions/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}

func TestW16_CasesEvidence_NoStore_RendersDomainCounts(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.NewString()+"/evidence", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	// Domain count buckets render even when there's no evidence.
	if !strings.Contains(rr.Body.String(), "Evidence") {
		t.Errorf("expected evidence section header")
	}
}

func TestW16_CasesCorrelation_NoStore_RendersRelatedCasesEmpty(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.NewString()+"/correlation", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Subject Correlation", "Related Cases", "ML correlation is W16+ follow-on"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestW16_CasesRemediate_NoStore_RendersCatalog(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.NewString()+"/remediate", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"open_three_way_match", "create_directed_task", "lock_otb_period", "flag_inventory_loss"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing remediation code %q", want)
		}
	}
}

func TestClassifyEvidenceDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"alert", "lp"},
		{"detection", "lp"},
		{"chirp", "lp"},
		{"inventory_movement", "inventory"},
		{"inventory_position", "inventory"},
		{"inventory_document", "inventory"},
		{"transaction", "finance"},
		{"tender", "finance"},
		{"refund", "finance"},
		{"goods_receipt", "receiving"},
		{"transfer", "receiving"},
		{"rtv", "receiving"},
		{"unknown", "lp"},
	}
	for _, c := range cases {
		v := c.in
		got := classifyEvidenceDomain(&v)
		if got != c.want {
			t.Errorf("classifyEvidenceDomain(%q) = %q want %q", c.in, got, c.want)
		}
	}
	// nil → lp
	if got := classifyEvidenceDomain(nil); got != "lp" {
		t.Errorf("nil = %q want lp", got)
	}
}
