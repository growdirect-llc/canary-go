package devops

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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
	// All P0 services in Phase 1 skeleton (catalog, manifest, observability,
	// pipeline, qa-agent) plus api-docs now have custom handlers. The
	// generic shell still backs evidence/anchor/mcp until they get
	// dedicated pages — exercise via those.
	for _, name := range []string{"evidence", "anchor", "mcp"} {
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

// TestServicePage_renders_serviceMetadata verifies that the generic shell
// renders service metadata correctly. Uses evidence — one of the remaining
// services on the generic shell after T3B.1/2/3/4 migrated catalog,
// manifest, observability, pipeline, and qa-agent to custom handlers.
func TestServicePage_renders_serviceMetadata(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/evidence", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		":9201",              // evidence port
		"P0",                 // priority
		"cross-tenant infra", // category
		"B × reference",      // cell
		"Append-only",        // status text snippet (from Service.Status)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("evidence page missing %q", want)
		}
	}
}

func TestKnownServices_returnsAllSkeletons(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := h.KnownServices()
	want := map[string]bool{
		"catalog": true, "manifest": true, "observability": true,
		"pipeline": true, "qa-agent": true, "api-docs": true,
		"evidence": true, "anchor": true, "mcp": true,
		"etl": true, "wallet": true, "test-lab": true,
		"scenarios": true, "pos-lab": true, "live-fire": true,
	}
	if len(got) != len(want) {
		t.Errorf("KnownServices count: got %d, want %d; got %v", len(got), len(want), got)
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

func TestApiDocs_pageRendersWhenSpecLoaded(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Force-load a synthetic spec so the test doesn't depend on cwd.
	h.openAPIRaw = []byte("openapi: 3.0.3\ninfo: { title: x, version: 0 }\npaths: {}\n")
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/api-docs", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"api-docs",
		"redoc.standalone.js",
		"Redoc.init(",
		`id="redoc-container"`,
		"/devops/api-docs/openapi.yaml", // appears as the spec URL — html/template encodes it for JS context
	} {
		if !strings.Contains(body, want) {
			t.Errorf("api-docs body missing %q", want)
		}
	}
}

func TestApiDocs_pageRendersPlaceholderWhenSpecMissing(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.openAPIRaw = nil // force not-loaded state
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/api-docs", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "SPEC NOT LOADED") {
		t.Errorf("placeholder banner missing")
	}
	if strings.Contains(body, "Redoc.init(") {
		t.Errorf("Redoc init script should NOT render when spec missing")
	}
}

func TestApiDocs_specEndpointReturnsYAML(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := []byte("openapi: 3.0.3\ninfo: { title: y, version: 1 }\npaths: {}\n")
	h.openAPIRaw = body
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/api-docs/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want application/yaml", ct)
	}
	if rr.Body.String() != string(body) {
		t.Errorf("spec body mismatch")
	}
}

func TestApiDocs_specEndpoint503WhenNotLoaded(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.openAPIRaw = nil
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/api-docs/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rr.Code)
	}
}

func TestApiDocs_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "api-docs" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("api-docs not in KnownServices()")
	}
}

func _sampleCatalog() *Catalog {
	port := 9100
	return &Catalog{
		Axes: []CatalogAxis{
			{Key: "A", Name: "Adapter", Direction: "POS → Canary"},
			{Key: "B", Name: "Resource", Direction: "Canary → external"},
			{Key: "C", Name: "Agent", Direction: "Canary → AI agents"},
		},
		Tiers: []string{"stream", "change-feed", "daily-batch", "bulk-window", "reference"},
		Cells: []CatalogCell{
			{Axis: "A", Tier: "stream", EndpointCount: 0, Services: nil},
			{Axis: "A", Tier: "change-feed", EndpointCount: 0, Services: nil},
			{Axis: "A", Tier: "daily-batch", EndpointCount: 0, Services: nil},
			{Axis: "A", Tier: "bulk-window", EndpointCount: 0, Services: nil},
			{Axis: "A", Tier: "reference", EndpointCount: 0, Services: nil},
			{Axis: "B", Tier: "stream", EndpointCount: 0, Services: nil},
			{Axis: "B", Tier: "change-feed", EndpointCount: 5, Services: []string{"observability"}},
			{Axis: "B", Tier: "daily-batch", EndpointCount: 0, Services: nil},
			{Axis: "B", Tier: "bulk-window", EndpointCount: 0, Services: nil},
			{Axis: "B", Tier: "reference", EndpointCount: 8, Services: []string{"catalog", "manifest"}},
			{Axis: "C", Tier: "stream", EndpointCount: 0, Services: nil},
			{Axis: "C", Tier: "change-feed", EndpointCount: 4, Services: []string{"qa-agent"}},
			{Axis: "C", Tier: "daily-batch", EndpointCount: 0, Services: nil},
			{Axis: "C", Tier: "bulk-window", EndpointCount: 0, Services: nil},
			{Axis: "C", Tier: "reference", EndpointCount: 0, Services: nil},
		},
		Services: []CatalogSvc{
			{Name: "catalog", Port: &port, Priority: "P0", Category: "cross-tenant infra", Cells: []string{"B×reference"}, EndpointCount: 3},
			{Name: "qa-agent", Port: &port, Priority: "P0", Category: "cross-tenant infra", Cells: []string{"C×change-feed"}, EndpointCount: 4},
		},
		Totals: CatalogTotals{ServiceCount: 5, EndpointCount: 17},
	}
}

