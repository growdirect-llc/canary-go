# Cross-Tenant IDOR Hardening Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Status note (2026-05-10):** This plan is retained as the historical implementation plan for the bundled GRO-904 / GRO-905 / GRO-916 IDOR work. Where it conflicts with the active unified dispatch or the CK2 close-out plan, follow `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md` and `docs/superpowers/plans/2026-05-09-phase3-ck2-and-pipeline.md`. In particular, CK2's formal acceptance scope is the data-layer tenant boundary plus cross-tenant tests; cmd-binary `APIKeyMiddleware` wiring was beneficial hardening in PR #6, but not a CK2 gate after the SDD-013 reframing.

**Goal:** Close GRO-904 (fox unauthenticated + IDOR), GRO-905 (chirp/item/inventory/transaction/pricing trust caller-supplied tenant_id), and GRO-916 (cross-tenant negative test coverage) by bundling the fix and the regression-blocking test together for each affected service.

**Architecture:** TDD red-first per service. For each service: (1) add cross-tenant negative test exercising the public surface, (2) replace `tenantFromQuery` / `tenantFromHeader` / body-derived tenant with `identity.ClaimsFromContext`, (3) add `tenant_id` predicate to any store function still missing it, (4) keep tests green. The original bundle also wired `identity.APIKeyMiddleware` into cmd binaries where absent; that remains valid hardening, but it should be read as extra scope relative to the narrowed CK2 gate. Five service slices land sequentially via subagent dispatch — they touch disjoint files (different `cmd/<svc>/main.go`, different `internal/<svc>/handler.go`, different `internal/<svc>/store.go`).

**Tech Stack:** Go 1.22+, Chi v5, pgx/v5, `internal/identity` (existing primitives), `internal/testutil`, `httptest`. PostgreSQL 17 test DB via `DATABASE_URL` for store tests; integration tests use `//go:build integration` + `GATEWAY_TEST_DATABASE_URL` per existing convention.

**Issues closed:**
- GRO-904 (Critical) — fox service
- GRO-905 (Critical) — chirp / item / inventory / transaction (pricing not yet a service)
- GRO-916 (Medium) — cross-tenant negative test coverage

**Reference patterns:**
- Store-level isolation test: [internal/lp/allowlist_test.go:217](../../../internal/lp/allowlist_test.go) (`TestAllowListStore_TenantIsolation`)
- Middleware wiring: [cmd/transaction/main.go:55](../../../cmd/transaction/main.go)
- Body-tenant assertion: `identity.AssertBodyTenantMatches` ([internal/identity/context.go:91](../../../internal/identity/context.go))
- Claims injection in tests: `identity.InjectClaims(ctx, identity.Claims{TenantID: tenantA, AuthMethod: identity.AuthMethodAPIKey})`

**Out of scope:**
- GRO-910 (webhook DLQ admin tenant scoping) — separate handler surface, distinct issue
- GRO-919 (CK2 checkpoint) — process gate, closes after these merge
- pricing service — does not exist as a separate service in this build

---

## Chunk 1: Shared scaffolding (Phase 0)

The five service slices share a small set of helpers. Lift them once so each per-service slice doesn't re-roll.

### Task 0.1: Lift `seedTenant` to `internal/testutil`

**Files:**
- Modify: `internal/testutil/db.go` (add `SeedTenant`)
- Modify: `internal/lp/allowlist_test.go` (delete local `seedTenant`, call `testutil.SeedTenant`)

**Why:** Five new tests will need `seedTenant`. It currently lives only in `internal/lp/allowlist_test.go:20`. Lifting to `testutil` means each service's new test references one helper instead of duplicating fixture inserts.

- [ ] **Step 1: Read the existing helper.**

```bash
sed -n '15,45p' internal/lp/allowlist_test.go
```

Capture exact body — must reproduce identically (org → tenant insert with `schema_name`).

- [ ] **Step 2: Add `SeedTenant` to `internal/testutil/db.go`.**

Append to the file:

