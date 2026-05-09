package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/alert"
)

// alertListPage renders the alert list from the real alert store.
func (h *Handler) alertListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var alerts []alert.AlertDTO
	if h.deps.AlertStore != nil {
		var err error
		alerts, err = h.deps.AlertStore.List(ctx, alert.ListFilters{
			TenantID: tenantID,
			Limit:    50,
		})
		if err != nil {
			h.logger.Error("alertListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "alerts", nil)
			return
		}
	}
	h.render(w, r, "alerts", "alerts", map[string]any{
		"Alerts": alerts,
	})
}

func (h *Handler) alertDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "alerts", nil)
		return
	}

	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.AlertStore == nil {
		h.render(w, r, "alert_detail", "alerts", map[string]any{
			"Alert": map[string]any{
				"ID": idStr, "ShortID": shortID,
				"Title": "Alert " + shortID, "Severity": "high",
				"Status": "open", "StatusClass": "", "Description": "—",
				"RuleID": "—", "RuleCode": "—", "StoreID": "—",
				"TransactionID": "—", "CreatedAt": "—",
			},
			"Timeline": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	a, err := h.deps.AlertStore.GetByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, alert.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "alerts", nil)
			return
		}
		h.logger.Error("alertDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "alerts", nil)
		return
	}

	h.render(w, r, "alert_detail", "alerts", map[string]any{
		"Alert": map[string]any{
			"ID": a.ID.String(), "ShortID": a.ID.String()[:8],
			"Title":         "Alert " + a.ID.String()[:8],
			"Severity":      a.Severity,
			"Status":        a.Status,
			"StatusClass":   "",
			"Description":   "—",
			"RuleID":        a.RuleID.String(),
			"RuleCode":      a.RuleCode,
			"StoreID":       "—",
			"TransactionID": a.SourceEntityID.String(),
			"CreatedAt":     a.CreatedAt.Format(time.RFC3339),
		},
		"Timeline": nil,
	})
}
