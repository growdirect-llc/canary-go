// Package identity provides the auth substrate for Canary Go services:
// RS256 JWT validation against an IdP JWKS endpoint, per-agent scoped
// API keys backed by app.api_keys, and tenant boundary helpers.
//
// Spec: docs/sdds/canary-go/identity-auth-tenant.md.
//
// This file: RS256 JWT validation. The legacy HS256 path in
// internal/auth stays in place during the migration; new
// service-to-service traffic uses RS256 against the configured IdP
// (default: GCP Identity Platform).
//
// References:
//   - RFC 7519 — JSON Web Token
//   - RFC 7517 — JSON Web Key
//   - GCP Identity Platform JWKS: https://identitytoolkit.googleapis.com/v1/projects/<project>/jwks
package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Errors surfaced by JWT validation. Handlers map these to 401 / 403
// per the convention in docs/conventions.md.
var (
	ErrJWTMissingKid    = errors.New("identity: JWT header missing kid")
	ErrJWTUnknownKid    = errors.New("identity: JWT kid not in JWKS")
	ErrJWTInvalidClaims = errors.New("identity: JWT claims invalid")
	ErrJWTExpired       = errors.New("identity: JWT expired")
	ErrJWKSFetch        = errors.New("identity: JWKS fetch failed")
)

// JWTClaims is the minimal claim set Canary Go expects on every
// service-to-service or human-user token. The IdP signs these as
// custom claims; missing fields fail validation.
type JWTClaims struct {
	Subject  uuid.UUID `json:"sub"`
	TenantID uuid.UUID `json:"tenant_id"`
	Scopes   []string  `json:"scopes,omitempty"`
	jwt.RegisteredClaims
}

// JWTValidator holds the configuration + JWKS cache for RS256
// validation. One per service; reuse across requests.
type JWTValidator struct {
	jwksURL    string
	issuer     string
	audience   string
	cacheTTL   time.Duration
	httpClient *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewJWTValidator constructs a validator from environment configuration:
//
//   IDENTITY_JWKS_URL                 (required for non-test paths)
//   IDENTITY_JWT_ISSUER               (required)
//   IDENTITY_JWT_AUDIENCE             (required)
//   IDENTITY_JWKS_CACHE_TTL_SECONDS   (optional, default 300)
//
// When IDENTITY_JWKS_URL is empty the validator constructs in
// "disabled" mode — Validate returns ErrJWKSFetch on every call. This
// is the default test-environment posture. cmd binaries should call
// MustEnabled() at boot if they require RS256.
func NewJWTValidator() *JWTValidator {
	ttl := 300
	if v := os.Getenv("IDENTITY_JWKS_CACHE_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttl = n
		}
	}
	return &JWTValidator{
		jwksURL:    os.Getenv("IDENTITY_JWKS_URL"),
		issuer:     os.Getenv("IDENTITY_JWT_ISSUER"),
		audience:   os.Getenv("IDENTITY_JWT_AUDIENCE"),
		cacheTTL:   time.Duration(ttl) * time.Second,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		keys:       map[string]*rsa.PublicKey{},
	}
}

// Enabled reports whether the validator has a configured JWKS endpoint.
// Callers can use this to decide whether to mount JWT-required routes.
func (v *JWTValidator) Enabled() bool {
	return v.jwksURL != "" && v.issuer != "" && v.audience != ""
}

// Validate verifies the bearer token's signature, issuer, audience,
// and expiry; extracts JWTClaims on success. Returns ErrJWKSFetch
// when the validator is disabled (no configured JWKS).
func (v *JWTValidator) Validate(ctx context.Context, tokenStr string) (*JWTClaims, error) {
	if !v.Enabled() {
		return nil, ErrJWKSFetch
	}

	keyFunc := func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("%w: alg=%v", ErrJWTInvalidClaims, t.Header["alg"])
		}
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, ErrJWTMissingKid
		}
		key, err := v.keyFor(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	claims := &JWTClaims{}
	parser := jwt.NewParser(
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
	)
	tok, err := parser.ParseWithClaims(tokenStr, claims, keyFunc)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrJWTExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrJWTInvalidClaims, err)
	}
	if !tok.Valid {
		return nil, ErrJWTInvalidClaims
	}
	if claims.Subject == uuid.Nil || claims.TenantID == uuid.Nil {
		return nil, fmt.Errorf("%w: missing sub or tenant_id", ErrJWTInvalidClaims)
	}
	return claims, nil
}

// keyFor returns the cached public key for kid, refreshing the JWKS
// if the cache is stale or the kid is unknown. Read-locks for the hot
// path; write-locks only when refreshing.
func (v *JWTValidator) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	if time.Since(v.fetchedAt) < v.cacheTTL {
		if k, ok := v.keys[kid]; ok {
			v.mu.RUnlock()
			return k, nil
		}
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	// re-check after upgrading the lock
	if time.Since(v.fetchedAt) < v.cacheTTL {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
	}
	if err := v.refreshLocked(ctx); err != nil {
		return nil, err
	}
	k, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrJWTUnknownKid, kid)
	}
	return k, nil
}

// jwksDoc is the on-the-wire JWKS shape — one of many keys, each with
// a kid + RSA public-key components. Per RFC 7517.
type jwksDoc struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
		Alg string `json:"alg"`
		Use string `json:"use"`
	} `json:"keys"`
}

// refreshLocked must be called under v.mu held for write.
func (v *JWTValidator) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrJWKSFetch, err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSFetch, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrJWKSFetch, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrJWKSFetch, err)
	}

	var doc jwksDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%w: parse: %v", ErrJWKSFetch, err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	return nil
}

// rsaPublicKey reconstructs an RSA public key from base64url-encoded
// modulus + exponent per RFC 7517 §6.3.1.
func rsaPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, errors.New("public exponent overflow")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
