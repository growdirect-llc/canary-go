package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/tenant"
)

// TestTenantSessionMiddleware_NilResolver_IsPassthrough verifies that
// when MerchantResolver is unset (e.g. test harness that doesn't wire
// squareauth), the middleware short-circuits to next without touching
// the request. Existing handler tests must keep working.
func TestTenantSessionMiddleware_NilResolver_IsPassthrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := tenant.FromContext(r.Context()); ok {
			t.Error("tenant unexpectedly present when resolver is nil")
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := tenantSessionMiddleware(nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest("GET", "/whatever", nil))

	if !called {
		t.Fatal("next handler was never called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestTenantSessionMiddleware_ResolverHit_InjectsTenant verifies that
// a successful resolver call lands the merchant UUID in
// tenant.FromContext for downstream handlers (and therefore for
// tenantIDFromCtx, which is what the 70+ store calls read).
func TestTenantSessionMiddleware_ResolverHit_InjectsTenant(t *testing.T) {
	want := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	resolver := func(_ *http.Request) (uuid.UUID, bool) {
		return want, true
	}

	var got uuid.UUID
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = tenantIDFromCtx(r.Context())
	})

	mw := tenantSessionMiddleware(resolver)
	mw(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/dashboard", nil))

	if got != want {
		t.Errorf("tenant in ctx: got %s, want %s", got, want)
	}
}

// TestTenantSessionMiddleware_ResolverMiss_LeavesContextEmpty verifies
// that a resolver returning ok=false does NOT inject — handlers see
// uuid.Nil from tenantIDFromCtx, which is the signal h.requireTenant
// uses to redirect to /connect.
func TestTenantSessionMiddleware_ResolverMiss_LeavesContextEmpty(t *testing.T) {
	resolver := func(_ *http.Request) (uuid.UUID, bool) {
		return uuid.Nil, false
	}

	var got uuid.UUID
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = tenantIDFromCtx(r.Context())
	})

	mw := tenantSessionMiddleware(resolver)
	mw(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/dashboard", nil))

	if got != uuid.Nil {
		t.Errorf("tenant in ctx: got %s, want uuid.Nil", got)
	}
}

// TestTenantSessionMiddleware_ResolverReturnsNilUUIDWithOK verifies
// the defensive path: even if a resolver returns (uuid.Nil, true) by
// mistake, the middleware does not inject a zero UUID — that would
// poison downstream queries.
func TestTenantSessionMiddleware_ResolverReturnsNilUUIDWithOK(t *testing.T) {
	resolver := func(_ *http.Request) (uuid.UUID, bool) {
		return uuid.Nil, true
	}

	var injected bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, injected = tenant.FromContext(r.Context())
	})

	mw := tenantSessionMiddleware(resolver)
	mw(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/dashboard", nil))

	if injected {
		t.Error("tenant.FromContext returned true for uuid.Nil — middleware injected a zero UUID")
	}
}

// TestRequireTenant_NilTenant_RedirectsToConnect verifies the
// per-handler nil-tenant gate: a request with no resolved tenant gets
// 302 → /connect instead of executing the wrapped handler.
func TestRequireTenant_NilTenant_RedirectsToConnect(t *testing.T) {
	h := &Handler{}
	wrappedCalled := false
	wrapped := h.requireTenant(func(http.ResponseWriter, *http.Request) {
		wrappedCalled = true
	})

	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest("GET", "/dashboard", nil))

	if wrappedCalled {
		t.Error("wrapped handler ran despite uuid.Nil tenant")
	}
	if rec.Code != http.StatusFound {
		t.Errorf("status: got %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/connect" {
		t.Errorf("redirect: got %q, want /connect", loc)
	}
}

// TestRequireTenant_ResolvedTenant_PassesThrough verifies the gate
// lets traffic through when the middleware has injected a real
// tenant UUID.
func TestRequireTenant_ResolvedTenant_PassesThrough(t *testing.T) {
	h := &Handler{}
	wrappedCalled := false
	wrapped := h.requireTenant(func(http.ResponseWriter, *http.Request) {
		wrappedCalled = true
	})

	tenantID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	ctx := tenant.InjectMerchantID(context.Background(), tenantID)
	req := httptest.NewRequest("GET", "/dashboard", nil).WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped(rec, req)

	if !wrappedCalled {
		t.Error("wrapped handler did not run despite resolved tenant")
	}
	if rec.Code == http.StatusFound {
		t.Error("redirected despite resolved tenant")
	}
}
