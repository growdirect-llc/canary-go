// Package item is the master-data CRUD service for the Item domain
// (schema m): items, product categories, vendors, item-vendor links,
// and item barcodes. The barcode resolve endpoint is the keystone
// POS-scan path — every scan invokes GetByBarcode, so the read path
// is shaped for sub-100ms latency.
//
// Built in GRO-761 Loop 2 Wave 2.
//
// Note on the schema/dispatch terminology mismatch: the dispatch brief
// uses "merchant_id" everywhere, but the canonical schema (deploy/schema/
// 02_m_items.sql) uses tenant_id throughout the m.* domain. We honor the
// schema as source of truth and surface tenant_id on the wire and in
// store calls. Auth middleware (internal/tenant) currently injects a
// "merchant_id" — a future Wave will reconcile that into a tenant
// context. For now we accept either spelling on the query string but
// only emit tenant_id on the way out.
package item

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// ─────────────────────────────────────────────────────────────────────
// Errors
// ─────────────────────────────────────────────────────────────────────

var (
	// ErrNotFound is returned when no row matches the lookup. Surfaces
	// as 404 at the HTTP layer.
	ErrNotFound = errors.New("item: not found")

	// ErrConflict is returned when a unique constraint would be violated
	// (duplicate SKU, duplicate barcode within tenant). Surfaces as 409.
	ErrConflict = errors.New("item: conflict")

	// ErrValidation is returned when request input fails domain
	// validation. Surfaces as 400.
	ErrValidation = errors.New("item: validation failed")
)

// ─────────────────────────────────────────────────────────────────────
// DTOs — wire-level shapes distinct from raw types.Item
// ─────────────────────────────────────────────────────────────────────

