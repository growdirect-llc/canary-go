package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (h *Handler) owlPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "owl", "owl", map[string]any{
		"Query":   r.URL.Query().Get("q"),
		"Results": nil,
	})
}

// owlDashboardsPage renders the multi-panel intelligence overview —
// LP rate by rule, top parties by value, KPI tiles. Tenant-scoped read
// over party.decisioning_facts and detection.detections / detection.cases
// via internal/owl.DashboardStore. Wired W6.
func (h *Handler) owlDashboardsPage(w http.ResponseWriter, r *http.Request) {
	from, to, label := owlPortalPeriod(r.URL.Query().Get("period"), time.Now())
	view := map[string]any{
		"Period":          label,
		"PeriodOptions":   []string{"day", "week", "month", "quarter"},
		"WindowFrom":      from.Format("2006-01-02 15:04"),
		"WindowTo":        to.Format("2006-01-02 15:04"),
		"TotalDetections": 0,
		"TotalCases":      0,
		"EscalationRate":  "0%",
		"PartyCount":      0,
		"LPRows":          nil,
		"TopParties":      nil,
	}

	if h.deps.OwlDashboard != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)

		lp, err := h.deps.OwlDashboard.LPRateRollup(ctx, tenantID, from, to)
		if err != nil {
			h.logger.Error("owlDashboardsPage: lp-rate", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(lp))
			var totalDet, totalCase int
			for _, m := range lp {
				rows = append(rows, map[string]any{
					"RuleType":      m.RuleType,
					"Detections":    m.DetectionCount,
					"Cases":         m.CaseCount,
					"EscalationPct": formatOwlPct(m.EscalationRate),
				})
				totalDet += m.DetectionCount
				totalCase += m.CaseCount
			}
			view["LPRows"] = rows
			view["TotalDetections"] = totalDet
			view["TotalCases"] = totalCase
			if totalDet > 0 {
				view["EscalationRate"] = formatOwlPct(float64(totalCase) / float64(totalDet))
			}
		}

		parties, err := h.deps.OwlDashboard.ListPartyRFM(ctx, tenantID, 10)
		if err != nil {
			h.logger.Error("owlDashboardsPage: parties", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(parties))
			for _, p := range parties {
				rows = append(rows, map[string]any{
					"PartyShort":     p.PartyID.String()[:8],
					"PartyValue":     p.PartyValue,
					"PartyFrequency": p.PartyFrequency,
					"PartyRecency":   p.PartyRecency,
					"Confidence":     p.Confidence,
				})
			}
			view["TopParties"] = rows
			view["PartyCount"] = len(parties)
		}
	}

	h.render(w, r, "owl_dashboards", "owl_intel", view)
}

// formatOwlPct renders a 0..1 ratio as a percentage with one decimal.
func formatOwlPct(r float64) string {
	return strconv.FormatFloat(r*100, 'f', 1, 64) + "%"
}

// owlPartiesPage renders the tenant's RFM party list, ordered by
// party_value DESC. Reads party.decisioning_facts via DashboardStore.
// Wired W6.
func (h *Handler) owlPartiesPage(w http.ResponseWriter, r *http.Request) {
	limit := parseOwlLimit(r.URL.Query().Get("limit"), 50)
	view := map[string]any{
		"Limit":        limit,
		"LimitOptions": []int{25, 50, 100, 250, 500},
		"PartyCount":   0,
		"Parties":      nil,
	}

	if h.deps.OwlDashboard != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		parties, err := h.deps.OwlDashboard.ListPartyRFM(ctx, tenantID, limit)
		if err != nil {
			h.logger.Error("owlPartiesPage: list", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(parties))
			for _, p := range parties {
				rows = append(rows, map[string]any{
					"PartyShort":     p.PartyID.String()[:8],
					"Confidence":     p.Confidence,
					"PartyValue":     p.PartyValue,
					"PartyFrequency": p.PartyFrequency,
					"PartyMonetary":  p.PartyMonetary,
					"PartyRecency":   p.PartyRecency,
					"PartyFraudRisk": p.PartyFraudRisk,
					"PartyChurnRisk": p.PartyChurnRisk,
				})
			}
			view["Parties"] = rows
			view["PartyCount"] = len(parties)
		}
	}

	h.render(w, r, "owl_parties", "owl_intel", view)
}

// parseOwlLimit clamps the limit query param to [1, 500] with a fallback.
// Shared with W8 / W11 list views.
func parseOwlLimit(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > 500 {
		return 500
	}
	return n
}

// owlLPPerformancePage renders LP-rate detail per rule type. Same
// underlying query as the dashboards page (LPRateRollup) but full-table
// view with drill-down to the alert list filtered by rule_type.
// Wired W6.
func (h *Handler) owlLPPerformancePage(w http.ResponseWriter, r *http.Request) {
	from, to, label := owlPortalPeriod(r.URL.Query().Get("period"), time.Now())
	view := map[string]any{
		"Period":            label,
		"PeriodOptions":     []string{"day", "week", "month", "quarter"},
		"WindowFrom":        from.Format("2006-01-02 15:04"),
		"WindowTo":          to.Format("2006-01-02 15:04"),
		"TotalDetections":   0,
		"TotalCases":        0,
		"OverallEscalation": "0%",
		"Rows":              nil,
	}

	if h.deps.OwlDashboard != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		lp, err := h.deps.OwlDashboard.LPRateRollup(ctx, tenantID, from, to)
		if err != nil {
			h.logger.Error("owlLPPerformancePage: rollup", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(lp))
			var totalDet, totalCase int
			for _, m := range lp {
				rows = append(rows, map[string]any{
					"RuleType":      m.RuleType,
					"Detections":    m.DetectionCount,
					"Cases":         m.CaseCount,
					"EscalationPct": formatOwlPct(m.EscalationRate),
				})
				totalDet += m.DetectionCount
				totalCase += m.CaseCount
			}
			view["Rows"] = rows
			view["TotalDetections"] = totalDet
			view["TotalCases"] = totalCase
			if totalDet > 0 {
				view["OverallEscalation"] = formatOwlPct(float64(totalCase) / float64(totalDet))
			}
		}
	}

	h.render(w, r, "owl_lp_performance", "owl_intel", view)
}

// owlPortalPeriod computes the (from, to, label) window for a portal page
// based on a "period" query param. UTC throughout for stable URLs across
// user devices. Used by all owl operator surfaces.
func owlPortalPeriod(kind string, now time.Time) (from, to time.Time, label string) {
	to = now.UTC()
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "day":
		from = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		return from, to, "day"
	case "month":
		from = time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
		return from, to, "month"
	case "quarter":
		qStartMonth := ((int(to.Month())-1)/3)*3 + 1
		from = time.Date(to.Year(), time.Month(qStartMonth), 1, 0, 0, 0, 0, time.UTC)
		return from, to, "quarter"
	default: // "week" or anything unrecognized
		offset := (int(to.Weekday()) + 6) % 7 // Monday = 0
		from = time.Date(to.Year(), to.Month(), to.Day()-offset, 0, 0, 0, 0, time.UTC)
		return from, to, "week"
	}
}
