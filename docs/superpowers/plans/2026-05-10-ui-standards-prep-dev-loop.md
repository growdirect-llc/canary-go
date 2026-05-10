# Canary Go UI Standards Prep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the standards decision and convention docs that must exist before more Canary frontend UI is built.

**Architecture:** Keep this as a docs-only prep loop. The implementation boundary is the project standards layer: vocabulary decisions, status taxonomy, component contract convention, connector metadata convention, PR review checklist, and a short update to the component-led UI vision.

**Tech Stack:** Markdown docs in the Canary Go repo; Go SSR and AtlasView compatibility are referenced as architecture constraints but no runtime code changes are made.

---

## Fresh Session Prompt

Use this prompt in the next session:

```text
We are in /Users/gclyle/CanaryGo. Execute docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md against docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md.

This is docs-only standards prep before more frontend building. Create/update the vocabulary, status taxonomy, component contract, connector metadata, PR checklist, and component-led vision docs. Do not build new components, do not refactor templates, and do not touch cmd/internal/deploy/migrations.
```

## Source Documents

- `docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md`
- `docs/architecture/component-led-ui-vision.md`
- `docs/conventions/ui-components.md`
- `docs/research/open-commerce-component-patterns-2026-05-10.md`
- `docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md`
- `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`

## File Map

- Create: `docs/decisions/ui-retail-vocabulary.md`
- Create: `docs/decisions/ui-status-taxonomy.md`
- Modify: `docs/conventions/ui-components.md`
- Create: `docs/conventions/connector-metadata.md`
- Create: `docs/conventions/ui-pr-review-checklist.md`
- Modify: `docs/architecture/component-led-ui-vision.md`

Do not modify `cmd/`, `internal/`, `deploy/`, `go.mod`, `go.sum`, or database migrations in this loop.

## Task 1: Reconcile Inputs

**Files:**
- Read: `docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md`
- Read: `docs/research/open-commerce-component-patterns-2026-05-10.md`
- Read: `docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md`
- Read: `docs/conventions/ui-components.md`

- [ ] **Step 1: Inspect working tree**

Run:

```bash
git status --short
```

Expected: note existing modified/untracked files. Do not revert unrelated user or prior-agent changes.

- [ ] **Step 2: Confirm scope**

Run:

```bash
rg -n "Recommended doc follow-ups|Shared vocabulary contract|Standards Gaps|Component candidate list|Marketplace-readiness checklist|Public API per component|Acceptance criteria" docs/research docs/conventions docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md
```

Expected: the command surfaces the source sections needed for the docs in this plan.

- [ ] **Step 3: Confirm this stays docs-only**

Run:

```bash
git diff --name-only
```

Expected: after implementation, changed files are limited to `docs/`.

## Task 2: Create Retail Vocabulary Decision

**Files:**
- Create: `docs/decisions/ui-retail-vocabulary.md`

- [ ] **Step 1: Create the decision doc**

Create `docs/decisions/ui-retail-vocabulary.md` with this structure:

```markdown
---
title: UI Retail Vocabulary Decision
date: 2026-05-10
status: accepted
owners: product, design, engineering
related:
  - docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/architecture/component-led-ui-vision.md
---

# UI Retail Vocabulary Decision

## Decision

Canary merchant UI uses retail-standard, operator-facing nouns by default. AtlasView may use broader platform nouns, but Canary screens should preserve merchant language unless the screen is explicitly administrative or technical.

| Concept | Canary user-facing noun | Connector/standards aliases | AtlasView/platform mapping | Rule |
|---|---|---|---|---|
| Sellable catalog unit | Item | Product, catalog item, SKU, variant | Downstream object alias | Use Item in Canary navigation and operator workflows; use Product only when an ecommerce connector requires it. |
| Product identifier | SKU, barcode, GTIN | UPC, EAN, supplier item code, external item id | Standards anchor metadata | Never collapse SKU, barcode, GTIN, and source-system id into one generic identifier. |
| Supplier relationship | Supplier | Vendor, partner | External party/partner | Use Supplier in merchant operations; Vendor is allowed for connector/source labels or third-party-risk governance. |
| Customer identity | Customer | Party, account | Party, Person where applicable | Use Customer in Canary UI; Party belongs in AtlasView or technical mapping docs. |
| Staff record | Employee | Associate, cashier, staff | Person, role assignment | Use Employee for people records and labor reporting. |
| POS action provenance | Operator | Cashier, associate, actor | Actor | Use Operator when describing who performed a POS or security-relevant action. |
| POS terminal | Register | Workstation, terminal, POS device | Downstream device alias | Use Register for POS terminal concepts; Device remains broader hardware/network language. |
| Payment method in POS proof | Tender | Payment method, payment instrument | Downstream field alias | Use Tender on POS proof, transaction detail, and payment reports where retail precision matters. |
| Broad payment concept | Payment | Authorization, capture, refund | Payment-data boundary metadata | Use Payment in broad merchant copy when POS tender precision is unnecessary. |
| Sale/return record | Transaction | Order, receipt, retail transaction | Downstream event/audit target | Use Transaction for POS records; use Order only for ecommerce/order-management connectors. |
| Physical/business place | Location | Store, warehouse, GLN | Downstream location alias | Use Location generally; use Store only when referring to a retail store specifically. |
| Permission shown to merchant | Permission | Scope, grant | Capability, entitlement, policy | Canary renders merchant-readable Permission labels; AtlasView may author Capability/Policy. |
| Published platform config | Published settings | Manifest, local view | Manifest, local-view state | Avoid Manifest in normal merchant copy; use Published settings or Configuration status. |

## Rationale

NRF/ARTS retail standards, POS marketplace patterns, and open commerce platforms all point toward plain retail vocabulary. Canary is the merchant execution surface, so it should sound like store operations. AtlasView is the management and orchestration plane, so it can keep broader organizational terms and publish mappings to Canary.

## Use in Canary

- New merchant screens must use the Canary user-facing noun from the table unless a product decision says otherwise.
- Connector screens may show aliases only when they help explain a source system.
- Technical identifiers such as GTIN, GLN, OIDC client id, source-system id, and manifest id should be collapsed behind detail/diagnostic affordances unless the screen is for setup, support, compliance, or troubleshooting.

## AtlasView mapping

AtlasView may author capabilities, policies, manifests, people, parties, operating modes, and connector metadata. Canary renders the effective downstream language from that platform substrate. The shared contract is the mapping, not a forced global noun.

## Review triggers

- A new UI introduces Product, Vendor, Party, Workstation, Device, Payment, Order, Capability, Entitlement, or Manifest as visible merchant copy.
- A connector maps external fields into Canary without preserving source-system aliases.
- A report or proof screen uses a term differently than POS or retail standards would.
```

Expected: the decision is concrete enough that reviewers can cite it in PRs.

## Task 3: Create Status Taxonomy Decision

**Files:**
- Create: `docs/decisions/ui-status-taxonomy.md`

- [ ] **Step 1: Create the taxonomy doc**

Create `docs/decisions/ui-status-taxonomy.md` with this structure:

```markdown
---
title: UI Status Taxonomy Decision
date: 2026-05-10
status: accepted
owners: product, design, engineering
related:
  - docs/conventions/ui-components.md
  - docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md
---

# UI Status Taxonomy Decision

## Decision

Canary status UI is organized by status family first, tone second. `components/status-pill` remains the inline rendering primitive, but callers must choose a semantic family before choosing color/tone.

| Family | Meaning | Examples | Allowed tones |
|---|---|---|---|
| lifecycle | Progress through a business workflow | draft, pending, active, complete, canceled, archived | neutral, info, success, warning |
| health | Operational health of a system, connector, or job | healthy, syncing, degraded, disconnected, blocked, failed | success, info, warning, danger |
| severity | Urgency or risk of an alert/case/finding | low, medium, high, critical | neutral, info, warning, danger |
| permission | Whether an actor/app can perform an action | allowed, read-only, missing, denied, sensitive | success, info, warning, danger |
| proof | Evidence/protocol confidence | unverified, verified, disputed, expired, tampered | neutral, success, warning, danger |
| freshness | Age and usability of published or synced data | fresh, stale-soft, stale-hard, unavailable | success, warning, danger, neutral |
| sync | Movement of data between systems | not connected, queued, syncing, synced, partial, failed | neutral, info, success, warning, danger |
| data-boundary | How Canary handles sensitive/payment-adjacent data | reference-only, stores, processes, transmits, tokenized, out-of-scope | neutral, info, warning, danger |

## Rationale

Color-only status does not scale across reports, cases, connectors, permissions, evidence, and AtlasView-published configuration. Families keep labels consistent while allowing each screen to remain domain-specific.

## Use in Canary

- `status-pill` may render `label` and `tone` today, but new callers must document the intended family in view-model code or nearby template comments until the component contract grows a formal `family` param.
- Do not invent new tones without updating this decision.
- Avoid using the same label for different meanings across families. For example, `blocked` in lifecycle and `blocked` in health must have surrounding copy that makes the meaning clear.

## AtlasView mapping

AtlasView may implement richer React status components, but shared statuses should preserve these family names and meanings. Manifest/local-view states map to `freshness`. Connector state maps to `health` or `sync` depending on context.

## Review triggers

- A new badge/status label is added.
- A new color/tone is introduced.
- A status is used without visible text.
- A screen mixes lifecycle, health, and severity as if they were one scale.
```

Expected: future `status-pill` usage has a standards anchor.

## Task 4: Update Component Contract Convention

**Files:**
- Modify: `docs/conventions/ui-components.md`

- [ ] **Step 1: Extend the header contract template**

Update the `Public API per component` example so the header contains `States` and `Accessibility`:

```markdown
  States:
    <state>  <when it appears and how it should render/behave>

  Accessibility:
    Role/landmark: <role or native semantic element>
    Name/label: <how the accessible name is produced>
    Keyboard/focus: <keyboard and focus behavior, or "native control">
    Assistive copy: <screen-reader-only or described-by expectations>
```

Expected: the convention makes accessibility and state part of the public API.

- [ ] **Step 2: Add a standards paragraph**

Add a short section named `Standards checks`:

```markdown
## Standards checks

Interactive components must treat accessibility as part of the contract:
keyboard behavior, focus movement, visible labels, ARIA labels or
descriptions where needed, and non-color-only status communication.
Connector-facing components must also preserve permission, compatibility,
support, and data-boundary language from `docs/conventions/connector-metadata.md`.
```

Expected: the convention points to accessibility and connector standards.

- [ ] **Step 3: Keep extraction discipline unchanged**

Verify the `When to add a component` rule still says components are extracted only when repeated or imminent.

Run:

```bash
rg -n "When to add a component|States:|Accessibility:|Standards checks" docs/conventions/ui-components.md
```

Expected: all four phrases appear.

## Task 5: Create Connector Metadata Convention

**Files:**
- Create: `docs/conventions/connector-metadata.md`

- [ ] **Step 1: Create the convention doc**

Create `docs/conventions/connector-metadata.md` with this structure:

```markdown
---
title: Connector Metadata Convention
date: 2026-05-10
status: draft
owners: product, design, engineering
related:
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/decisions/ui-retail-vocabulary.md
  - docs/decisions/ui-status-taxonomy.md
---

# Connector Metadata Convention

## Purpose

Connector and integration screens must be factual, reviewable, and marketplace-ready. A merchant should understand what the connector does, what data it touches, what state it is in, what it requires, and what to do next before authorizing it.

## Contract

| Field | Required | Meaning | Example |
|---|---:|---|---|
| `key` | yes | Stable internal connector id | `square` |
| `name` | yes | Visible connector name | `Square` |
| `summary` | yes | One factual sentence of merchant value | `Sync locations, tenders, transactions, and item references from Square.` |
| `category` | yes | Connector category | `POS`, `Ecommerce`, `Payments`, `Accounting`, `Inventory`, `Identity` |
| `state` | yes | Current lifecycle/health state | `available`, `connected`, `syncing`, `degraded`, `disconnected`, `blocked`, `unsupported` |
| `primaryActionLabel` | yes | Main action | `Connect`, `Manage`, `Resume sync` |
| `primaryActionHref` | yes | Main action URL | `/connect?source=square` |
| `permissions` | yes | Merchant-readable permissions with read/write and justification | `Read transactions to build proof timelines.` |
| `sensitiveData` | yes | PII/payment/security data touched | `customer email`, `payment reference`, `employee id` |
| `dataBoundary` | yes | How Canary handles sensitive or payment-adjacent data | `reference-only`, `stores`, `processes`, `transmits`, `tokenized`, `out-of-scope` |
| `compatibility` | yes | Regions, devices, plans, business types, or source versions | `Requires Square locations with Orders API access.` |
| `support` | yes | Support path or owner | `Canary support`, docs URL, partner URL |
| `pricingOrRequirements` | no | Cost, plan, prerequisite, or no-extra-cost statement | `Requires Square seller account.` |
| `lastSyncAt` | no | Last successful sync timestamp | `2026-05-10T18:30:00Z` |
| `nextSyncAt` | no | Next scheduled sync timestamp | `2026-05-10T18:45:00Z` |
| `sourceSystem` | no | External system of record | `Square` |
| `identifiers` | no | External ids shown for diagnostics | `location_id`, `merchant_id` |

## Permission shape

Each permission should include:

| Field | Meaning |
|---|---|
| `label` | Merchant-readable permission |
| `direction` | `read`, `write`, or `read-write` |
| `dataCategory` | Item, transaction, customer, employee, payment reference, location, inventory, or configuration |
| `sensitive` | Whether the permission touches sensitive data |
| `justification` | One sentence explaining why Canary needs it |
| `required` | Whether the connector can work without it |

## Data-boundary labels

| Label | Meaning |
|---|---|
| `reference-only` | Canary stores an external reference but does not process the underlying sensitive data. |
| `stores` | Canary stores the data in its own database. |
| `processes` | Canary performs business logic over the data. |
| `transmits` | Canary sends the data to another service. |
| `tokenized` | Canary stores or uses a token instead of raw sensitive data. |
| `out-of-scope` | Canary does not touch the sensitive data category. |

## Review rule

No connector screen should ship unless every required field above can be answered. Missing metadata means the product contract is not ready, even if the OAuth or API code works.
```

Expected: the doc is enough for a future Go view model and component set.

## Task 6: Create UI PR Review Checklist

**Files:**
- Create: `docs/conventions/ui-pr-review-checklist.md`

- [ ] **Step 1: Create the checklist**

Create `docs/conventions/ui-pr-review-checklist.md`:

