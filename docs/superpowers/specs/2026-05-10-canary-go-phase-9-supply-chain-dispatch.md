---
title: Canary.GO Phase 9 — Supply-Chain & Infra Hardening Dispatch
date: 2026-05-10
status: active
authors: Geoff Lyle (with Claude Opus 4.7)
supersedes-section-of: docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md (§Phase 9)
last-revised: 2026-05-10
---

# Canary.GO Phase 9 — Supply-Chain & Infra Hardening Dispatch

## Position in the unified plan

This dispatch is a successor to the 2026-05-08 unified plan, scoped to
**Phase 9 (Supply Chain & Infra Hardening)**. It is authored after
Phases 4, 6, 7, and 8 have closed (and Phase 9's first ticket
GRO-934 already shipped), so the queue is mostly mechanical
remediation rather than design.

The status claims in this file reflect the queue state at authoring.
A fresh execution session MUST verify Linear and `main` before picking
up the next ticket; if Linear disagrees with this document, Linear
wins and the runner should add a short note to the handoff.

The 2026-05-08 plan remains the canonical map of the broader
program. This dispatch lands here so the runner has a single doc to
consult for Phase 9 acceptance criteria + sequencing without paging
back through the older spec's Phase 9 table.

## Snapshot of what's done

The runner has closed every High and most Medium tickets from Phases
4-8 since 2026-05-08. The remaining queue before any Phase 5 UI
work or Phase 10 RBAC starts:

| Phase | Status |
|---|---|
| 4 — Identity Hardening | **closed** (CK4 GRO-921 green) |
| 6 — Tenant + Ops Boundary | **closed** (GRO-928/929/930/933 in main) |
| 7 — MCP Hardening | **closed** (GRO-935/936/937/938/939 in main) |
| 8 — Protocol & Data Integrity | **substrate landed**; GRO-952 wiring + crypto-erasure phased |
| **9 — Supply Chain & Infra Hardening** | **THIS DISPATCH** — 1 of 10 done (GRO-934); 9 to go |
| 5 — Frontend Product (Item Setup) | gated on GRO-922 + CK2; not started |
| 10 — RBAC + PII Scopes | needs brainstorm; gated on GRO-848 |

This dispatch only governs the Phase 9 batch. Phase 5 + Phase 10
remain on the prior dispatch's terms.

## Mission

Land the remaining nine Phase 9 tickets so a fresh `govulncheck`,
`gosec`, `trivy`, and `gitleaks` pass cleanly (or with documented
narrow allowlists) on `main`, every runtime image runs as non-root,
and the `go test -race ./...` suite is green. After this batch the
gateway + service binaries should be publishable to a production
registry without a security-team override.

## Runner model

Same as the parent plan: single long-lived runner, source of truth
is Linear, pickup order is `priority desc, identifier asc, no open
blockers`.

Each cycle should run in an isolated workspace or branch following
the active agent environment's conventions. In Codex, use a
`codex/gro-NNN-slug` branch or an isolated worktree if the session is
configured for worktrees. Do not hardcode `.claude/worktrees`; that
path is a Claude-runner convention, not a repo requirement.

Canonical loop:

1. Verify Linear, `main`, and existing PR state.
2. Mark exactly one ticket In Progress.
3. Implement the smallest fix that satisfies the ticket.
4. Add a named acceptance probe that fails before the fix where
   practical.
5. Run `go build ./...`, the named test/probe, and the relevant
   security command.
6. Commit with the standard footer and open a PR.
7. Mark the ticket In Review with the PR link.
8. Merge only when the operator or repo workflow authorizes merge.
9. After merge, pull `main`, mark Done, remove the isolated
   workspace, and report ticket + PR + next pickup in <=5 lines.

`go test -tags=integration` env vars unchanged from prior dispatch:

```
DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable
IDENTITY_DATABASE_URL=postgres://growdirect:growdirect_dev@localhost:5432/canary_identity_gcp_test?sslmode=disable
VALKEY_URL=redis://:valkey_dev@localhost:6379/2
SESSION_SECRET=test-session-secret-at-least-32-bytes!
INTERNAL_SERVICE_SECRET=test-internal-secret
```

## Phase 9 queue

Nine remediation tickets remain, plus the CK8 checkpoint. The runner
picks implementation tickets in **priority desc, identifier asc**
order unless Linear blockers say otherwise. Most are isolated
<=30-line changes; a few have non-trivial blast radius and are
flagged below.

