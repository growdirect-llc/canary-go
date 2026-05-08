package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPersonNotFound is returned when LookupPersonByEmail finds no
// active row matching the email. Login handlers MUST NOT distinguish
// this from ErrInvalidPassword in their HTTP responses.
var ErrPersonNotFound = errors.New("auth: person not found")

// ErrPersonLocked is returned when the credential row's locked_until
// is in the future. The handler responds 401 with no further detail.
var ErrPersonLocked = errors.New("auth: person locked out")

// Person is the identity-service-internal projection of public.persons.
// External callers see the WhoAmI shape (T-3) which derives from this
// struct.
type Person struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Email       string
	FirstName   string
	LastName    string
	DisplayName string
	Phone       string
	UserType    string
	IsSystem    bool
	IsActive    bool
}

// PersonWithCredential bundles a person with their credential row for
// the login path's single-query read.
type PersonWithCredential struct {
	Person
	PasswordHash string
	MFAEnabled   bool
	LockedUntil  *time.Time
}

// PersonStore wraps the identity DB pool. Callers hold one per
// service binary; the pool itself is goroutine-safe.
type PersonStore struct {
	pool *pgxpool.Pool
}

// NewPersonStore constructs a store. pool MUST point at
// canary_identity_gcp — see the platform-identity-database-boundary
// card for why.
func NewPersonStore(pool *pgxpool.Pool) *PersonStore {
	return &PersonStore{pool: pool}
}

// LookupForLogin reads a Person + credential row by email. Returns
// ErrPersonNotFound if no active row matches. Returns ErrPersonLocked
// if the credential row is currently locked out.
//
// The query joins persons + person_credentials in one round-trip; if
// either row is missing the join returns no rows and the caller sees
// ErrPersonNotFound.
func (s *PersonStore) LookupForLogin(ctx context.Context, email string) (*PersonWithCredential, error) {
	const q = `
		SELECT p.id, p.org_id, p.email, COALESCE(p.first_name, ''),
		       COALESCE(p.last_name, ''), COALESCE(p.display_name, ''),
		       COALESCE(p.phone, ''), p.user_type, p.is_system, p.is_active,
		       c.password_hash, c.mfa_enabled, c.locked_until
		FROM public.persons p
		JOIN public.person_credentials c ON c.person_id = p.id
		WHERE p.email = $1 AND p.is_active = true
	`
	row := s.pool.QueryRow(ctx, q, email)
	var pc PersonWithCredential
	if err := row.Scan(
		&pc.ID, &pc.OrgID, &pc.Email, &pc.FirstName, &pc.LastName,
		&pc.DisplayName, &pc.Phone, &pc.UserType, &pc.IsSystem, &pc.IsActive,
		&pc.PasswordHash, &pc.MFAEnabled, &pc.LockedUntil,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPersonNotFound
		}
		return nil, fmt.Errorf("auth: lookup person: %w", err)
	}
	if pc.LockedUntil != nil && pc.LockedUntil.After(time.Now()) {
		return nil, ErrPersonLocked
	}
	return &pc, nil
}

// LookupByID returns an active Person by id. Used by the refresh
// path to assert the Person is still active before issuing a new
// access token. Returns ErrPersonNotFound if no active row matches.
func (s *PersonStore) LookupByID(ctx context.Context, id uuid.UUID) (*Person, error) {
	const q = `
		SELECT id, org_id, email, COALESCE(first_name, ''),
		       COALESCE(last_name, ''), COALESCE(display_name, ''),
		       COALESCE(phone, ''), user_type, is_system, is_active
		FROM public.persons
		WHERE id = $1 AND is_active = true
	`
	var p Person
	if err := s.pool.QueryRow(ctx, q, id).Scan(
		&p.ID, &p.OrgID, &p.Email, &p.FirstName, &p.LastName,
		&p.DisplayName, &p.Phone, &p.UserType, &p.IsSystem, &p.IsActive,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPersonNotFound
		}
		return nil, fmt.Errorf("auth: lookup person by id: %w", err)
	}
	return &p, nil
}

// MarkLoginSuccess clears failed-login state and bumps last_login_at.
// Best-effort — a failure here does not prevent token issuance, but
// the surrounding caller should log it.
func (s *PersonStore) MarkLoginSuccess(ctx context.Context, personID uuid.UUID) error {
	const q1 = `UPDATE public.persons SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1`
	const q2 = `UPDATE public.person_credentials SET failed_login_count = 0, locked_until = NULL, updated_at = NOW() WHERE person_id = $1`
	if _, err := s.pool.Exec(ctx, q1, personID); err != nil {
		return fmt.Errorf("auth: mark login success (person): %w", err)
	}
	if _, err := s.pool.Exec(ctx, q2, personID); err != nil {
		return fmt.Errorf("auth: mark login success (credential): %w", err)
	}
	return nil
}

// MarkLoginFailure increments failed_login_count. The lockout policy
// (when to set locked_until) lives in the login handler so it can
// reason about the threshold without a round-trip per attempt.
func (s *PersonStore) MarkLoginFailure(ctx context.Context, personID uuid.UUID, lockUntil *time.Time) error {
	const q = `
		UPDATE public.person_credentials
		SET failed_login_count = failed_login_count + 1,
		    locked_until = COALESCE($2, locked_until),
		    updated_at = NOW()
		WHERE person_id = $1
	`
	if _, err := s.pool.Exec(ctx, q, personID, lockUntil); err != nil {
		return fmt.Errorf("auth: mark login failure: %w", err)
	}
	return nil
}

// CreatePersonWithPassword inserts a Person + credential row in one
// transaction. Used by tests today; a /auth/register surface lands
// later. password is the cleartext; this function calls HashPassword
// internally so callers never hold a hash.
func (s *PersonStore) CreatePersonWithPassword(
	ctx context.Context,
	orgID uuid.UUID,
	email, password, firstName, lastName, displayName, userType string,
) (uuid.UUID, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return uuid.Nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	personID := uuid.New()
	const q1 = `
		INSERT INTO public.persons (id, org_id, email, first_name, last_name, display_name, user_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	if _, err := tx.Exec(ctx, q1, personID, orgID, email, firstName, lastName, displayName, userType); err != nil {
		return uuid.Nil, fmt.Errorf("auth: insert person: %w", err)
	}
	const q2 = `INSERT INTO public.person_credentials (person_id, password_hash) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, q2, personID, hash); err != nil {
		return uuid.Nil, fmt.Errorf("auth: insert credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("auth: commit: %w", err)
	}
	return personID, nil
}
