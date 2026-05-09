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
// is wired. The User field in PageData will be populated by
// the auth middleware once it lands.
package web

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/casemgmt"
	"github.com/ruptiv/canary/internal/customer"
	"github.com/ruptiv/canary/internal/employee"
	"github.com/ruptiv/canary/internal/inventory"
	"github.com/ruptiv/canary/internal/item"
	"github.com/ruptiv/canary/internal/tenant"
	"github.com/ruptiv/canary/internal/transaction"
	"github.com/ruptiv/canary/internal/workflow"
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

	// CSRF — populated by render() from gorilla/csrf when the
	// middleware is wired (production path). Tests that build a
	// Handler without the gateway wrapper see empty values, which
	// is fine: they don't go through the CSRF gate.
	//
	// Forms embed {{ .CSRFField }} as a hidden input.
	// HTMX/Alpine reads {{ .CSRFToken }} from a base.html meta tag.
	// T-E.
	CSRFField template.HTML
	CSRFToken string
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
	h.mustParse("items_new", "templates/items/new.html")
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
	h.mustParse("owl_dashboards", "templates/owl/dashboards.html")
	h.mustParse("owl_parties", "templates/owl/parties.html")
	h.mustParse("owl_lp_performance", "templates/owl/lp_performance.html")
	h.mustParse("tasks", "templates/tasks.html")
	h.mustParse("assets_list", "templates/assets/list.html")
	h.mustParse("assets_detail", "templates/assets/detail.html")
	h.mustParse("billing_overview", "templates/billing/overview.html")
	h.mustParse("billing_invoices", "templates/billing/invoices.html")
	h.mustParse("billing_payment_method", "templates/billing/payment_method.html")
	h.mustParse("admin_audit", "templates/admin/audit.html")
	h.mustParse("admin_iso27001", "templates/admin/iso27001.html")
	h.mustParse("admin_users", "templates/admin/users.html")
	h.mustParse("admin_config", "templates/admin/config.html")
	h.mustParse("admin_hierarchy", "templates/admin/hierarchy.html")
	h.mustParse("admin_network_integrity", "templates/admin/network_integrity.html")
	h.mustParse("dashboards_cross_store", "templates/dashboards/cross_store.html")
	h.mustParse("suppliers_list", "templates/suppliers/list.html")
	h.mustParse("suppliers_detail", "templates/suppliers/detail.html")
	h.mustParse("suppliers_scorecard", "templates/suppliers/scorecard.html")
	h.mustParse("po_list", "templates/po/list.html")
	h.mustParse("po_detail", "templates/po/detail.html")
	h.mustParse("po_match", "templates/po/match.html")
	h.mustParseShared("onboarding_connect", "templates/onboarding/connect.html", "templates/onboarding/progress.html")
	h.mustParseShared("onboarding_import", "templates/onboarding/import.html", "templates/onboarding/progress.html")
	h.mustParseShared("onboarding_rules", "templates/onboarding/rules.html", "templates/onboarding/progress.html")
	h.mustParseShared("onboarding_welcome", "templates/onboarding/welcome.html", "templates/onboarding/progress.html")
	h.mustParseMobile("m_tasks", "templates/mobile/tasks.html")
	h.mustParseMobile("m_receiving", "templates/mobile/receiving.html")
	h.mustParseMobile("m_cycle_count", "templates/mobile/cycle_count.html")
	h.mustParseMobile("m_alert_detail", "templates/mobile/alert_detail.html")
	h.mustParseMobile("m_alerts", "templates/mobile/alert_detail.html")
	h.mustParse("ecom_orders", "templates/ecom/orders.html")
	h.mustParse("ecom_sync", "templates/ecom/sync.html")
	return h
}

// parseTemplateSet creates a template set with componentFuncs registered and
// templates/components/*.html loaded, then parses the supplied files into it.
// The returned set is named after the basename of the first file so callers
// using Execute() on a standalone page template still work (the stdlib
// ParseFS convention).
//
// All page-level template parses go through this so every page can compose
// from the {{template "components/<name>" ...}} primitives in
// templates/components/.
func parseTemplateSet(files ...string) *template.Template {
	if len(files) == 0 {
		panic("parseTemplateSet: at least one file required")
	}
	patterns := append([]string{}, files...)
	patterns = append(patterns, "templates/components/*.html")
	return template.Must(
		template.New(path.Base(files[0])).
			Funcs(componentFuncs).
			ParseFS(embedFS, patterns...),
	)
}

