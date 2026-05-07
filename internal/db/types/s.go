// Code generated from deploy/schema/03_l_s_locations.sql for Loop 2.
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Planogram mirrors s.planograms.
type Planogram struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	PlanogramCode    string          `db:"planogram_code"`
	Name             string          `db:"name"`
	CategoryID       *uuid.UUID      `db:"category_id"`
	EffectiveStart   *time.Time      `db:"effective_start"`
	EffectiveEnd     *time.Time      `db:"effective_end"`
	LayoutDimensions json.RawMessage `db:"layout_dimensions"`
	Status           string          `db:"status"`
	ApprovedBy       *uuid.UUID      `db:"approved_by"`
	ApprovedAt       *time.Time      `db:"approved_at"`
	Attributes       json.RawMessage `db:"attributes"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// PlanogramAssignment mirrors s.planogram_assignments.
type PlanogramAssignment struct {
	ID           uuid.UUID  `db:"id"`
	PlanogramID  uuid.UUID  `db:"planogram_id"`
	LocationID   uuid.UUID  `db:"location_id"`
	ZoneID       *uuid.UUID `db:"zone_id"`
	AssignedAt   time.Time  `db:"assigned_at"`
}

// PlanogramPosition mirrors s.planogram_positions.
type PlanogramPosition struct {
	ID              uuid.UUID       `db:"id"`
	TenantID        uuid.UUID       `db:"tenant_id"`
	PlanogramID     uuid.UUID       `db:"planogram_id"`
	ItemID          uuid.UUID       `db:"item_id"`
	ShelfNumber     *int32          `db:"shelf_number"`
	PositionOnShelf *int32          `db:"position_on_shelf"`
	Facings         int32           `db:"facings"`
	CapacityUnits   *int32          `db:"capacity_units"`
	Orientation     *string         `db:"orientation"`
	Geometry        json.RawMessage `db:"geometry"`
	Attributes      json.RawMessage `db:"attributes"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}
