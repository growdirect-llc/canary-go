---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
type: shared-library
package: internal/errors
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# go-errors — Platform Error Taxonomy

**Package:** `internal/errors`  
**Wraps:** stdlib `errors`  
**Imported by:** Every Canary Go service and shared library  
**Depends on:** stdlib only

Consistent error handling is not a style preference — it is the boundary between an API that clients can build against and one they cannot. `internal/errors` defines the full error taxonomy for the platform: typed error codes, structured context, HTTP status mapping, and the rules for what information flows to clients versus what stays in logs. Every service uses this package. No service defines its own error types.

---

## Business

### The Problem This Solves

Without a shared error taxonomy, services return inconsistent HTTP status codes for the same logical condition (one service returns 400 for a missing namespace, another returns 404), error bodies vary in structure across endpoints (clients can't parse them generically), and internal error details leak to clients in unguarded string formats. The result: integration partners write defensive code against every endpoint individually, and security reviews flag error responses as information disclosure vectors.

`internal/errors` solves all three at once. One taxonomy, one HTTP mapping, one client-facing body format. Partners integrate against a predictable contract. Security reviews have one place to audit.

### What Breaks Without It

- Client SDKs cannot map error responses to structured types — they string-match on `message`, which breaks on any wording change
- SQL error text (`pq: duplicate key value violates unique constraint`) reaches API consumers
- Different HTTP status codes for the same logical error across services — 409 from inventory, 422 from raas, 400 from tsp, all meaning "sequence violation"
- `pgx.ErrNoRows` propagates raw to JSON responses
- Stack traces in production error responses (PCI DSS finding in external audit)

---

## Technical

### Error Type

```go
// internal/errors/errors.go

type CanaryError struct {
    Code    ErrorCode       // typed enum — drives HTTP status mapping
    Message string          // internal diagnostic message — never sent to client
    Cause   error           // wrapped underlying error — for errors.Is/As unwrapping
    Fields  map[string]any  // structured context — logged, never serialized to client
}

func (e *CanaryError) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }
func (e *CanaryError) Unwrap() error { return e.Cause }
```

### Error Code Taxonomy

```go
type ErrorCode string

const (
    ErrNotFound          ErrorCode = "NOT_FOUND"
    ErrConflict          ErrorCode = "CONFLICT"           // sequence violation, duplicate key
    ErrUnauthorized      ErrorCode = "UNAUTHORIZED"       // no valid credentials
    ErrForbidden         ErrorCode = "FORBIDDEN"          // valid credentials, insufficient role
    ErrValidation        ErrorCode = "VALIDATION"         // malformed request, constraint violation
    ErrUpstream          ErrorCode = "UPSTREAM"           // external service failure (Square, NCR)
    ErrInternal          ErrorCode = "INTERNAL"           // unexpected; never expose details
    ErrInsufficientStock ErrorCode = "INSUFFICIENT_STOCK" // allocation request exceeds on-hand
    ErrSLABreach         ErrorCode = "SLA_BREACH"         // response time exceeded service threshold
    ErrChainViolation    ErrorCode = "CHAIN_VIOLATION"    // hash chain sequence broken
)
```

### HTTP Status Mapping

| ErrorCode | HTTP Status | Notes |
|-----------|------------|-------|
| `NOT_FOUND` | 404 | |
| `CONFLICT` | 409 | Also used for duplicate namespace registration |
| `UNAUTHORIZED` | 401 | Missing or expired JWT |
| `FORBIDDEN` | 403 | Valid JWT, insufficient role |
| `VALIDATION` | 422 | Unprocessable — malformed body, missing required field |
| `UPSTREAM` | 502 | Square/NCR/Shopify returned an error or timed out |
| `INTERNAL` | 500 | Default catch-all |
| `INSUFFICIENT_STOCK` | 409 | Business constraint — not a server error |
| `SLA_BREACH` | 503 | Service temporarily unable to meet latency contract |
| `CHAIN_VIOLATION` | 409 | Hash chain integrity failure — immutable constraint |

```go
// internal/errors/http.go
func HTTPStatus(err error) int {
    var ce *CanaryError
    if errors.As(err, &ce) {
        return statusMap[ce.Code]
    }
    return http.StatusInternalServerError
}
```

### Construction and Wrapping

```go
// Wrap an underlying error with typed context
func Wrap(cause error, code ErrorCode, message string, fields map[string]any) *CanaryError

// Construct with no underlying cause
func New(code ErrorCode, message string, fields map[string]any) *CanaryError

// Sentinel check — use errors.Is, not type assertion
func Is(err error, code ErrorCode) bool
```

**Usage pattern:**

```go
// In a repository function
row, err := q.GetNamespace(ctx, merchantID)
if errors.Is(err, pgx.ErrNoRows) {
    return nil, cerrors.Wrap(err, cerrors.ErrNotFound, "namespace not found",
        map[string]any{"merchant_id": merchantID})
}

// In a handler — log full error, send sanitized response
if err != nil {
    slog.ErrorContext(ctx, "get namespace failed",
        slog.Any("error", err),               // full CanaryError including Cause + Fields
        slog.String("request_id", runtime.RequestID(ctx)),
    )
    w.WriteHeader(cerrors.HTTPStatus(err))
    json.NewEncoder(w).Encode(cerrors.ClientBody(err, runtime.RequestID(ctx)))
    return
}
```

### Client-Facing Error Body

```go
// internal/errors/response.go
type ClientError struct {
    Code      string `json:"code"`
    Message   string `json:"message"`
}
type ErrorResponse struct {
    Error     ClientError `json:"error"`
    RequestID string      `json:"request_id"`
}

func ClientBody(err error, requestID string) ErrorResponse
```

The `Message` field in `ClientError` is a sanitized, human-readable string derived from the `ErrorCode` — not the internal `CanaryError.Message`. The mapping is defined in this package:

| ErrorCode | Client message |
|-----------|---------------|
| `NOT_FOUND` | `"resource not found"` |
| `CONFLICT` | `"conflict with existing resource"` |
| `UNAUTHORIZED` | `"authentication required"` |
| `FORBIDDEN` | `"insufficient permissions"` |
| `VALIDATION` | `"request validation failed"` |
| `UPSTREAM` | `"upstream service unavailable"` |
| `INTERNAL` | `"internal server error"` |
| `INSUFFICIENT_STOCK` | `"insufficient stock for allocation"` |
| `SLA_BREACH` | `"service temporarily unavailable"` |
| `CHAIN_VIOLATION` | `"event chain integrity violation"` |

Override the default client message by setting `CanaryError.Message` to a value prefixed with `"client:"` — the `ClientBody` function strips the prefix and uses the remainder as the client-facing string. This is the only sanctioned way to send a custom message to clients.

### Rules

1. **Never expose `Cause` or `Fields` in HTTP responses.** Internal details — SQL errors, pgx messages, upstream API bodies — stay in logs. `ClientBody` enforces this structurally: it reads only the `Code`.
2. **Always wrap with context.** Bare `return nil, err` propagation is forbidden in service code. Every error that crosses a package boundary must be wrapped with the appropriate `ErrorCode`, a message that identifies where it came from, and any structured fields useful for debugging.
3. **Use `errors.Is` for type checking, never string comparison.** `errors.Is(err, cerrors.ErrConflict)` works through the unwrap chain. `strings.Contains(err.Error(), "CONFLICT")` is fragile and prohibited.
4. **Log at the handler boundary, not in the call stack.** Intermediate functions wrap and return errors; they do not log them. The handler logs once with the full error. Logging at multiple levels produces duplicate entries with incomplete context.
5. **`ErrInternal` is the catch-all.** If the error doesn't fit a specific code, use `ErrInternal`. Do not create new error codes to avoid using `ErrInternal` on rare conditions.
6. **Domain-specific errors belong here.** `ErrInsufficientStock`, `ErrSLABreach`, and `ErrChainViolation` are defined at the platform level because multiple services produce and consume them. A service that adds error codes in its own package is fragmenting the taxonomy.

### Trade-off: Single Error Type vs Multiple Concrete Types

Go's standard approach to rich error handling is multiple concrete types with `errors.As`. The argument for it: type-safe field access without a `map[string]any`. The argument against: every service would need to import and switch on every other service's error types, which creates a dependency mesh.

`CanaryError` with a typed `ErrorCode` and untyped `Fields` is the correct choice for this platform. The `Fields` map is used only in logs — never in programmatic branching. Programmatic branching uses `errors.Is(err, cerrors.ErrNotFound)`, which is stable regardless of what's in `Fields`. If a future service needs type-safe field access on a specific error, that is a sign the field should be in a well-typed API response struct, not in the error type.

---

## Ops

### Failure Modes

| Scenario | Expected behaviour |
|----------|-------------------|
| Handler receives `pgx.ErrNoRows` unwrapped | Should not happen — repository layer wraps all pgx errors. If it does, RecoveryMiddleware catches it as a panic or the handler returns a 500 with no request_id in body. Add a test. |
| `ErrUpstream` from Square webhook timeout | 502 to caller; logged with full upstream response body in `Fields["upstream_body"]`; Square's retry logic handles re-delivery. |
| `ErrChainViolation` on raas append | 409 to caller; chain state not advanced; event rejected; caller must re-fetch current sequence and retry. |
| `ErrInternal` with nil Cause | Valid — some internal errors have no underlying cause. `ClientBody` still returns the correct sanitized body. |

---

## Compliance

### Information Disclosure Prevention

`ClientBody` is the single chokepoint between internal error state and client responses. It does not call `err.Error()`, does not serialize `Fields`, and does not expose `Cause`. Any modification to `ClientBody` that adds diagnostic detail must be reviewed for PCI DSS compliance (cardholder data must not appear in error responses) and GDPR compliance (merchant identifiers must not appear in error responses).

### Audit Trail

Every `ErrChainViolation` and `ErrSLABreach` is logged at `slog.LevelError` with `request_id`, `merchant_id`, and the full `Fields` payload. These log lines are the platform's primary signal for detecting chain integrity attacks and SLA degradation. Log lines must not be suppressed, sampled, or rate-limited for these two error codes.

---

## Related

- [[go-runtime]] — `RecoveryMiddleware` and `AuthMiddleware` produce `ErrInternal` and `ErrUnauthorized` errors via this taxonomy
- [[go-module-layout]] — `internal/errors/` package location
- [[go-security]] — `ErrUnauthorized` and `ErrForbidden` issued by JWT validation
- [[go-observability]] — error codes feed the `status` label on metrics; structured fields feed log payloads
- [[go-testing]] — error-path coverage requirement consumes this taxonomy
- [[microservice-architecture]] — REST contract uses the client-facing error body defined here
- [[platform-overview]] — top-level API contract posture
