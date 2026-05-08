// internal/web/handler_items_create.go
//
// Item-setup UI Flow C Screen 1 — minimal new-item form. GRO-886.
//
// Spec: Brain/wiki/cards/canary-item-setup-screen-decomp.md (Flow C / C1).
// Required fields only. Optional enrichment (C2 brand/image/allergens/etc.)
// is a separate dispatch.
//
// Routes wired in handler.go Mount() (inside the protected r.Group):
//
//   GET  /items/new   itemNewPage      — render the form
//   POST /items/new   itemCreateAction — validate + create + redirect
//
// Tenant scoping: tenant_id derives from tenantIDFromCtx() exclusively
// per T-B middleware. The form HTML never exposes a tenant_id input.
//
// CSRF: gorilla/csrf middleware (T-E) auto-validates the hidden
// {{ .CSRFField }} on POST. A POST without a valid token returns 403
// before this handler ever runs.

package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/item"
)

// itemNewPage renders the Flow C C1 form. Loads categories + vendors
// for the dropdowns. When the ItemStore is nil (test scaffolding /
// store-not-wired path), renders the form with empty dropdowns —
// useful for design-time review without a live database.
func (h *Handler) itemNewPage(w http.ResponseWriter, r *http.Request) {
	view := map[string]any{
		"Categories": []map[string]any{},
		"Vendors":    []map[string]any{},
		"Flash":      r.URL.Query().Get("flash"),
		"Form":       formStateFromQuery(r.URL.Query()),
	}

	if h.deps.ItemStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)

		if cats, err := h.deps.ItemStore.ListCategories(ctx, tenantID); err == nil {
			view["Categories"] = categoryDropdownView(cats)
		} else {
			h.logger.Warn("itemNewPage: ListCategories", zap.Error(err))
		}

		if vendors, err := h.deps.ItemStore.ListVendors(ctx, tenantID); err == nil {
			view["Vendors"] = vendorDropdownView(vendors)
		} else {
			h.logger.Warn("itemNewPage: ListVendors", zap.Error(err))
		}
	}

	h.render(w, r, "items_new", "items", view)
}

