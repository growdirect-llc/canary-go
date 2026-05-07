// Code generated from deploy/schema/07_p_f_pricing_finance.sql for Loop 2 (GRO-761).
// Wave 1 hand-written types — sqlc retrofit is Loop 3.
// Edit the SQL files in deploy/schema/, regenerate this file by hand.
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Promotion mirrors p.promotions.
type Promotion struct {
	ID                 uuid.UUID       `db:"id"`
	TenantID           uuid.UUID       `db:"tenant_id"`
	PromotionCode      string          `db:"promotion_code"`
	Name               string          `db:"name"`
	Description        *string         `db:"description"`
	PromotionType      string          `db:"promotion_type"`
	ScopeType          string          `db:"scope_type"`
	EffectiveStart     time.Time       `db:"effective_start"`
	EffectiveEnd       *time.Time      `db:"effective_end"`
	ActiveDays         []int32         `db:"active_days"`
	ActiveHours        json.RawMessage `db:"active_hours"`
	ActiveLocations    []uuid.UUID     `db:"active_locations"`
	ActiveChannels     []string        `db:"active_channels"`
	CustomerSegments   []string        `db:"customer_segments"`
	Stackable          bool            `db:"stackable"`
	ExclusiveWith      []uuid.UUID     `db:"exclusive_with"`
	MaxUsesTotal       *int32          `db:"max_uses_total"`
	MaxUsesPerCustomer *int32          `db:"max_uses_per_customer"`
	CurrentUses        int32           `db:"current_uses"`
	Attributes         json.RawMessage `db:"attributes"`
	Status             string          `db:"status"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

// ItemPrice mirrors p.item_prices.
type ItemPrice struct {
	ID                uuid.UUID       `db:"id"`
	TenantID          uuid.UUID       `db:"tenant_id"`
	ItemID            uuid.UUID       `db:"item_id"`
	LocationID        *uuid.UUID      `db:"location_id"`
	ZoneID            *uuid.UUID      `db:"zone_id"`
	Channel           *string         `db:"channel"`
	PriceType         string          `db:"price_type"`
	Amount            string          `db:"amount"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	Currency          string          `db:"currency"`
	UOM               string          `db:"uom"`
	EffectiveStart    time.Time       `db:"effective_start"`
	EffectiveEnd      *time.Time      `db:"effective_end"`
	SourcePromotionID *uuid.UUID      `db:"source_promotion_id"`
	Attributes        json.RawMessage `db:"attributes"`
	Status            string          `db:"status"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

// PromotionRule mirrors p.promotion_rules.
type PromotionRule struct {
	ID               uuid.UUID       `db:"id"`
	TenantID         uuid.UUID       `db:"tenant_id"`
	PromotionID      uuid.UUID       `db:"promotion_id"`
	RuleOrder        int32           `db:"rule_order"`
	TriggerType      string          `db:"trigger_type"`
	TriggerQualifier json.RawMessage `db:"trigger_qualifier"`
	BenefitType      string          `db:"benefit_type"`
	BenefitQualifier json.RawMessage `db:"benefit_qualifier"`
	CreatedAt        time.Time       `db:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at"`
}

// TaxClass mirrors p.tax_classes.
type TaxClass struct {
	ID          uuid.UUID       `db:"id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	Code        string          `db:"code"`
	Name        string          `db:"name"`
	Description *string         `db:"description"`
	IsDefault   bool            `db:"is_default"`
	Attributes  json.RawMessage `db:"attributes"`
	Status      string          `db:"status"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

// TaxRate mirrors p.tax_rates.
type TaxRate struct {
	ID             uuid.UUID       `db:"id"`
	TenantID       uuid.UUID       `db:"tenant_id"`
	TaxClassID     uuid.UUID       `db:"tax_class_id"`
	LocationID     *uuid.UUID      `db:"location_id"`
	Jurisdiction   *string         `db:"jurisdiction"`
	RateType       string          `db:"rate_type"`
	Rate           string          `db:"rate"` // numeric — decimal.Decimal dep needed; using string for Loop 2
	EffectiveStart time.Time       `db:"effective_start"`
	EffectiveEnd   *time.Time      `db:"effective_end"`
	Attributes     json.RawMessage `db:"attributes"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}
