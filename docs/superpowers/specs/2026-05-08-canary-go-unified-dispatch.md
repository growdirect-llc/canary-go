---
title: Canary.GO Unified Dispatch — Post-Triage Spec
date: 2026-05-08
last-revised: 2026-05-10
status: active
authors: Geoff Lyle (with Claude Opus 4.7)
supersedes: interim draft at docs/decisions/canary-go-unified-dispatch-plan-2026-05-08.md (deleted in same commit)
---

# Canary.GO Unified Dispatch — Post-Triage Spec

## REVISION 2026-05-09 — SDD-013 reframing

The original 2026-05-08 spec read the Platform Architecture Charter as committing canary.go to **migrate identity away** to AtlasView. The AtlasView dispatch `Ruptiv/dispatches/2026-05-09-atlasview-sdd-013-downstream-substrate-contract.md` and the new D-157 in `product/atlasview/decisions.md` make the actual model explicit:

> "AtlasView writes who can do what; canary.go enforces it on every request."

Concretely: **canary.go's `internal/identity` is the IdP runtime** (per D-134; mints JWTs, owns `app.api_keys`, runs the verifier). **AtlasView is the optional management plane** (per D-157; publishes a signed config manifest covering OIDC SSO config, roles, capabilities, agent profiles, operating modes). AtlasView outage does not stop canary serving traffic; at most it freezes the config snapshot.

What this revision changes in this spec:

