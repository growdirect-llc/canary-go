// internal/item/handler_test.go
//
// Unit tests for the item HTTP handler. Stub store, no DB. Verifies
// routing, parameter parsing, and error mapping. Integration tests
// (Postgres + real schema) live in integration_test.go behind the
// `integration` build tag.

package item

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// stubStore is a hand-written test double — keeps the handler tests
// dependency-free.
type stubStore struct {
	getByIDFn         func(ctx context.Context, tenantID, id uuid.UUID) (*Item, error)
	getBySKUFn        func(ctx context.Context, tenantID uuid.UUID, sku string) (*Item, error)
	getByBarcodeFn    func(ctx context.Context, tenantID uuid.UUID, barcode string) (*Item, error)
	createFn          func(ctx context.Context, req CreateRequest) (*Item, error)
	updateFn          func(ctx context.Context, tenantID, id uuid.UUID, patch PatchRequest) (*Item, error)
	deleteFn          func(ctx context.Context, tenantID, id uuid.UUID) error
	listFn            func(ctx context.Context, f ListFilters) ([]Item, error)
	listCategoriesFn  func(ctx context.Context, tenantID uuid.UUID) ([]Category, error)
	listVendorsFn     func(ctx context.Context, tenantID uuid.UUID) ([]Vendor, error)
}

func (s *stubStore) GetByID(ctx context.Context, t, i uuid.UUID) (*Item, error) {
	return s.getByIDFn(ctx, t, i)
}
func (s *stubStore) GetBySKU(ctx context.Context, t uuid.UUID, sku string) (*Item, error) {
	return s.getBySKUFn(ctx, t, sku)
}
func (s *stubStore) GetByBarcode(ctx context.Context, t uuid.UUID, b string) (*Item, error) {
	return s.getByBarcodeFn(ctx, t, b)
}
func (s *stubStore) Create(ctx context.Context, r CreateRequest) (*Item, error) {
	return s.createFn(ctx, r)
}
func (s *stubStore) Update(ctx context.Context, t, i uuid.UUID, p PatchRequest) (*Item, error) {
	return s.updateFn(ctx, t, i, p)
}
func (s *stubStore) Delete(ctx context.Context, t, i uuid.UUID) error {
	return s.deleteFn(ctx, t, i)
}
func (s *stubStore) List(ctx context.Context, f ListFilters) ([]Item, error) {
	return s.listFn(ctx, f)
}
func (s *stubStore) ListCategories(ctx context.Context, t uuid.UUID) ([]Category, error) {
	return s.listCategoriesFn(ctx, t)
}
func (s *stubStore) AggregateByCategory(ctx context.Context, t uuid.UUID) ([]CategoryAggregate, error) {
	// Not exercised by /v1/items handler tests — the SQL-aggregation
	// path lives in the web handler at /reports/category. Default to
	// nil so the stub satisfies the Store interface without forcing
	// every test to set up a fixture.
	return nil, nil
}
func (s *stubStore) ListVendors(ctx context.Context, t uuid.UUID) ([]Vendor, error) {
	return s.listVendorsFn(ctx, t)
}

