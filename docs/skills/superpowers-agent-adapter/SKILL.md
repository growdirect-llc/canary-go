---
name: superpowers-agent-adapter
description: Use when applying Superpowers-style workflows across agent platforms such as Claude Code, Codex, Copilot, Gemini, or other Agent Skills-compatible clients.
license: CC-BY-4.0
compatibility: Portable Agent Skills format. Requires an agent client that can read SKILL.md instructions; optional support for git, subagents, and shell commands improves execution.
metadata:
  version: "0.1.0"
  scope: "cross-platform-agent-workflows"
---

# Superpowers Agent Adapter

## Purpose

Apply Superpowers methodology across agent platforms without binding the
workflow to one product's tool names. Preserve the intent: careful
planning, parallel-safe delegation, branch safety, TDD where it fits,
technical review, and evidence before completion.

## Platform Rule

Follow this order:

1. System and safety instructions from the active agent client.
2. User instructions and repo instructions such as `AGENTS.md`,
   `CLAUDE.md`, `GEMINI.md`, or project docs.
3. Active tool availability and sandbox limits.
4. Superpowers workflow intent.
5. Literal platform-specific wording in any imported skill.

If an instruction names a tool that does not exist in the current
platform, translate the intent to the nearest safe platform capability.

## Portable Concepts

| Workflow concept | Claude-style term | Codex-style term | Portable rule |
|---|---|---|---|
| Skill activation | `Skill` tool | read loaded `SKILL.md` | Load only the needed skill body and follow the relevant section. |
| Task tracking | `TodoWrite` | `update_plan` or checklist | Track work visibly with one active item per lane. |
| Parallel work | `Task(...)` | `spawn_agent` | Use only when the user requested agents/delegation or the platform permits it. |
| Work isolation | worktree skill | branch/worktree tools | Use isolated branches/worktrees when safe; never fight the host harness. |
| Review | reviewer subagent | reviewer subagent or local review | Verify spec compliance before code quality. |
| Finish | merge/PR workflow | GitHub connector, `gh`, or local git | Do not merge/push/delete unless authorized. |

## Branch And Worktree Defaults

- Prefer the active platform's native branch/worktree mechanism.
- If no native mechanism exists, create a normal git branch.
- Use platform-appropriate branch prefixes:
  - Codex: `codex/`
  - Claude: follow repo convention, or `claude/` if the repo uses it.
  - Unknown platform: `agent/`
- Do not create nested worktrees.
- Do not delete worktrees unless you created them and the user authorized
  cleanup.

## Brainstorming Gate

Use brainstorming for genuinely new direction:

- New product or UX surfaces.
- New architecture.
- Ambiguous behavior.
- Multi-subsystem design.

Do not force a full brainstorming gate for:

- Docs-only cleanup requested by the user.
- Updating an existing plan/spec to resolve contradictions.
- Mechanical dependency, security, scanner, or config remediation.
- Reviewing code or explaining tradeoffs.

If the user already approved the direction in conversation, capture that
as approval context and proceed.

## TDD Gate

Use strict TDD for behavior-changing code:

- Bug fixes.
- New features.
- Parser behavior.
- Middleware/security enforcement.
- UI interaction behavior.
- Data mutation behavior.

For docs, research, dispatches, config, allowlists, or generated files,
use verification probes instead:

- Placeholder scan.
- Stale-name scan.
- Schema or frontmatter validation.
- Expected-file checks.
- Targeted grep acceptance probes.

## Parallel Agent Gate

Use parallel agents only when lanes are independent and file ownership is
disjoint.

Before dispatching:

1. Name each lane.
2. Assign exact owned files.
3. Define acceptance probes.
4. State forbidden files.
5. Require each agent to report changed files.

If two lanes need the same file, serialize them.

## Completion Claims

Do not claim completion without fresh evidence.

| Work type | Evidence |
|---|---|
| Docs | changed file list plus placeholder/stale-term scan |
| Go code | scoped test plus `go build ./...` when feasible |
| Security | named scanner/probe output or exact blocker |
| Docker | build/run probe |
| Agent work | inspected diff plus acceptance probe |

Say what was not verified.

## Review Discipline

For each substantial task:

1. Spec compliance: did the change meet the plan and avoid extra scope?
2. Quality: is the implementation maintainable, minimal, and safe?
3. Verification: did the named acceptance probe pass?

Fix spec gaps before quality review. Do not accept "close enough" when a
requirement is missing.

## Repo Instruction Compatibility

Repo instructions win over generic workflow advice. For example, in
CanaryGo:

- Use `canary_gcp` and `canary_gcp_test`.
- Use Valkey DB 2.
- Use pgx/v5.
- Respect the repo's amended sqlc rule.
- For schema changes, update both declarative schema and migrations.
- For UI work, keep Go SSR first and use component contracts.

Other repos should replace this section with their local constraints.
