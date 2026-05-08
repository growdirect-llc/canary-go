// Package devops implements the platform-operator sysadmin module
// at /devops/<service>. Each service renders the canonical six-zone
// service-detail-page (header · capability · KPI strip · endpoints ·
// service body · activity · linked) inside a shared shell with a
// grouped service sidebar and a tenant/env top nav.
//
// T2.0 / GRO-840 of the sysadmin module epic. Phase 2 of
// the build sequence captured in
// docs/superpowers/specs/2026-05-07-sysadmin-module-design.md.
//
// Subsequent backfill tickets (T2.1–T2.33) wire one real service at a
// time. T2.0 ships only the chrome — every service body says "TODO".
//
// Path strategy: this package mounts /devops/<service> for the
// services it knows about. The pre-existing internal/devops/ package
// (DEV_CONSOLE-gated pipeline/square/api/releases) keeps its routes;
// they don't collide with the sysadmin paths. Phase 3 (T3B.3 +
// existing /admin/* fold-in) folds the legacy pages into the new
// shell.
package devops

import (
	"bufio"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Drift is a parsed summary of services/canary-protocol/manifest/build/drift-report.txt.
// The reconcile.py emitter writes a fixed-shape report; this struct captures
// the counts that the manifest viewer surfaces. Empty Drift = report missing
// (the page degrades to an explanatory placeholder).
//
// Field names match the reconcile.py "Counts" + "Categories" labels so the
// template can render them without translation.
type Drift struct {
	Loaded            bool
	ManifestEndpoints int
	MountedEndpoints  int
	OpenAPIEndpoints  int
	Matched           int
	ManifestOnly      int
	OpenAPIOnly       int
	RoutesOnly        int // unaccounted — Phase 1 Gate metric
	GatePass          bool
}

// loadDrift parses build/drift-report.txt summary block. Tolerant of missing
// file; returns zero-value Drift{Loaded: false} on any failure. The reconcile
// emitter format is stable per spec §"reconcile.py" — see services/canary-
// protocol/manifest/gen/reconcile.py.
func loadDrift(logger *zap.Logger) *Drift {
	candidates := []string{
		"services/canary-protocol/manifest/build/drift-report.txt",
		"../services/canary-protocol/manifest/build/drift-report.txt",
		"../../services/canary-protocol/manifest/build/drift-report.txt",
	}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		d := &Drift{Loaded: true}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "manifest endpoints:"):
				d.ManifestEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "mounted endpoints:"):
				d.MountedEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "openapi endpoints:"):
				d.OpenAPIEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "matched:"):
				d.Matched = parseTrailingInt(line)
			case strings.HasPrefix(line, "manifest-only:"):
				d.ManifestOnly = parseTrailingInt(line)
			case strings.HasPrefix(line, "openapi-only:"):
				d.OpenAPIOnly = parseTrailingInt(line)
			case strings.HasPrefix(line, "routes-only (UNACCOUNTED):"):
				d.RoutesOnly = parseTrailingInt(line)
			case strings.Contains(line, "Phase 1 Gate"):
				d.GatePass = strings.Contains(line, "PASS")
			}
		}
		_ = f.Close()
		abs, _ := filepath.Abs(c)
		logger.Info("manifest: drift-report loaded",
			zap.String("path", abs),
			zap.Int("manifest_endpoints", d.ManifestEndpoints),
			zap.Int("manifest_only", d.ManifestOnly),
			zap.Int("openapi_only", d.OpenAPIOnly),
			zap.Bool("gate_pass", d.GatePass),
		)
		return d
	}
	logger.Warn("manifest: drift-report.txt not found; manifest page Activity zone will degrade")
	return &Drift{}
}

// TierInfo is the per-tier metadata block surfaced on /devops/observability.
// Values are pinned to spec §"Per-tier infrastructure" — change here if the
// spec evolves. Field shape mirrors the table columns of that spec section.
type TierInfo struct {
	Name      string // tier key — matches manifest.yaml's `tiers` array entries
	Protocol  string
	Auth      string
	RateLimit string
	Health    string
	Cache     string
	Recovery  string
}