// mustParseMobile builds a mobile-shell template set: mobile_base + page.
// No sidebar / no desktop chrome.
func (h *Handler) mustParseMobile(name, pageFile string) {
	h.templates[name] = parseTemplateSet(
		"templates/mobile/base.html",
		pageFile,
	)
}

// mustParseShared is like mustParse but pulls in an extra partial alongside
// the page template (used by the onboarding wizard for the progress bar).
func (h *Handler) mustParseShared(name, pageFile, sharedPartial string) {
	h.templates[name] = parseTemplateSet(
		"templates/base.html",
		"templates/partials/sidebar.html",
		sharedPartial,
		pageFile,
	)
}

// mustParse builds a per-page template set: base + sidebar + page file.
// Panics on parse error — caught at startup, not at request time.
func (h *Handler) mustParse(name, pageFile string) {
	h.templates[name] = parseTemplateSet(
		"templates/base.html",
		"templates/partials/sidebar.html",
		pageFile,
	)
}

// Mount registers all web UI routes on r.
//
// Route surface is split in two by T-B:
//
//   - Public routes (registered directly on r): static assets, the
//     home redirect, /join, /connect, /welcome, error pages. These
//     must work without a session.
//   - Protected routes (registered inside r.Group with
//     requireTenantMiddleware): every tenant-scoped page —
//     /dashboard, /transactions, /alerts, /reports, /admin, etc.
//     A request without a resolved tenant 302s to /connect.
//
// Both halves run through tenantSessionMiddleware first so the
// resolver fires once per request and tenantIDFromCtx returns the
// authenticated UUID downstream.
func (h *Handler) Mount(r chi.Router) {
	staticFS, _ := fs.Sub(embedFS, "static")

	// Resolve tenant from session cookie on every request (T-B / GRO-849).
	// This is a passthrough when MerchantResolver is nil (test wiring) or
	// when the request has no valid session cookie — public routes still
	// reach their handlers. The protected Group below adds the
	// redirect-on-nil gate.
	r.Use(tenantSessionMiddleware(h.deps.MerchantResolver))

	// ─── Public routes ─────────────────────────────────────────────────

	r.Handle("/web/static/*", http.StripPrefix("/web/static/",
		http.FileServer(http.FS(staticFS))))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})

	r.Get("/join", h.joinPage)
	r.Get("/login", h.loginPage)
	r.Get("/auth/logout", h.logoutHandler)
	// /auth/connect is the legacy CTA target from templates/auth/join.html
	// (line 128 "Connect your store"). Thin redirect to the provider
	// picker so the marketing surface stays clickable.
	r.Get("/auth/connect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	// Error pages (also reachable programmatically via Render403/404/500).
	// Public so a 403/404 redirect doesn't loop on logged-out clients.
	r.Get("/errors/403", h.errPage(403))
	r.Get("/errors/404", h.errPage(404))
	r.Get("/errors/500", h.errPage(500))

	// ─── Protected routes (require resolved tenant) ───────────────────

	r.Group(func(r chi.Router) {
		r.Use(h.requireTenantMiddleware)

		// /connect is the post-OAuth "data sync picker" (week start +
		// lookback days + Run Health Check). /welcome is the post-OAuth
		// "Your store is connected" landing. Both ARE post-auth pages —
		// pre-fix they were mounted in the public group, letting random
		// visitors see a "Connect Your Store" config UI without ever
		// logging in. They live in the protected group; squareauth.handleCallback
		// sets demo_merchant cookie BEFORE redirecting, so the post-OAuth
		// flow still reaches them with a resolved tenant.
		r.Get("/connect", h.page("connect", "connect", stubConnect))
		r.Get("/welcome", h.page("welcome", "welcome", nil))

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
		r.Get("/owl/dashboards", h.owlDashboardsPage)
		r.Get("/owl/parties", h.owlPartiesPage)
		r.Get("/owl/lp-performance", h.owlLPPerformancePage)
		r.Get("/rules", h.rulesListPage)

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
		// W1 dispatch: 
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

		// Transfers + inventory reports — wired W2b.
		r.Get("/transfers", h.transferListPage)
		r.Get("/transfers/{id}", h.transferDetailPage)
		r.Get("/transfers/{id}/variance", h.transferVariancePage)

		r.Get("/reports/distribution", h.reportDistributionPage)
		r.Get("/reports/inventory", h.reportInventoryPage)
		r.Get("/reports/category", h.reportCategoryPage)

		// Items + category report — wired W2c.
		// Item-setup Flow C C1 (manual entry, minimal form) — GRO-886.
		// Edit form (basic CRUD update path) — GRO-886 follow-on.
		r.Get("/items", h.itemListPage)
		r.Get("/items/new", h.itemNewPage)
		r.Post("/items/new", h.itemCreateAction)
		r.Get("/items/{id}", h.itemDetailPage)
		r.Get("/items/{id}/edit", h.itemEditPage)
		r.Post("/items/{id}/edit", h.itemUpdateAction)

		// Finance + payments — wired W2e.
		r.Get("/reports/finance", h.reportFinancePage)
		r.Get("/reports/payments", h.page("reports", "report_payments", func(_ *http.Request) any {
			// Tender mix requires a tender_mix aggregation method on transaction.Store
			// (per-transaction GetByID is too expensive). Filed as a follow-on.
			return map[string]any{"TotalTransactions": 0, "CashPct": "—", "CardPct": "—", "OtherPct": "—", "Tenders": nil, "SecurePayEnabled": false, "LastGatewaySync": "—"}
		}))
		r.Get("/reports/tax", h.reportTaxPage)
		r.Get("/reports/otb", h.reportOTBPage)
		r.Get("/orders/suggested", h.suggestedOrdersPage)
		r.Get("/reports/range", h.page("reports", "report_range", func(_ *http.Request) any {
			return map[string]any{"ActiveRanges": 0, "AvgSellThrough": "—", "AvgTurn": "—", "AvgGMROI": "—", "Ranges": nil}
		}))
		// Promotions calendar — wired W2f. Pricing reports remain stub
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

		// Receiving + RTV workflow — wired W2d, POST handlers W5.
		r.Get("/receiving", h.receivingListPage)
		r.Get("/receiving/{id}", h.receivingDetailPage)
		r.Get("/receiving/{id}/close", h.receivingClosePage)
		r.Post("/receiving/{id}/close", h.receivingCloseAction)
		r.Post("/receiving/{id}/lines/{lineID}/discrepancy", h.receivingLineDiscrepancyAction)
		r.Get("/returns", h.returnsListPage)
		r.Get("/returns/{id}", h.returnsDetailPage)

		// Operator workflow surfaces — directed-task queue + OTB action buttons.
		// W5.
		r.Get("/tasks", h.tasksListPage)
		r.Post("/tasks/{id}/claim", h.taskClaimAction)
		r.Post("/tasks/{id}/complete", h.taskCompleteAction)
		r.Post("/tasks/{id}/exception", h.taskExceptionAction)
		r.Post("/reports/otb/{budgetID}/lock", h.otbLockAction)
		r.Post("/orders/suggested/{id}/approve", h.suggestedOrderActionApprove)
		r.Post("/orders/suggested/{id}/reject", h.suggestedOrderActionReject)
		r.Post("/orders/suggested/{id}/send", h.suggestedOrderActionSend)

		// Asset registry + billing portal — wired W8 (read-only).
		r.Get("/assets", h.assetsListPage)
		r.Get("/assets/{id}", h.assetDetailPage)
		r.Get("/billing/overview", h.billingOverviewPage)
		r.Get("/billing/invoices", h.billingInvoicesPage)
		r.Get("/billing/payment-method", h.billingPaymentMethodPage)

		// Compliance + admin — wired W9.
		r.Get("/admin/audit", h.adminAuditPage)
		r.Get("/admin/iso27001", h.adminISO27001Page)
		r.Get("/admin/users", h.adminUsersPage)
		r.Get("/admin/config", h.adminConfigPage)

		// Multi-store intelligence — wired W10.
		r.Get("/admin/hierarchy", h.adminHierarchyPage)
		r.Post("/admin/hierarchy", h.adminHierarchyCreate)
		r.Get("/admin/network-integrity", h.adminNetworkIntegrityPage)
		r.Get("/dashboards/cross-store", h.dashboardsCrossStorePage)

		// Procurement — supplier + PO portal. W11.
		r.Get("/suppliers", h.suppliersListPage)
		r.Post("/suppliers", h.suppliersCreate)
		r.Get("/suppliers/{id}", h.supplierDetailPage)
		r.Get("/suppliers/{id}/scorecard", h.supplierScorecardPage)
		r.Get("/po", h.poListPage)
		r.Post("/po", h.poCreate)
		r.Get("/po/{id}", h.poDetailPage)
		r.Get("/po/{id}/match", h.poMatchPage)
		r.Post("/po/{id}/status", h.poStatusAction)

		// Onboarding wizard — W13.
		r.Get("/onboarding", h.onboardingIndexPage)
		r.Get("/onboarding/connect", h.onboardingConnectPage)
		r.Get("/onboarding/import", h.onboardingImportPage)
		r.Get("/onboarding/rules", h.onboardingRulesPage)
		r.Post("/onboarding/rules/enable", h.onboardingRulesEnableAction)
		r.Get("/onboarding/welcome", h.onboardingWelcomePage)

		// Mobile / Android POS UX — W14.
		r.Get("/m/tasks", h.mobileTasksPage)
		r.Get("/m/receiving", h.mobileReceivingPage)
		r.Get("/m/cycle-count", h.mobileCycleCountPage)
		r.Get("/m/alerts/{id}", h.mobileAlertDetailPage)

		// Ecom channel surface — W15.
		r.Get("/ecom/orders", h.ecomOrdersPage)
		r.Get("/ecom/sync", h.ecomSyncPage)

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

		// Cases analytics — wired W2e.
		r.Get("/reports/cases", h.reportCasesPage)

		// Workflow engine surfaces — wired W4 (unified list page;
		// per-workflow detail views are a follow-on dispatch).
		r.Get("/workflows", h.workflowsListPage)

		// MCP tool catalog — wired W12. Reads the in-process
		// registry; usage log + playground are follow-on dispatches.
		r.Get("/mcp/tools", h.mcpToolsPage)

		// Protocol portal — wired W7. Unified overview of Bitcoin L2
		// anchors, .jeffe namespace registrations, and L402 verification tokens.
		// Per-surface drilldowns (anchor proof viewer, charge dispute, evidence
		// chain per case) are follow-on dispatches.
		r.Get("/protocol", h.protocolOverviewPage)
	})

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
		Page:      activePage,
		Theme:     "canary-dark", // TODO: resolve from tenant config
		User:      stubUser(),
		Data:      data,
		CSRFField: csrf.TemplateField(r),
		CSRFToken: csrf.Token(r),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, "base.html", pd); err != nil {
		h.logger.Error("template execute", zap.String("name", tmplName), zap.Error(err))
	}
}

