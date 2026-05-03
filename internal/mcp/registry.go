// internal/mcp/registry.go
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ToolFunc executes a tool. args is the raw JSON "arguments" value from
// a tools/call request.
type ToolFunc func(ctx context.Context, args json.RawMessage) (any, error)

// ToolDef is the registration record for one MCP tool.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Registry maps tool names to definitions. Register all tools before
// serving requests — not safe for concurrent mutation.
type Registry struct {
	defs []ToolDef
	idx  map[string]ToolFunc
}

func NewRegistry() *Registry {
	return &Registry{idx: make(map[string]ToolFunc)}
}

func (r *Registry) Register(def ToolDef, fn ToolFunc) {
	r.defs = append(r.defs, def)
	r.idx[def.Name] = fn
}

func (r *Registry) List() []ToolDef { return r.defs }

// Call dispatches to the named tool. Returns a sentinel error whose
// message starts with "unknown tool:" for -32601 mapping.
func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	fn, ok := r.idx[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return fn(ctx, args)
}

// IsUnknownTool returns true for errors produced by Call on missing names.
func IsUnknownTool(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown tool:")
}

// errUnauth is returned by tool handlers when the request context has
// no tenant-scoped API key claims.
var errUnauth = errors.New("mcp: tenant-scoped API key required")
