// internal/obs/middleware.go
//
// chi.Router middleware that emits one OpenTelemetry span per request,
// records HTTP attributes, and surfaces the span context to downstream
// handlers via r.Context(). Pairs with obs.NewTracer + obs.NewLogger.

package obs

import (
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Middleware returns a chi-compatible HTTP middleware that wraps each
// request in a span named "<METHOD> <path>". Status code, duration, and
// route are attached as span attributes.
//
// serviceName names the tracer; it should match the value passed to
// NewTracer so spans group sensibly in the collector backend.
func Middleware(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)
	prop := otel.GetTextMapPropagator()
	if prop == nil {
		prop = propagation.TraceContext{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
					semconv.URLScheme(scheme(r)),
					attribute.String("http.user_agent", r.UserAgent()),
				),
			)
			defer span.End()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))
			dur := time.Since(start)

			span.SetAttributes(
				semconv.HTTPResponseStatusCode(rec.status),
				attribute.String("http.duration_ms", strconv.FormatInt(dur.Milliseconds(), 10)),
			)
			if rec.status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}

// scheme returns "https" if the request was forwarded with that scheme,
// "http" otherwise. Cheap heuristic — fine for span attributes.
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}

// statusRecorder wraps http.ResponseWriter to capture the final status
// code for span attribution. The chi pattern is well-known; this is the
// minimal re-implementation that doesn't pull a router-aware decorator.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}
