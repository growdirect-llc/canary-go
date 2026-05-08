// internal/auth/middleware.go
package auth

import (
	"net/http"
	"strings"

	"github.com/ruptiv/canary/internal/tenant"
)

// BearerMiddleware extracts the Authorization Bearer token, verifies it, and
// injects merchant_id into context. Returns 401 on missing or invalid token.
func BearerMiddleware(sessionSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				http.Error(w, `{"error":"missing_token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(header, "Bearer ")
			claims, err := VerifyToken(sessionSecret, tokenStr)
			if err != nil {
				http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
				return
			}
			ctx := tenant.InjectMerchantID(r.Context(), claims.MerchantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
