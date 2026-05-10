package devops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestLangGraphClient_PendingReleases(t *testing.T) {
	threads := []Thread{
		{
			ID:        "thread-abc",
			Status:    "interrupted",
			Values:    map[string]any{"task": "add hawk handler", "service": "canary-hawk"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads" || r.URL.Query().Get("status") != "interrupted" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(threads)
	}))
	defer srv.Close()

	client := NewLangGraphClient(srv.URL)
	got, err := client.PendingReleases(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(got))
	}
	if got[0].ID != "thread-abc" {
		t.Errorf("expected thread-abc, got %s", got[0].ID)
	}
}

func TestLangGraphClient_PendingReleases_ServerOffline(t *testing.T) {
	client := NewLangGraphClient("http://localhost:19999") // nothing listening
	got, err := client.PendingReleases(context.Background())
	if err == nil {
		t.Fatal("expected error for offline server")
	}
	if got != nil {
		t.Errorf("expected nil slice on error, got %v", got)
	}
}

func TestLangGraphClient_RejectsNonAllowlistedHostBeforeRequest(t *testing.T) {
	called := false
	client := NewLangGraphClient("http://evil.example/")
	client.http = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("unexpected outbound request")
		}),
	}

	got, err := client.PendingReleases(context.Background())
	if err == nil {
		t.Fatal("expected non-allowlisted host error")
	}
	if got != nil {
		t.Fatalf("expected nil threads, got %v", got)
	}
	if !strings.Contains(err.Error(), "host_not_allowed") {
		t.Fatalf("expected host_not_allowed error, got %v", err)
	}
	if called {
		t.Fatal("expected host validation before any outbound request")
	}
}

func TestLangGraphClient_Resume(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/threads/thread-xyz/runs" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		called = true
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cmd, _ := body["command"].(map[string]any)
		if cmd["resume"] != "approved" {
			http.Error(w, "wrong resume value", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"run_id":"run-1"}`))
	}))
	defer srv.Close()

	client := NewLangGraphClient(srv.URL)
	err := client.Resume(context.Background(), "thread-xyz", "codegen", "approved")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestLangGraphClient_GetThread(t *testing.T) {
	thread := Thread{
		ID:     "thread-abc",
		Status: "interrupted",
		Values: map[string]any{"task": "add hawk"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/threads/thread-abc" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(thread)
	}))
	defer srv.Close()

	client := NewLangGraphClient(srv.URL)
	got, err := client.GetThread(context.Background(), "thread-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "interrupted" {
		t.Errorf("expected interrupted, got %s", got.Status)
	}
}
