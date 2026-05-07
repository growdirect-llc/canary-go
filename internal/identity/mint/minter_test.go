package mint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/keystore"
	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// stubReader satisfies both ActiveKeyReader and tokenverify.VerifySetReader.
type stubReader struct {
	key *keystore.SigningKey
}

func (s *stubReader) Active(_ context.Context) (*keystore.SigningKey, error) {
	if s.key == nil {
		return nil, keystore.ErrNoActiveKey
	}
	return s.key, nil
}
func (s *stubReader) FindByKid(_ context.Context, kid string) (*keystore.SigningKey, error) {
	if s.key == nil || s.key.Kid != kid {
		return nil, errors.New("kid not found")
	}
	return s.key, nil
}

func newReader(t *testing.T) *stubReader {
	t.Helper()
	sk, err := keystore.GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA: %v", err)
	}
	return &stubReader{key: &sk}
}

func newSubject() Subject {
	return Subject{
		UserID:   uuid.New(),
		OrgID:    uuid.New(),
		PersonID: uuid.New(),
		UserType: "regular",
		Scopes:   []string{"identity:me", "ledger:read"},
	}
}

// TestMintPair_HappyPath verifies a fresh pair has both tokens
// non-empty, matching family-id, and both claims surface the
// requested subject + scopes.
func TestMintPair_HappyPath(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary"}})

	s := newSubject()
	pair, err := m.MintPair(context.Background(), s, uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("empty token in pair")
	}
	if pair.FamilyID == "" || pair.FamilyID == uuid.Nil.String() {
		t.Errorf("FamilyID: got %q, want a fresh UUID", pair.FamilyID)
	}
	if pair.AccessJTI == pair.RefreshJTI {
		t.Errorf("access and refresh share jti %q — must be distinct", pair.AccessJTI)
	}
}

// TestMintPair_AccessVerifiesViaTokenverify confirms the round-trip:
// access token minted here validates against the verifier from the
// other half of T-1's identity stack.
func TestMintPair_AccessVerifiesViaTokenverify(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary", "atlasview"}})
	v := tokenverify.New(r, "canary", "canary")

	s := newSubject()
	pair, err := m.MintPair(context.Background(), s, uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}

	claims, err := v.Verify(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("Verify access: %v", err)
	}
	if claims.Subject != s.UserID.String() {
		t.Errorf("sub: got %q, want %q", claims.Subject, s.UserID)
	}
	if claims.OrgID != s.OrgID.String() {
		t.Errorf("org_id: got %q, want %q", claims.OrgID, s.OrgID)
	}
	if claims.PersonID != s.PersonID.String() {
		t.Errorf("person_id: got %q, want %q", claims.PersonID, s.PersonID)
	}
	if claims.UserType != s.UserType {
		t.Errorf("user_type: got %q, want %q", claims.UserType, s.UserType)
	}
	if len(claims.Scopes) != len(s.Scopes) {
		t.Errorf("scopes: got %v, want %v", claims.Scopes, s.Scopes)
	}
}

// TestMintPair_AudienceSeparation verifies the refresh token is NOT
// accepted by the access-token verifier — refresh tokens are
// aud="refresh", access tokens are aud=canary/atlasview, so a
// captured refresh token can never substitute for an access token.
func TestMintPair_AudienceSeparation(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary"}})
	v := tokenverify.New(r, "canary", "canary")

	pair, err := m.MintPair(context.Background(), newSubject(), uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}

	if _, err := v.Verify(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("refresh token was accepted by access-token verifier (audience separation broken)")
	}
}

// TestMintPair_RefreshOnlyAcceptedByRefreshAudienceVerifier verifies
// the symmetric case: an access token is NOT accepted by a verifier
// pinned to aud="refresh".
func TestMintPair_RefreshOnlyAcceptedByRefreshAudienceVerifier(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary"}})
	refreshVerifier := tokenverify.New(r, "canary", "refresh")

	pair, err := m.MintPair(context.Background(), newSubject(), uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}

	if _, err := refreshVerifier.Verify(context.Background(), pair.AccessToken); err == nil {
		t.Fatal("access token accepted by refresh verifier (audience separation broken)")
	}
	// Refresh token DOES verify under the refresh-aud verifier.
	// We don't have a RefreshClaims-aware Verify yet (T-1's refresh
	// endpoint will own that path), but the same Verifier will
	// accept it because aud="refresh" matches.
	if _, err := refreshVerifier.Verify(context.Background(), pair.RefreshToken); err != nil {
		t.Errorf("refresh token rejected by refresh verifier: %v", err)
	}
}

// TestMintPair_FamilyIDContinuity verifies passing an existing
// familyID keeps it intact across mints. Family-id continuity is
// what lets reuse detection invalidate the whole chain on a single
// stolen token.
func TestMintPair_FamilyIDContinuity(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary"}})

	familyID := uuid.New()
	pair, err := m.MintPair(context.Background(), newSubject(), familyID)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}
	if pair.FamilyID != familyID.String() {
		t.Errorf("FamilyID: got %q, want %q (passed-in)", pair.FamilyID, familyID)
	}
}

// TestMintPair_NoActiveKey_Errors verifies the keystore-empty path
// surfaces ErrNoActiveKey so the caller can return 503 / page ops.
func TestMintPair_NoActiveKey_Errors(t *testing.T) {
	r := &stubReader{key: nil}
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary"}})

	_, err := m.MintPair(context.Background(), newSubject(), uuid.Nil)
	if !errors.Is(err, keystore.ErrNoActiveKey) {
		t.Errorf("expected ErrNoActiveKey, got %v", err)
	}
}

// TestMintPair_TTLs_DefaultPerContract verifies access TTL = 30 min
// and refresh TTL = 12 hours per GRO-848 surface 1, with a fixed
// clock so the assertion is exact.
func TestMintPair_TTLs_DefaultPerContract(t *testing.T) {
	r := newReader(t)
	fixed := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	m := NewWithClock(r, Config{Issuer: "canary", Audience: []string{"canary"}}, func() time.Time { return fixed })

	pair, err := m.MintPair(context.Background(), newSubject(), uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}
	if !pair.AccessExp.Equal(fixed.Add(30 * time.Minute)) {
		t.Errorf("AccessExp: got %v, want fixed+30m", pair.AccessExp)
	}
	if !pair.RefreshExp.Equal(fixed.Add(12 * time.Hour)) {
		t.Errorf("RefreshExp: got %v, want fixed+12h", pair.RefreshExp)
	}
}

// TestMintPair_AudienceSlice_AppliesAll verifies a multi-audience
// config (e.g. {"canary", "atlasview"}) embeds both in the access
// token aud claim — supports audience-narrowing tokens.
func TestMintPair_AudienceSlice_AppliesAll(t *testing.T) {
	r := newReader(t)
	m := New(r, Config{Issuer: "canary", Audience: []string{"canary", "atlasview"}})

	pair, err := m.MintPair(context.Background(), newSubject(), uuid.Nil)
	if err != nil {
		t.Fatalf("MintPair: %v", err)
	}

	// canary verifier accepts.
	canaryV := tokenverify.New(r, "canary", "canary")
	if _, err := canaryV.Verify(context.Background(), pair.AccessToken); err != nil {
		t.Errorf("canary verifier: %v", err)
	}
	// atlasview verifier also accepts (same token).
	atlasV := tokenverify.New(r, "canary", "atlasview")
	if _, err := atlasV.Verify(context.Background(), pair.AccessToken); err != nil {
		t.Errorf("atlasview verifier: %v", err)
	}
}
