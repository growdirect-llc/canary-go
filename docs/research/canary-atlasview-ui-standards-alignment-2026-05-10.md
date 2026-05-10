---
title: Canary + AtlasView UI Standards Alignment
date: 2026-05-10
status: research
owner: product-manager-agent
related:
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/architecture/component-led-ui-vision.md
  - docs/conventions/ui-components.md
  - /Users/gclyle/Ruptiv/spec/SDD-007-ui-framework.md
  - /Users/gclyle/Ruptiv/spec/SDD-013-downstream-substrate-contract.md
---

# Canary + AtlasView UI Standards Alignment

## Executive summary

Canary Go and AtlasView are directionally aligned with the major standards sources, but not yet explicit enough for design execution. Canary already speaks much of the retail language that matters for POS and operations: Items, Transactions, Tender, Employees, Customers, Suppliers, Locations, Returns, Receiving, Transfers, and Reports. AtlasView is not retail-specific; it is the organizational design and orchestration plane. Its core language is organization, person, role, team, accountability, policy, capability, operating mode, agent profile, notification, and command.

The main gap is the bridge between those vocabularies. Canary's merchant UI should not expose AtlasView terms such as manifest, entitlement, or agent profile unless the merchant needs them. AtlasView should stay domain-neutral, but its downstream contracts need adapter metadata so a downstream app such as Canary can preserve its own domain language. The shared contract should be a translation layer: retail-standard nouns for Canary operator work, organizational-design nouns for AtlasView authoring, and explicit mappings where a concept crosses the app boundary.

The second gap is component maturity. Canary's first five Go-template components are a good start, but marketplace, ARTS/NRF, MACH, GS1, EMVCo, PCI, OAGi, Square/Clover, W3C, OpenID, and Radix/shadcn patterns all point to the same missing primitives: `empty-state`, `filter-bar`, `action-bar`, `integration-card`, `connector-status`, `permission-scope-list`, `timeline`, `metric-tile`, `kpi-definition`, `risk-governance-panel`, `identifier-disclosure`, `data-boundary-panel`, and `source-system-chip`. AtlasView already plans many analogous React components, but the shared names and states are not yet pinned.

## Standards interpretation

| Source family | What it asks of our UI | Design rule for Canary | Design rule for AtlasView |
|---|---|---|---|
| ARTS/OMG retail standards | Preserve retail transaction, tender, line item, register/workstation, inventory, price, workforce, reporting KPI semantics. | Use retail-standard labels where they carry operational meaning; avoid genericizing POS proof. | Do not adopt retail vocabulary as core AtlasView language; support downstream domain mappings. |
| NRF retail governance | Treat AI, cybersecurity, fraud, customer trust, workforce impact, and partner accountability as retail buying criteria. | Show risk controls and owner/review path when AI or agent behavior affects retail operations. | Author domain-neutral governance, accountability, and review workflows; let downstream apps render domain-specific implications. |
| MACH ODM/composable commerce | Use translation patterns and shared reference points without forcing one vendor schema. | Map Item/Product, Transaction/Order, Tender/Payment, Supplier/Vendor deliberately in view models. | Provide mapping/translation mechanics, not retail-specific canonical data ownership. |
| Square/Clover marketplaces | Make integration UX factual, direct, transparent, supportable, and compatibility-aware. | Every connector screen shows value, permissions, state, next action, support, pricing/requirements, and compatibility. | Store generic connector metadata with downstream-specific presentation fields when needed. |
| GS1 identifiers and visibility | Product, place, party, asset, barcode/RFID, product-data sync, and supply-chain visibility events need precise identity and event semantics. | Distinguish SKU, GTIN/barcode, supplier item code, external item id, GLN/location, and source-system record ids. | Store standards anchors as adapter metadata; avoid turning GS1 acronyms into core AtlasView navigation. |
| EMVCo and PCI payment/security standards | Payment-adjacent screens must be precise about card, contactless, tokenization, secure remote commerce, PCI scope, and secure software/device boundaries. | Disclose whether Canary stores, processes, transmits, tokenizes, or only references payment data. | Author policy/data-boundary evidence and downstream permissions without making AtlasView a payment UI. |
| OAGi, ISO, and UN/CEFACT integration standards | Enterprise flows rely on stable document nouns: order, quote, invoice, shipment, payment, purchasing, compliance, and financials. | Use these standards for connector/import/export contracts and support diagnostics; keep daily operator copy concise. | Map cross-application business documents and source-system ownership in domain adapters. |
| OpenID, IETF OAuth, and WebAuthn | Identity and consent flows need clear provider/client/claim/token/session/passkey language and safe authorization behavior. | Show connected-app permissions and identity-provider configuration in plain language with technical details available. | Own identity delegation, capability, policy, and audit metadata for downstream authorization flows. |
| W3C WCAG | Accessibility is a testable contract for web app content and controls. | Component headers must include states, focus, keyboard, labels, and assistive copy. | shadcn/Radix components should preserve WCAG/Radix behavior through local wrappers and product states. |
| Shopify app design | Merchant admin surfaces should feel predictable and native to the host workflow. | Favor dense, task-first operator surfaces; avoid marketing composition. | Favor consistent app shell, navigation, notifications, command palette, and predictable action patterns. |
| Radix/shadcn | Component contracts include anatomy, accessibility, state, keyboard/focus behavior, and composition. | Extend Go-template component headers with states and accessibility obligations. | Keep shadcn/Radix-derived components locally owned and mirror contract names only where behavior truly matches Canary. |

