package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nextOK is a sentinel handler proving the middleware passed control
// downstream. Returns 200 with body "ok".
var nextOK = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

func TestRequireScopeMiddleware_NoClaims_401(t *testing.T) {
	h := RequireScopeMiddleware("transaction:read")(nextOK)

	req := httptest.NewRequest(http.MethodGet, "/v1/transactions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	var got struct {
		Code, Message string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("envelope JSON decode: %v (body=%s)", err, rec.Body.String())
	}
	if got.Code != "unauthenticated" {
		t.Errorf("code: got %q, want %q", got.Code, "unauthenticated")
	}
}

func TestRequireScopeMiddleware_MissingScope_403(t *testing.T) {
	ctx := InjectClaims(context.Background(), Claims{
		Scopes:     []string{ScopeTransactionRead},
		AuthMethod: AuthMethodAPIKey,
	})
	h := RequireScopeMiddleware(ScopeTransactionWrite)(nextOK)

	req := httptest.NewRequest(http.MethodPost, "/v1/transactions", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
	var got struct {
		Code, Message string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("envelope JSON decode: %v (body=%s)", err, rec.Body.String())
	}
	if got.Code != "insufficient_scope" {
		t.Errorf("code: got %q, want %q", got.Code, "insufficient_scope")
	}
	if !strings.Contains(got.Message, ScopeTransactionWrite) {
		t.Errorf("message should mention required scope %q; got %q", ScopeTransactionWrite, got.Message)
	}
}

func TestRequireScopeMiddleware_HasScope_PassesThrough(t *testing.T) {
	ctx := InjectClaims(context.Background(), Claims{
		Scopes:     []string{ScopeTransactionRead, ScopeTransactionWrite},
		AuthMethod: AuthMethodAPIKey,
	})
	h := RequireScopeMiddleware(ScopeTransactionWrite)(nextOK)

	req := httptest.NewRequest(http.MethodPost, "/v1/transactions", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (handler should run)", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body: got %q, want %q", got, "ok")
	}
}

func TestRequireScopeMiddleware_EmptyScopesSlice_403(t *testing.T) {
	// Edge case: a key with an empty scopes array hits a write route.
	ctx := InjectClaims(context.Background(), Claims{
		Scopes:     []string{},
		AuthMethod: AuthMethodAPIKey,
	})
	h := RequireScopeMiddleware(ScopeTransactionWrite)(nextOK)

	req := httptest.NewRequest(http.MethodPost, "/v1/transactions", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
}
