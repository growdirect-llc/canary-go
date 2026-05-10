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

// ─── GRO-936 MCP audit emission ──────────────────────────────────────

// stubRecorder captures the most recent AuditEvent. err lets tests
// assert handlers stay fail-open on recorder failures.
type stubRecorder struct {
	events []mcp.AuditEvent
	err    error
}

func (s *stubRecorder) Record(_ context.Context, e mcp.AuditEvent) error {
	s.events = append(s.events, e)
	return s.err
}

// scopedReqWithRecorder mirrors scopedReq but constructs a Handler
// via NewWithAudit so the recorder is wired in.
func scopedReqWithRecorder(t *testing.T, reg *mcp.Registry, rec mcp.AuditRecorder, scopes []string, params map[string]any, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	h := mcp.NewWithAudit(reg, rec, nil)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	keyID := uuid.New()
	tenantID := uuid.New()
	ctx := identity.InjectClaims(req.Context(), identity.Claims{
		AuthMethod: identity.AuthMethodAPIKey,
		TenantID:   tenantID,
		KeyID:      keyID,
		Scopes:     scopes,
	})
	req = req.WithContext(ctx)

	r := chi.NewRouter()
	h.Mount(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestToolsCall_Audit_RecordsOnSuccess is the GRO-936 acceptance probe.
// A successful tools/call MUST produce exactly one audit event whose
// fields capture: tool name, tenant id, key id, args hash, "ok" status,
// non-zero latency, request id. The args field itself is NOT recorded
// — only its SHA-256 — so customer PII passing through tool arguments
// stays out of app.audit_log.
//
// Fails pre-fix because pre-GRO-936 the handler did not call any
// recorder; events array stays empty.
func TestToolsCall_Audit_RecordsOnSuccess(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:          "canary.alert.list",
		Description:   "list",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:read",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"alerts": []any{}}, nil
	})
	rec := &stubRecorder{}

	args := json.RawMessage(`{"limit":10}`)
	params := map[string]any{"name": "canary.alert.list", "arguments": json.RawMessage(args)}
	w := scopedReqWithRecorder(t, reg, rec, []string{"alert:read"}, params, "req-abc-123")

	if got := rpcErrorCode(t, w.Body.Bytes()); got != 0 {
		t.Fatalf("expected success, got rpc code %d (body=%s)", got, w.Body.String())
	}
	if len(rec.events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(rec.events))
	}
	e := rec.events[0]
	if e.ToolName != "canary.alert.list" {
		t.Errorf("tool name: got %q, want canary.alert.list", e.ToolName)
	}
	if e.Status != "ok" {
		t.Errorf("status: got %q, want ok", e.Status)
	}
	if e.TenantID == uuid.Nil {
		t.Errorf("tenant id should be populated from claims")
	}
	if e.KeyID == uuid.Nil {
		t.Errorf("key id should be populated from claims")
	}
	if e.RequestID != "req-abc-123" {
		t.Errorf("request id: got %q, want req-abc-123", e.RequestID)
	}
	// PII guard: the raw args MUST NOT be reflected verbatim. Hash MUST be set
	// (32-byte SHA-256 → 64 hex chars).
	if len(e.ArgsDigest) != 64 {
		t.Errorf("args digest: expected 64-char sha256 hex, got %q", e.ArgsDigest)
	}
}

// TestToolsCall_Audit_RecordsOnScopeDenial verifies a scope-denied
// dispatch still produces an audit row — incident response needs to
// see the attempt, not just the success. Status carries the rpc error
// code as a string so dashboards can pivot on "denied" without a join.
func TestToolsCall_Audit_RecordsOnScopeDenial(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:          "canary.alert.acknowledge",
		Description:   "ack",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:write",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	rec := &stubRecorder{}

	params := map[string]any{"name": "canary.alert.acknowledge", "arguments": map[string]any{}}
	w := scopedReqWithRecorder(t, reg, rec, []string{"alert:read"}, params, "")

	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32001 {
		t.Fatalf("expected -32001, got %d", got)
	}
	if len(rec.events) != 1 {
		t.Fatalf("scope-denied call should still emit audit: got %d events", len(rec.events))
	}
	if rec.events[0].Status != "-32001" {
		t.Errorf("status: got %q, want -32001", rec.events[0].Status)
	}
	if rec.events[0].ToolName != "canary.alert.acknowledge" {
		t.Errorf("tool name should be the attempted tool")
	}
}

// TestToolsCall_Audit_RecordFailureIsNotFatal verifies the recorder
// is fail-open: a Record error MUST NOT turn into a 500 from the
// handler. The response must still carry the dispatch result.
func TestToolsCall_Audit_RecordFailureIsNotFatal(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:          "canary.alert.list",
		Description:   "list",
		InputSchema:   json.RawMessage(`{"type":"object"}`),
		RequiredScope: "alert:read",
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"alerts": []any{}}, nil
	})
	rec := &stubRecorder{err: errAuditDown}

	params := map[string]any{"name": "canary.alert.list", "arguments": map[string]any{}}
	w := scopedReqWithRecorder(t, reg, rec, []string{"alert:read"}, params, "")

	if got := rpcErrorCode(t, w.Body.Bytes()); got != 0 {
		t.Errorf("recorder error must not surface as rpc error; got code %d", got)
	}
	if len(rec.events) != 1 {
		t.Errorf("recorder should still have been invoked once")
	}
}

