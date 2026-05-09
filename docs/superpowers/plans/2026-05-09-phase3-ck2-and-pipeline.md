# Phase 3 Plan — Close CK2 + Pipeline Correctness

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close [GRO-919](https://linear.app/growdirect/issue/GRO-919) (CK2 — Phase 2 Identity Foundation green) by landing the last remaining prereq [GRO-910](https://linear.app/growdirect/issue/GRO-910) (webhook DLQ tenant scoping), then unblock the Phase 3 pipeline-correctness backlog.

**Architecture:** Two waves.

- **Wave A — CK2 close-out.** GRO-910 is one more bundled-slice fix in the shape of the prior IDOR work: scope every DLQ admin query by `merchant_id` from claims, add cross-tenant negative tests, run the CK2 grep audit, then move GRO-919 to Done.
- **Wave B — Phase 3 pipeline correctness.** Four HIGH bugs that GRO-919 unblocks, all in the protocol/ingest layer. Independent of each other (different files, different services), so dispatch in sequence (one subagent per slice, two-stage review).

**Tech stack:** unchanged. Go 1.22+, Chi v5, pgx/v5, `internal/identity`, `internal/testutil`, PostgreSQL 17.

**Issues touched:**
- Wave A: GRO-910 (HIGH), GRO-919 (CK2 process gate)
- Wave B candidates: GRO-907, GRO-908, GRO-909, GRO-914 — pick the order during execution

**Reference:** the IDOR-hardening PR [#6](https://github.com/growdirectprez/CanaryGo/pull/6) is the canonical pattern. Six commits there show the per-service shape: `identity.APIKeyMiddleware` wiring, `requireTenant` helper, `AssertBodyTenantMatches` on body fields, store-level scoping, store + handler cross-tenant tests via `testutil.SeedTenant` + `testutil.WithAPIKeyClaims`. Wave A is one more application of that pattern; Wave B is different bug families and gets its own discovery.

---

## Wave A — Close CK2 (GRO-910 + GRO-919)

### Task A.1 — Tenant-scope every DLQ admin query (GRO-910)

**Files:**
- Modify: `internal/webhook/dlq.go` — `Get` (line 126), `MarkReplayed` (line 205), `MarkRetryFailed` (line 228), `txGet` (line 286). All currently `WHERE id = $1`; need `WHERE id = $1 AND merchant_id = $2`.
- Modify: `cmd/gateway/admin.go` — the four handler call sites that invoke the methods above. Pull `merchant_id` from `identity.ClaimsFromContext(r.Context())` and pass it through.
- Create: `internal/webhook/dlq_cross_tenant_test.go` — `//go:build integration`, `package webhook`. Two tests: store-level isolation + handler-level 404 cross-tenant.

**Why:** GRO-919 prereq. The DLQ admin path is gated by `RequireScope("dlq:read"|"dlq:replay")` ([cmd/gateway/admin.go:60-63,110-113,155](cmd/gateway/admin.go)) but **not** by tenant. A tenant-A admin key with `dlq:replay` can replay another tenant's queued webhook events. `internal/webhook/dlq.go:165` already shows the merchant_id predicate pattern in `List`; lift it to the by-id paths.

- [ ] **Step 1 — Add `merchantID` parameter to the four affected `*DLQ` methods.**

```go
// before
func (q *DLQ) Get(ctx context.Context, id uuid.UUID) (*DLQRow, error)
// after
func (q *DLQ) Get(ctx context.Context, merchantID, id uuid.UUID) (*DLQRow, error)
```

Same shape for `MarkReplayed`, `MarkRetryFailed`, `txGet`. SQL becomes `WHERE id = $1 AND merchant_id = $2`. Return the existing not-found sentinel (`ErrDLQNotFound` or whatever the file uses) on miss — **do not** add a distinct cross-tenant error (existence leak).

- [ ] **Step 2 — Update `cmd/gateway/admin.go` callers.**

In `get`, `replay`, `retryFailed` (and any other handler hitting these methods), after the existing scope check pull the tenant:

```go
claims, _ := identity.ClaimsFromContext(r.Context())
if claims.TenantID == uuid.Nil {
    writeAdminError(w, http.StatusUnauthorized, "missing tenant claim")
    return
}
row, err := h.dlq.Get(r.Context(), claims.TenantID, id)
```

Adjust `dlqStore` interface in `cmd/gateway/admin.go:31` to match the new signatures.

- [ ] **Step 3 — Compile-check.**

```bash
go build ./internal/webhook/... ./cmd/gateway/...
```

- [ ] **Step 4 — Cross-tenant store test.**

`internal/webhook/dlq_cross_tenant_test.go`:

```go
//go:build integration

package webhook

import (
    "context"
    "errors"
    "os"
    "testing"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/ruptiv/canary/internal/testutil"
)

func dbPool(t *testing.T) *pgxpool.Pool {
    t.Helper()
    url := os.Getenv("DATABASE_URL")
    if url == "" {
        t.Skip("DATABASE_URL not set")
    }
    pool, err := pgxpool.New(context.Background(), url)
    if err != nil {
        t.Fatalf("pool: %v", err)
    }
    if err := pool.Ping(context.Background()); err != nil {
        t.Fatalf("ping: %v", err)
    }
    t.Cleanup(pool.Close)
    return pool
}

func TestDLQ_Get_TenantIsolation(t *testing.T) {
    ctx := context.Background()
    tenantA := testutil.SeedTenant(t, ctx)
    tenantB := testutil.SeedTenant(t, ctx)
    pool := dbPool(t)

    // Insert a DLQ row for tenantA via the package's own writer (so we
    // don't have to hand-roll the schema). Capture the row id; assert
    // (tenantB, id) returns ErrDLQNotFound and (tenantA, id) returns it.
    q := NewDLQ(pool)
    rowA, err := q.Append(ctx, /* … */ tenantA /* … */)
    if err != nil { t.Fatalf("append: %v", err) }
    t.Cleanup(func() {
        _, _ = pool.Exec(ctx, `DELETE FROM webhook.dlq WHERE id = $1`, rowA.ID)
    })

    if _, err := q.Get(ctx, tenantB, rowA.ID); !errors.Is(err, ErrDLQNotFound) {
        t.Errorf("tenantB Get: want ErrDLQNotFound, got %v", err)
    }
    if got, err := q.Get(ctx, tenantA, rowA.ID); err != nil || got.ID != rowA.ID {
        t.Errorf("tenantA Get: err=%v id=%v", err, got)
    }
}
```

Look at `internal/webhook/dlq.go` for the existing `Append` (or equivalent insert) signature, then fill in the `/* … */` placeholders. If there's no exported writer, fall back to direct `INSERT INTO webhook.dlq …` via the pool — same pattern as `seedTransaction` in [internal/transaction/cross_tenant_test.go:89](internal/transaction/cross_tenant_test.go).

- [ ] **Step 5 — Cross-tenant handler test.**

Build a chi router around the admin handlers, inject claims via `testutil.WithAPIKeyClaims` for tenantB but include the `dlq:replay` scope (use `identity.InjectClaims` directly with a `Claims{TenantID: tenantB, Scopes: []string{"dlq:replay"}}` — the scope is what makes this test meaningful, since the existing `RequireScope` check passes but tenant scoping must reject anyway). Hit `GET /v1/webhooks/dlq/{tenantA-row-id}` → 404, then `POST /v1/webhooks/dlq/{tenantA-row-id}/replay` → 404.

The test proves the layered gate: `dlq:replay` scope alone is no longer sufficient — tenant must also match.

- [ ] **Step 6 — Run tests.**

```bash
make db-reset-test
DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable' \
IDENTITY_DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_identity_gcp_test?sslmode=disable' \
VALKEY_URL=redis://:valkey_dev@localhost:6379/2 \
SESSION_SECRET="test-session-secret-at-least-32-bytes!" \
INTERNAL_SERVICE_SECRET=test-internal-secret \
go test -tags=integration ./internal/webhook/... ./cmd/gateway/... -count=1 -timeout 120s -v
```

All PASS — both new and existing.

- [ ] **Step 7 — Commit.**

```bash
git add internal/webhook/dlq.go internal/webhook/dlq_cross_tenant_test.go cmd/gateway/admin.go cmd/gateway/admin_test.go
git commit -m "fix(webhook): tenant-scope DLQ admin queries (GRO-910)

…
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 8 — PR.** Either fold into PR [#6](https://github.com/growdirectprez/CanaryGo/pull/6) (it's still open and same security family) or open a sibling PR. Recommendation: sibling PR — keeps PR #6's review surface stable; CK2 close-out should be its own commit/PR for traceability.

### Task A.2 — Close GRO-919 (CK2 verification)

**Files:** none. Verification + Linear status moves.

- [ ] **Step 1 — Run the CK2 acceptance bar.**

```bash
make db-reset-test
go build ./...                                    # must be clean
DATABASE_URL=... [other env] go test ./...        # unit tests green
DATABASE_URL=... [other env] go test -tags=integration ./...  # integration tests green
```

- [ ] **Step 2 — `tenantFromQuery` / `tenantFromHeader` grep audit.**

```bash
grep -rn "tenantFromQuery\|tenantFromHeader" internal/ cmd/
```

Expected: zero hits in any non-admin handler. The PR #6 commits deleted these in fox/chirp/item/inventory/transaction. If a hit appears anywhere else (gateway admin handlers excepted, per the CK2 spec), document it on GRO-919 as a partial pass.

- [ ] **Step 3 — Cross-tenant test inventory.**

```bash
grep -l "TestStore_.*TenantIsolation\|TestHandler_.*CrossTenant" internal/
```

Expected: at least 6 files — fox, chirp, item, inventory, transaction, webhook (after A.1). Each with at least one store and one handler test, all PASS.

- [ ] **Step 4 — DLQ tenant scoping check.**

```bash
grep -n "WHERE id = \$1\b" internal/webhook/dlq.go
```

Expected: zero. Every read-by-id should have an `AND merchant_id = $2`.

- [ ] **Step 5 — Linear close-out.** Comment on [GRO-919](https://linear.app/growdirect/issue/GRO-919) with the audit results + PR links. Move to Done.

---

## Wave B — Phase 3 Pipeline Correctness (post-CK2)

CK2 closing unblocks these. Each is a separate subagent dispatch; do **not** bundle — they touch different files and have distinct test surfaces.

### Candidate B.1 — [GRO-908](https://linear.app/growdirect/issue/GRO-908) (HIGH): sub1 hash-chain fork race

Concurrent workers can produce a DAG instead of a chain. Likely a `SELECT … FOR UPDATE` / advisory-lock fix in `internal/protocol/sub1/`. Land first because chain integrity is the foundation the other protocol stages assume.

### Candidate B.2 — [GRO-907](https://linear.app/growdirect/issue/GRO-907) (HIGH): sub3 cross-tenant Merkle batching

Per-tenant verifiability is the product pitch; batching across tenants breaks it. Fix in `internal/protocol/sub3/` — group leaves by tenant_id before forming the Merkle root. Add a property-style test: any batch contains exactly one tenant's leaves.

### Candidate B.3 — [GRO-909](https://linear.app/growdirect/issue/GRO-909) (HIGH): bull worker no panic recovery / no graceful shutdown

Replenishment crash kills L402 billing — already in CLAUDE.md context as part of the GRO-915 graceful shutdown work. Likely needs `recover()` in the worker loop + `cmdutil.RunServer`-style ctx propagation. Mostly mechanical given the graceful-shutdown pattern landed in `cmdutil`.

### Candidate B.4 — [GRO-914](https://linear.app/growdirect/issue/GRO-914) (MEDIUM): sub2 silently drops tender rows

Tender_type resolution failure currently swallows rows. Fix should emit a structured error and DLQ the offending event rather than dropping silently. Smallest of the four — natural last item.

**Suggested execution order:** B.1 → B.2 → B.3 → B.4 (severity + dependency). B.1 establishes correctness primitives (lock pattern) that B.2 may also use; B.3 has its own scaffold; B.4 is independent.

---

## Notes / risks

- **Wave A scope creep.** GRO-910's spec asks only for the four DLQ method changes. Don't expand scope — if the audit in A.2 turns up other pre-CK2 admin handlers without tenant predicates, file follow-up issues rather than including in this slice.
- **CK2 is a process gate, not code.** GRO-919 closing is a Linear-state change after the audit passes; don't write code "for" CK2.
- **Wave B bugs may need separate worktrees.** The five-service IDOR work was naturally serializable because every change touched its own service. Pipeline-correctness fixes touch shared protocol packages (`internal/protocol/sub1`, `sub2`, `sub3`) and a single bull worker — sequential is fine within one worktree, but if any subagent finds cross-cutting refactors, escalate before proceeding.
- **Pricing + GRO-848.** [GRO-848](https://linear.app/growdirect/issue/GRO-848) (AtlasView delegation) is a separate dependency. CK2 explicitly excludes scope-vocabulary work (GRO-906) and fox/cmd auth wiring is documented as deferred to GRO-848 — but PR #6 already wired all four `cmd/<svc>/main.go` middlewares. That's strictly more secure than CK2 required, not less; document it on GRO-919 so reviewers don't think we broke scope.
