---
title: Canary Go — Monorepo Conventions
type: conventions
status: active
date: 2026-05-03
linear: GRO-763
authority: Loop 4 Wave A Phase A.1 (codifies de-facto Loop 2 patterns)
last-compiled: 2026-05-03
needs-review: 2026-08-03
---

# Canary Go — Monorepo Conventions

Loop 2 stood up seven working modules without a written convention; Loop 3 Wave 1 added decimal substrate, timezone fields, tender-types seed, and audit infrastructure. This doc codifies the patterns that emerged so module work in Wave B and later loops doesn't re-discover them.

If a section disagrees with what's in `internal/<module>/`, the code wins and this doc gets a follow-up patch — but new modules must follow what's written here.

## Package layout

Each domain module lives under `internal/<module>/`. Standard files:

| File | Purpose |
|---|---|
| `handler.go` | HTTP layer: parse → delegate → render. Thin. No business logic. |
| `handler_test.go` | Handler-level unit tests (table-driven HTTP request/response). |
| `store.go` | DB access. pgxpool-backed. Concrete `*Store` plus interface(s) for handler injection. |
| `store_inserts.go` | Optional second store file when insert paths are large enough to merit a split. |
| `dto.go` | Wire-shape types — request/response structs with JSON tags. |
| `types.go` | Domain types when DTO ↔ entity diverge meaningfully. |
| `<noun>.go` | Per-entity helpers (`movement.go`, `position.go`, `promotions.go`, `tax.go`). |
| `integration_test.go` | `//go:build integration` — exercises real Docker postgres. |

`cmd/<service>/` contains the binary main package: `main.go`, `server.go`, `handlers.go` for service-level wiring (chi router, middleware, health endpoint). Domain logic stays in `internal/`.

## HTTP handler conventions

