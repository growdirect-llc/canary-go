package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/mint"
)

// ─── stubs ───────────────────────────────────────────────────────────

type stubPersonAuthStore struct {
	pc        *PersonWithCredential
	lookupErr error

	failureCalls int
	successCalls int
}

func (s *stubPersonAuthStore) LookupForLogin(_ context.Context, _ string) (*PersonWithCredential, error) {
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return s.pc, nil
}

func (s *stubPersonAuthStore) MarkLoginFailure(_ context.Context, _ uuid.UUID, _ *time.Time) error {
	s.failureCalls++
	return nil
}

func (s *stubPersonAuthStore) MarkLoginSuccess(_ context.Context, _ uuid.UUID) error {
	s.successCalls++
	return nil
}

type stubLoginMinter struct {
	pair *mint.Pair
	err  error
}

func (s *stubLoginMinter) MintPair(_ context.Context, _ mint.Subject, _ uuid.UUID) (*mint.Pair, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pair, nil
}

type stubFamilyCreator struct{ err error }

func (s *stubFamilyCreator) Create(_ context.Context, _, _ uuid.UUID, _ string) error {
	return s.err
}

// stubLoginLimiter satisfies LoginLimiter. Configurable per-call so
// tests can drive lockout, failure, and success transitions.
type stubLoginLimiter struct {
	checkStatus  LoginLockoutStatus
	checkErr     error
	failureRec   LoginFailureRecord
	failureErr   error
	clearErr     error
	failureCalls []failureCall
	checkCalls   []checkCall
	clearCalls   []string
}

type failureCall struct{ email, ip string }
type checkCall struct{ email, ip string }

func (s *stubLoginLimiter) Check(_ context.Context, email, ip string) (LoginLockoutStatus, error) {
	s.checkCalls = append(s.checkCalls, checkCall{email, ip})
	return s.checkStatus, s.checkErr
}

func (s *stubLoginLimiter) RecordFailure(_ context.Context, email, ip string) (LoginFailureRecord, error) {
	s.failureCalls = append(s.failureCalls, failureCall{email, ip})
	return s.failureRec, s.failureErr
}

func (s *stubLoginLimiter) Clear(_ context.Context, email string) error {
	s.clearCalls = append(s.clearCalls, email)
	return s.clearErr
}

// ─── helpers ────────────────────────────────────────────────────────

// validPersonWithCredential returns a fixed-hash credential row that
// VerifyPassword succeeds against for the password "secret".
func validPersonWithCredential(t *testing.T) *PersonWithCredential {
	t.Helper()
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return &PersonWithCredential{
		Person: Person{
			ID:       uuid.New(),
			OrgID:    uuid.New(),
			Email:    "person@example.test",
			UserType: "regular",
			IsActive: true,
		},
		PasswordHash: hash,
	}
}