// TierCatalog is the canonical per-tier metadata. Order matches the cadence-
// ladder progression (fastest → slowest); this is the order the
// observability page renders the tier cards.
var TierCatalog = []TierInfo{
	{
		Name:      "stream",
		Protocol:  "SSE / WebSocket / Valkey XREAD tail",
		Auth:      "Long-lived token",
		RateLimit: "Concurrent connections",
		Health:    "Heartbeat (no msg in N sec)",
		Cache:     "N/A",
		Recovery:  "Replay from queue",
	},
	{
		Name:      "change-feed",
		Protocol:  "REST polling with cursor / sub with watermark",
		Auth:      "API key per request",
		RateLimit: "Requests/min per key",
		Health:    "Lag exceeded / queue depth",
		Cache:     "Short TTL (~5 min)",
		Recovery:  "Catch up from watermark",
	},
	{
		Name:      "daily-batch",
		Protocol:  "Cron-driven export",
		Auth:      "Service-to-service signed",
		RateLimit: "Rate-of-runs",
		Health:    "Schedule missed / checksum diff",
		Cache:     "N/A",
		Recovery:  "Rerun the job",
	},
	{
		Name:      "bulk-window",
		Protocol:  "Scheduled file drop / blob",
		Auth:      "Signed URL",
		RateLimit: "Window-bounded",
		Health:    "Didn't land / row count off",
		Cache:     "N/A",
		Recovery:  "Reschedule",
	},
	{
		Name:      "reference",
		Protocol:  "REST GET with strong cache headers",
		Auth:      "API key (anon-read possible)",
		RateLimit: "High requests/min",
		Health:    "Version drift",
		Cache:     "Long TTL (~60s hot)",
		Recovery:  "Force resync",
	},
}

// TierRollup is the rendered shape — one card per tier with services list
// rolled up across all axes. Used by observabilityPage.
type TierRollup struct {
	TierInfo
	EndpointCount int
	Services      []string // dedup-sorted across A/B/C axes for the tier
}

// parseTrailingInt extracts the trailing integer from lines like
// "manifest endpoints:   80" or "manifest-only:                 78".
// Returns 0 on parse failure (the field stays zero-valued — the template
// renders 0, indistinguishable from a real zero count).
func parseTrailingInt(line string) int {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0
	}
	last := fields[len(fields)-1]
	// Strip parens / commas if present (reconcile output is plain ints today,
	// but be defensive against future format tweaks).
	last = strings.TrimRight(last, ".,)")
	last = strings.TrimLeft(last, "(")
	n, err := strconv.Atoi(last)
	if err != nil {
		return 0
	}
	return n
}

// Catalog is the JSON shape emitted by parse_manifest.py's
// build_catalog() — see services/canary-protocol/manifest/gen/.
// Keep the field tags in lockstep with that emitter.
//
// Deliberately no GeneratedAt field — the catalog is content-addressable
// via the input-file SHAs in manifest.yaml's generated_from. Including a
// wall-clock timestamp causes git churn on every `make manifest` run.
type Catalog struct {
	Axes     []CatalogAxis `json:"axes"`
	Tiers    []string      `json:"tiers"`
	Cells    []CatalogCell `json:"cells"`
	Services []CatalogSvc  `json:"services"`
	Totals   CatalogTotals `json:"totals"`
}

type CatalogAxis struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type CatalogCell struct {
	Axis          string   `json:"axis"`
	Tier          string   `json:"tier"`
	EndpointCount int      `json:"endpoint_count"`
	Services      []string `json:"services"`
}

type CatalogSvc struct {
	Name           string   `json:"name"`
	Port           *int     `json:"port"`
	Priority       string   `json:"priority"`
	Category       string   `json:"category"`
	Scope          string   `json:"scope"`
	Owner          string   `json:"owner"`
	Cells          []string `json:"cells"`
	EndpointCount  int      `json:"endpoint_count"`
}

type CatalogTotals struct {
	ServiceCount  int `json:"service_count"`
	EndpointCount int `json:"endpoint_count"`
}

//go:embed templates
var embedFS embed.FS

// Service is the metadata block rendered in the service-detail-page
// header + capability zone. T2.0 carries hardcoded definitions for
// the 5 Phase-1 skeleton services. T2.34 (catalog page) loads the
// full set from manifest.yaml.
type Service struct {
	Name           string   // catalog
	Port           int      // 9100
	Owner          string   // ALX
	Priority       string   // P0
	Scope          string   // cross-tenant
	Category       string   // cross-tenant infra
	Cells          []string // ["B × reference"]
	Status         string   // one-line description of the service surface
}

// Group is a sidebar grouping. Order in the sidebar follows the
// order of Categories below.
type Group struct {
	Title    string
	Services []Service
}

