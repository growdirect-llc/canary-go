---
title: Open Commerce Component Patterns
date: 2026-05-10
status: research
owner: product-manager-agent
dispatch: docs/superpowers/specs/2026-05-10-open-commerce-component-research-dispatch.md
related:
  - docs/architecture/component-led-ui-vision.md
  - docs/conventions/ui-components.md
---

# Open Commerce Component Patterns

## Executive summary

Canary Go should learn from open commerce systems at the level of vocabulary, component contracts, and validation rules, not runtime choices. The strongest cross-source pattern is that commerce platforms succeed when merchant-facing surfaces are plain, explicit, and action-oriented: what the feature does, what data it touches, what state it is in, what the merchant should do next, and where support or compatibility limits live.

The research supports the current Canary posture: keep Go SSR as the default merchant execution surface, keep AtlasView as the richer management plane, and make component contracts the portability layer between them. Marketplace ecosystems such as Square, Clover, Xero, Toast, and Shopify repeatedly validate the need for integration cards, permission-scope disclosure, direct get-started flows, support/pricing transparency, compatibility messaging, and installed-app health states. MACH and open-source commerce platforms validate shared nouns such as product/item, category, inventory/stock, location, price, promotion, order, payment, customer, fulfillment, return, channel, and connector. NRF/ARTS adds the older retail-standards layer Canary should respect for POS and operations vocabulary: store, workstation/register, retail transaction, line item, tender, tax, inventory control, stock ledger, employee/operator, and reporting KPI.

The expanded standards pass adds a second spine: interoperability by identifier, security boundary, message shape, and web platform behavior. GS1 pushes Canary toward explicit product, place, party, asset, barcode, RFID, GDSN, and EPCIS/CBV event semantics. EMVCo and PCI SSC push payment-adjacent UI toward precise disclosure and containment rather than casual payment language. OAGi/OAGIS and ISO/UN-CEFACT reinforce enterprise document nouns such as order, invoice, shipment, quote, purchasing, and compliance. W3C, IETF, and OpenID Foundation sources reinforce that accessibility, OAuth/OIDC, WebAuthn, secure headers, and event-stream behavior are not implementation trivia; they are UI contract requirements when the user is granting access, authenticating, or acting on live operational data.

The most useful next components for Canary are `empty-state`, `filter-bar`, `action-bar`, `integration-card`, `connector-status`, `permission-scope-list`, `stepper`, `metric-tile`, `timeline`, and `callout`. These should be added only when pulled by active screens, following `docs/conventions/ui-components.md` and tests in `internal/web/templates_components_test.go`.

## Source ledger

