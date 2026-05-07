// internal/inventory/handler_test.go
//
// Handler unit tests using httptest + stub Reader/Writer. No DB.
package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// stubStore implements both PositionReader and MovementWriter.
type stubStore struct {
	getPosition    func(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (*PositionDTO, error)
	listPositions  func(ctx context.Context, tenantID uuid.UUID, locationID, itemID *uuid.UUID, limit, offset int) ([]PositionDTO, error)
	appendMovement func(ctx context.Context, req AppendMovementRequest, movementAt time.Time) (*MovementDTO, *PositionDTO, error)
	listMovements  func(ctx context.Context, tenantID, itemID, locationID uuid.UUID, from, to *time.Time, limit, offset int) ([]MovementDTO, error)
}

func (s *stubStore) GetPosition(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (*PositionDTO, error) {
	return s.getPosition(ctx, tenantID, itemID, locationID)
}
func (s *stubStore) ListPositions(ctx context.Context, tenantID uuid.UUID, locationID, itemID *uuid.UUID, limit, offset int) ([]PositionDTO, error) {
	return s.listPositions(ctx, tenantID, locationID, itemID, limit, offset)
}
func (s *stubStore) AppendMovement(ctx context.Context, req AppendMovementRequest, movementAt time.Time) (*MovementDTO, *PositionDTO, error) {
	return s.appendMovement(ctx, req, movementAt)
}
func (s *stubStore) ListMovements(ctx context.Context, tenantID, itemID, locationID uuid.UUID, from, to *time.Time, limit, offset int) ([]MovementDTO, error) {
	return s.listMovements(ctx, tenantID, itemID, locationID, from, to, limit, offset)
}

func mountHandler(s *stubStore) http.Handler {
	r := chi.NewRouter()
	h := New(s, s, nil)
	h.Mount(r)
	return r
}

func TestGetPosition_Success(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()

	pos := &PositionDTO{
		ID:                uuid.New(),
		TenantID:          tenantID,
		ItemID:            itemID,
		LocationID:        locationID,
		OnHandQuantity:    "42.0000",
		ReservedQuantity:  "0.0000",
		OnOrderQuantity:   "0.0000",
		InTransitQuantity: "0.0000",
		Status:            "active",
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Attributes:        json.RawMessage("{}"),
	}

	stub := &stubStore{
		getPosition: func(_ context.Context, gtID, gItem, gLoc uuid.UUID) (*PositionDTO, error) {
			if gtID != tenantID || gItem != itemID || gLoc != locationID {
				t.Errorf("scope: tenant=%s item=%s loc=%s", gtID, gItem, gLoc)
			}
			return pos, nil
		},
	}
	srv := mountHandler(stub)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/inventory/positions/"+itemID.String()+"/"+locationID.String(), nil)
	req.Header.Set(HeaderMerchant, tenantID.String())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var got PositionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OnHandQuantity != "42.0000" {
		t.Errorf("on_hand: got %q", got.OnHandQuantity)
	}
}

func TestGetPosition_NotFound(t *testing.T) {
	stub := &stubStore{
		getPosition: func(_ context.Context, _, _, _ uuid.UUID) (*PositionDTO, error) {
			return nil, ErrPositionNotFound
		},
	}
	srv := mountHandler(stub)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/inventory/positions/"+uuid.NewString()+"/"+uuid.NewString(), nil)
	req.Header.Set(HeaderMerchant, uuid.NewString())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestGetPosition_MissingMerchantHeader(t *testing.T) {
	srv := mountHandler(&stubStore{})
	req := httptest.NewRequest(http.MethodGet,
		"/v1/inventory/positions/"+uuid.NewString()+"/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestListPositions_HappyPath(t *testing.T) {
	tenantID := uuid.New()
	expected := []PositionDTO{
		{ID: uuid.New(), TenantID: tenantID, OnHandQuantity: "5.0000", Status: "active"},
		{ID: uuid.New(), TenantID: tenantID, OnHandQuantity: "9.0000", Status: "active"},
	}
	stub := &stubStore{
		listPositions: func(_ context.Context, _ uuid.UUID, _, _ *uuid.UUID, limit, offset int) ([]PositionDTO, error) {
			if limit != 50 || offset != 0 {
				t.Errorf("pagination: limit=%d offset=%d", limit, offset)
			}
			return expected, nil
		},
	}
	srv := mountHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/v1/inventory/positions", nil)
	req.Header.Set(HeaderMerchant, tenantID.String())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var got PositionListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Items) != 2 {
		t.Errorf("items: got %d", len(got.Items))
	}
	if got.Page != 1 || got.Size != 50 {
		t.Errorf("page/size: %d/%d", got.Page, got.Size)
	}
}

func TestAppendMovement_HappyPath(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()
	userID := uuid.New()

	body := AppendMovementRequest{
		MerchantID:        tenantID,
		ItemID:            itemID,
		LocationID:        locationID,
		MovementType:      "sale",
		Quantity:          "-5",
		PerformedByUserID: &userID,
	}
	bodyJSON, _ := json.Marshal(body)

	stub := &stubStore{
		appendMovement: func(_ context.Context, req AppendMovementRequest, _ time.Time) (*MovementDTO, *PositionDTO, error) {
			if req.MovementType != "sale" || req.Quantity != "-5" {
				t.Errorf("req: type=%s qty=%s", req.MovementType, req.Quantity)
			}
			return &MovementDTO{
					ID: uuid.New(), TenantID: tenantID, ItemID: itemID,
					LocationID: locationID, MovementType: "sale",
					QuantityDelta: "-5", MovementAt: time.Now().UTC(),
					Attributes: json.RawMessage("{}"),
				},
				&PositionDTO{
					ID: uuid.New(), TenantID: tenantID, ItemID: itemID,
					LocationID: locationID, OnHandQuantity: "37.0000",
					Status: "active", Attributes: json.RawMessage("{}"),
				},
				nil
		},
	}
	srv := mountHandler(stub)
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/movements", bytes.NewReader(bodyJSON))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var got AppendMovementResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Position.OnHandQuantity != "37.0000" {
		t.Errorf("on_hand: got %q", got.Position.OnHandQuantity)
	}
	if got.Movement.QuantityDelta != "-5" {
		t.Errorf("qty: got %q", got.Movement.QuantityDelta)
	}
}

func TestAppendMovement_RejectsInvalidType(t *testing.T) {
	body := AppendMovementRequest{
		MerchantID:   uuid.New(),
		ItemID:       uuid.New(),
		LocationID:   uuid.New(),
		MovementType: "teleportation",
		Quantity:     "1",
	}
	bodyJSON, _ := json.Marshal(body)
	srv := mountHandler(&stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/movements", bytes.NewReader(bodyJSON))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_movement_type") {
		t.Errorf("error code missing: %s", rec.Body.String())
	}
}

func TestAppendMovement_RejectsZeroDelta(t *testing.T) {
	body := AppendMovementRequest{
		MerchantID:   uuid.New(),
		ItemID:       uuid.New(),
		LocationID:   uuid.New(),
		MovementType: "sale",
		Quantity:     "0",
	}
	bodyJSON, _ := json.Marshal(body)
	srv := mountHandler(&stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/movements", bytes.NewReader(bodyJSON))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestAppendMovement_RejectsMissingFields(t *testing.T) {
	bodyJSON := []byte(`{"movement_type":"sale","quantity":"1"}`)
	srv := mountHandler(&stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/movements", bytes.NewReader(bodyJSON))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestListMovements_HappyPath(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()
	stub := &stubStore{
		listMovements: func(_ context.Context, gtID, gItem, gLoc uuid.UUID, _, _ *time.Time, limit, offset int) ([]MovementDTO, error) {
			if gtID != tenantID || gItem != itemID || gLoc != locationID {
				t.Errorf("scope mismatch")
			}
			if limit != 50 || offset != 0 {
				t.Errorf("pagination: limit=%d offset=%d", limit, offset)
			}
			return []MovementDTO{{
				ID: uuid.New(), TenantID: tenantID, ItemID: itemID,
				LocationID: locationID, MovementType: "sale",
				QuantityDelta: "-1", MovementAt: time.Now().UTC(),
				Attributes: json.RawMessage("{}"),
			}}, nil
		},
	}
	srv := mountHandler(stub)
	url := "/v1/inventory/movements?item_id=" + itemID.String() + "&location_id=" + locationID.String()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set(HeaderMerchant, tenantID.String())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var got MovementListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Items) != 1 {
		t.Errorf("items: got %d", len(got.Items))
	}
}

func TestListMovements_TimeFilters(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()
	wantFrom := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	stub := &stubStore{
		listMovements: func(_ context.Context, _, _, _ uuid.UUID, from, to *time.Time, _, _ int) ([]MovementDTO, error) {
			if from == nil || !from.Equal(wantFrom) {
				return nil, errors.New("bad from")
			}
			if to == nil || !to.Equal(wantTo) {
				return nil, errors.New("bad to")
			}
			return nil, nil
		},
	}
	srv := mountHandler(stub)
	url := "/v1/inventory/movements?item_id=" + itemID.String() +
		"&location_id=" + locationID.String() +
		"&from=2026-05-01T00:00:00Z&to=2026-05-02T00:00:00Z"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set(HeaderMerchant, tenantID.String())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateAppendRequest_TableDriven(t *testing.T) {
	good := AppendMovementRequest{
		MerchantID:   uuid.New(),
		ItemID:       uuid.New(),
		LocationID:   uuid.New(),
		MovementType: "goods_receipt",
		Quantity:     "10",
	}
	cases := []struct {
		name    string
		mutate  func(r *AppendMovementRequest)
		wantErr error
	}{
		{"good", func(r *AppendMovementRequest) {}, nil},
		{"missing merchant", func(r *AppendMovementRequest) { r.MerchantID = uuid.Nil }, ErrMissingField},
		{"missing item", func(r *AppendMovementRequest) { r.ItemID = uuid.Nil }, ErrMissingField},
		{"missing location", func(r *AppendMovementRequest) { r.LocationID = uuid.Nil }, ErrMissingField},
		{"bad type", func(r *AppendMovementRequest) { r.MovementType = "nonsense" }, ErrInvalidMovementType},
		{"empty qty", func(r *AppendMovementRequest) { r.Quantity = "" }, ErrInvalidQuantity},
		{"non-numeric qty", func(r *AppendMovementRequest) { r.Quantity = "lots" }, ErrInvalidQuantity},
		{"zero qty", func(r *AppendMovementRequest) { r.Quantity = "0" }, ErrInvalidQuantity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			tc.mutate(&r)
			_, err := ValidateAppendRequest(r)
			if tc.wantErr == nil && err != nil {
				t.Errorf("want nil, got %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestAppendAdjustment_HappyPath(t *testing.T) {
	merchantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()
	movID := uuid.New()

	stub := &stubStore{
		appendMovement: func(_ context.Context, req AppendMovementRequest, _ time.Time) (*MovementDTO, *PositionDTO, error) {
			if req.MovementType != "cycle_count_correction" {
				t.Errorf("expected cycle_count_correction, got %q", req.MovementType)
			}
			mov := &MovementDTO{ID: movID, TenantID: merchantID, ItemID: itemID, LocationID: locationID, MovementType: req.MovementType, QuantityDelta: req.Quantity, MovementAt: time.Now()}
			pos := &PositionDTO{ID: uuid.New(), TenantID: merchantID, ItemID: itemID, LocationID: locationID, OnHandQuantity: "5", ReservedQuantity: "0", OnOrderQuantity: "0", InTransitQuantity: "0", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			return mov, pos, nil
		},
		listMovements: func(_ context.Context, _ uuid.UUID, _, _ uuid.UUID, _, _ *time.Time, _, _ int) ([]MovementDTO, error) {
			return nil, nil
		},
	}

	h := New(stub, stub, nil)
	r := chi.NewRouter()
	h.Mount(r)

	body, _ := json.Marshal(AdjustmentRequest{
		MerchantID: merchantID,
		ItemID:     itemID,
		LocationID: locationID,
		Quantity:   "-3",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/inventory/adjustments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AppendMovementResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Movement.ID != movID {
		t.Errorf("movement id mismatch")
	}
}

func TestIsZeroOrNegative(t *testing.T) {
	cases := []struct{ qty string; want bool }{
		{"0", true},
		{"0.0000", true},
		{"-1", true},
		{"-0.0001", true},
		{"1", false},
		{"0.0001", false},
		{"", true},
	}
	for _, tc := range cases {
		if got := isZeroOrNegative(tc.qty); got != tc.want {
			t.Errorf("isZeroOrNegative(%q) = %v, want %v", tc.qty, got, tc.want)
		}
	}
}