func TestCatalog_pageRendersGridAndServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalog()
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/catalog", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Grid headers — all 5 tiers + all 3 axes appear
	for _, tier := range []string{"stream", "change-feed", "daily-batch", "bulk-window", "reference"} {
		if !strings.Contains(body, tier) {
			t.Errorf("grid missing tier header %q", tier)
		}
	}
	for _, axis := range []string{"Adapter", "Resource", "Agent"} {
		if !strings.Contains(body, axis) {
			t.Errorf("grid missing axis name %q", axis)
		}
	}
	// Cell counts that are non-zero
	for _, want := range []string{">5<", ">8<", ">4<"} {
		if !strings.Contains(body, want) {
			t.Errorf("grid missing non-zero cell count snippet %q", want)
		}
	}
	// Service rows
	for _, name := range []string{"catalog", "qa-agent"} {
		if !strings.Contains(body, `href="/devops/`+name+`"`) {
			t.Errorf("service table missing link for %q", name)
		}
	}
	// Totals strip
	if !strings.Contains(body, ">5<") || !strings.Contains(body, ">17<") {
		t.Errorf("totals strip missing service/endpoint counts")
	}
}

func TestCatalog_pageRendersPlaceholderWhenMissing(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = nil
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/catalog", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "CATALOG NOT LOADED") {
		t.Errorf("placeholder banner missing")
	}
	if strings.Contains(body, "Cadence Ladder — Axis × Tier") {
		t.Errorf("grid should NOT render when catalog missing")
	}
}

func TestCatalog_setsNoStoreCacheControl(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalog()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/devops/catalog", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

// _sampleDrift builds a Drift with realistic shape — used by manifest tests
// when we want a fully-populated activity zone. Mirrors the Phase 1 Gate
// PASS state we ship at HEAD.
func _sampleDrift() *Drift {
	return &Drift{
		Loaded:            true,
		ManifestEndpoints: 178,
		MountedEndpoints:  0,
		OpenAPIEndpoints:  330,
		Matched:           0,
		ManifestOnly:      176,
		OpenAPIOnly:       328,
		RoutesOnly:        0,
		GatePass:          true,
	}
}

// _sampleCatalogForManifest extends _sampleCatalog with priority diversity
// so the manifest page's P0/P1/P2 sectioning has data in each section.
func _sampleCatalogForManifest() *Catalog {
	port := 9100
	return &Catalog{
		Axes:  []CatalogAxis{{Key: "B", Name: "Resource", Direction: "Canary → external"}},
		Tiers: []string{"reference"},
		Cells: []CatalogCell{},
		Services: []CatalogSvc{
			{Name: "catalog", Port: &port, Priority: "P0", Category: "cross-tenant infra", Cells: []string{"B×reference"}, EndpointCount: 3},
			{Name: "wallet", Port: &port, Priority: "P0", Category: "cross-tenant infra", Cells: []string{"B×reference"}, EndpointCount: 1},
			{Name: "vault", Port: &port, Priority: "P1", Category: "cross-tenant infra", Cells: []string{"B×reference"}, EndpointCount: 1},
			{Name: "keys", Port: &port, Priority: "P2", Category: "cross-tenant infra", Cells: []string{"B×reference"}, EndpointCount: 1},
		},
		Totals: CatalogTotals{ServiceCount: 4, EndpointCount: 6},
	}
}

func TestManifest_pageRendersSummaryAndServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForManifest()
	h.drift = _sampleDrift()
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Services",
		"Activity — Drift report",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("manifest page missing zone %q", zone)
		}
	}

	// KPI counts from the catalog
	if !strings.Contains(body, ">4<") {
		t.Errorf("KPI strip missing service count 4")
	}
	if !strings.Contains(body, ">6<") {
		t.Errorf("KPI strip missing endpoint count 6")
	}
	// Phase 1 Gate PASS pill
	if !strings.Contains(body, "PASS") {
		t.Errorf("KPI strip missing Phase 1 Gate PASS")
	}

	// Drift counts from the activity zone
	for _, want := range []string{
		"Manifest endpoints",
		">178<",
		"Manifest-only",
		">176<",
		"OpenAPI-only",
		">328<",
		"Phase 1 Gate ≤ 10",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("activity zone missing %q", want)
		}
	}

	// Services table has P0 and P1 sections + service-name links
	for _, want := range []string{
		"P0 — 2 services",
		"P1 — 1 services",
		"P2 — 1 services",
		`href="/devops/wallet"`,
		`href="/devops/vault"`,
		`href="/devops/keys"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("services table missing %q", want)
		}
	}

	// Linked cards point to consumer services
	for _, want := range []string{
		`href="/devops/catalog"`,
		`href="/devops/api-docs"`,
		`href="/devops/observability"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("linked zone missing %q", want)
		}
	}
}

func TestManifest_pageDegradesWhenCatalogMissing(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = nil
	h.drift = _sampleDrift()
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "devops-catalog.json not loaded") {
		t.Errorf("missing-catalog placeholder absent")
	}
}

func TestManifest_pageDegradesWhenDriftMissing(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForManifest()
	h.drift = &Drift{} // not loaded
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "drift-report.txt not loaded") {
		t.Errorf("missing-drift placeholder absent")
	}
	// Catalog section should still render even when drift is missing.
	if !strings.Contains(body, "Services") {
		t.Errorf("services zone should still render with catalog only")
	}
}