```go
// SeedTenant inserts a fresh organization + tenant pair and returns the
// tenant id. Cleanup is delegated to test-level TRUNCATE; callers that
// need narrower cleanup should TRUNCATE app.tenants + app.organizations
// themselves. Returns the tenant_id.
func SeedTenant(t *testing.T, ctx context.Context) uuid.UUID {
    t.Helper()
    pool := MustConnect(t)
    orgID := uuid.New()
    tenantID := uuid.New()
    short := tenantID.String()[:8]
    if _, err := pool.Exec(ctx,
        `INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
        orgID, "testutil seed org "+short); err != nil {
        t.Fatalf("testutil: seed org: %v", err)
    }
    if _, err := pool.Exec(ctx,
        `INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
         VALUES ($1, $2, $3, $4, $5)`,
        tenantID, orgID, "tu-"+short, "testutil tenant "+short,
        "tenant_testutil_"+short); err != nil {
        t.Fatalf("testutil: seed tenant: %v", err)
    }
    return tenantID
}
```

Required imports: `"github.com/google/uuid"`. Keep existing imports (`context`, `os`, `testing`, `pgxpool`).

- [ ] **Step 3: Delete the local helper in `internal/lp/allowlist_test.go`.**

Replace the local `seedTenant` definition (lines 18–~38) and every `seedTenant(t, ctx)` call site with `testutil.SeedTenant(t, ctx)`. Add `"github.com/ruptiv/canary/internal/testutil"` to the import block if not present. The reference test stays semantically identical.

- [ ] **Step 4: Run the lp tests.**

```bash
DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable' \
  go test ./internal/lp/...
```

Expected: PASS (the lifted helper produces identical seed rows).

- [ ] **Step 5: Commit.**

```bash
git add internal/testutil/db.go internal/lp/allowlist_test.go
git commit -m "test(testutil): lift SeedTenant helper from internal/lp"
```

### Task 0.2: Add `internal/testutil` claims helper for handler-level tests

**Files:**
- Modify: `internal/testutil/db.go` (add `WithAPIKeyClaims`)

**Why:** Each handler-level cross-tenant test needs to inject claims onto the test request without standing up the API-key DB row + middleware. Wrapping `identity.InjectClaims` in a one-liner keeps the test bodies short.

- [ ] **Step 1: Add the helper.**

Append to `internal/testutil/db.go`:

```go
// WithAPIKeyClaims returns a context carrying a minimal API-key Claims
// record for tenantID. Use to construct test requests that assert
// handler behaviour against tenant scoping without spinning up the
// APIKeyMiddleware DB lookup. Mirrors identity.InjectAPIKeyClaims but
// takes the resolved tenant directly.
func WithAPIKeyClaims(ctx context.Context, tenantID uuid.UUID) context.Context {
    return identity.InjectClaims(ctx, identity.Claims{
        TenantID:   tenantID,
        AgentName:  "test-agent",
        AuthMethod: identity.AuthMethodAPIKey,
    })
}
```

Add `"github.com/ruptiv/canary/internal/identity"` to the imports.

- [ ] **Step 2: Compile-check.**

```bash
go build ./internal/testutil/...
```

Expected: clean build.

- [ ] **Step 3: Commit.**

```bash
git add internal/testutil/db.go
git commit -m "test(testutil): add WithAPIKeyClaims helper for handler tests"
```

---

## Chunk 2: Phase 1 — fox (GRO-904)

Fox is the canonical pattern; it has the worst exposure (cmd binary mounts handler with zero auth) and the cleanest fix. Subsequent phases reference this template.

### Task 1.1: Store-level cross-tenant negative test (TDD red)

**Files:**
- Create: `internal/fox/store_tenant_isolation_test.go`

**Why:** `LoadDetection` ([internal/fox/store.go:44](../../../internal/fox/store.go)) and `LoadCase` ([internal/fox/store.go:71](../../../internal/fox/store.go)) query `WHERE id = $1` with no tenant predicate. A store-level test asserting cross-tenant Get returns `ErrNotFound` will fail until we add the predicate.

- [ ] **Step 1: Write the test file.**

```go
package fox

import (
    "context"
    "errors"
    "testing"

    "github.com/google/uuid"

    "github.com/ruptiv/canary/internal/testutil"
)

