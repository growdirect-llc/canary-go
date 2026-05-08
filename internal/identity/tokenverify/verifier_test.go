package tokenverify

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/keystore"
)

// stubReader satisfies VerifySetReader for tests.
type stubReader struct {
	keys map[string]*keystore.SigningKey // by kid
}

func (s *stubReader) FindByKid(_ context.Context, kid string) (*keystore.SigningKey, error) {
	k, ok := s.keys[kid]
	if !ok {
		return nil, errors.New("kid not found")
	}
	return k, nil
}

// mintTestToken signs a JWT with the keystore key for use by Verify
// tests. Mirrors the shape T-1's minter will produce.
func mintTestToken(t *testing.T, sk *keystore.SigningKey, claims jwt.Claims) string {
	t.Helper()
	block, _ := pem.Decode([]byte(sk.PrivateKeyPEM))
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS1: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = sk.Kid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func newKeystore(t *testing.T) *stubReader {
	t.Helper()
	sk, err := keystore.GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA: %v", err)
	}
	return &stubReader{keys: map[string]*keystore.SigningKey{sk.Kid: &sk}}
}

func goodClaims() *Claims {
	return &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "canary",
			Audience:  jwt.ClaimStrings{"atlasview"},
			Subject:   uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		OrgID:    uuid.New().String(),
		PersonID: uuid.New().String(),
		UserType: "regular",
		Scopes:   []string{"identity:me"},
	}
}

func ks(t *testing.T) (*stubReader, *keystore.SigningKey) {
	r := newKeystore(t)
	for _, k := range r.keys {
		return r, k
	}
	t.Fatal("no key in stub reader")
	return nil, nil
}

// TestVerify_HappyPath verifies a freshly-minted token in the
// canonical contract shape (issuer=canary, aud=atlasview, RS256)
// validates cleanly and returns the expected Claims.
func TestVerify_HappyPath(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	wantClaims := goodClaims()
	tok := mintTestToken(t, sk, wantClaims)

	gotClaims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if gotClaims.Subject != wantClaims.Subject {
		t.Errorf("sub: got %q, want %q", gotClaims.Subject, wantClaims.Subject)
	}
	if gotClaims.OrgID != wantClaims.OrgID {
		t.Errorf("org_id: got %q, want %q", gotClaims.OrgID, wantClaims.OrgID)
	}
	if gotClaims.UserType != wantClaims.UserType {
		t.Errorf("user_type: got %q, want %q", gotClaims.UserType, wantClaims.UserType)
	}
}

// TestVerify_ExpiredToken_Rejects verifies an exp-in-the-past
// token returns ErrTokenExpired so the bearer middleware can map
// to 401.
func TestVerify_ExpiredToken_Rejects(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	c := goodClaims()
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Minute))
	tok := mintTestToken(t, sk, c)

	_, err := v.Verify(context.Background(), tok)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

// TestVerify_BadAudience_Rejects verifies a token minted for a
// different audience returns ErrTokenAudience — the contract's
// audience-pinning defense.
func TestVerify_BadAudience_Rejects(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	c := goodClaims()
	c.Audience = jwt.ClaimStrings{"wrong-service"}
	tok := mintTestToken(t, sk, c)

	_, err := v.Verify(context.Background(), tok)
	if !errors.Is(err, ErrTokenAudience) {
		t.Errorf("expected ErrTokenAudience, got %v", err)
	}
}

// TestVerify_MultiAudience_AcceptsIfContains verifies a token whose
// aud claim is an array containing our audience PLUS others passes —
// audience-narrowing tokens (issued for both atlasview AND canary
// for example) must validate at both endpoints.
func TestVerify_MultiAudience_AcceptsIfContains(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	c := goodClaims()
	c.Audience = jwt.ClaimStrings{"atlasview", "canary", "other-svc"}
	tok := mintTestToken(t, sk, c)

	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Errorf("multi-aud token rejected: %v", err)
	}
}

// TestVerify_BadIssuer_Rejects verifies a token from a different
// issuer returns ErrTokenIssuer.
func TestVerify_BadIssuer_Rejects(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	c := goodClaims()
	c.Issuer = "evil-idp"
	tok := mintTestToken(t, sk, c)

	_, err := v.Verify(context.Background(), tok)
	if !errors.Is(err, ErrTokenIssuer) {
		t.Errorf("expected ErrTokenIssuer, got %v", err)
	}
}