func TestManifest_setsNoStoreCacheControl(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForManifest()
	h.drift = _sampleDrift()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestManifest_gateFailRendersFailPill(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForManifest()
	d := _sampleDrift()
	d.GatePass = false
	d.RoutesOnly = 42
	h.drift = d
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/devops/manifest", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "FAIL") {
		t.Errorf("KPI strip should render FAIL when gate fails")
	}
	if !strings.Contains(body, ">42<") {
		t.Errorf("KPI strip should show unaccounted route count when non-zero")
	}
}

func TestParseTrailingInt_extractsClean(t *testing.T) {
	cases := map[string]int{
		"manifest endpoints:   80":         80,
		"manifest-only:                 78": 78,
		"matched: 0":                       0,
		"routes-only (UNACCOUNTED):     5": 5,
		"some-line-without-number":         0,
	}
	for in, want := range cases {
		if got := parseTrailingInt(in); got != want {
			t.Errorf("parseTrailingInt(%q) = %d, want %d", in, got, want)
		}
	}
}

// _sampleCatalogForObservability gives every tier at least one service so
// the observability page renders all five tier-services lists with content.
func _sampleCatalogForObservability() *Catalog {
	return &Catalog{
		Axes:  []CatalogAxis{{Key: "B", Name: "Resource", Direction: "Canary → external"}},
		Tiers: []string{"stream", "change-feed", "daily-batch", "bulk-window", "reference"},
		Cells: []CatalogCell{
			{Axis: "B", Tier: "stream", EndpointCount: 2, Services: []string{"parsers", "devices"}},
			{Axis: "B", Tier: "change-feed", EndpointCount: 16, Services: []string{"alert", "chirp", "casemgmt"}},
			{Axis: "B", Tier: "daily-batch", EndpointCount: 1, Services: []string{"etl"}},
			{Axis: "B", Tier: "bulk-window", EndpointCount: 0, Services: nil},
			{Axis: "B", Tier: "reference", EndpointCount: 29, Services: []string{"catalog", "manifest", "api-docs"}},
		},
		Services: []CatalogSvc{},
		Totals:   CatalogTotals{ServiceCount: 9, EndpointCount: 48},
	}
}

func TestObservability_pageRendersFiveTiers(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForObservability()
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/observability", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Tier rollup",
		"Activity — Live health (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("observability page missing zone %q", zone)
		}
	}

	// All 5 tier names rendered with their data-tier attribute (border-color hook)
	for _, tier := range []string{"stream", "change-feed", "daily-batch", "bulk-window", "reference"} {
		if !strings.Contains(body, `data-tier="`+tier+`"`) {
			t.Errorf("tier card missing for %q (expected data-tier attribute)", tier)
		}
	}

	// Per-tier metadata content is verbatim from spec §"Per-tier infrastructure"
	for _, want := range []string{
		"SSE / WebSocket / Valkey XREAD tail", // stream protocol
		"REST polling with cursor",            // change-feed protocol
		"Cron-driven export",                  // daily-batch protocol
		"Scheduled file drop / blob",          // bulk-window protocol
		"REST GET with strong cache headers",  // reference protocol
		"Replay from queue",                   // stream recovery
		"Catch up from watermark",             // change-feed recovery
		"Force resync",                        // reference recovery
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tier metadata missing %q", want)
		}
	}
}

func TestObservability_pageListsServicesPerTier(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForObservability()
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/observability", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// Each non-empty tier has its services rendered as /devops/<name> links
	for _, want := range []string{
		`href="/devops/parsers"`,  // stream
		`href="/devops/devices"`,  // stream
		`href="/devops/alert"`,    // change-feed
		`href="/devops/chirp"`,    // change-feed
		`href="/devops/casemgmt"`, // change-feed
		`href="/devops/etl"`,      // daily-batch
		`href="/devops/catalog"`,  // reference
		`href="/devops/manifest"`, // reference
		`href="/devops/api-docs"`, // reference
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tier services list missing %q", want)
		}
	}
	// Empty tier shows the placeholder
	if !strings.Contains(body, "No services declared on this tier yet") {
		t.Errorf("empty-tier placeholder missing for bulk-window")
	}

	// KPI tile counts
	if !strings.Contains(body, ">5<") {
		t.Errorf("KPI tile missing tier count 5")
	}
	if !strings.Contains(body, ">9<") {
		t.Errorf("KPI tile missing services-tracked count 9")
	}
	if !strings.Contains(body, ">4<") {
		t.Errorf("KPI tile missing cells-with-endpoints count 4")
	}
}

func TestObservability_pageDegradesWhenCatalogMissing(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = nil
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/devops/observability", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// Tier metadata still renders even when catalog is missing
	if !strings.Contains(body, "SSE / WebSocket / Valkey XREAD tail") {
		t.Errorf("tier metadata should render even when catalog missing")
	}
	// Services tracked should be muted
	if !strings.Contains(body, `class="value muted">—`) {
		t.Errorf("KPI tile should show muted dash when catalog missing")
	}
}

func TestObservability_setsNoStoreCacheControl(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.catalog = _sampleCatalogForObservability()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/devops/observability", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestObservability_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "observability" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("observability not in KnownServices()")
	}
}