## Alignment matrix

| Area | Canary Go today | AtlasView today | External standard pressure | Alignment | Recommended action |
|---|---|---|---|---|---|
| Retail transaction vocabulary | Uses `Transactions`, `Tender`, proof, tax, payments, returns, and transaction detail screens. | Domain-neutral; owns audit, policy, role, and manifest vocabulary. | ARTS ODM and UnifiedPOS validate transaction/tender/register/operator language. | Partially aligned | Keep Canary POS labels; map AtlasView actor/audit concepts to Canary operator/register/tender only in the Canary adapter. |
| Product/item vocabulary | Canary uses `Items` in sidebar, list, detail, and item setup. | Domain-neutral; should not make product/item a core organizational noun. | MACH ODM and ecommerce platforms center `Product`; POS surfaces often use item/catalog item. | Aligned with caveat | Use `Item` in Canary UI; use `Product` as ecommerce connector alias; keep AtlasView generic with downstream field aliases. |
| Supplier/vendor vocabulary | Canary has `Suppliers`, but receiving/returns/scorecard still show `Vendor`. | May model external partners/vendors generically when relevant to org design or risk. | Retail and commerce sources use both; merchant operations benefit from one visible noun. | Not aligned | Standardize Canary merchant UI on `Supplier`; use `Vendor/Partner` in AtlasView only for general third-party relationships. |
| Customer/party identity | Canary uses `Customers`; some Owl/party surfaces use `party`. | AtlasView charter centers canonical Party/Person. | MACH/Saleor/Medusa use Customer; AtlasView needs Party for cross-domain identity. | Partially aligned | Canary UI should say Customer; AtlasView contracts can map Customer to Party with visible explanation only in admin/governance views. |
| Employee/operator vocabulary | Canary uses Employees; some copy mentions operator ID mapping. | AtlasView uses Person, Role, Team, actor. | ARTS/POS standards distinguish employee/operator/action provenance. | Partially aligned | Use Employee for HR/person record, Operator for POS action provenance, Actor for generic audit metadata. |
| Register/device vocabulary | Canary uses Devices and Register Device. | AtlasView does not own POS hardware. | UnifiedPOS validates POS device/workstation specificity. | Partially aligned | In Canary, use Register for POS terminal concepts and Device for broader hardware; add compatibility metadata to connector/device screens. |
| KPI/reporting vocabulary | Canary has many report metrics but no shared KPI definition surface. | AtlasView Briefs and governance templates define metrics, severity, reporting cadence. | ARTS DWM validates formal retail KPI definitions. | Not aligned | Add `kpi-definition` pattern for reports: name, formula, scope, source, freshness, owner. |
| Connector/integration UX | Canary has `connect.html`, `onboarding/connect.html`, and `ecom/sync.html`, but no reusable integration card/status/permission pattern. | Owns domain-neutral capability/policy/config authoring; may publish connector metadata as downstream config. | Square/Clover/Xero require value, direct start, support, pricing, permissions, compatibility. | Not aligned | Create connector metadata view model with generic fields plus downstream presentation aliases. |
| Product/place identifiers | Canary likely has SKUs, item ids, barcodes, locations, suppliers, and external ids, but no visible identifier convention. | Can store downstream identifier metadata without owning retail product identity. | GS1 validates GTIN/barcode, GLN, product master data, and EPCIS visibility event semantics. | Not aligned | Define identifier display rules: merchant label first, standards/external ids expandable for troubleshooting/compliance. |
| Payment-data boundary | Canary has payment/tender/proof language, but not a reusable disclosure for what data Canary touches. | Can author generic data-boundary and policy evidence for downstream use. | EMVCo and PCI SSC validate precise payment/security boundaries. | Not aligned | Add a `data-boundary-panel` contract before payment-adjacent integrations broaden. |
| Permissions/capabilities | Canary has auth/permission pages and 403 copy; integration permissions are not consistently rendered. | AtlasView has capability matrix, tool entitlements, policies, and operating modes. | Square/Clover permission docs require read/write scope, minimum necessary, and justification. | Partially aligned | Shared contract: AtlasView Capability -> Canary Permission label + read/write/sensitive/justification. |
| Identity and authorization flows | Canary and old/source app patterns include login/org switching, but standards-facing copy is not pinned. | AtlasView owns identity delegation and org/person context. | OpenID/OAuth/WebAuthn validate provider, client, claims, token, session, and authenticator semantics. | Partially aligned | Use merchant-friendly identity labels, with technical OIDC/OAuth/WebAuthn detail only in admin/support surfaces. |
| Status semantics | Canary uses `status-pill` plus many inline `.badge` variants with custom colors and inconsistent labels. | AtlasView specs include notification lifecycle, severity scales, and status concepts. | UI systems require semantic state, accessible labels, and consistent tone. | Not aligned | Define firm-wide status taxonomy: lifecycle, health, severity, permission, freshness, proof. Refactor badges into `status-pill` over time. |
| Empty states | Canary has many hand-written empty states. | AtlasView SDD expects app-shell/page conventions but not shared empty-state copy. | Marketplace and admin systems emphasize next action and clarity. | Not aligned | Add `empty-state` with `title`, `body`, `action`, `tone`, and optional requirement/support hints. |
| Tables/filters/actions | Canary has many bespoke tables, filters, action links, and inline styles. | AtlasView will use shadcn/TanStack patterns for rich tables. | shadcn warns against one universal data-table; admin patterns require filters/actions/bulk controls. | Partially aligned | Keep simple `data-table`; add adjacent `filter-bar`, `action-bar`, and `review-table` patterns. |
| Timeline/audit proof | Canary has timeline/proof screens for alerts, cases, transactions, evidence. | AtlasView owns notifications, audit, manifest state, and governance events. | Retail standards and security posture require durable provenance. | Partially aligned | Add shared `timeline` event shape: actor/operator, source, event type, occurred at, evidence/proof link. |
| Manifest/local-view state | Canary architecture mentions local view; no obvious UI primitive yet. | SDD-013 defines fresh/stale-soft/stale-hard/unavailable. | Operational-independence contract requires visible degraded states. | Not aligned | Add `manifest-state-panel` or admin status surface before AtlasView-published config becomes active. |
| AI/agent governance | Canary has no explicit AI governance UI yet. | AtlasView owns organization-level AI/agent governance, risk, tool entitlements, agent profiles, and operating modes. | NRF AI principles push governance, trust, workforce, and partner accountability in retail deployments. | Partially aligned | AtlasView authors generic governance; Canary renders retail-specific disclosures only when an AI/agent behavior affects retail operations. |
| Accessibility contracts | Canary component headers document params/slots, but only `drawer` names ARIA behavior in depth. | AtlasView relies on Radix/shadcn accessibility primitives. | Radix emphasizes roles, focus, keyboard, ARIA, and screen-reader announcements. | Partially aligned | Add `States` and `Accessibility` sections to every Canary component header as components evolve. |

