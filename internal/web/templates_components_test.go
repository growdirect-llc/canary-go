package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// loadComponentSet loads all component templates into one set so individual
// components can be rendered in isolation. Mirrors the FuncMap that the
// production loader will register so tests exercise the same surface.
func loadComponentSet(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("components").Funcs(componentFuncs).ParseFS(embedFS, "templates/components/*.html")
	if err != nil {
		t.Fatalf("load components: %v", err)
	}
	return tmpl
}

func renderComponent(t *testing.T, name string, params map[string]any) string {
	t.Helper()
	tmpl := loadComponentSet(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, params); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

func TestFormField_RendersBasicInput(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
	})
	assertContains(t, out, `name="email"`)
	assertContains(t, out, "Email")
	assertContains(t, out, "<input")
}

func TestFormField_DefaultTypeIsText(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "first",
		"label": "First name",
	})
	assertContains(t, out, `type="text"`)
}

func TestFormField_RespectsTypeParam(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
		"type":  "email",
	})
	assertContains(t, out, `type="email"`)
}

func TestFormField_PrePopulatesValue(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
		"value": "user@example.com",
	})
	assertContains(t, out, `value="user@example.com"`)
}

func TestFormField_RendersHelpText(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
		"help":  "We'll never share your address.",
	})
	assertContains(t, out, "We&#39;ll never share your address.")
}

func TestFormField_OmitsHelpWhenAbsent(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
	})
	if strings.Contains(out, "form-help") {
		t.Errorf("expected no form-help element when help param absent\ngot:\n%s", out)
	}
}

func TestFormField_RendersError(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
		"error": "Email is required",
	})
	assertContains(t, out, "Email is required")
	assertContains(t, out, "form-error")
}

func TestFormField_OmitsErrorWhenAbsent(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "email",
		"label": "Email",
	})
	if strings.Contains(out, "form-error") {
		t.Errorf("expected no form-error element when error param absent\ngot:\n%s", out)
	}
}

func TestFormField_RequiredAddsAttribute(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":     "email",
		"label":    "Email",
		"required": true,
	})
	assertContains(t, out, "required")
}

func TestFormField_TextareaVariant(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "notes",
		"label": "Notes",
		"type":  "textarea",
	})
	assertContains(t, out, "<textarea")
	assertContains(t, out, `name="notes"`)
	if strings.Contains(out, "<input") {
		t.Errorf("textarea variant should not render <input>\ngot:\n%s", out)
	}
}

// ─── data-table ───────────────────────────────────────────────────────────

func TestDataTable_RendersHeaders(t *testing.T) {
	out := renderComponent(t, "components/data-table", map[string]any{
		"columns": []string{"SKU", "Name", "Stock"},
		"rows":    []map[string]any{},
	})
	assertContains(t, out, "<th")
	assertContains(t, out, "SKU")
	assertContains(t, out, "Name")
	assertContains(t, out, "Stock")
}

func TestDataTable_RendersRows(t *testing.T) {
	out := renderComponent(t, "components/data-table", map[string]any{
		"columns": []string{"SKU", "Name"},
		"rows": []map[string]any{
			{"cells": []any{"ABC-1", "Widget"}},
			{"cells": []any{"XYZ-2", "Gadget"}},
		},
	})
	assertContains(t, out, "ABC-1")
	assertContains(t, out, "Widget")
	assertContains(t, out, "XYZ-2")
	assertContains(t, out, "Gadget")
}

func TestDataTable_EmptyStateWhenNoRows(t *testing.T) {
	out := renderComponent(t, "components/data-table", map[string]any{
		"columns": []string{"SKU", "Name"},
		"rows":    []map[string]any{},
		"empty":   "No items yet — add your first.",
	})
	assertContains(t, out, "No items yet")
	assertContains(t, out, "data-table-empty")
}

func TestDataTable_DefaultEmptyState(t *testing.T) {
	out := renderComponent(t, "components/data-table", map[string]any{
		"columns": []string{"SKU"},
		"rows":    []map[string]any{},
	})
	assertContains(t, out, "No data")
}

func TestDataTable_RowsCanCarryHrefForLinks(t *testing.T) {
	out := renderComponent(t, "components/data-table", map[string]any{
		"columns": []string{"Name"},
		"rows": []map[string]any{
			{"cells": []any{"Widget"}, "href": "/items/abc"},
		},
	})
	assertContains(t, out, `href="/items/abc"`)
}

// ─── drawer ───────────────────────────────────────────────────────────────