func TestStore_TenantIsolation_LoadCase(t *testing.T) {
    ctx := context.Background()
    tenantA := testutil.SeedTenant(t, ctx)
    tenantB := testutil.SeedTenant(t, ctx)
    pool := testutil.MustConnect(t)
    store := NewStore(pool)

    // Seed a case for tenantA via direct SQL (bypassing handler logic).
    caseID := uuid.New()
    if _, err := pool.Exec(ctx,
        `INSERT INTO detection.cases (id, tenant_id, case_number, case_type, title, severity, status)
         VALUES ($1, $2, 'C-LEAK-1', 'shrink', 'leak test', 'high', 'open')`,
        caseID, tenantA); err != nil {
        t.Fatalf("seed case: %v", err)
    }
    t.Cleanup(func() {
        _, _ = pool.Exec(ctx, `DELETE FROM detection.cases WHERE id = $1`, caseID)
    })

    // tenantB attempts to load tenantA's case by its leaked id.
    if _, err := store.LoadCaseScoped(ctx, tenantB, caseID); !errors.Is(err, ErrNotFound) {
        t.Errorf("tenantB LoadCaseScoped of tenantA id: want ErrNotFound, got %v", err)
    }
    // tenantA still owns it.
    got, err := store.LoadCaseScoped(ctx, tenantA, caseID)
    if err != nil {
        t.Fatalf("tenantA LoadCaseScoped: %v", err)
    }
    if got.ID != caseID {
        t.Errorf("got id=%v, want %v", got.ID, caseID)
    }
}
```

Note: `LoadCaseScoped` does not yet exist — that's the red part.

- [ ] **Step 2: Run the test, observe the failure.**

```bash
DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable' \
  go test ./internal/fox/ -run TestStore_TenantIsolation
```

Expected: FAIL — `LoadCaseScoped undefined`.

### Task 1.2: Add tenant-scoped reads to fox store (green)

**Files:**
- Modify: `internal/fox/store.go` (add `LoadCaseScoped`, `LoadDetectionScoped`; deprecate raw versions or add tenant param)

**Why:** Closes GRO-904 fix item #2.

- [ ] **Step 1: Add `LoadCaseScoped` and `LoadDetectionScoped`.**

Insert after the existing `LoadDetection`/`LoadCase` definitions:

```go
// LoadCaseScoped is the tenant-aware variant. Returns ErrNotFound when
// the case exists but belongs to a different tenant — same shape as a
// genuine miss to avoid leaking existence cross-tenant. Prefer this
// over LoadCase in any handler that has a Claims context.
func (s *Store) LoadCaseScoped(ctx context.Context, tenantID, id uuid.UUID) (*types.Case, error) {
    const q = `
        SELECT id, tenant_id, case_number, case_type, title, description,
               severity, status, primary_subject_id, primary_location_id,
               assigned_to, opened_at, resolved_at, resolution_type,
               loss_amount_estimated, loss_amount_recovered, attributes,
               created_at, updated_at
          FROM detection.cases
         WHERE id = $1 AND tenant_id = $2`
    row := s.pool.QueryRow(ctx, q, id, tenantID)
    var c types.Case
    err := row.Scan(
        &c.ID, &c.TenantID, &c.CaseNumber, &c.CaseType, &c.Title, &c.Description,
        &c.Severity, &c.Status, &c.PrimarySubjectID, &c.PrimaryLocationID,
        &c.AssignedTo, &c.OpenedAt, &c.ResolvedAt, &c.ResolutionType,
        &c.LossAmountEstimated, &c.LossAmountRecovered, &c.Attributes,
        &c.CreatedAt, &c.UpdatedAt,
    )
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("fox.LoadCaseScoped: %w", err)
    }
    return &c, nil
}

