// internal/web/handler_w8.go
//
// W8 / GRO-827 — Asset + Billing portal (read-only).
//
// Five GET surfaces over existing internal/asset and internal/billing
// stores. Per dispatch: no CRUD, no plan upgrade flow, no invoice
// generation. Hardware-asset taxonomy (app.assets per canary-asset.md)
// has no migration today; /assets renders the inventory-positions
// surface that internal/asset actually wraps. Document gap in closeout.

package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/asset"
	"github.com/growdirect-llc/rapidpos/internal/billing"
)

// ──────────────────────────────────────────────────────────────────────
// /assets — registry list
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) assetsListPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	lowStock := q.Get("low_stock") == "1"
	limit := parseOwlLimit(q.Get("limit"), 50)

	view := map[string]any{
		"Status":        status,
		"StatusOptions": []string{"active", "discontinued"},
		"LowStock":      lowStock,
		"Limit":         limit,
		"LimitOptions":  []int{25, 50, 100, 200},
		"Items":         nil,
		"Count":         0,
	}

	if h.deps.AssetStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		positions, err := h.deps.AssetStore.List(ctx, asset.ListFilters{
			TenantID: tenantID,
			Status:   status,
			LowStock: lowStock,
			Limit:    limit,
		})
		if err != nil {
			h.logger.Error("assetsListPage: list", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(positions))
			for _, p := range positions {
				rows = append(rows, assetRowView(p))
			}
			view["Items"] = rows
			view["Count"] = len(positions)
		}
	}

	h.render(w, r, "assets_list", "assets", view)
}

func assetRowView(p asset.PositionRow) map[string]any {
	cost := "—"
	if p.CostBasis != nil {
		cost = strconv.FormatFloat(*p.CostBasis, 'f', 2, 64)
	}
	lastMov := "—"
	if p.LastMovementAt != nil {
		lastMov = p.LastMovementAt.Format("2006-01-02")
	}
	return map[string]any{
		"ItemID":         p.ItemID.String(),
		"ShortItem":      p.ItemID.String()[:8],
		"SKU":            p.SKU,
		"Description":    p.Description,
		"LocationShort":  p.LocationID.String()[:8],
		"OnHand":         strconv.FormatFloat(p.OnHand, 'f', 2, 64),
		"Reserved":       strconv.FormatFloat(p.Reserved, 'f', 2, 64),
		"OnOrder":        strconv.FormatFloat(p.OnOrder, 'f', 2, 64),
		"InTransit":      strconv.FormatFloat(p.InTransit, 'f', 2, 64),
		"CostBasis":      cost,
		"Status":         p.Status,
		"LastMovementAt": lastMov,
	}
}

// ──────────────────────────────────────────────────────────────────────
// /assets/{id} — item detail (positions across locations + lots)
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) assetDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "assets", nil)
		return
	}

	if h.deps.AssetStore == nil {
		h.render(w, r, "assets_detail", "assets", map[string]any{
			"Item":      map[string]any{"ItemID": idStr, "ShortItem": idStr[:8], "SKU": "—", "Description": "—", "ItemType": "—", "UOM": "—", "Status": "—"},
			"Positions": nil,
			"Lots":      nil,
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	d, err := h.deps.AssetStore.GetItem(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, asset.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "assets", nil)
			return
		}
		h.logger.Error("assetDetailPage", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "assets", nil)
		return
	}

	posRows := make([]map[string]any, 0, len(d.Positions))
	for _, p := range d.Positions {
		posRows = append(posRows, assetRowView(p))
	}
	lotRows := make([]map[string]any, 0, len(d.Lots))
	for _, l := range d.Lots {
		expiry := "—"
		if l.ExpiryDate != nil {
			expiry = l.ExpiryDate.Format("2006-01-02")
		}
		lotRows = append(lotRows, map[string]any{
			"LotID":      l.LotID.String()[:8],
			"LotNumber":  l.LotNumber,
			"LotType":    l.LotType,
			"ExpiryDate": expiry,
			"Status":     l.Status,
		})
	}

	h.render(w, r, "assets_detail", "assets", map[string]any{
		"Item": map[string]any{
			"ItemID":      d.ItemID.String(),
			"ShortItem":   d.ItemID.String()[:8],
			"SKU":         d.SKU,
			"Description": d.Description,
			"ItemType":    d.ItemType,
			"UOM":         d.UOM,
			"Status":      d.Status,
		},
		"Positions": posRows,
		"Lots":      lotRows,
	})
}

