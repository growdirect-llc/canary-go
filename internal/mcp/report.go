// internal/mcp/report.go
package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/report"
)

// RegisterReportTools registers 3 report tools with the registry.
func RegisterReportTools(reg *Registry, s report.Storer) {
	reg.Register(ToolDef{
		Name:        "canary.report.create",
		Description: "Enqueue a report generation job. report_type: sales_summary | return_detail | shrink. format: csv | xlsx | json.",
		InputSchema: json.RawMessage(`{"type":"object","required":["report_type","from","to"],"properties":{"report_type":{"type":"string","enum":["sales_summary","return_detail","shrink"]},"from":{"type":"string","format":"date"},"to":{"type":"string","format":"date"},"location_id":{"type":"string","format":"uuid"},"format":{"type":"string","enum":["csv","xlsx","json"]}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p report.ReportRequest
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		return s.Create(ctx, claims.TenantID, p)
	})

	reg.Register(ToolDef{
		Name:        "canary.report.get",
		Description: "Get a report job by ID (poll for status and download_url).",
		InputSchema: json.RawMessage(`{"type":"object","required":["job_id"],"properties":{"job_id":{"type":"string","format":"uuid"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		var p struct{ JobID string `json:"job_id"` }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(p.JobID)
		if err != nil {
			return nil, err
		}
		return s.GetByID(ctx, claims.TenantID, id)
	})

	reg.Register(ToolDef{
		Name:        "canary.report.list",
		Description: "List all report jobs for the tenant, newest first.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		claims, ok := identity.ClaimsFromContext(ctx)
		if !ok {
			return nil, errUnauth
		}
		return s.List(ctx, claims.TenantID)
	})
}
