package squareauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ─── IsExpiring ────────────────────────────────────────────────────────────

func TestIsExpiring_expired(t *testing.T) {
	c := &StoredCredentials{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	if !c.IsExpiring(5 * time.Minute) {
		t.Fatal("expected IsExpiring=true for already-expired token")
	}
}

func TestIsExpiring_soonExpiring(t *testing.T) {
	c := &StoredCredentials{ExpiresAt: time.Now().Add(2 * time.Minute)}
	if !c.IsExpiring(5 * time.Minute) {
		t.Fatal("expected IsExpiring=true for token expiring in 2m with 5m threshold")
	}
}

func TestIsExpiring_notExpiring(t *testing.T) {
	c := &StoredCredentials{ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	if c.IsExpiring(5 * time.Minute) {
		t.Fatal("expected IsExpiring=false for token expiring in 30 days")
	}
}

func TestIsExpiring_zeroTime(t *testing.T) {
	c := &StoredCredentials{}
	if c.IsExpiring(5 * time.Minute) {
		t.Fatal("expected IsExpiring=false for zero ExpiresAt")
	}
}

// ─── RefreshToken ──────────────────────────────────────────────────────────

// TestRefreshToken_success spins up a mock Square token endpoint and verifies
// that RefreshToken posts grant_type=refresh_token and parses the response.
func TestRefreshToken_success(t *testing.T) {
	wantAccessToken := "EAAA-new-access-token"
	wantRefreshToken := "REFRESH-new-refresh-token"
	wantMerchantID := "MLM0SQ12345"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth2/token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Errorf("expected grant_type=refresh_token, got %v", body["grant_type"])
		}
		if body["refresh_token"] != "OLD-REFRESH" {
			t.Errorf("expected old refresh token, got %v", body["refresh_token"])
		}
		resp := TokenResponse{
			AccessToken:  wantAccessToken,
			RefreshToken: wantRefreshToken,
			TokenType:    "bearer",
			ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
			MerchantID:   wantMerchantID,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	svc := &Service{
		cfg: Config{
			ApplicationID:     "app-id",
			ApplicationSecret: "app-secret",
			Environment:       "sandbox",
		},
		httpClient: srv.Client(),
	}
	// Override connect base URL to point at test server.
	// We do this by patching the environment-derived URL inline via a
	// test-only helper on the httpClient transport.
	svc.httpClient.Transport = rewriteTransport{base: srv.URL, inner: srv.Client().Transport}

	tr, err := svc.RefreshToken(context.Background(), "OLD-REFRESH")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tr.AccessToken != wantAccessToken {
		t.Errorf("access token: got %q want %q", tr.AccessToken, wantAccessToken)
	}
	if tr.RefreshToken != wantRefreshToken {
		t.Errorf("refresh token: got %q want %q", tr.RefreshToken, wantRefreshToken)
	}
	if tr.MerchantID != wantMerchantID {
		t.Errorf("merchant id: got %q want %q", tr.MerchantID, wantMerchantID)
	}
}

func TestRefreshToken_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"type":"UNAUTHORIZED","errors":[]}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	svc := &Service{
		cfg:        Config{ApplicationID: "x", ApplicationSecret: "y", Environment: "sandbox"},
		httpClient: srv.Client(),
	}
	svc.httpClient.Transport = rewriteTransport{base: srv.URL, inner: srv.Client().Transport}

	_, err := svc.RefreshToken(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

// ─── encrypt / decrypt round-trip ─────────────────────────────────────────

func TestEncryptDecrypt_withKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	svc := &Service{encKey: key}

	plaintext := []byte(`{"access_token":"EAAA","refresh_token":"REFRESH"}`)
	ct, err := svc.encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(ct[:4]) != "GCM:" {
		t.Errorf("expected GCM: prefix, got %q", ct[:4])
	}

	got, err := svc.decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestEncryptDecrypt_plainFallback(t *testing.T) {
	svc := &Service{encKey: nil}
	plaintext := []byte(`{"access_token":"EAAA"}`)

	ct, err := svc.encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(ct[:6]) != "PLAIN:" {
		t.Errorf("expected PLAIN: prefix, got %q", ct[:6])
	}

	got, err := svc.decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

// rewriteTransport redirects all requests to base, preserving path/query.
type rewriteTransport struct {
	base  string
	inner http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = req.URL.Host
	// strip scheme+host prefix from base and replace
	parsed, _ := http.NewRequest("", rt.base, nil)
	req2.URL.Host = parsed.URL.Host
	req2.URL.Scheme = parsed.URL.Scheme
	if rt.inner == nil {
		return http.DefaultTransport.RoundTrip(req2)
	}
	return rt.inner.RoundTrip(req2)
}

// ─── GRO-933 encryption-key validation ────────────────────────────────

// TestLoadEncryptionKey_ValidBase64 verifies a properly-encoded 32-byte
// key is returned.
func TestLoadEncryptionKey_ValidBase64(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	key, err := loadEncryptionKey(encoded)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("got len %d, want 32", len(key))
	}
	for i := range key {
		if key[i] != byte(i) {
			t.Fatalf("byte %d: got %d, want %d", i, key[i], i)
		}
	}
}

// TestLoadEncryptionKey_Missing returns (nil, nil) so the caller can
// decide: production fatals; sandbox warns + continues.
func TestLoadEncryptionKey_Missing(t *testing.T) {
	key, err := loadEncryptionKey("")
	if err != nil {
		t.Fatalf("missing should not be an error directly (caller decides): %v", err)
	}
	if key != nil {
		t.Fatalf("missing should yield nil key, got %v", key)
	}
}

// TestLoadEncryptionKey_MalformedBase64 returns an error.
func TestLoadEncryptionKey_MalformedBase64(t *testing.T) {
	_, err := loadEncryptionKey("not!base64!@#$")
	if err == nil {
		t.Fatal("malformed base64 should error")
	}
	if !strings.Contains(err.Error(), "base64 decode") {
		t.Errorf("expected base64-decode error, got: %v", err)
	}
}

// TestLoadEncryptionKey_WrongLength returns an error when the decoded
// payload is not 32 bytes (AES-256-GCM key length).
func TestLoadEncryptionKey_WrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("only-16-bytes-x!"))
	_, err := loadEncryptionKey(short)
	if err == nil {
		t.Fatal("wrong-length payload should error")
	}
	if !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("expected length error, got: %v", err)
	}
}

