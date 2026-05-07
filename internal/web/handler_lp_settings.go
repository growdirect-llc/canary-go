// internal/web/handler_lp_settings.go
//
// Handlers for the 10 W1 LP settings screens — CRUD over detection.allow_list
// using the type+kind pattern discriminator.
//
// All 10 screens follow the same shape:
//   GET  <path>             → list entries filtered by pattern type+kind, render template
//   POST <path>             → create a new entry from the form
//   POST <path>/{id}/delete → delete one entry by id (tenant-scoped)
//
// Each screen's row → view-model and form → pattern conversion is captured in
// an lpScreen value; the unified mount loop wires all three routes per screen.
//
// W1 dispatch: 

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	lpPkg "github.com/ruptiv/canary/internal/lp"
)

// lpScreen ties one settings URL to the unified pattern-backed CRUD path.
type lpScreen struct {
	Path        string // e.g. /settings/allowlist/dead-count
	ActivePage  string // sidebar key, "settings"
	TmplName    string // template name (already pre-parsed in h.templates)
	PatternType string // lp.PatternType*
	Kind        string // lp.Kind*
	ListKey     string // template Data field for the rows ("Entries", "Codes", "Caps", etc.)

	// RowToView maps a stored row to the template's per-row map shape.
	RowToView func(row lpPkg.AllowListRow) map[string]any

	// FormToPattern parses the create-form fields, validates, and returns
	// the pattern jsonb plus an optional reason string.
	FormToPattern func(r *http.Request) (json.RawMessage, *string, error)

	// ExtraData (optional) computes extra template fields from the entry list.
	// Used e.g. to derive "Enabled" from the most recent training-mode entry.
	ExtraData func(entries []map[string]any) map[string]any
}

// mountLPSettings registers all 10 screens on the given chi router.
func (h *Handler) mountLPSettings(r chi.Router) {
	for _, s := range lpScreens() {
		// Capture by value to avoid loop variable closure pitfalls.
		s := s
		r.Get(s.Path, h.lpScreenGet(s))
		r.Post(s.Path, h.lpScreenPost(s))
		r.Post(s.Path+"/{id}/delete", h.lpScreenDelete(s))
	}
}

// lpScreenGet renders the list view for one screen.
func (h *Handler) lpScreenGet(s lpScreen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)

		var rows []lpPkg.AllowListRow
		if h.deps.AllowListStore != nil {
			var err error
			rows, err = h.deps.AllowListStore.ListByPattern(ctx, tenantID, s.PatternType, s.Kind, 200)
			if err != nil {
				h.logger.Error("lpScreenGet: list", zap.String("path", s.Path), zap.Error(err))
				w.WriteHeader(http.StatusInternalServerError)
				h.render(w, r, "err500", s.ActivePage, nil)
				return
			}
		}

		entries := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			entries = append(entries, s.RowToView(row))
		}

		data := map[string]any{
			s.ListKey: entries,
		}
		if s.ExtraData != nil {
			for k, v := range s.ExtraData(entries) {
				data[k] = v
			}
		}
		h.render(w, r, s.TmplName, s.ActivePage, data)
	}
}

// lpScreenPost handles the create-form submission. On success redirects back
// to the GET path; on validation error redirects with ?error=... so the
// template can surface the message.
func (h *Handler) lpScreenPost(s lpScreen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, s.Path+"?error=parse", http.StatusSeeOther)
			return
		}

		pattern, reason, err := s.FormToPattern(r)
		if err != nil {
			http.Redirect(w, r, s.Path+"?error="+formErrorCode(err), http.StatusSeeOther)
			return
		}

		if h.deps.AllowListStore == nil {
			h.logger.Warn("lpScreenPost: AllowListStore nil — write skipped", zap.String("path", s.Path))
			http.Redirect(w, r, s.Path, http.StatusSeeOther)
			return
		}

		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if _, err := h.deps.AllowListStore.Create(ctx, lpPkg.CreateInput{
			TenantID: tenantID,
			Pattern:  pattern,
			Reason:   reason,
		}); err != nil {
			h.logger.Error("lpScreenPost: create", zap.String("path", s.Path), zap.Error(err))
			http.Redirect(w, r, s.Path+"?error=create", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, s.Path, http.StatusSeeOther)
	}
}

