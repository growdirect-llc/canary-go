package employee

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/identity"
)

func TestHandlerMount_RegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"GET /v1/employees":                  true,
		"GET /v1/employees/alert-summary":    true,
		"GET /v1/employees/{id}":             true,
		"GET /v1/employees/{id}/alerts":      true,
	}
	got := map[string]bool{}
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	for k := range want {
		if !got[k] {
			t.Errorf("missing route: %s", k)
		}
	}
}

func TestHandler_NoAuth_Returns401(t *testing.T) {
	endpoints := []string{
		"/v1/employees",
		"/v1/employees/alert-summary",
		"/v1/employees/" + uuid.New().String(),
		"/v1/employees/" + uuid.New().String() + "/alerts",
	}
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", ep, w.Code)
		}
	}
}

func TestHandlerGet_MalformedID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/employees/not-a-uuid", nil)
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandlerAlerts_MalformedID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/employees/not-a-uuid/alerts", nil)
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
