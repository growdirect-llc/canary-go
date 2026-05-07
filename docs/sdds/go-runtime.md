---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
type: shared-library
package: internal/runtime
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# go-runtime — Platform Lifecycle Library

**Package:** `internal/runtime`  
**Imported by:** Every Canary Go service binary (`cmd/raas`, `cmd/inventory`, `cmd/tsp`, `cmd/owl`, `cmd/fox`, `cmd/hawk`, `cmd/identity`, `cmd/returns`, `cmd/webhook-pipeline`, `cmd/store-brain`, `cmd/analytics`, `cmd/notifications`, `cmd/bopis`)  
**Depends on:** stdlib (`context`, `net/http`, `os/signal`, `syscall`, `runtime/debug`, `log/slog`) plus `pgx`, `go-redis`, and `chi` for the type signatures on the lifecycle functions. No business-domain imports.

This library is the platform's shared lifecycle contract. Without it, every service re-implements shutdown, health checks, request IDs, panic recovery, and timeout handling independently — and they drift. `internal/runtime` is the place where that drift is impossible: one implementation, thirteen importers, one set of invariants.

---

## Business

### The Problem This Solves

A fleet of microservices that each handle SIGTERM differently is an ops liability. One service that ignores shutdown signals leaks DB connections and leaves transactions mid-flight; one that kills connections without draining in-flight requests returns 500s to clients at the worst possible moment — during a Square webhook batch. One service that lacks readiness probes gets traffic before its DB pool is warm and produces a cascade of connection errors on cold start.

`internal/runtime` enforces one pattern across all services: signal handling, health probes, middleware ordering, context key discipline, and GC tuning are solved once and never revisited per-service. Every engineer working on any module imports this package and gets all of it.

### What Breaks Without It

- Inconsistent `/healthz` vs `/readyz` implementations that load balancers can't rely on
- Different shutdown timeout values per service, some of which leave Cloud Run instances alive past their billing period
- Request IDs that don't propagate consistently, making distributed traces useless for cross-service debugging
- Panics that aren't recovered, crashing a service pod instead of returning a 500
- p99 latency jitter on heap-heavy services that didn't tune GC parameters

### Uniform Treatment of Human and Agent Traffic

Every Canary service handles both human-driven and agent-driven requests through the same middleware. The runtime stamps `ActorType` on every request — `human`, `agent`, or `system` — and propagates it through context, logs, and traces. This is what makes the agent accountability model auditable: every agent action is attributable, every human action is attributable, and the platform treats them as the same shape of event. A divergent middleware stack for agents would produce two parallel observability surfaces and break the meter model on which the platform thesis depends.

---

## Technical

### GC Tuning

Two levers, both required. The default Go GC (GOGC=100) triggers a collection when the heap doubles its post-GC live size — appropriate for batch workloads, wrong for latency-sensitive services under sustained append load.

**`GOGC=20`** — trigger GC when heap grows 20% above live data. More frequent collections with smaller individual pauses. Set via environment variable; the runtime package validates this is set on startup and logs a warning if it is not.

**`GOMEMLIMIT`** — set to 90% of container memory limit via `runtime/debug.SetMemoryLimit`. Prevents the Go runtime from allowing the heap to grow toward OOM before triggering a GC. The value is read from `GOMEMLIMIT_BYTES` env var; if absent, the runtime reads `CONTAINER_MEMORY_LIMIT_BYTES` and applies the 90% rule automatically.

In Go 1.21+, `GOMEMLIMIT` is the primary lever — it prevents OOM kills. `GOGC=20` is the latency-tuning knob on top of it.

**Trade-off: GOGC=20 vs default GOGC=100**

| Setting | GC CPU overhead | p99 pause reduction | Right for |
|---------|----------------|---------------------|-----------|
| GOGC=100 (default) | baseline | baseline | analytics, batch jobs |
| GOGC=20 | +15–20% CPU | −30–40ms on heap-heavy loads | raas, inventory, tsp |

For `analytics` (batch-heavy, latency-insensitive), the service should override to GOGC=100 via its own env config. The runtime package does not override GC settings for individual services — it reads the env var and logs the active values at startup so engineers can audit what's running.

### Graceful Shutdown

All services call `runtime.Run(...)` as their main loop. The function blocks on SIGINT/SIGTERM, then drains the HTTP server before closing the DB pool and Valkey client.