## Canary Go design implications

Canary is the retail execution surface. It should sound like a store operator's tool, not a generic admin SaaS. The UI should keep the current POS and retail vocabulary where it is already strong, then close the drift:

- Standardize visible merchant copy on `Supplier`; treat `Vendor` as an import/connector alias unless the screen is specifically third-party-risk governance.
- Keep `Tender` on POS/payment proof and payment reports; use `Payment` only for broad, non-POS merchant copy.
- Use `Operator` only for action provenance. Keep `Employee` for people records and labor reports.
- Use `Register` for POS terminal/workstation concepts; use `Device` for broader hardware or network integrity.
- Treat report numbers as KPIs only when their definition, formula, source, date/location scope, and freshness are known.
- Distinguish identifiers: SKU is not GTIN/barcode, GLN is not a store name, external record id is not the merchant-facing label, and source-system id is not proof by itself.
- Be explicit about payment scope: payment reference, token, tender label, authorization, capture, refund, and cardholder data boundary are different concepts.
- Do not expose `Manifest`, `Capability`, `Agent Profile`, or `Operating Mode` in merchant copy unless the page is explicitly administrative.

Canary's highest-priority UI improvements are:

| Priority | Improvement | Why | First surfaces |
|---:|---|---|---|
| 1 | Status taxonomy and badge consolidation | Inline badges are already drifting across reports, tasks, proof, devices, and settings. | `tasks.html`, `transaction_detail.html`, `settings/devices.html`, reports |
| 2 | Connector metadata view model | Marketplace-grade connector UX cannot be reliably rendered from ad hoc template data. | `connect.html`, `onboarding/connect.html`, `ecom/sync.html` |
| 3 | `empty-state` component | Empty states are repeated and often lack a next action or prerequisite. | list/report/onboarding templates |
| 4 | `filter-bar` + `action-bar` | Search/filter/action patterns are bespoke across lists and reports. | `items/list.html`, `transactions.html`, `po/list.html`, `exceptions/list.html` |
| 5 | `permission-scope-list` | Permissions must be reviewable before OAuth/connect. | connector setup screens |
| 6 | `kpi-definition` pattern | Retail reporting credibility depends on defined KPIs, not just visible numbers. | reports and dashboards |
| 7 | `timeline` pattern | Cases, transaction proof, alerts, and audit should share event semantics. | cases, transaction proof, audit |
| 8 | `manifest-state-panel` | Canary must expose AtlasView config freshness once manifest consumption is active. | admin/config or identity/settings |
| 9 | `identifier-disclosure` + `source-system-chip` | GS1/OAGi-style interoperability needs identifiers and source systems without polluting everyday merchant copy. | item detail, imports, connectors, reports |
| 10 | `data-boundary-panel` | Payment/security trust depends on clear store/process/transmit/reference boundaries. | payment-adjacent connector setup |