// Categories drives sidebar ordering. The full set is per spec
// §"Service inventory"; T2.0 lists only the 5 skeletons. Phase 2
// backfill tickets append.
var Categories = []Group{
	{
		Title: "Cross-tenant infra",
		Services: []Service{
			{
				Name: "catalog", Port: 9100, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "3×5 grid heat-map of every endpoint by axis × tier — wired in T2.34.",
			},
			{
				Name: "manifest", Port: 9101, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "Manifest editor + validator + history viewer — wired in T3B.1.",
			},
			{
				Name: "observability", Port: 9102, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × change-feed", "B × reference"},
				Status: "Five-tier health rollup with per-service drill-down — wired in T3B.2.",
			},
			{
				Name: "pipeline", Port: 9103, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × change-feed"},
				Status:       "TSP pipeline visualization (Webhook → Sub1 → Sub2 → Sub3) — wired in T3B.3.",
			},
			{
				Name: "qa-agent", Port: 9104, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"C × change-feed"},
				Status:       "Page-aware operator agent with cross-service MCP tools — wired in T3B.4.",
			},
			{
				Name: "api-docs", Port: 9105, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "Redoc-rendered OpenAPI 3.0 spec — wired in T2.35.",
			},
			{
				Name: "evidence", Port: 9201, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				Status:       "Append-only protocol audit log query UI — Phase 3 wires the operator workflow.",
			},
			{
				Name: "anchor", Port: 9202, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				Status:       "Merkle anchor batch viewer + Bitcoin L2 proof inspector — Phase 3.",
			},
			{
				Name: "mcp", Port: 9203, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"C × change-feed"},
				Status: "MCP tool catalog + per-tenant usage rollup — Phase 3 builds drill-down.",
			},
		},
	},
}

// Handler renders the sysadmin module shell.
type Handler struct {
	tmpl       *template.Template
	logger     *zap.Logger
	index      map[string]*Service // name → metadata, built once at New()
	openAPIRaw []byte              // openapi.yaml loaded once at boot for /devops/api-docs/openapi.yaml
	catalog    *Catalog            // devops-catalog.json loaded once at boot for /devops/catalog
	drift      *Drift              // build/drift-report.txt summary, loaded once at boot for /devops/manifest
}

// New constructs a Handler. Returns an error if templates fail to
// parse — main.go logs and continues without mounting. openAPIPath
// is the absolute or repo-relative path to openapi.yaml; if empty
// the loader tries a couple of common locations. Failure to load
// the spec is logged and api-docs degrades to a placeholder, but
// the rest of the shell still mounts.
func New(logger *zap.Logger) (*Handler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	tmpl, err := template.ParseFS(embedFS,
		"templates/shell.html",
		"templates/sidebar.html",
		"templates/api_docs.html",
		"templates/catalog.html",
		"templates/manifest.html",
		"templates/observability.html",
	)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]*Service)
	for gi := range Categories {
		for si := range Categories[gi].Services {
			s := &Categories[gi].Services[si]
			idx[s.Name] = s
		}
	}
	h := &Handler{tmpl: tmpl, logger: logger, index: idx}
	h.openAPIRaw = loadOpenAPI(logger)
	h.catalog = loadCatalog(logger)
	h.drift = loadDrift(logger)
	return h, nil
}

// loadCatalog reads devops-catalog.json from the same candidate set as
// loadOpenAPI. Returns nil if the file is missing or unparseable; the
// catalog page degrades to a placeholder.
func loadCatalog(logger *zap.Logger) *Catalog {
	candidates := []string{
		"services/canary-protocol/manifest/devops-catalog.json",
		"../services/canary-protocol/manifest/devops-catalog.json",
		"../../services/canary-protocol/manifest/devops-catalog.json",
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		var cat Catalog
		if err := json.Unmarshal(data, &cat); err != nil {
			abs, _ := filepath.Abs(c)
			logger.Warn("catalog: devops-catalog.json malformed",
				zap.String("path", abs), zap.Error(err))
			continue
		}
		abs, _ := filepath.Abs(c)
		logger.Info("catalog: devops-catalog.json loaded",
			zap.String("path", abs),
			zap.Int("services", cat.Totals.ServiceCount),
			zap.Int("endpoints", cat.Totals.EndpointCount),
		)
		return &cat
	}
	logger.Warn("catalog: devops-catalog.json not found in any candidate location " +
		"(catalog page will render placeholder; run `make manifest`)")
	return nil
}

