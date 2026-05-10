// Package auth implements the JWT mint endpoints from GRO-848
// surface 1: /auth/login, /auth/refresh, /auth/mfa.
//
// This file: /auth/refresh. The rotation endpoint composes:
//
//   - tokenverify.Verifier (audience "refresh") — validates the
//     incoming refresh token's signature, issuer, family, and
//     temporal claims.
//   - mint.Minter — produces the new access+refresh pair using
//     the keystore's active key.
//   - refreshfamily.Store — atomic check-and-rotate against the
//     family's last_jti; reuse detection revokes the family
//     family-wide before returning the error.
//
// Reuse-detection semantics (OAuth 2.1 / RFC 6819 §5.2.2.3): a
// refresh request whose jti != family.last_jti revokes the entire
// family. From that point forward every refresh against that family
// — including the legitimate client's — fails. The user has to log
// in again. This is intentional — the alternative is leaving a
// suspected-compromised family valid, which is worse.
//
// T-1 / GRO-861.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity/mint"
	"github.com/ruptiv/canary/internal/identity/refreshfamily"
	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// RefreshVerifier is the surface the handler depends on. Held as
// an interface so tests stub the verifier without a real keystore.
type RefreshVerifier interface {
	Verify(ctx context.Context, tokenStr string) (*tokenverify.Claims, error)
}

// PairMinter is the mint surface — same contract as *mint.Minter.
type PairMinter interface {
	MintPair(ctx context.Context, s mint.Subject, familyID uuid.UUID) (*mint.Pair, error)
}

// FamilyStore is the family-ledger surface. *refreshfamily.Store
// satisfies it. Revoke is invoked when post-verify, pre-mint state
// proves the refresh must not produce a new pair (e.g., deactivated
// person — GRO-949).
type FamilyStore interface {
	ValidateAndRotate(ctx context.Context, familyID uuid.UUID, presentedJTI, newJTI string) error
	Revoke(ctx context.Context, familyID uuid.UUID, reason string) error
}

// PersonLookup re-reads Person/Org state from the identity DB so the
// refresh handler can refuse rotation for deactivated/deleted users
// (GRO-949). *PersonStore satisfies it.
type PersonLookup interface {
	LookupByID(ctx context.Context, id uuid.UUID) (*Person, error)
}

// RefreshHandler serves /auth/refresh.
type RefreshHandler struct {
	verifier RefreshVerifier
	minter   PairMinter
	store    FamilyStore
	persons  PersonLookup
	logger   *zap.Logger
}

// NewRefreshHandler wires the handler. persons is consulted before
// minting so deactivated/deleted users cannot keep rotating.
func NewRefreshHandler(v RefreshVerifier, m PairMinter, s FamilyStore, persons PersonLookup, logger *zap.Logger) *RefreshHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RefreshHandler{verifier: v, minter: m, store: s, persons: persons, logger: logger}
}

// Mount registers POST /auth/refresh on r.
func (h *RefreshHandler) Mount(r chi.Router) {
	r.Post("/auth/refresh", h.serve)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"` // always "Bearer" per RFC 6749
	ExpiresIn    int64  `json:"expires_in"` // access TTL in seconds
}

