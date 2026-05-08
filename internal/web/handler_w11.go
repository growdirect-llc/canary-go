// internal/web/handler_w11.go
//
// W11 — Supplier + Purchase Order portal.
//
//   /suppliers                    list
//   /suppliers (POST)             create
//   /suppliers/{id}               detail
//   /suppliers/{id}/scorecard     scorecard placeholder
//   /po                           list
//   /po (POST)                    create
//   /po/{id}                      detail (PO + lines)
//   /po/{id}/match                three-way match summary placeholder
//   /po/{id}/status (POST)        lifecycle transition

package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/po"
	"github.com/ruptiv/canary/internal/supplier"
)

// ──────────────────────────────────────────────────────────────────────
// Suppliers
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) suppliersListPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	compliance := q.Get("compliance")
	limit := parseOwlLimit(q.Get("limit"), 100)

	view := map[string]any{
		"Flash":        r.URL.Query().Get("flash"),
		"Compliance":   compliance,
		"Options":      []string{"active", "review", "blocked"},
		"Limit":        limit,
		"LimitOptions": []int{50, 100, 250, 500},
		"Suppliers":    nil,
		"Count":        0,
	}
	if h.deps.SupplierStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		list, err := h.deps.SupplierStore.List(r.Context(), merchantID, compliance, limit)
		if err != nil {
			h.logger.Error("suppliersListPage", zap.Error(err))
		} else {
			view["Suppliers"] = supplierRowsView(list)
			view["Count"] = len(list)
		}
	}
	h.render(w, r, "suppliers_list", "procurement", view)
}

func (h *Handler) suppliersCreate(w http.ResponseWriter, r *http.Request) {
	if h.deps.SupplierStore == nil {
		http.Redirect(w, r, "/suppliers?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	name := r.PostFormValue("supplier_name")
	if name == "" {
		http.Redirect(w, r, "/suppliers?flash=missing_name", http.StatusSeeOther)
		return
	}
	req := supplier.CreateRequest{
		MerchantID:       merchantIDFromCtx(w, r),
		SupplierName:     name,
		ComplianceStatus: r.PostFormValue("compliance_status"),
	}
	if v := r.PostFormValue("contact_email"); v != "" {
		req.ContactEmail = &v
	}
	if v := r.PostFormValue("contact_phone"); v != "" {
		req.ContactPhone = &v
	}
	if v := r.PostFormValue("payment_terms"); v != "" {
		req.PaymentTerms = &v
	}
	if _, err := h.deps.SupplierStore.Create(r.Context(), req); err != nil {
		h.logger.Error("suppliersCreate", zap.Error(err))
		http.Redirect(w, r, "/suppliers?flash=create_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/suppliers?flash=created", http.StatusSeeOther)
}

func (h *Handler) supplierDetailPage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "procurement", nil)
		return
	}
	view := map[string]any{
		"Supplier": map[string]any{"ID": id.String(), "ShortID": id.String()[:8], "SupplierName": "—", "ContactEmail": "—", "ContactPhone": "—", "PaymentTerms": "—", "ComplianceStatus": "—"},
	}
	if h.deps.SupplierStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		sp, err := h.deps.SupplierStore.Get(r.Context(), merchantID, id)
		if err != nil {
			if errors.Is(err, supplier.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				h.render(w, r, "err404", "procurement", nil)
				return
			}
			h.logger.Error("supplierDetailPage", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "procurement", nil)
			return
		}
		view["Supplier"] = supplierDetailView(*sp)
	}
	h.render(w, r, "suppliers_detail", "procurement", view)
}

func (h *Handler) supplierScorecardPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "procurement", nil)
		return
	}
	view := map[string]any{
		"Supplier":      map[string]any{"ID": idStr, "ShortID": idStr[:8], "SupplierName": "—"},
		"OnTimePct":     "—",
		"AccuracyPct":   "—",
		"FillRatePct":   "—",
		"POCount":       0,
		"BlockedReason": "scorecard requires PO history joined to goods_receipt + supplier_invoice. Once POs flow through receive → match, this populates from app.purchase_orders + inventory.inventory_documents joins.",
	}
	if h.deps.SupplierStore != nil {
		if sp, err := h.deps.SupplierStore.Get(r.Context(), merchantIDFromCtx(w, r), id); err == nil {
			view["Supplier"] = supplierDetailView(*sp)
		}
	}
	h.render(w, r, "suppliers_scorecard", "procurement", view)
}

