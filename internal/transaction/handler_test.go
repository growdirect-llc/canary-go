package transaction

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// renderStoreErr → status code mapping. Pure logic; no DB.

func TestRenderStoreErrMapsErrors(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrValidation, http.StatusBadRequest},
		{errors.New("random"), http.StatusInternalServerError},
	}
	h := New(nil, nil)
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.renderStoreErr(w, c.err, "test op")
		if w.Code != c.want {
			t.Errorf("err=%v: got %d want %d", c.err, w.Code, c.want)
		}
	}
}

// tenantFromQuery accepts either ?tenant_id= or ?merchant_id=.

func TestTenantFromQueryAcceptsBothNames(t *testing.T) {
	id := uuid.New()
	cases := []string{
		"?tenant_id=" + id.String(),
		"?merchant_id=" + id.String(),
	}
	for _, qs := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/transactions"+qs, nil)
		w := httptest.NewRecorder()
		got, ok := tenantFromQuery(w, req)
		if !ok || got != id {
			t.Errorf("query %q: got (%v, %v), want (%v, true)", qs, got, ok, id)
		}
	}
}

func TestTenantFromQueryRejectsMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/transactions", nil)
	w := httptest.NewRecorder()
	_, ok := tenantFromQuery(w, req)
	if ok {
		t.Fatal("expected missing tenant rejection")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", w.Code)
	}
}

// Handler.Mount registers the expected route set.

func TestMountRegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := []struct{ method, path string }{
		{http.MethodGet, "/v1/transactions/by-receipt-number"},
		{http.MethodGet, "/v1/transactions/{id}"},
		{http.MethodGet, "/v1/transactions"},
		{http.MethodPost, "/v1/transactions"},
		{http.MethodPost, "/v1/transactions/{id}/voids"},
		{http.MethodPost, "/v1/transactions/{id}/returns"},
	}
	gotRoutes := map[string]bool{}
	walker := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		gotRoutes[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	if err := chi.Walk(r, walker); err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, w := range want {
		key := w.method + " " + w.path
		if !gotRoutes[key] {
			t.Errorf("route %s not registered; have: %v", key, gotRoutes)
		}
	}
}

// CreateRequest JSON round-trip — confirms wire shape stays stable.

func TestCreateRequestRoundTrip(t *testing.T) {
	in := CreateRequest{
		TenantID:          uuid.New(),
		TransactionNumber: "T-0001",
		LocationID:        uuid.New(),
		BusinessDate:      "2026-05-03",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out CreateRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.TransactionNumber != in.TransactionNumber || out.BusinessDate != in.BusinessDate {
		t.Errorf("round-trip mismatch: in=%+v out=%+v", in, out)
	}
}