## AtlasView design implications

AtlasView is the organizational design and orchestration plane. Its UI should not imitate Canary's dense merchant surface, and it should not become retail-specific. It should provide general primitives for organization design, accountability, policy, capability, operating modes, agent profiles, notifications, and downstream configuration. Retail context belongs in the Canary adapter and in downstream-specific aliases, not in AtlasView's core ontology.

AtlasView should own:

- Mapping infrastructure between downstream app concepts and platform governance concepts.
- Standards-anchor metadata for downstream adapters: GS1 identifiers, EPCIS event hints, OAGi document types, OAuth/OIDC client/provider references, payment-data boundaries, source systems, schema versions, and compatibility notes.
- Rich authoring for capabilities, policies, operating modes, agent profiles, and connector metadata.
- Notification, command palette, agent chat, and review workflows.
- AI/agent governance: owner, purpose, risk class, affected population or workflow, partner accountability, review cadence, incident path.
- Multi-app component vocabulary and tokens.

AtlasView should avoid:

- Forcing downstream concepts into a single generic label when publishing to domain-specific apps.
- Treating an agent profile as a merchant-facing feature. Canary should receive the effective state and explanation, not the authoring machinery.
- Creating connector metadata that only makes sense inside AtlasView. Downstream apps need plain factual copy, scope justifications, support, compatibility, and direct next action in their own domain language.
- Promoting downstream standards acronyms such as GTIN, GLN, EPCIS, PCI, EMV, or OAGIS into top-level product grammar unless a user is configuring or auditing that standard.

