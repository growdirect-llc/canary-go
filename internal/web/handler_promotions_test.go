//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/growdirect-llc/rapidpos/internal/pricing"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

func TestPromotionsCalendar_Renders_WithStore(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{PricingStore: pricing.NewPgxStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/promotions", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPromotionsCalendar_NoStore_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/promotions", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
}
