---
title: Open Commerce Component Research Dispatch
date: 2026-05-10
status: proposed
owner: product-manager-agent
related:
  - docs/architecture/component-led-ui-vision.md
  - docs/conventions/ui-components.md
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
---

# Open Commerce Component Research Dispatch

## Mission

Run a deep product-management research pass across open-source ecommerce, composable commerce, POS developer ecosystems, and app marketplaces. The goal is to extract standards, patterns, vocabulary, component concepts, and UX validation rules that should inform Canary Go's component-led merchant UI and AtlasView compatibility posture.

This is a research dispatch, not an implementation ticket. The output should make Canary easier to place in the open/composable commerce world and easier to extend through Square, Clover, TPAT/TPA-style partner ecosystems, MACH, and open-source commerce accelerators.

## Background

Canary Go is a Go SSR merchant execution surface. AtlasView is the richer management and orchestration plane. Both need a shared component vocabulary even though they use different frontend runtimes.

Canary already has a small Go-template component substrate:

- `form-field`
- `data-table`
- `status-pill`
- `card`
- `drawer`

The next phase should be informed by external commerce patterns before the UI hardens into local-only terminology.

## Core Research Questions

1. What public or open-source commerce UI patterns should Canary learn from?
2. What component primitives recur across modern commerce admin, marketplace, POS, inventory, and integration surfaces?
3. Which nouns and flows are standard enough that Canary should align with them?
4. Which open-source projects provide useful patterns without requiring a runtime migration?
5. What can Square and Clover marketplace docs teach us about merchant-facing integration UX?
6. What does MACH / composable commerce imply for component contracts, data vocabulary, and long-term portability?
7. How should Canary position its UI so future integrations can "play easily" in marketplaces and composable stacks?

## Required Source Families

The research must cover at least these families. Prefer primary sources and official docs.

### MACH and composable commerce

- MACH Alliance principles.
- MACH Open Data Model and GitHub materials if available.
- Composable UI / Composable.com open-source accelerator.
- commercetools frontend and composable commerce examples.
- Alokai / Vue Storefront patterns where relevant.
- Medusa, Saleor, Vendure, Reaction Commerce, Open Commerce, or similar open-source commerce admin/storefront projects.

### NRF, ARTS, and retail industry standards

- National Retail Federation resources relevant to retail technology, loss prevention, cybersecurity, AI governance, consumer insights, and retail operations.
- ARTS / OMG Retail standards, especially the ARTS Operational Data Model, retail data warehouse concepts, XML/message sets, UnifiedPOS, standard RFPs, and business process models.
- NRF event and research materials that shape retailer vocabulary, loss-prevention expectations, AI governance posture, and retail technology buying criteria.
- Any public ARTS-derived terminology relevant to products/items, transactions, tenders, promotions, inventory, returns, stores, registers, employees, customers, and reporting KPIs.

### Adjacent standards bodies and interoperability groups

- GS1 standards for product, place, asset, and party identifiers; barcodes; RFID; GTIN; GLN; GDSN product-data synchronization; EPCIS/CBV supply-chain visibility events.
- EMVCo specifications for secure and interoperable card, contactless, QR, tokenized, and remote commerce payments.
- PCI Security Standards Council guidance where Canary touches payments-adjacent data, card-present/card-not-present workflows, or marketplace trust posture.
- Open Applications Group (OAGi / OAGIS / connectSpec) for cross-application and B2B enterprise integration vocabulary.
- OMG Retail Domain Task Force and UnifiedPOS / WS-POS for point-of-service peripheral and device-interface terminology.
- W3C and IETF standards only where directly relevant to web app platform behavior, accessibility, identity, security headers, WebAuthn, OAuth/OIDC, or event streams.
- OpenID Foundation only where identity, OIDC discovery, SSO, or marketplace app authorization flows are in scope.
- ISO or UN/CEFACT references only where they materially affect commerce documents, supply-chain events, invoices, orders, or trade data vocabulary.

### POS and marketplace ecosystems

- Square Developer docs, App Marketplace publishing/listing guidance, sample apps, SDK repositories.
- Clover Developer docs, App Market listing, app category, collections, device compatibility, support-info guidance, SDK/sample repositories.
- Any public TPAT/TPA or partner-app marketplace standards relevant to POS, ecommerce, retail, accounting, or payments.
- Shopify app marketplace and Polaris admin patterns if useful as a secondary reference.

### UI/component systems

- shadcn/ui and Radix UI patterns, especially component contracts and accessibility.
- Shopify Polaris for commerce admin conventions.
- Tailwind/shadcn dashboard examples only as design references, not as drop-in code.
- Open-source admin templates only when they carry reusable interaction patterns.

## Research Method

1. Inventory sources.
   - Capture source name, URL, license posture, relevance, and trust level.
   - Separate official primary sources from blogs, examples, and community templates.
   - Classify each source by domain: retail data, product identity, supply-chain visibility, POS device, payments, enterprise integration, marketplace UX, UI component system, identity/security.

2. Extract vocabulary.
   - Commerce nouns: item/product, inventory, stock, category, price, promotion, order, payment, tender, customer, supplier/vendor, location, return, fulfillment.
   - Marketplace nouns: app, integration, connector, listing, category, collection, install, permission, support, pricing, compatibility.
   - Retail standards nouns: store, workstation/register, retail transaction, line item, tender, tax, discount, promotion, return, employee/operator, customer, inventory control document, stock ledger, reporting KPI.
   - Platform nouns: capability, role, policy, entitlement, manifest, agent profile, operating mode.

3. Extract component primitives.
   - Forms, tables, filters, search, cards, status badges, metric tiles, empty states, steppers, drawers, modals, command palette, notification center, integration cards, permission lists, audit timelines, import-review tables, bulk-fix workflows.

