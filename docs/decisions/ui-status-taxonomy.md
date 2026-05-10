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
| governance | Review/accountability posture for AI, fraud, security, or partner controls | reviewed, review-needed, overdue, suspended | success, warning, danger, neutral |

## Rationale

Color-only status does not scale across reports, cases, connectors, permissions, evidence, and AtlasView-published configuration. Families keep labels consistent while allowing each screen to remain domain-specific.

## Use in Canary

- `status-pill` may render `label` and `tone` today, but new callers must document the intended family in view-model code or nearby template comments until the component contract grows a formal `family` param.
- Do not invent new tones without updating this decision.
- Avoid using the same label for different meanings across families. For example, `blocked` in lifecycle and `blocked` in health must have surrounding copy that makes the meaning clear.

## AtlasView mapping

AtlasView may implement richer React status components, but shared statuses should preserve these family names and meanings. Manifest/local-view states map to `freshness`. Connector state maps to `health` or `sync` depending on context.

AI, fraud, security, and partner-accountability states map to `governance`.
Canary should render those only when a feature can affect customers,
employees, pricing, inventory, fraud, security, or other retail operations.

## Review triggers

- A new badge/status label is added.
- A new color/tone is introduced.
- A status is used without visible text.
- A screen mixes lifecycle, health, and severity as if they were one scale.
