// internal/web/handler_w10.go
//
// W10 / GRO-829 — Multi-store intelligence portal.
//
//   /admin/hierarchy       — list + create chains/regions/formats
//   /admin/network-integrity — store connectivity placeholder
//   /dashboards/cross-store — comparative location list (per-location
//                             roll-up is follow-on; needs new primitive)
//
// Store switcher in the main nav reads app.locations via the same
// hierarchy.Store. merchantIDFromCtx() pulled from the request scope —
// portal handlers in this codebase use tenantIDFromCtx today, but the
// hierarchy + locations tables are merchant-scoped (FK to app.merchants).
// We resolve through tenant→merchant via the existing flow when the
// identity middleware lands; for now we use a placeholder zero UUID
// so empty-state renders cleanly.

package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/hierarchy"
)

// merchantIDFromCtx returns the request-scoped merchant_id when
// available; otherwise the zero UUID, which causes the hierarchy
// queries to return empty (no rows match). This mirrors the W7 /
// W6 / W8 nil-safe shape — empty-state copy renders without 500ing.
//
// Real merchant resolution lands when the identity middleware (GRO-769)
// is wired in front of /admin and /dashboards routes.
func merchantIDFromCtx(_ http.ResponseWriter, r *http.Request) uuid.UUID {
	v := r.URL.Query().Get("merchant_id")
	if v == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// ──────────────────────────────────────────────────────────────────────
// /admin/hierarchy — list + create
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) adminHierarchyPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Flash": r.URL.Query().Get("flash"),
		"Nodes": nil,
		"Count": 0,
	}
	if h.deps.HierarchyStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		nodes, err := h.deps.HierarchyStore.ListNodes(r.Context(), merchantID)
		if err != nil {
			h.logger.Error("adminHierarchyPage", zap.Error(err))
		} else {
			view["Nodes"] = hierarchyNodesView(nodes)
			view["Count"] = len(nodes)
		}
	}
	h.render(w, r, "admin_hierarchy", "admin", view)
}

func hierarchyNodesView(ns []hierarchy.Node) []map[string]any {
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		parent := "—"
		if n.ParentID != nil {
			parent = n.ParentID.String()[:8]
		}
		out = append(out, map[string]any{
			"ID":          n.ID.String(),
			"ShortID":     n.ID.String()[:8],
			"Name":        n.Name,
			"Level":       n.Level,
			"ParentShort": parent,
			"CreatedAt":   n.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

func (h *Handler) adminHierarchyCreate(w http.ResponseWriter, r *http.Request) {
	if h.deps.HierarchyStore == nil {
		http.Redirect(w, r, "/admin/hierarchy?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	name := r.PostFormValue("name")
	level, _ := strconv.Atoi(r.PostFormValue("level"))
	if name == "" || level <= 0 {
		http.Redirect(w, r, "/admin/hierarchy?flash=invalid_input", http.StatusSeeOther)
		return
	}
	var parentID *uuid.UUID
	if v := r.PostFormValue("parent_id"); v != "" {
		if pid, err := uuid.Parse(v); err == nil {
			parentID = &pid
		}
	}
	merchantID := merchantIDFromCtx(w, r)
	if _, err := h.deps.HierarchyStore.CreateNode(r.Context(), merchantID, name, level, parentID); err != nil {
		h.logger.Error("adminHierarchyCreate", zap.Error(err))
		http.Redirect(w, r, "/admin/hierarchy?flash=create_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/hierarchy?flash=created", http.StatusSeeOther)
}

// ──────────────────────────────────────────────────────────────────────
// /admin/network-integrity — connectivity / sync status placeholder
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) adminNetworkIntegrityPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Locations": nil,
		"Count":     0,
		"Note":      "Per-location connectivity, sync lag, and hardware drift indicators populate when the network-integrity sensor table lands. Today's view shows the location inventory.",
	}
	if h.deps.HierarchyStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		locs, err := h.deps.HierarchyStore.ListLocations(r.Context(), merchantID)
		if err != nil {
			h.logger.Error("adminNetworkIntegrityPage", zap.Error(err))
		} else {
			view["Locations"] = locationsView(locs)
			view["Count"] = len(locs)
		}
	}
	h.render(w, r, "admin_network_integrity", "admin", view)
}

func locationsView(ls []hierarchy.Location) []map[string]any {
	out := make([]map[string]any, 0, len(ls))
	for _, l := range ls {
		city := "—"
		if l.City != nil {
			city = *l.City
		}
		state := "—"
		if l.State != nil {
			state = *l.State
		}
		status := "active"
		if !l.IsActive {
			status = "inactive"
		}
		out = append(out, map[string]any{
			"ID":           l.ID.String(),
			"ShortID":      l.ID.String()[:8],
			"LocationName": l.LocationName,
			"City":         city,
			"State":        state,
			"Status":       status,
			"CreatedAt":    l.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

// ──────────────────────────────────────────────────────────────────────
// /dashboards/cross-store — comparative location list
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) dashboardsCrossStorePage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Locations": nil,
		"Count":     0,
		"Note":      "Per-location LP-rate, sell-through, and shrink roll-ups need a new aggregation primitive. Out-of-band: see W6 /owl/dashboards for the rule-type roll-up that exists today.",
	}
	if h.deps.HierarchyStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		locs, err := h.deps.HierarchyStore.ListLocations(r.Context(), merchantID)
		if err != nil {
			h.logger.Error("dashboardsCrossStorePage", zap.Error(err))
		} else {
			view["Locations"] = locationsView(locs)
			view["Count"] = len(locs)
		}
	}
	h.render(w, r, "dashboards_cross_store", "dashboards", view)
}
