// internal/inventory/store.go
//
// pgx-backed access to i.inventory_positions and i.inventory_movements.
//
// Loop 2 dispatch override: direct pgx + raw SQL. The CanaryGo CLAUDE.md
// rule "all queries go through sqlc" is paused for Loop 2 — sqlc retrofit
// happens in Loop 3.
//
// Design invariants:
//
//  1. inventory_movements is APPEND-ONLY. There are no UpdateMovement or
//     DeleteMovement methods on this Store, even private. Per dispatch
//     line 67: "even private ones." The only mutation is INSERT.
//
//  2. inventory_document_lines.variance_quantity is GENERATED STORED. We
//     do not write to it. (This package does not touch document_lines yet
//     — flagged here so Wave 3+ inherits the invariant.)
//
//  3. AppendMovement is transactional: INSERT into inventory_movements +
//     UPSERT into inventory_positions, both committed together. The schema
//     ships no trigger to maintain on_hand_quantity from the movement log,
//     so the service does it inline. This makes the position row a cached
//     running balance, not a stale snapshot.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed access layer for the inventory package.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store wrapping the given pgxpool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ErrPositionNotFound is returned by GetPosition when no position row
// exists for the (item, location) pair. Callers may interpret this as
// "zero on-hand" or as a 404 — that policy lives in the handler.
var ErrPositionNotFound = errors.New("inventory: position not found")

// GetPosition reads the current position for (tenant, item, location).
// Returns ErrPositionNotFound if no row exists. zone_id is treated as
// NULL (location-aggregate row); zone-level reads are out of scope for
// Loop 2.
func (s *Store) GetPosition(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (*PositionDTO, error) {
	const q = `
		SELECT id, tenant_id, item_id, location_id, zone_id,
		       on_hand_quantity::text, reserved_quantity::text,
		       on_order_quantity::text, in_transit_quantity::text,
		       last_movement_at, last_count_at, cost_basis::text,
		       attributes, status, created_at, updated_at
		  FROM i.inventory_positions
		 WHERE tenant_id = $1
		   AND item_id = $2
		   AND location_id = $3
		   AND zone_id IS NULL
		 LIMIT 1`

	row := s.pool.QueryRow(ctx, q, tenantID, itemID, locationID)
	p, err := scanPosition(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPositionNotFound
		}
		return nil, fmt.Errorf("inventory: get position: %w", err)
	}
	return p, nil
}

// ListPositions returns positions filtered by tenant, optionally by
// location and/or item, with offset/limit pagination. Results are
// ordered by (location_id, item_id) for stable cursoring.
func (s *Store) ListPositions(ctx context.Context, tenantID uuid.UUID, locationID, itemID *uuid.UUID, limit, offset int) ([]PositionDTO, error) {
	// Build the WHERE clause incrementally so optional filters stay
	// indexable. tenant_id is always required.
	q := `
		SELECT id, tenant_id, item_id, location_id, zone_id,
		       on_hand_quantity::text, reserved_quantity::text,
		       on_order_quantity::text, in_transit_quantity::text,
		       last_movement_at, last_count_at, cost_basis::text,
		       attributes, status, created_at, updated_at
		  FROM i.inventory_positions
		 WHERE tenant_id = $1`

	args := []any{tenantID}
	idx := 2
	if locationID != nil {
		q += fmt.Sprintf(" AND location_id = $%d", idx)
		args = append(args, *locationID)
		idx++
	}
	if itemID != nil {
		q += fmt.Sprintf(" AND item_id = $%d", idx)
		args = append(args, *itemID)
		idx++
	}
	q += fmt.Sprintf(" ORDER BY location_id, item_id LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("inventory: list positions: %w", err)
	}
	defer rows.Close()

	out := make([]PositionDTO, 0, limit)
	for rows.Next() {
		p, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory: scan position: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inventory: list positions iter: %w", err)
	}
	return out, nil
}