func (s *Store) LoadDetectionScoped(ctx context.Context, tenantID, id uuid.UUID) (*types.Detection, error) {
    const q = `
        SELECT id, tenant_id, rule_id, detected_at, source_entity_type,
               source_entity_id, location_id, cashier_employee_id, customer_id,
               severity, signal_strength, evidence, case_id, status,
               acknowledged_at, acknowledged_by, attributes, created_at
          FROM detection.detections
         WHERE id = $1 AND tenant_id = $2`
    row := s.pool.QueryRow(ctx, q, id, tenantID)
    var d types.Detection
    err := row.Scan(
        &d.ID, &d.TenantID, &d.RuleID, &d.DetectedAt, &d.SourceEntityType,
        &d.SourceEntityID, &d.LocationID, &d.CashierEmployeeID, &d.CustomerID,
        &d.Severity, &d.SignalStrength, &d.Evidence, &d.CaseID, &d.Status,
        &d.AcknowledgedAt, &d.AcknowledgedBy, &d.Attributes, &d.CreatedAt,
    )
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("fox.LoadDetectionScoped: %w", err)
    }
    return &d, nil
}
```

- [ ] **Step 2: Run the store test — expect PASS.**

```bash
DATABASE_URL='...' go test ./internal/fox/ -run TestStore_TenantIsolation
```

- [ ] **Step 3: Commit.**

```bash
git add internal/fox/store.go internal/fox/store_tenant_isolation_test.go
git commit -m "feat(fox): tenant-scoped LoadCase/LoadDetection (GRO-904)"
```

### Task 1.3: Wire APIKeyMiddleware into `cmd/fox/main.go`

**Files:**
- Modify: `cmd/fox/main.go`

**Why:** Closes GRO-904 fix item #1. Currently the binary mounts the handler under only `RealIP`, `Recoverer`, `requestLogger` — anyone who can reach `:8083` can call any endpoint.

- [ ] **Step 1: Mirror the `cmd/transaction/main.go:55` pattern.**

Replace the current router section:

```go
r := chi.NewRouter()
r.Use(middleware.RealIP, middleware.Recoverer)
r.Use(requestLogger(logger))

r.Get("/health", healthHandler(cfg))
handler.Mount(r)
```

with:

```go
r := chi.NewRouter()
r.Use(middleware.RealIP, middleware.Recoverer)
r.Use(requestLogger(logger))

r.Get("/health", healthHandler(cfg))

r.Group(func(r chi.Router) {
    r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
        Pool:     pool,
        Required: true,
    }))
    handler.Mount(r)
})
```

Add `"github.com/ruptiv/canary/internal/identity"` to imports.

- [ ] **Step 2: Compile-check.**

```bash
go build ./cmd/fox/...
```

### Task 1.4: Replace caller-supplied tenant in `internal/fox/handler.go` with claims

**Files:**
- Modify: `internal/fox/handler.go`

**Why:** GRO-904 fix items #3 and #4. Three sites read tenant from body or query:
- `appendAction` (~line 421) — body `merchant_id`
- `closeCase` (~line 482)
- `listCases` (line 376) — query `merchant_id`

Plus the case-load paths must call the new `LoadCaseScoped` / `LoadDetectionScoped`.

- [ ] **Step 1: Audit the call sites.**

Run:

```bash
grep -n "merchant_id\|MerchantID\|LoadCase(\|LoadDetection(" internal/fox/handler.go
```

For each hit:
- Body/query-derived tenant → replace with `identity.ClaimsFromContext(r.Context())` and 401 if absent.
- `LoadCase(ctx, id)` / `LoadDetection(ctx, id)` → swap for the `*Scoped` variants using the claims tenant.
- If a body legitimately includes `merchant_id` (e.g. write paths), call `identity.AssertBodyTenantMatches(ctx, parsed)` and 403 on mismatch.

- [ ] **Step 2: Add a small `requireTenant` helper at the top of handler.go.**

```go
// requireTenant returns the authenticated tenant or writes 401 and
// returns uuid.Nil + false. All fox endpoints are tenant-scoped — the
// caller bails on !ok.
func requireTenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
    c, ok := identity.ClaimsFromContext(r.Context())
    if !ok || c.TenantID == uuid.Nil {
        writeError(w, http.StatusUnauthorized, "unauthenticated", "missing tenant claim")
        return uuid.Nil, false
    }
    return c.TenantID, true
}
```

Add `"github.com/ruptiv/canary/internal/identity"` to imports.

- [ ] **Step 3: Replace each handler's tenant-derivation line.**

Pattern (per call site):

```go
// before
tenantStr := r.URL.Query().Get("merchant_id")
// ... parse, validate, etc.

