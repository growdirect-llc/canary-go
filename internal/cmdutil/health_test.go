package cmdutil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler_NoDeps_Returns200WithVersionAndService(t *testing.T) {
	h := HealthHandler("asset")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["service"] != "asset" {
		t.Errorf("service = %v, want asset", body["service"])
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if v, ok := body["version"].(string); !ok || v == "" || v == "1.0.0" {
		t.Errorf("version = %v, want non-empty non-placeholder", body["version"])
	}
}

func TestHealthHandler_AllDepsOk_Returns200(t *testing.T) {
	okCheck := HealthCheck{
		Name:  "db",
		Check: func(ctx context.Context) error { return nil },
	}
	h := HealthHandler("asset", okCheck)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("checks = %v, want map", body["checks"])
	}
	if checks["db"] != "ok" {
		t.Errorf("checks.db = %v, want ok", checks["db"])
	}
}

func TestHealthHandler_AnyDepFails_Returns503(t *testing.T) {
	wantErr := errors.New("connection refused")
	deps := []HealthCheck{
		{Name: "db", Check: func(ctx context.Context) error { return nil }},
		{Name: "valkey", Check: func(ctx context.Context) error { return wantErr }},
	}
	h := HealthHandler("asset", deps...)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["db"] != "ok" {
		t.Errorf("checks.db = %v, want ok", checks["db"])
	}
	if vk, _ := checks["valkey"].(string); vk == "" || vk == "ok" {
		t.Errorf("checks.valkey = %v, want a non-ok error description", vk)
	}
}

func TestHealthHandler_RequestContextDeadlineRespected(t *testing.T) {
	// A check that observes context deadline and returns its error
	// should not stall the response when the request context is
	// cancelled.
	deps := []HealthCheck{
		{Name: "slow", Check: func(ctx context.Context) error { return ctx.Err() }},
	}
	h := HealthHandler("asset", deps...)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	req := httptest.NewRequest(http.MethodGet, "/health", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when ctx cancelled", rr.Code)
	}
}
