// Package party owns the party-substrate identity domain — the
// upstream-of-customer identity node that resolves cashier / customer
// / loyalty references into a single canonical party.parties row.
//
// B.5 landed the schema (party.parties, party.identifiers,
// party.resolution_events, party.households, party.household_memberships,
// party.household_evidence, party.decisioning_facts MV).
// (this package) ships the resolution helpers Fox + downstream
// modules call: ResolveFromDetection + ResolveSubject.
//
// Spec: GRO-764 Phase C.2 (folds Fox SDD-bug from
// docs/sdds/go-handoff/canonical-data-model-party-edits.md §D).
//
// Posture: LAZY-mode resolution by default. Detection volume is
// 100×–1000× case volume; we resolve party at case-escalation time
// (when Fox calls), not at every detection write. Per-tenant override
// available via app.tenants.attributes->>'subjects_resolve_mode' =
// 'eager'.
package party

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ruptiv/canary/internal/db/types"
)

// IdentifierType values for party.identifiers.identifier_type.
const (
	IdentifierTypeEmployeeID = "employee_id"
	IdentifierTypeCustomerID = "customer_id"
	IdentifierTypeLoyaltyID  = "loyalty_id"
	IdentifierTypePhone      = "phone"
	IdentifierTypeEmail      = "email"
	IdentifierTypeCard       = "card_token"
)

// Confidence values for party.parties.confidence.
const (
	ConfidenceAnonymous = "anonymous"
	ConfidenceWeak      = "weak"
	ConfidenceProbable  = "probable"
	ConfidenceStrong    = "strong"
)

// Errors surfaced to callers. Handlers map to HTTP status codes via
// the conventions doc pattern.
var (
	ErrNoIdentifier = errors.New("party: detection has no resolvable identifier")
)

// Store wraps pgxpool with the party-resolution methods Fox depends on.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ResolveFromDetection finds-or-creates a party row for the given
// detection. Precedence: cashier_employee_id (employee party) wins
// over customer_id (consumer party) — LP cases skew employee-driven
// per the Fox handler's existing precedence note.
//
// Returns nil when neither field is populated (detection has no
// resolvable signal subject) — Fox handles this as "open case with
// primary_subject_id NULL", same as behavior.
//
// LAZY-mode: each call performs UPSERT on
// party.identifiers(tenant_id, identifier_type, identifier_value_hash)
// and either returns the existing party_id or creates a new
// party.parties row with confidence='weak' for employee identifiers
// and 'anonymous' for unbound customer identifiers.
func (s *Store) ResolveFromDetection(ctx context.Context, det *types.Detection) (*uuid.UUID, error) {
	switch {
	case det.CashierEmployeeID != nil:
		id, err := s.resolveOrCreateParty(ctx, det.TenantID,
			IdentifierTypeEmployeeID,
			det.CashierEmployeeID.String(),
			"employee",
			ConfidenceWeak,
			"employee:"+det.CashierEmployeeID.String(),
		)
		if err != nil {
			return nil, err
		}
		return &id, nil

	case det.CustomerID != nil:
		id, err := s.resolveOrCreateParty(ctx, det.TenantID,
			IdentifierTypeCustomerID,
			det.CustomerID.String(),
			"consumer",
			ConfidenceAnonymous,
			"customer:"+det.CustomerID.String(),
		)
		if err != nil {
			return nil, err
		}
		return &id, nil
	}
	return nil, nil
}

