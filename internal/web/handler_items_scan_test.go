package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
	"github.com/ruptiv/canary/internal/item"
)

type scanItemStoreStub struct {
	getByBarcodeFn   func(ctx context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error)
	createFn         func(ctx context.Context, req item.CreateRequest) (*item.Item, error)
	listCategoriesFn func(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error)
	listVendorsFn    func(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error)
}

func (s *scanItemStoreStub) GetByID(context.Context, uuid.UUID, uuid.UUID) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) GetBySKU(context.Context, uuid.UUID, string) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) GetByBarcode(ctx context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error) {
	if s.getByBarcodeFn != nil {
		return s.getByBarcodeFn(ctx, tenantID, barcode)
	}
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) Create(ctx context.Context, req item.CreateRequest) (*item.Item, error) {
	if s.createFn != nil {
		return s.createFn(ctx, req)
	}
	return &item.Item{ID: uuid.New(), TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
}
func (s *scanItemStoreStub) Update(context.Context, uuid.UUID, uuid.UUID, item.PatchRequest) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *scanItemStoreStub) List(context.Context, item.ListFilters) ([]item.Item, error) {
	return nil, nil
}
func (s *scanItemStoreStub) ListCategories(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error) {
	if s.listCategoriesFn != nil {
		return s.listCategoriesFn(ctx, tenantID)
	}
	return nil, nil
}
func (s *scanItemStoreStub) AggregateByCategory(context.Context, uuid.UUID) ([]item.CategoryAggregate, error) {
	return nil, nil
}
func (s *scanItemStoreStub) ListVendors(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error) {
	if s.listVendorsFn != nil {
		return s.listVendorsFn(ctx, tenantID)
	}
	return nil, nil
}

var _ item.Store = (*scanItemStoreStub)(nil)

type scanLookupStub struct {
	called bool
	result barcodelookup.Result
	err    error
}

func (s *scanLookupStub) Lookup(context.Context, string) (barcodelookup.Result, error) {
	s.called = true
	return s.result, s.err
}

