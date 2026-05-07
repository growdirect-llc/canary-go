// internal/web/handler_w5.go
//
// W5 / GRO-824 — operator workflow surfaces over engines that already
// run headless: directed-task queue (internal/task), inventory document
// close + line discrepancy (internal/inventory), L402 OTB budget lock
// (internal/billing), three-way-match workflow trigger (internal/workflow).
//
// Suggested-orders POSTs (approve / reject / send) are placeholders —
// W11 owns the supplier + PO model. These handlers redirect with a
// flash so the operator surface feels responsive without persisting.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/billing"
	"github.com/growdirect-llc/rapidpos/internal/inventory"
	"github.com/growdirect-llc/rapidpos/internal/task"
	"github.com/growdirect-llc/rapidpos/internal/workflow"
)

// ──────────────────────────────────────────────────────────────────────
// Receiving — close + line discrepancy
// ──────────────────────────────────────────────────────────────────────

// receivingCloseAction closes a goods_receipt document (status →
// 'closed') and kicks off a three-way-match workflow execution.
// Idempotent on close — UpdateDocumentStatus stamps completed_at only
// once; KickOff is best-effort and logs (but does not fail the request)
// if the workflow definition has not been registered yet.
func (h *Handler) receivingCloseAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if h.deps.InventoryStore == nil {
		http.Redirect(w, r, "/receiving/"+idStr+"?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	doc, err := h.deps.InventoryStore.UpdateDocumentStatus(ctx, tenantID, id, inventory.DocumentStatusClosed, nil)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingCloseAction: update status", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}

	if h.deps.WorkflowStore != nil {
		if err := kickoffThreeWayMatch(ctx, h.deps.WorkflowStore, doc); err != nil {
			// Don't block the close if the workflow isn't registered
			// yet — log and let the operator continue.
			h.logger.Warn("receivingCloseAction: kickoff three-way-match",
				zap.String("doc_id", id.String()),
				zap.Error(err),
			)
		}
	}

	http.Redirect(w, r, "/receiving/"+idStr+"?flash=closed", http.StatusSeeOther)
}

// kickoffThreeWayMatch resolves the workflow definition and kicks off
// an execution row keyed to the goods_receipt document. Returns the
// underlying error so the caller can decide whether to fail or warn.
func kickoffThreeWayMatch(ctx context.Context, store *workflow.Store, doc *inventory.DocumentDTO) error {
	def, err := store.GetDefinitionByCode(ctx, workflow.ThreeWayMatchCode, workflow.ThreeWayMatchVersion)
	if err != nil {
		return err
	}
	ext := doc.DocumentNumber
	ctxJSON, _ := json.Marshal(map[string]any{
		"goods_receipt_id":     doc.ID.String(),
		"goods_receipt_number": doc.DocumentNumber,
		"tenant_id":            doc.TenantID.String(),
	})
	_, err = store.KickOff(ctx, doc.TenantID, def.ID, &ext, ctxJSON)
	return err
}

