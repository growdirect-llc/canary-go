// internal/fox/subjects.go
//
// detection.subjects resolution — looks up or creates a subject row keyed on
// (tenant_id, related_<kind>_id). Closes the Loop 2 Fox SDD-bug:
// subjectFromDetection returned cashier_employee_id directly into
// detection.cases.primary_subject_id, violating the FK on detection.subjects(id) on
// every auto-escalated case.
//
// LAZY mode is the default — resolve happens at Fox case-escalation
// time inside handler.subjectFromDetection, not on chirp detection
// write. EAGER mode (chirp-time resolve) is reserved for tenants
// that need real-time subject clustering signals on the detection
// stream itself; configurable via SUBJECTS_RESOLVE_MODE env var or
// per-tenant app.tenants.attributes->>'subjects_resolve_mode'.
package fox

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SubjectKind enumerates the three detection.subjects.related_*_id columns
// the resolver knows how to upsert against.
type SubjectKind string

const (
	SubjectEmployee SubjectKind = "employee"
	SubjectCustomer SubjectKind = "customer"
	SubjectVendor   SubjectKind = "vendor"
)

// SubjectsResolveMode controls when the resolver runs relative to
// the detection lifecycle.
type SubjectsResolveMode string

const (
	// ResolveModeLazy resolves at case-escalation time (handler.fromDetection
	// → OpenCase). This is the default — detection volume is 100×–1000×
	// case volume; eager resolve would burden the hot path with FK lookups
	// for signals that 99% never escalate.
	ResolveModeLazy SubjectsResolveMode = "lazy"
	// ResolveModeEager resolves on detection write (chirp detection
	// insert path). Reserved for tenants that need real-time subject
	// clustering signals on the detection stream itself.
	ResolveModeEager SubjectsResolveMode = "eager"
)

// envVarSubjectsResolveMode is the env var that controls the global
// default. Per-tenant override via app.tenants.attributes JSONB.
const envVarSubjectsResolveMode = "SUBJECTS_RESOLVE_MODE"

// DefaultSubjectsResolveMode reads the env var and returns the
// platform-wide default mode. Lazy when unset or invalid — the safe
// option for the hot path.
func DefaultSubjectsResolveMode() SubjectsResolveMode {
	switch SubjectsResolveMode(os.Getenv(envVarSubjectsResolveMode)) {
	case ResolveModeEager:
		return ResolveModeEager
	default:
		return ResolveModeLazy
	}
}

// SubjectResolver is the persistence-layer interface fox needs for
// the resolve operation. Store satisfies it.
type SubjectResolver interface {
	ResolveSubject(ctx context.Context, tenantID uuid.UUID, kind SubjectKind, refID uuid.UUID) (uuid.UUID, error)
}

// Compile-time guard: Store satisfies SubjectResolver.
var _ SubjectResolver = (*Store)(nil)

// ErrInvalidSubjectKind is returned when an unrecognized SubjectKind
// is passed to Resolve.
var ErrInvalidSubjectKind = errors.New("fox: invalid subject kind")

// ResolveSubject upserts a detection.subjects row keyed on
// (tenant_id, subject_code) where subject_code is a deterministic
// derivation of (kind, refID): "emp:<uuid>", "cust:<uuid>",
// "vend:<uuid>". Idempotent — the existing detection.subjects unique index
// uq (tenant_id, subject_code) provides the upsert key.
//
// Returns the subject's id (existing or newly-minted) and nil. On
// error returns uuid.Nil + the wrapped error.
func (s *Store) ResolveSubject(ctx context.Context, tenantID uuid.UUID, kind SubjectKind, refID uuid.UUID) (uuid.UUID, error) {
	subjectCode, subjectType, columnName, displayName, err := subjectAttrs(kind, refID)
	if err != nil {
		return uuid.Nil, err
	}

	// INSERT ... ON CONFLICT DO UPDATE ... RETURNING id pattern. The
	// DO UPDATE updates updated_at so we get a RETURNING id even on
	// conflict (DO NOTHING would skip RETURNING for the conflicting
	// row, requiring a follow-up SELECT).
	q := fmt.Sprintf(`
		INSERT INTO detection.subjects (tenant_id, subject_code, subject_type, %s, display_name)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, subject_code)
		DO UPDATE SET updated_at = now()
		RETURNING id
	`, columnName)

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, q, tenantID, subjectCode, subjectType, refID, displayName).Scan(&id)
	if err != nil {
		// Not-found is impossible with this UPSERT shape; any error is
		// a real DB or constraint failure (e.g., refID doesn't FK to
		// the related table — detection.subjects related_*_id columns are
		// formal FKs in the schema).
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("fox: ResolveSubject returned no row for tenant=%s kind=%s ref=%s", tenantID, kind, refID)
		}
		return uuid.Nil, fmt.Errorf("fox: ResolveSubject: %w", err)
	}
	return id, nil
}

// subjectAttrs maps a SubjectKind into the schema column + subject_type
// label + subject_code prefix + display name template.
func subjectAttrs(kind SubjectKind, refID uuid.UUID) (subjectCode, subjectType, columnName, displayName string, err error) {
	switch kind {
	case SubjectEmployee:
		return "emp:" + refID.String(), "known_employee", "related_employee_id", "Employee " + refID.String()[:8], nil
	case SubjectCustomer:
		return "cust:" + refID.String(), "known_customer", "related_customer_id", "Customer " + refID.String()[:8], nil
	case SubjectVendor:
		return "vend:" + refID.String(), "known_vendor", "related_vendor_id", "Vendor " + refID.String()[:8], nil
	default:
		return "", "", "", "", fmt.Errorf("%w: %s", ErrInvalidSubjectKind, kind)
	}
}