AtlasView's highest-priority UI improvements are:

| Priority | Improvement | Why | First surfaces |
|---:|---|---|---|
| 1 | Downstream vocabulary map editor/view | Lets AtlasView remain domain-neutral while each subscriber preserves its own language. | Manifest/config admin |
| 2 | Capability-to-permission disclosure mapping | Lets Canary render merchant-readable permissions from AtlasView-authored capabilities. | Capability matrix / connector settings |
| 3 | Connector metadata authoring | Enables downstream apps to render connector cards without hardcoding copy. | Connector management |
| 4 | Manifest publication status view | Authors need to see which downstreams are fresh, stale-soft, stale-hard, or unavailable. | Downstream management |
| 5 | AI/agent governance panel | NRF-style accountability belongs in AtlasView authoring before Canary renders simplified controls. | Agent profile / operating mode pages |
| 6 | Shared component contract catalog | Ensures Canary Go components and AtlasView React components share names only when contracts match. | Design system docs |
| 7 | Standards anchor registry | Keeps AtlasView domain-neutral while allowing downstream apps to preserve standards mappings. | Domain adapters / downstream subscriber pages |

## Shared vocabulary contract

| Cross-app concept | Canary label | AtlasView label | External anchor | Decision |
|---|---|---|---|---|
| Sellable catalog unit | Item | Downstream object alias, not core AtlasView ontology | MACH Product, POS item/catalog item | Use `Item` in Canary; AtlasView may store alias metadata for this subscriber. |
| Customer identity | Customer | Party, Person where human actor | MACH Customer, AtlasView Party | Use `Customer` in Canary; map to Party in AtlasView contracts. |
| Staff identity | Employee | Person, Role assignment | ARTS employee/operator, AtlasView Person | Use `Employee` for records; `Operator` for POS action provenance. |
| POS action record | Transaction | Downstream event/audit target | ARTS retail transaction | Use `Transaction` in Canary; AtlasView treats it as a subscriber-defined event type. |
| Payment method in POS proof | Tender | Downstream field alias | ARTS tender, Square Payments | Use `Tender` for POS proof; AtlasView should not canonicalize this outside the Canary adapter. |
| Supplier relationship | Supplier | External party/partner where organization design needs it | Retail supplier/vendor | Use `Supplier` in Canary; use Partner/Vendor in AtlasView only for general external relationships. |
| POS terminal | Register | Downstream device alias | UnifiedPOS workstation/device | Use `Register` for POS terminals; AtlasView treats hardware labels as subscriber-specific. |
| Product barcode | GTIN/barcode | Downstream identifier | GS1 GTIN, EAN/UPC | Keep distinct from SKU and item id; expose in diagnostics/imports/details where helpful. |
| Location identifier | GLN/external location id | Downstream identifier | GS1 GLN | Use for integrations and compliance; do not replace merchant-facing Location/Store name. |
| Supply-chain visibility event | Inventory/receiving/transfer event | Downstream event metadata | GS1 EPCIS/CBV | Use event-shape semantics for traceability timelines, not daily navigation. |
| Payment data handling | Payment/tender boundary | Data-boundary policy | EMVCo, PCI SSC | Disclose reference/store/process/transmit/tokenization posture when relevant. |
| Connected app identity | Connected integration | OAuth/OIDC client/provider metadata | OpenID Foundation, IETF OAuth | Use plain language first; technical claims/tokens/client ids in admin support surfaces. |
| Permission shown to merchant | Permission | Capability/Entitlement/Policy | Square/Clover scopes | AtlasView authors generic capabilities; Canary renders domain-specific permission labels with justification. |
| Published config | Published settings | Manifest | SDD-013 | Avoid `Manifest` in merchant copy; use it in admin/engineering UI. |
| Config freshness | Configuration status | Local-view/manifest state | SDD-013 | Shared states: fresh, stale-soft, stale-hard, unavailable. |
| Health/status badge | Status | Status/severity/lifecycle | Radix/shadcn semantics | Define status families before adding more tones. |

