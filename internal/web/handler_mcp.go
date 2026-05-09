package web

import (
	"net/http"
	"strings"
)

// mcpToolsPage renders the catalog of registered MCP tools by reading
// the in-process Registry. Tools are grouped by module via a name-prefix
// convention (e.g. "canary.alert.list" → module "alert"). Wired W12 /
// GRO-831. Usage log + playground are follow-on.
func (h *Handler) mcpToolsPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.MCPRegistry == nil {
		h.render(w, r, "mcp_tools", "settings", map[string]any{
			"Tools": nil, "Count": 0,
		})
		return
	}
	defs := h.deps.MCPRegistry.List()
	rows := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		rows = append(rows, map[string]any{
			"Name":        d.Name,
			"Module":      mcpModuleFromName(d.Name),
			"Description": d.Description,
		})
	}
	h.render(w, r, "mcp_tools", "settings", map[string]any{
		"Tools": rows,
		"Count": len(rows),
	})
}

// mcpModuleFromName extracts a display module name from an MCP tool's
// dotted name. e.g. "canary.alert.list" → "alert".
func mcpModuleFromName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "—"
}
