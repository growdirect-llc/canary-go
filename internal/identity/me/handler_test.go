package me

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// stubVerifier returns canned outputs based on the input token.
type stubVerifier struct {
	claims *tokenverify.Claims
	err    error
}

func (s *stubVerifier) Verify(_ context.Context, _ string) (*tokenverify.Claims, error) {
	return s.claims, s.err
}

func mountAndCall(t *testing.T, v Verifier, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	New(v, nil).Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestMe_HappyPath_ReturnsPersonRecord verifies a valid bearer
// token produces the contract-shaped Person record with id
// derived from PersonID claim.
func TestMe_HappyPath_ReturnsPersonRecord(t *testing.T) {
	personID := uuid.New().String()
	v := &stubVerifier{claims: &tokenverify.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "different-from-person-id"},
		PersonID:         personID,
		UserType:         "regular",
	}}
	rec := mountAndCall(t, v, "Bearer faketoken")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}

	var got PersonRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, rec.Body.String())
	}
	if got.ID != personID {
		t.Errorf("id: got %q, want %q (PersonID claim)", got.ID, personID)
	}
	if got.System {
		t.Error("System: got true, want false (user_type=regular)")
	}
}

// TestMe_SystemUser_FlagsSystem verifies user_type=system populates
// the system bool true. Receiving clients use this to distinguish
// service-account tokens from user tokens.
func TestMe_SystemUser_FlagsSystem(t *testing.T) {
	v := &stubVerifier{claims: &tokenverify.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.New().String()},
		UserType:         "system",
	}}
	rec := mountAndCall(t, v, "Bearer faketoken")

	var got PersonRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.System {
		t.Error("System: got false, want true (user_type=system)")
	}
}

// TestMe_PersonIDFallsBackToSubject verifies that when PersonID is
// empty, id falls back to sub — handles tokens minted before the
// AtlasView claims contract was added.
func TestMe_PersonIDFallsBackToSubject(t *testing.T) {
	sub := uuid.New().String()
	v := &stubVerifier{claims: &tokenverify.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: sub},
	}}
	rec := mountAndCall(t, v, "Bearer faketoken")

	var got PersonRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != sub {
		t.Errorf("id: got %q, want %q (sub fallback)", got.ID, sub)
	}
}

// TestMe_MissingAuthHeader_401 verifies absent Authorization header
// returns 401 with missing_bearer.
func TestMe_MissingAuthHeader_401(t *testing.T) {
	rec := mountAndCall(t, &stubVerifier{}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_bearer") {
		t.Errorf("body: got %q, expected missing_bearer envelope", rec.Body.String())
	}
}

// TestMe_MalformedAuthHeader_401 verifies wrong schemes / bare
// tokens / empty Bearer all produce 401 (no fingerprinting).
func TestMe_MalformedAuthHeader_401(t *testing.T) {
	cases := []string{
		"Basic dXNlcjpwYXNz",   // wrong scheme
		"raw-token",            // no scheme
		"Bearer",               // bare scheme, no token
		"Bearer   ",            // scheme + whitespace, no token
	}
	for _, hdr := range cases {
		rec := mountAndCall(t, &stubVerifier{}, hdr)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization=%q: got %d, want 401", hdr, rec.Code)
		}
	}
}

// TestMe_BearerSchemeIsCaseInsensitive verifies "bearer" (lowercase)
// works — RFC 6750 §2.1 says the scheme is case-insensitive even
// though "Bearer" is the canonical form.
func TestMe_BearerSchemeIsCaseInsensitive(t *testing.T) {
	v := &stubVerifier{claims: &tokenverify.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.New().String()},
	}}
	rec := mountAndCall(t, v, "bearer faketoken")
	if rec.Code != http.StatusOK {
		t.Errorf("lowercase 'bearer': got %d, want 200", rec.Code)
	}
}

// TestMe_ExpiredToken_401 verifies token_expired surfaces as 401
// with a distinct code so clients can refresh.
func TestMe_ExpiredToken_401(t *testing.T) {
	v := &stubVerifier{err: tokenverify.ErrTokenExpired}
	rec := mountAndCall(t, v, "Bearer faketoken")

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token_expired") {
		t.Errorf("body: got %q, expected token_expired", rec.Body.String())
	}
}

// TestMe_BadAudience_403 verifies aud mismatch surfaces as 403
// (forbidden, not unauthenticated — the caller IS authenticated,
// just not for this audience).
func TestMe_BadAudience_403(t *testing.T) {
	v := &stubVerifier{err: tokenverify.ErrTokenAudience}
	rec := mountAndCall(t, v, "Bearer faketoken")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "audience_mismatch") {
		t.Errorf("body: got %q, expected audience_mismatch", rec.Body.String())
	}
}

// TestMe_InvalidToken_401Generic verifies signature / malformed /
// unknown-kid all collapse into a single "invalid_token" 401 —
// fingerprinting defense (attacker can't tell which validation
// failed).
func TestMe_InvalidToken_401Generic(t *testing.T) {
	for _, e := range []error{
		tokenverify.ErrTokenSignature,
		tokenverify.ErrTokenMalformed,
		tokenverify.ErrTokenUnknownKid,
		tokenverify.ErrTokenAlgorithm,
		tokenverify.ErrTokenMissingKid,
	} {
		v := &stubVerifier{err: e}
		rec := mountAndCall(t, v, "Bearer faketoken")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("err=%v: got %d, want 401", e, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "invalid_token") {
			t.Errorf("err=%v: body %q expected invalid_token", e, rec.Body.String())
		}
	}
}
