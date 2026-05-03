package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/growdirect-llc/rapidpos/internal/mcp"
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