| # | Source | URL | Family | Posture | Trust | Canary use |
|---|---|---|---|---|---|---|
| 1 | MACH principles | <https://machalliance.org/mach-principles> | MACH | Public guidance | Primary | Vocabulary decision, AtlasView note |
| 2 | MACH Open Data Model article | <https://machalliance.org/insights-hub/the-mach-alliance-open-data-model> | MACH | Public guidance | Primary | Vocabulary decision, component contracts |
| 3 | MACH ODM launch article | <https://machalliance.org/insights-hub/mach-alliance-announces-open-data-model-initiative> | MACH | Public guidance | Primary | AtlasView note, source of interoperability stance |
| 4 | Composable UI product page | <https://composable.com/composable-ui> | Composable commerce | Public docs | Primary | Inspiration only; no runtime migration |
| 5 | Composable UI docs | <https://docs.composable.com/> | Composable commerce | Public docs | Primary | Component/data orchestration patterns |
| 6 | Composable UI GitHub | <https://github.com/composable-com/composable-ui> | Open source | MIT | Primary | License-safe inspiration, not copied code |
| 7 | commercetools docs index | <https://docs.commercetools.com/docs> | Composable commerce | Public docs | Primary | Domain nouns and connector vocabulary |
| 8 | commercetools Frontend overview | <https://docs.commercetools.com/frontend-getting-started/developer-guide> | Composable commerce | Public docs | Primary | Business-user/developer component split |
| 9 | commercetools extension docs | <https://docs.commercetools.com/frontend-development/using-the-commercetools-extension> | Composable commerce | Public docs | Primary | Data orchestration and extension boundary |
| 10 | Square App Marketplace listing guide | <https://developer.squareup.com/docs/app-marketplace/listing-best-practices> | POS marketplace | Public docs | Primary | Marketplace-readiness checklist |
| 11 | Square listing basic info | <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/basics> | POS marketplace | Public docs | Primary | Content/vocabulary rules |
| 12 | Square listing details | <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/details> | POS marketplace | Public docs | Primary | Integration card text, feature rows |
| 13 | Square get-started links | <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/get-started> | POS marketplace | Public docs | Primary | Direct onboarding flow |
| 14 | Square pricing guidance | <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/pricing> | POS marketplace | Public docs | Primary | Pricing/support transparency |
| 15 | Square OAuth permissions | <https://developer.squareup.com/docs/oauth-api/square-permissions> | POS marketplace | Public docs | Primary | Permission-scope list |
| 16 | Square Developer GitHub | <https://github.com/Square-Developers> | POS developer ecosystem | Public repos | Primary/community mix | Sample app posture |
| 17 | Square Open Source | <https://square.github.io/> | Developer ecosystem | Open-source catalog | Primary | License caution and inspiration |
| 18 | Clover developers | <https://www.clover.com/developers> | POS developer ecosystem | Public docs | Primary | POS app posture |
| 19 | Clover App Market navigation | <https://docs.clover.com/dev/docs/navigating-the-clover-app-market> | POS marketplace | Public docs | Primary | Discovery, installed apps, filters |
| 20 | Clover listing management | <https://docs.clover.com/dev/docs/managing-app-details> | POS marketplace | Public docs | Primary | Listing fields, support, compatibility |
| 21 | Clover permissions | <https://docs.clover.com/dev/docs/permissions> | POS marketplace | Public docs | Primary | Permission justification and PII guardrails |
| 22 | Clover devices | <https://docs.clover.com/docs/clover-devices> | POS hardware | Public docs | Primary | Compatibility disclosure |
| 23 | Clover GitHub | <https://github.com/clover> | POS developer ecosystem | Public repos | Primary/community mix | SDK/sample posture |
| 24 | Shopify app design guidelines | <https://shopify.dev/docs/apps/design> | App marketplace | Public docs | Primary | Predictable merchant admin UX |
| 25 | Shopify Polaris React | <https://polaris-react.shopify.com> | UI system | Deprecated public docs | Primary but deprecated | Pattern vocabulary only |
| 26 | shadcn/ui docs | <https://ui.shadcn.com/docs> | UI system | MIT-style open code ecosystem | Primary | Component ownership and composability |
| 27 | shadcn/ui data table guide | <https://ui.shadcn.com/docs/components/data-table> | UI system | Public docs | Primary | Avoid over-generalizing data tables |
| 28 | Radix UI introduction | <https://www.radix-ui.com/primitives/docs/overview/introduction> | UI primitives | MIT | Primary | Accessible primitive contracts |
| 29 | Radix UI accessibility | <https://www.radix-ui.com/primitives/docs/overview/accessibility> | UI primitives | MIT | Primary | Focus, keyboard, ARIA checklist |
| 30 | Radix Dialog | <https://www.radix-ui.com/primitives/docs/components/dialog> | UI primitives | MIT | Primary | Drawer/modal accessibility inspiration |
| 31 | Radix Tabs | <https://www.radix-ui.com/primitives/docs/components/tabs> | UI primitives | MIT | Primary | Keyboard behavior for tabbed admin surfaces |
| 32 | Medusa GitHub | <https://github.com/medusajs/medusa> | Open-source commerce | MIT | Primary | Modular commerce nouns |
| 33 | Medusa commerce modules | <https://docs.medusajs.com/resources/commerce-modules> | Open-source commerce | Public docs | Primary | Component backlog by domain |
| 34 | Medusa product module | <https://docs.medusajs.com/resources/commerce-modules/product> | Open-source commerce | Public docs | Primary | Product/item vocabulary |
| 35 | Medusa order module | <https://docs.medusajs.com/resources/commerce-modules/order> | Open-source commerce | Public docs | Primary | Order/return/timeline flows |
| 36 | Saleor GitHub | <https://github.com/saleor/saleor> | Open-source commerce | BSD-3-Clause | Primary | API-first extensibility |
| 37 | Saleor dashboard GitHub | <https://github.com/saleor/saleor-dashboard> | Open-source admin | BSD-3-Clause | Primary | Dashboard app extension posture |
| 38 | Saleor docs | <https://docs.saleor.io/docs/guides/taxes> | Open-source commerce | Public docs | Primary | Channel, checkout, promotion, app nouns |
| 39 | Saleor product page | <https://saleor.io/> | Open-source commerce | Public product docs | Primary | Dashboard UI extension pattern |
| 40 | Vendure GitHub | <https://github.com/vendure-ecommerce/vendure> | Open-source commerce | GPLv3/commercial | Primary | Concepts only; no code copying |
| 41 | Vendure channels docs | <https://docs.vendure.io/current/core/core-concepts/channels> | Open-source commerce | Public docs | Primary | Channel/location/role vocabulary |
| 42 | Vendure Admin Central | <https://vendure.io/product/platform/admin-central> | Open-source admin | Public product docs | Primary | Bulk editing, filtering, admin extensibility |
| 43 | Xero App Store terms | <https://developer.xero.com/xero-app-store-terms-and-conditions/> | App marketplace | Public terms | Primary | Listing/support obligations |
| 44 | Xero connecting apps | <https://apps.xero.com/us/pages/re-connecting-apps> | App marketplace | Public merchant docs | Primary | Connect flow and data disclosure |
| 45 | Toast partner integration listing docs | <https://doc.toasttab.com/doc/devguide/apiDeveloperPortalCustomIntegrations.html> | POS/restaurant marketplace | Public docs | Primary | Listing progress and support details |
| 46 | NRF home | <https://nrf.com/> | Retail industry | Public industry body | Primary | Retail vocabulary and buyer context |
| 47 | NRF Center for Retail & Consumer Insights | <https://cdn.nrf.com/research-insights/center-retail-consumer-insights> | Retail industry | Public research hub | Primary | Consumer/retail context |
| 48 | NRF AI principles | <https://nrf.com/research/principles-use-artificial-intelligence-retail-sector> | Retail governance | Public report | Primary | AI governance and partner accountability |
| 49 | NRF AI principles release | <https://nrf.com/media-center/press-releases/nrf-releases-retail-principles-artificial-intelligence> | Retail governance | Public press release | Primary | Governance, trust, workforce, partner accountability |
| 50 | NRF Center for Digital Risk & Innovation | <https://nrf.com/resources/nrf-center-digital-risk-innovation> | Retail governance | Public resource hub | Primary | Cybersecurity, fraud, AI risk posture |
| 51 | NRF AI focus area | <https://nrf.com/resources/nrf-center-digital-risk-innovation/ai-focus-area> | Retail governance | Public resource hub | Primary | Agentic AI and retail governance posture |
| 52 | OMG Retail Domain Task Force / ARTS | <https://www.omg.org/industries/retail.htm> | Retail standards | Public standards body | Primary | ARTS, UnifiedPOS, DWM, XML message sets |
| 53 | ARTS Operational Data Model | <https://www.omg.org/retail/operational-data-model.htm> | Retail standards | Public standards overview | Primary | Retail data model and terminology |
| 54 | ARTS ODM 7.3 overview | <https://www.omg.org/retail-depository/arts-odm-73/introduction_and_overview.htm> | Retail standards | Public standard narrative | Primary | Retail entity/business-area vocabulary |
| 55 | ARTS schema downloads | <https://www.omg.org/retail/schema.htm> | Retail standards | Public standards catalog | Primary | Retail XML/message-set integration patterns |
| 56 | OMG Retail DTF profile | <https://www.omg.org/hot-topics/retail-profile-for-security-maturity-model.htm> | Retail standards | Public standards summary | Primary | BPM, DWM, ODM, schemas, RFP posture |
| 57 | GS1 standards overview | <https://www.gs1.org/standards> | Product/place/asset identity | Public standards catalog | Primary | GTIN, GLN, GPC, barcode, RFID, GDSN, EPCIS anchors |
| 58 | GS1 retail industry page | <https://www.gs1.org/industries/retail> | Retail standards | Public industry guidance | Primary | Product and supply-chain identifier posture |
| 59 | GS1 GDSN | <https://www.gs1.org/standards/gdsn> | Product data sync | Public standards catalog | Primary | Master product data, product images, measurements, data quality |
| 60 | GS1 EPCIS and CBV | <https://www.gs1.org/standards/epcis> | Supply-chain visibility | Public standard | Primary | What/when/where/why/how event semantics |
| 61 | GS1 GLN allocation rules | <https://www.gs1.org/standards/gs1-gln-allocation-rules-standard/current-standard> | Party/location identity | Public standard | Primary | Parties, locations, traceability, recall readiness |
| 62 | EMVCo overview | <https://www.emvco.com/about-us/overview-of-emvco/> | Payments interoperability | Public standards body | Primary | Card/contactless/3DS/token/QR/SRC interoperability posture |
| 63 | PCI SSC standards overview | <https://www.pcisecuritystandards.org/standards/> | Payment security | Public standards body | Primary | PCI DSS, P2PE, Secure Software, PTS POI, MPoC/CPoC/SPoC |
| 64 | OAGi | <https://oagi.org/> | Enterprise integration | Public standards body | Primary | OAGIS/connectSpec, ERP, order, invoice, shipment vocabulary |
| 65 | OpenID Connect overview | <https://www.openid.net/developers/how-connect-works/> | Identity/security | Public standards body | Primary | OIDC, OAuth-based sign-in, claims, relying party, provider |
| 66 | W3C WCAG overview | <https://www.w3.org/WAI/standards-guidelines/wcag/> | Web accessibility | Public standard | Primary | WCAG 2.x conformance and testable accessibility criteria |
| 67 | IETF OAuth 2.0 security BCP | <https://www.ietf.org/archive/id/draft-ietf-oauth-security-topics-29.html> | Authorization/security | Public internet draft | Primary | OAuth threat model and secure authorization posture |
| 68 | W3C WebAuthn | <https://www.w3.org/TR/webauthn-3/> | Authentication/security | Public recommendation | Primary | Passkeys/authenticator ceremonies where identity flows mature |
| 69 | UN/CEFACT | <https://unece.org/trade/uncefact> | Trade documents | Public standards body | Primary | Cross-border trade, business process, semantic document vocabulary |
| 70 | ISO/TC 154 | <https://www.iso.org/committee/53186.html> | Business document exchange | Public standards body | Primary | Processes, data elements, documents for commerce and administration |