// receivingLineDiscrepancyAction marks an inventory_document_line with
// a variance_reason. Form fields: reason (required string).
func (h *Handler) receivingLineDiscrepancyAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	lineIDStr := chi.URLParam(r, "lineID")
	lineID, err := uuid.Parse(lineIDStr)
	if err != nil {
		http.Error(w, "invalid lineID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	reason := r.PostFormValue("reason")
	if reason == "" {
		reason = "operator_marked"
	}
	if h.deps.InventoryStore == nil {
		http.Redirect(w, r, "/receiving/"+idStr+"?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	if err := h.deps.InventoryStore.MarkLineDiscrepancy(ctx, tenantID, lineID, reason); err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingLineDiscrepancyAction", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	http.Redirect(w, r, "/receiving/"+idStr+"?flash=discrepancy_marked", http.StatusSeeOther)
}

// ──────────────────────────────────────────────────────────────────────
// Tasks — directed-task queue page + claim/complete/exception POSTs
// ──────────────────────────────────────────────────────────────────────

// tasksListPage renders the operator's directed-task queue. Filterable
// by status (default: queued + assigned + in_progress). Each row
// exposes claim / complete / exception action buttons.
func (h *Handler) tasksListPage(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	view := map[string]any{
		"StatusFilter":   statusFilter,
		"StatusOptions":  []string{"open", "queued", "assigned", "in_progress", "complete", "verified"},
		"Tasks":          nil,
		"PendingCount":   0,
		"CompletedCount": 0,
		"Flash":          r.URL.Query().Get("flash"),
	}

	if h.deps.TaskStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		tasks, err := h.deps.TaskStore.ListByTenant(ctx, tenantID, statusFilter, 100)
		if err != nil {
			h.logger.Error("tasksListPage: list", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(tasks))
			pending, completed := 0, 0
			for _, t := range tasks {
				switch t.Status {
				case task.StatusComplete, task.StatusVerified:
					completed++
				case task.StatusQueued, task.StatusAssigned, task.StatusInProgress:
					pending++
				}
				rows = append(rows, taskRowView(t))
			}
			view["Tasks"] = rows
			view["PendingCount"] = pending
			view["CompletedCount"] = completed
		}
	}

	h.render(w, r, "tasks", "tasks", view)
}

// receivingLineView is the per-line shape for the receiving detail
// page. Includes LineID so the discrepancy form can post to a stable
// URL; transferLineView is shared with transfers/RTV and intentionally
// kept narrower (no LineID) so its callers don't pay for a UUID stringify
// they don't use.
func receivingLineView(l inventory.DocumentLineDTO) map[string]any {
	expected := "—"
	if l.ExpectedQuantity != nil {
		expected = *l.ExpectedQuantity
	}
	actual := "—"
	if l.ActualQuantity != nil {
		actual = *l.ActualQuantity
	}
	variance := ""
	if v := strToFloat(&l.VarianceQuantity); v != 0 {
		variance = formatQty(v)
	}
	reason := ""
	if l.VarianceReason != nil {
		reason = *l.VarianceReason
	}
	return map[string]any{
		"LineID":         l.ID.String(),
		"POLine":         l.LineNumber,
		"SKU":            l.ItemID.String()[:8],
		"Description":    "—",
		"POQty":          expected,
		"ReceivedQty":    actual,
		"Variance":       variance,
		"VarianceReason": reason,
		"HasDiscrepancy": variance != "",
	}
}

// taskRowView shapes one TaskDTO for the tasks template.
func taskRowView(t task.TaskDTO) map[string]any {
	short := t.ID.String()[:8]
	qty := "—"
	if t.Quantity != nil {
		qty = *t.Quantity
	}
	loc := "—"
	if t.LocationID != nil {
		loc = t.LocationID.String()[:8]
	}
	return map[string]any{
		"ID":         t.ID.String(),
		"ShortID":    short,
		"Type":       t.TaskType,
		"Priority":   t.Priority,
		"Status":     t.Status,
		"Quantity":   qty,
		"LocationID": loc,
		"CreatedAt":  t.CreatedAt.Format("2006-01-02 15:04"),
		"Claimable":  t.Status == task.StatusQueued,
		"Completable": t.Status == task.StatusAssigned || t.Status == task.StatusInProgress,
	}
}

// taskClaimAction transitions a task to in_progress. Reuses the task
// store's UpdateStatus + ValidateTransition path.
func (h *Handler) taskClaimAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if h.deps.TaskStore == nil {
		http.Redirect(w, r, "/tasks?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	// Two-step: queued → assigned → in_progress. The task package's
	// state machine refuses queued → in_progress directly.
	if _, err := h.deps.TaskStore.UpdateStatus(ctx, tenantID, id, task.StatusAssigned); err != nil && !errors.Is(err, task.ErrInvalidTransition) {
		h.logger.Warn("taskClaimAction: assign", zap.Error(err))
	}
	if _, err := h.deps.TaskStore.UpdateStatus(ctx, tenantID, id, task.StatusInProgress); err != nil {
		h.logger.Error("taskClaimAction: in_progress", zap.Error(err))
		http.Redirect(w, r, "/tasks?flash=claim_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tasks?flash=claimed", http.StatusSeeOther)
}

// taskCompleteAction transitions a task to complete.
func (h *Handler) taskCompleteAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if h.deps.TaskStore == nil {
		http.Redirect(w, r, "/tasks?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	if _, err := h.deps.TaskStore.UpdateStatus(ctx, tenantID, id, task.StatusComplete); err != nil {
		h.logger.Error("taskCompleteAction", zap.Error(err))
		http.Redirect(w, r, "/tasks?flash=complete_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tasks?flash=completed", http.StatusSeeOther)
}

// taskExceptionAction logs an exception against a task. Form fields:
// reason_code (required), note (optional).
func (h *Handler) taskExceptionAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	reasonCode := r.PostFormValue("reason_code")
	if reasonCode == "" {
		reasonCode = "other"
	}
	note := r.PostFormValue("note")
	if h.deps.TaskStore == nil {
		http.Redirect(w, r, "/tasks?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	req := task.ExceptionRequest{ReasonCode: reasonCode}
	if note != "" {
		req.Note = &note
	}
	if _, err := h.deps.TaskStore.LogException(ctx, tenantID, id, req); err != nil {
		h.logger.Error("taskExceptionAction", zap.Error(err))
		http.Redirect(w, r, "/tasks?flash=exception_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tasks?flash=exception_logged", http.StatusSeeOther)
}

// ──────────────────────────────────────────────────────────────────────
// OTB report — real budgets + lock action
// ──────────────────────────────────────────────────────────────────────

// reportOTBPage renders the L402 OTB budget surface. Reads from
// billing.Store.ListBudgets, falls back to empty-state when the store
// is nil. Wired W5 / GRO-824 (replaces the W2-era stub).
func (h *Handler) reportOTBPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Flash":       r.URL.Query().Get("flash"),
		"Budgets":     nil,
		"ActiveCount": 0,
		"LockedCount": 0,
		"TotalSats":   int64(0),
	}

	if h.deps.BillingStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		budgets, err := h.deps.BillingStore.ListBudgets(ctx, tenantID, "")
		if err != nil {
			h.logger.Error("reportOTBPage", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(budgets))
			active, locked := 0, 0
			var totalSats int64
			for _, b := range budgets {
				if b.Status == billing.BudgetStatusActive {
					active++
				}
				if b.Status == billing.BudgetStatusLocked {
					locked++
				}
				totalSats += b.BudgetSatoshis
				rows = append(rows, otbBudgetRowView(b))
			}
			view["Budgets"] = rows
			view["ActiveCount"] = active
			view["LockedCount"] = locked
			view["TotalSats"] = totalSats
		}
	}

	h.render(w, r, "report_otb", "reports", view)
}

// otbBudgetRowView shapes one OTBBudget for the report_otb template.
func otbBudgetRowView(b billing.OTBBudget) map[string]any {
	periodEnd := "—"
	if b.BudgetPeriodEnd != nil {
		periodEnd = b.BudgetPeriodEnd.Format("2006-01-02")
	}
	return map[string]any{
		"ID":                b.ID.String(),
		"PeriodStart":       b.BudgetPeriodStart.Format("2006-01-02"),
		"PeriodEnd":         periodEnd,
		"ScopeType":         b.ScopeType,
		"BudgetSatoshis":    b.BudgetSatoshis,
		"ConsumedSatoshis":  b.ConsumedSatoshis,
		"RemainingSatoshis": b.RemainingSatoshis,
		"HardLimit":         b.HardLimit,
		"Status":            b.Status,
		"Lockable":          b.Status == billing.BudgetStatusActive,
		"Unlockable":        b.Status == billing.BudgetStatusLocked,
	}
}

// otbLockAction toggles a budget's status active ↔ locked. Form field
// 'action' carries "lock" or "unlock"; missing or unrecognized
// defaults to "lock" (the dispatch's "lock period" call-out).
func (h *Handler) otbLockAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "budgetID")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid budgetID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	target := billing.BudgetStatusLocked
	if r.PostFormValue("action") == "unlock" {
		target = billing.BudgetStatusActive
	}
	if h.deps.BillingStore == nil {
		http.Redirect(w, r, "/reports/otb?flash=no_store", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	if _, err := h.deps.BillingStore.UpdateBudgetStatus(ctx, tenantID, id, target); err != nil {
		h.logger.Error("otbLockAction", zap.Error(err))
		http.Redirect(w, r, "/reports/otb?flash=lock_failed", http.StatusSeeOther)
		return
	}
	flash := "locked"
	if target == billing.BudgetStatusActive {
		flash = "unlocked"
	}
	http.Redirect(w, r, "/reports/otb?flash="+flash, http.StatusSeeOther)
}

// ──────────────────────────────────────────────────────────────────────
// Suggested orders — GET (placeholder list) + placeholder POSTs
// ──────────────────────────────────────────────────────────────────────

// suggestedOrdersPage renders the suggested-orders surface. No backing
// PO model today (W11 territory), so the list is stub. The action
// buttons in the template POST to the placeholder handlers below; the
// flash query param surfaces the action result back to the operator.
func (h *Handler) suggestedOrdersPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "report_suggested_orders", "reports", map[string]any{
		"Orders":       nil,
		"PendingCount": 0,
		"Flash":        r.URL.Query().Get("flash"),
	})
}


//
// Per dispatch: vendor integration is W11. These handlers do not
// persist; they redirect with a flash so the operator surface feels
// responsive. When the supplier + PO module lands, swap the redirect
// for a real store call.

func (h *Handler) suggestedOrderActionApprove(w http.ResponseWriter, r *http.Request) {
	suggestedOrderRedirect(w, r, "approved")
}

func (h *Handler) suggestedOrderActionReject(w http.ResponseWriter, r *http.Request) {
	suggestedOrderRedirect(w, r, "rejected")
}

func (h *Handler) suggestedOrderActionSend(w http.ResponseWriter, r *http.Request) {
	suggestedOrderRedirect(w, r, "queued_for_vendor")
}

func suggestedOrderRedirect(w http.ResponseWriter, r *http.Request, flash string) {
	http.Redirect(w, r, "/orders/suggested?flash="+flash, http.StatusSeeOther)
}
