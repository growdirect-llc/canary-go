package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
	"github.com/ruptiv/canary/internal/item"
)

func (h *Handler) itemScanPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "items_scan", "items", map[string]any{
		"Flash": r.URL.Query().Get("flash"),
		"Form": map[string]string{
			"barcode": r.URL.Query().Get("barcode"),
		},
	})
}

func (h *Handler) itemScanLookupAction(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		h.scanLookupFailed(w, r, http.StatusServiceUnavailable, "no_store", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.scanLookupFailed(w, r, http.StatusBadRequest, "invalid_form", "")
		return
	}
	barcode := normalizeScanBarcode(r.PostFormValue("barcode"))
	if barcode == "" {
		h.scanLookupFailed(w, r, http.StatusBadRequest, "missing_barcode", "")
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	existing, err := h.deps.ItemStore.GetByBarcode(ctx, tenantID, barcode)
	if err == nil && existing != nil {
		h.renderDuplicateScan(w, r, barcode, existing)
		return
	}
	if err != nil && !errors.Is(err, item.ErrNotFound) {
		h.logger.Error("itemScanLookupAction: GetByBarcode", zap.Error(err))
		h.scanLookupFailed(w, r, http.StatusBadGateway, "lookup_failed", barcode)
		return
	}
	if h.deps.BarcodeLookup == nil {
		h.scanLookupFailed(w, r, http.StatusServiceUnavailable, "lookup_failed", barcode)
		return
	}

	result, err := h.deps.BarcodeLookup.Lookup(ctx, barcode)
	if errors.Is(err, barcodelookup.ErrBarcodeNotFound) {
		q := url.Values{}
		q.Set("barcode", barcode)
		q.Set("flash", "barcode_not_found")
		manualURL := "/items/new?" + q.Encode()
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusNotFound, map[string]string{
				"status":     "not_found",
				"manual_url": manualURL,
			})
			return
		}
		http.Redirect(w, r, manualURL, http.StatusSeeOther)
		return
	}
	if err != nil {
		h.scanLookupFailed(w, r, http.StatusBadGateway, "lookup_failed", barcode)
		return
	}

	state := scanFlowState{
		Barcode:       barcode,
		Source:        result.Source,
		Confidence:    result.Confidence,
		PartialFields: result.PartialFields,
		Product:       productFieldsFromLookup(result.Fields),
		Operational: scanOperationalFields{
			SKU:           barcode,
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}
	token, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantID, state)
	if err != nil {
		h.logger.Error("itemScanLookupAction: encode flow", zap.Error(err))
		h.scanLookupFailed(w, r, http.StatusInternalServerError, "lookup_failed", barcode)
		return
	}
	reviewURL := "/items/scan/review?flow=" + url.QueryEscape(token)
	if wantsScanJSON(r) {
		writeScanJSON(w, http.StatusOK, map[string]string{
			"status":     "found",
			"review_url": reviewURL,
		})
		return
	}
	http.Redirect(w, r, reviewURL, http.StatusSeeOther)
}

func (h *Handler) itemScanReviewPage(w http.ResponseWriter, r *http.Request) {
	state, token, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	h.render(w, r, "items_scan_review", "items", map[string]any{
		"Flow":            token,
		"State":           state,
		"ConfidenceLabel": confidenceLabel(state.Confidence),
	})
}

func (h *Handler) itemScanOperationalAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/items/scan?flash=invalid_form", http.StatusSeeOther)
		return
	}
	state, _, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	mergeScanProductForm(&state, r)
	mergeScanOperationalForm(&state, r)

	if r.PostFormValue("intent") != "preview" {
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, ""))
		return
	}

	if missing := firstMissingScanOperational(state); missing != "" {
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "missing_"+missing))
		return
	}

	token, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantIDFromCtx(r.Context()), state)
	if err != nil {
		h.logger.Error("itemScanOperationalAction: encode", zap.Error(err))
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "scan_expired"))
		return
	}
	h.render(w, r, "items_scan_confirm", "items", map[string]any{
		"Flow":            token,
		"State":           state,
		"ConfidenceLabel": confidenceLabel(state.Confidence),
	})
}

