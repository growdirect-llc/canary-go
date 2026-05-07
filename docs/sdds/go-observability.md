---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
type: shared-library
package: internal/observability
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# go-observability — Structured Logging, Metrics, and Tracing

**Package:** `internal/observability`  
**Imported by:** Every Canary Go service; `internal/runtime` (for middleware logging)  
**Depends on:** `log/slog` (stdlib), `github.com/prometheus/client_golang`, `go.opentelemetry.io/otel` (no-op in V1)

Observability is the only mechanism by which an operator can distinguish "the service is working correctly under load" from "the service is silently failing in a way that will surface as a P1 at midnight." `internal/observability` is the platform's three-pillar implementation: structured logs that machines can parse, metrics that Prometheus can alert on, and trace hooks that are wired to no-ops in V1 but cost nothing to add now and everything to retrofit later.

---

## Business

### The Problem This Solves

A service that logs unstructured text is searchable only by humans with grep. A service that doesn't export Prometheus metrics cannot be alerted on. A service that doesn't propagate trace IDs produces logs that are useless when debugging a failure that crosses three services — you have timestamps and a general sense of dread, but no causal chain.

This package gives every service a consistent signal surface. Grafana dashboards, PagerDuty alerts, and Cloud Logging queries are built against the field names and metric names defined here. If a service invents its own, the dashboards break.

### What Breaks Without It

- Log aggregation pipelines fail to parse unstructured log lines — fields are missing, alerting rules fire on the wrong data
- No `canary_request_duration_ms` histogram means no SLA alerting — p99 breaches go undetected until a merchant complains
- Missing `merchant_id` on log lines makes incident investigation for a specific tenant require full table scans of logs
- DB pool exhaustion is invisible until connections start failing — `DBPoolSize` gauge surfaces it before that point
- Without trace_id correlation, debugging a raas append failure that originated in webhook-pipeline requires reconstructing the call chain from timestamps

---

## Technical

### Structured Logging with slog

Go 1.21+ stdlib. Zero external dependencies. JSON format in all environments (including development — parsing structured logs in development is the correct default; the developer learns the field names early).

```go
// internal/observability/logger.go

func NewLogger(serviceName string) *slog.Logger {
    level := parseLogLevel(os.Getenv("LOG_LEVEL")) // default: slog.LevelInfo
    return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level:     level,
        AddSource: level == slog.LevelDebug, // source file:line only at DEBUG
    })).With(
        slog.String("service", serviceName),
    )
}
```

The `service` field is set at logger construction, not per log call. All log lines carry it automatically.

**Standard fields on every request log (emitted by `internal/runtime.LoggingMiddleware`):**

| Field | Type | Source |
|-------|------|--------|
| `service` | string | Set at logger init |
| `request_id` | string | `runtime.RequestID(ctx)` |
| `merchant_id` | string | `runtime.MerchantID(ctx)` — omitted if unauthenticated |
| `actor_id` | string | `runtime.ActorID(ctx)` — the user, agent, or system identifier from the JWT |
| `actor_type` | string | `runtime.ActorType(ctx)` — `human`, `agent`, or `system`. Required for the agent accountability model — every log line distinguishes human-driven from agent-driven traffic |
| `method` | string | `r.Method` |
| `path` | string | `r.URL.Path` — never `r.URL.RawQuery` (query strings may contain credentials) |
| `status` | int | Response status code |
| `duration_ms` | int64 | Handler wall time |
| `trace_id` | string | From OTel context if present; empty string if no-op tracer |

**Log level discipline:**

| Level | Use for |
|-------|---------|
| `DEBUG` | Per-request DB query parameters, Valkey cache hit/miss. Never in production default. |
| `INFO` | Request completed, service started, config loaded. |
| `WARN` | Retried operation succeeded, deprecated API called, readiness check degraded. |
| `ERROR` | Operation failed, panic recovered, chain violation, SLA breach. Always include `request_id`. |

### Prometheus Metrics

