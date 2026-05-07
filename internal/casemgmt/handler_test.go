package casemgmt

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

func TestTenantFromQueryAcceptsBothNames(t *testing.T) {
	id := uuid.New()
	for _, name := range []string{"tenant_id", "merchant_id"} {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/cases?"+name+"="+id.String(), nil)
		w := httptest.NewRecorder()
		got, ok := tenantFromQuery(w, req)
		if !ok || got != id {
			t.Errorf("query %s: got (%v, %v)", name, got, ok)
		}
	}
}

func TestMountRegistersAllRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"POST /v1/cases":                  true,
		"GET /v1/cases":                   true,
		"GET /v1/cases/{id}":              true,
		"POST /v1/cases/{id}/actions":     true,
		"GET /v1/cases/{id}/actions":      true,
		"POST /v1/cases/{id}/evidence":    true,
		"GET /v1/cases/{id}/evidence":     true,
		"POST /v1/cases/{id}/close":       true,
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
			t.Errorf("missing route: %s", key)
		}
	}
}
