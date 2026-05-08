package web

// W16 — Cross-domain case management capstone.
//
// Five operator-facing surfaces that bridge the alert/exception layer
// into casemgmt and the cross-domain evidence aggregation:
//
//   exceptionDetailPage      /exceptions/{id}             — exception drill-down
//   casesNewPage             /cases/new?exception=…        — new case form, optionally pre-filled
//   casesEvidencePage        /cases/{id}/evidence          — evidence list + per-domain counts
//   casesCorrelationPage     /cases/{id}/correlation       — subject-based pattern surface
//   casesRemediatePage       /cases/{id}/remediate         — remediation catalog (workflow KickOff target)
//
// Pre-W16 these handlers lived inline in handler.go (~236 LOC). Moved
// here in Sprint 2 T-J to match the per-W-series file
// convention (handler_w5.go, handler_w8.go, handler_w9.go, etc.).
// No behavior change — pure relocation. Route registrations remain
// in handler.go's Mount().

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/casemgmt"
)

// exceptionDetailPage — exception drill-down with module-aware context.
// "Exception" today maps to an alert row — the alert system is the
// cross-domain exception substrate. When the operations-hub table lands,
// the lookup widens beyond alerts.
func (h *Handler) exceptionDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	view := map[string]any{
		"Exception": map[string]any{
			"ID":             idStr,
			"ShortID":        shortID,
			"Domain":         "—",
			"Type":           "—",
			"Severity":       "—",
			"Status":         "—",
			"Store":          "—",
			"DetectedAt":     "—",
			"AssignedTo":     "—",
			"TriggerRule":    "—",
			"TriggerProcess": "—",
			"SignalSummary":  "—",
		},
	}
	if id, err := uuid.Parse(idStr); err == nil && h.deps.AlertStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if a, err := h.deps.AlertStore.GetByID(ctx, tenantID, id); err == nil && a != nil {
			loc := "—"
			if a.LocationID != nil {
				loc = a.LocationID.String()[:8]
			}
			view["Exception"] = map[string]any{
				"ID":             idStr,
				"ShortID":        shortID,
				"Domain":         a.SourceEntityType,
				"Type":           a.RuleCategory,
				"Severity":       a.Severity,
				"Status":         a.Status,
				"Store":          loc,
				"DetectedAt":     a.DetectedAt.Format("2006-01-02 15:04"),
				"AssignedTo":     "—",
				"TriggerRule":    a.RuleCode,
				"TriggerProcess": a.SourceEntityType,
				"SignalSummary":  a.RuleCategory + " · severity=" + a.Severity,
			}
		}
	}
	h.render(w, r, "exceptions_detail", "exceptions", view)
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

// casesEvidencePage — cross-domain evidence aggregation (E.5.3).
// Reads casemgmt.Case + ListEvidence; domain counts are derived from
// evidence.SourceEntityType (alert / detection / inventory_movement /
// goods_receipt / etc.).
func (h *Handler) casesEvidencePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	view := map[string]any{
		"Case": map[string]any{
			"ID":      idStr,
			"ShortID": shortID,
			"Title":   "Case " + shortID,
		},
		"Evidence":     nil,
		"DomainCounts": map[string]int{"lp": 0, "inventory": 0, "finance": 0, "receiving": 0},
	}
	if id, err := uuid.Parse(idStr); err == nil && h.deps.CaseStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if c, err := h.deps.CaseStore.GetCase(ctx, tenantID, id); err == nil {
			view["Case"] = map[string]any{
				"ID":      c.ID.String(),
				"ShortID": c.ID.String()[:8],
				"Title":   c.Title,
			}
		}
		if ev, err := h.deps.CaseStore.ListEvidence(ctx, id); err == nil {
			counts := map[string]int{"lp": 0, "inventory": 0, "finance": 0, "receiving": 0}
			rows := make([]map[string]any, 0, len(ev))
			for _, e := range ev {
				domain := classifyEvidenceDomain(e.SourceEntityType)
				counts[domain]++
				et := "—"
				if e.SourceEntityType != nil {
					et = *e.SourceEntityType
				}
				rows = append(rows, map[string]any{
					"EvidenceType":     e.EvidenceType,
					"SourceEntityType": et,
					"Domain":           domain,
					"CollectedAt":      e.CollectedAt.Format("2006-01-02 15:04"),
				})
			}
			view["Evidence"] = rows
			view["DomainCounts"] = counts
		}
	}
	h.render(w, r, "cases_evidence", "cases", view)
}

