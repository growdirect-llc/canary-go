// internal/lp/substrate.go
//
// Read-only access to detection.lp_substrate and detection.allow_list.
//
// lp_substrate: event-ingestion table — one row per LP substrate payload
// received from the detection pipeline. Columns: id, tenant_id, entity_type,
// entity_id, location_id, payload (jsonb), received_at, processed_at, created_at.
//
// allow_list: tenant-scoped allow-list entries linked to detection_rules.
// Columns: id, tenant_id, rule_id, pattern (jsonb), reason, expires_at,
// created_at, created_by, updated_at, updated_by.

package lp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Substrate ─────────────────────────────────────────────────────────────────

// SubstrateStore reads LP substrate events from detection.lp_substrate.
type SubstrateStore struct {
	pool *pgxpool.Pool
}

// NewSubstrateStore returns a SubstrateStore backed by the given pool.
func NewSubstrateStore(pool *pgxpool.Pool) *SubstrateStore {
	return &SubstrateStore{pool: pool}
}

// SubstrateRow mirrors a row in detection.lp_substrate.
type SubstrateRow struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	EntityType  string
	EntityID    uuid.UUID
	LocationID  *uuid.UUID
	Payload     json.RawMessage
	ReceivedAt  time.Time
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

// ListByLocation returns recent substrate events for a tenant and location
// (up to limit rows, ordered by received_at DESC).
// If locationID is uuid.Nil all events for the tenant are returned.
func (s *SubstrateStore) ListByLocation(
	ctx context.Context,
	tenantID uuid.UUID,
	locationID uuid.UUID,
	limit int,
) ([]SubstrateRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	var (
		rows interface{ Next() bool; Close() }
		err  error
	)

	if locationID == uuid.Nil {
		pgRows, qErr := s.pool.Query(ctx, `
			SELECT id, tenant_id, entity_type, entity_id, location_id,
			       payload, received_at, processed_at, created_at
			FROM detection.lp_substrate
			WHERE tenant_id = $1
			ORDER BY received_at DESC
			LIMIT $2`,
			tenantID, limit)
		rows, err = pgRows, qErr
	} else {
		pgRows, qErr := s.pool.Query(ctx, `
			SELECT id, tenant_id, entity_type, entity_id, location_id,
			       payload, received_at, processed_at, created_at
			FROM detection.lp_substrate
			WHERE tenant_id = $1 AND location_id = $2
			ORDER BY received_at DESC
			LIMIT $3`,
			tenantID, locationID, limit)
		rows, err = pgRows, qErr
	}
	if err != nil {
		return nil, fmt.Errorf("lp: substrate list: %w", err)
	}

	// Type-assert to the concrete pgx rows type to get Scan/Close.
	type scanner interface {
		Next() bool
		Scan(dest ...any) error
		Close()
		Err() error
	}
	pgxRows, ok := rows.(scanner)
	if !ok {
		return nil, fmt.Errorf("lp: substrate list: unexpected rows type")
	}
	defer pgxRows.Close()

	out := make([]SubstrateRow, 0, limit)
	for pgxRows.Next() {
		var r SubstrateRow
		if err := pgxRows.Scan(
			&r.ID, &r.TenantID, &r.EntityType, &r.EntityID, &r.LocationID,
			&r.Payload, &r.ReceivedAt, &r.ProcessedAt, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("lp: substrate scan: %w", err)
		}
		out = append(out, r)
	}
	return out, pgxRows.Err()
}

// ── AllowList ─────────────────────────────────────────────────────────────────

// AllowListStore reads LP allow-list entries from detection.allow_list.
type AllowListStore struct {
	pool *pgxpool.Pool
}

// NewAllowListStore returns an AllowListStore backed by the given pool.
func NewAllowListStore(pool *pgxpool.Pool) *AllowListStore {
	return &AllowListStore{pool: pool}
}

// AllowListRow mirrors a row in detection.allow_list.
type AllowListRow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	RuleID    *uuid.UUID
	Pattern   json.RawMessage
	Reason    *string
	ExpiresAt *time.Time
	CreatedAt time.Time
	CreatedBy *uuid.UUID
	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}

// List returns all active allow-list entries for the tenant
// (entries where expires_at is NULL or in the future).
func (s *AllowListStore) List(ctx context.Context, tenantID uuid.UUID, limit int) ([]AllowListRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, rule_id, pattern, reason, expires_at,
		       created_at, created_by, updated_at, updated_by
		FROM detection.allow_list
		WHERE tenant_id = $1
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT $2`,
		tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("lp: allow_list list: %w", err)
	}
	defer rows.Close()

	out := make([]AllowListRow, 0, limit)
	for rows.Next() {
		var r AllowListRow
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
			&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("lp: allow_list scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListByRuleID returns allow-list entries scoped to a specific detection rule.
func (s *AllowListStore) ListByRuleID(ctx context.Context, tenantID, ruleID uuid.UUID, limit int) ([]AllowListRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, rule_id, pattern, reason, expires_at,
		       created_at, created_by, updated_at, updated_by
		FROM detection.allow_list
		WHERE tenant_id = $1 AND rule_id = $2
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT $3`,
		tenantID, ruleID, limit)
	if err != nil {
		return nil, fmt.Errorf("lp: allow_list by rule: %w", err)
	}
	defer rows.Close()

	out := make([]AllowListRow, 0, limit)
	for rows.Next() {
		var r AllowListRow
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
			&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("lp: allow_list by rule scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