// TestVerify_BadSignature_Rejects verifies a token whose signature
// has been tampered with returns ErrTokenSignature. Tampers in the
// middle of the signature segment (not the last char — base64url's
// last char can encode padding bits that decode to identical bytes,
// so end-tampering can spuriously pass).
func TestVerify_BadSignature_Rejects(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	tok := mintTestToken(t, sk, goodClaims())
	// Find the second dot — start of signature segment.
	dot1 := strings.IndexByte(tok, '.')
	dot2 := dot1 + 1 + strings.IndexByte(tok[dot1+1:], '.')
	sigStart := dot2 + 1
	// Tamper a byte ~25% into the signature so we're well clear of
	// any base64 padding-bit edge cases.
	tamperIdx := sigStart + (len(tok)-sigStart)/4
	original := tok[tamperIdx]
	replacement := byte('A')
	if original == 'A' {
		replacement = 'B'
	}
	tampered := tok[:tamperIdx] + string(replacement) + tok[tamperIdx+1:]
	if tampered == tok {
		t.Fatal("tamper produced identical token — test setup bug")
	}

	_, err := v.Verify(context.Background(), tampered)
	if !errors.Is(err, ErrTokenSignature) {
		t.Errorf("expected ErrTokenSignature, got %v", err)
	}
}

// TestVerify_UnknownKid_Rejects verifies a token whose kid is not
// in the keystore (e.g. token signed by an expired-and-removed key,
// or a token forged with a kid that never existed) returns
// ErrTokenUnknownKid.
func TestVerify_UnknownKid_Rejects(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	// Mint with the real key, then rewrite the kid header to a non-
	// existent one. We need a token whose kid lookup misses but
	// whose other fields are otherwise valid.
	tok := mintTestToken(t, sk, goodClaims())

	// To get an unknown kid we mint a SECOND key, sign with it, but
	// don't add it to the store.
	other, _ := keystore.GenerateRSA()
	otherTok := mintTestToken(t, &other, goodClaims())

	_ = tok // first token works; we want the second to fail
	_, err := v.Verify(context.Background(), otherTok)
	if !errors.Is(err, ErrTokenUnknownKid) {
		t.Errorf("expected ErrTokenUnknownKid, got %v", err)
	}
}

// TestVerify_AlgorithmConfusion_HS256AgainstRSAKey verifies the
// classic alg-confusion attack is blocked: an attacker can't take
// our RSA public key, sign a token with HS256 using the public key
// AS the HMAC secret, and have it accepted. jwt.WithValidMethods
// at parse time + alg-pinning at the keyfunc both gate this.
func TestVerify_AlgorithmConfusion_HS256AgainstRSAKey(t *testing.T) {
	r, sk := ks(t)
	v := New(r, "canary", "atlasview")

	// Attacker mints HS256 token using our public-key bytes as the
	// HMAC secret.
	block, _ := pem.Decode([]byte(sk.PrivateKeyPEM))
	priv, _ := x509.ParsePKCS1PrivateKey(block.Bytes)
	pubBytes := x509.MarshalPKCS1PublicKey(&priv.PublicKey)

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, goodClaims())
	tok.Header["kid"] = sk.Kid
	signed, err := tok.SignedString(pubBytes)
	if err != nil {
		t.Fatalf("HS256 sign: %v", err)
	}

	_, err = v.Verify(context.Background(), signed)
	if err == nil {
		t.Fatal("HS256-against-RSA-public-key was accepted — algorithm confusion not blocked")
	}
	// Either ErrTokenAlgorithm (pinning) or ErrTokenSignature is
	// acceptable — both stop the attack.
}

// TestVerify_NoneAlg_Rejects verifies the legacy alg=none attack is
// blocked. JWT libraries have historically had bugs that let
// alg=none bypass signature checks; we defend at parse-time via
// jwt.WithValidMethods.
func TestVerify_NoneAlg_Rejects(t *testing.T) {
	r, _ := ks(t)
	v := New(r, "canary", "atlasview")

	// Hand-rolled alg=none token (no library produces this anymore,
	// so we craft it directly).
	header := `{"alg":"none","typ":"JWT","kid":"any"}`
	payload := `{"iss":"canary","aud":"atlasview","sub":"u","exp":9999999999,"iat":1}`
	tok := base64URL([]byte(header)) + "." + base64URL([]byte(payload)) + "."

	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("alg=none token was accepted")
	}
}

// base64URL is a tiny RawURLEncoding helper for the alg=none test
// (avoids dragging encoding/base64 into the test file's main
// imports for one use site).
func base64URL(b []byte) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, (len(b)*4+2)/3)
	for i := 0; i < len(b); i += 3 {
		var n uint32
		var l int
		for j := 0; j < 3; j++ {
			n <<= 8
			if i+j < len(b) {
				n |= uint32(b[i+j])
				l++
			}
		}
		switch l {
		case 3:
			out = append(out, tbl[(n>>18)&0x3f], tbl[(n>>12)&0x3f], tbl[(n>>6)&0x3f], tbl[n&0x3f])
		case 2:
			out = append(out, tbl[(n>>18)&0x3f], tbl[(n>>12)&0x3f], tbl[(n>>6)&0x3f])
		case 1:
			out = append(out, tbl[(n>>18)&0x3f], tbl[(n>>12)&0x3f])
		}
	}
	return string(out)
}
