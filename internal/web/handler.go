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
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/casemgmt"
	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/customer"
	"github.com/growdirect-llc/rapidpos/internal/employee"
	"github.com/growdirect-llc/rapidpos/internal/inventory"
	"github.com/growdirect-llc/rapidpos/internal/item"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/transaction"
	"github.com/growdirect-llc/rapidpos/internal/workflow"
)

//go:embed static templates
var embedFS embed.FS

// Handler serves the Canary application UI.
type Handler struct {
	logger    *zap.Logger
	templates map[string]*template.Template
	deps      Deps
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
func New(deps Deps, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	h := &Handler{
		logger:    logger,
		templates: make(map[string]*template.Template),
		deps:      deps,
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
	h.mustParse("hawk_evidence", "templates/hawk/evidence_attach.html")
	h.mustParse("hawk_analytics", "templates/hawk/case_analytics.html")
	h.mustParse("hawk_patterns", "templates/hawk/cross_case_patterns.html")
	h.mustParse("alert_detail", "templates/alert_detail.html")
	h.mustParse("rule_detail", "templates/rule_detail.html")
	h.mustParse("chirp_detail", "templates/chirp_detail.html")
	h.mustParse("transaction_detail", "templates/transaction_detail.html")
	h.mustParse("transaction_proof", "templates/transaction_proof.html")
	h.mustParse("err403", "templates/errors/403.html")
	h.mustParse("err404", "templates/errors/404.html")
	h.mustParse("err500", "templates/errors/500.html")
	h.mustParse("customers_list", "templates/customers/list.html")
	h.mustParse("customers_detail", "templates/customers/detail.html")
	h.mustParse("customers_risk", "templates/customers/risk.html")
	h.mustParse("customers_context", "templates/customers/context.html")
	h.mustParse("settings_allowlist_dead_count", "templates/settings/allowlist_dead_count.html")
	h.mustParse("settings_allowlist_discounts", "templates/settings/allowlist_discounts.html")
	h.mustParse("settings_allowlist_voids", "templates/settings/allowlist_voids.html")
	h.mustParse("settings_allowlist_comps", "templates/settings/allowlist_comps.html")
	h.mustParse("settings_training_mode", "templates/settings/training_mode.html")
	h.mustParse("settings_alert_routing", "templates/settings/alert_routing.html")
	h.mustParse("settings_store_drawer", "templates/settings/store_drawer.html")
	h.mustParse("settings_store_discounts", "templates/settings/store_discounts.html")
	h.mustParse("settings_store_void_reasons", "templates/settings/store_void_reasons.html")
	h.mustParse("settings_store_comp_reasons", "templates/settings/store_comp_reasons.html")
	h.mustParse("transfers_list", "templates/transfers/list.html")
	h.mustParse("transfers_detail", "templates/transfers/detail.html")
	h.mustParse("transfers_variance", "templates/transfers/variance.html")
	h.mustParse("report_distribution", "templates/reports/distribution.html")
	h.mustParse("report_inventory", "templates/reports/inventory.html")
	h.mustParse("items_list", "templates/items/list.html")
	h.mustParse("items_detail", "templates/items/detail.html")
	h.mustParse("report_category", "templates/reports/category.html")
	h.mustParse("settings_devices", "templates/settings/devices.html")
	h.mustParse("settings_devices_new", "templates/settings/devices_new.html")
	h.mustParse("settings_store_config", "templates/settings/store_config.html")
	h.mustParse("report_finance", "templates/reports/finance.html")
	h.mustParse("report_payments", "templates/reports/payments.html")
	h.mustParse("report_tax", "templates/reports/tax.html")
	h.mustParse("receiving_list", "templates/receiving/list.html")
	h.mustParse("receiving_detail", "templates/receiving/detail.html")
	h.mustParse("receiving_close", "templates/receiving/close.html")
	h.mustParse("returns_list", "templates/returns/list.html")
	h.mustParse("returns_detail", "templates/returns/detail.html")
	h.mustParse("report_otb", "templates/reports/otb.html")
	h.mustParse("report_suggested_orders", "templates/reports/suggested_orders.html")
	h.mustParse("report_range", "templates/reports/range.html")
	h.mustParse("promotions_calendar", "templates/promotions/calendar.html")
	h.mustParse("report_pricing", "templates/reports/pricing.html")
	h.mustParse("report_price_history", "templates/reports/price_history.html")
	h.mustParse("report_markdowns", "templates/reports/markdowns.html")
	h.mustParse("employees_detail", "templates/employees/detail.html")
	h.mustParse("report_labor", "templates/reports/labor.html")
	h.mustParse("exceptions_list", "templates/exceptions/list.html")
	h.mustParse("exceptions_detail", "templates/exceptions/detail.html")
	h.mustParse("cases_new", "templates/cases/new.html")
	h.mustParse("cases_evidence", "templates/cases/evidence.html")
	h.mustParse("cases_correlation", "templates/cases/correlation.html")
	h.mustParse("cases_remediate", "templates/cases/remediate.html")
	h.mustParse("report_cases", "templates/reports/cases.html")
	h.mustParse("workflows_list", "templates/workflows/list.html")
	h.mustParse("mcp_tools", "templates/mcp/tools.html")
	h.mustParse("protocol_overview", "templates/protocol/overview.html")
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
	r.Get("/chirps", h.chirpListPage)
	r.Get("/transactions", h.page("transactions", "transactions", stubTransactions))
	r.Get("/transactions/{id}", h.transactionDetailPage)
	r.Get("/transactions/{id}/proof", h.transactionProofPage)
	r.Get("/alerts", h.alertListPage)
	r.Get("/cases", h.page("cases", "cases", stubCases))
	r.Get("/employees", h.page("employees", "employees", stubEmployees))
	r.Get("/reports", h.page("reports", "reports", stubReports))
	r.Get("/settings", h.page("settings", "settings", stubSettings))
	r.Get("/owl", h.owlPage)
	r.Get("/rules", h.rulesListPage)
	r.Get("/connect", h.page("connect", "connect", stubConnect))
	r.Get("/welcome", h.page("welcome", "welcome", nil))

	// Hawk case management
	r.Get("/cases/hawk", h.hawkListPage)
	r.Get("/cases/hawk/new", h.page("cases", "hawk_new", stubHawkNew))
	r.Get("/cases/hawk/analytics", h.page("cases", "hawk_analytics", stubHawkAnalytics))
	r.Get("/cases/hawk/patterns", h.page("cases", "hawk_patterns", stubHawkPatterns))
	r.Get("/cases/hawk/{id}", h.hawkDetailPage)
	r.Get("/cases/hawk/{id}/evidence", h.hawkEvidencePage)
	r.Post("/cases/hawk", h.hawkCreateCase)

	// Detail pages
	r.Get("/alerts/{id}", h.alertDetailPage)
	r.Get("/rules/{id}", h.ruleDetailPage)
	r.Get("/chirps/{id}", h.chirpDetailPage)

	// Customer investigator
	r.Get("/customers", h.customersListPage)
	r.Get("/customers/{id}", h.customerDetailPage)
	r.Get("/customers/{id}/risk", h.customerRiskPage)
	r.Get("/customers/{id}/context", h.customerContextPage)

	// Settings — LP allow-list + N.4 thresholds + training mode + alert routing.
	// 10 screens, each backed by detection.allow_list with a pattern type+kind
	// discriminator. CRUD wired via h.mountLPSettings (handler_lp_settings.go).
	// W1 dispatch: GRO-814.
	h.mountLPSettings(r)
	r.Get("/settings/devices", h.page("settings", "settings_devices", func(_ *http.Request) any {
		return map[string]any{"Online": 0, "Offline": 0, "Degraded": 0, "Devices": nil}
	}))
	r.Get("/settings/devices/new", h.page("settings", "settings_devices_new", func(_ *http.Request) any {
		return map[string]any{}
	}))
	r.Get("/settings/store", h.page("settings", "settings_store_config", func(_ *http.Request) any {
		return map[string]any{"StoreID": "—", "POSSource": "—", "LastSync": "—", "ActiveRuleCount": 0, "AllowListCount": 0, "TrainingMode": false}
	}))

	// Transfers + inventory reports — wired W2b / GRO-816.
	r.Get("/transfers", h.transferListPage)
	r.Get("/transfers/{id}", h.transferDetailPage)
	r.Get("/transfers/{id}/variance", h.transferVariancePage)

	r.Get("/reports/distribution", h.reportDistributionPage)
	r.Get("/reports/inventory", h.reportInventoryPage)
	r.Get("/reports/category", h.reportCategoryPage)
	r.Get("/reports/category", h.page("reports", "report_category", func(_ *http.Request) any {
		return map[string]any{"TotalCategories": 0, "TopCategory": "—", "AvgMargin": "—", "SKUsTracked": 0, "Categories": nil}
	}))

	// Items + category report — wired W2c / GRO-817.
	r.Get("/items", h.itemListPage)
	r.Get("/items/{id}", h.itemDetailPage)

	// Finance + payments — wired W2e / GRO-819.
	r.Get("/reports/finance", h.reportFinancePage)
	r.Get("/reports/payments", h.page("reports", "report_payments", func(_ *http.Request) any {
		// Tender mix requires a tender_mix aggregation method on transaction.Store
		// (per-transaction GetByID is too expensive). Filed as a follow-on.
		return map[string]any{"TotalTransactions": 0, "CashPct": "—", "CardPct": "—", "OtherPct": "—", "Tenders": nil, "SecurePayEnabled": false, "LastGatewaySync": "—"}
	}))
	r.Get("/reports/tax", h.page("reports", "report_tax", func(_ *http.Request) any {
		return map[string]any{"TotalTax": "—", "AuthorityCount": 0, "NexusStates": 0, "FilingPeriod": "—", "Authorities": nil}
	}))
	r.Get("/reports/otb", h.page("reports", "report_otb", func(_ *http.Request) any {
		return map[string]any{"OTBRemaining": "—", "Committed": "—", "Received": "—", "Variance": "—", "Periods": nil}
	}))
	r.Get("/orders/suggested", h.page("reports", "report_suggested_orders", func(_ *http.Request) any {
		return map[string]any{"Orders": nil, "PendingCount": 0}
	}))
	r.Get("/reports/range", h.page("reports", "report_range", func(_ *http.Request) any {
		return map[string]any{"ActiveRanges": 0, "AvgSellThrough": "—", "AvgTurn": "—", "AvgGMROI": "—", "Ranges": nil}
	}))
	// Promotions calendar — wired W2f / GRO-820. Pricing reports remain stub
	// until market-price + price-history + markdown source data lands.
	r.Get("/promotions", h.promotionsCalendarPage)
	r.Get("/reports/pricing", h.page("reports", "report_pricing", func(_ *http.Request) any {
		return map[string]any{"ItemsTracked": 0, "AboveMarket": 0, "AtMarket": 0, "BelowMarket": 0, "Items": nil}
	}))
	r.Get("/reports/price-history", h.page("reports", "report_price_history", func(_ *http.Request) any {
		return map[string]any{"Changes": nil, "TotalCount": 0}
	}))
	r.Get("/reports/markdowns", h.page("reports", "report_markdowns", func(_ *http.Request) any {
		return map[string]any{"ActiveMarkdowns": 0, "AvgDepth": "—", "UnitsMoved": 0, "RevenueRecovery": "—", "Items": nil}
	}))
	r.Get("/employees/{id}", h.employeeDetailPage)
	r.Get("/reports/labor", h.reportLaborPage)

	// Receiving + RTV workflow — wired W2d / GRO-818.
	r.Get("/receiving", h.receivingListPage)
	r.Get("/receiving/{id}", h.receivingDetailPage)
	r.Get("/receiving/{id}/close", h.receivingClosePage)
	r.Get("/returns", h.returnsListPage)
	r.Get("/returns/{id}", h.returnsDetailPage)

	// Cross-domain exceptions
	r.Get("/exceptions", h.page("exceptions", "exceptions_list", func(_ *http.Request) any {
		return map[string]any{"Exceptions": nil, "OpenCount": 0, "TotalCount": 0, "DomainFilter": ""}
	}))
	r.Get("/exceptions/{id}", h.exceptionDetailPage)

	// Cross-domain case management (registered after /cases/hawk/* to avoid conflicts)
	r.Get("/cases/new", h.casesNewPage)
	r.Get("/cases/{id}/evidence", h.casesEvidencePage)
	r.Get("/cases/{id}/correlation", h.casesCorrelationPage)
	r.Get("/cases/{id}/remediate", h.casesRemediatePage)

	// Cases analytics — wired W2e / GRO-819.
	r.Get("/reports/cases", h.reportCasesPage)

	// Workflow engine surfaces — wired W4 / GRO-823 (unified list page;
	// per-workflow detail views are a follow-on dispatch).
	r.Get("/workflows", h.workflowsListPage)

	// MCP tool catalog — wired W12 / GRO-831. Reads the in-process
	// registry; usage log + playground are follow-on dispatches.
	r.Get("/mcp/tools", h.mcpToolsPage)

	// Protocol portal — wired W7 / GRO-826. Unified overview of Bitcoin L2
	// anchors, .jeffe namespace registrations, and L402 verification tokens.
	// Per-surface drilldowns (anchor proof viewer, charge dispute, evidence
	// chain per case) are follow-on dispatches.
	r.Get("/protocol", h.protocolOverviewPage)

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
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "cases", nil)
		return
	}
	if h.deps.CaseStore == nil {
		h.render(w, r, "hawk_detail", "cases", map[string]any{
			"Case": map[string]any{
				"ID": idStr, "ShortID": idStr[:8],
				"Title": "Case " + idStr[:8], "Status": "open",
				"StatusClass": "", "CreatedAt": "—", "Subjects": nil,
			},
			"Timeline": nil, "EvidenceCount": 0, "Evidence": nil,
		})
		return
	}
	tenantID := tenantIDFromCtx(ctx)
	c, err := h.deps.CaseStore.GetCase(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, casemgmt.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "cases", nil)
			return
		}
		h.logger.Error("hawkDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "cases", nil)
		return
	}
	timeline, _ := h.deps.CaseStore.ListActions(ctx, id)
	evidence, _ := h.deps.CaseStore.ListEvidence(ctx, id)
	h.render(w, r, "hawk_detail", "cases", map[string]any{
		"Case":          c,
		"Timeline":      timeline,
		"EvidenceCount": len(evidence),
		"Evidence":      evidence,
	})
}