// newTestRouter builds a chi router with the handler mounted, using the
// supplied stub.
func newTestRouter(s Store) *chi.Mux {
	h := New(s, zap.NewNop())
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func sampleItem(tenantID, id uuid.UUID) *Item {
	return &Item{
		ID:              id,
		TenantID:        tenantID,
		SKU:             "SKU-001",
		Description:     "Test Widget",
		ItemType:        "standard",
		UnitOfMeasure:   "EA",
		UOMQuantity:     "1",
		DefaultCurrency: "USD",
		Attributes:      json.RawMessage(`{}`),
		Status:          "active",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func TestHandler_GetByID_OK(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	want := sampleItem(tenantID, itemID)

	stub := &stubStore{
		getByIDFn: func(_ context.Context, gotTenant, gotID uuid.UUID) (*Item, error) {
			if gotTenant != tenantID {
				t.Errorf("tenantID: got %v want %v", gotTenant, tenantID)
			}
			if gotID != itemID {
				t.Errorf("itemID: got %v want %v", gotID, itemID)
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/items/"+itemID.String()+"?tenant_id="+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got Item
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != itemID {
		t.Errorf("id: got %v want %v", got.ID, itemID)
	}
	if got.SKU != "SKU-001" {
		t.Errorf("sku: got %q", got.SKU)
	}
}

func TestHandler_GetByID_NotFound(t *testing.T) {
	stub := &stubStore{
		getByIDFn: func(_ context.Context, _, _ uuid.UUID) (*Item, error) { return nil, ErrNotFound },
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/items/"+uuid.NewString()+"?tenant_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}

func TestHandler_GetByID_MalformedUUID(t *testing.T) {
	stub := &stubStore{}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/items/not-a-uuid?tenant_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_GetByID_MissingTenant(t *testing.T) {
	stub := &stubStore{}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/items/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_GetBySKU_OK(t *testing.T) {
	tenantID := uuid.New()
	want := sampleItem(tenantID, uuid.New())
	stub := &stubStore{
		getBySKUFn: func(_ context.Context, gotTenant uuid.UUID, sku string) (*Item, error) {
			if sku != "SKU-001" {
				t.Errorf("sku: got %q", sku)
			}
			if gotTenant != tenantID {
				t.Errorf("tenantID mismatch")
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/items?tenant_id="+tenantID.String()+"&sku=SKU-001", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_GetByBarcode_OK(t *testing.T) {
	tenantID := uuid.New()
	want := sampleItem(tenantID, uuid.New())
	stub := &stubStore{
		getByBarcodeFn: func(_ context.Context, gotTenant uuid.UUID, bc string) (*Item, error) {
			if bc != "012345678905" {
				t.Errorf("barcode: got %q", bc)
			}
			if gotTenant != tenantID {
				t.Errorf("tenantID mismatch")
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/items/by-barcode?tenant_id="+tenantID.String()+"&barcode=012345678905", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"sku":"SKU-001"`) {
		t.Errorf("body missing sku: %s", rec.Body.String())
	}
}

func TestHandler_GetByBarcode_MissingBarcode(t *testing.T) {
	stub := &stubStore{}
	r := newTestRouter(stub)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/items/by-barcode?tenant_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_GetByBarcode_AcceptsMerchantIDAlias(t *testing.T) {
	tenantID := uuid.New()
	want := sampleItem(tenantID, uuid.New())
	stub := &stubStore{
		getByBarcodeFn: func(_ context.Context, gotTenant uuid.UUID, _ string) (*Item, error) {
			if gotTenant != tenantID {
				t.Errorf("tenantID mismatch")
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/items/by-barcode?merchant_id="+tenantID.String()+"&barcode=X", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Create_OK(t *testing.T) {
	tenantID := uuid.New()
	want := sampleItem(tenantID, uuid.New())
	stub := &stubStore{
		createFn: func(_ context.Context, req CreateRequest) (*Item, error) {
			if req.TenantID != tenantID {
				t.Errorf("tenantID mismatch")
			}
			if req.SKU != "NEW-001" {
				t.Errorf("sku: got %q", req.SKU)
			}
			if len(req.Barcodes) != 2 {
				t.Errorf("barcodes: got %d want 2", len(req.Barcodes))
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)

	body, _ := json.Marshal(CreateRequest{
		TenantID:    tenantID,
		SKU:         "NEW-001",
		Description: "New Widget",
		Barcodes: []BarcodeRequest{
			{Value: "012345678905"},
			{Value: "5012345678900"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/items", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Create_ValidationError(t *testing.T) {
	stub := &stubStore{
		createFn: func(_ context.Context, req CreateRequest) (*Item, error) {
			return nil, req.Validate()
		},
	}
	r := newTestRouter(stub)

	body, _ := json.Marshal(CreateRequest{TenantID: uuid.New()}) // missing sku & description
	req := httptest.NewRequest(http.MethodPost, "/v1/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Create_Conflict(t *testing.T) {
	stub := &stubStore{
		createFn: func(_ context.Context, _ CreateRequest) (*Item, error) {
			return nil, ErrConflict
		},
	}
	r := newTestRouter(stub)

	body, _ := json.Marshal(CreateRequest{
		TenantID: uuid.New(), SKU: "X", Description: "X",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409", rec.Code)
	}
}

func TestHandler_Update_OK(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	want := sampleItem(tenantID, itemID)
	want.Description = "Updated"
	stub := &stubStore{
		updateFn: func(_ context.Context, gotTenant, gotID uuid.UUID, p PatchRequest) (*Item, error) {
			if gotTenant != tenantID || gotID != itemID {
				t.Errorf("ids mismatch")
			}
			if p.Description == nil || *p.Description != "Updated" {
				t.Errorf("description not patched")
			}
			return want, nil
		},
	}
	r := newTestRouter(stub)

	body, _ := json.Marshal(PatchRequest{Description: ptr("Updated")})
	req := httptest.NewRequest(http.MethodPatch,
		"/v1/items/"+itemID.String()+"?tenant_id="+tenantID.String(),
		bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Delete_OK(t *testing.T) {
	stub := &stubStore{
		deleteFn: func(_ context.Context, _, _ uuid.UUID) error { return nil },
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodDelete,
		"/v1/items/"+uuid.NewString()+"?tenant_id="+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rec.Code)
	}
}

func TestHandler_List_WithFilters(t *testing.T) {
	tenantID := uuid.New()
	categoryID := uuid.New()
	stub := &stubStore{
		listFn: func(_ context.Context, f ListFilters) ([]Item, error) {
			if f.TenantID != tenantID {
				t.Errorf("tenantID mismatch")
			}
			if f.CategoryID == nil || *f.CategoryID != categoryID {
				t.Errorf("category filter not applied")
			}
			if f.Limit != 10 {
				t.Errorf("limit: got %d want 10", f.Limit)
			}
			if f.Offset != 20 {
				t.Errorf("offset: got %d want 20 (page 3, size 10)", f.Offset)
			}
			return []Item{*sampleItem(tenantID, uuid.New())}, nil
		},
	}
	r := newTestRouter(stub)

	url := "/v1/items?tenant_id=" + tenantID.String() +
		"&category_id=" + categoryID.String() +
		"&page=3&size=10"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []Item `json:"items"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 {
		t.Errorf("count: got %d want 1", resp.Count)
	}
}

func TestHandler_ListCategories_OK(t *testing.T) {
	tenantID := uuid.New()
	stub := &stubStore{
		listCategoriesFn: func(_ context.Context, gotTenant uuid.UUID) ([]Category, error) {
			if gotTenant != tenantID {
				t.Errorf("tenantID mismatch")
			}
			return []Category{{ID: uuid.New(), TenantID: tenantID, Code: "FOOD", Name: "Food",
				Attributes: json.RawMessage(`{}`), Status: "active"}}, nil
		},
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/categories?tenant_id="+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_ListVendors_OK(t *testing.T) {
	tenantID := uuid.New()
	stub := &stubStore{
		listVendorsFn: func(_ context.Context, gotTenant uuid.UUID) ([]Vendor, error) {
			if gotTenant != tenantID {
				t.Errorf("tenantID mismatch")
			}
			return []Vendor{{ID: uuid.New(), TenantID: tenantID, VendorCode: "ACME", Name: "Acme",
				PrimaryContact: json.RawMessage(`{}`), Address: json.RawMessage(`{}`),
				Attributes: json.RawMessage(`{}`), Status: "active"}}, nil
		},
	}
	r := newTestRouter(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/vendors?tenant_id="+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidate_RejectsEmptyFields(t *testing.T) {
	cases := []struct {
		name string
		req  CreateRequest
	}{
		{"missing tenant", CreateRequest{SKU: "X", Description: "Y"}},
		{"missing sku", CreateRequest{TenantID: uuid.New(), Description: "Y"}},
		{"missing description", CreateRequest{TenantID: uuid.New(), SKU: "X"}},
		{"empty barcode value", CreateRequest{
			TenantID: uuid.New(), SKU: "X", Description: "Y",
			Barcodes: []BarcodeRequest{{Value: ""}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.req.Validate(); err == nil {
				t.Errorf("expected validation error")
			}
		})
	}
}

// ptr is a tiny helper for taking pointers to literals.
func ptr[T any](v T) *T { return &v }
