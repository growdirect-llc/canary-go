//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ruptiv/canary/internal/casemgmt"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/transaction"
	"github.com/ruptiv/canary/internal/web"
)

func TestReportFinance_Renders_WithStore(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{TransactionStore: transaction.NewStore(pool)}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/reports/finance", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportFinance_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/reports/finance", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}

func TestReportCases_Renders_WithStore(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{CaseStore: casemgmt.NewStore(pool)}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/reports/cases", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestReportCases_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/reports/cases", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}
