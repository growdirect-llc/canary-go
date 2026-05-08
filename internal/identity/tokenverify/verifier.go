// Package tokenverify validates JWTs minted by canary.go's identity
// service against the keystore-backed verify set. Used by:
//
//   - T-3's BearerJWTRequired middleware on /v1/me and other
//     authenticated routes.
//   - T-1's refresh endpoint (validates the incoming refresh token).
//   - Future internal services that gate on a JWT.
//
// Distinct from internal/identity/jwt.go's JWTValidator, which
// validates against a REMOTE JWKS URL (consumes JWTs minted by an
// external IdP). This verifier reads its keys directly from the
// local keystore — no network round-trip when both the minter and
// the verifier live in the same binary.
//
// T-3 / GRO-863.
package tokenverify

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ruptiv/canary/internal/identity/keystore"
)

// Errors callers can errors.Is against. Bearer middleware maps each
// to the matching HTTP status; everything else is opaque so a
// fingerprinting attacker can't tell expired-vs-bad-signature.
var (
	ErrTokenExpired    = errors.New("tokenverify: token expired")
	ErrTokenSignature  = errors.New("tokenverify: signature does not validate")
	ErrTokenAudience   = errors.New("tokenverify: audience mismatch")
	ErrTokenIssuer     = errors.New("tokenverify: issuer mismatch")
	ErrTokenAlgorithm  = errors.New("tokenverify: algorithm not in whitelist")
	ErrTokenMissingKid = errors.New("tokenverify: token header missing kid")
	ErrTokenUnknownKid = errors.New("tokenverify: kid not in keystore verify set")
	ErrTokenMalformed  = errors.New("tokenverify: token malformed")
)

// Claims is the canonical claim shape emitted by canary.go's minter
// (T-1 populates the same struct). Verifier returns it so callers
// don't have to re-parse jwt.Token internals.
//
// Both access and refresh tokens carry this shape — only the aud
// claim differentiates. Access tokens have aud ∈ {canary, atlasview};
// refresh tokens have aud="refresh". FamilyID is populated on both
// for forensic continuity (audit log + family-rotation lookup at
// /auth/refresh time).
type Claims struct {
	jwt.RegisteredClaims          // exp, iat, iss, aud, sub, jti, nbf
	OrgID                string   `json:"org_id,omitempty"`    // tenant UUID
	PersonID             string   `json:"person_id,omitempty"` // user UUID
	UserType             string   `json:"user_type,omitempty"` // read_only|regular|power|admin|system
	Scopes               []string `json:"scopes,omitempty"`
	FamilyID             string   `json:"family_id,omitempty"` // refresh-token family — T-1 / GRO-861
}

// VerifySetReader is the keystore surface the verifier depends on —
// FindByKid + VerifySet are enough to dispatch a token to its
// public key. Held as an interface so tests stub the keystore
// without a pgx pool.
type VerifySetReader interface {
	FindByKid(ctx context.Context, kid string) (*keystore.SigningKey, error)
}

// Verifier holds verifier state. Issuer + audience are pinned at
// construction so the contract is enforced at every Verify() call —
// caller can't accidentally accept tokens with a different aud.
type Verifier struct {
	store    VerifySetReader
	issuer   string
	audience string
}

// New constructs a Verifier for tokens issued by `issuer` with
// `audience` in the aud claim. Both must match exactly — partial
// audience matching is a common source of token-forwarding attacks.
func New(store VerifySetReader, issuer, audience string) *Verifier {
	return &Verifier{store: store, issuer: issuer, audience: audience}
}

