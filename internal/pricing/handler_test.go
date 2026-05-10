package pricing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/pricing"
)

// TestHandleResolve_Unauthenticated_Returns401 is the GRO-928
// acceptance probe for pricing's resolve endpoint. With no API-key
// claims attached to the request context, the handler MUST refuse
// before touching the resolver — there's no tenant to scope the
// query, and falling back to a body-supplied tenant_id is exactly the
// cross-tenant write surface the ticket closes.
//
// Fails pre-fix because pre-GRO-928 the handler read tenant from the
// body (req.TenantID) and dispatched against any caller-supplied id.
func TestHandleResolve_Unauthenticated_Returns401(t *testing.T) {
	h := pricing.New(nil, nil, nil) // resolver/store untouched on the 401 path
	r := chi.NewRouter()
	h.Mount(r)

	// No claims injected — simulate a direct hit to the binary's port
	// outside the auth middleware.
	body, _ := json.Marshal(map[string]any{
		"tenant_id": uuid.New().String(),
		"location_id": uuid.New().String(),
		"lines":     []any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/pricing/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("unauthenticated")) {
		t.Errorf("body should report unauthenticated; got %s", w.Body.String())
	}
}

// TestHandleResolve_TenantMismatch_Returns403 verifies the body-vs-claim
// reconciliation: an authenticated caller cannot pass a different
// tenant_id in the request body. Defends against a compromised key
// trying to escalate by spoofing tenant in the payload.
func TestHandleResolve_TenantMismatch_Returns403(t *testing.T) {
	h := pricing.New(nil, nil, nil)
	r := chi.NewRouter()
	h.Mount(r)

	authTenant := uuid.New()
	bodyTenant := uuid.New() // different
	body, _ := json.Marshal(map[string]any{
		"tenant_id": bodyTenant.String(),
		"location_id": uuid.New().String(),
		"lines":     []any{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/pricing/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := identity.InjectClaims(req.Context(), identity.Claims{
		AuthMethod: identity.AuthMethodAPIKey,
		TenantID:   authTenant,
	})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("tenant_mismatch")) {
		t.Errorf("body should report tenant_mismatch; got %s", w.Body.String())
	}
}

// TestHandleListPromotions_QueryTenantMismatch_Returns403 covers the
// GET-side equivalent: a tenant_id query param that doesn't match the
// claim is rejected.
func TestHandleListPromotions_QueryTenantMismatch_Returns403(t *testing.T) {
	h := pricing.New(nil, nil, nil)
	r := chi.NewRouter()
	h.Mount(r)

	authTenant := uuid.New()
	queryTenant := uuid.New()

	req := httptest.NewRequest(http.MethodGet,
		"/v1/pricing/promotions?tenant_id="+queryTenant.String()+"&location_id="+uuid.New().String(), nil)
	ctx := identity.InjectClaims(req.Context(), identity.Claims{
		AuthMethod: identity.AuthMethodAPIKey,
		TenantID:   authTenant,
	})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

var _ = context.Background