```go
// internal/runtime/shutdown.go
func Run(
    ctx context.Context,
    srv *http.Server,
    pool *pgxpool.Pool,
    redis *redis.Client,
    timeout time.Duration,
) error {
    ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()
    srv.Shutdown(shutdownCtx)
    pool.Close()
    redis.Close()
    return nil
}
```

Default shutdown timeout: 30 seconds. Override via `SHUTDOWN_TIMEOUT_SECONDS` env var. If the drain takes longer than the timeout, `srv.Shutdown` returns an error — the function logs it and closes the pool regardless. In-flight requests that did not complete within the timeout window receive a 503 from the load balancer, not a connection reset.

**Shutdown order is not negotiable:** HTTP drain before DB close. Closing the pool while the HTTP server is still serving will produce `pgx: pool closed` errors on any request that hits a DB call during drain. The opposite order is always safe.

### Health Endpoints

Registered by the shared runtime on every service's Chi router. Services do not implement these themselves.

| Endpoint | Behaviour | Probe type |
|----------|-----------|------------|
| `GET /healthz` | Always 200 if process is alive. No DB or Valkey check. Returns 503 only on internal deadlock detected via the runtime monitor goroutine. | Liveness |
| `GET /readyz` | Pings DB pool (`pgxpool.Ping`) and Valkey (`PING`). Returns 503 if either fails. Removes instance from load balancer without killing it. | Readiness |
| `GET /metrics` | Prometheus scrape target. Registered here; metrics defined in `internal/observability`. Unauthed — internal network only. | Scrape |

Readiness failures during a DB failover are expected and correct: the instance stops receiving traffic, waits for the pool to reconnect, then returns 200 on the next probe cycle. Do not add retry logic to `/readyz` — the probe interval handles retry.

### Context Keys

All context keys are typed integers. No string keys. String context keys are ungoverned; any package can shadow them. Typed keys are unexported from this package and accessible only via the accessor functions.

```go
// internal/runtime/context.go
type contextKey int

const (
    ContextKeyRequestID contextKey = iota
    ContextKeyMerchantID
    ContextKeyActorID
    ContextKeyActorType
    ContextKeyTraceID
)

// Accessor functions — use these, not direct context.Value() calls
func RequestID(ctx context.Context) string
func MerchantID(ctx context.Context) uuid.UUID
func ActorID(ctx context.Context) string
func ActorType(ctx context.Context) string   // "human" | "agent" | "system"
func TraceID(ctx context.Context) string
```

Accessors return zero values (`""`, `uuid.Nil`) when the key is absent — they do not panic. Callers that require a non-zero value must check and handle the zero case explicitly.

### Middleware Stack

Applied in this order on every service router. Order is not configurable per-service — the stack is deterministic.

```
1. RequestIDMiddleware    — generate or propagate X-Request-ID; inject into context
2. TimeoutMiddleware(d)   — cancel context after d; return 503 on timeout
3. RecoveryMiddleware     — recover panics; log stack trace; return 500
4. LoggingMiddleware      — log: method, path, status, duration_ms, request_id, merchant_id, actor_id, actor_type
5. AuthMiddleware         — JWT validation (delegates to internal/security)
```

`TimeoutMiddleware` duration is set per-service via `SERVICE_REQUEST_TIMEOUT_MS` env var. Default: 5000ms. RaaS and raas-adjacent services that do hash chain appends should set this to 10000ms — the append path under contention can take 3–4 seconds at high queue depth.

`RecoveryMiddleware` logs the full stack trace at `slog.LevelError` with the request_id attached. The client receives a generic `{"error": {"code": "INTERNAL", "message": "internal server error"}, "request_id": "..."}` — never a stack trace.

`AuthMiddleware` is registered last in the chain but applies to every route except `/healthz`, `/readyz`, and `/metrics`. Those three bypass auth by route pattern — no explicit exemption needed per handler.

### Trade-off: Shared Middleware Stack vs Per-Service Configuration

The tradeoff is flexibility vs correctness. A configurable stack lets engineers skip AuthMiddleware on a per-route basis — which is also how auth bypasses get introduced silently. The fixed stack prevents that class of mistake. Any route that genuinely must be unauthed (`/healthz`, `/readyz`, `/metrics`, POS webhook ingestion endpoints) is declared as an explicit exception in this package, reviewed once, and documented here. Engineers do not make this decision at the service level.

