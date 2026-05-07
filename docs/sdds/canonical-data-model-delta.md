# Canonical Data Model — Delta from Previous Spec

**Status**: 2026-05-01 — accompanies `canonical-data-model.md` v1
**Branch**: `gd-canonical-data-model`

What changed from `data-model.md` v0 to `canonical-data-model.md` v1, why, and what was deliberately NOT changed.

## What changed

### 1. Anchoring: ad-hoc → ARTS

**Before**: 116 entities organized by domain conventions inherited from Canary's Python prototype lineage.
**After**: 65 entities anchored on ARTS (Association for Retail Technology Standards) — the industry's canonical retail data model. Naming, decomposition, and FK relationships follow ARTS conventions translated to Postgres-friendly snake_case.

**Why**: Industry alignment. Any retail integration partner (NCR, Oracle Retail, Square, Shopify, Lightspeed, Counterpoint, RapidPOS) speaks ARTS-derived models. We avoid translation layers at every boundary.

### 2. Lifecycle: structural-only → structural + operational

**Before**: Schema captured entity structure (tables, FKs, columns).
**After**: Each canonical entity carries TWO layers:
- **Schema** (ARTS-aligned DDL)
- **Operational lifecycle** — the MCP service junctions (producers, consumers) that touch it, with cadence and SLA

**Why**: Per founder direction (2026-05-01), each MCP service is born self-aware of its SLA at its L3 bus junction. The operational lifecycle is sourced from TOM (Tesco Operating Model) interface specs — 79 fingerprints extracted in Chunk 1.5 — which document field-level data exchange between named retail systems circa 2007. ARTS gives us "what entities exist"; TOM gives us "when each entity is produced/updated/consumed."

### 3. Decomposition: enterprise-2010 → SMB-2030

**Before** (and in source materials we drew from): enterprise-scale decomposition optimized for 4,000-store estates with multi-banner conglomerates and 6-level merchandise hierarchies.

**After**: SMB-2030 cardinality-aware folding:
- 6-level merchandise hierarchy → recursive `m.product_categories` (1 table, ltree index)
- 4-table style variants → `attributes jsonb` on `m.items`
- 3-table pack mechanics → 1 table `m.item_packs`
- 5-entity Promotion chain (GSLM) → 2 tables `p.promotions` + `p.promotion_rules` (JSONB triggers/benefits)
- 5 CRDM movement entities → 1 unified `i.inventory_movements` with `movement_type` discriminator
- 7 Fox + 8 Hawk case tables (current Canary) → 6 q-schema entities with `case_type` discriminator
- 25 CRDM POS entities → 9 t-schema entities (aggregates correctly routed to `metrics` schema)

**Why**: SMB merchants run 1-100 stores, not 4,000. Modern tech (Postgres + JSONB + pgvector) provides primitives that didn't exist when GSLM/TOM were designed. Per founder: "we should be efficient and not waste valuable token and storage for future ways the agents will need to read the data."

### 4. UUID strategy made explicit and uniform

**Before**: Mixed (some UUID, some serial, some text).
**After**: UUID PKs everywhere via `gen_random_uuid()`. External system IDs (POS-native, vendor-assigned, GS1 GTIN) preserved as `text` columns or in `external_ids jsonb`, never as PKs.

**Why**: Multi-source federation (Square, Stripe, Counterpoint, RapidPOS) requires stable internal IDs that don't conflict at merger. Source-system IDs change; UUIDs don't.

### 5. JSONB usage rules locked

**Before**: JSONB used inconsistently — sometimes for queryables, sometimes for variants.
**After**: JSONB is for variants/extensions/semi-structured payloads. Identifiers and queryables are typed columns. Rule: start with typed column, demote to JSONB only when 3+ variants emerge.

**Why**: Predictable query performance and clear schema evolution path.

### 6. Tenant_id on every entity

**Before**: Tenant isolation lived at the schema boundary (schema-per-tenant).
**After**: `tenant_id uuid NOT NULL REFERENCES app.tenants(id)` on every entity, in addition to schema-per-tenant.

**Why**: Defense in depth. Schema-per-tenant prevents cross-tenant joins by default; tenant_id column enforces per-row isolation even when sharing a schema (cross-tenant admin queries, support impersonation, observability rollups).

### 7. Generated columns for derived values

