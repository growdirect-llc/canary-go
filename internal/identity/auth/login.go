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
	"net"
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

// PersonAuthStore is the PersonStore surface this handler depends on.
// *PersonStore satisfies it. Held as an interface so tests stub the
// store without a real DB.
type PersonAuthStore interface {
	LookupForLogin(ctx context.Context, email string) (*PersonWithCredential, error)
	MarkLoginFailure(ctx context.Context, personID uuid.UUID, lockUntil *time.Time) error
	MarkLoginSuccess(ctx context.Context, personID uuid.UUID) error
}

// LoginLockoutStatus is the small surface the handler reads from a
// LoginLimiter.Check. Mirrors identity.LockoutStatus to avoid an
// import cycle with internal/identity (which already imports auth's
// concrete types via the cmd/identity wiring path).
type LoginLockoutStatus struct {
	Locked     bool
	RetryAfter time.Duration
}

// LoginFailureRecord reports which limiter buckets transitioned into
// a new lockout on a RecordFailure call. Handler emits a security log
// for each true bucket.
type LoginFailureRecord struct {
	AccountLockedNow bool
	IPLockedNow      bool
}

// LoginLimiter is the rate-limit surface (GRO-954). nil-safe: if the
// handler is wired with a nil limiter the gates degrade to no-ops and
// login behaves as it did before GRO-954.
type LoginLimiter interface {
	Check(ctx context.Context, email, ip string) (LoginLockoutStatus, error)
	RecordFailure(ctx context.Context, email, ip string) (LoginFailureRecord, error)
	Clear(ctx context.Context, email string) error
}

// LoginHandler serves POST /auth/login.
type LoginHandler struct {
	persons  PersonAuthStore
	minter   LoginMinter
	families FamilyCreator
	limiter  LoginLimiter
	logger   *zap.Logger
}

// NewLoginHandler wires the handler. Logger may be nil. limiter may be
// nil — in which case the rate-limit gates are no-ops.
func NewLoginHandler(persons PersonAuthStore, minter LoginMinter, families FamilyCreator, limiter LoginLimiter, logger *zap.Logger) *LoginHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &LoginHandler{persons: persons, minter: minter, families: families, limiter: limiter, logger: logger}
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

	// Per-account + per-source-IP rate limit gate (GRO-954). Checked
	// before lookup so a locked-out (email, ip) pair never spends an
	// argon2id verify or a DB round-trip. Fail-open on Valkey errors:
	// the auth path must not depend on Valkey availability.
	sourceIP := loginSourceIP(r)
	if h.limiter != nil {
		status, err := h.limiter.Check(r.Context(), req.Email, sourceIP)
		if err != nil {
			h.logger.Warn("login rate-limit check failed (fail-open)",
				zap.String("source_ip", sourceIP), zap.Error(err))
		} else if status.Locked {
			writeAuthRateLimitErr(w, status.RetryAfter,
				"login_rate_limited",
				"too many failed login attempts; try again later")
			return
		}
	}

	// Lookup. Email-not-found and password-mismatch land in the same
	// generic 401 to prevent enumeration; both paths run argon2id
	// so the timing is similar.
	pc, lookupErr := h.persons.LookupForLogin(r.Context(), req.Email)
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrPersonLocked) {
			h.recordLoginFailure(r.Context(), req.Email, sourceIP, "person_locked")
			writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		if errors.Is(lookupErr, ErrPersonNotFound) {
			_ = VerifyPassword(req.Password, ensureDummyHash())
			h.recordLoginFailure(r.Context(), req.Email, sourceIP, "person_not_found")
			writeAuthErr(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		h.logger.Error("auth login lookup", zap.Error(lookupErr))
		writeAuthErr(w, http.StatusInternalServerError, "lookup_failed", "internal error")
		return
	}

	if err := VerifyPassword(req.Password, pc.PasswordHash); err != nil {
		// Wrong password — bump the DB failure counter (forensics) and
		// the limiter counter (lockout policy). The limiter is the
		// load-bearing gate; the DB counter remains for audit history.
		if err := h.persons.MarkLoginFailure(r.Context(), pc.ID, nil); err != nil {
			h.logger.Warn("mark login failure", zap.Error(err))
		}
		h.recordLoginFailure(r.Context(), req.Email, sourceIP, "bad_password")
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

	// Clear the per-account limiter bucket so a couple of mistaken
	// attempts followed by the right password don't accumulate toward
	// a future lockout. Per-IP bucket is intentionally NOT cleared —
	// one user authenticating from a NAT shouldn't reset failure
	// tracking that may reflect a separate attacker on the same IP.
	if h.limiter != nil {
		if err := h.limiter.Clear(r.Context(), req.Email); err != nil {
			h.logger.Warn("login limiter clear", zap.Error(err))
		}
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

// writeAuthRateLimitErr emits 429 with a Retry-After header. Mirrors
// the API-key limiter error envelope (RFC 7231 §7.1.3).
func writeAuthRateLimitErr(w http.ResponseWriter, retryAfter time.Duration, code, msg string) {
	if retryAfter > 0 {
		secs := int(retryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", itoa(secs))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + msg + `"}`))
}

// itoa is the strconv-free integer-to-decimal helper used by the
// Retry-After header writer. Avoids the import for one tiny use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// recordLoginFailure bumps the limiter counter for (email, ip) and
// logs a security-event line on each new lockout. Errors are logged
// fail-open — the limiter must not block a login on a Valkey blip.
func (h *LoginHandler) recordLoginFailure(ctx context.Context, email, ip, reason string) {
	if h.limiter == nil {
		return
	}
	res, err := h.limiter.RecordFailure(ctx, email, ip)
	if err != nil {
		h.logger.Warn("login limiter record failure (fail-open)",
			zap.String("source_ip", ip), zap.String("reason", reason), zap.Error(err))
		return
	}
	// Audit/security events on lockout transitions only — one log per
	// lockout, not per failed attempt.
	if res.AccountLockedNow {
		h.logger.Warn("login: account locked out",
			zap.String("email", email),
			zap.String("source_ip", ip),
			zap.String("reason", reason))
	}
	if res.IPLockedNow {
		h.logger.Warn("login: source ip locked out",
			zap.String("source_ip", ip),
			zap.String("reason", reason))
	}
}

// loginSourceIP extracts the request's source IP for rate-limit
// bucketing. Trusts middleware.RealIP's rewrite of r.RemoteAddr.
// Strips the port. Returns "" if RemoteAddr cannot be parsed so the
// limiter no-ops for that request rather than bucketing every
// malformed source under the empty-string key.
func loginSourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if ip := net.ParseIP(r.RemoteAddr); ip != nil {
			return r.RemoteAddr
		}
		return ""
	}
	return host
}
