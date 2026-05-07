package web

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/tenant"
)

// tenantSessionMiddleware resolves the merchant UUID from the request's
// session cookie (via the resolver wired in Deps) and injects it into
// the request context so handlers calling tenantIDFromCtx — and the
// 70+ tenant-scoped queries downstream — see the authenticated tenant
// instead of uuid.Nil.
//
// Public routes (the `/`, `/connect`, `/welcome`, OAuth callback,
// static assets) keep working: the middleware never short-circuits.
// requireTenant() is the per-handler gate that turns a missing tenant
// into a redirect; this middleware just makes the resolved value
// available.
//
// When resolver is nil (e.g. test harnesses that don't exercise auth),
// the middleware is a passthrough — preserves existing handler tests
// that build a Handler without wiring squareauth.
//
// T-B / GRO-849.
func tenantSessionMiddleware(resolver MerchantResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if resolver == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, ok := resolver(r); ok && id != uuid.Nil {
				ctx := tenant.InjectMerchantID(r.Context(), id)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireTenant wraps a handler that needs an authenticated tenant.
// When the request has no resolved tenant (uuid.Nil from
// tenantIDFromCtx), it redirects to /connect — the Square OAuth start
// page — instead of rendering a page with empty data, which is what
// every tenant-scoped handler did pre-T-B.
//
// Usage:
//
//	r.Get("/dashboard", h.requireTenant(h.dashboardPage))
//
// Public routes (/, /connect, /welcome, /join, OAuth flow, static
// assets) MUST NOT use this wrapper — they need to stay reachable
// without a session.
//
// T-B / GRO-849.
func (h *Handler) requireTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tenantIDFromCtx(r.Context()) == uuid.Nil {
			http.Redirect(w, r, "/connect", http.StatusFound)
			return
		}
		next(w, r)
	}
}