func TestBuildTierRollups_dedupsServicesAcrossAxes(t *testing.T) {
	cat := &Catalog{
		Cells: []CatalogCell{
			// Same service appears in multiple cells of the same tier — should dedup
			{Axis: "B", Tier: "reference", EndpointCount: 5, Services: []string{"catalog", "manifest"}},
			{Axis: "C", Tier: "reference", EndpointCount: 3, Services: []string{"catalog"}}, // catalog repeats
			{Axis: "A", Tier: "stream", EndpointCount: 2, Services: []string{"devices"}},
		},
	}
	rollups := buildTierRollups(cat)

	if len(rollups) != len(TierCatalog) {
		t.Fatalf("rollups: got %d tiers, want %d", len(rollups), len(TierCatalog))
	}
	// Find the reference tier rollup
	var ref *TierRollup
	for i := range rollups {
		if rollups[i].Name == "reference" {
			ref = &rollups[i]
		}
	}
	if ref == nil {
		t.Fatal("reference tier rollup not found")
	}
	if ref.EndpointCount != 8 { // 5 + 3
		t.Errorf("reference EndpointCount: got %d, want 8", ref.EndpointCount)
	}
	if len(ref.Services) != 2 {
		t.Errorf("reference Services should dedup to 2, got %d (%v)", len(ref.Services), ref.Services)
	}
}

func TestBuildTierRollups_nilCatalogReturnsMetadataOnly(t *testing.T) {
	rollups := buildTierRollups(nil)
	if len(rollups) != len(TierCatalog) {
		t.Fatalf("rollups: got %d tiers, want %d", len(rollups), len(TierCatalog))
	}
	for _, r := range rollups {
		if len(r.Services) != 0 {
			t.Errorf("nil catalog tier %q should have empty Services, got %v", r.Name, r.Services)
		}
		if r.Protocol == "" {
			t.Errorf("tier %q metadata not preserved when catalog is nil", r.Name)
		}
	}
}

func TestPipeline_pageRendersFiveStages(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pipeline", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Pipeline flow",
		"Activity — Live event tracing (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("pipeline page missing zone %q", zone)
		}
	}

	// All 5 stages render with their data-tier attribute
	for _, stage := range []string{"Webhook Receipt", "Valkey Stream", "Sub1 — Seal", "Sub2 — Parse", "Sub3 — Merkle"} {
		if !strings.Contains(body, stage) {
			t.Errorf("pipeline page missing stage %q", stage)
		}
	}

	// Stage indices ("Stage 1" through "Stage 5")
	for i := 1; i <= 5; i++ {
		want := "Stage " + strconv.Itoa(i)
		if !strings.Contains(body, want) {
			t.Errorf("pipeline page missing %q", want)
		}
	}

	// Tier badges — pipeline spans stream, change-feed, daily-batch tiers
	for _, tier := range []string{`data-tier="stream"`, `data-tier="change-feed"`, `data-tier="daily-batch"`} {
		if !strings.Contains(body, tier) {
			t.Errorf("pipeline page missing tier attribute %q", tier)
		}
	}
}

func TestPipeline_pageListsBackingPackages(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pipeline", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, pkg := range []string{
		"internal/protocol/webhook",
		"internal/protocol/hmac",
		"internal/protocol/publisher",
		"internal/protocol/sub1",
		"internal/protocol/sub2",
		"internal/protocol/sub3",
	} {
		if !strings.Contains(body, pkg) {
			t.Errorf("pipeline page missing backing package %q", pkg)
		}
	}

	// KPI tile should reflect the unique-package count (6 in PipelineFlow).
	if !strings.Contains(body, ">6<") {
		t.Errorf("KPI tile should show 6 backing packages")
	}
	// Stage count tile = 5.
	if !strings.Contains(body, ">5<") {
		t.Errorf("KPI tile should show 5 stages")
	}
}

func TestPipeline_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pipeline", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestPipeline_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "pipeline" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pipeline not in KnownServices()")
	}
}

func TestPipeline_renderedSourceLinks(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pipeline", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		"docs/sdds/go-handoff/tsp.md",
		"docs/sdds/go-handoff/webhook-pipeline.md",
		"CanaryGo/internal/protocol/",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("source-files zone missing %q", want)
		}
	}
}

func TestPipelineFlow_stagesAreSequential(t *testing.T) {
	for i, st := range PipelineFlow {
		if st.Index != i+1 {
			t.Errorf("PipelineFlow[%d].Index = %d, want %d", i, st.Index, i+1)
		}
		if st.Name == "" || st.Role == "" || st.Tier == "" {
			t.Errorf("PipelineFlow[%d] has empty required field", i)
		}
		if len(st.Packages) == 0 {
			t.Errorf("PipelineFlow[%d] (%s) declares no backing packages", i, st.Name)
		}
	}
}

func TestQAAgent_pageRendersToolCategories(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/qa-agent", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Sidecar architecture",
		"Tool catalog",
		"Activity — Chat sessions + bugs filed (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("qa-agent page missing zone %q", zone)
		}
	}

	// All tool categories from the system prompt render as cards
	for _, cat := range []string{"Atlas", "Alerts", "Analytics", "Chirp", "Fox", "Identity", "Owl", "TSP", "Scenarios", "Bug filing"} {
		if !strings.Contains(body, cat) {
			t.Errorf("tool catalog missing category %q", cat)
		}
	}
}

