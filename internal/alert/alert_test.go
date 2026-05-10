package alert

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/testutil"
)

func TestThreshold_BelowThreshold(t *testing.T) {
	cfg := ThresholdConfig{ThresholdCount: 3, ThresholdWindowMinutes: 60}
	fire, _ := EvaluateThreshold(cfg, 2, "medium")
	if fire {
		t.Error("count=2 < threshold=3 should not fire")
	}
}

func TestThreshold_AtThreshold(t *testing.T) {
	cfg := ThresholdConfig{ThresholdCount: 3, ThresholdWindowMinutes: 60}
	fire, sev := EvaluateThreshold(cfg, 3, "medium")
	if !fire {
		t.Error("count=3 == threshold=3 should fire")
	}
	if sev != "medium" {
		t.Errorf("severity = %q want medium", sev)
	}
}

func TestThreshold_SeverityOverride(t *testing.T) {
	cfg := ThresholdConfig{
		ThresholdCount:         1,
		ThresholdWindowMinutes: 60,
		SeverityOverrides:      map[string]int{"high": 5, "critical": 10},
	}
	_, sev := EvaluateThreshold(cfg, 7, "medium")
	if sev != "high" {
		t.Errorf("count=7 should override to high, got %q", sev)
	}
	_, sev = EvaluateThreshold(cfg, 10, "medium")
	if sev != "critical" {
		t.Errorf("count=10 should override to critical, got %q", sev)
	}
}

func TestThreshold_DefaultConfig(t *testing.T) {
	cfg := DefaultThresholdConfig()
	if cfg.ThresholdCount != 1 {
		t.Errorf("default threshold_count = %d, want 1", cfg.ThresholdCount)
	}
	if cfg.ThresholdWindowMinutes != 60 {
		t.Errorf("default window = %d, want 60", cfg.ThresholdWindowMinutes)
	}
}

func TestParseThresholdConfig_Partial(t *testing.T) {
	raw := []byte(`{"threshold_count": 5}`)
	cfg := ParseThresholdConfig(raw)
	if cfg.ThresholdCount != 5 {
		t.Errorf("threshold_count = %d, want 5", cfg.ThresholdCount)
	}
	if cfg.ThresholdWindowMinutes != 60 {
		t.Errorf("window minutes should default to 60, got %d", cfg.ThresholdWindowMinutes)
	}
}

func TestParseThresholdConfig_Empty(t *testing.T) {
	cfg := ParseThresholdConfig(nil)
	if cfg.ThresholdCount != 1 || cfg.ThresholdWindowMinutes != 60 {
		t.Error("nil input should produce defaults")
	}
}

func TestSeverityRank(t *testing.T) {
	cases := []struct{ s string; want int }{
		{"low", 1}, {"medium", 2}, {"high", 3}, {"critical", 4}, {"unknown", 0},
	}
	for _, c := range cases {
		if got := SeverityRank(c.s); got != c.want {
			t.Errorf("SeverityRank(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestHandlerMount_RegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)

	want := map[string]bool{
		"GET /v1/alerts":                      true,
		"GET /v1/alerts/stats":                true,
		"GET /v1/alerts/{id}":                 true,
		"POST /v1/alerts/{id}/acknowledge":    true,
		"POST /v1/alerts/{id}/resolve":        true,
		"POST /v1/alerts/{id}/suppress":       true,
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

func TestHandlerGet_MalformedID(t *testing.T) {
	r := chi.NewRouter()
	h := New(nil, nil)
	h.Mount(r)
	// Inject valid tenant claims so auth passes; ID parse fires next.
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/alerts/not-a-uuid", nil)
	req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tid))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
