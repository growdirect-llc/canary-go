// Code generated from deploy/schema/05_i_inventory.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// InventoryDocument mirrors i.inventory_documents.
type InventoryDocument struct {
	ID                    uuid.UUID       `db:"id"`
	TenantID              uuid.UUID       `db:"tenant_id"`
	DocumentType          string          `db:"document_type"`
	DocumentNumber        string          `db:"document_number"`
	SourceLocationID      *uuid.UUID      `db:"source_location_id"`
	DestinationLocationID *uuid.UUID      `db:"destination_location_id"`
	VendorID              *uuid.UUID      `db:"vendor_id"`
	RelatedOrderID        *uuid.UUID      `db:"related_order_id"`
	Status                string          `db:"status"`
	ExpectedAt            *time.Time      `db:"expected_at"`
	CompletedAt           *time.Time      `db:"completed_at"`
	TotalQuantity *string `db:"total_quantity"` // numeric — decimal.Decimal dep needed; using string for
	TotalCost *string `db:"total_cost"` // numeric — decimal.Decimal dep needed; using string for
	PerformedByUserID     *uuid.UUID      `db:"performed_by_user_id"`
	Attributes            json.RawMessage `db:"attributes"`
	CreatedAt             time.Time       `db:"created_at"`
	UpdatedAt             time.Time       `db:"updated_at"`
}

// InventoryLot mirrors i.inventory_lots.
type InventoryLot struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	ItemID           uuid.UUID       `db:"item_id"`
	LotNumber        string          `db:"lot_number"`
	LotType          string          `db:"lot_type"`
	ExpiryDate       *time.Time      `db:"expiry_date"`
	ManufactureDate  *time.Time      `db:"manufacture_date"`
	ReceivedAt       *time.Time      `db:"received_at"`
	VendorID         *uuid.UUID      `db:"vendor_id"`
	SourceDocumentID *uuid.UUID      `db:"source_document_id"`
	Status           string          `db:"status"`
	Attributes       json.RawMessage `db:"attributes"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// InventoryDocumentLine mirrors i.inventory_document_lines.
type InventoryDocumentLine struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	DocumentID       uuid.UUID       `db:"document_id"`
	LineNumber       int32           `db:"line_number"`
	ItemID           uuid.UUID       `db:"item_id"`
	ExpectedQuantity *string `db:"expected_quantity"` // numeric — decimal.Decimal dep needed; using string for
	ActualQuantity *string `db:"actual_quantity"` // numeric — decimal.Decimal dep needed; using string for
	VarianceQuantity string `db:"variance_quantity"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	VarianceReason   *string         `db:"variance_reason"`
	UnitCost *string `db:"unit_cost"` // numeric — decimal.Decimal dep needed; using string for
	LotID            *uuid.UUID      `db:"lot_id"`
	Attributes       json.RawMessage `db:"attributes"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// InventoryPosition mirrors i.inventory_positions.
type InventoryPosition struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           uuid.UUID       `db:"tenant_id"`
	ItemID             uuid.UUID       `db:"item_id"`
	LocationID         uuid.UUID       `db:"location_id"`
	ZoneID             *uuid.UUID      `db:"zone_id"`
	OnHandQuantity string `db:"on_hand_quantity"` // numeric — decimal.Decimal dep needed; using string for
	ReservedQuantity string `db:"reserved_quantity"` // numeric — decimal.Decimal dep needed; using string for
	OnOrderQuantity string `db:"on_order_quantity"` // numeric — decimal.Decimal dep needed; using string for
	InTransitQuantity string `db:"in_transit_quantity"` // numeric — decimal.Decimal dep needed; using string for
	LastMovementAt     *time.Time      `db:"last_movement_at"`
	LastCountAt        *time.Time      `db:"last_count_at"`
	CostBasis *string `db:"cost_basis"` // numeric — decimal.Decimal dep needed; using string for
	Attributes         json.RawMessage `db:"attributes"`
	Status             string          `db:"status"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

// InventoryMovement mirrors i.inventory_movements. Append-only — no UpdatedAt.
type InventoryMovement struct {
	ID                    uuid.UUID       `db:"id"`
	TenantID              uuid.UUID       `db:"tenant_id"`
	ItemID                uuid.UUID       `db:"item_id"`
	LocationID            uuid.UUID       `db:"location_id"`
	ZoneID                *uuid.UUID      `db:"zone_id"`
	LotID                 *uuid.UUID      `db:"lot_id"`
	MovementType          string          `db:"movement_type"`
	QuantityDelta string `db:"quantity_delta"` // numeric — decimal.Decimal dep needed; using string for
	MovementAt            time.Time       `db:"movement_at"`
	SourceDocumentID      *uuid.UUID      `db:"source_document_id"`
	SourceDocumentLineID  *uuid.UUID      `db:"source_document_line_id"`
	SourceTransactionID   *uuid.UUID      `db:"source_transaction_id"`
	ReasonCode            *string         `db:"reason_code"`
	Reference             *string         `db:"reference"`
	PerformedByUserID     *uuid.UUID      `db:"performed_by_user_id"`
	PerformedByEmployeeID *uuid.UUID      `db:"performed_by_employee_id"`
	CostBasis *string `db:"cost_basis"` // numeric — decimal.Decimal dep needed; using string for
	Attributes            json.RawMessage `db:"attributes"`
	CreatedAt             time.Time       `db:"created_at"`
}
