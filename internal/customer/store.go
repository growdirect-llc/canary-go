// internal/customer/store.go
//
// pgx-backed customer store. Reads customer.customers and customer.loyalty_memberships.
// No mutation endpoints — customer records are written by the Bull ingest
// pipeline from POS transaction data.
//
//

package customer

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the pgx-backed customer access layer.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var ErrNotFound = errors.New("customer: not found")

const customerCols = `
	c.id, c.tenant_id, c.customer_code, c.customer_type,
	c.first_name, c.last_name, c.display_name,
	c.email, c.phone, c.marketing_opt_in,
	c.status, c.created_at, c.updated_at`

func scanCustomer(row interface{ Scan(dest ...any) error }) (*CustomerDTO, error) {
	var c CustomerDTO
	return &c, row.Scan(
		&c.ID, &c.TenantID, &c.CustomerCode, &c.CustomerType,
		&c.FirstName, &c.LastName, &c.DisplayName,
		&c.Email, &c.Phone, &c.MarketingOptIn,
		&c.Status, &c.CreatedAt, &c.UpdatedAt,
	)
}

// List returns customers matching the filters.
func (s *Store) List(ctx context.Context, f ListFilters) ([]CustomerDTO, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.TenantID}
	q := `SELECT` + customerCols + `
		FROM customer.customers c
		WHERE c.tenant_id = $1`

	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND c.status = $%d", len(args))
	} else {
		q += " AND c.status != 'merged'"
	}
	if f.CustomerType != "" {
		args = append(args, f.CustomerType)
		q += fmt.Sprintf(" AND c.customer_type = $%d", len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		idx := len(args)
		q += fmt.Sprintf(
			" AND (c.display_name ILIKE $%d OR c.email ILIKE $%d OR c.phone ILIKE $%d)",
			idx, idx, idx,
		)
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY c.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("customer: list: %w", err)
	}
	defer rows.Close()
	out := make([]CustomerDTO, 0, f.Limit)
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, fmt.Errorf("customer: list scan: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetByID returns a single customer.
func (s *Store) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*CustomerDTO, error) {
	q := `SELECT` + customerCols + `
		FROM customer.customers c
		WHERE c.tenant_id = $1 AND c.id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	c, err := scanCustomer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("customer: get: %w", err)
	}
	return c, nil
}

// GetMemberships returns all loyalty memberships for a customer.
func (s *Store) GetMemberships(ctx context.Context, tenantID, customerID uuid.UUID) ([]LoyaltyMembershipDTO, error) {
	const q = `
		SELECT id, tenant_id, customer_id, program_code, membership_number,
		       tier, points_balance, points_lifetime, status, expires_at,
		       created_at, updated_at
		FROM customer.loyalty_memberships
		WHERE tenant_id = $1 AND customer_id = $2
		ORDER BY program_code, created_at`
	rows, err := s.pool.Query(ctx, q, tenantID, customerID)
	if err != nil {
		return nil, fmt.Errorf("customer: memberships: %w", err)
	}
	defer rows.Close()
	var out []LoyaltyMembershipDTO
	for rows.Next() {
		var m LoyaltyMembershipDTO
		if err := rows.Scan(
			&m.ID, &m.TenantID, &m.CustomerID, &m.ProgramCode, &m.MembershipNumber,
			&m.Tier, &m.PointsBalance, &m.PointsLifetime, &m.Status, &m.ExpiresAt,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("customer: memberships scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