All metrics are registered with the default Prometheus registry at package init. Services call `observability.MustRegisterMetrics()` in their `main.go` — this is a no-op if called more than once (safe for test isolation).

```go
// internal/observability/metrics.go

var (
    // HTTP request latency — primary SLA metric
    RequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "canary_request_duration_ms",
            Help:    "HTTP request duration in milliseconds.",
            Buckets: []float64{5, 20, 50, 100, 200, 500, 1000, 2000, 5000},
        },
        []string{"service", "method", "path", "status"},
    )

    // Request volume counter
    RequestCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "canary_requests_total",
            Help: "Total HTTP requests by service, method, path, and status.",
        },
        []string{"service", "method", "path", "status"},
    )

    // DB pool health
    DBPoolAcquireDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "canary_db_pool_acquire_duration_ms",
            Help:    "Time to acquire a DB connection from the pool.",
            Buckets: []float64{1, 5, 10, 25, 50, 100, 500},
        },
        []string{"service"},
    )
    DBPoolSize = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "canary_db_pool_size",
            Help: "Current number of connections in the DB pool.",
        },
        []string{"service", "state"}, // state: "idle" | "in_use" | "max"
    )

    // Valkey command latency
    ValkeyCmdDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "canary_valkey_cmd_duration_ms",
            Help:    "Valkey command round-trip duration.",
            Buckets: []float64{0.5, 1, 2, 5, 10, 25, 50},
        },
        []string{"service", "command"},
    )

    // Session tracking (store-brain primarily)
    ActiveSessions = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "canary_active_sessions",
            Help: "Current number of active merchant sessions.",
        },
        []string{"service"},
    )

    // Chain append throughput (raas)
    ChainAppendTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "canary_chain_append_total",
            Help: "Total raas chain append operations.",
        },
        []string{"service", "status"}, // status: "ok" | "violation" | "timeout"
    )
)
```

**Metrics scrape endpoint:** `GET /metrics` — registered by `internal/runtime`, populated by this package. Unauthed. Internal network only.

**Cardinality discipline:** path labels must use route patterns, not raw paths. `/api/v1/merchants/{id}/events` not `/api/v1/merchants/abc-123/events`. High-cardinality path labels will exhaust Prometheus memory. `internal/runtime.LoggingMiddleware` uses Chi's `RouteContext` to extract the route pattern — never `r.URL.Path` — when recording metrics labels.

### OpenTelemetry Hooks

V1 uses a no-op tracer. The interface is wired; no spans are emitted. This is intentional — OTel adds ~5% overhead per request in its full form; we take 0% now and enable it when we have a trace backend to send to.

```go
// internal/observability/tracing.go

// Tracer returns the global OTel tracer. No-op in V1.
func Tracer() trace.Tracer {
    return otel.Tracer("canary-go")
}

// InjectTraceID extracts trace ID from OTel span context and injects into
// runtime.ContextKeyTraceID. No-op if span context is invalid (V1 behaviour).
func InjectTraceID(ctx context.Context) context.Context

// TraceIDFromContext returns the trace ID string, or "" if no-op.
func TraceIDFromContext(ctx context.Context) string
```

When the platform graduates to a real OTel exporter (Cloud Trace, Jaeger, etc.), the only change needed is registering a real tracer provider at startup. All call sites using `Tracer()` automatically get real spans.

### Trade-off: slog vs zerolog vs zap

| Library | Allocs/op | ns/op | External dep | stdlib |
|---------|-----------|-------|-------------|--------|
| slog (Go 1.21+) | low | ~300 | No | Yes |
| zerolog | near-zero | ~60 | Yes | No |
| zap | low | ~100 | Yes | No |

zerolog is 3–5× faster than slog for high-throughput structured logging. For V1 at projected peak load (<1,000 RPS per service), slog's 300 ns/op cost is unmeasurable against the DB call latency that dominates every request path. Adding zerolog or zap now would save approximately 0.0003ms per request while adding an external dependency that must be pinned, audited, and upgraded.