// joinPage serves the standalone public join page (no base template).
func (h *Handler) joinPage(w http.ResponseWriter, r *http.Request) {
	tmpl := parseTemplateSet("templates/auth/join.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

// loginPage serves the standalone public landing page for first-time
// users. Standalone template — does NOT extend base.html, so the
// merchant sidebar (full nav to /alerts, /chirps, /dashboard, etc.) is
// not rendered for unauthenticated users.
//
// Hosts the primary OAuth call-to-action (Connect Your Square) plus a
// placeholder for the NCR Counterpoint flow. After OAuth completes the
// user lands on /connect (the data-sync picker) with a resolved
// merchant.
//
// Bug context: pre-fix the gateway had no public login surface —
// unauthenticated requests for protected routes redirected to /connect,
// dumping users into the post-OAuth configuration page with no way to
// actually log in. The `?error=` query string surfaces failures from
// the Square OAuth callback.
func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	tmpl := parseTemplateSet("templates/auth/login.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"Error":            r.URL.Query().Get("error"),
		"SquareConfigured": squareConfigured(),
		"DemoLoginEnabled": demoLoginEnabled(),
	})
}

// demoLoginEnabled mirrors squareauth.Service.DevDemoLoginEnabled —
// the dev-only login bypass at /auth/demo. Read directly from env so
// the login template can render a "Demo Login" button without
// threading squareauth into web.Deps. Production never sets the var.
func demoLoginEnabled() bool {
	v := os.Getenv("DEV_DEMO_LOGIN")
	return v == "1" || v == "true" || v == "TRUE"
}