// lpScreenDelete removes one entry by id and redirects back to the list.
func (h *Handler) lpScreenDelete(s lpScreen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Redirect(w, r, s.Path+"?error=bad_id", http.StatusSeeOther)
			return
		}
		if h.deps.AllowListStore == nil {
			http.Redirect(w, r, s.Path, http.StatusSeeOther)
			return
		}
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if err := h.deps.AllowListStore.Delete(ctx, tenantID, id); err != nil {
			if errors.Is(err, lpPkg.ErrAllowListNotFound) {
				http.Redirect(w, r, s.Path, http.StatusSeeOther)
				return
			}
			h.logger.Error("lpScreenDelete", zap.String("path", s.Path), zap.Error(err))
			http.Redirect(w, r, s.Path+"?error=delete", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, s.Path, http.StatusSeeOther)
	}
}

// formErrorCode flattens an error to a stable URL token.
func formErrorCode(err error) string {
	msg := strings.ToLower(err.Error())
	msg = strings.ReplaceAll(msg, " ", "_")
	if len(msg) > 40 {
		msg = msg[:40]
	}
	return msg
}

// ── Helpers used by FormToPattern / RowToView definitions ─────────────────────

func formStr(r *http.Request, key string) string {
	return strings.TrimSpace(r.FormValue(key))
}