// alertListPage renders the alert list from the real alert store.
func (h *Handler) alertListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var alerts []alert.AlertDTO
	if h.deps.AlertStore != nil {
		var err error
		alerts, err = h.deps.AlertStore.List(ctx, alert.ListFilters{
			TenantID: tenantID,
			Limit:    50,
		})
		if err != nil {
			h.logger.Error("alertListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "alerts", nil)
			return
		}
	}
	h.render(w, r, "alerts", "alerts", map[string]any{
		"Alerts": alerts,
	})
}

func (h *Handler) alertDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "alerts", nil)
		return
	}

	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.AlertStore == nil {
		h.render(w, r, "alert_detail", "alerts", map[string]any{
			"Alert": map[string]any{
				"ID": idStr, "ShortID": shortID,
				"Title": "Alert " + shortID, "Severity": "high",
				"Status": "open", "StatusClass": "", "Description": "—",
				"RuleID": "—", "RuleCode": "—", "StoreID": "—",
				"TransactionID": "—", "CreatedAt": "—",
			},
			"Timeline": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	a, err := h.deps.AlertStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, alert.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "alerts", nil)
			return
		}
		h.logger.Error("alertDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "alerts", nil)
		return
	}

	h.render(w, r, "alert_detail", "alerts", map[string]any{
		"Alert": map[string]any{
			"ID": a.ID.String(), "ShortID": a.ID.String()[:8],
			"Title":         "Alert " + a.ID.String()[:8],
			"Severity":      a.Severity,
			"Status":        a.Status,
			"StatusClass":   "",
			"Description":   "—",
			"RuleID":        a.RuleID.String(),
			"RuleCode":      a.RuleCode,
			"StoreID":       "—",
			"TransactionID": a.SourceEntityID.String(),
			"CreatedAt":     a.CreatedAt.Format(time.RFC3339),
		},
		"Timeline": nil,
	})
}

// hawkListPage renders the Hawk case list from the real case store.
func (h *Handler) hawkListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	type statusOpt struct {
		Value string
		Label string
	}
	statuses := []statusOpt{
		{"open", "Open"}, {"investigating", "Investigating"},
		{"closed", "Closed"}, {"", "All"},
	}
	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "open"
	}

	var cases []casemgmt.Case
	if h.deps.CaseStore != nil {
		var err error
		cases, err = h.deps.CaseStore.ListCases(ctx, casemgmt.ListFilters{
			TenantID: tenantID,
			Status:   statusFilter,
			Limit:    100,
		})
		if err != nil {
			h.logger.Error("hawkListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "cases", nil)
			return
		}
	}
	h.render(w, r, "hawk_list", "cases", map[string]any{
		"Cases":        cases,
		"OpenCount":    0,
		"StatusFilter": statusFilter,
		"Statuses":     statuses,
	})
}

// rulesListPage renders the detection rules list from the real chirp store.
func (h *Handler) rulesListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var rules []chirp.Rule
	if h.deps.ChirpStore != nil {
		var err error
		rules, err = h.deps.ChirpStore.ListRules(ctx, tenantID)
		if err != nil {
			h.logger.Error("rulesListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "rules", nil)
			return
		}
	}
	h.render(w, r, "rules", "rules", map[string]any{
		"Rules":       rules,
		"ActiveCount": 0,
		"TotalCount":  len(rules),
	})
}

func (h *Handler) ruleDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "rules", nil)
		return
	}
	if h.deps.ChirpStore == nil {
		h.render(w, r, "rule_detail", "rules", map[string]any{
			"Rule": map[string]any{
				"ID": idStr, "Name": "Rule " + idStr,
				"Severity": "high", "Category": "—", "Description": "—",
				"Enabled": false, "FireCount": 0, "FiresToday": 0,
				"FiresThisWeek": 0, "Parameters": nil,
			},
		})
		return
	}
	tenantID := tenantIDFromCtx(ctx)
	rule, err := h.deps.ChirpStore.GetRuleByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, chirp.ErrRuleNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "rules", nil)
			return
		}
		h.logger.Error("ruleDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "rules", nil)
		return
	}
	h.render(w, r, "rule_detail", "rules", map[string]any{
		"Rule": rule,
	})
}