| Ticket | What | Blast | Blockers |
|---|---|---|---|
| [GRO-940](https://linear.app/growdirect/issue/GRO-940) | Upgrade vulnerable `btcd v0.20.1-beta` chain to a patched line. Sub3 anchor signing path depends on the btcd primitives — confirm the upgrade preserves signet behavior under `internal/protocol/sub3` integration tests | medium | none |
| [GRO-941](https://linear.app/growdirect/issue/GRO-941) | dbcheck/hello/identity Dockerfiles run as root. Mirror `Dockerfile.gateway`'s `USER nonroot:nonroot` + `chown` for the binary path. Verify the entrypoint still works under the non-root UID | small | none |
| [GRO-942](https://linear.app/growdirect/issue/GRO-942) | Add `ReadHeaderTimeout` (slowloris) to every `cmd/*` HTTP server via a new `cmdutil` helper. Default 10s; configurable per-binary | small | none |
| [GRO-943](https://linear.app/growdirect/issue/GRO-943) | Fix the SSE test data race so `go test -race ./internal/web/middleware/streamsse/...` is clean. Likely a closure capture in the broadcast loop | small | none |
| [GRO-944](https://linear.app/growdirect/issue/GRO-944) | LangGraph SSRF: allowlist URL host/scheme; structured URL building for LangGraph client/proxy surfaces. Current likely code surfaces are `internal/devops/langgraph.go` and `/devops/qa-agent` rendering in `internal/web/devops/devops.go`; confirm the exact vulnerable path before patching | medium | depends on GRO-929 ✓ |
| [GRO-945](https://linear.app/growdirect/issue/GRO-945) | Clean / allowlist gitleaks + trufflehog noise. Add `.gitleaks.toml` + `.trufflehog/exclude.txt` covering test fixtures + the dev-key bootstrap path | small | none |
| [GRO-946](https://linear.app/growdirect/issue/GRO-946) | Bound-check int32 narrowing in the Counterpoint POS parser (`internal/adapters/counterpoint/parser.go` numeric coercions). Add a regression test that feeds an over-int32 quantity and asserts the parser returns a typed `ErrInvalidQuantity` instead of silently truncating | small | none |
| [GRO-947](https://linear.app/growdirect/issue/GRO-947) | Pin reproducible scanner toolchain in `Makefile` + `scripts/`. Add a `make security-scan` target that runs the standardized versions of `govulncheck`, `gosec`, `trivy`, `gitleaks` and exits non-zero on findings outside the allowlist | small | none |
| [GRO-948](https://linear.app/growdirect/issue/GRO-948) | Centralize cookie security attributes in `internal/web/cookie` (or similar). `Secure=true` MUST be derived from `ENV=production`, never from the forwarded-proto header. Audit every cookie-setting site (CSRF, session, demo) and route through the helper | medium | none |
| **CK8** (new ticket needed if it does not already exist) | Phase 9 checkpoint: `govulncheck`, `trivy`, `gosec`, `gitleaks` clean or narrowly allowlisted; `go test -race ./...` green; all runtime images run as non-root | medium | 934, 940, 941, 942, 943, 944, 945, 946, 947, 948 |

### Sequencing

The parent unified dispatch already defines **CK8** for Phase 9. This
dispatch makes that checkpoint explicit because Phase 9 is only done
when the individual fixes and the cross-cutting security/race/image
probes are green together.

Per the unified-plan §Failure handling, the runner stops on first
failed build/test/merge, but otherwise proceeds linearly through the
implementation tickets, then runs CK8 as the close-out ticket.

If Linear's priority ordering surfaces GRO-945 before GRO-947, GRO-945
may use direct tool invocations for gitleaks/trufflehog. After GRO-947
lands, rerun GRO-945's findings through `make security-scan` so the
allowlists and pinned scanner target agree.

After Phase 9 closes, the runner is unblocked for **either** of:

- Phase 5 UI (GRO-901/902/903) once GRO-922 substrate + CK2 are in,
- Phase 10 brainstorm (GRO-950 RBAC + GRO-951 PII scopes) — needs a
  human-led brainstorm session before the runner can pick up.

The product-manager agent's research dispatch
(`2026-05-10-open-commerce-component-research-dispatch.md`) feeds
Phase 5's vocabulary; engineering execution does not gate on it.

## Per-ticket Definition of Done

Same shape as the unified plan:

```
Done when:
  - go build ./... clean
  - go test ./... green (or named subset for scoped fixes)
  - acceptance probe: 1-3 lines describing a curl, SQL query, or
    test name that proves the ticket's stated outcome
  - PR opened, status → In Review, link in Linear

On merge → status → Done, runner removes worktree.
```

Worked acceptance probes for the Phase 9 tickets:

- **GRO-940 (btcd):** `make vulncheck` clean for the btcd transitive
  chain; `go test -tags=integration ./internal/protocol/sub3/...`
  green (signet anchor still produces a valid Merkle proof).
- **GRO-941 (Dockerfiles non-root):** for each affected image,
  `docker run --rm <img> id` reports `uid=65532(nonroot)` (or
  whatever distroless USER resolves to); `docker run --rm <img>
  /<binary> --version` works.
- **GRO-942 (ReadHeaderTimeout):** new
  `internal/cmdutil/server_test.go` test asserts the constructed
  `*http.Server` carries a non-zero `ReadHeaderTimeout`. Add or use a
  helper such as `cmdutil.NewHTTPServer(handler)` and migrate the
  simple `cmd/*` binaries to it. Acceptance grep:
  `rg -n "&http\\.Server\\{" cmd internal --glob "*.go"` should show
  only the helper/tests and the explicitly audited sub2/sub3 servers
  that already set `ReadHeaderTimeout`.
- **GRO-943 (SSE race):** `go test -race ./internal/web/middleware/streamsse/...`
  green across 100 iterations.
- **GRO-944 (LangGraph SSRF):** new `TestLangGraphProxy_RejectsNonAllowlistedHost`
  unit test seeds a request whose `target` URL is `http://evil.example/`
  and asserts 400 `host_not_allowed`, or, if the vulnerable surface is
  the `internal/devops.LangGraphClient` base URL instead of a request
  proxy, add the equivalent constructor/client test that rejects
  non-allowlisted scheme/host before issuing a network request.
- **GRO-945 (gitleaks):** `make security-scan` (post-GRO-947) or the
  current invocation reports zero findings on the working tree.
- **GRO-946 (int32 narrow):** new test in
  `internal/adapters/counterpoint/parser_test.go` feeds quantity
  `int64(math.MaxInt32) + 1` and asserts `ErrInvalidQuantity`.
- **GRO-947 (scanner pinning):** `make security-scan` exists and
  runs to completion on a clean checkout; the Makefile target
  references pinned tool versions, not `@latest`.
- **GRO-948 (cookie security):** `grep -rn "http.Cookie{" cmd/ internal/`
  should show every site routing through the central helper; new
  test asserts the helper emits `Secure=false` when ENV is unset
  and `Secure=true` when `ENV=production`, regardless of any
  `X-Forwarded-Proto` header value.
- **CK8 (checkpoint):** run `make security-scan`, `go test -race ./...`,
  and the runtime image non-root probes for gateway, identity, hello,
  and dbcheck. CK8 cannot close while any Phase 9 implementation ticket
  is unmerged or while scanner findings exist outside documented narrow
  allowlists.

## Failure handling

Same as the unified plan. Stop on first failed build / test / merge,
mark the ticket `Blocked` with logs, surface to the operator. No
speculative alternate-fix attempts. No bypass of pre-commit hooks.

The pre-existing `internal/web/` integration-test redirect
failures (handler returns 302 to /login on no-store stub paths,
flagged repeatedly during the Phase 4 batch) are still on `main`
and are NOT introduced by Phase 9 work. The runner SHOULD ignore
them when checking "did my PR break anything" — they fail with or
without Phase 9 changes — but a separate ticket to fix the auth
middleware mounting or the test fixtures should be filed if it
hasn't been already (the spec calls this out in the original
dispatch's "Failure handling" table).

## Out of scope

This dispatch does NOT cover:

- Phase 5 UI flows (GRO-901/902/903). Those gate on GRO-922 + CK2
  per the unified plan.
- Phase 10 RBAC + PII scopes (GRO-950/951). Need human brainstorm.
- GRO-952 follow-on phases (Phase 2 wiring through Phase 5
  retention). Stay in the GRO-952 ADR's queue.
- The Open Commerce / NRF / ARTS research dispatch
  (`2026-05-10-open-commerce-component-research-dispatch.md`).
  Different surface; runs in parallel under product management.

## Cross-references

- Parent plan: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`
- Vision fit matrix: `docs/architecture/canary-go-vision-fit-matrix.md`
- ADR fed by Phase 8 GRO-952: `docs/decisions/gro-952-pii-redaction-policy.md`
- Component-led UI vision: `docs/architecture/component-led-ui-vision.md`
- Research dispatch (parallel): `docs/superpowers/specs/2026-05-10-open-commerce-component-research-dispatch.md`
- CLAUDE.md / AGENTS.md — runner operating contract.

## Status

Active. Governs Phase 9 pickup order until the queue drains or a
human re-shapes it.
