package web

import (
	"context"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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

// TestRequireTenant_NilTenant_RedirectsToLogin verifies the
// per-handler nil-tenant gate: a request with no resolved tenant gets
// 302 → /login instead of executing the wrapped handler.
//
// Pre-fix this redirected to /connect (post-OAuth data-sync picker) —
// confusing UX for unauthenticated users. /login is the public landing.
func TestRequireTenant_NilTenant_RedirectsToLogin(t *testing.T) {
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
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect: got %q, want /login", loc)
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

// TestMount_PublicRoutes_ReachableWithoutTenant verifies T-B Phase 2:
// the `/connect`, `/welcome`, `/join`, error pages, and the home
// redirect must remain reachable for a logged-out client (no resolved
// tenant) — those are the routes the redirect-on-nil gate sends users
// to. If they were inside the protected Group the gate would loop.
func TestMount_PublicRoutes_ReachableWithoutTenant(t *testing.T) {
	// No MerchantResolver -> tenantSessionMiddleware is a passthrough,
	// every request lands with uuid.Nil tenant.
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	publicRoutes := []struct {
		path       string
		wantStatus int
	}{
		{"/", http.StatusFound},                  // 302 → /dashboard
		{"/errors/404", http.StatusNotFound},     // errPage(404) renders a 404 body
		{"/errors/500", http.StatusInternalServerError},
	}
	for _, tc := range publicRoutes {
		req := httptest.NewRequest("GET", tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.wantStatus {
			t.Errorf("public path %s: got %d, want %d", tc.path, rec.Code, tc.wantStatus)
		}
	}
}

// TestMount_ProtectedRoutes_RedirectWhenNoTenant verifies that a
// representative tenant-scoped route gets redirected to /login when
// no session is resolved. This proves the Group-level gate is wired.
//
// Redirect target updated by the auth-flow fix: /connect → /login.
func TestMount_ProtectedRoutes_RedirectWhenNoTenant(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, p := range []string{"/dashboard", "/transactions", "/admin/audit", "/protocol"} {
		req := httptest.NewRequest("GET", p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Errorf("protected path %s: got %d, want 302", p, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("protected path %s redirect: got %q, want /login", p, loc)
		}
	}
}

// TestMaxBytesMiddleware_GetRequest_NotCapped verifies the middleware
// is method-gated to POST/PUT/PATCH — GET requests pass through
// untouched (their body is empty anyway, but the middleware should
// not wrap r.Body since wrapping unnecessarily incurs allocation).
func TestMaxBytesMiddleware_GetRequest_NotCapped(t *testing.T) {
	mw := MaxBytesMiddleware(8)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if !called {
		t.Fatal("next never called for GET")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// TestMaxBytesMiddleware_PostRequest_ReadsUpToCap verifies a POST
// body within the cap reads cleanly — the wrapper does not block
// or truncate compliant requests.
func TestMaxBytesMiddleware_PostRequest_ReadsUpToCap(t *testing.T) {
	mw := MaxBytesMiddleware(64)
	body := bytes.NewBufferString("compliant=yes")
	read := -1
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		read = len(buf)
	})

	mw(next).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/x", body))

	if read != 13 {
		t.Errorf("read: got %d bytes, want 13", read)
	}
}

// TestMaxBytesMiddleware_PostOverCap_FailsRead verifies that a body
// over the cap causes the io.ReadAll call to error — defense-in-
// depth against unbounded request bodies.
func TestMaxBytesMiddleware_PostOverCap_FailsRead(t *testing.T) {
	mw := MaxBytesMiddleware(8)
	body := bytes.NewBufferString("more-than-eight-bytes-here")
	var readErr error
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})

	mw(next).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest("POST", "/x", body))

	if readErr == nil {
		t.Fatal("io.ReadAll over-cap body did not error")
	}
}