func (h *Handler) itemScanConfirmAction(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		http.Redirect(w, r, "/items/scan?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/items/scan?flash=invalid_form", http.StatusSeeOther)
		return
	}
	state, token, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("intent") != "create" {
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":            token,
			"State":           state,
			"ConfidenceLabel": confidenceLabel(state.Confidence),
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	existing, err := h.deps.ItemStore.GetByBarcode(ctx, tenantID, state.Barcode)
	if err == nil && existing != nil {
		h.renderDuplicateScan(w, r, state.Barcode, existing)
		return
	}
	if err != nil && !errors.Is(err, item.ErrNotFound) {
		h.logger.Error("itemScanConfirmAction: final GetByBarcode", zap.Error(err))
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":            token,
			"State":           state,
			"ConfidenceLabel": confidenceLabel(state.Confidence),
			"Flash":           "create_failed",
		})
		return
	}

	req, err := createRequestFromScanState(tenantID, state)
	if err != nil {
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "validation_failed"))
		return
	}
	created, err := h.deps.ItemStore.Create(ctx, req)
	if err != nil {
		if errors.Is(err, item.ErrConflict) {
			h.renderDuplicateScan(w, r, state.Barcode, nil)
			return
		}
		h.logger.Error("itemScanConfirmAction: Create", zap.Error(err))
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":            token,
			"State":           state,
			"ConfidenceLabel": confidenceLabel(state.Confidence),
			"Flash":           "create_failed",
		})
		return
	}
	http.Redirect(w, r, "/items/"+created.ID.String()+"?flash=created", http.StatusSeeOther)
}

func (h *Handler) decodeScanFlow(w http.ResponseWriter, r *http.Request) (scanFlowState, string, bool) {
	token := r.URL.Query().Get("flow")
	if token == "" {
		token = r.PostFormValue("flow")
	}
	state, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Decode(token, tenantIDFromCtx(r.Context()))
	if err != nil {
		http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
		return scanFlowState{}, "", false
	}
	return state, token, true
}

func (h *Handler) scanLookupFailed(w http.ResponseWriter, r *http.Request, status int, flash, barcode string) {
	if wantsScanJSON(r) {
		writeScanJSON(w, status, map[string]string{"status": flash})
		return
	}
	if barcode == "" {
		http.Redirect(w, r, "/items/scan?flash="+url.QueryEscape(flash), http.StatusSeeOther)
		return
	}
	h.render(w, r, "items_scan", "items", map[string]any{
		"Flash": flash,
		"Form":  map[string]string{"barcode": barcode},
	})
}

func (h *Handler) renderDuplicateScan(w http.ResponseWriter, r *http.Request, barcode string, existing *item.Item) {
	if wantsScanJSON(r) {
		body := map[string]string{"status": "duplicate"}
		if existing != nil {
			body["item_url"] = "/items/" + existing.ID.String()
		}
		writeScanJSON(w, http.StatusOK, body)
		return
	}
	duplicate := map[string]string{}
	if existing != nil {
		duplicate = map[string]string{
			"ID":          existing.ID.String(),
			"SKU":         existing.SKU,
			"Description": existing.Description,
		}
	}
	h.render(w, r, "items_scan", "items", map[string]any{
		"Flash":     "duplicate_barcode",
		"Form":      map[string]string{"barcode": barcode},
		"Duplicate": duplicate,
	})
}

func (h *Handler) scanOperationalView(r *http.Request, state scanFlowState, flash string) map[string]any {
	token, _ := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantIDFromCtx(r.Context()), state)
	view := map[string]any{
		"Flash":           flash,
		"Flow":            token,
		"State":           state,
		"ConfidenceLabel": confidenceLabel(state.Confidence),
		"Categories":      []map[string]any{},
		"Vendors":         []map[string]any{},
	}
	if h.deps.ItemStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if cats, err := h.deps.ItemStore.ListCategories(ctx, tenantID); err == nil {
			view["Categories"] = categoryDropdownView(cats)
		}
		if vendors, err := h.deps.ItemStore.ListVendors(ctx, tenantID); err == nil {
			view["Vendors"] = vendorDropdownView(vendors)
		}
	}
	return view
}

func normalizeScanBarcode(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\r\n\t")
	if len(v) > 64 {
		return ""
	}
	return v
}

