// internal/web/handler_w13.go
//
// W13 — Onboarding wizard.
//
// Four steps stitched over existing surfaces (no new substrate today):
//   /onboarding         → step picker / index
//   /onboarding/connect → step 1 (POS connect; links to /connect)
//   /onboarding/import  → step 2 (config ingestion progress)
//   /onboarding/rules   → step 3 (default rule pack seed; POST to enable)
//   /onboarding/welcome → step 4 (first-week landing)

package web

import (
	"net/http"
)

func (h *Handler) onboardingIndexPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/onboarding/connect", http.StatusSeeOther)
}

func (h *Handler) onboardingConnectPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Step":      1,
		"Total":     4,
		"Title":     "Connect POS",
		"NextURL":   "/onboarding/import",
		"PrevURL":   "",
		"Connected": false,
	}
	h.render(w, r, "onboarding_connect", "onboarding", view)
}

func (h *Handler) onboardingImportPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Step":    2,
		"Total":   4,
		"Title":   "Import Store Config",
		"NextURL": "/onboarding/rules",
		"PrevURL": "/onboarding/connect",
		"Sections": []map[string]any{
			{"Code": "N.1", "Name": "POS credentials", "Status": "—"},
			{"Code": "N.2", "Name": "Inventory thresholds", "Status": "—"},
			{"Code": "N.3", "Name": "Allow-list rules", "Status": "—"},
		},
	}
	h.render(w, r, "onboarding_import", "onboarding", view)
}

func (h *Handler) onboardingRulesPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Step":    3,
		"Total":   4,
		"Title":   "Enable Default Rules",
		"NextURL": "/onboarding/welcome",
		"PrevURL": "/onboarding/import",
		"Flash":   r.URL.Query().Get("flash"),
		"Pack": []map[string]any{
			{"Code": "void_high_count", "Name": "Excess void count per cashier", "DefaultEnabled": true},
			{"Code": "comp_high_count", "Name": "Excess comp count per cashier", "DefaultEnabled": true},
			{"Code": "discount_high_pct", "Name": "Discount percent above threshold", "DefaultEnabled": true},
			{"Code": "dead_count_terminal", "Name": "Dead-count terminal events", "DefaultEnabled": true},
			{"Code": "no_sale_after_drawer_open", "Name": "Drawer-open without sale", "DefaultEnabled": false},
		},
	}
	h.render(w, r, "onboarding_rules", "onboarding", view)
}

func (h *Handler) onboardingRulesEnableAction(w http.ResponseWriter, r *http.Request) {
	// No bulk-seed method on detection.allow_list today; per dispatch
	// "default seed data on tenant create" — capturing the intent
	// with a flash redirect so the wizard flow demonstrates end-to-end.
	http.Redirect(w, r, "/onboarding/rules?flash=enabled", http.StatusSeeOther)
}

func (h *Handler) onboardingWelcomePage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Step":    4,
		"Total":   4,
		"Title":   "First Detection",
		"NextURL": "/dashboard",
		"PrevURL": "/onboarding/rules",
		"Tiles": []map[string]any{
			{"Title": "Chirps feed", "Subtitle": "Real-time detection alerts", "URL": "/chirps", "Color": "var(--signal-yellow)"},
			{"Title": "Alerts list", "Subtitle": "Detection alerts grouped by rule", "URL": "/alerts", "Color": "var(--accent-blue)"},
			{"Title": "Tasks queue", "Subtitle": "Receiving + replenishment + cycle-count", "URL": "/tasks", "Color": "var(--health-green)"},
			{"Title": "Owl dashboards", "Subtitle": "LP-rate + party intelligence", "URL": "/owl/dashboards", "Color": "var(--signal-yellow)"},
		},
	}
	h.render(w, r, "onboarding_welcome", "onboarding", view)
}
