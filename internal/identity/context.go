// internal/identity/context.go
//
// Request-context plumbing for auth claims. Lives in the identity
// package (not internal/tenant) so all auth-method-specific shapes
// can co-evolve here. internal/tenant still owns the tenant-only
// boundary helpers (RequireMerchant, FromContext) for backward
// compatibility with existing handlers.

package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/tenant"
)

// ErrTenantMismatch is returned by AssertBodyTenantMatches when the
// tenant_id in the request body does not match the authenticated
// tenant. Handlers map this to 403.
var ErrTenantMismatch = errors.New("identity: body tenant_id does not match authenticated tenant")

// AuthMethod values for downstream code that needs to distinguish
// the path that produced the claim.
const (
	AuthMethodJWT        = "jwt"
	AuthMethodAPIKey     = "apikey"
	AuthMethodLegacyHMAC = "legacy_hmac"
)

// Claims is the unified per-request auth shape. Either JWT or API key
// flows populate it.
type Claims struct {
	TenantID   uuid.UUID // zero on platform-scope API key paths
	AgentName  string    // populated on API key auth
	UserID     uuid.UUID // populated on JWT auth (zero on API key)
	Scopes     []string  // API key scopes (nil on JWT paths unless IdP supplies)
	AuthMethod string    // jwt | apikey | legacy_hmac
	KeyID      uuid.UUID // populated on API key auth (zero otherwise)
}

type contextKey string

const claimsKey contextKey = "identity.claims"

// InjectClaims returns ctx augmented with the auth claims. Both JWT
// and API-key paths funnel through here.
func InjectClaims(ctx context.Context, c Claims) context.Context {
	ctx = context.WithValue(ctx, claimsKey, c)
	if c.TenantID != uuid.Nil {
		// also populate the legacy tenant.merchantIDKey so existing
		// handlers using tenant.FromContext keep working
		ctx = tenant.InjectMerchantID(ctx, c.TenantID)
	}
	return ctx
}

// ClaimsFromContext returns the claims attached by InjectClaims.
// Returns (Claims{}, false) if none are set.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}

// InjectAPIKeyClaims is the convenience adapter for APIKeyMiddleware.
// Maps the local APIKeyAuthClaims into the unified Claims shape.
func InjectAPIKeyClaims(ctx context.Context, ak APIKeyAuthClaims) context.Context {
	c := Claims{
		AgentName:  ak.AgentName,
		Scopes:     ak.Scopes,
		AuthMethod: AuthMethodAPIKey,
		KeyID:      ak.KeyID,
	}
	if ak.TenantID != nil {
		c.TenantID = *ak.TenantID
	}
	return InjectClaims(ctx, c)
}

// InjectJWTClaims is the convenience adapter for the JWT path.
func InjectJWTClaims(ctx context.Context, jc JWTClaims) context.Context {
	return InjectClaims(ctx, Claims{
		TenantID:   jc.TenantID,
		UserID:     jc.Subject,
		Scopes:     jc.Scopes,
		AuthMethod: AuthMethodJWT,
	})
}

// AssertBodyTenantMatches enforces the cross-tenant defense: a
// request body that names a tenant_id different from the
// authenticated tenant returns ErrTenantMismatch.
//
// Skips the check when:
//
//   - the request is on a platform-scope API key (claims.TenantID is
//     uuid.Nil) — those keys are intentionally cross-tenant
//   - bodyTenantID is uuid.Nil — the body didn't specify a tenant
func AssertBodyTenantMatches(ctx context.Context, bodyTenantID uuid.UUID) error {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		// Unauthenticated routes don't have claims to compare against
		// — caller decides whether to enforce 401 separately.
		return nil
	}
	if c.TenantID == uuid.Nil {
		return nil
	}
	if bodyTenantID == uuid.Nil {
		return nil
	}
	if c.TenantID != bodyTenantID {
		return ErrTenantMismatch
	}
	return nil
}

// RequireScope is a handler-level helper that returns true if the
// claims attached to ctx include the named scope. Returns false on
// missing claims or scope absence.
func RequireScope(ctx context.Context, scope string) bool {
	c, ok := ClaimsFromContext(ctx)
	if !ok {
		return false
	}
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