// after
tenantID, ok := requireTenant(w, r)
if !ok {
    return
}
```

For body-derived tenant (e.g. `appendAction`): keep parsing the body, but replace `tenantID := body.MerchantID` with:

```go
tenantID, ok := requireTenant(w, r)
if !ok { return }
if body.MerchantID != "" {
    bodyTenant, err := uuid.Parse(body.MerchantID)
    if err != nil {
        writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
        return
    }
    if err := identity.AssertBodyTenantMatches(r.Context(), bodyTenant); err != nil {
        writeError(w, http.StatusForbidden, "tenant_mismatch", err.Error())
        return
    }
}
```

- [ ] **Step 4: Swap LoadCase / LoadDetection for the scoped variants.**

Each `store.LoadCase(ctx, id)` becomes `store.LoadCaseScoped(ctx, tenantID, id)`. Same for `LoadDetection`.

- [ ] **Step 5: Compile-check.**

```bash
go build ./internal/fox/... ./cmd/fox/...
```

### Task 1.5: Handler-level cross-tenant negative test (red → green via 1.3 + 1.4)

**Files:**
- Modify: `internal/fox/handler_test.go` (add `TestHandler_CrossTenant_404`)

- [ ] **Step 1: Add the test.**

```go
func TestHandler_CrossTenant_404(t *testing.T) {
    ctx := context.Background()
    tenantA := testutil.SeedTenant(t, ctx)
    tenantB := testutil.SeedTenant(t, ctx)
    pool := testutil.MustConnect(t)

    // Seed a case for tenantA.
    caseID := uuid.New()
    if _, err := pool.Exec(ctx,
        `INSERT INTO detection.cases (id, tenant_id, case_number, case_type, title, severity, status)
         VALUES ($1, $2, 'C-XT-1', 'shrink', 'cross-tenant negative test', 'high', 'open')`,
        caseID, tenantA); err != nil {
        t.Fatalf("seed case: %v", err)
    }
    t.Cleanup(func() {
        _, _ = pool.Exec(ctx, `DELETE FROM detection.cases WHERE id = $1`, caseID)
    })

    h := New(NewStore(pool), DefaultEscalationConfig(), nil)
    r := chi.NewRouter()
    h.Mount(r)

    // tenantB authenticates and tries to GET tenantA's case.
    req := httptest.NewRequest(http.MethodGet, "/v1/fox/cases/"+caseID.String(), nil)
    req = req.WithContext(testutil.WithAPIKeyClaims(req.Context(), tenantB))
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    if rec.Code != http.StatusNotFound {
        t.Errorf("cross-tenant GET: got %d, want 404 (no existence leak); body=%s",
            rec.Code, rec.Body.String())
    }
}
```

- [ ] **Step 2: Run it.**

```bash
DATABASE_URL='...' go test ./internal/fox/ -run TestHandler_CrossTenant_404
```

Expected: PASS (handler now refuses cross-tenant). If FAIL, iterate on Task 1.4 — likely a missed call site.

- [ ] **Step 3: Run full fox test suite.**

```bash
DATABASE_URL='...' go test ./internal/fox/...
GATEWAY_TEST_DATABASE_URL='...' go test -tags=integration ./internal/fox/...
```

- [ ] **Step 4: Commit.**

```bash
git add cmd/fox/main.go internal/fox/handler.go internal/fox/handler_test.go
git commit -m "fix(fox): require API key + tenant-scope all reads (GRO-904, GRO-916)"
```

---

## Chunk 3: Phases 2–5 — chirp / item / inventory / transaction

Each follows the fox template with these per-service deltas.

### Phase 2: chirp (GRO-905)

**Caller-supplied tenant sites:**
- [internal/chirp/handler.go:84](../../../internal/chirp/handler.go) — body `merchant_id`
- [internal/chirp/handler.go:105](../../../internal/chirp/handler.go) — query `merchant_id` in `ListRules`
- [internal/chirp/handler.go:131](../../../internal/chirp/handler.go) — query `merchant_id` in `ListDetections`

**Cmd binary:** `cmd/chirp/main.go` — verify if APIKeyMiddleware is wired; if not, mirror the fox change.

**Store check:** `grep -n "WHERE id = \$1\b\|tenant_id" internal/chirp/store.go` — add scoped variants for any read-by-id missing a tenant predicate.

**Tasks (mirror fox 1.1–1.5):**

- [ ] 2.1: Store-level cross-tenant test if any read-by-id without tenant exists (else skip).
- [ ] 2.2: Add scoped store reads if needed.
- [ ] 2.3: Wire APIKeyMiddleware in `cmd/chirp/main.go`.
- [ ] 2.4: Replace body/query merchant_id reads with `requireTenant` + `AssertBodyTenantMatches`.
- [ ] 2.5: Add `TestHandler_CrossTenant_404` against `GET /v1/chirp/rules?merchant_id=<tenantA>` from tenantB context — assert non-200 (404 or 403).
- [ ] 2.6: Run `DATABASE_URL=... go test ./internal/chirp/...` and integration variant.
- [ ] 2.7: Commit: `fix(chirp): tenant from claims, not request (GRO-905, GRO-916)`.

### Phase 3: item (GRO-905)

**Caller-supplied tenant sites:** [internal/item/handler.go](../../../internal/item/handler.go) lines 85, 99, 118, 132, 223, 253, 265, 278 (all `tenantFromQuery`).

**Cmd binary:** `cmd/item/main.go`.

**Tasks:** mirror Phase 2.

- [ ] 3.1: Cross-tenant store test if applicable.
- [ ] 3.2: Scoped store reads if applicable.
- [ ] 3.3: Wire middleware in `cmd/item/main.go`.
- [ ] 3.4: Delete `tenantFromQuery` (line 294-311) entirely; replace each call with `requireTenant`. Remove the `?tenant_id=` / `?merchant_id=` URL params from public docs in the handler comment block (lines 40–52).
- [ ] 3.5: Add `TestHandler_CrossTenant_404` — tenantB GETs `/v1/items/{tenantA-item-id}` → 404.
- [ ] 3.6: Run unit + integration tests.
- [ ] 3.7: Commit: `fix(item): tenant from claims, drop ?tenant_id query (GRO-905, GRO-916)`.

### Phase 4: inventory (GRO-905)

**Caller-supplied tenant sites:** [internal/inventory/handler.go](../../../internal/inventory/handler.go) lines 68, 101, 181 (all `tenantFromHeader` reading `X-Canary-Merchant`).

**Cmd binary:** `cmd/inventory/main.go`.

**Tasks:**

- [ ] 4.1–4.2: Store cross-tenant test + scoped reads if applicable.
- [ ] 4.3: Wire middleware in `cmd/inventory/main.go`.
- [ ] 4.4: Delete `tenantFromHeader` (line 293); replace each call with `requireTenant`. The `X-Canary-Merchant` header is now unused — remove from doc strings.
- [ ] 4.5: Cross-tenant test — tenantB issues a position read for tenantA's location; expect 404. Note `MerchantID` body field at line 237 — add `AssertBodyTenantMatches` for write paths that accept it.
- [ ] 4.6: Run unit + integration tests.
- [ ] 4.7: Commit: `fix(inventory): tenant from claims, drop X-Canary-Merchant (GRO-905, GRO-916)`.

### Phase 5: transaction (GRO-905)

**Caller-supplied tenant sites:** [internal/transaction/handler.go](../../../internal/transaction/handler.go) lines 75, 88, 115, 186, 218, 247 (all `tenantFromQuery`).

**Cmd binary:** `cmd/transaction/main.go` already wires middleware ([cmd/transaction/main.go:55](../../../cmd/transaction/main.go)). **No middleware change needed.**

**Tasks:**

- [ ] 5.1–5.2: Store cross-tenant test + scoped reads if applicable.
- [ ] 5.3: (skip — middleware already wired)
- [ ] 5.4: Delete `tenantFromQuery` (line 246); replace each call with `requireTenant`.
- [ ] 5.5: Cross-tenant test against transaction read endpoints.
- [ ] 5.6: Run unit + integration tests.
- [ ] 5.7: Commit: `fix(transaction): tenant from claims, drop ?tenant_id query (GRO-905, GRO-916)`.

---

## Chunk 4: Close-out (Phase 6)

### Task 6.1: Run full repo tests

- [ ] **Step 1: Unit tests.**

```bash
DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable' \
  go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Integration tests.**

