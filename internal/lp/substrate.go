// internal/lp/substrate.go
//
// Read-only access to detection.lp_substrate; full CRUD on detection.allow_list.
//
// lp_substrate: event-ingestion table — one row per LP substrate payload
// received from the detection pipeline. Columns: id, tenant_id, entity_type,
// entity_id, location_id, payload (jsonb), received_at, processed_at, created_at.
//
// allow_list: tenant-scoped admin surface backed by detection.allow_list.
// Columns: id, tenant_id, rule_id, pattern (jsonb), reason, expires_at,
// created_at, created_by, updated_at, updated_by.
//
// The pattern jsonb carries a type+kind discriminator so a single table backs
// 10 settings screens (W1 dispatch). Each entry's pattern.type ∈
// {allowlist, routing, threshold, vocab, setting} and pattern.kind names the
// specific surface (dead_count, discount_cap, drawer, etc.). See PatternType*
// and Kind* constants below.

package lp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAllowListNotFound is returned by Get/Update/Delete when the row doesn't
// exist for the (tenant_id, id) pair.
var ErrAllowListNotFound = errors.New("lp: allow_list entry not found")

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

// ── Pattern type/kind discriminators ──────────────────────────────────────────
// Stored in the pattern jsonb's "type" and "kind" fields. A single
// detection.allow_list table backs 10 settings screens (W1 dispatch).

// Pattern type discriminators.
const (
	PatternTypeAllowlist = "allowlist" // suppression patterns (cashier IDs, reason codes)
	PatternTypeRouting   = "routing"   // alert routing rules
	PatternTypeThreshold = "threshold" // numeric thresholds (drawer variance, discount cap)
	PatternTypeVocab     = "vocab"     // valid value vocabularies (void/comp reason codes)
	PatternTypeSetting   = "setting"   // tenant-wide settings (training mode)
)

// Pattern kind discriminators (sub-type within type).
const (
	KindDeadCount    = "dead_count"
	KindDiscounts    = "discounts"
	KindVoids        = "voids"
	KindComps        = "comps"
	KindAlertRouting = "alert_routing"
	KindDrawer       = "drawer"
	KindDiscountCap  = "discount_cap"
	KindVoidReason   = "void_reason"
	KindCompReason   = "comp_reason"
	KindTrainingMode = "training_mode"
)

// ListByPattern returns allow-list entries filtered by pattern type and kind.
// Used by the 10 W1 settings handlers to read only the rows that belong to
// their screen (e.g. dead-count screen reads only entries with
// pattern.type='allowlist' and pattern.kind='dead_count').
//
// Returns active entries only (expires_at NULL or in the future), ordered by
// created_at DESC, capped at limit (default 100, max 500).
func (s *AllowListStore) ListByPattern(
	ctx context.Context,
	tenantID uuid.UUID,
	patternType, kind string,
	limit int,
) ([]AllowListRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, rule_id, pattern, reason, expires_at,
		       created_at, created_by, updated_at, updated_by
		FROM detection.allow_list
		WHERE tenant_id = $1
		  AND pattern->>'type' = $2
		  AND pattern->>'kind' = $3
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT $4`,
		tenantID, patternType, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("lp: allow_list by pattern: %w", err)
	}
	defer rows.Close()

	out := make([]AllowListRow, 0, limit)
	for rows.Next() {
		var r AllowListRow
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
			&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("lp: allow_list by pattern scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateInput is the wire-shape for AllowListStore.Create.
// RuleID, Reason, ExpiresAt, CreatedBy are optional (zero/nil to skip).
type CreateInput struct {
	TenantID  uuid.UUID
	RuleID    *uuid.UUID
	Pattern   json.RawMessage // must be a valid JSON object with "type" and "kind"
	Reason    *string
	ExpiresAt *time.Time
	CreatedBy *uuid.UUID
}

// Create inserts a new allow-list entry and returns the persisted row. The
// pattern jsonb must encode at minimum a "type" and "kind" discriminator —
// callers should construct it via NewPattern below to keep the discriminator
// shape consistent.
//
// TenantID may be uuid.Nil during the pre-identity-middleware bootstrap; the
// DB column has NOT NULL but accepts the zero UUID. Once GRO-769 lands, all
// writes will carry the real tenant id from request context.
func (s *AllowListStore) Create(ctx context.Context, in CreateInput) (AllowListRow, error) {
	if len(in.Pattern) == 0 {
		return AllowListRow{}, fmt.Errorf("lp: allow_list create: pattern required")
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO detection.allow_list (
			tenant_id, rule_id, pattern, reason, expires_at, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING id, tenant_id, rule_id, pattern, reason, expires_at,
		          created_at, created_by, updated_at, updated_by`,
		in.TenantID, in.RuleID, []byte(in.Pattern), in.Reason, in.ExpiresAt, in.CreatedBy)

	var r AllowListRow
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
		&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
	); err != nil {
		return AllowListRow{}, fmt.Errorf("lp: allow_list create: %w", err)
	}
	return r, nil
}