func supplierRowsView(ls []supplier.Supplier) []map[string]any {
	out := make([]map[string]any, 0, len(ls))
	for _, s := range ls {
		email := "—"
		if s.ContactEmail != nil {
			email = *s.ContactEmail
		}
		out = append(out, map[string]any{
			"ID":               s.ID.String(),
			"ShortID":          s.ID.String()[:8],
			"SupplierName":     s.SupplierName,
			"ContactEmail":     email,
			"ComplianceStatus": s.ComplianceStatus,
			"CreatedAt":        s.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

func supplierDetailView(s supplier.Supplier) map[string]any {
	stringOrDash := func(p *string) string {
		if p == nil || *p == "" {
			return "—"
		}
		return *p
	}
	return map[string]any{
		"ID":               s.ID.String(),
		"ShortID":          s.ID.String()[:8],
		"SupplierName":     s.SupplierName,
		"ContactEmail":     stringOrDash(s.ContactEmail),
		"ContactPhone":     stringOrDash(s.ContactPhone),
		"PaymentTerms":     stringOrDash(s.PaymentTerms),
		"ComplianceStatus": s.ComplianceStatus,
		"CreatedAt":        s.CreatedAt.Format("2006-01-02"),
	}
}

// ──────────────────────────────────────────────────────────────────────
// Purchase Orders
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) poListPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	limit := parseOwlLimit(q.Get("limit"), 100)
	view := map[string]any{
		"Flash":         r.URL.Query().Get("flash"),
		"Status":        status,
		"StatusOptions": []string{"draft", "submitted", "received", "closed", "cancelled"},
		"Limit":         limit,
		"LimitOptions":  []int{50, 100, 250, 500},
		"POs":           nil,
		"Count":         0,
	}
	if h.deps.POStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		list, err := h.deps.POStore.List(r.Context(), merchantID, status, limit)
		if err != nil {
			h.logger.Error("poListPage", zap.Error(err))
		} else {
			view["POs"] = poRowsView(list)
			view["Count"] = len(list)
		}
	}
	h.render(w, r, "po_list", "procurement", view)
}

func (h *Handler) poCreate(w http.ResponseWriter, r *http.Request) {
	if h.deps.POStore == nil {
		http.Redirect(w, r, "/po?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	supplierIDStr := r.PostFormValue("supplier_id")
	supplierID, err := uuid.Parse(supplierIDStr)
	if err != nil {
		http.Redirect(w, r, "/po?flash=invalid_supplier", http.StatusSeeOther)
		return
	}
	poNum := r.PostFormValue("po_number")
	if poNum == "" {
		http.Redirect(w, r, "/po?flash=missing_po_number", http.StatusSeeOther)
		return
	}
	req := po.CreateRequest{
		MerchantID: merchantIDFromCtx(w, r),
		SupplierID: supplierID,
		PONumber:   poNum,
	}
	if v := r.PostFormValue("total_cost"); v != "" {
		req.TotalCost = &v
	}
	if _, err := h.deps.POStore.Create(r.Context(), req); err != nil {
		h.logger.Error("poCreate", zap.Error(err))
		http.Redirect(w, r, "/po?flash=create_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/po?flash=created", http.StatusSeeOther)
}

func (h *Handler) poDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "procurement", nil)
		return
	}
	view := map[string]any{
		"PO":    map[string]any{"ID": idStr, "ShortID": idStr[:8], "PONumber": "—", "Status": "—", "ExpectedAt": "—", "TotalCost": "—"},
		"Lines": nil,
		"Flash": r.URL.Query().Get("flash"),
	}
	if h.deps.POStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		p, err := h.deps.POStore.Get(r.Context(), merchantID, id)
		if err != nil {
			if errors.Is(err, po.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				h.render(w, r, "err404", "procurement", nil)
				return
			}
			h.logger.Error("poDetailPage", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "procurement", nil)
			return
		}
		view["PO"] = poDetailView(*p)
		lines, _ := h.deps.POStore.ListLines(r.Context(), merchantID, id)
		view["Lines"] = poLineRowsView(lines)
	}
	h.render(w, r, "po_detail", "procurement", view)
}

