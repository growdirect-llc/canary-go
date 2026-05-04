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
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/chirp"
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
	r.Get("/rules", h.page("rules", "rules", stubRules))
	r.Get("/connect", h.page("connect", "connect", stubConnect))
	r.Get("/welcome", h.page("welcome", "welcome", nil))

	// Hawk case management
	r.Get("/cases/hawk", h.page("cases", "hawk_list", stubHawkList))
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
	r.Get("/customers", h.page("customers", "customers_list", stubCustomersList))
	r.Get("/customers/{id}", h.customerDetailPage)
	r.Get("/customers/{id}/risk", h.customerRiskPage)
	r.Get("/customers/{id}/context", h.customerContextPage)

	// Settings sub-pages
	r.Get("/settings/allowlist/dead-count", h.page("settings", "settings_allowlist_dead_count", func(_ *http.Request) any { return map[string]any{"Entries": nil, "StoreID": "—"} }))
	r.Get("/settings/allowlist/discounts", h.page("settings", "settings_allowlist_discounts", func(_ *http.Request) any { return map[string]any{"Entries": nil} }))
	r.Get("/settings/allowlist/voids", h.page("settings", "settings_allowlist_voids", func(_ *http.Request) any { return map[string]any{"Entries": nil} }))
	r.Get("/settings/allowlist/comps", h.page("settings", "settings_allowlist_comps", func(_ *http.Request) any { return map[string]any{"Entries": nil} }))
	r.Get("/settings/training-mode", h.page("settings", "settings_training_mode", func(_ *http.Request) any { return map[string]any{"Enabled": false, "ActiveWindow": nil, "RecentWindows": nil} }))
	r.Get("/settings/alert-routing", h.page("settings", "settings_alert_routing", func(_ *http.Request) any { return map[string]any{"Routes": nil} }))
	r.Get("/settings/store/drawer", h.page("settings", "settings_store_drawer", func(_ *http.Request) any { return map[string]any{"Thresholds": nil} }))
	r.Get("/settings/store/discounts", h.page("settings", "settings_store_discounts", func(_ *http.Request) any { return map[string]any{"Caps": nil} }))
	r.Get("/settings/store/void-reasons", h.page("settings", "settings_store_void_reasons", func(_ *http.Request) any { return map[string]any{"Codes": nil} }))
	r.Get("/settings/store/comp-reasons", h.page("settings", "settings_store_comp_reasons", func(_ *http.Request) any { return map[string]any{"Codes": nil} }))
	r.Get("/settings/devices", h.page("settings", "settings_devices", func(_ *http.Request) any {
		return map[string]any{"Online": 0, "Offline": 0, "Degraded": 0, "Devices": nil}
	}))
	r.Get("/settings/devices/new", h.page("settings", "settings_devices_new", func(_ *http.Request) any {
		return map[string]any{}
	}))
	r.Get("/settings/store", h.page("settings", "settings_store_config", func(_ *http.Request) any {
		return map[string]any{"StoreID": "—", "POSSource": "—", "LastSync": "—", "ActiveRuleCount": 0, "AllowListCount": 0, "TrainingMode": false}
	}))

	// Transfers
	r.Get("/transfers", h.page("transfers", "transfers_list", func(_ *http.Request) any {
		return map[string]any{"Transfers": nil, "InTransitCount": 0, "TotalCount": 0}
	}))
	r.Get("/transfers/{id}", h.transferDetailPage)
	r.Get("/transfers/{id}/variance", h.transferVariancePage)

	// Reports
	r.Get("/reports/distribution", h.page("reports", "report_distribution", func(_ *http.Request) any {
		return map[string]any{"TotalTransfers": 0, "InTransit": 0, "VarianceFlags": 0, "Resolved": 0, "Lanes": nil}
	}))
	r.Get("/reports/inventory", h.page("reports", "report_inventory", func(_ *http.Request) any {
		return map[string]any{"TotalSKUs": 0, "Locations": 0, "VarianceItems": 0, "LastUpdated": "—", "Items": nil}
	}))
	r.Get("/reports/category", h.page("reports", "report_category", func(_ *http.Request) any {
		return map[string]any{"TotalCategories": 0, "TopCategory": "—", "AvgMargin": "—", "SKUsTracked": 0, "Categories": nil}
	}))

	// Items
	r.Get("/items", h.page("items", "items_list", func(r *http.Request) any {
		return map[string]any{"Items": nil, "TotalCount": 0, "Query": r.URL.Query().Get("q")}
	}))
	r.Get("/items/{id}", h.itemDetailPage)

	// Finance reports
	r.Get("/reports/finance", h.page("reports", "report_finance", func(_ *http.Request) any {
		return map[string]any{"GrossSales": "—", "NetSales": "—", "COGS": "—", "GrossMargin": "—", "TenderRows": nil}
	}))
	r.Get("/reports/payments", h.page("reports", "report_payments", func(_ *http.Request) any {
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
	r.Get("/promotions", h.page("promotions", "promotions_calendar", func(_ *http.Request) any {
		return map[string]any{"Promotions": nil, "ActiveCount": 0, "UpcomingCount": 0}
	}))
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
	r.Get("/reports/labor", h.page("reports", "report_labor", func(_ *http.Request) any {
		return map[string]any{"ActiveEmployees": 0, "StoreAvgTxnHr": "—", "TopTxnHr": "—", "FlagRate": "—", "Employees": nil}
	}))

	// Receiving workflow
	r.Get("/receiving", h.page("receiving", "receiving_list", func(_ *http.Request) any {
		return map[string]any{"Sessions": nil, "OpenCount": 0, "TotalCount": 0}
	}))
	r.Get("/receiving/{id}", h.receivingDetailPage)
	r.Get("/receiving/{id}/close", h.receivingClosePage)

	// Returns / RTV workflow
	r.Get("/returns", h.page("returns", "returns_list", func(_ *http.Request) any {
		return map[string]any{"Returns": nil, "PendingCount": 0, "TotalCount": 0}
	}))
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

	// Case analytics report
	r.Get("/reports/cases", h.page("reports", "report_cases", func(_ *http.Request) any {
		return map[string]any{"TotalCases": 0, "OpenCases": 0, "AvgResolutionDays": "—", "RemediationsDispatched": 0, "ByDomain": nil, "BySeverity": nil}
	}))

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

func (h *Handler) ruleDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.render(w, r, "rule_detail", "rules", map[string]any{
		"Rule": map[string]any{
			"ID": id, "Name": "Rule " + id,
			"Severity":      "high",
			"Category":      "—",
			"Description":   "—",
			"Enabled":       false,
			"FireCount":     0,
			"FiresToday":    0,
			"FiresThisWeek": 0,
			"Parameters":    nil,
		},
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

func (h *Handler) transactionDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "transaction_detail", "transactions", map[string]any{
		"Transaction": map[string]any{
			"ID": id, "ShortID": shortID, "POSSource": "—",
			"Amount": "—", "Cashier": "—", "StoreID": "—",
			"Hash":        "0000000000000000000000000000000000000000000000000000000000000000",
			"SealStatus":  "pending",
			"ParseStatus": "pending",
			"CreatedAt":   "—",
		},
		"Events": nil, "LineItems": nil, "AlertCount": 0,
	})
}

func (h *Handler) transactionProofPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "transaction_proof", "transactions", map[string]any{
		"Transaction": map[string]any{
			"ID": id, "ShortID": shortID,
			"Hash":      "0000000000000000000000000000000000000000000000000000000000000000",
			"CreatedAt": "—",
		},
		"ProofStatus": "pending",
		"MerklePath":  nil,
		"RootHash":    "—",
		"AnchorRef":   "—",
		"AnchoredAt":  "—",
	})
}

func (h *Handler) transferDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "transfers_detail", "transfers", map[string]any{
		"Transfer": map[string]any{
			"ID": id, "ShortID": shortID, "FromStore": "—", "ToStore": "—",
			"Status": "in-transit", "StatusClass": "", "ItemCount": 0,
			"InitiatedBy": "—", "InitiatedAt": "—", "ExpectedArrival": "—",
		},
		"Lines": nil,
	})
}

