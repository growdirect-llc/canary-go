// Code generated from deploy/schema/06_o_orders.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// PurchaseOrder mirrors o.purchase_orders.
type PurchaseOrder struct {
	ID                    uuid.UUID       `db:"id"`
	TenantID              uuid.UUID       `db:"tenant_id"`
	PONumber              string          `db:"po_number"`
	VendorID              uuid.UUID       `db:"vendor_id"`
	DestinationLocationID *uuid.UUID      `db:"destination_location_id"`
	OrderMethod           string          `db:"order_method"`
	OrderType             string          `db:"order_type"`
	Status                string          `db:"status"`
	OrderedAt             *time.Time      `db:"ordered_at"`
	ExpectedDeliveryAt    *time.Time      `db:"expected_delivery_at"`
	AcknowledgedAt        *time.Time      `db:"acknowledged_at"`
	CancelledAt           *time.Time      `db:"cancelled_at"`
	TotalQuantity *string `db:"total_quantity"` // numeric — decimal.Decimal dep needed; using string for
	TotalCost *string `db:"total_cost"` // numeric — decimal.Decimal dep needed; using string for
	Currency              string          `db:"currency"`
	PaymentTerms          *string         `db:"payment_terms"`
	ShippingTerms         *string         `db:"shipping_terms"`
	ApprovalUserID        *uuid.UUID      `db:"approval_user_id"`
	ApprovedAt            *time.Time      `db:"approved_at"`
	Attributes            json.RawMessage `db:"attributes"`
	CreatedAt             time.Time       `db:"created_at"`
	UpdatedAt             time.Time       `db:"updated_at"`
}

// PurchaseOrderLine mirrors o.purchase_order_lines.
type PurchaseOrderLine struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           uuid.UUID       `db:"tenant_id"`
	POID               uuid.UUID       `db:"po_id"`
	LineNumber         int32           `db:"line_number"`
	ItemID             uuid.UUID       `db:"item_id"`
	VendorSKU          *string         `db:"vendor_sku"`
	OrderedQuantity string `db:"ordered_quantity"` // numeric — decimal.Decimal dep needed; using string for
	ReceivedQuantity string `db:"received_quantity"` // numeric — decimal.Decimal dep needed; using string for
	CancelledQuantity string `db:"cancelled_quantity"` // numeric — decimal.Decimal dep needed; using string for
	UnitCost string `db:"unit_cost"` // numeric — decimal.Decimal dep needed; using string for
	TotalCost string `db:"total_cost"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	ExpectedDeliveryAt *time.Time      `db:"expected_delivery_at"`
	Status             string          `db:"status"`
	Attributes         json.RawMessage `db:"attributes"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

// SalesOrder mirrors o.sales_orders.
type SalesOrder struct {
	ID                    uuid.UUID       `db:"id"`
	TenantID              uuid.UUID       `db:"tenant_id"`
	OrderNumber           string          `db:"order_number"`
	CustomerID            *uuid.UUID      `db:"customer_id"`
	Channel               string          `db:"channel"`
	OriginLocationID      *uuid.UUID      `db:"origin_location_id"`
	DestinationLocationID *uuid.UUID      `db:"destination_location_id"`
	DestinationAddressID  *uuid.UUID      `db:"destination_address_id"`
	Status                string          `db:"status"`
	OrderedAt             time.Time       `db:"ordered_at"`
	PromisedAt            *time.Time      `db:"promised_at"`
	FulfilledAt           *time.Time      `db:"fulfilled_at"`
	CancelledAt           *time.Time      `db:"cancelled_at"`
	Subtotal *string `db:"subtotal"` // numeric — decimal.Decimal dep needed; using string for
	TaxTotal *string `db:"tax_total"` // numeric — decimal.Decimal dep needed; using string for
	ShippingTotal *string `db:"shipping_total"` // numeric — decimal.Decimal dep needed; using string for
	DiscountTotal *string `db:"discount_total"` // numeric — decimal.Decimal dep needed; using string for
	GrandTotal *string `db:"grand_total"` // numeric — decimal.Decimal dep needed; using string for
	Currency              string          `db:"currency"`
	PaymentStatus         string          `db:"payment_status"`
	Attributes            json.RawMessage `db:"attributes"`
	ExternalIDs           json.RawMessage `db:"external_ids"`
	CreatedAt             time.Time       `db:"created_at"`
	UpdatedAt             time.Time       `db:"updated_at"`
}

