package pricing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
)

// resolvedTax is the per-line tax breakdown returned by computeTax.
type resolvedTax struct {
	taxClassID   uuid.UUID
	taxClassCode string
	jurisdiction *string
	ratePpm      int64
	taxCents     int64
}

// computeTax resolves the item's tax_class → looks up the rate for the
// (location, asOf) → multiplies subtotal × rate.
//
// Returns nil if the item has no tax_class (treated as exempt) OR no
// matching rate exists. The dispatch said "fall back to merchant-level
// tax_rate and document with SDD-vague" — see store.GetTaxRate which
// already falls back from location-specific to tenant-default rate.
//
// SDD-vague: rate_type='flat_amount' and 'tiered' are mentioned in §8 but
// undefined in detail. LOOP2-decision: only 'percentage' is computed in
// Wave 2; flat_amount and tiered return zero with no error.
//
// SDD-vague: schema has no explicit "tax-inclusive vs tax-exclusive"
// flag. LOOP2-decision: assume tax-EXCLUSIVE (line_subtotal does NOT
// include tax; tax_lines add to it). This matches US convention; VAT
// merchants will need a price-inclusive flag — flag for Wave 3.
func computeTax(
	ctx context.Context,
	store Store,
	tenantID uuid.UUID,
	locationID *uuid.UUID,
	item *types.Item,
	lineSubtotalCents int64,
	asOf time.Time,
) ([]resolvedTax, error) {
	if item.TaxClass == nil || *item.TaxClass == "" {
		return nil, nil
	}

	cls, err := store.GetTaxClassByCode(ctx, tenantID, *item.TaxClass)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// SDD-vague: item references an unknown tax class — treat as
			// exempt rather than fail the whole resolve. Surface in logs.
			return nil, nil
		}
		return nil, err
	}

	rate, err := store.GetTaxRate(ctx, tenantID, cls.ID, locationID, asOf)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	if rate.RateType != "percentage" {
		// flat_amount / tiered not implemented — skip silently for Wave 2.
		return nil, nil
	}

	ppm, err := parseRateToPpm(rate.Rate)
	if err != nil {
		return nil, err
	}
	taxCents := multiplyCentsByPpm(lineSubtotalCents, ppm)

	return []resolvedTax{{
		taxClassID:   cls.ID,
		taxClassCode: cls.Code,
		jurisdiction: rate.Jurisdiction,
		ratePpm:      ppm,
		taxCents:     taxCents,
	}}, nil
}