## Pattern inventory

### Marketplace and connector surfaces

Marketplace ecosystems converge on a small set of merchant-facing primitives:

- App/integration listing with name, logo, tagline, description, feature bullets, screenshots, category, pricing, support, legal/privacy links, and compatibility.
- Get-started path that sends the merchant directly to onboarding, connection, or sales contact when self-serve is not possible.
- Installed-app inventory with status, pricing/fees, open/manage action, subscription change, uninstall, and rating/review affordances.
- Category and collection browsing, search, sort, and merchant vertical filtering.
- Permissions shown before install/connect, with clear read/write scope and justification.
- Device, region, business type, and data-sensitivity compatibility disclosures.

Canary implication: every connector screen should be internally reviewable as if it were a Square/Clover/Xero-style listing, even if Canary never publishes that screen in a marketplace.

### Composable commerce surfaces

Composable commerce sources emphasize:

- Stable domain entities over vendor-specific screen names.
- Extension points and recipes instead of one forced implementation path.
- Data orchestration across product, inventory, pricing, promotion, media, channel, cart/order, payment, and fulfillment services.
- Business-user surfaces separated from developer extension mechanisms.
- Incremental adoption and replaceable modules.

Canary implication: component params and view models should preserve domain shape. A connector-specific template should translate external terms at the boundary, not leak one vendor's model through the shared component system.

