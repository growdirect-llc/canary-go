package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestNewTracerNoOpDefault — when OTEL_ENABLED is unset and the OTLP
// endpoint is empty, NewTracer returns a no-op tracer with a no-op
// shutdown. This is the test-environment posture.
func TestNewTracerNoOpDefault(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tr, err := NewTracer(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	if tr == nil || tr.Tracer == nil {
		t.Fatal("expected non-nil tracer + Tracer")
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestNewTracerDisabled — explicit OTEL_ENABLED=false short-circuits to
// the no-op path even with a configured endpoint.
func TestNewTracerDisabled(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	tr, err := NewTracer(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil tracer")
	}
}

func TestSampleRateDefaults(t *testing.T) {
	cases := []struct {
		env  string
		rate float64
		want float64
	}{
		{"", 0, 1.0},
		{"prod", 0, 0.1},
		{"production", 0, 0.1},
		{"staging", 0, 0.1},
		{"dev", 0, 1.0},
	}
	for _, c := range cases {
		os.Unsetenv("OTEL_SAMPLE_RATE")
		t.Setenv("ENV", c.env)
		if got := sampleRate(); got != c.want {
			t.Errorf("ENV=%q: sampleRate()=%v want %v", c.env, got, c.want)
		}
	}
}

func TestSampleRateOverride(t *testing.T) {
	t.Setenv("OTEL_SAMPLE_RATE", "0.25")
	if got := sampleRate(); got != 0.25 {
		t.Errorf("OTEL_SAMPLE_RATE=0.25: got %v", got)
	}
}

// TestNewLogger — exercises the constructor in both dev and prod modes.
// We don't assert on log output; we just ensure construction succeeds
// and the logger sync path doesn't panic.
func TestNewLogger(t *testing.T) {
	for _, env := range []string{"dev", "prod"} {
		t.Setenv("ENV", env)
		l := NewLogger("test-service")
		if l == nil {
			t.Fatalf("ENV=%q: NewLogger returned nil", env)
		}
	}
}

// TestMiddlewareEmitsSpan — exercises the middleware end-to-end against
// a tiny chi router and a mock handler. We assert on the recorded
// status code via the wrapped writer.
func TestMiddlewareEmitsSpan(t *testing.T) {
	r := chi.NewRouter()
	r.Use(Middleware("test-service"))
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestEnvBool(t *testing.T) {
	cases := []struct {
		raw  string
		def  bool
		want bool
	}{
		{"", true, true},
		{"", false, false},
		{"true", false, true},
		{"false", true, false},
		{"1", false, true},
		{"0", true, false},
		{"yes", false, true},
		{"no", true, false},
		{"garbage", true, true},
	}
	for _, c := range cases {
		t.Setenv("OBS_TEST_BOOL", c.raw)
		if got := envBool("OBS_TEST_BOOL", c.def); got != c.want {
			t.Errorf("envBool(%q, def=%v) = %v, want %v", c.raw, c.def, got, c.want)
		}
	}
}
