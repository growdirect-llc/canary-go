package owl

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestNewDashboardHandlerNilLogger(t *testing.T) {
	h := NewDashboardHandler(nil, nil)
	if h == nil || h.Logger == nil {
		t.Fatal("expected non-nil handler + logger fallback")
	}
}

func TestTenantFromQueryAcceptsBothNames(t *testing.T) {
	id := uuid.New()
	for _, name := range []string{"tenant_id", "merchant_id"} {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/owl/parties?"+name+"="+id.String(), nil)
		w := httptest.NewRecorder()
		got, ok := tenantFromQuery(w, req)
		if !ok || got != id {
			t.Errorf("query %s: got (%v, %v)", name, got, ok)
		}
	}
}

func TestTenantFromQueryRejectsMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/owl/parties", nil)
	w := httptest.NewRecorder()
	_, ok := tenantFromQuery(w, req)
	if ok {
		t.Fatal("expected missing tenant rejection")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", w.Code)
	}
}

func TestMountRegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := NewDashboardHandler(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"GET /v1/owl/parties":             true,
		"GET /v1/owl/parties/{id}/rfm":    true,
		"POST /v1/owl/parties/refresh":    true,
		"GET /v1/owl/lp-rate":             true,
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

func TestErrNotFoundIsSentinel(t *testing.T) {
	wrapped := errors.Join(ErrNotFound, errors.New("downstream"))
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("ErrNotFound should match through errors.Join")
	}
}