**Before**: Computed values like `extended_price`, `variance`, `total_cost` calculated in application code.
**After**: Postgres generated columns enforce consistency at the database level. Examples:
- `t.transaction_line_items.extended_price` (quantity × unit_price - unit_discount)
- `i.inventory_document_lines.variance_quantity` (actual - expected)
- `t.cash_drawer_events.variance` (counted - expected)
- `ledger.l402_otb_budgets.remaining_satoshis` (budget - consumed)

**Why**: Eliminates application-layer drift; queries can `WHERE variance != 0` without recomputation.

### 8. EXCLUDE constraints for temporal exclusivity

**Before**: Application-layer enforcement of "single primary vendor per item," "single default address per customer," "no overlapping active prices."
**After**: Postgres `EXCLUDE` constraints with `gist` indexes enforce these invariants at the database level, including temporal cases via `tstzrange`.

Examples:
- `m.item_vendors`: `EXCLUDE (item_id WITH =) WHERE (is_primary = true)` — only one primary vendor per item
- `c.customer_addresses`: similar for default-per-type
- `p.item_prices`: temporal exclusion via `tstzrange` — no two active prices for same scope at any moment
- `e.employee_location_assignments`: single primary location per active employee

**Why**: Consistency guarantees that survive application bugs.

### 9. Append-only event logs separated from state

**Before**: Some entities mixed mutable state and event log (e.g., loyalty_memberships with embedded points history).
**After**: Clean separation:
- `t.loyalty_events` (append-only) ↔ `c.loyalty_memberships.points_balance` (denormalized, recomputable)
- `t.gift_card_events` (append-only) ↔ `app.gift_cards.balance`
- `i.inventory_movements` (append-only) ↔ `i.inventory_positions.on_hand_quantity`
- `q.case_evidence` (append-only with hash chain + blockchain anchor) ↔ `q.cases`
- `app.audit_log` (append-only) — every state-mutating operation

**Why**: Audit-grade traceability + recomputability. Denormalized state for read performance; event log for source-of-truth and replay.

### 10. Three accountability rails fully instrumented

**Before**: Operational accountability via `audit_log`; financial via `ledger.stock_ledger_entries` + `ledger.rib_batches`; evidentiary not yet wired.
**After**: All three rails per platform thesis (`project_platform_thesis_locked`):
- **Operational**: `app.audit_log` (cross-cutting) + per-entity status discriminators
- **Financial**: `ledger.stock_ledger_entries` + `ledger.ildwac_positions` (cost-to-serve in satoshis) + `ledger.rib_batches` + `ledger.l402_otb_budgets` (gated open-to-buy)
- **Evidentiary**: `q.case_evidence` (hash-chained) + `ledger.blockchain_anchors` (L2 batched anchors)

**Why**: The "no unknown loss" / "L402-gated OTB" / "L2 hash anchoring" three-rail model from platform thesis is now buildable, not aspirational.

### 11. Greenfield closures + remaining gaps explicit

**Closed**:
- O (Orders) module — designed from scratch using TOM J-Prefix as pattern reference. 8 entities (PO + SO + fulfillment + allocation + ASN/BOL).
- D (Distribution) — was partial in Canary current spec; 5 i-schema entities now cover full operational scope.

**Deferred (legitimately greenfield, no source material)**:
- E (Execution / Workflow) — task management, work assignment. To be designed when first workflow use case is clear.
- N (Device) — currently inline as `t.transactions.pos_terminal_id` text column. Full Device schema TBD when device-management requirements solidify.

## What was deliberately NOT changed

### Schema-per-tenant strategy

**Preserved as-is.** Current Canary spec uses schema-per-tenant for multi-tenant isolation; we add tenant_id columns but keep the schema boundary as primary isolation mechanism.

### Memory schema (alx_memories, alx_sessions)

**Preserved as-is** from current Canary spec. Agent persistence layer using pgvector. Not in canonical's design scope.

### Cross-cutting `app` schema entities not redesigned

Most `app.*` entities (organizations, merchants, merchant_settings, users, roles, user_roles, source_systems, merchant_sources, feature_flags, app_config, etc.) are preserved as-is from current Canary spec. We named the canonical-essential subset (`app.tenants`, `app.users`, `app.audit_log`, `app.external_identities`) but did NOT redesign the broader Canary platform layer — those are working and don't need ARTS alignment.

### Webhook / RaaS / Vault / Bull / Subscription domains

**Preserved as-is**. These are Canary platform mechanics that ride above the retail spine. Not redesigned in this canonical pass.

### Vertical extensions

