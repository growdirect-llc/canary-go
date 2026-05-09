package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/inventory"
)

// receivingListPage lists open + recent goods_receipt documents.
// Wired W2d.
func (h *Handler) receivingListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_list", "receiving", map[string]any{
			"Sessions": nil, "OpenCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    []string{inventory.DocumentTypeGoodsReceipt},
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("receivingListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	open := 0
	sessions := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if d.Status == "draft" || d.Status == "in_progress" {
			open++
		}
		sessions = append(sessions, receivingRowView(d))
	}
	h.render(w, r, "receiving_list", "receiving", map[string]any{
		"Sessions":   sessions,
		"OpenCount":  open,
		"TotalCount": len(sessions),
	})
}

// receivingDetailPage renders one goods_receipt document with hydrated lines.
// Wired W2d.
func (h *Handler) receivingDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "receiving", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_detail", "receiving", map[string]any{
			"Session": map[string]any{
				"ID": idStr, "ShortID": shortID, "PONumber": "—", "Vendor": "—",
				"Status": "open", "ReceivedBy": "—", "OpenedAt": "—",
			},
			"Lines": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("receivingDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	view := receivingRowView(*doc)
	view["ReceivedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["ReceivedBy"] = doc.PerformedByUserID.String()[:8]
	}
	view["OpenedAt"] = doc.CreatedAt.Format(time.RFC3339)
	lineRows := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		lineRows = append(lineRows, receivingLineView(l))
	}
	h.render(w, r, "receiving_detail", "receiving", map[string]any{
		"Session": view,
		"Lines":   lineRows,
		"Flash":   r.URL.Query().Get("flash"),
	})
}

// receivingClosePage renders the close-and-post summary for a goods_receipt
// document. Wired W2d. Close action POST is W5.
func (h *Handler) receivingClosePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "receiving", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "receiving_close", "receiving", map[string]any{
			"Session":   map[string]any{"ID": idStr, "ShortID": shortID, "PONumber": "—", "Vendor": "—"},
			"LineCount": 0, "TotalReceived": 0, "DiscrepancyCount": 0, "Discrepancies": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "receiving", nil)
			return
		}
		h.logger.Error("receivingClosePage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("receivingClosePage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "receiving", nil)
		return
	}
	totalReceived := 0.0
	disc := make([]map[string]any, 0)
	for _, l := range lines {
		totalReceived += strToFloat(l.ActualQuantity)
		if v := strToFloat(&l.VarianceQuantity); v != 0 {
			disc = append(disc, transferLineView(l))
		}
	}
	view := receivingRowView(*doc)
	h.render(w, r, "receiving_close", "receiving", map[string]any{
		"Session":          view,
		"LineCount":        len(lines),
		"TotalReceived":    formatQty(totalReceived),
		"DiscrepancyCount": len(disc),
		"Discrepancies":    disc,
	})
}

// returnsListPage lists RTV (return-to-vendor) documents.
// Wired W2d.
func (h *Handler) returnsListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "returns_list", "returns", map[string]any{
			"Returns": nil, "PendingCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    []string{inventory.DocumentTypeRTV},
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("returnsListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	pending := 0
	rows := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if d.Status == "draft" || d.Status == "in_progress" {
			pending++
		}
		rows = append(rows, returnRowView(d))
	}
	h.render(w, r, "returns_list", "returns", map[string]any{
		"Returns":      rows,
		"PendingCount": pending,
		"TotalCount":   len(rows),
	})
}

// returnsDetailPage renders one RTV document with hydrated lines.
// Wired W2d.
func (h *Handler) returnsDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "returns", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}
	if h.deps.InventoryStore == nil {
		h.render(w, r, "returns_detail", "returns", map[string]any{
			"Return": map[string]any{
				"ID": idStr, "ShortID": shortID, "Vendor": "—", "Status": "pending",
				"InitiatedBy": "—", "InitiatedAt": "—",
				"CreditExpected": "—", "CreditReceived": "—", "Reconciled": false,
			},
			"Items": nil,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	doc, err := h.deps.InventoryStore.GetDocument(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, inventory.ErrDocumentNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "returns", nil)
			return
		}
		h.logger.Error("returnsDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("returnsDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "returns", nil)
		return
	}
	view := returnRowView(*doc)
	view["InitiatedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["InitiatedBy"] = doc.PerformedByUserID.String()[:8]
	}
	view["InitiatedAt"] = doc.CreatedAt.Format(time.RFC3339)
	view["CreditExpected"] = strDeref(doc.TotalCost, "—")
	view["CreditReceived"] = "—"
	view["Reconciled"] = doc.Status == "reconciled"

	items := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		items = append(items, transferLineView(l))
	}
	h.render(w, r, "returns_detail", "returns", map[string]any{
		"Return": view,
		"Items":  items,
	})
}

// receivingRowView is the shared row→view-model for receiving list/detail.
func receivingRowView(d inventory.DocumentDTO) map[string]any {
	vendor := "—"
	if d.VendorID != nil {
		vendor = d.VendorID.String()[:8]
	}
	return map[string]any{
		"ID":       d.ID.String(),
		"ShortID":  d.ID.String()[:8],
		"PONumber": d.DocumentNumber,
		"Vendor":   vendor,
		"Status":   d.Status,
	}
}

// returnRowView is the shared row→view-model for RTV list/detail.
func returnRowView(d inventory.DocumentDTO) map[string]any {
	vendor := "—"
	if d.VendorID != nil {
		vendor = d.VendorID.String()[:8]
	}
	itemCount := 0
	if d.TotalQuantity != nil {
		itemCount = int(strToFloat(d.TotalQuantity))
	}
	return map[string]any{
		"ID":        d.ID.String(),
		"ShortID":   d.ID.String()[:8],
		"Vendor":    vendor,
		"Status":    d.Status,
		"ItemCount": itemCount,
	}
}