func TestDrawer_RendersBodyAndTitle(t *testing.T) {
	out := renderComponent(t, "components/drawer", map[string]any{
		"id":    "test-drawer",
		"title": "Bulk fix",
		"body":  template.HTML("<p>Choose a fix class</p>"),
	})
	assertContains(t, out, "Bulk fix")
	assertContains(t, out, "Choose a fix class")
	assertContains(t, out, "drawer")
	assertContains(t, out, `id="test-drawer"`)
}

func TestDrawer_HasCloseButton(t *testing.T) {
	out := renderComponent(t, "components/drawer", map[string]any{
		"id":    "test-drawer",
		"title": "Settings",
		"body":  template.HTML("<p>x</p>"),
	})
	assertContains(t, out, "drawer-close")
	assertContains(t, out, `aria-label="Close"`)
}

func TestDrawer_RendersOptionalFooter(t *testing.T) {
	out := renderComponent(t, "components/drawer", map[string]any{
		"id":     "x",
		"title":  "Edit",
		"body":   template.HTML("<p>x</p>"),
		"footer": template.HTML("<button>Save</button>"),
	})
	assertContains(t, out, "drawer-footer")
	assertContains(t, out, "<button>Save</button>")
}

func TestDrawer_DefaultClosed(t *testing.T) {
	out := renderComponent(t, "components/drawer", map[string]any{
		"id":    "x",
		"title": "Edit",
		"body":  template.HTML("<p>x</p>"),
	})
	assertContains(t, out, `aria-hidden="true"`)
}

func TestDrawer_OpenWhenRequested(t *testing.T) {
	out := renderComponent(t, "components/drawer", map[string]any{
		"id":    "x",
		"title": "Edit",
		"body":  template.HTML("<p>x</p>"),
		"open":  true,
	})
	assertContains(t, out, `aria-hidden="false"`)
	assertContains(t, out, "drawer-open")
}

// ─── card ─────────────────────────────────────────────────────────────────

func TestCard_RendersBodyContent(t *testing.T) {
	out := renderComponent(t, "components/card", map[string]any{
		"body": template.HTML("<p>Hello card</p>"),
	})
	assertContains(t, out, "<p>Hello card</p>")
	assertContains(t, out, "card")
}

func TestCard_RendersOptionalTitle(t *testing.T) {
	out := renderComponent(t, "components/card", map[string]any{
		"title": "Today",
		"body":  template.HTML("<p>0</p>"),
	})
	assertContains(t, out, "Today")
	assertContains(t, out, "card-title")
}

func TestCard_OmitsTitleWhenAbsent(t *testing.T) {
	out := renderComponent(t, "components/card", map[string]any{
		"body": template.HTML("<p>0</p>"),
	})
	if strings.Contains(out, "card-title") {
		t.Errorf("expected no card-title element when title param absent\ngot:\n%s", out)
	}
}

func TestCard_RendersOptionalFooter(t *testing.T) {
	out := renderComponent(t, "components/card", map[string]any{
		"body":   template.HTML("<p>0</p>"),
		"footer": template.HTML("<a href=\"/x\">View</a>"),
	})
	assertContains(t, out, "card-footer")
	assertContains(t, out, `href="/x"`)
}

// ─── status-pill ──────────────────────────────────────────────────────────

func TestStatusPill_RendersLabel(t *testing.T) {
	out := renderComponent(t, "components/status-pill", map[string]any{
		"label": "Open",
	})
	assertContains(t, out, "Open")
	assertContains(t, out, "status-pill")
}

func TestStatusPill_AppliesToneClass(t *testing.T) {
	out := renderComponent(t, "components/status-pill", map[string]any{
		"label": "Done",
		"tone":  "success",
	})
	assertContains(t, out, "status-pill-success")
}

func TestStatusPill_DefaultToneIsNeutral(t *testing.T) {
	out := renderComponent(t, "components/status-pill", map[string]any{
		"label": "Pending",
	})
	assertContains(t, out, "status-pill-neutral")
}

func TestFormField_SelectVariantRendersOptions(t *testing.T) {
	out := renderComponent(t, "components/form-field", map[string]any{
		"name":  "tier",
		"label": "Tier",
		"type":  "select",
		"options": []map[string]any{
			{"value": "free", "label": "Free"},
			{"value": "pro", "label": "Pro"},
		},
	})
	assertContains(t, out, "<select")
	assertContains(t, out, `value="free"`)
	assertContains(t, out, ">Free<")
	assertContains(t, out, `value="pro"`)
	assertContains(t, out, ">Pro<")
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q\ngot:\n%s", needle, haystack)
	}
}