// loadOpenAPI reads openapi.yaml from a couple of candidate locations
// relative to the current working directory. Gateway boots with cwd
// set by deploy/start scripts; the candidates cover repo-root runs and
// CanaryGo-rooted runs. Returns nil if no file is found — the api-docs
// page degrades gracefully.
func loadOpenAPI(logger *zap.Logger) []byte {
	candidates := []string{
		"services/canary-protocol/openapi/openapi.yaml",
		"../services/canary-protocol/openapi/openapi.yaml",
		"../../services/canary-protocol/openapi/openapi.yaml",
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		data, err := os.ReadFile(c)
		if err == nil && len(data) > 0 {
			logger.Info("api-docs: openapi.yaml loaded",
				zap.String("path", abs),
				zap.Int("bytes", len(data)),
			)
			return data
		}
	}
	logger.Warn("api-docs: openapi.yaml not found in any candidate location " +
		"(api-docs page will render placeholder)")
	return nil
}

// Mount registers a route per known service. The runner registers
// these specifically (rather than a /devops/{service} catch-all) so
// chi can detect collisions cleanly during boot and so the existing
// internal/devops/ package's specific routes (/devops/square,
// /devops/api, /devops/releases) keep working.
//
// Special services with custom handlers (api-docs renders Redoc, not
// the generic shell) are routed before the generic loop.
func (h *Handler) Mount(r chi.Router) {
	// Special routes — rendered with custom handlers, NOT the generic
	// six-zone shell. T2.35: api-docs serves Redoc + raw openapi.yaml.
	// T2.34: catalog renders the 3×5 grid heat-map from devops-catalog.json.
	r.Get("/devops/api-docs", h.apiDocsPage)
	r.Get("/devops/api-docs/openapi.yaml", h.apiDocsSpec)
	r.Get("/devops/catalog", h.catalogPage)
	r.Get("/devops/manifest", h.manifestPage)
	r.Get("/devops/observability", h.observabilityPage)

	for name := range h.index {
		switch name {
		case "api-docs", "catalog", "manifest", "observability":
			continue // mounted above with custom handlers
		}
		path := "/devops/" + name
		r.Get(path, h.servicePage)
	}
	h.logger.Info("sysadmin module mounted",
		zap.Int("services", len(h.index)),
		zap.String("path_prefix", "/devops/"),
	)
}

// KnownServices returns the set of service names the shell knows
// about. Useful for tests and for the Phase 2 catalog page when it
// needs to enumerate manifest entries.
func (h *Handler) KnownServices() []string {
	out := make([]string, 0, len(h.index))
	for k := range h.index {
		out = append(out, k)
	}
	return out
}