### NRF/ARTS retail standards surfaces

NRF and ARTS/OMG add the standards vocabulary that predates modern app marketplaces and composable commerce:

- ARTS ODM frames retail as a transaction-heavy operating model spanning inventory management, price management, sales reporting, and workforce management.
- ARTS and OMG publish multiple integration artifacts: Operational Data Model, Data Warehouse Model, XML/message sets, business process models, UnifiedPOS, and template RFPs.
- UnifiedPOS is specifically about point-of-service device interfaces such as scanners, printers, and scales.
- The Data Warehouse Model posture validates reporting KPIs as a first-class retail concern, not only dashboards as decoration.
- NRF's digital-risk work frames AI, cybersecurity, fraud prevention, customer trust, workforce impact, and business partner accountability as retail technology buying concerns.

Canary implication: Canary's operator/security/protocol identity is not an exception to retail standards; it is the reason to be precise. POS-facing screens should preserve retail transaction, tender, workstation/register, operator, tax, discount, return, stock ledger, and KPI language where those terms carry industry meaning.

### Adjacent standards and interoperability surfaces

The expanded dispatch adds standards that should inform contracts more than screens:

- GS1 validates treating product, location, party, asset, barcode, RFID, product data synchronization, and supply-chain visibility events as explicit modeled concepts rather than free-text metadata.
- EPCIS/CBV is especially useful for event semantics: what object moved or changed, when it happened, where it happened, why it happened, and which business step/disposition applies.
- EMVCo validates precise payment technology language for card, contactless, 3-D Secure, tokenization, QR, and secure remote commerce. Canary should avoid implying direct card-data handling where a payment partner owns it.
- PCI SSC validates UI disclosure around payment-data boundaries, device/payment-solution trust, secure software posture, and whether Canary stores, processes, transmits, or merely references payment data.
- OAGi/OAGIS and trade-document standards validate enterprise integration nouns such as order, quote, invoice, shipment, purchasing, compliance, payment, and financials.
- OpenID/OAuth/WebAuthn standards validate explicit sign-in, consent, claims, relying party/client, provider, token, passkey, session, and logout language in identity or marketplace authorization flows.
- W3C WCAG validates accessibility as a component contract, not a final QA pass.

Canary implication: connector, identity, device, payment-adjacent, inventory, and supply-chain screens need contract fields for identifiers, data boundary, source system, assurance level, and support path. AtlasView implication: standards are mostly adapter metadata and governance context, not core AtlasView ontology.

### Admin and operations surfaces

Open-source commerce admin systems repeatedly expose:

- Dense product/order/inventory tables with search, filters, sort, row actions, and bulk actions.
- Detail pages with status, timeline/history, related entities, and next actions.
- Guided setup/import flows with progress, validation, and review before commit.
- Extension/app surfaces that add dashboard panels, shortcuts, webhooks, or embedded views.
- Multi-channel and multi-location scoping.

Canary implication: `data-table` should stay simple, but Canary needs companion primitives for filters, row actions, bulk review, empty states, and timelines.

### UI primitive systems

Radix and shadcn/ui validate the contract-first approach:

- Prefer accessible primitive anatomy: trigger, content, title, description, close, list, item, panel, etc.
- Publish component contracts and states, not just visuals.
- Treat focus management, keyboard behavior, and screen-reader labels as part of the API.
- Keep ownership local when customization matters.
- Avoid making a single data-table abstraction carry every possible sorting/filtering/pagination need.

Canary implication: Go template component headers should grow accessibility and state sections as components become more interactive.

## Vocabulary alignment table

