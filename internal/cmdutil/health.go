package cmdutil

import (
	"context"
	"encoding/json"
	"net/http"
)

// HealthCheck names a dependency and a probe function. The probe runs
// against the request's context so callers can apply per-request
// deadlines (typical: a small timeout via http.TimeoutHandler around
// HealthHandler) without coupling the dep code to a specific timeout.
//
// Probe contract: return nil for healthy, any error otherwise. The
// error's message lands in the JSON response under
// `checks[<name>]` so operators see the failure cause directly.
type HealthCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// HealthHandler returns an http.HandlerFunc that responds to /health
// with a JSON body summarizing service identity, build version, and
// per-dependency status.
//
// Behavior:
//
//   - 200 OK with `{"status":"ok", "service":<name>, "version":<v>, "checks":{...}}`
//     when every dep returns nil.
//   - 503 Service Unavailable with `"status":"degraded"` when any dep
//     returns an error. Healthy deps still report "ok"; failing deps
//     report their error message.
//   - With zero deps, always 200 (the historic static-health behavior
//     in 25 of 26 cmd binaries — preserved for binaries that genuinely
//     have no runtime dependencies to probe).
//
// Replaces the hardcoded `"version": "1.0.0"` static JSON across 20
// cmd binaries and the more thorough but per-binary-bespoke health
// implementation in cmd/identity.
//
// Usage:
//
//	r.Get("/health", cmdutil.HealthHandler("asset",
//	    cmdutil.HealthCheck{Name: "db", Check: func(ctx context.Context) error {
//	        return pool.Ping(ctx)
//	    }},
//	))
func HealthHandler(service string, deps ...HealthCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		checks := make(map[string]string, len(deps))
		degraded := false
		for _, d := range deps {
			if err := d.Check(ctx); err != nil {
				checks[d.Name] = err.Error()
				degraded = true
			} else {
				checks[d.Name] = "ok"
			}
		}

		body := map[string]any{
			"service": service,
			"status":  "ok",
			"version": Version(),
		}
		if len(checks) > 0 {
			body["checks"] = checks
		}
		status := http.StatusOK
		if degraded {
			body["status"] = "degraded"
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}