func TestQAAgent_pageListsAllTools(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/qa-agent", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// Spot-check tools across categories — verbatim from system prompt
	for _, tool := range []string{
		"atlas_figure", "atlas_search",
		"list_alerts", "rank_alerts",
		"get_dashboard", "score_metrics",
		"validate_thresholds",
		"list_cases", "verify_chain",
		"get_merchant", "list_employees",
		"knowledge_search",
		"get_stream_health", "get_dead_letters",
		"fire_scenario", "list_scenarios",
		"file_linear_bug",
	} {
		if !strings.Contains(body, tool) {
			t.Errorf("tool catalog missing tool %q", tool)
		}
	}

	// KPI tile counts: 10 categories, sum of all tools across QAToolCatalog
	if !strings.Contains(body, ">10<") {
		t.Errorf("KPI tile should show 10 tool categories")
	}
	// Total tool count is whatever len() across QAToolCatalog gives — assert it lands somewhere
	totalTools := 0
	for _, c := range QAToolCatalog {
		totalTools += len(c.Tools)
	}
	if totalTools < 30 {
		t.Errorf("expected at least 30 tools across catalog, got %d", totalTools)
	}
}

func TestQAAgent_pageRendersSidecarContract(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/qa-agent", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"Merchant page",
		"Go gateway",
		"Sidecar",
		"Anthropic SDK",
		"Page: &lt;path&gt;",      // page-context contract (HTML-escaped angle brackets)
		"Merchant: &lt;uuid&gt;",  // RLS context
		"50 msg/session",          // session limit
		"200 msg/day",             // daily budget
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sidecar contract section missing %q", want)
		}
	}
}

func TestQAAgent_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/qa-agent", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestQAAgent_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "qa-agent" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("qa-agent not in KnownServices()")
	}
}

func TestQAToolCatalog_invariants(t *testing.T) {
	if len(QAToolCatalog) < 9 {
		t.Errorf("QAToolCatalog should have at least 9 categories (Atlas/Alerts/Analytics/Chirp/Fox/Identity/Owl/TSP/Scenarios), got %d", len(QAToolCatalog))
	}
	for _, c := range QAToolCatalog {
		if c.Name == "" || c.Role == "" {
			t.Errorf("category has empty Name or Role: %+v", c)
		}
		if len(c.Tools) == 0 {
			t.Errorf("category %q declares no tools", c.Name)
		}
	}
}

func TestETL_pageRendersTwoStages(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/etl", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Pipeline stages",
		"Fact tables",
		"Activity — Run history (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("etl page missing zone %q", zone)
		}
	}

	// Both stages render
	for _, stage := range []string{"Daily aggregation", "Period rollup"} {
		if !strings.Contains(body, stage) {
			t.Errorf("etl page missing stage %q", stage)
		}
	}
	for i := 1; i <= 2; i++ {
		want := "Stage " + strconv.Itoa(i)
		if !strings.Contains(body, want) {
			t.Errorf("etl page missing %q", want)
		}
	}
}

func TestETL_pageListsSixFactTables(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/etl", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// All 6 fact tables from etl_runner.py METRICS_TABLES render
	for _, tbl := range []string{
		"daily_metrics",
		"hourly_metrics",
		"employee_daily_metrics",
		"product_daily_metrics",
		"period_metrics",
		"employee_period_metrics",
	} {
		if !strings.Contains(body, tbl) {
			t.Errorf("etl page missing fact table %q", tbl)
		}
	}

	// KPI tile counts: 2 stages, 6 tables
	if !strings.Contains(body, ">2<") {
		t.Errorf("KPI tile should show 2 stages")
	}
	if !strings.Contains(body, ">6<") {
		t.Errorf("KPI tile should show 6 fact tables")
	}
}

func TestETL_pageRendersSourceLinks(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/etl", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		"Canary/canary/services/metrics_etl.py",
		"Canary/canary/services/etl_runner.py",
		"Canary/canary/services/period_aggregation.py",
		"docs/sdds/go-handoff/data-model.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("source-files zone missing %q", want)
		}
	}
}

func TestETL_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/etl", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestETL_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "etl" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("etl not in KnownServices()")
	}
}

func TestETLPipeline_invariants(t *testing.T) {
	if len(ETLPipeline) != 2 {
		t.Errorf("ETLPipeline should have exactly 2 stages, got %d", len(ETLPipeline))
	}
	for i, s := range ETLPipeline {
		if s.Index != i+1 {
			t.Errorf("ETLPipeline[%d].Index = %d, want %d", i, s.Index, i+1)
		}
		if s.Name == "" || s.Role == "" || s.Source == "" {
			t.Errorf("ETLPipeline[%d] has empty required field", i)
		}
		if len(s.Tables) == 0 {
			t.Errorf("ETLPipeline[%d] (%s) declares no tables", i, s.Name)
		}
	}
}

func TestETLTableCatalog_invariants(t *testing.T) {
	if len(ETLTableCatalog) != 6 {
		t.Errorf("ETLTableCatalog should have exactly 6 fact tables, got %d", len(ETLTableCatalog))
	}
	for _, tbl := range ETLTableCatalog {
		if tbl.Name == "" || tbl.Role == "" || tbl.Cadence == "" || tbl.GroupedBy == "" {
			t.Errorf("table catalog row has empty field: %+v", tbl)
		}
		if tbl.Stage != 1 && tbl.Stage != 2 {
			t.Errorf("table %q has invalid Stage %d (must be 1 or 2)", tbl.Name, tbl.Stage)
		}
	}
}

