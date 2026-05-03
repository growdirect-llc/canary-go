package pricing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// Resolver wires the store to the price-resolve algorithm. It is the
// runtime entry point invoked by the HTTP handler and any future internal
// caller (e.g., transaction service at line-add time).
type Resolver struct {
	Store  Store
	Logger *zap.Logger
	Now    func() time.Time // overridable for tests
}

// NewResolver constructs a Resolver with sensible defaults.
func NewResolver(s Store, l *zap.Logger) *Resolver {
	if l == nil {
		l = zap.NewNop()
	}
	return &Resolver{
		Store:  s,
		Logger: l,
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// Resolve takes a ResolveRequest and returns a fully-resolved
// ResolveResponse. It is the single API into pricing math.
func (r *Resolver) Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResponse, error) {
	if req == nil {
		return nil, errors.New("pricing: nil request")
	}
	if len(req.Lines) == 0 {
		return nil, errors.New("pricing: at least one line required")
	}
	if req.TenantID == uuid.Nil {
		return nil, errors.New("pricing: tenant_id required")
	}
	if req.LocationID == uuid.Nil {
		return nil, errors.New("pricing: location_id required")
	}

	asOf := r.Now()
	if req.AsOf != nil {
		asOf = req.AsOf.UTC()
	}
	channel := req.Channel
	if channel == "" {
		// LOOP2-decision: default to in-store channel — most realistic for
		// a Wave 2 caller (POS at the register).
		channel = "brick"
	}

	// Pull all active promotions once for the (tenant, location, asOf).
	promos, err := r.Store.ListActivePromotions(ctx, req.TenantID, req.LocationID, asOf)
	if err != nil {
		return nil, fmt.Errorf("pricing: list promos: %w", err)
	}

	// Pre-fetch each promo's rules in one pass — avoids N×M queries.
	promoRules := make(map[uuid.UUID][]types.PromotionRule, len(promos))
	for _, p := range promos {
		rules, err := r.Store.ListPromotionRules(ctx, req.TenantID, p.ID)
		if err != nil {
			return nil, fmt.Errorf("pricing: list rules for %s: %w", p.ID, err)
		}
		promoRules[p.ID] = rules
	}

	evalCtx := promoEvalContext{
		channel:          channel,
		customerID:       req.CustomerID,
		customerSegments: req.CustomerSegments,
	}

	out := &ResolveResponse{
		Lines:      make([]ResponseLine, 0, len(req.Lines)),
		ResolvedAt: asOf,
		Currency:   "USD", // first-line currency overrides if different
	}
	var cartCents int64

	for _, line := range req.Lines {
		respLine, err := r.resolveLine(ctx, req, line, promos, promoRules, evalCtx, asOf)
		if err != nil {
			return nil, err
		}
		out.Lines = append(out.Lines, *respLine)

		// Add line total to cart total.
		lineTotalCents, err := parseMajorToCents(respLine.LineTotal)
		if err != nil {
			return nil, fmt.Errorf("pricing: parse line total: %w", err)
		}
		cartCents += lineTotalCents
	}

	out.CartTotal = formatCents(cartCents)
	return out, nil
}

// resolveLine handles one cart line end-to-end: lookup item → lookup
// price → eval promos → compute tax → assemble.
func (r *Resolver) resolveLine(
	ctx context.Context,
	req *ResolveRequest,
	line RequestLine,
	promos []types.Promotion,
	promoRules map[uuid.UUID][]types.PromotionRule,
	evalCtx promoEvalContext,
	asOf time.Time,
) (*ResponseLine, error) {
	if line.ItemID == uuid.Nil {
		return nil, errors.New("pricing: line item_id required")
	}
	qtyMicros, err := parseQuantityToMicros(line.Quantity)
	if err != nil {
		return nil, fmt.Errorf("pricing: parse quantity for item %s: %w", line.ItemID, err)
	}
	if qtyMicros == 0 {
		return nil, fmt.Errorf("pricing: zero quantity for item %s", line.ItemID)
	}

	// 1. Item lookup (need tax_class + category).
	item, err := r.Store.GetItem(ctx, req.TenantID, line.ItemID)
	if err != nil {
		return nil, fmt.Errorf("pricing: get item %s: %w", line.ItemID, err)
	}

	// 2. Base price.
	locPtr := &req.LocationID
	priceRow, err := r.Store.GetItemPrice(ctx, req.TenantID, line.ItemID, locPtr, evalCtx.channel, asOf)
	if err != nil {
		// Fallback: item.default_price catalog row.
		if errors.Is(err, ErrNotFound) {
			if item.DefaultPrice == nil {
				return nil, fmt.Errorf("pricing: no price found for item %s at location %s",
					line.ItemID, req.LocationID)
			}
			defCents, perr := parseMajorToCents(*item.DefaultPrice)
			if perr != nil {
				return nil, fmt.Errorf("pricing: parse default_price: %w", perr)
			}
			return r.assembleLine(ctx, req, item, line, qtyMicros, defCents, asOf, promos, promoRules, evalCtx)
		}
		return nil, fmt.Errorf("pricing: get_item_price: %w", err)
	}
	baseCents, err := parseMajorToCents(priceRow.Amount)
	if err != nil {
		return nil, fmt.Errorf("pricing: parse base price: %w", err)
	}
	return r.assembleLine(ctx, req, item, line, qtyMicros, baseCents, asOf, promos, promoRules, evalCtx)
}

// assembleLine puts together the full ResponseLine after price + promo +
// tax steps run.
func (r *Resolver) assembleLine(
	ctx context.Context,
	req *ResolveRequest,
	item *types.Item,
	line RequestLine,
	qtyMicros int64,
	baseCents int64,
	asOf time.Time,
	promos []types.Promotion,
	promoRules map[uuid.UUID][]types.PromotionRule,
	evalCtx promoEvalContext,
) (*ResponseLine, error) {
	// 3. Promotion evaluation.
	var candidates []evaluatedPromo
	for _, p := range promos {
		rules := promoRules[p.ID]
		applies, discount, benefitType, err := evaluatePromotion(
			p, rules,
			line.ItemID, item.CategoryID,
			baseCents, qtyMicros, evalCtx,
		)
		if err != nil {
			return nil, fmt.Errorf("pricing: evaluate promo %s: %w", p.PromotionCode, err)
		}
		if !applies {
			continue
		}
		candidates = append(candidates, evaluatedPromo{
			promo:         p,
			discountCents: discount,
			benefitType:   benefitType,
		})
	}

	picked := pickBestPromotions(candidates)

	var totalDiscount int64
	applied := make([]AppliedPromotion, 0, len(picked))
	for _, c := range picked {
		applied = append(applied, AppliedPromotion{
			PromotionID:    c.promo.ID,
			PromotionCode:  c.promo.PromotionCode,
			Name:           c.promo.Name,
			BenefitType:    c.benefitType,
			DiscountAmount: formatCents(c.discountCents),
			Stackable:      c.promo.Stackable,
		})
		totalDiscount += c.discountCents
	}
	if totalDiscount > baseCents {
		totalDiscount = baseCents
	}
	unitAfterCents := baseCents - totalDiscount
	subtotalCents := multiplyCentsByMicros(unitAfterCents, qtyMicros)

	// 4. Tax.
	taxes, err := computeTax(ctx, r.Store, req.TenantID, &req.LocationID, item, subtotalCents, asOf)
	if err != nil {
		return nil, fmt.Errorf("pricing: compute tax for item %s: %w", line.ItemID, err)
	}
	taxLines := make([]TaxLine, 0, len(taxes))
	var totalTax int64
	for _, t := range taxes {
		taxLines = append(taxLines, TaxLine{
			TaxClassID:   t.taxClassID,
			TaxClassCode: t.taxClassCode,
			Jurisdiction: t.jurisdiction,
			Rate:         formatRatePpm(t.ratePpm),
			TaxAmount:    formatCents(t.taxCents),
		})
		totalTax += t.taxCents
	}

	totalCents := subtotalCents + totalTax

	return &ResponseLine{
		ItemID:                 line.ItemID,
		Quantity:               formatMicros(qtyMicros),
		BasePrice:              formatCents(baseCents),
		AppliedPromotions:      applied,
		UnitPriceAfterDiscount: formatCents(unitAfterCents),
		LineSubtotal:           formatCents(subtotalCents),
		TaxLines:               taxLines,
		LineTotal:              formatCents(totalCents),
	}, nil
}