**Router:** [chi v5](https://github.com/go-chi/chi). Each domain package exposes `Mount(r chi.Router)` that registers its routes.

**Path style:** `/v1/<resource>[/{id}]`, lower-snake-case path segments. Most-specific routes first (`/v1/items/by-barcode` before `/v1/items/{id}`).

**Tenant scope:** every authenticated route pulls tenant from one of three sources (in order of preference):

1. Request context — set by `internal/auth` JWT middleware (preferred for new modules)
2. `X-Canary-Merchant` header — protocol gateway convention; used by `internal/protocol/*` and `internal/inventory/`
3. `?tenant_id=` or `?merchant_id=` query parameter — admin and POS-scan endpoints (e.g., `internal/item/`)

New modules added in Wave B+ should prefer context-from-JWT; query/header is the migration tolerance for callers built against pre-auth contracts.

**Pagination:** accept `?page=N&size=M` (1-indexed page, 50/200 default/max). The `pageSize(r)` helper in `internal/inventory/handler.go` is the canonical implementation. Some modules also accept `?limit=&offset=` — keep both query forms when retrofitting.

**Response envelope (success):**

```json
{ "items": [...], "page": 1, "size": 50, "count": 17 }
```

Single-resource reads return the resource directly (no envelope).

**Error envelope (every non-2xx):**

```json
{ "code": "snake_case_error_code", "message": "human-readable detail (optional)" }
```

The `errorBody` struct + `writeError(w, status, code, message)` helper appears in every module's `handler.go`. Codes are stable wire identifiers — clients match on them. Messages are advisory.

**Status code mapping (handler-level via `renderStoreError`):**

| Sentinel | Status |
|---|---|
| `ErrNotFound` | 404 `not_found` |
| `ErrConflict` | 409 `conflict` |
| `ErrValidation` | 400 `invalid_request` |
| (other) | 500 `internal_error` (logged with `zap.Error`, message redacted) |

**Body size:** every JSON body read goes through `http.MaxBytesReader(w, r.Body, 1<<20)` (1 MiB) unless the handler documents a larger budget.

## Store interface pattern

Store layer is pgxpool-backed. Each module exposes:

1. A concrete `*Store` constructed via `NewStore(pool *pgxpool.Pool) *Store`.
2. One or more interfaces (`Reader`, `Writer`, or method-named) that the handler accepts. The concrete `*Store` satisfies them all; tests substitute a stub.

Example (from `internal/inventory/`):

```go
type PositionReader interface {
    GetPosition(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (*PositionDTO, error)
    ListPositions(ctx context.Context, ...) ([]PositionDTO, error)
}

type MovementWriter interface {
    AppendMovement(ctx context.Context, req AppendMovementRequest, t time.Time) (*MovementDTO, *PositionDTO, error)
    ListMovements(ctx context.Context, ...) ([]MovementDTO, error)
}

type Handler struct {
    Reader PositionReader
    Writer MovementWriter
    ...
}
```

Domain sentinels live next to the store:

```go
var ErrPositionNotFound = errors.New("inventory: position not found")
```

The handler's `renderStoreError` maps these to HTTP status codes. Sentinels are package-scoped — no shared `errs` package.

**Transaction discipline:** multi-statement writes use `pool.BeginTx` with `IsoLevel: pgx.ReadCommitted`. The `defer tx.Rollback(ctx)` is a safe no-op after commit.

**Scan helpers:** when the same `SELECT` appears in QueryRow + Query iterations, factor a `scanXxx(r scannable)` helper using the small interface:

```go
type scannable interface { Scan(dest ...any) error }
```

## Adapter pattern

POS-vendor adapters live under `internal/adapters/<source>/`:

```
internal/adapters/
  square/
    parser.go    — wire (Square API JSON) → canonical (db types / DTOs)
    parser_test.go
    lookup.go    — vendor-id ↔ canonical-id translation
  counterpoint/
    parser.go
    parser_test.go
  clover/
    parser.go
    ...
```

Naming rule: `parser.go` translates external wire format to canonical. Lookup shims handle vendor-id resolution against `m.external_identifiers` (or whichever shared resolver lands in Wave B). Adapters never write to the DB directly — they produce canonical DTOs, and the calling module's store does the persistence.

## Test layout

**Unit tests:** alongside the code in `*_test.go`. Default for handler tests, parser tests, validation, business logic. Run on every `go test ./...`.

**Integration tests:** `_test.go` files with `//go:build integration` at the top. Exercise real postgres (Docker) and Valkey. Required env vars at minimum:

```
GATEWAY_TEST_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_go_test?sslmode=disable
GATEWAY_TEST_VALKEY_URL=redis://:valkey_dev@localhost:6379/2
```

Run via `go test -tags=integration ./...`. CI runs both passes; the integration job depends on a postgres service container.

**Tier-3 (excluded) tests:** when a module's tests are pre-existing failures the current dispatch chooses not to fix, they get a `Tier-3` tag in the build tags and an excluded-by-dispatch comment at the top of the file. Do not let Tier-3 spread — every dispatch should aim to either fix or remove. See `cmd/identity/main_test.go` for the Loop 2 + Loop 3 + Wave A example (now resolved by Phase C).

## Decimal handling

[`github.com/shopspring/decimal`](https://github.com/shopspring/decimal) v1.4.0+, exposed as `internal/db/types.Decimal` (direct alias).

Authority: OQ Resolution Pack §A.1 OQ-2.3. Full reasoning + per-module retrofit roadmap: [`Brain/wiki/cards/loop3-decimal-standard.md`](../../Brain/wiki/cards/loop3-decimal-standard.md).

**Rules:**

- All money fields scan/value as `Decimal` against `numeric(N,M)` columns (default `numeric(14,4)`).
- Boundary parsing goes through `types.NewDecimalFromString`, `types.NewDecimalFromFloat`, `types.NewDecimal(value, exp)`.
- Wire format remains string ("12.34") — `Decimal.MarshalJSON` handles this.
- Module-specific rounding: `RoundCash` for cash drawers, `Round(4)` for percent math, `StringFixed(2)` for display.
- Currency travels alongside as a separate `string` field — no `Money` wrapper struct.

New code lands using `Decimal` directly; legacy `*string` for `numeric` columns is a Loop 3 Wave 2 retrofit (see decimal-standard card for ordered targets).

## Soft-FK convention (per Loop 3 backlog #12)

Some columns reference rows in another schema but carry no formal `REFERENCES` constraint. They are documented with a comment:

```sql
related_employee_id uuid,  -- soft-FK to e.employees(id)
```

**When to use a soft-FK:**

- The referenced row may not exist yet at insert time (race against an upstream sync).
- Cascade semantics across schemas would be expensive (large transaction-side dependency graphs).
- The referenced table belongs to a domain whose write path is owned by a different module and constraint failure would create cross-module coupling.

**When to formalize:**

- Once the referenced table has stabilized AND insertion order is deterministic AND there's a real risk of orphaned rows in production.
- Loop 3 backlog item #12 lists `q.subjects.related_employee_id`, `q.subjects.related_customer_id`, `q.subjects.related_vendor_id`, and `q.detections.cashier_employee_id` as the four soft-FKs scheduled for promotion.

**Documentation requirement:** every soft-FK gets a `-- soft-FK to <schema>.<table>(<column>)` comment in the schema file. Grep finds them.

## sqlc rule reconciliation (per Loop 3 backlog #15)

The CanaryGo `CLAUDE.md` historically said "all queries go through sqlc." Loop 2 shipped seven modules with direct pgx + raw SQL because the dispatch overrode the rule. Wave A reconciles.

**Rule (effective 2026-05-03):**

- **Read paths** — direct pgx is acceptable. Inline SQL with `pool.QueryRow`/`pool.Query` and a `scanXxx` helper is the canonical pattern.
- **Simple writes** — single `INSERT`/`UPDATE`/`DELETE` with no transaction composition: direct pgx is acceptable.
- **Complex writes** — multi-statement transactions, cross-table coordination, or write paths exercised by ≥3 distinct callers SHOULD use sqlc-generated queries. The threshold is human-judged; if you're unsure whether sqlc is warranted, the answer is "use it."
- **New modules** — add `internal/db/sqlc/<module>.sql` for any sqlc-required path. Run `make sqlc-gen`. Generated output lives in `internal/db/query/<module>/` and is not hand-edited.

The de-facto retrofit catalog — modules whose write paths cross the threshold and should be migrated to sqlc — is tracked in Loop 3 backlog #15. Wave B picks up `internal/sub2/` (transaction pipeline) as the first retrofit target since it owns the write path most callers traverse.

`CanaryGo/CLAUDE.md` is updated alongside this doc to reflect the amended rule.

## Configurability discipline (standing meta-rule §2)

No hardcoded thresholds, defaults, tiers, or cadences. Every such value gets one of:

1. **Config table row** — for per-tenant overrides or per-archetype tiers (e.g., `f.markup_envelope_tiers`, `f.tender_types`).
2. **`app.merchant_settings` column** — for per-merchant policy toggles (e.g., `de_merge_audit_visibility`, `wo_loop_close_window_days`).
3. **`app.tenants.attributes` JSONB key** — for ad-hoc per-tenant overrides without schema change.
4. **Env var with documented default** — for service-level operational knobs (e.g., `OTEL_SAMPLE_RATE`, `IDENTITY_JWKS_CACHE_TTL_SECONDS`).
5. **Feature flag** — when the value is a boolean toggle that should be controllable without redeploy.

The mechanism MUST be documented in the artifact that introduces the value. `docs/superpowers/plans/2026-05-03-oq-resolution-pack.md` §A.3 holds the running configuration registry.

## Open-source standard documentation (standing meta-rule §1)

Whenever a library or pattern enters the codebase, cite the spec/package URL inline:

- Library imports: comment with the upstream URL on the import line if it's the canonical project.
- Pattern adoption: link the spec (`pg_advisory_lock`, `OAuth 2.0 RFC 6749`, `IANA tz database`).
- New convention: the doc that introduces it includes the URL of the precedent.

Examples already in-repo: `shopspring/decimal`, `pg_advisory_lock`, `Wazuh`, IANA tz, OpenTelemetry, OAuth 2.0 + OIDC.

## Cross-references

- [`CanaryGo/CLAUDE.md`](../CLAUDE.md) — short agent-context sheet; references this doc
- [`Brain/wiki/cards/loop3-decimal-standard.md`](../../Brain/wiki/cards/loop3-decimal-standard.md) — decimal standard
- [`Brain/wiki/cards/loop2-build-report.md`](../../Brain/wiki/cards/loop2-build-report.md) — Loop 2 closure + top-10 findings
- [`docs/superpowers/plans/2026-05-03-oq-resolution-pack.md`](../../docs/superpowers/plans/2026-05-03-oq-resolution-pack.md) — 22 founder-approved decisions
- [`docs/superpowers/plans/2026-05-03-loop3-backlog.md`](../../docs/superpowers/plans/2026-05-03-loop3-backlog.md) — 15-item priority backlog
- [`docs/sdds/go-handoff/go-observability.md`](../../docs/sdds/go-handoff/go-observability.md) — observability SDD (full spec)
- [`docs/sdds/go-handoff/go-module-layout.md`](../../docs/sdds/go-handoff/go-module-layout.md) — service inventory + port assignments
