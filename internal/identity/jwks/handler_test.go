package jwks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/keystore"
)

// stubReader satisfies VerifySetReader for tests.
type stubReader struct {
	keys []keystore.SigningKey
	err  error
}

func (s *stubReader) VerifySet(_ context.Context) ([]keystore.SigningKey, error) {
	return s.keys, s.err
}

func mountAndServe(t *testing.T, r VerifySetReader, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	New(r, nil).Mount(router)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestJWKS_ServesActiveAndRetiringKeys verifies the handler emits
// the standard {"keys":[...]} envelope and includes the JWK bytes
// produced by the keystore verbatim — no re-marshaling.
func TestJWKS_ServesActiveAndRetiringKeys(t *testing.T) {
	active := keystore.SigningKey{
		ID: uuid.New(), Kid: "active-kid", Alg: keystore.AlgRS256,
		Status:    keystore.StatusActive,
		PublicJWK: json.RawMessage(`{"kty":"RSA","kid":"active-kid","use":"sig","alg":"RS256","n":"AQAB","e":"AQAB"}`),
	}
	retiring := keystore.SigningKey{
		ID: uuid.New(), Kid: "retiring-kid", Alg: keystore.AlgRS256,
		Status:    keystore.StatusRetiring,
		PublicJWK: json.RawMessage(`{"kty":"RSA","kid":"retiring-kid","use":"sig","alg":"RS256","n":"BQAB","e":"AQAB"}`),
	}

	rec := mountAndServe(t, &stubReader{keys: []keystore.SigningKey{active, retiring}}, "/.well-known/jwks.json")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/jwk-set+json" {
		t.Errorf("Content-Type: got %q, want application/jwk-set+json", ct)
	}
	if cache := rec.Header().Get("Cache-Control"); !strings.Contains(cache, "max-age=60") {
		t.Errorf("Cache-Control: got %q, want max-age=60", cache)
	}

	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal response: %v\nbody=%s", err, rec.Body.String())
	}
	if len(doc.Keys) != 2 {
		t.Fatalf("keys: got %d, want 2", len(doc.Keys))
	}
	gotKids := []string{doc.Keys[0]["kid"], doc.Keys[1]["kid"]}
	if !contains(gotKids, "active-kid") || !contains(gotKids, "retiring-kid") {
		t.Errorf("kids: got %v, want both active-kid and retiring-kid", gotKids)
	}
}

// TestJWKS_EmptyKeySet_StillReturns200 verifies that even an empty
// verify set produces a valid JWKS document — clients shouldn't
// crash on bootstrap before the first key is published.
func TestJWKS_EmptyKeySet_StillReturns200(t *testing.T) {
	rec := mountAndServe(t, &stubReader{keys: nil}, "/.well-known/jwks.json")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(doc.Keys) != 0 {
		t.Errorf("empty set: got %d keys, want 0", len(doc.Keys))
	}
}

// TestJWKS_KeystoreError_Returns500 verifies a keystore failure
// surfaces as 500 with a structured error envelope, not a panic or
// empty response.
func TestJWKS_KeystoreError_Returns500(t *testing.T) {
	rec := mountAndServe(t, &stubReader{err: errors.New("database is down")}, "/.well-known/jwks.json")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "jwks_unavailable") {
		t.Errorf("body: got %q, expected error envelope with jwks_unavailable", rec.Body.String())
	}
}

// TestJWKS_SkipsMalformedKeys verifies a row with an empty PublicJWK
// is dropped from the output rather than crashing the whole
// document. Malformed-row resilience matters because the keystore
// backs onto a Postgres column and a corrupt row would otherwise
// take down all token verification cluster-wide.
func TestJWKS_SkipsMalformedKeys(t *testing.T) {
	good := keystore.SigningKey{
		ID: uuid.New(), Kid: "good-kid", Alg: keystore.AlgRS256,
		Status:    keystore.StatusActive,
		PublicJWK: json.RawMessage(`{"kty":"RSA","kid":"good-kid","use":"sig","alg":"RS256","n":"X","e":"AQAB"}`),
	}
	bad := keystore.SigningKey{
		ID: uuid.New(), Kid: "empty-jwk", Alg: keystore.AlgRS256,
		Status:    keystore.StatusRetiring,
		PublicJWK: nil,
	}

	rec := mountAndServe(t, &stubReader{keys: []keystore.SigningKey{good, bad}}, "/.well-known/jwks.json")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if len(doc.Keys) != 1 {
		t.Fatalf("keys: got %d, want 1 (malformed row should be skipped)", len(doc.Keys))
	}
	if doc.Keys[0]["kid"] != "good-kid" {
		t.Errorf("kid: got %q, want good-kid", doc.Keys[0]["kid"])
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
