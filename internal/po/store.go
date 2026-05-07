// Package po is the purchase-order lifecycle store.
// Wires app.purchase_orders + app.purchase_order_lines per W11 / GRO-830.

package po

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

const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusReceived  = "received"
	StatusClosed    = "closed"
	StatusCancelled = "cancelled"
)

var (
	ErrNotFound          = errors.New("po: not found")
	ErrValidation        = errors.New("po: validation failed")
	ErrInvalidTransition = errors.New("po: invalid status transition")
)

// PO is the wire-shape for an app.purchase_orders row.
type PO struct {
	ID          uuid.UUID
	MerchantID  uuid.UUID
	SupplierID  uuid.UUID
	PONumber    string
	Status      string
	ExpectedAt  *time.Time
	TotalCost   *string
	Attributes  json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
	SubmittedAt *time.Time
	ClosedAt    *time.Time
}

// Line is the wire-shape for a purchase_order_lines row.
type Line struct {
	ID          uuid.UUID
	MerchantID  uuid.UUID
	POID        uuid.UUID
	LineNumber  int
	ItemID      *uuid.UUID
	Description *string
	OrderedQty  string
	ReceivedQty string
	UnitCost    *string
	Attributes  json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateRequest is the create body.
type CreateRequest struct {
	MerchantID uuid.UUID
	SupplierID uuid.UUID
	PONumber   string
	ExpectedAt *time.Time
	TotalCost  *string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a new draft PO.
func (s *Store) Create(ctx context.Context, req CreateRequest) (*PO, error) {
	if req.MerchantID == uuid.Nil || req.SupplierID == uuid.Nil {
		return nil, fmt.Errorf("%w: merchant_id and supplier_id required", ErrValidation)
	}
	if req.PONumber == "" {
		return nil, fmt.Errorf("%w: po_number required", ErrValidation)
	}
	const q = `
		INSERT INTO app.purchase_orders
		    (merchant_id, supplier_id, po_number, expected_at, total_cost)
		VALUES ($1, $2, $3, $4, $5::numeric)
		RETURNING id, merchant_id, supplier_id, po_number, status, expected_at,
		          total_cost::text, attributes, created_at, updated_at, submitted_at, closed_at`
	row := s.pool.QueryRow(ctx, q,
		req.MerchantID, req.SupplierID, req.PONumber, req.ExpectedAt, req.TotalCost)
	return scanPO(row)
}

// List returns POs for a merchant. status filter is optional.
func (s *Store) List(ctx context.Context, merchantID uuid.UUID, status string, limit int) ([]PO, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{merchantID}
	q := `SELECT id, merchant_id, supplier_id, po_number, status, expected_at,
	             total_cost::text, attributes, created_at, updated_at, submitted_at, closed_at
	      FROM app.purchase_orders WHERE merchant_id = $1`
	if status != "" {
		args = append(args, status)
		q += " AND status = $2"
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("po: list: %w", err)
	}
	defer rows.Close()
	out := make([]PO, 0, limit)
	for rows.Next() {
		p, err := scanPO(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get returns a single PO by id.
func (s *Store) Get(ctx context.Context, merchantID, id uuid.UUID) (*PO, error) {
	const q = `
		SELECT id, merchant_id, supplier_id, po_number, status, expected_at,
		       total_cost::text, attributes, created_at, updated_at, submitted_at, closed_at
		FROM app.purchase_orders WHERE merchant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, merchantID, id)
	out, err := scanPO(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return out, err
}

// ListLines returns lines for a PO ordered by line_number.
func (s *Store) ListLines(ctx context.Context, merchantID, poID uuid.UUID) ([]Line, error) {
	const q = `
		SELECT id, merchant_id, po_id, line_number, item_id, description,
		       ordered_qty::text, received_qty::text, unit_cost::text,
		       attributes, created_at, updated_at
		FROM app.purchase_order_lines
		WHERE merchant_id = $1 AND po_id = $2
		ORDER BY line_number ASC`
	rows, err := s.pool.Query(ctx, q, merchantID, poID)
	if err != nil {
		return nil, fmt.Errorf("po: list lines: %w", err)
	}
	defer rows.Close()
	out := make([]Line, 0, 16)
	for rows.Next() {
		var l Line
		if err := rows.Scan(
			&l.ID, &l.MerchantID, &l.POID, &l.LineNumber, &l.ItemID, &l.Description,
			&l.OrderedQty, &l.ReceivedQty, &l.UnitCost,
			&l.Attributes, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpdateStatus advances a PO through its lifecycle. Validates the
// transition; ErrInvalidTransition for illegal moves.
func (s *Store) UpdateStatus(ctx context.Context, merchantID, id uuid.UUID, to string) (*PO, error) {
	current, err := s.Get(ctx, merchantID, id)
	if err != nil {
		return nil, err
	}
	if !validTransition(current.Status, to) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, current.Status, to)
	}
	q := `UPDATE app.purchase_orders SET status = $1, updated_at = NOW()`
	args := []any{to}
	idx := 2
	switch to {
	case StatusSubmitted:
		q += fmt.Sprintf(", submitted_at = COALESCE(submitted_at, NOW())")
	case StatusClosed, StatusCancelled:
		q += fmt.Sprintf(", closed_at = COALESCE(closed_at, NOW())")
	}
	q += fmt.Sprintf(" WHERE merchant_id = $%d AND id = $%d RETURNING id, merchant_id, supplier_id, po_number, status, expected_at, total_cost::text, attributes, created_at, updated_at, submitted_at, closed_at", idx, idx+1)
	args = append(args, merchantID, id)
	row := s.pool.QueryRow(ctx, q, args...)
	return scanPO(row)
}

func validTransition(from, to string) bool {
	allowed := map[string]map[string]bool{
		StatusDraft:     {StatusSubmitted: true, StatusCancelled: true},
		StatusSubmitted: {StatusReceived: true, StatusCancelled: true},
		StatusReceived:  {StatusClosed: true},
		StatusClosed:    {},
		StatusCancelled: {},
	}
	if moves, ok := allowed[from]; ok && moves[to] {
		return true
	}
	return false
}

type scannable interface{ Scan(dest ...any) error }

func scanPO(r scannable) (*PO, error) {
	var p PO
	if err := r.Scan(
		&p.ID, &p.MerchantID, &p.SupplierID, &p.PONumber, &p.Status, &p.ExpectedAt,
		&p.TotalCost, &p.Attributes, &p.CreatedAt, &p.UpdatedAt, &p.SubmittedAt, &p.ClosedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}
