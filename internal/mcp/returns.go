// internal/mcp/returns.go
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/identity"
	"github.com/growdirect-llc/rapidpos/internal/returns"
)

// RegisterReturnsTools registers 4 returns tools with the registry.
func RegisterReturnsTools(reg *Registry, s *returns.Store) {
	reg.Register(ToolDef{
		Name:        "canary.returns.list",
		Description: "List return transactions. Filters: location_id, customer_id, from, to, limit, offset.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"location_id":{"type":"string","format":"uuid"},"customer_id":{"type":"string","format":"uuid"},"from":{"type":"string","format":"date"},"to":{"type":"string","format":"date"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			LocationID *string `json:"location_id"`
			CustomerID *string `json:"customer_id"`
			From       string  `json:"from"`
			To         string  `json:"to"`
			Limit      int     `json:"limit"`
			Offset     int     `json:"offset"`
		}
		_ = json.Unmarshal(args, &p)
		f := returns.ListFilters{TenantID: claims.TenantID, Limit: p.Limit, Offset: p.Offset}
		if p.LocationID != nil {
			if id, err := uuid.Parse(*p.LocationID); err == nil {
				f.LocationID = &id
			}
		}
		if p.CustomerID != nil {
			if id, err := uuid.Parse(*p.CustomerID); err == nil {
				f.CustomerID = &id
			}
		}
		now := time.Now().UTC()
		if p.From != "" {
			if t, err := time.ParseInLocation(dateLayout, p.From, time.UTC); err == nil {
				f.From = t
			}
		} else {
			f.From = now.AddDate(0, 0, -30)
		}
		if p.To != "" {
			if t, err := time.ParseInLocation(dateLayout, p.To, time.UTC); err == nil {
				f.To = t
			}
		} else {
			f.To = now
		}
		return s.List(ctx, f)
	})

	reg.Register(ToolDef{
		Name:        "canary.returns.get",
		Description: "Get a return transaction with its line items.",
		InputSchema: json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(p.ID)
		if err != nil {
			return nil, err
		}
		return s.GetByID(ctx, claims.TenantID, id)
	})

	reg.Register(ToolDef{
		Name:        "canary.returns.summary",
		Description: "Aggregate return statistics (count, revenue, avg amount) for a date range.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","format":"date"},"to":{"type":"string","format":"date"},"location_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			From       string  `json:"from"`
			To         string  `json:"to"`
			LocationID *string `json:"location_id"`
		}
		_ = json.Unmarshal(args, &p)
		now := time.Now().UTC()
		from, err := time.ParseInLocation(dateLayout, p.From, time.UTC)
		if err != nil {
			from = now.AddDate(0, 0, -30)
		}
		to, err2 := time.ParseInLocation(dateLayout, p.To, time.UTC)
		if err2 != nil {
			to = now
		}
		var locID *uuid.UUID
		if p.LocationID != nil {
			if id, err3 := uuid.Parse(*p.LocationID); err3 == nil {
				locID = &id
			}
		}
		return s.Summary(ctx, claims.TenantID, from, to, locID)
	})

	reg.Register(ToolDef{
		Name:        "canary.returns.fraud_flag",
		Description: "Flag a return transaction as suspicious. Creates a q.detections row that drives the alert lifecycle.",
		InputSchema: json.RawMessage(`{"type":"object","required":["transaction_id","detection_rule_id","reason","severity","flagged_by"],"properties":{"transaction_id":{"type":"string","format":"uuid"},"detection_rule_id":{"type":"string","format":"uuid"},"reason":{"type":"string"},"severity":{"type":"string","enum":["low","medium","high","critical"]},"flagged_by":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			TransactionID   string `json:"transaction_id"`
			DetectionRuleID string `json:"detection_rule_id"`
			Reason          string `json:"reason"`
			Severity        string `json:"severity"`
			FlaggedBy       string `json:"flagged_by"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		txID, _ := uuid.Parse(p.TransactionID)
		ruleID, _ := uuid.Parse(p.DetectionRuleID)
		flaggedBy, _ := uuid.Parse(p.FlaggedBy)
		return s.FraudFlag(ctx, claims.TenantID, txID, returns.FraudFlagRequest{
			DetectionRuleID: ruleID, Reason: p.Reason, Severity: p.Severity, FlaggedBy: flaggedBy,
		})
	})
}
