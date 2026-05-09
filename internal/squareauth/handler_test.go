package squareauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// newTestService builds a Service with only the fields the cookie-signing
// helpers need. Avoids touching the pgx pool / http client.
func newTestService(t *testing.T, key string) *Service {
	t.Helper()
	return &Service{
		logger:     zap.NewNop(),
		sessionKey: []byte(key),
	}
}

func TestCookieSign_RoundTripsKnownUUID(t *testing.T) {
	s := newTestService(t, "test-session-secret-not-for-prod")
	id := uuid.MustParse("0a1b2c3d-4e5f-6789-abcd-0123456789ab")

	val := s.signCookieValue(id)
	if !strings.Contains(val, ".") {
		t.Fatalf("signed value missing separator: %q", val)
	}
	if !strings.HasPrefix(val, id.String()+".") {
		t.Errorf("signed value should start with the UUID; got %q", val)
	}

	gotID, ok := s.verifyCookieValue(val)
	if !ok {
		t.Fatal("verify rejected a value we just signed")
	}
	if gotID != id {
		t.Errorf("round-trip mismatch: got %s, want %s", gotID, id)
	}
}

func TestCookieVerify_RejectsBareUUID(t *testing.T) {
	// The pre-T-D cookie format was a bare plaintext UUID. After T-D
	// any cookie without a signature separator must be rejected — this
	// is the regression guard against the C5 forgery vector.
	s := newTestService(t, "key")
	id := uuid.New()
	if _, ok := s.verifyCookieValue(id.String()); ok {
		t.Fatal("verify accepted bare UUID — forged-cookie attack still possible")
	}
}

func TestCookieVerify_RejectsTamperedUUID(t *testing.T) {
	s := newTestService(t, "key")
	signed := s.signCookieValue(uuid.MustParse("0a1b2c3d-4e5f-6789-abcd-0123456789ab"))
	parts := strings.SplitN(signed, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("setup: signed value malformed: %q", signed)
	}
	// Replace the UUID with a different one but keep the original signature.
	tampered := uuid.New().String() + "." + parts[1]
	if _, ok := s.verifyCookieValue(tampered); ok {
		t.Fatal("verify accepted UUID tampering — signature must cover the UUID")
	}
}

func TestCookieVerify_RejectsBadSignature(t *testing.T) {
	s := newTestService(t, "key")
	id := uuid.New()
	bad := id.String() + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, ok := s.verifyCookieValue(bad); ok {
		t.Fatal("verify accepted a forged signature")
	}
}

func TestCookieVerify_RejectsBadBase64(t *testing.T) {
	s := newTestService(t, "key")
	id := uuid.New()
	// `!!!` is not valid base64url
	bad := id.String() + ".!!!"
	if _, ok := s.verifyCookieValue(bad); ok {
		t.Fatal("verify accepted invalid base64 in signature slot")
	}
}

func TestCookieVerify_DifferentKeyRejects(t *testing.T) {
	signer := newTestService(t, "key-A")
	verifier := newTestService(t, "key-B")
	id := uuid.New()
	signed := signer.signCookieValue(id)
	if _, ok := verifier.verifyCookieValue(signed); ok {
		t.Fatal("verifier with different key accepted cookie signed by another key")
	}
}

func TestCookieSign_DevFallback_NoKey(t *testing.T) {
	// When SESSION_SECRET is unset (sessionKey nil), the sign helper
	// produces a bare UUID and verify accepts it. This is the documented
	// dev fallback. Production deployments set ENV=production via
	// gateway-side config so this path isn't reachable.
	s := newTestService(t, "")
	id := uuid.New()
	signed := s.signCookieValue(id)
	if signed != id.String() {
		t.Errorf("dev fallback should emit bare UUID; got %q", signed)
	}
	gotID, ok := s.verifyCookieValue(signed)
	if !ok {
		t.Fatal("dev fallback should accept bare UUID when sessionKey is empty")
	}
	if gotID != id {
		t.Errorf("dev fallback round-trip mismatch")
	}
}

// TestDevDemoLogin_DisabledByDefault confirms /auth/demo returns 404 when
// DEV_DEMO_LOGIN is unset. Production deploys never set the var, so this
// is the production-equivalent path. Hardening: 404 (not 403/401) so the
// route doesn't reveal that "demo login" is a thing.
func TestDevDemoLogin_DisabledByDefault(t *testing.T) {
	t.Setenv("DEV_DEMO_LOGIN", "")
	s := newTestService(t, "test-session-secret")

	r := chi.NewRouter()
	r.Get("/auth/demo", s.handleDevDemoLogin)
	req := httptest.NewRequest(http.MethodGet, "/auth/demo", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("/auth/demo with flag unset = %d, want 404", rr.Code)
	}
	if c := rr.Result().Cookies(); len(c) > 0 {
		t.Errorf("disabled handler should not set any cookies; got %d", len(c))
	}
}

// TestDevDemoLogin_SignsCookieAndRedirectsWhenEnabled confirms the happy
// path: env flag on, route signs the demo session cookie and redirects
// to /dashboard. The cookie value round-trips through verifyCookieValue
// to the seeded demo merchant UUID.
func TestDevDemoLogin_SignsCookieAndRedirectsWhenEnabled(t *testing.T) {
	t.Setenv("DEV_DEMO_LOGIN", "1")
	s := newTestService(t, "test-session-secret")

	r := chi.NewRouter()
	r.Get("/auth/demo", s.handleDevDemoLogin)
	req := httptest.NewRequest(http.MethodGet, "/auth/demo", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("/auth/demo with flag on = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("redirect target = %q, want /dashboard", loc)
	}

	// Cookie should be set + signed for the seeded demo merchant.
	var sessionCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected %q cookie to be set; got cookies %v",
			sessionCookieName, rr.Result().Cookies())
	}
	if !sessionCookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if sessionCookie.Path != "/" {
		t.Errorf("cookie Path = %q, want /", sessionCookie.Path)
	}

	// Verify the cookie carries the seeded merchant UUID.
	gotID, ok := s.verifyCookieValue(sessionCookie.Value)
	if !ok {
		t.Fatal("verifyCookieValue rejected the demo cookie")
	}
	wantID := uuid.MustParse(devDemoMerchantID)
	if gotID != wantID {
		t.Errorf("demo cookie merchant = %s, want %s (seeded demo)", gotID, wantID)
	}
}

// TestDevDemoLoginEnabled_ReadsEnvVar — small unit test on the env gate
// helper. Truthy values turn it on; everything else (including unset)
// leaves it off. Mirrors getBool in internal/config.
func TestDevDemoLoginEnabled_ReadsEnvVar(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"no":    false,
		"1":     true,
		"true":  true,
		"TRUE":  true,
	}
	s := newTestService(t, "k")
	for in, want := range cases {
		t.Run("env="+in, func(t *testing.T) {
			t.Setenv("DEV_DEMO_LOGIN", in)
			if got := s.DevDemoLoginEnabled(); got != want {
				t.Errorf("DEV_DEMO_LOGIN=%q → %v, want %v", in, got, want)
			}
		})
	}
}
