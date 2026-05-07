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
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

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
		},
	},
}

// Handler renders the sysadmin module shell.
type Handler struct {
	tmpl   *template.Template
	logger *zap.Logger
	index  map[string]*Service // name → metadata, built once at New()
}

// New constructs a Handler. Returns an error if templates fail to
// parse — main.go logs and continues without mounting.
func New(logger *zap.Logger) (*Handler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	tmpl, err := template.ParseFS(embedFS,
		"templates/shell.html",
		"templates/sidebar.html",
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
	return &Handler{tmpl: tmpl, logger: logger, index: idx}, nil
}

// Mount registers a route per known service. The runner registers
// these specifically (rather than a /devops/{service} catch-all) so
// chi can detect collisions cleanly during boot and so the existing
// internal/devops/ package's specific routes (/devops/square,
// /devops/api, /devops/releases) keep working.
func (h *Handler) Mount(r chi.Router) {
	for name := range h.index {
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