## Shared component contracts

These names should be shared between apps only at the contract level. Implementation can differ: Go templates in Canary, React/shadcn/Radix in AtlasView.

| Contract | Canary implementation | AtlasView implementation | Shared states/fields |
|---|---|---|---|
| `status-pill` | Existing Go template, needs taxonomy | Badge/Status component | `label`, `family`, `tone`, `assistiveLabel` |
| `empty-state` | New Go template | React component | `title`, `body`, `action`, `tone`, `requirement`, `supportHref` |
| `filter-bar` | New Go template around server query params | React component with URL state | `search`, `filters`, `sort`, `activeCount`, `clearHref` |
| `action-bar` | New Go template | React toolbar | `primary`, `secondary`, `bulk`, `selectedCount`, `disabledReason` |
| `integration-card` | New Go template | Connector card | `name`, `summary`, `status`, `category`, `action`, `support`, `compatibility` |
| `connector-status` | New Go template | Connector health panel | `health`, `lastSync`, `nextSync`, `issues`, `supportHref` |
| `permission-scope-list` | New Go template | Capability disclosure component | `scope`, `readWrite`, `sensitive`, `justification`, `required` |
| `timeline` | New Go template | Event timeline | `events[]` with actor/operator, source, type, occurredAt, evidenceHref |
| `kpi-definition` | New report helper/component | Metric definition panel | `name`, `formula`, `scope`, `source`, `freshness`, `owner` |
| `manifest-state-panel` | New admin component | Downstream status component | `state`, `etag`, `lastRefresh`, `publisher`, `blockedActions` |
| `risk-governance-panel` | Rendered only when needed | Authoring source of truth | `riskArea`, `owner`, `controls`, `reviewCadence`, `lastReviewed` |
| `identifier-disclosure` | New diagnostics/detail component | Standards anchor panel | `label`, `identifierType`, `value`, `source`, `copyable`, `confidence` |
| `data-boundary-panel` | New connector/security component | Policy evidence component | `dataType`, `boundary`, `processor`, `retention`, `tokenized`, `outOfScopeReason` |
| `source-system-chip` | New inline metadata chip | Source-system indicator | `system`, `recordId`, `freshness`, `authority`, `lastSeenAt` |

## Standards Gaps

