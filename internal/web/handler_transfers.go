package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/inventory"
)

// transferListPage renders all transfer documents (transfer_out + transfer_in)
// for the tenant. Wired W2b.
func (h *Handler) transferListPage(w http.ResponseWriter, r *http.Request) {
	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_list", "transfers", map[string]any{
			"Transfers": nil, "InTransitCount": 0, "TotalCount": 0,
		})
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	docs, err := h.deps.InventoryStore.ListDocuments(ctx, inventory.ListDocumentsFilter{
		TenantID: tenantID,
		Types:    inventory.TransferTypes,
		Limit:    100,
	})
	if err != nil {
		h.logger.Error("transferListPage: list", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	transfers := make([]map[string]any, 0, len(docs))
	inTransit := 0
	for _, d := range docs {
		if d.Status == "in_progress" || d.Status == "draft" {
			inTransit++
		}
		transfers = append(transfers, transferRowView(d))
	}
	h.render(w, r, "transfers_list", "transfers", map[string]any{
		"Transfers":      transfers,
		"InTransitCount": inTransit,
		"TotalCount":     len(transfers),
	})
}

// transferDetailPage renders one transfer document with its line items.
// Wired W2b.
func (h *Handler) transferDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transfers", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_detail", "transfers", map[string]any{
			"Transfer": map[string]any{
				"ID": idStr, "ShortID": shortID, "FromStore": "—", "ToStore": "—",
				"Status": "in-transit", "StatusClass": "", "ItemCount": 0,
				"InitiatedBy": "—", "InitiatedAt": "—", "ExpectedArrival": "—",
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
			h.render(w, r, "err404", "transfers", nil)
			return
		}
		h.logger.Error("transferDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("transferDetailPage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}

	view := transferRowView(*doc)
	view["InitiatedBy"] = "—"
	if doc.PerformedByUserID != nil {
		view["InitiatedBy"] = doc.PerformedByUserID.String()[:8]
	}
	expected := "—"
	if doc.ExpectedAt != nil {
		expected = doc.ExpectedAt.Format(time.RFC3339)
	}
	view["ExpectedArrival"] = expected

	lineViews := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		lineViews = append(lineViews, transferLineView(l))
	}

	h.render(w, r, "transfers_detail", "transfers", map[string]any{
		"Transfer": view,
		"Lines":    lineViews,
	})
}

// transferVariancePage renders shipped vs received variance for one transfer.
// Wired W2b.
func (h *Handler) transferVariancePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "transfers", nil)
		return
	}
	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.InventoryStore == nil {
		h.render(w, r, "transfers_variance", "transfers", map[string]any{
			"Transfer":     map[string]any{"ID": idStr, "ShortID": shortID, "FromStore": "—", "ToStore": "—"},
			"ShippedTotal": 0, "ReceivedTotal": 0, "VarianceCount": 0, "ValueAtRisk": "—",
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
			h.render(w, r, "err404", "transfers", nil)
			return
		}
		h.logger.Error("transferVariancePage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}
	lines, err := h.deps.InventoryStore.ListDocumentLines(ctx, tenantID, doc.ID)
	if err != nil {
		h.logger.Error("transferVariancePage: list lines", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "transfers", nil)
		return
	}

	shippedTotal := 0.0
	receivedTotal := 0.0
	varianceLines := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		shipped := strToFloat(l.ExpectedQuantity)
		received := strToFloat(l.ActualQuantity)
		shippedTotal += shipped
		receivedTotal += received
		varQ := strToFloat(&l.VarianceQuantity)
		if varQ == 0 {
			continue
		}
		var estValue string = "—"
		if l.UnitCost != nil {
			cost := strToFloat(l.UnitCost)
			estValue = formatMoney(varQ * cost)
		}
		varianceLines = append(varianceLines, map[string]any{
			"SKU":         l.ItemID.String()[:8],
			"Description": "—",
			"QtyShipped":  formatQty(shipped),
			"QtyReceived": formatQty(received),
			"VarianceQty": formatQty(varQ),
			"EstValue":    estValue,
		})
	}

	view := transferRowView(*doc)
	h.render(w, r, "transfers_variance", "transfers", map[string]any{
		"Transfer":      view,
		"ShippedTotal":  formatQty(shippedTotal),
		"ReceivedTotal": formatQty(receivedTotal),
		"VarianceCount": len(varianceLines),
		"ValueAtRisk":   "—",
		"Lines":         varianceLines,
	})
}

// transferRowView is a shared row-to-view-model for the transfer list,
// detail, and variance handlers.
func transferRowView(d inventory.DocumentDTO) map[string]any {
	from := "—"
	if d.SourceLocationID != nil {
		from = d.SourceLocationID.String()[:8]
	}
	to := "—"
	if d.DestinationLocationID != nil {
		to = d.DestinationLocationID.String()[:8]
	}
	itemCount := 0
	if d.TotalQuantity != nil {
		itemCount = int(strToFloat(d.TotalQuantity))
	}
	return map[string]any{
		"ID":          d.ID.String(),
		"ShortID":     d.ID.String()[:8],
		"FromStore":   from,
		"ToStore":     to,
		"Status":      mapDocStatus(d.Status),
		"StatusClass": "",
		"ItemCount":   itemCount,
		"InitiatedAt": d.CreatedAt.Format(time.RFC3339),
	}
}

// transferLineView shapes one document line for the detail template.
// Shared with handler_w5.go (receiving uses the same line shape).
func transferLineView(l inventory.DocumentLineDTO) map[string]any {
	shipped := "—"
	if l.ExpectedQuantity != nil {
		shipped = *l.ExpectedQuantity
	}
	received := "—"
	if l.ActualQuantity != nil {
		received = *l.ActualQuantity
	}
	variance := ""
	if v := strToFloat(&l.VarianceQuantity); v != 0 {
		variance = formatQty(v)
	}
	return map[string]any{
		"SKU":         l.ItemID.String()[:8],
		"Description": "—",
		"QtyShipped":  shipped,
		"QtyReceived": received,
		"Variance":    variance,
	}
}

// mapDocStatus translates schema status values to the template's expected
// in-transit / received / variance vocabulary used for badge styling.
func mapDocStatus(s string) string {
	switch s {
	case "in_progress", "draft":
		return "in-transit"
	case "completed":
		return "received"
	case "cancelled":
		return "cancelled"
	case "reconciled":
		return "received"
	default:
		return s
	}
}

// strToFloat parses a *string decimal into float64; nil/empty/invalid → 0.
// Shared helper used across transfers, receiving, and reports.
func strToFloat(s *string) float64 {
	if s == nil || *s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0
	}
	return f
}

// formatQty formats a quantity float as integer when whole, else 4-decimal.
func formatQty(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 4, 64)
}

// formatMoney formats a float as a USD-style currency string.
func formatMoney(f float64) string {
	return "$" + strconv.FormatFloat(f, 'f', 2, 64)
}
