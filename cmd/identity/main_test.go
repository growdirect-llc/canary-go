//go:build integration

// cmd/identity/main_test.go
//
// Pre-Loop 2 Tier-3 dispatch (GRO-761) gated these tests under the
// integration build tag because testServer() calls config.Load() which
// require()s DATABASE_URL/VALKEY_URL/INTERNAL_SERVICE_SECRET/SESSION_SECRET
// — those panic in the default test invocation. Opt-in only until
// the proper test-setup refactor lands.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/auth"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/db"
	"github.com/redis/go-redis/v9"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Load("canary-identity")
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("db connect: %v", err)
	}
	t.Cleanup(pool.Close)

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		t.Fatalf("parse valkey url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })

	return NewServer(pool, rdb, cfg)
}

func TestHealthEndpoint(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d want 200", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("health.ok: got %v want true", resp["ok"])
	}
	checks, _ := resp["checks"].(map[string]interface{})
	if checks["database"] != "ok" {
		t.Errorf("health.checks.database: got %v want ok", checks["database"])
	}
}

func TestSessionValidate_MissingBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions/validate", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("validate missing: got %d want 401", w.Code)
	}
}

func TestSessionValidate_ExpiredToken(t *testing.T) {
	srv := testServer(t)
	cfg := config.Load("canary-identity")

	token, _ := auth.SignToken(cfg.SessionSecret, uuid.New(), uuid.New(), []string{"owner"}, -1*time.Second)

	body, _ := json.Marshal(map[string]string{"token": token})
	req := httptest.NewRequest(http.MethodPost, "/sessions/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired: got %d want 401", w.Code)
	}
}

func TestSessionValidate_ValidToken_NotInValkey(t *testing.T) {
	srv := testServer(t)
	cfg := config.Load("canary-identity")

	// A cryptographically valid JWT that was never registered in Valkey
	token, _ := auth.SignToken(cfg.SessionSecret, uuid.New(), uuid.New(), []string{"owner"}, time.Hour)

	body, _ := json.Marshal(map[string]string{"token": token})
	req := httptest.NewRequest(http.MethodPost, "/sessions/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	// Valid JWT but no Valkey session = invalid (revocation check)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("valid-jwt-no-valkey: got %d want 401", w.Code)
	}
}