| Gap | Severity | Why it matters | Recommended fix |
|---|---:|---|---|
| No written status taxonomy across apps | High | Status colors and labels already drift; AtlasView has severity and notification states that could diverge further. | Add `docs/decisions/ui-status-taxonomy.md` or a section in `component-led-ui-vision.md`. |
| Supplier/vendor terminology drift | Medium | Merchant screens mix Supplier and Vendor, weakening retail clarity. | Rename visible Canary copy to Supplier where it is not third-party-risk governance. |
| Connector metadata is not a first-class contract | High | Marketplace-grade integration UX needs repeatable fields and review gates. | Define `ConnectorMetadata` view model and AtlasView authoring counterpart. |
| Permission disclosure lacks shared shape | High | Square/Clover-style permissions require read/write/sensitive/justification before connect. | Define capability-to-permission mapping and render `permission-scope-list`. |
| KPIs lack definitions | Medium | ARTS DWM pressure says retail metrics should be defined and comparable. | Add KPI definition metadata to reports, at least in docs first. |
| Manifest state lacks UI | Medium now, High once active | SDD-013 requires local-view freshness to be visible and operable. | Add `manifest-state-panel` before active AtlasView publishing. |
| AI/agent governance has no Canary-facing pattern | Medium now, High for AI features | NRF governance pressure will matter once agents affect pricing, inventory, fraud, customers, or workforce. | Keep authoring in AtlasView; render simplified risk/governance disclosure in Canary only where needed. |
| Identifier semantics are not explicit | Medium | GS1/OAGi-style integration depends on stable product, place, party, asset, and document identifiers. | Add identifier display and source-system conventions before broad import/export work. |
| Payment-data scope is not visible | High for payment-adjacent flows | EMVCo/PCI posture requires care around what Canary touches versus what partners handle. | Define `data-boundary-panel` and review copy for payment/tender screens. |
| Identity-provider and connected-app terminology is not pinned | Medium | OAuth/OIDC/WebAuthn flows can confuse users if provider/client/token/session language leaks randomly. | Add identity/authorization copy rules for admin and connector screens. |
| Accessibility not uniformly part of component API | Medium | Radix/shadcn expectations include keyboard/focus/ARIA, not just markup. | Add `States` and `Accessibility` sections to Go component headers. |

## Recommended doc follow-ups

1. Update `docs/architecture/component-led-ui-vision.md` with NRF/ARTS as a sixth provenance input and with the shared vocabulary contract above.
2. Add a decision record for visible nouns: Item/Product, Supplier/Vendor, Customer/Party, Employee/Operator, Register/Device, Tender/Payment.
3. Add a decision record or convention section for status families: lifecycle, health, severity, permission, proof, freshness.
4. Extend `docs/conventions/ui-components.md` so every component header includes `States` and `Accessibility`.
5. Add a connector metadata convention covering value summary, permissions, compatibility, support, pricing/requirements, health, and next action.
6. Add a report/KPI convention for formula, scope, source, and freshness.
7. Add an identifier convention for SKU, GTIN/barcode, GLN/location, supplier item code, external record id, source-system id, and OIDC subject/client identifiers.
8. Add a payment/security data-boundary convention for reference-only, stores, processes, transmits, tokenized, sensitive, and out-of-scope states.

## Recommended engineering follow-ups

| Rank | Work item | App | Notes |
|---:|---|---|---|
| 1 | Define status taxonomy and refactor obvious inline badges to `status-pill` | Canary | Start with no visual redesign; parity first. |
| 2 | Add `empty-state` with tests | Canary | Use list/report/onboarding pages as first consumers. |
| 3 | Add connector metadata view model | Canary + AtlasView | Canary renders; AtlasView eventually authors. |
| 4 | Add `integration-card`, `connector-status`, `permission-scope-list` | Canary | Pair with connector metadata. |
| 5 | Add `filter-bar` and `action-bar` | Canary | Keep `data-table` simple. |
| 6 | Add `kpi-definition` documentation or helper | Canary | Code component can wait; docs can start now. |
| 7 | Add manifest freshness UI once manifest cache implementation begins | Canary + AtlasView | Use SDD-013 states. |
| 8 | Create shared component contract catalog | Both | Contract names, fields, states, accessibility notes. |
| 9 | Add standards anchor metadata to domain adapter/downstream docs | AtlasView | Include GS1, OAGi, OAuth/OIDC, payment-data-boundary, source-system, schema-version anchors. |
| 10 | Add identifier and data-boundary components when first pulled by screens | Canary | Start with connector/import/detail diagnostics; avoid broad visual redesign. |

## Decision prompts

