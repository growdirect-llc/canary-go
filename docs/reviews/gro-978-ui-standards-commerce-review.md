---
title: GRO-978 UI Standards Commerce Review
date: 2026-05-10
status: accepted
owners: product, design, engineering
linear: GRO-978
related:
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/research/canary-atlasview-ui-standards-alignment-2026-05-10.md
  - docs/architecture/component-led-ui-vision.md
  - docs/decisions/ui-retail-vocabulary.md
  - docs/decisions/ui-status-taxonomy.md
  - docs/conventions/connector-metadata.md
  - docs/conventions/ui-pr-review-checklist.md
  - docs/conventions/ui-components.md
---

# GRO-978 UI Standards Commerce Review

## Decision

Phase 9 UI standards are aligned enough to proceed into Phase 5 UI design.
This is a **go for design**, not a start signal for unrelated Phase 5 UI
implementation.

The review found no reason to reopen Phase 9. It did identify small standards
tightening changes needed before Phase 5 screens are built:

- Connector metadata now requires an explicit authorization model.
- Connector review now captures structured compatibility, source-system
  identifiers, security/privacy links, optional feature bullets, and
  retention/disconnect behavior when sensitive data is involved.
- Vocabulary now pins source records, KPI usage, identity provider language,
  and connected integration language.
- Status taxonomy now includes a governance family for AI, fraud, security,
  and partner-accountability states.
- The PR checklist now makes the standards checks explicit enough for review.

## Review Matrix

| Source family | Review result | Local contract |
|---|---|---|
| Square/Clover marketplace conventions | Aligned after tightening connector metadata around authorization, compatibility, support, pricing/requirements, and direct next action. | `docs/conventions/connector-metadata.md`, `docs/conventions/ui-pr-review-checklist.md` |
| MACH/composable commerce | Aligned. Canary keeps Go SSR and uses stable component/view-model contracts as translation points rather than adopting a vendor runtime. | `docs/architecture/component-led-ui-vision.md`, `docs/decisions/ui-retail-vocabulary.md` |
| NRF/ARTS retail standards | Aligned. Canary preserves Item, Transaction, Tender, Register, Operator, Supplier, Location, Return, Inventory, and KPI language where retail precision matters. | `docs/decisions/ui-retail-vocabulary.md` |
| GS1/OAGi/enterprise interoperability | Aligned after adding source-record and identifier discipline to connector and vocabulary contracts. Standards identifiers remain setup, diagnostics, compliance, or support detail instead of ordinary navigation. | `docs/decisions/ui-retail-vocabulary.md`, `docs/conventions/connector-metadata.md` |
| PCI/EMVCo payment/security boundaries | Aligned. Connector and PR review require data-boundary labels before payment-adjacent UI can imply what Canary stores, processes, transmits, tokenizes, or only references. | `docs/conventions/connector-metadata.md`, `docs/decisions/ui-status-taxonomy.md` |
| OpenID/OAuth/WebAuthn identity flows | Aligned after adding identity provider, connected integration, authorization model, and external scope language. | `docs/decisions/ui-retail-vocabulary.md`, `docs/conventions/connector-metadata.md` |
| W3C/Radix/shadcn accessibility | Aligned. Component headers require states and accessibility; PR checklist requires labels, keyboard/focus behavior, ARIA/described-by copy, and non-color-only status. | `docs/conventions/ui-components.md`, `docs/conventions/ui-pr-review-checklist.md` |
| AtlasView compatibility | Aligned. Canary remains runtime-independent while sharing vocabulary, status families, component contract names, manifest freshness, and capability-to-permission mapping. | `docs/architecture/component-led-ui-vision.md`, `docs/decisions/ui-status-taxonomy.md` |

## Corrections Made

- `docs/decisions/ui-retail-vocabulary.md` adds source record, KPI,
  identity provider, and connected integration decisions.
- `docs/decisions/ui-status-taxonomy.md` adds a governance status family.
- `docs/conventions/connector-metadata.md` moves to accepted, adds GRO-978
  review metadata, and tightens connector authorization, compatibility,
  privacy/security, feature, retention, and standards-alignment fields.
- `docs/conventions/ui-pr-review-checklist.md` adds explicit connector,
  identity/auth, governance, and raw-standards-acronym review checks.
- `docs/architecture/component-led-ui-vision.md` records the GRO-978 review
  as the sixth provenance input and notes the Phase 5 design gate.

## Go / No-Go

**Go:** Phase 5 UI design may proceed using these standards.

**Conditions:**

- Phase 5 PRs must use the UI PR checklist before merge.
- Connector or integration UI must satisfy the connector metadata convention.
- New components must follow the component header contract and add tests.
- No Phase 5 work should introduce a React runtime unless it passes the
  React-vs-Go-SSR decision rule.
- AtlasView-published configuration may be represented only through local
  Canary states; AtlasView must not become a runtime dependency for merchant
  execution screens.

**No-go areas from this review:**

- Do not expose raw standards acronyms as normal merchant navigation.
- Do not hide permissions until after OAuth or partner authorization starts.
- Do not imply Canary handles card data unless the data-boundary contract says
  it stores, processes, transmits, or tokenizes that data.
- Do not use KPI labels without formula, scope, source, and freshness.
- Do not begin Phase 10 RBAC from this review.

## Acceptance Evidence

- Inputs from `GRO-978` were read and compared against the Phase 9 standards
  docs.
- Corrections are docs-only and limited to standards, conventions, and review
  evidence.
- Phase 5 UI implementation was not started.
- Phase 10 RBAC was not started.
