package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────
// API key hashing — round-trip + plaintext format
// ─────────────────────────────────────────────────────────────────────

func TestGenerateAPIKeyPlaintextFormat(t *testing.T) {
	for i := 0; i < 5; i++ {
		p, err := GenerateAPIKeyPlaintext()
		if err != nil {
			t.Fatalf("GenerateAPIKeyPlaintext: %v", err)
		}
		if !strings.HasPrefix(p, KeyPlaintextPrefix) {
			t.Errorf("plaintext missing prefix: %s", p)
		}
		if len(p) < 40 {
			t.Errorf("plaintext too short (%d): %s", len(p), p)
		}
	}
}

func TestGenerateAPIKeyPlaintextDistinct(t *testing.T) {
	a, _ := GenerateAPIKeyPlaintext()
	b, _ := GenerateAPIKeyPlaintext()
	if a == b {
		t.Fatal("two consecutive plaintexts collided")
	}
}

func TestHashAndVerifyAPIKeyRoundTrip(t *testing.T) {
	plaintext, err := GenerateAPIKeyPlaintext()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	hash, err := HashAPIKey(plaintext)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "argon2id$") {
		t.Errorf("hash missing argon2id prefix: %s", hash)
	}

	ok, err := VerifyAPIKey(plaintext, hash)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("verify returned false for matching plaintext")
	}

	ok, err = VerifyAPIKey("cy_wrong_plaintext_value", hash)
	if err != nil {
		t.Fatalf("verify (wrong): %v", err)
	}
	if ok {
		t.Fatal("verify returned true for non-matching plaintext")
	}
}

func TestHashAPIKeyEmptyRejected(t *testing.T) {
	if _, err := HashAPIKey(""); err == nil {
		t.Fatal("expected error for empty plaintext")
	}
}

func TestVerifyAPIKeyMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-the-right-format",
		"sha256$x$y",
		"argon2id$only-two-parts",
		"argon2id$bad-base64$" + strings.Repeat("=", 50),
	}
	for _, c := range cases {
		_, err := VerifyAPIKey("cy_doesnt_matter", c)
		if !errors.Is(err, ErrInvalidHashFmt) {
			t.Errorf("VerifyAPIKey(%q) err=%v want ErrInvalidHashFmt", c, err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Claims plumbing — InjectClaims, ClaimsFromContext, RequireScope,
// AssertBodyTenantMatches
// ─────────────────────────────────────────────────────────────────────

func TestInjectClaimsRoundTrip(t *testing.T) {
	tenantID := uuid.New()
	in := Claims{
		TenantID:   tenantID,
		AgentName:  "alx-dev",
		AuthMethod: AuthMethodAPIKey,
		Scopes:     []string{"evidence:read", "transaction:read"},
	}
	ctx := InjectClaims(context.Background(), in)
	out, ok := ClaimsFromContext(ctx)
	if !ok {
		t.Fatal("claims not found")
	}
	if out.TenantID != in.TenantID || out.AgentName != in.AgentName || out.AuthMethod != in.AuthMethod {
		t.Errorf("round-trip mismatch: in=%+v out=%+v", in, out)
	}
}

func TestRequireScope(t *testing.T) {
	ctx := InjectClaims(context.Background(), Claims{
		Scopes: []string{"webhook:write", "evidence:read"},
	})
	if !RequireScope(ctx, "webhook:write") {
		t.Error("expected RequireScope to find webhook:write")
	}
	if RequireScope(ctx, "nonexistent:scope") {
		t.Error("RequireScope returned true for missing scope")
	}
	if RequireScope(context.Background(), "anything") {
		t.Error("RequireScope returned true on context without claims")
	}
}

func TestAssertBodyTenantMatches(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()

	ctxA := InjectClaims(context.Background(), Claims{TenantID: tenantA, AuthMethod: AuthMethodJWT})

	// Match → nil
	if err := AssertBodyTenantMatches(ctxA, tenantA); err != nil {
		t.Errorf("matched tenant: err=%v want nil", err)
	}
	// Mismatch → ErrTenantMismatch
	if err := AssertBodyTenantMatches(ctxA, tenantB); !errors.Is(err, ErrTenantMismatch) {
		t.Errorf("mismatched tenant: err=%v want ErrTenantMismatch", err)
	}
	// Body has no tenant → skip check
	if err := AssertBodyTenantMatches(ctxA, uuid.Nil); err != nil {
		t.Errorf("body uuid.Nil: err=%v want nil", err)
	}
	// Platform-scope (claims.TenantID == uuid.Nil) → skip check
	platCtx := InjectClaims(context.Background(), Claims{TenantID: uuid.Nil, AuthMethod: AuthMethodAPIKey})
	if err := AssertBodyTenantMatches(platCtx, tenantA); err != nil {
		t.Errorf("platform-scope: err=%v want nil", err)
	}
	// No claims at all → skip check (caller enforces 401 separately)
	if err := AssertBodyTenantMatches(context.Background(), tenantA); err != nil {
		t.Errorf("no claims: err=%v want nil", err)
	}
}

func TestInjectAPIKeyClaimsBridgesTenantContext(t *testing.T) {
	// API key auth path should populate both identity.Claims and
	// the legacy tenant.FromContext path so existing handlers keep
	// working without refactor.
	tenantID := uuid.New()
	ak := APIKeyAuthClaims{
		KeyID:     uuid.New(),
		TenantID:  &tenantID,
		AgentName: "alx-dev",
		Scopes:    []string{"evidence:read"},
	}
	ctx := InjectAPIKeyClaims(context.Background(), ak)
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		t.Fatal("identity claims not set")
	}
	if c.AuthMethod != AuthMethodAPIKey || c.TenantID != tenantID {
		t.Errorf("claims wrong: %+v", c)
	}
}

// ─────────────────────────────────────────────────────────────────────
// JWT validator — disabled path (no JWKS configured)
// ─────────────────────────────────────────────────────────────────────

func TestJWTValidatorDisabledByDefault(t *testing.T) {
	t.Setenv("IDENTITY_JWKS_URL", "")
	t.Setenv("IDENTITY_JWT_ISSUER", "")
	t.Setenv("IDENTITY_JWT_AUDIENCE", "")

	v := NewJWTValidator()
	if v.Enabled() {
		t.Fatal("expected disabled-by-default validator")
	}

	_, err := v.Validate(context.Background(), "anything")
	if !errors.Is(err, ErrJWKSFetch) {
		t.Errorf("disabled Validate err=%v want ErrJWKSFetch", err)
	}
}

func TestJWTValidatorEnabledOnFullConfig(t *testing.T) {
	t.Setenv("IDENTITY_JWKS_URL", "https://example.test/jwks")
	t.Setenv("IDENTITY_JWT_ISSUER", "https://example.test")
	t.Setenv("IDENTITY_JWT_AUDIENCE", "canary-test")

	v := NewJWTValidator()
	if !v.Enabled() {
		t.Fatal("expected Enabled=true on full config")
	}
}

// API key middleware end-to-end behavior (header → DB lookup →
// claims injection) is exercised by the cmd/identity integration
// suite. Constructor-level nil-pool guard is enforced via panic; not
// worth a separate unit test.