**Out of scope**. CRDM had vertical-specific entities (`CRDM_ETopUp` for mobile phone top-up, `CRDM_Repair` for service operations, `Rx*` for pharmacy aggregates). These are vertical extensions, not core retail. Core canonical supports them via `attributes jsonb` and `item_type` discriminator; explicit vertical-extension schemas are a future deliverable.

## Provenance summary per source

| Source | Entities documented | What we kept | What we folded | What we dropped |
|---|---|---|---|---|
| **GSLM MDM Site (S0)** | ~102 across 9 domains | Naming patterns, hierarchical structure, scope of master data coverage | Most entities folded via JSONB / recursive / discriminator | Multi-banner BusinessDivisions; pre-decomposed 6-level hierarchy as separate tables |
| **GSLM SQL DDL (S1)** | 43 implementation subset | Type choices (uuid/text/numeric), constraint patterns | All folded into S0-anchored canonical | Schema-prefix conventions ([dbo], [GSLM]) — translated to Postgres |
| **CRDM POS 1.8 (S2)** | 25 POS operational | Transaction line item structure, tender pattern, lot tracking concept | Most operational events → unified movement/event tables | Pre-aggregated FastFact (~110 cols) → metrics schema, NOT in t |
| **CRDM POS 1.7.2 (S3)** | 27 (with Customer + Repair extras) | Customer entity (operational) — covered by transaction join, not separate | All folded | Repair vertical |
| **Canary Go data-model.md (S4)** | ~116 | Cross-cutting `app` essentials, ledger, memory schemas | Fox + Hawk consolidated; alerts split into detections/cases | Loose patterns superseded by ARTS-aligned naming |
| **Python proto (S5)** | ~95 | Semantic reference | (no DDL pulled) | (entire schema retired with v0-python-prototype tag) |
| **Recovery DDL (S6)** | ~20 | Reference data patterns (Ref_LocationHierarchy), case management precursors (CaseCentre, Video) | Concepts folded | Most files outside CRDM domain |
| **GSLM Entity Descriptions (S7)** | 25 (subset of S0) | Attribute patterns, narrative justifications | Folded into entity provenance trails | (no separate use) |
| **GSLM Domain Overviews (S8)** | n/a (prose) | Domain rationale + design context | (background reading) | (no separate use) |
| **TOM Interface Specs (S9)** | 82 specs (operational, not entities) | Operational lifecycle for every entity touched | All 79 fingerprints bound to entities as producer/consumer/cadence | Tesco-specific naming (RMS, GFO, ORMS, etc.) translated to Canary MCP service names |
| **ARTS Standards (S10)** | ~150 in full ODM | Structural anchor for everything | Implemented SMB-2030 subset | Multi-tenant Party hierarchy as physical supertype (we maintain ARTS naming without materializing supertype table) |

## Cross-references

- **Memory references** that informed design choices:
  - `project_satoshi_cost_model` → `ledger.ildwac_positions`, `ledger.l402_otb_budgets`
  - `project_platform_thesis_locked` → three accountability rails instrumentation
  - `project_multi_tier_assortment_model` → `l.location_assortment.assortment_tier`
  - `project_canary_replaces_counterpoint_long_arc` → ARTS-anchored design enables long-arc strategy
  - `project_data_hosting_compliance_phase4` → audit_log + tenant_id everywhere supports SOC 2 / GDPR / CCPA
  - `feedback_solex_illustrative` → CRDM/GSLM as illustrative reference, not authoritative source
  - `feedback_scrub_client_names` → no Coles / Walmart / Tesco names in entity definitions
  - `feedback_no_volatile_data_in_wiki` → metrics aggregations correctly routed to metrics schema, not entity tables

- **GRO ticket closures**:
  - **GRO-732** (sizing template breakdown) — absorbed into `ledger.ildwac_positions` schema (the L/W/C inputs for cost-to-serve). Sizing template now formally feeds the schema. Mark Done with comment linking to canonical-data-model.md §10.

## Status

- **Canonical SDD (`canonical-data-model.md`) and MCP Service Junctions SDD (`mcp-service-junctions.md`) are committed on branch `gd-canonical-data-model`.**
- Old `data-model.md` v0 to be marked superseded; preserve at git tag `data-model-v0`.
- Branch ready for review or merge to main.
- Next-session work to consider:
  - E (Execution/Workflow) module design
  - N (Device) module schema
  - Vertical extension schemas (Rx, food/grocery, mobile-topup if relevant)
  - Migration plan from current `app.*` heavy schema to canonical schemas
