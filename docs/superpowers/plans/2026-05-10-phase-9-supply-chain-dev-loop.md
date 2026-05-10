# Canary Go Phase 9 Supply Chain Dev Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute the Phase 9 supply-chain and infrastructure hardening queue through CK8 with clean scanner, race, and runtime-image evidence.

**Architecture:** Treat each GRO ticket as an isolated PR with its own failing-before/passing-after acceptance probe. Keep Linear as the execution source of truth, use the Phase 9 dispatch for technical acceptance, and reserve CK8 for the cross-cutting proof that all security, race, and image hardening works together.

**Tech Stack:** Go 1.22+, Chi v5, pgx/v5, Docker/distroless, PostgreSQL 17 test DBs, Valkey DB 2, `govulncheck`, `gosec`, `trivy`, `gitleaks`, `trufflehog`.

---

## Source Documents

- Dispatch: `docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md`
- Parent dispatch: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`
- Repo context: `AGENTS.md`
- Component/product context that runs in parallel, not as a blocker:
  - `docs/architecture/component-led-ui-vision.md`
  - `docs/superpowers/specs/2026-05-10-open-commerce-component-research-dispatch.md`

## Fresh Session Kickoff Prompt

Use this prompt in the new session:

```text
We are in /Users/gclyle/CanaryGo. Execute docs/superpowers/plans/2026-05-10-phase-9-supply-chain-dev-loop.md against docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md.

