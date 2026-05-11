---
title: Canary Go Vision Fit Matrix
date: 2026-05-11
status: active
owners: product, architecture, engineering
related:
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
  - docs/superpowers/specs/2026-05-10-canary-go-phase-9-supply-chain-dispatch.md
  - docs/architecture/component-led-ui-vision.md
  - docs/decisions/gro-848-atlasview-identity-integration.md
  - /Users/gclyle/ruptiv-execution-vault/00-start-here/current-operating-picture.md
  - /Users/gclyle/ruptiv-execution-vault/30-canary-go/delivery-wave-priorities.md
  - /Users/gclyle/ruptiv-execution-vault/35-retail-capabilities/capability-map.md
---

# Canary Go Vision Fit Matrix

## Purpose

This document is the translation layer between the current Canary Go engineering plan and the broader Ruptiv company vision.

Use it when selecting the next dispatch, shaping a Linear ticket, or deciding whether a change is merely a local fix or part of the Canary/AtlasView/shared-platform operating model.

It does not replace Linear or the active dispatch specs. Linear remains the execution queue; this matrix explains how the queue supports the full vision.

## Fit Summary

| Area | Current Fit | Evidence | Main Gap |
|---|---|---|---|
| Canary as merchant execution surface | Strong | Unified dispatch and component-led UI vision both state Canary must operate without AtlasView at runtime. | Need more working flows against real persistence, not just hardened services. |
| AtlasView as management and orchestration plane | Good | GRO-848/GRO-923 and component-led UI vision preserve AtlasView as publisher/authoring plane. | Manifest consumer, local-view cache, operating modes, capability matrix, and agent-profile consumption are not fully built. |
| Retail capability coverage | Partial | Schemas and services cover many retail domains; vault capability cards preserve the broader scope. | Go dispatches currently cover hardening plus Item Setup; many retail capability cards are not yet mapped to implementation waves. |
| Detection and proof spine | Strong | TSP, Chirp, Fox/Hawk, Alert, Bull, Owl, Analytics, protocol, audit, and evidence modules exist and are under hardening. | Operator surfaces and workflow recovery views lag behind service depth. |
| UI standards and component model | Good | Component-led UI docs, UI conventions, vocabulary, status taxonomy, and GRO-901 design are present. | Needs repeated enforcement through PR review and more component extraction as screens ship. |
| Agentic and memory operating model | Emerging | Active vault defines memory-bus, curation loop, agent contracts, and no-HIL promotion controls. | Canary Go has runner specs, but not a local closeout rule that updates the vault when delivery scope changes. |
| Security and production readiness | Good near-term focus | Phase 9 dispatch targets scanner gates, non-root images, timeouts, dependency fixes, cookie security, and race cleanup. | CK8 must close before broad UI/product acceleration. |

## Vision Areas To Execution Surfaces