func (h *Handler) servicePage(w http.ResponseWriter, r *http.Request) {
	// Extract service name from the path. r.URL.Path is /devops/<name>;
	// cut the prefix.
	const prefix = "/devops/"
	name := r.URL.Path
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		name = name[len(prefix):]
	}
	svc, ok := h.index[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     name,
		"Tenant":     "all",
		"Env":        "lab",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "shell.html", view); err != nil {
		h.logger.Error("shell template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) apiDocsPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["api-docs"]
	view := map[string]any{
		"Service":     svc,
		"Categories":  Categories,
		"Active":      "api-docs",
		"Tenant":      "all",
		"Env":         "lab",
		"SpecURL":     "/devops/api-docs/openapi.yaml",
		"SpecLoaded":  len(h.openAPIRaw) > 0,
		"SpecBytes":   len(h.openAPIRaw),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "api_docs.html", view); err != nil {
		h.logger.Error("api-docs template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) apiDocsSpec(w http.ResponseWriter, r *http.Request) {
	if len(h.openAPIRaw) == 0 {
		http.Error(w, "openapi.yaml not loaded", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(h.openAPIRaw)
}

// catalogPage renders the 3×5 axis-tier heat-map plus a service list.
// Manifest-driven: data comes from devops-catalog.json loaded at boot.
// When the file is absent, a placeholder explains how to regenerate.
func (h *Handler) catalogPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["catalog"]
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     "catalog",
		"Tenant":     "all",
		"Env":        "lab",
		"Catalog":    h.catalog,
		"Loaded":     h.catalog != nil,
	}
	if h.catalog != nil {
		// Pre-arrange cells into a row-per-axis structure for the template:
		// rows[i] = the 5 cells of axis i in tier order.
		rows := make(map[string][]CatalogCell, len(h.catalog.Axes))
		for _, c := range h.catalog.Cells {
			rows[c.Axis] = append(rows[c.Axis], c)
		}
		// Compute max endpoint count for heat-color scaling.
		maxCount := 0
		for _, c := range h.catalog.Cells {
			if c.EndpointCount > maxCount {
				maxCount = c.EndpointCount
			}
		}
		view["GridRows"] = rows
		view["MaxCellCount"] = maxCount
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "catalog.html", view); err != nil {
		h.logger.Error("catalog template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// manifestPage renders the /devops/manifest viewer — the operator surface
// for understanding current manifest state. Sources:
//   - h.catalog (devops-catalog.json)         → service inventory + totals
//   - h.drift   (build/drift-report.txt)      → reconciliation summary
//
// Six zones per spec §"Service-detail-page common shape":
//   Header     — name + port + priority + scope (T2.0 pattern)
//   Capability — owner + cells (T2.0 pattern)
//   KPI strip  — services count · endpoints · gate status · drift
//   Endpoints  — links to source files (canary-go-portal.md, SDDs)
//   Body       — services table sorted by category (P0 first)
//   Activity   — drift-report summary counts
//   Linked     — catalog (consumes manifest), api-docs (consumes openapi)
//
// T3B.1 / GRO-879. Editor (write) and full git-log history viewer are
// captured as out-of-scope follow-ons.
func (h *Handler) manifestPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["manifest"]
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     "manifest",
		"Tenant":     "all",
		"Env":        "lab",
		"Catalog":    h.catalog,
		"CatalogLoaded": h.catalog != nil,
		"Drift":      h.drift,
		"DriftLoaded": h.drift != nil && h.drift.Loaded,
	}
	if h.catalog != nil {
		// Group services by category for the body table. P0 first, then P1, P2.
		byPriority := map[string][]CatalogSvc{}
		for _, s := range h.catalog.Services {
			byPriority[s.Priority] = append(byPriority[s.Priority], s)
		}
		view["P0Services"] = byPriority["P0"]
		view["P1Services"] = byPriority["P1"]
		view["P2Services"] = byPriority["P2"]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "manifest.html", view); err != nil {
		h.logger.Error("manifest template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// observabilityPage renders the /devops/observability viewer — five tier
// cards covering the full cadence ladder. Each tier card surfaces the
// per-tier infrastructure metadata (protocol/auth/rate-limit/health/
// cache/recovery) per spec §"Per-tier infrastructure", plus the dedup-
// sorted set of services declared in any cell of that tier.
//
// Live health checks and per-service drill-down are out of scope here —
// they land in T3A.4 ("per-tier health rollups" middleware ticket) and
// follow-on Phase 3 work respectively. This page renders the contract
// (what each tier means + which services occupy it), not live telemetry.
//
// T3B.2 / GRO-883.
func (h *Handler) observabilityPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["observability"]
	rollups := buildTierRollups(h.catalog)
	cellsCovered, cellsEmpty, servicesTracked := 0, 0, 0
	if h.catalog != nil {
		seen := map[string]bool{}
		for _, c := range h.catalog.Cells {
			if c.EndpointCount > 0 {
				cellsCovered++
			} else {
				cellsEmpty++
			}
			for _, s := range c.Services {
				if !seen[s] {
					seen[s] = true
					servicesTracked++
				}
			}
		}
	}
	view := map[string]any{
		"Service":         svc,
		"Categories":      Categories,
		"Active":          "observability",
		"Tenant":          "all",
		"Env":             "lab",
		"Catalog":         h.catalog,
		"CatalogLoaded":   h.catalog != nil,
		"Tiers":           rollups,
		"CellsCovered":    cellsCovered,
		"CellsEmpty":      cellsEmpty,
		"ServicesTracked": servicesTracked,
		"TotalTiers":      len(TierCatalog),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "observability.html", view); err != nil {
		h.logger.Error("observability template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// buildTierRollups walks the Catalog cells and rolls services up by tier.
// A nil catalog yields rollups with empty service lists — the template
// still renders the tier metadata cards so the page is self-explanatory
// even when devops-catalog.json is absent.
func buildTierRollups(cat *Catalog) []TierRollup {
	out := make([]TierRollup, 0, len(TierCatalog))
	for _, ti := range TierCatalog {
		r := TierRollup{TierInfo: ti}
		if cat != nil {
			seen := map[string]bool{}
			for _, c := range cat.Cells {
				if c.Tier != ti.Name {
					continue
				}
				r.EndpointCount += c.EndpointCount
				for _, s := range c.Services {
					if !seen[s] {
						seen[s] = true
						r.Services = append(r.Services, s)
					}
				}
			}
		}
		out = append(out, r)
	}
	return out
}