func validLoginPair() *mint.Pair {
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

func mountAndPostLogin(
	t *testing.T,
	persons PersonAuthStore,
	minter LoginMinter,
	families FamilyCreator,
	limiter LoginLimiter,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	NewLoginHandler(persons, minter, families, limiter, nil).Mount(router)

	var bodyBytes []byte
	switch b := body.(type) {
	case string:
		bodyBytes = []byte(b)
	default:
		bodyBytes, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:54321" // RFC 5737 example IPv4
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// ─── tests ──────────────────────────────────────────────────────────

// TestLogin_LockedOut_Returns429_WithoutTouchingPasswordVerify is the
// GRO-954 acceptance probe. When the limiter reports Locked=true:
//
//  1. Handler MUST return 429 (not 401) so clients distinguish "locked"
//     from "wrong password" — locked users back off; mistaken passwords
//     keep prompting.
//  2. Retry-After header MUST be set to the lockout's remaining TTL
//     in whole seconds, per RFC 7231 §7.1.3.
//  3. PersonStore lookup MUST NOT happen — the gate runs before the DB
//     round-trip and argon2id verify.
//  4. limiter.RecordFailure MUST NOT happen — locked-out attempts
//     don't add to the failure counter (otherwise an attacker could
//     extend the lockout indefinitely).
//
// Fails pre-fix because pre-GRO-954 the handler had no limiter wired
// at all — every request hit the DB and ran argon2id regardless of
// past failures.
func TestLogin_LockedOut_Returns429_WithoutTouchingPasswordVerify(t *testing.T) {
	persons := &stubPersonAuthStore{pc: validPersonWithCredential(t)}
	minter := &stubLoginMinter{pair: validLoginPair()}
	families := &stubFamilyCreator{}
	limiter := &stubLoginLimiter{
		checkStatus: LoginLockoutStatus{Locked: true, RetryAfter: 13 * time.Minute},
	}

	rec := mountAndPostLogin(t, persons, minter, families, limiter,
		map[string]string{"email": "person@example.test", "password": "secret"})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "login_rate_limited") {
		t.Errorf("body: got %q, expected login_rate_limited", rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "780" {
		t.Errorf("Retry-After: got %q, want 780 (13 min)", got)
	}

	// PersonStore lookup MUST NOT have run.
	if persons.failureCalls != 0 || persons.successCalls != 0 {
		t.Errorf("person store touched on locked-out request: f=%d s=%d", persons.failureCalls, persons.successCalls)
	}

	// limiter.RecordFailure MUST NOT have run.
	if len(limiter.failureCalls) != 0 {
		t.Errorf("limiter.RecordFailure ran on locked-out request: %+v", limiter.failureCalls)
	}

	// Check ran once with the request's email + extracted source IP.
	if len(limiter.checkCalls) != 1 || limiter.checkCalls[0].email != "person@example.test" || limiter.checkCalls[0].ip != "203.0.113.7" {
		t.Errorf("limiter.Check calls: %+v", limiter.checkCalls)
	}
}

// TestLogin_BadPassword_RecordsLimiterFailure verifies a wrong-password
// attempt bumps both the DB counter (forensics) and the limiter
// counter (lockout policy). The limiter counter is the load-bearing
// gate.
func TestLogin_BadPassword_RecordsLimiterFailure(t *testing.T) {
	persons := &stubPersonAuthStore{pc: validPersonWithCredential(t)}
	minter := &stubLoginMinter{}
	families := &stubFamilyCreator{}
	limiter := &stubLoginLimiter{} // Check returns Locked=false by default

	rec := mountAndPostLogin(t, persons, minter, families, limiter,
		map[string]string{"email": "person@example.test", "password": "wrong"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if persons.failureCalls != 1 {
		t.Errorf("person.MarkLoginFailure calls: got %d, want 1", persons.failureCalls)
	}
	if len(limiter.failureCalls) != 1 || limiter.failureCalls[0].email != "person@example.test" || limiter.failureCalls[0].ip != "203.0.113.7" {
		t.Errorf("limiter.RecordFailure calls: %+v", limiter.failureCalls)
	}
}

// TestLogin_PersonNotFound_RecordsLimiterFailure verifies the
// enumeration-defense path (email-not-found → dummy hash + 401)
// ALSO records a limiter failure. Without this, an attacker could
// brute-force unknown emails freely.
func TestLogin_PersonNotFound_RecordsLimiterFailure(t *testing.T) {
	persons := &stubPersonAuthStore{lookupErr: ErrPersonNotFound}
	minter := &stubLoginMinter{}
	families := &stubFamilyCreator{}
	limiter := &stubLoginLimiter{}

	rec := mountAndPostLogin(t, persons, minter, families, limiter,
		map[string]string{"email": "ghost@example.test", "password": "anything"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	if len(limiter.failureCalls) != 1 || limiter.failureCalls[0].email != "ghost@example.test" {
		t.Errorf("limiter.RecordFailure calls: %+v", limiter.failureCalls)
	}
}

// TestLogin_SuccessClearsAccountBucket verifies a successful login
// clears the per-account bucket so a couple of mistaken attempts
// followed by the right password don't accumulate toward a lockout.
func TestLogin_SuccessClearsAccountBucket(t *testing.T) {
	persons := &stubPersonAuthStore{pc: validPersonWithCredential(t)}
	minter := &stubLoginMinter{pair: validLoginPair()}
	families := &stubFamilyCreator{}
	limiter := &stubLoginLimiter{}

	rec := mountAndPostLogin(t, persons, minter, families, limiter,
		map[string]string{"email": "person@example.test", "password": "secret"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(limiter.clearCalls) != 1 || limiter.clearCalls[0] != "person@example.test" {
		t.Errorf("limiter.Clear calls: %+v", limiter.clearCalls)
	}
	if persons.successCalls != 1 {
		t.Errorf("person.MarkLoginSuccess calls: got %d, want 1", persons.successCalls)
	}
}

// TestLogin_LimiterCheckFailsOpen verifies a Valkey error on the
// initial check does NOT block login — auth must not depend on Valkey
// availability. The handler still proceeds to lookup + verify, and
// a successful password yields 200.
func TestLogin_LimiterCheckFailsOpen(t *testing.T) {
	persons := &stubPersonAuthStore{pc: validPersonWithCredential(t)}
	minter := &stubLoginMinter{pair: validLoginPair()}
	families := &stubFamilyCreator{}
	limiter := &stubLoginLimiter{checkErr: errContext("valkey down")}

	rec := mountAndPostLogin(t, persons, minter, families, limiter,
		map[string]string{"email": "person@example.test", "password": "secret"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (fail-open expected)", rec.Code)
	}
}

// TestLogin_NilLimiter_PassesThrough verifies a nil limiter degrades
// to a no-op so existing wirings without the limiter (or callers that
// disable it for a specific deployment) keep working.
func TestLogin_NilLimiter_PassesThrough(t *testing.T) {
	persons := &stubPersonAuthStore{pc: validPersonWithCredential(t)}
	minter := &stubLoginMinter{pair: validLoginPair()}
	families := &stubFamilyCreator{}

	rec := mountAndPostLogin(t, persons, minter, families, nil,
		map[string]string{"email": "person@example.test", "password": "secret"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 with nil limiter", rec.Code)
	}
}

// errContext is a tiny shim so we don't import errors just for one stub.
type errContext string

func (e errContext) Error() string { return string(e) }
