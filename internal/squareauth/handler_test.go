package squareauth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// newTestService builds a Service with only the fields the cookie-signing
// helpers need. Avoids touching the pgx pool / http client. GRO-857 / T-D.
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
