# Ruptiv Execution Vault Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the first working `ruptiv-execution-vault` repository as a curated company knowledge base for Ruptiv, AtlasView, Canary Go, retail capabilities, shared platform architecture, and multi-agent HIL development.

**Architecture:** The new repo is Markdown-first and Obsidian-compatible. Canonical files live in a stable folder taxonomy, frontmatter is validated by a lightweight script, and memory-bus indexing remains derived from files with `memory-index: true`. The first wave scaffolds the repo, adds templates and validation, seeds the minimal handoff corpus, then inventories source material for curation without bulk-importing legacy content.

**Tech Stack:** Markdown, YAML frontmatter, Git, Python 3 standard library for validation, shell commands, existing memory-bus seeding scripts to be connected after the vault scaffold is proven.

---

## File Structure

Create the new repository at:

```text
/Users/gclyle/ruptiv-execution-vault/
```

If filesystem permissions block creation outside `/Users/gclyle/CanaryGo`, request write access for `/Users/gclyle/ruptiv-execution-vault` before executing this plan.

Planned files:

```text
README.md
.gitignore
00-start-here/README.md
00-start-here/current-operating-picture.md
00-start-here/how-to-use-this-vault.md
00-start-here/active-context-index.md
00-start-here/glossary.md
10-ruptiv-company/principles.md
20-atlasview/product-brief.md
30-canary-go/project-brief.md
35-retail-capabilities/README.md
35-retail-capabilities/capability-map.md
40-shared-platform/memory/memory-bus-contract.md
50-dev-system/session-protocol.md
50-dev-system/multi-agent-flow.md
50-dev-system/human-gates.md
50-dev-system/dispatch-template.md
50-dev-system/closeout-template.md
60-standards/documentation.md
60-standards/architecture-records.md
60-standards/frontmatter.md
90-intake/README.md
90-intake/source-inventory.md
templates/brief.md
templates/decision.md
templates/standard.md
templates/playbook.md
templates/contract.md
templates/architecture-note.md
templates/retail-capability.md
templates/agent-card.md
templates/dispatch.md
templates/closeout.md
tools/validate_frontmatter.py
tests/test_validate_frontmatter.py
```

Responsibilities:

- `00-start-here/` is the entry point for humans and agents.
- `20-atlasview/`, `30-canary-go/`, and `35-retail-capabilities/` hold current product and domain context.
- `40-shared-platform/` holds reusable platform contracts, starting with memory-bus.
- `50-dev-system/` holds the multi-agent/HIL operating loop.
- `60-standards/` holds reusable standards and validation rules.
- `90-intake/` holds source inventories only; it is not authoritative.
- `templates/` makes future curation repeatable.
- `tools/validate_frontmatter.py` enforces the minimum metadata contract.

## Task 1: Create Repo Skeleton

**Files:**
- Create directory: `/Users/gclyle/ruptiv-execution-vault`
- Create: `/Users/gclyle/ruptiv-execution-vault/.gitignore`
- Create: `/Users/gclyle/ruptiv-execution-vault/README.md`
- Create directories listed in the file structure.

- [ ] **Step 1: Create the repo directory**

Run:

```bash
mkdir -p /Users/gclyle/ruptiv-execution-vault
cd /Users/gclyle/ruptiv-execution-vault
git init
```

Expected: Git reports an initialized empty repository.

- [ ] **Step 2: Create the top-level folders**

Run:

```bash
mkdir -p \
  00-start-here \
  10-ruptiv-company \
  20-atlasview/architecture 20-atlasview/decisions 20-atlasview/dispatches 20-atlasview/substrate 20-atlasview/agents 20-atlasview/connectors 20-atlasview/policies \
  30-canary-go/architecture 30-canary-go/decisions 30-canary-go/contracts 30-canary-go/runbooks 30-canary-go/delivery \
  35-retail-capabilities/process 35-retail-capabilities/technical 35-retail-capabilities/standards-crosswalks 35-retail-capabilities/vendor-crosswalks 35-retail-capabilities/cards \
  40-shared-platform/memory 40-shared-platform/mcp 40-shared-platform/identity 40-shared-platform/audit-and-proof 40-shared-platform/policy 40-shared-platform/protocol 40-shared-platform/integrations \
  50-dev-system/agent-cards 50-dev-system/review-playbooks \
  60-standards \
  90-intake \
  templates \
  tools \
  tests
```

Expected: All folders exist.

- [ ] **Step 3: Write `.gitignore`**

Create `/Users/gclyle/ruptiv-execution-vault/.gitignore`:

```gitignore
.DS_Store
.obsidian/
.trash/
*.tmp
__pycache__/
.pytest_cache/
```