func (h *Handler) poStatusAction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if h.deps.POStore == nil {
		http.Redirect(w, r, "/po/"+idStr+"?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	to := r.PostFormValue("status")
	merchantID := merchantIDFromCtx(w, r)
	if _, err := h.deps.POStore.UpdateStatus(r.Context(), merchantID, id, to); err != nil {
		h.logger.Warn("poStatusAction", zap.Error(err))
		http.Redirect(w, r, "/po/"+idStr+"?flash=transition_failed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/po/"+idStr+"?flash="+to, http.StatusSeeOther)
}

func (h *Handler) poMatchPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "procurement", nil)
		return
	}
	view := map[string]any{
		"PO":            map[string]any{"ID": idStr, "ShortID": idStr[:8], "PONumber": "—", "Status": "—"},
		"Lines":         nil,
		"BlockedReason": "Three-way match wires the PO line ↔ receipt line ↔ supplier-invoice line triple. Once supplier_invoice substrate lands, this view shows variance flags per line.",
	}
	if h.deps.POStore != nil {
		merchantID := merchantIDFromCtx(w, r)
		if p, err := h.deps.POStore.Get(r.Context(), merchantID, id); err == nil {
			view["PO"] = poDetailView(*p)
			lines, _ := h.deps.POStore.ListLines(r.Context(), merchantID, id)
			view["Lines"] = poLineRowsView(lines)
		}
	}
	h.render(w, r, "po_match", "procurement", view)
}

func poRowsView(ls []po.PO) []map[string]any {
	out := make([]map[string]any, 0, len(ls))
	for _, p := range ls {
		expected := "—"
		if p.ExpectedAt != nil {
			expected = p.ExpectedAt.Format("2006-01-02")
		}
		total := "—"
		if p.TotalCost != nil {
			total = *p.TotalCost
		}
		out = append(out, map[string]any{
			"ID":         p.ID.String(),
			"ShortID":    p.ID.String()[:8],
			"PONumber":   p.PONumber,
			"Status":     p.Status,
			"ExpectedAt": expected,
			"TotalCost":  total,
			"CreatedAt":  p.CreatedAt.Format("2006-01-02"),
		})
	}
	return out
}

func poDetailView(p po.PO) map[string]any {
	expected := "—"
	if p.ExpectedAt != nil {
		expected = p.ExpectedAt.Format("2006-01-02")
	}
	total := "—"
	if p.TotalCost != nil {
		total = *p.TotalCost
	}
	return map[string]any{
		"ID":         p.ID.String(),
		"ShortID":    p.ID.String()[:8],
		"PONumber":   p.PONumber,
		"Status":     p.Status,
		"ExpectedAt": expected,
		"TotalCost":  total,
		"SupplierID": p.SupplierID.String(),
		"CreatedAt":  p.CreatedAt.Format("2006-01-02"),
	}
}

func poLineRowsView(ls []po.Line) []map[string]any {
	out := make([]map[string]any, 0, len(ls))
	for _, l := range ls {
		desc := "—"
		if l.Description != nil {
			desc = *l.Description
		}
		unitCost := "—"
		if l.UnitCost != nil {
			unitCost = *l.UnitCost
		}
		out = append(out, map[string]any{
			"LineNumber":  strconv.Itoa(l.LineNumber),
			"Description": desc,
			"OrderedQty":  l.OrderedQty,
			"ReceivedQty": l.ReceivedQty,
			"UnitCost":    unitCost,
			"Variance":    "—", // computed once supplier_invoice + receipt joins land
		})
	}
	return out
}
