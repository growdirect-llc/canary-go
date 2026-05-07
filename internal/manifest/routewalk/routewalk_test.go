package routewalk

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestWalk_emitsAllRoutes(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/health", func(http.ResponseWriter, *http.Request) {})
	r.Post("/v1/foo", func(http.ResponseWriter, *http.Request) {})
	r.Group(func(r chi.Router) {
		r.Get("/v1/bar", func(http.ResponseWriter, *http.Request) {})
		r.Delete("/v1/bar/{id}", func(http.ResponseWriter, *http.Request) {})
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "routes-seen.json")
	if err := Walk(r, "test-svc", out); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got Output
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Count != 4 {
		t.Errorf("count: got %d, want 4 (routes: %v)", got.Count, got.Routes)
	}
	if got.Service != "test-svc" {
		t.Errorf("service: got %q, want %q", got.Service, "test-svc")
	}
	if got.GeneratedAt == "" {
		t.Errorf("generated_at empty")
	}

	seen := map[string]bool{}
	for _, r := range got.Routes {
		seen[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /health",
		"POST /v1/foo",
		"GET /v1/bar",
		"DELETE /v1/bar/{id}",
	} {
		if !seen[want] {
			t.Errorf("missing route %q in walk output", want)
		}
	}
}

func TestWalk_routesSortedByPathThenMethod(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/v1/foo", func(http.ResponseWriter, *http.Request) {})
	r.Get("/v1/foo", func(http.ResponseWriter, *http.Request) {})
	r.Get("/v1/bar", func(http.ResponseWriter, *http.Request) {})

	out := filepath.Join(t.TempDir(), "out.json")
	if err := Walk(r, "x", out); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	data, _ := os.ReadFile(out)
	var doc Output
	_ = json.Unmarshal(data, &doc)

	want := []string{
		"GET /v1/bar",
		"GET /v1/foo",
		"POST /v1/foo",
	}
	if len(doc.Routes) != len(want) {
		t.Fatalf("route count: got %d, want %d", len(doc.Routes), len(want))
	}
	for i, w := range want {
		got := doc.Routes[i].Method + " " + doc.Routes[i].Path
		if got != w {
			t.Errorf("route[%d]: got %q, want %q", i, got, w)
		}
	}
}

func TestWalk_defaultOutPath(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/x", func(http.ResponseWriter, *http.Request) {})
	if err := Walk(r, "svc", ""); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, DefaultOutPath)); err != nil {
		t.Fatalf("default output not created at %s: %v", DefaultOutPath, err)
	}
}

func TestWalk_emptyRouter(t *testing.T) {
	r := chi.NewRouter()
	out := filepath.Join(t.TempDir(), "empty.json")
	if err := Walk(r, "empty", out); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	data, _ := os.ReadFile(out)
	var doc Output
	_ = json.Unmarshal(data, &doc)
	if doc.Count != 0 {
		t.Errorf("count: got %d, want 0", doc.Count)
	}
	if doc.Routes == nil {
		// JSON decoded "null" — acceptable; just don't crash on iteration
		return
	}
	if len(doc.Routes) != 0 {
		t.Errorf("len(routes): got %d, want 0", len(doc.Routes))
	}
}

func TestHandlerName_funcReturnsSomething(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	name := handlerName(h)
	if name == "" {
		t.Error("handlerName returned empty for HandlerFunc")
	}
}

func TestHandlerName_nilHandler(t *testing.T) {
	if got := handlerName(nil); got != "" {
		t.Errorf("handlerName(nil): got %q, want empty", got)
	}
}