```bash
GATEWAY_TEST_DATABASE_URL='...' go test -tags=integration ./...
```

Expected: all PASS.

- [ ] **Step 3: Build all binaries.**

```bash
go build ./cmd/...
```

### Task 6.2: Update Linear issues

- [ ] **Step 1: Comment on GRO-904** with the fox commit SHA + a one-line summary: middleware wired, store reads tenant-scoped, cross-tenant negative test added.
- [ ] **Step 2: Comment on GRO-905** with each of the four (chirp/item/inventory/transaction) commit SHAs + summary.
- [ ] **Step 3: Comment on GRO-916** with all five commit SHAs + the test convention used (per-service `*_test.go` with `DATABASE_URL` and integration variant for HTTP layer).
- [ ] **Step 4: Move all three issues** to the project's "In Review" or "Done" status (depending on team convention). Status values are workflow-specific — check `mcp__a018de2b-6aea-4cf1-aa2a-20375d7d8e69__list_issue_statuses` first.

### Task 6.3: PR

- [ ] **Step 1: Push branch.**

```bash
git push -u origin claude/festive-ellis-c01e45
```

- [ ] **Step 2: Open PR.**

```bash
gh pr create --title "fix: cross-tenant IDOR hardening across 5 services (GRO-904, GRO-905, GRO-916)" \
  --body "$(cat <<'EOF'
## Summary

Closes GRO-904, GRO-905, GRO-916 in one bundled change.

- Wires `identity.APIKeyMiddleware` in `cmd/fox`, `cmd/chirp`, `cmd/item`, `cmd/inventory` (transaction already had it).
- Replaces every `tenantFromQuery` / `tenantFromHeader` / body `merchant_id` derivation with `identity.ClaimsFromContext`. `?tenant_id=` and `X-Canary-Merchant` are no longer trusted.
- Adds tenant-scoped variants (`LoadCaseScoped`, `LoadDetectionScoped`, etc.) for read-by-id store paths previously missing the predicate.
- Adds a cross-tenant negative test per service: tenantB attempting to GET tenantA's row returns 404 (no existence leak).
- Lifts `seedTenant` to `internal/testutil` so the new tests share fixtures.

## Test plan
- [ ] Unit: `DATABASE_URL=... go test ./...`
- [ ] Integration: `GATEWAY_TEST_DATABASE_URL=... go test -tags=integration ./...`
- [ ] All five `TestHandler_CrossTenant_404` tests pass
- [ ] Manual: hit `/v1/fox/cases/<id>` without `X-Canary-API-Key` → 401
- [ ] Manual: hit `/v1/items?tenant_id=<other>` with valid key → 401 or 404 (no rows visible)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Drop PR URL into Linear comments on GRO-904, GRO-905, GRO-916.**

---

## Notes / risks

- **Existing /v1/items?tenant_id=… clients break.** GRO-905 explicitly mandates dropping caller-supplied tenant; any internal caller passing it will now get a 401 if no API key, or have the query param ignored. Check `internal/gateway/` and `cmd/edge/` for callers before merge.
- **Chirp body `merchant_id` is wire-format-load-bearing.** The handler comment says "we accept merchant_id in the request and pass it through as tenant_id." Don't break the wire format — keep parsing the field but assert match via `AssertBodyTenantMatches` instead of using it as truth.
- **Subagent dispatch caveat.** Each service slice touches `internal/testutil/db.go` (Phase 0 lift) — Phase 0 must land before any subagent kicks off Phase 1+ work, or merge conflicts ensue.
- **Test DB schema:** `make db-reset` (per CLAUDE.md) recreates from `deploy/schema/`. If a slice fails because `app.tenants.schema_name` is required, that's the seed helper format — see Task 0.1.