// Verify parses and validates a JWT against the keystore. Returns
// the decoded Claims on success.
//
// Validation order (each failure produces a distinct error):
//  1. Token parses
//  2. Header carries a kid
//  3. kid is in the keystore verify set
//  4. Algorithm matches the keystore record's alg (defends against
//     algorithm-confusion attacks where a token signed with HS256
//     could be verified using the RSA public key as the HMAC secret)
//  5. Signature validates
//  6. exp not past, nbf not future
//  7. iss matches v.issuer
//  8. aud contains v.audience
func (v *Verifier) Verify(ctx context.Context, tokenStr string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		// Header kid → keystore lookup.
		kidRaw, ok := t.Header["kid"]
		if !ok {
			return nil, ErrTokenMissingKid
		}
		kid, ok := kidRaw.(string)
		if !ok || kid == "" {
			return nil, ErrTokenMissingKid
		}

		sk, err := v.store.FindByKid(ctx, kid)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrTokenUnknownKid, kid)
		}

		// Algorithm pinning — token's alg must match the keystore
		// record's alg. This blocks alg=none (the keystore CHECK
		// constraint excludes "none" from valid algs) AND alg-
		// confusion (HS256 against an RSA public key).
		if t.Method.Alg() != sk.Alg {
			return nil, fmt.Errorf("%w: token alg=%s, key alg=%s",
				ErrTokenAlgorithm, t.Method.Alg(), sk.Alg)
		}

		// Decode the public key. Today only RS256 is supported (the
		// keystore generator only mints RS256). ES256 + EdDSA land
		// when those algorithms are adopted operationally.
		//
		// Prefer PublicJWK when present — keystore.VerifySet returns
		// rows with PrivateKeyPEM cleared (verifiers must not see
		// private material). Fall back to PrivateKeyPEM for the
		// stub-keystore unit-test path that pre-dates the JWK column.
		switch sk.Alg {
		case keystore.AlgRS256:
			if len(sk.PublicJWK) > 0 {
				return parseRSAPublicKeyFromJWK(sk.PublicJWK)
			}
			return parseRSAPublicKeyFromPEM(sk.PrivateKeyPEM)
		default:
			return nil, fmt.Errorf("%w: %s not yet implemented", ErrTokenAlgorithm, sk.Alg)
		}
	},
		jwt.WithValidMethods([]string{keystore.AlgRS256}), // double-check at the parser layer
	)

	if err != nil {
		return nil, mapJWTError(err)
	}
	if !token.Valid {
		return nil, ErrTokenSignature
	}

	// Issuer + audience pinning. RegisteredClaims has GetIssuer + GetAudience but
	// they don't enforce equality — we do it explicitly.
	if claims.Issuer != v.issuer {
		return nil, fmt.Errorf("%w: token iss=%q, want %q", ErrTokenIssuer, claims.Issuer, v.issuer)
	}
	if !audienceContains(claims.Audience, v.audience) {
		return nil, fmt.Errorf("%w: token aud=%v, want contains %q", ErrTokenAudience, claims.Audience, v.audience)
	}

	return claims, nil
}

// parseRSAPublicKeyFromPEM extracts the RSA public key from a
// PKCS1 RSA private key PEM. Used by the unit-test path where a
// stub keystore returns SigningKey rows with PrivateKeyPEM populated.
// Production keystore.VerifySet clears PrivateKeyPEM (verifiers
// don't see private material) — the JWK path is taken instead.
func parseRSAPublicKeyFromPEM(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("tokenverify: failed to decode PEM")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("tokenverify: parse PKCS1: %w", err)
	}
	return &priv.PublicKey, nil
}

// parseRSAPublicKeyFromJWK reconstructs an RSA public key from the
// JWK representation stored in keystore.SigningKey.PublicJWK. RFC
// 7517 §6.3.1 — n + e are base64url-RAW-encoded big-endian bytes.
// This is the production verifier path.
func parseRSAPublicKeyFromJWK(jwkBytes json.RawMessage) (*rsa.PublicKey, error) {
	var jwk struct {
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	if err := json.Unmarshal(jwkBytes, &jwk); err != nil {
		return nil, fmt.Errorf("tokenverify: unmarshal jwk: %w", err)
	}
	if jwk.Kty != "RSA" {
		return nil, fmt.Errorf("tokenverify: unsupported kty %q", jwk.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("tokenverify: decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("tokenverify: decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, fmt.Errorf("tokenverify: rsa exponent overflow")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

// audienceContains is the manual aud-contains check. JWT aud can be
// a string OR an array; jwt.ClaimStrings normalizes that into a
// slice for us.
func audienceContains(aud jwt.ClaimStrings, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}

// mapJWTError translates jwt-go errors into our package-level
// sentinel errors so callers can errors.Is them.
func mapJWTError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrTokenExpired
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return ErrTokenExpired
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return ErrTokenSignature
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ErrTokenMalformed
	case errors.Is(err, ErrTokenMissingKid),
		errors.Is(err, ErrTokenUnknownKid),
		errors.Is(err, ErrTokenAlgorithm),
		errors.Is(err, ErrTokenAudience),
		errors.Is(err, ErrTokenIssuer):
		return err
	default:
		return fmt.Errorf("tokenverify: %w", err)
	}
}