// AppendMovement INSERTs a new row into i.inventory_movements and
// updates the matching i.inventory_positions row in a single transaction.
// Returns the persisted movement and the new position.
//
// If no position row exists for (tenant, item, location, zone=NULL),
// one is created with on_hand_quantity = quantity_delta. The DB-side
// `UNIQUE NULLS NOT DISTINCT (tenant_id, item_id, location_id, zone_id)`
// constraint handles concurrent inserts.
func (s *Store) AppendMovement(ctx context.Context, req AppendMovementRequest, movementAt time.Time) (*MovementDTO, *PositionDTO, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe no-op after Commit

	movID := uuid.New()
	const insertQ = `
		INSERT INTO i.inventory_movements
		    (id, tenant_id, item_id, location_id, zone_id, lot_id,
		     movement_type, quantity_delta, movement_at,
		     source_document_id, source_transaction_id,
		     reason_code, reference, performed_by_user_id,
		     cost_basis, attributes)
		VALUES
		    ($1, $2, $3, $4, $5, $6,
		     $7, $8::numeric, $9,
		     $10, $11,
		     $12, $13, $14,
		     $15::numeric, COALESCE($16, '{}'::jsonb))
		RETURNING id, tenant_id, item_id, location_id, zone_id, lot_id,
		          movement_type, quantity_delta::text, movement_at,
		          source_document_id, source_document_line_id,
		          source_transaction_id, reason_code, reference,
		          performed_by_user_id, performed_by_employee_id,
		          cost_basis::text, attributes, created_at`

	mrow := tx.QueryRow(ctx, insertQ,
		movID, req.MerchantID, req.ItemID, req.LocationID, req.ZoneID, req.LotID,
		req.MovementType, req.Quantity, movementAt,
		req.SourceDocumentID, req.SourceTransactionID,
		req.ReasonCode, req.ReferenceID, req.PerformedByUserID,
		req.CostBasis, req.Attributes,
	)
	mov, err := scanMovement(mrow)
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: insert movement: %w", err)
	}

	// UPSERT the position row. ON CONFLICT path uses the unique constraint
	// (tenant_id, item_id, location_id, zone_id) to add the delta atomically.
	// Cost-basis is updated if provided; otherwise left untouched.
	const upsertQ = `
		INSERT INTO i.inventory_positions
		    (tenant_id, item_id, location_id, zone_id,
		     on_hand_quantity, last_movement_at, cost_basis)
		VALUES ($1, $2, $3, NULL,
		        $4::numeric, $5, $6::numeric)
		ON CONFLICT (tenant_id, item_id, location_id, zone_id)
		DO UPDATE SET
		    on_hand_quantity = i.inventory_positions.on_hand_quantity + EXCLUDED.on_hand_quantity,
		    last_movement_at = EXCLUDED.last_movement_at,
		    cost_basis       = COALESCE(EXCLUDED.cost_basis, i.inventory_positions.cost_basis),
		    updated_at       = now()
		RETURNING id, tenant_id, item_id, location_id, zone_id,
		          on_hand_quantity::text, reserved_quantity::text,
		          on_order_quantity::text, in_transit_quantity::text,
		          last_movement_at, last_count_at, cost_basis::text,
		          attributes, status, created_at, updated_at`

	prow := tx.QueryRow(ctx, upsertQ,
		req.MerchantID, req.ItemID, req.LocationID,
		req.Quantity, movementAt, req.CostBasis,
	)
	pos, err := scanPosition(prow)
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: upsert position: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("inventory: commit: %w", err)
	}
	return mov, pos, nil
}

// ListMovements returns the audit-trail movements for (tenant, item,
// location) filtered to the [from, to] window if provided. Ordered by
// movement_at DESC (most recent first).
func (s *Store) ListMovements(ctx context.Context, tenantID, itemID, locationID uuid.UUID, from, to *time.Time, limit, offset int) ([]MovementDTO, error) {
	q := `
		SELECT id, tenant_id, item_id, location_id, zone_id, lot_id,
		       movement_type, quantity_delta::text, movement_at,
		       source_document_id, source_document_line_id,
		       source_transaction_id, reason_code, reference,
		       performed_by_user_id, performed_by_employee_id,
		       cost_basis::text, attributes, created_at
		  FROM i.inventory_movements
		 WHERE tenant_id = $1
		   AND item_id = $2
		   AND location_id = $3`

	args := []any{tenantID, itemID, locationID}
	idx := 4
	if from != nil {
		q += fmt.Sprintf(" AND movement_at >= $%d", idx)
		args = append(args, *from)
		idx++
	}
	if to != nil {
		q += fmt.Sprintf(" AND movement_at <= $%d", idx)
		args = append(args, *to)
		idx++
	}
	q += fmt.Sprintf(" ORDER BY movement_at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("inventory: list movements: %w", err)
	}
	defer rows.Close()

	out := make([]MovementDTO, 0, limit)
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory: scan movement: %w", err)
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inventory: list movements iter: %w", err)
	}
	return out, nil
}

// scannable is the small subset of pgx.Row / pgx.Rows that scan helpers
// need. Lets us share scan code between QueryRow and Query iterations.
type scannable interface {
	Scan(dest ...any) error
}

func scanPosition(r scannable) (*PositionDTO, error) {
	var p PositionDTO
	if err := r.Scan(
		&p.ID, &p.TenantID, &p.ItemID, &p.LocationID, &p.ZoneID,
		&p.OnHandQuantity, &p.ReservedQuantity,
		&p.OnOrderQuantity, &p.InTransitQuantity,
		&p.LastMovementAt, &p.LastCountAt, &p.CostBasis,
		&p.Attributes, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func scanMovement(r scannable) (*MovementDTO, error) {
	var m MovementDTO
	if err := r.Scan(
		&m.ID, &m.TenantID, &m.ItemID, &m.LocationID, &m.ZoneID, &m.LotID,
		&m.MovementType, &m.QuantityDelta, &m.MovementAt,
		&m.SourceDocumentID, &m.SourceDocumentLineID,
		&m.SourceTransactionID, &m.ReasonCode, &m.Reference,
		&m.PerformedByUserID, &m.PerformedByEmployeeID,
		&m.CostBasis, &m.Attributes, &m.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &m, nil
}
