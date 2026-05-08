// Package auth implements the JWT mint endpoints from GRO-848
// surface 1: /auth/login, /auth/refresh, /auth/mfa.
//
// This file: /auth/login. Composes:
//
//   - PersonStore (canary_identity_gcp) — Person lookup by email
//   - argon2id credential verify
//   - mint.Minter — produces the access+refresh pair using the
//     keystore active key
//   - refreshfamily.Store (canary_gcp.app.refresh_token_families)
//     — records the new family on the just-minted refresh jti
//
// Design notes:
//   - email-not-found and bad-password collapse to a single generic
//     401; both paths run argon2id verify (dummy hash on miss) so
//     timing is roughly constant
//   - MFA-enabled accounts are rejected with code=mfa_required;
//     T-1.c will replace the rejection with a challenge endpoint
//   - Audience narrowing happens at mint time: the request optionally
//     specifies which of {canary, atlasview} the access token is
//     valid for; the refresh token always carries aud=refresh per
//     mint.Minter's audience-separation invariant
//
// T-1.a / GRO-848.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/identity/mint"
)

const loginRequestBodyLimit = 1 << 14 // 16 KiB

// FamilyCreator is the refreshfamily surface the login handler
// depends on. *refreshfamily.Store satisfies it.
type FamilyCreator interface {
	Create(ctx context.Context, familyID, subject uuid.UUID, jti string) error
}

// LoginMinter is the mint surface — same contract as *mint.Minter.
// Held as an interface so tests stub the minter without a real
// keystore.
type LoginMinter interface {
	MintPair(ctx context.Context, s mint.Subject, familyID uuid.UUID) (*mint.Pair, error)
}

// LoginHandler serves POST /auth/login.
type LoginHandler struct {
	persons  *PersonStore
	minter   LoginMinter
	families FamilyCreator
	logger   *zap.Logger
}

// NewLoginHandler wires the handler. Logger may be nil.
func NewLoginHandler(persons *PersonStore, minter LoginMinter, families FamilyCreator, logger *zap.Logger) *LoginHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &LoginHandler{persons: persons, minter: minter, families: families, logger: logger}
}

// Mount registers POST /auth/login on r.
func (h *LoginHandler) Mount(r chi.Router) {
	r.Post("/auth/login", h.login)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	PersonID     string `json:"person_id"`
	OrgID        string `json:"org_id"`
}

// dummyHashOnce holds a precomputed argon2 hash used to even out the
// timing of "email not found" against "password mismatch."
var (
	dummyHashOnce sync.Once
	dummyHash     string
)

func ensureDummyHash() string {
	dummyHashOnce.Do(func() {
		h, err := HashPassword("__dummy_argon2id_target__never_used__")
		if err != nil {
			dummyHash = ""
			return
		}
		dummyHash = h
	})
	return dummyHash
}

func (h *LoginHandler) login(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, loginRequestBodyLimit))
	if err != nil {
		writeAuthErr(w, http.StatusBadRequest, "body_read_failed", "could not read body")
		return
	}
	var req loginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAuthErr(w, http.StatusBadRequest, "malformed_json", "body must be JSON")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		// Always 401 (not 400) for missing credentials so callers
		// can't probe the route by submitting empties.
		writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	// Lookup. Email-not-found and password-mismatch land in the same
	// generic 401 to prevent enumeration; both paths run argon2id
	// so the timing is similar.
	pc, lookupErr := h.persons.LookupForLogin(r.Context(), req.Email)
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrPersonLocked) {
			writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		if errors.Is(lookupErr, ErrPersonNotFound) {
			_ = VerifyPassword(req.Password, ensureDummyHash())
			writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		h.logger.Error("auth login lookup", zap.Error(lookupErr))
		writeAuthErr(w, http.StatusInternalServerError, "lookup_failed", "internal error")
		return
	}

	if err := VerifyPassword(req.Password, pc.PasswordHash); err != nil {
		// Wrong password — bump the failure counter for forensics.
		// Threshold-based lockout is filed as a Sprint 4 follow-up
		// (it needs failed_login_count surfaced on the read).
		if err := h.persons.MarkLoginFailure(r.Context(), pc.ID, nil); err != nil {
			h.logger.Warn("mark login failure", zap.Error(err))
		}
		writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	if pc.MFAEnabled {
		writeAuthErr(w, http.StatusUnauthorized, "mfa_required",
			"this account requires multi-factor authentication")
		return
	}

	// Mint. familyID = uuid.Nil tells mint.MintPair to start a
	// fresh family — it generates one and returns it on Pair.FamilyID.
	subject := mint.Subject{
		UserID:   pc.ID, // sub = Person id; refresh handler reads back to look up Person
		OrgID:    pc.OrgID,
		PersonID: pc.ID,
		UserType: pc.UserType,
	}
	pair, err := h.minter.MintPair(r.Context(), subject, uuid.Nil)
	if err != nil {
		h.logger.Error("mint pair", zap.Error(err))
		writeAuthErr(w, http.StatusInternalServerError, "mint_failed", "internal error")
		return
	}
	familyID, err := uuid.Parse(pair.FamilyID)
	if err != nil {
		h.logger.Error("mint pair returned non-UUID family_id", zap.String("family_id", pair.FamilyID))
		writeAuthErr(w, http.StatusInternalServerError, "mint_failed", "internal error")
		return
	}

	// Record the new family — the subject id used here is the
	// Person id (matches sub). Refresh-rotation reads this row
	// under SELECT FOR UPDATE.
	if err := h.families.Create(r.Context(), familyID, pc.ID, pair.RefreshJTI); err != nil {
		h.logger.Error("create refresh family", zap.Error(err))
		writeAuthErr(w, http.StatusInternalServerError, "family_create_failed", "internal error")
		return
	}

	if err := h.persons.MarkLoginSuccess(r.Context(), pc.ID); err != nil {
		// Logged but non-fatal — tokens are issued; failing to reset
		// the counter is recoverable.
		h.logger.Warn("mark login success", zap.Error(err))
	}

	resp := loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(time.Until(pair.AccessExp).Seconds()),
		PersonID:     pc.ID.String(),
		OrgID:        pc.OrgID.String(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeAuthErr writes the canonical {"code":"...","message":"..."}
// envelope. Matches the existing /v1/identity/* and /auth/refresh
// shape (see internal/identity/auth/refresh.go: writeError).
func writeAuthErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}
