package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// TestOwlPortalPeriod_Day pins a known UTC midpoint and verifies the
// helper truncates to midnight UTC.
func TestOwlPortalPeriod_Day(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	from, to, label := owlPortalPeriod("day", now)

	if !from.Equal(time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day from = %v want 2026-05-06 00:00 UTC", from)
	}
	if !to.Equal(now) {
		t.Errorf("day to = %v want %v", to, now)
	}
	if label != "day" {
		t.Errorf("label = %q want %q", label, "day")
	}
}

// TestOwlPortalPeriod_Week_Wednesday — ISO week starts Monday.
func TestOwlPortalPeriod_Week_Wednesday(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC) // Wed
	from, _, _ := owlPortalPeriod("week", now)
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // prev Mon
	if !from.Equal(want) {
		t.Errorf("week from = %v want %v", from, want)
	}
}

// TestOwlPortalPeriod_Week_Sunday — Sunday rolls back to the
// preceding Monday (not forward to tomorrow's Monday).
func TestOwlPortalPeriod_Week_Sunday(t *testing.T) {
	now := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC) // Sun
	from, _, _ := owlPortalPeriod("week", now)
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // prev Mon
	if !from.Equal(want) {
		t.Errorf("sun: week from = %v want %v", from, want)
	}
}

func TestOwlPortalPeriod_Month(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	from, _, _ := owlPortalPeriod("month", now)
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !from.Equal(want) {
		t.Errorf("month from = %v want %v", from, want)
	}
}

// TestOwlPortalPeriod_Quarter — covers all four calendar quarters.
func TestOwlPortalPeriod_Quarter(t *testing.T) {
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, // Q1
		{time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},  // Q2
		{time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}, // Q3
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, // Q4
	}
	for _, c := range cases {
		from, _, _ := owlPortalPeriod("quarter", c.now)
		if !from.Equal(c.want) {
			t.Errorf("quarter(now=%v) from = %v want %v", c.now, from, c.want)
		}
	}
}

// TestOwlPortalPeriod_Default — empty + unknown both fall through to week.
func TestOwlPortalPeriod_Default(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	if _, _, label := owlPortalPeriod("", now); label != "week" {
		t.Errorf("empty kind label = %q want week", label)
	}
	if _, _, label := owlPortalPeriod("nonsense", now); label != "week" {
		t.Errorf("unknown kind label = %q want week", label)
	}
}

// TestOwlDashboards_NoStore_RendersEmptyState — dashboards page renders
// with empty-state copy when the store is nil.
func TestOwlDashboards_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/owl/dashboards", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Owl", "Dashboards", "No LP-rate data yet", "No party data yet"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestOwlDashboards_PeriodSelector — each period parses + renders the
// active pill.
func TestOwlDashboards_PeriodSelector(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, period := range []string{"day", "week", "month", "quarter"} {
		req := httptest.NewRequest(http.MethodGet, "/owl/dashboards?period="+period, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("period=%s: expected 200 got %d", period, rr.Code)
			continue
		}
		body := rr.Body.String()
		// Each pill renders with data-period="<kind>". The active pill gets
		// the "active" class — find the data-period attribute, then check
		// that "period-pill active" appears within the same anchor tag.
		needle := `data-period="` + period + `"`
		idx := strings.Index(body, needle)
		if idx < 0 {
			t.Errorf("period=%s: data-period attribute not in body", period)
			continue
		}
		// 200 chars is enough to reach the class attribute on the same tag.
		end := idx + 200
		if end > len(body) {
			end = len(body)
		}
		if !strings.Contains(body[idx:end], "period-pill active") {
			t.Errorf("period=%s: active class not on selected pill", period)
		}
	}
}

// TestOwlParties_NoStore_RendersEmptyState — parties page renders the
// empty-state copy when the store is nil.
func TestOwlParties_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/owl/parties", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "No parties tracked yet") {
		t.Errorf("expected empty-state copy")
	}
}

// TestOwlParties_LimitClamp — limit is clamped + falls back; never errors.
func TestOwlParties_LimitClamp(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, q := range []string{"limit=9999", "limit=abc", "limit=0", "limit=25", ""} {
		req := httptest.NewRequest(http.MethodGet, "/owl/parties?"+q, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("query=%q: expected 200 got %d", q, rr.Code)
		}
	}
}

// TestOwlLPPerformance_NoStore_RendersEmptyState — LP-perf page renders
// the empty-state copy with KPI tiles at 0.
func TestOwlLPPerformance_NoStore_RendersEmptyState(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/owl/lp-performance", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"LP Performance", "Per Rule Type", "No LP-rate data yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestOwlLPPerformance_PeriodSelector — every period renders 200.
func TestOwlLPPerformance_PeriodSelector(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, period := range []string{"day", "week", "month", "quarter"} {
		req := httptest.NewRequest(http.MethodGet, "/owl/lp-performance?period="+period, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("period=%s: expected 200 got %d", period, rr.Code)
		}
	}
}
