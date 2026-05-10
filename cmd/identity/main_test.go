//go:build integration

// cmd/identity/main_test.go
//
// Tagged //go:build integration because testServer() calls
// config.Load() which require()s DATABASE_URL / VALKEY_URL /
// INTERNAL_SERVICE_SECRET / SESSION_SECRET — those panic in the
// default test invocation. Run via `make test` (sets the env vars)
// or with the same env vars + `go test -tags=integration ./cmd/identity/...`.
//
// GRO-763 Phase C lifted the prior Tier-3 dispatch-level exclusion;
// the file now exercises the rebuilt v1/identity/* endpoints in
// addition to the legacy /health and /sessions/validate paths.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/ruptiv/canary/internal/auth"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Load("canary-identity")
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("db connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if cfg.IdentityDatabaseURL == "" {
		t.Fatal("IDENTITY_DATABASE_URL must be set for identity integration tests")
	}
	identityPool, err := db.Connect(context.Background(), cfg.IdentityDatabaseURL)
	if err != nil {
		t.Fatalf("identity db connect: %v", err)
	}
	t.Cleanup(identityPool.Close)

	opts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		t.Fatalf("parse valkey url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { rdb.Close() })

	return NewServer(pool, identityPool, rdb, cfg, nil)
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

// ─────────────────────────────────────────────────────────────────────
// /v1/identity/* endpoint coverage
// ─────────────────────────────────────────────────────────────────────

// testPool returns a fresh pgxpool against the test DB, plus a
// teardown func that closes it. Used by tests that need to seed
// API keys directly.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := config.Load("canary-identity")
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("testPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAPIKey creates an API key directly via the identity helper and
// returns the plaintext + the row id. Used to bootstrap admin auth
// for the v1/identity/* tests.
func seedAPIKey(t *testing.T, pool *pgxpool.Pool, tenantID *uuid.UUID, agent string, scopes []string) (string, uuid.UUID) {
	t.Helper()
	plaintext, id, err := identity.CreateAPIKeyRow(
		context.Background(), pool, tenantID, agent, scopes, 600, nil,
	)
	if err != nil {
		t.Fatalf("seedAPIKey: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app.api_keys WHERE id = $1`, id)
	})
	return plaintext, id
}

func TestKeysCreate_PlatformScope(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	// identity:keys:admin permits cross-tenant + arbitrary-scope minting
	// per GRO-931, which is what platform-scope dev/admin keys need.
	adminPlaintext, _ := seedAPIKey(t, pool, nil, "test-admin", []string{"identity:keys:admin"})

	body, _ := json.Marshal(map[string]any{
		"agent_name": "test-new-agent",
		"scopes":     []string{"webhook:write"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.HeaderAPIKey, adminPlaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: got %d want 201; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	plaintext, _ := resp["plaintext"].(string)
	if plaintext == "" || plaintext[:3] != "cy_" {
		t.Errorf("plaintext shape unexpected: %q", plaintext)
	}
	createdID, _ := resp["id"].(string)
	if _, err := uuid.Parse(createdID); err != nil {
		t.Errorf("id not a UUID: %q", createdID)
	}
	// cleanup
	_, _ = pool.Exec(context.Background(), `DELETE FROM app.api_keys WHERE id = $1`, createdID)
}

func TestKeysCreate_NoAuthReturns401(t *testing.T) {
	srv := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"agent_name": "test-no-auth",
		"scopes":     []string{"webhook:write"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth: got %d want 401", w.Code)
	}
}

func TestKeysCreate_InvalidKeyReturns401(t *testing.T) {
	srv := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"agent_name": "test-bad-key",
		"scopes":     []string{"webhook:write"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.HeaderAPIKey, "cy_not_a_real_key_12345")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad-key: got %d want 401", w.Code)
	}
}

func TestKeysList_TenantScoped(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)

	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001") // dev seed tenant
	plaintext, _ := seedAPIKey(t, pool, &tenant, "test-list",
		[]string{"evidence:read", "identity:keys:read"})

	req := httptest.NewRequest(http.MethodGet, "/v1/identity/keys", nil)
	req.Header.Set(identity.HeaderAPIKey, plaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, _ := resp["items"].([]any)
	if len(items) == 0 {
		t.Fatal("expected at least the seeded key in the list")
	}
	// Every row's tenant_id (if present) must match our tenant
	for _, it := range items {
		row, _ := it.(map[string]any)
		if tid, ok := row["tenant_id"].(string); ok && tid != "" && tid != tenant.String() {
			t.Errorf("cross-tenant leak: got tenant_id=%s want %s", tid, tenant)
		}
	}
}

func TestKeysRevoke_Idempotent(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	plaintext, id := seedAPIKey(t, pool, &tenant, "test-revoke",
		[]string{"evidence:read", "identity:keys:revoke"})

	url := fmt.Sprintf("/v1/identity/keys/%s/revoke", id)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, url, nil)
		req.Header.Set(identity.HeaderAPIKey, plaintext)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		// First call: 204; second call should also return 204 (idempotent)
		// — but the second request authenticates with a now-revoked key.
		// We expect the middleware to refuse it (401/403). So only the
		// first call gets through; we re-issue with a fresh admin key.
		if i == 0 {
			if w.Code != http.StatusNoContent {
				t.Fatalf("first revoke: got %d want 204; body=%s", w.Code, w.Body.String())
			}
		}
	}

	// Re-revoke with a fresh admin key — verify still 204 (idempotent).
	freshPlaintext, _ := seedAPIKey(t, pool, &tenant, "test-revoke-admin",
		[]string{"identity:keys:admin"})
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.Header.Set(identity.HeaderAPIKey, freshPlaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("re-revoke: got %d want 204; body=%s", w.Code, w.Body.String())
	}

	// Verify the row is now revoked
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM app.api_keys WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("status check: %v", err)
	}
	if status != "revoked" {
		t.Errorf("status: got %q want revoked", status)
	}
}

func TestWhoami_APIKey(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	plaintext, _ := seedAPIKey(t, pool, &tenant, "test-whoami", []string{"evidence:read", "transaction:read"})

	req := httptest.NewRequest(http.MethodGet, "/v1/identity/whoami", nil)
	req.Header.Set(identity.HeaderAPIKey, plaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("whoami: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["auth_method"] != "apikey" {
		t.Errorf("auth_method: got %v want apikey", resp["auth_method"])
	}
	if resp["agent_name"] != "test-whoami" {
		t.Errorf("agent_name: got %v want test-whoami", resp["agent_name"])
	}
	if resp["tenant_id"] != tenant.String() {
		t.Errorf("tenant_id: got %v want %s", resp["tenant_id"], tenant)
	}
	scopes, _ := resp["scopes"].([]any)
	if len(scopes) != 2 {
		t.Errorf("scopes count: got %d want 2", len(scopes))
	}
}

func TestKeysCreate_TenantMismatchRejected(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	// Tenant-scoped key with create scope but NOT admin — must be 403'd
	// when it tries to mint into another tenant.
	plaintext, _ := seedAPIKey(t, pool, &tenant, "test-mismatch",
		[]string{"webhook:write", "identity:keys:create"})

	otherTenant := uuid.New().String()
	body, _ := json.Marshal(map[string]any{
		"tenant_id":  otherTenant,
		"agent_name": "smuggle",
		"scopes":     []string{"webhook:write"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.HeaderAPIKey, plaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("tenant-mismatch: got %d want 403; body=%s", w.Code, w.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────
// GRO-931 acceptance probes — API-key lifecycle scope + tenant gates
// ─────────────────────────────────────────────────────────────────────

// TestKeysCreate_ReadOnlyKey_403 proves that a key holding only the
// read scope cannot mint a new key. Pre-fix: 201; post-fix: 403
// insufficient_scope.
func TestKeysCreate_ReadOnlyKey_403(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	plaintext, _ := seedAPIKey(t, pool, &tenant, "test-readonly-create",
		[]string{"identity:keys:read"})

	body, _ := json.Marshal(map[string]any{
		"agent_name": "should-not-mint",
		"scopes":     []string{"identity:keys:read"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.HeaderAPIKey, plaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only create: got %d want 403; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "insufficient_scope" {
		t.Errorf("code: got %v want insufficient_scope", resp["code"])
	}
}

// TestKeysCreate_ScopeEscalation_403 proves a tenant key with only
// identity:keys:create cannot mint a key carrying scopes the caller
// does not personally hold.
func TestKeysCreate_ScopeEscalation_403(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)
	tenant := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	// Caller holds: read evidence + create keys.
	plaintext, _ := seedAPIKey(t, pool, &tenant, "test-escalate",
		[]string{"evidence:read", "identity:keys:create"})

	// Asks for: webhook:write — a scope the caller does not hold.
	body, _ := json.Marshal(map[string]any{
		"agent_name": "escalation-attempt",
		"scopes":     []string{"webhook:write"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identity.HeaderAPIKey, plaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("scope-escalation: got %d want 403; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "scope_escalation" {
		t.Errorf("code: got %v want scope_escalation", resp["code"])
	}
}

// seedTenantForTest inserts an organization + tenant pair and returns
// the tenant id. Used by GRO-931 cross-tenant tests that need a tenant
// distinct from the dev seed.
func seedTenantForTest(t *testing.T, pool *pgxpool.Pool, namePrefix string) uuid.UUID {
	t.Helper()
	orgID := uuid.New()
	tenantID := uuid.New()
	short := tenantID.String()[:8]
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, namePrefix+"-org-"+short,
	); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		namePrefix+"-"+short,
		namePrefix+" tenant "+short,
		"tenant_"+namePrefix+"_"+short,
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app.tenants WHERE id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM app.organizations WHERE id = $1`, orgID)
	})
	return tenantID
}

// TestKeysRevoke_CrossTenant_404 proves tenant A cannot revoke a key
// belonging to tenant B; the response is 404 not_found (NOT 403) so
// the existence of the key in another tenant is not leaked.
func TestKeysRevoke_CrossTenant_404(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)

	tenantA := uuid.MustParse("22222222-0000-0000-0000-000000000001") // dev-seed tenant
	tenantB := seedTenantForTest(t, pool, "gro931-victim")

	// Victim key in tenantB — not used to authenticate; just sits
	// there as the target of the cross-tenant revoke attempt.
	_, victimID := seedAPIKey(t, pool, &tenantB, "test-victim",
		[]string{"evidence:read"})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, victimID)
	})

	// Attacker key in tenantA, holding revoke scope (but NOT admin).
	attackerPlaintext, _ := seedAPIKey(t, pool, &tenantA, "test-attacker",
		[]string{"identity:keys:revoke"})

	url := fmt.Sprintf("/v1/identity/keys/%s/revoke", victimID)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.Header.Set(identity.HeaderAPIKey, attackerPlaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant revoke: got %d want 404 (no existence leak); body=%s",
			w.Code, w.Body.String())
	}

	// Verify victim row is still active.
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM app.api_keys WHERE id = $1`, victimID).Scan(&status); err != nil {
		t.Fatalf("status check: %v", err)
	}
	if status != "active" {
		t.Errorf("victim key status: got %q want active (cross-tenant revoke must not succeed)", status)
	}
}

// TestKeysRevoke_AdminCrossTenant_204 proves a key with
// identity:keys:admin CAN legitimately revoke across tenants — the
// admin scope is the explicit knob that grants that capability.
func TestKeysRevoke_AdminCrossTenant_204(t *testing.T) {
	srv := testServer(t)
	pool := testPool(t)

	tenantA := uuid.MustParse("22222222-0000-0000-0000-000000000001")
	tenantB := seedTenantForTest(t, pool, "gro931-admin-victim")

	_, victimID := seedAPIKey(t, pool, &tenantB, "test-admin-victim",
		[]string{"evidence:read"})
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app.api_keys WHERE id = $1`, victimID)
	})

	adminPlaintext, _ := seedAPIKey(t, pool, &tenantA, "test-admin-revoke",
		[]string{"identity:keys:admin"})

	url := fmt.Sprintf("/v1/identity/keys/%s/revoke", victimID)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.Header.Set(identity.HeaderAPIKey, adminPlaintext)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("admin cross-tenant revoke: got %d want 204; body=%s",
			w.Code, w.Body.String())
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM app.api_keys WHERE id = $1`, victimID).Scan(&status); err != nil {
		t.Fatalf("status check: %v", err)
	}
	if status != "revoked" {
		t.Errorf("victim status after admin revoke: got %q want revoked", status)
	}
}

func TestJWTValidatorDisabledInTestEnv(t *testing.T) {
	// In the test environment IDENTITY_JWKS_URL is not set, so the
	// validator is disabled. Validate should consistently return
	// ErrJWKSFetch — this is the read-path acceptance signal that
	// the JWT plumbing wires correctly even without a live IdP.
	v := identity.NewJWTValidator()
	if v.Enabled() {
		t.Fatal("expected disabled validator in test env")
	}
}

// Verify cleanup helper — useful when re-running locally to
// guarantee no test rows leak. Idempotent.
func TestCleanupTestKeys(t *testing.T) {
	pool := testPool(t)
	tag, err := pool.Exec(context.Background(),
		`DELETE FROM app.api_keys WHERE agent_name LIKE 'test-%'`)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	t.Logf("cleaned %d test API keys", tag.RowsAffected())
}