func TestWallet_pageRendersL402Flow(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/wallet", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"L402 paywall flow",
		"Wallet state machine",
		"Goose modules",
		"Activity — Ledger + pending invoices (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("wallet page missing zone %q", zone)
		}
	}

	// All 5 L402 steps render. html/template escapes "+" to "&#43;"; the
	// 402+invoice step name contains a +, so assert on the distinctive
	// "402" + "invoice" pair around the escaped plus.
	for _, step := range []string{"Request", "Pay (Lightning)", "Macaroon issued", "Retry"} {
		if !strings.Contains(body, step) {
			t.Errorf("L402 flow missing step %q", step)
		}
	}
	if !strings.Contains(body, "402 &#43; invoice") {
		t.Errorf("L402 flow missing escaped 402+invoice step")
	}
	for i := 1; i <= 5; i++ {
		want := "Step " + strconv.Itoa(i)
		if !strings.Contains(body, want) {
			t.Errorf("L402 flow missing %q", want)
		}
	}
}

func TestWallet_pageListsAllNineModules(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/wallet", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// All 9 Goose modules render verbatim from Python source
	for _, mod := range []string{
		"wallet_service.py",
		"treasury.py",
		"strike_client.py",
		"l402_middleware.py",
		"macaroon_service.py",
		"gas_meter.py",
		"gas_metered.py",
		"onboarding.py",
		"seed.py",
	} {
		if !strings.Contains(body, mod) {
			t.Errorf("module catalog missing %q", mod)
		}
	}

	// KPI tile counts
	if !strings.Contains(body, ">9<") {
		t.Errorf("KPI tile should show 9 modules")
	}
	if !strings.Contains(body, ">4<") {
		t.Errorf("KPI tile should show 4 wallet states")
	}
}

func TestWallet_pageRendersStateMachine(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/wallet", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// All 4 states render with their data-state attribute (color border hook)
	for _, state := range []string{"active", "warning", "depleted", "suspended"} {
		if !strings.Contains(body, `data-state="`+state+`"`) {
			t.Errorf("state machine missing state attribute %q", state)
		}
	}
}

func TestWallet_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/wallet", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestWallet_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "wallet" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("wallet not in KnownServices()")
	}
}

func TestGooseModules_invariants(t *testing.T) {
	if len(GooseModules) != 9 {
		t.Errorf("GooseModules should have exactly 9 modules, got %d", len(GooseModules))
	}
	for _, m := range GooseModules {
		if m.Name == "" || m.Role == "" {
			t.Errorf("module has empty Name or Role: %+v", m)
		}
		if len(m.Owns) == 0 {
			t.Errorf("module %q declares no owned types", m.Name)
		}
	}
}

func TestWalletStateMachine_invariants(t *testing.T) {
	if len(WalletStateMachine) != 4 {
		t.Errorf("WalletStateMachine should have exactly 4 states, got %d", len(WalletStateMachine))
	}
	expectedStates := []string{"active", "warning", "depleted", "suspended"}
	for i, s := range WalletStateMachine {
		if s.Name != expectedStates[i] {
			t.Errorf("WalletStateMachine[%d].Name = %q, want %q", i, s.Name, expectedStates[i])
		}
		if s.Description == "" || s.Transition == "" {
			t.Errorf("state %q has empty Description or Transition", s.Name)
		}
	}
}

func TestL402Flow_invariants(t *testing.T) {
	if len(L402Flow) != 5 {
		t.Errorf("L402Flow should have exactly 5 steps, got %d", len(L402Flow))
	}
	for i, s := range L402Flow {
		if s.Index != i+1 {
			t.Errorf("L402Flow[%d].Index = %d, want %d", i, s.Index, i+1)
		}
		if s.Name == "" || s.Role == "" {
			t.Errorf("L402Flow[%d] has empty required field", i)
		}
	}
}

func TestTestLab_pageRendersTwoTabs(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/test-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	// Six-zone canonical layout
	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Console tabs",
		"Pipeline progress",
		"Sandbox guard",
		"Activity — Run history (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("test-lab page missing zone %q", zone)
		}
	}

	// Both tabs render
	for _, tab := range []string{"Scenarios", "Live Fire"} {
		if !strings.Contains(body, tab) {
			t.Errorf("test-lab page missing tab %q", tab)
		}
	}
}

func TestTestLab_pageRendersFiveStages(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/test-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, stage := range []string{"Square API", "Webhook", "Chirp", "Complete", "Owl Verify"} {
		if !strings.Contains(body, stage) {
			t.Errorf("pipeline stages missing %q", stage)
		}
	}
	for i := 1; i <= 5; i++ {
		want := "Stage " + strconv.Itoa(i)
		if !strings.Contains(body, want) {
			t.Errorf("pipeline progress missing %q", want)
		}
	}

	// KPI tile counts: 2 tabs, 5 stages
	if !strings.Contains(body, ">2<") {
		t.Errorf("KPI tile should show 2 tabs")
	}
	if !strings.Contains(body, ">5<") {
		t.Errorf("KPI tile should show 5 stages")
	}
}