// classifyEvidenceDomain maps a CaseEvidence.SourceEntityType to the
// cross-domain bucket the evidence template renders. Deterministic
// (no ML correlation per dispatch out-of-scope).
func classifyEvidenceDomain(srcType *string) string {
	if srcType == nil {
		return "lp"
	}
	switch *srcType {
	case "alert", "detection", "chirp":
		return "lp"
	case "inventory_movement", "inventory_position", "inventory_document":
		return "inventory"
	case "transaction", "tender", "refund":
		return "finance"
	case "goods_receipt", "transfer", "rtv":
		return "receiving"
	default:
		return "lp"
	}
}

// casesCorrelationPage — subject-based pattern surface across modules
// (E.5.4). Deterministic per dispatch out-of-scope: finds other cases
// with the same primary_subject_id. ML correlation is filed for follow-on.
func (h *Handler) casesCorrelationPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	view := map[string]any{
		"Case": map[string]any{
			"ID":          idStr,
			"ShortID":     shortID,
			"SubjectID":   "—",
		},
		"RelatedCases": nil,
		"Timeline":     nil,
	}
	if id, err := uuid.Parse(idStr); err == nil && h.deps.CaseStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		c, err := h.deps.CaseStore.GetCase(ctx, tenantID, id)
		if err == nil && c != nil {
			subj := "—"
			if c.PrimarySubjectID != nil {
				subj = c.PrimarySubjectID.String()[:8]
			}
			view["Case"] = map[string]any{
				"ID":        c.ID.String(),
				"ShortID":   c.ID.String()[:8],
				"SubjectID": subj,
			}
			// Find other cases with the same primary subject.
			if c.PrimarySubjectID != nil {
				all, err := h.deps.CaseStore.ListCases(ctx, casemgmt.ListFilters{TenantID: tenantID, Limit: 100})
				if err == nil {
					rows := make([]map[string]any, 0)
					for _, related := range all {
						if related.ID == c.ID {
							continue
						}
						if related.PrimarySubjectID != nil && *related.PrimarySubjectID == *c.PrimarySubjectID {
							rows = append(rows, map[string]any{
								"ID":         related.ID.String(),
								"ShortID":    related.ID.String()[:8],
								"Title":      related.Title,
								"Severity":   related.Severity,
								"Status":     related.Status,
								"OpenedAt":   related.OpenedAt.Format("2006-01-02"),
							})
						}
					}
					view["RelatedCases"] = rows
				}
			}
		}
	}
	h.render(w, r, "cases_correlation", "cases", view)
}

// casesRemediatePage — dispatch remediation to target module workflow
// (E.5.5). Surfaces a static catalog of remediation actions; each
// links to the workflow KickOff path that lands when W4 ships the
// operator-facing workflow advance UI.
func (h *Handler) casesRemediatePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	view := map[string]any{
		"Case": map[string]any{
			"ID":      idStr,
			"ShortID": shortID,
			"Title":   "Case " + shortID,
		},
		"Catalog": []map[string]any{
			{"Code": "open_three_way_match", "Name": "Open three-way match", "Module": "workflow", "Note": "Triggers workflow.KickOff(three_way_match) — W4 wired this from receiving close."},
			{"Code": "create_directed_task", "Name": "Create directed task", "Module": "task", "Note": "Adds a receiving / replenishment / cycle_count task to the queue."},
			{"Code": "lock_otb_period", "Name": "Lock OTB period", "Module": "billing", "Note": "Locks the active L402 OTB budget — blocks further metered spend."},
			{"Code": "flag_inventory_loss", "Name": "Flag inventory loss", "Module": "asset", "Note": "Writes an adjustment movement; SOH consumer reconciles."},
		},
		"Remediations": nil,
	}
	if id, err := uuid.Parse(idStr); err == nil && h.deps.CaseStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if c, err := h.deps.CaseStore.GetCase(ctx, tenantID, id); err == nil && c != nil {
			view["Case"] = map[string]any{
				"ID":      c.ID.String(),
				"ShortID": c.ID.String()[:8],
				"Title":   c.Title,
			}
		}
	}
	h.render(w, r, "cases_remediate", "cases", view)
}
