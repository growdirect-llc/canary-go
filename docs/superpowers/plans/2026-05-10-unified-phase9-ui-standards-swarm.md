# Canary Go Unified Phase 9 + UI Standards Swarm Execution Plan

> **For agentic workers:** REQUIRED SUB-SKILLS: Use superpowers:dispatching-parallel-agents for parallel lanes, then superpowers:subagent-driven-development or superpowers:executing-plans inside each lane. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run Phase 9 supply-chain hardening through CK8 while, in parallel, completing the UI standards prep needed before the next frontend build loop.

**Architecture:** Split work into independent lanes with disjoint file ownership, then merge through coordinator review. Phase 9 code/security work and UI standards docs can run in parallel. CK8 is the final security checkpoint and does not start until every Phase 9 implementation ticket is merged or explicitly excluded by the operator.

**Tech Stack:** Go 1.22+, Chi v5, pgx/v5, Docker/distroless, PostgreSQL 17 test DBs, Valkey DB 2, `govulncheck`, `gosec`, `trivy`, `gitleaks`, `trufflehog`, Markdown docs.

---

## Source Plans And Dispatches

- Phase 9 dispatch: `docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md`
- Phase 9 dev loop: `docs/superpowers/plans/2026-05-10-phase-9-supply-chain-dev-loop.md`
- UI standards prep dispatch: `docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md`
- UI standards prep loop: `docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md`
- Parent unified dispatch: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`
- Repo context: `AGENTS.md`

## Fresh Session Kickoff Prompt

Use this prompt in the fresh execution session:

```text
We are in /Users/gclyle/CanaryGo. Execute docs/superpowers/plans/2026-05-10-unified-phase9-ui-standards-swarm.md.

