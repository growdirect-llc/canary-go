package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/item"
)

// stubItemStore satisfies item.Store with function-typed overrides
// per method, so each test sets only what it needs.
type stubItemStore struct {
	createFn         func(ctx context.Context, req item.CreateRequest) (*item.Item, error)
	listCategoriesFn func(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error)
	listVendorsFn    func(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error)
}

func (s *stubItemStore) GetByID(_ context.Context, _, _ uuid.UUID) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *stubItemStore) GetBySKU(_ context.Context, _ uuid.UUID, _ string) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *stubItemStore) GetByBarcode(_ context.Context, _ uuid.UUID, _ string) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *stubItemStore) Create(ctx context.Context, req item.CreateRequest) (*item.Item, error) {
	if s.createFn != nil {
		return s.createFn(ctx, req)
	}
	return &item.Item{ID: uuid.New(), TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
}
func (s *stubItemStore) Update(_ context.Context, _, _ uuid.UUID, _ item.PatchRequest) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *stubItemStore) Delete(_ context.Context, _, _ uuid.UUID) error { return nil }
func (s *stubItemStore) List(_ context.Context, _ item.ListFilters) ([]item.Item, error) {
	return nil, nil
}
func (s *stubItemStore) ListCategories(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error) {
	if s.listCategoriesFn != nil {
		return s.listCategoriesFn(ctx, tenantID)
	}
	return nil, nil
}
func (s *stubItemStore) AggregateByCategory(_ context.Context, _ uuid.UUID) ([]item.CategoryAggregate, error) {
	return nil, nil
}
func (s *stubItemStore) ListVendors(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error) {
	if s.listVendorsFn != nil {
		return s.listVendorsFn(ctx, tenantID)
	}
	return nil, nil
}

// Compile-time guard.
var _ item.Store = (*stubItemStore)(nil)

