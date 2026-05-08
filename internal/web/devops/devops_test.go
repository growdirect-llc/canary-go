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