func (h *Handler) transferVariancePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "transfers_variance", "transfers", map[string]any{
		"Transfer": map[string]any{"ID": id, "ShortID": shortID, "FromStore": "—", "ToStore": "—"},
		"ShippedTotal": 0, "ReceivedTotal": 0, "VarianceCount": 0, "ValueAtRisk": "—",
		"Lines": nil,
	})
}

func (h *Handler) itemDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.render(w, r, "items_detail", "items", map[string]any{
		"Item": map[string]any{
			"ID": id, "SKU": id, "Description": "—", "Category": "—",
			"Status": "active", "Supplier": "—", "UnitCost": "—",
			"UnitPrice": "—", "Margin": "—", "ReorderPoint": 0,
			"LeadDays": 0, "DriftAlertCount": 0, "LastDriftAt": "—",
		},
	})
}

func (h *Handler) employeeDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.render(w, r, "employees_detail", "employees", map[string]any{
		"Employee": map[string]any{
			"ID": id, "Name": "Employee " + id, "Role": "cashier", "Store": "—",
			"TxnPerHour": "—", "AvgTxnValue": "—", "VoidRate": "—",
			"DiscountRate": "—", "CompRate": "—", "CaseCount": 0, "AlertCount": 0,
		},
	})
}

func (h *Handler) receivingDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "receiving_detail", "receiving", map[string]any{
		"Session": map[string]any{
			"ID": id, "ShortID": shortID, "PONumber": "—", "Vendor": "—",
			"Status": "open", "ReceivedBy": "—", "OpenedAt": "—",
		},
		"Lines": nil,
	})
}

