package asset

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/testutil"
)

func TestHandlerMount_RegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"GET /v1/assets":                true,
		"GET /v1/assets/shrink":         true,
		"GET /v1/assets/{item_id}":      true,
		"POST /v1/assets/{item_id}/flag": true,
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
		{http.MethodGet, "/v1/assets"},
		{http.MethodGet, "/v1/assets/shrink"},
		{http.MethodGet, "/v1/assets/" + uuid.New().String()},
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

func TestHandlerGet_MalformedItemID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/assets/not-a-uuid", nil)
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandlerFlag_MalformedItemID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	body := `{"location_id":"` + uuid.New().String() + `","quantity_delta":-1,"reason_code":"theft"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/assets/not-a-uuid/flag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandlerFlag_MissingLocationID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	itemID := uuid.New()
	// location_id is zero UUID (not set)
	body := `{"quantity_delta":-1,"reason_code":"theft"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/assets/"+itemID.String()+"/flag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "missing_location_id" {
		t.Errorf("code = %q, want missing_location_id", resp["code"])
	}
}

func TestHandlerFlag_MissingReasonCode(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	itemID := uuid.New()
	body := `{"location_id":"` + uuid.New().String() + `","quantity_delta":-1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/assets/"+itemID.String()+"/flag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "missing_reason_code" {
		t.Errorf("code = %q, want missing_reason_code", resp["code"])
	}
}

func TestHandlerList_MalformedLocationID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/assets?location_id=bad", nil)
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDateRange_Defaults(t *testing.T) {
	from, to := dateRange("", "")
	if to.IsZero() || from.IsZero() {
		t.Error("defaults should not be zero")
	}
	diff := to.Sub(from)
	if diff < 29*24*time.Hour || diff > 31*24*time.Hour {
		t.Errorf("default range should be ~30 days, got %v", diff)
	}
}

func TestDateRange_ValidInput(t *testing.T) {
	from, to := dateRange("2026-01-01", "2026-03-31")
	if from.Year() != 2026 || from.Month() != 1 || from.Day() != 1 {
		t.Errorf("from = %v, want 2026-01-01", from)
	}
	if to.Year() != 2026 || to.Month() != 3 || to.Day() != 31 {
		t.Errorf("to = %v, want 2026-03-31", to)
	}
}