// squareConfigured mirrors the gate inside squareauth.handleAuthorize
// (handler.go line 71): all three of SQUARE_APPLICATION_ID, SECRET, and
// REDIRECT_URI must be present. When any is empty, /auth/square 503s,
// so the login template renders a "not configured" notice instead of
// a button that drives into a 503.
//
// We read env directly (rather than threading squareauth.Service through
// web.Deps) so this stays a 1-line gate with no construction-order
// coupling. If those env vars start being read elsewhere, refactor to a
// shared config helper.
func squareConfigured() bool {
	return os.Getenv("SQUARE_APPLICATION_ID") != "" &&
		os.Getenv("SQUARE_APPLICATION_SECRET") != "" &&
		os.Getenv("SQUARE_REDIRECT_URI") != ""
}

// logoutHandler clears the demo_merchant session cookie and redirects
// to /login. The sidebar in templates/partials/sidebar.html links here
// (`<a href="/auth/logout">`) — pre-fix this 404'd because no GET
// handler was mounted (squareauth only exposes POST /auth/square/disconnect).
//
// We mirror the cookie-clearing pattern from
// squareauth.handleDisconnect: empty value + MaxAge=-1 + matching
// Path/HttpOnly/Secure flags so the cookie is cleared cleanly.
func (h *Handler) logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "demo_merchant",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
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