// chirpListPage renders the chirp (detection) list from the real chirp store.
func (h *Handler) chirpListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var detections []chirp.Detection
	if h.deps.ChirpStore != nil {
		var err error
		detections, err = h.deps.ChirpStore.ListDetections(ctx, chirp.DetectionQuery{
			TenantID: tenantID,
			Limit:    50,
		})
		if err != nil {
			h.logger.Error("chirpListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "chirps", nil)
			return
		}
	}
	h.render(w, r, "chirps", "chirps", map[string]any{
		"Chirps": detections,
	})
}

func (h *Handler) chirpDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "chirps", nil)
		return
	}

	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.ChirpStore == nil {
		h.render(w, r, "chirp_detail", "chirps", map[string]any{
			"Chirp": map[string]any{
				"ID": idStr, "ShortID": shortID,
				"EventType": "—", "StoreID": "—", "CashierID": "—",
				"Amount": "—", "SKUCount": 0,
				"Hash":      "0000000000000000000000000000000000000000000000000000000000000000",
				"CreatedAt": "—", "CaseID": "",
			},
			"Signals": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	d, err := h.deps.ChirpStore.GetDetectionByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, chirp.ErrDetectionNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "chirps", nil)
			return
		}
		h.logger.Error("chirpDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "chirps", nil)
		return
	}

	caseID := ""
	if d.CaseID != nil {
		caseID = d.CaseID.String()
	}
	h.render(w, r, "chirp_detail", "chirps", map[string]any{
		"Chirp": map[string]any{
			"ID": d.ID.String(), "ShortID": d.ID.String()[:8],
			"EventType": d.SourceEntityType,
			"StoreID":   "—",
			"CashierID": "—",
			"Amount":    "—",
			"SKUCount":  0,
			"Hash":      "0000000000000000000000000000000000000000000000000000000000000000",
			"CreatedAt": d.CreatedAt.Format(time.RFC3339),
			"CaseID":    caseID,
		},
		"Signals": nil,
	})
}

// transactionDetailPage renders one canonical transaction with hydrated
// line items, tenders, and discounts. Falls back to the stub view when the
// TransactionStore is unavailable (pre-wire dev path). Wired W2a / GRO-815.
func (h *Handler) transactionDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transactions", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.TransactionStore == nil {
		h.render(w, r, "transaction_detail", "transactions", map[string]any{
			"Transaction": map[string]any{
				"ID": idStr, "ShortID": shortID, "POSSource": "—",
				"Amount": "—", "Cashier": "—", "StoreID": "—",
				"Hash":        deriveTxnHash(idStr),
				"SealStatus":  "pending",
				"ParseStatus": "pending",
				"CreatedAt":   "—",
			},
			"Events": nil, "LineItems": nil, "AlertCount": 0,
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	dto, err := h.deps.TransactionStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, transaction.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transactions", nil)
			return
		}
		h.logger.Error("transactionDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transactions", nil)
		return
	}

	cashier := "—"
	if dto.CashierEmployeeID != nil {
		cashier = dto.CashierEmployeeID.String()[:8]
	}
	pos := "—"
	if dto.POSTerminalID != nil && *dto.POSTerminalID != "" {
		pos = *dto.POSTerminalID
	}

	lineItems := make([]map[string]any, 0, len(dto.LineItems))
	for _, li := range dto.LineItems {
		sku := "—"
		if li.ItemID != nil {
			sku = li.ItemID.String()[:8]
		}
		lineItems = append(lineItems, map[string]any{
			"SKU":         sku,
			"Description": li.Description,
			"Qty":         li.Quantity.String(),
			"UnitPrice":   li.UnitPrice.String(),
			"Extended":    li.LineTotal.String(),
		})
	}

	// Canonical events for the transaction header — render a single event
	// summarizing the txn type + amount until tsp event ingestion lands.
	events := []map[string]any{
		{
			"Type":      dto.TransactionType,
			"Amount":    dto.GrandTotal.String(),
			"Cashier":   cashier,
			"Timestamp": dto.EndedAt.Format(time.RFC3339),
		},
	}

	h.render(w, r, "transaction_detail", "transactions", map[string]any{
		"Transaction": map[string]any{
			"ID":          dto.ID.String(),
			"ShortID":     dto.ID.String()[:8],
			"POSSource":   pos,
			"Amount":      dto.GrandTotal.String() + " " + dto.Currency,
			"Cashier":     cashier,
			"StoreID":     dto.LocationID.String()[:8],
			"Hash":        deriveTxnHash(dto.ID.String()),
			"SealStatus":  txnSealStatus(dto),
			"ParseStatus": "ok",
			"CreatedAt":   dto.CreatedAt.Format(time.RFC3339),
		},
		"Events":     events,
		"LineItems":  lineItems,
		"AlertCount": 0, // populated when alert→transaction join lands (out of scope)
	})
}

// transactionProofPage renders the audit proof for a transaction by looking
// up the anchor record keyed by the transaction's derived event_hash.
// Returns "pending" when the protocol pipeline hasn't anchored this txn yet —
// the common state today since the demo path doesn't yet feed retail txns
// into Sub1/Sub3. Wired W2a / GRO-815.
func (h *Handler) transactionProofPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transactions", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	view := map[string]any{
		"Transaction": map[string]any{
			"ID":        idStr,
			"ShortID":   shortID,
			"Hash":      deriveTxnHash(idStr),
			"CreatedAt": "—",
		},
		"ProofStatus": "pending",
		"MerklePath":  nil,
		"RootHash":    "—",
		"AnchorRef":   "—",
		"AnchoredAt":  "—",
	}

	// Fill CreatedAt from the txn record when available.
	if h.deps.TransactionStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		dto, err := h.deps.TransactionStore.GetByID(ctx, tenantID, id)
		if err == nil {
			view["Transaction"].(map[string]any)["CreatedAt"] = dto.CreatedAt.Format(time.RFC3339)
		} else if errors.Is(err, transaction.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transactions", nil)
			return
		}
	}

	if h.deps.ValidateStore != nil {
		eventHash := deriveTxnHash(idStr)
		proof, err := h.deps.ValidateStore.GetAnchorProof(r.Context(), eventHash)
		switch {
		case err == nil:
			view["ProofStatus"] = "valid"
			view["RootHash"] = proof.MerkleRoot
			view["AnchoredAt"] = proof.AnchoredAt.Format(time.RFC3339)
			view["AnchorRef"] = formatAnchorRef(proof)
			view["MerklePath"] = decodeMerklePath(proof.MerkleProof)
		case errors.Is(err, validate.ErrNotAnchored), errors.Is(err, validate.ErrNotFound):
			// Stay "pending" — common case until protocol pipeline anchors retail txns.
		default:
			h.logger.Error("transactionProofPage: get anchor proof",
				zap.String("event_hash", eventHash), zap.Error(err))
			// Render as pending rather than 5xx so the operator still sees the page.
		}
	}

	h.render(w, r, "transaction_proof", "transactions", view)
}

// deriveTxnHash returns a deterministic event_hash for a transaction's UUID.
// Used as the protocol-pipeline lookup key. Hex-encoded SHA-256(uuid-string).
func deriveTxnHash(txnID string) string {
	sum := sha256.Sum256([]byte(txnID))
	return hex.EncodeToString(sum[:])
}

// txnSealStatus reports the seal state for a transaction. Until tsp event
// ingestion lands, every persisted txn is treated as "sealed" by the canonical
// store (the row exists). Reflects the current on-the-wire reality.
func txnSealStatus(_ *transaction.TransactionDTO) string {
	return "sealed"
}

// formatAnchorRef collapses an AnchorProof's chain coordinates into a single
// display string for the proof page sidebar.
func formatAnchorRef(p *validate.AnchorProof) string {
	switch {
	case p.InscriptionID != nil:
		return *p.InscriptionID
	case p.BtcTxID != nil && p.BtcBlockHeight != nil:
		return p.Network + " " + (*p.BtcTxID)[:12] + "@" + intToString(*p.BtcBlockHeight)
	case p.BtcTxID != nil:
		return p.Network + " " + (*p.BtcTxID)[:12]
	default:
		return p.Network + " (anchor pending)"
	}
}

// decodeMerklePath unmarshals the proof.MerkleProof jsonb into a slice of
// {Index, Hash} maps for template rendering. Returns nil on parse error so
// the template falls back to its empty-state branch.
func decodeMerklePath(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var nodes []struct {
		Index int    `json:"index"`
		Hash  string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{"Index": n.Index, "Hash": n.Hash})
	}
	return out
}

func intToString(n int64) string {
	return strconv.FormatInt(n, 10)
}