// itemCreateAction handles POST /items/new. Validates required fields
// server-side, builds a CreateRequest, calls ItemStore.Create, and
// redirects to the new item's detail page on success.
//
// On validation failure or store error, redirects back to the form
// with a `?flash=<code>` and the previously-entered values preserved
// in query params so the form re-renders with the operator's input
// intact. (Codebase idiom — matches suppliersCreate / poCreate.)
//
// Audit: catalog.items already lives under the audit middleware on
// the protected route group, so the audit row is written automatically
// by the middleware on the POST. Per-action `action='item.create.manual'`
// classification can land in a follow-on dispatch when the audit-tag
// helper exists.
func (h *Handler) itemCreateAction(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		http.Redirect(w, r, "/items/new?flash=no_store", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.logger.Warn("itemCreateAction: ParseForm", zap.Error(err))
		http.Redirect(w, r, "/items/new?flash=invalid_form", http.StatusSeeOther)
		return
	}

	form := parseItemCreateForm(r)

	if missing := form.firstMissingRequired(); missing != "" {
		h.logger.Debug("itemCreateAction: missing required field", zap.String("field", missing))
		http.Redirect(w, r, form.redirectTo("missing_"+missing), http.StatusSeeOther)
		return
	}

	tenantID := tenantIDFromCtx(r.Context())

	req := item.CreateRequest{
		TenantID:    tenantID,
		SKU:         form.SKU,
		Description: form.Description,
	}
	if form.CategoryID != "" {
		if id, err := uuid.Parse(form.CategoryID); err == nil {
			req.CategoryID = &id
		}
	}
	if form.UnitOfMeasure != "" {
		req.UnitOfMeasure = &form.UnitOfMeasure
	}
	if form.UnitCost != "" {
		req.DefaultCost = &form.UnitCost
	}
	if form.SellingPrice != "" {
		req.DefaultPrice = &form.SellingPrice
	}
	if form.Status != "" {
		req.Status = &form.Status
	}
	if form.Barcode != "" {
		req.Barcodes = []item.BarcodeRequest{
			{Value: form.Barcode},
		}
	}

	created, err := h.deps.ItemStore.Create(r.Context(), req)
	switch {
	case err == nil:
		// Happy path.
	case errors.Is(err, item.ErrConflict):
		// Duplicate SKU or barcode in tenant. Surface via flash with
		// the SKU so the form's flash-handler can render a "[Open
		// existing]" affordance pointing the operator at the existing
		// row. Detail-page lookup is by SKU in a follow-on; today the
		// operator gets the flash and goes back to the list.
		http.Redirect(w, r, form.redirectTo("duplicate_sku"), http.StatusSeeOther)
		return
	case errors.Is(err, item.ErrValidation):
		http.Redirect(w, r, form.redirectTo("validation_failed"), http.StatusSeeOther)
		return
	default:
		h.logger.Error("itemCreateAction: Create", zap.Error(err))
		http.Redirect(w, r, form.redirectTo("create_failed"), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/items/"+created.ID.String()+"?flash=created", http.StatusSeeOther)
}

// itemCreateForm is the parsed form input. Held as a struct so
// validation + redirect-with-state can share a single shape.
type itemCreateForm struct {
	SKU           string
	Description   string
	CategoryID    string
	Barcode       string
	VendorID      string // captured but not yet wired to a vendor-link insert in this slice
	UnitOfMeasure string
	UnitCost      string
	SellingPrice  string
	CasePack      string // captured but not yet wired (case_pack rides via item_packs in a follow-on)
	Status        string
}

func parseItemCreateForm(r *http.Request) itemCreateForm {
	return itemCreateForm{
		SKU:           r.PostFormValue("sku"),
		Description:   r.PostFormValue("description"),
		CategoryID:    r.PostFormValue("category_id"),
		Barcode:       r.PostFormValue("barcode"),
		VendorID:      r.PostFormValue("vendor_id"),
		UnitOfMeasure: defaultIfEmpty(r.PostFormValue("unit_of_measure"), "EA"),
		UnitCost:      r.PostFormValue("unit_cost"),
		SellingPrice:  r.PostFormValue("selling_price"),
		CasePack:      r.PostFormValue("case_pack"),
		Status:        defaultIfEmpty(r.PostFormValue("status"), "hidden"),
	}
}

// firstMissingRequired returns the field name of the first required
// field that is empty, or "" if all required fields are present.
// Order matches the form's field order so the operator sees the
// top-most miss first.
func (f itemCreateForm) firstMissingRequired() string {
	switch {
	case f.SKU == "":
		return "sku"
	case f.Description == "":
		return "description"
	case f.UnitCost == "":
		return "unit_cost"
	case f.SellingPrice == "":
		return "selling_price"
	}
	return ""
}

// redirectTo builds the form-flash redirect URL with the previously-
// entered values as query params so the form re-renders with the
// operator's input intact. (Without this, a single missing field
// makes the operator re-type everything — bad UX.)
func (f itemCreateForm) redirectTo(flash string) string {
	q := url.Values{}
	q.Set("flash", flash)
	q.Set("sku", f.SKU)
	q.Set("description", f.Description)
	q.Set("category_id", f.CategoryID)
	q.Set("barcode", f.Barcode)
	q.Set("vendor_id", f.VendorID)
	q.Set("unit_of_measure", f.UnitOfMeasure)
	q.Set("unit_cost", f.UnitCost)
	q.Set("selling_price", f.SellingPrice)
	q.Set("case_pack", f.CasePack)
	q.Set("status", f.Status)
	return "/items/new?" + q.Encode()
}

// formStateFromQuery rehydrates a form's previously-entered values
// from the query string for the GET-after-flash render path.
func formStateFromQuery(q url.Values) map[string]string {
	return map[string]string{
		"sku":             q.Get("sku"),
		"description":     q.Get("description"),
		"category_id":     q.Get("category_id"),
		"barcode":         q.Get("barcode"),
		"vendor_id":       q.Get("vendor_id"),
		"unit_of_measure": q.Get("unit_of_measure"),
		"unit_cost":       q.Get("unit_cost"),
		"selling_price":   q.Get("selling_price"),
		"case_pack":       q.Get("case_pack"),
		"status":          q.Get("status"),
	}
}

// categoryDropdownView shapes a Category list for the form's
// <select> element. Only active categories are exposed.
func categoryDropdownView(cats []item.Category) []map[string]any {
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		if c.Status != "active" {
			continue
		}
		out = append(out, map[string]any{
			"ID":   c.ID.String(),
			"Code": c.Code,
			"Name": c.Name,
		})
	}
	return out
}

// vendorDropdownView shapes a Vendor list for the form's <select>.
// Only active vendors are exposed.
func vendorDropdownView(vendors []item.Vendor) []map[string]any {
	out := make([]map[string]any, 0, len(vendors))
	for _, v := range vendors {
		if v.Status != "active" {
			continue
		}
		out = append(out, map[string]any{
			"ID":   v.ID.String(),
			"Code": v.VendorCode,
			"Name": v.Name,
		})
	}
	return out
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