// reportDistributionPage renders the distribution variance report — variance
// aggregated by transfer lane (source→destination). Wired W2b.
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
// Wired W2b.
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
// Wired W2c.
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
// margin. Wired W2c.
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
// Wired W2f. Uses uuid.Nil location which only matches promotions
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
// transaction set. Wired W2e. Uses an in-memory aggregate over the
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
// and L402 verification tokens. Wired W7.
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


// workflowsListPage renders all registered workflow definitions plus
// recent executions across the three engines (3-way match, L402 charge
// cycle, investigation lifecycle). Wired W4. Per-engine
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
// casemgmt store. Wired W2e.
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
// Wired W2c; SQL-aggregated in T-N.
func (h *Handler) reportCategoryPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		h.render(w, r, "report_category", "reports", map[string]any{
			"TotalCategories": 0, "TopCategory": "—", "AvgMargin": "—", "SKUsTracked": 0, "Categories": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	aggs, err := h.deps.ItemStore.AggregateByCategory(ctx, tenantID)
	if err != nil {
		h.logger.Error("reportCategoryPage: aggregate by category", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "reports", nil)
		return
	}

	rows := make([]map[string]any, 0, len(aggs))
	totalMarginSum := 0.0
	priceMarginCategories := 0
	totalSKUs := 0
	topName := "—"
	topCount := 0
	for _, a := range aggs {
		avgMargin := "—"
		if a.HasMargin {
			avgMargin = strconv.FormatFloat(a.AvgMarginPct, 'f', 1, 64) + "%"
			totalMarginSum += a.AvgMarginPct
			priceMarginCategories++
		}
		if a.SKUCount > topCount {
			topCount = a.SKUCount
			topName = a.Name
		}
		totalSKUs += a.SKUCount
		rows = append(rows, map[string]any{
			"Name":       a.Name,
			"SKUCount":   a.SKUCount,
			"TotalSales": "—", // wires when sales aggregation per category lands
			"AvgMargin":  avgMargin,
			"Turn":       "—",
		})
	}
	avgMarginAll := "—"
	if priceMarginCategories > 0 {
		avgMarginAll = strconv.FormatFloat(totalMarginSum/float64(priceMarginCategories), 'f', 1, 64) + "%"
	}

	h.render(w, r, "report_category", "reports", map[string]any{
		"TotalCategories": len(aggs),
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
// internal/employee.Store. Wired W2g.
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
// summaries from internal/employee.Store.AlertSummaries. Wired W2g.
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

func stubTransactions(_ *http.Request) any {
	return map[string]any{"Transactions": nil, "TotalCount": 0}
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

func stubConnect(_ *http.Request) any {
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	type lookbackOpt struct {
		Value string
		Label string
	}
	return map[string]any{
		"WeekDays":  days,
		"WeekStart": 0,
		"Lookback":  "30",
		"LookbackOpts": []lookbackOpt{
			{"7", "7 days"}, {"30", "30 days"}, {"90", "90 days"}, {"all", "All"},
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
					"ID":               c.ID.String(),
					"ShortID":          shortID,
					"Name":             name,
					"RiskTier":         "—",
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

// W16 case-management capstone handlers (exceptionDetailPage, casesNewPage,
// casesEvidencePage, classifyEvidenceDomain, casesCorrelationPage,
// casesRemediatePage) live in handler_w16.go per the per-W-series
// file convention. Route registrations stay in this file's Mount().
// Sprint 2 T-J.

// tenantIDFromCtx extracts the tenant UUID from the request context.
// Wired by tenantSessionMiddleware (T-B / GRO-849), which reads the
// signed session cookie via Deps.MerchantResolver and injects through
// tenant.InjectMerchantID. Returns uuid.Nil when no session is
// resolved — handlers that need an authenticated tenant should be
// wrapped in h.requireTenant() so the nil case redirects to /connect
// instead of rendering empty data.
func tenantIDFromCtx(ctx context.Context) uuid.UUID {
	id, _ := tenant.FromContext(ctx)
	return id
}