// transferListPage renders all transfer documents (transfer_out + transfer_in)
// for the tenant. Wired W2b / GRO-816.
func (h *Handler) transferListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_list", "transfers", map[string]any{
			"Transfers": nil, "InTransitCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    inventory.TransferTypes,
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("transferListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	transfers := make([]map[string]any, 0, len(docs))
	inTransit := 0
	for _, d := range docs {
		if d.Status == "in_progress" || d.Status == "draft" {
			inTransit++
		}
		transfers = append(transfers, transferRowView(d))
	}
	h.render(w, r, "transfers_list", "transfers", map[string]any{
		"Transfers":      transfers,
		"InTransitCount": inTransit,
		"TotalCount":     len(transfers),
	})
}

// transferDetailPage renders one transfer document with its line items.
// Wired W2b / GRO-816.
func (h *Handler) transferDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transfers", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_detail", "transfers", map[string]any{
			"Transfer": map[string]any{
				"ID": idStr, "ShortID": shortID, "FromStore": "—", "ToStore": "—",
				"Status": "in-transit", "StatusClass": "", "ItemCount": 0,
				"InitiatedBy": "—", "InitiatedAt": "—", "ExpectedArrival": "—",
			},
			"Lines": nil,
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transfers", nil)
			return
		}
		h.logger.Error("transferDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("transferDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}

	view := transferRowView(*doc)
	view["InitiatedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["InitiatedBy"] = doc.PerformedByUserID.String()[:8]
	}
	expected := "—"
	if doc.ExpectedAt != nil {
		expected = doc.ExpectedAt.Format(time.RFC3339)
	}
	view["ExpectedArrival"] = expected

	lineViews := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		lineViews = append(lineViews, transferLineView(l))
	}

	h.render(w, r, "transfers_detail", "transfers", map[string]any{
		"Transfer": view,
		"Lines":    lineViews,
	})
}

// transferVariancePage renders shipped vs received variance for one transfer.
// Wired W2b / GRO-816.
func (h *Handler) transferVariancePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transfers", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_variance", "transfers", map[string]any{
			"Transfer":     map[string]any{"ID": idStr, "ShortID": shortID, "FromStore": "—", "ToStore": "—"},
			"ShippedTotal": 0, "ReceivedTotal": 0, "VarianceCount": 0, "ValueAtRisk": "—",
			"Lines": nil,
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "transfers", nil)
			return
		}
		h.logger.Error("transferVariancePage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("transferVariancePage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}

	shippedTotal := 0.0
	receivedTotal := 0.0
	varianceLines := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		shipped := strToFloat(l.ExpectedQuantity)
		received := strToFloat(l.ActualQuantity)
		shippedTotal += shipped
		receivedTotal += received
		varQ := strToFloat(&l.VarianceQuantity)
		if varQ == 0 {
			continue
		}
		var estValue string = "—"
		if l.UnitCost != nil {
			cost := strToFloat(l.UnitCost)
			estValue = formatMoney(varQ * cost)
		}
		varianceLines = append(varianceLines, map[string]any{
			"SKU":         l.ItemID.String()[:8],
			"Description": "—",
			"QtyShipped":  formatQty(shipped),
			"QtyReceived": formatQty(received),
			"VarianceQty": formatQty(varQ),
			"EstValue":    estValue,
		})
	}

	view := transferRowView(*doc)
	h.render(w, r, "transfers_variance", "transfers", map[string]any{
		"Transfer":      view,
		"ShippedTotal":  formatQty(shippedTotal),
		"ReceivedTotal": formatQty(receivedTotal),
		"VarianceCount": len(varianceLines),
		"ValueAtRisk":   "—",
		"Lines":         varianceLines,
	})
}

// transferRowView is a shared row-to-view-model for the transfer list,
// detail, and variance handlers.
func transferRowView(d inventory.DocumentDTO) map[string]any {
	from := "—"
	if d.SourceLocationID != nil {
		from = d.SourceLocationID.String()[:8]
	}
	to := "—"
	if d.DestinationLocationID != nil {
		to = d.DestinationLocationID.String()[:8]
	}
	itemCount := 0
	if d.TotalQuantity != nil {
		itemCount = int(strToFloat(d.TotalQuantity))
	}
	return map[string]any{
		"ID":          d.ID.String(),
		"ShortID":     d.ID.String()[:8],
		"FromStore":   from,
		"ToStore":     to,
		"Status":      mapDocStatus(d.Status),
		"StatusClass": "",
		"ItemCount":   itemCount,
		"InitiatedAt": d.CreatedAt.Format(time.RFC3339),
	}
}

// transferLineView shapes one document line for the detail template.
func transferLineView(l inventory.DocumentLineDTO) map[string]any {
	shipped := "—"
	if l.ExpectedQuantity != nil {
		shipped = *l.ExpectedQuantity
	}
	received := "—"
	if l.ActualQuantity != nil {
		received = *l.ActualQuantity
	}
	variance := ""
	if v := strToFloat(&l.VarianceQuantity); v != 0 {
		variance = formatQty(v)
	}
	return map[string]any{
		"SKU":         l.ItemID.String()[:8],
		"Description": "—",
		"QtyShipped":  shipped,
		"QtyReceived": received,
		"Variance":    variance,
	}
}

// mapDocStatus translates schema status values to the template's expected
// in-transit / received / variance vocabulary used for badge styling.
func mapDocStatus(s string) string {
	switch s {
	case "in_progress", "draft":
		return "in-transit"
	case "completed":
		return "received"
	case "cancelled":
		return "cancelled"
	case "reconciled":
		return "received"
	default:
		return s
	}
}

func strToFloat(s *string) float64 {
	if s == nil || *s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0
	}
	return f
}

func formatQty(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 4, 64)
}

func formatMoney(f float64) string {
	return "$" + strconv.FormatFloat(f, 'f', 2, 64)
}

