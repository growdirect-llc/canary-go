package billing

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestRenderStoreErrMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrValidation, http.StatusBadRequest},
		{ErrHardLimitHit, http.StatusPaymentRequired},
		{errors.New("random"), http.StatusInternalServerError},
	}
	h := New(nil, nil)
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.renderStoreErr(w, c.err, "test")
		if w.Code != c.want {
			t.Errorf("err=%v: got %d want %d", c.err, w.Code, c.want)
		}
	}
}

func TestTenantFromQuery(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		query string
		ok    bool
	}{
		{"?tenant_id=" + id.String(), true},
		{"?merchant_id=" + id.String(), true},
		{"", false},
		{"?tenant_id=not-a-uuid", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/v1/billing/otb"+c.query, nil)
		w := httptest.NewRecorder()
		_, ok := tenantFromQuery(w, req)
		if ok != c.ok {
			t.Errorf("query %q: got ok=%v, want %v (status=%d)", c.query, ok, c.ok, w.Code)
		}
	}
}

func TestMountRegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"POST /v1/billing/otb":              true,
		"GET /v1/billing/otb":               true,
		"GET /v1/billing/otb/{id}":          true,
		"POST /v1/billing/otb/{id}/consume": true,
		"GET /v1/billing/cost-rollup":       true,
	}
	got := map[string]bool{}
	walker := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	if err := chi.Walk(r, walker); err != nil {
		t.Fatalf("walk: %v", err)
	}
	for key := range want {
		if !got[key] {
			t.Errorf("missing route: %s; have: %v", key, got)
		}
	}
}
