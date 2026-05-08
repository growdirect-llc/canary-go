// internal/mcp/employee.go
package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/employee"
	"github.com/ruptiv/canary/internal/identity"
)

// RegisterEmployeeTools registers 3 employee tools with the registry.
func RegisterEmployeeTools(reg *Registry, s *employee.Store) {
	reg.Register(ToolDef{
		Name:        "canary.employee.list",
		Description: "List employees (cashier roster) for the tenant. Filters: employment_status (active|terminated), search (name/code/email fragment), limit, offset.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"employment_status":{"type":"string","enum":["active","terminated"]},"search":{"type":"string"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			EmploymentStatus string `json:"employment_status"`
			Search           string `json:"search"`
			Limit            int    `json:"limit"`
			Offset           int    `json:"offset"`
		}
		_ = json.Unmarshal(args, &p)
		return s.List(ctx, employee.ListFilters{
			TenantID:         claims.TenantID,
			EmploymentStatus: p.EmploymentStatus,
			Search:           p.Search,
			Limit:            p.Limit,
			Offset:           p.Offset,
		})
	})

	reg.Register(ToolDef{
		Name:        "canary.employee.get",
		Description: "Get a single employee record by ID.",
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
		Name:        "canary.employee.alert_summaries",
		Description: "Return detection counts grouped by cashier employee, ordered by total alerts DESC.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		return s.AlertSummaries(ctx, claims.TenantID)
	})
}