// reportDistributionPage renders the distribution variance report — variance
// aggregated by transfer lane (source→destination). Wired W2b / GRO-816.
func (h *Handler) reportDistributionPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "report_distribution", "reports", map[string]any{
			"TotalTransfers": 0, "InTransit": 0, "VarianceFlags": 0, "Resolved": 0, "Lanes": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	// Counts come from the documents list; lane variance from the aggregate.
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    inventory.TransferTypes,
		Limit:    500,
	})
	if err != nil {
		h.logger.Error("reportDistributionPage: list docs", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	lanes, err := h.deps.InventoryStore.ListDistributionLanes(ctx, tenantID, 100)
	if err != nil {
		h.logger.Error("reportDistributionPage: list lanes", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}

	inTransit := 0
	resolved := 0
	for _, d := range docs {
		switch d.Status {
		case "in_progress", "draft":
			inTransit++
		case "completed", "reconciled":
			resolved++
		}
	}

	varianceFlags := 0
	laneViews := make([]map[string]any, 0, len(lanes))
	for _, l := range lanes {
		from := "—"
		if l.SourceLocationID != nil {
			from = l.SourceLocationID.String()[:8]
		}
		to := "—"
		if l.DestinationLocationID != nil {
			to = l.DestinationLocationID.String()[:8]
		}
		varianceQty := strToFloat(&l.TotalVariance)
		varianceCount := 0
		if varianceQty != 0 {
			varianceCount = 1
			varianceFlags++
		}
		shipped := strToFloat(&l.TotalShipped)
		variancePct := "0%"
		if shipped > 0 {
			variancePct = strconv.FormatFloat((varianceQty/shipped)*100, 'f', 1, 64) + "%"
		}
		laneViews = append(laneViews, map[string]any{
			"FromStore":     from,
			"ToStore":       to,
			"Transfers":     l.DocumentCount,
			"VarianceCount": varianceCount,
			"VariancePct":   variancePct,
		})
	}

	h.render(w, r, "report_distribution", "reports", map[string]any{
		"TotalTransfers": len(docs),
		"InTransit":      inTransit,
		"VarianceFlags":  varianceFlags,
		"Resolved":       resolved,
		"Lanes":          laneViews,
	})
}

// reportInventoryPage renders the snapshot-vs-perpetual inventory balance.
// Wired W2b / GRO-816.
func (h *Handler) reportInventoryPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "report_inventory", "reports", map[string]any{
			"TotalSKUs": 0, "Locations": 0, "VarianceItems": 0, "LastUpdated": "—", "Items": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	positions, err := h.deps.InventoryStore.ListPositions(ctx, tenantID, nil, nil, 200, 0)
	if err != nil {
		h.logger.Error("reportInventoryPage: list positions", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}

	items := make([]map[string]any, 0, len(positions))
	skus := map[uuid.UUID]struct{}{}
	locs := map[uuid.UUID]struct{}{}
	variance := 0
	var lastUpdate time.Time
	for _, p := range positions {
		skus[p.ItemID] = struct{}{}
		locs[p.LocationID] = struct{}{}
		// "snapshot" = on-hand at last count; "perpetual" = current on-hand.
		// Until snapshot tracking lands, we approximate snapshot via on_hand
		// minus reserved + in-transit.
		perpetual := strToFloat(&p.OnHandQuantity)
		snapshot := perpetual - strToFloat(&p.ReservedQuantity) - strToFloat(&p.InTransitQuantity)
		delta := perpetual - snapshot
		deltaStr := ""
		if delta != 0 {
			deltaStr = formatQty(delta)
			variance++
		}
		if p.UpdatedAt.After(lastUpdate) {
			lastUpdate = p.UpdatedAt
		}
		items = append(items, map[string]any{
			"SKU":          p.ItemID.String()[:8],
			"Description":  "—",
			"Location":     p.LocationID.String()[:8],
			"SnapshotQty":  formatQty(snapshot),
			"PerpetualQty": formatQty(perpetual),
			"Delta":        deltaStr,
		})
	}
	lastUpdated := "—"
	if !lastUpdate.IsZero() {
		lastUpdated = lastUpdate.Format("2006-01-02 15:04")
	}

	h.render(w, r, "report_inventory", "reports", map[string]any{
		"TotalSKUs":     len(skus),
		"Locations":     len(locs),
		"VarianceItems": variance,
		"LastUpdated":   lastUpdated,
		"Items":         items,
	})
}

// itemListPage renders the catalog search results.
// Wired W2c / GRO-817.
func (h *Handler) itemListPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if h.deps.ItemStore == nil {
		h.render(w, r, "items_list", "items", map[string]any{
			"Items": nil, "TotalCount": 0, "Query": query,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	items, err := h.deps.ItemStore.List(ctx, item.ListFilters{
		TenantID: tenantID,
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("itemListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "items", nil)
		return
	}

	// Resolve category names once via a single ListCategories lookup.
	catNames := h.categoryNameMap(ctx, tenantID)

	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		// In-memory filter when q is set.
		if query != "" && !containsIgnoreCase(it.SKU, query) && !containsIgnoreCase(it.Description, query) {
			continue
		}
		catName := "—"
		if it.CategoryID != nil {
			if name, ok := catNames[*it.CategoryID]; ok {
				catName = name
			}
		}
		rows = append(rows, map[string]any{
			"ID":          it.ID.String(),
			"SKU":         it.SKU,
			"Description": it.Description,
			"Category":    catName,
			"UnitPrice":   strDeref(it.DefaultPrice, "—"),
			"Margin":      computeMarginPct(it.DefaultPrice, it.DefaultCost),
			"Status":      it.Status,
		})
	}

	h.render(w, r, "items_list", "items", map[string]any{
		"Items":      rows,
		"TotalCount": len(rows),
		"Query":      query,
	})
}

// itemDetailPage renders one catalog item with attributes, supplier, and
// margin. Wired W2c / GRO-817.
func (h *Handler) itemDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "items", nil)
		return
	}

	if h.deps.ItemStore == nil {
		h.render(w, r, "items_detail", "items", map[string]any{
			"Item": map[string]any{
				"ID": idStr, "SKU": idStr, "Description": "—", "Category": "—",
				"Status": "active", "Supplier": "—", "UnitCost": "—",
				"UnitPrice": "—", "Margin": "—", "ReorderPoint": 0,
				"LeadDays": 0, "DriftAlertCount": 0, "LastDriftAt": "—",
			},
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	it, err := h.deps.ItemStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, item.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "items", nil)
			return
		}
		h.logger.Error("itemDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "items", nil)
		return
	}

	catName := "—"
	if it.CategoryID != nil {
		names := h.categoryNameMap(ctx, tenantID)
		if name, ok := names[*it.CategoryID]; ok {
			catName = name
		}
	}

	h.render(w, r, "items_detail", "items", map[string]any{
		"Item": map[string]any{
			"ID":              it.ID.String(),
			"SKU":             it.SKU,
			"Description":     it.Description,
			"Category":        catName,
			"Status":          it.Status,
			"Supplier":        "—", // populated when item→vendor join lands
			"UnitCost":        strDeref(it.DefaultCost, "—"),
			"UnitPrice":       strDeref(it.DefaultPrice, "—"),
			"Margin":          computeMarginPct(it.DefaultPrice, it.DefaultCost),
			"ReorderPoint":    0, // wires when inventory_thresholds surface lands
			"LeadDays":        0,
			"DriftAlertCount": 0, // wires after S.5.1 drift detection lands
			"LastDriftAt":     "—",
		},
	})
}

// promotionsCalendarPage lists active promotions for the tenant.
// Wired W2f / GRO-820. Uses uuid.Nil location which only matches promotions
// with NULL active_locations (i.e. tenant-wide promotions).
func (h *Handler) promotionsCalendarPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.PricingStore == nil {
		h.render(w, r, "promotions_calendar", "promotions", map[string]any{
			"Promotions": nil, "ActiveCount": 0, "UpcomingCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	promos, err := h.deps.PricingStore.ListActivePromotions(ctx, tenantID, uuid.Nil, time.Now())
	if err != nil {
		h.logger.Error("promotionsCalendarPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "promotions", nil)
		return
	}
	rows := make([]map[string]any, 0, len(promos))
	for _, p := range promos {
		end := "—"
		if p.EffectiveEnd != nil {
			end = p.EffectiveEnd.Format("2006-01-02")
		}
		stores := "All"
		if p.ActiveLocations != nil && len(p.ActiveLocations) > 0 {
			stores = strconv.Itoa(len(p.ActiveLocations))
		}
		rows = append(rows, map[string]any{
			"ID":       p.ID.String(),
			"Name":     p.Name,
			"Type":     p.PromotionType,
			"Start":    p.EffectiveStart.Format("2006-01-02"),
			"End":      end,
			"Discount": p.PromotionType, // detail comes from PromotionRules; out of scope
			"Stores":   stores,
		})
	}
	h.render(w, r, "promotions_calendar", "promotions", map[string]any{
		"Promotions":    rows,
		"ActiveCount":   len(rows),
		"UpcomingCount": 0,
	})
}

// reportFinancePage aggregates gross/net/discount/tax totals from the recent
// transaction set. Wired W2e / GRO-819. Uses an in-memory aggregate over the
// transaction.Store.List result (capped at 200 txns) until a dedicated
// totals-by-period aggregation method lands.
func (h *Handler) reportFinancePage(w http.ResponseWriter, r *http.Request) {
	if h.deps.TransactionStore == nil {
		h.render(w, r, "report_finance", "reports", map[string]any{
			"GrossSales": "—", "NetSales": "—", "COGS": "—", "GrossMargin": "—", "TenderRows": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	txns, err := h.deps.TransactionStore.List(ctx, transaction.ListFilters{
		TenantID: tenantID,
		Limit:    200,
	})
	if err != nil {
		h.logger.Error("reportFinancePage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	var gross, tax, discount float64
	for _, t := range txns {
		gross += parseDecimal(t.GrandTotal.String())
		tax += parseDecimal(t.TaxTotal.String())
		discount += parseDecimal(t.DiscountTotal.String())
	}
	net := gross - tax
	h.render(w, r, "report_finance", "reports", map[string]any{
		"GrossSales":  formatMoney(gross),
		"NetSales":    formatMoney(net),
		"COGS":        "—",
		"GrossMargin": "—",
		"TenderRows":  nil,
	})
	_ = discount
}

// parseDecimal coerces a string-decimal into float64. Returns 0 on parse error.
func parseDecimal(s string) float64 {
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// protocolOverviewPage renders a unified read-out of the cryptographic
// substrate — recent Bitcoin L2 anchors, .jeffe namespace registrations,
// and L402 verification tokens. Wired W7 / GRO-826.
//
// Cross-tenant view: anchors and tokens are platform-wide substrate, not
// tenant-scoped. Operators with portal access see all recent activity.
// Drill-down per surface (anchor proof viewer, charge dispute flow) is
// out of scope for this dispatch.
func (h *Handler) protocolOverviewPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"AnchorCount":    0,
		"NamespaceCount": 0,
		"SatsCollected":  "0",
		"PendingTokens":  0,
		"Anchors":        nil,
		"Namespaces":     nil,
		"Tokens":         nil,
	}

	ctx := r.Context()
	if h.deps.ProtocolValidate != nil {
		anchors, err := h.deps.ProtocolValidate.ListAnchors(ctx, 25)
		if err != nil {
			h.logger.Error("protocolOverviewPage: list anchors", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(anchors))
			for _, a := range anchors {
				rows = append(rows, map[string]any{
					"AnchorID":        a.AnchorID.String(),
					"MerkleRootShort": shortHex(a.MerkleRoot, 16),
					"EventCount":      a.EventCount,
					"Network":         a.Network,
					"Status":          a.AnchorStatus,
					"AnchoredAt":      a.AnchoredAt.Format(time.RFC3339),
				})
			}
			view["Anchors"] = rows
			view["AnchorCount"] = len(rows)
		}

		tokens, err := h.deps.ProtocolValidate.ListTokens(ctx, 25)
		if err != nil {
			h.logger.Error("protocolOverviewPage: list tokens", zap.Error(err))
		} else {
			var collected int64
			pending := 0
			tokRows := make([]map[string]any, 0, len(tokens))
			for _, t := range tokens {
				if t.Status == "paid" || t.Status == "consumed" {
					collected += t.SatoshiPrice
				}
				if t.Status == "pending" {
					pending++
				}
				tokRows = append(tokRows, map[string]any{
					"TokenShort": t.TokenID.String()[:8],
					"EventShort": shortHex(t.EventHash, 12),
					"Sats":       t.SatoshiPrice,
					"Status":     t.Status,
					"CreatedAt":  t.CreatedAt.Format(time.RFC3339),
				})
			}
			view["Tokens"] = tokRows
			view["SatsCollected"] = strconv.FormatInt(collected, 10)
			view["PendingTokens"] = pending
		}
	}

	if h.deps.ProtocolNamespace != nil {
		regs, err := h.deps.ProtocolNamespace.ListRecent(ctx, 25)
		if err != nil {
			h.logger.Error("protocolOverviewPage: list namespace", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(regs))
			for _, n := range regs {
				rows = append(rows, map[string]any{
					"Name":         n.Name,
					"OwnerType":    n.OwnerType,
					"Status":       n.RegStatus,
					"Network":      n.Network,
					"RegisteredAt": n.RegisteredAt.Format(time.RFC3339),
				})
			}
			view["Namespaces"] = rows
			view["NamespaceCount"] = len(rows)
		}
	}

	h.render(w, r, "protocol_overview", "protocol", view)
}

// shortHex truncates a hex string for display, appending an ellipsis when
// truncated. Used for Merkle roots / event hashes in the protocol portal.
func shortHex(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mcpToolsPage renders the catalog of registered MCP tools by reading
// the in-process Registry. Tools are grouped by module via a name-prefix
// convention (e.g. "canary.alert.list" → module "alert"). Wired W12 /
// GRO-831. Usage log + playground are follow-on.
func (h *Handler) mcpToolsPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.MCPRegistry == nil {
		h.render(w, r, "mcp_tools", "settings", map[string]any{
			"Tools": nil, "Count": 0,
		})
		return
	}
	defs := h.deps.MCPRegistry.List()
	rows := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		rows = append(rows, map[string]any{
			"Name":        d.Name,
			"Module":      mcpModuleFromName(d.Name),
			"Description": d.Description,
		})
	}
	h.render(w, r, "mcp_tools", "settings", map[string]any{
		"Tools": rows,
		"Count": len(rows),
	})
}

// mcpModuleFromName extracts a display module name from an MCP tool's
// dotted name. e.g. "canary.alert.list" → "alert".
func mcpModuleFromName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) >= 2 {
		return parts[1]
	}
	return "—"
}

// workflowsListPage renders all registered workflow definitions plus
// recent executions across the three engines (3-way match, L402 charge
// cycle, investigation lifecycle). Wired W4 / GRO-823. Per-engine
// drilldown + manual advance/cancel are a follow-on dispatch.
func (h *Handler) workflowsListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.WorkflowStore == nil {
		h.render(w, r, "workflows_list", "workflows", map[string]any{
			"Definitions": nil, "Executions": nil, "RunningCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	defs, err := h.deps.WorkflowStore.ListDefinitions(ctx)
	if err != nil {
		h.logger.Error("workflowsListPage: list definitions", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "workflows", nil)
		return
	}
	execs, err := h.deps.WorkflowStore.ListExecutions(ctx, workflow.ListExecutionsFilter{
		TenantID: tenantID,
		Limit:    200,
	})
	if err != nil {
		h.logger.Error("workflowsListPage: list executions", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "workflows", nil)
		return
	}

	// Map workflow_id → display_name for execution rows.
	defByID := map[uuid.UUID]workflow.Definition{}
	defViews := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		defByID[d.ID] = d
		defViews = append(defViews, map[string]any{
			"Code":        d.WorkflowCode,
			"DisplayName": d.DisplayName,
			"Version":     d.Version,
			"Status":      d.Status,
		})
	}

	running := 0
	execViews := make([]map[string]any, 0, len(execs))
	for _, e := range execs {
		if e.Status == "running" || e.Status == "pending" {
			running++
		}
		code := "—"
		if d, ok := defByID[e.WorkflowID]; ok {
			code = d.WorkflowCode
		}
		step := "—"
		if e.CurrentStep != nil && *e.CurrentStep != "" {
			step = *e.CurrentStep
		}
		execViews = append(execViews, map[string]any{
			"ShortID":      e.ID.String()[:8],
			"WorkflowCode": code,
			"Step":         step,
			"Status":       e.Status,
			"StartedAt":    e.StartedAt,
		})
	}

	h.render(w, r, "workflows_list", "workflows", map[string]any{
		"Definitions":  defViews,
		"Executions":   execViews,
		"RunningCount": running,
		"TotalCount":   len(execViews),
	})
}

// reportCasesPage aggregates case counts by domain + severity from the
// casemgmt store. Wired W2e / GRO-819.
func (h *Handler) reportCasesPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.CaseStore == nil {
		h.render(w, r, "report_cases", "reports", map[string]any{
			"TotalCases": 0, "OpenCases": 0, "AvgResolutionDays": "—",
			"RemediationsDispatched": 0, "ByDomain": nil, "BySeverity": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	cases, err := h.deps.CaseStore.ListCases(ctx, casemgmt.ListFilters{
		TenantID: tenantID,
		Limit:    500,
	})
	if err != nil {
		h.logger.Error("reportCasesPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	open := 0
	for _, c := range cases {
		if c.Status == "open" || c.Status == "investigating" {
			open++
		}
	}
	// Bucket by case_type (used as "domain") and severity.
	domainCounts := map[string]int{}
	domainOpen := map[string]int{}
	severityCounts := map[string]int{}
	for _, c := range cases {
		domain := c.CaseType
		if domain == "" {
			domain = "uncategorized"
		}
		domainCounts[domain]++
		if c.Status == "open" || c.Status == "investigating" {
			domainOpen[domain]++
		}
		sev := c.Severity
		if sev == "" {
			sev = "—"
		}
		severityCounts[sev]++
	}
	byDomain := make([]map[string]any, 0, len(domainCounts))
	for d, total := range domainCounts {
		byDomain = append(byDomain, map[string]any{
			"Domain":            d,
			"Total":             total,
			"Open":              domainOpen[d],
			"AvgResolutionDays": "—",
		})
	}
	bySeverity := make([]map[string]any, 0, len(severityCounts))
	for sev, count := range severityCounts {
		pct := "0%"
		if len(cases) > 0 {
			pct = strconv.FormatFloat(float64(count)/float64(len(cases))*100, 'f', 1, 64) + "%"
		}
		bySeverity = append(bySeverity, map[string]any{
			"Severity": sev,
			"Count":    count,
			"Pct":      pct,
		})
	}

	h.render(w, r, "report_cases", "reports", map[string]any{
		"TotalCases":             len(cases),
		"OpenCases":              open,
		"AvgResolutionDays":      "—",
		"RemediationsDispatched": 0,
		"ByDomain":               byDomain,
		"BySeverity":             bySeverity,
	})
}

// reportCategoryPage renders margin + volume by category.
// Wired W2c / GRO-817.
func (h *Handler) reportCategoryPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		h.render(w, r, "report_category", "reports", map[string]any{
			"TotalCategories": 0, "TopCategory": "—", "AvgMargin": "—", "SKUsTracked": 0, "Categories": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	cats, err := h.deps.ItemStore.ListCategories(ctx, tenantID)
	if err != nil {
		h.logger.Error("reportCategoryPage: list categories", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	items, err := h.deps.ItemStore.List(ctx, item.ListFilters{TenantID: tenantID, Limit: 500})
	if err != nil {
		h.logger.Error("reportCategoryPage: list items", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}

	// Bucket items by category and compute per-bucket aggregate margin.
	type bucket struct {
		count     int
		marginSum float64
	}
	buckets := map[uuid.UUID]*bucket{}
	for _, it := range items {
		if it.CategoryID == nil {
			continue
		}
		b, ok := buckets[*it.CategoryID]
		if !ok {
			b = &bucket{}
			buckets[*it.CategoryID] = b
		}
		b.count++
		if margin := marginPct(it.DefaultPrice, it.DefaultCost); margin >= 0 {
			b.marginSum += margin
		}
	}

	rows := make([]map[string]any, 0, len(cats))
	totalMargin := 0.0
	totalSKUs := 0
	topName := "—"
	topCount := 0
	for _, c := range cats {
		b := buckets[c.ID]
		count := 0
		avgMargin := "—"
		if b != nil {
			count = b.count
			if count > 0 {
				avg := b.marginSum / float64(count)
				avgMargin = strconv.FormatFloat(avg, 'f', 1, 64) + "%"
				totalMargin += avg
			}
		}
		if count > topCount {
			topCount = count
			topName = c.Name
		}
		totalSKUs += count
		rows = append(rows, map[string]any{
			"Name":       c.Name,
			"SKUCount":   count,
			"TotalSales": "—", // wires when sales aggregation per category lands
			"AvgMargin":  avgMargin,
			"Turn":       "—",
		})
	}
	avgMarginAll := "—"
	if len(rows) > 0 {
		avgMarginAll = strconv.FormatFloat(totalMargin/float64(len(rows)), 'f', 1, 64) + "%"
	}

	h.render(w, r, "report_category", "reports", map[string]any{
		"TotalCategories": len(cats),
		"TopCategory":     topName,
		"AvgMargin":       avgMarginAll,
		"SKUsTracked":     totalSKUs,
		"Categories":      rows,
	})
}

// categoryNameMap fetches all category names for the tenant once for cheap
// in-memory lookup. Returns an empty map on store error.
func (h *Handler) categoryNameMap(ctx context.Context, tenantID uuid.UUID) map[uuid.UUID]string {
	if h.deps.ItemStore == nil {
		return nil
	}
	cats, err := h.deps.ItemStore.ListCategories(ctx, tenantID)
	if err != nil {
		return nil
	}
	out := make(map[uuid.UUID]string, len(cats))
	for _, c := range cats {
		out[c.ID] = c.Name
	}
	return out
}

func strDeref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func containsIgnoreCase(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func computeMarginPct(price, cost *string) string {
	m := marginPct(price, cost)
	if m < 0 {
		return "—"
	}
	return strconv.FormatFloat(m, 'f', 1, 64) + "%"
}

func marginPct(price, cost *string) float64 {
	p := strToFloat(price)
	c := strToFloat(cost)
	if p == 0 {
		return -1
	}
	return ((p - c) / p) * 100
}

// employeeDetailPage renders one employee with productivity metrics from
// internal/employee.Store. Wired W2g / GRO-821.
func (h *Handler) employeeDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "employees", nil)
		return
	}
	if h.deps.EmployeeStore == nil {
		h.render(w, r, "employees_detail", "employees", map[string]any{
			"Employee": map[string]any{
				"ID": idStr, "Name": "Employee " + idStr, "Role": "cashier", "Store": "—",
				"TxnPerHour": "—", "AvgTxnValue": "—", "VoidRate": "—",
				"DiscountRate": "—", "CompRate": "—", "CaseCount": 0, "AlertCount": 0,
			},
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	emp, err := h.deps.EmployeeStore.GetByID(ctx, tenantID, id)
	if err != nil {
		// employee.Store returns sql.ErrNoRows → wrap as 404
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "employees", nil)
		return
	}
	name := emp.FirstName + " " + emp.LastName
	if emp.DisplayName != nil && *emp.DisplayName != "" {
		name = *emp.DisplayName
	}
	role := "—"
	if emp.PayType != nil {
		role = *emp.PayType
	}

	h.render(w, r, "employees_detail", "employees", map[string]any{
		"Employee": map[string]any{
			"ID":           emp.ID.String(),
			"Name":         name,
			"Role":         role,
			"Store":        "—", // wires when employee→primary_location join lands
			"TxnPerHour":   "—",
			"AvgTxnValue":  "—",
			"VoidRate":     "—",
			"DiscountRate": "—",
			"CompRate":     "—",
			"CaseCount":    0,
			"AlertCount":   0,
		},
	})
}

// reportLaborPage renders the productivity dashboard with per-employee alert
// summaries from internal/employee.Store.AlertSummaries. Wired W2g / GRO-821.
func (h *Handler) reportLaborPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.EmployeeStore == nil {
		h.render(w, r, "report_labor", "reports", map[string]any{
			"ActiveEmployees": 0, "StoreAvgTxnHr": "—", "TopTxnHr": "—",
			"FlagRate": "—", "Employees": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	emps, err := h.deps.EmployeeStore.List(ctx, employee.ListFilters{
		TenantID: tenantID,
		Limit:    200,
	})
	if err != nil {
		h.logger.Error("reportLaborPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	summaries, err := h.deps.EmployeeStore.AlertSummaries(ctx, tenantID)
	if err != nil {
		h.logger.Error("reportLaborPage: alert summaries", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}
	alertsByEmp := map[uuid.UUID]int64{}
	for _, s := range summaries {
		alertsByEmp[s.EmployeeID] = s.TotalAlerts
	}
	active := 0
	rows := make([]map[string]any, 0, len(emps))
	for _, e := range emps {
		if e.EmploymentStatus == "active" {
			active++
		}
		name := e.FirstName + " " + e.LastName
		if e.DisplayName != nil && *e.DisplayName != "" {
			name = *e.DisplayName
		}
		rows = append(rows, map[string]any{
			"ID":         e.ID.String(),
			"Name":       name,
			"Store":      "—",
			"TxnHr":      "—",
			"AlertCount": alertsByEmp[e.ID],
		})
	}
	h.render(w, r, "report_labor", "reports", map[string]any{
		"ActiveEmployees": active,
		"StoreAvgTxnHr":   "—",
		"TopTxnHr":        "—",
		"FlagRate":        "—",
		"Employees":       rows,
	})
}

// receivingListPage lists open + recent goods_receipt documents.
// Wired W2d / GRO-818.
func (h *Handler) receivingListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_list", "receiving", map[string]any{
			"Sessions": nil, "OpenCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    []string{inventory.DocumentTypeGoodsReceipt},
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("receivingListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	open := 0
	sessions := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if d.Status == "draft" || d.Status == "in_progress" {
			open++
		}
		sessions = append(sessions, receivingRowView(d))
	}
	h.render(w, r, "receiving_list", "receiving", map[string]any{
		"Sessions":   sessions,
		"OpenCount":  open,
		"TotalCount": len(sessions),
	})
}

// receivingDetailPage renders one goods_receipt document with hydrated lines.
// Wired W2d / GRO-818.
func (h *Handler) receivingDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "receiving", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_detail", "receiving", map[string]any{
			"Session": map[string]any{
				"ID": idStr, "ShortID": shortID, "PONumber": "—", "Vendor": "—",
				"Status": "open", "ReceivedBy": "—", "OpenedAt": "—",
			},
			"Lines": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("receivingDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	view := receivingRowView(*doc)
	view["ReceivedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["ReceivedBy"] = doc.PerformedByUserID.String()[:8]
	}
	view["OpenedAt"] = doc.CreatedAt.Format(time.RFC3339)
	lineRows := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		lineRows = append(lineRows, transferLineView(l)) // shared shape: SKU/Description/QtyShipped/QtyReceived/Variance
	}
	h.render(w, r, "receiving_detail", "receiving", map[string]any{
		"Session": view,
		"Lines":   lineRows,
	})
}

// receivingClosePage renders the close-and-post summary for a goods_receipt
// document. Wired W2d / GRO-818. Close action POST is W5 / GRO-824.
func (h *Handler) receivingClosePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "receiving", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_close", "receiving", map[string]any{
			"Session":   map[string]any{"ID": idStr, "ShortID": shortID, "PONumber": "—", "Vendor": "—"},
			"LineCount": 0, "TotalReceived": 0, "DiscrepancyCount": 0, "Discrepancies": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingClosePage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("receivingClosePage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	totalReceived := 0.0
	disc := make([]map[string]any, 0)
	for _, l := range lines {
		totalReceived += strToFloat(l.ActualQuantity)
		if v := strToFloat(&l.VarianceQuantity); v != 0 {
			disc = append(disc, transferLineView(l))
		}
	}
	view := receivingRowView(*doc)
	h.render(w, r, "receiving_close", "receiving", map[string]any{
		"Session":          view,
		"LineCount":        len(lines),
		"TotalReceived":    formatQty(totalReceived),
		"DiscrepancyCount": len(disc),
		"Discrepancies":    disc,
	})
}

// returnsListPage lists RTV (return-to-vendor) documents.
// Wired W2d / GRO-818.
func (h *Handler) returnsListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "returns_list", "returns", map[string]any{
			"Returns": nil, "PendingCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    []string{inventory.DocumentTypeRTV},
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("returnsListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	pending := 0
	rows := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if d.Status == "draft" || d.Status == "in_progress" {
			pending++
		}
		rows = append(rows, returnRowView(d))
	}
	h.render(w, r, "returns_list", "returns", map[string]any{
		"Returns":      rows,
		"PendingCount": pending,
		"TotalCount":   len(rows),
	})
}

// returnsDetailPage renders one RTV document with hydrated lines.
// Wired W2d / GRO-818.
func (h *Handler) returnsDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "returns", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "returns_detail", "returns", map[string]any{
			"Return": map[string]any{
				"ID": idStr, "ShortID": shortID, "Vendor": "—", "Status": "pending",
				"InitiatedBy": "—", "InitiatedAt": "—",
				"CreditExpected": "—", "CreditReceived": "—", "Reconciled": false,
			},
			"Items": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "returns", nil)
			return
		}
		h.logger.Error("returnsDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("returnsDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	view := returnRowView(*doc)
	view["InitiatedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["InitiatedBy"] = doc.PerformedByUserID.String()[:8]
	}
	view["InitiatedAt"] = doc.CreatedAt.Format(time.RFC3339)
	view["CreditExpected"] = strDeref(doc.TotalCost, "—")
	view["CreditReceived"] = "—"
	view["Reconciled"] = doc.Status == "reconciled"

	items := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		items = append(items, transferLineView(l))
	}
	h.render(w, r, "returns_detail", "returns", map[string]any{
		"Return": view,
		"Items":  items,
	})
}

// receivingRowView is the shared row→view-model for receiving list/detail.
func receivingRowView(d inventory.DocumentDTO) map[string]any {
	vendor := "—"
	if d.VendorID != nil {
		vendor = d.VendorID.String()[:8]
	}
	return map[string]any{
		"ID":       d.ID.String(),
		"ShortID":  d.ID.String()[:8],
		"PONumber": d.DocumentNumber,
		"Vendor":   vendor,
		"Status":   d.Status,
	}
}

// returnRowView is the shared row→view-model for RTV list/detail.
func returnRowView(d inventory.DocumentDTO) map[string]any {
	vendor := "—"
	if d.VendorID != nil {
		vendor = d.VendorID.String()[:8]
	}
	itemCount := 0
	if d.TotalQuantity != nil {
		itemCount = int(strToFloat(d.TotalQuantity))
	}
	return map[string]any{
		"ID":        d.ID.String(),
		"ShortID":   d.ID.String()[:8],
		"Vendor":    vendor,
		"Status":    d.Status,
		"ItemCount": itemCount,
	}
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

func stubHawkAnalytics(_ *http.Request) any {
	return map[string]any{
		"OpenCount": 0, "ClosedThisMonth": 0,
		"AvgResolutionDays": "—", "TotalEvidenceItems": 0,
		"ByRule": nil, "ByStore": nil,
	}
}

func stubHawkPatterns(_ *http.Request) any {
	return map[string]any{
		"TopSubjects": nil, "RulePairs": nil, "SubjectTimeline": nil,
	}
}

func (h *Handler) hawkEvidencePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "cases", nil)
		return
	}
	if h.deps.CaseStore == nil {
		shortID := idStr
		if len(idStr) >= 8 {
			shortID = idStr[:8]
		}
		h.render(w, r, "hawk_evidence", "cases", map[string]any{
			"Case":     map[string]any{"ID": idStr, "ShortID": shortID, "Title": "Case " + shortID},
			"Evidence": nil,
		})
		return
	}
	tenantID := tenantIDFromCtx(ctx)
	c, err := h.deps.CaseStore.GetCase(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, casemgmt.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "cases", nil)
			return
		}
		h.logger.Error("hawkEvidencePage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "cases", nil)
		return
	}
	evidence, _ := h.deps.CaseStore.ListEvidence(ctx, id)
	h.render(w, r, "hawk_evidence", "cases", map[string]any{
		"Case":     c,
		"Evidence": evidence,
	})
}

// customersListPage is search-first: if no ?q param, renders the empty search
// state. If ?q is provided and a CustomerStore is wired, runs a full-text
// search against customer.customers.
func (h *Handler) customersListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query().Get("q")
	tenantID := tenantIDFromCtx(ctx)

	var customers []map[string]any
	totalCount := 0

	if h.deps.CustomerStore != nil && q != "" {
		results, err := h.deps.CustomerStore.List(ctx, customer.ListFilters{
			TenantID: tenantID,
			Search:   q,
			Limit:    50,
		})
		if err != nil {
			h.logger.Error("customersListPage: list", zap.Error(err))
		} else {
			totalCount = len(results)
			customers = make([]map[string]any, 0, len(results))
			for _, c := range results {
				name := customerDisplayName(c)
				shortID := c.ID.String()[:8]
				customers = append(customers, map[string]any{
					"ID":              c.ID.String(),
					"ShortID":         shortID,
					"Name":            name,
					"RiskTier":        "—",
					"LastPurchaseDate": "—",
				})
			}
		}
	}

	h.render(w, r, "customers_list", "customers", map[string]any{
		"Customers":  customers,
		"TotalCount": totalCount,
		"Query":      q,
	})
}

func (h *Handler) customerDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "customers", nil)
		return
	}

	if h.deps.CustomerStore == nil {
		// No store wired — fall through to stub.
		shortID := idStr
		if len(idStr) >= 8 {
			shortID = idStr[:8]
		}
		h.render(w, r, "customers_detail", "customers", map[string]any{
			"Customer": map[string]any{
				"ID":          idStr,
				"ShortID":     shortID,
				"Name":        "Customer " + shortID,
				"RiskScore":   0,
				"RiskTier":    "low",
				"MemberSince": "—",
				"CaseCount":   0,
			},
			"Purchases": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	c, err := h.deps.CustomerStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "customers", nil)
			return
		}
		h.logger.Error("customerDetailPage: get", zap.String("id", idStr), zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "customers", nil)
		return
	}

	name := customerDisplayName(*c)
	shortID := c.ID.String()[:8]
	h.render(w, r, "customers_detail", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":          c.ID.String(),
			"ShortID":     shortID,
			"Name":        name,
			"RiskScore":   0,
			"RiskTier":    "—",
			"MemberSince": c.CreatedAt.Format("Jan 2006"),
			"CaseCount":   0,
		},
		"Purchases": nil,
	})
}

func (h *Handler) customerRiskPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "customers", nil)
		return
	}

	if h.deps.CustomerStore == nil {
		shortID := idStr
		if len(idStr) >= 8 {
			shortID = idStr[:8]
		}
		h.render(w, r, "customers_risk", "customers", map[string]any{
			"Customer": map[string]any{
				"ID":        idStr,
				"ShortID":   shortID,
				"Name":      "Customer " + shortID,
				"RiskScore": 0,
				"RiskTier":  "low",
			},
			"Signals":   nil,
			"RuleFires": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	c, err := h.deps.CustomerStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "customers", nil)
			return
		}
		h.logger.Error("customerRiskPage: get", zap.String("id", idStr), zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "customers", nil)
		return
	}

	name := customerDisplayName(*c)
	shortID := c.ID.String()[:8]
	h.render(w, r, "customers_risk", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":        c.ID.String(),
			"ShortID":   shortID,
			"Name":      name,
			"RiskScore": 0,
			"RiskTier":  "—",
		},
		"Signals":   nil,
		"RuleFires": nil,
	})
}

func (h *Handler) customerContextPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "customers", nil)
		return
	}

	if h.deps.CustomerStore == nil {
		shortID := idStr
		if len(idStr) >= 8 {
			shortID = idStr[:8]
		}
		h.render(w, r, "customers_context", "customers", map[string]any{
			"Customer": map[string]any{
				"ID":        idStr,
				"ShortID":   shortID,
				"Name":      "Customer " + shortID,
				"RiskScore": 0,
				"RiskTier":  "low",
			},
			"Cases":  nil,
			"Chirps": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	c, err := h.deps.CustomerStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "customers", nil)
			return
		}
		h.logger.Error("customerContextPage: get", zap.String("id", idStr), zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "customers", nil)
		return
	}

	name := customerDisplayName(*c)
	shortID := c.ID.String()[:8]
	h.render(w, r, "customers_context", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":        c.ID.String(),
			"ShortID":   shortID,
			"Name":      name,
			"RiskScore": 0,
			"RiskTier":  "—",
		},
		"Cases":  nil,
		"Chirps": nil,
	})
}