- [ ] **Step 4: Write `README.md`**

Create `/Users/gclyle/ruptiv-execution-vault/README.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [execution-vault, company-knowledge]
---

# Ruptiv Execution Vault

This repository is the curated company knowledge base for Ruptiv development execution.

It supports:

- Ruptiv company ways of working
- AtlasView product and substrate development
- Canary Go architecture and delivery
- Retail capability modeling
- Shared platform contracts
- Multi-agent, human-in-the-loop development cycles

Canonical files are truth. Memory systems are derived indexes. Sessions are the refresh mechanism.

Start with `00-start-here/README.md`.
```

- [ ] **Step 5: Verify skeleton**

Run:

```bash
find . -maxdepth 2 -type d | sort
```

Expected: Output includes every top-level section and first-level child directories.

- [ ] **Step 6: Commit**

Run:

```bash
git add .gitignore README.md
git commit -m "chore: initialize ruptiv execution vault"
```

Expected: Commit succeeds.

## Task 2: Add Frontmatter Standard And Validator

**Files:**
- Create: `/Users/gclyle/ruptiv-execution-vault/60-standards/frontmatter.md`
- Create: `/Users/gclyle/ruptiv-execution-vault/tools/validate_frontmatter.py`
- Create: `/Users/gclyle/ruptiv-execution-vault/tests/test_validate_frontmatter.py`

- [ ] **Step 1: Write the frontmatter standard**

Create `/Users/gclyle/ruptiv-execution-vault/60-standards/frontmatter.md`:

```markdown
---
type: standard
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [frontmatter, metadata, standards]
---

# Frontmatter Standard

Every curated Markdown file must begin with YAML frontmatter.

Required fields:

| Field | Allowed values |
|---|---|
| `type` | `brief`, `decision`, `standard`, `playbook`, `contract`, `architecture-note`, `retail-capability`, `agent-card`, `dispatch`, `closeout` |
| `project` | `ruptiv`, `atlasview`, `canary-go`, `retail-capabilities`, `shared-platform` |
| `status` | `draft`, `active`, `superseded`, `retired` |
| `owner` | accountable owner or team |
| `last-reviewed` | ISO date, `YYYY-MM-DD` |
| `review-cadence` | `weekly`, `monthly`, `quarterly`, `event-driven`, `none` |
| `source-status` | `curated`, `source-mined`, `derived`, `imported` |
| `memory-index` | `true` or `false` |
| `tags` | YAML list of kebab-case tags |

Files in `90-intake/` should use `memory-index: false`.
```

- [ ] **Step 2: Write a failing validator test**

Create `/Users/gclyle/ruptiv-execution-vault/tests/test_validate_frontmatter.py`:

```python
import tempfile
import unittest
from pathlib import Path

from tools.validate_frontmatter import validate_file


class FrontmatterValidationTests(unittest.TestCase):
    def test_valid_file_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "valid.md"
            path.write_text(
                """---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [example]
---

# Valid
""",
                encoding="utf-8",
            )
            self.assertEqual(validate_file(path), [])

    def test_missing_required_field_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "missing.md"
            path.write_text(
                """---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
tags: [example]
---

# Missing memory-index
""",
                encoding="utf-8",
            )
            errors = validate_file(path)
            self.assertIn("missing required field: memory-index", errors)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 3: Run test and verify it fails**

Run:

```bash
python3 -m unittest tests/test_validate_frontmatter.py
```

Expected: FAIL with an import error because `tools.validate_frontmatter` does not exist yet.

- [ ] **Step 4: Write validator implementation**

Create `/Users/gclyle/ruptiv-execution-vault/tools/validate_frontmatter.py`:

```python
#!/usr/bin/env python3
import re
import sys
from pathlib import Path

REQUIRED_FIELDS = [
    "type",
    "project",
    "status",
    "owner",
    "last-reviewed",
    "review-cadence",
    "source-status",
    "memory-index",
    "tags",
]

ALLOWED = {
    "type": {
        "brief",
        "decision",
        "standard",
        "playbook",
        "contract",
        "architecture-note",
        "retail-capability",
        "agent-card",
        "dispatch",
        "closeout",
    },
    "project": {"ruptiv", "atlasview", "canary-go", "retail-capabilities", "shared-platform"},
    "status": {"draft", "active", "superseded", "retired"},
    "review-cadence": {"weekly", "monthly", "quarterly", "event-driven", "none"},
    "source-status": {"curated", "source-mined", "derived", "imported"},
    "memory-index": {"true", "false"},
}


def parse_frontmatter(text):
    if not text.startswith("---\n"):
        return None
    end = text.find("\n---\n", 4)
    if end == -1:
        return None
    return text[4:end]


