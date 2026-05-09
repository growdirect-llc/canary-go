package cmdutil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequestLogger_LogsBasicRequestFields(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	mw := RequestLogger(logger)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}
	entry := logs.All()[0]
	if entry.Message != "http" {
		t.Errorf("expected message %q, got %q", "http", entry.Message)
	}
	fields := entry.ContextMap()
	if got := fields["method"]; got != http.MethodGet {
		t.Errorf("method field = %v, want GET", got)
	}
	if got := fields["path"]; got != "/healthz" {
		t.Errorf("path field = %v, want /healthz", got)
	}
	if got := fields["status"]; got != int64(http.StatusTeapot) {
		t.Errorf("status field = %v, want 418", got)
	}
	if got := fields["bytes"]; got != int64(2) {
		t.Errorf("bytes field = %v, want 2", got)
	}
}

func TestRequestLogger_PassesRequestThrough(t *testing.T) {
	core, _ := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	mw := RequestLogger(logger)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Errorf("middleware did not invoke wrapped handler")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("response code = %d, want 200", rr.Code)
	}
}

func TestRequestLogger_StatusZeroWhenHandlerOmitsWriteHeader(t *testing.T) {
	// Deliberate: chi's WrapResponseWriter reports the status the handler
	// explicitly set. A handler that writes nothing logs status=0. This
	// matches the behavior of the original 12-service inline
	// requestLogger that this middleware replaces — a faithful refactor,
	// not a behavior change.
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	mw := RequestLogger(logger)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no WriteHeader / Write
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}
	if got := logs.All()[0].ContextMap()["status"]; got != int64(0) {
		t.Errorf("status field = %v, want 0 (unset)", got)
	}
}