// customerDisplayName returns a human-readable name from a CustomerDTO.
// Falls back through DisplayName → FirstName+LastName → CustomerCode → ID.
func customerDisplayName(c customer.CustomerDTO) string {
	if c.DisplayName != nil && *c.DisplayName != "" {
		return *c.DisplayName
	}
	if c.FirstName != nil || c.LastName != nil {
		first := ""
		last := ""
		if c.FirstName != nil {
			first = *c.FirstName
		}
		if c.LastName != nil {
			last = *c.LastName
		}
		if n := first + " " + last; n != " " {
			return n
		}
	}
	if c.CustomerCode != nil && *c.CustomerCode != "" {
		return *c.CustomerCode
	}
	return c.ID.String()[:8]
}

func (h *Handler) exceptionDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "exceptions_detail", "exceptions", map[string]any{
		"Exception": map[string]any{
			"ID":             id,
			"ShortID":        shortID,
			"Domain":         "—",
			"Type":           "—",
			"Severity":       "high",
			"Status":         "open",
			"Store":          "—",
			"DetectedAt":     "—",
			"AssignedTo":     "—",
			"TriggerRule":    "—",
			"TriggerProcess": "—",
			"SignalSummary":  "—",
		},
	})
}

func (h *Handler) casesNewPage(w http.ResponseWriter, r *http.Request) {
	exceptionID := r.URL.Query().Get("exception")
	preFillTitle := ""
	if exceptionID != "" {
		preFillTitle = "Exception " + exceptionID
	}
	h.render(w, r, "cases_new", "cases", map[string]any{
		"ExceptionID":  exceptionID,
		"PreFillTitle": preFillTitle,
	})
}

func (h *Handler) casesEvidencePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "cases_evidence", "cases", map[string]any{
		"Case": map[string]any{
			"ID":      id,
			"ShortID": shortID,
			"Title":   "Case " + shortID,
		},
		"Evidence": nil,
		"DomainCounts": map[string]int{
			"lp":        0,
			"inventory": 0,
			"finance":   0,
			"receiving": 0,
		},
	})
}

func (h *Handler) casesCorrelationPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "cases_correlation", "cases", map[string]any{
		"Case": map[string]any{
			"ID":      id,
			"ShortID": shortID,
		},
		"Subjects": nil,
		"Timeline": nil,
	})
}

func (h *Handler) casesRemediatePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "cases_remediate", "cases", map[string]any{
		"Case": map[string]any{
			"ID":      id,
			"ShortID": shortID,
			"Title":   "Case " + shortID,
		},
		"Remediations": nil,
	})
}

// tenantIDFromCtx extracts the tenant UUID from the request context.
// Returns uuid.Nil until auth middleware (GRO-769) is wired.
func tenantIDFromCtx(ctx context.Context) uuid.UUID {
	// TODO(GRO-769): replace with identity.TenantIDFromCtx(ctx)
	return uuid.Nil
}