def parse_scalar_fields(frontmatter):
    fields = {}
    for line in frontmatter.splitlines():
        if not line or line.startswith(" ") or line.startswith("-"):
            continue
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        fields[key.strip()] = value.strip().strip('"').strip("'")
    return fields


def validate_file(path):
    text = path.read_text(encoding="utf-8")
    frontmatter = parse_frontmatter(text)
    if frontmatter is None:
        return ["missing YAML frontmatter"]

    fields = parse_scalar_fields(frontmatter)
    errors = []

    for field in REQUIRED_FIELDS:
        if field not in fields:
            errors.append(f"missing required field: {field}")

    for field, allowed_values in ALLOWED.items():
        value = fields.get(field)
        if value and value not in allowed_values:
            errors.append(f"invalid {field}: {value}")

    reviewed = fields.get("last-reviewed")
    if reviewed and not re.fullmatch(r"\d{4}-\d{2}-\d{2}", reviewed):
        errors.append(f"invalid last-reviewed date: {reviewed}")

    tags = fields.get("tags")
    if tags and not (tags.startswith("[") and tags.endswith("]")):
        errors.append("tags must be an inline YAML list, for example: [retail, inventory]")

    if "90-intake/" in str(path) and fields.get("memory-index") == "true":
        errors.append("90-intake files must use memory-index: false")

    return errors


def iter_markdown_files(root):
    for path in root.rglob("*.md"):
        if ".git" not in path.parts:
            yield path


def main():
    root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(".")
    failed = False
    for path in iter_markdown_files(root):
        errors = validate_file(path)
        if errors:
            failed = True
            print(f"{path}:")
            for error in errors:
                print(f"  - {error}")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
```

- [ ] **Step 5: Add package marker for tests**

Run:

```bash
touch tools/__init__.py
```

Expected: `tools` can be imported by `unittest`.

- [ ] **Step 6: Run unit tests**

Run:

```bash
python3 -m unittest tests/test_validate_frontmatter.py
```

Expected: `OK`.

- [ ] **Step 7: Commit**

Run:

```bash
git add 60-standards/frontmatter.md tools/validate_frontmatter.py tools/__init__.py tests/test_validate_frontmatter.py
git commit -m "feat: add frontmatter standard and validator"
```

Expected: Commit succeeds.

## Task 3: Add Artifact Templates

**Files:**
- Create all files under `/Users/gclyle/ruptiv-execution-vault/templates/`

- [ ] **Step 1: Create template files**

Create `/Users/gclyle/ruptiv-execution-vault/templates/retail-capability.md`:

```markdown
---
type: retail-capability
project: retail-capabilities
domain: inventory
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: quarterly
source-status: curated
memory-index: true
tags: [retail]
---

# Capability Name

## Business Capability

State what the retailer needs to do and why it matters.

## Process Flow

Describe trigger, actors, states, happy path, exception path, and closeout state.

## Technical Shape

Describe data entities, service ownership, APIs, events, audit/proof needs, and integration boundaries.

## Canary Go Implications

List affected services, schemas, screens, migrations, tests, runbooks, or backlog items.

## AtlasView Implications

List relevant roles, policies, agents, governance controls, operating modes, or review workflows.

## Standards And Vendor References

List relevant standards, reference systems, and comparison points.

## Source Notes

List source files used and material intentionally excluded.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/brief.md`:

```markdown
---
type: brief
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [brief]
---

# Brief Title

## Current State

Summarize the current operating picture.

## Canonical Sources

List the files that define the current truth.

## Active Priorities

List the work that should guide the next session.

## Open Gates

List decisions that require human or external review.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/decision.md`:

```markdown
---
type: decision
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: event-driven
source-status: curated
memory-index: true
tags: [decision]
---

# Decision Title

## Decision

State the settled choice.

## Context

Explain the situation that made the decision necessary.

## Options Considered

List the credible options and why they were accepted or rejected.

## Rationale

Explain why this decision is the right company move.

## Consequences

List operational, technical, product, or governance effects.

## Related

Link related standards, contracts, briefs, or prior decisions.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/standard.md`:

```markdown
---
type: standard
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [standard]
---

# Standard Title

## Rule

State the required way of working.

## Rationale

Explain why the rule exists.

## Examples

Show correct and incorrect examples.

## Enforcement

Name the review, test, or gate that enforces the rule.

## Related

Link related files.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/playbook.md`:

```markdown
---
type: playbook
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [playbook]
---

# Playbook Title

## When To Use

Describe the trigger for this playbook.

## Inputs

List required files, tools, credentials, or decisions.

## Steps

List the exact operating steps.

## Verification

Describe the evidence that the playbook succeeded.

## Failure Modes