// SalesOrderLine mirrors o.sales_order_lines.
type SalesOrderLine struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	SalesOrderID      uuid.UUID       `db:"sales_order_id"`
	LineNumber        int32           `db:"line_number"`
	ItemID            uuid.UUID       `db:"item_id"`
	OrderedQuantity string `db:"ordered_quantity"` // numeric — decimal.Decimal dep needed; using string for
	FulfilledQuantity string `db:"fulfilled_quantity"` // numeric — decimal.Decimal dep needed; using string for
	CancelledQuantity string `db:"cancelled_quantity"` // numeric — decimal.Decimal dep needed; using string for
	RefundedQuantity string `db:"refunded_quantity"` // numeric — decimal.Decimal dep needed; using string for
	UnitPrice string `db:"unit_price"` // numeric — decimal.Decimal dep needed; using string for
	UnitDiscount string `db:"unit_discount"` // numeric — decimal.Decimal dep needed; using string for
	UnitTax string `db:"unit_tax"` // numeric — decimal.Decimal dep needed; using string for
	LineTotal string `db:"line_total"` // numeric — decimal.Decimal dep needed; using string for Loop 2 (GENERATED)
	Status            string          `db:"status"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// Fulfillment mirrors o.fulfillments.
type Fulfillment struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	FulfillmentNumber string          `db:"fulfillment_number"`
	SourceLocationID  *uuid.UUID      `db:"source_location_id"`
	FulfillmentMethod string          `db:"fulfillment_method"`
	Status            string          `db:"status"`
	AssignedTo        *uuid.UUID      `db:"assigned_to"`
	PickedAt          *time.Time      `db:"picked_at"`
	PackedAt          *time.Time      `db:"packed_at"`
	ShippedAt         *time.Time      `db:"shipped_at"`
	DeliveredAt       *time.Time      `db:"delivered_at"`
	Carrier           *string         `db:"carrier"`
	TrackingNumber    *string         `db:"tracking_number"`
	TrackingURL       *string         `db:"tracking_url"`
	Attributes        json.RawMessage `db:"attributes"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// FulfillmentLine mirrors o.fulfillment_lines.
type FulfillmentLine struct {
	ID                  uuid.UUID       `db:"id"`
	TenantID            uuid.UUID       `db:"tenant_id"`
	FulfillmentID       uuid.UUID       `db:"fulfillment_id"`
	SalesOrderLineID    uuid.UUID       `db:"sales_order_line_id"`
	ItemID              uuid.UUID       `db:"item_id"`
	Quantity string `db:"quantity"` // numeric — decimal.Decimal dep needed; using string for
	PickedQuantity string `db:"picked_quantity"` // numeric — decimal.Decimal dep needed; using string for
	PackedQuantity string `db:"packed_quantity"` // numeric — decimal.Decimal dep needed; using string for
	ShippedQuantity string `db:"shipped_quantity"` // numeric — decimal.Decimal dep needed; using string for
	LotID               *uuid.UUID      `db:"lot_id"`
	InventoryMovementID *uuid.UUID      `db:"inventory_movement_id"`
	Attributes          json.RawMessage `db:"attributes"`
	CreatedAt           time.Time       `db:"created_at"`
	UpdatedAt           time.Time       `db:"updated_at"`
}

// Allocation mirrors o.allocations.
type Allocation struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	SalesOrderLineID     uuid.UUID       `db:"sales_order_line_id"`
	InventoryPositionID  uuid.UUID       `db:"inventory_position_id"`
	AllocationType       string          `db:"allocation_type"`
	Quantity string `db:"quantity"` // numeric — decimal.Decimal dep needed; using string for
	AllocatedAt          time.Time       `db:"allocated_at"`
	ExpiresAt            *time.Time      `db:"expires_at"`
	ConsumedByMovementID *uuid.UUID      `db:"consumed_by_movement_id"`
	Status               string          `db:"status"`
	Attributes           json.RawMessage `db:"attributes"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}

// ShippingDocument mirrors o.shipping_documents.
type ShippingDocument struct {
	ID                   uuid.UUID       `db:"id"`
	TenantID             uuid.UUID       `db:"tenant_id"`
	DocumentType         string          `db:"document_type"`
	DocumentNumber       string          `db:"document_number"`
	RelatedPOID          *uuid.UUID      `db:"related_po_id"`
	RelatedFulfillmentID *uuid.UUID      `db:"related_fulfillment_id"`
	VendorID             *uuid.UUID      `db:"vendor_id"`
	Carrier              *string         `db:"carrier"`
	TrackingNumber       *string         `db:"tracking_number"`
	ExpectedArrivalAt    *time.Time      `db:"expected_arrival_at"`
	ShippedAt            *time.Time      `db:"shipped_at"`
	TotalQuantity *string `db:"total_quantity"` // numeric — decimal.Decimal dep needed; using string for
	TotalWeight *string `db:"total_weight"` // numeric — decimal.Decimal dep needed; using string for
	TotalVolume *string `db:"total_volume"` // numeric — decimal.Decimal dep needed; using string for
	Attributes           json.RawMessage `db:"attributes"`
	Status               string          `db:"status"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}
