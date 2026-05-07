// Package devops implements the platform-operator sysadmin module
// at /devops/<service>. Each service renders the canonical six-zone
// service-detail-page (header · capability · KPI strip · endpoints ·
// service body · activity · linked) inside a shared shell with a
// grouped service sidebar and a tenant/env top nav.
//
// T2.0 / GRO-840 of the sysadmin module epic (GRO-836). Phase 2 of
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
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Catalog is the JSON shape emitted by parse_manifest.py's
// build_catalog() — see services/canary-protocol/manifest/gen/.
// Keep the field tags in lockstep with that emitter.
type Catalog struct {
	GeneratedAt string         `json:"generated_at"`
	Axes        []CatalogAxis  `json:"axes"`
	Tiers       []string       `json:"tiers"`
	Cells       []CatalogCell  `json:"cells"`
	Services    []CatalogSvc   `json:"services"`
	Totals      CatalogTotals  `json:"totals"`
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
	Card           string   `json:"card"`
	Cells          []string `json:"cells"`
	EndpointCount  int      `json:"endpoint_count"`
	PythonPriorArt *string  `json:"python_prior_art"`
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
	Card           string   // Brain/wiki/cards/catalog.md
	Priority       string   // P0
	Scope          string   // cross-tenant
	Category       string   // cross-tenant infra
	Cells          []string // ["B × reference"]
	PythonPriorArt string   // Canary/canary/services/devops_monitor.py
	BodyTODO       string   // one-line description of what Phase 3 wires
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
				Card: "Brain/wiki/cards/catalog.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				BodyTODO: "3×5 grid heat-map of every endpoint by axis × tier — wired in T2.34.",
			},
			{
				Name: "manifest", Port: 9101, Owner: "ALX",
				Card: "Brain/wiki/cards/manifest.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				BodyTODO: "Manifest editor + validator + history viewer — wired in T3B.1.",
			},
			{
				Name: "observability", Port: 9102, Owner: "ALX",
				Card: "Brain/wiki/cards/observability.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × change-feed", "B × reference"},
				BodyTODO: "Five-tier health rollup with per-service drill-down — wired in T3B.2.",
			},
			{
				Name: "pipeline", Port: 9103, Owner: "ALX",
				Card: "Brain/wiki/cards/pipeline.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × change-feed"},
				PythonPriorArt: "Canary/canary/services/devops_monitor.py",
				BodyTODO:       "TSP pipeline visualization (Webhook → Sub1 → Sub2 → Sub3) — wired in T3B.3.",
			},
			{
				Name: "qa-agent", Port: 9104, Owner: "ALX",
				Card: "Brain/wiki/cards/qa-agent.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"C × change-feed"},
				PythonPriorArt: "Canary/canary/qa_agent/",
				BodyTODO:       "Page-aware operator agent with cross-service MCP tools — wired in T3B.4.",
			},
			{
				Name: "api-docs", Port: 9105, Owner: "ALX",
				Card: "Brain/wiki/cards/api-docs.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				BodyTODO: "Redoc-rendered OpenAPI 3.0 spec — wired in T2.35.",
			},
			{
				Name: "evidence", Port: 9201, Owner: "ALX",
				Card: "Brain/wiki/cards/evidence.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				PythonPriorArt: "Canary/canary/services/evidence_service.py",
				BodyTODO:       "Append-only protocol audit log query UI — Phase 3 wires the operator workflow.",
			},
			{
				Name: "anchor", Port: 9202, Owner: "ALX",
				Card: "Brain/wiki/cards/anchor.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				PythonPriorArt: "Canary/canary/services/anchor_service.py",
				BodyTODO:       "Merkle anchor batch viewer + Bitcoin L2 proof inspector — Phase 3.",
			},
			{
				Name: "mcp", Port: 9203, Owner: "ALX",
				Card: "Brain/wiki/cards/mcp.md", Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"C × change-feed"},
				BodyTODO: "MCP tool catalog + per-tenant usage rollup — Phase 3 builds drill-down.",
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

	for name := range h.index {
		if name == "api-docs" || name == "catalog" {
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
