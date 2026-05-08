//go:build integration

// cmd/identity/login_test.go
//
// Integration tests for the reconciled /auth/login flow on the
// identity binary: persons table (canary_identity_gcp) + argon2id
// + mint primitive + refreshfamily ledger + /v1/me end-to-end.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity/auth"
)

func identityTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := config.Load("canary-identity")
	if cfg.IdentityDatabaseURL == "" {
		t.Fatal("IDENTITY_DATABASE_URL must be set")
	}
	pool, err := db.Connect(context.Background(), cfg.IdentityDatabaseURL)
	if err != nil {
		t.Fatalf("identity testPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedPerson(t *testing.T, pool *pgxpool.Pool, email, password string) uuid.UUID {
	t.Helper()
	store := auth.NewPersonStore(pool)
	orgID := uuid.MustParse("00000000-0000-0000-0000-0000aabbccdd")
	personID, err := store.CreatePersonWithPassword(
		context.Background(), orgID, email, password,
		"Test", "Person", "Test Person", "regular",
	)
	if err != nil {
		t.Fatalf("seedPerson: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.persons WHERE id = $1`, personID)
	})
	return personID
}

func postJSON(t *testing.T, srv http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestAuthLogin_HappyPath(t *testing.T) {
	srv := testServer(t)
	pool := identityTestPool(t)
	personID := seedPerson(t, pool, "happy@example.com", "correct-horse-battery-staple")

	w := postJSON(t, srv, "/auth/login", map[string]any{
		"email":    "happy@example.com",
		"password": "correct-horse-battery-staple",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login: got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["token_type"] != "Bearer" {
		t.Errorf("token_type: got %v", resp["token_type"])
	}
	access, _ := resp["access_token"].(string)
	if access == "" || strings.Count(access, ".") != 2 {
		t.Errorf("access shape: %q", access)
	}
	rt, _ := resp["refresh_token"].(string)
	if rt == "" || strings.Count(rt, ".") != 2 {
		// Their mint produces JWT refresh tokens (aud="refresh"),
		// not opaque rt_ secrets. Validate JWT shape.
		t.Errorf("refresh shape: %q", rt)
	}
	if resp["person_id"] != personID.String() {
		t.Errorf("person_id: got %v want %v", resp["person_id"], personID)
	}
}

func TestAuthLogin_BadPassword(t *testing.T) {
	srv := testServer(t)
	pool := identityTestPool(t)
	seedPerson(t, pool, "wrongpw@example.com", "the-real-password")
	w := postJSON(t, srv, "/auth/login", map[string]any{
		"email":    "wrongpw@example.com",
		"password": "not-the-password",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad pw: got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthLogin_EmailNotFound(t *testing.T) {
	srv := testServer(t)
	w := postJSON(t, srv, "/auth/login", map[string]any{
		"email":    "ghost-" + uuid.NewString() + "@example.com",
		"password": "whatever",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no email: got %d body=%s", w.Code, w.Body.String())
	}
}

// TestAuthLogin_MeRoundTrip — the integration ratchet for the
// reconciled stack: login mints an access token, /v1/me verifies
// it with the same keystore + reads the persons table → returns
// the AtlasView contract shape.
func TestAuthLogin_MeRoundTrip(t *testing.T) {
	srv := testServer(t)
	pool := identityTestPool(t)
	personID := seedPerson(t, pool, "round@example.com", "round-trip-pw-1234")

	loginRes := postJSON(t, srv, "/auth/login", map[string]any{
		"email":    "round@example.com",
		"password": "round-trip-pw-1234",
	})
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login: got %d body=%s", loginRes.Code, loginRes.Body.String())
	}
	var loginResp map[string]any
	_ = json.NewDecoder(loginRes.Body).Decode(&loginResp)
	access, _ := loginResp["access_token"].(string)

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/v1/me: got %d body=%s", w.Code, w.Body.String())
	}
	var meResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&meResp)
	if meResp["id"] != personID.String() {
		t.Errorf("id: got %v want %v", meResp["id"], personID)
	}
	if meResp["email"] != "round@example.com" {
		t.Errorf("email: got %v", meResp["email"])
	}
	if meResp["first_name"] != "Test" {
		t.Errorf("first_name: got %v", meResp["first_name"])
	}
	if meResp["last_name"] != "Person" {
		t.Errorf("last_name: got %v", meResp["last_name"])
	}
}

func TestAuthLogin_RefreshFamilyRecorded(t *testing.T) {
	srv := testServer(t)
	pool := identityTestPool(t)
	personID := seedPerson(t, pool, "family@example.com", "family-pw-12345")

	w := postJSON(t, srv, "/auth/login", map[string]any{
		"email":    "family@example.com",
		"password": "family-pw-12345",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login: got %d body=%s", w.Code, w.Body.String())
	}

	// Confirm a refresh family row exists in canary_gcp.app for this
	// person — this is the integration boundary between login (which
	// runs in identity_gcp) and the family ledger (which lives in
	// canary_gcp.app per migration 034).
	cfg := config.Load("canary-identity")
	canaryPool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("canary pool: %v", err)
	}
	defer canaryPool.Close()

	var count int
	if err := canaryPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM app.refresh_token_families WHERE subject = $1`,
		personID,
	).Scan(&count); err != nil {
		t.Fatalf("count families: %v", err)
	}
	if count != 1 {
		t.Errorf("family rows for new login: got %d want 1", count)
	}

	// Cleanup the family row this test created.
	t.Cleanup(func() {
		_, _ = canaryPool.Exec(context.Background(),
			`DELETE FROM app.refresh_token_families WHERE subject = $1`, personID)
	})
}
