package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ruptiv/canary/internal/config"
)

func TestDiscoveryHandler(t *testing.T) {
	t.Run("returns valid discovery document", func(t *testing.T) {
		cfg := &config.Config{PublicURL: "https://demo.growdirect.io"}
		h := discoveryHandler(cfg)

		req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp.json", nil)
		w := httptest.NewRecorder()
		h(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %q", ct)
		}

		var doc map[string]any
		if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
			t.Fatalf("decode: %v", err)
		}

		// Required fields.
		for _, key := range []string{"mcp_version", "name", "endpoint", "transport", "auth", "modules", "tools_count", "openapi"} {
			if _, ok := doc[key]; !ok {
				t.Errorf("missing field %q", key)
			}
		}

		// Endpoint must use the configured PublicURL.
		if got := doc["endpoint"].(string); got != "https://demo.growdirect.io/mcp" {
			t.Errorf("endpoint = %q, want https://demo.growdirect.io/mcp", got)
		}

		// tools_count must be a positive number.
		if n, ok := doc["tools_count"].(float64); !ok || n <= 0 {
			t.Errorf("tools_count = %v, want positive number", doc["tools_count"])
		}
	})

	t.Run("derives base URL from request host when PUBLIC_URL unset", func(t *testing.T) {
		cfg := &config.Config{PublicURL: ""}
		h := discoveryHandler(cfg)

		req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp.json", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Host = "gateway.example.com"
		w := httptest.NewRecorder()
		h(w, req)

		var doc map[string]any
		if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got := doc["endpoint"].(string); got != "https://gateway.example.com/mcp" {
			t.Errorf("endpoint = %q, want https://gateway.example.com/mcp", got)
		}
	})
}