// ──────────────────────────────────────────────────────────────────────
// /billing/overview — current period metered cost + active budgets
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) billingOverviewPage(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	view := map[string]any{
		"PeriodStart":      periodStart.Format("2006-01-02"),
		"PeriodEnd":        periodEnd.Format("2006-01-02"),
		"TotalUnits":       int64(0),
		"StorageUnits":     int64(0),
		"WorkloadUnits":    int64(0),
		"CaptureUnits":     int64(0),
		"PositionCount":    0,
		"UnbilledCount":    0,
		"OldestUnbilled":   "—",
		"ActiveBudgets":    nil,
		"PlanTier":         "—",
	}

	if h.deps.BillingStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		rollup, err := h.deps.BillingStore.CostRollup(ctx, billing.CostRollupRequest{
			TenantID:    tenantID,
			PeriodStart: periodStart,
			PeriodEnd:   periodEnd,
		})
		if err != nil {
			h.logger.Error("billingOverviewPage: rollup", zap.Error(err))
		} else if rollup != nil {
			view["TotalUnits"] = rollup.TotalSatoshis
			view["StorageUnits"] = rollup.StorageSatoshis
			view["WorkloadUnits"] = rollup.WorkloadSatoshis
			view["CaptureUnits"] = rollup.CaptureSatoshis
			view["PositionCount"] = rollup.PositionCount
			view["UnbilledCount"] = rollup.UnbilledCount
			if rollup.OldestUnbilled != nil {
				view["OldestUnbilled"] = rollup.OldestUnbilled.Format("2006-01-02")
			}
		}

		budgets, err := h.deps.BillingStore.ListBudgets(ctx, tenantID, billing.BudgetStatusActive)
		if err != nil {
			h.logger.Error("billingOverviewPage: budgets", zap.Error(err))
		} else {
			rows := make([]map[string]any, 0, len(budgets))
			for _, b := range budgets {
				rows = append(rows, otbBudgetRowView(b))
			}
			view["ActiveBudgets"] = rows
		}
	}

	h.render(w, r, "billing_overview", "billing", view)
}

// ──────────────────────────────────────────────────────────────────────
// /billing/invoices — invoiced / unbilled position groups
// ──────────────────────────────────────────────────────────────────────
//
// No invoices table today. Aggregate view sourced from cost rollup
// over a 90-day rolling window so the operator can see what was
// invoiced vs what is pending invoice (per `invoiced_at IS NULL`).

func (h *Handler) billingInvoicesPage(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	periodStart := now.AddDate(0, -3, 0)

	view := map[string]any{
		"WindowFrom":     periodStart.Format("2006-01-02"),
		"WindowTo":       now.Format("2006-01-02"),
		"TotalUnits":     int64(0),
		"PositionCount":  0,
		"UnbilledCount":  0,
		"BilledCount":    0,
		"OldestUnbilled": "—",
	}

	if h.deps.BillingStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		rollup, err := h.deps.BillingStore.CostRollup(ctx, billing.CostRollupRequest{
			TenantID:    tenantID,
			PeriodStart: periodStart,
			PeriodEnd:   now,
		})
		if err != nil {
			h.logger.Error("billingInvoicesPage", zap.Error(err))
		} else if rollup != nil {
			view["TotalUnits"] = rollup.TotalSatoshis
			view["PositionCount"] = rollup.PositionCount
			view["UnbilledCount"] = rollup.UnbilledCount
			view["BilledCount"] = rollup.PositionCount - rollup.UnbilledCount
			if rollup.OldestUnbilled != nil {
				view["OldestUnbilled"] = rollup.OldestUnbilled.Format("2006-01-02")
			}
		}
	}

	h.render(w, r, "billing_invoices", "billing", view)
}

// ──────────────────────────────────────────────────────────────────────
// /billing/payment-method — read-only LNURL wallet binding
// ──────────────────────────────────────────────────────────────────────
//
// LNURL is the canonical wallet binding for L402 charge cycles. Per
// dispatch: read-only display; rotation lives at /connect (separate
// flow). This handler renders the placeholder view today; when the
// LNURL store grows a tenant-scoped lookup, swap the static content
// for a real read.

func (h *Handler) billingPaymentMethodPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Connected":   false,
		"WalletKey":   "—",
		"BoundAt":     "—",
		"ConnectURL":  "/connect",
	}
	h.render(w, r, "billing_payment_method", "billing", view)
}