// Item is the response DTO for a single item. JSON shape mirrors the
// canonical column names; null SQL columns are pointers so JSON omits
// them when unset.
type Item struct {
	ID                uuid.UUID       `json:"id"`
	TenantID          uuid.UUID       `json:"tenant_id"`
	SKU               string          `json:"sku"`
	Description       string          `json:"description"`
	ShortDescription  *string         `json:"short_description,omitempty"`
	ItemType          string          `json:"item_type"`
	CategoryID        *uuid.UUID      `json:"category_id,omitempty"`
	UnitOfMeasure     string          `json:"unit_of_measure"`
	UOMQuantity       string          `json:"uom_quantity"`
	DefaultPrice      *string         `json:"default_price,omitempty"`
	DefaultCost       *string         `json:"default_cost,omitempty"`
	DefaultCurrency   string          `json:"default_currency"`
	TaxClass          *string         `json:"tax_class,omitempty"`
	FoodStampEligible bool            `json:"food_stamp_eligible"`
	AgeRestriction    *int32          `json:"age_restriction,omitempty"`
	Weighable         bool            `json:"weighable"`
	Attributes        json.RawMessage `json:"attributes"`
	Status            string          `json:"status"`
	Barcodes          []Barcode       `json:"barcodes,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// Barcode is the response DTO for an item barcode.
type Barcode struct {
	ID          uuid.UUID `json:"id"`
	Barcode     string    `json:"barcode"`
	BarcodeType string    `json:"barcode_type"`
	UOMQuantity string    `json:"uom_quantity"`
	IsPrimary   bool      `json:"is_primary"`
	Status      string    `json:"status"`
}

// Category is the response DTO for a product category.
type Category struct {
	ID         uuid.UUID       `json:"id"`
	TenantID   uuid.UUID       `json:"tenant_id"`
	ParentID   *uuid.UUID      `json:"parent_id,omitempty"`
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Level      int32           `json:"level"`
	Path       *string         `json:"path,omitempty"` // ltree as text
	Attributes json.RawMessage `json:"attributes"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// Vendor is the response DTO for a supplier.
type Vendor struct {
	ID             uuid.UUID       `json:"id"`
	TenantID       uuid.UUID       `json:"tenant_id"`
	VendorCode     string          `json:"vendor_code"`
	Name           string          `json:"name"`
	ShortName      *string         `json:"short_name,omitempty"`
	VendorType     string          `json:"vendor_type"`
	PrimaryContact json.RawMessage `json:"primary_contact"`
	Address        json.RawMessage `json:"address"`
	PaymentTerms   *string         `json:"payment_terms,omitempty"`
	Currency       *string         `json:"currency,omitempty"`
	TaxID          *string         `json:"tax_id,omitempty"`
	Attributes     json.RawMessage `json:"attributes"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────────
// Request bodies
// ─────────────────────────────────────────────────────────────────────

// CreateRequest is the POST /v1/items body. Only sku, name, and
// tenant_id are strictly required; everything else has a sensible
// default in the schema.
type CreateRequest struct {
	TenantID          uuid.UUID         `json:"tenant_id"`
	SKU               string            `json:"sku"`
	Description       string            `json:"description"`
	ShortDescription  *string           `json:"short_description,omitempty"`
	ItemType          *string           `json:"item_type,omitempty"`
	CategoryID        *uuid.UUID        `json:"category_id,omitempty"`
	UnitOfMeasure     *string           `json:"unit_of_measure,omitempty"`
	UOMQuantity       *string           `json:"uom_quantity,omitempty"`
	DefaultPrice      *string           `json:"default_price,omitempty"`
	DefaultCost       *string           `json:"default_cost,omitempty"`
	DefaultCurrency   *string           `json:"default_currency,omitempty"`
	TaxClass          *string           `json:"tax_class,omitempty"`
	FoodStampEligible *bool             `json:"food_stamp_eligible,omitempty"`
	AgeRestriction    *int32            `json:"age_restriction,omitempty"`
	Weighable         *bool             `json:"weighable,omitempty"`
	Attributes        json.RawMessage   `json:"attributes,omitempty"`
	Status            *string           `json:"status,omitempty"`
	Barcodes          []BarcodeRequest  `json:"barcodes,omitempty"`
}

// BarcodeRequest is one item-barcode in a CreateRequest.
type BarcodeRequest struct {
	Value       string  `json:"value"`
	Type        *string `json:"type,omitempty"`         // GTIN | UPC_A | EAN_13 | ITF_14 | DATABAR | INTERNAL | PLU
	UOMQuantity *string `json:"uom_quantity,omitempty"` // numeric as string
	IsPrimary   *bool   `json:"is_primary,omitempty"`
}

// PatchRequest is the PATCH /v1/items/{id} body. Every field is a
// pointer so a missing key means "leave alone" (vs. an explicit null
// which would set the column to NULL).
type PatchRequest struct {
	Description       *string         `json:"description,omitempty"`
	ShortDescription  *string         `json:"short_description,omitempty"`
	ItemType          *string         `json:"item_type,omitempty"`
	CategoryID        *uuid.UUID      `json:"category_id,omitempty"`
	UnitOfMeasure     *string         `json:"unit_of_measure,omitempty"`
	UOMQuantity       *string         `json:"uom_quantity,omitempty"`
	DefaultPrice      *string         `json:"default_price,omitempty"`
	DefaultCost       *string         `json:"default_cost,omitempty"`
	DefaultCurrency   *string         `json:"default_currency,omitempty"`
	TaxClass          *string         `json:"tax_class,omitempty"`
	FoodStampEligible *bool           `json:"food_stamp_eligible,omitempty"`
	AgeRestriction    *int32          `json:"age_restriction,omitempty"`
	Weighable         *bool           `json:"weighable,omitempty"`
	Attributes        json.RawMessage `json:"attributes,omitempty"`
	Status            *string         `json:"status,omitempty"`
}

// ListFilters is the parsed query string for GET /v1/items?...
type ListFilters struct {
	TenantID   uuid.UUID
	CategoryID *uuid.UUID
	VendorID   *uuid.UUID
	Status     *string
	Limit      int
	Offset     int
}

// ─────────────────────────────────────────────────────────────────────
// Validation
// ─────────────────────────────────────────────────────────────────────

// Validate enforces the minimum field set for POST /v1/items.
//
// Rule choices:
//   - tenant_id required (multi-tenant boundary)
//   - sku required, non-empty (UNIQUE (tenant_id, sku) in schema)
//   - description required (NOT NULL in schema)
//   - barcodes optional but, if present, each must have a non-empty
//     value
//
// We deliberately do NOT validate item_type, unit_of_measure, status, or
// barcode_type values here — the schema has free-text columns with
// CHECK comments rather than CHECK constraints, and we want to surface
// the merchant's source-system code verbatim. The store will let
// Postgres reject bad UUIDs and the like.
func (r CreateRequest) Validate() error {
	if r.TenantID == uuid.Nil {
		return wrap("tenant_id is required")
	}
	if r.SKU == "" {
		return wrap("sku is required")
	}
	if r.Description == "" {
		return wrap("description is required")
	}
	for i, b := range r.Barcodes {
		if b.Value == "" {
			return wrap("barcodes[" + itoa(i) + "].value is required")
		}
	}
	return nil
}

// itoa is a tiny strconv-free Itoa for the validation error path. We
// don't want to drag a strconv import for a dozen-byte error string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func wrap(msg string) error {
	return &validationError{msg: msg}
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func (e *validationError) Unwrap() error { return ErrValidation }

// ─────────────────────────────────────────────────────────────────────
// Conversions — types.* (db row) → DTO
// ─────────────────────────────────────────────────────────────────────

// fromTypesItem converts a Wave-1 raw row type into the JSON DTO. The
// barcode slice is supplied separately so the store can optionally
// hydrate it (lookups by ID/SKU/barcode include barcodes; list does
// not, to keep the response payload small).
func fromTypesItem(row types.Item, barcodes []types.ItemBarcode) Item {
	out := Item{
		ID:                row.ID,
		TenantID:          row.TenantID,
		SKU:               row.SKU,
		Description:       row.Description,
		ShortDescription:  row.ShortDescription,
		ItemType:          row.ItemType,
		CategoryID:        row.CategoryID,
		UnitOfMeasure:     row.UnitOfMeasure,
		UOMQuantity:       row.UOMQuantity,
		DefaultPrice:      row.DefaultPrice,
		DefaultCost:       row.DefaultCost,
		DefaultCurrency:   row.DefaultCurrency,
		TaxClass:          row.TaxClass,
		FoodStampEligible: row.FoodStampEligible,
		AgeRestriction:    row.AgeRestriction,
		Weighable:         row.Weighable,
		Attributes:        row.Attributes,
		Status:            row.Status,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
	// Default attributes to {} for callers — DBs may yield nil RawMessage.
	if len(out.Attributes) == 0 {
		out.Attributes = json.RawMessage(`{}`)
	}
	if len(barcodes) > 0 {
		out.Barcodes = make([]Barcode, len(barcodes))
		for i, b := range barcodes {
			out.Barcodes[i] = Barcode{
				ID:          b.ID,
				Barcode:     b.Barcode,
				BarcodeType: b.BarcodeType,
				UOMQuantity: b.UOMQuantity,
				IsPrimary:   b.IsPrimary,
				Status:      b.Status,
			}
		}
	}
	return out
}

func fromTypesCategory(row types.ProductCategory) Category {
	out := Category{
		ID:         row.ID,
		TenantID:   row.TenantID,
		ParentID:   row.ParentID,
		Code:       row.Code,
		Name:       row.Name,
		Level:      row.Level,
		Path:       row.Path,
		Attributes: row.Attributes,
		Status:     row.Status,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
	if len(out.Attributes) == 0 {
		out.Attributes = json.RawMessage(`{}`)
	}
	return out
}

func fromTypesVendor(row types.Vendor) Vendor {
	out := Vendor{
		ID:             row.ID,
		TenantID:       row.TenantID,
		VendorCode:     row.VendorCode,
		Name:           row.Name,
		ShortName:      row.ShortName,
		VendorType:     row.VendorType,
		PrimaryContact: row.PrimaryContact,
		Address:        row.Address,
		PaymentTerms:   row.PaymentTerms,
		Currency:       row.Currency,
		TaxID:          row.TaxID,
		Attributes:     row.Attributes,
		Status:         row.Status,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if len(out.PrimaryContact) == 0 {
		out.PrimaryContact = json.RawMessage(`{}`)
	}
	if len(out.Address) == 0 {
		out.Address = json.RawMessage(`{}`)
	}
	if len(out.Attributes) == 0 {
		out.Attributes = json.RawMessage(`{}`)
	}
	return out
}
