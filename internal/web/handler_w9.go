// internal/web/handler_w9.go
//
// W9 / GRO-828 — Compliance + Admin portal.
//
// Five surfaces:
//   /admin/audit       — app.audit_log viewer (real read; filterable).
//   /admin/iso27001    — controls inventory (placeholder; no schema today).
//   /admin/users       — users list (placeholder; depends on GRO-769/770).
//   /admin/config      — N.1-N.3 ingestion completeness (placeholder).
//   /reports/tax       — multi-authority tax (rewires existing stub).

package web

import (
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/protocol/audit"
)

// ──────────────────────────────────────────────────────────────────────
// /admin/audit — real audit log viewer
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) adminAuditPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source := q.Get("source")
	action := q.Get("action")
	limit := parseOwlLimit(q.Get("limit"), 100)

	view := map[string]any{
		"Source":       source,
		"Action":       action,
		"Limit":        limit,
		"LimitOptions": []int{50, 100, 250, 500},
		"Rows":         nil,
		"Count":        0,
	}

	if h.deps.AuditReader != nil {
		ctx := r.Context()
		rows, err := h.deps.AuditReader.ListByMerchant(ctx, audit.ListFilters{
			SourceCode: source,
			Action:     action,
			Limit:      limit,
		})
		if err != nil {
			h.logger.Error("adminAuditPage", zap.Error(err))
		} else {
			view["Rows"] = auditRowsView(rows)
			view["Count"] = len(rows)
		}
	}

	h.render(w, r, "admin_audit", "admin", view)
}

func auditRowsView(rows []audit.LogRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, lr := range rows {
		merchant := "—"
		if lr.MerchantID != nil {
			merchant = lr.MerchantID.String()[:8]
		}
		source := "—"
		if lr.SourceCode != nil && *lr.SourceCode != "" {
			source = *lr.SourceCode
		}
		actor := "—"
		if lr.ActorType != nil && *lr.ActorType != "" {
			actor = *lr.ActorType
		}
		status := "—"
		if lr.StatusCode != nil {
			status = strconv.Itoa(*lr.StatusCode)
		}
		latency := "—"
		if lr.LatencyMS != nil {
			latency = strconv.Itoa(*lr.LatencyMS) + "ms"
		}
		digest := "—"
		if lr.PayloadDigest != nil && len(*lr.PayloadDigest) >= 12 {
			digest = (*lr.PayloadDigest)[:12] + "…"
		}
		out = append(out, map[string]any{
			"CreatedAt":     lr.CreatedAt.Format("2006-01-02 15:04:05"),
			"MerchantShort": merchant,
			"Action":        lr.Action,
			"Resource":      lr.Resource,
			"SourceCode":    source,
			"ActorType":     actor,
			"StatusCode":    status,
			"LatencyMS":     latency,
			"PayloadDigest": digest,
		})
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────
// /admin/iso27001 — placeholder controls inventory
// ──────────────────────────────────────────────────────────────────────

// iso27001Controls is the static reference inventory until evidence
// collection lands (separate dispatch). Categories taken from ISO/IEC
// 27001:2022 Annex A.
var iso27001Controls = []map[string]any{
	{"Code": "A.5", "Name": "Organizational controls", "Status": "in_progress", "Evidence": "Brain wiki + capability cards"},
	{"Code": "A.6", "Name": "People controls", "Status": "not_started", "Evidence": "—"},
	{"Code": "A.7", "Name": "Physical controls", "Status": "not_started", "Evidence": "—"},
	{"Code": "A.8", "Name": "Technological controls", "Status": "in_progress", "Evidence": "Audit log + Bitcoin L2 anchor + L402 rail"},
}

func (h *Handler) adminISO27001Page(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin_iso27001", "admin", map[string]any{
		"Controls": iso27001Controls,
	})
}

// ──────────────────────────────────────────────────────────────────────
// /admin/users — placeholder pending GRO-769/770
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) adminUsersPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "admin_users", "admin", map[string]any{
		"Users":         nil,
		"BlockedReason": "user list, role assignment, and deactivation depend on the identity middleware (GRO-769) and admin module (GRO-770) which are still in progress",
	})
}

// ──────────────────────────────────────────────────────────────────────
// /admin/config — N.1-N.3 ingestion completeness placeholder
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) adminConfigPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Sections": []map[string]any{
			{"Code": "N.1", "Name": "POS credentials", "Status": "—", "Note": "Stored in app.pos_tenant_credentials"},
			{"Code": "N.2", "Name": "Inventory thresholds", "Status": "—", "Note": "Min/Max in app.inventory_thresholds"},
			{"Code": "N.3", "Name": "Allow-list / detection rules", "Status": "—", "Note": "Configured per location via /settings/allowlist/*"},
		},
		"Note": "Config-health roll-up requires per-tenant aggregate queries that this dispatch does not introduce. Operator should cross-reference settings pages today.",
	}
	h.render(w, r, "admin_config", "admin", view)
}

// ──────────────────────────────────────────────────────────────────────
// /reports/tax — rewires existing stub with empty-state placeholder
// ──────────────────────────────────────────────────────────────────────
//
// Tax aggregation needs a tax-amount column on transactions or a
// dedicated tax_collections table. Neither is in the active migrations
// today. /reports/tax renders an authoritative empty-state until the
// schema lands.

func (h *Handler) reportTaxPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "report_tax", "reports", map[string]any{
		"TotalTax":       "—",
		"AuthorityCount": 0,
		"NexusStates":    0,
		"FilingPeriod":   "—",
		"Authorities":    nil,
		"BlockedReason":  "tax aggregation requires a tax_amount or tax_collections schema that is not yet migrated; this view will populate once that lands",
	})
}
