// internal/mcp/analytics.go
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/analytics"
	"github.com/growdirect-llc/rapidpos/internal/identity"
)

const dateLayout = "2006-01-02"

func parseDateRange(tenantID uuid.UUID, from, to string, locationID *string, limit int) analytics.DateRangeFilter {
	now := time.Now().UTC()
	f, err := time.ParseInLocation(dateLayout, from, time.UTC)
	if err != nil {
		f = now.AddDate(0, 0, -30)
	}
	t, err2 := time.ParseInLocation(dateLayout, to, time.UTC)
	if err2 != nil {
		t = now
	}
	flt := analytics.DateRangeFilter{TenantID: tenantID, From: f, To: t, Limit: limit}
	if locationID != nil {
		if id, err3 := uuid.Parse(*locationID); err3 == nil {
			flt.LocationID = &id
		}
	}
	return flt
}

var dateSchema = json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","format":"date"},"to":{"type":"string","format":"date"},"location_id":{"type":"string","format":"uuid"},"limit":{"type":"integer"}}}`)

type dateArgs struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	LocationID *string `json:"location_id"`
	Limit      int     `json:"limit"`
}

// RegisterAnalyticsTools registers 5 analytics tools with the registry.
func RegisterAnalyticsTools(reg *Registry, s *analytics.Store) {
	reg.Register(ToolDef{Name: "canary.analytics.sales_summary", Description: "Aggregate revenue, transaction count, avg ticket, and return stats for a date range.", InputSchema: dateSchema},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			claims, ok := identity.ClaimsFromContext(ctx)
			if !ok {
				return nil, errUnauth
			}
			var p dateArgs
			_ = json.Unmarshal(args, &p)
			return s.SalesSummary(ctx, parseDateRange(claims.TenantID, p.From, p.To, p.LocationID, p.Limit))
		})

	reg.Register(ToolDef{Name: "canary.analytics.basket_metrics", Description: "Per-ticket composition (avg items, avg value) and tender-type distribution.", InputSchema: dateSchema},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			claims, ok := identity.ClaimsFromContext(ctx)
			if !ok {
				return nil, errUnauth
			}
			var p dateArgs
			_ = json.Unmarshal(args, &p)
			return s.BasketMetrics(ctx, parseDateRange(claims.TenantID, p.From, p.To, p.LocationID, p.Limit))
		})

	reg.Register(ToolDef{Name: "canary.analytics.cohort", Description: "Monthly new vs returning customer cohort rows for a date range.", InputSchema: dateSchema},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			claims, ok := identity.ClaimsFromContext(ctx)
			if !ok {
				return nil, errUnauth
			}
			var p dateArgs
			_ = json.Unmarshal(args, &p)
			return s.CohortRows(ctx, parseDateRange(claims.TenantID, p.From, p.To, p.LocationID, p.Limit))
		})

	reg.Register(ToolDef{Name: "canary.analytics.velocity", Description: "Top items by quantity sold in the date range.", InputSchema: dateSchema},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			claims, ok := identity.ClaimsFromContext(ctx)
			if !ok {
				return nil, errUnauth
			}
			var p dateArgs
			_ = json.Unmarshal(args, &p)
			return s.VelocityItems(ctx, parseDateRange(claims.TenantID, p.From, p.To, p.LocationID, p.Limit))
		})

	reg.Register(ToolDef{Name: "canary.analytics.shrink", Description: "Return count/revenue, void count/revenue, unknown-scan count, and shrink rate for a date range.", InputSchema: dateSchema},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			claims, ok := identity.ClaimsFromContext(ctx)
			if !ok {
				return nil, errUnauth
			}
			var p dateArgs
			_ = json.Unmarshal(args, &p)
			return s.ShrinkSummary(ctx, parseDateRange(claims.TenantID, p.From, p.To, p.LocationID, p.Limit))
		})
}
