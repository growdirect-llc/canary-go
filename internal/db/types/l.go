// Code generated from deploy/schema/03_l_s_locations.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Location mirrors l.locations.
type Location struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	LocationCode      string          `db:"location_code"`
	Name              string          `db:"name"`
	LocationType      string          `db:"location_type"`
	ParentLocationID  *uuid.UUID      `db:"parent_location_id"`
	Banner            *string         `db:"banner"`
	Status            string          `db:"status"`
	OpenDate          *time.Time      `db:"open_date"`
	CloseDate         *time.Time      `db:"close_date"`
	RemodelDate       *time.Time      `db:"remodel_date"`
	SquareFootage     *int32          `db:"square_footage"`
	SellingAreaSqft   *int32          `db:"selling_area_sqft"`
	StorageAreaSqft   *int32          `db:"storage_area_sqft"`
	Channel           *string         `db:"channel"`
	Format            *string         `db:"format"`
	Currency          string          `db:"currency"`
	Language          string          `db:"language"`
	Timezone          string          `db:"timezone"`
	Address           json.RawMessage `db:"address"`
	Contact           json.RawMessage `db:"contact"`
	OperatingHours    json.RawMessage `db:"operating_hours"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// LocationHierarchy mirrors l.location_hierarchy.
type LocationHierarchy struct {
	ID            uuid.UUID       `db:"id"`
	TenantID      uuid.UUID       `db:"tenant_id"`
	ParentID      *uuid.UUID      `db:"parent_id"`
	Code          string          `db:"code"`
	Name          string          `db:"name"`
	HierarchyType string          `db:"hierarchy_type"`
	Level         int32           `db:"level"`
	Path *string `db:"path"` // ltree — string with TODO: ltree-aware type for
	Attributes    json.RawMessage `db:"attributes"`
	Status        string          `db:"status"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

// LocationHierarchyAssignment mirrors l.location_hierarchy_assignments.
type LocationHierarchyAssignment struct {
	LocationID   uuid.UUID `db:"location_id"`
	HierarchyID  uuid.UUID `db:"hierarchy_id"`
}

// LocationZone mirrors l.location_zones.
type LocationZone struct {
	ID           uuid.UUID       `db:"id"`
	TenantID     uuid.UUID       `db:"tenant_id"`
	LocationID   uuid.UUID       `db:"location_id"`
	ParentZoneID *uuid.UUID      `db:"parent_zone_id"`
	Code         string          `db:"code"`
	Name         string          `db:"name"`
	ZoneType     string          `db:"zone_type"`
	Level        int32           `db:"level"`
	Path *string `db:"path"` // ltree — string with TODO: ltree-aware type for
	Geometry     json.RawMessage `db:"geometry"`
	Attributes   json.RawMessage `db:"attributes"`
	Status       string          `db:"status"`
	CreatedAt    time.Time       `db:"created_at"`
	UpdatedAt    time.Time       `db:"updated_at"`
}

// LocationAssortment mirrors l.location_assortment.
type LocationAssortment struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	LocationID        uuid.UUID       `db:"location_id"`
	ItemID            uuid.UUID       `db:"item_id"`
	ZoneID            *uuid.UUID      `db:"zone_id"`
	AssortmentTier    string          `db:"assortment_tier"`
	EffectiveStart    *time.Time      `db:"effective_start"`
	EffectiveEnd      *time.Time      `db:"effective_end"`
	SourcePlanogramID *uuid.UUID      `db:"source_planogram_id"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}
