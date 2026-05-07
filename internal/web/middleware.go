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
// T-B.
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
// T-B.
func (h *Handler) requireTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tenantIDFromCtx(r.Context()) == uuid.Nil {
			http.Redirect(w, r, "/connect", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireTenantMiddleware is the chi-middleware form of requireTenant
// — wraps a whole subrouter so every route inside a chi.Group is
// gated. Public routes (registered outside the group) remain
// reachable without a session.
//
// T-B.
func (h *Handler) requireTenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantIDFromCtx(r.Context()) == uuid.Nil {
			http.Redirect(w, r, "/connect", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MaxBytesMiddleware caps the request body at maxBytes. Returns a
// chi-compatible middleware. Wraps r.Body in http.MaxBytesReader so
// downstream io.ReadAll / json.NewDecoder calls fail fast with a
// "http: request body too large" error after the cap.
//
// 64 KiB is the T-E default — every form on every page is well
// under that. Routes that need a higher cap (e.g. evidence upload
// when that lands) should register their own MaxBytesReader inside
// the handler before parsing the body, overriding the upstream
// limit.
//
// Method-gated to POST/PUT/PATCH so GETs aren't penalized by the
// body wrapping (they shouldn't have bodies, but defensive).
//
// T-E.
func MaxBytesMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