| Canary term | External terms | Recommendation | Mapping type |
|---|---|---|---|
| Item | Product, SKU, variant, catalog item | Keep `Item` in POS/operator UI; expose `Product` as integration alias when ecommerce context dominates. | Vocabulary decision |
| SKU | SKU, variant SKU, item code | Keep `SKU`; distinguish SKU from variant/item id in view models. | Vocabulary decision |
| Category | Category, collection, facet, product type | Use `Category` for merchant grouping; use `Collection` only for curated marketplace/app groups. | Vocabulary decision |
| Inventory | Inventory, stock, availability | Use `Inventory` for domain; use `Stock` for quantity-at-location labels. | Vocabulary decision |
| Location | Store, stock location, warehouse, channel | Use `Location` for physical store/stock place; use `Channel` only for sales channel. | Vocabulary decision |
| GLN | Location id, party id, ship-to, bill-to | Use as an integration identifier where present; do not expose as primary merchant copy unless troubleshooting or compliance requires it. | Vocabulary decision |
| GTIN | Barcode, UPC/EAN, item id, product id | Preserve as product identifier metadata; keep `SKU` distinct from GTIN/barcode in view models. | Vocabulary decision |
| GDSN product data | Product master data, catalog sync | Treat as source/sync metadata for product/item imports, not as a UI label. | UX validation checklist item |
| EPCIS event | Visibility event, traceability event, chain-of-custody event | Use event-shape semantics for supply-chain/import/audit timelines when needed. | Component candidate: `timeline` |
| Price | Price, price list, pricing tier | Use `Price`; make price list/tier explicit where needed. | Vocabulary decision |
| Promotion | Promotion, discount, voucher, offer | Use `Promotion` for configurable rule; use `Discount` for applied transaction effect. | Vocabulary decision |
| Transaction | Order, payment, tender, receipt | Keep `Transaction` for POS proof; map ecommerce `Order` separately. | Vocabulary decision |
| Retail transaction | Transaction, sale, return, exchange, receipt | Use `Retail transaction` in standards-facing docs; use `Transaction` in UI where concise. | Vocabulary decision |
| Line item | Order line, sale line, transaction line | Use `Line item` for receipt/transaction detail rows. | Vocabulary decision |
| Tender | Payment method, payment instrument | Keep `Tender` in POS proof and transaction detail; use `Payment` in broad merchant copy. | Vocabulary decision |
| Workstation/register | POS device, register, terminal, station | Use `Register` in merchant copy; preserve `Workstation` only in standards/technical mapping. | Vocabulary decision |
| Employee/operator | Staff, user, cashier, associate | Use `Employee` for HR/admin, `Operator` for action provenance on POS/security events. | Vocabulary decision |
| Reporting KPI | Metric, measure, performance indicator | Treat KPIs as named retail measures with definitions, not arbitrary dashboard numbers. | UX validation checklist item |
| Customer | Customer, party, account | Keep `Customer`; preserve AtlasView `Party` as a platform-level identity concept. | AtlasView compatibility note |
| Supplier | Supplier, vendor, partner | Prefer `Supplier` in retail ops; map `Vendor` at connector boundary. | Vocabulary decision |
| Connector | App, integration, extension, partner app | Use `Connector` for configured data link; use `Integration` for merchant-facing setup copy. | Vocabulary decision |
| OAuth client | App, connected app, relying party | Use standards language in technical/admin UI; merchant copy should say connected app/integration. | UX validation checklist item |
| OIDC provider | Identity provider, sign-in provider, authorization server | Use `Identity provider` in user-facing admin copy; keep OIDC/OpenID terms in technical docs. | Vocabulary decision |
| Payment data boundary | Card data environment, token, payment account data, sensitive auth data | Explicitly disclose whether Canary stores/processes/transmits payment data or only references partner records. | UX validation checklist item |
| Capability | Permission, scope, entitlement, role | Use `Capability` for AtlasView/Canary policy contract; show merchant-facing `Permission` labels. | AtlasView compatibility note |
| Manifest | App manifest, extension manifest, local view | Keep `Manifest` technical; surface as "configuration source" or "published settings" in merchant UI. | AtlasView compatibility note |
| Operating mode | Mode, environment, release group | Keep `Operating mode`; show effects as concrete permissions/actions. | AtlasView compatibility note |

## Component candidate list