func TestTestLab_pageRendersSandboxGuard(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/test-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"Sandbox guard",
		`SQUARE_ENVIRONMENT == "sandbox"`,
		"admin role",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sandbox guard zone missing %q", want)
		}
	}
}

func TestTestLab_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/test-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestTestLab_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "test-lab" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("test-lab not in KnownServices()")
	}
}

func TestTestLabTabs_invariants(t *testing.T) {
	if len(TestLabTabs) != 2 {
		t.Errorf("TestLabTabs should have exactly 2 tabs, got %d", len(TestLabTabs))
	}
	for _, tab := range TestLabTabs {
		if tab.Name == "" || tab.Role == "" {
			t.Errorf("tab has empty Name or Role: %+v", tab)
		}
		if len(tab.Tags) == 0 {
			t.Errorf("tab %q declares no tags", tab.Name)
		}
	}
}

func TestTestLabStages_invariants(t *testing.T) {
	if len(TestLabStages) != 5 {
		t.Errorf("TestLabStages should have exactly 5 stages, got %d", len(TestLabStages))
	}
	for i, s := range TestLabStages {
		if s.Index != i+1 {
			t.Errorf("TestLabStages[%d].Index = %d, want %d", i, s.Index, i+1)
		}
		if s.Name == "" || s.Role == "" {
			t.Errorf("TestLabStages[%d] has empty required field", i)
		}
	}
}

func TestCategories_includesTestValidationGroup(t *testing.T) {
	found := false
	for _, g := range Categories {
		if g.Title == "Test & validation" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Categories should include a 'Test & validation' group after T3B.7")
	}
}

func TestScenarios_pageRendersAllEightScenarios(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/scenarios", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Scripted scenario catalog",
		"Randomizer mode",
		"Activity — Run history (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("scenarios page missing zone %q", zone)
		}
	}

	// All 8 scenarios render verbatim (Name + Key)
	for _, want := range []string{
		"Happy Path Payment", "happy_path_payment",
		"Rapid Refund", "refund_detection",
		"Void Pattern", "void_pattern",
		"Cash Drawer Variance", "cash_drawer_variance",
		"Employee Risk Score", "employee_risk_score",
		"Multi-Location", "multi_location",
		"Fox Case Lifecycle", "fox_case_lifecycle",
		"Full Pipeline E2E", "pipeline_e2e",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scenarios catalog missing %q", want)
		}
	}
}

func TestScenarios_pageRendersExpectedRules(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/scenarios", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// Distinct Chirp rule codes from the SCENARIOS dict
	for _, rule := range []string{"C-001", "C-002", "C-007", "C-102"} {
		if !strings.Contains(body, rule) {
			t.Errorf("scenarios catalog missing rule pill %q", rule)
		}
	}

	// "no rule expected to fire" placeholder for rule-less scenarios
	if !strings.Contains(body, "no rule expected to fire") {
		t.Errorf("scenarios catalog missing 'no rule expected' placeholder")
	}
}

func TestScenarios_pageRendersRandomizer(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/scenarios", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"Randomizer mode",
		"run_randomizer(count = 10)",
		"purge_scenario_data()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("randomizer panel missing %q", want)
		}
	}

	// KPI counts: 8 scenarios, 10 randomizer default, 4 distinct rules, 3 query types
	if !strings.Contains(body, ">8<") {
		t.Errorf("KPI tile should show 8 scenarios")
	}
	if !strings.Contains(body, ">10<") {
		t.Errorf("KPI tile should show 10 randomizer default")
	}
	if !strings.Contains(body, ">4<") {
		t.Errorf("KPI tile should show 4 distinct rules")
	}
}

func TestScenarios_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/scenarios", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestScenarios_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "scenarios" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("scenarios not in KnownServices()")
	}
}

func TestScenariosCatalog_invariants(t *testing.T) {
	if len(ScenariosCatalog) != 8 {
		t.Errorf("ScenariosCatalog should have exactly 8 scenarios, got %d", len(ScenariosCatalog))
	}
	keys := map[string]bool{}
	for _, s := range ScenariosCatalog {
		if s.Key == "" || s.Name == "" || s.Desc == "" {
			t.Errorf("scenario has empty required field: %+v", s)
		}
		if keys[s.Key] {
			t.Errorf("duplicate scenario key %q", s.Key)
		}
		keys[s.Key] = true
		if len(s.VerificationQs) == 0 {
			t.Errorf("scenario %q has no verification queries", s.Key)
		}
	}
}

func TestPosLab_pageRendersTwoModes(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pos-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"Fire modes",
		"Parameter space",
		"Activity — Fire history (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("pos-lab page missing zone %q", zone)
		}
	}

	// Both modes render with their data-mode attr
	for _, mode := range []string{`data-mode="Local Fire"`, `data-mode="Live Fire"`} {
		if !strings.Contains(body, mode) {
			t.Errorf("pos-lab page missing mode attr %q", mode)
		}
	}

	// Latency labels visible
	for _, lat := range []string{"~50ms", "~3-5s"} {
		if !strings.Contains(body, lat) {
			t.Errorf("pos-lab page missing latency %q", lat)
		}
	}
}