func newItemScanRouter(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	if len(deps.ScanFlowSecret) == 0 {
		deps.ScanFlowSecret = []byte("01234567890123456789012345678901")
	}
	deps = withTestAuth(deps)
	h := New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func postScanForm(r http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func fixedScanState() scanFlowState {
	return scanFlowState{
		Barcode:    "012345678905",
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Product: scanProductFields{
			Name:  "Organic Whole Milk",
			Brand: "Clover",
		},
		Operational: scanOperationalFields{
			SKU:           "012345678905",
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}
}

func fixedScanToken(t *testing.T, state scanFlowState) string {
	t.Helper()
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	token, err := codec.Encode(testTenant, state)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return token
}

func TestItemScan_RendersScanForm_NoTenantField(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Scan item", `action="/items/scan/lookup"`, `name="barcode"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "tenant_id") {
		t.Error("scan form must not expose tenant_id")
	}
}

func TestItemList_HasScanButton(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/items/scan"`) {
		t.Error("items list missing scan link")
	}
	if !strings.Contains(body, "Scan") {
		t.Error("items list missing Scan button text")
	}
}

func TestItemScanLookup_DuplicateShortCircuitsExternalLookup(t *testing.T) {
	existingID := uuid.New()
	lookup := &scanLookupStub{}
	store := &scanItemStoreStub{
		getByBarcodeFn: func(_ context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error) {
			if tenantID != testTenant {
				t.Errorf("tenantID = %s, want %s", tenantID, testTenant)
			}
			if barcode != "012345678905" {
				t.Errorf("barcode = %q", barcode)
			}
			return &item.Item{ID: existingID, SKU: "MILK-1", Description: "Milk"}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {" 012345678905 "}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if lookup.called {
		t.Fatal("external lookup must not run when local duplicate exists")
	}
	body := rec.Body.String()
	for _, want := range []string{"Barcode already exists", "/items/" + existingID.String(), "Create related item"} {
		if !strings.Contains(body, want) {
			t.Errorf("duplicate body missing %q", want)
		}
	}
}

func TestItemScanLookup_NotFoundRedirectsToManualForm(t *testing.T) {
	lookup := &scanLookupStub{err: barcodelookup.ErrBarcodeNotFound}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	for _, want := range []string{"/items/new?", "barcode=012345678905", "flash=barcode_not_found"} {
		if !strings.Contains(loc, want) {
			t.Errorf("redirect %q missing %q", loc, want)
		}
	}
}

func TestItemScanLookup_AllSourcesFailedRendersRetry(t *testing.T) {
	lookup := &scanLookupStub{err: barcodelookup.ErrAllSourcesFailed}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Lookup is unavailable", "Manual entry"} {
		if !strings.Contains(body, want) {
			t.Errorf("failure body missing %q", want)
		}
	}
}

func TestItemScanLookup_FoundRedirectsToReview(t *testing.T) {
	lookup := &scanLookupStub{result: barcodelookup.Result{
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Fields: map[string]any{
			"name":      "Organic Whole Milk",
			"brand":     "Clover",
			"image_url": "https://example.test/milk.png",
		},
		PartialFields: []string{"size"},
	}}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/items/scan/review?flow=") {
		t.Fatalf("Location = %q, want review flow", loc)
	}
}

func TestItemScanLookup_JSONFoundReturnsReviewURL(t *testing.T) {
	lookup := &scanLookupStub{result: barcodelookup.Result{
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Fields: map[string]any{
			"name":  "Organic Whole Milk",
			"brand": "Clover",
		},
	}}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	form := url.Values{"barcode": {"012345678905"}}
	req := httptest.NewRequest(http.MethodPost, "/items/scan/lookup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if body["status"] != "found" {
		t.Errorf("status = %q, want found", body["status"])
	}
	if !strings.HasPrefix(body["review_url"], "/items/scan/review?flow=") {
		t.Errorf("review_url = %q, want scan review URL", body["review_url"])
	}
}

func TestItemScanReview_RendersLookupFields(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	req := httptest.NewRequest(http.MethodGet, "/items/scan/review?flow="+url.QueryEscape(token), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Review item", "Organic Whole Milk", "Clover", "Open Food Facts", `name="name"`} {
		if !strings.Contains(body, want) {
			t.Errorf("review body missing %q", want)
		}
	}
	if strings.Contains(body, "tenant_id") {
		t.Error("review form must not expose tenant_id")
	}
}

func TestItemScanReview_TamperedFlowRedirectsToScan(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan/review?flow=tampered", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/items/scan?flash=scan_expired" {
		t.Fatalf("Location = %q, want scan_expired", loc)
	}
}

func TestItemScanOperational_RendersStoreFieldsFromReview(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	form := url.Values{
		"flow":   {token},
		"intent": {"edit_operational"},
		"name":   {"Organic Whole Milk"},
		"brand":  {"Clover"},
		"sku":    {"012345678905"},
	}
	rec := postScanForm(r, "/items/scan/operational", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Store fields", "Organic Whole Milk", `value="012345678905"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestItemScanOperational_ValidationPreservesInput(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	form := url.Values{
		"flow":            {token},
		"intent":          {"preview"},
		"name":            {"Organic Whole Milk"},
		"brand":           {"Clover"},
		"sku":             {""},
		"unit_of_measure": {"EA"},
	}
	rec := postScanForm(r, "/items/scan/operational", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Store fields", "SKU is required", "Organic Whole Milk"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestItemScanOperational_ValidRendersConfirm(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	form := validScanOperationalForm(token)
	rec := postScanForm(r, "/items/scan/operational", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Confirm item", "MILK-001", "Organic Whole Milk", `name="intent" value="create"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestItemScanConfirmCreate_CreatesItemBarcodeAndVendorLink(t *testing.T) {
	state := fixedScanState()
	vendorID := uuid.New()
	state.Product.CategorySuggestion = "Dairy"
	state.Operational = scanOperationalFields{
		SKU:           "MILK-001",
		VendorID:      vendorID.String(),
		UnitOfMeasure: "EA",
		UnitCost:      "2.49",
		SellingPrice:  "4.99",
		CasePack:      "12",
		Status:        "active",
	}
	token := fixedScanToken(t, state)
	createdID := uuid.New()
	var captured item.CreateRequest
	store := &scanItemStoreStub{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			captured = req
			return &item.Item{ID: createdID, TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store})

	rec := postScanForm(r, "/items/scan/confirm", url.Values{
		"flow":   {token},
		"intent": {"create"},
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/items/"+createdID.String()+"?flash=created" {
		t.Fatalf("Location = %q", loc)
	}
	if captured.TenantID != testTenant {
		t.Errorf("TenantID = %s, want %s", captured.TenantID, testTenant)
	}
	if captured.SKU != "MILK-001" || captured.Description != "Organic Whole Milk" {
		t.Errorf("captured item = %#v", captured)
	}
	if len(captured.Barcodes) != 1 || captured.Barcodes[0].Value != "012345678905" {
		t.Fatalf("Barcodes = %#v, want scanned barcode", captured.Barcodes)
	}
	if captured.Barcodes[0].Type == nil || *captured.Barcodes[0].Type != "UPC_A" {
		t.Fatalf("Barcode type = %#v, want UPC_A", captured.Barcodes[0].Type)
	}
	if len(captured.VendorLinks) != 1 {
		t.Fatalf("VendorLinks = %#v, want one primary vendor link", captured.VendorLinks)
	}
	vendorLink := captured.VendorLinks[0]
	if vendorLink.VendorID != vendorID {
		t.Errorf("VendorID = %s, want %s", vendorLink.VendorID, vendorID)
	}
	if vendorLink.UnitCost == nil || *vendorLink.UnitCost != "2.49" {
		t.Errorf("Vendor unit cost = %#v, want 2.49", vendorLink.UnitCost)
	}
	if vendorLink.CasePackQty == nil || *vendorLink.CasePackQty != 12 {
		t.Errorf("CasePackQty = %#v, want 12", vendorLink.CasePackQty)
	}
	if vendorLink.IsPrimary == nil || !*vendorLink.IsPrimary {
		t.Errorf("IsPrimary = %#v, want true", vendorLink.IsPrimary)
	}
	attrs := string(captured.Attributes)
	for _, want := range []string{"Open Food Facts", `"barcode_type":"UPC_A"`, vendorID.String(), `"case_pack":"12"`, "Dairy"} {
		if !strings.Contains(attrs, want) {
			t.Fatalf("Attributes = %s, want %q", attrs, want)
		}
	}
}

func TestItemScanConfirmCreate_InvalidCasePackDoesNotCreate(t *testing.T) {
	state := fixedScanState()
	state.Operational.SKU = "MILK-001"
	state.Operational.UnitCost = "2.49"
	state.Operational.SellingPrice = "4.99"
	state.Operational.CasePack = "0"
	token := fixedScanToken(t, state)
	storeCalled := false
	store := &scanItemStoreStub{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			storeCalled = true
			return nil, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store})

	rec := postScanForm(r, "/items/scan/confirm", url.Values{
		"flow":   {token},
		"intent": {"create"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if storeCalled {
		t.Fatal("Create must not be called for invalid case pack")
	}
	if !strings.Contains(rec.Body.String(), "Check the fields and try again") {
		t.Fatalf("body missing validation message: %s", rec.Body.String())
	}
}

func TestItemScanConfirmCreate_FinalDuplicateRace(t *testing.T) {
	state := fixedScanState()
	state.Operational.SKU = "MILK-001"
	state.Operational.UnitCost = "2.49"
	state.Operational.SellingPrice = "4.99"
	token := fixedScanToken(t, state)
	existingID := uuid.New()
	store := &scanItemStoreStub{
		getByBarcodeFn: func(_ context.Context, _ uuid.UUID, _ string) (*item.Item, error) {
			return &item.Item{ID: existingID, SKU: "EXISTING", Description: "Existing item"}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store})

	rec := postScanForm(r, "/items/scan/confirm", url.Values{
		"flow":   {token},
		"intent": {"create"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Barcode already exists", "/items/" + existingID.String()} {
		if !strings.Contains(body, want) {
			t.Errorf("duplicate race body missing %q", want)
		}
	}
}

func TestItemScanFlow_UIVocabulary(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Scan item", "Barcode", "Manual entry"} {
		if !strings.Contains(body, want) {
			t.Errorf("scan UI missing approved copy %q", want)
		}
	}
	for _, forbidden := range []string{"Product ID", "tenant_id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("scan UI contains forbidden copy %q", forbidden)
		}
	}
}

func validScanOperationalForm(token string) url.Values {
	return url.Values{
		"flow":            {token},
		"intent":          {"preview"},
		"name":            {"Organic Whole Milk"},
		"brand":           {"Clover"},
		"sku":             {"MILK-001"},
		"unit_of_measure": {"EA"},
		"unit_cost":       {"2.49"},
		"selling_price":   {"4.99"},
		"status":          {"active"},
	}
}