// ResolveSubject upserts a detection.subjects row keyed on (tenant_id,
// party_id) and returns the canonical subject id. Idempotent —
// re-resolving the same party returns the same detection.subjects.id.
//
// Subject row carries party_id (Wave A canonical-data-model-party-
// edits §D); the legacy related_employee_id / related_customer_id /
// related_vendor_id columns stay during Phases 1-5 for read-path
// compatibility per the SDD's deprecation note.
func (s *Store) ResolveSubject(ctx context.Context, tenantID, partyID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, fmt.Errorf("party: subject begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Look for existing subject by party_id
	var existing uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM detection.subjects
		 WHERE tenant_id = $1 AND party_id = $2
		 LIMIT 1`, tenantID, partyID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("party: subject lookup: %w", err)
	}

	// Insert new subject. subject_code = "party:" + party_id (deterministic
	// per tenant); subject_type = "external_party" matches the schema
	// CHECK enum. display_name is human-readable; downstream UX overrides.
	const insertQ = `
		INSERT INTO detection.subjects (tenant_id, party_id, subject_code,
		    subject_type, display_name, status)
		VALUES ($1, $2, $3, 'external_party', $4, 'active')
		ON CONFLICT (tenant_id, subject_code) DO UPDATE
		    SET updated_at = now()
		RETURNING id`
	subjectCode := "party:" + partyID.String()
	displayName := "party " + partyID.String()[:8]
	var id uuid.UUID
	if err := tx.QueryRow(ctx, insertQ, tenantID, partyID, subjectCode, displayName).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("party: subject insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("party: subject commit: %w", err)
	}
	return id, nil
}

// resolveOrCreateParty does the heavy lifting: hash the identifier,
// look it up in party.identifiers, return its party_id; if absent,
// create a new party row + identifier row in the same tx.
func (s *Store) resolveOrCreateParty(
	ctx context.Context,
	tenantID uuid.UUID,
	identifierType, identifierValue, partyType, confidence, partyCodeSeed string,
) (uuid.UUID, error) {
	hash := hashIdentifier(tenantID, identifierType, identifierValue)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, fmt.Errorf("party: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Existing identifier?
	var partyID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT party_id FROM party.identifiers
		 WHERE tenant_id = $1 AND identifier_type = $2 AND identifier_value_hash = $3`,
		tenantID, identifierType, hash).Scan(&partyID)
	if err == nil {
		// Bump occurrence counter + last_seen_at, fire-and-forget
		_, _ = tx.Exec(ctx, `
			UPDATE party.identifiers
			   SET occurrence_count = occurrence_count + 1,
			       last_seen_at = now(),
			       updated_at = now()
			 WHERE tenant_id = $1 AND identifier_type = $2 AND identifier_value_hash = $3`,
			tenantID, identifierType, hash)
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("party: commit existing: %w", err)
		}
		return partyID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("party: identifier lookup: %w", err)
	}

	// Create new party.parties row
	const insertPartyQ = `
		INSERT INTO party.parties (tenant_id, party_code, party_type,
		    display_name, status, confidence)
		VALUES ($1, $2, $3, $4, 'active', $5)
		RETURNING id`
	if err := tx.QueryRow(ctx, insertPartyQ,
		tenantID, partyCodeSeed, partyType,
		fmt.Sprintf("%s (%s)", partyType, identifierValue[:min(len(identifierValue), 8)]),
		confidence,
	).Scan(&partyID); err != nil {
		return uuid.Nil, fmt.Errorf("party: insert party: %w", err)
	}

	// Insert identifier row
	const insertIdentifierQ = `
		INSERT INTO party.identifiers (tenant_id, party_id,
		    identifier_type, identifier_value_hash, source_system, quality_score)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := tx.Exec(ctx, insertIdentifierQ,
		tenantID, partyID, identifierType, hash, "canary",
		qualityScoreFor(identifierType),
	); err != nil {
		return uuid.Nil, fmt.Errorf("party: insert identifier: %w", err)
	}

	// Append resolution event
	if _, err := tx.Exec(ctx, `
		INSERT INTO party.resolution_events
		    (tenant_id, party_id, event_type, confidence_after, actor)
		VALUES ($1, $2, 'created', $3, 'system')`,
		tenantID, partyID, confidence,
	); err != nil {
		return uuid.Nil, fmt.Errorf("party: insert resolution event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("party: commit new: %w", err)
	}
	return partyID, nil
}

// hashIdentifier computes a deterministic hash for storage as
// party.identifiers.identifier_value_hash. Tenant + type are
// folded in so the same identifier value across tenants doesn't
// collide.
func hashIdentifier(tenantID uuid.UUID, identifierType, identifierValue string) string {
	h := sha256.Sum256([]byte(tenantID.String() + ":" + identifierType + ":" + identifierValue))
	return hex.EncodeToString(h[:])
}

// qualityScoreFor returns the default quality score for an identifier
// type. Strong signals (employee_id, loyalty_id) score higher than
// weak signals (anonymous customer_id).
func qualityScoreFor(identifierType string) string {
	switch identifierType {
	case IdentifierTypeEmployeeID, IdentifierTypeLoyaltyID:
		return "0.95"
	case IdentifierTypeCustomerID:
		return "0.50"
	default:
		return "0.30"
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
