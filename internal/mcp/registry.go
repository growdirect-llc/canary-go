// internal/mcp/registry.go
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ruptiv/canary/internal/identity"
)

// ToolFunc executes a tool. args is the raw JSON "arguments" value from
// a tools/call request.
type ToolFunc func(ctx context.Context, args json.RawMessage) (any, error)

// ToolDef is the registration record for one MCP tool. RequiredScope,
// if non-empty, names the API-key scope that the caller MUST hold for
// the registry's Call() to dispatch the tool. Tools that legitimately
// need no scope (e.g. canary.test.ping for liveness probes) leave it
// empty; everything else SHOULD set it. GRO-935.
type ToolDef struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	RequiredScope string          `json:"-"`
}

// ErrInsufficientScope is returned by Registry.Call when the caller's
// claims do not include the tool's RequiredScope. Distinct from a
// dispatch failure inside the tool itself so the handler can map it
// to a JSON-RPC error code that surfaces "insufficient_scope" to the
// client.
var ErrInsufficientScope = errors.New("mcp: insufficient scope")

// ErrInvalidParams is returned by tool handlers when the supplied
// arguments don't match the tool's contract: missing required field,
// malformed UUID, malformed date, bad enum value, etc. Mapped to
// JSON-RPC -32602 by the handler. GRO-939.
var ErrInvalidParams = errors.New("mcp: invalid params")

// InvalidParamsf wraps ErrInvalidParams with a contextual message so
// the JSON-RPC error tells clients exactly which field failed:
//
//	id, err := uuid.Parse(p.ID)
//	if err != nil {
//	    return nil, mcp.InvalidParamsf("id: %v", err)
//	}
func InvalidParamsf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidParams}, args...)...)
}

// IsInvalidParams reports whether err was produced by InvalidParamsf
// or any caller wrapping ErrInvalidParams. Mirrors IsUnknownTool's
// role in the handler's error-code mapping.
func IsInvalidParams(err error) bool {
	return errors.Is(err, ErrInvalidParams)
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

// findDef returns the ToolDef for name. Linear scan is fine —
// registrations are O(tens) at startup.
func (r *Registry) findDef(name string) (ToolDef, bool) {
	for i := range r.defs {
		if r.defs[i].Name == name {
			return r.defs[i], true
		}
	}
	return ToolDef{}, false
}

// Call dispatches to the named tool. Returns a sentinel error whose
// message starts with "unknown tool:" for -32601 mapping. Returns
// ErrInsufficientScope when the tool declares a RequiredScope the
// caller's claims do not include — gated BEFORE the tool runs so a
// read-only key cannot land any side-effects of a write tool, even
// if the tool's first action is a tenant filter. GRO-935.
func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	fn, ok := r.idx[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	def, _ := r.findDef(name)
	if def.RequiredScope != "" {
		if !identity.RequireScope(ctx, def.RequiredScope) {
			return nil, fmt.Errorf("%w: tool %q requires %q", ErrInsufficientScope, name, def.RequiredScope)
		}
	}
	return fn(ctx, args)
}

// IsUnknownTool returns true for errors produced by Call on missing names.
func IsUnknownTool(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "unknown tool:")
}

// IsInsufficientScope reports whether err originated from the
// scope-gate in Registry.Call. Mirrors IsUnknownTool's role for the
// handler's error-code mapping.
func IsInsufficientScope(err error) bool {
	return errors.Is(err, ErrInsufficientScope)
}

// errUnauth is returned by tool handlers when the request context has
// no tenant-scoped API key claims.
var errUnauth = errors.New("mcp: tenant-scoped API key required")
