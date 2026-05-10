// internal/identity/scopemw.go
//
// RequireScopeMiddleware — chi middleware enforcing API-key scopes at
// the route-group boundary. Pairs with APIKeyMiddleware: APIKeyMiddleware
// authenticates and injects Claims; RequireScopeMiddleware authorizes
// against the Scopes slice on those claims.
//
// Wire pattern (typical service main.go):
//
//	r.Group(func(r chi.Router) {
//	    r.Use(identity.APIKeyMiddleware(opts))
//	    r.Group(func(r chi.Router) {
//	        r.Use(identity.RequireScopeMiddleware(identity.ScopeFooRead))
//	        // GET routes here
//	    })
//	    r.Group(func(r chi.Router) {
//	        r.Use(identity.RequireScopeMiddleware(identity.ScopeFooWrite))
//	        // POST/PUT/PATCH/DELETE routes here
//	    })
//	})
//
// The handler-level RequireScope helper in context.go is still useful
// when one handler accepts multiple scopes or makes a conditional
// authorization decision (see cmd/gateway/admin.go DLQ replay paths).
// New code should prefer the middleware; the handler-level helper
// stays for those legitimate edge cases.

package identity

import (
	"fmt"
	"net/http"
)

// RequireScopeMiddleware returns a chi middleware that 403s any request
// whose authenticated Claims do not include the named scope.
//
// 401 is returned if no claims are attached at all — the middleware is
// designed to run AFTER APIKeyMiddleware (or any other identity
// middleware), so missing claims means the chain is mis-wired and the
// caller is treated as unauthenticated.
//
// On success, the request passes through unchanged.
func RequireScopeMiddleware(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				writeScopeError(w, http.StatusUnauthorized, "unauthenticated",
					"missing or invalid credentials")
				return
			}
			if !hasScope(claims.Scopes, scope) {
				writeScopeError(w, http.StatusForbidden, "insufficient_scope",
					fmt.Sprintf("scope %q required", scope))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hasScope is the linear-scan check shared with RequireScope. Kept
// internal so the read code lives in one place.
func hasScope(granted []string, want string) bool {
	for _, s := range granted {
		if s == want {
			return true
		}
	}
	return false
}

// writeScopeError matches the envelope shape used by writeAuthError in
// apikey.go (`{"code":"...","message":"..."}`) so clients see a uniform
// auth/authz error format regardless of which middleware tripped.
func writeScopeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, message)
}
