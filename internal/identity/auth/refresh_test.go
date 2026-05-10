package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/mint"
	"github.com/ruptiv/canary/internal/identity/refreshfamily"
	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// ─── stubs ───────────────────────────────────────────────────────────

type stubVerifier struct {
	claims *tokenverify.Claims
	err    error
}

func (s *stubVerifier) Verify(_ context.Context, _ string) (*tokenverify.Claims, error) {
	return s.claims, s.err
}

type stubMinter struct {
	pair *mint.Pair
	err  error
	// Captures the inputs for assertion.
	gotSubject  mint.Subject
	gotFamilyID uuid.UUID
}

func (s *stubMinter) MintPair(_ context.Context, sub mint.Subject, familyID uuid.UUID) (*mint.Pair, error) {
	s.gotSubject = sub
	s.gotFamilyID = familyID
	return s.pair, s.err
}

type stubFamilyStore struct {
	err error
	// Captures the inputs.
	gotFamilyID     uuid.UUID
	gotPresentedJTI string
	gotNewJTI       string

	// Revoke captures.
	revokedFamilyID uuid.UUID
	revokedReason   string
	revokeErr       error
}

func (s *stubFamilyStore) ValidateAndRotate(_ context.Context, fid uuid.UUID, presented, newJTI string) error {
	s.gotFamilyID = fid
	s.gotPresentedJTI = presented
	s.gotNewJTI = newJTI
	return s.err
}

func (s *stubFamilyStore) Revoke(_ context.Context, fid uuid.UUID, reason string) error {
	s.revokedFamilyID = fid
	s.revokedReason = reason
	return s.revokeErr
}

// stubPersonLookup satisfies PersonLookup. By default returns an
// active Person with the looked-up id and a fresh OrgID.
type stubPersonLookup struct {
	person *Person
	err    error

	// Captured input.
	gotID uuid.UUID
}

func (s *stubPersonLookup) LookupByID(_ context.Context, id uuid.UUID) (*Person, error) {
	s.gotID = id
	if s.err != nil {
		return nil, s.err
	}
	if s.person != nil {
		return s.person, nil
	}
	return &Person{ID: id, OrgID: uuid.New(), IsActive: true}, nil
}

// ─── helpers ─────────────────────────────────────────────────────────

func validClaims() *tokenverify.Claims {
	return &tokenverify.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  uuid.New().String(),
			ID:       uuid.New().String(),
			Audience: jwt.ClaimStrings{"refresh"},
		},
		FamilyID: uuid.New().String(),
		OrgID:    uuid.New().String(),
		PersonID: uuid.New().String(),
		UserType: "regular",
		Scopes:   []string{"identity:me"},
	}
}

func validPair() *mint.Pair {
	return &mint.Pair{
		AccessToken:  "new.access.token",
		RefreshToken: "new.refresh.token",
		AccessJTI:    uuid.New().String(),
		RefreshJTI:   uuid.New().String(),
		FamilyID:     uuid.New().String(),
		AccessExp:    time.Now().Add(30 * time.Minute),
		RefreshExp:   time.Now().Add(12 * time.Hour),
	}
}

func mountAndPost(t *testing.T, v RefreshVerifier, m PairMinter, s FamilyStore, body any) *httptest.ResponseRecorder {
	t.Helper()
	return mountAndPostWithPersons(t, v, m, s, &stubPersonLookup{}, body)
}

func mountAndPostWithPersons(t *testing.T, v RefreshVerifier, m PairMinter, s FamilyStore, p PersonLookup, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	NewRefreshHandler(v, m, s, p, nil).Mount(router)

	var bodyBytes []byte
	switch b := body.(type) {
	case string:
		bodyBytes = []byte(b)
	default:
		bodyBytes, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// ─── tests ──────────────────────────────────────────────────────────

// TestRefresh_HappyPath verifies a valid refresh produces a new
// pair, the verifier+minter+family store are wired correctly, and
// the response shape matches RFC 6749 §5.1 (token_type=Bearer,
// expires_in present).
func TestRefresh_HappyPath(t *testing.T) {
	claims := validClaims()
	pair := validPair()
	v := &stubVerifier{claims: claims}
	m := &stubMinter{pair: pair}
	s := &stubFamilyStore{} // happy path: nil err

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old.refresh"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp refreshResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken != pair.AccessToken {
		t.Errorf("access_token: got %q, want %q", resp.AccessToken, pair.AccessToken)
	}
	if resp.RefreshToken != pair.RefreshToken {
		t.Errorf("refresh_token: got %q, want %q", resp.RefreshToken, pair.RefreshToken)
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type: got %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expires_in: got %d, want > 0", resp.ExpiresIn)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store (RFC 6749 §5.1)", cache)
	}

	// Verify the family store saw the right inputs.
	wantFamilyID, _ := uuid.Parse(claims.FamilyID)
	if s.gotFamilyID != wantFamilyID {
		t.Errorf("family rotation: got family %s, want %s", s.gotFamilyID, wantFamilyID)
	}
	if s.gotPresentedJTI != claims.ID {
		t.Errorf("family rotation: presented jti %q, want %q", s.gotPresentedJTI, claims.ID)
	}
	if s.gotNewJTI != pair.RefreshJTI {
		t.Errorf("family rotation: new jti %q, want %q", s.gotNewJTI, pair.RefreshJTI)
	}
	// Verify the minter saw the family-id continuity.
	if m.gotFamilyID != wantFamilyID {
		t.Errorf("minter: got family %s, want %s (continuity broken)", m.gotFamilyID, wantFamilyID)
	}
}

