// Package pricing — price-resolve service. Given (tenant, location, line items,
// optional customer, optional as_of), returns the resolved unit price after
// promotions plus tax breakdown. 
//
// This package owns the on-the-wire request/response contract for
// POST /v1/pricing/resolve plus three quick-lookup GET endpoints. The
// runtime resolver lives in resolver.go; promotion eligibility in
// promotions.go; tax computation in tax.go; pgx access in store.go.
//
// Numeric handling: every monetary value crosses the wire as a JSON string
// in major-units form ("12.34") to dodge float-64 imprecision. Internally
// the resolver works in int64 cents — see resolver.go for conversion rules
// and rounding decisions.
package pricing

import (
	"time"

	"github.com/google/uuid"
)

// ResolveRequest is the body of POST /v1/pricing/resolve.
//
// SDD-vague: the canonical model talks about "merchant_id" interchangeably
// with "tenant_id" in places. Pricing tables key on tenant_id, so the API
// uses tenant_id to match the storage row. Dispatch said merchant_id; we
// expose tenant_id since it's load-bearing in every WHERE clause and the
// gateway header (X-Canary-Merchant) already maps merchant→tenant upstream.
type ResolveRequest struct {
	TenantID   uuid.UUID  `json:"tenant_id"`
	LocationID uuid.UUID  `json:"location_id"`
	CustomerID *uuid.UUID `json:"customer_id,omitempty"`
	// AsOf — nil means "right now". Used for back-dated quotes and
	// what-if pricing. RFC3339 on the wire.
	AsOf *time.Time `json:"as_of,omitempty"`
	// Channel — defaults to "all" / "brick" if omitted.
	Channel string         `json:"channel,omitempty"`
	Lines   []RequestLine  `json:"lines"`
	// CustomerSegments — optional override (loyalty tier list). When
	// CustomerID is set the resolver could fetch segments from c.customers
	// but Wave 2 keeps it explicit-pass to avoid a customer-module dep.
	CustomerSegments []string `json:"customer_segments,omitempty"`
}

// RequestLine is one cart line — item + quantity.
type RequestLine struct {
	ItemID   uuid.UUID `json:"item_id"`
	Quantity string    `json:"quantity"` // decimal-as-string (e.g., "2", "0.5")
}

// ResolveResponse is the success body of POST /v1/pricing/resolve.
type ResolveResponse struct {
	Lines      []ResponseLine `json:"lines"`
	CartTotal  string         `json:"cart_total"`
	Currency   string         `json:"currency"`
	ResolvedAt time.Time      `json:"resolved_at"`
}

// ResponseLine is one resolved cart line with the full breakdown.
type ResponseLine struct {
	ItemID                uuid.UUID          `json:"item_id"`
	Quantity              string             `json:"quantity"`
	BasePrice             string             `json:"base_price"`               // unit, before promo
	AppliedPromotions     []AppliedPromotion `json:"applied_promotions"`       // [] if none
	UnitPriceAfterDiscount string            `json:"unit_price_after_discount"`
	LineSubtotal          string             `json:"line_subtotal"`            // qty * unit_after_discount
	TaxLines              []TaxLine          `json:"tax_lines"`                // [] if exempt / no rate
	LineTotal             string             `json:"line_total"`               // subtotal + sum(tax)
}

// AppliedPromotion describes one promo that touched this line.
type AppliedPromotion struct {
	PromotionID    uuid.UUID `json:"promotion_id"`
	PromotionCode  string    `json:"promotion_code"`
	Name           string    `json:"name"`
	BenefitType    string    `json:"benefit_type"`    // amount_off | percent_off | fixed_price
	DiscountAmount string    `json:"discount_amount"` // per-unit discount in major units
	Stackable      bool      `json:"stackable"`
}

// TaxLine is one tax breakdown row applied to a cart line.
type TaxLine struct {
	TaxClassID   uuid.UUID `json:"tax_class_id"`
	TaxClassCode string    `json:"tax_class_code"`
	Jurisdiction *string   `json:"jurisdiction,omitempty"`
	Rate         string    `json:"rate"`         // e.g., "0.0825"
	TaxAmount    string    `json:"tax_amount"`   // per-line, not per-unit
}

// BasePriceResponse — body of GET /v1/pricing/items/{item_id}/base.
type BasePriceResponse struct {
	ItemID         uuid.UUID  `json:"item_id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	LocationID     *uuid.UUID `json:"location_id,omitempty"`
	Channel        string     `json:"channel"`
	PriceType      string     `json:"price_type"`
	Amount         string     `json:"amount"`
	Currency       string     `json:"currency"`
	UOM            string     `json:"uom"`
	EffectiveStart time.Time  `json:"effective_start"`
	EffectiveEnd   *time.Time `json:"effective_end,omitempty"`
}

// PromotionsListResponse — body of GET /v1/pricing/promotions.
type PromotionsListResponse struct {
	Promotions []PromotionSummary `json:"promotions"`
}

// PromotionSummary is the lite view of a promo for listing.
type PromotionSummary struct {
	ID             uuid.UUID `json:"id"`
	PromotionCode  string    `json:"promotion_code"`
	Name           string    `json:"name"`
	PromotionType  string    `json:"promotion_type"`
	ScopeType      string    `json:"scope_type"`
	EffectiveStart time.Time `json:"effective_start"`
	EffectiveEnd   *time.Time `json:"effective_end,omitempty"`
	Stackable      bool      `json:"stackable"`
	Status         string    `json:"status"`
}

// TaxRatesListResponse — body of GET /v1/pricing/tax-rates.
type TaxRatesListResponse struct {
	TaxRates []TaxRateSummary `json:"tax_rates"`
}

// TaxRateSummary is the lite view of a tax rate.
type TaxRateSummary struct {
	ID             uuid.UUID  `json:"id"`
	TaxClassID     uuid.UUID  `json:"tax_class_id"`
	TaxClassCode   string     `json:"tax_class_code"`
	LocationID     *uuid.UUID `json:"location_id,omitempty"`
	Jurisdiction   *string    `json:"jurisdiction,omitempty"`
	RateType       string     `json:"rate_type"`
	Rate           string     `json:"rate"`
	EffectiveStart time.Time  `json:"effective_start"`
	EffectiveEnd   *time.Time `json:"effective_end,omitempty"`
}
