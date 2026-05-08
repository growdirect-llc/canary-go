// Code generated from deploy/schema/02_m_items.sql for
// Wave 1 hand-written types — sqlc retrofit is
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Item mirrors m.items.
type Item struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	SKU               string          `db:"sku"`
	Description       string          `db:"description"`
	ShortDescription  *string         `db:"short_description"`
	ItemType          string          `db:"item_type"`
	CategoryID        *uuid.UUID      `db:"category_id"`
	UnitOfMeasure     string          `db:"unit_of_measure"`
	UOMQuantity string `db:"uom_quantity"` // numeric — decimal.Decimal dep needed; using string for
	DefaultPrice *string `db:"default_price"` // numeric — decimal.Decimal dep needed; using string for
	DefaultCost *string `db:"default_cost"` // numeric — decimal.Decimal dep needed; using string for
	DefaultCurrency   string          `db:"default_currency"`
	TaxClass          *string         `db:"tax_class"`
	FoodStampEligible bool            `db:"food_stamp_eligible"`
	AgeRestriction    *int32          `db:"age_restriction"`
	Weighable         bool            `db:"weighable"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// ProductCategory mirrors m.product_categories.
type ProductCategory struct {
	ID         uuid.UUID       `db:"id"`
	TenantID   uuid.UUID       `db:"tenant_id"`
	ParentID   *uuid.UUID      `db:"parent_id"`
	Code       string          `db:"code"`
	Name       string          `db:"name"`
	Level      int32           `db:"level"`
	Path *string `db:"path"` // ltree — string with TODO: ltree-aware type for
	Attributes json.RawMessage `db:"attributes"`
	Status     string          `db:"status"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
}

// Vendor mirrors m.vendors.
type Vendor struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	VendorCode     string          `db:"vendor_code"`
	Name           string          `db:"name"`
	ShortName      *string         `db:"short_name"`
	VendorType     string          `db:"vendor_type"`
	PrimaryContact json.RawMessage `db:"primary_contact"`
	Address        json.RawMessage `db:"address"`
	PaymentTerms   *string         `db:"payment_terms"`
	Currency       *string         `db:"currency"`
	TaxID          *string         `db:"tax_id"`
	Attributes     json.RawMessage `db:"attributes"`
	Status         string          `db:"status"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// ItemVendor mirrors m.item_vendors.
type ItemVendor struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	ItemID            uuid.UUID       `db:"item_id"`
	VendorID          uuid.UUID       `db:"vendor_id"`
	VendorSKU         *string         `db:"vendor_sku"`
	VendorDescription *string         `db:"vendor_description"`
	UnitCost *string `db:"unit_cost"` // numeric — decimal.Decimal dep needed; using string for
	CasePackQty       *int32          `db:"case_pack_qty"`
	MinOrderQty       *int32          `db:"min_order_qty"`
	LeadTimeDays      *int32          `db:"lead_time_days"`
	IsPrimary         bool            `db:"is_primary"`
	CountryOfOrigin   *string         `db:"country_of_origin"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// ItemBarcode mirrors m.item_barcodes.
type ItemBarcode struct {
	ID          uuid.UUID       `db:"id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	ItemID      uuid.UUID       `db:"item_id"`
	Barcode     string          `db:"barcode"`
	BarcodeType string          `db:"barcode_type"`
	UOMQuantity string `db:"uom_quantity"` // numeric — decimal.Decimal dep needed; using string for
	IsPrimary   bool            `db:"is_primary"`
	Attributes  json.RawMessage `db:"attributes"`
	Status      string          `db:"status"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

// ItemPack mirrors m.item_packs.
type ItemPack struct {
	ID              uuid.UUID       `db:"id"`
	TenantID        uuid.UUID       `db:"tenant_id"`
	PackItemID      uuid.UUID       `db:"pack_item_id"`
	ComponentItemID uuid.UUID       `db:"component_item_id"`
	Quantity string `db:"quantity"` // numeric — decimal.Decimal dep needed; using string for
	PackType        string          `db:"pack_type"`
	Attributes      json.RawMessage `db:"attributes"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}
