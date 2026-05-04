// Package web serves the Canary application UI at the root path.
//
// Routes:
//
//	GET /                     → redirect to /dashboard
//	GET /join                 → public join/onboarding page
//	GET /dashboard            → main dashboard
//	GET /chirps               → chirp alert feed
//	GET /transactions         → transaction list
//	GET /alerts               → alert list
//	GET /alerts/:id           → alert detail (stub)
//	GET /cases                → case list (hawk)
//	GET /cases/hawk           → hawk case list
//	GET /cases/hawk/new       → hawk case wizard
//	GET /cases/hawk/:id       → hawk case detail
//	POST /cases/hawk          → create case
//	GET /employees            → employee list
//	GET /reports              → reports list
//	GET /settings             → merchant settings
//	GET /owl                  → owl semantic search
//	GET /rules                → detection rule list
//	GET /connect              → post-OAuth setup
//	GET /welcome              → onboarding welcome
//	GET /web/static/*         → embedded CSS + images
//
// Auth: placeholder — all routes are open until the identity middleware
// is wired (GRO-769). The User field in PageData will be populated by
// the auth middleware once it lands.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

//go:embed static templates
var embedFS embed.FS

// Handler serves the Canary application UI.
type Handler struct {
	logger    *zap.Logger
	templates map[string]*template.Template
}

// PageData is the top-level template context passed to every app page.
type PageData struct {
	Page  string // active page key for sidebar highlighting
	Title string
	User  UserData
	Theme string // CSS theme file stem, e.g. "canary-dark"
	Data  any    // page-specific data
}

// UserData is the authenticated user context injected into every page.
type UserData struct {
	DisplayName string
	Role        string
	IsAdmin     bool
}

// New constructs a Handler with all templates pre-parsed.
func New(logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	h := &Handler{
		logger:    logger,
		templates: make(map[string]*template.Template),
	}
	h.mustParse("dashboard", "templates/dashboard.html")
	h.mustParse("chirps", "templates/chirps.html")
	h.mustParse("transactions", "templates/transactions.html")
	h.mustParse("alerts", "templates/alerts.html")
	h.mustParse("cases", "templates/cases.html")
	h.mustParse("employees", "templates/employees.html")
	h.mustParse("reports", "templates/reports.html")
	h.mustParse("settings", "templates/settings.html")
	h.mustParse("owl", "templates/owl.html")
	h.mustParse("rules", "templates/rules.html")
	h.mustParse("connect", "templates/connect.html")
	h.mustParse("welcome", "templates/welcome.html")
	h.mustParse("hawk_list", "templates/hawk/case_list.html")
	h.mustParse("hawk_detail", "templates/hawk/case_detail.html")
	h.mustParse("hawk_new", "templates/hawk/wizard_start.html")
	h.mustParse("err403", "templates/errors/403.html")
	h.mustParse("err404", "templates/errors/404.html")
	h.mustParse("err500", "templates/errors/500.html")
	return h
}

// mustParse builds a per-page template set: base + sidebar + page file.
// Panics on parse error — caught at startup, not at request time.
func (h *Handler) mustParse(name, pageFile string) {
	h.templates[name] = template.Must(template.ParseFS(embedFS,
		"templates/base.html",
		"templates/partials/sidebar.html",
		pageFile,
	))
}

// Mount registers all web UI routes on r.
func (h *Handler) Mount(r chi.Router) {
	staticFS, _ := fs.Sub(embedFS, "static")

	r.Handle("/web/static/*", http.StripPrefix("/web/static/",
		http.FileServer(http.FS(staticFS))))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	// Public auth pages (standalone HTML, no base template)
	r.Get("/join", h.joinPage)

	// App pages — auth guard will wrap these once identity middleware lands
	r.Get("/dashboard", h.page("dashboard", "dashboard", stubDashboard))
	r.Get("/chirps", h.page("chirps", "chirps", stubChirps))
	r.Get("/transactions", h.page("transactions", "transactions", stubTransactions))
	r.Get("/alerts", h.page("alerts", "alerts", stubAlerts))
	r.Get("/cases", h.page("cases", "cases", stubCases))
	r.Get("/employees", h.page("employees", "employees", stubEmployees))
	r.Get("/reports", h.page("reports", "reports", stubReports))
	r.Get("/settings", h.page("settings", "settings", stubSettings))
	r.Get("/owl", h.owlPage)
	r.Get("/rules", h.page("rules", "rules", stubRules))
	r.Get("/connect", h.page("connect", "connect", stubConnect))
	r.Get("/welcome", h.page("welcome", "welcome", nil))

	// Hawk case management
	r.Get("/cases/hawk", h.page("cases", "hawk_list", stubHawkList))
	r.Get("/cases/hawk/new", h.page("cases", "hawk_new", stubHawkNew))
	r.Get("/cases/hawk/{id}", h.hawkDetailPage)
	r.Post("/cases/hawk", h.hawkCreateCase)

	// Error pages (also reachable programmatically via Render403/404/500)
	r.Get("/errors/403", h.errPage(403))
	r.Get("/errors/404", h.errPage(404))
	r.Get("/errors/500", h.errPage(500))

	h.logger.Info("web UI mounted", zap.String("path", "/"))
}

// ── Page helpers ──────────────────────────────────────────────────────

