# Canary Go — Canonical Data Model (SDD)

**Status**: DRAFT v1 (2026-05-01)
**Branch**: `gd-canonical-data-model`
**Supersedes**: `docs/sdds/go-handoff/data-model.md` (v0 — preserved at git tag `data-model-v0` for reference)
**Companion**: `docs/sdds/go-handoff/mcp-service-junctions.md` (SLA inventory for L3 bus junctions)

## Abstract

The canonical data model for Canary Go's retail spine. **65 designed entities** across **11 schemas**, **ARTS-anchored** (Association for Retail Technology Standards) for industry alignment, with **TOM operational lifecycle** (Tesco Operating Model interface specs, 2007) bound to each entity for the temporal/SLA dimension that ARTS structural model lacks.

**Scope**: SMB-2030 retail tenants (1-100 stores, modern tech default — Postgres + JSONB + pgvector, agent-driven workflows, MCP service-per-junction architecture).

**Compression**: ~250 source entities (GSLM 102 + CRDM 25 + Canary current 116 + recovery DDL 20) folded to **~77 canonical** (65 newly designed + ~12 preserved from current Canary spec) via:
- ARTS-anchored decomposition (industry standard, no over-decomposition)
- JSONB pragmatism for variants/extensions
- Discriminator columns for type variants
- Recursive tables with ltree for hierarchies
- COALESCE-PK pattern for nullable-FK uniqueness
- EXCLUDE constraints for temporal and exclusivity rules
- Generated columns for derived values

**Module spine coverage**: 11 of 13 modules covered (M, A, S, C, L, D, O, P, F, T, Q). E (Execution / Workflow) and N (Device) deferred — both genuinely greenfield with no source material.

## Document structure

1. **§1 Sources & Provenance** — what we drew from (inventory)
2. **§2 Design Principles** — the 13 design rules every entity follows
3. **§3-§10 Entity Walks** — ARTS-anchored canonical per domain, with TOM operational lifecycle
   - §3 Item (m schema)
   - §4 Location + Space (l, s schemas)
   - §5 Customer + Employee (c, e schemas)
   - §6 Inventory + Distribution (i schema)
   - §7 Orders (o schema)
   - §8 Pricing + Financial (p, f schemas)
   - §9 POSLog + Sales Audit (t schema)
   - §10 Canary Platform Mechanics (q, ledger, app, memory schemas)
4. **§11 Module Ownership Matrix** — which module owns each entity
5. **Companion document** — MCP Service Junction Inventory (separate SDD)

## How to read this

- **Implementers**: Start with §2 (design principles), then jump to your domain section. Each entity has full DDL + operational lifecycle + provenance.
- **Reviewers**: Read §1 (sources) and §11 (module matrix) for the bird's-eye view; deep-dive any concerning entity from there.
- **Auditors**: §1 + provenance trails per entity give the full lineage of every design choice.
- **Agents**: All entities follow the conventions in §2 (UUID PKs, tenant_id everywhere, JSONB rules, naming).

---


# §1 Sources & Provenance

**Working file. Chunk 1 output. Not for promotion.** Branch: `gd-canonical-data-model`.

Catalogues every entity-mention across every source. Anchors the entity-by-entity reconciliation walk that follows. Built per the founder's directive: *"go table by table by entity in the GSLM and produce the canonical."*

## Sources

| # | Source | Path | Era | Layer | Entities |
|---|---|---|---|---|---|
| **S0** | **GSLM MDM Site** ⭐ (HTML domain documentation, 9-domain canonical) | `~/CRDM-recovery/gslm-mdm-site/` (converted from `Brain/raw/inbox/DollarDollar/GSLM/GSLM WEB Content/`) | 2009-12 to 2010-01, Walmart Int'l GSLM team | **Master data — full canonical reference** | **~102** (across 9 domains) |
| S1 | GSLM SQL DDL (partial impl) | `~/CRDM-recovery/sql/Logical-Model-2009-12-14.sql` | 2009-12 | Master data — implementation subset | 43 |
| S2 | **CRDM POS 1.8 Data Dictionary** | `~/CRDM-recovery/data-dictionaries/CRDM-1.8-Data-Dictionary.md` | ~2013, Secure Store 3.2 | POS operational | 25 |
| S3 | CRDM POS 1.7.2 Data Dictionary | `~/CRDM-recovery/data-dictionaries/CRDM-1.7.2-Data-Dictionary.md` | ~2012 | POS operational | 27 |
| S4 | **Canary Go data-model.md** (active) | `docs/sdds/go-handoff/data-model.md` | 2026, GA-track | Canary platform + commercial | ~116 (65 detailed + 51 listed) |
| S5 | Canary Python data-model.md (frozen) | `docs/sdds/canary/data-model.md` | 2025, v0-python-prototype | Earlier Canary draft | ~95 |
| S6 | Recovery DDL fragments | `~/CRDM-recovery/sql/*.sql` (excl. Logical-Model) | 2010-2015 | Misc operational + reference | ~20 |
| S7 | GSLM Entity Descriptions (Word) | `~/CRDM-recovery/gslm-mdm-site/GSLM-Entity-Descriptions.md` | 2009-12 | Merchandise hierarchy reference | ~25 (subset of S0) |
| S8 | GSLM per-domain narrative overviews (.doc) | `~/CRDM-recovery/gslm-mdm-site/GSLM*Overview.txt` (11 files) | 2009-2010 | Domain rationale / context | n/a (prose) |
| **S9** | **TOM Interface Design Documents** ⭐ (Tesco Operating Model integration program) | `Brain/raw/inbox/Interface Design Documents/` | 2007, Project BEN | **Operational clock — when entities are produced/updated/consumed, by which junctions** | **82 interface specs** (154 .doc + 69 .vsd + 129 .xls) |
| **S10** | **ARTS standards** ⭐⭐ (Association for Retail Technology Standards) | local PDFs in `Brain/raw/inbox/DollarDollar/Downloads/retail-17-07-09/` and `retail-17-07-11/` + public ARTS ODM knowledge | 2005-2017 ARTS publications | **Industry-standard structural anchor for ALL retail entities** | Full ODM (~150-200 entities); we'll implement SMB-2030 subset |

**Authority order for canonical reconciliation (REVISED again — S9 added):**
1. **GSLM MDM Site (S0)** ⭐ — full canonical structure; the abstract anchor
2. **TOM Interface Design Documents (S9)** ⭐ — operational reality / field-level data exchange between real systems; the concrete reinforcement
3. **GSLM Entity Descriptions (S7)** — entity-by-entity attribute reference (subset of S0)
4. **GSLM SQL DDL (S1)** — physical implementation reference (for type choices, constraint patterns)
5. **GSLM narrative overviews (S8)** — domain rationale, context for design decisions
6. **Canary Go data-model (S4)** — active platform spec, supersedes Python proto
7. **CRDM 1.8 (S2)** — POS operational layer; preferred over 1.7.2 where they overlap
8. **CRDM 1.7.2 (S3)** — only consulted for entities dropped in 1.8 (Customer, Repair) or to surface evolution deltas
9. **Python proto (S5)** — semantic reference only; no DDL pulls
10. **Recovery DDL fragments (S6)** — pulled selectively for specific reference tables and platform precursors

**S0 vs S9 relationship:** GSLM (S0) is the abstract canonical model — what entities SHOULD exist and how they SHOULD relate. TOM Interface Design Documents (S9) are the concrete operational specs — what FIELDS actually moved between real production systems (RMS, GFO, Storeline, ORMS, RWMS, TIMS, etc.) at Tesco circa 2007. S0 tells us what the entity is; S9 tells us what its real-world payload looks like, in COBOL flat-file precision (PIC clauses, byte positions). Use them together: S0 for entity definition, S9 for field-level grounding and missing operational entities (Orders, Distribution movements, Finance flows).

## S0 — GSLM MDM Site entities (~102 across 9 domains, ANCHOR)

Founder's own canonical retail data model, fully documented as a 9-domain web property (Jan 2010 build). Each domain has its own HTML page (now markdown), narrative .doc overview, and ERD PNG diagram. **This is the actual canonical — the SQL was a partial implementation subset.**

### Domain entity counts (per `Entity Name` table headers)

| Domain | Entities | File | Narrative |
|---|---|---|---|
| **Item** | 22 | `Item.md` (3859 lines) | `GSLM Item Overview.txt` (33 KB) |
| **Supply** (Chain / Vendor / Inventory) | 19 | `Supply.md` (3512 lines) | `GSLM Supply Chain Overview.txt` (18 KB) |
| **Location** | 16 | `Location.md` (2083 lines) | `GSLM Location Overview.txt` (12 KB) |
| **Customer** | 14 | `Customer.md` (1956 lines) | `GSLM Customer Overview.txt` (12 KB) |
| **Finance** | 13 | `Finance.md` (2459 lines) | `GSLM Finance Overview.txt` (15 KB) |
| **Space** (Planning / Planogram) | 8 | `Space.md` (1420 lines) | `GSLM Space Planning Overview.txt` (9 KB) |
| **Price** (and Promotion) | 6 | `Price.md` (956 lines) | `GSLM Price and Promotion Overview.txt` (8 KB) |
| **People** | 4 | `People.md` (972 lines) | `GSLM People Overview.txt` (5 KB) |
| **Controls** (Parameters / interface scaffolding) | 0 (narrative only) | `Controls.md` (505 lines) | `GSLM Controls and Parameters Overview.txt` (3 KB) |
| **TOTAL** | **~102** | | |

### Why this changes everything

The Logical-Model SQL (S1, 43 tables) covered only the Item/Location/Vendor/Promotion subset that the Walmart project's first phase implemented. The MDM site (S0) is the **complete 9-domain canonical** — it includes:

- **Customer** (14 entities) — Canary currently has only `app.customers`
- **Finance** (13 entities) — Canary has `app.bank_accounts` and `ledger.*` only; no GL, AP/AR, three-way match, or tax model
- **People** (4 entities) — Canary has `app.employees` only; no labor/scheduling
- **Space** (8 entities) — GSLM SQL had only `Planogram` (1); MDM site has 8 including shelf placement
- **Supply** (19 entities — vendor + distribution + inventory) — Canary has 2 movement entities; MDM has the full chain

This means the **module gap analysis from earlier needs revision**:
- F (Finance), L (Labor), C (Customer) — **NOT greenfield** — they have full MDM-layer canonical to draw from
- D (Distribution), S (Space), P (Pricing) — gaps in Canary, **but rich source material** in GSLM MDM
- O (Orders), E (Execution) — still genuinely greenfield (no MDM source covers them)

### Per-domain entity name extraction status

The HTM-derived markdown retains MS Office HTML markup (inline styles), so the ~102 entity-name table cells require per-domain reading during the walk rather than a one-pass regex extract. Each chunk (2-7) will read its domain's full markdown and extract entities one at a time. This is fine — the canonical work is per-entity anyway.

## S1 — GSLM SQL DDL entities (43, partial implementation)

Subset of S0, with concrete SQL Server 2008 R2 DDL. Use for type/constraint reference when reconciling. Grouped by their natural place in S0's 9 domains:

| S0 Domain | S1 SQL tables (subset implemented) |
|---|---|
| Item (22 in S0) | `Merchandise`, `MerchandiseAttributesLanguages`, `MerchandiseType`, `BusinessDivisions`, `Departments`, `Classes`, `SubClasses`, `Finelines`, `Sections`, `SKUItems`, `Styles`, `StyleVariants`, `StyleVariantValues`, `StyleVariantGroups`, `StyleVariantGroupAssignments`, `ArticleItems`, `ArticleTypes`, `Ingredients`, `PackItems`, `PackItemBreakout`, `PackBreakout`, `UserDefinedAttributes`, `UserDefinedAttributeValues` (23 of S0's 22 — overlap likely from S0 not breaking out UDA from item core) |
| Location (16 in S0) | `SalesOutlets`, `SalesFloors`, `SalesFloorsInSalesOutlet`, `SalesOutletDepartments`, `SalesOutletAssets`, `SalesOutletAssetLocation`, `SalesOutletHolidays` (7 of 16) |
| Supply (19 in S0) | `Vendors`, `SKUItemVendors` (2 of 19 — minimal vendor master only) |
| Price (6 in S0) | `Promotions`, `PromotionComponents`, `PromotionComponentDetails`, `PromotionThresholds`, `ThresholdIntervals`, `Taxes`, `ItemTaxesInSalesOutlets`, `SalesOutletSKUItemPromoCompDetails`, `SKUItemsInSalesOutlets`, `PackItemsInSalesOutlets` (10 — Pricing + cross-store placement) |
| Space (8 in S0) | `Planogram` (1 of 8) |
| Customer (14 in S0) | (none implemented in SQL) |
| Finance (13 in S0) | (none — except `Taxes` listed under Price) |
| People (4 in S0) | (none implemented) |
| Controls (narrative) | (n/a) |

> **Critical observation (revised):** GSLM as documented in S0 covers all 9 domains of master data — including Customer, Finance, People — that the SQL implementation never reached. The canonical walk should anchor on S0 (full domain coverage), then use S1 where actual SQL types/constraints are needed, then add CRDM operational and Canary platform layers on top.

## S2/S3 — CRDM POS entities (25 in 1.8, 27 in 1.7.2)

All prefixed `CRDM_*`. Grouped by transaction role:

### Transaction core (3)
`CRDM_Header` · `CRDM_Item` · `CRDM_FastFact` (pre-aggregated transaction summary, ~110 columns)

### Transaction subtypes (5)
`CRDM_Tender` · `CRDM_TransactionDiscount` · `CRDM_ItemDiscount` · `CRDM_ItemFastFact` · `CRDM_RecalledTransaction`

### Money / payment subordinates (4)
`CRDM_AccountPayment` · `CRDM_GiftCard` · `CRDM_PointsCoupon` · `CRDM_PaidIn_PaidOut`

### Operational subordinates (4)
`CRDM_StaffDiscount` · `CRDM_OperatorAction` · `CRDM_AgeInformation` · `CRDM_NotOnFile`

### Inventory movement (5)
`CRDM_GoodsReceived` · `CRDM_StockAdjustment` · `CRDM_SupplierReturn` · `CRDM_Transfer` · `CRDM_WebItemReturn`

### Cash management (1)
`CRDM_CashOfficeSafe`

### Receipt + ETopUp + Loyalty (3)
`CRDM_Receipt` · `CRDM_ETopUp` · `CRDM_LoyaltyCard`

### Dropped between 1.7.2 → 1.8 (2 — only in 1.7.2)
`CRDM_Customer` · `CRDM_Repair`

> **Observation:** CRDM is operational POS event capture — every entity is rooted in a transaction (TransactionID/CheckPointID/StoreNo/POSNo/TicketNo/TradingDay/CashierNo skeleton). CRDM_Customer was likely dropped because customer master moved to a separate system; CRDM_Repair was a vertical-specific feature retired.

## S4 — Canary Go data-model.md entities (~116, by schema/domain)

### app schema (~60 tables)

| Domain | Tables |
|---|---|
| Identity (19) | organizations · merchants · merchant_settings · users · roles · user_roles · employees · locations · location_hierarchy · customers · products · square_oauth_tokens · source_systems · merchant_sources · external_identities · user_employee_links · employee_location_assignments · gift_cards · bank_accounts |
| Chirp / detection (2) | detection_rules · merchant_rule_config |
| Alert (4) | alerts · alert_history · notification_log · notification_schedule |
| Owl / observability (4) | owl_sessions · owl_findings · owl_merchant_memory · owl_action_log |
| Fox / case mgmt (7) | fox_cases · fox_case_alerts · fox_case_timeline · fox_case_actions · fox_evidence · fox_evidence_access_log · fox_subjects |
| Hawk / incident (8) | hawk_incident_types · hawk_sources · hawk_cases · hawk_subjects · hawk_actions · hawk_timeline · hawk_compliance_obligations · hawk_cards |
| Bull (Phase 3 stub) | (planned, no migration) |
| Webhook (3) | webhook_events · schema_fingerprints · schema_drift_alerts |
| UI/BFF (5) | feature_flags · merchant_feature_flags · app_config · card_profiles · blocked_entities |
| RaaS (2) | namespace_registrations · namespace_aliases |
| Vault (1) | vault_memories |
| Subscription/Transfer (2) | subscriptions · transfer_orders |
| Cross-cutting (2) | audit_log · interest_signups |

### sales schema (~30 tables)

| Domain | Tables |
|---|---|
| Transaction Core (4) | transactions · transaction_line_items · transaction_tenders · refund_links |
| Order Detail (6) | (listed, not all detailed in this version) |
| Cash Management (2) | cash_drawer_shifts · cash_drawer_events |
| Gift Card & Loyalty (3) | (listed) |
| Disputes & Payouts (3) | (listed) |
| Inventory & Labor (2) | (listed) |
| Terminal (2) | (listed) |
| Pipeline Infrastructure (3) | (listed) |
| Event Journal (5) | (listed) |

### metrics schema (~21 tables)

Fact (6) · Dimension (3) · ML Feature Store (3) · Risk Scoring (2) · Baselines & Scorecards (7)

### memory schema (2)

`memory.alx_memories` · `memory.alx_sessions`

### ledger schema (3)

`ledger.rib_batches` · `ledger.stock_ledger_entries` · `ledger.ilwac_positions`

> **Schema strategy** (per S4): schema-per-tenant for sales, single shared `app` schema for cross-tenant control plane. Multi-tenant isolation lives at the schema boundary.

## S6 — Recovery DDL fragments (selective)

| File | Tables | Why included |
|---|---|---|
| `wmt-ref-location-create.sql` | `Ref_LocationHierarchy` | Reference hierarchy for store grouping (banner/region/district) — pull during Store/Location domain |
| `CreateTablesAndProcedures.sql` | `Reference_*` (5 tables) | Reference data: AverageBrandItemRetailCost, HR, LOCATION, States, ZoneBrandMatch — pull selectively |
| `CaseCentre.sql` | `Case_Custom` | Case management precursor — already superseded by Canary Fox; consult for column ideas |
| `Video.sql` + `Video-CaseManagement.sql` | `Camera_Reference`, `Video_Queue`, `Video_Request`, `Case_Case_Case_Video` | Evidence/video chain precursor — consult during Q (LP) module Fox section |
| `RxAggTables.sql` + `RXScorecard*.sql` | `Rx*` (5 tables) | Pharmacy vertical aggregates — consult only if pharmacy vertical is in scope |
| `SAS-Scorecard*.sql` | `SASScorecardPOSAggregate` | POS aggregate — consult during metrics layer |
| `create-scorecard-base.sql` | `scorecards.Scorecard_StoreNo_CashierNo` | Cashier scorecard pattern — relevant for Q (LP) baselines |
| `3-Notifying-Table-Changes.sql` | `Notification_Checkpoint`, `Notification_SummaryCheckpoint` | Change-notification pattern — superseded by Canary webhook_events but consult |
| `CRDM-MaxID-Table-Changes.sql` | `MaximumTx` | TransactionID generator — pattern reference for sequencing |
| `Create-Dyno-Tab*.sql` | `Dyno_Tabs`, `Dyno_TabUserPreference` | UI dynamic-tab — superseded by Canary feature_flags pattern; consult if useful |

## Cross-source alias map (initial — to be expanded per chunk)

Anchors the canonical reconciliation. For each row, **canonical column convention** = lowercase snake_case, Postgres-typed, schema-qualified.

| Canonical entity | GSLM (S1) | CRDM 1.8 (S2) | Canary go-handoff (S4) | Module owner |
|---|---|---|---|---|
| `m.products` (item master) | `SKUItems` + `Styles` + `StyleVariants` | (referenced by ArticleID in `CRDM_Item`) | `app.products` | M (Merchandising) |
| `m.product_taxonomy` (department/class/etc.) | `BusinessDivisions` → `Departments` → `Classes` → `SubClasses` → `Finelines` → `Sections` | — | (not present — gap) | M |
| `m.vendors` | `Vendors` + `SKUItemVendors` | — | (not present — gap) | M |
| `m.taxes` | `Taxes` + `ItemTaxesInSalesOutlets` | (`TaxAmount` columns on Header/Item) | (not present — gap) | F or M |
| `m.promotions` | `Promotions` + `PromotionComponents` + `PromotionComponentDetails` + `PromotionThresholds` + `ThresholdIntervals` | (`PromotionID` referenced from `CRDM_ItemDiscount`/`CRDM_TransactionDiscount`) | (not present — gap) | P (Pricing) |
| `m.packs` | `PackItems` + `PackItemBreakout` + `PackBreakout` | — | (not present — gap) | M |
| `m.style_variants` | `StyleVariants` + `StyleVariantValues` + `StyleVariantGroups` + `StyleVariantGroupAssignments` | — | (subsumed in `app.products` attributes JSON?) | M |
| `m.ingredients` | `Ingredients` | — | (gap; matters for food/grocery/pharmacy verticals) | M |
| `m.user_defined_attrs` | `UserDefinedAttributes` + `UserDefinedAttributeValues` | — | (gap; partially served by `app.merchant_settings` JSON) | M |
| `a.locations` (sales outlets) | `SalesOutlets` | (referenced by `StoreNo`) | `app.locations` + `app.location_hierarchy` | A (Asset) |
| `a.location_hierarchy` | (implicit in SalesOutlets columns) | — | `app.location_hierarchy` + `Ref_LocationHierarchy` (S6) | A |
| `a.location_floors_sections` | `SalesFloors` + `SalesFloorsInSalesOutlet` + `Sections` | — | (gap) | A or S |
| `a.location_assets` | `SalesOutletAssets` + `SalesOutletAssetLocation` | — | (gap; partially `app.terminals`) | A or N |
| `a.location_holidays` | `SalesOutletHolidays` | — | (gap) | A |
| `s.planogram` | `Planogram` | — | (gap) | S (Space) |
| `s.shelf_placement` | `SKUItemsInSalesOutlets` + `PackItemsInSalesOutlets` | — | (gap) | S |
| `t.transactions` | — | `CRDM_Header` | `sales.transactions` | T (Transaction Pipeline) |
| `t.transaction_line_items` | — | `CRDM_Item` | `sales.transaction_line_items` | T |
| `t.transaction_tenders` | — | `CRDM_Tender` | `sales.transaction_tenders` | T |
| `t.transaction_discounts` | — | `CRDM_TransactionDiscount` + `CRDM_ItemDiscount` | (in line items?) | T |
| `t.transaction_summary` (FastFact) | — | `CRDM_FastFact` (~110 cols) + `CRDM_ItemFastFact` | `metrics.fact_*` (similar role) | T → metrics |
| `t.gift_card_events` | — | `CRDM_GiftCard` + `CRDM_PointsCoupon` | `app.gift_cards` (master) + (events gap) | T |
| `t.account_payments` | — | `CRDM_AccountPayment` | (gap) | F |
| `t.cash_drawer` | — | `CRDM_CashOfficeSafe` + `CRDM_PaidIn_PaidOut` | `sales.cash_drawer_shifts` + `sales.cash_drawer_events` | T or F |
| `t.recalled_transactions` | — | `CRDM_RecalledTransaction` | (gap) | T |
| `t.receipt` | — | `CRDM_Receipt` | (typically derived, not stored — confirm) | T |
| `c.customers` | — | `CRDM_Customer` (1.7.2 only) | `app.customers` | C (Customer) |
| `c.loyalty` | — | `CRDM_LoyaltyCard` + `CRDM_PointsCoupon` | (gap) | C |
| `c.age_verification` | — | `CRDM_AgeInformation` | (gap; matters for alcohol/tobacco/Rx) | C or Q |
| `e.staff_discount` | — | `CRDM_StaffDiscount` | (gap) | C/L (depending on framing) |
| `e.operator_actions` | — | `CRDM_OperatorAction` | (relates to `app.audit_log`?) | Q (LP) |
| `e.not_on_file` | — | `CRDM_NotOnFile` (items scanned without master) | (gap; LP signal) | Q |
| `d.goods_received` | — | `CRDM_GoodsReceived` | (gap) | D (Distribution) |
| `d.stock_adjustments` | — | `CRDM_StockAdjustment` | `ledger.stock_ledger_entries` (overlaps?) | D |
| `d.transfers` | — | `CRDM_Transfer` | `app.transfer_orders` | D |
| `d.supplier_returns` | — | `CRDM_SupplierReturn` | (gap) | D |
| `d.web_returns` | — | `CRDM_WebItemReturn` | (gap; matters for omnichannel) | O or D |
| `d.etopup` | — | `CRDM_ETopUp` (mobile top-up — vertical-specific) | (likely scope-out; phone vertical) | (skip) |

**(All Canary platform-mechanics entities — Chirp/Fox/Hawk/Owl/Bull/Webhook/RaaS/Vault/etc. — have no GSLM or CRDM analog and will be added in Chunk 8 as second-tier canonical: platform layer, not domain.)**

## Coverage gap summary (high-level findings for record)

**Modules well-covered between sources:**
- T (Transaction Pipeline) — CRDM operational + Canary sales schema
- A (Asset) — GSLM SalesOutlets + Canary app.locations
- M (Merchandising) — GSLM rich master data, Canary partial
- Q (Loss Prevention) — Canary platform layer (Chirp/Fox/Hawk) is the active build

**Modules with material gaps:**
- **P (Pricing)** — GSLM has full Promotion model (5 tables); Canary has none yet. **Gap.**
- **F (Finance)** — GSLM has Taxes; Canary has bank_accounts but no tax model, no GL, no AP/AR, no three-way match. **Big gap.**
- **D (Distribution)** — CRDM has 5 movement entities; Canary has only `transfer_orders` and `stock_ledger_entries`. **Material gap.**
- **S (Space)** — GSLM has Planogram (1 table) + SKU/Pack-in-outlet placement (2 tables); Canary has zero. **Gap.**
- **L (Labor)** — Neither GSLM nor CRDM nor Canary has this. **Greenfield gap — must design from scratch.**
- **O (Orders)** — Sales orders / purchase orders absent across all sources except Canary `transfer_orders`. **Greenfield gap.**
- **E (Execution / Workflow)** — No source has task/workflow tables. **Greenfield gap.**
- **N (Device)** — All sources have implicit POS/terminal references via columns; only Canary has explicit `app.terminals`/`sales.terminals`. **Partial.**
- **C (Customer)** — CRDM 1.7.2 had it, dropped in 1.8. Canary has app.customers. **OK at master, weak at loyalty/segmentation.**

## What Chunk 2 onward will produce

For each entity in the canonical (~100-120 total), one entry like:

```
### m.products  [Module M — Merchandising]

**Sources:**
- GSLM3.SKUItems (43 cols) — anchor
- GSLM3.Styles + StyleVariants — variant decomposition (folded as JSON or separate table; TBD per design)
- CRDM_Item.ArticleID — referenced by transactions
- Canary app.products (12 cols) — current Canary version

**Canonical DDL** (Postgres):
```sql
CREATE TABLE m.products (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id uuid NOT NULL REFERENCES m.tenants(id),
  sku text NOT NULL,
  -- ...
);
```

**Reconciliation notes:**
- GSLM had `bigint` for product_id; canonical uses `uuid` per Canary convention
- GSLM has `style_variant_id` FK chain; canonical folds variants into `attributes jsonb` for simplicity (variant_group can be reconstructed via index)
- Canary current has no `vendor_id` link — added per GSLM
- ...
```

## Status

- **Chunk 1 complete (revised after S0 discovery).** This file = the foundation everything else builds on.

## Revised chunk plan (post-S9-discovery)

| # | Domain / Layer | S0 entities | + S9 prefix interfaces | + Operational (CRDM/Canary) | Module owner(s) |
|---|---|---|---|---|---|
| 2 | **Item** (S0) | 22 | C-Prefix (11): Product details, supplier ref, dept/sub-dept, store/warehouse attrs, PLU | + Canary `app.products`; CRDM `Item` line items | M (Merchandising) |
| 3 | **Location** (S0) + **Space** (S0) | 16 + 8 = 24 | S-Prefix (19): store details, store range, capacity info, planogram product map, range data | + Canary `app.locations`, `app.location_hierarchy`; recovery `Ref_LocationHierarchy` | A (Asset) + S (Space) |
| 4 | **Customer** (S0) + **People** (S0) | 14 + 4 = 18 | (P-Prefix empty; R-Prefix empty — gap remains for People exchange) | + Canary `app.customers`, `app.users`, `app.employees`, `app.user_*`, `app.employee_*`; CRDM 1.7.2 `Customer` | C (Customer) + L (Labor) |
| 5 | **Supply** (S0) — vendor + distribution + inventory | 19 | **D-Prefix (18)**: PO download, GRN, inventory adjustment, stocktake, RTV, direct PO receipt, stock transfer | + CRDM `GoodsReceived`, `Transfer`, `StockAdjustment`, `SupplierReturn`, `WebItemReturn`; Canary `transfer_orders`, `ledger.stock_ledger_entries` | M (vendors) + D (Distribution) |
| **5b** | **NEW — Orders** (closes greenfield) | (none in S0) | **J-Prefix (29)**: allocations, sales forecast, PBS/PBL/Direct order types, ASN, BOL picking, item-warehouse-supplier, transfer details, promotions interface | (no Canary equivalent — net new) | **O (Orders)** |
| 6 | **Price** (S0) + **Finance** (S0) | 6 + 13 = 19 | F-Prefix (5): PO RMS↔TIMS, supplier invoice, supplier info, PO ack, Tesco invoice ReIM | + Canary `app.bank_accounts`, `ledger.*` | P (Pricing) + F (Finance) |
| 7 | **CRDM POS Operational** (Transaction Pipeline) | n/a (S0 doesn't have transactions) | (no S9 prefix for POS — TOM was back-office) | CRDM 25 entities (Header, Item, Tender, Discount, FastFact, etc.) + Canary `sales.*` schema | T (Transaction Pipeline) |
| 8 | **Canary platform mechanics** | n/a (no MDM source) | n/a (TOM had its own "Operational Framework" — out of scope) | Chirp, Fox, Hawk, Owl, Bull, Webhook, RaaS, Vault, ILDWAC, blockchain anchor, evidence chain, identity, audit_log | Q (LP) + cross-cutting |
| 9 | Module ownership tagging across whole spine | | | | all 13 modules |
| 10 | Render `canonical-data-model.md` + provenance memo + closures | | | | |

**Estimated total canonical entities (REVISED):** ~165-180
- GSLM domains (S0): ~102
- TOM Order entities (S9 J-Prefix net-new): ~10-15 (PO, BOL, ASN, Allocation, Transfer Order, Direct Store Order, Procurement Order, Final Order, etc.)
- CRDM POS operational layer (S2): ~25
- Canary platform mechanics (S4 net-new): ~25-30

**Greenfield modules remaining (no source has them):**
- E (Execution / workflow) — task management, work assignment, status flows
- L (Labor schedules + time records) — People exists in S0 but only 4 entities (employee master); scheduling/time/labor allocation is greenfield

**Closed by S9:**
- ~~O (Orders)~~ — J-Prefix gives PBS, PBL, Direct Store Order, Allocation, Transfer; D-Prefix gives PO download/receipt
- ~~D (Distribution movements partial)~~ — D-Prefix gives full operational movement set

## Design intent — MCP Service SLAs at L3 bus junctions

**Founder directive (2026-05-01):**

> "Our design wants to put an MCP service at every L3 bus process and Go subsystem junction. It has to know its service level agreements at that node, and this detail helps define it."

This reframes what Chunk 9b produces and elevates the operational fingerprints from documentation to **executable spec**.

### The mapping

Every operational fingerprint extracted in Chunk 1.5 = the SLA spec for one MCP service junction in Canary Go's L3 bus. Each MCP service at a junction needs to be **self-aware** of its own contract:

| Fingerprint field (captured) | Maps to MCP service SLA dimension |
|---|---|
| Source system | Upstream binding |
| Target system | Downstream binding |
| Direction / type | Service role (producer / consumer / bridge) |
| Trigger / cadence | When it fires (real-time / scheduled / event / on-demand) |
| Payload format | Message envelope contract (XML / flat / JSON) |
| Header structure | Routing & correlation contract |
| Entities transported | Payload schema contract |
| Approx field count | Payload size class |
| Volume/scale notes | Throughput SLO |
| Cross-references | Service dependency graph |
| Business event | Trigger condition |

### What still needs to be designed (Chunk 9b enrichment)

Things the source TOM specs flagged as "TBC" or pushed to a separate "IMOF" framework — these are now Canary Go's job to design per-junction:

- **Latency targets** — response time SLO per junction (p50/p95/p99)
- **Freshness targets** — max staleness for downstream consumers
- **Atomicity guarantee** — transactional vs eventual; replay-safe?
- **Retry policy** — count, backoff, dead-letter
- **Failure handling** — alert thresholds, escalation paths
- **Idempotency contract** — replay-safe via message ID? content hash?
- **Authentication / authorization** at the junction
- **Audit / observability** requirements (what gets logged, retained how long, queried by whom)

These are the **net-new SLA fields** Canary Go's MCP service architecture adds on top of TOM's data exchange specs. The fingerprints give us the WHAT and WHEN; the SLA enrichment gives us the HOW WELL.

### What Chunk 9b now produces

Not just "operational architecture" — specifically:

**Output 1: MCP Service Junction Inventory** — `docs/sdds/go-handoff/mcp-service-junctions.md`
- One entry per junction (~79 candidates from fingerprints, may consolidate)
- Each entry: full SLA spec (source/target binding + payload + cadence + latency/freshness/atomicity/retry/idempotency/auth/observability)
- Service dependency graph (DOT or mermaid) showing junction-to-junction edges

**Output 2: L3 Bus Topology** — embedded in MCP service junctions doc
- System graph: nodes = systems (RMS, GFO, Storeline, etc., reframed for Canary Go), edges = MCP service junctions
- Visual + tabular

**Output 3: Operational Clock** — embedded
- Calendar of when each MCP service fires (real-time / hourly / daily / weekly / event)
- Critical path identification (which junctions block each other)

This is the same Chunk 9b deliverable as before, but with the framing locked: **each MCP service is born self-aware of its SLA at its junction**, and the spec we're producing IS that SLA.

## Chunk 1.5 — Operational Fingerprint Extraction (COMPLETE)

5 parallel subagents processed all 82 interface specs across 5 non-empty prefix folders.

| Prefix | Folders | Fingerprints | .txt cached |
|---|---|---|---|
| C (Commercial / master data) | 11 | 11 | 11 |
| D (Distribution / movements) | 18 | 18 | 18 |
| F (Finance / PO+invoice) | 5 | 6 (F001/F015 split) | 5 |
| J (GFO / Orders & forecasting) | 29 | 28 (J032 family collapsed) | 27 |
| S (Space Range Display) | 19 | 16 (3 unnamed dups merged) | 16 |
| **TOTAL** | **82** | **79** | **77** |

Output files in `~/CRDM-recovery/interface-design-docs/`:
- `_fingerprints-C-prefix.md` (16.8 KB)
- `_fingerprints-D-prefix.md` (25.2 KB)
- `_fingerprints-F-prefix.md` (19.2 KB)
- `_fingerprints-J-prefix.md` (41.8 KB) — largest, includes 6 cross-cutting pattern families noted by subagent
- `_fingerprints-S-prefix.md` (31.9 KB)

Plus 77 cached `.txt` files under `{prefix}-converted/` for entity walks.

### Cross-cutting patterns surfaced by subagents (worth bringing into Chunk 9b)

- **Order-routing JIROA/JIROB schema family** (J017/J019/J020/J024) — single COBOL schema, `JIROB-PBL-IND` discriminator (1=PBL, 2=PBS, 3=DTS) drives downstream routing
- **Sales feeds two-cadence pattern** — J004 hourly to UDD, J035 every-15-min to GFO, both from same TDS staging
- **GFO product/location masters daily refresh family** (J052/J054/J057/J076/J087/J088/J100) — shared `TOM_IDSGFO_FileTransfer` orchestration
- **D-Prefix four architectural patterns** — RIB-mediated push, DB2 SP invocation, FTP file-drop, master-system outbound
- **C-Prefix common envelope** — RMS → BizTalk → SQL Server IDS via real-time JMS RIB (XML); C001TR is the Turkey deviant (flat-file batch via SSIS)
- **F-Prefix three-way match implicit pattern** — F001/F015 (PO) → F013 (PO ack) → receipt → F004 (supplier invoice via ReIM into OFi)
- **F-Prefix multi-model tax** — F004/F014 carry parallel sub-groups for VAT (rate-table, multi-rate per invoice) and Sales Tax — anticipated country-by-country tax-engine variance
- **S-Prefix Common Forms pattern** — 13 IDS-sourced interfaces all funnel through named stored procs returning common rowset shapes per entity (Item, Location, Merchandise Hierarchy, SKU Hierarchy, Division Hierarchy, Supplier Hierarchy)
- **S-Prefix SAE-as-hub** — feeds RPAS forecasting + IKB planogram + receives RMS sales aggregation + IKB replenishment ref
- **IMOF (Integration Management Operational Framework)** — was still in design across all corpus dates; load-bearing infrastructure left as "placeholder" pattern. **This is exactly what Canary Go MCP services replace.**

These patterns are SLA archetypes — Canary Go MCP services will fall into similar families.

- **Resume point:** Chunk 2 — GSLM Item domain (22 entities). Read `~/CRDM-recovery/gslm-mdm-site/Item.md` entity-by-entity, reconcile with S1 SQL DDL types and Canary `app.products`, produce canonical entries appended to `02-canonical-draft.md` in this folder.

---

# §2 Design Principles

**Working file. Chunk 1.6 output. Ratification gate before entity walks.**

This document locks the design rules every canonical entity in chunks 2-8 must follow. Any rule here that's wrong needs to be wrong in one place, not corrected 80 times during the walk. Read once, ratify, then I execute.

## 1. Anchor: ARTS-compliant, SMB-2030 scoped

**Structural anchor:** ARTS (Association for Retail Technology Standards) — the industry-standard retail operational data model. Originally published by NRF/ARTS, widely implemented by NCR, Oracle Retail, IBM, SAP retail, and most enterprise retail platforms. ARTS dissolved as an org in 2018; the standards remain authoritative.

**ARTS coverage we honor:**
- ARTS Item (product master, hierarchy, attributes)
- ARTS Party (Customer / Employee / Vendor — unified party hierarchy)
- ARTS Location (store, warehouse, hierarchy)
- ARTS Inventory (SKU, stock-on-hand, movements)
- ARTS Pricing (price book, promotions, deals)
- ARTS Tender (payment types, currency)
- ARTS POSLog (transaction event log)
- ARTS Sales Audit
- ARTS Financial (GL hooks, AP, AR, tax)
- ARTS Loyalty
- ARTS Employee (master, role)
- ARTS Device (POS terminal, kiosk, mobile)
- ARTS Planogram

**Local ARTS source PDFs (S10):**
- `ARTS XML Inventory Charter.pdf` + `CR IXRetail Inventory Technical Specification V1.0 20050813.pdf` + `InventoryV2.0.0.xsd`
- `ARTS_LocationV2_Charter_20160202.pdf` + `CR_ARTS_Location_Technical_Specification_V2.0.0_20170301_final.pdf` + `Location V2 Schemas 20170208.zip`
- `LCWD ARTS Planogram Domain Model V2.0.0 20170207.pdf`

For domains where we don't have the local PDF (POSLog, Customer, Item, Pricing, etc.), I use my training knowledge of ARTS canonical structure — naming conventions, entity relationships, attribute patterns. This is well-documented public material.

**Target market: SMB-2030, not enterprise-2010.** Specifically:
- Single store or small fleet (1-100 stores), often 1
- Solo operator wears every hat (per founder memory `project_engine_map_and_main_street_archetype`)
- Single banner (no BusinessDivisions over-decomposition)
- Real-time tech default (Postgres, JSONB, pgvector — not COBOL flat files via BizTalk)
- Agent-driven workflows (MCP services, not human ETL teams)

## 2. Lifecycle: TOM operational clock

ARTS gives the **structure** (what entities exist, what fields, what FKs). TOM (Tesco Operating Model interface specs, S9, 79 fingerprints) gives the **lifecycle dynamics** — when each entity is produced, updated, consumed, by which junction, on what cadence.

**Per founder direction (2026-05-01):** each MCP service at an L3 bus junction is born self-aware of its SLA at that node. The operational clock from TOM is critical for the relationships — it's what makes the canonical executable rather than just descriptive.

**Every canonical entity carries two layers:**
1. **Schema** — ARTS-aligned DDL with PK/FK + UUID strategy + JSONB pragmatism
2. **Operational lifecycle** — the MCP service junctions that touch it (producer, updaters, consumers), each with cadence + SLA

## 3. UUID strategy

| Field | Type | Default | Why |
|---|---|---|---|
| Internal PKs | `uuid` | `gen_random_uuid()` | Stable across systems, no integer-key collision when merging tenants/data |
| Internal FKs | `uuid REFERENCES other(id)` | — | Enforced relational integrity |
| External system IDs (POS-native, vendor-assigned, GS1 GTIN, etc.) | `text` | — | Preserved as data, never as PK |
| Multi-system reconciliation | `app.external_identities` table (already exists in current spec) | — | Maps `(canonical_id, source_system, external_id)` |

**Rule:** UUID PKs everywhere. Never `serial` / `bigserial`. Never composite PKs (composite UNIQUE constraints fine).

## 4. PK / FK conventions

```sql
-- Standard entity skeleton
CREATE TABLE {schema}.{entity} (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES app.tenants(id),
  {parent_id}   uuid {NOT NULL} REFERENCES {schema}.{parent}(id),
  {natural_key} text NOT NULL,
  ...
  attributes    jsonb DEFAULT '{}',  -- extension fields
  status        text NOT NULL DEFAULT 'active',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, {natural_key})
);

CREATE INDEX idx_{entity}_tenant ON {schema}.{entity}(tenant_id);
CREATE INDEX idx_{entity}_parent ON {schema}.{entity}({parent_id});
```

**Universal columns** on every entity:
- `id uuid PK`
- `tenant_id uuid NOT NULL REFERENCES app.tenants(id)` — schema-per-tenant strategy already lives in current data-model.md
- `created_at` / `updated_at`
- `attributes jsonb` — extension fields (rule below)
- `status` — soft-state lifecycle column where applicable

**FK rules:**
- All FKs use `uuid REFERENCES`. No string-key FKs.
- `ON DELETE CASCADE` only when the child has zero independent meaning (e.g., line item → transaction). Otherwise `ON DELETE RESTRICT` (default).
- All FKs are indexed.

## 5. JSONB usage rules

**JSONB IS for:**
- Variant attributes (color, size, material — anything where the set of attribute keys differs per item)
- Vertical-specific fields (Rx data on a pharmacy SKU, calorie data on a food SKU)
- Extension fields (merchant-defined attributes, integration-payload echoes)
- Semi-structured payload that might evolve (audit log details, webhook bodies)
- Anything where schema evolution would otherwise require a migration

**JSONB IS NOT for:**
- Identifiers (always typed columns)
- Anything that's queried structurally (`WHERE attributes->>'foo' = 'bar'` is a smell — make it a column)
- Anything indexed for relational integrity
- Anything that has a clear cardinality and stable schema

**When in doubt:** start with a typed column, demote to JSONB only when 3+ variants emerge.

## 6. Entity sizing rule

**Target: 10-30 columns per entity.** If you're at 50+, decompose. If you're at 5, fold into parent.

Counter-examples from sources:
- CRDM_FastFact has ~110 columns → that's a denormalized *aggregate* table, fine for `metrics` schema, NOT for an OLTP entity. SMB-2030 canonical excludes pre-aggregated FastFacts (build aggregations in `metrics` schema as separate entities).
- GSLM SalesOutletSKUItemPromoCompDetails — 5-table joined name → 1 entity that crosses too many concerns. Decompose into store_assortment + assortment_promotion (2 entities).

## 7. Naming conventions

- **Schema names:** lowercase, single word where possible — `m` (master), `t` (transaction), `i` (inventory), `p` (pricing), `f` (financial), `o` (orders), `q` (quality/loss-prevention), `app` (cross-cutting platform)
- **Entity names:** lowercase plural snake_case — `items`, `transactions`, `purchase_orders`
- **Column names:** lowercase singular snake_case — `item_id`, `created_at`, `tenant_id`
- **ARTS naming:** ARTS uses PascalCase singular (`RetailTransaction`, `Item`). We translate to lowercase plural snake_case for Postgres/SMB convention (`retail_transactions`, `items`). Document the mapping in each entity entry.
- **No abbreviations** in entity names. `purchase_orders` not `pos`. `customer_loyalty_memberships` not `cust_lty_mem`.

## 8. Module-schema mapping

Per the 13-module spine (post-rename: M=Merchandising, O=Orders, C=Customer, E=Execution, T=Transaction, F=Finance, A=Asset, D=Distribution, N=Device, P=Pricing, S=Space, L=Labor, Q=Loss Prevention):

| Schema | Modules served | Notes |
|---|---|---|
| `m` (master) | M | items, vendors, categories, attributes |
| `i` (inventory) | D | inventory positions, movements (separate from item master) |
| `t` (transaction) | T | POSLog transactions + line items + tenders |
| `o` (orders) | O | purchase orders, sales orders, fulfillment, ASN, BOL |
| `p` (pricing) | P | price books, promotions, deals, taxes |
| `f` (financial) | F | GL accounts, AP/AR, supplier invoices, three-way match |
| `c` (customer) | C | customer master, loyalty, segments |
| `l` (location) | A, S | stores, hierarchy, sales floors, planograms |
| `e` (employee) | L (people domain) | employees, roles, schedules |
| `n` (device) | N | POS terminals, kiosks, mobile |
| `app` | cross-cutting | identity, tenants, settings, feature flags, audit |
| `q` (loss prevention) | Q | Chirp rules, Fox cases, Hawk incidents, Owl observability |
| `metrics` | reporting | facts, dimensions, ML features (denormalized) |
| `ledger` | F (sub-schema) | RIB batches, stock ledger, ILDWAC positions |
| `memory` | platform | agent memory, sessions |

## 9. Cardinality-aware decomposition

Don't pre-decompose for theoretical scale SMB will never hit. Examples of WHERE we deviate from full enterprise ARTS:

| ARTS / GSLM full | SMB-2030 canonical |
|---|---|
| BusinessDivision → Department → Section → Class → SubClass → FineLine (6 levels, 6 tables) | `m.product_categories` (recursive, 1 table, depth as data) |
| StyleVariants + Values + Groups + Assignments (4 tables) | `attributes jsonb` on `m.items` |
| ArticleItems vs SKUItems vs PackItems (separate tables for different item kinds) | `m.items` with `item_type` column; `m.item_pack_components` for pack relationships only when pack-aware merchant |
| MerchandiseAttributesLanguages (multi-language separate table) | `attributes->>'i18n'` jsonb (single-language v1, multi-lang via JSONB later) |
| SalesOutletSKUItemPromoCompDetails (5-concept join table) | `i.store_assortment` + `p.assortment_promotion` (2 narrow tables) |

**Test for inclusion:** is this decomposition justified by SMB-2030 query patterns or scale? If no, fold.

## 10. Future-proofing for agent reads (the founder's direct question)

Six principles applied to every entity:

1. **Agent-shaped reads** — each entity is one coherent semantic unit. One agent read = one meaningful object. No "join 5 tables to make sense of a single product."
2. **Embedding-friendly** — text columns (descriptions, names, notes) support pgvector semantic search natively. Don't shred descriptive text into normalized lookup tables.
3. **MCP-junction-friendly** — entity boundaries map cleanly to MCP service boundaries. No straddling.
4. **JSONB for variants & extensions** — schema evolution doesn't require a migration.
5. **Cardinality-aware** — see §9.
6. **Standards-conformant** — ARTS-aligned naming and relationships. Future integration with any retail tech speaks our language without translation layers.

## 11. Provenance requirement

Every canonical entity entry documents its provenance trail:

- **ARTS reference** — which ARTS standard (and version) it aligns to
- **GSLM source** — which GSLM entity(ies) it folds (if any)
- **CRDM source** — which CRDM entity it's the operational counterpart for (if any)
- **TOM lifecycle** — which TOM interface fingerprints touch it (which junctions produce/update/consume)
- **Canary current** — which Canary `data-model.md` table it supersedes (if any)
- **Justification** — one sentence on why we made the design choices we did

This is the audit trail that lets the next session (or auditor) understand WHY this canonical looks the way it does.

## 12. Operational lifecycle template

For every entity, a structured "lifecycle" section using TOM fingerprints (S9):

```markdown
#### Operational lifecycle (TOM operational clock)

**Producers** (junctions that create/update this entity):
- `mcp.{domain}.{entity}.create` ← {ARTS event} ← {originating system in TOM}
- `mcp.{domain}.{entity}.update` ← {ARTS event} ← {originating system in TOM}

**Consumers** (junctions that read this entity downstream):
- `mcp.{domain}.{entity}.lookup` (real-time, T module)
- `mcp.{domain}.{entity}.aggregate` (batch, metrics schema)
- `mcp.{domain}.{entity}.replicate` (daily, downstream forecast)

**Cadence at producer:** real-time | event-triggered | scheduled-batch | on-demand
**Cadence at consumers:** {per-consumer SLA}
**Idempotency contract:** {key} (e.g., (tenant_id, sku) for items)
**SLA targets at producer:** latency p95 <X, freshness <Y, atomicity {transactional | eventual}
**Cross-references:** {related junctions, e.g., "produces inventory_movement when consumed"}
```

The values come from the TOM fingerprints we extracted in Chunk 1.5. Each canonical entity references its lifecycle by enumerating which fingerprints touch it.

## 13. Canary platform additions

Canary's loss-prevention / observability / case-management layer (Chirp / Fox / Hawk / Owl / Bull / RaaS / Vault / ILDWAC / blockchain anchor / evidence chain) sits ABOVE ARTS — these aren't ARTS entities, they're Canary platform mechanics. Same design rules apply (UUID PKs, JSONB pragmatism, agent-friendly), but no ARTS provenance.

## Status

- **Chunk 1.6 in progress.** Ratification needed before walks proceed.
- **Resume point:** Chunk 2 — Item domain. ARTS Item structural anchor + TOM C-Prefix lifecycle fingerprints. Target ~6-8 entities (items, product_categories, vendors, item_vendors, item_packs if needed, item_taxes if needed).

## Ratification gate

Five questions for the founder before I proceed:

1. **ARTS as structural anchor + TOM as lifecycle dimension** — yes?
2. **UUID PKs + tenant_id everywhere + standard timestamp columns** — yes?
3. **JSONB for variants/extensions, typed columns for queryables** — yes?
4. **10-30 column target per entity, fold ARTS over-decomposition where SMB doesn't need it** — yes?
5. **Schema-per-domain (m, i, t, o, p, f, c, l, e, n, q + cross-cutting app) within schema-per-tenant pattern** — yes?

Say "yes all five" or call out which ones need adjustment.

---

# §3 Item Domain

**ARTS anchor**: ARTS Item (ODM) + IXRetail Inventory V1.0 + ARTS standard Vendor / Pricing / Hierarchy entities.
**Module**: M (Merchandising).
**Entities**: 6 (items · product_categories · vendors · item_vendors · item_barcodes · item_packs).
**Folded from sources**: GSLM 22 Item entities → 6 SMB-2030 canonical via §9 cardinality rules.

## Domain narrative

The Item domain is the master data spine for everything sold. ARTS canonical decomposes Item across Style/SKU/Pack/Variant/Hierarchy — appropriate for 4,000-store enterprise estates with millions of SKUs. SMB-2030 retailers carry 1K-50K SKUs across 1-100 stores; over-decomposition costs more than it returns. We collapse style variants into JSONB (`attributes`), collapse 6-level merchandise hierarchy into a single recursive `product_categories` table, and keep packs as a single composition table (used only when pack-aware).

Operational lifecycle in TOM: items are produced/updated by `mcp.master.item.*` junctions (replacing C001/C002/C003/C008/C010/C012/C013 RIB pattern), consumed by `mcp.{transaction,forecast,planogram,store-line}.item.lookup` junctions across all downstream domains. Real-time push at producer; consumer freshness varies (real-time for transaction lookup, daily for forecast, half-hourly for missed-barcode recovery).

---

## m.items

**ARTS reference**: ARTS Item (ODM) — Item / SKU / Style consolidated.
**Module**: M.

### Schema

```sql
CREATE TABLE m.items (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sku                 text NOT NULL,                                 -- merchant's primary SKU
  description         text NOT NULL,                                 -- shelf-name (full-text indexed)
  short_description   text,                                          -- receipt-name
  item_type           text NOT NULL DEFAULT 'standard',              -- standard | service | giftcard | tare | pack | bundle
  category_id         uuid REFERENCES m.product_categories(id),      -- canonical category (single FK; see categories table for hierarchy)
  unit_of_measure     text NOT NULL DEFAULT 'EA',                    -- EA | LB | KG | OZ | GAL | etc. (UN/ECE Recommendation 20)
  uom_quantity        numeric(10,4) NOT NULL DEFAULT 1,              -- e.g., 0.5 LB per unit
  default_price       numeric(12,4),                                 -- catalog price; per-location overrides in p.item_prices
  default_cost        numeric(12,4),                                 -- last-known cost; vendor-specific in m.item_vendors
  default_currency    text NOT NULL DEFAULT 'USD',                   -- ISO 4217
  tax_class           text,                                          -- tax classification key (lookup in p.tax_classes)
  food_stamp_eligible boolean NOT NULL DEFAULT false,                -- US SNAP/EBT
  age_restriction     int,                                           -- minimum buyer age (alcohol, tobacco, Rx)
  weighable           boolean NOT NULL DEFAULT false,                -- requires scale at POS
  attributes          jsonb NOT NULL DEFAULT '{}',                   -- style variants (color, size), vertical fields (Rx NDC, food calories), merchant-defined
  status              text NOT NULL DEFAULT 'active',                -- active | discontinued | seasonal | hidden
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sku)
);

CREATE INDEX idx_items_tenant ON m.items(tenant_id);
CREATE INDEX idx_items_category ON m.items(category_id);
CREATE INDEX idx_items_status ON m.items(status) WHERE status != 'active';
CREATE INDEX idx_items_description_trgm ON m.items USING gin(description gin_trgm_ops);
CREATE INDEX idx_items_attributes ON m.items USING gin(attributes);
```

### Operational lifecycle (TOM operational clock)

**Producers**:
- `mcp.master.item.create` — real-time push when merchant or import creates an item (TOM equivalent: C001US RMS→IDS via RIB JMS)
- `mcp.master.item.update` — real-time push on attribute change (C001US update path)
- `mcp.master.item.delete` — soft-delete via status change (C001US ItemRef delete path)

**Consumers**:
- `mcp.transaction.item.lookup` — real-time, p99 < 50ms (downstream of every POS scan)
- `mcp.inventory.item.position-refresh` — real-time on movement (D028 GRN, D019 adjustment, etc.)
- `mcp.pricing.item.price-resolve` — real-time at quote/checkout
- `mcp.forecast.item.daily-refresh` — daily batch (TOM J052/J054/J057 equivalent)
- `mcp.store-line.item.attributes-push` — overnight + half-hourly emergency (TOM C023+C045+C027 PLU feed)
- `mcp.planogram.item.update` — daily (TOM S036 equivalent)

**SLA at producer**: latency p95 < 200ms, idempotent on `(tenant_id, sku)`, atomicity transactional with attribute writes.
**SLA at consumers**: real-time lookups p99 < 50ms; freshness < 5s for lookup, < 24h for forecast.

### Provenance

- **ARTS Item ODM** (structural anchor) — Item canonical with extension via attributes
- **GSLM folded**: `SKUItems` + `Styles` + `StyleVariants` + `StyleVariantValues` + `StyleVariantGroups` + `StyleVariantGroupAssignments` + `ArticleItems` + `ArticleTypes` + `Merchandise` + `MerchandiseAttributesLanguages` (10 GSLM entities → 1 + JSONB; variant decomposition → `attributes`)
- **CRDM operational counterpart**: `CRDM_Item` is the line-item event in T schema (`t.transaction_line_items`); not the master record
- **TOM junctions touching**: C001US/TR (Product), C002US (Department→category), C003US (Class/SubClass→category), C023/C027/C045/C054 (PLU feed)
- **Canary current**: `app.products` — superseded by `m.items`

### Justification

Single canonical product master with `item_type` discriminator avoids the ARTS Item/SKU/Pack 3-table split that hurts SMB query performance with no scale benefit. Style variants in JSONB (per §5) — merchants without variants pay zero cost; merchants with them get full flexibility. Tax/food-stamp/age fields surface as columns because they're queried structurally at every POS scan. Pricing intentionally split into `p.item_prices` (multi-location overrides) — `default_price` here is the catalog fallback, not the source of truth for sale price.

---

## m.product_categories

**ARTS reference**: ARTS Item Hierarchy (MerchandiseHierarchy collapsed).
**Module**: M.

### Schema

```sql
CREATE TABLE m.product_categories (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES m.product_categories(id),  -- NULL for root
  code            text NOT NULL,                              -- merchant or POS-native category code
  name            text NOT NULL,
  level           int NOT NULL,                               -- depth (0=root); denormalized for query speed
  path            ltree,                                      -- materialized path for subtree queries (Postgres ltree)
  attributes      jsonb NOT NULL DEFAULT '{}',                -- merchant-defined (e.g., margin tier, demand class)
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_categories_tenant ON m.product_categories(tenant_id);
CREATE INDEX idx_categories_parent ON m.product_categories(parent_id);
CREATE INDEX idx_categories_path ON m.product_categories USING gist(path);
```

### Operational lifecycle

**Producers**:
- `mcp.master.category.upsert` — real-time on hierarchy change (TOM C002US/C003US RIB pattern)

**Consumers**:
- `mcp.master.item.lookup` (FK from items)
- `mcp.metrics.aggregate.by-category` — daily rollup (M and Q modules)
- `mcp.pricing.promotion.scope-by-category`
- `mcp.store-line.hierarchy-push` — daily batch (TOM C024/C025)

**SLA at producer**: real-time, p95 < 100ms, idempotent on `(tenant_id, code)`.

### Provenance

- **ARTS reference**: MerchandiseHierarchy (recursive)
- **GSLM folded**: `BusinessDivisions` + `Departments` + `Classes` + `SubClasses` + `Finelines` + `Sections` (6 levels → recursive single table; depth in `level` column)
- **TOM junctions**: C002US, C003US, C024+C025 (downstream Storeline)
- **Canary current**: not present — net-new
- **Justification**: 6-level enterprise hierarchy is over-decomposition for SMB. Recursive table with `path ltree` supports any depth (1, 3, or 6 levels) without schema change. ltree gist index gives O(log n) subtree queries.

---

## m.vendors

**ARTS reference**: ARTS Vendor / Supplier (Party hierarchy).
**Module**: M (with cross-cuts to F for AP and D for receiving).

### Schema

```sql
CREATE TABLE m.vendors (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  vendor_code     text NOT NULL,                          -- merchant-assigned or POS-native
  name            text NOT NULL,                          -- legal/trading name
  short_name      text,
  vendor_type     text NOT NULL DEFAULT 'supplier',       -- supplier | manufacturer | distributor | broker | dropship
  primary_contact jsonb DEFAULT '{}',                     -- {name, email, phone, fax}
  address         jsonb DEFAULT '{}',                     -- {line1, line2, city, region, postal_code, country, timezone}
  payment_terms   text,                                   -- 'NET30' | 'COD' | 'PREPAY' | etc.
  currency        text DEFAULT 'USD',                     -- ISO 4217
  tax_id          text,                                   -- EIN/VAT/TIN — sensitive (PII tier 2)
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',         -- active | inactive | hold
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, vendor_code)
);

CREATE INDEX idx_vendors_tenant ON m.vendors(tenant_id);
CREATE INDEX idx_vendors_status ON m.vendors(status) WHERE status != 'active';
CREATE INDEX idx_vendors_name_trgm ON m.vendors USING gin(name gin_trgm_ops);
```

### Operational lifecycle

**Producers**:
- `mcp.master.vendor.upsert` — real-time on RMS supplier change (TOM C008US RIB pattern)
- `mcp.master.vendor.from-financial-sync` — F-Prefix F012 (OFi → RMS) — vendor master originates in financial system, syncs to operational

**Consumers**:
- `mcp.master.item-vendor.lookup` (FK from item_vendors)
- `mcp.orders.purchase-order.assign-vendor`
- `mcp.financial.invoice.match-vendor` (three-way match — F004/F014)
- `mcp.distribution.goods-received.note-vendor` (D028 GRN)

**SLA at producer**: real-time push, idempotent on `(tenant_id, vendor_code)`.

### Provenance

- **ARTS reference**: Vendor (Party subtype)
- **GSLM**: `Vendors` (1 table — minimal in GSLM SQL implementation; richer in S0 MDM site Supply domain)
- **TOM junctions**: C008US (operational), F012 (financial-source-of-truth pattern)
- **Canary current**: not present — net-new
- **Justification**: Financial-system origin (F012 OFi→RMS) is a critical pattern — vendor master has dual lineage (financial onboarding + operational ops). Both consumed by the same canonical entity. Address and contact in JSONB because internationalization variability (US uses state, UK uses county, JP uses prefecture).

---

## m.item_vendors

**ARTS reference**: ARTS ItemVendor (Vendor-Item association).
**Module**: M.

### Schema

```sql
CREATE TABLE m.item_vendors (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
  vendor_id           uuid NOT NULL REFERENCES m.vendors(id) ON DELETE RESTRICT,
  vendor_sku          text,                              -- vendor's identifier for the item
  vendor_description  text,                              -- vendor's catalog description
  unit_cost           numeric(12,4),                     -- vendor's per-unit cost
  case_pack_qty       int DEFAULT 1,                     -- units per case
  min_order_qty       int DEFAULT 1,
  lead_time_days      int,
  is_primary          boolean NOT NULL DEFAULT false,    -- the default vendor for this item
  country_of_origin   text,                              -- ISO 3166 alpha-2
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, vendor_id),
  CONSTRAINT one_primary_per_item EXCLUDE (item_id WITH =) WHERE (is_primary = true AND status = 'active')
);

CREATE INDEX idx_item_vendors_tenant ON m.item_vendors(tenant_id);
CREATE INDEX idx_item_vendors_item ON m.item_vendors(item_id);
CREATE INDEX idx_item_vendors_vendor ON m.item_vendors(vendor_id);
CREATE INDEX idx_item_vendors_primary ON m.item_vendors(item_id) WHERE is_primary = true;
```

### Operational lifecycle

**Producers**:
- `mcp.master.item-vendor.assign` — real-time when item-vendor link created (subset of C001US payload — StyleSupplier, SKUSupplier, PackSupplier)
- `mcp.master.item-vendor.update-cost` — when cost changes (vendor PO ack F013)

**Consumers**:
- `mcp.orders.purchase-order.lookup-vendor-cost` — at PO creation
- `mcp.orders.suggest-replenishment-vendor` — daily forecast (J100 Item-Warehouse-Supplier pattern)
- `mcp.financial.invoice.cost-validate` — three-way match against received invoice

**SLA at producer**: real-time, EXCLUDE constraint enforces single primary vendor per item per tenant.

### Provenance

- **ARTS reference**: ItemVendor association
- **GSLM**: `SKUItemVendors` (folded — collapses Style/SKU/Pack vendor associations into single canonical)
- **TOM junctions**: C001US carries Style/SKU/Pack-Supplier as part of product master payload; J003 (Supplier Authority Data); J100 (Item-Warehouse-Supplier — denormalized for forecast)
- **Justification**: Single association table with all 3 GSLM variants folded. EXCLUDE constraint guarantees database-level uniqueness of primary vendor (no application-layer enforcement gap).

---

## m.item_barcodes

**ARTS reference**: ARTS Item Identification (UPC/EAN/GTIN).
**Module**: M.

### Schema

```sql
CREATE TABLE m.item_barcodes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  item_id         uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
  barcode         text NOT NULL,                       -- the scan-string (UPC-A 12, EAN-13, GTIN-14, ITF-14, GS1 DataBar)
  barcode_type    text NOT NULL DEFAULT 'GTIN',        -- GTIN | UPC_A | EAN_13 | ITF_14 | DATABAR | INTERNAL | PLU
  uom_quantity    numeric(10,4) NOT NULL DEFAULT 1,    -- units this barcode represents (case = 12, individual = 1)
  is_primary      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, barcode)
);

CREATE INDEX idx_barcodes_tenant ON m.item_barcodes(tenant_id);
CREATE INDEX idx_barcodes_item ON m.item_barcodes(item_id);
CREATE UNIQUE INDEX idx_barcodes_lookup ON m.item_barcodes(tenant_id, barcode) WHERE status = 'active';
```

### Operational lifecycle

**Producers**:
- `mcp.master.item-barcode.assign` — at item creation (multiple barcodes per item)
- `mcp.master.item-barcode.recover-not-on-file` — when POS scans unknown barcode (C027 NoF half-hourly recovery cycle)

**Consumers**:
- `mcp.transaction.scan-resolve` — every POS scan, p99 < 30ms
- `mcp.inventory.cycle-count.scan-resolve`

**SLA at producer**: real-time. Active-barcode unique constraint prevents duplicate scans resolving to multiple items.

### Provenance

- **ARTS reference**: Item Identification (multiple identifiers per Item)
- **GSLM**: not separately modeled (barcodes lived in SKU table)
- **TOM junctions**: C001US payload (EAN field), C027 (Not-on-File half-hourly recovery — critical for SMB-2030 where new SKUs scan-fail)
- **Justification**: Separate table because items have many barcodes (one per UoM, one per pack size) and barcodes need fast unique-key lookup. Conditional unique index allows historical/inactive barcodes to retain rows for audit without blocking new active assignments.

---

## m.item_packs

**ARTS reference**: ARTS Item Composition / Pack Hierarchy.
**Module**: M.
**Optional**: only used by pack-aware merchants. SMB-2030 default deployment can skip this table.

### Schema

```sql
CREATE TABLE m.item_packs (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL REFERENCES app.tenants(id),
  pack_item_id      uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,    -- the parent (case/bundle)
  component_item_id uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,   -- the child (each unit)
  quantity          numeric(10,4) NOT NULL,                                    -- e.g., 12 for "12-pack"
  pack_type         text NOT NULL DEFAULT 'case',                              -- case | bundle | kit | mix
  attributes        jsonb NOT NULL DEFAULT '{}',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, pack_item_id, component_item_id)
);

CREATE INDEX idx_packs_tenant ON m.item_packs(tenant_id);
CREATE INDEX idx_packs_pack ON m.item_packs(pack_item_id);
CREATE INDEX idx_packs_component ON m.item_packs(component_item_id);
```

### Operational lifecycle

**Producers**:
- `mcp.master.item-pack.compose` — when pack defined

**Consumers**:
- `mcp.inventory.pack.break-down` — when pack received and decomposed to components
- `mcp.transaction.pack.sell-as-unit` — when pack sold whole

**SLA at producer**: not time-critical (master data); idempotent on `(tenant_id, pack_item_id, component_item_id)`.

### Provenance

- **ARTS reference**: Item Composition
- **GSLM folded**: `PackItems` + `PackBreakout` + `PackItemBreakout` (3 tables → 1; pack_type column captures the variant)
- **TOM junctions**: C001US carries Pack and PackSupplier in payload; D028 GRN handles pack-receipt
- **Justification**: GSLM had 3 tables for pack mechanics (item-as-pack, breakdown, breakout). All collapse to "pack composes components in quantity" — one table, one row per (pack, component) pair.

---

## Domain summary

**6 entities, 1 domain (Merchandising), 1 schema (m)**:
- `m.items` (~25 cols) — master record
- `m.product_categories` (~10 cols) — recursive hierarchy
- `m.vendors` (~14 cols) — supplier master
- `m.item_vendors` (~13 cols) — many-to-many with cost/lead-time
- `m.item_barcodes` (~9 cols) — scan-key lookup
- `m.item_packs` (~8 cols, optional) — composition

**Folded from sources**:
- GSLM 22 Item entities → 6 canonical (73% reduction via JSONB and recursive collapse, no functional loss)
- TOM C-Prefix 11 interfaces → 6 entities + 11 lifecycle bindings (each entity touched by multiple junctions)
- ARTS Item ODM core preserved; SMB-2030 cardinality-aware decomposition rules applied per §9

**Net new** (not in any source): `m.item_barcodes` as separate entity (was field on SKU in GSLM), justified by C027 lifecycle pattern + active-barcode uniqueness constraint requirement.

**MCP service junctions defined for this domain (12)**:
- Producers: `mcp.master.item.{create,update,delete}`, `mcp.master.category.upsert`, `mcp.master.vendor.upsert`, `mcp.master.vendor.from-financial-sync`, `mcp.master.item-vendor.{assign,update-cost}`, `mcp.master.item-barcode.{assign,recover-not-on-file}`, `mcp.master.item-pack.compose`
- Consumers: `mcp.transaction.item.lookup`, `mcp.transaction.scan-resolve`, `mcp.inventory.item.position-refresh`, `mcp.pricing.item.price-resolve`, `mcp.forecast.item.daily-refresh`, `mcp.store-line.item.attributes-push`, `mcp.planogram.item.update`, `mcp.metrics.aggregate.by-category`, `mcp.orders.purchase-order.lookup-vendor-cost`, `mcp.financial.invoice.match-vendor`, etc.

These will be consolidated into the Chunk 9b MCP Service Junction Inventory.

## Status

- **Chunk 2 complete.** 6 ARTS-aligned Item entities with TOM operational lifecycle bindings.
- **Resume**: Chunk 3 — Location + Space domain (l schema). ARTS Location V2 anchor (we have full PDF spec) + S-Prefix lifecycle. Target ~6-8 entities.

---

# §4 Location + Space Domain

**ARTS anchor**: ARTS Location V2 (full local PDF spec) + ARTS Planogram V2 + IXRetail Inventory V1.0 (location-portion).
**Modules**: A (Asset), S (Space).
**Entities**: 6 (locations · location_hierarchy · location_zones · planograms · planogram_positions · location_assortment).
**Folded from sources**: GSLM 16 Location + 8 Space entities → 6 SMB-2030 canonical.

## Domain narrative

ARTS Location V2 (March 2017 spec, in our local PDFs) treats Location as the abstract place; physical attributes, addresses, hierarchy, operating hours decompose into 8-12 sub-entities for full enterprise coverage. SMB-2030 doesn't need separate `OperatingHours` and `LocationContact` tables — these collapse into JSONB on the parent `locations` row. Hierarchy is recursive (banner → region → district → store, OR just store, depending on merchant). Within-store zoning (sales floors, departments, aisles, sections) uses a single `location_zones` table — a store with one floor and no zones simply has no rows here.

Space domain is planogram + assortment. `planograms` is the master plan; `planogram_positions` carries the where-does-each-item-sit detail (the S078 "most architecturally rich" interface payload). `location_assortment` answers "what items does this store carry?" — same role as GSLM's `SKUItemsInSalesOutlets` + `PackItemsInSalesOutlets`, simplified to one table with item_type discriminator.

Operational lifecycle in TOM: store master flows IDS → CRDB / IKB / GPM nightly batch (S008, S035, S074). Planogram flows IKB → GPM + SR daily transactional fan-out (S078). For Canary Go MCP services these become real-time push junctions with same producer/consumer relationships but modern cadence (real-time master propagation, daily planogram sync).

---

## l.locations

**ARTS reference**: ARTS Location V2 — RetailStore + Warehouse + DistributionCenter unified.
**Module**: A.

### Schema

```sql
CREATE TABLE l.locations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  location_code       text NOT NULL,                              -- merchant or POS-native code (StoreNo equivalent)
  name                text NOT NULL,
  location_type       text NOT NULL DEFAULT 'store',              -- store | warehouse | distribution_center | dropship | virtual | popup
  parent_location_id  uuid REFERENCES l.locations(id),            -- e.g., distribution center serves stores; nullable
  banner              text,                                        -- merchant banner if multi-banner
  status              text NOT NULL DEFAULT 'active',             -- active | inactive | closed | construction | pending_open
  open_date           date,
  close_date          date,
  remodel_date        date,
  square_footage      int,
  selling_area_sqft   int,
  storage_area_sqft   int,
  channel             text,                                        -- brick | online | hybrid | popup
  format              text,                                        -- supermarket | convenience | specialty | warehouse | etc.
  currency            text NOT NULL DEFAULT 'USD',
  language            text NOT NULL DEFAULT 'en-US',               -- BCP 47
  timezone            text NOT NULL DEFAULT 'America/Los_Angeles', -- IANA
  address             jsonb DEFAULT '{}',                          -- {line1, line2, city, region, postal_code, country, latitude, longitude, county}
  contact             jsonb DEFAULT '{}',                          -- {name, phone, email, manager_name}
  operating_hours     jsonb DEFAULT '{}',                          -- {monday: [{open: "07:00", close: "22:00"}], ...}
  attributes          jsonb NOT NULL DEFAULT '{}',                 -- merchant-defined (e.g., DUNS, integrated POS ind, MSA)
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_code)
);

CREATE INDEX idx_locations_tenant ON l.locations(tenant_id);
CREATE INDEX idx_locations_parent ON l.locations(parent_location_id);
CREATE INDEX idx_locations_status ON l.locations(status) WHERE status != 'active';
CREATE INDEX idx_locations_type ON l.locations(location_type);
CREATE INDEX idx_locations_address_gin ON l.locations USING gin(address);
```

### Operational lifecycle (TOM operational clock)

**Producers**:
- `mcp.master.location.create` — real-time on store/warehouse onboard (TOM C012US Store + C013US Warehouse RIB pattern)
- `mcp.master.location.update` — real-time on attribute change

**Consumers**:
- `mcp.transaction.location.lookup` — real-time at every POS event
- `mcp.inventory.location.position-init` — when location added (initialize inventory positions)
- `mcp.orders.location.assign` — at order routing
- `mcp.forecast.location.refresh` — daily (TOM J087 Store Info IL→GFO equivalent)
- `mcp.planogram.location.assign` — when location ready for planograms
- `mcp.metrics.aggregate.by-location`
- `mcp.store-line.location-attributes-push` — overnight (TOM S035 IDS→IKB pattern)

**SLA at producer**: real-time push, p95 < 200ms, idempotent on `(tenant_id, location_code)`.
**SLA at consumers**: lookup p99 < 50ms; freshness < 5s for transaction, < 24h for forecast/planogram.

### Provenance

- **ARTS reference**: Location V2 — RetailStore + Warehouse + DistributionCenter all conform to one Location entity with type discriminator
- **GSLM folded**: `SalesOutlets` + (parts of) `SalesOutletDepartments` + `SalesOutletHolidays` — operating hours + holidays into JSONB; departments into `location_zones` separately
- **TOM junctions**: C012US (store), C013US (warehouse), S035 (downstream IKB), S008 (downstream CRDB), S074 (downstream GPM), J087 (downstream GFO)
- **Canary current**: `app.locations` + `app.location_hierarchy` — superseded; hierarchy moves to separate table for clarity
- **Justification**: Single `locations` table covers store/warehouse/DC because they share 80% of attributes; type discriminator and partial indexes optimize query paths. Address/contact/operating_hours in JSONB because they're rarely queried structurally and vary by region (US has state, UK has county, JP has prefecture). Latitude/longitude in address JSONB enables geospatial queries with Postgres `point` cast or PostGIS extension.

---

## l.location_hierarchy

**ARTS reference**: ARTS Location V2 — LocationHierarchy.
**Module**: A.

### Schema

```sql
CREATE TABLE l.location_hierarchy (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES l.location_hierarchy(id),     -- NULL for root
  code            text NOT NULL,                                  -- e.g., "WEST_REGION", "DIST_LA_NORTH"
  name            text NOT NULL,
  hierarchy_type  text NOT NULL DEFAULT 'organizational',         -- organizational | distribution | banner | tax_zone
  level           int NOT NULL,                                   -- denormalized depth
  path            ltree,                                          -- materialized path
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, hierarchy_type, code)
);

CREATE TABLE l.location_hierarchy_assignments (
  location_id     uuid NOT NULL REFERENCES l.locations(id) ON DELETE CASCADE,
  hierarchy_id    uuid NOT NULL REFERENCES l.location_hierarchy(id) ON DELETE CASCADE,
  PRIMARY KEY (location_id, hierarchy_id)
);

CREATE INDEX idx_loc_hier_tenant ON l.location_hierarchy(tenant_id);
CREATE INDEX idx_loc_hier_parent ON l.location_hierarchy(parent_id);
CREATE INDEX idx_loc_hier_path ON l.location_hierarchy USING gist(path);
CREATE INDEX idx_loc_hier_assign_loc ON l.location_hierarchy_assignments(location_id);
CREATE INDEX idx_loc_hier_assign_hier ON l.location_hierarchy_assignments(hierarchy_id);
```

### Operational lifecycle

**Producers**:
- `mcp.master.location-hierarchy.upsert` — real-time when hierarchy node changes (TOM J002 Location Hierarchy IDS→UDD)
- `mcp.master.location-hierarchy.assign` — real-time when location added to hierarchy node

**Consumers**:
- `mcp.metrics.aggregate.by-hierarchy` — rollup queries (district sales, region performance)
- `mcp.pricing.zone-promotion.scope-by-hierarchy` — promotions targeting a region/banner
- `mcp.financial.tax-zone.resolve` — tax computation

**SLA at producer**: real-time, p95 < 200ms.

### Provenance

- **ARTS reference**: LocationHierarchy
- **GSLM folded**: hierarchy was inline columns on `SalesOutlets` (banner, region) — promoted to separate hierarchy table for multi-hierarchy support
- **Recovery DDL**: `Ref_LocationHierarchy` (from `wmt-ref-location-create.sql`) — Walmart's reference hierarchy, validates the structure
- **TOM junctions**: J002 (Location Hierarchy IDS→UDD), J006 (Commercial Hierarchy IDS→UDD)
- **Justification**: Multiple hierarchy types coexist (organizational, distribution, tax zones, banners). Many-to-many assignments table because a single store may belong to multiple hierarchies (e.g., West Region for ops, Tax Zone CA for finance, Banner X for merchandising).

---

## l.location_zones

**ARTS reference**: ARTS RetailStore — within-store geography.
**Module**: A (with cross-cuts to S for planogram positioning).

### Schema

```sql
CREATE TABLE l.location_zones (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  location_id     uuid NOT NULL REFERENCES l.locations(id) ON DELETE CASCADE,
  parent_zone_id  uuid REFERENCES l.location_zones(id),
  code            text NOT NULL,                          -- e.g., "FLOOR_1", "GROCERY_AISLE_3", "ENDCAP_5"
  name            text NOT NULL,
  zone_type       text NOT NULL DEFAULT 'department',     -- floor | department | aisle | section | endcap | bin | shelf | cooler
  level           int NOT NULL,                            -- depth within location
  path            ltree,
  geometry        jsonb DEFAULT '{}',                      -- {coordinates, dimensions} for store-mapping later
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, code)
);

CREATE INDEX idx_zones_tenant ON l.location_zones(tenant_id);
CREATE INDEX idx_zones_location ON l.location_zones(location_id);
CREATE INDEX idx_zones_parent ON l.location_zones(parent_zone_id);
CREATE INDEX idx_zones_path ON l.location_zones USING gist(path);
```

### Operational lifecycle

**Producers**:
- `mcp.master.location-zone.upsert` — when store layout configured
- `mcp.master.location-zone.from-planogram-derive` — planogram-driven zone creation

**Consumers**:
- `mcp.planogram.position.assign-zone` — every planogram position references a zone
- `mcp.transaction.line-item.zone-attribute` — for shrink/loss-by-zone analytics
- `mcp.inventory.cycle-count.scope-by-zone`

**SLA at producer**: not time-critical (configuration data).

### Provenance

- **ARTS reference**: ARTS extends Location with sub-zone concepts (departments, sections)
- **GSLM folded**: `SalesFloors` + `SalesFloorsInSalesOutlet` + `Sections` + `SalesOutletDepartments` (4 entities → 1 with `zone_type` discriminator + recursive parent)
- **TOM junctions**: C012US carries store hierarchy fields that imply zones; explicit zone management is in S033 IDS→IKB pattern
- **Justification**: Single recursive table with discriminator handles 0-level (no zones) through N-level (floor → dept → aisle → shelf → bin) without schema change. Geometry as JSONB allows future store-mapping/AR overlays without column changes.

---

## s.planograms

**ARTS reference**: ARTS Planogram V2 (LCWD ARTS Planogram Domain Model V2.0 — local PDF).
**Module**: S.

### Schema

```sql
CREATE TABLE s.planograms (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  planogram_code      text NOT NULL,                      -- merchant-assigned (e.g., "GROCERY_GR_VALID_24Q1")
  name                text NOT NULL,
  category_id         uuid REFERENCES m.product_categories(id),  -- the merchandise category this plans
  effective_start     date,
  effective_end       date,                               -- NULL for indefinite
  layout_dimensions   jsonb DEFAULT '{}',                 -- {width_cm, height_cm, depth_cm, shelf_count, etc.}
  status              text NOT NULL DEFAULT 'draft',      -- draft | approved | active | retired
  approved_by         uuid REFERENCES app.users(id),
  approved_at         timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, planogram_code)
);

CREATE TABLE s.planogram_assignments (
  planogram_id    uuid NOT NULL REFERENCES s.planograms(id) ON DELETE CASCADE,
  location_id     uuid NOT NULL REFERENCES l.locations(id) ON DELETE CASCADE,
  zone_id         uuid REFERENCES l.location_zones(id),
  assigned_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (planogram_id, location_id, COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid))
);

CREATE INDEX idx_planograms_tenant ON s.planograms(tenant_id);
CREATE INDEX idx_planograms_category ON s.planograms(category_id);
CREATE INDEX idx_planograms_status ON s.planograms(status);
CREATE INDEX idx_planogram_assign_loc ON s.planogram_assignments(location_id);
```

### Operational lifecycle

**Producers**:
- `mcp.space.planogram.upsert` — when planogram designed (in JDA-equivalent or merchant tool)
- `mcp.space.planogram.assign-location` — when planogram applied to a store/zone

**Consumers**:
- `mcp.space.position.lookup` (FK from planogram_positions)
- `mcp.store-line.planogram-push` — daily (TOM S078 IKB→GPM+SR pattern)
- `mcp.inventory.replenishment.use-planogram-capacity` — capacity-driven replenishment (TOM S075 GPM→SR pattern)

**SLA at producer**: not time-critical (planning data); idempotent on `(tenant_id, planogram_code)`.

### Provenance

- **ARTS reference**: ARTS Planogram V2 — local PDF spec
- **GSLM folded**: `Planogram` (1 table — minimal in GSLM SQL implementation; richer in S0 MDM site Space domain)
- **TOM junctions**: S078 (IKB→GPM+SR — the "most architecturally rich SRD interface"), S057 (Store Range Data IKB→SR), S075 (Capacity GPM→SR)
- **Justification**: Composite PK on assignments uses COALESCE-on-zone trick to allow location-wide planograms (zone_id NULL) and zone-specific planograms in same table. Layout dimensions in JSONB because planogram layout systems vary (grid-based, freeform, 3D coordinate); merchant can switch tools without schema change.

---

## s.planogram_positions

**ARTS reference**: ARTS Planogram V2 — Position / Facing.
**Module**: S.

### Schema

```sql
CREATE TABLE s.planogram_positions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  planogram_id        uuid NOT NULL REFERENCES s.planograms(id) ON DELETE CASCADE,
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,
  shelf_number        int,                                -- 1=top, N=bottom
  position_on_shelf   int,                                -- 1=left, N=right
  facings             int NOT NULL DEFAULT 1,             -- horizontal facings count
  capacity_units      int,                                -- max units this position holds
  orientation         text DEFAULT 'face_forward',        -- face_forward | sideways | hanging | etc.
  geometry            jsonb DEFAULT '{}',                 -- {x_cm, y_cm, width_cm, height_cm}
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, planogram_id, item_id, shelf_number, position_on_shelf)
);

CREATE INDEX idx_positions_tenant ON s.planogram_positions(tenant_id);
CREATE INDEX idx_positions_planogram ON s.planogram_positions(planogram_id);
CREATE INDEX idx_positions_item ON s.planogram_positions(item_id);
```

### Operational lifecycle

**Producers**:
- `mcp.space.position.upsert` — when planogram positions defined

**Consumers**:
- `mcp.store-line.position-push` — daily (TOM S078 transactional fan-out to GPM+SR)
- `mcp.inventory.replenishment.calc-by-position` — uses capacity for replenishment qty
- `mcp.shelf-edge.label-print` — TOM S075 GPM→SR capacity drives label print

**SLA at producer**: not time-critical (planning); high volume per planogram (50-500 positions per planogram typical).
**SLA at consumers**: daily push, transactional fan-out across two downstream targets (GPM and SR per S078 pattern).

### Provenance

- **ARTS reference**: ARTS Planogram V2 — Position / Facing detail
- **GSLM folded**: not present in GSLM SQL; in S0 MDM Space domain
- **TOM junctions**: S078 (IKB→GPM+SR — the rich one), S075 (GPM→SR capacity), S077 (GFO→SR actual range feedback)
- **Justification**: Single position-per-row enables flexibility (item-on-shelf, item-in-bin, item-on-endcap all share the same row shape). Geometry JSONB because positioning is rarely queried structurally beyond same-shelf adjacency.

---

## l.location_assortment

**ARTS reference**: ARTS Item-Location association (Range / Assortment).
**Module**: M (item-side) + A (location-side) + S (planogram-side).

### Schema

```sql
CREATE TABLE l.location_assortment (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  location_id         uuid NOT NULL REFERENCES l.locations(id) ON DELETE CASCADE,
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
  zone_id             uuid REFERENCES l.location_zones(id),
  assortment_tier     text NOT NULL DEFAULT 'store_carry',  -- store_carry | warehouse_only | expanded_storefront | dropship | deleted
  effective_start     date,
  effective_end       date,
  source_planogram_id uuid REFERENCES s.planograms(id),     -- if assortment driven by planogram
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, item_id, COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid))
);

CREATE INDEX idx_assortment_tenant ON l.location_assortment(tenant_id);
CREATE INDEX idx_assortment_location ON l.location_assortment(location_id);
CREATE INDEX idx_assortment_item ON l.location_assortment(item_id);
CREATE INDEX idx_assortment_active ON l.location_assortment(location_id, status) WHERE status = 'active';
```

### Operational lifecycle

**Producers**:
- `mcp.assortment.upsert` — when item authorized for location (manual or planogram-derived)
- `mcp.assortment.from-planogram-derive` — automatic from planogram positions
- `mcp.assortment.lifecycle-event` — new product launch / discontinuation (TOM S047 launches/delists)

**Consumers**:
- `mcp.transaction.scan-validate` — verify item is sellable at this location
- `mcp.orders.replenishment.scope-by-assortment` — only order what's carried
- `mcp.inventory.position.scope-by-assortment` — only track for assorted items
- `mcp.forecast.demand.scope-by-assortment` (TOM J009 Authorised Range)
- `mcp.metrics.lost-sale.detect` — scan for non-assorted item = lost sale signal

**SLA at producer**: real-time on assortment change.
**SLA at consumers**: lookup p99 < 30ms (real-time during POS scan).

### Provenance

- **ARTS reference**: ARTS Item-Location association (Range)
- **GSLM folded**: `SKUItemsInSalesOutlets` + `PackItemsInSalesOutlets` (2 entities → 1 with item-type-discriminator already on `m.items`)
- **TOM junctions**: J009 (Authorised Range and Shelf Capacities GFO→UDD), S047 (Store Range, Capacity, New/Discontinued SR→GFO), S077 (Actual Range GFO→SR)
- **Memory reference**: `project_multi_tier_assortment_model` — three-tier model (store / warehouse / expanded) — implemented via `assortment_tier` column
- **Justification**: Single assortment table with `assortment_tier` column captures the founder's documented three-tier model (store/warehouse/expanded) plus dropship and deleted states. `source_planogram_id` traceability lets assortment changes be reversed if planogram retired. COALESCE-PK pattern allows location-wide and zone-specific assortment in same table.

---

## Domain summary

**6 entities, 2 schemas (l, s), 2 modules (A + S)**:
- `l.locations` (~24 cols) — store/warehouse/DC unified
- `l.location_hierarchy` + `l.location_hierarchy_assignments` (~10 + 2 cols) — multi-hierarchy
- `l.location_zones` (~12 cols) — within-location recursive
- `s.planograms` + `s.planogram_assignments` (~14 + 4 cols) — master + location binding
- `s.planogram_positions` (~13 cols) — item placement detail
- `l.location_assortment` (~13 cols) — item-location authorization with multi-tier

**Folded from sources**:
- GSLM 16 Location entities → 3 (`l.locations` collapses store/warehouse/DC; `l.location_zones` collapses 4 inside-store entities; hierarchy promoted to separate)
- GSLM 8 Space entities → 3 (`s.planograms` master, `s.planogram_positions` detail, `l.location_assortment` for the SKU-in-store relationship)

**MCP service junctions defined for this domain (~16)**:
- Producers: location create/update, hierarchy upsert/assign, zone upsert, planogram upsert/assign-location, position upsert, assortment upsert/from-planogram/lifecycle-event
- Consumers: transaction lookup, scan-validate, inventory position-init, orders assign + replenishment scope, forecast refresh, planogram push, position push (transactional fan-out per S078), shelf-edge label print, metrics aggregate by location/hierarchy, lost-sale detect

## Status

- **Chunk 3 complete.** 6 ARTS-Location-V2-aligned entities with TOM S-Prefix lifecycle bindings.
- **Resume**: Chunk 4 — Party domain (Customer + Employee + Vendor cross-reference). ARTS Party hierarchy. Target ~5-7 entities.

---

# §5 Customer + Employee Domain

**ARTS anchor**: ARTS Party (abstract) → Customer + Employee (Vendor already covered in `m.vendors`, Chunk 2).
**Modules**: C (Customer), L (Labor / People).
**Entities**: 6 (customers · customer_addresses · loyalty_memberships · employees · employee_role_assignments · employee_location_assignments).
**Folded from sources**: GSLM 14 Customer + 4 People entities → 6 SMB-2030 canonical.

## Domain narrative

ARTS Party is an abstract supertype: Customer, Employee, Vendor, and Organization all conform to its base shape (id, name, contact info). For SMB-2030 we don't materialize the Party supertype as a single table (tenant-isolation patterns differ by subtype, and SMB rarely queries "all parties") — but we maintain ARTS naming and the Party-derived attribute conventions so future federation (e.g., a customer who's also an employee) is clean.

Customer is **thin by default** for SMB: most transactions are anonymous, loyalty enrollments are phone-or-email only, full profiles are the exception not the rule. Schema supports the sparse case (most fields nullable) without paying decomposition cost. Loyalty is a sub-domain of Customer (one canonical entity, supports multi-program later).

Employee in SMB-2030 is similarly thin: a small staff (1-50 people typical), most without complex role hierarchies. We keep ARTS-aligned role and location-assignment as many-to-many tables because both are queried structurally (RBAC checks, manager-of-store reports), but neither is over-decomposed.

GSLM gave us 14 Customer entities and 4 People entities — we fold most into the master rows or JSONB. CRDM 1.7.2 had `CRDM_Customer` (dropped in 1.8) — operational customer-touchpoint detail; we capture this via the loyalty/transaction join, not a separate operational customer table.

TOM operational lifecycle: notably **TOM had no P-Prefix or R-Prefix interfaces** (People and Retail folders empty in S9 corpus). Customer and Employee master data lived in separate enterprise systems Tesco didn't expose via the integration layer corpus we have. Canary Go MCP services for these domains are designed fresh — the lifecycle bindings are net-new, not derived from TOM fingerprints.

---

## c.customers

**ARTS reference**: ARTS Customer (Party subtype).
**Module**: C.

### Schema

```sql
CREATE TABLE c.customers (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  customer_code   text,                                  -- merchant-assigned (loyalty number, account number); nullable for anonymous walk-in
  customer_type   text NOT NULL DEFAULT 'individual',    -- individual | business | household | guest
  first_name      text,
  last_name       text,
  display_name    text,                                  -- computed or business name
  email           text,                                  -- primary email (PII tier 2)
  phone           text,                                  -- primary phone (PII tier 2; E.164 format)
  birth_date      date,                                  -- for age-restriction verification + birthday promos (PII tier 3)
  preferred_language text DEFAULT 'en-US',
  marketing_opt_in   boolean NOT NULL DEFAULT false,     -- explicit consent
  primary_address jsonb DEFAULT '{}',                    -- {line1, line2, city, region, postal_code, country}
  attributes      jsonb NOT NULL DEFAULT '{}',           -- demographics, segments, merchant-defined
  status          text NOT NULL DEFAULT 'active',        -- active | inactive | suppressed | merged
  merged_into     uuid REFERENCES c.customers(id),       -- for dedup / merge events
  external_ids    jsonb DEFAULT '{}',                    -- {pos_native_id, square_id, stripe_customer_id, etc.}
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, customer_code) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX idx_customers_tenant ON c.customers(tenant_id);
CREATE INDEX idx_customers_email ON c.customers(tenant_id, lower(email)) WHERE email IS NOT NULL AND status = 'active';
CREATE INDEX idx_customers_phone ON c.customers(tenant_id, phone) WHERE phone IS NOT NULL AND status = 'active';
CREATE INDEX idx_customers_status ON c.customers(status) WHERE status != 'active';
CREATE INDEX idx_customers_attributes ON c.customers USING gin(attributes);
CREATE INDEX idx_customers_external_ids ON c.customers USING gin(external_ids);
```

### Operational lifecycle (TOM operational clock)

**Producers**:
- `mcp.customer.create` — at first touchpoint (loyalty enrollment, account creation, B2B onboard)
- `mcp.customer.update` — profile changes
- `mcp.customer.merge` — dedup event (sets `status='merged'` and `merged_into` on duplicate)
- `mcp.customer.from-pos-native-sync` — when POS-native customer record exists (Square Customer, Stripe Customer, Counterpoint AR_CUST)

**Consumers**:
- `mcp.transaction.customer.lookup` — at POS for loyalty redemption, age verification (real-time, p99 < 30ms)
- `mcp.loyalty.points.compute` — earn/redeem at transaction time
- `mcp.marketing.campaign.scope-by-segment` — opt-in audiences
- `mcp.metrics.customer-aggregate` — RFM, LTV, segment rollups
- `mcp.compliance.consent-audit` — GDPR/CCPA consent tracking

**SLA at producer**: real-time, p95 < 200ms, idempotent on `(tenant_id, customer_code)` OR `(tenant_id, lower(email))` OR `(tenant_id, phone)` depending on touchpoint.
**SLA at consumers**: lookup p99 < 30ms; freshness < 5s for transaction-time lookups.

### Provenance

- **ARTS reference**: Customer (Party subtype)
- **GSLM Customer (S0)**: 14 entities folded — Customer master + addresses + contacts + segments + loyalty + preferences (most into JSONB; multi-address gets its own table below; loyalty gets its own table)
- **CRDM 1.7.2**: `CRDM_Customer` (dropped in 1.8) — was operational POS-touch customer record; we don't replicate that operational role (transaction join provides it)
- **TOM junctions**: none (P-Prefix empty); Canary Go customer junctions are net-new
- **Canary current**: `app.customers` — superseded
- **Justification**: Sparse row design (almost all fields nullable) supports anonymous-walk-in through full B2B-account merchants without schema variance. `external_ids` JSONB enables clean federation with POS-native customer records (Square, Stripe, Counterpoint) without per-source columns. Conditional unique indexes on email/phone prevent duplicates only for active rows. DEFERRABLE constraint on `customer_code` allows merge transactions that temporarily violate uniqueness during cleanup.

---

## c.customer_addresses

**ARTS reference**: ARTS Customer Address (multiple per customer).
**Module**: C.

### Schema

```sql
CREATE TABLE c.customer_addresses (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  customer_id     uuid NOT NULL REFERENCES c.customers(id) ON DELETE CASCADE,
  address_type    text NOT NULL DEFAULT 'shipping',      -- shipping | billing | mailing | service | pickup
  recipient_name  text,
  line_1          text NOT NULL,
  line_2          text,
  city            text NOT NULL,
  region          text,                                   -- state/province/county
  postal_code     text,
  country         text NOT NULL DEFAULT 'US',             -- ISO 3166 alpha-2
  latitude        numeric(10,7),
  longitude       numeric(10,7),
  is_default      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT one_default_per_type EXCLUDE (customer_id WITH =, address_type WITH =) WHERE (is_default = true AND status = 'active')
);

CREATE INDEX idx_addresses_tenant ON c.customer_addresses(tenant_id);
CREATE INDEX idx_addresses_customer ON c.customer_addresses(customer_id);
CREATE INDEX idx_addresses_type_default ON c.customer_addresses(customer_id, address_type) WHERE is_default = true;
```

### Operational lifecycle

**Producers**:
- `mcp.customer-address.add` — when customer adds shipping/billing address
- `mcp.customer-address.from-order-derive` — auto-add shipping address from a delivery order

**Consumers**:
- `mcp.orders.shipping.resolve-address`
- `mcp.financial.invoice.bill-to-address`
- `mcp.marketing.geo-segment`

**SLA at producer**: real-time. EXCLUDE constraint enforces single default per address type per customer.

### Provenance

- **ARTS reference**: Customer Address (multiple per Customer)
- **GSLM folded**: GSLM Customer domain had separate `CustomerAddress` entities — preserved (most SMB merchants need it for B2B accounts and ship-to)
- **TOM junctions**: none (no Customer-domain TOM interfaces)
- **Justification**: Separate table because SMB B2B customers genuinely have multiple ship-to addresses (corporate office, multiple warehouses, multiple stores). Pure B2C merchants can skip this table — `c.customers.primary_address` JSONB covers the single-address case.

---

## c.loyalty_memberships

**ARTS reference**: ARTS Loyalty (Membership + Program).
**Module**: C.

### Schema

```sql
CREATE TABLE c.loyalty_memberships (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  customer_id         uuid NOT NULL REFERENCES c.customers(id) ON DELETE CASCADE,
  program_code        text NOT NULL DEFAULT 'default',   -- merchant may run multiple programs
  membership_number   text NOT NULL,                     -- the loyalty card / member ID
  enrollment_date     date NOT NULL DEFAULT CURRENT_DATE,
  tier                text DEFAULT 'standard',           -- standard | silver | gold | platinum | etc.
  points_balance      bigint NOT NULL DEFAULT 0,         -- current available points
  points_lifetime     bigint NOT NULL DEFAULT 0,         -- cumulative earned (informational)
  birth_date          date,                              -- for birthday promos (denormalized from customer for query speed)
  preferences         jsonb DEFAULT '{}',                -- communication prefs, category interests
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',    -- active | suspended | expired | closed
  expires_at          timestamptz,                        -- if program has expiration
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, program_code, membership_number),
  UNIQUE (tenant_id, customer_id, program_code)            -- one membership per customer per program
);

CREATE INDEX idx_loyalty_tenant ON c.loyalty_memberships(tenant_id);
CREATE INDEX idx_loyalty_customer ON c.loyalty_memberships(customer_id);
CREATE INDEX idx_loyalty_member_lookup ON c.loyalty_memberships(tenant_id, membership_number) WHERE status = 'active';
CREATE INDEX idx_loyalty_tier ON c.loyalty_memberships(tier) WHERE status = 'active';
```

### Operational lifecycle

**Producers**:
- `mcp.loyalty.enroll` — at sign-up
- `mcp.loyalty.points-earn` — at transaction completion
- `mcp.loyalty.points-redeem` — at transaction completion when points used as tender
- `mcp.loyalty.tier-evaluate` — periodic (monthly) tier recalc
- `mcp.loyalty.expire` — when membership lapses

**Consumers**:
- `mcp.transaction.loyalty.lookup` — at POS, p99 < 30ms (every loyalty-tender or earn)
- `mcp.marketing.tier-segment` — campaign targeting by tier
- `mcp.metrics.loyalty-engagement`

**SLA at producer**: real-time for earn/redeem; idempotent (use transaction_id for earn deduplication).

### Provenance

- **ARTS reference**: ARTS Loyalty (Membership + Program — we fold both into single membership entity; multi-program supported via `program_code`)
- **GSLM folded**: GSLM Customer domain loyalty entities collapsed (~3 entities → 1 + JSONB preferences)
- **CRDM**: `CRDM_LoyaltyCard` is the operational POS-touch event (lives in T schema as part of transaction); not the master record
- **TOM junctions**: none for master; J035 Actual Sales TDS→GFO carries loyalty signals downstream (we'll cross-reference in Chunk 7)
- **Justification**: `points_balance` denormalized on the membership row (not summed from a points-transaction log) — read performance at every POS scan beats audit-trail purity. Points-transaction log lives in `t.loyalty_events` (Chunk 7) for audit/recompute.

---

## e.employees

**ARTS reference**: ARTS Employee (Party subtype).
**Module**: L.

### Schema

```sql
CREATE TABLE e.employees (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  user_id             uuid REFERENCES app.users(id),         -- if employee has a Canary login (managers, supervisors); nullable for cashiers without login
  employee_code       text NOT NULL,                          -- POS cashier number, badge ID
  first_name          text NOT NULL,
  last_name           text NOT NULL,
  display_name        text,
  email               text,                                   -- work email (PII tier 2)
  phone               text,                                   -- (PII tier 2)
  hire_date           date NOT NULL,
  termination_date    date,
  employment_status   text NOT NULL DEFAULT 'active',        -- active | on_leave | terminated | seasonal | applicant
  pay_type            text,                                   -- hourly | salaried | contract | tipped (no actual pay rate stored — sensitive)
  attributes          jsonb NOT NULL DEFAULT '{}',           -- merchant-defined fields (badge color, training certs)
  external_ids        jsonb DEFAULT '{}',                    -- payroll system ID, POS-native cashier ID
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_code)
);

CREATE INDEX idx_employees_tenant ON e.employees(tenant_id);
CREATE INDEX idx_employees_user ON e.employees(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_employees_status ON e.employees(employment_status) WHERE employment_status != 'active';
CREATE INDEX idx_employees_external_ids ON e.employees USING gin(external_ids);
```

### Operational lifecycle

**Producers**:
- `mcp.employee.hire` — onboard event
- `mcp.employee.update` — profile change
- `mcp.employee.terminate` — soft-terminate (sets `employment_status='terminated'`, `termination_date=today`)
- `mcp.employee.from-pos-native-sync` — sync from POS (Counterpoint USR_FILE / Square Team Member)

**Consumers**:
- `mcp.transaction.employee.lookup` — every POS transaction tagged with cashier_id
- `mcp.audit.employee-action.attribute` — for OperatorAction (CRDM equivalent), shrink investigations
- `mcp.scheduling.employee-roster` — labor scheduling (future)
- `mcp.metrics.employee-performance` — sales per hour, transactions per shift

**SLA at producer**: real-time, p95 < 200ms.

### Provenance

- **ARTS reference**: Employee (Party subtype)
- **GSLM folded**: 4 People entities collapsed (most fields into employee row + role/location assignments below)
- **CRDM operational**: `CRDM_OperatorAction` and `CRDM_StaffDiscount` reference cashier_no — we tie into `e.employees.employee_code` for cashier identification
- **TOM junctions**: none (P-Prefix empty); Canary Go is greenfield here
- **Canary current**: `app.employees` — superseded
- **Justification**: No pay rate stored (sensitive — separate payroll system, not retail platform's role). Employee-to-Canary-user link nullable because cashiers may not have logins (just clock in via POS). Soft-termination preserves audit trail.

---

## e.employee_role_assignments

**ARTS reference**: ARTS Employee Role.
**Module**: L.

### Schema

```sql
CREATE TABLE e.employee_role_assignments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  employee_id     uuid NOT NULL REFERENCES e.employees(id) ON DELETE CASCADE,
  role_code       text NOT NULL,                              -- cashier | shift_lead | manager | gm | inventory_lead | etc.
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,                                       -- NULL = current
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_id, role_code, effective_start)
);

CREATE INDEX idx_emp_roles_tenant ON e.employee_role_assignments(tenant_id);
CREATE INDEX idx_emp_roles_employee ON e.employee_role_assignments(employee_id);
CREATE INDEX idx_emp_roles_active ON e.employee_role_assignments(employee_id, role_code) WHERE effective_end IS NULL;
```

### Operational lifecycle

**Producers**:
- `mcp.employee-role.assign` — at promotion / role change
- `mcp.employee-role.expire` — when role ended (sets `effective_end`)

**Consumers**:
- `mcp.access-control.role-check` — RBAC at every Canary action
- `mcp.transaction.role-validate` — e.g., "manager override" requires manager role
- `mcp.audit.role-change-history`

**SLA at producer**: real-time.

### Provenance

- **ARTS reference**: Employee Role (with effective dating)
- **GSLM**: not richly modeled in People domain (4 entities only)
- **TOM junctions**: none
- **Justification**: Effective-dated role assignments support audit ("who was a manager on date X?"). Role code is a string, not a FK to a roles table — RBAC permissions are configured per-role in `app.roles` (already exists in current Canary spec); this table is the assignment relation.

---

## e.employee_location_assignments

**ARTS reference**: ARTS Employee-Store assignment.
**Module**: L.

### Schema

```sql
CREATE TABLE e.employee_location_assignments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  employee_id     uuid NOT NULL REFERENCES e.employees(id) ON DELETE CASCADE,
  location_id     uuid NOT NULL REFERENCES l.locations(id) ON DELETE CASCADE,
  assignment_type text NOT NULL DEFAULT 'home',                 -- home | rotating | temporary | floating
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,
  is_primary      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_id, location_id, effective_start),
  CONSTRAINT one_primary_per_employee EXCLUDE (employee_id WITH =) WHERE (is_primary = true AND effective_end IS NULL)
);

CREATE INDEX idx_emp_loc_tenant ON e.employee_location_assignments(tenant_id);
CREATE INDEX idx_emp_loc_employee ON e.employee_location_assignments(employee_id);
CREATE INDEX idx_emp_loc_location ON e.employee_location_assignments(location_id);
CREATE INDEX idx_emp_loc_active ON e.employee_location_assignments(employee_id, location_id) WHERE effective_end IS NULL;
```

### Operational lifecycle

**Producers**:
- `mcp.employee-location.assign` — at hire / transfer
- `mcp.employee-location.expire` — at transfer-out / termination

**Consumers**:
- `mcp.transaction.employee-at-location.validate` — was this cashier authorized to ring up at this store?
- `mcp.scheduling.location-staff-roster`
- `mcp.metrics.location-staff-headcount`

**SLA at producer**: real-time.

### Provenance

- **ARTS reference**: Employee-Location assignment
- **GSLM**: 4 People entities — folded
- **TOM junctions**: none
- **Canary current**: `app.employee_location_assignments` already exists — preserved as-is, ARTS-aligned name retained
- **Justification**: EXCLUDE constraint enforces single primary location per active employee. Effective-dating supports transfer history. `assignment_type` discriminates "home store" from "covers shifts elsewhere."

---

## Domain summary

**6 entities, 2 schemas (c, e), 2 modules (C + L)**:
- `c.customers` (~22 cols) — sparse-by-default master
- `c.customer_addresses` (~16 cols) — multi-address (optional for B2B)
- `c.loyalty_memberships` (~16 cols) — denormalized points balance, multi-program
- `e.employees` (~16 cols) — no pay rate stored
- `e.employee_role_assignments` (~9 cols) — effective-dated
- `e.employee_location_assignments` (~10 cols) — primary-location EXCLUDE constraint

**Folded from sources**:
- GSLM 14 Customer entities → 3 (customer master + addresses + loyalty)
- GSLM 4 People entities → 3 (employee master + role assignments + location assignments)
- CRDM 1.7.2 `CRDM_Customer` was operational POS-touch — covered by transaction join, not separate operational table

**Net new** (no source has):
- `c.loyalty_memberships` multi-program support — most sources assumed single program; SMB-2030 may need program-per-channel or program-per-banner
- Effective-dated role and location assignments — TOM never modeled this (P-Prefix empty); designed fresh

**MCP service junctions defined for this domain (~16)**:
- Producers: customer create/update/merge/from-pos-sync, customer-address add/from-order, loyalty enroll/earn/redeem/tier-evaluate/expire, employee hire/update/terminate/from-pos-sync, employee-role assign/expire, employee-location assign/expire
- Consumers: transaction customer-lookup, loyalty points-compute, marketing campaign-scope, metrics aggregate, compliance consent-audit, transaction employee-lookup, audit role-change-history, access-control role-check, scheduling roster

## Status

- **Chunk 4 complete.** 6 ARTS-Party-aligned entities (Customer + Employee). Vendor already in Chunk 2's `m.vendors`.
- **Resume**: Chunk 5 — Inventory + Distribution domain (i schema). ARTS Inventory V2 anchor (we have full PDF spec) + D-Prefix lifecycle. Target ~6-8 entities.

---

# §6 Inventory + Distribution Domain

**ARTS anchor**: ARTS IXRetail Inventory V1.0 (full local PDF) + InventoryV2.0.0.xsd + ARTS movement / adjustment / receipt / transfer entities.
**Modules**: D (Distribution), with cross-cuts to F (financial valuation in `ledger.stock_ledger_entries`).
**Entities**: 5 (inventory_positions · inventory_movements · inventory_documents · inventory_document_lines · inventory_lots).
**Folded from sources**: CRDM 5 inventory entities + GSLM Supply 19 entities + D-Prefix 18 interfaces → 5 SMB-2030 canonical.

## Domain narrative

ARTS Inventory V2 separates the **state** (current stock-on-hand) from the **events** (what changed it). SMB-2030 keeps that clean split — `inventory_positions` is the rolling balance you query at every POS scan; `inventory_movements` is the append-only event log of every SOH delta. Documents (`inventory_documents` + `_lines`) capture the business intent behind movements — a goods-received note, a stock-count, a transfer-out — with line-level detail. Most movements derive from a document; some are direct (manual adjustment, sale-derived auto-decrement).

The D-Prefix TOM interfaces gave us 18 distinct operational flows for a single retail estate — split because Tesco had 4 named target systems (RMS, ORMS, GFO, TIMS) and each operation needed a copy in each system. Canary Go MCP services consolidate: one canonical movement happens once, junctions fan-out the notification to consumers (forecast, financial, replenishment) instead of materializing the same event in 4 copies.

Inventory lots (lot/serial-tracked items with expiry) is a sub-domain — some merchants (food, Rx, electronics with serials) need it; most SMB don't. Include the table in canonical, but it's empty for merchants that don't lot-track. Cardinality-aware per §9.

---

## i.inventory_positions

**ARTS reference**: ARTS InventoryControlBook (current SOH per item per location).
**Module**: D.

### Schema

```sql
CREATE TABLE i.inventory_positions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  item_id                 uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,
  location_id             uuid NOT NULL REFERENCES l.locations(id) ON DELETE RESTRICT,
  zone_id                 uuid REFERENCES l.location_zones(id),    -- bin-level if zone-tracked; NULL = location-aggregate
  on_hand_quantity        numeric(14,4) NOT NULL DEFAULT 0,
  reserved_quantity       numeric(14,4) NOT NULL DEFAULT 0,        -- allocated but not yet picked (orders awaiting fulfillment)
  on_order_quantity       numeric(14,4) NOT NULL DEFAULT 0,        -- POs placed, not yet received
  in_transit_quantity     numeric(14,4) NOT NULL DEFAULT 0,        -- transfers in-flight
  last_movement_at        timestamptz,
  last_count_at           timestamptz,                              -- last stock-count timestamp (for cycle-count cadence)
  cost_basis              numeric(14,4),                            -- weighted-average cost (financial; cross-references ledger.stock_ledger_entries)
  attributes              jsonb NOT NULL DEFAULT '{}',
  status                  text NOT NULL DEFAULT 'active',           -- active | discontinued | bin_relocated
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, location_id, COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid))
);

CREATE INDEX idx_positions_tenant ON i.inventory_positions(tenant_id);
CREATE INDEX idx_positions_item ON i.inventory_positions(item_id);
CREATE INDEX idx_positions_location ON i.inventory_positions(location_id);
CREATE INDEX idx_positions_low_stock ON i.inventory_positions(tenant_id, location_id, item_id) WHERE on_hand_quantity <= 0;
CREATE INDEX idx_positions_unsynced ON i.inventory_positions(last_movement_at) WHERE last_count_at IS NULL OR last_count_at < last_movement_at - interval '30 days';
```

### Operational lifecycle (TOM operational clock)

**Producers** (write to inventory_positions):
- `mcp.inventory.position.materialize` — recomputes from `inventory_movements` deltas (atomic on each movement)
- `mcp.inventory.position.from-stock-count` — direct overwrite from physical count (sets `last_count_at = now()`)

**Consumers**:
- `mcp.transaction.scan.check-availability` — real-time at POS (do we have it?)
- `mcp.orders.fulfillment.allocate` — reserve quantity for an order
- `mcp.replenishment.demand.compute` — replenishment trigger (TOM J010 Current SOH GFO→UDD)
- `mcp.forecast.position.snapshot` — daily snapshot (TOM J010 nightly)
- `mcp.inventory.report.daily` — daily reporting (TOM D029 Inventory Report RMS→TIMS, D033 Storeline→GFO)
- `mcp.audit.shrink.compare-to-count` — Q-module shrink detection

**SLA at producer**: real-time on each movement, atomic with movement insert (transactional), p95 < 100ms.
**SLA at consumers**: lookup p99 < 30ms; freshness < 2s for transaction-scan.

### Provenance

- **ARTS reference**: InventoryControlBook (current state per item × location)
- **GSLM**: not richly modeled in S0 SQL implementation; the MDM site Supply domain has 19 entities including stock position
- **CRDM operational**: implicit (CRDM_Item operational reduces inventory; CRDM_GoodsReceived increases)
- **TOM junctions**: J010 (Current SOH GFO→UDD), D029 (RMS→TIMS report), D033 (Storeline→GFO report)
- **Canary current**: `ledger.stock_ledger_entries` is the financial-valuation counterpart — physical SOH is here, financial valuation there
- **Justification**: Reserved + on-order + in-transit columns are essential for "available-to-promise" calculations (orders fulfillment) — not separate tables because they're always queried together with on-hand. Cost basis denormalized for read speed; source-of-truth for cost is `ledger.stock_ledger_entries`. COALESCE-PK pattern allows location-aggregate (zone_id NULL) and bin-level positions to coexist.

---

## i.inventory_movements

**ARTS reference**: ARTS InventoryMovement (event log).
**Module**: D.

### Schema

```sql
CREATE TABLE i.inventory_movements (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  item_id                 uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,
  location_id             uuid NOT NULL REFERENCES l.locations(id) ON DELETE RESTRICT,
  zone_id                 uuid REFERENCES l.location_zones(id),
  lot_id                  uuid REFERENCES i.inventory_lots(id),
  movement_type           text NOT NULL,                            -- goods_receipt | adjustment | transfer_in | transfer_out | rtv | sale | return | write_off | cycle_count_correction | reservation | release_reservation
  quantity_delta          numeric(14,4) NOT NULL,                   -- signed; positive = increase, negative = decrease
  movement_at             timestamptz NOT NULL DEFAULT now(),
  source_document_id      uuid REFERENCES i.inventory_documents(id),  -- nullable for direct movements (manual adjustment)
  source_document_line_id uuid REFERENCES i.inventory_document_lines(id),
  source_transaction_id   uuid,                                       -- t.transactions(id) — sale-derived movements
  reason_code             text,                                       -- damaged | theft | spoilage | recount_corrected | etc.
  reference                text,                                       -- merchant or external reference (PO #, RTV #, etc.)
  performed_by_user_id    uuid REFERENCES app.users(id),
  performed_by_employee_id uuid REFERENCES e.employees(id),
  cost_basis              numeric(14,4),                              -- cost at time of movement (snapshot)
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- NOTE: no updated_at — this is append-only
);

CREATE INDEX idx_movements_tenant ON i.inventory_movements(tenant_id);
CREATE INDEX idx_movements_item ON i.inventory_movements(item_id);
CREATE INDEX idx_movements_location ON i.inventory_movements(location_id);
CREATE INDEX idx_movements_at ON i.inventory_movements(movement_at);
CREATE INDEX idx_movements_type ON i.inventory_movements(movement_type);
CREATE INDEX idx_movements_document ON i.inventory_movements(source_document_id);
CREATE INDEX idx_movements_transaction ON i.inventory_movements(source_transaction_id) WHERE source_transaction_id IS NOT NULL;
CREATE INDEX idx_movements_position_recompute ON i.inventory_movements(tenant_id, item_id, location_id, COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid), movement_at DESC);
```

### Operational lifecycle

**Producers**:
- `mcp.inventory.movement.from-goods-receipt` — D028 GRN equivalent
- `mcp.inventory.movement.from-adjustment` — D019/D020 adjustment
- `mcp.inventory.movement.from-transfer` — D036/D038 transfer
- `mcp.inventory.movement.from-rtv` — D030/D032/D035 RTV
- `mcp.inventory.movement.from-sale` — derived from t.transactions completion
- `mcp.inventory.movement.from-cycle-count` — D029 stock-count corrections
- `mcp.inventory.movement.reserve` — soft reservation for orders
- `mcp.inventory.movement.release-reservation` — order cancelled or fulfilled

**Consumers**:
- `mcp.inventory.position.materialize` — every movement triggers position recompute (atomic in same transaction)
- `mcp.financial.stock-ledger.post` — financial valuation entry per movement (Canary `ledger.stock_ledger_entries`)
- `mcp.audit.shrink.detect` — pattern detection on adjustment movements (Q module)
- `mcp.metrics.movement-velocity` — daily/weekly aggregations
- `mcp.replenishment.signal.from-receipt` — receipt completion triggers replenishment recalc

**SLA at producer**: real-time, atomic with position update (transactional), append-only (no updates ever).
**Consumer freshness**: position recompute synchronous in same transaction; downstream consumers async <5s.

### Provenance

- **ARTS reference**: InventoryMovement (one event per atomic SOH change)
- **GSLM Supply (S0)**: 19 entities — most fold here as movement_type variants
- **CRDM folded**: `CRDM_GoodsReceived` + `CRDM_StockAdjustment` + `CRDM_SupplierReturn` + `CRDM_Transfer` + `CRDM_WebItemReturn` (5 CRDM entities → 1 movement table with discriminator)
- **TOM junctions**: D028 (GRN RWMS→TIMS), D019/D020/D033 (adjustments), D029/D022 (stock counts), D030/D032/D035 (RTV), D036/D037/D038 (transfers)
- **Canary current**: `ledger.stock_ledger_entries` is financial counterpart; `i.inventory_movements` is physical
- **Justification**: Append-only event log gives us full audit trail and replay capability. Position table can always be recomputed from movements (sum of deltas) — useful for debugging, reconciling with physical counts. Movement-type discriminator avoids 5+ separate tables (one per movement kind) per §9 cardinality rule. Single index `idx_movements_position_recompute` supports the position-materialization query path.

---

## i.inventory_documents

**ARTS reference**: ARTS InventoryDocument (header for receipt/transfer/count/return).
**Module**: D.

### Schema

```sql
CREATE TABLE i.inventory_documents (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_type       text NOT NULL,                              -- goods_receipt | transfer_out | transfer_in | rtv | stock_count | adjustment_batch
  document_number     text NOT NULL,                              -- merchant-assigned (PO# for receipt, RTV# for return, etc.)
  source_location_id  uuid REFERENCES l.locations(id),            -- origin (transfers, RTVs); NULL for receipts
  destination_location_id uuid REFERENCES l.locations(id),         -- destination (receipts, transfers); NULL for RTVs
  vendor_id           uuid REFERENCES m.vendors(id),               -- for receipts and RTVs
  related_order_id    uuid,                                        -- o.purchase_orders(id) when known — Chunk 5b
  status              text NOT NULL DEFAULT 'draft',               -- draft | in_progress | completed | cancelled | reconciled
  expected_at         timestamptz,
  completed_at        timestamptz,
  total_quantity      numeric(14,4),                               -- sum of line quantities (denormalized)
  total_cost          numeric(14,4),                               -- sum of line cost (denormalized)
  performed_by_user_id uuid REFERENCES app.users(id),
  attributes          jsonb NOT NULL DEFAULT '{}',                 -- carrier, BOL #, packing list URL, photo evidence URLs
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_type, document_number)
);

CREATE INDEX idx_idocs_tenant ON i.inventory_documents(tenant_id);
CREATE INDEX idx_idocs_type ON i.inventory_documents(document_type);
CREATE INDEX idx_idocs_status ON i.inventory_documents(status) WHERE status NOT IN ('completed', 'cancelled');
CREATE INDEX idx_idocs_destination ON i.inventory_documents(destination_location_id);
CREATE INDEX idx_idocs_source ON i.inventory_documents(source_location_id);
CREATE INDEX idx_idocs_vendor ON i.inventory_documents(vendor_id);
CREATE INDEX idx_idocs_related_order ON i.inventory_documents(related_order_id);
```

### Operational lifecycle

**Producers**:
- `mcp.inventory.document.create` — draft document at intent (PO arrival expected, transfer scheduled)
- `mcp.inventory.document.complete` — when physical activity done (sets `completed_at`, `status='completed'`, triggers movement creation)
- `mcp.inventory.document.cancel` — abort

**Consumers**:
- `mcp.inventory.document.audit-trail` — query all activity for a location/period
- `mcp.financial.three-way-match` — match receipt document against PO and supplier invoice (F-Prefix F004 pattern)

**SLA at producer**: real-time create; complete is the trigger event for downstream movements.

### Provenance

- **ARTS reference**: InventoryDocument header
- **TOM junctions touching**: D016 (PO Download), D028 (GRN), D029 (stock count), D030 (RTV dispatch), D036 (stock transfer)
- **Justification**: Single document table with `document_type` discriminator covers all five physical-inventory-change document kinds. Status field discriminates draft (intent) → in_progress (executing) → completed (movements posted) → reconciled (matched against expected). Related-order FK cross-references Orders domain (Chunk 5b) for receipt-vs-PO reconciliation.

---

## i.inventory_document_lines

**ARTS reference**: ARTS InventoryDocumentLine.
**Module**: D.

### Schema

```sql
CREATE TABLE i.inventory_document_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_id         uuid NOT NULL REFERENCES i.inventory_documents(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,
  expected_quantity   numeric(14,4),                              -- planned (for receipts vs PO, transfers vs request)
  actual_quantity     numeric(14,4),                              -- physically counted/received
  variance_quantity   numeric(14,4) GENERATED ALWAYS AS (COALESCE(actual_quantity, 0) - COALESCE(expected_quantity, 0)) STORED,
  variance_reason     text,                                       -- damaged | short | over | wrong_item | quality_reject
  unit_cost           numeric(14,4),                              -- per-unit cost at receipt
  lot_id              uuid REFERENCES i.inventory_lots(id),
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_id, line_number)
);

CREATE INDEX idx_idoc_lines_tenant ON i.inventory_document_lines(tenant_id);
CREATE INDEX idx_idoc_lines_document ON i.inventory_document_lines(document_id);
CREATE INDEX idx_idoc_lines_item ON i.inventory_document_lines(item_id);
CREATE INDEX idx_idoc_lines_variance ON i.inventory_document_lines(document_id) WHERE variance_quantity != 0;
```

### Operational lifecycle

**Producers**:
- `mcp.inventory.document-line.add` — at document creation (from PO lines for receipts, from transfer order lines for transfers)
- `mcp.inventory.document-line.update-actual` — at physical count

**Consumers**:
- `mcp.inventory.movement.from-document-line` — when document completed, generates one movement per line
- `mcp.audit.variance.report` — for variance reports (received vs ordered)

**SLA at producer**: real-time.

### Provenance

- **ARTS reference**: InventoryDocumentLine
- **Justification**: Generated `variance_quantity` column eliminates application-level variance computation (always consistent with stored values). Allows variance reports without table scans.

---

## i.inventory_lots

**ARTS reference**: ARTS Lot / Serial Tracking (optional).
**Module**: D.
**Optional**: only used by lot/serial-tracked merchants (food, Rx, electronics with serials).

### Schema

```sql
CREATE TABLE i.inventory_lots (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE RESTRICT,
  lot_number          text NOT NULL,                              -- batch/lot/serial number
  lot_type            text NOT NULL DEFAULT 'batch',              -- batch | serial | expiry | catch_weight
  expiry_date         date,                                       -- for date-tracked items
  manufacture_date    date,
  received_at         timestamptz,
  vendor_id           uuid REFERENCES m.vendors(id),
  source_document_id  uuid REFERENCES i.inventory_documents(id),  -- the receipt that introduced this lot
  status              text NOT NULL DEFAULT 'active',             -- active | quarantine | recalled | exhausted | expired
  attributes          jsonb NOT NULL DEFAULT '{}',                -- catch-weight, country-of-origin, FDA NDC, etc.
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, lot_number)
);

CREATE INDEX idx_lots_tenant ON i.inventory_lots(tenant_id);
CREATE INDEX idx_lots_item ON i.inventory_lots(item_id);
CREATE INDEX idx_lots_expiry ON i.inventory_lots(expiry_date) WHERE expiry_date IS NOT NULL AND status = 'active';
CREATE INDEX idx_lots_status ON i.inventory_lots(status) WHERE status != 'active';
```

### Operational lifecycle

**Producers**:
- `mcp.inventory.lot.create` — at goods receipt (when item is lot-tracked)
- `mcp.inventory.lot.recall` — recall event (sets `status='recalled'`)
- `mcp.inventory.lot.expire` — scheduled job (when expiry_date < today)

**Consumers**:
- `mcp.transaction.lot.scan-resolve` — at POS for lot-tracked items
- `mcp.inventory.fefo.allocate` — first-expiry-first-out allocation for orders
- `mcp.compliance.recall.locate-stock` — find all stock of a recalled lot
- `mcp.audit.expiry.report` — daily report of items approaching expiry

**SLA at producer**: real-time at receipt; idempotent on `(tenant_id, item_id, lot_number)`.

### Provenance

- **ARTS reference**: ARTS Lot/Serial Tracking
- **GSLM**: not modeled (Walmart Item domain didn't model lots)
- **CRDM**: not modeled (POS-only)
- **TOM junctions**: not in TOM corpus
- **Justification**: Lot is referenced from `inventory_movements.lot_id` and `inventory_document_lines.lot_id` for full traceability. Recall capability (status='recalled' + locate-stock query) is a regulatory requirement for food/Rx merchants. Optional for non-lot-tracked merchants — table simply remains empty.

---

## Domain summary

**5 entities, 1 schema (i), 1 module (D)**:
- `i.inventory_positions` (~16 cols) — current SOH per item × location, with reserved/on-order/in-transit
- `i.inventory_movements` (~17 cols, append-only) — atomic event log per SOH change, all movement types unified
- `i.inventory_documents` (~17 cols) — header for receipts/transfers/counts/RTVs
- `i.inventory_document_lines` (~12 cols) — per-line detail with generated variance column
- `i.inventory_lots` (~14 cols, optional) — lot/serial/expiry tracking

**Folded from sources**:
- CRDM 5 inventory entities (`GoodsReceived`, `Transfer`, `StockAdjustment`, `SupplierReturn`, `WebItemReturn`) → 1 unified `inventory_movements` table with `movement_type` discriminator
- GSLM Supply 19 entities (S0 MDM site) → 5 SMB-2030 canonical (most folded as movement_types or attribute JSONB)
- TOM 18 D-Prefix interfaces → 8 MCP service junction patterns (movement-from-{receipt,adjustment,transfer,rtv,sale,count,reservation,release})

**MCP service junctions defined for this domain (~17)**:
- Position writers: position.materialize, position.from-stock-count
- Movement creators: movement.from-{goods-receipt, adjustment, transfer, rtv, sale, cycle-count}, movement.{reserve, release-reservation}
- Document lifecycle: document.create, document.complete, document.cancel, document-line.add, document-line.update-actual, movement.from-document-line
- Lot lifecycle: lot.create, lot.recall, lot.expire
- Consumers: scan.check-availability, fulfillment.allocate, replenishment.demand.compute, forecast.position.snapshot, inventory.report.daily, audit.shrink.detect, three-way-match (cross-cuts F), audit.variance.report, fefo.allocate, compliance.recall.locate-stock, audit.expiry.report

## Status

- **Chunk 5 complete.** 5 ARTS-Inventory-V2-aligned entities with full TOM D-Prefix lifecycle bindings.
- **Resume**: Chunk 5b — Orders domain (o schema, greenfield via J-Prefix). Target ~6-8 entities (purchase_orders, sales_orders, fulfillment_orders, allocations, ASN, BOL).

---

# §7 Orders Domain

**ARTS anchor**: ARTS PurchaseOrder + SalesOrder + Fulfillment (ODM canonical patterns).
**Module**: O (Orders).
**Entities**: 8 (purchase_orders · purchase_order_lines · sales_orders · sales_order_lines · fulfillments · fulfillment_lines · allocations · shipping_documents).
**Source**: greenfield gap closure via TOM J-Prefix (28 interfaces) + ARTS standard order patterns.

## Domain narrative

The Orders domain is greenfield in our previous Canary spec — neither GSLM (master-data-only) nor CRDM (POS event capture only) modeled it. TOM J-Prefix gave us the single richest source: 28 interface specs covering allocations, sales forecasts, three order types (PBS Pick-By-Store, PBL Pick-By-Line, DTS Direct-To-Supplier), ASN, BOL, transfer details, and item-warehouse-supplier denormalization. SMB-2030 doesn't need three discriminated order types in three separate tables — collapse to single `purchase_orders` with `order_method` discriminator (replenishment | direct | dropship), where the discriminator drives which downstream junction handles fulfillment.

The model splits orders into two main flows:
- **Purchase Orders** (supplier-direction): merchant orders from vendor, receives goods
- **Sales Orders** (customer-direction): customer orders from merchant, gets goods

Both link to `fulfillments` (the physical pick/pack/ship operation) and `allocations` (the reservation of inventory positions). `shipping_documents` covers both ASN (inbound advance notice from supplier) and BOL (outbound bill of lading) with type discriminator.

For SMB-2030: single-store merchants may only use purchase_orders (sales happen via t.transactions at POS). E-commerce/BOPIS merchants need the full set. Inter-store transfers are inventory_movements (Chunk 5), not orders — different lifecycle.

J019 (the most-fingerprinted J-Prefix interface) revealed the order-routing schema family: JIROA header / JIROB detail / JIROZ trailer with PBL-IND discriminator. Canary Go captures the same logical structure but as a single canonical with the discriminator as a column, not separate tables.

---

## o.purchase_orders

**ARTS reference**: ARTS PurchaseOrder.
**Module**: O.

### Schema

```sql
CREATE TABLE o.purchase_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  po_number               text NOT NULL,                              -- merchant or system-assigned
  vendor_id               uuid NOT NULL REFERENCES m.vendors(id),
  destination_location_id uuid REFERENCES l.locations(id),            -- where goods will be received
  order_method            text NOT NULL DEFAULT 'replenishment',      -- replenishment | direct | dropship | warehouse_consolidation
  order_type              text NOT NULL DEFAULT 'standard',           -- standard | rush | pre_book | promotional | open
  status                  text NOT NULL DEFAULT 'draft',              -- draft | submitted | acknowledged | in_transit | partial_received | received | closed | cancelled
  ordered_at              timestamptz,                                -- when submitted to vendor
  expected_delivery_at    timestamptz,
  acknowledged_at         timestamptz,                                -- vendor acknowledgement (F013 PO Ack equivalent)
  cancelled_at            timestamptz,
  total_quantity          numeric(14,4),                              -- denormalized sum of line quantities
  total_cost              numeric(14,4),                              -- denormalized sum of line costs
  currency                text NOT NULL DEFAULT 'USD',                -- ISO 4217
  payment_terms           text,                                       -- inherits from vendor by default; override per-PO
  shipping_terms          text,                                       -- FOB origin | FOB destination | etc.
  approval_user_id        uuid REFERENCES app.users(id),
  approved_at             timestamptz,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, po_number)
);

CREATE INDEX idx_pos_tenant ON o.purchase_orders(tenant_id);
CREATE INDEX idx_pos_vendor ON o.purchase_orders(vendor_id);
CREATE INDEX idx_pos_destination ON o.purchase_orders(destination_location_id);
CREATE INDEX idx_pos_status ON o.purchase_orders(status) WHERE status NOT IN ('received', 'closed', 'cancelled');
CREATE INDEX idx_pos_expected ON o.purchase_orders(expected_delivery_at) WHERE status IN ('submitted', 'acknowledged', 'in_transit');
```

### Operational lifecycle (TOM operational clock)

**Producers**:
- `mcp.orders.purchase-order.create` — manual or replenishment-suggested
- `mcp.orders.purchase-order.from-replenishment-engine` — auto-generated by demand signal (J017 PBS / J019 Direct / J020 PBL pattern)
- `mcp.orders.purchase-order.acknowledge` — vendor acknowledgement received (F013 pattern)
- `mcp.orders.purchase-order.update-status` — partial-received, in-transit, etc.

**Consumers**:
- `mcp.financial.po.export-to-financial` — for AP encumbrance (F001/F015 RMS→TIMS pattern)
- `mcp.inventory.position.update-on-order` — increments `on_order_quantity`
- `mcp.inventory.document.create-from-po` — when receipt expected, creates draft i.inventory_documents
- `mcp.financial.three-way-match.po-leg` — PO is one leg of three-way (PO + receipt + invoice)
- `mcp.metrics.po-cycle-time` — from order to receipt analytics

**SLA at producer**: real-time, p95 < 200ms. Idempotent on `(tenant_id, po_number)`.

### Provenance

- **ARTS reference**: PurchaseOrder
- **TOM junctions**: J017 (PBS Stock Order GFO→RMS), J019 (Direct Store Order GFO→RMS), J020 (PBL Procurement Order GFO→RMS), J024 (PBL Final Order GFO→RMS), J032 (PO doc nos in interfaces RMS→GFO), F001/F015 (PO RMS→TIMS), F013 (PO Acknowledgement TIMS→RMS), D016 (PO Download for Directs ORMS→Storeline), D031 (Direct PO Receipt IL→RMS), D034 (Direct PO Receipt IL→GFO)
- **Canary current**: `app.transfer_orders` exists but covers only inter-store transfers (which we model as inventory movements, not orders); supersede with proper purchase_orders
- **Justification**: Single PO table with `order_method` discriminator covers TOM's PBS/PBL/DTS three-way split. The 28-interface complexity in J-Prefix collapses because Tesco maintained separate copies in 4 systems (RMS, GFO, ORMS, TIMS) — Canary Go has one canonical and lets MCP services fan-out notifications.

---

## o.purchase_order_lines

**ARTS reference**: ARTS PurchaseOrderLine.
**Module**: O.

### Schema

```sql
CREATE TABLE o.purchase_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  po_id               uuid NOT NULL REFERENCES o.purchase_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES m.items(id),
  vendor_sku          text,                                       -- vendor's identifier (from m.item_vendors)
  ordered_quantity    numeric(14,4) NOT NULL,
  received_quantity   numeric(14,4) NOT NULL DEFAULT 0,           -- accumulates across partial receipts
  cancelled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  unit_cost           numeric(14,4) NOT NULL,
  total_cost          numeric(14,4) GENERATED ALWAYS AS (ordered_quantity * unit_cost) STORED,
  expected_delivery_at timestamptz,
  status              text NOT NULL DEFAULT 'open',                -- open | partial | received | cancelled | closed
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, po_id, line_number)
);

CREATE INDEX idx_po_lines_tenant ON o.purchase_order_lines(tenant_id);
CREATE INDEX idx_po_lines_po ON o.purchase_order_lines(po_id);
CREATE INDEX idx_po_lines_item ON o.purchase_order_lines(item_id);
CREATE INDEX idx_po_lines_open ON o.purchase_order_lines(po_id) WHERE status IN ('open', 'partial');
```

### Operational lifecycle

**Producers**: `mcp.orders.po-line.add`, `mcp.orders.po-line.update-receipt-quantity`, `mcp.orders.po-line.cancel`
**Consumers**: `mcp.inventory.document-line.create-from-po-line`, `mcp.financial.po-line.expected-cost`, `mcp.replenishment.po-line.fill-rate-metric`

### Provenance

- **ARTS reference**: PurchaseOrderLine
- **TOM**: J019 / J020 / J024 detail records (JIROB segment) carry one PO line each
- **Justification**: Generated `total_cost` column ensures consistency. Received quantity accumulates for partial-receipt support without separate "receipt" tracking — the actual inventory_movements link via documents.

---

## o.sales_orders

**ARTS reference**: ARTS SalesOrder / CustomerOrder.
**Module**: O.

### Schema

```sql
CREATE TABLE o.sales_orders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  order_number            text NOT NULL,                              -- merchant or system-assigned
  customer_id             uuid REFERENCES c.customers(id),            -- nullable for guest orders
  channel                 text NOT NULL DEFAULT 'web',                -- web | bopis | ship_to_store | special_order | phone | marketplace
  origin_location_id      uuid REFERENCES l.locations(id),            -- where the order will be fulfilled from (or null for assigned-later)
  destination_location_id uuid REFERENCES l.locations(id),            -- for BOPIS/ship-to-store; NULL for ship-to-customer-address
  destination_address_id  uuid REFERENCES c.customer_addresses(id),   -- for shipped orders; NULL for in-store pickup
  status                  text NOT NULL DEFAULT 'pending',            -- pending | confirmed | allocated | picking | packed | shipped | delivered | completed | cancelled | returned
  ordered_at              timestamptz NOT NULL DEFAULT now(),
  promised_at             timestamptz,                                -- promised delivery / pickup time
  fulfilled_at            timestamptz,
  cancelled_at            timestamptz,
  subtotal                numeric(14,4),
  tax_total               numeric(14,4),
  shipping_total          numeric(14,4),
  discount_total          numeric(14,4),
  grand_total             numeric(14,4),
  currency                text NOT NULL DEFAULT 'USD',
  payment_status          text NOT NULL DEFAULT 'pending',            -- pending | authorized | captured | refunded | failed
  attributes              jsonb NOT NULL DEFAULT '{}',                -- gift_message, delivery_instructions, special_handling
  external_ids            jsonb DEFAULT '{}',                         -- shopify_id, square_order_id, marketplace_order_id
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, order_number)
);

CREATE INDEX idx_so_tenant ON o.sales_orders(tenant_id);
CREATE INDEX idx_so_customer ON o.sales_orders(customer_id);
CREATE INDEX idx_so_origin ON o.sales_orders(origin_location_id);
CREATE INDEX idx_so_destination ON o.sales_orders(destination_location_id);
CREATE INDEX idx_so_status ON o.sales_orders(status) WHERE status NOT IN ('completed', 'cancelled', 'returned');
CREATE INDEX idx_so_external_ids ON o.sales_orders USING gin(external_ids);
```

### Operational lifecycle

**Producers**: `mcp.orders.sales-order.create-from-{web,bopis,phone,marketplace}`, `mcp.orders.sales-order.update-status`
**Consumers**: `mcp.inventory.allocation.allocate-for-order`, `mcp.fulfillment.create-from-order`, `mcp.financial.sales-order.payment-capture`, `mcp.transaction.from-completed-order` (when fulfilled at POS, becomes a transaction in T schema)

### Provenance

- **ARTS reference**: SalesOrder
- **TOM**: not in J-Prefix (Tesco's order types were all supplier-direction)
- **Justification**: Channel discriminator drives downstream fulfillment routing. Multi-status enables visibility from order placement through delivery.

---

## o.sales_order_lines

**ARTS reference**: ARTS SalesOrderLine.
**Module**: O.

### Schema

```sql
CREATE TABLE o.sales_order_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_id      uuid NOT NULL REFERENCES o.sales_orders(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  item_id             uuid NOT NULL REFERENCES m.items(id),
  ordered_quantity    numeric(14,4) NOT NULL,
  fulfilled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  cancelled_quantity  numeric(14,4) NOT NULL DEFAULT 0,
  refunded_quantity   numeric(14,4) NOT NULL DEFAULT 0,
  unit_price          numeric(14,4) NOT NULL,
  unit_discount       numeric(14,4) NOT NULL DEFAULT 0,
  unit_tax            numeric(14,4) NOT NULL DEFAULT 0,
  line_total          numeric(14,4) GENERATED ALWAYS AS ((ordered_quantity * (unit_price - unit_discount)) + (ordered_quantity * unit_tax)) STORED,
  status              text NOT NULL DEFAULT 'open',
  attributes          jsonb NOT NULL DEFAULT '{}',                  -- {customizations, gift_wrap, etc.}
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, sales_order_id, line_number)
);

CREATE INDEX idx_so_lines_tenant ON o.sales_order_lines(tenant_id);
CREATE INDEX idx_so_lines_order ON o.sales_order_lines(sales_order_id);
CREATE INDEX idx_so_lines_item ON o.sales_order_lines(item_id);
```

### Operational lifecycle

**Producers**: `mcp.orders.so-line.add`, `mcp.orders.so-line.update-fulfilled-quantity`
**Consumers**: `mcp.inventory.allocation.allocate-for-line`, `mcp.fulfillment.line.create-from-so-line`

---

## o.fulfillments

**ARTS reference**: ARTS Fulfillment / Shipment.
**Module**: O.

### Schema

```sql
CREATE TABLE o.fulfillments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_number  text NOT NULL,
  source_location_id  uuid REFERENCES l.locations(id),                -- where stock is picked from
  fulfillment_method  text NOT NULL DEFAULT 'pick_and_ship',          -- pick_and_ship | bopis_pickup | curbside | dropship | direct_ship_from_warehouse
  status              text NOT NULL DEFAULT 'pending',                 -- pending | picking | packed | shipped | delivered | cancelled
  assigned_to         uuid REFERENCES e.employees(id),
  picked_at           timestamptz,
  packed_at           timestamptz,
  shipped_at          timestamptz,
  delivered_at        timestamptz,
  carrier             text,                                            -- USPS | UPS | FedEx | DHL | merchant_delivery | customer_pickup
  tracking_number     text,
  tracking_url        text,
  attributes          jsonb NOT NULL DEFAULT '{}',                    -- driver_name, photo_proof_url, signature_url
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, fulfillment_number)
);

CREATE INDEX idx_fulfill_tenant ON o.fulfillments(tenant_id);
CREATE INDEX idx_fulfill_source ON o.fulfillments(source_location_id);
CREATE INDEX idx_fulfill_assigned ON o.fulfillments(assigned_to);
CREATE INDEX idx_fulfill_status ON o.fulfillments(status) WHERE status NOT IN ('delivered', 'cancelled');
CREATE INDEX idx_fulfill_tracking ON o.fulfillments(tracking_number) WHERE tracking_number IS NOT NULL;
```

### Operational lifecycle

**Producers**: `mcp.fulfillment.create`, `mcp.fulfillment.assign`, `mcp.fulfillment.update-status` (state machine through pending → picking → packed → shipped → delivered)
**Consumers**: `mcp.inventory.movement.from-fulfillment-pick`, `mcp.metrics.fulfillment-cycle-time`, `mcp.notification.customer-shipping-update`

### Provenance

- **ARTS reference**: Fulfillment / Shipment
- **TOM**: J097/J097.5 BOL Picking Details (ORWMS→GFO) and BOL Load and Left-Off — fulfillment-side data exchange
- **Justification**: One fulfillment can satisfy multiple sales orders (consolidated picking) or one sales order can have multiple fulfillments (partial shipping). Many-to-many via `fulfillment_lines`.

---

## o.fulfillment_lines

**ARTS reference**: ARTS FulfillmentLine.
**Module**: O.

### Schema

```sql
CREATE TABLE o.fulfillment_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  fulfillment_id      uuid NOT NULL REFERENCES o.fulfillments(id) ON DELETE CASCADE,
  sales_order_line_id uuid NOT NULL REFERENCES o.sales_order_lines(id),
  item_id             uuid NOT NULL REFERENCES m.items(id),
  quantity            numeric(14,4) NOT NULL,
  picked_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  packed_quantity     numeric(14,4) NOT NULL DEFAULT 0,
  shipped_quantity    numeric(14,4) NOT NULL DEFAULT 0,
  lot_id              uuid REFERENCES i.inventory_lots(id),           -- if lot-tracked
  inventory_movement_id uuid REFERENCES i.inventory_movements(id),    -- the movement that decremented stock
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ful_lines_tenant ON o.fulfillment_lines(tenant_id);
CREATE INDEX idx_ful_lines_ful ON o.fulfillment_lines(fulfillment_id);
CREATE INDEX idx_ful_lines_so_line ON o.fulfillment_lines(sales_order_line_id);
CREATE INDEX idx_ful_lines_item ON o.fulfillment_lines(item_id);
```

### Operational lifecycle

**Producers**: `mcp.fulfillment.line.allocate`, `mcp.fulfillment.line.update-picked`, `mcp.fulfillment.line.update-packed`, `mcp.fulfillment.line.update-shipped`
**Consumers**: `mcp.inventory.movement.from-pick` (decrements inventory_positions when picked), `mcp.metrics.pick-accuracy`

### Provenance

- **ARTS reference**: FulfillmentLine
- **TOM**: J097 BOL Picking detail (one line per picked item)
- **Justification**: Three-quantity tracking (picked, packed, shipped) supports back-order and partial-pack scenarios. `inventory_movement_id` link makes the chain order → fulfillment → inventory_movement traceable for reconciliation.

---

## o.allocations

**ARTS reference**: ARTS Allocation (reservation of inventory for orders).
**Module**: O.

### Schema

```sql
CREATE TABLE o.allocations (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  sales_order_line_id uuid NOT NULL REFERENCES o.sales_order_lines(id) ON DELETE CASCADE,
  inventory_position_id uuid NOT NULL REFERENCES i.inventory_positions(id) ON DELETE RESTRICT,
  allocation_type     text NOT NULL DEFAULT 'soft',                  -- soft | hard | committed
  quantity            numeric(14,4) NOT NULL,
  allocated_at        timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz,                                    -- soft allocations expire (cart abandonment)
  consumed_by_movement_id uuid REFERENCES i.inventory_movements(id),  -- when picked, links to the actual decrement
  status              text NOT NULL DEFAULT 'active',                 -- active | consumed | expired | released | cancelled
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_alloc_tenant ON o.allocations(tenant_id);
CREATE INDEX idx_alloc_so_line ON o.allocations(sales_order_line_id);
CREATE INDEX idx_alloc_position ON o.allocations(inventory_position_id);
CREATE INDEX idx_alloc_active ON o.allocations(inventory_position_id) WHERE status = 'active';
CREATE INDEX idx_alloc_expiring ON o.allocations(expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;
```

### Operational lifecycle

**Producers**: `mcp.allocation.allocate-for-line`, `mcp.allocation.upgrade-soft-to-hard` (cart → checkout), `mcp.allocation.release` (cart abandonment, order cancel)
**Consumers**: `mcp.inventory.position.compute-available-to-promise` (subtract active allocations from on-hand), `mcp.metrics.allocation-fill-rate`

### Provenance

- **ARTS reference**: Allocation
- **TOM**: J001 Allocations from RMS to GFO, J113 Allocation Details to IDS — Tesco's allocations were primarily replenishment-side (DC-to-store) rather than customer-order-side
- **Justification**: Three-tier allocation (soft/hard/committed) supports modern e-commerce: soft = cart, hard = checkout-paid, committed = picked. Expiration-driven cleanup of abandoned carts. The `inventory_position_id` link enables real-time ATP (Available To Promise) queries: `on_hand - SUM(active allocations)`.

---

## o.shipping_documents

**ARTS reference**: ARTS ASN + BOL (advance shipping notice + bill of lading).
**Module**: O.

### Schema

```sql
CREATE TABLE o.shipping_documents (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  document_type       text NOT NULL,                              -- asn_inbound | bol_outbound | packing_list | manifest
  document_number     text NOT NULL,
  related_po_id       uuid REFERENCES o.purchase_orders(id),      -- ASN inbound references PO
  related_fulfillment_id uuid REFERENCES o.fulfillments(id),      -- BOL outbound references fulfillment
  vendor_id           uuid REFERENCES m.vendors(id),              -- ASN inbound: who is shipping to us
  carrier             text,
  tracking_number     text,
  expected_arrival_at timestamptz,                                -- ASN
  shipped_at          timestamptz,                                -- BOL
  total_quantity      numeric(14,4),                              -- denormalized
  total_weight        numeric(14,4),
  total_volume        numeric(14,4),
  attributes          jsonb NOT NULL DEFAULT '{}',                -- pallet count, container number, customs declaration
  status              text NOT NULL DEFAULT 'pending',            -- pending | acknowledged | in_transit | delivered | cancelled
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, document_type, document_number)
);

CREATE INDEX idx_shipdoc_tenant ON o.shipping_documents(tenant_id);
CREATE INDEX idx_shipdoc_po ON o.shipping_documents(related_po_id);
CREATE INDEX idx_shipdoc_ful ON o.shipping_documents(related_fulfillment_id);
CREATE INDEX idx_shipdoc_carrier ON o.shipping_documents(carrier, tracking_number);
CREATE INDEX idx_shipdoc_active ON o.shipping_documents(status) WHERE status NOT IN ('delivered', 'cancelled');
```

### Operational lifecycle

**Producers**: `mcp.shipping.asn.from-vendor` (J023 ASN IN TIMS→RMS pattern), `mcp.shipping.bol.from-fulfillment` (J097 pattern), `mcp.shipping.update-tracking`
**Consumers**: `mcp.inventory.expected-arrival.update`, `mcp.notification.customer-tracking-update`, `mcp.audit.shipping-reconciliation`

### Provenance

- **ARTS reference**: ASN + BOL
- **TOM**: J023 PO ASN IN (TIMS→RMS), J097 / J097.5 BOL (Picking + Load and Left-Off ORWMS→GFO)
- **Justification**: Single table with `document_type` discriminator covers ASN (inbound from supplier) and BOL (outbound to customer/store) — they share field structure (carrier, tracking, weight, volume). Single index strategy supports both lookup paths.

---

## Domain summary

**8 entities, 1 schema (o), 1 module (O)**:
- `o.purchase_orders` (~22 cols) — supplier-direction, three-method discriminator
- `o.purchase_order_lines` (~14 cols) — generated total_cost
- `o.sales_orders` (~22 cols) — customer-direction, multi-channel
- `o.sales_order_lines` (~16 cols) — generated line_total with discount + tax
- `o.fulfillments` (~16 cols) — pick/pack/ship state machine
- `o.fulfillment_lines` (~13 cols) — three-quantity tracking, inventory link
- `o.allocations` (~12 cols) — soft/hard/committed with ATP support
- `o.shipping_documents` (~16 cols) — ASN + BOL unified

**TOM J-Prefix 28 interfaces folded** to 8 entities + ~18 MCP service junctions. The PBL-IND/DTS discriminator that drove TOM's three-table split collapses to a single `order_method` column.

**Greenfield closures from this chunk**:
- O (Orders) module — fully designed from scratch using TOM J-Prefix as pattern reference
- ATP (Available-To-Promise) computation now possible via `inventory_positions.on_hand - SUM(active allocations)`
- Three-way match (PO + receipt + invoice) chain instrumented via FKs from `i.inventory_documents.related_order_id` to `o.purchase_orders.id`

**MCP service junctions defined for this domain (~22)**:
- PO lifecycle: create, from-replenishment-engine, acknowledge, update-status, line.add, line.update-receipt-quantity, line.cancel
- SO lifecycle: create-from-{web,bopis,phone,marketplace}, update-status, line.add, line.update-fulfilled-quantity
- Fulfillment lifecycle: create, assign, update-status, line.allocate, line.update-{picked,packed,shipped}
- Allocation lifecycle: allocate-for-line, upgrade-soft-to-hard, release
- Shipping: asn.from-vendor, bol.from-fulfillment, update-tracking
- Cross-domain consumers: inventory.allocation.allocate-for-order, financial.po.export-to-financial, financial.three-way-match.po-leg, transaction.from-completed-order

## Status

- **Chunk 5b complete.** 8 ARTS-aligned Order entities. Greenfield gap closed.
- **Resume**: Chunk 6 — Pricing + Financial domain (p, f schemas). ARTS Pricing + Financial + F-Prefix lifecycle. Target ~6-8 entities.

---

# §8 Pricing + Financial Domain

**ARTS anchor**: ARTS Pricing (PriceList, Promotion, Tax) + ARTS Financial (GLAccount, Tender, SupplierInvoice).
**Modules**: P (Pricing), F (Finance).
**Entities**: 10 (item_prices · promotions · promotion_rules · tax_classes · tax_rates · tender_types · gl_accounts · supplier_invoices · supplier_invoice_lines · payments).
**Folded from sources**: GSLM Price 6 + GSLM Finance 13 + TOM F-Prefix 5 + TOM C010 (price details) → 10 SMB-2030 canonical.

## Domain narrative

Pricing and Financial sit adjacent because they share the tax model (taxes are configured in pricing, computed at transaction, posted to financial). Per F-Prefix subagent finding: "Tax architecture is multi-model from day one — F004/F014 carry parallel sub-groups for VAT (rate-table per code, multi-rate per invoice) and Sales Tax, with v1.0 change record explicitly anticipating country-by-country tax-engine variance." We honor that — tax model supports VAT-style (rate by class) and Sales-Tax-style (rate by location × class) without schema split.

The F-Prefix pattern subagent flagged is critical: **AP three-way match is the implicit governing pattern stitching F001/F015 → F013 → (receipt) → F004 through ReIM into OFi**. This means every supplier invoice must reconcile against PO + receipt — Canary Go enforces this at the schema level (FKs from `f.supplier_invoices.related_po_id` and `f.supplier_invoices.related_receipt_document_id`).

For SMB-2030: most merchants do simple pricing (one price per item, occasional promotion), simple tax (one or two tax classes), simple AP (small vendor list, monthly invoice cadence). Schema supports the simple case (most fields nullable, default tender_types library) with extension capacity for complex merchants without reshape.

---

## p.item_prices

**ARTS reference**: ARTS PriceList / ItemPrice.
**Module**: P.

### Schema

```sql
CREATE TABLE p.item_prices (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES m.items(id) ON DELETE CASCADE,
  location_id         uuid REFERENCES l.locations(id),               -- NULL = default for all locations
  zone_id             uuid REFERENCES l.location_zones(id),          -- NULL = location-wide
  channel             text DEFAULT 'all',                            -- all | brick | web | bopis | marketplace
  price_type          text NOT NULL DEFAULT 'regular',               -- regular | clearance | member | wholesale | cost_plus
  amount              numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  uom                 text NOT NULL DEFAULT 'EA',                    -- price per EA, LB, KG, etc.
  effective_start     timestamptz NOT NULL DEFAULT now(),
  effective_end       timestamptz,                                    -- NULL = open-ended
  source_promotion_id uuid REFERENCES p.promotions(id),               -- if price came from a promotion
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  -- A given item × location × zone × channel × price_type can only have ONE active price at any moment
  EXCLUDE USING gist (
    tenant_id WITH =, item_id WITH =,
    COALESCE(location_id, '00000000-0000-0000-0000-000000000000'::uuid) WITH =,
    COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid) WITH =,
    channel WITH =, price_type WITH =,
    tstzrange(effective_start, effective_end, '[)') WITH &&
  ) WHERE (status = 'active')
);

CREATE INDEX idx_iprice_tenant ON p.item_prices(tenant_id);
CREATE INDEX idx_iprice_item ON p.item_prices(item_id);
CREATE INDEX idx_iprice_location ON p.item_prices(location_id);
CREATE INDEX idx_iprice_active_now ON p.item_prices(item_id, location_id, channel) WHERE status = 'active' AND (effective_end IS NULL OR effective_end > now());
```

### Operational lifecycle

**Producers**:
- `mcp.pricing.item-price.set` — manual or system price change
- `mcp.pricing.item-price.from-promotion` — promotion start triggers price override
- `mcp.pricing.item-price.expire` — promotion end / clearance complete
- `mcp.pricing.item-price.import-from-pos` — sync from POS-native price (Counterpoint PRICE, Square pricing)

**Consumers**:
- `mcp.transaction.price.resolve` — at every POS scan / web add-to-cart, p99 < 30ms
- `mcp.store-line.price-push` — PLU feed (TOM C023+C027+C045 PLU pattern)
- `mcp.metrics.price-elasticity` — analytics

**SLA at producer**: real-time, p95 < 200ms. EXCLUDE constraint enforces no overlapping active prices for same scope.
**SLA at consumers**: lookup p99 < 30ms (every scan).

### Provenance

- **ARTS reference**: PriceList (location-scoped) + ItemPrice (item-scoped); we unify in single table with nullable location/zone
- **GSLM Price domain**: 6 entities — promotions get separate tables below; basic price collapses here
- **TOM junctions**: C010US (Price details RMS→IDS — RegularPriceChange + ClearPriceChange)
- **Justification**: `EXCLUDE USING gist` with `tstzrange` enforces no two active prices for same scope at same time — Postgres-native temporal exclusion. `price_type` discriminator (regular | clearance | member | wholesale) replaces ARTS's separate price-list-per-type pattern. SMB without per-location pricing simply has all rows with `location_id IS NULL`.

---

## p.promotions

**ARTS reference**: ARTS Promotion (header).
**Module**: P.

### Schema

```sql
CREATE TABLE p.promotions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  promotion_code      text NOT NULL,
  name                text NOT NULL,
  description         text,
  promotion_type      text NOT NULL DEFAULT 'percent_off',           -- percent_off | amount_off | bogo | x_for_y | tier_threshold | bundle | fixed_price | loyalty_member_price
  scope_type          text NOT NULL DEFAULT 'item',                  -- item | category | brand | merchandise_total | tender | customer_segment
  effective_start     timestamptz NOT NULL,
  effective_end       timestamptz,
  active_days         int[] DEFAULT '{1,2,3,4,5,6,7}',               -- ISO day-of-week (1=Monday)
  active_hours        jsonb DEFAULT '{}',                            -- {"start": "08:00", "end": "20:00"}
  active_locations    uuid[],                                        -- NULL = all; array of l.locations.id
  active_channels     text[] DEFAULT '{}',                           -- {} = all
  customer_segments   text[],                                        -- target loyalty tiers / segments
  stackable           boolean NOT NULL DEFAULT false,                -- can stack with other promotions?
  exclusive_with      uuid[],                                        -- IDs of promotions that block this one
  max_uses_total      int,                                           -- across all customers
  max_uses_per_customer int,
  current_uses        int NOT NULL DEFAULT 0,
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'draft',                 -- draft | scheduled | active | paused | expired | cancelled
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, promotion_code)
);

CREATE INDEX idx_promo_tenant ON p.promotions(tenant_id);
CREATE INDEX idx_promo_active ON p.promotions(effective_start, effective_end) WHERE status = 'active';
CREATE INDEX idx_promo_status ON p.promotions(status) WHERE status NOT IN ('expired', 'cancelled');
```

### Operational lifecycle

**Producers**: `mcp.promotion.create`, `mcp.promotion.activate`, `mcp.promotion.pause`, `mcp.promotion.expire-on-schedule`
**Consumers**: `mcp.transaction.promotion.evaluate-applicable` (at every POS line add), `mcp.metrics.promotion-effectiveness`, `mcp.store-line.promotion-push` (C010 promotion feed)

### Provenance

- **ARTS reference**: Promotion
- **GSLM Promotion**: 5 entities (Promotions + PromotionComponents + PromotionComponentDetails + PromotionThresholds + ThresholdIntervals) — folded into promotion + promotion_rules
- **TOM**: C010US (price details with promotion components), C010TR (Promotions RMS→GFO)
- **Justification**: `active_days` / `active_hours` / `active_locations` / `active_channels` collapse what TOM modeled in 4 separate tables. Postgres array types handle multi-value scoping without join tables for queries like "find promotions active at this location on this channel today."

---

## p.promotion_rules

**ARTS reference**: ARTS Promotion Component / Rule.
**Module**: P.

### Schema

```sql
CREATE TABLE p.promotion_rules (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  promotion_id        uuid NOT NULL REFERENCES p.promotions(id) ON DELETE CASCADE,
  rule_order          int NOT NULL DEFAULT 1,                        -- sequence (some rules check before others)
  trigger_type        text NOT NULL,                                  -- buy_quantity | spend_amount | own_loyalty_card | scan_coupon | match_basket
  trigger_qualifier   jsonb NOT NULL DEFAULT '{}',                    -- {item_ids: [], category_ids: [], min_quantity: 2, min_amount: 25.00}
  benefit_type        text NOT NULL,                                  -- amount_off | percent_off | fixed_price | free_item | tier_unlock
  benefit_qualifier   jsonb NOT NULL DEFAULT '{}',                    -- {amount: 5.00, percent: 0.20, fixed_price: 10.00, free_item_ids: []}
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_prules_tenant ON p.promotion_rules(tenant_id);
CREATE INDEX idx_prules_promo ON p.promotion_rules(promotion_id);
```

### Operational lifecycle

**Producers**: `mcp.promotion-rule.add`, `mcp.promotion-rule.update`
**Consumers**: `mcp.transaction.promotion.evaluate` — runs at line-add and basket-total to check trigger satisfaction

### Provenance

- **ARTS reference**: PromotionComponent + PromotionComponentDetail
- **GSLM**: PromotionComponents + PromotionComponentDetails + PromotionThresholds + ThresholdIntervals (4 entities → 1 with trigger/benefit JSONB)
- **Justification**: Promotion engines need flexibility — every BOGO/X-for-Y/tier variation has different trigger and benefit semantics. JSONB qualifiers let the rule engine evaluate without schema migration for every new promo type. Rule_order supports sequenced evaluation (e.g., apply customer-tier discount THEN basket-total threshold).

---

## p.tax_classes

**ARTS reference**: ARTS Tax Classification.
**Module**: P (configured) / F (consumed).

### Schema

```sql
CREATE TABLE p.tax_classes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  code            text NOT NULL,                                  -- "STD", "FOOD", "RX", "ALCOHOL", "SERVICE", "EXEMPT"
  name            text NOT NULL,
  description     text,
  is_default      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code),
  CONSTRAINT one_default_class EXCLUDE (tenant_id WITH =) WHERE (is_default = true AND status = 'active')
);

CREATE INDEX idx_tclasses_tenant ON p.tax_classes(tenant_id);
```

### Operational lifecycle

**Producers**: `mcp.tax-class.upsert`
**Consumers**: `mcp.master.item.assign-tax-class` (referenced by `m.items.tax_class`), `mcp.tax.compute`

### Provenance

- **ARTS reference**: Tax Classification
- **GSLM**: `Taxes` (1 table — minimal in SQL impl; richer in S0 MDM Finance domain)
- **Justification**: Tax class is the *category* (food, alcohol, standard); rate is per-location-per-class (next table). Single default per tenant via EXCLUDE constraint.

---

## p.tax_rates

**ARTS reference**: ARTS Tax Rate (location × class).
**Module**: P / F.

### Schema

```sql
CREATE TABLE p.tax_rates (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  tax_class_id    uuid NOT NULL REFERENCES p.tax_classes(id),
  location_id     uuid REFERENCES l.locations(id),                  -- NULL = applies tenant-wide (default)
  jurisdiction    text,                                              -- "CA", "CA-LA-County", "EU-DE", "EU-DE-Berlin" — for tax-engine integration
  rate_type       text NOT NULL DEFAULT 'percentage',                -- percentage | flat_amount | tiered
  rate            numeric(8,6) NOT NULL,                             -- 0.0825 for 8.25%; for tiered, JSONB schedule in attributes
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,
  attributes      jsonb NOT NULL DEFAULT '{}',                       -- VAT details, GST/HST distinction, multi-rate schedule
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, tax_class_id, COALESCE(location_id, '00000000-0000-0000-0000-000000000000'::uuid), effective_start)
);

CREATE INDEX idx_trates_tenant ON p.tax_rates(tenant_id);
CREATE INDEX idx_trates_class ON p.tax_rates(tax_class_id);
CREATE INDEX idx_trates_location ON p.tax_rates(location_id);
CREATE INDEX idx_trates_active ON p.tax_rates(tax_class_id, location_id) WHERE effective_end IS NULL OR effective_end > CURRENT_DATE;
```

### Operational lifecycle

**Producers**: `mcp.tax-rate.set`, `mcp.tax-rate.from-tax-engine-sync` (Avalara, TaxJar integration)
**Consumers**: `mcp.tax.compute` (at every transaction), `mcp.financial.tax-liability.aggregate`

### Provenance

- **ARTS reference**: Tax Rate
- **GSLM**: `Taxes` + `ItemTaxesInSalesOutlets` (folded)
- **TOM**: F004/F014 carry tax sub-groups (multi-rate per invoice via VAT) — schema supports this via tiered rate_type + JSONB schedule
- **Justification**: Per F-Prefix subagent finding, multi-model tax (VAT vs Sales Tax) was anticipated from day one. JSONB schedule supports complex rate structures (tiered, brackets, surcharges) without column proliferation. NULL location_id for tenant-wide default; specific location_id for jurisdictional overrides.

---

## f.tender_types

**ARTS reference**: ARTS Tender (master).
**Module**: F.

### Schema

```sql
CREATE TABLE f.tender_types (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  code            text NOT NULL,                                  -- CASH, VISA, MC, AMEX, EBT, GIFT, STORE_CREDIT, CHECK
  name            text NOT NULL,
  tender_class    text NOT NULL,                                  -- cash | credit_card | debit_card | gift_card | store_credit | check | electronic_check | ebt_snap | wic | crypto
  is_active       boolean NOT NULL DEFAULT true,
  is_change_giving boolean NOT NULL DEFAULT false,                 -- can give change as this tender (cash yes; gift card no)
  is_refundable   boolean NOT NULL DEFAULT true,
  open_drawer     boolean NOT NULL DEFAULT false,                  -- triggers cash drawer (cash, check)
  gl_account_id   uuid REFERENCES f.gl_accounts(id),               -- accounting destination
  rounding_rule   text,                                             -- nearest_cent | nickel | etc. (cash-rounding for currencies that need it)
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_tender_tenant ON f.tender_types(tenant_id);
CREATE INDEX idx_tender_class ON f.tender_types(tender_class);
```

### Operational lifecycle

**Producers**: `mcp.tender-type.upsert`
**Consumers**: `mcp.transaction.tender.lookup` (at every payment), `mcp.cash-management.drawer-open-trigger`, `mcp.financial.tender-aggregate.by-class`

### Provenance

- **ARTS reference**: Tender (master enumeration)
- **CRDM operational**: `CRDM_Tender` is the operational tender event in T schema; this is the master enumeration
- **Justification**: Tender type is master data (a small fixed set per merchant). The actual payment events live in `t.transaction_tenders`. GL account FK enables direct posting to accounting on each tender.

---

## f.gl_accounts

**ARTS reference**: ARTS GLAccount.
**Module**: F.

### Schema

```sql
CREATE TABLE f.gl_accounts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  parent_id       uuid REFERENCES f.gl_accounts(id),
  code            text NOT NULL,                                  -- merchant chart of accounts code
  name            text NOT NULL,
  account_type    text NOT NULL,                                  -- asset | liability | equity | revenue | expense | contra
  account_subtype text,                                           -- current_asset | inventory | accounts_payable | sales | cogs | etc.
  is_postable     boolean NOT NULL DEFAULT true,                  -- false for parent rollups
  currency        text NOT NULL DEFAULT 'USD',
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, code)
);

CREATE INDEX idx_gl_tenant ON f.gl_accounts(tenant_id);
CREATE INDEX idx_gl_parent ON f.gl_accounts(parent_id);
CREATE INDEX idx_gl_type ON f.gl_accounts(account_type);
```

### Operational lifecycle

**Producers**: `mcp.gl-account.upsert` (manual or sync from accounting system)
**Consumers**: `mcp.financial.tender-post`, `mcp.financial.invoice-post`, `mcp.financial.cogs-post`

### Provenance

- **ARTS reference**: GLAccount
- **TOM**: F-Prefix uses GL coding throughout — F004 ReIM posts into OFi GL accounts
- **Justification**: Standard chart of accounts with parent FK for hierarchy (rollups). Per F-Prefix subagent finding, "GL coding is downstream — F-prefix interfaces are operational pipes; GL account assignment happens inside OFi after ReIM posting." We model GL accounts as canonical because Canary Go integrates with QuickBooks / Xero / NetSuite — needs to know merchant's account structure.

---

## f.supplier_invoices

**ARTS reference**: ARTS SupplierInvoice (AP).
**Module**: F.

### Schema

```sql
CREATE TABLE f.supplier_invoices (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  invoice_number      text NOT NULL,                              -- vendor's invoice number
  vendor_id           uuid NOT NULL REFERENCES m.vendors(id),
  invoice_date        date NOT NULL,
  due_date            date,
  related_po_id       uuid REFERENCES o.purchase_orders(id),      -- three-way match: invoice ↔ PO
  related_receipt_document_id uuid REFERENCES i.inventory_documents(id),  -- invoice ↔ receipt (third leg)
  status              text NOT NULL DEFAULT 'received',           -- received | matched | discrepancy | approved | paid | disputed | cancelled
  subtotal            numeric(14,4) NOT NULL,
  tax_total           numeric(14,4) NOT NULL DEFAULT 0,
  shipping_total      numeric(14,4) NOT NULL DEFAULT 0,
  discount_total      numeric(14,4) NOT NULL DEFAULT 0,
  grand_total         numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  match_status        text NOT NULL DEFAULT 'pending',            -- pending | matched | partial_match | mismatch | manual_override
  match_variance      numeric(14,4),                              -- variance vs PO + receipt
  approval_user_id    uuid REFERENCES app.users(id),
  approved_at         timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',                -- vendor_credit_note_ref, payment_terms_override, original_doc_url
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, vendor_id, invoice_number)
);

CREATE INDEX idx_sinv_tenant ON f.supplier_invoices(tenant_id);
CREATE INDEX idx_sinv_vendor ON f.supplier_invoices(vendor_id);
CREATE INDEX idx_sinv_po ON f.supplier_invoices(related_po_id);
CREATE INDEX idx_sinv_receipt ON f.supplier_invoices(related_receipt_document_id);
CREATE INDEX idx_sinv_status ON f.supplier_invoices(status) WHERE status NOT IN ('paid', 'cancelled');
CREATE INDEX idx_sinv_due ON f.supplier_invoices(due_date) WHERE status = 'approved';
```

### Operational lifecycle

**Producers**: `mcp.supplier-invoice.from-vendor` (F004 / F014 ReIM pattern), `mcp.supplier-invoice.three-way-match` (auto-match against PO + receipt)
**Consumers**: `mcp.payment.create-from-invoice`, `mcp.financial.gl-post.invoice`, `mcp.metrics.ap-aging`

### Provenance

- **ARTS reference**: SupplierInvoice (AP)
- **TOM**: F004 (Supplier Invoice TIMS→ReIM), F014 (Tesco Invoice from ReIM to TIMS)
- **Justification**: FK to both PO and receipt enables three-way match at schema level. `match_status` discriminator captures the matching outcome explicitly. Per F-Prefix subagent finding, "three-way matching is the implicit governing pattern" — Canary Go makes it explicit.

---

## f.supplier_invoice_lines

**ARTS reference**: ARTS SupplierInvoiceLine.
**Module**: F.

### Schema

```sql
CREATE TABLE f.supplier_invoice_lines (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  invoice_id          uuid NOT NULL REFERENCES f.supplier_invoices(id) ON DELETE CASCADE,
  line_number         int NOT NULL,
  related_po_line_id  uuid REFERENCES o.purchase_order_lines(id),     -- three-way match at line level
  related_receipt_line_id uuid REFERENCES i.inventory_document_lines(id),
  item_id             uuid REFERENCES m.items(id),                    -- nullable for non-merchandise lines (freight, fees)
  description         text NOT NULL,
  quantity            numeric(14,4),
  unit_cost           numeric(14,4),
  line_total          numeric(14,4) NOT NULL,
  tax_amount          numeric(14,4) NOT NULL DEFAULT 0,
  gl_account_id       uuid REFERENCES f.gl_accounts(id),              -- override GL account for this line
  match_variance      numeric(14,4),                                  -- variance vs PO line / receipt line
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, invoice_id, line_number)
);

CREATE INDEX idx_sinvline_tenant ON f.supplier_invoice_lines(tenant_id);
CREATE INDEX idx_sinvline_invoice ON f.supplier_invoice_lines(invoice_id);
CREATE INDEX idx_sinvline_po_line ON f.supplier_invoice_lines(related_po_line_id);
CREATE INDEX idx_sinvline_receipt_line ON f.supplier_invoice_lines(related_receipt_line_id);
```

### Operational lifecycle

**Producers**: `mcp.supplier-invoice-line.add`, `mcp.supplier-invoice-line.three-way-match-line`
**Consumers**: `mcp.financial.line-variance-report`, `mcp.financial.gl-post.line`

### Provenance

- **ARTS reference**: SupplierInvoiceLine
- **TOM**: F004/F014 invoice line records
- **Justification**: Line-level three-way match (vs PO line + receipt line) catches discrepancies at item granularity. Item FK nullable because some invoice lines are non-merchandise (freight, fees, adjustments).

---

## f.payments

**ARTS reference**: ARTS Payment (AP outbound).
**Module**: F.

### Schema

```sql
CREATE TABLE f.payments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  payment_number      text NOT NULL,
  vendor_id           uuid NOT NULL REFERENCES m.vendors(id),
  payment_method      text NOT NULL,                              -- check | ach | wire | credit_card | virtual_card
  payment_date        date NOT NULL,
  amount              numeric(14,4) NOT NULL,
  currency            text NOT NULL DEFAULT 'USD',
  bank_account_id     uuid,                                       -- references app.bank_accounts in current Canary spec
  reference_number    text,                                       -- check #, wire ref, ACH trace
  status              text NOT NULL DEFAULT 'scheduled',          -- scheduled | issued | cleared | voided | bounced
  cleared_at          timestamptz,
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, payment_number)
);

CREATE TABLE f.payment_invoice_applications (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  payment_id          uuid NOT NULL REFERENCES f.payments(id) ON DELETE CASCADE,
  invoice_id          uuid NOT NULL REFERENCES f.supplier_invoices(id),
  amount_applied      numeric(14,4) NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_pay_tenant ON f.payments(tenant_id);
CREATE INDEX idx_pay_vendor ON f.payments(vendor_id);
CREATE INDEX idx_pay_status ON f.payments(status) WHERE status IN ('scheduled', 'issued');
CREATE INDEX idx_pay_invapp_payment ON f.payment_invoice_applications(payment_id);
CREATE INDEX idx_pay_invapp_invoice ON f.payment_invoice_applications(invoice_id);
```

### Operational lifecycle

**Producers**: `mcp.payment.schedule-from-invoice`, `mcp.payment.issue`, `mcp.payment.bank-clear`, `mcp.payment.void`
**Consumers**: `mcp.financial.gl-post.payment`, `mcp.metrics.cash-flow-projection`

### Provenance

- **ARTS reference**: Payment (AP)
- **TOM**: not modeled in F-Prefix corpus (Tesco's payment was downstream of OFi)
- **Justification**: Many-to-many payment-invoice via `payment_invoice_applications` supports multi-invoice payments (single check covers multiple invoices) and partial payments (one invoice paid across multiple cheques).

---

## Domain summary

**10 entities (+ 1 join), 2 schemas (p, f), 2 modules (P + F)**:
- `p.item_prices` (~16 cols, EXCLUDE temporal constraint) — multi-scope pricing
- `p.promotions` (~22 cols, array scoping) — header
- `p.promotion_rules` (~7 cols, JSONB triggers/benefits) — flexible rules
- `p.tax_classes` (~9 cols, EXCLUDE single-default) — tax category master
- `p.tax_rates` (~12 cols) — location × class effective-dated
- `f.tender_types` (~12 cols) — payment method master
- `f.gl_accounts` (~13 cols, recursive parent) — chart of accounts
- `f.supplier_invoices` (~22 cols, three-way match FKs) — AP invoice
- `f.supplier_invoice_lines` (~14 cols) — invoice detail with line-level match
- `f.payments` + `f.payment_invoice_applications` (~14 + 4 cols) — AP payment with many-to-many invoice application

**Folded from sources**:
- GSLM Price 6 entities → 5 (Promotion 5-table chain → 2 with JSONB triggers)
- GSLM Finance 13 entities (S0 MDM site) → 5 essentials (tender, GL, AP invoice + lines, payment)
- TOM F-Prefix 5 interfaces → 5 entities + ~10 MCP service junctions
- TOM C010US (price details) → `p.item_prices` + `p.promotions`

**MCP service junctions defined for this domain (~22)**:
- Pricing: item-price.set, item-price.from-promotion, item-price.expire, item-price.import-from-pos, promotion.{create,activate,pause,expire-on-schedule}, promotion-rule.{add,update}, tax-class.upsert, tax-rate.set, tax-rate.from-tax-engine-sync
- Financial: tender-type.upsert, gl-account.upsert, supplier-invoice.from-vendor, supplier-invoice.three-way-match, supplier-invoice-line.add, supplier-invoice-line.three-way-match-line, payment.{schedule-from-invoice, issue, bank-clear, void}
- Cross-cutting consumers: tax.compute, transaction.price.resolve, transaction.tender.lookup, financial.gl-post.{tender, invoice, cogs, payment}, metrics.{price-elasticity, ap-aging, cash-flow-projection}

## Status

- **Chunk 6 complete.** 10 ARTS-aligned Pricing + Financial entities. Three-way match instrumented. Multi-model tax supported.
- **Resume**: Chunk 7 — POSLog + Sales Audit (t schema). ARTS POSLog standard + CRDM operational POS as concrete reference. Target ~8-10 entities.

---

# §9 POSLog + Sales Audit Domain

**ARTS anchor**: ARTS POSLog (XML standard for POS transaction logging) + ARTS Sales Audit.
**Module**: T (Transaction Pipeline).
**Entities**: 9 (transactions · transaction_line_items · transaction_tenders · transaction_discounts · cashier_actions · cash_drawer_events · shift_events · loyalty_events · gift_card_events).
**Folded from sources**: CRDM 25 POS entities → 9 SMB-2030 canonical via aggregation-table separation (FastFact-style aggregates → metrics schema, not here).

## Domain narrative

ARTS POSLog is the public XML standard for capturing point-of-sale transaction events. Every major POS vendor (NCR, Oracle Retail, IBM, etc.) implements POSLog for output; many implement it for input. SMB-2030 canonical T-schema honors the POSLog field structure but in relational form (Postgres rather than XML).

CRDM's 25 POS entities collapsed by separating **operational events** from **denormalized aggregations**:
- Operational events stay in `t` schema (this chunk) — every transaction, line, tender, discount captured atomically
- Pre-aggregated summaries (CRDM_FastFact ~110 columns, CRDM_ItemFastFact, scorecards) live in `metrics` schema as views/materialized views computed from `t` — they're NOT canonical entities

Returns are modeled as regular transactions with `transaction_type='return'` and `parent_transaction_id` FK to the original. Cancellations are status changes, not separate entities.

The T schema is the **single highest-volume schema** — every POS scan, every receipt, every tender. Indexing strategy is critical. Partitioning by `business_date` is recommended at scale (>10M transactions/year) but not required for SMB-2030 v1 (typical merchant: 50K-1M transactions/year per location).

This is the domain that connects to every other domain via FK: items (line items), customers (loyalty), employees (cashiers), locations (stores), tender_types (payment), promotions (discount source), inventory_movements (stock decrement on sale). Most queries against T are by (location, business_date) range — both are first-class columns.

---

## t.transactions

**ARTS reference**: ARTS POSLog Transaction header.
**Module**: T.

### Schema

```sql
CREATE TABLE t.transactions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_number      text NOT NULL,                              -- POS-native or system-assigned (uniqueness scope below)
  transaction_type        text NOT NULL DEFAULT 'sale',               -- sale | return | exchange | void | no_sale | layaway | quote | training
  parent_transaction_id   uuid REFERENCES t.transactions(id),         -- for returns / exchanges / voids
  location_id             uuid NOT NULL REFERENCES l.locations(id),
  pos_terminal_id         text,                                       -- POSNo (CRDM equivalent); links to n.devices when device schema exists
  cashier_employee_id     uuid REFERENCES e.employees(id),            -- the operator
  customer_id             uuid REFERENCES c.customers(id),            -- the customer (NULL for anonymous)
  loyalty_membership_id   uuid REFERENCES c.loyalty_memberships(id),
  business_date           date NOT NULL,                              -- the trading day (CRDM TradingDay equivalent)
  started_at              timestamptz NOT NULL,                       -- transaction start
  ended_at                timestamptz NOT NULL,                       -- transaction complete
  status                  text NOT NULL DEFAULT 'completed',          -- pending | completed | voided | suspended | recalled
  ticket_number           int,                                         -- TicketNo (per-day sequence)
  item_count              int NOT NULL DEFAULT 0,
  subtotal                numeric(14,4) NOT NULL DEFAULT 0,
  tax_total               numeric(14,4) NOT NULL DEFAULT 0,
  discount_total          numeric(14,4) NOT NULL DEFAULT 0,
  grand_total             numeric(14,4) NOT NULL DEFAULT 0,
  currency                text NOT NULL DEFAULT 'USD',
  channel                 text NOT NULL DEFAULT 'pos',                -- pos | self_checkout | mobile_pos | online | phone
  pos_software_version    text,                                       -- CodeVersion (CRDM equivalent)
  is_training_mode        boolean NOT NULL DEFAULT false,             -- TrainingModeFlg
  is_offline              boolean NOT NULL DEFAULT false,             -- POSOfflineFlg (transaction created offline, synced later)
  is_reentered            boolean NOT NULL DEFAULT false,             -- ReenteredTransactionFlg
  is_suspended            boolean NOT NULL DEFAULT false,
  void_reason             text,                                       -- if voided
  attributes              jsonb NOT NULL DEFAULT '{}',                -- gift receipt printed, VAT receipt, custom flags
  external_ids            jsonb DEFAULT '{}',                         -- {pos_native_id, square_payment_id, processor_ref}
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, business_date, transaction_number)
);

CREATE INDEX idx_tx_tenant ON t.transactions(tenant_id);
CREATE INDEX idx_tx_location_date ON t.transactions(location_id, business_date);
CREATE INDEX idx_tx_cashier ON t.transactions(cashier_employee_id, business_date);
CREATE INDEX idx_tx_customer ON t.transactions(customer_id) WHERE customer_id IS NOT NULL;
CREATE INDEX idx_tx_loyalty ON t.transactions(loyalty_membership_id) WHERE loyalty_membership_id IS NOT NULL;
CREATE INDEX idx_tx_parent ON t.transactions(parent_transaction_id) WHERE parent_transaction_id IS NOT NULL;
CREATE INDEX idx_tx_started ON t.transactions(started_at);
CREATE INDEX idx_tx_status ON t.transactions(status) WHERE status != 'completed';
CREATE INDEX idx_tx_external_ids ON t.transactions USING gin(external_ids);
```

### Operational lifecycle

**Producers**:
- `mcp.transaction.start` — when first scan or transaction-open event
- `mcp.transaction.complete` — when payment cleared
- `mcp.transaction.void` — manager-authorized void (creates new void transaction with parent FK)
- `mcp.transaction.return-from-receipt` — return transaction created with parent FK to original
- `mcp.transaction.suspend-and-recall` — suspend / recall flow

**Consumers**:
- `mcp.inventory.movement.from-transaction` — generates inventory movements for sold items
- `mcp.financial.tender-aggregate.by-transaction` — for cash drawer reconciliation
- `mcp.loyalty.points-earn.from-transaction` — earn calculation
- `mcp.metrics.fact-transaction.from-tx` — feeds metrics schema fact tables (TOM J004 Sales TDS→UDD pattern)
- `mcp.q.detection.scan-transaction` — Chirp rules evaluate every transaction (Q module)
- `mcp.audit.transaction.export` — for sales audit reports

**SLA at producer**: real-time, p95 < 500ms (transaction commit includes all related lines/tenders atomically).
**SLA at consumers**: inventory movement < 1s, metrics fact rollup < 5s, Chirp detection < 30s.

### Provenance

- **ARTS reference**: POSLog Transaction header
- **CRDM folded**: `CRDM_Header` + portions of `CRDM_FastFact` (most FastFact columns are aggregations → metrics schema)
- **TOM**: not in TOM corpus (Tesco POS was outside TOM scope; TOM is back-office)
- **Canary current**: `sales.transactions` (4 cols detailed) — superseded; expanded to ARTS POSLog field set
- **Justification**: Single transactions table with `transaction_type` discriminator covers sale/return/exchange/void/no-sale/layaway/quote/training — CRDM's 5+ transaction-flag columns become a single discriminator. Parent FK enables full return/void traceability. `business_date` first-class column (not derived from `started_at`) handles trading-day semantics where the trading day spans midnight (CRDM had explicit TradingDay column for this reason).

---

## t.transaction_line_items

**ARTS reference**: ARTS POSLog SaleLineItem.
**Module**: T.

### Schema

```sql
CREATE TABLE t.transaction_line_items (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES t.transactions(id) ON DELETE CASCADE,
  line_number             int NOT NULL,
  item_id                 uuid REFERENCES m.items(id),                -- nullable for unknown items (sold-as-not-on-file)
  barcode_scanned         text,                                       -- the actual barcode (for not-on-file recovery)
  description             text NOT NULL,                              -- snapshot (item description at sale time)
  quantity                numeric(14,4) NOT NULL,
  unit_of_measure         text NOT NULL DEFAULT 'EA',
  unit_price              numeric(14,4) NOT NULL,                     -- price at sale (snapshot from p.item_prices)
  list_price              numeric(14,4),                              -- original price before discounts
  unit_discount           numeric(14,4) NOT NULL DEFAULT 0,
  unit_tax                numeric(14,4) NOT NULL DEFAULT 0,
  extended_price          numeric(14,4) GENERATED ALWAYS AS (quantity * (unit_price - unit_discount)) STORED,
  extended_tax            numeric(14,4) GENERATED ALWAYS AS (quantity * unit_tax) STORED,
  line_total              numeric(14,4) GENERATED ALWAYS AS ((quantity * (unit_price - unit_discount)) + (quantity * unit_tax)) STORED,
  cost_basis              numeric(14,4),                              -- weighted-avg cost at sale (for margin)
  margin                  numeric(14,4) GENERATED ALWAYS AS (((quantity * (unit_price - unit_discount)) - (quantity * COALESCE(cost_basis, 0)))) STORED,
  category_id             uuid REFERENCES m.product_categories(id),    -- snapshot for analytics (item may be re-categorized later)
  zone_id                 uuid REFERENCES l.location_zones(id),        -- where it was scanned (for shrink-by-zone)
  lot_id                  uuid REFERENCES i.inventory_lots(id),        -- if lot-tracked
  inventory_movement_id   uuid REFERENCES i.inventory_movements(id),   -- the resulting stock decrement
  is_void                 boolean NOT NULL DEFAULT false,              -- line voided within transaction
  void_reason             text,
  is_return               boolean NOT NULL DEFAULT false,              -- return line (negative quantity)
  return_reason           text,                                        -- DamagedReceived | NoLongerNeeded | Defective | etc.
  is_weighable            boolean NOT NULL DEFAULT false,
  is_food_stamp_eligible  boolean NOT NULL DEFAULT false,              -- copy of m.items value at sale time (for audit)
  attributes              jsonb NOT NULL DEFAULT '{}',                 -- {gift_wrap, customizations, age_verified_by_employee_id, scan_method}
  created_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, transaction_id, line_number)
);

CREATE INDEX idx_lines_tenant ON t.transaction_line_items(tenant_id);
CREATE INDEX idx_lines_tx ON t.transaction_line_items(transaction_id);
CREATE INDEX idx_lines_item ON t.transaction_line_items(item_id);
CREATE INDEX idx_lines_category ON t.transaction_line_items(category_id);
CREATE INDEX idx_lines_zone ON t.transaction_line_items(zone_id);
CREATE INDEX idx_lines_returns ON t.transaction_line_items(transaction_id) WHERE is_return = true;
CREATE INDEX idx_lines_voids ON t.transaction_line_items(transaction_id) WHERE is_void = true;
CREATE INDEX idx_lines_unknown ON t.transaction_line_items(barcode_scanned) WHERE item_id IS NULL;
```

### Operational lifecycle

**Producers**: `mcp.transaction.line.add`, `mcp.transaction.line.void`, `mcp.transaction.line.return`
**Consumers**: `mcp.inventory.movement.from-line`, `mcp.metrics.fact-sale-line`, `mcp.q.detection.line-pattern` (shrink, sweethearting, scan-then-void)

### Provenance

- **ARTS reference**: POSLog SaleLineItem
- **CRDM folded**: `CRDM_Item` + portions of `CRDM_ItemFastFact`
- **Justification**: Generated columns (`extended_price`, `extended_tax`, `line_total`, `margin`) ensure consistency. `item_id` nullable + `barcode_scanned` populated for not-on-file lines (sold but unrecognized barcode — Q-module signal). Snapshots (description, category_id, is_food_stamp_eligible) preserve sale-time values for audit even when master data changes.

---

## t.transaction_tenders

**ARTS reference**: ARTS POSLog Tender.
**Module**: T.

### Schema

```sql
CREATE TABLE t.transaction_tenders (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES t.transactions(id) ON DELETE CASCADE,
  tender_sequence         int NOT NULL,
  tender_type_id          uuid NOT NULL REFERENCES f.tender_types(id),
  amount                  numeric(14,4) NOT NULL,
  currency                text NOT NULL DEFAULT 'USD',
  cash_back_amount        numeric(14,4) NOT NULL DEFAULT 0,           -- debit cash-back
  change_amount           numeric(14,4) NOT NULL DEFAULT 0,           -- change given (cash)
  card_token              text,                                       -- masked card number (PII tier 2: tokenized, not raw PAN)
  card_last_4             text,
  card_brand              text,                                       -- VISA | MC | AMEX | DISC
  authorization_code      text,
  processor_reference     text,                                       -- gateway transaction ID
  is_voided               boolean NOT NULL DEFAULT false,
  is_refund               boolean NOT NULL DEFAULT false,             -- this tender is the refund leg
  contactless             boolean NOT NULL DEFAULT false,
  attributes              jsonb NOT NULL DEFAULT '{}',                -- gift card balance after, EBT eligible amount, etc.
  created_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, transaction_id, tender_sequence)
);

CREATE INDEX idx_tend_tenant ON t.transaction_tenders(tenant_id);
CREATE INDEX idx_tend_tx ON t.transaction_tenders(transaction_id);
CREATE INDEX idx_tend_type ON t.transaction_tenders(tender_type_id);
CREATE INDEX idx_tend_card ON t.transaction_tenders(card_last_4) WHERE card_last_4 IS NOT NULL;
CREATE INDEX idx_tend_processor ON t.transaction_tenders(processor_reference) WHERE processor_reference IS NOT NULL;
```

### Operational lifecycle

**Producers**: `mcp.transaction.tender.add`, `mcp.transaction.tender.void`, `mcp.transaction.tender.refund`
**Consumers**: `mcp.cash-management.drawer-update`, `mcp.financial.tender-aggregate.daily`, `mcp.q.detection.tender-pattern` (suspicious refund-only-card patterns)

### Provenance

- **ARTS reference**: POSLog Tender
- **CRDM folded**: `CRDM_Tender` (most fields preserved; some columns like SmartCardFlg fold into JSONB)
- **Justification**: Card token only (PII tier 2) — never raw PAN. Cashback and change as separate columns enable proper drawer reconciliation. Multi-tender per transaction (sequence) supports split tender (partial cash + partial card).

---

## t.transaction_discounts

**ARTS reference**: ARTS POSLog Discount + LineDiscount.
**Module**: T.

### Schema

```sql
CREATE TABLE t.transaction_discounts (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid NOT NULL REFERENCES t.transactions(id) ON DELETE CASCADE,
  discount_sequence       int NOT NULL,
  scope                   text NOT NULL,                              -- transaction | line | tender
  line_item_id            uuid REFERENCES t.transaction_line_items(id) ON DELETE CASCADE,  -- if scope=line
  discount_type           text NOT NULL,                              -- promotion | manual_override | manager_discount | staff_discount | loyalty_redeem | coupon | senior | military | etc.
  source_promotion_id     uuid REFERENCES p.promotions(id),
  promotion_rule_id       uuid REFERENCES p.promotion_rules(id),
  amount                  numeric(14,4) NOT NULL,                     -- the actual amount discounted
  percentage              numeric(5,4),                                -- if percentage-based
  reason_code             text,
  authorized_by_employee_id uuid REFERENCES e.employees(id),         -- manager override
  attributes              jsonb NOT NULL DEFAULT '{}',                -- coupon code, loyalty redemption details
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_disc_tenant ON t.transaction_discounts(tenant_id);
CREATE INDEX idx_disc_tx ON t.transaction_discounts(transaction_id);
CREATE INDEX idx_disc_line ON t.transaction_discounts(line_item_id);
CREATE INDEX idx_disc_promo ON t.transaction_discounts(source_promotion_id);
CREATE INDEX idx_disc_type ON t.transaction_discounts(discount_type);
CREATE INDEX idx_disc_authorizer ON t.transaction_discounts(authorized_by_employee_id) WHERE authorized_by_employee_id IS NOT NULL;
```

### Operational lifecycle

**Producers**: `mcp.transaction.discount.apply`, `mcp.transaction.discount.void`
**Consumers**: `mcp.metrics.discount-effectiveness`, `mcp.q.detection.discount-abuse` (manager-discount frequency, staff-discount patterns), `mcp.loyalty.redeem.from-discount`

### Provenance

- **ARTS reference**: POSLog Discount + LineDiscount
- **CRDM folded**: `CRDM_TransactionDiscount` + `CRDM_ItemDiscount` (2 entities → 1 with `scope` discriminator)
- **Justification**: Single table with `scope` discriminator (transaction-wide, line-specific, tender-specific) covers all discount kinds. `authorized_by_employee_id` is critical for Q-module audit (who approved the override?).

---

## t.cashier_actions

**ARTS reference**: ARTS POSLog OperationalEvent.
**Module**: T (Q-relevant).

### Schema

```sql
CREATE TABLE t.cashier_actions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  transaction_id          uuid REFERENCES t.transactions(id),         -- nullable for between-transaction actions
  location_id             uuid NOT NULL REFERENCES l.locations(id),
  cashier_employee_id     uuid NOT NULL REFERENCES e.employees(id),
  pos_terminal_id         text,
  action_type             text NOT NULL,                              -- key_lock_change | manager_override | drawer_open | price_check | item_lookup | suspend | recall | training_mode_toggle | refund_authorize
  performed_at            timestamptz NOT NULL DEFAULT now(),
  authorized_by_employee_id uuid REFERENCES e.employees(id),         -- if requires manager auth
  details                 jsonb NOT NULL DEFAULT '{}',                -- action-specific payload
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_actions_tenant ON t.cashier_actions(tenant_id);
CREATE INDEX idx_actions_tx ON t.cashier_actions(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_actions_cashier ON t.cashier_actions(cashier_employee_id, performed_at);
CREATE INDEX idx_actions_type ON t.cashier_actions(action_type);
CREATE INDEX idx_actions_authorizer ON t.cashier_actions(authorized_by_employee_id) WHERE authorized_by_employee_id IS NOT NULL;
```

### Operational lifecycle

**Producers**: `mcp.cashier.action.log` — every cashier action logged
**Consumers**: `mcp.q.detection.action-pattern` (override frequency, drawer-open without sale), `mcp.audit.cashier-activity-report`

### Provenance

- **ARTS reference**: POSLog OperationalEvent
- **CRDM folded**: `CRDM_OperatorAction`
- **Justification**: Critical for Q-module shrink detection — patterns like "frequent manager overrides by cashier X" or "drawer opens without transaction" are first-class signals.

---

## t.cash_drawer_events

**ARTS reference**: ARTS Sales Audit — drawer open/close, count.
**Module**: T.

### Schema

```sql
CREATE TABLE t.cash_drawer_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  location_id             uuid NOT NULL REFERENCES l.locations(id),
  pos_terminal_id         text NOT NULL,
  cashier_employee_id     uuid REFERENCES e.employees(id),
  event_type              text NOT NULL,                              -- shift_start_count | shift_end_count | mid_shift_count | paid_in | paid_out | safe_drop | float_pull
  event_at                timestamptz NOT NULL DEFAULT now(),
  expected_amount         numeric(14,4),                              -- system-computed expected balance
  counted_amount          numeric(14,4),                              -- physically counted
  variance                numeric(14,4) GENERATED ALWAYS AS (COALESCE(counted_amount, 0) - COALESCE(expected_amount, 0)) STORED,
  reason                  text,
  paid_in_out_amount      numeric(14,4),                              -- for paid_in / paid_out events
  reference               text,                                       -- supplier name (paid_out), invoice ref (paid_in)
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_drawer_tenant ON t.cash_drawer_events(tenant_id);
CREATE INDEX idx_drawer_location_terminal ON t.cash_drawer_events(location_id, pos_terminal_id, event_at);
CREATE INDEX idx_drawer_cashier ON t.cash_drawer_events(cashier_employee_id, event_at);
CREATE INDEX idx_drawer_variance ON t.cash_drawer_events(location_id) WHERE variance IS NOT NULL AND variance != 0;
```

### Operational lifecycle

**Producers**: `mcp.cash-drawer.shift-start`, `mcp.cash-drawer.count`, `mcp.cash-drawer.paid-in`, `mcp.cash-drawer.paid-out`, `mcp.cash-drawer.shift-end`
**Consumers**: `mcp.financial.cash-reconciliation`, `mcp.q.detection.drawer-variance`, `mcp.metrics.cashier-accuracy`

### Provenance

- **ARTS reference**: Sales Audit drawer events
- **CRDM folded**: `CRDM_CashOfficeSafe` + `CRDM_PaidIn_PaidOut`
- **Canary current**: `sales.cash_drawer_shifts` + `sales.cash_drawer_events` — this consolidates and enriches
- **Justification**: Generated `variance` column makes shrink-detection queries O(1) per event. Cash drawer variance is the second-most-watched Q signal after refund-fraud.

---

## t.shift_events

**ARTS reference**: ARTS Sales Audit — operator session.
**Module**: T.

### Schema

```sql
CREATE TABLE t.shift_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  location_id             uuid NOT NULL REFERENCES l.locations(id),
  pos_terminal_id         text NOT NULL,
  cashier_employee_id     uuid NOT NULL REFERENCES e.employees(id),
  shift_start             timestamptz NOT NULL,
  shift_end               timestamptz,                                 -- NULL = active shift
  transaction_count       int NOT NULL DEFAULT 0,                     -- denormalized
  total_sales             numeric(14,4),                               -- denormalized
  starting_drawer_amount  numeric(14,4),
  ending_drawer_amount    numeric(14,4),
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, location_id, pos_terminal_id, cashier_employee_id, shift_start)
);

CREATE INDEX idx_shifts_tenant ON t.shift_events(tenant_id);
CREATE INDEX idx_shifts_location ON t.shift_events(location_id);
CREATE INDEX idx_shifts_cashier ON t.shift_events(cashier_employee_id);
CREATE INDEX idx_shifts_active ON t.shift_events(location_id) WHERE shift_end IS NULL;
```

### Operational lifecycle

**Producers**: `mcp.shift.start`, `mcp.shift.end`, `mcp.shift.update-running-totals`
**Consumers**: `mcp.metrics.cashier-productivity`, `mcp.q.detection.shift-anomaly`, `mcp.scheduling.actual-vs-planned`

### Provenance

- **ARTS reference**: Sales Audit operator session
- **Justification**: Denormalized counts and totals on shift row for fast cashier-shift performance queries without re-aggregating transactions.

---

## t.loyalty_events

**ARTS reference**: ARTS Loyalty Event (earn / redeem / adjustment).
**Module**: T.

### Schema

```sql
CREATE TABLE t.loyalty_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  loyalty_membership_id   uuid NOT NULL REFERENCES c.loyalty_memberships(id),
  transaction_id          uuid REFERENCES t.transactions(id),         -- if event is transaction-derived
  event_type              text NOT NULL,                              -- earn | redeem | manual_adjustment | tier_upgrade | tier_downgrade | bonus | expire | enroll
  points_delta            bigint NOT NULL,                            -- signed; positive = added, negative = used
  amount_basis            numeric(14,4),                              -- $ amount that earned the points
  reason                  text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_loyalty_evt_tenant ON t.loyalty_events(tenant_id);
CREATE INDEX idx_loyalty_evt_member ON t.loyalty_events(loyalty_membership_id, created_at);
CREATE INDEX idx_loyalty_evt_tx ON t.loyalty_events(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_loyalty_evt_type ON t.loyalty_events(event_type);
```

### Operational lifecycle

**Producers**: `mcp.loyalty.event.from-transaction`, `mcp.loyalty.event.manual-adjustment`, `mcp.loyalty.event.tier-evaluate`
**Consumers**: `mcp.loyalty.points-balance.recompute` (sum-of-deltas — backstop for c.loyalty_memberships.points_balance), `mcp.metrics.loyalty-engagement`

### Provenance

- **ARTS reference**: Loyalty Event
- **CRDM folded**: `CRDM_LoyaltyCard` + `CRDM_PointsCoupon`
- **Justification**: Append-only event log gives full audit trail and recompute capability. The denormalized `c.loyalty_memberships.points_balance` is updated atomically with each event; `t.loyalty_events` is the source of truth.

---

## t.gift_card_events

**ARTS reference**: ARTS Gift Card Activity.
**Module**: T.

### Schema

```sql
CREATE TABLE t.gift_card_events (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  gift_card_id            uuid NOT NULL,                              -- references app.gift_cards (current Canary spec)
  transaction_id          uuid REFERENCES t.transactions(id),
  event_type              text NOT NULL,                              -- activate | reload | redeem | refund | expire | inquiry | adjustment
  amount_delta            numeric(14,4) NOT NULL,                     -- signed
  balance_after           numeric(14,4) NOT NULL,
  authorization_code      text,
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_gc_tenant ON t.gift_card_events(tenant_id);
CREATE INDEX idx_gc_card ON t.gift_card_events(gift_card_id, created_at);
CREATE INDEX idx_gc_tx ON t.gift_card_events(transaction_id) WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_gc_type ON t.gift_card_events(event_type);
```

### Operational lifecycle

**Producers**: `mcp.gift-card.event.from-transaction`, `mcp.gift-card.event.from-online-portal`
**Consumers**: `mcp.gift-card.balance.compute`, `mcp.financial.deferred-revenue.aggregate-gift-cards`, `mcp.q.detection.gift-card-fraud-pattern`

### Provenance

- **ARTS reference**: Gift Card Activity
- **CRDM folded**: `CRDM_GiftCard` (operational events; the gift card master is `app.gift_cards` in current Canary spec, preserved)
- **Justification**: Append-only event log. Gift card balance is a financial liability (deferred revenue) — accurate event log enables both customer-facing balance display and financial-statement gift-card-liability calculation.

---

## Domain summary

**9 entities, 1 schema (t), 1 module (T)**:
- `t.transactions` (~30 cols) — header with parent FK for returns/voids
- `t.transaction_line_items` (~24 cols, 4 generated) — full POSLog line detail
- `t.transaction_tenders` (~15 cols) — multi-tender, card-tokenized
- `t.transaction_discounts` (~13 cols) — single table with scope discriminator
- `t.cashier_actions` (~10 cols) — operator action log
- `t.cash_drawer_events` (~13 cols, generated variance) — drawer reconciliation
- `t.shift_events` (~13 cols) — operator session with denormalized totals
- `t.loyalty_events` (~9 cols, append-only) — earn/redeem log
- `t.gift_card_events` (~9 cols, append-only) — gift card activity log

**Folded from sources**:
- CRDM 25 POS entities → 9 transaction entities (others: aggregations like `CRDM_FastFact`, `CRDM_ItemFastFact` → metrics schema; movements like `CRDM_GoodsReceived`, `CRDM_StockAdjustment`, etc. → i schema (Chunk 5); customer/employee → c/e schemas)

**MCP service junctions defined for this domain (~22)**:
- Transaction: start, complete, void, return-from-receipt, suspend-and-recall, line.{add, void, return}, tender.{add, void, refund}, discount.{apply, void}
- Cashier: action.log
- Cash drawer: shift-start, count, paid-in, paid-out, shift-end
- Shift: start, end, update-running-totals
- Loyalty: event.from-transaction, event.manual-adjustment, event.tier-evaluate
- Gift card: event.from-transaction, event.from-online-portal
- Cross-cutting consumers: inventory.movement.from-transaction, financial.tender-aggregate.daily, metrics.fact-transaction, q.detection.scan-transaction, audit.transaction.export, q.detection.{action-pattern, drawer-variance, tender-pattern, gift-card-fraud-pattern}, loyalty.points-balance.recompute, gift-card.balance.compute

## Status

- **Chunk 7 complete.** 9 ARTS-POSLog-aligned transaction entities. CRDM 25 POS entities folded with aggregates correctly routed to metrics schema.
- **Resume**: Chunk 8 — Canary platform mechanics (Chirp / Fox / Hawk / Owl / ILDWAC / identity / audit / etc.). Target ~10-15 entities. These sit ABOVE ARTS — Canary-specific.

---

# §10 Canary Platform Mechanics

**Anchor**: Canary Go platform spec — these are not ARTS entities. They're Canary-specific mechanics that ride above ARTS.
**Modules**: Q (Loss Prevention) + cross-cutting (identity, audit, ledger, agent memory).
**Entities**: 15 (across 4 schemas).

## Domain narrative

ARTS covers retail business entities. Canary's value-add is the **agentic platform layer above retail**: detection rules that emit signals, cases that investigate them, evidence chains that prove integrity, ledger positions that track per-tenant cost-to-serve, and the agent memory + audit infrastructure that ties it together.

This chunk follows a different rule than chunks 2-7: instead of folding sources, we **preserve existing Canary patterns** where they work and only redesign where SMB-2030 + ARTS-anchoring rules apply. Most identity/audit entities already exist in current `app` schema and are well-formed (UUID PKs, schema-per-tenant, Postgres-native). We name them in canonical, preserve as-is, and add the missing q-schema, ledger-schema, and memory-schema entities.

The Q schema is the **operational canary** (the warning-bird metaphor): detection rules emit signals → signals open cases → cases collect evidence → evidence anchors to L2 blockchain → cases drive actions. The ledger schema is the **financial canary** (the accountability rails): every operation costs satoshis, tracked per-tenant, payable via L402, anchored to chain.

---

## Q schema — Loss Prevention (6 entities)

### q.detection_rules

**Module**: Q.

```sql
CREATE TABLE q.detection_rules (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  rule_code       text NOT NULL,                              -- merchant or system-assigned
  name            text NOT NULL,
  description     text,
  rule_category   text NOT NULL,                              -- shrink | fraud | discount_abuse | tender_pattern | scan_avoidance | refund_pattern | drawer_variance | etc.
  rule_definition jsonb NOT NULL,                             -- the actual rule logic — SQL template + thresholds + filters
  severity        text NOT NULL DEFAULT 'medium',             -- low | medium | high | critical
  status          text NOT NULL DEFAULT 'active',             -- active | paused | retired
  evaluation_frequency text NOT NULL DEFAULT 'on_event',      -- on_event | hourly | daily | weekly
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, rule_code)
);

CREATE INDEX idx_qrules_tenant ON q.detection_rules(tenant_id);
CREATE INDEX idx_qrules_category ON q.detection_rules(rule_category);
CREATE INDEX idx_qrules_active ON q.detection_rules(tenant_id, evaluation_frequency) WHERE status = 'active';
```

**Lifecycle**: producers `mcp.q.rule.{create,update,activate,pause}`; consumers `mcp.q.detection.evaluate-on-event`, `mcp.q.detection.scheduled-batch`, `mcp.q.metrics.rule-effectiveness`.

**Provenance**: Canary current `app.detection_rules` (preserved with q-schema move + JSONB rule definition). Folds CRDM `CRDM_OperatorAction` patterns + scorecard signals from recovery DDL.

### q.detections

**Module**: Q. (Append-only event log of detected signals.)

```sql
CREATE TABLE q.detections (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  rule_id             uuid NOT NULL REFERENCES q.detection_rules(id),
  detected_at         timestamptz NOT NULL DEFAULT now(),
  source_entity_type  text NOT NULL,                              -- transaction | line_item | tender | drawer_event | shift | cashier_action
  source_entity_id    uuid NOT NULL,
  location_id         uuid REFERENCES l.locations(id),
  cashier_employee_id uuid REFERENCES e.employees(id),
  customer_id         uuid REFERENCES c.customers(id),
  severity            text NOT NULL,
  signal_strength     numeric(5,4),                                -- 0.0-1.0 confidence
  evidence            jsonb NOT NULL DEFAULT '{}',                 -- snapshot of source data
  case_id             uuid REFERENCES q.cases(id),                 -- nullable — case opened only if escalated
  status              text NOT NULL DEFAULT 'new',                 -- new | acknowledged | escalated_to_case | dismissed | duplicate
  acknowledged_at     timestamptz,
  acknowledged_by     uuid REFERENCES app.users(id),
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_qdet_tenant ON q.detections(tenant_id);
CREATE INDEX idx_qdet_rule ON q.detections(rule_id, detected_at);
CREATE INDEX idx_qdet_source ON q.detections(source_entity_type, source_entity_id);
CREATE INDEX idx_qdet_location ON q.detections(location_id, detected_at);
CREATE INDEX idx_qdet_cashier ON q.detections(cashier_employee_id, detected_at) WHERE cashier_employee_id IS NOT NULL;
CREATE INDEX idx_qdet_case ON q.detections(case_id) WHERE case_id IS NOT NULL;
CREATE INDEX idx_qdet_unresolved ON q.detections(tenant_id, status) WHERE status NOT IN ('dismissed', 'duplicate');
```

**Lifecycle**: producers `mcp.q.detection.emit-from-rule`; consumers `mcp.q.case.escalate-from-detection`, `mcp.alert.notify-from-detection`, `mcp.metrics.q-detection-volume`.

**Provenance**: Canary current `app.alerts` (split into detections + cases for clarity — current spec conflates them).

### q.cases

**Module**: Q.

```sql
CREATE TABLE q.cases (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  case_number         text NOT NULL,
  case_type           text NOT NULL DEFAULT 'investigation',      -- investigation | incident | dispute | compliance_review
  title               text NOT NULL,
  description         text,
  severity            text NOT NULL,
  status              text NOT NULL DEFAULT 'open',                -- open | active | pending_action | resolved | closed | reopened
  primary_subject_id  uuid REFERENCES q.subjects(id),
  primary_location_id uuid REFERENCES l.locations(id),
  assigned_to         uuid REFERENCES app.users(id),
  opened_at           timestamptz NOT NULL DEFAULT now(),
  resolved_at         timestamptz,
  resolution_type     text,                                       -- substantiated | unsubstantiated | recovered | restitution | termination | no_action
  loss_amount_estimated numeric(14,4),
  loss_amount_recovered numeric(14,4),
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, case_number)
);

CREATE INDEX idx_qcases_tenant ON q.cases(tenant_id);
CREATE INDEX idx_qcases_subject ON q.cases(primary_subject_id);
CREATE INDEX idx_qcases_location ON q.cases(primary_location_id);
CREATE INDEX idx_qcases_assigned ON q.cases(assigned_to);
CREATE INDEX idx_qcases_active ON q.cases(tenant_id, status) WHERE status NOT IN ('resolved', 'closed');
```

**Lifecycle**: producers `mcp.q.case.{create,assign,update-status,resolve,reopen}`; consumers `mcp.audit.case-history`, `mcp.metrics.case-resolution-cycle-time`.

**Provenance**: Canary current `app.fox_cases` + `app.hawk_cases` (consolidated — current spec has 7 + 8 = 15 cols across 2 case tables; canonical is 1 case table with `case_type` discriminator). Recovery DDL `Case_Custom`, `CaseCentre`, `Video_CaseManagement` were precursors to this design.

### q.case_evidence

**Module**: Q. (Evidence chain — append-only with hash for L2 blockchain anchoring.)

```sql
CREATE TABLE q.case_evidence (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  case_id                 uuid NOT NULL REFERENCES q.cases(id) ON DELETE RESTRICT,
  evidence_type           text NOT NULL,                              -- transaction_snapshot | video_clip | photo | document | witness_statement | system_log | scan_replay
  source_entity_type      text,                                       -- e.g., transaction
  source_entity_id        uuid,
  payload                 jsonb NOT NULL DEFAULT '{}',                -- the evidence content (or pointer to object storage URL)
  payload_hash            text NOT NULL,                              -- SHA-256 of canonical-JSON payload
  prev_evidence_hash      text,                                       -- chain reference
  blockchain_anchor_id    uuid REFERENCES ledger.blockchain_anchors(id),  -- when batched into L2 anchor
  collected_by            uuid REFERENCES app.users(id),
  collected_at            timestamptz NOT NULL DEFAULT now(),
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- append-only — never updated, never deleted (audit / chain-of-custody)
);

CREATE INDEX idx_qev_tenant ON q.case_evidence(tenant_id);
CREATE INDEX idx_qev_case ON q.case_evidence(case_id, collected_at);
CREATE INDEX idx_qev_hash ON q.case_evidence(payload_hash);
CREATE INDEX idx_qev_unanchored ON q.case_evidence(tenant_id) WHERE blockchain_anchor_id IS NULL;
```

**Lifecycle**: producers `mcp.q.evidence.collect-from-{transaction,video,document,system}`; consumers `mcp.ledger.blockchain-anchor.batch-evidence`, `mcp.q.evidence.access-log`, `mcp.audit.chain-of-custody.verify`.

**Provenance**: Canary current `app.fox_evidence` + `app.fox_evidence_access_log` (preserved with explicit blockchain anchor FK). Cryptographic accountability rail per platform thesis (memory `project_platform_thesis_locked`).

### q.case_actions

**Module**: Q.

```sql
CREATE TABLE q.case_actions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  case_id         uuid NOT NULL REFERENCES q.cases(id) ON DELETE CASCADE,
  action_type     text NOT NULL,                                  -- note | status_change | assignment_change | evidence_collected | external_notification | resolution
  performed_by    uuid REFERENCES app.users(id),
  performed_at    timestamptz NOT NULL DEFAULT now(),
  details         jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_qact_tenant ON q.case_actions(tenant_id);
CREATE INDEX idx_qact_case ON q.case_actions(case_id, performed_at);
CREATE INDEX idx_qact_type ON q.case_actions(action_type);
```

**Lifecycle**: producers `mcp.q.case-action.log` (every state change writes one); consumers `mcp.audit.case-action-history`.

**Provenance**: Canary current `app.fox_case_actions` + `app.fox_case_timeline` (consolidated to single action log per §6 cardinality rule).

### q.subjects

**Module**: Q. (Party-like — people / entities involved in cases. Distinct from c.customers / e.employees because subjects can be unknown / external / suspected without being formal master records.)

```sql
CREATE TABLE q.subjects (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  subject_code        text NOT NULL,                              -- merchant or system-assigned
  subject_type        text NOT NULL,                              -- known_employee | known_customer | known_vendor | suspected_individual | unknown_person | external_party
  display_name        text NOT NULL,                              -- may be "Suspect #1" for unknowns
  related_employee_id uuid REFERENCES e.employees(id),            -- if subject is a known employee
  related_customer_id uuid REFERENCES c.customers(id),            -- if subject is a known customer
  related_vendor_id   uuid REFERENCES m.vendors(id),              -- if subject is a vendor (RTV fraud, kickbacks)
  description         text,
  identifiers         jsonb DEFAULT '{}',                          -- {phone, email, license_plate, badge_id, photo_urls — all PII tier 2-3, encrypted}
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',             -- active | resolved | dismissed
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, subject_code)
);

CREATE INDEX idx_qsub_tenant ON q.subjects(tenant_id);
CREATE INDEX idx_qsub_employee ON q.subjects(related_employee_id) WHERE related_employee_id IS NOT NULL;
CREATE INDEX idx_qsub_customer ON q.subjects(related_customer_id) WHERE related_customer_id IS NOT NULL;
CREATE INDEX idx_qsub_type ON q.subjects(subject_type);
```

**Lifecycle**: producers `mcp.q.subject.{create,update,resolve,merge}`; consumers `mcp.q.case.assign-primary-subject`, `mcp.audit.subject-investigation-history`.

**Provenance**: Canary current `app.fox_subjects` + `app.hawk_subjects` (consolidated). Critical privacy/PII handling — `identifiers` JSONB encrypted at rest.

---

## ledger schema — Cost-to-Serve + Accountability Rails (5 entities)

### ledger.stock_ledger_entries

**Module**: F + D cross-cut.

```sql
CREATE TABLE ledger.stock_ledger_entries (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  inventory_movement_id   uuid NOT NULL REFERENCES i.inventory_movements(id),
  posted_at               timestamptz NOT NULL DEFAULT now(),
  item_id                 uuid NOT NULL REFERENCES m.items(id),
  location_id             uuid NOT NULL REFERENCES l.locations(id),
  quantity_delta          numeric(14,4) NOT NULL,
  cost_per_unit           numeric(14,4) NOT NULL,                   -- cost at posting time
  cost_amount             numeric(14,4) GENERATED ALWAYS AS (quantity_delta * cost_per_unit) STORED,
  cost_method             text NOT NULL DEFAULT 'weighted_average', -- weighted_average | fifo | lifo | specific
  gl_account_id           uuid REFERENCES f.gl_accounts(id),
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_sl_tenant ON ledger.stock_ledger_entries(tenant_id);
CREATE INDEX idx_sl_movement ON ledger.stock_ledger_entries(inventory_movement_id);
CREATE INDEX idx_sl_item_location ON ledger.stock_ledger_entries(item_id, location_id, posted_at);
CREATE INDEX idx_sl_gl ON ledger.stock_ledger_entries(gl_account_id);
```

**Lifecycle**: producer `mcp.ledger.stock-ledger.post-from-movement` (atomic with inventory movement); consumer `mcp.financial.cogs.aggregate`, `mcp.metrics.margin.compute`.

**Provenance**: Canary current `ledger.stock_ledger_entries` — preserved with explicit FK to `i.inventory_movements`. Provides financial-valuation counterpart to physical movement log.

### ledger.ildwac_positions

**Module**: F. (ILDWAC = the satoshi cost-to-serve rollup per memory `project_satoshi_cost_model`.)

```sql
CREATE TABLE ledger.ildwac_positions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id               uuid NOT NULL REFERENCES app.tenants(id),
  position_period         tstzrange NOT NULL,                       -- the window this position covers
  cadence_step            text NOT NULL,                            -- minute | hour | day | week | month (the cadence-ladder tier per memory)
  l_storage_satoshis      bigint NOT NULL DEFAULT 0,                -- L = storage cost in satoshis
  w_workload_satoshis     bigint NOT NULL DEFAULT 0,                -- W = workload cost
  c_capture_satoshis      bigint NOT NULL DEFAULT 0,                -- C = capture-fidelity cost
  total_satoshis          bigint GENERATED ALWAYS AS (l_storage_satoshis + w_workload_satoshis + c_capture_satoshis) STORED,
  bytes_under_management  bigint,                                   -- the bytes input from CRDM-sizing-template-derived calc (GRO-732)
  workload_units          bigint,                                   -- queries / writes / events processed
  capture_tier            text,                                     -- low | medium | high | full (per CRDM TLOG detail level)
  invoiced_at             timestamptz,                               -- when L402-OTB charged this position
  payment_proof           text,                                     -- L402 receipt / on-chain reference
  attributes              jsonb NOT NULL DEFAULT '{}',
  created_at              timestamptz NOT NULL DEFAULT now()
  -- append-only after invoiced
);

CREATE INDEX idx_ildwac_tenant ON ledger.ildwac_positions(tenant_id);
CREATE INDEX idx_ildwac_period ON ledger.ildwac_positions USING gist(position_period);
CREATE INDEX idx_ildwac_cadence ON ledger.ildwac_positions(cadence_step);
CREATE INDEX idx_ildwac_unbilled ON ledger.ildwac_positions(tenant_id) WHERE invoiced_at IS NULL;
```

**Lifecycle**: producers `mcp.ledger.ildwac.compute-position` (per cadence step), `mcp.ledger.ildwac.invoice-position` (when ready to bill); consumers `mcp.l402.charge-tenant-position`, `mcp.metrics.cost-to-serve-by-tenant`.

**Provenance**: Per memory `project_satoshi_cost_model` and GRO-732 (sizing template input layer). Net-new — no source modeled this. ARTS doesn't cover platform cost-to-serve.

### ledger.rib_batches

**Module**: F. (RIB = Receipt-In-Batch — accumulates receipts for cost averaging.)

```sql
CREATE TABLE ledger.rib_batches (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  item_id             uuid NOT NULL REFERENCES m.items(id),
  location_id         uuid REFERENCES l.locations(id),
  batch_period        tstzrange NOT NULL,
  total_quantity      numeric(14,4) NOT NULL DEFAULT 0,
  total_cost          numeric(14,4) NOT NULL DEFAULT 0,
  weighted_avg_cost   numeric(14,4) GENERATED ALWAYS AS (CASE WHEN total_quantity > 0 THEN total_cost / total_quantity ELSE 0 END) STORED,
  receipt_count       int NOT NULL DEFAULT 0,
  closed_at           timestamptz,                                 -- when batch closed and posted to position
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_rib_tenant ON ledger.rib_batches(tenant_id);
CREATE INDEX idx_rib_item_location ON ledger.rib_batches(item_id, location_id);
CREATE INDEX idx_rib_period ON ledger.rib_batches USING gist(batch_period);
CREATE INDEX idx_rib_open ON ledger.rib_batches(tenant_id) WHERE closed_at IS NULL;
```

**Lifecycle**: producers `mcp.ledger.rib-batch.{create,append-receipt,close}`; consumers `mcp.ledger.stock-ledger.post-from-rib-close`, `mcp.metrics.cost-trend`.

**Provenance**: Canary current `ledger.rib_batches` — preserved. Per memory `project_ilwac_bitcoin_standard`.

### ledger.l402_otb_budgets

**Module**: F. (L402 = Lightning Network 402 protocol; OTB = Open-To-Buy budget gate.)

```sql
CREATE TABLE ledger.l402_otb_budgets (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  budget_period       tstzrange NOT NULL,
  scope_type          text NOT NULL,                              -- tenant_total | category | location | service
  scope_id            uuid,                                       -- references the scope entity (NULL for tenant_total)
  budget_satoshis     bigint NOT NULL,
  consumed_satoshis   bigint NOT NULL DEFAULT 0,
  remaining_satoshis  bigint GENERATED ALWAYS AS (budget_satoshis - consumed_satoshis) STORED,
  hard_limit          boolean NOT NULL DEFAULT false,             -- if true, blocks operations when exhausted
  alert_threshold_pct numeric(5,4) DEFAULT 0.80,                  -- alert when consumed >= threshold * budget
  status              text NOT NULL DEFAULT 'active',             -- active | exhausted | paused | closed
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_otb_tenant ON ledger.l402_otb_budgets(tenant_id);
CREATE INDEX idx_otb_period ON ledger.l402_otb_budgets USING gist(budget_period);
CREATE INDEX idx_otb_active ON ledger.l402_otb_budgets(tenant_id, scope_type) WHERE status = 'active';
```

**Lifecycle**: producers `mcp.ledger.otb.{set-budget,consume,close-period}`; consumers `mcp.l402.gate.check-before-operation`, `mcp.alert.budget-threshold-breach`.

**Provenance**: Per platform thesis memory `project_platform_thesis_locked` — L402-gated OTB is one of three accountability rails (operational, financial, evidentiary). Net-new.

### ledger.blockchain_anchors

**Module**: F + Q cross-cut. (The L2 blockchain hash anchoring records — third accountability rail.)

```sql
CREATE TABLE ledger.blockchain_anchors (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid REFERENCES app.tenants(id),            -- nullable for cross-tenant batch anchors
  anchor_type         text NOT NULL,                              -- evidence_batch | ildwac_position | gl_period | merkle_root
  payload_hash        text NOT NULL,                              -- the hash being anchored
  merkle_root         text,                                        -- if Merkle-batched, the root of the tree
  anchored_at         timestamptz NOT NULL DEFAULT now(),
  l2_chain            text NOT NULL DEFAULT 'lightning',          -- lightning | rgb | liquid | rsk
  l2_transaction_id   text,                                        -- the on-chain reference
  l2_block_height     bigint,
  l2_proof            jsonb,                                       -- proof of inclusion
  related_entity_count int,                                        -- how many entities this anchor batched
  status              text NOT NULL DEFAULT 'pending',             -- pending | confirmed | failed
  attributes          jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_anchor_tenant ON ledger.blockchain_anchors(tenant_id);
CREATE INDEX idx_anchor_type ON ledger.blockchain_anchors(anchor_type);
CREATE INDEX idx_anchor_payload_hash ON ledger.blockchain_anchors(payload_hash);
CREATE INDEX idx_anchor_pending ON ledger.blockchain_anchors(tenant_id) WHERE status = 'pending';
```

**Lifecycle**: producers `mcp.ledger.anchor.{batch-evidence,batch-position,submit-to-l2,confirm}`; consumers `mcp.q.evidence.verify-anchor`, `mcp.audit.cryptographic-integrity.verify`.

**Provenance**: Per platform thesis (third rail: evidentiary). Net-new. Enables third-party verification of evidence and cost rollups without trusting Canary as a custodian.

---

## app schema — Cross-Cutting Platform (4 entities)

These are essentials. Most current Canary `app.*` entities (organizations, merchants, users, roles, settings, feature_flags, source_systems, merchant_sources, external_identities, audit_log, etc.) are preserved as-is from the current spec. We name the canonical-essential subset here.

### app.tenants

**Module**: cross-cutting.

```sql
CREATE TABLE app.tenants (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL,                                  -- references app.organizations (current Canary spec)
  tenant_code     text NOT NULL,
  name            text NOT NULL,
  status          text NOT NULL DEFAULT 'active',                 -- active | onboarding | suspended | terminated | archived
  schema_name     text NOT NULL,                                  -- physical schema name (per schema-per-tenant strategy)
  region          text NOT NULL DEFAULT 'us-west',                -- data residency region
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id, tenant_code),
  UNIQUE (schema_name)
);
```

**Lifecycle**: producers `mcp.tenant.{onboard,suspend,terminate,archive}`; consumers `every other entity in the canonical references this`.

**Provenance**: Implicit in current Canary spec via `app.merchants` + `app.merchant_settings`. Promoted to first-class `app.tenants` for clarity (every other entity has `tenant_id REFERENCES app.tenants(id)`).

### app.users

**Preserve as-is from current Canary `app.users`.** Auth identity per `Brain/wiki/canary-go-portal.md`.

### app.audit_log

**Module**: cross-cutting.

```sql
CREATE TABLE app.audit_log (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid REFERENCES app.tenants(id),
  user_id         uuid REFERENCES app.users(id),
  action_type     text NOT NULL,                                  -- create | update | delete | view | export | login | logout | impersonate
  entity_type     text NOT NULL,                                  -- e.g., 'm.items', 't.transactions'
  entity_id       uuid,
  changes         jsonb,                                          -- before/after diff
  context         jsonb DEFAULT '{}',                             -- {ip_address, user_agent, request_id}
  performed_at    timestamptz NOT NULL DEFAULT now()
  -- append-only
);

CREATE INDEX idx_audit_tenant ON app.audit_log(tenant_id, performed_at);
CREATE INDEX idx_audit_user ON app.audit_log(user_id, performed_at);
CREATE INDEX idx_audit_entity ON app.audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_action ON app.audit_log(action_type, performed_at);
```

**Lifecycle**: producer `mcp.audit.log` (called from every state-mutating MCP service via middleware); consumer `mcp.audit.report.{by-user,by-entity,by-time}`.

**Provenance**: Canary current `app.audit_log` — preserved as core platform mechanic.

### app.external_identities

**Preserve as-is from current Canary `app.external_identities`.** Maps canonical entity IDs to source-system IDs (POS-native cashier IDs, Square Customer IDs, Counterpoint AR_CUST IDs, etc.). Critical for federation — referenced by every entity that has `external_ids jsonb` for individual mappings, plus this table for join queries.

---

## memory schema — Agent Memory (preserved)

`memory.alx_memories` and `memory.alx_sessions` — preserved as-is from current Canary spec. These are the agent persistence layer (semantic search via pgvector) referenced by `mcp.vault.*` and `mcp.memory.*` services.

---

## Domain summary

**15 entities (6 q + 5 ledger + 4 app explicitly designed; ~10 more app/memory entities preserved as-is from current Canary spec)**:

**q schema (LP)**:
- `q.detection_rules` — JSONB rule definitions, multi-frequency
- `q.detections` — append-only signal log
- `q.cases` — unified case_type discriminator (folds Fox + Hawk current 15 cols → 1 table)
- `q.case_evidence` — append-only with hash chain + blockchain anchor FK
- `q.case_actions` — append-only state log
- `q.subjects` — Party-like with cross-FK to employee/customer/vendor

**ledger schema (cost-to-serve + accountability)**:
- `ledger.stock_ledger_entries` — financial valuation per inventory movement
- `ledger.ildwac_positions` — satoshi cost-to-serve per cadence step (GRO-732 input layer)
- `ledger.rib_batches` — receipt-in-batch cost averaging
- `ledger.l402_otb_budgets` — L402-gated open-to-buy
- `ledger.blockchain_anchors` — L2 hash anchoring (third accountability rail)

**app schema (cross-cutting)**:
- `app.tenants` — promoted from implicit
- `app.users` — preserved
- `app.audit_log` — preserved
- `app.external_identities` — preserved

**memory schema** — `memory.alx_memories` + `memory.alx_sessions` preserved

**Folded from sources**:
- Canary current `app.fox_*` (7 tables) + `app.hawk_*` (8 tables) → 6 q-schema entities (50% reduction via unification)
- Canary current `app.detection_rules` + `app.alerts` + `app.alert_history` → `q.detection_rules` + `q.detections` (cleaner separation: rules vs detections vs cases)
- ILDWAC + L402-OTB + blockchain-anchor — net-new per platform thesis

**MCP service junctions defined for this domain (~30)**:
- Q: rule.{create,update,activate,pause}, detection.{evaluate-on-event, scheduled-batch, emit-from-rule}, case.{create,assign,update-status,resolve,reopen,escalate-from-detection}, evidence.{collect-from-*, access-log}, case-action.log, subject.{create,update,resolve,merge}
- Ledger: stock-ledger.post-from-movement, ildwac.{compute-position, invoice-position}, rib-batch.{create, append-receipt, close}, otb.{set-budget, consume, close-period}, anchor.{batch-evidence, batch-position, submit-to-l2, confirm}
- App: tenant.{onboard,suspend,terminate,archive}, audit.log, audit.report.*

## Status

- **Chunk 8 complete.** 15 Canary platform mechanics entities. Q (Loss Prevention) consolidated. Ledger (cost-to-serve + 3 accountability rails) implemented per platform thesis.
- **Resume**: Chunk 9 — Module ownership tagging across the 13-module spine. Cross-table that maps every canonical entity to its primary module + cross-module touches.

---

# §11 Module Ownership Matrix

**Purpose**: Map every canonical entity to its primary module + cross-module consumers. The 13-module spine (post-rename: M, O, C, E, T, F, A, D, N, P, S, L, Q) is the architectural decomposition; this matrix shows where each entity lives and who touches it.

## The 13-module spine

| Letter | Module | Domain |
|---|---|---|
| **M** | Merchandising | Item master, vendor master, categorization, packs |
| **O** | Orders | Purchase orders, sales orders, fulfillment, allocation, ASN/BOL |
| **C** | Customer | Customer master, addresses, loyalty |
| **E** | Execution / Workflow | Task management, work assignment **(greenfield — no entities in this canonical)** |
| **T** | Transaction Pipeline | POSLog transactions, line items, tenders, discounts, sales audit |
| **F** | Finance | GL accounts, supplier invoices, payments, tender types, ledger |
| **A** | Asset | Locations (stores, warehouses, DCs), location hierarchy |
| **D** | Distribution | Inventory positions, movements, documents, lots |
| **N** | Device | POS terminals, kiosks, mobile **(currently inline in t.transactions.pos_terminal_id; full schema TBD)** |
| **P** | Pricing | Price books, promotions, promotion rules, taxes |
| **S** | Space | Planograms, planogram positions, location zones, location assortment |
| **L** | Labor / People | Employees, role assignments, location assignments |
| **Q** | Loss Prevention | Detection rules, detections, cases, evidence, subjects |

## Canonical entity → module ownership matrix

**Convention**: Primary owner in **bold**. Secondary modules consume but don't own.

### Schema `m` (Merchandising)

| Entity | Primary | Consumed by |
|---|---|---|
| `m.items` | **M** | T (line items), D (inventory), P (pricing), O (PO/SO lines), Q (LP scans) |
| `m.product_categories` | **M** | T (snapshot at sale), F (tax mapping), P (promotion scope), S (planogram scope) |
| `m.vendors` | **M** | O (PO vendor), F (invoice vendor), D (GRN vendor), Q (RTV-fraud subject ref) |
| `m.item_vendors` | **M** | O (PO cost lookup), D (receipt cost), F (3-way match) |
| `m.item_barcodes` | **M** | T (scan resolve), D (cycle count) |
| `m.item_packs` | **M** | D (break-down on receipt), T (sell-as-unit) |

### Schema `l` (Location / Asset)

| Entity | Primary | Consumed by |
|---|---|---|
| `l.locations` | **A** | All other domains (every entity is location-scoped) |
| `l.location_hierarchy` + `_assignments` | **A** | F (tax zones), P (regional promotions), Metrics (rollups) |
| `l.location_zones` | **A** | S (planogram positions), Q (shrink-by-zone), D (cycle-count scope) |
| `l.location_assortment` | **A** + **M** + **S** | T (scan validate), D (replenishment scope), O (replenishment), Q (lost sale signal) |

### Schema `s` (Space)

| Entity | Primary | Consumed by |
|---|---|---|
| `s.planograms` + `_assignments` | **S** | D (replenishment capacity), N (shelf-edge labels) |
| `s.planogram_positions` | **S** | D (capacity-driven replenishment qty), N (shelf labels) |

### Schema `c` (Customer)

| Entity | Primary | Consumed by |
|---|---|---|
| `c.customers` | **C** | T (transaction customer), O (sales order customer), F (invoice bill-to), Q (case subject ref), Marketing |
| `c.customer_addresses` | **C** | O (shipping resolve), F (bill-to), Marketing (geo-segment) |
| `c.loyalty_memberships` | **C** | T (transaction loyalty lookup), Marketing (tier campaigns) |

### Schema `e` (Employee)

| Entity | Primary | Consumed by |
|---|---|---|
| `e.employees` | **L** | T (cashier on transaction), Q (subject), F (manager-approval cross-cuts), Audit |
| `e.employee_role_assignments` | **L** | App (RBAC), T (manager-override authorize) |
| `e.employee_location_assignments` | **L** | T (employee-at-location validate), Scheduling |

### Schema `i` (Inventory / Distribution)

| Entity | Primary | Consumed by |
|---|---|---|
| `i.inventory_positions` | **D** | T (scan availability), O (allocation), F (valuation cross-cut), Replenishment, Forecast, Q (shrink) |
| `i.inventory_movements` | **D** | F (stock_ledger_entries — financial valuation), Q (shrink detection on adjustments), Metrics |
| `i.inventory_documents` + `_lines` | **D** | F (3-way match), O (receipt-vs-PO reconciliation), Audit |
| `i.inventory_lots` | **D** | T (lot scan), O (FEFO allocation), Compliance (recall) |

### Schema `o` (Orders)

| Entity | Primary | Consumed by |
|---|---|---|
| `o.purchase_orders` + `_lines` | **O** | F (AP encumbrance, 3-way match), D (receipt expectation), M (item-vendor cost) |
| `o.sales_orders` + `_lines` | **O** | T (POS order completion), F (revenue recognition), C (customer history) |
| `o.fulfillments` + `_lines` | **O** | D (movement on pick), L (employee assignment), Customer (notification) |
| `o.allocations` | **O** | D (ATP computation: on_hand - SUM allocations), T (scan-validate vs allocation) |
| `o.shipping_documents` | **O** | D (expected arrival update), Customer (tracking) |

### Schema `p` (Pricing)

| Entity | Primary | Consumed by |
|---|---|---|
| `p.item_prices` | **P** | T (price resolve), Storefront, Metrics |
| `p.promotions` + `_rules` | **P** | T (promotion evaluate), Metrics (effectiveness) |
| `p.tax_classes` + `p.tax_rates` | **P** + **F** | T (tax compute), F (tax liability aggregate) |

### Schema `f` (Finance)

| Entity | Primary | Consumed by |
|---|---|---|
| `f.tender_types` | **F** | T (every payment), Q (tender pattern detection) |
| `f.gl_accounts` | **F** | F (posting destination), Accounting integration |
| `f.supplier_invoices` + `_lines` | **F** | O (3-way match), F (payment scheduling), Audit |
| `f.payments` + `_invoice_applications` | **F** | F (cash flow), Audit |

### Schema `t` (Transaction Pipeline)

| Entity | Primary | Consumed by |
|---|---|---|
| `t.transactions` | **T** | D (movements), F (tender aggregate), C (loyalty earn), Q (rule eval), Metrics |
| `t.transaction_line_items` | **T** | D (movement-from-line), Q (line pattern detection), Metrics |
| `t.transaction_tenders` | **T** | F (drawer reconciliation), Q (tender pattern) |
| `t.transaction_discounts` | **T** | P (promotion effectiveness), Q (discount abuse), C (loyalty redeem) |
| `t.cashier_actions` | **T** | Q (action pattern), Audit |
| `t.cash_drawer_events` | **T** | F (cash recon), Q (drawer variance), Metrics |
| `t.shift_events` | **T** | L (productivity), Scheduling, Q (shift anomaly) |
| `t.loyalty_events` | **T** | C (balance recompute backstop), Metrics |
| `t.gift_card_events` | **T** | F (deferred revenue), Q (gift-card-fraud) |

### Schema `q` (Loss Prevention)

| Entity | Primary | Consumed by |
|---|---|---|
| `q.detection_rules` | **Q** | (configuration only) |
| `q.detections` | **Q** | Alert (notify), Q (case escalation), Metrics |
| `q.cases` | **Q** | Audit (case history), Metrics (resolution time) |
| `q.case_evidence` | **Q** | Ledger (blockchain anchor), Audit (chain of custody) |
| `q.case_actions` | **Q** | Audit |
| `q.subjects` | **Q** | Q (case primary subject), Audit (subject investigation history) |

### Schema `ledger` (Cost-to-Serve + Accountability)

| Entity | Primary | Consumed by |
|---|---|---|
| `ledger.stock_ledger_entries` | **F** | F (COGS), Metrics (margin) |
| `ledger.ildwac_positions` | **F** | F (L402 charge), Metrics (cost-to-serve) |
| `ledger.rib_batches` | **F** | F (cost averaging → stock_ledger), Metrics (cost trend) |
| `ledger.l402_otb_budgets` | **F** | All operations (gate-check before consuming), Alert (threshold breach) |
| `ledger.blockchain_anchors` | **F** + **Q** | Q (evidence anchor verify), Audit (cryptographic integrity) |

### Schema `app` (Cross-Cutting Platform)

| Entity | Primary | Consumed by |
|---|---|---|
| `app.tenants` | platform | Every other entity |
| `app.users` | platform | Every state-changing operation (audit attribution), RBAC |
| `app.audit_log` | platform | Compliance, support, Q (forensic timeline) |
| `app.external_identities` | platform | Every entity with external system origin (POS, processor, accounting) |

### Schema `memory` (Agent Memory)

| Entity | Primary | Consumed by |
|---|---|---|
| `memory.alx_memories` | platform | Agent operations, semantic recall |
| `memory.alx_sessions` | platform | Agent context loading |

## Module coverage summary

| Module | Owns (entity count) | Status |
|---|---|---|
| **M** Merchandising | 6 | ✅ ARTS-aligned, GSLM-folded |
| **A** Asset | 4 | ✅ ARTS Location V2 anchored |
| **S** Space | 2 | ✅ ARTS Planogram V2 anchored |
| **C** Customer | 3 | ✅ ARTS Party-Customer aligned, sparse-by-default |
| **L** Labor | 3 | ✅ ARTS Party-Employee, no pay rate stored |
| **D** Distribution | 5 | ✅ ARTS Inventory V2, append-only movement log |
| **O** Orders | 8 | ✅ Greenfield closed via TOM J-Prefix; 3-way match instrumented |
| **P** Pricing | 5 (3 + 2 shared with F) | ✅ Multi-model tax, JSONB rules |
| **F** Finance | 9 (4 f + 5 ledger) | ✅ AP, GL, payment, ILDWAC, L402-OTB, blockchain anchor |
| **T** Transaction | 9 | ✅ ARTS POSLog, append-only events, generated computed cols |
| **Q** LP | 6 | ✅ Fox + Hawk consolidated, evidence chain anchored |
| **E** Execution / Workflow | 0 | ⚠️ **Greenfield not closed in this canonical** — task management TBD |
| **N** Device | 0 (inline) | ⚠️ Currently inline as `t.transactions.pos_terminal_id` text; full Device schema TBD |

**13 modules · 11 fully covered · 2 deferred** (E and N — both legitimately greenfield with no source material to reference; require fresh design later).

## Cross-cutting (not module-owned)

| Schema | Entities | Role |
|---|---|---|
| `app` | 4 (tenants, users, audit_log, external_identities) | Platform fabric — referenced by every module |
| `memory` | 2 (alx_memories, alx_sessions) | Agent persistence |

## Total canonical entity count

**65 canonical entities across 11 schemas:**

| Schema | Entities |
|---|---|
| `m` Merchandising | 6 |
| `l` Location | 4 (locations + hierarchy + assignments + zones + assortment) |
| `s` Space | 2 (planograms + assignments + positions) |
| `c` Customer | 3 |
| `e` Employee | 3 |
| `i` Inventory | 5 |
| `o` Orders | 8 |
| `p` Pricing | 5 |
| `f` Finance | 5 |
| `t` Transaction | 9 |
| `q` Loss Prevention | 6 |
| `ledger` | 5 |
| `app` | 4 (essentials; ~10 more preserved as-is from current Canary spec) |
| `memory` | 2 (preserved) |
| **TOTAL designed** | **65** (+ ~12 preserved from current Canary spec = ~77 in operational canonical) |

Within the 60-80 entity target per Chunk 1.6 design principles. Folded down from:
- GSLM ~102 entities (across 9 domains)
- CRDM 25 POS entities
- TOM 79 fingerprints (operational lifecycle bindings, not entities)
- Canary current ~116 entities (many platform-only and superseded)

Compression ratio: roughly **~250 source entities → ~77 canonical**, achieved through:
- ARTS-anchored decomposition (industry standard, no over-decomposition)
- JSONB pragmatism for variants/extensions
- Discriminator columns for type variants (transaction_type, document_type, movement_type, case_type, etc.)
- Recursive tables with ltree for hierarchies
- COALESCE-PK pattern for nullable-FK uniqueness
- EXCLUDE constraints for temporal and exclusivity rules
- Generated columns for derived values

## Status

- **Chunk 9 complete.** Module ownership matrix locked. 65 canonical entities mapped to 11 of 13 modules (E and N legitimately deferred).
- **Resume**: Chunk 9b — MCP Service Junction Inventory. Consolidate the ~150+ MCP service junctions sprinkled across chunks 2-8 into a single SLA-spec inventory.