First verify git status, Linear state, main branch freshness, and whether CK8 exists. Then pick the next unblocked Phase 9 ticket by Linear priority desc / identifier asc. Keep each GRO ticket in its own branch or isolated worktree, add a named acceptance probe, run the scoped verification, open a PR, and stop before merge unless explicitly authorized. Do not touch Phase 5 UI, Phase 10 RBAC, or GRO-952 follow-on work.
```

## Execution Environment

Use these integration values when a ticket needs integration tests:

```bash
export DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable'
export IDENTITY_DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_identity_gcp_test?sslmode=disable'
export VALKEY_URL='redis://:valkey_dev@localhost:6379/2'
export SESSION_SECRET='test-session-secret-at-least-32-bytes!'
export INTERNAL_SERVICE_SECRET='test-internal-secret'
```

## File Map

- `go.mod`, `go.sum`: dependency updates for GRO-940.
- `deploy/Dockerfile.gateway`, `deploy/Dockerfile.identity`, `deploy/Dockerfile.hello`, `deploy/Dockerfile.dbcheck`: non-root runtime image verification for GRO-941 and CK8.
- `internal/cmdutil/server.go`, `internal/cmdutil/server_test.go`, `cmd/*/main.go`: shared HTTP server construction for GRO-942.
- `internal/web/middleware/streamsse/`: expected SSE race surface for GRO-943.
- `internal/devops/langgraph.go`, `internal/devops/langgraph_test.go`, `internal/web/devops/devops.go`, `internal/web/devops/devops_test.go`: likely LangGraph and `/devops/qa-agent` SSRF surfaces for GRO-944.
- `.gitleaks.toml`, `.trufflehog/exclude.txt`: expected allowlist files for GRO-945.
- `Makefile`, `scripts/`: reproducible scanner tooling for GRO-947.
- `internal/adapters/counterpoint/parser.go`, `internal/adapters/counterpoint/parser_test.go`: int32 narrowing fix for GRO-946.
- `internal/web/cookie/`, `internal/web/handler.go`, `internal/squareauth/handler.go`: central cookie helper for GRO-948.

## Task 1: Reconcile The Queue

**Files:**
- Read: `docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md`
- Read: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`
- Read: `AGENTS.md`

- [ ] **Step 1: Inspect repo state**

Run:

```bash
git status --short
git branch --show-current
git log --oneline -5
```

Expected: identify current branch, any dirty files, and whether local work belongs to this plan. Do not revert unrelated files.

- [ ] **Step 2: Verify Phase 9 dispatch consistency**

Run:

```bash
rg -n "GRO-940|GRO-941|GRO-942|GRO-943|GRO-944|GRO-945|GRO-946|GRO-947|GRO-948|CK8" docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
```

Expected: both docs agree that CK8 is the Phase 9 close-out checkpoint and that Phase 9 includes GRO-934 plus GRO-940 through GRO-948.

- [ ] **Step 3: Verify actual code surfaces before picking a ticket**

Run:

```bash
rg -n "&http\\.Server\\{|ReadHeaderTimeout|RunServer|LangGraph|qa-agent|http\\.Cookie\\{|security-scan|govulncheck|gitleaks|trufflehog" cmd internal deploy Makefile -g "*.go" -g "Makefile" -g "Dockerfile*"
```

Expected: confirm current code still matches the dispatch's likely file map. If a surface has moved, update the ticket note before implementation.

- [ ] **Step 4: Verify Linear state**

Use Linear to check GRO-940 through GRO-948 and CK8. Expected: choose the next unblocked ticket by priority desc / identifier asc. If CK8 does not exist, create or request it before declaring Phase 9 complete.

- [ ] **Step 5: Report the selected ticket**

Report:

```text
Selected: GRO-XXX
Why: highest-priority unblocked Phase 9 ticket in Linear
Scope: <one sentence>
Acceptance probe: <one command or test>
Merge posture: PR only unless operator authorizes merge
```

## Task 2: Per-Ticket Development Loop

**Files:**
- Modify only the files required by the selected GRO ticket.
- Do not batch unrelated Phase 9 tickets in one PR unless the ticket explicitly requires it.

- [ ] **Step 1: Create isolated work**

Run one of:

```bash
git switch -c codex/gro-XXX-short-slug
```

or, when the session is configured for worktrees:

```bash
git worktree add ../canarygo-gro-XXX -b codex/gro-XXX-short-slug main
```

Expected: the ticket has an isolated branch or worktree. Branch names use the `codex/` prefix.

- [ ] **Step 2: Write or identify the failing acceptance probe**

Use the dispatch's probe for the selected ticket:

```text
GRO-940: make vulncheck plus go test -tags=integration ./internal/protocol/sub3/...
GRO-941: docker image user probe for gateway, identity, hello, dbcheck
GRO-942: internal/cmdutil/server_test.go plus grep for unaudited &http.Server literals
GRO-943: go test -race ./internal/web/middleware/streamsse/...
GRO-944: LangGraph allowlist/SSRF rejection unit test
GRO-945: gitleaks/trufflehog direct invocation or make security-scan after GRO-947
GRO-946: Counterpoint over-int32 quantity parser test
GRO-947: make security-scan target with pinned versions
GRO-948: cookie helper tests plus grep for direct http.Cookie construction
CK8: make security-scan, go test -race ./..., runtime image non-root probes
```

Expected: the probe fails before the fix, or the runner documents why pre-fix failure cannot be safely reproduced.

- [ ] **Step 3: Implement the smallest ticket-scoped fix**

Follow these boundaries:

```text
GRO-940: dependency update only plus signet/sub3 verification.
GRO-941: Dockerfile user/ownership changes only.
GRO-942: cmdutil HTTP server helper plus simple cmd binary migration.
GRO-943: SSE race fix only.
GRO-944: URL scheme/host allowlist and structured URL construction only.
GRO-945: secrets scanner allowlists only; do not hide real secrets.
GRO-946: explicit numeric bound check and typed parser error.
GRO-947: scanner pinning and make target only.
GRO-948: cookie helper and call-site migration only.
CK8: verification and any tiny documentation/allowlist reconciliation; no unrelated feature work.
```

Expected: no unrelated refactors, schema changes, UI work, or Phase 10 work.

- [ ] **Step 4: Run base verification**

Run:

```bash
go build ./...
go test ./...
```

Expected: both pass, or any pre-existing unrelated failure is documented with evidence that it reproduces on `main`.

- [ ] **Step 5: Run the ticket acceptance probe**

Run the selected ticket's named probe from Step 2. Expected: it passes after the implementation.

- [ ] **Step 6: Inspect the diff**

Run:

```bash
git diff --stat
git diff --check
git diff
```

Expected: diff is ticket-scoped, has no whitespace errors, and does not touch unrelated user changes.

- [ ] **Step 7: Commit**

Run:

```bash
git add <ticket-scoped-files>
git commit -m "fix: harden GRO-XXX surface"
```

Expected: commit contains only the selected ticket's files.

- [ ] **Step 8: Open PR and update Linear**

Open a PR using the repo's active GitHub workflow. Then update Linear:

```text
Status: In Review
Comment: PR <url>; acceptance probe <command/test> passed
```

Expected: the ticket is not marked Done until the PR merges.

## Task 3: CK8 Close-Out Loop

**Files:**
- Read: all Phase 9 PRs/tickets.
- Modify: only documentation or allowlist files if a scanner finding is a documented false positive.

- [ ] **Step 1: Confirm every implementation ticket is merged or intentionally excluded**

Check GRO-934 and GRO-940 through GRO-948. Expected: every ticket is Done, merged, or explicitly documented as excluded by the operator.

- [ ] **Step 2: Run the scanner gate**

Run:

```bash
make security-scan
```

Expected: exits zero. Findings are allowed only if they are narrow, documented, and committed through the Phase 9 allowlist policy.

- [ ] **Step 3: Run the race gate**

Run:

```bash
go test -race ./...
```

Expected: exits zero. If a package has a pre-existing race unrelated to Phase 9, CK8 fails and a follow-up ticket must be linked; do not close CK8 green.

- [ ] **Step 4: Run non-root image probes**

Build and run the runtime images:

```bash
docker build -f deploy/Dockerfile.gateway -t canary-gateway:ck8 .
docker build -f deploy/Dockerfile.identity -t canary-identity:ck8 .
docker build -f deploy/Dockerfile.hello -t canary-hello:ck8 .
docker build -f deploy/Dockerfile.dbcheck -t canary-dbcheck:ck8 .
docker run --rm canary-gateway:ck8 id
docker run --rm canary-identity:ck8 id
docker run --rm canary-hello:ck8 id
docker run --rm canary-dbcheck:ck8 id
```

Expected: each `id` output reports the non-root runtime user used by the image.

- [ ] **Step 5: Record CK8 evidence**

Add a Linear comment:

```text
CK8 evidence:
- make security-scan: PASS
- go test -race ./...: PASS
- runtime images non-root: gateway PASS, identity PASS, hello PASS, dbcheck PASS
- Phase 9 PRs: paste the merged PR URLs for GRO-934 and GRO-940 through GRO-948, or state the operator-approved exclusion beside any missing ticket
```

Expected: CK8 has enough evidence for a reviewer to validate the checkpoint without reading the full session transcript.

- [ ] **Step 6: Close or escalate**

If all CK8 evidence passes, mark CK8 Done. If any evidence fails, mark CK8 Blocked, link the failing ticket or create a new follow-up, and stop Phase 9 close-out.

## Stop Conditions

- Stop before merging any PR unless the operator authorizes merge.
- Stop if Linear and the dispatch disagree about blockers.
- Stop if `go build ./...` fails for a reason introduced by the current branch.
- Stop if scanner output reveals a real secret or live credential.
- Stop if CK8 fails; do not proceed to Phase 5 or Phase 10 under a red checkpoint.

## Completion Signal

Phase 9 is complete only when CK8 is Done with evidence for:

```text
make security-scan PASS
go test -race ./... PASS
runtime images non-root PASS
GRO-934 and GRO-940 through GRO-948 Done or operator-excluded
```