func TestPosLab_pageListsParameterSpace(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pos-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	// All 9 transaction types
	for _, tt := range []string{"SALE", "REFUND", "RETURN", "VOID", "POST_VOID", "NO_SALE", "PAID_IN", "PAID_OUT", "EXCHANGE"} {
		if !strings.Contains(body, tt) {
			t.Errorf("transaction types missing %q", tt)
		}
	}
	// Entry methods (6)
	for _, em := range []string{"CHIP", "CONTACTLESS", "KEYED", "SWIPED", "MANUAL", "ON_FILE"} {
		if !strings.Contains(body, em) {
			t.Errorf("entry methods missing %q", em)
		}
	}
	// Card brands (5)
	for _, cb := range []string{"VISA", "MASTERCARD", "AMEX", "DISCOVER", "JCB"} {
		if !strings.Contains(body, cb) {
			t.Errorf("card brands missing %q", cb)
		}
	}

	// KPI tile counts
	if !strings.Contains(body, ">2<") {
		t.Errorf("KPI tile should show 2 fire modes")
	}
	if !strings.Contains(body, ">9<") {
		t.Errorf("KPI tile should show 9 transaction types")
	}
	if !strings.Contains(body, ">6<") {
		t.Errorf("KPI tile should show 6 entry methods")
	}
	if !strings.Contains(body, ">5<") {
		t.Errorf("KPI tile should show 5 card brands")
	}
}

func TestPosLab_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/pos-lab", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestPosLab_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "pos-lab" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pos-lab not in KnownServices()")
	}
}

func TestPosLabModes_invariants(t *testing.T) {
	if len(PosLabModes) != 2 {
		t.Errorf("PosLabModes should have exactly 2 modes, got %d", len(PosLabModes))
	}
	want := map[string]bool{"Local Fire": true, "Live Fire": true}
	for _, m := range PosLabModes {
		if !want[m.Name] {
			t.Errorf("unexpected mode name %q", m.Name)
		}
		if m.Latency == "" || m.Path == "" || m.Tests == "" {
			t.Errorf("mode %q has empty required field", m.Name)
		}
	}
}

func TestPosLabParameterSpaces_invariants(t *testing.T) {
	if len(PosLabTransactionTypes) != 9 {
		t.Errorf("PosLabTransactionTypes count = %d, want 9", len(PosLabTransactionTypes))
	}
	if len(PosLabEntryMethods) != 6 {
		t.Errorf("PosLabEntryMethods count = %d, want 6", len(PosLabEntryMethods))
	}
	if len(PosLabCardBrands) != 5 {
		t.Errorf("PosLabCardBrands count = %d, want 5", len(PosLabCardBrands))
	}
}

func TestLiveFire_pageRendersActions(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/live-fire", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	for _, zone := range []string{
		"Capability",
		"KPIs",
		"Endpoints (Source files)",
		"SDD-064 invariant",
		"Action grammar",
		"Square API surface",
		"Activity — Fire history (planned)",
		"Linked",
	} {
		if !strings.Contains(body, zone) {
			t.Errorf("live-fire page missing zone %q", zone)
		}
	}

	// All 6 action types render verbatim
	for _, act := range []string{"order", "payment", "refund", "partial_refund", "cancel", "seed"} {
		if !strings.Contains(body, act) {
			t.Errorf("action grammar missing %q", act)
		}
	}
}

func TestLiveFire_pageRendersSquareAPISurface(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/live-fire", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"Orders API",
		"Payments API",
		"Refunds API",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Square API surface missing %q", want)
		}
	}

	// KPI tile counts: 6 actions, 20 scenarios, 3 APIs
	if !strings.Contains(body, ">6<") {
		t.Errorf("KPI tile should show 6 action types")
	}
	if !strings.Contains(body, ">20<") {
		t.Errorf("KPI tile should show 20 scenarios")
	}
	if !strings.Contains(body, ">3<") {
		t.Errorf("KPI tile should show 3 Square APIs")
	}
}

func TestLiveFire_pageRendersSDD064Invariant(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/live-fire", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()

	for _, want := range []string{
		"SDD-064 invariant",
		"Real pipeline only",
		"does NOT generate synthetic data",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("invariant box missing %q", want)
		}
	}
}

func TestLiveFire_setsNoStoreCacheControl(t *testing.T) {
	r := newRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/devops/live-fire", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control: got %q, want no-store", got)
	}
}

func TestLiveFire_includedInKnownServices(t *testing.T) {
	h, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, n := range h.KnownServices() {
		if n == "live-fire" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("live-fire not in KnownServices()")
	}
}

func TestLiveFireActions_invariants(t *testing.T) {
	if len(LiveFireActions) != 6 {
		t.Errorf("LiveFireActions count = %d, want 6", len(LiveFireActions))
	}
	want := map[string]bool{
		"order": true, "payment": true, "refund": true,
		"partial_refund": true, "cancel": true, "seed": true,
	}
	for _, a := range LiveFireActions {
		if !want[a.Name] {
			t.Errorf("unexpected action name %q", a.Name)
		}
		if a.Role == "" || a.API == "" {
			t.Errorf("action %q has empty Role or API", a.Name)
		}
	}
}

func TestSquareAPISurfaces_invariants(t *testing.T) {
	if len(SquareAPISurfaces) != 3 {
		t.Errorf("SquareAPISurfaces count = %d, want 3", len(SquareAPISurfaces))
	}
	for _, api := range SquareAPISurfaces {
		if api.Name == "" || api.Used == "" {
			t.Errorf("API surface row has empty field: %+v", api)
		}
	}
}