| Rank | Candidate | Role | Expected params | States | Source inspiration | First Canary refactor |
|---:|---|---|---|---|---|---|
| 1 | `empty-state` | Reusable no-data/no-config result with one clear next action. | `title`, `body`, `actionLabel`, `actionHref`, `tone`, `icon` | default, warning, blocked, loading fallback | Square plain-language listing guidance; shadcn Empty | `internal/web/templates/onboarding/import.html`, list pages |
| 2 | `filter-bar` | Search, status, category, date/location filters above tables. | `searchName`, `searchValue`, `filters`, `sortOptions`, `actionHref` | default, filtered, no results | Clover search/categories/sort; admin tables | `internal/web/templates/items/list.html`, `transactions.html` |
| 3 | `action-bar` | Page-level primary, secondary, and bulk actions. | `primary`, `secondary`, `bulk`, `selectedCount` | default, disabled, bulk-active | Shopify predictable admin UX; commerce admin tables | `internal/web/templates/items/detail.html`, settings pages |
| 4 | `integration-card` | Summary card for connector/app with value, status, and next action. | `name`, `summary`, `logo`, `status`, `category`, `action`, `metadata` | available, connected, degraded, blocked, unsupported | Square/Clover/Xero app listings | `internal/web/templates/connect.html`, `onboarding/connect.html` |
| 5 | `connector-status` | Compact connector health and sync state display. | `status`, `lastSync`, `nextSync`, `errors`, `supportHref` | healthy, syncing, degraded, disconnected, blocked | Installed app and integration health patterns | `internal/web/templates/ecom/sync.html` |
| 6 | `permission-scope-list` | Human-readable read/write/data-sensitivity disclosure. | `scopes`, `justifications`, `pii`, `required` | read-only, write, sensitive, missing | Square OAuth scopes; Clover permission justifications | `internal/web/templates/onboarding/connect.html` |
| 7 | `stepper` | Guided setup/import/progress flow indicator. | `steps`, `current`, `completeHref`, `orientation` | pending, active, complete, error | Square listing progress; Toast listing progress | `internal/web/templates/onboarding/progress.html` |
| 8 | `metric-tile` | KPI/stat card with delta and context. | `label`, `value`, `delta`, `trend`, `href`, `tone` | normal, good, warning, critical, loading | Commerce/admin dashboards | `internal/web/templates/dashboard.html`, reports |
| 9 | `timeline` | Audit, order, return, sync, and case event history. | `events`, `empty`, `dense` | default, empty, error | ARTS transaction history; Medusa order lifecycle; admin audit trails | `internal/web/templates/cases/evidence.html`, `transaction_proof.html` |
| 10 | `callout` | Inline warning/info/success/error message with optional action. | `tone`, `title`, `body`, `action` | info, warning, critical, success | Square transparency; Clover compatibility banners | `internal/web/templates/settings/devices.html` |
| 11 | `compatibility-list` | Region/device/business-type compatibility disclosure. | `regions`, `devices`, `businessTypes`, `limitations` | compatible, partial, unsupported | Clover device/category guidance | Connector detail screens |
| 12 | `review-table` | Import/bulk-change review with valid/error rows. | `columns`, `rows`, `summary`, `actions` | clean, warnings, errors, partial | CSV import and bulk-fix flows | `internal/web/templates/onboarding/import.html` |
| 13 | `kpi-definition` | Defines a retail KPI with numerator, denominator, date/location scope, and source. | `name`, `definition`, `formula`, `scope`, `source`, `updatedAt` | normal, draft, deprecated | ARTS DWM/KPI posture; NRF retail reporting expectations | Report pages |
| 14 | `risk-governance-panel` | Shows AI/fraud/cyber partner-risk controls and accountability. | `riskArea`, `owner`, `controls`, `reviewedAt`, `actions` | compliant, review-needed, blocked | NRF Digital Risk & Innovation and AI principles | Future AI/agent integration screens |
| 15 | `tabs` | Small number of related panels with keyboard contract. | `tabs`, `active`, `ariaLabel` | active, disabled | Radix Tabs | Admin/settings detail screens |
| 16 | `toast/notice` | Temporary feedback after actions. | `tone`, `message`, `action` | success, error, undoable | App admin systems | Post-submit flows |
| 17 | `manifest-state-panel` | Shows AtlasView local-view freshness and operability. | `state`, `publishedAt`, `expiresAt`, `blockedActions` | fresh, stale-soft, stale-hard, unavailable | AtlasView downstream contract | Future admin/config screen |
| 18 | `identifier-disclosure` | Shows external identifiers without confusing them with merchant names. | `identifiers`, `source`, `copyable`, `supportHref` | hidden, summary, expanded, mismatch | GS1 GTIN/GLN; OIDC subject/client identifiers | Item/location/customer/connector diagnostics |
| 19 | `data-boundary-panel` | Explains what data Canary stores, processes, transmits, or references for an integration. | `dataTypes`, `boundary`, `processor`, `retention`, `risk`, `supportHref` | reference-only, stores, transmits, sensitive, blocked | PCI SSC, EMVCo, marketplace permission patterns | Payment-adjacent connector setup |
| 20 | `source-system-chip` | Labels the authoritative source for a field, event, KPI, or connector state. | `system`, `recordId`, `freshness`, `lastSeenAt` | authoritative, mirrored, stale, conflict | GS1 GDSN/EPCIS, OAGi integration posture | Import review, item detail, reports |

## Canary implications

- Keep the Go SSR runtime. None of the researched sources justifies moving Canary merchant workflows to React. The React-heavy sources are useful as contract and pattern references only.
- Add components when repeated screens pull them forward. The highest-confidence primitives are `empty-state`, `filter-bar`, `integration-card`, `connector-status`, `permission-scope-list`, and `stepper`.
- Split `data-table` responsibilities. Keep `components/data-table` as a simple rendering primitive; place search/filter/sort/bulk action behavior in adjacent components or page view models.
- Treat connector setup as marketplace-grade UX. Connector pages should always answer: what does this do, what data does it need, what state is it in, what should I do next, what does it cost or require, what support/compatibility limits exist?
- Make permissions reviewable. Every permission/scope shown to a merchant should have read/write semantics, data category, and a short justification.
- Use external vocabulary as a translation layer. Canary can keep retail-specific terms like Item and Transaction, but connector code should map Product/Order/Payment terms deliberately.
- Use ARTS/NRF vocabulary when Canary is describing POS, reporting, loss-prevention, workforce, tender, or transaction provenance concepts. These standards support Canary's operator/security posture instead of diluting it.
- Model GS1 identifiers deliberately. `SKU`, `GTIN`, barcode, product id, item id, and supplier item code should not collapse into one generic identifier.
- Treat EPCIS/CBV as a useful event-shape reference for traceability and supply-chain timelines when Canary needs chain-of-custody proof.
- Add a payment-data-boundary convention before payment-adjacent connector screens imply more than Canary actually handles. EMVCo and PCI SSC should shape disclosure and containment, not drive a payment UI redesign.
- Keep OAGi/OAGIS, ISO, and UN/CEFACT as enterprise-integration vocabulary references for documents and partner flows; do not import heavy document standards into normal operator copy.
- Extend component headers. For any interactive component, the header should include states, keyboard/focus expectations, ARIA labels, and escape/close behavior where relevant.