// errAuditDown is a tiny shim for the fail-open test.
type errAuditDownT struct{}

func (errAuditDownT) Error() string { return "audit store unavailable" }

var errAuditDown = errAuditDownT{}

// TestToolsCall_Audit_NilRecorderIsOK verifies a Handler built via
// New() (no recorder) keeps working — the audit emission is opt-in.
// Belt-and-suspenders for any code path that constructs a registry
// without wiring audit.
func TestToolsCall_Audit_NilRecorderIsOK(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.ping",
		Description: "ping",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"pong": true}, nil
	})
	h := mcp.New(reg) // no recorder

	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "canary.test.ping", "arguments": map[string]any{}},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != 0 {
		t.Errorf("nil recorder should not affect dispatch; rpc code = %d", got)
	}
}

// ─── GRO-939 invalid-params enforcement ──────────────────────────────

// TestToolsCall_InvalidParams_MapsTo_32602 is the GRO-939 acceptance
// probe. A tool that returns InvalidParamsf MUST surface as JSON-RPC
// -32602 with the field name in the message — clients can self-correct
// without a debug session, and dashboards can pivot on the error
// class. Belt-and-suspenders: the tool function never reaches the
// store on a parse failure (verified via a flag that flips only after
// the parse).
//
// Fails pre-fix because pre-GRO-939 tool handlers swallowed
// uuid.Parse errors silently and dispatched with uuid.Nil — producing
// a 200 + a wrong write or returning a generic -32603 internal error.
func TestToolsCall_InvalidParams_MapsTo_32602(t *testing.T) {
	reg := mcp.NewRegistry()
	reachedStore := false
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.write_with_uuid",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","required":["id"]}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		var p struct{ ID string `json:"id"` }
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, mcp.InvalidParamsf("body: %v", err)
		}
		if _, err := uuid.Parse(p.ID); err != nil {
			return nil, mcp.InvalidParamsf("id: %v", err)
		}
		reachedStore = true
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "canary.test.write_with_uuid",
			"arguments": map[string]any{"id": "not-a-uuid"},
		},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32602 {
		t.Errorf("rpc code: got %d, want -32602 (Invalid params)", got)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("id:")) {
		t.Errorf("error message should name the failing field; got %s", w.Body.String())
	}
	if reachedStore {
		t.Errorf("store reached despite invalid params — handler did not short-circuit")
	}
}

// TestToolsCall_InvalidParams_MissingRequiredField verifies a missing
// required field surfaces as -32602 (not silently zero/default).
func TestToolsCall_InvalidParams_MissingRequiredField(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.requires_field",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object","required":["disposition"]}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		var p struct{ Disposition string `json:"disposition"` }
		_ = json.Unmarshal(args, &p)
		if p.Disposition == "" {
			return nil, mcp.InvalidParamsf("disposition: required")
		}
		return map[string]any{"ok": true}, nil
	})
	h := mcp.New(reg)

	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "canary.test.requires_field",
			"arguments": map[string]any{}, // missing disposition
		},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32602 {
		t.Errorf("rpc code: got %d, want -32602", got)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("disposition")) {
		t.Errorf("error should name disposition; got %s", w.Body.String())
	}
}

// TestToolsCall_InvalidParams_BadEnumValue verifies enum violations
// land on -32602.
func TestToolsCall_InvalidParams_BadEnumValue(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:        "canary.test.enum_field",
		Description: "test",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (any, error) {
		var p struct{ Severity string `json:"severity"` }
		_ = json.Unmarshal(args, &p)
		switch p.Severity {
		case "low", "medium", "high", "critical":
			return map[string]any{"ok": true}, nil
		default:
			return nil, mcp.InvalidParamsf("severity: must be one of low|medium|high|critical, got %q", p.Severity)
		}
	})
	h := mcp.New(reg)

	w := postMCP(t, h, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "canary.test.enum_field",
			"arguments": map[string]any{"severity": "purple"},
		},
	})
	if got := rpcErrorCode(t, w.Body.Bytes()); got != -32602 {
		t.Errorf("rpc code: got %d, want -32602", got)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("severity")) {
		t.Errorf("error should name the bad enum field; got %s", w.Body.String())
	}
}

// TestIsInvalidParams_HelperWorks pins the helper used by the handler
// for code-mapping. errors.Is should report true for both directly
// wrapped and InvalidParamsf-built errors.
func TestIsInvalidParams_HelperWorks(t *testing.T) {
	if !mcp.IsInvalidParams(mcp.InvalidParamsf("x")) {
		t.Error("IsInvalidParams should report true for InvalidParamsf result")
	}
	if mcp.IsInvalidParams(nil) {
		t.Error("IsInvalidParams should report false for nil")
	}
	if mcp.IsInvalidParams(context.Canceled) {
		t.Error("IsInvalidParams should report false for unrelated errors")
	}
}
