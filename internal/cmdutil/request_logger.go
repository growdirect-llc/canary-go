package cmdutil

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// RequestLogger returns chi-style middleware that logs an "http" entry
// per request with method, path, status code, and bytes written.
//
// Replaces the inline `requestLogger` function copy-pasted in 12 cmd
// binaries (asset, fox, item, inventory, owl, returns, employee,
// analytics, report, pricing, customer, gateway). Bodies were
// character-identical pre-extraction.
//
// Compose with chi:
//
//	r := chi.NewRouter()
//	r.Use(cmdutil.RequestLogger(logger))
func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
			)
		})
	}
}