func productFieldsFromLookup(fields map[string]any) scanProductFields {
	return scanProductFields{
		Name:               stringField(fields, "name"),
		Brand:              stringField(fields, "brand"),
		Size:               stringField(fields, "size"),
		ImageURL:           stringField(fields, "image_url"),
		CategorySuggestion: stringField(fields, "category"),
	}
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	if v, ok := fields[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func mergeScanProductForm(state *scanFlowState, r *http.Request) {
	state.Product = scanProductFields{
		Name:               strings.TrimSpace(r.PostFormValue("name")),
		Brand:              strings.TrimSpace(r.PostFormValue("brand")),
		Size:               strings.TrimSpace(r.PostFormValue("size")),
		ImageURL:           strings.TrimSpace(r.PostFormValue("image_url")),
		CategorySuggestion: strings.TrimSpace(r.PostFormValue("category_suggestion")),
	}
}

func mergeScanOperationalForm(state *scanFlowState, r *http.Request) {
	state.Operational = scanOperationalFields{
		SKU:           scanPostFormValue(r, "sku", state.Operational.SKU),
		CategoryID:    strings.TrimSpace(r.PostFormValue("category_id")),
		VendorID:      strings.TrimSpace(r.PostFormValue("vendor_id")),
		UnitOfMeasure: defaultIfEmpty(scanPostFormValue(r, "unit_of_measure", state.Operational.UnitOfMeasure), "EA"),
		UnitCost:      strings.TrimSpace(r.PostFormValue("unit_cost")),
		SellingPrice:  strings.TrimSpace(r.PostFormValue("selling_price")),
		CasePack:      strings.TrimSpace(r.PostFormValue("case_pack")),
		Status:        defaultIfEmpty(scanPostFormValue(r, "status", state.Operational.Status), "active"),
	}
}

func scanPostFormValue(r *http.Request, key, fallback string) string {
	if _, ok := r.PostForm[key]; !ok {
		return fallback
	}
	return strings.TrimSpace(r.PostFormValue(key))
}

func firstMissingScanOperational(state scanFlowState) string {
	switch {
	case state.Product.Name == "":
		return "name"
	case state.Operational.SKU == "":
		return "sku"
	case state.Operational.UnitCost == "":
		return "unit_cost"
	case state.Operational.SellingPrice == "":
		return "selling_price"
	default:
		return ""
	}
}

func createRequestFromScanState(tenantID uuid.UUID, state scanFlowState) (item.CreateRequest, error) {
	if firstMissingScanOperational(state) != "" {
		return item.CreateRequest{}, item.ErrValidation
	}
	casePackQty, err := parseScanCasePack(state.Operational.CasePack)
	if err != nil {
		return item.CreateRequest{}, err
	}
	barcodeType := inferScanBarcodeType(state.Barcode)
	attrs, err := json.Marshal(map[string]any{
		"scan_lookup": map[string]any{
			"source":              state.Source,
			"confidence":          state.Confidence,
			"partial_fields":      state.PartialFields,
			"brand":               state.Product.Brand,
			"size":                state.Product.Size,
			"image_url":           state.Product.ImageURL,
			"category_suggestion": state.Product.CategorySuggestion,
		},
		"scan_operational": map[string]any{
			"barcode_type": barcodeType,
			"vendor_id":    state.Operational.VendorID,
			"case_pack":    state.Operational.CasePack,
		},
	})
	if err != nil {
		return item.CreateRequest{}, err
	}

	req := item.CreateRequest{
		TenantID:    tenantID,
		SKU:         state.Operational.SKU,
		Description: state.Product.Name,
		Attributes:  attrs,
		Barcodes: []item.BarcodeRequest{
			{Value: state.Barcode, Type: &barcodeType, IsPrimary: boolPtr(true)},
		},
	}
	if state.Operational.CategoryID != "" {
		if id, err := uuid.Parse(state.Operational.CategoryID); err == nil {
			req.CategoryID = &id
		}
	}
	if state.Operational.UnitOfMeasure != "" {
		req.UnitOfMeasure = &state.Operational.UnitOfMeasure
	}
	if state.Operational.UnitCost != "" {
		req.DefaultCost = &state.Operational.UnitCost
	}
	if state.Operational.SellingPrice != "" {
		req.DefaultPrice = &state.Operational.SellingPrice
	}
	if state.Operational.Status != "" {
		req.Status = &state.Operational.Status
	}
	if state.Operational.VendorID != "" {
		vendorID, err := uuid.Parse(state.Operational.VendorID)
		if err != nil {
			return item.CreateRequest{}, item.ErrValidation
		}
		req.VendorLinks = []item.VendorLinkRequest{{
			VendorID:    vendorID,
			UnitCost:    req.DefaultCost,
			CasePackQty: casePackQty,
			IsPrimary:   boolPtr(true),
		}}
	}
	return req, nil
}

func parseScanCasePack(v string) (*int32, error) {
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 1 {
		return nil, item.ErrValidation
	}
	out := int32(n)
	return &out, nil
}

func inferScanBarcodeType(barcode string) string {
	if barcode == "" {
		return "GTIN"
	}
	for _, r := range barcode {
		if r < '0' || r > '9' {
			return "INTERNAL"
		}
	}
	switch len(barcode) {
	case 4, 5:
		return "PLU"
	case 12:
		return "UPC_A"
	case 13:
		return "EAN_13"
	case 14:
		return "GTIN"
	default:
		return "GTIN"
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func wantsScanJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeScanJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func confidenceLabel(confidence float64) string {
	if confidence <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", confidence*100)
}