## AtlasView implications

- Shared vocabulary matters more than shared runtime. Canary and AtlasView should agree on status, capability, connector, permission, manifest, operating mode, location, channel, and customer/party mapping.
- AtlasView can author richer integration metadata, but Canary should render the local operator view independently.
- Manifest freshness needs a UI contract. Canary should eventually expose fresh, stale-soft, stale-hard, and unavailable states through a reusable component.
- Component names should be intentionally mirrored only when contracts match. For example, `permission-scope-list` and `connector-status` can plausibly exist in both stacks. `data-table` may need different implementation contracts across Go SSR and React.
- Agentic/composable posture benefits from MACH ODM-style translation recipes: do not force one global schema, but document bridges between systems.
- NRF AI governance implies AtlasView-authored AI/agent capabilities should surface accountability, workforce impact, customer trust, and business partner controls when those capabilities are rendered in Canary.
- AtlasView should store standards anchors as adapter metadata: GS1 identifiers, OAuth/OIDC client/provider references, payment-data boundaries, source systems, schema versions, and downstream vocabulary aliases.
- AtlasView should not turn GTIN, GLN, EPCIS, PCI, or EMV terminology into first-class navigation. These belong in subscriber configuration, connector diagnostics, governance evidence, and domain adapters.

## Marketplace-readiness checklist

Use this as a no-code PR review checklist for connector or integration screens.

- The screen explains the connector's merchant value in one factual sentence.
- The primary action is a direct get-started/connect/manage path.
- The screen shows current state: available, connected, syncing, degraded, disconnected, blocked, or unsupported.
- Required permissions are shown with read/write labels and plain-language justifications.
- Sensitive data or PII access is explicitly marked.
- Payment-data boundaries are explicit: reference-only, stores, processes, transmits, tokenized, or out-of-scope.
- Product/place/party identifiers are distinguished when relevant: SKU, GTIN/barcode, GLN/location, customer id, supplier id, external record id.
- Prerequisites are visible before the merchant starts the flow.
- Region, device, location, sales channel, or business-type limitations are visible.
- Support path is visible: contact, docs, or owner.
- Pricing, plan requirement, or "no additional cost" is visible when relevant.
- Transaction, tender, tax, discount, return, register, operator, and KPI labels match retail-standard meaning when those concepts appear.
- AI/agent-driven connector behavior identifies owner, purpose, risk controls, and review path when it can affect customers, employees, security, fraud, pricing, or inventory decisions.
- The screen has an empty/error/degraded state that tells the merchant what to do next.
- A first-time merchant can understand the copy without knowing Canary internals.
- The integration vocabulary maps cleanly to Canary domain vocabulary in the view model.
- The same screen could be represented as a marketplace listing with no missing factual fields.

## Recommendation appendix

### Top 10 patterns to adopt

| Pattern | Recommendation map |
|---|---|
| Plain functional tagline/summary | UX validation checklist item |
| Direct get-started/connect/manage action | Component candidate: `integration-card` |
| Permission scopes with justifications | Component candidate: `permission-scope-list` |
| Compatibility disclosures | Component candidate: `compatibility-list` |
| Installed connector health | Component candidate: `connector-status` |
| Search/filter/sort above operational tables | Component candidate: `filter-bar` |
| Guided setup progress | Component candidate: `stepper` |
| Import/review-before-commit | Component candidate: `review-table` |
| Timeline/history for operational proof | Component candidate: `timeline` |
| Retail-standard vocabulary translation for Product/Item, Order/Transaction, Tender/Payment, Supplier/Vendor | Vocabulary decision |
| Identifier and source-system disclosure | Component candidates: `identifier-disclosure`, `source-system-chip` |
| Payment-data boundary disclosure | Component candidate: `data-boundary-panel` |

### Top 10 patterns to avoid or defer

| Pattern | Reason | Recommendation map |
|---|---|---|
| React migration for parity with commerce examples | Runtime mismatch for Canary SSR workflows | Deliberate do not adopt |
| Copying third-party component code into Go templates | License/fit risk; contracts matter more | Deliberate do not adopt |
| Storefront-first hero/marketing layouts in operator UI | Canary is an execution surface | Deliberate do not adopt |
| One mega `data-table` component with all behavior | Variation will balloon params | Component decision |
| Vendor-specific nouns in shared components | Makes future connectors harder and weakens retail-standard clarity | Vocabulary decision |
| Opaque connector pricing/requirements | Marketplace sources discourage obscurity | UX checklist |
| Hidden permissions until OAuth redirect | Reduces trust and reviewability | UX checklist |
| Visual-only status indicators | Accessibility and operations risk | Component contract rule |
| Building all component candidates immediately | Design-system sprawl | Deliberate defer |
| Treating AtlasView as required for Canary operations | Violates local operator surface principle | AtlasView compatibility note |
| Exposing raw standards acronyms as normal merchant navigation | Standards should inform contracts and diagnostics, not clutter daily work | Deliberate do not adopt |

