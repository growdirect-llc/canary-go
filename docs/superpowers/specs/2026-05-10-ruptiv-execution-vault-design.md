---
title: Ruptiv Execution Vault Design
date: 2026-05-10
status: draft
owner: ruptiv
scope: company-knowledge-repo
related:
  - /Users/gclyle/GrowDirect/docs/sdds/platform/memory-bus.md
  - /Users/gclyle/GrowDirect/Brain/wiki/cards/runbook-memory-bus-seed.md
  - /Users/gclyle/Ruptiv/zettel/README.md
  - /Users/gclyle/Ruptiv/dispatches/2026-05-07-atlasview-session-handoff.md
  - docs/decisions/gro-848-atlasview-identity-integration.md
  - docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md
---

# Ruptiv Execution Vault Design

## Purpose

Create a new repository, `ruptiv-execution-vault`, as the curated company knowledge base for Ruptiv development execution. The repo is a handoff-grade asset for a development team and for multi-agent, human-in-the-loop delivery across Ruptiv, AtlasView, Canary Go, retail capability modeling, and shared platform architecture.

The repo is not an archive of prior notes. It is a curated execution corpus. Existing GrowDirect Brain/wiki pages, Ruptiv zettels, AtlasView dispatches, Canary Go specs, memory-bus records, and retail decompositions are source material only. They enter the new repo only after they pass a company-use test and are rewritten into the standard artifact shape.

## Design Principle

Canonical files are truth. Memory systems are derived indexes. Sessions are the refresh mechanism.

This principle separates three concerns:

- Human-readable company knowledge lives in versioned Markdown files.
- Machine retrieval lives in memory-bus / pgvector indexes derived from those files.
- Development sessions keep the corpus current through explicit start, work, closeout, and reseed rituals.

The repo should remain useful even if memory-bus is unavailable. Memory-bus improves recall; it does not become the source of truth.

## Leading-Practice Alignment

The design follows four practical standards:

1. Obsidian-compatible Markdown with consistent YAML properties so the repo can be browsed, filtered, and queried as a vault.
2. Zettelkasten discipline where atomic thinking is preserved only when it is current, linkable, and written in Ruptiv company language.
3. MCP resource discipline where server-side context is exposed by stable resource identity and retrieved as needed, rather than hidden as untraceable chat memory.
4. Software-delivery discipline where decisions, standards, contracts, and runbooks are versioned, reviewed, and refreshed alongside implementation work.

## Repository Shape

```text
ruptiv-execution-vault/
  README.md
  00-start-here/
  10-ruptiv-company/
  20-atlasview/
  30-canary-go/
  35-retail-capabilities/
  40-shared-platform/
  50-dev-system/
  60-standards/
  90-intake/
```

### `00-start-here/`

The entry point for humans and agents.

Required files:

```text
00-start-here/
  README.md
  current-operating-picture.md
  how-to-use-this-vault.md
  active-context-index.md
  glossary.md
```

`current-operating-picture.md` names the current product state, active development priorities, known gates, and the canonical files to read first. `active-context-index.md` is the routing layer for agents: it points to the current brief, current dispatch, active standards, and relevant contracts.

### `10-ruptiv-company/`

Company-level operating knowledge.

Recommended files:

```text
10-ruptiv-company/
  principles.md
  ways-of-working.md
  governance-model.md
  decision-discipline.md
  roles-and-responsibilities.md
  vocabulary.md
```

This section is for transferable Ruptiv company doctrine: how decisions are made, how work is reviewed, what human gates exist, and how company vocabulary should be used.

### `20-atlasview/`

AtlasView product and substrate knowledge.

Recommended structure:

```text
20-atlasview/
  README.md
  product-brief.md
  architecture/
  decisions/
  dispatches/
  substrate/
  agents/
  connectors/
  policies/
```

This section preserves current AtlasView product truth: requirements, architecture, SDD mappings, decisions, agent/card substrate, connector contracts, policy bundles, and delivery context. Ruptiv zettels can inform this section, but only curated company-facing concepts graduate.

### `30-canary-go/`

Canary Go development execution knowledge.

Recommended structure:

```text
30-canary-go/
  README.md
  project-brief.md
  service-map.md
  architecture/
  decisions/
  contracts/
  runbooks/
  delivery/
```

This section preserves current Canary Go architecture and implementation guidance: M1 foundation context, service boundaries, SDD index, migration rules, sqlc/pgx conventions, identity contracts, MCP contracts, audit/protocol requirements, and delivery runbooks.

### `35-retail-capabilities/`

Retail domain capability library. This is a first-class section, not an archive.

Recommended structure:

```text
35-retail-capabilities/
  README.md
  capability-map.md
  process/
  technical/
  standards-crosswalks/
  vendor-crosswalks/
  cards/
```

This section preserves retail process and technical decompositions as Ruptiv company assets. It should include merchandising, item master, inventory, receiving, returns, pricing, promotion, labor, transactions, reporting, suppliers, customers, POS/EJ spine, sales audit, integration models, retail data models, and vendor/system crosswalks.

