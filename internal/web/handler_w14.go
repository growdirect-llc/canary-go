// internal/web/handler_w14.go
//
// W14 / GRO-833 — Mobile / Android POS UX.
//
// Responsive web cuts of operator surfaces (no native app).
//   /m/tasks         → mobile directed-task list
//   /m/receiving     → mobile receiving line entry
//   /m/cycle-count   → mobile cycle-count workflow
//   /m/alerts/{id}   → mobile alert acknowledge
//
// All four use a standalone mobile shell (no sidebar, max-width 480px,
// large hit targets). They reuse existing W5 store deps: TaskStore,
// InventoryStore, AlertStore.

package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/alert"
	"github.com/ruptiv/canary/internal/inventory"
	"github.com/ruptiv/canary/internal/task"
)

func (h *Handler) mobileTasksPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Tasks": nil,
		"Count": 0,
		"Flash": r.URL.Query().Get("flash"),
	}
	if h.deps.TaskStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		tasks, err := h.deps.TaskStore.ListByTenant(ctx, tenantID, "open", 50)
		if err != nil {
			h.logger.Error("mobileTasksPage", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(tasks))
			for _, t := range tasks {
				rows = append(rows, mobileTaskRowView(t))
			}
			view["Tasks"] = rows
			view["Count"] = len(tasks)
		}
	}
	h.renderMobile(w, r, "m_tasks", view)
}

func mobileTaskRowView(t task.TaskDTO) map[string]any {
	qty := "—"
	if t.Quantity != nil {
		qty = *t.Quantity
	}
	return map[string]any{
		"ID":       t.ID.String(),
		"ShortID":  t.ID.String()[:8],
		"Type":     t.TaskType,
		"Priority": t.Priority,
		"Status":   t.Status,
		"Quantity": qty,
	}
}

func (h *Handler) mobileReceivingPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Sessions": nil,
		"Count":    0,
	}
	if h.deps.InventoryStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
			TenantID: tenantID,
			Types:    []string{inventory.DocumentTypeGoodsReceipt},
			Limit:    25,
		})
		if err != nil {
			h.logger.Error("mobileReceivingPage", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(docs))
			for _, d := range docs {
				rows = append(rows, map[string]any{
					"ID":             d.ID.String(),
					"ShortID":        d.ID.String()[:8],
					"DocumentNumber": d.DocumentNumber,
					"Status":         d.Status,
				})
			}
			view["Sessions"] = rows
			view["Count"] = len(rows)
		}
	}
	h.renderMobile(w, r, "m_receiving", view)
}

func (h *Handler) mobileCycleCountPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Tasks": nil,
		"Count": 0,
	}
	if h.deps.TaskStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		// Filter to cycle_count type only; ListByTenant doesn't support
		// a type filter today (would need a small extension). Filter in-memory.
		all, err := h.deps.TaskStore.ListByTenant(ctx, tenantID, "open", 50)
		if err != nil {
			h.logger.Error("mobileCycleCountPage", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0)
			for _, t := range all {
				if t.TaskType != task.TypeCycleCount {
					continue
				}
				rows = append(rows, mobileTaskRowView(t))
			}
			view["Tasks"] = rows
			view["Count"] = len(rows)
		}
	}
	h.renderMobile(w, r, "m_cycle_count", view)
}

func (h *Handler) mobileAlertDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.renderMobile(w, r, "m_alerts", map[string]any{"NotFound": true})
		return
	}
	view := map[string]any{
		"Alert": map[string]any{"ID": idStr, "ShortID": idStr[:8], "Title": "—", "Severity": "—", "RuleType": "—", "Status": "—"},
		"Flash": r.URL.Query().Get("flash"),
	}
	if h.deps.AlertStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		a, err := h.deps.AlertStore.GetByID(ctx, tenantID, id)
		if err == nil && a != nil {
			view["Alert"] = mobileAlertView(*a)
		}
	}
	h.renderMobile(w, r, "m_alert_detail", view)
}

func mobileAlertView(a alert.AlertDTO) map[string]any {
	title := a.RuleCode
	if title == "" {
		title = "Alert " + a.ID.String()[:8]
	}
	return map[string]any{
		"ID":       a.ID.String(),
		"ShortID":  a.ID.String()[:8],
		"Title":    title,
		"Severity": a.Severity,
		"RuleType": a.RuleCategory,
		"Status":   a.Status,
	}
}

// renderMobile writes the mobile shell (no sidebar) with the requested
// page template. Mirrors `render` but skips the desktop chrome.
func (h *Handler) renderMobile(w http.ResponseWriter, r *http.Request, tmplName string, data any) {
	tmpl, ok := h.templates[tmplName]
	if !ok {
		h.logger.Error("mobile template not found", zap.String("name", tmplName))
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	pd := PageData{
		Page:  "mobile",
		Theme: "canary-dark",
		User:  stubUser(),
		Data:  data,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, "mobile_base.html", pd); err != nil {
		h.logger.Error("mobile template execute", zap.String("name", tmplName), zap.Error(err))
	}
}
