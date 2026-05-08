// Package supplier is the vendor master / supplier profile store.
// Wires app.suppliers — minimal viable shape per W11.
//
// Out of scope (per dispatch): EDI, vendor self-service, RFQ.

package supplier

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
	ComplianceActive  = "active"
	ComplianceReview  = "review"
	ComplianceBlocked = "blocked"
)

var (
	ErrNotFound   = errors.New("supplier: not found")
	ErrValidation = errors.New("supplier: validation failed")
)

// Supplier is the wire-shape for an app.suppliers row.
type Supplier struct {
	ID               uuid.UUID
	MerchantID       uuid.UUID
	SupplierName     string
	ContactEmail     *string
	ContactPhone     *string
	PaymentTerms     *string
	ComplianceStatus string
	Attributes       json.RawMessage
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DBStatus         string
}

// CreateRequest is the create body.
type CreateRequest struct {
	MerchantID       uuid.UUID
	SupplierName     string
	ContactEmail     *string
	ContactPhone     *string
	PaymentTerms     *string
	ComplianceStatus string
}

// Store wraps the pool.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts a new supplier.
func (s *Store) Create(ctx context.Context, req CreateRequest) (*Supplier, error) {
	if req.MerchantID == uuid.Nil {
		return nil, fmt.Errorf("%w: merchant_id required", ErrValidation)
	}
	if req.SupplierName == "" {
		return nil, fmt.Errorf("%w: supplier_name required", ErrValidation)
	}
	status := req.ComplianceStatus
	if status == "" {
		status = ComplianceActive
	}
	const q = `
		INSERT INTO app.suppliers
		    (merchant_id, supplier_name, contact_email, contact_phone, payment_terms, compliance_status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, merchant_id, supplier_name, contact_email, contact_phone, payment_terms,
		          compliance_status, attributes, created_at, updated_at, db_status`
	row := s.pool.QueryRow(ctx, q,
		req.MerchantID, req.SupplierName, req.ContactEmail, req.ContactPhone, req.PaymentTerms, status)
	return scanSupplier(row)
}

// List returns suppliers for a merchant. compliance filter is optional.
func (s *Store) List(ctx context.Context, merchantID uuid.UUID, compliance string, limit int) ([]Supplier, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{merchantID}
	q := `SELECT id, merchant_id, supplier_name, contact_email, contact_phone, payment_terms,
	             compliance_status, attributes, created_at, updated_at, db_status
	      FROM app.suppliers
	      WHERE merchant_id = $1 AND db_status = 'active'`
	if compliance != "" {
		args = append(args, compliance)
		q += " AND compliance_status = $2"
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY supplier_name ASC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("supplier: list: %w", err)
	}
	defer rows.Close()
	out := make([]Supplier, 0, limit)
	for rows.Next() {
		sp, err := scanSupplier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sp)
	}
	return out, rows.Err()
}

// Get returns a single supplier by id.
func (s *Store) Get(ctx context.Context, merchantID, id uuid.UUID) (*Supplier, error) {
	const q = `
		SELECT id, merchant_id, supplier_name, contact_email, contact_phone, payment_terms,
		       compliance_status, attributes, created_at, updated_at, db_status
		FROM app.suppliers WHERE merchant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, merchantID, id)
	out, err := scanSupplier(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return out, err
}

type scannable interface{ Scan(dest ...any) error }

func scanSupplier(r scannable) (*Supplier, error) {
	var sp Supplier
	if err := r.Scan(
		&sp.ID, &sp.MerchantID, &sp.SupplierName, &sp.ContactEmail, &sp.ContactPhone,
		&sp.PaymentTerms, &sp.ComplianceStatus, &sp.Attributes, &sp.CreatedAt, &sp.UpdatedAt, &sp.DBStatus,
	); err != nil {
		return nil, err
	}
	return &sp, nil
}
