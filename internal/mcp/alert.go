// internal/mcp/alert.go
package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/alert"
	"github.com/ruptiv/canary/internal/identity"
)

// RegisterAlertTools registers 6 alert tools with the registry.
func RegisterAlertTools(reg *Registry, s *alert.Store) {
	reg.Register(ToolDef{
		Name:        "canary.alert.list",
		Description: "List loss-prevention alerts for the authenticated tenant. Supports filters: severity (low|medium|high|critical), status (new|acknowledged|dismissed), rule_type, location_id, limit (max 200), offset.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"severity":{"type":"string","enum":["low","medium","high","critical"]},
				"status":{"type":"string","enum":["new","acknowledged","dismissed","duplicate"]},
				"rule_type":{"type":"string"},
				"location_id":{"type":"string","format":"uuid"},
				"limit":{"type":"integer","minimum":1,"maximum":200},
				"offset":{"type":"integer","minimum":0}
			}
		}`),
		RequiredScope: identity.ScopeAlertRead,
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			Severity   string  `json:"severity"`
			Status     string  `json:"status"`
			RuleType   string  `json:"rule_type"`
			LocationID *string `json:"location_id"`
			Limit      int     `json:"limit"`
			Offset     int     `json:"offset"`
		}
		_ = json.Unmarshal(args, &p)
		f := alert.ListFilters{
			TenantID: claims.TenantID,
			Severity: p.Severity,
			Status:   p.Status,
			RuleType: p.RuleType,
			Limit:    p.Limit,
			Offset:   p.Offset,
		}
		if p.LocationID != nil {
			id, err := uuid.Parse(*p.LocationID)
			if err == nil {
				f.LocationID = &id
			}
		}
		return s.List(ctx, f)
	})

	reg.Register(ToolDef{
		Name:          "canary.alert.get",
		Description:   "Get a single alert by ID.",
		InputSchema:   json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string","format":"uuid"}}}`),
		RequiredScope: identity.ScopeAlertRead,
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
		Name:          "canary.alert.stats",
		Description:   "Return detection counts grouped by rule_category, severity, and status for the tenant.",
		InputSchema:   json.RawMessage(`{"type":"object","properties":{}}`),
		RequiredScope: identity.ScopeAlertRead,
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		return s.Stats(ctx, claims.TenantID)
	})

	reg.Register(ToolDef{
		Name:          "canary.alert.acknowledge",
		Description:   "Acknowledge an alert (status new → acknowledged).",
		InputSchema:   json.RawMessage(`{"type":"object","required":["id","acknowledged_by"],"properties":{"id":{"type":"string","format":"uuid"},"acknowledged_by":{"type":"string","format":"uuid"}}}`),
		RequiredScope: identity.ScopeAlertWrite,
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			ID             string `json:"id"`
			AcknowledgedBy string `json:"acknowledged_by"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(p.ID)
		by, _ := uuid.Parse(p.AcknowledgedBy)
		return s.Acknowledge(ctx, claims.TenantID, id, by)
	})

	reg.Register(ToolDef{
		Name:          "canary.alert.resolve",
		Description:   "Resolve/dismiss an alert with a disposition label (dismissed|false_positive|escalated).",
		InputSchema:   json.RawMessage(`{"type":"object","required":["id","disposition"],"properties":{"id":{"type":"string","format":"uuid"},"disposition":{"type":"string","enum":["dismissed","false_positive","escalated"]},"note":{"type":"string"}}}`),
		RequiredScope: identity.ScopeAlertWrite,
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			ID          string `json:"id"`
			Disposition string `json:"disposition"`
			Note        string `json:"note"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(p.ID)
		if p.Disposition == "" {
			p.Disposition = "dismissed"
		}
		return s.Resolve(ctx, claims.TenantID, id, alert.ResolveRequest{Disposition: p.Disposition, Note: p.Note})
	})

	reg.Register(ToolDef{
		Name:          "canary.alert.suppress",
		Description:   "Suppress an alert. duration_minutes=0 means indefinite.",
		InputSchema:   json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string","format":"uuid"},"duration_minutes":{"type":"integer","minimum":0},"reason":{"type":"string"}}}`),
		RequiredScope: identity.ScopeAlertWrite,
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			ID              string `json:"id"`
			DurationMinutes int    `json:"duration_minutes"`
			Reason          string `json:"reason"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(p.ID)
		return s.Suppress(ctx, claims.TenantID, id, alert.SuppressRequest{DurationMinutes: p.DurationMinutes, Reason: p.Reason})
	})
}
