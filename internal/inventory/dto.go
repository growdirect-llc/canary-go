// Package inventory implements the IaaS Position Read + Movement Append
// service. It is the runtime over the canonical i.inventory_positions and
// i.inventory_movements tables (see deploy/schema/05_i_inventory.sql).
//
// Scope:
// - Position read: GET /v1/inventory/positions/{item_id}/{location_id}
// and the list endpoint.
// - Movement append: POST /v1/inventory/movements — append-only, never
// UPDATE/DELETE. The position row is maintained inside the same tx as
// a cached running balance.
// - Movement list: audit trail read for an (item, location) over a time
// window.
//
// Out of scope: reservations, BOPIS holds, fulfillment routing, MCP tool
// surface, Valkey hot-path caching. Those land in later loops; the SDD
// `docs/sdds/go-handoff/inventory-as-a-service.md` carries the full vision.
//
// SDD-conflict (canonical schema vs IaaS SDD):
// - SDD speaks of `merchants(id)`; canonical schema uses `app.tenants(id)`
// as the multi-tenant scope. Public DTOs accept "merchant_id" for
// ergonomics; the store treats it as `tenant_id` against the schema.
// - SDD has reasons {received, sold_instore, ...}; canonical schema uses
// {goods_receipt, sale, return, transfer_in, transfer_out, rtv,
// adjustment, write_off, cycle_count_correction, reservation,
// release_reservation}. We follow the canonical schema enum.
package inventory

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MovementTypes is the set of valid movement_type values per the
// canonical schema (deploy/schema/05_i_inventory.sql line 124). The
// dispatch listed a slightly different set ({sale, return, receive,
// transfer_out, transfer_in, adjust, cycle_count, shrink}); this set
// is the schema-true superset and is what the handler validates against.
//
// SDD-conflict: dispatch enum is informal shorthand. Schema is canonical.
var MovementTypes = map[string]struct{}{
	"goods_receipt":           {},
	"adjustment":              {},
	"transfer_in":             {},
	"transfer_out":            {},
	"rtv":                     {},
	"sale":                    {},
	"return":                  {},
	"write_off":               {},
	"cycle_count_correction":  {},
	"reservation":             {},
	"release_reservation":     {},
}

// PositionDTO is the wire shape returned by GET /v1/inventory/positions.
// Numeric quantities are serialised as strings (matching pgx numeric →
// string round-trip from internal/db/types/i.go) to preserve precision
// without dragging in shopspring/decimal at the API boundary.
type PositionDTO struct {
	ID                 uuid.UUID       `json:"id"`
	TenantID           uuid.UUID       `json:"tenant_id"`
	ItemID             uuid.UUID       `json:"item_id"`
	LocationID         uuid.UUID       `json:"location_id"`
	ZoneID             *uuid.UUID      `json:"zone_id,omitempty"`
	OnHandQuantity     string          `json:"on_hand_quantity"`
	ReservedQuantity   string          `json:"reserved_quantity"`
	OnOrderQuantity    string          `json:"on_order_quantity"`
	InTransitQuantity  string          `json:"in_transit_quantity"`
	LastMovementAt     *time.Time      `json:"last_movement_at,omitempty"`
	LastCountAt        *time.Time      `json:"last_count_at,omitempty"`
	CostBasis          *string         `json:"cost_basis,omitempty"`
	Attributes         json.RawMessage `json:"attributes,omitempty"`
	Status             string          `json:"status"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// MovementDTO is the wire shape for inventory_movements rows. There is no
// updated_at field — the row is append-only (see schema line 137).
type MovementDTO struct {
	ID                    uuid.UUID       `json:"id"`
	TenantID              uuid.UUID       `json:"tenant_id"`
	ItemID                uuid.UUID       `json:"item_id"`
	LocationID            uuid.UUID       `json:"location_id"`
	ZoneID                *uuid.UUID      `json:"zone_id,omitempty"`
	LotID                 *uuid.UUID      `json:"lot_id,omitempty"`
	MovementType          string          `json:"movement_type"`
	QuantityDelta         string          `json:"quantity_delta"`
	MovementAt            time.Time       `json:"movement_at"`
	SourceDocumentID      *uuid.UUID      `json:"source_document_id,omitempty"`
	SourceDocumentLineID  *uuid.UUID      `json:"source_document_line_id,omitempty"`
	SourceTransactionID   *uuid.UUID      `json:"source_transaction_id,omitempty"`
	ReasonCode            *string         `json:"reason_code,omitempty"`
	Reference             *string         `json:"reference,omitempty"`
	PerformedByUserID     *uuid.UUID      `json:"performed_by_user_id,omitempty"`
	PerformedByEmployeeID *uuid.UUID      `json:"performed_by_employee_id,omitempty"`
	CostBasis             *string         `json:"cost_basis,omitempty"`
	Attributes            json.RawMessage `json:"attributes,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
}

// AppendMovementRequest is the POST body for /v1/inventory/movements.
//
// MerchantID is the public name; internally it maps to tenant_id (see
// SDD-conflict note in package doc). Quantity is signed — positive
// adds to on-hand, negative removes.
type AppendMovementRequest struct {
	MerchantID          uuid.UUID       `json:"merchant_id"`
	ItemID              uuid.UUID       `json:"item_id"`
	LocationID          uuid.UUID       `json:"location_id"`
	ZoneID              *uuid.UUID      `json:"zone_id,omitempty"`
	LotID               *uuid.UUID      `json:"lot_id,omitempty"`
	MovementType        string          `json:"movement_type"`
	Quantity            string          `json:"quantity"`
	ReferenceID         *string         `json:"reference_id,omitempty"`
	ReasonCode          *string         `json:"reason_code,omitempty"`
	PerformedByUserID   *uuid.UUID      `json:"performed_by"`
	SourceDocumentID    *uuid.UUID      `json:"source_document_id,omitempty"`
	SourceTransactionID *uuid.UUID      `json:"source_transaction_id,omitempty"`
	CostBasis           *string         `json:"cost_basis,omitempty"`
	Attributes          json.RawMessage `json:"attributes,omitempty"`
}

// AppendMovementResponse returns both the new movement row and the
// updated position so the caller has the full state in one round trip.
type AppendMovementResponse struct {
	Movement MovementDTO `json:"movement"`
	Position PositionDTO `json:"position"`
}

// PositionListResponse is the paginated list-positions wire shape.
type PositionListResponse struct {
	Items []PositionDTO `json:"items"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}

// MovementListResponse is the paginated list-movements wire shape.
type MovementListResponse struct {
	Items []MovementDTO `json:"items"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}
