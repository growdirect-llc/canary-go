// internal/mcp/customer.go
package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/customer"
	"github.com/ruptiv/canary/internal/identity"
)

// RegisterCustomerTools registers 3 customer tools with the registry.
func RegisterCustomerTools(reg *Registry, s *customer.Store) {
	reg.Register(ToolDef{
		Name:        "canary.customer.list",
		Description: "List customers for the tenant. Filters: search (name/email fragment), status, customer_type, limit, offset.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string"},"status":{"type":"string"},"customer_type":{"type":"string"},"limit":{"type":"integer"},"offset":{"type":"integer"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct {
			Search       string `json:"search"`
			Status       string `json:"status"`
			CustomerType string `json:"customer_type"`
			Limit        int    `json:"limit"`
			Offset       int    `json:"offset"`
		}
		_ = json.Unmarshal(args, &p)
		return s.List(ctx, customer.ListFilters{
			TenantID:     claims.TenantID,
			Search:       p.Search,
			Status:       p.Status,
			CustomerType: p.CustomerType,
			Limit:        p.Limit,
			Offset:       p.Offset,
		})
	})

	reg.Register(ToolDef{
		Name:        "canary.customer.get",
		Description: "Get a single customer record by ID.",
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
		Name:        "canary.customer.memberships",
		Description: "Get all loyalty program memberships for a customer.",
		InputSchema: json.RawMessage(`{"type":"object","required":["customer_id"],"properties":{"customer_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct{ CustomerID string `json:"customer_id"` }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(p.CustomerID)
		if err != nil {
			return nil, err
		}
		return s.GetMemberships(ctx, claims.TenantID, id)
	})
}
