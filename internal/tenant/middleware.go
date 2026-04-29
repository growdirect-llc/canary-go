// internal/tenant/middleware.go
package tenant

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey string

const merchantIDKey contextKey = "merchant_id"

// FromContext retrieves the merchant_id injected by the auth middleware.
func FromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(merchantIDKey).(uuid.UUID)
	return v, ok
}

// InjectMerchantID is called by auth middleware after JWT validation.
func InjectMerchantID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, merchantIDKey, id)
}

// RequireMerchant returns 401 if no merchant_id is in context.
func RequireMerchant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
