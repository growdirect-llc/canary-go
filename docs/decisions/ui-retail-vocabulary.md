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
| Source-system record | Source record | External id, remote id, system record | Standards anchor metadata | Use Source record when showing the authoritative external record behind a mirrored Canary object. |
| Retail metric | KPI | Metric, measure, performance indicator | Report definition metadata | Use KPI only when formula, scope, source, and freshness are known or linked. |
| Permission shown to merchant | Permission | Scope, grant | Capability, entitlement, policy | Canary renders merchant-readable Permission labels; AtlasView may author Capability/Policy. |
| Authorization provider | Identity provider | OIDC provider, sign-in provider, authorization server | Identity delegation metadata | Use Identity provider in admin copy; keep OIDC/OAuth terms for setup, support, or diagnostics. |
| Connected app authorization | Connected integration | OAuth client, app grant, relying party | Capability/policy mapping | Use Connected integration for merchant-facing consent and Connected app only when the source system uses that noun. |
| Published platform config | Published settings | Manifest, local view | Manifest, local-view state | Avoid Manifest in normal merchant copy; use Published settings or Configuration status. |

## Rationale

NRF/ARTS retail standards, POS marketplace patterns, and open commerce platforms all point toward plain retail vocabulary. Canary is the merchant execution surface, so it should sound like store operations. AtlasView is the management and orchestration plane, so it can keep broader organizational terms and publish mappings to Canary.

## Use in Canary

- New merchant screens must use the Canary user-facing noun from the table unless a product decision says otherwise.
- Connector screens may show aliases only when they help explain a source system.
- Technical identifiers such as GTIN, GLN, OIDC client id, source-system id, and manifest id should be collapsed behind detail/diagnostic affordances unless the screen is for setup, support, compliance, or troubleshooting.
- KPI labels must not imply standards-grade comparability unless the definition, source, freshness, and location/date scope are available.
- Identity and authorization screens should use plain merchant language first, then expose provider/client/scope/token detail only when it helps configuration or support.

## AtlasView mapping

AtlasView may author capabilities, policies, manifests, people, parties, operating modes, and connector metadata. Canary renders the effective downstream language from that platform substrate. The shared contract is the mapping, not a forced global noun.

## Review triggers

- A new UI introduces Product, Vendor, Party, Workstation, Device, Payment, Order, Capability, Entitlement, or Manifest as visible merchant copy.
- A connector maps external fields into Canary without preserving source-system aliases.
- A report or proof screen uses a term differently than POS or retail standards would.
- A screen exposes OAuth/OIDC, GTIN, GLN, EPCIS, PCI, EMV, or manifest terminology as ordinary navigation or marketing copy instead of setup, diagnostics, or compliance detail.