**Decision:** slog for V1. Revisit if any service sustains >10,000 RPS and profiling shows logging in the top-10 CPU consumers. The slog `Handler` interface is stable — a zerolog or zap backend can be dropped in as a `slog.Handler` implementation without changing any call site.

### Trade-off: Prometheus vs OpenTelemetry Metrics

OTel metrics are vendor-neutral and forward-compatible with every observability backend. Prometheus metrics require Prometheus or a compatible scraper. The argument for OTel metrics: lock-in avoidance. The argument for Prometheus: the platform runs on GCP, Cloud Monitoring natively scrapes Prometheus format, and the `prometheus/client_golang` library has a decade of production hardening that the OTel Go metrics SDK does not yet match.

**Decision:** Prometheus metrics in V1. If the platform moves to a non-Prometheus stack, the `prometheus/client_golang` library supports OTel Bridge export — both protocols from one registration. Not a migration, a config change.

---

## Ops

### Initialization

```go
// In each service main.go — call once, before starting the HTTP server
observability.MustRegisterMetrics()
logger := observability.NewLogger("raas")
slog.SetDefault(logger) // set as global logger — all stdlib log calls route here
```

`MustRegisterMetrics` panics if a metric name collides with an already-registered metric. This only happens if two packages both try to register the same metric name — which is a build-time bug, not a runtime condition. The panic surfaces it during integration testing, not in production.

### DB Pool Metrics Collection

DB pool metrics are collected on a background goroutine started by `internal/runtime.MustConnectDB`. The goroutine ticks every 15 seconds and writes to `DBPoolSize` and `DBPoolAcquireDuration`. It stops on context cancellation (graceful shutdown).

### Failure Modes

| Failure | Behaviour |
|---------|-----------|
| Prometheus registry collision | Panic at startup — fix the duplicate metric name |
| slog handler write error (stdout full) | slog drops the log line silently — this is a container infrastructure failure, not an application bug |
| OTel no-op tracer | `TraceIDFromContext` returns `""` — log lines carry empty `trace_id` field; no impact on functionality |
| `/metrics` endpoint slow | Prometheus scrape timeout — Grafana shows gap in data; no functional impact. If this happens, the metrics collection goroutine is blocked — investigate. |

---

## Compliance

### Log Sanitization

The following values must never appear in log output:

- JWT tokens or any substring of a token (`eyJ...`)
- `CANARY_ENCRYPTION_KEY` or any derived key material
- `JWT_SECRET` values
- `external_merchant_id` values (Restricted field per `go-security.md`)
- PCI cardholder data fields from event payloads

`LoggingMiddleware` logs `r.URL.Path` using the Chi route pattern — not the raw URL. Query parameters are never logged. This prevents merchant-supplied values in query strings (which may include external IDs) from appearing in logs.

Log lines are written to stdout. Container log collection routes them to Cloud Logging. Cloud Logging retention and access controls are the data plane security boundary — field-level redaction is not applied in this package.

### Metrics Cardinality and PII

Prometheus metric labels must not contain merchant IDs, user IDs, or any tenant-identifying values. Labels are stored in-process in Prometheus histograms for the full lifecycle of the process — a high-cardinality label set containing merchant IDs would constitute PII retention in process memory.

`RequestDuration` and `RequestCount` use `service`, `method`, `path` (route pattern only), and `status` as labels. None of these carry PII.

`actor_type` (`human` / `agent` / `system`) is added as a label on `RequestCount` only — its cardinality is bounded at three values, well below the cardinality threshold. This enables agent traffic share monitoring without per-actor identity exposure.

---

## Related

- [[go-runtime]] — `LoggingMiddleware` emits the standard fields defined here; `MustConnectDB` registers the DB pool metrics goroutine
- [[go-module-layout]] — `internal/observability/` package location
- [[go-security]] — log sanitization rules for secret values, JWT tokens, and Restricted fields
- [[go-errors]] — error model whose codes feed the `status` label on metrics
- [[microservice-architecture]] — service mesh whose call graph the trace_id propagation supports
- [[platform-overview]] — top-level observability posture
