---
title: Canary Go UI Standards Prep Dispatch
date: 2026-05-10
status: proposed
owner: design-engineering-agent
related:
  - docs/architecture/component-led-ui-vision.md
  - docs/conventions/ui-components.md
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
---

# Canary Go UI Standards Prep Dispatch

## Mission

Prepare Canary Go for the next frontend build loop by locking the
standards decisions that should govern new merchant UI work.

This dispatch is intentionally **documentation and contract prep**. It
does not build new pages, add new UI components, or refactor existing
templates. Its job is to remove ambiguity before Phase 5 Item Setup or
connector/onboarding UI work resumes.

## Why this exists

Canary now has a real Go-template component substrate:

- `components/form-field`
- `components/data-table`
- `components/status-pill`
- `components/card`
- `components/drawer`

The research pass shows the substrate is directionally aligned with
MACH/composable commerce, NRF/ARTS retail vocabulary, Square/Clover
marketplace patterns, GS1 identifier discipline, PCI/EMV payment-boundary
language, W3C accessibility, and AtlasView compatibility. The remaining
risk is that new screens will make vocabulary, status, accessibility, and
connector-permission decisions locally.

This dispatch turns those standards into local project contracts.

## Scope

Create or update these docs:

1. `docs/decisions/ui-retail-vocabulary.md`
   - Locks user-facing nouns for Canary merchant UI.
   - Covers Item/Product, Supplier/Vendor, Customer/Party,
     Employee/Operator, Register/Device, Tender/Payment, Transaction/Order,
     Location/Store, SKU/GTIN/barcode, Permission/Capability, and
     Manifest/Published settings.

2. `docs/decisions/ui-status-taxonomy.md`
   - Defines status families and allowed tones.
   - Covers lifecycle, health, severity, permission, proof, freshness, sync,
     and data-boundary states.
   - Gives examples that map to `components/status-pill`.

3. `docs/conventions/ui-components.md`
   - Extends the component header contract to require `States` and
     `Accessibility`.
   - Adds standards checks for WCAG/Radix-style keyboard, focus, labels,
     roles, and assistive copy where applicable.
   - Keeps the existing "extract only when repeated" rule.

4. `docs/conventions/connector-metadata.md`
   - Defines the view-model contract for marketplace-grade connector and
     integration screens.
   - Covers value summary, current state, permissions, sensitive data,
     compatibility, support, pricing/requirements, health, source system,
     identifiers, payment-data boundary, and next action.

5. `docs/conventions/ui-pr-review-checklist.md`
   - Gives reviewers a compact checklist before new UI lands.
   - Covers vocabulary, status family, component reuse, accessibility,
     connector permissions, payment/data boundaries, KPI definition, and
     AtlasView manifest-state handling.

6. `docs/architecture/component-led-ui-vision.md`
   - Add a short "Standards contracts now govern implementation" section
     linking the new decision/convention docs.
   - Keep Go SSR first and AtlasView-compatible contracts as the architecture
     posture.

## Out of scope

- No new templates in `internal/web/templates/components/`.
- No refactors of existing merchant screens.
- No React or AtlasView implementation.
- No Phase 5 Item Setup page work.
- No backend schema, handler, or migration work.
- No copy changes in existing screens beyond documentation examples.

## Standards posture to preserve

- **MACH/composable commerce:** components are contracts and translation
  points, not framework artifacts.
- **NRF/ARTS:** Canary merchant UI should use retail-standard language for
  POS, inventory, tender, register, operator, return, and KPI concepts.
- **Square/Clover marketplaces:** connector UX must disclose value,
  permissions, compatibility, support, requirements, status, and next action.
- **GS1/OAGi:** identifiers and source systems must be explicit when they
  matter, but acronyms should stay out of ordinary merchant copy.
- **PCI/EMVCo:** payment-adjacent screens must disclose the data boundary and
  avoid implying Canary handles card data unless it actually does.
- **W3C/Radix/shadcn:** accessibility, state, focus, keyboard behavior, and
  ARIA are component API concerns, not final polish.
- **AtlasView:** share vocabulary, tokens, states, and component contracts;
  do not make AtlasView a runtime dependency for Canary.

## Acceptance criteria

The dispatch is complete when:

- All six docs listed in Scope exist or are updated.
- Each decision doc has a `Decision`, `Rationale`, `Use in Canary`,
  `AtlasView mapping`, and `Review triggers` section.
- `docs/conventions/ui-components.md` shows the updated component header
  template with `States` and `Accessibility`.
- `docs/conventions/connector-metadata.md` includes a concrete field table
  that a future Go view model can implement.
- `docs/conventions/ui-pr-review-checklist.md` is short enough to use in PRs.
- `docs/architecture/component-led-ui-vision.md` links the new docs.
- No implementation files under `cmd/`, `internal/`, `deploy/`, or
  migrations are changed.

## Fresh-session kickoff

Use this prompt in a fresh session:

```text
We are in /Users/gclyle/CanaryGo. Execute docs/superpowers/plans/2026-05-10-ui-standards-prep-dev-loop.md against docs/superpowers/specs/2026-05-10-ui-standards-prep-dispatch.md.

This is a docs-only standards prep loop before more frontend building. Create/update the vocabulary, status taxonomy, component contract, connector metadata, PR checklist, and component-led vision docs. Do not build new components or refactor templates.
```

## Status

Proposed for the next fresh dev session.