1. Should the shared contract call the cross-app mechanism `downstream vocabulary aliases`, `domain adapter labels`, or something else?
2. Should AtlasView author connector metadata directly, or only publish generic capability/config metadata that Canary enriches locally?
3. Should `Supplier` replace all visible `Vendor` copy in Canary merchant screens now, while keeping database/import field names untouched until later?
4. Should Canary use `Register` as the visible POS terminal noun and reserve `Device` for generic hardware/network surfaces?
5. Should `Tender` remain visible in payment reports and transaction proof, or should broad report pages say `Payment method` with `Tender` only in detail views?
6. Should KPIs become first-class report metadata in M1, or should the first pass be documentation-only?
7. Which status families are canonical across both apps: lifecycle, health, severity, permission, proof, freshness, and sync?
8. Should Canary render AtlasView manifest state as `Published settings`, `Configuration source`, or another merchant-friendly label?
9. For AI/agent behavior, should Canary always show a governance disclosure, or only when the behavior can affect pricing, inventory, fraud, customers, employees, or write actions?
10. Should the shared component contract catalog live in Canary, AtlasView, or a neutral cross-repo spec?
11. Should AtlasView own a standards-anchor registry, or should standards mappings live inside each downstream adapter?
12. Should Canary show GS1 identifiers by default on item/location screens, or keep them collapsed behind diagnostics/support affordances?
13. Should payment-adjacent screens classify data boundaries with a fixed taxonomy before any connector-specific copy is written?
14. Which identity labels are user-facing: Identity provider, Sign-in provider, Connected app, Client, Session, Token, Claim?

## PR review checklist additions

- Does this screen use the agreed noun for the domain: Item, Supplier, Customer, Employee, Operator, Register, Tender, Transaction?
- If this is a connector screen, does it show value, state, permissions, compatibility, support, pricing/requirements, and a direct next action?
- If this displays a permission/capability, does it show read/write direction, data category, sensitivity, and justification?
- If this displays identifiers, does it distinguish merchant label, SKU, GTIN/barcode, GLN/location, external id, and source-system record id?
- If this touches payment-adjacent data, does it state the data boundary instead of implying Canary handles card data directly?
- If this touches identity/auth, does it use consistent provider/client/session/permission language and hide token/claim detail unless needed?
- If this displays a KPI, is the formula/scope/source/freshness documented or linked?
- If this displays a status, does it use an approved status family and tone?
- If this is a component change, does the component header document states and accessibility obligations?
- If this depends on AtlasView-published config, does the UI behave correctly for fresh, stale-soft, stale-hard, and unavailable?
- If AI/agent behavior affects retail operations, does the UI identify owner, purpose, controls, and review path?

## Durable source anchors

- ARTS Operational Data Model: <https://www.omg.org/retail/operational-data-model.htm>
- OMG retail standards overview: <https://www.omg.org/industries/retail.htm>
- NRF AI principles: <https://nrf.com/research/principles-use-artificial-intelligence-retail-sector>
- MACH Open Data Model: <https://machalliance.org/insights-hub/the-mach-alliance-open-data-model>
- Square listing details: <https://developer.squareup.com/docs/app-marketplace/listing-best-practices/details>
- Square OAuth permissions: <https://developer.squareup.com/docs/oauth-api/square-permissions>
- Clover permissions: <https://docs.clover.com/dev/docs/permissions>
- Radix accessibility: <https://www.radix-ui.com/primitives/docs/overview/accessibility>
- shadcn/ui introduction: <https://ui.shadcn.com/docs>
- Shopify app design guidelines: <https://shopify.dev/docs/apps/design>
- GS1 standards: <https://www.gs1.org/standards>
- GS1 EPCIS/CBV: <https://www.gs1.org/standards/epcis>
- PCI SSC standards: <https://www.pcisecuritystandards.org/standards/>
- EMVCo overview: <https://www.emvco.com/about-us/overview-of-emvco/>
- OpenID Connect overview: <https://www.openid.net/developers/how-connect-works/>
- W3C WCAG overview: <https://www.w3.org/WAI/standards-guidelines/wcag/>