// TestRefresh_ReuseDetected_401 verifies the family-store reuse-
// detection error surfaces as 401 with the reuse_detected code.
// The new pair is computed but discarded — never returned to the
// caller — because returning it after revoking the family would
// be a security hole.
func TestRefresh_ReuseDetected_401(t *testing.T) {
	v := &stubVerifier{claims: validClaims()}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{err: refreshfamily.ErrReuseDetected}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reuse_detected") {
		t.Errorf("body: got %q, expected reuse_detected", body)
	}
	// Critical: the new pair must NOT leak into the response body.
	if strings.Contains(body, "new.access.token") || strings.Contains(body, "new.refresh.token") {
		t.Errorf("new pair leaked into response despite reuse-detection: %s", body)
	}
}

// TestRefresh_FamilyRevoked_401 verifies an explicitly-revoked
// family (logout, admin revoke) returns 401 family_revoked.
func TestRefresh_FamilyRevoked_401(t *testing.T) {
	v := &stubVerifier{claims: validClaims()}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{err: refreshfamily.ErrFamilyRevoked}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "family_revoked") {
		t.Errorf("body: got %q, expected family_revoked", rec.Body.String())
	}
}

// TestRefresh_FamilyNotFound_401 verifies an unknown family returns
// 401 family_not_found (could be: server forgot it, or token was
// forged with a fake family-id).
func TestRefresh_FamilyNotFound_401(t *testing.T) {
	v := &stubVerifier{claims: validClaims()}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{err: refreshfamily.ErrFamilyNotFound}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "family_not_found") {
		t.Errorf("body: got %q, expected family_not_found", rec.Body.String())
	}
}

// TestRefresh_ExpiredToken_401 verifies an expired refresh token
// returns 401 token_expired (so clients know to re-login).
func TestRefresh_ExpiredToken_401(t *testing.T) {
	v := &stubVerifier{err: tokenverify.ErrTokenExpired}
	m := &stubMinter{}
	s := &stubFamilyStore{}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token_expired") {
		t.Errorf("body: got %q, expected token_expired", rec.Body.String())
	}
	// Mint must not have been called — saves a wasted JWT signing.
	if m.gotFamilyID != uuid.Nil {
		t.Errorf("minter was called with family %s — should not have been invoked on expired token", m.gotFamilyID)
	}
}

// TestRefresh_BadAudience_403 verifies a token with aud != refresh
// (e.g., a captured access token) returns 403 audience_mismatch.
// This is the audience-separation defense: an access token can
// never substitute as a refresh.
func TestRefresh_BadAudience_403(t *testing.T) {
	v := &stubVerifier{err: tokenverify.ErrTokenAudience}
	m := &stubMinter{}
	s := &stubFamilyStore{}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "access-token-misused"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "audience_mismatch") {
		t.Errorf("body: got %q, expected audience_mismatch", rec.Body.String())
	}
}

// TestRefresh_MissingFamilyID_401 verifies a token without
// family_id is rejected — could happen if a token was minted
// before family-id was rolled out, or if it was forged.
func TestRefresh_MissingFamilyID_401(t *testing.T) {
	c := validClaims()
	c.FamilyID = ""
	v := &stubVerifier{claims: c}
	rec := mountAndPost(t, v, &stubMinter{}, &stubFamilyStore{}, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_family_id") {
		t.Errorf("body: got %q, expected missing_family_id", rec.Body.String())
	}
}

// TestRefresh_MalformedRequestBody_400 verifies non-JSON or empty-
// body requests return 400 (not 401 — the auth-related errors
// only apply once we've parsed a token).
func TestRefresh_MalformedRequestBody_400(t *testing.T) {
	rec := mountAndPost(t, &stubVerifier{}, &stubMinter{}, &stubFamilyStore{}, "not json {")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_json") {
		t.Errorf("body: got %q, expected invalid_json", rec.Body.String())
	}
}

// TestRefresh_EmptyRefreshToken_400 verifies a request with a
// well-formed body but empty refresh_token returns 400.
func TestRefresh_EmptyRefreshToken_400(t *testing.T) {
	rec := mountAndPost(t, &stubVerifier{}, &stubMinter{}, &stubFamilyStore{}, map[string]string{"refresh_token": ""})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_refresh_token") {
		t.Errorf("body: got %q, expected missing_refresh_token", rec.Body.String())
	}
}

