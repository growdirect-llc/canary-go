---
title: Canary Go Component-Led UI Vision
date: 2026-05-10
status: draft
owners: product, design, engineering
related:
  - docs/architecture/canary-go-vision-fit-matrix.md
  - docs/conventions/ui-components.md
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
  - docs/decisions/gro-848-atlasview-identity-integration.md
  - /Users/gclyle/Ruptiv/spec/SDD-007-ui-framework.md
  - /Users/gclyle/Ruptiv/spec/SDD-013-downstream-substrate-contract.md
---

# Canary Go Component-Led UI Vision

## Purpose

Canary Go needs a merchant operator UI that works on its own, aligns with AtlasView when AtlasView is present, and can participate in the broader composable-commerce ecosystem without being rebuilt every time the product surface grows.

The component architecture is the bridge. Components are not just visual fragments. They are durable product contracts: named UI primitives with stable inputs, clear behavior, accessibility expectations, and domain meaning. They let Canary keep the simplicity of Go server-rendered pages while preserving a path toward richer AtlasView and React-based surfaces later.

## Thesis

Canary Go should remain Go SSR for the current merchant surface, but the UI should be authored as a composable system.

That means:

- Keep the runtime boring: Go templates, Chi, pgx, Postgres, server-rendered pages, small JavaScript where needed.
- Make the interface portable: documented components, shared tokens, stable contracts, domain vocabulary, visual parity checks.
- Treat AtlasView as a richer management plane, not a hard runtime dependency for Canary.
- Validate commerce vocabulary and marketplace UX against open and public commerce ecosystems before locking large UI patterns.

The goal is not to copy React, Square, Clover, MACH, or Composable UI. The goal is to learn the common patterns, then express the right subset in Canary's own component substrate.

## Provenance

This vision comes from six local and external inputs.

### 1. Canary Go dispatch posture

The unified dispatch states that Canary's merchant UI is not a temporary throwaway surface. It is the "operates-without-AtlasView" path. AtlasView may eventually offer a richer editor over the same data, but Canary keeps a local operator surface that can continue serving if AtlasView is unavailable.

Source: `docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md`.

### 2. Canary component convention

The existing component convention establishes the first substrate:

- `components/form-field`
- `components/data-table`
- `components/status-pill`
- `components/card`
- `components/drawer`

It also sets the rule that a component's header contract is its public API. That is the right design center. A component is stable when consumers can rely on documented params and slots without reading internals.

Source: `docs/conventions/ui-components.md`.

### 3. AtlasView UI framework

AtlasView's UI is architected differently because it has a different job. AtlasView is the multi-agent command center: Team Page landing, Person Page, notification center, command palette, agent chat drawer, live collaboration, and type-safe API consumption. Its SDD-007 frontend architecture uses React 18, TanStack Router, TanStack Query, Zustand, Tailwind, and shadcn/ui because AtlasView needs heavier browser-side state and interaction.

Canary should learn from that component and token discipline without forcing its current merchant UI into a React runtime before the product needs it.

Source: `/Users/gclyle/Ruptiv/spec/SDD-007-ui-framework.md`.

### 4. AtlasView downstream contract

AtlasView's SDD-013 explicitly frames Canary as the canonical downstream subscriber. AtlasView publishes signed configuration, policy, capability, operating-mode, and agent-profile substrate. Canary materializes a local view, serves from it, and survives publisher outage.

This contract means the UI architecture must be flexible at two boundaries:

- Canary renders local operator workflows directly.
- AtlasView can later author or enrich the same configuration through a richer UI.

Source: `/Users/gclyle/Ruptiv/spec/SDD-013-downstream-substrate-contract.md`.

### 5. Open commerce and POS marketplace precedents

Public commerce sources provide validation material:

- MACH Alliance principles: composable, connected, incremental, open, autonomous.
- MACH Open Data Model: shared vocabulary and translation patterns for Product, Category, Inventory, Promotion, Pricing, and later commerce entities.
- Square App Marketplace docs: seller-facing listing, onboarding, screenshots, support, pricing, and integration transparency.
- Clover App Market docs: search, categories, collections, merchant verticals, app details, support information, and device compatibility.
- Composable UI: open-source React/Next commerce accelerator with a component and design-token orientation.

Sources:

- <https://machalliance.org/mach-principles>
- <https://machalliance.org/insights-hub/the-mach-alliance-open-data-model>
- <https://developer.squareup.com/docs/app-marketplace/listing-best-practices>
- <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/details>
- <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/get-started>
- <https://docs.clover.com/dev/docs/navigating-the-clover-app-market>
- <https://docs.clover.com/dev/docs/managing-app-details>
- <https://composable.com/composable-ui>

### 6. Standards and trust-boundary review

The GRO-978 review compared the Phase 9 standards docs against the research
memo before Phase 5 UI work. The result keeps the architecture direction, but
tightens the local contracts around authorization model, connector
compatibility, source-system identifiers, KPI definitions, payment-data
boundaries, identity language, and AI/fraud/security governance states.

Source: `docs/reviews/gro-978-ui-standards-commerce-review.md`.

## Architectural Position

### Canary Go is the merchant execution surface

Canary owns retail-domain execution: items, inventory, receiving, transfers, returns, transactions, pricing, alerts, cases, reports, workflows, and source-system connectors.

The merchant UI should optimize for:

- Operational clarity.
- Low-latency form and table workflows.
- Robust behavior under partial platform outage.
- Fast server-side access to tenant-scoped data.
- Minimal frontend build surface.
- Component reuse across many server-rendered templates.

### AtlasView is the management and orchestration surface

AtlasView owns platform-level structures: organization, org units, zones, teams, roles, positions, people, tool entitlements, policies, operating modes, agent profiles, notifications, and methodology runtime.

AtlasView should optimize for:

- Configurable work surfaces.
- Agent-mediated operations.
- Live collaboration.
- Rich client-side state.
- Type-safe generated API clients.
- Cross-vertical governance.

### The component substrate is the compatibility layer

The shared denominator is not the frontend runtime. The shared denominator is component vocabulary:

- What is a status?
- What is an integration?
- What is a capability?
- What is an action?
- What is an exception?
- What is a data table?
- What is an empty state?
- What is a guided setup flow?

If Canary and AtlasView share names, tokens, behavior, and domain intent, they can use different rendering stacks without fragmenting the product.

## Design Principles

### 1. Component contracts before visual expansion

Every reusable primitive gets a documented public contract before it spreads to multiple screens. The contract names:

- Rendered role.
- Required and optional params.
- Slots.
- States.
- Accessibility obligations.
- Example usage.

### 2. Domain language beats visual novelty

Use names merchants and commerce systems recognize. Prefer "Items", "Inventory", "Orders", "Pricing", "Promotions", "Customers", "Suppliers", "Locations", "Transactions", "Returns", "Receiving", and "Transfers" unless a Canary-specific term carries product meaning.

Specialized terms such as "Chirps" can remain, but screens should expose the operational noun next to the brand noun when clarity matters.

### 3. Go SSR first, portable contracts always

Canary components are Go templates today. They should be shaped so a future React component could implement the same contract:

- Stable prop names.
- Clear slot boundaries.
- Minimal hidden template logic.
- Data prepared in handlers or view models.
- Tokens in CSS variables rather than one-off inline colors.

### 4. AtlasView compatible, not AtlasView dependent

Canary must serve local operator workflows without AtlasView. But Canary should use AtlasView's design decisions where they represent firm-level product posture:

- Component-led architecture.
- Token-driven theming.
- Accessible primitives.
- Command/action vocabulary.
- Policy and capability language.
- Manifest-consumer local-view model.

### 5. Marketplace-ready UX

Square and Clover marketplace docs make one lesson clear: merchant-facing integrations must be plain, factual, transparent, and easy to adopt.

Canary setup and integration surfaces should show:

- What the integration does.
- What permissions/data it needs.
- What status it is in.
- What the merchant should do next.
- What support path exists.
- What business type or maturity level it fits.

### 6. Incremental adoption

The component system should grow only where reuse is real. The existing rule still holds: extract a component when the primitive appears in three or more templates, or two templates with one imminent.

Do not build a massive design system ahead of product demand. Build a narrow substrate and let real screens pull the next component forward.

## Standards Contracts

The component-led architecture is now governed by local standards docs:

- `docs/architecture/canary-go-vision-fit-matrix.md` controls how UI work maps to the broader Ruptiv, AtlasView, retail-capability, shared-platform, and agent-memory model.
- `docs/decisions/ui-retail-vocabulary.md` controls visible merchant nouns.
- `docs/decisions/ui-status-taxonomy.md` controls status families and tones.
- `docs/conventions/ui-components.md` controls component public APIs, states, and accessibility obligations.
- `docs/conventions/connector-metadata.md` controls marketplace-ready connector metadata.
- `docs/conventions/ui-pr-review-checklist.md` controls frontend PR review.

These docs translate MACH, NRF/ARTS, Square/Clover, GS1, PCI/EMVCo, W3C, and AtlasView lessons into local implementation rules. They govern how new UI is designed, reviewed, and shipped while staying runtime-neutral: Canary can keep Go SSR for merchant execution screens, and AtlasView can implement compatible component contracts in React where its richer management-plane workflows need them.

GRO-978 confirmed a **go for Phase 5 UI design**, with one guardrail: new
Phase 5 screens must pass the UI PR checklist before implementation merges.
That is a design/readiness gate, not permission to start unrelated Phase 5 UI
work from this document.

## Component Taxonomy

### Existing substrate

| Component | Role | Keep |
|---|---|---|
| `form-field` | Label + input/select/textarea + help/error | Yes. Extend before forking. |
| `data-table` | Table with headers, rows, empty state | Yes. Needs mobile and action affordance review. |
| `status-pill` | Semantic inline status label | Yes. Normalize tones and meanings. |
| `card` | Bordered content container | Yes, but avoid nested-card page layouts. |
| `drawer` | Slide-out panel | Yes. Important for bulk-fix and detail workflows. |

### Near-term component candidates

| Candidate | Why it exists | Source inspiration |
|---|---|---|
| `metric-tile` | Dashboard/report KPIs repeat across many templates | Canary dashboard and reports |
| `empty-state` | Empty states are hand-written throughout templates | Square clarity guidance; internal drift |
| `action-bar` | Page-level primary/secondary actions repeat | Canary item/settings/list pages |
| `filter-bar` | Search, sort, date range, status filters | Clover marketplace search/sort/category patterns |
| `integration-card` | POS, ecommerce, payments, and source connectors need clear setup/status | Square/Clover marketplace listing cards |
| `connector-status` | OAuth, webhook, sync, health, last-run state | Square get-started and Clover developer dashboard patterns |
| `permission-scope-list` | Display what an integration or API key can access | AtlasView capability/entitlement model |
| `timeline` | Alerts, cases, audit, receiving, returns, protocol proof | Canary evidence and audit surfaces |
| `stepper` | Onboarding, import jobs, guided setup flows | Square listing progress; Canary onboarding |
| `callout` | Configuration warnings, blocked states, degraded mode | Square transparency guidance |
| `command-menu` | Future keyboard/action surface, possibly AtlasView-aligned | AtlasView SDD-007 command palette |

### Later component candidates

| Candidate | Trigger |
|---|---|
| `chart-panel` | Dashboard chart dependency and empty/error handling become standardized |
| `heatmap` | Alert density and staffing/inventory density views repeat |
| `bulk-fix-drawer` | CSV import and exception remediation flows need repeatable grouped edits |
| `evidence-panel` | Cases, exceptions, transaction proof, protocol validation converge |
| `manifest-state-panel` | AtlasView local-view freshness becomes visible in Canary admin UI |

## Commerce Vocabulary Alignment

Canary should treat MACH ODM and public POS marketplace docs as validation references, not as binding schema.

Initial alignment targets:

| Canary domain | External vocabulary to compare | UX implication |
|---|---|---|
| Item | Product, SKU, variant, category, media | Item setup should not paint us into a single-POS vocabulary. |
| Inventory | Inventory, stock, availability, location | Reports and movements should separate quantity, location, and availability. |
| Pricing | Price, promotion, tax, discount | Pricing screens should distinguish base price, applied promotion, and tax treatment. |
| Customer | Customer, party, identifier | Customer risk and context should leave room for AtlasView Party alignment. |
| Order/Transaction | Order, payment, tender, receipt | Transaction proof should keep payment/tender language understandable. |
| Supplier | Vendor, supplier, partner | Use supplier in UI where possible; map vendor aliases at connector boundary. |
| Integration | App, connector, source, marketplace listing | Setup screens should show value, permissions, status, support, and next action. |

## UX Validation Heuristics

Use these checks when reviewing new screens.

### Merchant comprehension