- **Phase 4 is no longer "charter-deferred."** GRO-906, GRO-912, GRO-913 are real work hardening canary's own IdP runtime. The administrative-close rule on CK4 is dropped.
- **Phase 5 is no longer "charter-deferred."** Item Setup UI is the operates-without-AtlasView merchant surface — real product work, not transitional. AtlasView eventually provides a richer editor on top of the same data; this UI persists.
- **Phase 5 blockers reduced.** GRO-901/902/903 depend on GRO-922 (UI substrate) + CK2 (data-layer foundation). GRO-906 is parallel work, not a hard precondition.
- **GRO-848 is re-scoped.** Still the Urgent gating spec, but for the canary-side **manifest-consumer contract** (signed envelope, signing-key rotation, OIDC SSO shape, capability schema), not for "decide whether identity migrates" (which is settled — it doesn't).
- **GRO-923 added.** New ticket for authoring the canary-side counterpart SDD that mirrors AtlasView's SDD-013 from the consumer side.

The §"Architectural reconciliation" → "Charter posture" subsection below is rewritten in place. The §"What's deferred and why" table, §"Sequencing rationale" #2, and §"Durable engineering lessons" #4 are corrected. Phase 4 and Phase 5 section headings drop "(charter-deferred)".

The original charter-triage assumptions are not preserved in-line; they were wrong. Git history is the audit trail.

## Purpose

Coordinate four independent streams of work in `CanaryGo/` under a single ordered execution model:

1. **Code-review remediation** — 14 issues filed 2026-05-08 (GRO-904 through GRO-917), spanning IDOR fixes, pipeline correctness, identity hardening, boilerplate cleanup.
2. **Frontend product** — Item Setup UI Flows A / B / C-completion (GRO-901 / 902 / 903).
3. **Identity contract** — pin the canary-side consumer interface to AtlasView's SDD-013 manifest contract (GRO-848 gating spec; GRO-923 counterpart SDD).
4. **UI substrate** — establish reusable component primitives in `templates/components/` before further frontend work (GRO-922).

Reconciles all four against the Platform Architecture Charter (`Ruptiv/spec/platform-architecture-charter.md`, ratified 2026-05-08), AtlasView SDD-013 (under authoring per `Ruptiv/dispatches/2026-05-09-atlasview-sdd-013-downstream-substrate-contract.md`), and the UI components directive (2026-05-08).

This spec is the **human-readable narrative**. Linear is the **machine-readable queue**. If they disagree, Linear wins.

## Source materials

- Code review findings: 2026-05-08 review pass (Grok validation + deep audit). 14 issues filed: [GRO-904 fox IDOR](https://linear.app/growdirect/issue/GRO-904) through [GRO-917 cmdutil cleanup](https://linear.app/growdirect/issue/GRO-917).
- Platform Architecture Charter: `spec/platform-architecture-charter.md` (Ruptiv repo, ratified 2026-05-08). Commitments 1-2 + sequencing step 4 govern the identity decisions in this dispatch.
- AtlasView identity contract reference: `docs/decisions/gro-848-atlasview-identity-integration.md`.
- Vision fit matrix: `docs/architecture/canary-go-vision-fit-matrix.md`, mapping the current Go execution queue to the Ruptiv, AtlasView, shared-platform, retail capability, and agent-memory operating model.
- Frontend specs: `Brain/wiki/cards/canary-item-setup-screen-decomp.md` (Flow A / B / C decomposition) and `Brain/wiki/cards/canary-item-master-and-catalog.md` (5-dimension lifecycle).
- UI substrate directive: 2026-05-08 conversational directive ("design in reusable components"). Saved as durable preference in `~/.claude/projects/-Users-gclyle-CanaryGo/memory/feedback_ui_reusable_components.md`.

## Runtime model

Single long-lived runner (the "ALX / laptop" agent). Source of truth for "what's next" is Linear:

```
project = Canary.GO
status  = Todo
labels  ⊇ {laptop, ALX}
no open blockers (Linear blockedBy edges)
order by priority desc, identifier asc
take(1)
```

The runner consumes one ticket per cycle, opens a worktree, lands a PR, advances Linear status, repeats. Linear `blockedBy` relations enforce the dependency graph. This spec documents *why* the graph is shaped the way it is; the runner doesn't read this doc to operate.

## Architectural reconciliation

Three concurrent forces shape this dispatch:

### Charter + SDD-013 posture (revised 2026-05-09)

The Platform Architecture Charter commits the platform to "one identity" (commitment 2) and "AtlasView is the operator surface" (commitment 1). The 2026-05-09 SDD-013 dispatch and D-157 clarify how that resolves operationally:

- **canary.go's `internal/identity` is the IdP runtime** (per D-134). It mints JWTs, owns `app.api_keys`, runs the verifier middleware. It is not going away.
- **AtlasView is the optional management plane** (per D-157). It publishes a signed config manifest covering OIDC SSO config, roles, capability matrix, agent profiles, operating modes. canary.go materializes the manifest into local state.
- **Operational independence**: AtlasView outage does not stop canary serving traffic; the cache freezes at last-good. The merchant surface in canary continues to serve.
- **Two operator surfaces, complementary**: canary.go's merchant UI is the operates-without-AtlasView path; AtlasView is "a richer editor when present, not a runtime dependency when absent." Both stay.

This means **identity work in canary is real and current**, not deferred. Rate limiting, scope enforcement, and `last_used_at` hardening apply to canary's own IdP code. The bilateral contract (GRO-848) pins the manifest schema; the canary-side counterpart SDD (GRO-923) describes consumption — neither blocks ongoing canary IdP work today.

### Code-review findings

The 14 review findings split cleanly:

- **Data-layer correctness.** Tenant predicates on queries (`tenant_id` to WHERE clauses), cross-tenant test harness, pipeline correctness (sub1 chain race, sub3 per-merchant Merkle, sub2 tender drops, bull worker recovery), boilerplate cleanup (cmdutil, server runner, version embedding). All proceed.

- **Identity hardening** (canary's IdP runtime). Scope vocabulary + RequireScope helper, API-key rate limiter, `last_used_at` aggregating writer, cmd-binary middleware mount across services. All proceed — these harden canary's own surfaces, which stay per D-134.

- **Test infrastructure.** Cross-tenant negative-test harness — precondition for closing the IDOR fixes.

### UI substrate state

Discovered during planning: `internal/web/templates/` has 117 templates and 1 partial. Almost no shared visual primitives. Building Item Setup UI on top of a fresh-handler-each-time pattern would compound the duplication. **GRO-922 lays a 5-component substrate in Phase 1 so all subsequent UI work composes from documented primitives.**

Components also serve the charter's longer arc: even though canary's merchant UI is permanent (per the 2026-05-09 reframing), AtlasView may eventually offer a richer editor for the same data. Documented composable primitives are easier to share across surfaces than bespoke screens.

## Phases

Five phases, gated by checkpoint tickets. The runner does not pick up Phase N+1 work until CKn is Done.

### Phase 1 — Foundation

Build shared building blocks before anything else uses them. Three parallel substrate items + checkpoint.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-917](https://linear.app/growdirect/issue/GRO-917) | `cmdutil` package — shared logger init, `requestLogger` middleware, build-info version helper, server runner with graceful shutdown | none |
| [GRO-915](https://linear.app/growdirect/issue/GRO-915) | Migrate every `cmd/*` HTTP service to the shared server runner | GRO-917 |
| [GRO-911](https://linear.app/growdirect/issue/GRO-911) | Split feature blocks out of `internal/web/handler.go` until the file is ≤ 2,000 lines (currently 3,276). Coordinated with GRO-922 for template-side component extraction | none |
| [GRO-922](https://linear.app/growdirect/issue/GRO-922) | UI component substrate — `templates/components/` directory, 5 starter components (`form-field`, `data-table`, `status-pill`, `card`, `drawer`), per-component header contract, 3 templates refactored to consume them, conventions doc | none |
| **CK1** ([GRO-918](https://linear.app/growdirect/issue/GRO-918)) | Phase 1 checkpoint | 917, 915, 911, 922 |

CK1 verifies: build clean, tests green, `make db-reset` + `make migrate-up` produce equivalent schema, every `cmd/*` health endpoint returns 200 with build-info version (no more `"version": "1.0.0"`), `internal/web/handler.go` ≤ 2,000 lines, components substrate mounted and consumed by ≥ 3 templates.

### Phase 2 — Identity Foundation (charter-narrowed)

The charter narrowed Phase 2's scope. Originally scoped to land both the data-layer fixes AND the auth-wiring (which middleware sits at each cmd binary). The auth-wiring half deferred to post-GRO-848. What remains in Phase 2:

| Ticket | What | Blockers |
|---|---|---|
| [GRO-916](https://linear.app/growdirect/issue/GRO-916) | Cross-tenant negative-test harness (pattern from `internal/lp/allowlist_test.go:220-247`) — proves IDOR fixes hold | none (was blocked by 906, unblocked during triage) |
| [GRO-905](https://linear.app/growdirect/issue/GRO-905) | **Data-layer half:** kill `tenantFromQuery` / `tenantFromHeader`, replace with `identity.ClaimsFromContext(r.Context()).TenantID` across chirp / item / inventory / pricing / transaction. The `ClaimsFromContext` helper is identity-issuer-agnostic — works whether middleware verifies an API key or an AtlasView JWT | GRO-916 |
| [GRO-904](https://linear.app/growdirect/issue/GRO-904) | **Data-layer half:** add `tenant_id` predicate to `LoadCase` / `LoadDetection` / `appendAction` / `closeCase` / `listCases`. Auth-wiring half deferred to GRO-848 | GRO-916 |
| [GRO-910](https://linear.app/growdirect/issue/GRO-910) | Webhook DLQ admin: add `merchant_id` predicate to `Get` / `MarkReplayed` / `MarkRetryFailed`. Fully in scope (no auth-wiring change needed) | GRO-916 |
| **CK2** ([GRO-919](https://linear.app/growdirect/issue/GRO-919)) | Phase 2 checkpoint (narrowed scope) | 916, 905, 904, 910 |

**Removed from CK2** (deferred to a follow-up checkpoint after GRO-848 closes): GRO-906 (scope vocabulary + `RequireScope` enforcement). The vocabulary needs to align with what AtlasView issues in JWT claims.

CK2 verifies: build clean, tests green, cross-tenant negatives green for fox / chirp / item / inventory / transaction, `tenantFromQuery` and `tenantFromHeader` helpers grep-clean (or retained only inside admin-mode handlers), DLQ admin queries scope by `merchant_id`.

### Phase 3 — Pipeline Correctness

Independent of UI and identity migration. Tightens the protocol layer.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-908](https://linear.app/growdirect/issue/GRO-908) | sub1 advisory lock — close the chain-fork race via `pg_advisory_xact_lock(hashtext(merchant_id::text))`. Add concurrency test | CK2 |
| [GRO-907](https://linear.app/growdirect/issue/GRO-907) | sub3 per-merchant Merkle batching — group `protocol.evidence` by `merchant_id` before `BuildMerkleTree`. Closes the cross-tenant Merkle leak | CK2 |
| [GRO-914](https://linear.app/growdirect/issue/GRO-914) | sub2 tender-drop warn + metric (currently silent `continue`); distinguish `pgx.ErrNoRows` from other errors in `lookupEmployee` | CK2 |
| [GRO-909](https://linear.app/growdirect/issue/GRO-909) | Bull worker: add `recover()` to background goroutine; refactor `cmd/bull/main.go` to mirror sub2/sub3 graceful-shutdown pattern using the runner from GRO-917 | CK2 |
| **CK3** ([GRO-920](https://linear.app/growdirect/issue/GRO-920)) | Phase 3 checkpoint | 908, 907, 914, 909 |

CK3 verifies: end-to-end protocol smoke (ingest 5 events for one merchant, verify 5 evidence rows + 1 anchor with merkle_root scoped to that merchant only, no fork in chain), sub1 fork-free under 10 concurrent goroutines, bull survives a panicking handler.

### Phase 4 — Identity Hardening

Hardens canary.go's own IdP runtime. Per D-134, canary remains the IdP — these tickets harden code that stays. **Expanded 2026-05-10** with adjacent identity-boundary findings from the Codex security review.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-906](https://linear.app/growdirect/issue/GRO-906) | Scope vocabulary + `RequireScope` enforcement across 10 services | CK1 |
| [GRO-912](https://linear.app/growdirect/issue/GRO-912) | API-key rate limiter (Valkey-backed) | CK3 |
| [GRO-913](https://linear.app/growdirect/issue/GRO-913) | `last_used_at` aggregating writer (replace per-request goroutines) | CK3 |
| [GRO-931](https://linear.app/growdirect/issue/GRO-931) | **CRITICAL** — scope-gate API-key lifecycle endpoints (`/v1/identity/keys`); revoke must filter by tenant | CK1 (GRO-906 vocab in place) |
| [GRO-949](https://linear.app/growdirect/issue/GRO-949) | Refresh handler reloads Person/Org state before minting; deactivated users 401 + family revoked | none |
| [GRO-954](https://linear.app/growdirect/issue/GRO-954) | Login rate limit + lockout (credential-side mirror of GRO-912) | GRO-912 (shares limiter substrate) |
| [GRO-955](https://linear.app/growdirect/issue/GRO-955) | `MaxBytesReader` cap on `/auth/refresh` (login already capped) | none |
| **CK4** ([GRO-921](https://linear.app/growdirect/issue/GRO-921)) | Phase 4 checkpoint | 906, 912, 913, 931, 949, 954, 955 |

### Phase 5 — Frontend Product

Item Setup UI flows. The merchant operator surface — operates with or without AtlasView. Depends on:

- **GRO-922** (component substrate, Phase 1) — for composable primitives.
- **CK2** (Phase 2 checkpoint) — for the data-layer tenant foundation. Item Setup writes use the canonical `ClaimsFromContext` boundary.

GRO-906 (scope enforcement) lands in parallel, not as a hard precondition. New Item Setup write surfaces use whatever scope enforcement is current at the time they ship; if 906 hasn't closed yet, the surfaces inherit existing enforcement and benefit when 906 lands.

Two adjacent identity-contract tickets coexist with this phase but do not gate it:

- **GRO-848** (Urgent gating spec) — pins the canary-side **manifest-consumer** contract (signed envelope, signing-key rotation, OIDC SSO shape) per AtlasView's SDD-013. Human-authored; informs the canary identity team's roadmap, not Phase 5 product work.
- **GRO-923** — counterpart SDD authoring for canary's manifest-consumer side. Blocked by GRO-848.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-848](https://linear.app/growdirect/issue/GRO-848) | **Gating spec** (Urgent) — canary-side manifest-consumer contract per AtlasView SDD-013 | none (human-authored) |
| [GRO-923](https://linear.app/growdirect/issue/GRO-923) | Counterpart SDD authoring (canary subscriber-side) | GRO-848 |
| [GRO-901](https://linear.app/growdirect/issue/GRO-901) | Item Setup Flow A — scan-to-lookup (mobile-first 4-screen create path; barcode lookup adapters; viewfinder + scanner-listener) | GRO-922, CK2 |
| [GRO-902](https://linear.app/growdirect/issue/GRO-902) | Item Setup Flow B — supplier CSV import (5-screen lifecycle on `catalog.import_jobs`, atomic-batch commit, bulk-fix-by-class drawer) | GRO-922, CK2 |
| [GRO-903](https://linear.app/growdirect/issue/GRO-903) | Item Setup Flow C completion — C2 enrichment + C3 PLU generation (label-print preview, PLU range config) | GRO-922, CK2 |

C4 (variant matrix) deferred — apparel-specific, customer-pulled per the screen-decomp spec.

## REVISION 2026-05-10 — Codex security review additions

A Codex code review of commit `2277e19` (PR #6 IDOR-fix merge) filed 28 new tickets (GRO-927 through GRO-956). They cluster into four orthogonal themes that don't fit Phase 4's identity-only frame, so they ship as Phases 6-9. The most urgent of the batch — [GRO-931](https://linear.app/growdirect/issue/GRO-931) (CRITICAL: API-key lifecycle endpoints allow cross-tenant revoke + arbitrary scope minting) — folds *into* Phase 4 above because it shares substrate with GRO-906 and must close before CK4.

[GRO-953](https://linear.app/growdirect/issue/GRO-953) was filed as a duplicate of [GRO-929](https://linear.app/growdirect/issue/GRO-929) and was closed as Duplicate during triage.

Phase 5 (Item Setup UI) remains independent. Phases 6-9 sequence after CK4 and before Phase 5's UI work, because they expand the *security boundary* the UI sits behind. They do not gate Phase 5 hard — CK2 + GRO-922 still are the substrate gates — but the runner will pick them up in priority order before pulling new UI tickets.

### Phase 6 — Tenant + Ops Boundary (epic [GRO-927](https://linear.app/growdirect/issue/GRO-927))

The tenant boundary inside service binaries and the ops surface exposed by the gateway. Several services authenticate via gateway today; closing the per-binary auth gap means each cmd/* listening port is hardened against direct hits even if the mesh is bypassed.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-928](https://linear.app/growdirect/issue/GRO-928) | item / pricing / inventory / bull (task) mount handlers without auth; query-param tenant. Wrap in `APIKeyMiddleware`, derive tenant from `ClaimsFromContext`. **Subsumes the earlier "task service auth gap" follow-up.** | CK4 (Phase 4 substrate ready) |
| [GRO-929](https://linear.app/growdirect/issue/GRO-929) | `/devops/*` mounted unconditionally; gate behind `DEV_CONSOLE=1` for local + admin scope (`ops:read` / `ops:admin`) for production. **Also closes the owl read-routes-mounted-outside-auth side-effect** noted during GRO-906 wiring. | none |
| [GRO-933](https://linear.app/growdirect/issue/GRO-933) | Square OAuth: refuse to boot if `ENV=production` and `CANARY_ENCRYPTION_KEY` is missing/malformed; align `.env.example` and parser on one encoding. | none |
| [GRO-930](https://linear.app/growdirect/issue/GRO-930) | Gateway: refuse to boot in production with `SECRET_BACKEND` unset or `pgx`. | none |
| **CK5** (new ticket needed) | Phase 6 checkpoint — every cmd/* binary 401s unauth'd; no `tenant_id` query/header/body wins over claims; `/devops/*` 401s without scope; Square + gateway secret fall-throughs gone. | 928, 929, 930, 933 |

### Phase 7 — MCP Hardening

Five findings clustered around the MCP endpoint. Land as one bundled PR (mirrors the IDOR-fix bundle pattern): vocabulary + dispatch refactor done once, all five findings closed together.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-935](https://linear.app/growdirect/issue/GRO-935) | Per-tool scope gate. `ToolDef` carries required scope; registry refuses dispatch without it. | GRO-906 vocabulary |
| [GRO-936](https://linear.app/growdirect/issue/GRO-936) | MCP mutations bypass audit — wrap `/mcp` in audit middleware OR emit MCP-specific audit records (tool name, key id, tenant, args hash, status). | none |
| [GRO-937](https://linear.app/growdirect/issue/GRO-937) | MCP discovery advertises `X-API-Key`, middleware expects `X-Canary-API-Key` — fix discovery + add a `discovery.header == identity.HeaderAPIKey` test. | none |
| [GRO-938](https://linear.app/growdirect/issue/GRO-938) | Implement MCP `initialize` / capability handshake OR rename the endpoint and discovery to disclaim full MCP compatibility. | decision needed |
| [GRO-939](https://linear.app/growdirect/issue/GRO-939) | Tool argument schema enforcement (validate against `InputSchema` before dispatch); stop ignoring UUID/date parse errors. | none |
| **CK6** (new ticket needed) | Phase 7 checkpoint — read-only key denied on every mutation tool; audit record exists for every mutation tool call; discovery + middleware agree on header; malformed args produce `-32602 Invalid params`. | 935, 936, 937, 939 (938 may run async if it requires a product decision) |

### Phase 8 — Protocol & Data Integrity

Three findings on the protocol pipeline + audit durability.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-932](https://linear.app/growdirect/issue/GRO-932) | L402 proof tokens redeemable via unauthenticated `GET` — require L402 auth on GET, or remove the GET-redeem path. | none |
| [GRO-952](https://linear.app/growdirect/issue/GRO-952) | Raw POS payloads (potentially PII) written to `protocol.evidence.raw_payload` and triggers block delete. Define a redaction/tokenization policy at ingestion; store hashes/proofs separately from raw data. | none (largest design lift in this batch) |
| [GRO-956](https://linear.app/growdirect/issue/GRO-956) | Audit fails open: insertion happens after response, only logs failures. Classify regulated mutations and add fail-closed / spool / DLQ behavior. | GRO-936 (audit substrate touched there too) |
| **CK7** (new ticket needed) | Phase 8 checkpoint — unauth GET on a 402'd token returns 401/402; PII/payment-like fields don't persist raw in `protocol.evidence`; regulated mutation paths fail closed when audit is unavailable. | 932, 952, 956 |

### Phase 9 — Supply Chain & Infra Hardening

Mostly mechanical. Likely batched into 2-3 PRs.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-934](https://linear.app/growdirect/issue/GRO-934) | Bump Go toolchain to 1.26.3+ (template + HTTP/2 stdlib CVEs); pin Docker builder digests | none |
| [GRO-940](https://linear.app/growdirect/issue/GRO-940) | Upgrade vulnerable `btcd v0.20.1-beta` chain | none |
| [GRO-941](https://linear.app/growdirect/issue/GRO-941) | dbcheck/hello/identity Dockerfiles run as root → mirror `Dockerfile.gateway`'s `USER nonroot:nonroot` | none |
| [GRO-942](https://linear.app/growdirect/issue/GRO-942) | Add `ReadHeaderTimeout` (slowloris) to every `cmd/*` HTTP server via `cmdutil` helper | none |
| [GRO-943](https://linear.app/growdirect/issue/GRO-943) | Fix SSE test data race so `go test -race ./...` runs clean | none |
| [GRO-944](https://linear.app/growdirect/issue/GRO-944) | LangGraph SSRF — allowlist URL host/scheme; structured URL building | depends on Phase 6 (devops auth gate) |
| [GRO-948](https://linear.app/growdirect/issue/GRO-948) | Centralize cookie security attributes; production-`Secure=true` not derived from forwarded-proto | none |
| [GRO-946](https://linear.app/growdirect/issue/GRO-946) | Bound-check int32 narrowing in Counterpoint POS parser | none |
| [GRO-945](https://linear.app/growdirect/issue/GRO-945) | Clean / allowlist gitleaks + trufflehog noise | none |
| [GRO-947](https://linear.app/growdirect/issue/GRO-947) | Pin reproducible scanner toolchain; add CI gates | none |
| **CK8** (new ticket needed) | Phase 9 checkpoint — `govulncheck`, `trivy`, `gosec`, `gitleaks` all clean (or with documented narrow allowlists); `go test -race ./...` green; all runtime images run as non-root. | 934, 940, 941, 942, 943, 944, 945, 946, 947, 948 |

### Phase 10 — RBAC + PII Scopes (largest lift; brainstorming required)

These two need a design conversation before implementation — they reshape how UI auth and PII access work.

| Ticket | What | Blockers |
|---|---|---|
| [GRO-950](https://linear.app/growdirect/issue/GRO-950) | Replace tenant-cookie UI auth with user session + RBAC (customer / employee / case / alert / report / settings pages) | brainstorm session |
| [GRO-951](https://linear.app/growdirect/issue/GRO-951) | Per-PII scopes on customer + employee endpoints (`customer:read:pii`, `employee:read:pii`); redact fields when scope absent | brainstorm session |

**Sequencing note:** Phase 10 may slip behind Phase 5 (UI flows) depending on the brainstorm output. If RBAC requires AtlasView's identity manifest (per [GRO-848](https://linear.app/growdirect/issue/GRO-848)), it parks until that contract closes. PII scopes don't have that dependency and can land alongside any of Phases 6-9.

## Per-ticket Definition of Done

Every dispatch ticket inherits the following contract. The runner appends a `## Dispatch` section at start-of-cycle:

```
Branch:    <Linear-supplied gitBranchName>
Worktree:  .claude/worktrees/<vigilant-name>

Done when:
  - go build ./... clean
  - go test ./... green (or named subset for scoped fixes)
  - sqlc-gen run if any *.sql in internal/db/sqlc/ changed
  - migration: declarative + incremental updated together
    (deploy/schema/*.sql AND deploy/migrations/NNN_*.{up,down}.sql,
     per CLAUDE.md two-tier rule)
  - acceptance probe: <1-3 lines: a curl, a SQL query, or a test name
                       that proves the ticket's stated outcome>
  - PR opened, status → In Review, link in Linear

On merge → status → Done, runner removes worktree.
```

The acceptance probe is the load-bearing line. Every ticket gets one concrete probe defined at start-of-cycle so "done" is measurable, not aspirational. Worked examples:

- **GRO-904 (fox tenant_id predicates):** the integration test `TestFoxLoadCase_DeniesCrossTenant` (in `internal/fox/store_test.go` post-fix) seeds a case for tenant A, queries as tenant B, asserts `pgx.ErrNoRows`. Test must exist and pass.
- **GRO-908 (sub1 advisory lock):** `TestSub1Seal_NoForkUnderConcurrency` runs 10 goroutines sealing for one merchant, then asserts both: (a) precondition — `SELECT count(*) FROM protocol.evidence WHERE merchant_id = $1` is exactly 10 (proves the test wasn't a no-op), AND (b) `SELECT count(*) FROM protocol.evidence WHERE merchant_id = $1 GROUP BY prev_chain_hash HAVING count(*) > 1` returns zero rows (no fork). Both conditions must hold.
- **GRO-922 (UI components substrate):** `find internal/web/templates/components -name '*.html' | wc -l` returns ≥ 5 (one per starter component: form-field, data-table, status-pill, card, drawer); `grep -rlE 'components/(form-field\|data-table\|status-pill\|card\|drawer)' internal/web/templates --include='*.html' | grep -v '^internal/web/templates/components/' | wc -l` returns ≥ 3 (proves at least 3 non-component templates consume at least one component).

A probe that cannot be expressed as a command, a test name, or a SQL query is not a probe.

## Failure handling

| Failure | Runner response |
|---|---|
| Build/test fails after fix attempt | Stop. Mark ticket **Blocked**. Surface logs. Runner does not speculatively try alternate fixes — root cause investigation belongs with a human or a fresh agent. |
| Pre-commit hook fails | Investigate root cause, fix, re-stage, **new commit** (no `--amend` per CLAUDE.md). |
| Merge conflict on push | Stop. Surface. No destructive operations. |
| Linear API fails (status transition, etc.) | Retry 3× with exponential backoff, then stop and surface. Status transitions are part of "done." |
| Acceptance probe fails despite green build | Ticket stays **In Progress**. Green build alone does not mark Done. |
| Worktree dirty at end | Stash + surface; never auto-commit unrelated changes. |
| **Checkpoint fails** | Stop. Open `CKn-failed-<root-cause>` ticket linked to the failing dispatch ticket. No Phase N+1 work until checkpoint is green. |
| Sqlc input changed but `make sqlc-gen` not run | Block — generated code must be in sync with input queries. |
| Schema change without both `deploy/schema/*.sql` AND `deploy/migrations/NNN_*.{up,down}.sql` updated | Block — two-tier rule enforced. |
| **GRO-848 remains open >30 days from this spec's date (2026-05-08 → 2026-06-07)** | Re-triage Phases 4 + 5 with a new dispatch revision. The current shape assumes the AtlasView contract closes within a normal authoring cycle. A multi-month stall invalidates the deferred-vs-skip decisions and warrants a fresh charter-mapping pass. |
| **Charter (`spec/platform-architecture-charter.md`) updates after this spec is published** | Halt the runner. Re-run charter-mapping audit (lesson #1 below) on every active and queued ticket. **A human authors the revised dispatch spec** (the runner does not speculatively rewrite specs); the new spec supersedes this one via the frontmatter `supersedes` field. |

## Sequencing rationale

Three sequencing decisions are load-bearing. Each is documented here so future planners (human or agent) understand *why* and don't re-derive it.

### 1. GRO-922 + GRO-911 land before any new UI work

The merchant-UI templates layer has 117 files and 1 shared partial. Every screen is bespoke. The charter's longer arc (operator surface migrates to AtlasView) makes the duplication doubly expensive — bespoke screens get re-implemented during migration; documented composable components survive.

GRO-922 lays the substrate; GRO-911 takes the opportunity of the Go-side handler split to extract template duplication as it goes. Subsequent UI tickets (901 / 902 / 903) consume the substrate.

### 2. Identity work is canary-side; AtlasView is parallel

Per D-134 + D-157, canary.go is the IdP runtime; AtlasView is the optional management plane. The full review-finding scope (data-layer predicates, IDOR fixes, test harness, cmd-binary middleware mounts, scope vocabulary, rate limiter, `last_used_at` aggregator) hardens canary's own surfaces and proceeds.

The AtlasView identity-contract work (GRO-848 manifest-consumer pin; GRO-923 counterpart SDD) runs in parallel — human-authored on a different cadence — and does not gate ongoing canary IdP hardening.

### 3. Pipeline correctness is independent

Phase 3 (sub1 advisory lock, sub3 per-merchant Merkle, sub2 tender warn, bull recovery) lives entirely inside `internal/protocol/`. No identity coupling, no template work. It runs on the Phase 2 checkpoint as a gating signal but is structurally independent of Phases 1-2 internals — failure in any sub2/sub3 ticket does not retroactively block work that has already shipped. The single-runner model means this independence is latent, not exploited; named here so a future planner adding a second runner has the dependency map ready.

## What's deferred and why

| Item | Why deferred | Decision after |
|---|---|---|
| GRO-923 (canary-side counterpart SDD) | Authors the consumer-side spec mirroring AtlasView's SDD-013; cannot start until SDD-013 publisher contract is named | GRO-848 closes |
| Manifest-cache library implementation (future ticket, not yet filed) | Implementation of consumer side; depends on GRO-923 closing | GRO-923 closes |
| Item Setup Flow C4 (variant matrix) | Apparel-specific, customer-pulled per screen-decomp spec | A future Bart's-stores apparel engagement |

Items previously listed as deferred (GRO-906, GRO-912, GRO-913, auth-wiring of GRO-904/905, Item Setup UI flows) are **un-deferred per the 2026-05-09 SDD-013 reframing**. They were wrongly parked behind the now-corrected charter assumption. See the Revision callout at the top of this spec.

## What we learned in triage

### Durable engineering lessons

Call-outs for future planners working in this codebase. These persist beyond this dispatch.

1. **Charter ratification is a planning trigger.** When `spec/platform-architecture-charter.md` (or any peer-level architectural commitment doc) updates, run a charter-mapping audit on every active dispatch ticket *before* the runner consumes more cycles. The cost of re-mapping is hours; the cost of building on the wrong substrate is weeks of re-work.

2. **`ClaimsFromContext` is the right boundary.** Whatever middleware verifies the inbound credential, the *consumers* should pull tenant from `identity.ClaimsFromContext(r.Context()).TenantID`. This insulates handler code from identity-issuer changes — exactly the property needed across a delegation migration.

3. **117 templates, 1 partial is a smell.** When the templates count grows past ~30 with no shared partial extraction, that's structural debt accumulating silently. UI tickets should be reviewed for component candidates before merge, not after.

4. **Read peer-architecture commitments before assuming what they imply.** The original 2026-05-08 spec read the Platform Architecture Charter as committing canary.go to migrate identity away. The 2026-05-09 SDD-013 dispatch + D-157 made the actual model explicit: canary stays the IdP runtime; AtlasView is the optional management plane; the two compose. The cost of the misread was a triage round that wrongly parked seven tickets and shaped two phase headings around a transition that wasn't happening. Charter-mapping audits should pull the peer-architecture spec authoring directly (Ruptiv repo: `spec/`, `product/atlasview/decisions.md`, `dispatches/`), not infer from charter language alone.

### Process notes (this dispatch only)

5. **Phase checkpoints must be real Linear tickets, not just doc sections.** Without a ticket the runner can pick up, "checkpoint" becomes "thing humans were supposed to remember to do." CK1-CK4 are mandatory dispatch units in this spec; future dispatches should follow suit.

6. **Vision fit must be explicit.** The active execution queue is narrower than the full Ruptiv vision by design. Use `docs/architecture/canary-go-vision-fit-matrix.md` before reshaping priorities so agents can distinguish local hardening, merchant product work, AtlasView subscriber contracts, retail capability expansion, and company-memory updates.

## Out of scope

This dispatch does **not** cover:

- AtlasView-side implementation of the identity delegation contract (lives in the Ruptiv repo).
- Detailed schema migration spec for any `app.api_keys` deprecation (will be authored as part of GRO-848 closeout).
- Item Setup Flow C4 (variant matrix) — apparel-specific, deferred.
- Other Linear backlog items not listed here. They stay in `Backlog` until the runner finishes Phase 5 or a human re-prioritizes.
- Production deploy gates. There is no production. "Logical and measured" here means dependency-correct and checkpointed, not exposure-windowed.
- AtlasView component-library convergence (whether Canary's `templates/components/` aligns with whatever AtlasView ships) — separate question for after charter step 4.

## Cross-references

- Platform Architecture Charter: `spec/platform-architecture-charter.md` (Ruptiv repo).
- AtlasView identity contract substrate: `docs/decisions/gro-848-atlasview-identity-integration.md`.
- Code review findings: comments on GRO-904 through GRO-917 in Linear.
- UI components directive: `~/.claude/projects/-Users-gclyle-CanaryGo/memory/feedback_ui_reusable_components.md`.
- CLAUDE.md — repository operating rules (sqlc discipline, two-tier migration rule, Never-list).
- `docs/conventions.md` — HTTP handler conventions, error envelope, sqlc rule (amended 2026-05-03).
- `docs/architecture/`, `docs/decisions/`, and `docs/conventions/` — active architecture, decision, and implementation standards.
- Item Setup screen decomp: `Brain/wiki/cards/canary-item-setup-screen-decomp.md`.
- Item Master + Catalog model: `Brain/wiki/cards/canary-item-master-and-catalog.md`.

## Status

Active. This spec governs the runner's pickup order and the per-phase contracts until either (a) all phases close, (b) a charter-level update triggers another triage, or (c) a human re-shapes the queue.
