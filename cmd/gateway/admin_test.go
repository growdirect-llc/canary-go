package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/protocol/publisher"
	"github.com/ruptiv/canary/internal/webhook"
)

// stubDLQ is the test implementation of dlqStore. Returns canned
// rows + records the filter passed to List() so tests can verify
// MerchantID was clamped to the caller's tenant.
type stubDLQ struct {
	rows       map[uuid.UUID]*webhook.DLQRow
	listFilter webhook.ListFilters
	listResult []webhook.DLQRow
}

func (s *stubDLQ) Get(_ context.Context, merchantID, id uuid.UUID) (*webhook.DLQRow, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, webhook.ErrDLQNotFound
	}
	// GRO-910: simulate the SQL-layer tenant filter — cross-tenant
	// reads return ErrDLQNotFound (no existence leak).
	if r.MerchantID != merchantID {
		return nil, webhook.ErrDLQNotFound
	}
	return r, nil
}
func (s *stubDLQ) List(_ context.Context, f webhook.ListFilters) ([]webhook.DLQRow, error) {
	s.listFilter = f
	return s.listResult, nil
}
func (s *stubDLQ) MarkReplayed(_ context.Context, merchantID, id uuid.UUID) error {
	r, ok := s.rows[id]
	if !ok || r.MerchantID != merchantID {
		return webhook.ErrDLQNotFound
	}
	return nil
}
func (s *stubDLQ) MarkRetryFailed(_ context.Context, merchantID, id uuid.UUID, _ string) (*webhook.DLQRow, error) {
	r, ok := s.rows[id]
	if !ok || r.MerchantID != merchantID {
		return nil, webhook.ErrDLQNotFound
	}
	return nil, nil
}

// stubPublisher records the published events; never fails.
type stubPublisher struct{ published []publisher.Event }

func (p *stubPublisher) Publish(_ context.Context, e publisher.Event) error {
	p.published = append(p.published, e)
	return nil
}

func newAdminTestRig() (*adminHandlers, *stubDLQ, *stubPublisher) {
	dlq := &stubDLQ{rows: map[uuid.UUID]*webhook.DLQRow{}}
	pub := &stubPublisher{}
	return newAdminHandlers(dlq, pub), dlq, pub
}

func tenantClaims(tenantID uuid.UUID, scopes ...string) identity.Claims {
	return identity.Claims{
		TenantID:   tenantID,
		Scopes:     scopes,
		AuthMethod: identity.AuthMethodAPIKey,
	}
}

// TestAdminList_TenantScopedKey_ClampsMerchantFilter verifies T-H:
// a tenant-scoped key calling /v1/webhooks/dlq?merchant_id=<other>
// has MerchantID forced to the caller's tenant before the DLQ
// query — no cross-tenant read.
func TestAdminList_TenantScopedKey_ClampsMerchantFilter(t *testing.T) {
	h, dlq, _ := newAdminTestRig()
	caller := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	other := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")

	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks/dlq?merchant_id="+other.String(), nil)
	ctx := identity.InjectClaims(req.Context(), tenantClaims(caller, "dlq:read"))
	rec := httptest.NewRecorder()
	h.list(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if dlq.listFilter.MerchantID == nil || *dlq.listFilter.MerchantID != caller {
		t.Errorf("MerchantID filter: got %v, want %s (clamp to caller tenant)",
			dlq.listFilter.MerchantID, caller)
	}
}

// TestAdminList_PlatformKey_HonorsMerchantFilter verifies platform-
// scope keys (TenantID == uuid.Nil) keep the merchant_id query
// param as a free filter — they're cross-tenant by design.
func TestAdminList_PlatformKey_HonorsMerchantFilter(t *testing.T) {
	h, dlq, _ := newAdminTestRig()
	target := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")

	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks/dlq?merchant_id="+target.String(), nil)
	ctx := identity.InjectClaims(req.Context(), tenantClaims(uuid.Nil, "dlq:read"))
	rec := httptest.NewRecorder()
	h.list(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if dlq.listFilter.MerchantID == nil || *dlq.listFilter.MerchantID != target {
		t.Errorf("platform key MerchantID: got %v, want %s (honored from query)",
			dlq.listFilter.MerchantID, target)
	}
}

// TestAdminGet_CrossTenant_Returns404 verifies T-H: a tenant-scoped
// key fetching a row that belongs to a different tenant gets 404
// (not 403, not 200 with another tenant's payload). 404 matches
// the response shape of a true miss to avoid existence leak.
func TestAdminGet_CrossTenant_Returns404(t *testing.T) {
	h, dlq, _ := newAdminTestRig()
	caller := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	rowOwner := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	rowID := uuid.New()
	dlq.rows[rowID] = &webhook.DLQRow{ID: rowID, MerchantID: rowOwner, Status: "pending"}

	r := chi.NewRouter()
	r.Get("/v1/webhooks/dlq/{id}", h.get)
	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks/dlq/"+rowID.String(), nil)
	ctx := identity.InjectClaims(req.Context(), tenantClaims(caller, "dlq:read"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get: got %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "not_found" {
		t.Errorf("error code: got %q, want not_found", resp["code"])
	}
}

// TestAdminGet_OwnTenant_Returns200 verifies the happy path: a
// tenant-scoped key fetching its own row gets the row.
func TestAdminGet_OwnTenant_Returns200(t *testing.T) {
	h, dlq, _ := newAdminTestRig()
	caller := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	rowID := uuid.New()
	dlq.rows[rowID] = &webhook.DLQRow{ID: rowID, MerchantID: caller, Status: "pending"}

	r := chi.NewRouter()
	r.Get("/v1/webhooks/dlq/{id}", h.get)
	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks/dlq/"+rowID.String(), nil)
	ctx := identity.InjectClaims(req.Context(), tenantClaims(caller, "dlq:read"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusOK {
		t.Errorf("own-tenant get: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestAdminReplay_CrossTenant_Returns404 verifies T-H: a tenant-
// scoped key replaying another tenant's DLQ row gets 404 — does
// NOT republish under the foreign MerchantID.
func TestAdminReplay_CrossTenant_Returns404(t *testing.T) {
	h, dlq, pub := newAdminTestRig()
	caller := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	rowOwner := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	rowID := uuid.New()
	dlq.rows[rowID] = &webhook.DLQRow{ID: rowID, MerchantID: rowOwner, Status: "pending"}

	r := chi.NewRouter()
	r.Post("/v1/webhooks/replay/{id}", h.replay)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/replay/"+rowID.String(), nil)
	ctx := identity.InjectClaims(req.Context(), tenantClaims(caller, "dlq:replay"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant replay: got %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Errorf("publisher: got %d events, want 0 (cross-tenant replay must not republish)",
			len(pub.published))
	}
}

// Compile-time guard: ensure the stub satisfies the interface.
var _ dlqStore = (*stubDLQ)(nil)
var _ publisher.Publisher = (*stubPublisher)(nil)

// Compile-time guard so the import is used in the helper above.
var _ = errors.New