// UpdateInput identifies the row and carries the fields to mutate.
// Only non-nil fields are written; the others retain their existing values.
type UpdateInput struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Pattern   json.RawMessage // optional — set to update
	Reason    *string         // optional
	ExpiresAt *time.Time      // optional
	UpdatedBy *uuid.UUID      // optional — written to updated_by column
}

// Update mutates a single allow-list row using COALESCE for partial updates.
// Returns ErrAllowListNotFound if the (tenant_id, id) pair has no row.
// id must be non-nil; tenant_id may be uuid.Nil for pre-identity bootstrap rows.
func (s *AllowListStore) Update(ctx context.Context, in UpdateInput) (AllowListRow, error) {
	if in.ID == uuid.Nil {
		return AllowListRow{}, fmt.Errorf("lp: allow_list update: id required")
	}

	var patternBytes []byte
	if len(in.Pattern) > 0 {
		patternBytes = []byte(in.Pattern)
	}

	row := s.pool.QueryRow(ctx, `
		UPDATE detection.allow_list
		SET pattern    = COALESCE($3::jsonb, pattern),
		    reason     = COALESCE($4, reason),
		    expires_at = COALESCE($5, expires_at),
		    updated_by = COALESCE($6, updated_by),
		    updated_at = NOW()
		WHERE tenant_id = $1 AND id = $2
		RETURNING id, tenant_id, rule_id, pattern, reason, expires_at,
		          created_at, created_by, updated_at, updated_by`,
		in.TenantID, in.ID, patternBytes, in.Reason, in.ExpiresAt, in.UpdatedBy)

	var r AllowListRow
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
		&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AllowListRow{}, ErrAllowListNotFound
		}
		return AllowListRow{}, fmt.Errorf("lp: allow_list update: %w", err)
	}
	return r, nil
}

// Delete removes a single allow-list row. Returns ErrAllowListNotFound if the
// (tenant_id, id) pair has no row. Tenant scoping in the WHERE clause prevents
// cross-tenant deletes even if a caller passes a leaked ID. id must be
// non-nil; tenant_id may be uuid.Nil for pre-identity bootstrap rows.
func (s *AllowListStore) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("lp: allow_list delete: id required")
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM detection.allow_list
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	if err != nil {
		return fmt.Errorf("lp: allow_list delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAllowListNotFound
	}
	return nil
}

// Get returns a single allow-list row by id (tenant-scoped). Returns
// ErrAllowListNotFound if the (tenant_id, id) pair has no row.
func (s *AllowListStore) Get(ctx context.Context, tenantID, id uuid.UUID) (AllowListRow, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, rule_id, pattern, reason, expires_at,
		       created_at, created_by, updated_at, updated_by
		FROM detection.allow_list
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	var r AllowListRow
	if err := row.Scan(
		&r.ID, &r.TenantID, &r.RuleID, &r.Pattern, &r.Reason, &r.ExpiresAt,
		&r.CreatedAt, &r.CreatedBy, &r.UpdatedAt, &r.UpdatedBy,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AllowListRow{}, ErrAllowListNotFound
		}
		return AllowListRow{}, fmt.Errorf("lp: allow_list get: %w", err)
	}
	return r, nil
}

// ── Pattern jsonb helpers ─────────────────────────────────────────────────────
// Construct pattern jsonb with the type+kind discriminator + arbitrary fields.

// NewPattern returns a pattern jsonb that always carries type and kind plus
// the supplied fields. Callers don't need to remember to add the discriminator
// keys — these helpers enforce the shape.
func NewPattern(patternType, kind string, fields map[string]any) (json.RawMessage, error) {
	m := map[string]any{
		"type": patternType,
		"kind": kind,
	}
	for k, v := range fields {
		if k == "type" || k == "kind" {
			continue // reserved
		}
		m[k] = v
	}
	return json.Marshal(m)
}

// DecodePattern parses a pattern jsonb back into a map for template rendering.
// Returns nil if the pattern is empty.
func DecodePattern(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("lp: decode pattern: %w", err)
	}
	return m, nil
}
