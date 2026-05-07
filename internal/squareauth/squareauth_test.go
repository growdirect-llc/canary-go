package squareauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