### Top 10 durable source links

1. <https://machalliance.org/mach-principles>
2. <https://machalliance.org/insights-hub/the-mach-alliance-open-data-model>
3. <https://developer.squareup.com/docs/app-marketplace/listing-best-practices>
4. <https://developer.squareup.com/docs/oauth-api/square-permissions>
5. <https://docs.clover.com/dev/docs/navigating-the-clover-app-market>
6. <https://docs.clover.com/dev/docs/managing-app-details>
7. <https://docs.clover.com/dev/docs/permissions>
8. <https://shopify.dev/docs/apps/design>
9. <https://www.omg.org/retail/operational-data-model.htm>
10. <https://nrf.com/research/principles-use-artificial-intelligence-retail-sector>
11. <https://www.gs1.org/standards>
12. <https://www.gs1.org/standards/epcis>
13. <https://www.pcisecuritystandards.org/standards/>
14. <https://www.emvco.com/about-us/overview-of-emvco/>

## Decision prompts

1. Should Canary standardize on `Item` as the primary operator noun while documenting `Product` as the ecommerce connector alias?
2. Should connector screens use `Integration` in merchant copy and `Connector` in technical/component contracts?
3. Which status taxonomy should `status-pill`, `connector-status`, and future AtlasView status components share?
4. Should every connector require a marketplace-style metadata view model even when rendered only inside Canary?
5. Should permission scopes be stored as first-class view-model data for all integrations, not just OAuth connectors?
6. Should AtlasView manifest freshness be exposed in M1-era Canary admin UI, or deferred until AtlasView publishing is active?
7. Should `filter-bar` be generic across lists, or should high-variance reports keep page-specific filters?
8. Should `review-table` be a standalone component or a documented pattern built from `data-table`, `callout`, and `action-bar`?
9. Should Canary maintain a generated component catalog page for visual QA and AtlasView parity review?
10. Which source family wins when terms conflict: POS marketplace, MACH ODM, open-source commerce admin, or internal Canary retail language?
11. Should Canary adopt ARTS terms such as `Retail Transaction`, `Tender`, `Line Item`, and `Workstation/Register` in docs while keeping shorter merchant-facing labels in UI?
12. Should AI/agent connector screens require a governance panel before M1 ships any AI-assisted retail decisioning?
13. Should Canary introduce a visible identifier convention now for SKU, GTIN/barcode, GLN/location, supplier item code, and external record id?
14. Should connector screens classify data boundaries using `reference-only`, `stores`, `processes`, `transmits`, `tokenized`, and `out-of-scope`?
15. Should AtlasView own standards-anchor metadata centrally, or should each downstream app own its own standards mappings?

## Risks and open questions

- MACH ODM is valuable but still evolving. Treat it as a reference and translation guide, not a binding schema.
- Polaris React is now marked deprecated in favor of newer Shopify web component direction; use Shopify app design guidance more than old React component APIs.
- Vendure's GPL/commercial posture means it is safe for conceptual research, but not a copy source.
- Composable UI is storefront-oriented; it helps with component contracts and composable architecture but should not dominate Canary operator UI.
- Marketplace docs optimize public app acquisition; Canary must adapt those patterns for installed operational trust, not marketing conversion.
- Some external terms conflict. `Product`, `Item`, `Order`, `Transaction`, `Vendor`, and `Supplier` need explicit decisions before broad refactors.
- ARTS terminology is comprehensive and sometimes heavier than merchant UI should expose directly. Use it to define contracts and reporting semantics, then choose concise labels for screens.
- NRF AI guidance is governance-oriented, not component-specific. Its value is in validation checks for AI/agent features, partner accountability, workforce impact, and customer trust.
- GS1, EMVCo, PCI, OAGi, ISO, UN/CEFACT, W3C, IETF, and OpenID sources are mostly contract and trust-boundary references. They should rarely add visible merchant UI nouns unless the screen is about identifiers, payment/data handling, identity, compliance, or troubleshooting.
- Accessibility obligations in current Canary component headers are lighter than Radix-style contracts. This should be improved incrementally, starting with drawer/dialog-like patterns.

## Acceptance criteria trace

| Criterion | Status |
|---|---|
| At least 20 sources reviewed | Met: 70 sources in ledger |
| Primary sources marked | Met: `Trust` column |
| Square included | Met: sources 10-17 |
| Clover included | Met: sources 18-23 |
| MACH included | Met: sources 1-3 |
| NRF/ARTS included | Met: sources 46-56 |
| At least three open-source commerce projects included | Met: Medusa, Saleor, Vendure, Composable UI |
| Recommendations mapped | Met: recommendation tables and component list |
| Avoids long proprietary excerpts | Met: paraphrased only |
| Distinguishes inspiration from implementation | Met: source posture, risk notes |
| No React migration required | Met: Canary implications and avoid list |