- Can a first-time merchant understand the page without knowing Canary internals?
- Are technical acronyms expanded or avoided?
- Is the primary task clear?
- Does the page say what changed and what to do next?

### Marketplace readiness

- Could this integration be explained in a Square or Clover marketplace listing?
- Does the setup path have a direct "get started" route?
- Are screenshots and status states easy to understand on mobile?
- Are prerequisites explicit?
- Is support information discoverable?

### Composability

- Is this a new one-off pattern or an instance of an existing primitive?
- If the same screen were later re-authored in AtlasView React, what component contract would carry over?
- Does the component accept structured params rather than raw HTML where possible?
- Does the view model do the data shaping before template render?

### Operational independence

- If AtlasView is down, does the Canary screen still serve?
- If the AtlasView manifest is stale-soft, does the UI show degraded-but-operable state?
- If stale-hard, are write actions clearly blocked while reads continue?

## React vs Go SSR Decision Rule

Use Go SSR when the screen is mostly:

- Forms.
- Tables.
- Filtered lists.
- Detail pages.
- Simple cards.
- Report snapshots.
- Server-owned workflows.

Consider React or AtlasView ownership when the screen requires:

- Rich client-side state.
- Drag and drop.
- Live collaboration.
- Heavy graph interaction.
- Command palette workflows.
- Agent chat as the primary interaction model.
- Offline client behavior.
- Complex optimistic updates.

This rule is about fit, not ideology. Canary can stay Go SSR while AtlasView uses React because they serve different layers.

## Implementation Direction

### Phase A: Stabilize the substrate

- Keep `docs/conventions/ui-components.md` as the component API convention.
- Add missing documentation for states and accessibility to each component header over time.
- Refactor high-reuse pages away from inline style blocks into shared CSS classes and components.
- Add visual parity notes when refactors change appearance.

### Phase B: Add commerce-aware primitives

Add only the components pulled by real screens:

- `empty-state`
- `metric-tile`
- `filter-bar`
- `integration-card`
- `connector-status`
- `permission-scope-list`
- `stepper`

Each new component requires a test in `internal/web/templates_components_test.go`.

### Phase C: Validate against open commerce patterns

Run the product-manager research dispatch in:

`docs/superpowers/specs/2026-05-10-open-commerce-component-research-dispatch.md`

The output should become a researched addendum to this document or a new decision record, depending on whether it changes architecture.

### Phase D: Align with AtlasView where contracts cross

For each cross-surface concept, name the owning layer:

- Identity runtime: Canary `internal/identity`.
- Manifest publisher: AtlasView.
- Manifest local view: Canary.
- Rich org/team/person editor: AtlasView.
- Merchant execution screens: Canary.
- Shared component vocabulary: both.

## Decisions to Preserve

1. Canary's merchant UI is permanent.
2. AtlasView is a richer management plane, not a runtime dependency for Canary.
3. Go SSR remains the default for Canary operator workflows.
4. React remains appropriate for AtlasView's stateful command-center UX.
5. Components are the unit of portability.
6. Commerce vocabulary should be validated externally before it hardens internally.
7. Marketplace UX should be treated as a product-quality bar, not only as a distribution channel.

## Open Questions

- Which Canary components should share exact names with AtlasView React components?
- Should `status-pill` tones be aligned to a firm-wide severity/status taxonomy?
- Should connector setup surfaces follow Square-style app listing sections internally?
- How should Canary expose AtlasView manifest freshness in the admin UI?
- Which commerce vocabulary source wins when MACH ODM, Square, Clover, and ARTS use different terms?
- Should a generated component catalog page exist for Canary templates?

## Non-Goals

- Do not migrate Canary's merchant UI to React solely for architectural symmetry.
- Do not import third-party React component libraries into the Go SSR surface.
- Do not make AtlasView availability a prerequisite for Canary's core operator workflows.
- Do not build a broad design system ahead of actual screen demand.
- Do not copy marketplace wording or screenshots from Square, Clover, or any other platform.

## Success Criteria

The component architecture is working when:

- New screens compose from existing primitives by default.
- Visual drift across tables, forms, cards, status pills, and empty states declines.
- A designer or product manager can name a new screen in terms of component primitives.
- AtlasView and Canary can discuss the same concept with the same vocabulary even when rendered in different stacks.
- Integration setup screens are understandable to merchants without developer context.
- Public commerce references can validate, challenge, or improve our nouns and flows before implementation.