---

## Ops

### Startup Initialization Sequence

Every service `main.go` calls these in order:

```go
runtime.InitGC()                     // validate and apply GOGC + GOMEMLIMIT
runtime.InitLogger()                 // configure slog JSON handler, log active settings
pool := runtime.MustConnectDB(cfg)   // pgxpool.Connect with retry; fatal if unreachable after 30s
redis := runtime.MustConnectValkey(cfg)  // redis.NewClient; PING with retry
router := runtime.NewRouter(cfg)     // Chi router with full middleware stack registered
// ... register service-specific routes ...
srv := runtime.NewServer(cfg, router)
runtime.Run(ctx, srv, pool, redis, runtime.ShutdownTimeout(cfg))
```

`MustConnectDB` and `MustConnectValkey` are retry-with-backoff wrappers that log progress and call `os.Exit(1)` after the retry budget is exhausted. They do not return an error — a service that cannot reach its DB at startup is not a partial-start scenario, it is a failed deployment.

### Failure Modes

| Failure | Behaviour |
|---------|-----------|
| SIGTERM received | Drain HTTP (30s default), close pool, close Valkey, exit 0 |
| SIGKILL received | Immediate termination — no drain. Cloud Run sends SIGTERM first with a 30s buffer before SIGKILL; drain must complete within that window. |
| DB pool exhausted | Requests queue until timeout; `/readyz` returns 503; load balancer removes instance |
| Valkey unreachable | `/readyz` returns 503; session-dependent operations fail with ErrUpstream |
| Panic in handler | RecoveryMiddleware catches; logs stack; returns 500; service continues running |
| Panic in goroutine outside handler | Uncaught — service crashes. All goroutines launched in service code must have their own recover() wrapper. |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GOGC` | `20` | GC trigger percentage. Override to `100` for analytics. |
| `GOMEMLIMIT_BYTES` | — | Explicit memory limit. If absent, reads `CONTAINER_MEMORY_LIMIT_BYTES` × 0.90. |
| `CONTAINER_MEMORY_LIMIT_BYTES` | — | Container memory limit; used only if `GOMEMLIMIT_BYTES` is unset. |
| `SHUTDOWN_TIMEOUT_SECONDS` | `30` | HTTP drain timeout before pool close. |
| `SERVICE_REQUEST_TIMEOUT_MS` | `5000` | Context cancellation deadline per request. |
| `LOG_LEVEL` | `info` | slog level: `debug`, `info`, `warn`, `error`. |

---

## Compliance

### Context Key Discipline

`ContextKeyMerchantID` and `ContextKeyActorID` are the platform's primary attribution fields. They are injected by `AuthMiddleware` from the validated JWT and used by `LoggingMiddleware` to stamp every request log. Handlers must never inject these keys directly — that would allow a handler to forge attribution. The keys are unexported; only the accessor functions are exported.

### Panic Recovery and Information Exposure

`RecoveryMiddleware` logs stack traces at ERROR level. Stack traces must not reach clients. The middleware catches the recovered value, formats it for the log, and returns a generic 500 body. Any engineer who modifies RecoveryMiddleware to pass error details to the client is introducing an information disclosure vulnerability — this is explicitly prohibited.

### Health Endpoint Security

`/healthz` and `/readyz` are unauthed by design: the load balancer's health-check agent does not carry a JWT. These endpoints must expose no merchant data, no DB schema details, and no configuration values. The current implementation returns only `{"status": "ok"}` or `{"status": "unavailable", "reason": "db"}`. Adding diagnostic detail to these endpoints is prohibited.

---

## Related

- [[go-module-layout]] — package layout, port assignments, binary naming conventions
- [[go-security]] — auth, encryption, secret loading, PII hashing primitives consumed by `AuthMiddleware`
- [[go-observability]] — slog conventions, trace propagation, metrics naming consumed by `LoggingMiddleware`
- [[go-testing]] — test harness conventions for services importing this runtime
- [[go-errors]] — error model emitted by `RecoveryMiddleware` and `AuthMiddleware`
- [[microservice-architecture]] — service mesh, startup order, and lifecycle gates this runtime implements
- [[platform-overview]] — top-level product context for the services that import this runtime
