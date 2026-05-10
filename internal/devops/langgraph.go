// Package devops — LangGraph API client.
//
// Talks to a running LangGraph server (local langgraph dev or Cloud Run).
// The server URL is read from LANGGRAPH_URL env var at Handler construction
// time; the client itself is stateless.
package devops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Thread is a LangGraph thread (one graph execution with checkpoint history).
type Thread struct {
	ID        string         `json:"thread_id"`
	Status    string         `json:"status"` // "interrupted" | "idle" | "busy" | "error"
	Values    map[string]any `json:"values"` // PipelineState fields
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// LangGraphClient is a thin HTTP client for the LangGraph server REST API.
type LangGraphClient struct {
	baseURL string
	http    *http.Client
}

// NewLangGraphClient creates a client targeting baseURL (e.g. "http://localhost:2024").
func NewLangGraphClient(baseURL string) *LangGraphClient {
	return &LangGraphClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *LangGraphClient) endpoint(path []string, query url.Values) (string, error) {
	base, err := parseAllowedLangGraphBase(c.baseURL)
	if err != nil {
		return "", err
	}
	joined, err := url.JoinPath(base.String(), path...)
	if err != nil {
		return "", fmt.Errorf("langgraph: build url: %w", err)
	}
	u, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("langgraph: build url: %w", err)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func parseAllowedLangGraphBase(raw string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("langgraph: invalid base url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("langgraph: scheme_not_allowed %q", base.Scheme)
	}
	host := strings.ToLower(base.Hostname())
	if host == "" {
		return nil, fmt.Errorf("langgraph: host_not_allowed %q", host)
	}
	if isAllowedLangGraphEndpoint(base.Scheme, host) {
		base.RawQuery = ""
		base.Fragment = ""
		return base, nil
	}
	return nil, fmt.Errorf("langgraph: host_not_allowed %q", host)
}

func isAllowedLangGraphEndpoint(scheme, host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return scheme == "https" && strings.HasSuffix(host, ".run.app")
}

// PendingReleases returns threads with status=interrupted (awaiting human approval).
func (c *LangGraphClient) PendingReleases(ctx context.Context) ([]Thread, error) {
	target, err := c.endpoint([]string{"threads"}, url.Values{"status": {"interrupted"}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("langgraph: GET /threads status %d", resp.StatusCode)
	}
	var threads []Thread
	if err := json.NewDecoder(resp.Body).Decode(&threads); err != nil {
		return nil, fmt.Errorf("langgraph: decode threads: %w", err)
	}
	return threads, nil
}

// GetThread returns the full state of a single thread.
func (c *LangGraphClient) GetThread(ctx context.Context, id string) (*Thread, error) {
	target, err := c.endpoint([]string{"threads", id}, nil)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("langgraph: thread %s not found", id)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("langgraph: GET /threads/%s status %d", id, resp.StatusCode)
	}
	var t Thread
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, fmt.Errorf("langgraph: decode thread: %w", err)
	}
	return &t, nil
}

// Resume sends a command to an interrupted thread, resuming graph execution.
// decision is passed as command.resume — conventionally "approved" or "rejected".
func (c *LangGraphClient) Resume(ctx context.Context, threadID, assistantID, decision string) error {
	body, err := json.Marshal(map[string]any{
		"assistant_id": assistantID,
		"command":      map[string]any{"resume": decision},
	})
	if err != nil {
		return err
	}
	target, err := c.endpoint([]string{"threads", threadID, "runs"}, nil)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("langgraph: resume thread %s status %d", threadID, resp.StatusCode)
	}
	return nil
}
