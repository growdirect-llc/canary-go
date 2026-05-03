// internal/asset/dto.go
//
// Wire types for the asset (inventory) endpoints.
//
// Spec: GRO-766 Phase C.

package asset

import (
	"time"

	"github.com/google/uuid"
)

// PositionRow is one row in the inventory position list.
type PositionRow struct {
	ItemID           uuid.UUID  `json:"item_id"`
	SKU              string     `json:"sku"`
	Description      string     `json:"description"`
	LocationID       uuid.UUID  `json:"location_id"`
	ZoneID           *uuid.UUID `json:"zone_id,omitempty"`
	OnHand           float64    `json:"on_hand_quantity"`
	Reserved         float64    `json:"reserved_quantity"`
	OnOrder          float64    `json:"on_order_quantity"`
	InTransit        float64    `json:"in_transit_quantity"`
	CostBasis        *float64   `json:"cost_basis,omitempty"`
	Status           string     `json:"status"`
	LastMovementAt   *time.Time `json:"last_movement_at,omitempty"`
	LastCountAt      *time.Time `json:"last_count_at,omitempty"`
}

// ItemDetail is the full asset view for a single item: item metadata,
// all position rows across locations, and an active lot summary.
type ItemDetail struct {
	ItemID      uuid.UUID     `json:"item_id"`
	SKU         string        `json:"sku"`
	Description string        `json:"description"`
	ItemType    string        `json:"item_type"`
	UOM         string        `json:"unit_of_measure"`
	Status      string        `json:"status"`
	Positions   []PositionRow `json:"positions"`
	Lots        []LotSummary  `json:"lots"`
}

// LotSummary is a lightweight view of an active inventory lot.
type LotSummary struct {
	LotID      uuid.UUID  `json:"lot_id"`
	LotNumber  string     `json:"lot_number"`
	LotType    string     `json:"lot_type"`
	ExpiryDate *time.Time `json:"expiry_date,omitempty"`
	Status     string     `json:"status"`
}

// MovementShrinkRow is one row in the asset shrink summary.
type MovementShrinkRow struct {
	MovementType string  `json:"movement_type"`
	ReasonCode   string  `json:"reason_code"`
	Count        int64   `json:"count"`
	TotalDelta   float64 `json:"total_delta"` // sum of quantity_delta (negative = shrink)
}

// FlagRequest is the body for POST /v1/assets/{item_id}/flag.
// Creates an adjustment movement record to mark an inventory discrepancy.
type FlagRequest struct {
	LocationID      uuid.UUID `json:"location_id"`
	QuantityDelta   float64   `json:"quantity_delta"` // negative for loss
	ReasonCode      string    `json:"reason_code"`    // theft | damaged | spoilage | recount_corrected
	Reference       string    `json:"reference"`
	PerformedByUser *uuid.UUID `json:"performed_by_user_id,omitempty"`
}

// FlagResponse is the created movement record returned after a flag.
type FlagResponse struct {
	MovementID uuid.UUID  `json:"movement_id"`
	ItemID     uuid.UUID  `json:"item_id"`
	LocationID uuid.UUID  `json:"location_id"`
	Delta      float64    `json:"quantity_delta"`
	ReasonCode string     `json:"reason_code"`
	MovementAt time.Time  `json:"movement_at"`
}

// ListFilters controls the inventory position listing.
type ListFilters struct {
	TenantID   uuid.UUID
	LocationID *uuid.UUID
	Status     string // "active" | "discontinued" | "" (all)
	LowStock   bool   // only positions where on_hand_quantity <= 0
	Limit      int
	Offset     int
}