func newItemCreateRouter(t *testing.T, store item.Store) http.Handler {
	t.Helper()
	deps := withTestAuth(Deps{ItemStore: store})
	h := New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// TestItemNew_RendersForm_NilStore verifies the form renders even
// when ItemStore is nil — design-time review path without a live DB.
func TestItemNew_RendersForm_NilStore(t *testing.T) {
	r := newItemCreateRouter(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/items/new", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"New item", "Item name", "SKU", "Unit cost", "Selling price", `action="/items/new"`, `method="POST"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

// TestItemNew_RendersForm_WithCategoriesAndVendors verifies the
// dropdowns render the active category + vendor options.
func TestItemNew_RendersForm_WithCategoriesAndVendors(t *testing.T) {
	store := &stubItemStore{
		listCategoriesFn: func(_ context.Context, _ uuid.UUID) ([]item.Category, error) {
			return []item.Category{
				{ID: uuid.New(), Code: "DAIRY", Name: "Dairy", Status: "active"},
				{ID: uuid.New(), Code: "PROD", Name: "Produce", Status: "active"},
				{ID: uuid.New(), Code: "OLD", Name: "Old (inactive)", Status: "inactive"},
			}, nil
		},
		listVendorsFn: func(_ context.Context, _ uuid.UUID) ([]item.Vendor, error) {
			return []item.Vendor{
				{ID: uuid.New(), VendorCode: "HRZ", Name: "Horizon Wholesale", Status: "active"},
			}, nil
		},
	}
	r := newItemCreateRouter(t, store)

	req := httptest.NewRequest(http.MethodGet, "/items/new", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{"Dairy", "Produce", "Horizon Wholesale"} {
		if !strings.Contains(body, want) {
			t.Errorf("dropdown missing %q", want)
		}
	}
	if strings.Contains(body, "Old (inactive)") {
		t.Error("inactive category should not render in dropdown")
	}
}

// postForm wraps httptest's POST helper so tests can submit with a
// form body without re-stating the boilerplate.
func postForm(r http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// validForm returns a form-values map that satisfies all required
// fields. Tests mutate this to test specific failure modes.
func validForm() url.Values {
	v := url.Values{}
	v.Set("sku", "SKU-001")
	v.Set("description", "Organic Whole Milk")
	v.Set("unit_cost", "2.49")
	v.Set("selling_price", "4.99")
	v.Set("unit_of_measure", "EA")
	v.Set("status", "hidden")
	return v
}

// TestItemCreate_NoStore_Flash verifies the no-store path redirects
// back with `flash=no_store`.
func TestItemCreate_NoStore_Flash(t *testing.T) {
	r := newItemCreateRouter(t, nil)
	rec := postForm(r, "/items/new", validForm())

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "flash=no_store") {
		t.Errorf("redirect: got %q, expected flash=no_store", loc)
	}
}

// TestItemCreate_MissingRequired covers the four required-field
// missing paths in one table.
func TestItemCreate_MissingRequired(t *testing.T) {
	cases := []struct {
		omit  string
		flash string
	}{
		{"sku", "missing_sku"},
		{"description", "missing_description"},
		{"unit_cost", "missing_unit_cost"},
		{"selling_price", "missing_selling_price"},
	}
	for _, tc := range cases {
		t.Run(tc.omit, func(t *testing.T) {
			r := newItemCreateRouter(t, &stubItemStore{})
			form := validForm()
			form.Del(tc.omit)
			rec := postForm(r, "/items/new", form)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status: got %d, want 303", rec.Code)
			}
			loc := rec.Header().Get("Location")
			if !strings.Contains(loc, tc.flash) {
				t.Errorf("redirect: got %q, expected %s", loc, tc.flash)
			}
		})
	}
}

// TestItemCreate_PreservesFormState verifies that a flash redirect
// carries the previously-entered values back to the form so the
// operator doesn't have to retype.
func TestItemCreate_PreservesFormState(t *testing.T) {
	r := newItemCreateRouter(t, &stubItemStore{})
	form := validForm()
	form.Del("description") // trigger missing_description flash
	rec := postForm(r, "/items/new", form)

	loc := rec.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	q := parsed.Query()
	if q.Get("sku") != "SKU-001" {
		t.Errorf("sku not preserved: got %q", q.Get("sku"))
	}
	if q.Get("unit_cost") != "2.49" {
		t.Errorf("unit_cost not preserved: got %q", q.Get("unit_cost"))
	}
	if q.Get("selling_price") != "4.99" {
		t.Errorf("selling_price not preserved: got %q", q.Get("selling_price"))
	}
}

// TestItemCreate_DuplicateSku_Flash verifies an item.ErrConflict from
// the store surfaces as `flash=duplicate_sku`.
func TestItemCreate_DuplicateSku_Flash(t *testing.T) {
	store := &stubItemStore{
		createFn: func(_ context.Context, _ item.CreateRequest) (*item.Item, error) {
			return nil, item.ErrConflict
		},
	}
	r := newItemCreateRouter(t, store)
	rec := postForm(r, "/items/new", validForm())

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "flash=duplicate_sku") {
		t.Errorf("redirect: got %q, expected flash=duplicate_sku", loc)
	}
}

// TestItemCreate_HappyPath_RedirectsToDetail verifies a successful
// create lands on /items/{id}?flash=created.
func TestItemCreate_HappyPath_RedirectsToDetail(t *testing.T) {
	createdID := uuid.New()
	store := &stubItemStore{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			return &item.Item{ID: createdID, TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
		},
	}
	r := newItemCreateRouter(t, store)
	rec := postForm(r, "/items/new", validForm())

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	wantPrefix := "/items/" + createdID.String()
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Errorf("redirect: got %q, expected prefix %q", loc, wantPrefix)
	}
	if !strings.Contains(loc, "flash=created") {
		t.Errorf("redirect: missing flash=created, got %q", loc)
	}
}

// TestItemCreate_BarcodeOptional verifies that omitting the barcode
// field is fine (it's optional in the form). The CreateRequest's
// Barcodes slice should be empty (not a single zero-value entry).
func TestItemCreate_BarcodeOptional(t *testing.T) {
	var captured item.CreateRequest
	store := &stubItemStore{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			captured = req
			return &item.Item{ID: uuid.New(), TenantID: req.TenantID, SKU: req.SKU}, nil
		},
	}
	r := newItemCreateRouter(t, store)
	form := validForm()
	form.Del("barcode")
	postForm(r, "/items/new", form)

	if len(captured.Barcodes) != 0 {
		t.Errorf("Barcodes: got %d entries, want 0 when barcode field is empty", len(captured.Barcodes))
	}
}

// TestItemCreate_BarcodeProvided_AddedToRequest verifies barcode
// passthrough — submitting the barcode field results in one
// BarcodeRequest entry on the CreateRequest.
func TestItemCreate_BarcodeProvided_AddedToRequest(t *testing.T) {
	var captured item.CreateRequest
	store := &stubItemStore{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			captured = req
			return &item.Item{ID: uuid.New(), TenantID: req.TenantID, SKU: req.SKU}, nil
		},
	}
	r := newItemCreateRouter(t, store)
	form := validForm()
	form.Set("barcode", "0123456789012")
	postForm(r, "/items/new", form)

	if len(captured.Barcodes) != 1 {
		t.Fatalf("Barcodes: got %d, want 1", len(captured.Barcodes))
	}
	if captured.Barcodes[0].Value != "0123456789012" {
		t.Errorf("Barcode value: got %q, want 0123456789012", captured.Barcodes[0].Value)
	}
}

// TestItemList_HasNewItemButton verifies the items list page now
// links to /items/new — the operator-facing entry point for the
// flow this dispatch ships.
func TestItemList_HasNewItemButton(t *testing.T) {
	r := newItemCreateRouter(t, &stubItemStore{})
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/items/new"`) {
		t.Error("list page missing href=\"/items/new\" link to new-item form")
	}
	if !strings.Contains(body, "+ New item") {
		t.Error("list page missing '+ New item' button text")
	}
}
