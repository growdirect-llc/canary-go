// internal/asset/store.go
//
// pgx-backed asset store. Reads inventory.inventory_positions, inventory.inventory_lots,
// inventory.inventory_movements, and catalog.items. The flag write creates an
// inventory.inventory_movements row (adjustment); it does not update
// inventory.inventory_positions — that is the job of the Bull ingest pipeline
// which processes movements and reconciles SOH.
//
// Spec: GRO-766 Phase C.

package asset

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed asset access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var ErrNotFound = errors.New("asset: not found")

// List returns inventory positions matching the filters.
func (s *Store) List(ctx context.Context, f ListFilters) ([]PositionRow, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `
		SELECT
		    p.item_id, mi.sku, mi.description,
		    p.location_id, p.zone_id,
		    p.on_hand_quantity, p.reserved_quantity,
		    p.on_order_quantity, p.in_transit_quantity,
		    p.cost_basis, p.status,
		    p.last_movement_at, p.last_count_at
		FROM inventory.inventory_positions p
		JOIN catalog.items mi ON mi.id = p.item_id
		WHERE p.tenant_id = $1`

	if f.LocationID != nil {
		args = append(args, *f.LocationID)
		q += fmt.Sprintf(" AND p.location_id = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND p.status = $%d", len(args))
	}
	if f.LowStock {
		q += " AND p.on_hand_quantity <= 0"
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY mi.description ASC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("asset: list: %w", err)
	}
	defer rows.Close()
	out := make([]PositionRow, 0, f.Limit)
	for rows.Next() {
		var p PositionRow
		err := rows.Scan(
			&p.ItemID, &p.SKU, &p.Description,
			&p.LocationID, &p.ZoneID,
			&p.OnHand, &p.Reserved,
			&p.OnOrder, &p.InTransit,
			&p.CostBasis, &p.Status,
			&p.LastMovementAt, &p.LastCountAt,
		)
		if err != nil {
			return nil, fmt.Errorf("asset: list scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetItem returns an ItemDetail with positions and lots for a single item.
func (s *Store) GetItem(ctx context.Context, tenantID, itemID uuid.UUID) (*ItemDetail, error) {
	// Item header
	const itemQ = `
		SELECT id, sku, description, item_type, unit_of_measure, status
		FROM catalog.items
		WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, itemQ, tenantID, itemID)
	var d ItemDetail
	if err := row.Scan(&d.ItemID, &d.SKU, &d.Description, &d.ItemType, &d.UOM, &d.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("asset: get item: %w", err)
	}

	// Positions
	posQ := `
		SELECT
		    p.item_id, mi.sku, mi.description,
		    p.location_id, p.zone_id,
		    p.on_hand_quantity, p.reserved_quantity,
		    p.on_order_quantity, p.in_transit_quantity,
		    p.cost_basis, p.status,
		    p.last_movement_at, p.last_count_at
		FROM inventory.inventory_positions p
		JOIN catalog.items mi ON mi.id = p.item_id
		WHERE p.tenant_id = $1 AND p.item_id = $2
		ORDER BY p.location_id`
	posRows, err := s.pool.Query(ctx, posQ, tenantID, itemID)
	if err != nil {
		return nil, fmt.Errorf("asset: get item positions: %w", err)
	}
	defer posRows.Close()
	for posRows.Next() {
		var p PositionRow
		if err := posRows.Scan(
			&p.ItemID, &p.SKU, &p.Description,
			&p.LocationID, &p.ZoneID,
			&p.OnHand, &p.Reserved,
			&p.OnOrder, &p.InTransit,
			&p.CostBasis, &p.Status,
			&p.LastMovementAt, &p.LastCountAt,
		); err != nil {
			return nil, fmt.Errorf("asset: get item pos scan: %w", err)
		}
		d.Positions = append(d.Positions, p)
	}
	if posRows.Err() != nil {
		return nil, fmt.Errorf("asset: get item pos rows: %w", posRows.Err())
	}

	// Active lots
	const lotQ = `
		SELECT id, lot_number, lot_type, expiry_date, status
		FROM inventory.inventory_lots
		WHERE tenant_id = $1 AND item_id = $2 AND status IN ('active','quarantine')
		ORDER BY COALESCE(expiry_date, '9999-12-31') ASC, lot_number ASC`
	lotRows, err := s.pool.Query(ctx, lotQ, tenantID, itemID)
	if err != nil {
		return nil, fmt.Errorf("asset: get item lots: %w", err)
	}
	defer lotRows.Close()
	for lotRows.Next() {
		var l LotSummary
		if err := lotRows.Scan(&l.LotID, &l.LotNumber, &l.LotType, &l.ExpiryDate, &l.Status); err != nil {
			return nil, fmt.Errorf("asset: get item lot scan: %w", err)
		}
		d.Lots = append(d.Lots, l)
	}
	return &d, lotRows.Err()
}

// ShrinkMovements aggregates write_off and negative-adjustment movements
// by type + reason code over a date range.
func (s *Store) ShrinkMovements(ctx context.Context, tenantID uuid.UUID, from, to time.Time, locationID *uuid.UUID) ([]MovementShrinkRow, error) {
	args := []any{tenantID, from, to}
	locClause := ""
	if locationID != nil {
		args = append(args, *locationID)
		locClause = fmt.Sprintf(" AND m.location_id = $%d", len(args))
	}
	q := `
		SELECT
		    m.movement_type,
		    COALESCE(m.reason_code, '') AS reason_code,
		    COUNT(*) AS cnt,
		    SUM(m.quantity_delta) AS total_delta
		FROM inventory.inventory_movements m
		WHERE m.tenant_id = $1
		  AND m.movement_at >= $2
		  AND m.movement_at <= $3
		  AND (m.movement_type = 'write_off'
		       OR (m.movement_type = 'adjustment' AND m.quantity_delta < 0))` + locClause + `
		GROUP BY m.movement_type, COALESCE(m.reason_code, '')
		ORDER BY total_delta ASC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("asset: shrink movements: %w", err)
	}
	defer rows.Close()
	var out []MovementShrinkRow
	for rows.Next() {
		var r MovementShrinkRow
		if err := rows.Scan(&r.MovementType, &r.ReasonCode, &r.Count, &r.TotalDelta); err != nil {
			return nil, fmt.Errorf("asset: shrink scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Flag creates an inventory movement row to record a discrepancy flag.
// Does not update inventory_positions — the Bull pipeline reconciles SOH.
func (s *Store) Flag(ctx context.Context, tenantID, itemID uuid.UUID, req FlagRequest) (*FlagResponse, error) {
	now := time.Now().UTC()
	const q = `
		INSERT INTO inventory.inventory_movements
		    (tenant_id, item_id, location_id, movement_type, quantity_delta,
		     movement_at, reason_code, reference, performed_by_user_id)
		VALUES ($1, $2, $3, 'adjustment', $4, $5, $6, $7, $8)
		RETURNING id`
	row := s.pool.QueryRow(ctx, q,
		tenantID, itemID, req.LocationID,
		req.QuantityDelta, now,
		req.ReasonCode, req.Reference, req.PerformedByUser,
	)
	var movID uuid.UUID
	if err := row.Scan(&movID); err != nil {
		return nil, fmt.Errorf("asset: flag: %w", err)
	}
	return &FlagResponse{
		MovementID: movID,
		ItemID:     itemID,
		LocationID: req.LocationID,
		Delta:      req.QuantityDelta,
		ReasonCode: req.ReasonCode,
		MovementAt: now,
	}, nil
}