Describe known failures and recovery steps.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/contract.md`:

```markdown
---
type: contract
project: shared-platform
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [contract]
---

# Contract Title

## Boundary

Define the system, team, repo, or agent boundary.

## Provider

Name the owner that provides the capability.

## Consumer

Name the consumer and expected use.

## Inputs

List accepted inputs and constraints.

## Outputs

List outputs, side effects, and guarantees.

## Invariants

List what must remain true across versions.

## Human Gates

List review points that require human approval.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/architecture-note.md`:

```markdown
---
type: architecture-note
project: shared-platform
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [architecture]
---

# Architecture Note Title

## Context

Explain the technical situation.

## Design

Describe the proposed or current design.

## Data Flow

Describe the main movement of data, control, or state.

## Risks

List risks, mitigations, and review gates.

## Related

Link related files.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/agent-card.md`:

```markdown
---
type: agent-card
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [agent-card]
---

# Agent Name

## Role

State the agent's responsibility.

## Inputs

List required context and files.

## Outputs

List expected work products.

## Guardrails

List constraints, forbidden actions, and human gates.

## Handoff

Describe how the agent reports completion or blockers.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/dispatch.md`:

```markdown
---
type: dispatch
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: event-driven
source-status: curated
memory-index: true
tags: [dispatch]
---

# Dispatch Title

## Objective

State the outcome for this work cycle.

## Scope

List included and excluded work.

## Context To Read

List canonical vault files and target repo files.

## Tasks

List agent tasks and owners.

## Human Gates

List decisions requiring human approval.

## Closeout Requirements

List files to update and evidence to capture before closing.
```

Create `/Users/gclyle/ruptiv-execution-vault/templates/closeout.md`:

```markdown
---
type: closeout
project: ruptiv
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: event-driven
source-status: curated
memory-index: true
tags: [closeout]
---

# Closeout Title

## Summary

Summarize what changed.

## Decisions

List decisions made during the cycle.

## Changed Files

List files changed in the vault and target repos.

## Risks

List unresolved risks and follow-up owners.

## Next Entry Point

Name the file or dispatch the next session should start from.
```

- [ ] **Step 2: Run validator on templates**

Run:

```bash
python3 tools/validate_frontmatter.py templates
```

Expected: no output and exit code 0.

- [ ] **Step 3: Commit**

Run:

```bash
git add templates
git commit -m "feat: add artifact templates"
```

Expected: Commit succeeds.

## Task 4: Add Required Initial Vault Files

**Files:**
- Create all required initial files from the design spec.

- [ ] **Step 1: Write `00-start-here/README.md`**

Create `/Users/gclyle/ruptiv-execution-vault/00-start-here/README.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [start-here, onboarding]
---

# Start Here

This section is the entry point for humans and agents.

Read in order:

1. `current-operating-picture.md`
2. `how-to-use-this-vault.md`
3. `active-context-index.md`
4. The relevant project brief for AtlasView or Canary Go
5. The relevant standards, contracts, and retail capability cards
```

- [ ] **Step 2: Write the operating picture**

Create `/Users/gclyle/ruptiv-execution-vault/00-start-here/current-operating-picture.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: true
tags: [operating-picture, atlasview, canary-go]
---

# Current Operating Picture

Ruptiv is establishing a curated execution vault for company knowledge and multi-agent development.

Active product focus:

- AtlasView: organizational design and orchestration plane.
- Canary Go: retail execution platform and service tree.
- Shared platform: memory-bus, MCP, identity, audit/proof, policy, and protocol contracts.
- Retail capabilities: process and technical decompositions that inform Canary Go and AtlasView.

Current rule: canonical files are truth, memory-bus is a derived index, and sessions keep the corpus current.
```

- [ ] **Step 3: Write the context index and usage guide**

Create `/Users/gclyle/ruptiv-execution-vault/00-start-here/how-to-use-this-vault.md`:

```markdown
---
type: playbook
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [vault-usage, playbook]
---

# How To Use This Vault

Use this repo as the canonical knowledge source for Ruptiv execution.

For a development session:

1. Read `00-start-here/current-operating-picture.md`.
2. Read `00-start-here/active-context-index.md`.
3. Read the relevant project brief.
4. Read relevant standards and contracts.
5. Use memory-bus recall only to find candidate context.
6. Verify recalled context against canonical files.
7. Close the session by updating canonical files and refreshing the memory index.
```

Create `/Users/gclyle/ruptiv-execution-vault/00-start-here/active-context-index.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: true
tags: [active-context, routing]
---

# Active Context Index

Project entry points:

- AtlasView: `20-atlasview/product-brief.md`
- Canary Go: `30-canary-go/project-brief.md`
- Retail capabilities: `35-retail-capabilities/capability-map.md`
- Shared platform: `40-shared-platform/memory/memory-bus-contract.md`
- Development system: `50-dev-system/session-protocol.md`
- Standards: `60-standards/frontmatter.md`
```

- [ ] **Step 4: Write the glossary and company principles**

Create `/Users/gclyle/ruptiv-execution-vault/00-start-here/glossary.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [glossary, vocabulary]
---

# Glossary

Core terms:

- AtlasView: Ruptiv's organizational design and orchestration plane.
- Canary Go: the Go service tree for the Canary retail platform.
- Retail capability: a reusable business and technical model for retail execution.
- Execution vault: the curated company knowledge repo.
- Memory-bus: the derived MCP/pgvector retrieval layer.
- Human gate: a point where product, architecture, security, data, provenance, or handoff decisions require human approval.
```

Create `/Users/gclyle/ruptiv-execution-vault/10-ruptiv-company/principles.md`:

```markdown
---
type: standard
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [principles, ways-of-working]
---

# Ruptiv Principles

## Canonical Files Are Truth

Durable company knowledge lives in reviewed, versioned files.

## Memory Is Derived

Memory-bus improves recall but does not replace canonical files.

## Sessions Refresh The Corpus

Every meaningful development cycle updates the files needed by the next cycle.

## Retail Capability Is Company IP

Retail process and technical decompositions are preserved as active capability models.

## Human Gates Protect Direction

Product, architecture, security, data, provenance, and handoff decisions require human approval.
```

- [ ] **Step 5: Write project briefs**

Create `/Users/gclyle/ruptiv-execution-vault/20-atlasview/product-brief.md`:

```markdown
---
type: brief
project: atlasview
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: true
tags: [atlasview, product-brief]
---

# AtlasView Product Brief

AtlasView is Ruptiv's organizational design and orchestration plane.

This vault section preserves current AtlasView product truth: requirements, architecture, decisions, substrate concepts, agents, connectors, policies, and development dispatches.

First source-mining targets:

- `/Users/gclyle/Ruptiv/product/atlasview/requirements.md`
- `/Users/gclyle/Ruptiv/product/atlasview/decisions.md`
- `/Users/gclyle/Ruptiv/spec/`
- `/Users/gclyle/Ruptiv/atlasview/sparring-partners/`
- `/Users/gclyle/Ruptiv/dispatches/`
```

Create `/Users/gclyle/ruptiv-execution-vault/30-canary-go/project-brief.md`:

```markdown
---
type: brief
project: canary-go
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: true
tags: [canary-go, project-brief]
---

# Canary Go Project Brief

Canary Go is the Go service tree for the Canary retail platform.

This vault section preserves current Canary Go architecture and implementation guidance: M1 foundation context, service boundaries, SDDs, decisions, migration rules, identity contracts, MCP contracts, audit/proof, protocol requirements, and runbooks.

First source-mining targets:

- `/Users/gclyle/CanaryGo/docs/sdds/go-handoff/`
- `/Users/gclyle/CanaryGo/docs/decisions/`
- `/Users/gclyle/CanaryGo/docs/superpowers/specs/`
- `/Users/gclyle/CanaryGo/deploy/schema/`
- `/Users/gclyle/CanaryGo/deploy/migrations/`
```

- [ ] **Step 6: Write retail and platform seed files**

Create `/Users/gclyle/ruptiv-execution-vault/35-retail-capabilities/README.md`:

```markdown
---
type: brief
project: retail-capabilities
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [retail-capabilities]
---

# Retail Capabilities

This section preserves retail process and technical decompositions as active Ruptiv company knowledge.

It is not an archive. Source material graduates here only after curation.
```

Create `/Users/gclyle/ruptiv-execution-vault/35-retail-capabilities/capability-map.md`:

```markdown
---
type: brief
project: retail-capabilities
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [retail, capability-map]
---

# Retail Capability Map

Initial capability families:

- Item master and catalog
- Merchandising and assortment
- Inventory and availability
- Receiving and inbound
- Returns and reverse logistics
- Pricing and promotions
- Transactions, tender, and sales audit
- Labor, employee, and operator provenance
- Reporting and KPIs
- Supplier and vendor lifecycle
- POS and integration spine
```

Create `/Users/gclyle/ruptiv-execution-vault/40-shared-platform/memory/memory-bus-contract.md`:

```markdown
---
type: contract
project: shared-platform
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [memory-bus, mcp, pgvector]
---

# Memory-Bus Contract

Canonical files in this repo are the source of truth. Memory-bus indexes selected files for retrieval.

Rules:

- Index only files with `memory-index: true`.
- Do not index `90-intake/` by default.
- Store canonical source path metadata with every memory row.
- Treat retrieval as a pointer to source files, not as authority.
- Reseed after curated files change.
```

- [ ] **Step 7: Write dev-system seed files**

Create `/Users/gclyle/ruptiv-execution-vault/50-dev-system/session-protocol.md`:

```markdown
---
type: playbook
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [session-protocol, multi-agent]
---

# Session Protocol

Every development session follows this loop:

1. Start from `00-start-here/active-context-index.md`.
2. Read the relevant project brief.
3. Read applicable standards, contracts, and retail capability cards.
4. Use memory-bus recall only for discovery.
5. Verify important context against canonical files.
6. Implement or curate.
7. Close out with decisions, changed files, risks, and next entry point.
8. Refresh the memory index.
```

Create `/Users/gclyle/ruptiv-execution-vault/50-dev-system/human-gates.md`:

```markdown
---
type: standard
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [human-gates, hil]
---

# Human Gates

Human review is required for:

- Product direction changes
- Architecture boundary changes
- Security posture changes
- Data policy changes
- External API contracts
- Irreversible migrations
- Provenance, old-IP, or personal-material questions
- Public or team handoff documents
```

Create `/Users/gclyle/ruptiv-execution-vault/50-dev-system/multi-agent-flow.md`:

```markdown
---
type: playbook
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [multi-agent, hil]
---

# Multi-Agent Flow

## When To Use

Use this flow for AtlasView, Canary Go, shared platform, and retail capability work that benefits from separated exploration, implementation, and review.

## Inputs

- Active project brief
- Relevant standards and contracts
- Relevant retail capability cards
- Dispatch objective and human gates

## Steps

1. Lead agent reads the active context index.
2. Explorer agents answer bounded context questions.
3. Worker agents make scoped changes in disjoint file areas.
4. Reviewer agents check tests, standards, drift, and risks.
5. Human reviews gated decisions.
6. Lead agent integrates results and writes closeout.

## Verification

The cycle is complete when changed files are committed, tests or document checks pass, and the next entry point is written.

## Failure Modes

- If agents disagree on canonical context, stop and read source files.
- If a human gate appears, pause implementation until reviewed.
- If source material contains personal or old-IP risk, do not curate it.
```

Create `/Users/gclyle/ruptiv-execution-vault/50-dev-system/dispatch-template.md` by copying the exact contents of `templates/dispatch.md` and changing `status: draft` to `status: active`.

Create `/Users/gclyle/ruptiv-execution-vault/50-dev-system/closeout-template.md` by copying the exact contents of `templates/closeout.md` and changing `status: draft` to `status: active`.

- [ ] **Step 8: Write standards and intake seed files**

Create `/Users/gclyle/ruptiv-execution-vault/60-standards/documentation.md`:

```markdown
---
type: standard
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [documentation]
---

# Documentation Standard

Documentation must be current, actionable, and tied to a clear audience.

Every curated document must state whether it is a brief, decision, standard, playbook, contract, architecture note, retail capability, agent card, dispatch, or closeout.
```

Create `/Users/gclyle/ruptiv-execution-vault/60-standards/architecture-records.md`:

```markdown
---
type: standard
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [architecture, decisions]
---

# Architecture Records

Architecture records must separate decision, rationale, consequences, and related contracts.

Do not bury durable decisions in session chat or memory-bus rows.
```

Create `/Users/gclyle/ruptiv-execution-vault/90-intake/README.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: false
tags: [intake]
---

# Intake

This section is a temporary curation queue.

Nothing here is authoritative. Files in this section are not indexed by memory-bus by default.
```

Create `/Users/gclyle/ruptiv-execution-vault/90-intake/source-inventory.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: false
tags: [source-inventory, curation]
---

# Source Inventory

Initial source-mining areas:

- `/Users/gclyle/GrowDirect/Brain/wiki/cards/`
- `/Users/gclyle/GrowDirect/Brain/wiki/`
- `/Users/gclyle/Ruptiv/`
- `/Users/gclyle/CanaryGo/docs/`
- `/Users/gclyle/GrowDirect/services/memory-bus/`
```

- [ ] **Step 9: Run validator**

Run:

```bash
python3 tools/validate_frontmatter.py .
```

Expected: no output and exit code 0.

- [ ] **Step 10: Commit**

Run:

```bash
git add 00-start-here 10-ruptiv-company 20-atlasview 30-canary-go 35-retail-capabilities 40-shared-platform 50-dev-system 60-standards 90-intake
git commit -m "feat: add initial execution vault corpus"
```

Expected: Commit succeeds.

## Task 5: Inventory First Curation Wave

**Files:**
- Modify: `/Users/gclyle/ruptiv-execution-vault/90-intake/source-inventory.md`
- Create: `/Users/gclyle/ruptiv-execution-vault/90-intake/curation-queue.md`

- [ ] **Step 1: Generate candidate lists**

Run:

```bash
rg --files /Users/gclyle/GrowDirect/Brain/wiki/cards | rg 'retail|canary|platform|runbook|memory|inventory|receiving|item|pricing|supplier|vendor|transaction|report' > /tmp/retail-cards.txt
rg --files /Users/gclyle/GrowDirect/Brain/wiki | rg 'retail|canary|ncr|counterpoint|rapidpos|square|inventory|receiving|item|pricing|transaction|report' > /tmp/retail-wiki.txt
rg --files /Users/gclyle/Ruptiv | rg 'product/atlasview|spec/SDD|dispatches|zettel|sparring-partners|skills|connectors|policies' > /tmp/atlasview-sources.txt
rg --files /Users/gclyle/CanaryGo/docs | rg 'sdds|decisions|research|superpowers/specs|architecture|conventions' > /tmp/canary-sources.txt
```

Expected: Each `/tmp/*.txt` file contains candidate source paths.

- [ ] **Step 2: Create curation queue**

Create `/Users/gclyle/ruptiv-execution-vault/90-intake/curation-queue.md`:

```markdown
---
type: brief
project: ruptiv
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: weekly
source-status: curated
memory-index: false
tags: [curation-queue]
---

# Curation Queue

Selection rule: choose current, transferable, non-personal, legally safe material that directly supports Ruptiv, AtlasView, Canary Go, retail capabilities, or shared platform execution.

## Wave 1 Targets

### Retail Capabilities

- `Brain/wiki/cards/canary-item.md`
- `Brain/wiki/cards/canary-inventory.md`
- `Brain/wiki/cards/canary-receiving.md`
- `Brain/wiki/cards/retail-sales-audit.md`
- `Brain/wiki/cards/retail-purchase-order-model.md`
- `Brain/wiki/cards/retail-vendor-lifecycle.md`
- `Brain/wiki/cards/retail-operations-kpis.md`
- `Brain/wiki/retail-module-decomposition.md`

### AtlasView

- `Ruptiv/product/atlasview/requirements.md`
- `Ruptiv/product/atlasview/decisions.md`
- `Ruptiv/spec/SDD-013-downstream-substrate-contract.md`
- `Ruptiv/atlasview/sparring-partners/README.md`

### Canary Go

- `CanaryGo/docs/sdds/go-handoff/data-model.md`
- `CanaryGo/docs/sdds/go-handoff/go-module-layout.md`
- `CanaryGo/docs/sdds/go-handoff/microservice-architecture.md`
- `CanaryGo/docs/decisions/gro-848-atlasview-identity-integration.md`

### Shared Platform

- `GrowDirect/docs/sdds/platform/memory-bus.md`
- `GrowDirect/Brain/wiki/cards/runbook-memory-bus-seed.md`
```

- [ ] **Step 3: Append inventory counts**

Run:

```bash
{
  echo ""
  echo "## Generated Candidate Counts"
  echo ""
  echo "- Retail cards: $(wc -l < /tmp/retail-cards.txt)"
  echo "- Retail wiki pages: $(wc -l < /tmp/retail-wiki.txt)"
  echo "- AtlasView sources: $(wc -l < /tmp/atlasview-sources.txt)"
  echo "- Canary Go sources: $(wc -l < /tmp/canary-sources.txt)"
} >> 90-intake/source-inventory.md
```

Expected: `source-inventory.md` includes candidate counts.

- [ ] **Step 4: Run validator**

Run:

```bash
python3 tools/validate_frontmatter.py 90-intake
```

Expected: no output and exit code 0.

- [ ] **Step 5: Commit**

Run:

```bash
git add 90-intake/source-inventory.md 90-intake/curation-queue.md
git commit -m "docs: inventory first curation wave"
```

Expected: Commit succeeds.

## Task 6: Curate First Three Representative Assets

**Files:**
- Create: `/Users/gclyle/ruptiv-execution-vault/35-retail-capabilities/cards/canary-item-master.md`
- Create: `/Users/gclyle/ruptiv-execution-vault/20-atlasview/contracts/downstream-substrate-contract.md`
- Create: `/Users/gclyle/ruptiv-execution-vault/40-shared-platform/memory/memory-bus-runbook.md`

- [ ] **Step 1: Read source files**

Run:

```bash
sed -n '1,220p' /Users/gclyle/GrowDirect/Brain/wiki/cards/canary-item.md
sed -n '1,220p' /Users/gclyle/Ruptiv/spec/SDD-013-downstream-substrate-contract.md
sed -n '1,220p' /Users/gclyle/GrowDirect/Brain/wiki/cards/runbook-memory-bus-seed.md
```

Expected: Source files are readable.

- [ ] **Step 2: Curate retail capability**

Create `/Users/gclyle/ruptiv-execution-vault/35-retail-capabilities/cards/canary-item-master.md` using the retail capability template. Preserve only current retail capability content: item master business purpose, item setup process, technical data/API implications, Canary Go service implications, AtlasView governance implications, and safe standards/vendor references.

Required frontmatter:

```yaml
---
type: retail-capability
project: retail-capabilities
domain: item-master
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: quarterly
source-status: curated
memory-index: true
tags: [retail, item-master, canary-go]
---
```

- [ ] **Step 3: Curate AtlasView contract**

Create `/Users/gclyle/ruptiv-execution-vault/20-atlasview/contracts/downstream-substrate-contract.md` as a contract artifact. Explain the boundary between AtlasView and downstream apps, with Canary Go as the first practical consumer.

Required frontmatter:

```yaml
---
type: contract
project: atlasview
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [atlasview, canary-go, downstream-substrate]
---
```

- [ ] **Step 4: Curate memory-bus runbook**

Create `/Users/gclyle/ruptiv-execution-vault/40-shared-platform/memory/memory-bus-runbook.md` as a playbook artifact. Keep the operational rules for seeding, verification, stale-result handling, and canonical-file source pointers.

Required frontmatter:

```yaml
---
type: playbook
project: shared-platform
status: draft
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [memory-bus, seeding, pgvector]
---
```

- [ ] **Step 5: Run validator**

Run:

```bash
python3 tools/validate_frontmatter.py .
```

Expected: no output and exit code 0.

- [ ] **Step 6: Commit**

Run:

```bash
git add 35-retail-capabilities/cards/canary-item-master.md 20-atlasview/contracts/downstream-substrate-contract.md 40-shared-platform/memory/memory-bus-runbook.md
git commit -m "docs: curate representative execution assets"
```

Expected: Commit succeeds.

## Task 7: Document Memory-Bus Seeding Follow-Up

**Files:**
- Create: `/Users/gclyle/ruptiv-execution-vault/40-shared-platform/memory/vault-indexing-plan.md`

- [ ] **Step 1: Write indexing plan**

Create `/Users/gclyle/ruptiv-execution-vault/40-shared-platform/memory/vault-indexing-plan.md`:

```markdown
---
type: playbook
project: shared-platform
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: [memory-bus, indexing, execution-vault]
---

# Vault Indexing Plan

The execution vault is indexed into memory-bus as a derived retrieval layer.

## Indexing Rules

- Read Markdown files from `ruptiv-execution-vault`.
- Index only files with `memory-index: true`.
- Skip `90-intake/`.
- Store `source_repo`, `source_file`, `type`, `project`, `status`, `last_reviewed`, and `source_status`.
- Treat retrieval as a pointer back to canonical files.

## Implementation Target

Extend the existing GrowDirect memory-bus seeding flow or create a sibling seeder that accepts a vault root and applies the rules above.

## Verification

After indexing, memory recall for `Canary item master retail capability` should return `35-retail-capabilities/cards/canary-item-master.md`.
```

- [ ] **Step 2: Run validator**

Run:

```bash
python3 tools/validate_frontmatter.py 40-shared-platform/memory
```

Expected: no output and exit code 0.

- [ ] **Step 3: Commit**

Run:

```bash
git add 40-shared-platform/memory/vault-indexing-plan.md
git commit -m "docs: define execution vault indexing plan"
```

Expected: Commit succeeds.

## Task 8: Final Verification

**Files:**
- No new files.

- [ ] **Step 1: Run full validator**

Run:

```bash
python3 tools/validate_frontmatter.py .
```

Expected: no output and exit code 0.

- [ ] **Step 2: Run unit tests**

Run:

```bash
python3 -m unittest tests/test_validate_frontmatter.py
```

Expected: `OK`.

- [ ] **Step 3: Confirm authoritative corpus size**

Run:

```bash
find . -path './90-intake' -prune -o -name '*.md' -print | wc -l
```

Expected: at least 25 Markdown files outside intake.

- [ ] **Step 4: Confirm intake is not indexed**

Run:

```bash
python3 tools/validate_frontmatter.py 90-intake
rg -n "memory-index: true" 90-intake
```

Expected: validator passes; `rg` returns no matches in `90-intake`.

- [ ] **Step 5: Final commit if needed**

Run:

```bash
git status --short
```

Expected: no uncommitted changes. If changes exist, inspect them and commit only intentional files.