| Vision Area | Canary Go Surface | AtlasView / Shared Platform Surface | Current Spec Or Ticket | Fit | Next Action |
|---|---|---|---|---|---|
| One identity boundary | `internal/identity`, API-key middleware, scopes, refresh family, JWKS | AtlasView publishes roles, capabilities, operating modes, and identity config | GRO-906, GRO-912, GRO-913, GRO-931, GRO-949, GRO-954, GRO-955, GRO-848, GRO-923 | Good | Finish manifest-consumer contract and local-view consumption after hardening gates. |
| Tenant-safe retail execution | Service handlers, stores, `ClaimsFromContext`, tenant predicates | Shared policy vocabulary and capability matrix | GRO-904, GRO-905, GRO-916, GRO-928 | Strong | Keep every new route tenant-derived from claims; no form/query tenant overrides. |
| Retail item master | `internal/item`, catalog schema, barcode lookup, item web templates | AtlasView may author richer configuration; Canary remains local create/edit path | GRO-901, GRO-902, GRO-903 | Good near-term | Ship scan-to-lookup first, then import and enrichment flows. |
| Inventory and receiving | `internal/inventory`, `internal/receiving`, `internal/replenishment`, schema | Retail capability cards for inventory, receiving, replenishment, OTB | Delivery waves 2, 5, 11 | Partial | After Phase 9/Item Setup, select one integration-boundary wave and wire real persistence. |
| POS and sales audit spine | TSP, transaction, Counterpoint adapter, protocol evidence | Standards anchors for ARTS/GS1 and connector metadata | Phase 3, Phase 8, GRO-946, GRO-952 | Strong substrate | Add operator visibility for ingestion, redaction, validation, and exception recovery. |
| Detection and case workflow | Chirp, Alert, Fox, Hawk, Bull, workflow package | Case-type registry, closed-loop attribution, evidentiary rail | Phases 3, 6-8; delivery waves 1, 3, 4 | Good | Prioritize allow-list/threshold plumbing, rule consumption, and workflow surfaces. |
| Analytics and intelligence | Owl, analytics service, report service | AtlasView agent profiles and shared memory context | Delivery waves 6, 12 | Emerging | Surface existing Owl analytics before adding new model work. |
| Shared memory and agent execution | MCP endpoint, audit, memory-bus references, docs | Ruptiv execution vault, memory-bus, agent commissioning, no-HIL loop | Phase 7, vault curation specs | Emerging | Add closeout rule: dispatches that alter capability scope update the vault and reseed memory. |
| Component-led UI | Go templates, `templates/components`, UI conventions | AtlasView React component discipline and design tokens | GRO-922, GRO-901, GRO-978 review | Good | Treat component public headers and UI PR checklist as merge gates. |
| Multi-store operating model | location, hierarchy, tenant, store-level services | AtlasView org units, zones, teams, roles | Delivery wave 10 | Partial | Do not bury store hierarchy inside screen-specific handlers; keep it as a platform concept. |
| Supplier and PO lifecycle | supplier, PO, receiving, three-way match workflow | Retail capability cards and policy/approval model | Delivery wave 11 | Partial | Map existing schema/modules before adding new tables. |
| Compliance, audit, and proof | protocol audit/evidence, L402, redaction, namespaces | Evidentiary rail standard, Mission Control/handoff proof concepts | GRO-932, GRO-952, GRO-956, delivery wave 7 | Good substrate | Make proof and audit inspectable, authenticated, and failure-aware. |

## Priority Interpretation

The current Go specs are intentionally narrower than the full vision. They are doing the right near-term job: harden identity, tenant scope, protocol, MCP, supply chain, and UI substrate before product acceleration.

The broader vision should influence order after those gates:

1. Close Phase 9 and CK8 so the service tree is safe enough for sustained product work.
2. Ship Phase 5 Item Setup Flow A/B/C because it is the first concrete merchant workflow that proves the component-led UI model.
3. Move into delivery waves 1-3 from the execution vault: allow-list/threshold plumbing, portal data wiring, and detection rule consumption.
4. Then run waves 4-7: workflow recovery, inventory/receiving, Owl intelligence, and protocol portal.
5. Keep AtlasView manifest consumption as a parallel architecture thread, not a blocker for local Canary execution unless a ticket explicitly depends on published policy/capability state.

## Agent Operating Rules

Agents working in this repo should apply the matrix this way:

- Start with the active dispatch spec for the immediate queue.
- Check this matrix when a ticket touches identity, UI, retail capabilities, AtlasView, MCP, audit/proof, or memory.
- Prefer working integration boundaries over screen scaffolds.
- Do not add schema just to satisfy a screen if an existing module can be wired.
- Do not promote old source material directly into memory; update the Ruptiv execution vault first, then reseed memory-bus.
- If a dispatch changes capability scope, delivery order, or an operating rule, update the corresponding vault artifact during closeout.
- Treat the vault as company memory and memory-bus as a derived index, never the other way around.

## Known Gaps To Close

| Gap | Why It Matters | Proposed Owner |
|---|---|---|
| Historical `docs/sdds/go-handoff/` references remain in comments and generated provenance | Agents can chase missing files if they treat old references as active docs. | Documentation hygiene pass after current hardening work. |
| No generated mapping from retail capability cards to Go packages/routes | Capability scope can drift from implementation scope. | Product/architecture closeout after Phase 5. |
| Manifest-consumer implementation is not yet a stable local subsystem | AtlasView policy and capability publishing cannot fully operate until Canary can materialize local state. | Identity/platform dispatch after GRO-848/GRO-923. |
| Vault update is not a formal Canary Go Definition of Done | The company memory can lag behind repo execution. | Add to future dispatch templates and closeout checklist. |
| Operator workflow surfaces lag behind backend service depth | Useful service behavior remains hard to inspect and recover. | Delivery waves 1-7. |

## Closeout Standard

When a Canary Go task changes the vision fit, close the loop in this order:

1. Update the code, tests, migrations, and local repo docs.
2. Update this matrix if the cross-product mapping changed.
3. Update the Ruptiv execution vault if the company memory, retail capability map, or agent operating rule changed.
4. Reseed memory-bus from the curated vault.
5. Commit repo changes with the acceptance evidence named in the dispatch.