Use a swarm model with disjoint work ownership. Run UI standards docs prep in parallel with Phase 9 implementation lanes. Each agent owns only the files assigned to its lane, does not revert unrelated changes, adds the scoped acceptance evidence, and reports changed files. Do not start CK8 until all Phase 9 implementation tickets are merged or operator-excluded. Do not start Phase 5 UI or Phase 10 RBAC.
```

## Global Rules

- Linear is the source of truth for Phase 9 ticket state and blockers.
- Use `codex/` branch names by default.
- Prefer isolated worktrees for simultaneous implementation lanes.
- No two active agents may edit the same file unless the coordinator explicitly serializes their work.
- Each Phase 9 implementation ticket gets its own branch or worktree and PR unless the operator approves a bundle.
- UI standards prep is docs-only and must not touch `cmd/`, `internal/`, `deploy/`, `go.mod`, `go.sum`, or migrations.
- Do not merge PRs unless the operator or repo workflow authorizes merge.
- CK8 closes Phase 9 only after the merged code on `main` passes the CK8 gates.

## Shared Integration Environment

Use these values for integration tests when needed:

```bash
export DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_gcp_test?sslmode=disable'
export IDENTITY_DATABASE_URL='postgres://growdirect:growdirect_dev@localhost:5432/canary_identity_gcp_test?sslmode=disable'
export VALKEY_URL='redis://:valkey_dev@localhost:6379/2'
export SESSION_SECRET='test-session-secret-at-least-32-bytes!'
export INTERNAL_SERVICE_SECRET='test-internal-secret'
```

## Swarm Lanes

| Lane | Agent | Scope | File ownership | Can run in parallel with |
|---|---|---|---|---|
| 0 | Coordinator | Verify repo, Linear, branches, blockers, and merge order | No implementation files unless resolving plan docs | All lanes |
| 1A | UI Vocabulary | Retail vocabulary decision | `docs/decisions/ui-retail-vocabulary.md` | 1B, 1C, Phase 9 lanes |
| 1B | UI Status | Status taxonomy decision | `docs/decisions/ui-status-taxonomy.md` | 1A, 1C, Phase 9 lanes |
| 1C | UI Connector Metadata | Connector metadata convention + PR checklist | `docs/conventions/connector-metadata.md`, `docs/conventions/ui-pr-review-checklist.md` | 1A, 1B, Phase 9 lanes |
| 1D | UI Component Contract | Component convention update | `docs/conventions/ui-components.md` | Phase 9 lanes; after reading 1B output if available |
| 1E | UI Vision Integrator | Link standards docs into architecture vision | `docs/architecture/component-led-ui-vision.md` | Starts after 1A-1D complete |
| 2A | Phase 9 Dependency | GRO-940 btcd dependency | `go.mod`, `go.sum` | 2B, 2C, 2D, 2E |
| 2B | Phase 9 Images | GRO-941 non-root Dockerfiles | `deploy/Dockerfile.*` | 2A, 2C, 2D, 2E |
| 2C | Phase 9 SSE | GRO-943 SSE race | `internal/web/middleware/streamsse/` | 2A, 2B, 2D, 2E |
| 2D | Phase 9 LangGraph | GRO-944 SSRF | `internal/devops/`, `internal/web/devops/` | 2A, 2B, 2C, 2E |
| 2E | Phase 9 Counterpoint | GRO-946 int32 narrowing | `internal/adapters/counterpoint/` | 2A, 2B, 2C, 2D |
| 3A | Phase 9 Server Helper | GRO-942 ReadHeaderTimeout | `internal/cmdutil/`, `cmd/*/main.go` | 3B, 3C if no shared files |
| 3B | Phase 9 Scanner Tooling | GRO-947 scanner pinning | `Makefile`, `scripts/` | 3A, 3C |
| 3C | Phase 9 Cookies | GRO-948 cookie helper | `internal/web/cookie/`, `internal/web/handler.go`, `internal/squareauth/handler.go` | 3A, 3B if no shared files |
| 4A | Phase 9 Secret Scans | GRO-945 gitleaks/trufflehog allowlists | `.gitleaks.toml`, `.trufflehog/exclude.txt` | Starts after 3B if possible |
| 5A | CK8 | Final Phase 9 checkpoint | Verification only, allowlist/docs only if needed | Starts after Phase 9 implementation PRs merge |

## Task 0: Coordinator Bootstrap

**Files:**
- Read: all source plans and dispatches listed above.
- Modify: none unless a plan contradiction blocks execution.

- [ ] **Step 1: Inspect repo state**

Run:

```bash
git status --short
git branch --show-current
git log --oneline -5
```

Expected: identify existing dirty docs and current branch. Do not revert unrelated changes.

- [ ] **Step 2: Verify Phase 9 and CK8 state**

Use Linear to verify GRO-934, GRO-940 through GRO-948, and CK8. Expected: record which tickets are Done, In Review, Todo, Blocked, or missing. If CK8 does not exist, create it or ask the operator to create it before final close-out.

- [ ] **Step 3: Verify work surfaces**

Run:

```bash
rg -n "&http\\.Server\\{|ReadHeaderTimeout|RunServer|LangGraph|qa-agent|http\\.Cookie\\{|security-scan|govulncheck|gitleaks|trufflehog" cmd internal deploy Makefile -g "*.go" -g "Makefile" -g "Dockerfile*"
```

Expected: confirm the lane file ownership table still matches the repo.

- [ ] **Step 4: Create swarm tracker**

Create a local execution note in the session, not necessarily a file, with:

```text
Lane:
Ticket/doc:
Branch/worktree:
Owned files:
Acceptance probe:
Status:
PR:
```

Expected: every active agent has a lane and owned file set.

## Task 1: Dispatch UI Standards Prep Swarm

**Files:**
- Create/modify only:
  - `docs/decisions/ui-retail-vocabulary.md`
  - `docs/decisions/ui-status-taxonomy.md`
  - `docs/conventions/connector-metadata.md`
  - `docs/conventions/ui-pr-review-checklist.md`
  - `docs/conventions/ui-components.md`
  - `docs/architecture/component-led-ui-vision.md`

- [ ] **Step 1: Dispatch Lane 1A, UI Vocabulary**

Agent prompt:

```text
You own Lane 1A. Create docs/decisions/ui-retail-vocabulary.md using docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md Task 2. Read the UI standards dispatch and research docs. Do not touch files outside docs/decisions/ui-retail-vocabulary.md. Return changed files and a short summary.
```

Expected: vocabulary decision exists with Decision, Rationale, Use in Canary, AtlasView mapping, and Review triggers.

- [ ] **Step 2: Dispatch Lane 1B, UI Status**

Agent prompt:

```text
You own Lane 1B. Create docs/decisions/ui-status-taxonomy.md using docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md Task 3. Read the UI standards dispatch and research docs. Do not touch files outside docs/decisions/ui-status-taxonomy.md. Return changed files and a short summary.
```

Expected: status taxonomy exists with status families and allowed tones.

- [ ] **Step 3: Dispatch Lane 1C, Connector Metadata And PR Checklist**

Agent prompt:

```text
You own Lane 1C. Create docs/conventions/connector-metadata.md and docs/conventions/ui-pr-review-checklist.md using docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md Tasks 5 and 6. Read the UI standards dispatch and research docs. Do not touch files outside your two owned docs. Return changed files and a short summary.
```

Expected: connector metadata field contract and PR checklist exist.

- [ ] **Step 4: Dispatch Lane 1D, Component Contract Convention**

Agent prompt:

```text
You own Lane 1D. Update docs/conventions/ui-components.md using docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md Task 4. Add States and Accessibility to the public component header template, add Standards checks, and preserve the existing extraction discipline. Do not touch any other file. Return changed files and a short summary.
```

Expected: component convention now requires states and accessibility.

- [ ] **Step 5: Integrate UI lanes**

Run:

```bash
rg -n "Decision|Rationale|Use in Canary|AtlasView mapping|Review triggers|States:|Accessibility:|Connector Metadata|UI PR Review Checklist" docs/decisions docs/conventions docs/architecture/component-led-ui-vision.md
git diff --name-only docs
```

Expected: UI prep changes are docs-only and complete enough for Lane 1E.

- [ ] **Step 6: Dispatch Lane 1E, UI Vision Integrator**

Agent prompt:

```text
You own Lane 1E. Update docs/architecture/component-led-ui-vision.md using docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md Task 7. Link the new standards docs and explain that they govern implementation while preserving Go SSR and AtlasView-compatible component contracts. Do not touch any other file. Return changed files and a short summary.
```

Expected: component-led UI vision links the new standards docs.

- [ ] **Step 7: Verify UI standards prep**

Run:

```bash
test -f docs/decisions/ui-retail-vocabulary.md
test -f docs/decisions/ui-status-taxonomy.md
test -f docs/conventions/connector-metadata.md
test -f docs/conventions/ui-pr-review-checklist.md
rg -n "States:|Accessibility:|Standards checks" docs/conventions/ui-components.md
rg -n "ui-retail-vocabulary|ui-status-taxonomy|connector-metadata|ui-pr-review-checklist" docs/architecture/component-led-ui-vision.md
```

Expected: all commands pass.

## Task 2: Dispatch Phase 9 Wave 1 Swarm

**Files:**
- Lane-specific Phase 9 files only.

- [ ] **Step 1: Dispatch Lane 2A, GRO-940**

Agent prompt:

```text
You own Lane 2A / GRO-940. Upgrade the vulnerable btcd dependency chain only. Owned files: go.mod and go.sum. Acceptance: make vulncheck clean for the btcd transitive chain and go test -tags=integration ./internal/protocol/sub3/... green, or document any environment blocker. Do not edit unrelated files.
```

- [ ] **Step 2: Dispatch Lane 2B, GRO-941**

Agent prompt:

```text
You own Lane 2B / GRO-941. Make affected runtime Dockerfiles run as non-root. Owned files: deploy/Dockerfile.gateway, deploy/Dockerfile.identity, deploy/Dockerfile.hello, deploy/Dockerfile.dbcheck. Acceptance: build each image and docker run --rm <img> id reports the non-root runtime user. Do not edit unrelated files.
```

- [ ] **Step 3: Dispatch Lane 2C, GRO-943**

Agent prompt:

```text
You own Lane 2C / GRO-943. Fix the SSE test data race. Owned files: internal/web/middleware/streamsse/ only. Acceptance: go test -race ./internal/web/middleware/streamsse/... green across repeated runs. Do not edit unrelated files.
```

- [ ] **Step 4: Dispatch Lane 2D, GRO-944**

Agent prompt:

```text
You own Lane 2D / GRO-944. Fix LangGraph SSRF by validating allowed URL scheme/host and using structured URL construction. Owned files: internal/devops/ and internal/web/devops/ only. Acceptance: a unit test rejects http://evil.example/ before any outbound request, or the equivalent client/base-URL allowlist test if that is the actual surface. Do not edit unrelated files.
```

- [ ] **Step 5: Dispatch Lane 2E, GRO-946**

Agent prompt:

```text
You own Lane 2E / GRO-946. Add explicit int32 bound checks in the Counterpoint parser. Owned files: internal/adapters/counterpoint/parser.go and parser tests. Acceptance: a test feeds int64(math.MaxInt32)+1 and gets ErrInvalidQuantity instead of truncation. Do not edit unrelated files.
```

- [ ] **Step 6: Review and integrate Wave 1**

For each returned lane:

```bash
git diff --stat
git diff --check
go build ./...
go test ./...
```

Also run each lane's named acceptance probe. Expected: no file conflicts and no unrelated changes. Open PRs or stage according to the active repo workflow.

## Task 3: Dispatch Phase 9 Wave 2 Swarm

Wave 2 starts after Wave 1 diffs are reviewed because these lanes touch broader surfaces.

- [ ] **Step 1: Dispatch Lane 3A, GRO-942**

Agent prompt:

```text
You own Lane 3A / GRO-942. Add a cmdutil HTTP server helper that sets ReadHeaderTimeout by default and migrate simple cmd binaries to it. Owned files: internal/cmdutil/ and cmd/*/main.go. Acceptance: internal/cmdutil/server_test.go asserts non-zero ReadHeaderTimeout; rg -n "&http\\.Server\\{" cmd internal --glob "*.go" shows only helper/tests and explicitly audited sub2/sub3 servers. Do not edit unrelated files.
```

- [ ] **Step 2: Dispatch Lane 3B, GRO-947**

Agent prompt:

```text
You own Lane 3B / GRO-947. Pin the reproducible scanner toolchain and add make security-scan. Owned files: Makefile and scripts/ only. Acceptance: make security-scan exists, references pinned versions rather than @latest, and runs to completion or documents missing local scanner binaries. Do not edit unrelated files.
```

- [ ] **Step 3: Dispatch Lane 3C, GRO-948**

Agent prompt:

```text
You own Lane 3C / GRO-948. Centralize cookie security attributes. Owned files: internal/web/cookie/, internal/web/handler.go, internal/squareauth/handler.go, and related tests only. Acceptance: helper emits Secure=false when ENV is unset and Secure=true when ENV=production regardless of X-Forwarded-Proto; grep for direct http.Cookie construction shows only audited helper/test sites. Do not edit unrelated files.
```

- [ ] **Step 4: Review and integrate Wave 2**

Run:

```bash
go build ./...
go test ./...
rg -n "&http\\.Server\\{" cmd internal --glob "*.go"
rg -n "http\\.Cookie\\{" cmd internal --glob "*.go"
```

Expected: Wave 2 satisfies its probes and does not conflict across files.

## Task 4: Dispatch Phase 9 Wave 3

GRO-945 should run after GRO-947 when possible so the final scanner target is available.

- [ ] **Step 1: Dispatch Lane 4A, GRO-945**

Agent prompt:

```text
You own Lane 4A / GRO-945. Clean or narrowly allowlist gitleaks and trufflehog findings. Owned files: .gitleaks.toml and .trufflehog/exclude.txt only unless a real leaked secret requires operator escalation. Acceptance: make security-scan or direct gitleaks/trufflehog invocation reports zero findings outside documented allowlists. Do not hide real secrets; stop and escalate if one appears.
```

- [ ] **Step 2: Review and integrate Wave 3**

Run:

```bash
make security-scan
git diff --stat
git diff --check
```

Expected: scanner findings are clean or narrowly allowlisted.

## Task 5: CK8 Close-Out

**Files:**
- Prefer verification only.
- Modify docs or allowlists only if a scanner false positive requires documented narrow handling.

- [ ] **Step 1: Confirm Phase 9 ticket state**

Verify GRO-934 and GRO-940 through GRO-948 are Done/merged or operator-excluded. Expected: no unmerged Phase 9 implementation ticket remains.

- [ ] **Step 2: Run CK8 scanner gate**

Run:

```bash
make security-scan
```

Expected: PASS.

- [ ] **Step 3: Run CK8 race gate**

Run:

```bash
go test -race ./...
```

Expected: PASS. If it fails, CK8 is Blocked and the failing package gets a linked follow-up.

- [ ] **Step 4: Run CK8 non-root image probes**

Run:

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

Expected: every image reports a non-root runtime user.

- [ ] **Step 5: Record CK8 evidence**

Post this evidence to CK8 in Linear:

```text
CK8 evidence:
- make security-scan: PASS
- go test -race ./...: PASS
- runtime images non-root: gateway PASS, identity PASS, hello PASS, dbcheck PASS
- Phase 9 tickets: GRO-934 and GRO-940 through GRO-948 Done or operator-excluded
- UI standards prep: completed in parallel; not a CK8 gate
```

Expected: CK8 can close only if all Phase 9 gates pass.

## Task 6: Final Swarm Review

- [ ] **Step 1: Confirm scope boundaries**

Run:

```bash
git diff --name-only
```

Expected: UI standards changes are docs-only; Phase 9 changes are limited to their assigned surfaces.

- [ ] **Step 2: Confirm no forbidden names or stale docs**

Run:

```bash
rg -n "canary_go_test|canary_test|Valkey DB 0|There is no checkpoint|fieldalignment|TODO|TBD|<links>" docs cmd internal deploy Makefile
```

Expected: no stale planning markers or forbidden runtime names introduced by this swarm.

- [ ] **Step 3: Produce final report**

Report:

```text
UI standards prep: complete / blocked, files changed
Phase 9 tickets: status by GRO id
CK8: PASS / BLOCKED with evidence
Open PRs:
Follow-ups:
```

## Stop Conditions

- Stop if Linear blockers disagree with the plan.
- Stop if two agents need the same file and the conflict cannot be serialized cleanly.
- Stop if scanner output reveals a real secret or live credential.
- Stop if `go build ./...` fails because of the current branch.
- Stop if CK8 fails.
- Stop before Phase 5 UI or Phase 10 RBAC.

## Completion Signal

This unified swarm is complete only when:

```text
UI standards prep docs exist and are linked.
GRO-940 through GRO-948 are merged or operator-excluded.
CK8 scanner gate passes.
CK8 race gate passes.
CK8 runtime image non-root probes pass.
No Phase 5 UI or Phase 10 RBAC work was started.
```