// TestEncrypt_RefusesPlainInProduction is the GRO-933 acceptance probe.
// The defense-in-depth runtime check: even if a Service somehow exists
// in production mode without an encryption key, encrypt() must refuse
// to write the PLAIN: sentinel. The startup-time fatal in New() is the
// load-bearing gate; this test asserts the second line of defense
// holds.
//
// Fails pre-fix because pre-GRO-933 encrypt() had no production check —
// every nil-key call wrote PLAIN: regardless of environment.
func TestEncrypt_RefusesPlainInProduction(t *testing.T) {
	s := &Service{
		encKey:     nil,
		production: true,
	}
	_, err := s.encrypt([]byte("EAAA-secret-merchant-token"))
	if err == nil {
		t.Fatal("encrypt with nil key in production must error, not write PLAIN:")
	}
	if !strings.Contains(err.Error(), "PLAIN") {
		t.Errorf("error should reference PLAIN: refusal, got: %v", err)
	}
}

// TestEncrypt_AllowsPlainInSandbox verifies the sandbox fallback still
// works — the production guard must not regress dev/CI flows.
func TestEncrypt_AllowsPlainInSandbox(t *testing.T) {
	s := &Service{
		encKey:     nil,
		production: false,
	}
	out, err := s.encrypt([]byte("EAAA-sandbox-token"))
	if err != nil {
		t.Fatalf("sandbox encrypt should succeed: %v", err)
	}
	if !strings.HasPrefix(string(out), "PLAIN:") {
		t.Errorf("sandbox encrypt should write PLAIN: sentinel, got %q", string(out))
	}
}

// TestEncrypt_GCMHappyPath verifies the production-equivalent path:
// with a valid 32-byte key, encrypt produces GCM: ciphertext that
// decrypt() round-trips.
func TestEncrypt_GCMHappyPath(t *testing.T) {
	key := make([]byte, 32)
	s := &Service{encKey: key, production: true}

	plaintext := []byte("EAAA-real-token-with-real-merchant-secret")
	ct, err := s.encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(string(ct), "GCM:") {
		t.Errorf("expected GCM: prefix, got %q", string(ct[:8]))
	}
	pt, err := s.decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q, want %q", string(pt), string(plaintext))
	}
}

// TestIsProduction_EnvOrSquareEnv verifies either flag tripping the
// production gate. Mirrors the OAuth deployment realities: ops may
// deploy with ENV=staging (general infra) and SQUARE_ENVIRONMENT=
// production (real merchant tokens) — we still need the guard.
func TestIsProduction_EnvOrSquareEnv(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		squareEnv string
		want      bool
	}{
		{"both unset", "", "", false},
		{"ENV=production only", "production", "", true},
		{"SQUARE_ENVIRONMENT=production only", "", "production", true},
		{"both production", "production", "production", true},
		{"both sandbox-equivalents", "development", "sandbox", false},
		{"ENV=staging + SQUARE_ENVIRONMENT=production", "staging", "production", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ENV", c.env)
			cfg := Config{Environment: c.squareEnv}
			if got := isProduction(cfg); got != c.want {
				t.Errorf("isProduction(env=%q, square=%q) = %v, want %v",
					c.env, c.squareEnv, got, c.want)
			}
		})
	}
}