// serve handles the rotation flow:
//
//  1. Decode body.
//  2. Verify the refresh token (signature, issuer, aud="refresh",
//     temporal claims).
//  3. Pull subject + family from the verified claims.
//  4. Mint a new access+refresh pair (under the same family).
//  5. Atomic ValidateAndRotate against the family ledger — if
//     presented jti != last_jti, family is revoked and ErrReuseDetected
//     surfaces. The new pair is discarded (never returned).
//  6. Return the new pair.
func (h *RefreshHandler) serve(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "missing_refresh_token", "refresh_token is required")
		return
	}

	// Step 1: verify the incoming token.
	claims, err := h.verifier.Verify(r.Context(), req.RefreshToken)
	if err != nil {
		// Same fingerprinting-defense buckets as /v1/me.
		switch {
		case errors.Is(err, tokenverify.ErrTokenExpired):
			writeError(w, http.StatusUnauthorized, "token_expired", "refresh token expired; user must log in")
		case errors.Is(err, tokenverify.ErrTokenAudience):
			writeError(w, http.StatusForbidden, "audience_mismatch", "token aud != refresh")
		default:
			h.logger.Debug("refresh: token verify failed", zap.Error(err))
			writeError(w, http.StatusUnauthorized, "invalid_token", "refresh token did not validate")
		}
		return
	}

	// Step 2: family-id and subject must be present.
	if claims.FamilyID == "" {
		// Legacy tokens minted before family-id was added — refuse
		// rather than silently accept; user logs in again.
		writeError(w, http.StatusUnauthorized, "missing_family_id", "refresh token has no family_id; re-login required")
		return
	}
	familyID, err := uuid.Parse(claims.FamilyID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "malformed_family_id", "family_id is not a valid UUID")
		return
	}
	subjectID, err := uuid.Parse(claims.Subject)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "malformed_subject", "sub is not a valid UUID")
		return
	}

	// Step 3: mint new pair (same family, fresh jtis). Mint happens
	// before the family check so the family-row lock is held for
	// minimal time — but we discard the pair if reuse is detected.
	subject := mint.Subject{
		UserID:   subjectID,
		UserType: claims.UserType,
		Scopes:   claims.Scopes,
	}
	if claims.OrgID != "" {
		if id, err := uuid.Parse(claims.OrgID); err == nil {
			subject.OrgID = id
		}
	}
	if claims.PersonID != "" {
		if id, err := uuid.Parse(claims.PersonID); err == nil {
			subject.PersonID = id
		}
	}

	// Step 3a (GRO-949): re-read Person/Org state. A deactivated or
	// deleted user must not be able to keep rotating refresh tokens
	// until the family TTL ends — that weakens incident response and
	// right-to-forget enforcement. When PersonID is present and the
	// active row is gone, revoke the family family-wide and 401.
	// PersonID-less subjects (system / service users) bypass this
	// check.
	if h.persons != nil && subject.PersonID != uuid.Nil {
		person, err := h.persons.LookupByID(r.Context(), subject.PersonID)
		switch {
		case errors.Is(err, ErrPersonNotFound):
			if revErr := h.store.Revoke(r.Context(), familyID, "person_inactive"); revErr != nil {
				h.logger.Error("refresh: revoke on inactive person",
					zap.String("family_id", familyID.String()),
					zap.String("person_id", subject.PersonID.String()),
					zap.Error(revErr))
			}
			h.logger.Warn("refresh: person inactive — family revoked",
				zap.String("family_id", familyID.String()),
				zap.String("person_id", subject.PersonID.String()))
			writeError(w, http.StatusUnauthorized, "person_inactive",
				"user is no longer active; refresh family revoked. Re-login required.")
			return
		case err != nil:
			h.logger.Error("refresh: person lookup",
				zap.String("person_id", subject.PersonID.String()),
				zap.Error(err))
			writeError(w, http.StatusInternalServerError, "lookup_failed",
				"could not verify person state")
			return
		}
		// Org assignment may have changed since the original token —
		// reflect current state in the new pair.
		subject.OrgID = person.OrgID
	}
	pair, err := h.minter.MintPair(r.Context(), subject, familyID)
	if err != nil {
		h.logger.Error("refresh: mint", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "mint_failed", "could not mint new token pair")
		return
	}

	// Step 4: atomic check + rotate.
	err = h.store.ValidateAndRotate(r.Context(), familyID, claims.ID, pair.RefreshJTI)
	switch {
	case err == nil:
		// Happy path; fall through.
	case errors.Is(err, refreshfamily.ErrReuseDetected):
		// Family is now revoked. Log loudly — this is either a
		// stolen-token attack or a misbehaving client (replaying
		// after an earlier successful refresh). Either way the
		// chain is dead.
		h.logger.Warn("refresh: reuse detected — family revoked",
			zap.String("family_id", familyID.String()),
			zap.String("subject", subjectID.String()))
		writeError(w, http.StatusUnauthorized, "reuse_detected",
			"refresh-token reuse detected; this family has been revoked. Re-login required.")
		return
	case errors.Is(err, refreshfamily.ErrFamilyRevoked):
		writeError(w, http.StatusUnauthorized, "family_revoked", "this refresh family has been revoked")
		return
	case errors.Is(err, refreshfamily.ErrFamilyNotFound):
		// Could be: user logged out and family was deleted, or the
		// token references a family the server doesn't know about.
		// 401 either way.
		writeError(w, http.StatusUnauthorized, "family_not_found", "refresh family not found")
		return
	default:
		h.logger.Error("refresh: family validate", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "rotate_failed", "could not rotate refresh family")
		return
	}

	// Step 5: return the new pair.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store") // RFC 6749 §5.1 — token responses must not be cached
	_ = json.NewEncoder(w).Encode(refreshResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(time.Until(pair.AccessExp).Seconds()),
	})
}

// writeError matches the canary error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
}
