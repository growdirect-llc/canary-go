// Package me serves GET /v1/me — the WhoAmI RPC per GRO-848 contract
// surface 3. Verifies the bearer JWT against the keystore-backed
// verifier, looks up the Person record by sub claim, and returns
// the canonical contract shape.
//
// User store is currently stub. Until the Person table lands (Sprint
// 3 Wave 4 / T-6 JIT provisioning), the endpoint reflects what's
// available from the JWT claims directly: id from sub, email from
// claims.email if present, the rest empty. AtlasView's middleware
// caches per-jti for 60s anyway, so a richer payload from a real
// user table can swap in transparently when T-6 ships.
//
// T-3 / GRO-863.
package me

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// PersonRecord is the response shape per GRO-848 surface 3. JSON
// fields ordered to match the contract doc verbatim — receiving
// clients pattern-match on key order in some debug tooling.
type PersonRecord struct {
	ID                  string `json:"id"`
	Email               string `json:"email,omitempty"`
	Name                string `json:"name,omitempty"`
	FirstName           string `json:"first_name,omitempty"`
	LastName            string `json:"last_name,omitempty"`
	Phone               string `json:"phone,omitempty"`
	PictureURL          string `json:"picture_url,omitempty"`
	PictureThumbnailURL string `json:"picture_thumbnail_url,omitempty"`
	System              bool   `json:"system"`
}

// Verifier is the surface the handler depends on. Held as an
// interface so tests stub the verifier without a real keystore.
// *tokenverify.Verifier satisfies it.
type Verifier interface {
	Verify(ctx context.Context, tokenStr string) (*tokenverify.Claims, error)
}

// Handler serves /v1/me.
type Handler struct {
	verifier Verifier
	logger   *zap.Logger
}

// New wires a handler. Logger may be nil.
func New(verifier Verifier, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{verifier: verifier, logger: logger}
}

// Mount registers /v1/me on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/v1/me", h.serve)
}

// serve handles the GET. Bearer auth inline; could be extracted to a
// chi middleware once a second route needs the same gate (T-1's
// /auth/refresh will).
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	tokenStr, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing_bearer", "Authorization header missing or malformed")
		return
	}

	claims, err := h.verifier.Verify(r.Context(), tokenStr)
	if err != nil {
		// Map verifier sentinels to specific 401/403 buckets so
		// clients can distinguish "your token expired" from "your
		// token was forged" — but no further detail leaks beyond
		// what's needed for legitimate clients to act.
		switch {
		case errors.Is(err, tokenverify.ErrTokenExpired):
			writeError(w, http.StatusUnauthorized, "token_expired", "JWT exp claim is past")
		case errors.Is(err, tokenverify.ErrTokenAudience):
			writeError(w, http.StatusForbidden, "audience_mismatch", "JWT not issued for this audience")
		case errors.Is(err, tokenverify.ErrTokenIssuer):
			writeError(w, http.StatusForbidden, "issuer_mismatch", "JWT issuer not recognized")
		default:
			// Signature, malformed, unknown kid, alg violations all
			// collapse into "invalid token" — fingerprinting defense.
			h.logger.Debug("token verify failed", zap.Error(err))
			writeError(w, http.StatusUnauthorized, "invalid_token", "JWT signature or claim validation failed")
		}
		return
	}

	// Person record — minimal projection from claims until the user
	// table lands. PersonID claim takes precedence over Subject for
	// the id field (PersonID is the AtlasView-owned UUID; Subject is
	// the raw JWT sub).
	id := claims.PersonID
	if id == "" {
		id = claims.Subject
	}
	rec := PersonRecord{
		ID:     id,
		System: claims.UserType == "system",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=60") // matches AtlasView's per-jti cache
	_ = json.NewEncoder(w).Encode(rec)
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
// Case-insensitive on the scheme per RFC 6750. Returns "", false on
// any deviation from the canonical shape — clients sending bare
// tokens or wrong schemes get the same generic 401 as missing-header.
func bearerToken(authHeader string) (string, bool) {
	if authHeader == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(authHeader) < len(prefix) {
		return "", false
	}
	if !strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(authHeader[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// writeError emits the canary error envelope per docs/conventions.md.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}