`process/` contains workflow-oriented decompositions. `technical/` contains data, API, event, service, and implementation implications. `standards-crosswalks/` connects retail capabilities to standards such as ARTS/OMG, NRF, GS1, EMVCo, PCI, OAGi, and MACH/composable commerce where relevant. `vendor-crosswalks/` preserves reference learning from NCR, Counterpoint, RapidPOS, Square, Clover, Shopify, Oracle Retail, Retek, Sysrepublic, and similar systems when the learning is current and legally safe to reuse.

Capability cards are the preferred atomic unit. Each card explains the business capability, process flow, technical shape, Canary Go implications, AtlasView implications, and standards/vendor references.

### `40-shared-platform/`

Cross-product platform architecture.

Recommended structure:

```text
40-shared-platform/
  README.md
  memory/
  mcp/
  identity/
  audit-and-proof/
  policy/
  protocol/
  integrations/
```

This section owns the reusable platform substrate: memory-bus, MCP conventions, identity delegation, audit/proof, policy engine, protocol boundaries, integration patterns, shared data classification, and cross-product contracts.

### `50-dev-system/`

The multi-agent, human-in-the-loop development method.

Recommended structure:

```text
50-dev-system/
  README.md
  session-protocol.md
  multi-agent-flow.md
  human-gates.md
  dispatch-template.md
  closeout-template.md
  agent-cards/
  review-playbooks/
```

This section makes the development cycle repeatable. It defines how agents start, what they read, how work is split, where humans must approve, how review happens, and how sessions close.

### `60-standards/`

Reusable execution standards.

Recommended structure:

```text
60-standards/
  README.md
  go.md
  sql-and-migrations.md
  api.md
  security.md
  documentation.md
  ui-accessibility.md
  testing.md
  release.md
  architecture-records.md
```

Standards are prescriptive. They should state the rule, rationale, examples, and enforcement method. Canary Go and AtlasView project-specific standards may link here rather than duplicating rules.

### `90-intake/`

Temporary curation queue.

Recommended structure:

```text
90-intake/
  README.md
  source-inventory.md
  curation-queue.md
```

Nothing in `90-intake/` is authoritative. It is a landing zone for source-mined material before curation. Each item must graduate into a curated section or be dropped. Intake files should not be indexed into memory-bus by default.

## Artifact Types

Every curated Markdown file should use one of these types:

| Type | Purpose |
|---|---|
| `brief` | Current operating context for a project, product, or workstream |
| `decision` | Settled choice with rationale and consequences |
| `standard` | Required way of working |
| `playbook` | Repeatable workflow or operating procedure |
| `contract` | Boundary between systems, repos, teams, or agents |
| `architecture-note` | Technical design explanation that is not itself a binding decision |
| `retail-capability` | Retail process / technical capability card |
| `agent-card` | Agent role, scope, inputs, outputs, and guardrails |
| `dispatch` | Scoped execution package for a development cycle |
| `closeout` | End-of-session summary, decisions, and next entry point |

## Required Frontmatter

All curated files use YAML frontmatter:

```yaml
---
type: standard
project: atlasview
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: monthly
source-status: curated
memory-index: true
tags: []
---
```

Field rules:

- `type`: one of the artifact types above.
- `project`: `ruptiv`, `atlasview`, `canary-go`, `retail-capabilities`, or `shared-platform`.
- `status`: `draft`, `active`, `superseded`, or `retired`.
- `owner`: accountable owner. For now this can be `ruptiv`; later it should become a team or named role.
- `last-reviewed`: date the file was last checked for currency.
- `review-cadence`: `weekly`, `monthly`, `quarterly`, `event-driven`, or `none`.
- `source-status`: `curated`, `source-mined`, `derived`, or `imported`.
- `memory-index`: `true` only for files that should seed memory-bus.
- `tags`: kebab-case tags.

Optional fields:

- `supersedes`: prior file path or decision id.
- `superseded-by`: later file path or decision id.
- `source-files`: original local paths used during curation.
- `related`: related canonical files.
- `human-gate`: required human review gate before use.

## Retail Capability Card Template

```markdown
---
type: retail-capability
project: retail-capabilities
domain: inventory
status: active
owner: ruptiv
last-reviewed: 2026-05-10
review-cadence: quarterly
source-status: curated
memory-index: true
tags: [retail, inventory]
---

# Capability Name

## Business Capability

What the retailer needs to do and why it matters.

## Process Flow

Trigger, actors, states, happy path, exception path, and closeout state.

## Technical Shape

Data entities, service ownership, APIs, events, audit/proof needs, and integration boundaries.

## Canary Go Implications

Services, schemas, screens, migrations, tests, runbooks, or backlog implications.

## AtlasView Implications

Roles, policies, agents, governance controls, operating modes, or review workflows.

## Standards And Vendor References

Relevant standards, vendor systems, and comparison points.

## Source Notes

Source files used and what was intentionally excluded.
```

## Curation Rules

A source item graduates into `ruptiv-execution-vault` only when it is:

1. Current enough to guide future work.
2. Transferable to a development team.
3. Relevant to Ruptiv, AtlasView, Canary Go, retail capabilities, or shared platform execution.
4. Free of personal material.
5. Free of old IP, prior-employer confidential material, or frozen prototype dependency.
6. Rewritten in Ruptiv company language.
7. Assigned an artifact type, owner, status, and review cadence.
8. Linked to adjacent canonical files.

Source material that fails the test is not moved into the repo. If a useful idea is trapped inside non-transferable material, rewrite the idea from first principles and cite only safe source context.

## Source Mining Priorities

Initial source mining should prioritize:

1. `/Users/gclyle/GrowDirect/Brain/wiki/cards/` retail, platform, and runbook cards.
2. `/Users/gclyle/GrowDirect/Brain/wiki/` retail process and technical decompositions.
3. `/Users/gclyle/Ruptiv/` AtlasView requirements, decisions, dispatches, zettels, sparring partners, standards, skills, connectors, and policies.
4. `/Users/gclyle/CanaryGo/docs/` Canary Go SDDs, decisions, research, conventions, runbooks, and specs.
5. `/Users/gclyle/GrowDirect/services/memory-bus/` memory-bus service docs, scripts, migrations, and tests when they define current shared-platform behavior.

The first migration wave should produce a small, high-confidence corpus rather than a broad import. Target 30 to 50 active files: enough to run real development cycles, small enough to review.

## Memory-Bus Integration

Memory-bus should index `ruptiv-execution-vault` as a derived retrieval layer. It should not store the only copy of any canonical knowledge.

Minimum memory metadata for each indexed file:

```json
{
  "source_repo": "ruptiv-execution-vault",
  "source_file": "35-retail-capabilities/cards/retail-receiving.md",
  "type": "retail-capability",
  "project": "retail-capabilities",
  "status": "active",
  "last_reviewed": "2026-05-10",
  "source_status": "curated"
}
```

Indexing rules:

- Index only files with `memory-index: true`.
- Do not index `90-intake/` by default.
- Include canonical path metadata in every memory row.
- Reseed after curated files change.
- Retrieval results must be treated as pointers to canonical files, not as authority by themselves.

## Multi-Agent Human-In-The-Loop Flow

Every multi-agent development cycle follows the same loop:

1. **Start.** Human or lead agent selects a project objective and reads `00-start-here/active-context-index.md`.
2. **Dispatch.** A dispatch defines objective, target repos, scope, constraints, relevant vault files, agent roles, and human gates.
3. **Orient.** Agents read the project brief, shared contracts, standards, and relevant retail capability cards. Memory-bus recall can propose context, but agents verify against canonical files.
4. **Split.** Explorer agents gather context; worker agents implement; reviewer agents check tests, standards, risks, and drift.
5. **Human gates.** Product direction, architecture boundary changes, security posture, data policy, external API contracts, irreversible migrations, and old-IP/provenance questions require human review.
6. **Integrate.** Work lands in AtlasView, Canary Go, or shared-platform repos with tests and review evidence.
7. **Closeout.** The cycle writes a closeout file or updates the active project brief with decisions, changed files, risks, and next entry point.
8. **Refresh.** Memory-bus is reseeded from canonical files, and stale or superseded files are marked.

## Required Initial Files

The first commit of `ruptiv-execution-vault` should include:

```text
README.md
00-start-here/README.md
00-start-here/current-operating-picture.md
00-start-here/how-to-use-this-vault.md
00-start-here/active-context-index.md
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
90-intake/README.md
90-intake/source-inventory.md
```

## Open Decisions

These are explicit decisions for the implementation plan, not gaps in this design:

1. Whether the repo should live under the Ruptiv GitHub organization or the current private `growdirectprez` namespace.
2. Whether memory-bus indexing should be handled by extending the existing `seed_standalone.py` script or by creating a new seeder for `ruptiv-execution-vault`.
3. Whether Obsidian workspace files should be committed. Recommendation: no, unless a deliberately minimal `.obsidian` profile is created later for shared Bases/views.

## Acceptance Criteria

The design is successful when:

1. A new developer can read `00-start-here/` and understand what to read next for AtlasView or Canary Go.
2. A multi-agent session can start from a dispatch and retrieve all required context from canonical files.
3. Retail capability knowledge is preserved as active company IP, not lost in old wiki drift.
4. Memory-bus can retrieve relevant context with source-file pointers.
5. Stale or non-authoritative material is visible as such.
6. No personal material, old IP, or frozen prototype internals are promoted into the curated corpus.

## Implementation Sequence

1. Create `ruptiv-execution-vault` repository with the top-level structure and required initial files.
2. Add artifact templates and frontmatter validation guidance.
3. Curate the first 30 to 50 files from GrowDirect Brain/wiki, Ruptiv, CanaryGo, and memory-bus sources.
4. Add a memory-bus seeding rule for files with `memory-index: true`.
5. Run one AtlasView development cycle and one Canary Go development cycle using the vault as the context source.
6. Adjust folder names, templates, and human gates based on those two dry runs.