func patStr(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	if v, ok := p[key]; ok {
		switch s := v.(type) {
		case string:
			return s
		case float64:
			return fmt.Sprintf("%g", s)
		case bool:
			if s {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

func patBool(p map[string]any, key string) bool {
	if p == nil {
		return false
	}
	if v, ok := p[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ── Screen definitions ────────────────────────────────────────────────────────

func lpScreens() []lpScreen {
	return []lpScreen{
		// 1. Allow-list — Dead count (cashier IDs)
		{
			Path:        "/settings/allowlist/dead-count",
			ActivePage:  "settings",
			TmplName:    "settings_allowlist_dead_count",
			PatternType: lpPkg.PatternTypeAllowlist,
			Kind:        lpPkg.KindDeadCount,
			ListKey:     "Entries",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":        row.ID.String(),
					"CashierID": patStr(p, "cashier_id"),
					"Store":     patStr(p, "store"),
					"AddedBy":   "—",
					"AddedAt":   row.CreatedAt.Format("2006-01-02 15:04"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				cashier := formStr(r, "cashier_id")
				store := formStr(r, "store")
				if cashier == "" {
					return nil, nil, errors.New("cashier_id required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeAllowlist, lpPkg.KindDeadCount, map[string]any{
					"cashier_id": cashier,
					"store":      store,
				})
				if err != nil {
					return nil, nil, err
				}
				return p, nilIfEmpty(formStr(r, "reason")), nil
			},
			ExtraData: func(_ []map[string]any) map[string]any {
				return map[string]any{"StoreID": "—"}
			},
		},

		// 2. Allow-list — Discounts (pre-approved discount patterns)
		{
			Path:        "/settings/allowlist/discounts",
			ActivePage:  "settings",
			TmplName:    "settings_allowlist_discounts",
			PatternType: lpPkg.PatternTypeAllowlist,
			Kind:        lpPkg.KindDiscounts,
			ListKey:     "Entries",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":         row.ID.String(),
					"ReasonCode": patStr(p, "reason_code"),
					"MaxPct":     patStr(p, "max_pct"),
					"Scope":      patStr(p, "scope"),
					"AddedAt":    row.CreatedAt.Format("2006-01-02 15:04"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				maxPct := formStr(r, "max_pct")
				scope := formStr(r, "scope")
				if code == "" {
					return nil, nil, errors.New("reason_code required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeAllowlist, lpPkg.KindDiscounts, map[string]any{
					"reason_code": code,
					"max_pct":     maxPct,
					"scope":       scope,
				})
				return p, nil, err
			},
		},

		// 3. Allow-list — Admin voids (authorized void reason codes)
		{
			Path:        "/settings/allowlist/voids",
			ActivePage:  "settings",
			TmplName:    "settings_allowlist_voids",
			PatternType: lpPkg.PatternTypeAllowlist,
			Kind:        lpPkg.KindVoids,
			ListKey:     "Entries",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":          row.ID.String(),
					"ReasonCode":  patStr(p, "reason_code"),
					"Description": patStr(p, "description"),
					"AddedAt":     row.CreatedAt.Format("2006-01-02 15:04"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				desc := formStr(r, "description")
				if code == "" {
					return nil, nil, errors.New("reason_code required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeAllowlist, lpPkg.KindVoids, map[string]any{
					"reason_code": code,
					"description": desc,
				})
				return p, nil, err
			},
		},

		// 4. Allow-list — Comps (authorized comp reason codes)
		{
			Path:        "/settings/allowlist/comps",
			ActivePage:  "settings",
			TmplName:    "settings_allowlist_comps",
			PatternType: lpPkg.PatternTypeAllowlist,
			Kind:        lpPkg.KindComps,
			ListKey:     "Entries",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":          row.ID.String(),
					"ReasonCode":  patStr(p, "reason_code"),
					"Description": patStr(p, "description"),
					"AddedAt":     row.CreatedAt.Format("2006-01-02 15:04"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				desc := formStr(r, "description")
				if code == "" {
					return nil, nil, errors.New("reason_code required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeAllowlist, lpPkg.KindComps, map[string]any{
					"reason_code": code,
					"description": desc,
				})
				return p, nil, err
			},
		},

		// 5. Training mode (toggle — last entry wins)
		{
			Path:        "/settings/training-mode",
			ActivePage:  "settings",
			TmplName:    "settings_training_mode",
			PatternType: lpPkg.PatternTypeSetting,
			Kind:        lpPkg.KindTrainingMode,
			ListKey:     "RecentWindows",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":      row.ID.String(),
					"Enabled": patBool(p, "enabled"),
					"Start":   row.CreatedAt.Format("2006-01-02 15:04"),
					"End":     "—",
					"Stores":  patStr(p, "stores"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				enabled := formStr(r, "enabled") == "true"
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeSetting, lpPkg.KindTrainingMode, map[string]any{
					"enabled": enabled,
				})
				return p, nil, err
			},
			ExtraData: func(entries []map[string]any) map[string]any {
				// Most recent entry (entries are ordered DESC) wins as the
				// current state. Empty list = OFF.
				out := map[string]any{
					"Enabled":      false,
					"ActiveWindow": nil,
				}
				if len(entries) > 0 {
					out["Enabled"] = entries[0]["Enabled"]
				}
				return out
			},
		},

		// 6. Alert routing (severity + type + store → destination)
		{
			Path:        "/settings/alert-routing",
			ActivePage:  "settings",
			TmplName:    "settings_alert_routing",
			PatternType: lpPkg.PatternTypeRouting,
			Kind:        lpPkg.KindAlertRouting,
			ListKey:     "Routes",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":          row.ID.String(),
					"Severity":    patStr(p, "severity"),
					"AlertType":   patStr(p, "alert_type"),
					"Store":       patStr(p, "store"),
					"Destination": patStr(p, "destination"),
					"AddedAt":     row.CreatedAt.Format("2006-01-02 15:04"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				severity := formStr(r, "severity")
				alertType := formStr(r, "alert_type")
				store := formStr(r, "store")
				destination := formStr(r, "destination")
				if destination == "" {
					return nil, nil, errors.New("destination required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeRouting, lpPkg.KindAlertRouting, map[string]any{
					"severity":    severity,
					"alert_type":  alertType,
					"store":       store,
					"destination": destination,
				})
				return p, nil, err
			},
		},

		// 7. Store config — Drawer thresholds (N.4.1)
		{
			Path:        "/settings/store/drawer",
			ActivePage:  "settings",
			TmplName:    "settings_store_drawer",
			PatternType: lpPkg.PatternTypeThreshold,
			Kind:        lpPkg.KindDrawer,
			ListKey:     "Thresholds",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":            row.ID.String(),
					"Store":         patStr(p, "store"),
					"Threshold":     patStr(p, "threshold"),
					"EffectiveDate": row.CreatedAt.Format("2006-01-02"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				store := formStr(r, "store")
				threshold := formStr(r, "threshold")
				if store == "" || threshold == "" {
					return nil, nil, errors.New("store and threshold required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeThreshold, lpPkg.KindDrawer, map[string]any{
					"store":     store,
					"threshold": threshold,
				})
				return p, nil, err
			},
		},

		// 8. Store config — Discount caps (N.4.2)
		{
			Path:        "/settings/store/discounts",
			ActivePage:  "settings",
			TmplName:    "settings_store_discounts",
			PatternType: lpPkg.PatternTypeThreshold,
			Kind:        lpPkg.KindDiscountCap,
			ListKey:     "Caps",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":            row.ID.String(),
					"ReasonCode":    patStr(p, "reason_code"),
					"Cap":           patStr(p, "cap_pct"),
					"Store":         patStr(p, "store"),
					"EffectiveDate": row.CreatedAt.Format("2006-01-02"),
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				cap := formStr(r, "cap_pct")
				store := formStr(r, "store")
				if code == "" || cap == "" {
					return nil, nil, errors.New("reason_code and cap_pct required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeThreshold, lpPkg.KindDiscountCap, map[string]any{
					"reason_code": code,
					"cap_pct":     cap,
					"store":       store,
				})
				return p, nil, err
			},
		},

		// 9. Store config — Void reason codes (N.4.3)
		{
			Path:        "/settings/store/void-reasons",
			ActivePage:  "settings",
			TmplName:    "settings_store_void_reasons",
			PatternType: lpPkg.PatternTypeVocab,
			Kind:        lpPkg.KindVoidReason,
			ListKey:     "Codes",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":          row.ID.String(),
					"ReasonCode":  patStr(p, "reason_code"),
					"Description": patStr(p, "description"),
					"Active":      true,
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				desc := formStr(r, "description")
				if code == "" {
					return nil, nil, errors.New("reason_code required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeVocab, lpPkg.KindVoidReason, map[string]any{
					"reason_code": code,
					"description": desc,
				})
				return p, nil, err
			},
		},

		// 10. Store config — Comp reason codes (N.4.4)
		{
			Path:        "/settings/store/comp-reasons",
			ActivePage:  "settings",
			TmplName:    "settings_store_comp_reasons",
			PatternType: lpPkg.PatternTypeVocab,
			Kind:        lpPkg.KindCompReason,
			ListKey:     "Codes",
			RowToView: func(row lpPkg.AllowListRow) map[string]any {
				p, _ := lpPkg.DecodePattern(row.Pattern)
				return map[string]any{
					"ID":          row.ID.String(),
					"ReasonCode":  patStr(p, "reason_code"),
					"Description": patStr(p, "description"),
					"Active":      true,
				}
			},
			FormToPattern: func(r *http.Request) (json.RawMessage, *string, error) {
				code := formStr(r, "reason_code")
				desc := formStr(r, "description")
				if code == "" {
					return nil, nil, errors.New("reason_code required")
				}
				p, err := lpPkg.NewPattern(lpPkg.PatternTypeVocab, lpPkg.KindCompReason, map[string]any{
					"reason_code": code,
					"description": desc,
				})
				return p, nil, err
			},
		},
	}
}