```markdown
# UI PR Review Checklist

Use this checklist before merging Canary merchant UI changes.

- Does the screen use approved vocabulary from `docs/decisions/ui-retail-vocabulary.md`?
- Does every badge/status map to a family in `docs/decisions/ui-status-taxonomy.md`?
- Does the change reuse existing components before adding bespoke markup?
- If a component changed, does its header document params, slots, states, and accessibility?
- Is status communicated with visible text, not color alone?
- Are labels, focus behavior, keyboard behavior, and ARIA/described-by copy correct for interactive controls?
- If this is a connector/integration screen, does it satisfy `docs/conventions/connector-metadata.md`?
- Are permissions shown with read/write direction, data category, sensitivity, justification, and required/optional status?
- Are payment-adjacent data boundaries explicit?
- Are SKU, GTIN/barcode, source-system id, location id, and merchant labels kept distinct where identifiers appear?
- If this shows a KPI, are formula, scope, source, and freshness documented or linked?
- If this depends on AtlasView-published configuration, are fresh, stale-soft, stale-hard, and unavailable states handled or explicitly out of scope?
- Does the screen remain useful without AtlasView at runtime unless the feature is explicitly AtlasView-admin-only?
- Does the PR avoid new React/runtime commitments unless the React-vs-Go-SSR decision rule has been satisfied?
```

Expected: the checklist is compact enough to paste into PR review.

## Task 7: Update Component-Led UI Vision

**Files:**
- Modify: `docs/architecture/component-led-ui-vision.md`

- [ ] **Step 1: Add standards contract section**

Add a short section after `Design Principles` or before `Component Taxonomy`:

```markdown
## Standards Contracts

The component-led architecture is now governed by local standards docs:

- `docs/decisions/ui-retail-vocabulary.md` controls visible merchant nouns.
- `docs/decisions/ui-status-taxonomy.md` controls status families and tones.
- `docs/conventions/ui-components.md` controls component public APIs, states, and accessibility obligations.
- `docs/conventions/connector-metadata.md` controls marketplace-ready connector metadata.
- `docs/conventions/ui-pr-review-checklist.md` controls frontend PR review.

These docs translate MACH, NRF/ARTS, Square/Clover, GS1, PCI/EMVCo,
W3C, and AtlasView lessons into local rules. They are intentionally
runtime-neutral: Canary can keep Go SSR while AtlasView can implement
matching contracts in React where useful.
```

Expected: the vision points future builders at the new standards layer.

## Task 8: Self-Review And Verification

**Files:**
- Verify all files touched in this plan.

- [ ] **Step 1: Scan for placeholders and forbidden scope**

Run:

```bash
rg -n "TBD|TODO|fill in|later|cmd/|internal/|deploy/|migration" docs/decisions/ui-retail-vocabulary.md docs/decisions/ui-status-taxonomy.md docs/conventions/ui-components.md docs/conventions/connector-metadata.md docs/conventions/ui-pr-review-checklist.md docs/architecture/component-led-ui-vision.md
```

Expected: no TODO/TBD placeholders. Mentions of `internal/` are acceptable only in existing cross-reference/context, not as modified implementation scope.

- [ ] **Step 2: Confirm all expected docs exist**

Run:

```bash
test -f docs/decisions/ui-retail-vocabulary.md
test -f docs/decisions/ui-status-taxonomy.md
test -f docs/conventions/connector-metadata.md
test -f docs/conventions/ui-pr-review-checklist.md
```

Expected: all commands exit zero.

- [ ] **Step 3: Confirm docs-only diff**

Run:

```bash
git diff --name-only
```

Expected: changed files are only under `docs/`.

- [ ] **Step 4: Inspect final diff**

Run:

```bash
git diff -- docs/decisions/ui-retail-vocabulary.md docs/decisions/ui-status-taxonomy.md docs/conventions/ui-components.md docs/conventions/connector-metadata.md docs/conventions/ui-pr-review-checklist.md docs/architecture/component-led-ui-vision.md
```

Expected: the diff implements the dispatch scope and does not start building UI.

## Completion Signal

The prep loop is complete when the six scoped docs are created/updated and the next frontend build session can cite:

```text
Vocabulary: docs/decisions/ui-retail-vocabulary.md
Status: docs/decisions/ui-status-taxonomy.md
Components: docs/conventions/ui-components.md
Connectors: docs/conventions/connector-metadata.md
PR review: docs/conventions/ui-pr-review-checklist.md
Architecture: docs/architecture/component-led-ui-vision.md
```
