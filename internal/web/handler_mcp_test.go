package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ruptiv/canary/internal/mcp"
	"github.com/ruptiv/canary/internal/web"
)

func TestMCPTools_NoRegistry_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No MCP tools registered") {
		t.Errorf("expected empty-state copy")
	}
}

func TestMCPTools_WithRegistry_RendersTools(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolDef{
		Name:        "canary.alert.list",
		Description: "List alerts for the tenant",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })
	reg.Register(mcp.ToolDef{
		Name:        "canary.customer.get",
		Description: "Fetch a customer by ID",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })

	h := web.New(web.Deps{MCPRegistry: reg}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/mcp/tools", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"canary.alert.list", "canary.customer.get", "List alerts", "Fetch a customer"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Module column is derived from the dotted-name middle segment.
	if !strings.Contains(body, "alert") || !strings.Contains(body, "customer") {
		t.Errorf("expected module names alert / customer in body")
	}
}
