package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/chirp"
)

// rulesListPage renders the detection rules list from the real chirp store.
func (h *Handler) rulesListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var rules []chirp.Rule
	if h.deps.ChirpStore != nil {
		var err error
		rules, err = h.deps.ChirpStore.ListRules(ctx, tenantID)
		if err != nil {
			h.logger.Error("rulesListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "rules", nil)
			return
		}
	}
	h.render(w, r, "rules", "rules", map[string]any{
		"Rules":       rules,
		"ActiveCount": 0,
		"TotalCount":  len(rules),
	})
}

func (h *Handler) ruleDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "rules", nil)
		return
	}
	if h.deps.ChirpStore == nil {
		h.render(w, r, "rule_detail", "rules", map[string]any{
			"Rule": map[string]any{
				"ID": idStr, "Name": "Rule " + idStr,
				"Severity": "high", "Category": "—", "Description": "—",
				"Enabled": false, "FireCount": 0, "FiresToday": 0,
				"FiresThisWeek": 0, "Parameters": nil,
			},
		})
		return
	}
	tenantID := tenantIDFromCtx(ctx)
	rule, err := h.deps.ChirpStore.GetRuleByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, chirp.ErrRuleNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "rules", nil)
			return
		}
		h.logger.Error("ruleDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "rules", nil)
		return
	}
	h.render(w, r, "rule_detail", "rules", map[string]any{
		"Rule": rule,
	})
}

// chirpListPage renders the chirp (detection) list from the real chirp store.
func (h *Handler) chirpListPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)

	var detections []chirp.Detection
	if h.deps.ChirpStore != nil {
		var err error
		detections, err = h.deps.ChirpStore.ListDetections(ctx, chirp.DetectionQuery{
			TenantID: tenantID,
			Limit:    50,
		})
		if err != nil {
			h.logger.Error("chirpListPage: list", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			h.render(w, r, "err500", "chirps", nil)
			return
		}
	}
	h.render(w, r, "chirps", "chirps", map[string]any{
		"Chirps": detections,
	})
}

func (h *Handler) chirpDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, r, "err404", "chirps", nil)
		return
	}

	shortID := idStr
	if len(idStr) >= 8 {
		shortID = idStr[:8]
	}

	if h.deps.ChirpStore == nil {
		h.render(w, r, "chirp_detail", "chirps", map[string]any{
			"Chirp": map[string]any{
				"ID": idStr, "ShortID": shortID,
				"EventType": "—", "StoreID": "—", "CashierID": "—",
				"Amount": "—", "SKUCount": 0,
				"Hash":      "0000000000000000000000000000000000000000000000000000000000000000",
				"CreatedAt": "—", "CaseID": "",
			},
			"Signals": nil,
		})
		return
	}

	tenantID := tenantIDFromCtx(ctx)
	d, err := h.deps.ChirpStore.GetDetectionByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, chirp.ErrDetectionNotFound) {
			w.WriteHeader(http.StatusNotFound)
			h.render(w, r, "err404", "chirps", nil)
			return
		}
		h.logger.Error("chirpDetailPage: get", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		h.render(w, r, "err500", "chirps", nil)
		return
	}

	caseID := ""
	if d.CaseID != nil {
		caseID = d.CaseID.String()
	}
	h.render(w, r, "chirp_detail", "chirps", map[string]any{
		"Chirp": map[string]any{
			"ID": d.ID.String(), "ShortID": d.ID.String()[:8],
			"EventType": d.SourceEntityType,
			"StoreID":   "—",
			"CashierID": "—",
			"Amount":    "—",
			"SKUCount":  0,
			"Hash":      "0000000000000000000000000000000000000000000000000000000000000000",
			"CreatedAt": d.CreatedAt.Format(time.RFC3339),
			"CaseID":    caseID,
		},
		"Signals": nil,
	})
}