// page returns a handler that renders the named template with data from dataFn.
func (h *Handler) page(activePage, tmplName string, dataFn func(*http.Request) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var data any
		if dataFn != nil {
			data = dataFn(r)
		}
		h.render(w, r, tmplName, activePage, data)
	}
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, tmplName, activePage string, data any) {
	tmpl, ok := h.templates[tmplName]
	if !ok {
		h.logger.Error("template not found", zap.String("name", tmplName))
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	pd := PageData{
		Page:  activePage,
		Theme: "canary-dark", // TODO: resolve from tenant config
		User:  stubUser(),
		Data:  data,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, "base.html", pd); err != nil {
		h.logger.Error("template execute", zap.String("name", tmplName), zap.Error(err))
	}
}

// joinPage serves the standalone public join page (no base template).
func (h *Handler) joinPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(embedFS, "templates/auth/join.html")
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

func (h *Handler) owlPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "owl", "owl", map[string]any{
		"Query":   r.URL.Query().Get("q"),
		"Results": nil,
	})
}

func (h *Handler) hawkDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.render(w, r, "hawk_detail", "cases", map[string]any{
		"Case": map[string]any{
			"ID":          id,
			"ShortID":     id[:8],
			"Title":       "Case " + id[:8],
			"Status":      "open",
			"StatusClass": "",
			"CreatedAt":   "—",
			"Subjects":    nil,
		},
		"Timeline":      nil,
		"EvidenceCount": 0,
	})
}

func (h *Handler) hawkCreateCase(w http.ResponseWriter, r *http.Request) {
	// TODO: wire to casemgmt store
	http.Redirect(w, r, "/cases/hawk", http.StatusSeeOther)
}

func (h *Handler) errPage(code int) http.HandlerFunc {
	tmplName := map[int]string{403: "err403", 404: "err404", 500: "err500"}[code]
	return func(w http.ResponseWriter, r *http.Request) {
		tmpl, ok := h.templates[tmplName]
		if !ok {
			http.Error(w, http.StatusText(code), code)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		_ = tmpl.ExecuteTemplate(w, "base.html", PageData{
			Theme: "canary-dark",
			User:  stubUser(),
		})
	}
}

// ── Stub data (replaced by real store calls once modules are wired) ──

func stubUser() UserData {
	return UserData{DisplayName: "", Role: "viewer", IsAdmin: false}
}

type dashboardData struct {
	DateRange      string
	Tiles          []statTile
	StubTiles      []string
	AlertsByRule   []alertRuleRow
	PipelineStages []pipelineStage
}

type statTile struct {
	URL   string
	Value string
	Label string
}

type alertRuleRow struct {
	RuleID   string
	RuleName string
	Count    int
	Severity string
}

type pipelineStage struct {
	Name    string
	Value   string
	Hint    string
	HasData bool
}

func stubDashboard(_ *http.Request) any {
	return dashboardData{
		DateRange: "Last 30 days",
		StubTiles: []string{"Transactions", "Refunds", "Voids", "Alerts"},
		PipelineStages: []pipelineStage{
			{Name: "Ingestion", Value: "—", Hint: "awaiting data"},
			{Name: "Sales CRDM", Value: "—", Hint: "awaiting data"},
			{Name: "Metrics", Value: "—", Hint: "awaiting ETL"},
			{Name: "Owl Report", Value: "—", Hint: "no report yet"},
		},
	}
}

func stubChirps(_ *http.Request) any {
	return map[string]any{"Chirps": nil}
}

func stubTransactions(_ *http.Request) any {
	return map[string]any{"Transactions": nil, "TotalCount": 0}
}

func stubAlerts(_ *http.Request) any {
	return map[string]any{"Alerts": nil, "OpenCount": 0, "TotalCount": 0}
}

func stubCases(_ *http.Request) any {
	return map[string]any{"Cases": nil, "OpenCount": 0, "TotalCount": 0}
}

func stubEmployees(_ *http.Request) any {
	return map[string]any{"Employees": nil, "TotalCount": 0}
}

func stubReports(_ *http.Request) any {
	return map[string]any{"Reports": nil}
}

func stubSettings(_ *http.Request) any {
	return map[string]any{
		"MerchantID":      "—",
		"POSSource":       "—",
		"Theme":           "canary-dark",
		"WeekStartDay":    "Monday",
		"ActiveRuleCount": 0,
	}
}

func stubRules(_ *http.Request) any {
	return map[string]any{"Rules": nil, "ActiveCount": 0, "TotalCount": 0}
}

func stubConnect(_ *http.Request) any {
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	type lookbackOpt struct {
		Value string
		Label string
	}
	return map[string]any{
		"WeekDays":    days,
		"WeekStart":   0,
		"Lookback":    "30",
		"LookbackOpts": []lookbackOpt{
			{"7", "7 days"}, {"30", "30 days"}, {"90", "90 days"}, {"all", "All"},
		},
	}
}

func stubHawkList(_ *http.Request) any {
	type statusOpt struct {
		Value string
		Label string
	}
	return map[string]any{
		"Cases":        nil,
		"OpenCount":    0,
		"StatusFilter": "open",
		"Statuses": []statusOpt{
			{"open", "Open"}, {"investigating", "Investigating"},
			{"closed", "Closed"}, {"", "All"},
		},
	}
}

func stubHawkNew(_ *http.Request) any {
	return map[string]any{"Alerts": nil}
}
