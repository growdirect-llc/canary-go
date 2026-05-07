package devops

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newRouter(t *testing.T) *chi.Mux {
	t.Helper()
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func TestServicePage_rendersShellForKnownService(t *testing.T) {
	r := newRouter(t)
	for _, name := range []string{"catalog", "manifest", "observability", "pipeline", "qa-agent"} {
		req := httptest.NewRequest(http.MethodGet, "/devops/"+name, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", name, rr.Code)
			continue
		}
		body := rr.Body.String()
		for _, want := range []string{
			name,
			"Canary Sysadmin",
			"Capability",
			"KPIs",
			"Endpoints",
			"Service body",
			"Activity",
			"Linked",
			`<div class="todo-tag">TODO</div>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body missing %q", name, want)
			}
		}
	}
}

func TestServicePage_unknownService404s(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/no-such-service", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestServicePage_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/catalog", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestServicePage_sidebarHighlightsActiveService(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, `href="/devops/manifest" class="active"`) {
		t.Errorf("manifest sidebar entry should have active class; body excerpt missing it")
	}
	if !strings.Contains(body, `href="/devops/catalog" class=""`) {
		t.Errorf("catalog sidebar entry should not be active when manifest is the page")
	}
}

func TestServicePage_renders_serviceMetadata(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pipeline", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		":9103",                                  // port
		"P0",                                     // priority
		"cross-tenant infra",                     // category
		"Brain/wiki/cards/pipeline.md",           // card path
		"B × change-feed",                        // cell
		"Canary/canary/services/devops_monitor.py", // python prior art
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pipeline page missing %q", want)
		}
	}
}

func TestKnownServices_returnsAllSkeletons(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := h.KnownServices()
	if len(got) != 5 {
		t.Errorf("KnownServices count: got %d, want 5; got %v", len(got), got)
	}
	want := map[string]bool{
		"catalog": true, "manifest": true, "observability": true,
		"pipeline": true, "qa-agent": true,
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected service %q", name)
		}
	}
}

func TestNew_buildsTemplate(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h.tmpl == nil {
		t.Error("tmpl is nil")
	}
}
