# GRO-768 Template Audit — Python → Canary Go

**Date:** 2026-05-04  
**Scope:** `Canary/templates/` (39 files) → `internal/web/templates/` (24 ported, 13 skipped)

---

## CSS / Theme Decision

The Python prototype ships a full white-label CSS infrastructure. Ported wholesale — **not** replaced with the devops console CSS.

| File | Lines | Role |
|---|---|---|
| `canary.css` | 2,031 | Component library — 100% `var()`-based, zero hard-coded colors |
| `themes/canary-dark.css` | 137 | Default Canary brand tokens (dark) |
| `themes/canary-light.css` | 137 | Default Canary brand tokens (light) |
| `themes/whitelabel-dark.css` | 137 | Neutral palette, no Canary yellow — for embedded deployments |
| `themes/whitelabel-light.css` | 137 | Neutral palette, light variant |
| `themes/ops-dark.css` | 34 | Ops console variant |
| `gesture.css` | — | Mobile gesture support |
| `mobile-panels.css` | — | Mobile panel overlays |
| `responsive-shell.css` | — | Responsive layout shell |

**White-label mechanism:** `base.html` loads `themes/{{.Theme}}.css` before `canary.css`. Swapping `.Theme` in `PageData` (e.g., from `"canary-dark"` to `"whitelabel-dark"`) recoats the entire UI. Theme is resolved per-tenant once the config module is wired.

---

## Template Audit

### Ported (24 files)

| Python template | Go template | Go module | Notes |
|---|---|---|---|
| `base.html` | `templates/base.html` | `internal/web` | Theme loader + mobile header + sidebar slot |
| `partials/sidebar.html` | `templates/partials/sidebar.html` | `internal/web` | `{{define "sidebar"}}` — active page via `.Page` |
| `app/home.html` | `templates/dashboard.html` | dashboard | Chart.js calls preserved; data stubs until owl/analytics wired |
| `app/chirps.html` | `templates/chirps.html` | `chirp` | List view; data stub |
| `app/transactions.html` | `templates/transactions.html` | `transaction` | Table view; data stub |
| `app/alert_detail.html` → generalized | `templates/alerts.html` | `alert` | List + detail link; data stub |
| `app/case_detail.html` | `templates/cases.html` | `casemgmt` | List entry point; hawk/ for detail |
| `app/hawk/case_list.html` | `templates/hawk/case_list.html` | `casemgmt` | Status filter strip; data stub |
| `app/hawk/case_detail.html` | `templates/hawk/case_detail.html` | `casemgmt` | Timeline + evidence sidebar; data stub |
| `app/hawk/wizard_start.html` | `templates/hawk/wizard_start.html` | `casemgmt` | POST form to `/cases/hawk` |
| `app/team.html` | `templates/employees.html` | `employee` | Risk score column; data stub |
| `app/reports.html` | `templates/reports.html` | `report` | List view; data stub |
| `app/settings.html` | `templates/settings.html` | `config`/`tenant` | Merchant + detection config; data stub |
| `app/owl.html` | `templates/owl.html` | `owl` | Search form + results; data stub |
| `app/rule_editor.html` | `templates/rules.html` | `chirp/rules` | Rule list; no inline edit yet |
| `app/connect.html` | `templates/connect.html` | `auth` | Week-start + lookback pills; POST to `/connect` |
| `app/welcome.html` | `templates/welcome.html` | `auth` | Simplified; full config wired later |
| `auth/join.html` | `templates/auth/join.html` | `auth` | Standalone page (no base template); hero + phone mockup preserved |
| `errors/403.html` | `templates/errors/403.html` | `internal/web` | — |
| `errors/404.html` | `templates/errors/404.html` | `internal/web` | — |
| `errors/500.html` | `templates/errors/500.html` | `internal/web` | — |
| `partials/_receipt_popup.html` | _(inline in txn detail)_ | `transaction` | Not yet needed; wire with txn detail page |

### Skipped (13 files)

| Python template | Reason |
|---|---|
| `admin/audit_log.html` | Python-only admin scaffolding — no Go admin module |
| `admin/config.html` | Same |
| `admin/config_health.html` | Same |
| `admin/user_form.html` | Same |
| `admin/users.html` | Same |
| `ops/atlas.html` | Replaced by Go devops console (`internal/devops/`) |
| `ops/atlas_figure.html` | Same |
| `ops/base_ops.html` | Same |
| `ops/method.html` | Same |
| `ops/qa.html` | Same |
| `ops/test_lab.html` | Same |
| `ops/_context_snapshot.html` | Same |
| `marketing/splash.html` | Landing page — lives outside the app shell |
| `pipeline_trace.html` | Python TSP debug view — no Go equivalent needed |

---

## Go Template Pattern

Each app page uses a per-page template set to avoid named-block conflicts:

```go
template.ParseFS(embedFS,
    "templates/base.html",
    "templates/partials/sidebar.html",
    "templates/<page>.html",
)
```

`base.html` is executed directly (`ExecuteTemplate(w, "base.html", data)`). Each page file defines `{{define "content"}}` and optionally `{{define "title"}}` and `{{define "scripts"}}`.

The join page is standalone — parsed and executed directly without base.html.

---

## Stub Status

All app pages render correct shell (sidebar, CSS, layout) with empty/stub data. Data wiring follows as each module's store is plumbed into the web handler (GRO-769 and beyond).
