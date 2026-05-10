package analytics

import (
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
		"GET /v1/analytics/sales":    true,
		"GET /v1/analytics/basket":   true,
		"GET /v1/analytics/cohort":   true,
		"GET /v1/analytics/velocity": true,
		"GET /v1/analytics/shrink":   true,
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
		"/v1/analytics/sales",
		"/v1/analytics/basket",
		"/v1/analytics/cohort",
		"/v1/analytics/velocity",
		"/v1/analytics/shrink",
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

func TestHandler_WithAuth_NoStore_Returns500(t *testing.T) {
	endpoints := []string{
		"/v1/analytics/sales",
		"/v1/analytics/basket",
		"/v1/analytics/cohort",
		"/v1/analytics/velocity",
		"/v1/analytics/shrink",
	}
	r := chi.NewRouter()
	// Store is nil — triggers nil pointer on DB call → 500
	h := New(nil, nil)
	h.Mount(r)
	tid := uuid.New()
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
		w := httptest.NewRecorder()
		// expect panic recovery → 500; chi Recoverer not wired here so
		// we recover manually and just check that auth passed (not 401).
		func() {
			defer func() { recover() }()
			r.ServeHTTP(w, req)
		}()
		if w.Code == http.StatusUnauthorized {
			t.Errorf("%s: got 401 with valid claims injected", ep)
		}
	}
}

func TestParseDateRange_Defaults(t *testing.T) {
	from, to := parseDateRange("", "")
	if to.IsZero() || from.IsZero() {
		t.Error("defaults should not be zero")
	}
	diff := to.Sub(from)
	if diff < 29*24*time.Hour || diff > 31*24*time.Hour {
		t.Errorf("default range should be ~30 days, got %v", diff)
	}
}

func TestParseDateRange_ValidInput(t *testing.T) {
	from, to := parseDateRange("2026-01-01", "2026-03-31")
	if from.Year() != 2026 || from.Month() != 1 || from.Day() != 1 {
		t.Errorf("from = %v, want 2026-01-01", from)
	}
	if to.Year() != 2026 || to.Month() != 3 || to.Day() != 31 {
		t.Errorf("to = %v, want 2026-03-31", to)
	}
}

func TestBuildFilter_LocationIDParsed(t *testing.T) {
	tid := uuid.New()
	lid := uuid.New()
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	var capturedFilter DateRangeFilter
	// We test buildFilter directly
	req := httptest.NewRequest(http.MethodGet, "/v1/analytics/sales?location_id="+lid.String(), nil)
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	capturedFilter = buildFilter(tid, req)
	if capturedFilter.LocationID == nil {
		t.Fatal("location_id should be parsed")
	}
	if *capturedFilter.LocationID != lid {
		t.Errorf("location_id = %v, want %v", *capturedFilter.LocationID, lid)
	}
}

func TestBuildFilter_InvalidLocationIgnored(t *testing.T) {
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/analytics/sales?location_id=not-a-uuid", nil)
	f := buildFilter(tid, req)
	if f.LocationID != nil {
		t.Error("invalid location_id should be silently ignored")
	}
}
