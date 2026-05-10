package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/mcp"
)

func fakeEcho(_ context.Context, args json.RawMessage) (any, error) {
	return map[string]any{"echo": string(args)}, nil
}

func buildHandler(t *testing.T) (*mcp.Handler, *mcp.Registry) {
	t.Helper()
	reg := mcp.NewRegistry()
	return mcp.New(reg), reg
}

func postMCP(t *testing.T, h *mcp.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestToolsList_EmptyRegistry(t *testing.T) {
	h, _ := buildHandler(t)
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Result struct {
			Tools []any `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Result.Tools) != 0 {
		t.Errorf("tools = %d, want 0", len(resp.Result.Tools))
	}
}

func TestToolsList_AfterRegister(t *testing.T) {
	h, reg := buildHandler(t)
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.ping",
		Description: "test tool",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, fakeEcho)
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{},
	})
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Result.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(resp.Result.Tools))
	}
	if resp.Result.Tools[0]["name"] != "canary.test.ping" {
		t.Errorf("name = %v", resp.Result.Tools[0]["name"])
	}
}

func TestToolsCall_Dispatch(t *testing.T) {
	h, reg := buildHandler(t)
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.echo",
		Description: "echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, fakeEcho)
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "canary.test.echo", "arguments": map[string]any{"x": 1}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Result.Content) == 0 || resp.Result.Content[0].Type != "text" {
		t.Errorf("unexpected result: %+v", resp.Result)
	}
}

func TestToolsCall_UnknownTool(t *testing.T) {
	h, _ := buildHandler(t)
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "canary.no.exist", "arguments": map[string]any{}},
	})
	var resp struct {
		Error struct{ Code int `json:"code"` } `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
}

func TestToolsCall_UnknownMethod(t *testing.T) {
	h, _ := buildHandler(t)
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/bogus",
	})
	var resp struct {
		Error struct{ Code int `json:"code"` } `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
}

func TestMalformedJSON(t *testing.T) {
	h, _ := buildHandler(t)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{bad json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Error struct{ Code int `json:"code"` } `json:"error"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error.Code != -32700 {
		t.Errorf("error.code = %d, want -32700", resp.Error.Code)
	}
}

// ─── GRO-935 per-tool scope gate ─────────────────────────────────────

// scopedReq decorates a tools/call request with API-key claims that
// hold the supplied scopes, then runs the handler.
func scopedReq(t *testing.T, h *mcp.Handler, scopes []string, params map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := identity.InjectClaims(req.Context(), identity.Claims{
		AuthMethod: identity.AuthMethodAPIKey,
		TenantID:   uuid.New(),
		Scopes:     scopes,
	})
	req = req.WithContext(ctx)

	r := chi.NewRouter()
	h.Mount(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// rpcErrorCode unmarshals a JSON-RPC error code from a response body,
// returning 0 on parse failure.
func rpcErrorCode(t *testing.T, body []byte) int {
	t.Helper()
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode rpc body: %v", err)
	}
	if resp.Error == nil {
		return 0
	}
	return resp.Error.Code
}

// TestToolsCall_ScopeGate_DeniesReadOnlyKey is the GRO-935 acceptance
// probe. A read-only API key MUST NOT be able to dispatch a write
// tool — even after authentication succeeds at the MCP entry point.
//
// Multi-assert:
//
//   1. The handler returns the JSON-RPC -32001 (insufficient_scope)
//      error code so clients can distinguish "need scope" from "broken".
//   2. The error message names the missing scope so an operator can
//      grant it without a debug session.
//   3. The tool function is NEVER invoked. We assert via a bool flag —
//      pre-fix the registry would have called the function and the
//      flag would flip true.
//
// Fails pre-fix because the prior Registry.Call ran the tool fn
// directly with no scope check.
func TestToolsCall_ScopeGate_DeniesReadOnlyKey(t *testing.T) {
	reg := mcp.NewRegistry()
	called := false
	reg.Register(mcp.ToolDef{
		Name:          "canary.write_tool",
		Description:   "test write",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:write",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		called = true
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	w := scopedReq(t, h, []string{"alert:read"}, map[string]any{
		"name":      "canary.write_tool",
		"arguments": map[string]any{},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status: got %d, want 200 (JSON-RPC errors ride 200)", w.Code)
	}
	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32001 {
		t.Errorf("rpc error.code: got %d, want -32001 (insufficient_scope)", got)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("alert:write")) {
		t.Errorf("error message should name the missing scope; got: %s", w.Body.String())
	}
	if called {
		t.Errorf("tool function ran despite missing scope — gate did not short-circuit")
	}
}

// TestToolsCall_ScopeGate_AllowsMatchingScope verifies the happy path:
// a key with the exact required scope dispatches the tool.
func TestToolsCall_ScopeGate_AllowsMatchingScope(t *testing.T) {
	reg := mcp.NewRegistry()
	called := false
	reg.Register(mcp.ToolDef{
		Name:          "canary.write_tool",
		Description:   "test write",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:write",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		called = true
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	w := scopedReq(t, h, []string{"alert:write", "alert:read"}, map[string]any{
		"name":      "canary.write_tool",
		"arguments": map[string]any{},
	})

	if got := rpcErrorCode(t, w.Body.Bytes()); got != 0 {
		t.Errorf("expected no error, got code %d (body=%s)", got, w.Body.String())
	}
	if !called {
		t.Errorf("tool function should have run with matching scope")
	}
}

// TestToolsCall_ScopeGate_ReadDoesNotImplyWrite verifies the
// read-vs-write split: holding alert:read only does not satisfy
// alert:write. Without this guarantee the entire scope vocabulary
// collapses to a single read level.
func TestToolsCall_ScopeGate_ReadDoesNotImplyWrite(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:          "canary.write_tool",
		Description:   "test write",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:write",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return nil, nil
	})
	reg.Register(mcp.ToolDef{
		Name:          "canary.read_tool",
		Description:   "test read",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:read",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	// Read scope → read tool: allowed.
	wRead := scopedReq(t, h, []string{"alert:read"}, map[string]any{
		"name":      "canary.read_tool",
		"arguments": map[string]any{},
	})
	if got := rpcErrorCode(t, wRead.Body.Bytes()); got != 0 {
		t.Errorf("read tool with read scope: expected no error, got %d", got)
	}

	// Read scope → write tool: denied with -32001.
	wWrite := scopedReq(t, h, []string{"alert:read"}, map[string]any{
		"name":      "canary.write_tool",
		"arguments": map[string]any{},
	})
	if got := rpcErrorCode(t, wWrite.Body.Bytes()); got != -32001 {
		t.Errorf("write tool with read scope: expected -32001, got %d", got)
	}
}

// TestToolsCall_ScopeGate_NoClaimsDenied verifies an unauthenticated
// caller (no claims in context) is denied even for tools with a
// declared RequiredScope. Belt-and-suspenders against the chance that
// MCP gets mounted without APIKeyMiddleware ahead of it.
func TestToolsCall_ScopeGate_NoClaimsDenied(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:          "canary.read_tool",
		Description:   "test read",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:read",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	// No claims context — postMCP.
	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "canary.read_tool", "arguments": map[string]any{}},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32001 {
		t.Errorf("expected -32001 for no claims, got %d (body=%s)", got, w.Body.String())
	}
}

// TestToolsCall_NoRequiredScope_AllowsUnscopedTools verifies that
// tools registered without a RequiredScope (e.g. liveness probes)
// keep working without claims. The gate is opt-in per-tool.
func TestToolsCall_NoRequiredScope_AllowsUnscopedTools(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.ping",
		Description: "ping",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		// RequiredScope intentionally empty.
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"pong": true}, nil
	})
	h := mcp.New(reg)

	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "canary.test.ping", "arguments": map[string]any{}},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != 0 {
		t.Errorf("unscoped tool should run without claims; got rpc code %d", got)
	}
}
