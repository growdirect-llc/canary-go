// Package hierarchy is the merchant location-hierarchy reader/writer.
// Wraps app.location_hierarchy + app.locations for the W10 multi-store
// portal.
//
// Tenant scoping happens via merchant_id (the column carried in both
// tables today). Direct pgx is fine here per the amended sqlc rule
// (read paths + simple writes acceptable).
package hierarchy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("hierarchy: not found")

// Node mirrors a row in app.location_hierarchy.
type Node struct {
	ID        uuid.UUID
	Name      string
	Level     int
	ParentID  *uuid.UUID
	CreatedAt time.Time
}

// Location mirrors a relevant subset of app.locations for the portal.
type Location struct {
	ID           uuid.UUID
	LocationName string
	City         *string
	State        *string
	IsActive     bool
	CreatedAt    time.Time
}

// Store wraps the pool.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ListNodes returns the merchant's hierarchy nodes ordered by level ASC.
func (s *Store) ListNodes(ctx context.Context, merchantID uuid.UUID) ([]Node, error) {
	const q = `
		SELECT id, name, level, parent_id, created_at
		  FROM app.location_hierarchy
		 WHERE merchant_id = $1 AND db_status = 'active'
		 ORDER BY level ASC, name ASC`
	rows, err := s.pool.Query(ctx, q, merchantID)
	if err != nil {
		return nil, fmt.Errorf("hierarchy: list nodes: %w", err)
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.Level, &n.ParentID, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CreateNode inserts a new hierarchy row. parentID is optional.
func (s *Store) CreateNode(ctx context.Context, merchantID uuid.UUID, name string, level int, parentID *uuid.UUID) (*Node, error) {
	const q = `
		INSERT INTO app.location_hierarchy (merchant_id, name, level, parent_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, level, parent_id, created_at`
	row := s.pool.QueryRow(ctx, q, merchantID, name, level, parentID)
	var n Node
	if err := row.Scan(&n.ID, &n.Name, &n.Level, &n.ParentID, &n.CreatedAt); err != nil {
		return nil, fmt.Errorf("hierarchy: create node: %w", err)
	}
	return &n, nil
}

// ListLocations returns the merchant's active locations ordered by name.
func (s *Store) ListLocations(ctx context.Context, merchantID uuid.UUID) ([]Location, error) {
	const q = `
		SELECT id, location_name, city, state, is_active, created_at
		  FROM app.locations
		 WHERE merchant_id = $1 AND db_status = 'active'
		 ORDER BY location_name ASC`
	rows, err := s.pool.Query(ctx, q, merchantID)
	if err != nil {
		return nil, fmt.Errorf("hierarchy: list locations: %w", err)
	}
	defer rows.Close()
	var out []Location
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.ID, &l.LocationName, &l.City, &l.State, &l.IsActive, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLocation returns one location by ID.
func (s *Store) GetLocation(ctx context.Context, merchantID, id uuid.UUID) (*Location, error) {
	const q = `
		SELECT id, location_name, city, state, is_active, created_at
		  FROM app.locations
		 WHERE merchant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, merchantID, id)
	var l Location
	if err := row.Scan(&l.ID, &l.LocationName, &l.City, &l.State, &l.IsActive, &l.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("hierarchy: get location: %w", err)
	}
	return &l, nil
}
