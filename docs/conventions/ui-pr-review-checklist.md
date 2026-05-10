---
title: UI PR Review Checklist
date: 2026-05-10
status: accepted
owners: product, design, engineering
reviewed_by: GRO-978
related:
  - docs/decisions/ui-retail-vocabulary.md
  - docs/decisions/ui-status-taxonomy.md
  - docs/conventions/ui-components.md
  - docs/conventions/connector-metadata.md
---

# UI PR Review Checklist

Use this checklist before merging Canary merchant UI changes.

- Does the screen use approved vocabulary from `docs/decisions/ui-retail-vocabulary.md`?
- Does every badge/status map to a family in `docs/decisions/ui-status-taxonomy.md`?
- Does the change reuse existing components before adding bespoke markup?
- If a component changed, does its header document params, slots, states, and accessibility?
- Is status communicated with visible text, not color alone?
- Are labels, focus behavior, keyboard behavior, and ARIA/described-by copy correct for interactive controls?
- If this is a connector/integration screen, does it satisfy `docs/conventions/connector-metadata.md`?
- Does the connector show authorization model, prerequisites, compatibility, support, and pricing/requirements before the merchant starts the flow?
- Are permissions shown with read/write direction, data category, sensitivity, justification, and required/optional status?
- Are payment-adjacent data boundaries explicit?
- Are SKU, GTIN/barcode, source-system id, location id, and merchant labels kept distinct where identifiers appear?
- If this shows a KPI, are formula, scope, source, and freshness documented or linked?
- If this touches identity/auth, does it use Identity provider, Connected integration, Permission, and source-system scope language consistently?
- If this depends on AtlasView-published configuration, are fresh, stale-soft, stale-hard, and unavailable states handled or explicitly out of scope?
- Does the screen remain useful without AtlasView at runtime unless the feature is explicitly AtlasView-admin-only?
- If AI, fraud, security, or partner automation affects retail operations, is owner, purpose, review path, and governance state visible?
- Does the PR avoid new React/runtime commitments unless the React-vs-Go-SSR decision rule has been satisfied?
- Does the PR avoid exposing raw standards acronyms as ordinary navigation or marketing copy unless the screen is for setup, diagnostics, compliance, or support?
