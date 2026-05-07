// internal/web/handler_w15.go
//
// W15 / GRO-834 — Ecom channel surface (thin).
//
//   /ecom/orders → list of orders by channel (empty-state today)
//   /ecom/sync   → channel registry + sync health

package web

import (
	"net/http"

	"github.com/growdirect-llc/rapidpos/internal/ecom"
)

func (h *Handler) ecomOrdersPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Orders":   nil,
		"Count":    0,
		"Channels": ecomChannelRowsView(ecom.Registry()),
		"Note":     "Order rows populate when the first ecom adapter (Shopify) ships and connects. Adapter contract is defined in internal/ecom/ecom.go; SDK wiring is the next dispatch.",
	}
	h.render(w, r, "ecom_orders", "channels", view)
}

func (h *Handler) ecomSyncPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Channels": ecomChannelRowsView(ecom.Registry()),
		"Note":     "Sync health populates per-channel as adapters report. All channels are 'deferred' today — adapter implementations live in internal/ecom/<channel>/ once shipped.",
	}
	h.render(w, r, "ecom_sync", "channels", view)
}

func ecomChannelRowsView(cs []ecom.Channel) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{
			"Code":        c.Code,
			"DisplayName": c.DisplayName,
			"Status":      c.Status,
			"Note":        c.Note,
		})
	}
	return out
}