4. Extract flow patterns.
   - OAuth/connect.
   - App listing / details.
   - Install / get started.
   - Integration health.
   - Product/item setup.
   - Inventory sync.
   - CSV import and review.
   - App marketplace discovery.
   - Support and compatibility disclosure.

5. Compare against Canary.
   - Map each pattern to current Canary templates or missing component candidates.
   - Identify vocabulary mismatches that could make Canary feel non-standard.
   - Identify where Canary should intentionally differ because it is an operator/security/protocol product.

6. Produce recommendations.
   - Recommend additions to `docs/architecture/component-led-ui-vision.md`.
   - Recommend new component candidates for `internal/web/templates/components/`.
   - Recommend any decision records needed.
   - Recommend no-code UX checks that can be added to PR review.

## Deliverables

The product-manager agent must produce:

1. **Research memo** at `docs/research/open-commerce-component-patterns-2026-05-10.md`.
   - Executive summary.
   - Source table.
   - Pattern inventory.
   - Component candidate list.
   - Vocabulary alignment table.
   - Canary implications.
   - AtlasView implications.
   - Risks and open questions.

2. **Recommendation appendix** in the memo.
   - Top 10 patterns to adopt.
   - Top 10 patterns to avoid or defer.
   - Top 10 source links to keep as durable references.

3. **Component backlog proposal**.
   - A ranked list of candidate components.
   - For each: role, expected params, states, source inspiration, first Canary template to refactor.

4. **Marketplace-readiness checklist**.
   - A short checklist that future connector/integration screens must pass.

5. **Decision prompts**.
   - Any founder/product decisions needed before implementation.

## Acceptance Criteria

The dispatch is complete when:

- At least 20 sources are reviewed, with primary sources clearly marked.
- Square, Clover, MACH, NRF/ARTS, and at least three open-source commerce projects are included.
- Every recommendation maps to either:
  - a Canary component candidate,
  - a vocabulary decision,
  - a UX validation checklist item,
  - an AtlasView compatibility note,
  - or a deliberate "do not adopt" conclusion.
- The memo avoids copying proprietary text or long excerpts.
- The memo distinguishes inspiration from implementation.
- No recommendation requires moving Canary Go to React unless it explicitly passes the React-vs-Go-SSR decision rule in `docs/architecture/component-led-ui-vision.md`.

## Suggested Seed Sources

- MACH principles: <https://machalliance.org/mach-principles>
- MACH Open Data Model: <https://machalliance.org/insights-hub/the-mach-alliance-open-data-model>
- Composable UI: <https://composable.com/composable-ui>
- Square App Marketplace listing best practices: <https://developer.squareup.com/docs/app-marketplace/listing-best-practices>
- Square listing details guidance: <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/details>
- Square get-started guidance: <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/get-started>
- Square Developer sample apps: <https://github.com/Square-Developers>
- Square Open Source: <https://square.github.io/>
- Clover Developers: <https://www.clover.com/developers>
- Clover App Market navigation: <https://docs.clover.com/dev/docs/navigating-the-clover-app-market>
- Clover app listing management: <https://docs.clover.com/dev/docs/managing-app-details>
- NRF: <https://nrf.com/>
- NRF Center for Retail & Consumer Insights: <https://cdn.nrf.com/research-insights/center-retail-consumer-insights>
- NRF AI principles for retail: <https://nrf.com/research/principles-use-artificial-intelligence-retail-sector>
- OMG Retail Domain Task Force / ARTS: <https://www.omg.org/industries/retail.htm>
- ARTS Operational Data Model 7.3 overview: <https://www.omg.org/retail-depository/arts-odm-73/introduction_and_overview.htm>
- UnifiedPOS: <https://www.omg.org/retail/unified-pos.htm>
- GS1 standards: <https://www.gs1.org/standards>
- GS1 retail: <https://www.gs1.org/industries/retail>
- GS1 GDSN: <https://www.gs1.org/services/gdsn>
- GS1 EPCIS and CBV: <https://www.gs1.org/standards/epcis>
- EMVCo overview: <https://www.emvco.com/about-us/overview-of-emvco/>
- OAGi: <https://oagi.org/>
- Shopify Polaris: <https://polaris.shopify.com/>
- shadcn/ui: <https://ui.shadcn.com/>
- Radix UI: <https://www.radix-ui.com/>
- Medusa: <https://github.com/medusajs/medusa>
- Saleor: <https://github.com/saleor/saleor>
- Vendure: <https://github.com/vendure-ecommerce/vendure>
- Alokai: <https://github.com/vuestorefront/vue-storefront>
- commercetools GitHub: <https://github.com/commercetools>

## Research Guardrails

- Treat licenses seriously. Do not recommend copying components unless the license and attribution path are clear.
- Prefer concepts and contracts over visual imitation.
- Keep Canary's current Go SSR architecture as the default.
- Keep AtlasView compatibility in vocabulary, tokens, and component contracts, not necessarily in runtime.
- Avoid adopting storefront-only patterns that do not serve merchant operations.
- Avoid design-system sprawl: recommend new components only when they map to repeated Canary needs.

## Follow-On Work

After the research memo lands:

1. Product reviews vocabulary and component recommendations.
2. Engineering converts accepted recommendations into a short implementation plan.
3. Any accepted component gets a test-first implementation in `internal/web/templates_components_test.go`.
4. Any accepted vocabulary change gets a decision record or update to `docs/architecture/component-led-ui-vision.md`.
5. Any AtlasView-crossing contract gets mirrored in the Ruptiv repo or referenced from SDD-013.