func (h *Handler) receivingClosePage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "receiving_close", "receiving", map[string]any{
		"Session": map[string]any{"ID": id, "ShortID": shortID, "PONumber": "—", "Vendor": "—"},
		"LineCount": 0, "TotalReceived": 0, "DiscrepancyCount": 0, "Discrepancies": nil,
	})
}

func (h *Handler) returnsDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "returns_detail", "returns", map[string]any{
		"Return": map[string]any{
			"ID": id, "ShortID": shortID, "Vendor": "—", "Status": "pending",
			"InitiatedBy": "—", "InitiatedAt": "—",
			"CreditExpected": "—", "CreditReceived": "—", "Reconciled": false,
		},
		"Items": nil,
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
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "hawk_evidence", "cases", map[string]any{
		"Case": map[string]any{
			"ID": id, "ShortID": shortID, "Title": "Case " + shortID,
		},
		"Evidence": nil,
	})
}

func stubCustomersList(r *http.Request) any {
	return map[string]any{
		"Customers":  nil,
		"TotalCount": 0,
		"Query":      r.URL.Query().Get("q"),
	}
}

func (h *Handler) customerDetailPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "customers_detail", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":          id,
			"ShortID":     shortID,
			"Name":        "Customer " + shortID,
			"RiskScore":   0,
			"RiskTier":    "low",
			"MemberSince": "—",
			"CaseCount":   0,
		},
		"Purchases": nil,
	})
}

func (h *Handler) customerRiskPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "customers_risk", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":        id,
			"ShortID":   shortID,
			"Name":      "Customer " + shortID,
			"RiskScore": 0,
			"RiskTier":  "low",
		},
		"Signals":   nil,
		"RuleFires": nil,
	})
}

func (h *Handler) customerContextPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	shortID := id
	if len(id) >= 8 {
		shortID = id[:8]
	}
	h.render(w, r, "customers_context", "customers", map[string]any{
		"Customer": map[string]any{
			"ID":        id,
			"ShortID":   shortID,
			"Name":      "Customer " + shortID,
			"RiskScore": 0,
			"RiskTier":  "low",
		},
		"Cases":  nil,
		"Chirps": nil,
	})
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
