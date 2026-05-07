package returns

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity"
)

func TestHandlerMount_RegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"GET /v1/returns":              true,
		"GET /v1/returns/summary":      true,
		"GET /v1/returns/{id}":         true,
		"POST /v1/returns/{id}/flag":   true,
		"GET /v1/returns/{id}/lines":   true,
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
	endpoints := []struct{ method, path string }{
		{http.MethodGet, "/v1/returns"},
		{http.MethodGet, "/v1/returns/summary"},
		{http.MethodGet, "/v1/returns/" + uuid.New().String()},
		{http.MethodGet, "/v1/returns/" + uuid.New().String() + "/lines"},
	}
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", ep.method, ep.path, w.Code)
		}
	}
}

func TestHandlerGet_MalformedID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/returns/not-a-uuid", nil)
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

func TestHandlerFlag_MissingDetectionRuleID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	txID := uuid.New()
	body := `{"reason":"suspicious","severity":"high","flagged_by":"` + uuid.New().String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/returns/"+txID.String()+"/flag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "missing_detection_rule_id" {
		t.Errorf("code = %q, want missing_detection_rule_id", resp["code"])
	}
}

func TestParseDateRange_Defaults(t *testing.T) {
	from, to := parseDateRange("", "")
	diff := to.Sub(from)
	if diff < 29*24*time.Hour || diff > 31*24*time.Hour {
		t.Errorf("default range ~30 days, got %v", diff)
	}
}

func TestParseDateRange_ValidInput(t *testing.T) {
	from, to := parseDateRange("2026-01-01", "2026-01-31")
	if from.Year() != 2026 || from.Month() != 1 || from.Day() != 1 {
		t.Errorf("from = %v", from)
	}
	if to.Month() != 1 || to.Day() != 31 {
		t.Errorf("to = %v", to)
	}
}