// TestRefresh_MintFails_500 verifies a keystore failure during
// mint surfaces as 500 (internal server error — not the user's
// fault).
func TestRefresh_MintFails_500(t *testing.T) {
	v := &stubVerifier{claims: validClaims()}
	m := &stubMinter{err: errors.New("keystore unavailable")}
	s := &stubFamilyStore{}

	rec := mountAndPost(t, v, m, s, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "mint_failed") {
		t.Errorf("body: got %q, expected mint_failed", rec.Body.String())
	}
}

// TestRefresh_PersonDeactivated_RevokesFamilyAnd401 is the
// GRO-949 acceptance probe. When the Person referenced by the
// refresh-token claims is no longer active (deactivated or deleted),
// the handler MUST:
//
//  1. Refuse to mint a new pair (no body leak — the new pair never
//     reaches the wire).
//  2. Revoke the family family-wide so subsequent refreshes also
//     fail (closes the rotation window before the next TTL).
//  3. Return 401 with the person_inactive code so clients re-login.
func TestRefresh_PersonDeactivated_RevokesFamilyAnd401(t *testing.T) {
	claims := validClaims()
	v := &stubVerifier{claims: claims}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{} // happy ValidateAndRotate path; should not be reached
	p := &stubPersonLookup{err: ErrPersonNotFound}

	rec := mountAndPostWithPersons(t, v, m, s, p, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "person_inactive") {
		t.Errorf("body: got %q, expected person_inactive", body)
	}

	// Critical (mirrors reuse-detection invariant): the new pair must
	// NOT leak into the response body.
	if strings.Contains(body, "new.access.token") || strings.Contains(body, "new.refresh.token") {
		t.Errorf("new pair leaked into response despite person-inactive: %s", body)
	}

	// PersonLookup must have been called with the PersonID from claims.
	wantPersonID, _ := uuid.Parse(claims.PersonID)
	if p.gotID != wantPersonID {
		t.Errorf("person lookup: got %s, want %s", p.gotID, wantPersonID)
	}

	// Family must be revoked family-wide so subsequent refreshes also
	// fail until re-login establishes a new family.
	wantFamilyID, _ := uuid.Parse(claims.FamilyID)
	if s.revokedFamilyID != wantFamilyID {
		t.Errorf("family revoke: got %s, want %s", s.revokedFamilyID, wantFamilyID)
	}
	if s.revokedReason != "person_inactive" {
		t.Errorf("revoke reason: got %q, want %q", s.revokedReason, "person_inactive")
	}

	// ValidateAndRotate must NOT have been called — we short-circuited
	// before the rotation lock ever opened.
	if s.gotFamilyID != uuid.Nil {
		t.Errorf("ValidateAndRotate ran despite inactive person: %s", s.gotFamilyID)
	}
}

// TestRefresh_HappyPath_ReloadsOrgFromPersonStore verifies the
// post-fix subject's OrgID reflects the freshly-read DB value, not
// the (potentially stale) value from claims. Catches the regression
// where a user moved between orgs would keep refreshing under the
// old org until their refresh TTL ended.
func TestRefresh_HappyPath_ReloadsOrgFromPersonStore(t *testing.T) {
	claims := validClaims()
	freshOrgID := uuid.New()
	personID, _ := uuid.Parse(claims.PersonID)

	v := &stubVerifier{claims: claims}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{}
	p := &stubPersonLookup{person: &Person{ID: personID, OrgID: freshOrgID, IsActive: true}}

	rec := mountAndPostWithPersons(t, v, m, s, p, map[string]string{"refresh_token": "old"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if m.gotSubject.OrgID != freshOrgID {
		t.Errorf("subject.OrgID: got %s, want %s (claims override should use DB org)", m.gotSubject.OrgID, freshOrgID)
	}
}

// TestRefresh_PersonLookupFails_500 verifies a transient DB error in
// the active-state check surfaces as 500 — we do NOT silently mint a
// pair when we can't verify the user is still active, and we do NOT
// revoke the family on a transient failure (revoke is reserved for
// the can-confirm-deactivated path).
func TestRefresh_PersonLookupFails_500(t *testing.T) {
	claims := validClaims()
	v := &stubVerifier{claims: claims}
	m := &stubMinter{pair: validPair()}
	s := &stubFamilyStore{}
	p := &stubPersonLookup{err: errors.New("identity db unavailable")}

	rec := mountAndPostWithPersons(t, v, m, s, p, map[string]string{"refresh_token": "old"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "lookup_failed") {
		t.Errorf("body: got %q, expected lookup_failed", rec.Body.String())
	}
	if s.revokedFamilyID != uuid.Nil {
		t.Errorf("family revoked on transient lookup failure: should only revoke when we can confirm deactivation")
	}
}
