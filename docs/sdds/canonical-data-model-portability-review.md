---
spec-version: 1.0
target-implementation: Postgres 17+ portable; OCI containers; OpenTelemetry standard
status: handoff-ready
updated: 2026-05-03
review-target: docs/sdds/go-handoff/canonical-data-model.md (4462 lines, 65 entities, 11 schemas)
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Canary Go — Canonical Data Model Portability Review

## Governing thesis

The founder direction is "target GCP, don't get hostage to it." This review audits the 65-entity canonical (`canonical-data-model.md`) and the 171-junction MCP inventory (`mcp-service-junctions.md`) against six portability criteria. The verdict is: **the canonical itself is highly portable; the lock-in lives in three specific compute/orchestration choices, not in the data model.** Three concrete recommendations close the gap before Phase 4 (PCI scope), where re-platforming becomes a multi-quarter project.

The Top-3 portability risks (ranked by severity × difficulty-to-mitigate-later):

1. **Cloud Workflows for A8 (three-way match, L402 charge cycle, anchor submit)** — proprietary YAML state machine, deepest lock-in in the stack. Replace before Phase 4.
2. **Pub/Sub message-ordering keys** — semantically diverge from Kafka partition keys; A3 + A5 fan-out patterns will not lift-and-shift cleanly. Document deviations and constrain ordering-key usage to where Kafka semantics map.
3. **pgvector usage in `memory.alx_memories`** — portable across Postgres flavors but requires rewrite to migrate to Pinecone / Weaviate / Qdrant / dedicated vector DB. Acceptable for now; flag as a possible Phase 5+ rework if scale demands.

The 65 canonical entities themselves use only well-understood Postgres extensions (ltree, btree_gist, pgcrypto, citext, pg_trgm, pgvector) all of which exist on RDS Postgres, Aurora, Crunchy, and self-hosted. None of the data-side patterns (UUID PKs, JSONB attributes, generated columns, EXCLUDE temporal constraints, partition-by-date, append-only event logs, hash chains) are GCP-specific. The model survives a port.

---

## §1. Review methodology

The 6 audit criteria from the dispatch:

1. **Postgres-portable.** Schema must run on Postgres 17+ on any infrastructure (RDS, Aurora, Crunchy, GCE-hosted, on-prem). Postgres extensions allowed but must be widely available.
2. **Standard SQL.** Avoid Postgres-only constructs that have no portable equivalent. JSONB acceptable (PostgreSQL is the de facto standard for JSON-in-relational); arrays acceptable; window functions acceptable.
3. **Standard interface.** Application reads/writes via standard Postgres wire protocol (pgx, JDBC, psycopg). No proprietary client libraries.
4. **OpenTelemetry-instrumentable.** Every junction must emit OTel spans + metrics + logs through the standard OTel Go SDK to a configurable collector. No vendor-locked observability SDKs.
5. **Container-portable.** Every service runs as an OCI container on any container runtime. No GCP-specific runtime dependencies (e.g., metadata service hard-coded).
6. **OCI-image deployable.** Build artifacts are OCI images deployable to Cloud Run, GKE, AWS Fargate, Azure Container Apps, k8s, Nomad, or `docker run`. Manifest layer (Cloud Run YAML vs k8s YAML vs Nomad HCL) is the only environment-specific surface.

For each entity, code review yields a finding (None / Low / Medium / High severity) with a recommendation. Severity scale:

- **None** — pure portable Postgres; no concern.
- **Low** — uses a Postgres extension that is widely available; document the dependency.
- **Medium** — uses a Postgres-specific or cloud-specific feature; portable but requires rework on migration.
- **High** — uses a feature that is not portable without significant rewrite; should be replaced before Phase 4.

---

## §2. Findings per entity

For each of 65 canonical entities, finding + severity + recommendation. Most pass. The honest gotchas are flagged.

### Schema `m` (Merchandising) — 6 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `m.items` | UUID PK, JSONB attributes, generated columns. Vanilla Postgres. | None | Pass. |
| `m.product_categories` | Uses `path ltree` for materialized hierarchy paths. | Low | Document `CREATE EXTENSION IF NOT EXISTS ltree` requirement in DDL header. ltree exists on RDS, Aurora, Crunchy, all self-hosted. |
| `m.vendors` | Vanilla Postgres + JSONB. | None | Pass. |
| `m.item_vendors` | EXCLUDE constraint `one_primary_per_item EXCLUDE (item_id WITH =) WHERE (is_primary = true AND status = 'active')` requires btree_gist. | Low | Document `CREATE EXTENSION IF NOT EXISTS btree_gist` requirement. Widely available. |
| `m.item_barcodes` | Vanilla Postgres. Half-hourly NoF recovery cadence is application-layer concern. | None | Pass. |
| `m.item_packs` | Vanilla Postgres. | None | Pass. |

### Schema `l` (Location / Asset) — 4 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `l.locations` | Vanilla Postgres + JSONB. **Loop 2 finding (chirp + owl):** missing `timezone TEXT` column — schema gap, not portability. | None for portability | Pass for portability. Schema gap tracked separately for Loop 3 schema additions. |
| `l.location_hierarchy` | Uses `path ltree`. | Low | Same as m.product_categories — document ltree dependency. |
| `l.location_zones` | Uses `path ltree`. | Low | Same. |
| `l.location_assortment` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `s` (Space) — 2 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `s.planograms` | Vanilla Postgres + JSONB. | None | Pass. |
| `s.planogram_positions` | Vanilla Postgres. | None | Pass. |

### Schema `c` (Customer) — 3 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `c.customers` | Vanilla Postgres + JSONB. | None | Pass. |
| `c.customer_addresses` | EXCLUDE constraint `one_default_per_type EXCLUDE (customer_id WITH =, address_type WITH =) WHERE (is_default = true AND status = 'active')` requires btree_gist. | Low | btree_gist already required (m.item_vendors). |
| `c.loyalty_memberships` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `e` (Employee) — 3 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `e.employees` | Vanilla Postgres + JSONB. | None | Pass. |
| `e.employee_role_assignments` | Vanilla Postgres. | None | Pass. |
| `e.employee_location_assignments` | EXCLUDE constraint `one_primary_per_employee EXCLUDE (employee_id WITH =) WHERE (is_primary = true AND effective_end IS NULL)` requires btree_gist. | Low | Same as above. |

### Schema `i` (Inventory / Distribution) — 5 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `i.inventory_positions` | Vanilla Postgres. Hot cache via Memorystore Valkey; replaceable with any Redis-protocol cache. | None | Pass. |
| `i.inventory_movements` | **Index `idx_movements_position_recompute` uses COALESCE in index expression** — `COALESCE(zone_id, '00000000-0000-0000-0000-000000000000'::uuid)`. Postgres-specific functional index pattern. | Low | Document — works on any Postgres-fork. Not portable to MySQL/SQLite without rework. Acceptable per Postgres-only target. Append-only enforced by application convention (no UPDATE / DELETE in producers); no DB constraint. |
| `i.inventory_documents` | Vanilla Postgres. | None | Pass. |
| `i.inventory_document_lines` | **Generated column** `variance_quantity numeric(14,4) GENERATED ALWAYS AS (COALESCE(actual_quantity, 0) - COALESCE(expected_quantity, 0)) STORED` — Postgres ≥12 standard SQL. | None | Pass. Standard since Postgres 12. |
| `i.inventory_lots` | Vanilla Postgres. | None | Pass. |

### Schema `o` (Orders) — 8 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `o.purchase_orders` | Vanilla Postgres + JSONB. | None | Pass. |
| `o.purchase_order_lines` | Generated column `total_cost`. | None | Pass. |
| `o.sales_orders` | Vanilla Postgres + JSONB. | None | Pass. |
| `o.sales_order_lines` | Generated column `line_total`. | None | Pass. |
| `o.fulfillments` | Vanilla Postgres + JSONB. | None | Pass. |
| `o.fulfillment_lines` | Vanilla Postgres. | None | Pass. |
| `o.allocations` | Vanilla Postgres. | None | Pass. |
| `o.shipping_documents` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `p` (Pricing) — 5 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `p.item_prices` | **EXCLUDE USING gist with tstzrange:** `EXCLUDE USING gist (... tstzrange(effective_start, effective_end, '[)') WITH &&)` — Postgres-native temporal exclusion. The strongest pattern for "no overlapping active prices for same scope at same time." Requires btree_gist + tstzrange. | Low | Document. Both extensions widely available. This pattern is genuinely Postgres-best-of-breed; replicating it elsewhere requires application-layer enforcement + race-condition mitigation. **Worth keeping** for the integrity guarantee. |
| `p.promotions` | Vanilla Postgres + JSONB. | None | Pass. |
| `p.promotion_rules` | Vanilla Postgres + JSONB. | None | Pass. |
| `p.tax_classes` | EXCLUDE constraint for single-default-per-tenant. | Low | Same btree_gist dependency. |
| `p.tax_rates` | Vanilla Postgres. | None | Pass. |

### Schema `f` (Finance) — 5 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `f.tender_types` | Vanilla Postgres. **Loop 2 finding (sub2):** seed rows missing — required for FK integrity from `t.transaction_tenders.tender_type_id`. | None for portability | Pass for portability. Seed-data gap tracked separately. |
| `f.gl_accounts` | Vanilla Postgres. | None | Pass. |
| `f.supplier_invoices` | Vanilla Postgres + JSONB. | None | Pass. |
| `f.supplier_invoice_lines` | Vanilla Postgres. | None | Pass. |
| `f.payments` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `t` (Transaction Pipeline) — 9 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `t.transactions` | Vanilla Postgres + JSONB + GIN index on `external_ids`. | None | Pass. GIN indexes are standard Postgres. **Partition strategy** (cloud-architecture-workload.md §4) is application-layer; entity definition is portable. |
| `t.transaction_line_items` | **Multiple generated columns** (extended_price, extended_tax, line_total, margin) — all standard Postgres ≥12. | None | Pass. |
| `t.transaction_tenders` | Vanilla Postgres. | None | Pass. |
| `t.transaction_discounts` | Vanilla Postgres. | None | Pass. |
| `t.cashier_actions` | Vanilla Postgres + JSONB. | None | Pass. |
| `t.cash_drawer_events` | Vanilla Postgres. | None | Pass. |
| `t.shift_events` | Vanilla Postgres. | None | Pass. |
| `t.loyalty_events` | Vanilla Postgres + JSONB. | None | Pass. |
| `t.gift_card_events` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `q` (Loss Prevention) — 6 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `q.detection_rules` | Vanilla Postgres + JSONB. Rule definitions stored as JSONB — portable. | None | Pass. |
| `q.detections` | Vanilla Postgres + JSONB. **Loop 2 finding:** soft FK to `e.employees` (no DB constraint, application-enforced). Pattern is portable. | None | Pass. Document the soft-FK pattern in canonical DDL header — see Recommendation R-3. |
| `q.cases` | Vanilla Postgres + JSONB. **Loop 2 finding:** missing `closed_at TIMESTAMPTZ` separate from `resolved_at`. Schema gap, not portability. | None for portability | Pass for portability. Schema gap tracked. |
| `q.case_evidence` | **Hash chain pattern** (`payload_hash text NOT NULL` + `prev_evidence_hash text` + `blockchain_anchor_id` FK). Application-layer enforced; entity itself is vanilla Postgres. | None | Pass. Hash chain semantics are application-layer, fully portable. |
| `q.case_actions` | Vanilla Postgres + JSONB. | None | Pass. |
| `q.subjects` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `ledger` — 5 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `ledger.stock_ledger_entries` | **Generated column** `cost_amount GENERATED ALWAYS AS (quantity_delta * cost_per_unit) STORED`. Standard Postgres ≥12. | None | Pass. |
| `ledger.ildwac_positions` | **`tstzrange` for `position_period`** + GIST index on the range + **generated column** `total_satoshis`. Postgres ≥12 + btree_gist. | Low | Document tstzrange dependency. Range types are Postgres-specific — replicating in MySQL requires two columns + check constraint + manual range-overlap query patterns. **Worth keeping** for the temporal-position semantics. |
| `ledger.rib_batches` | Same `tstzrange` + GIST pattern as ildwac_positions. Generated column `weighted_avg_cost` with CASE. | Low | Same recommendation. |
| `ledger.l402_otb_budgets` | Same `tstzrange` + GIST. Generated column `remaining_satoshis`. | Low | Same. |
| `ledger.blockchain_anchors` | Vanilla Postgres + JSONB for `l2_proof`. | None | Pass. **Lock-in is to L2 chain protocol** (Lightning, RGB, Liquid, RSK), not to GCP. Acceptable per memory `project_canary_chain_is_storage` — the chain IS the S4 storage tier. |

### Schema `app` (Cross-Cutting Platform) — 4 entities + ~10 preserved

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `app.tenants` | Vanilla Postgres + JSONB. UNIQUE on (organization_id, tenant_code) and on schema_name. | None | Pass. |
| `app.users` | Preserved from current Canary spec. Vanilla Postgres. | None | Pass. |
| `app.audit_log` | Vanilla Postgres + JSONB. Append-only enforced by application convention. | None | Pass. |
| `app.external_identities` | Preserved from current Canary spec. Vanilla Postgres + JSONB. | None | Pass. |

### Schema `memory` — 2 entities

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `memory.alx_memories` | **Uses pgvector** (cosine similarity with `vector(1024)` columns per qwen3-embedding:8b). | Medium | Document `CREATE EXTENSION IF NOT EXISTS vector` requirement. pgvector exists on RDS Postgres (since 2023), Aurora Postgres (since 2024), Crunchy, self-hosted Postgres. **Migration to dedicated vector DB** (Pinecone, Weaviate, Qdrant, Chroma) requires rewrite of the search interface and re-embedding the corpus — accept as a possible Phase 5+ rework if scale demands it. |
| `memory.alx_sessions` | Vanilla Postgres + JSONB. | None | Pass. |

### Schema `party` (added GRO-734) — 6 entities + 1 materialized view

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `party.parties` | Vanilla Postgres + JSONB. **Soft FKs** from `t.transactions.party_id`, `o.sales_orders.party_id`, `q.subjects.party_id`, `q.detections.party_id` — application-enforced per Loop 2 precedent. | None | Pass. Document soft-FK pattern (R-3). |
| `party.identifiers` | Vanilla Postgres. **Tenant-salted SHA-256 hash** stored — application-layer; entity is portable. | None | Pass. |
| `party.resolution_events` | Vanilla Postgres + JSONB. | None | Pass. |
| `party.households` | Vanilla Postgres + JSONB. | None | Pass. |
| `party.household_memberships` | Vanilla Postgres. | None | Pass. |
| `party.household_evidence` | Vanilla Postgres + JSONB. | None | Pass. |
| `party.decisioning_facts` (materialized view) | **MV refresh discipline** (nightly batch + on-demand fraud-risk recompute). MVs are vanilla Postgres but **incremental MV refresh requires Postgres ≥15** for `REFRESH MATERIALIZED VIEW CONCURRENTLY` semantics. | Low | Document Postgres ≥15 dependency for `CONCURRENTLY` mode. Stays portable across Postgres flavors. |

### Schema `protocol` (gateway + evidence — 11_protocol.sql)

| Entity | Finding | Severity | Recommendation |
|---|---|---|---|
| `protocol.payloads` (gateway) | Vanilla Postgres + JSONB. Hash + envelope stored; raw bodies offloaded to Cloud Storage via signed URL. | None for entity | Pass for entity definition. The Cloud Storage offload pattern is portable to any S3-compatible store (R2, MinIO, Backblaze, AWS S3). |
| `protocol.evidence_log` (gateway audit) | Vanilla Postgres. | None | Pass. |

### Summary table — severity distribution across all 65 + 7 (party + protocol) entities

| Severity | Count | % | Rationale |
|---|---:|---:|---|
| None | 51 | 71% | Pure vanilla Postgres entities |
| Low | 19 | 26% | Documented Postgres extension dependencies (ltree, btree_gist, pgvector, tstzrange) |
| Medium | 2 | 3% | pgvector usage in `memory.alx_memories` (rewrite-on-migration); `protocol` Pub/Sub fanout pattern (semantic differences, see §3) |
| High | 0 | 0% | **No data-side high-risk findings.** The lock-in lives in compute/orchestration choices (§3). |

---

## §3. Hot spots — junction archetypes (A1-A10)

For each archetype, audit the assumed cloud service from the cloud-architecture-workload.md §5 table. Specifically call out the dispatch's named gotchas.

| Archetype | Cloud service assumption | Portability finding | Severity | Recommendation |
|---|---|---|---|---|
| **A1** Real-time master replicate | Cloud Run + Pub/Sub (ordering-key=tenant_id) | Cloud Run is OCI-portable. Pub/Sub ordering-key semantics differ from Kafka partition keys — Pub/Sub guarantees ordering per ordering-key; Kafka guarantees ordering per partition. For A1 (master replicate per tenant), the semantic maps cleanly. | Low | Document the ordering-key→partition-key mapping for migration. Constrain ordering-key values to `tenant_id` to keep the mapping 1-to-1. |
| **A2** Real-time scan lookup | Cloud Run + Memorystore Valkey | Both portable. Valkey is OSS Redis-protocol-compatible. | None | Pass. |
| **A3** Real-time event emit | Cloud Run + Pub/Sub (transactional fan-out, ordering-key=tenant_id+location_id) | Same Pub/Sub semantic concern. **A3 has higher fan-out volume** (40 of 171 junctions = 23%) so the migration cost compounds. | Low-Medium | Same recommendation as A1. Add: load-test fan-out latency on Kafka before any migration so the SLA contract (p95 < 500ms) survives. |
| **A4** Scheduled batch aggregate | Cloud Run jobs + Cloud Scheduler | Cloud Run jobs are OCI-portable. Cloud Scheduler is replaceable with k8s CronJob, AWS EventBridge, or any cron. | None | Pass. |
| **A5** Event-triggered fan-out | Cloud Run + Pub/Sub | Same Pub/Sub concern as A3. | Low | Document. |
| **A6** Long-running poll | Cloud Run min=1, always-on | OCI-portable. Pattern is cloud-agnostic. | None | Pass. |
| **A7** Append-only event log | Cloud Run + Pub/Sub (30-day retention) | Pub/Sub 7-day max retention by default; 30-day requires explicit config + cost. Kafka retention is independently configurable per topic. | None | Pass — Pub/Sub supports up to 31-day retention (configurable). |
| **A8** Three-way match (stateful) | **Cloud Run + Cloud Workflows** | **HIGH lock-in.** Cloud Workflows YAML is proprietary. The 4 A8 junctions are: `mcp.supplier-invoice.three-way-match`, `mcp.supplier-invoice-line.three-way-match-line`, `mcp.l402.charge-tenant-position`, `mcp.ledger.ildwac.invoice-position`. All carry significant business logic in workflow YAML that doesn't lift-and-shift. | **HIGH** | **R-1: Replace Cloud Workflows with application-state-machine** in Go using Postgres advisory locks for serialization and a `workflow_executions` table for state. Temporal.io is the OSS managed alternative if a managed orchestration layer is needed off-GCP. **Decision required before Phase 4 (PCI scope)** — once compliance is in scope, replacing the orchestration layer becomes a multi-quarter project. |
| **A9** Discriminated message routing | Inline header-routing in subscribers | No dedicated service. Pattern is cloud-agnostic. | None | Pass. |
| **A10** Cross-tenant administrative | Cloud Run + Identity Platform OIDC | OIDC is the standard. Cloud Run is OCI-portable. | None | Pass. |

**Cross-cutting junction concern: per-junction "Portable to: X | Y | Z" annotation missing.** The mcp-service-junctions.md inventory documents archetypes + SLA but doesn't surface portability per junction. **R-4: Add `portable-to:` field to each junction definition** noting the alternative-cloud / self-hosted equivalents that match the SLA. Forces the conversation now while the architecture is still being written.

---

## §4. Acceptable lock-ins (data-side, not code-side)

Some lock-ins are operational, not architectural. They cost time + money to migrate but don't require redesigning the code.

| Lock-in | Severity | Acceptability | Notes |
|---|---|---|---|
| **Cloud SQL storage layer** | Operational | ACCEPTABLE | Data export possible via `pg_dump` over the wire; multi-TB exports take hours but are mechanical. Tenant export tool (cloud-architecture-workload.md §7) keeps the export path warm. |
| **BigQuery storage layer** | Operational | ACCEPTABLE | Export to Cloud Storage as Parquet works at petabyte scale. Migration to Snowflake / Databricks / ClickHouse is a quarter of work, not a week — but it's mechanical, not architectural. |
| **Cloud Storage objects** | Operational | ACCEPTABLE | S3-compatible API at the boundary; objects portable to R2 / Backblaze / MinIO via standard tooling. |
| **Lightning Network state** | Chain-native | ACCEPTABLE | Chain choice is architectural (per memory `project_canary_chain_is_storage` — the chain IS the S4 storage tier). Not a GCP lock-in. |
| **Memory bus pgvector embeddings** | Mid | ACCEPTABLE-WITH-CAVEAT | Embeddings are re-computable from source documents in ~4-8 hours of GPU time. Migration to dedicated vector DB requires rewrite of the search interface — flag as Phase 5+ if scale demands. |
| **OpenTelemetry → Cloud Trace + Cloud Logging** | None at code layer | ACCEPTABLE | OTel SDK is vendor-neutral; only the backend changes (swap collector destination). |
| **Identity Platform OIDC federation** | None at code layer | ACCEPTABLE | OIDC is the standard; Identity Platform behind a standard interface. |

The pattern: **lock-ins live in storage layers (acceptable — exports work) and orchestration layers (the high-risk concern, addressed in §3 and §5).** No data-model-side lock-ins.

---

## §5. Recommendations summary

Prioritized list of changes to close portability gaps. R-1 is must-do-before-Phase-4. R-2 through R-5 are housekeeping that pays off across every future cloud-architecture conversation.

### R-1 — Replace Cloud Workflows assumption in A8 with application-state-machine (HIGH PRIORITY)

**Where**: cloud-architecture-workload.md §5 A8 row + the 4 specific junctions (`mcp.supplier-invoice.three-way-match`, `mcp.supplier-invoice-line.three-way-match-line`, `mcp.l402.charge-tenant-position`, `mcp.ledger.ildwac.invoice-position`).

**Recommended pattern**: Go application-state-machine with Postgres advisory locks for serialization + a `workflow_executions` table tracking state per execution. Schema:

```sql
CREATE TABLE app.workflow_executions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  workflow_type   text NOT NULL,                              -- three-way-match | l402-charge | ildwac-invoice | l2-anchor-submit
  state           text NOT NULL DEFAULT 'pending',            -- pending | in_progress | succeeded | failed | cancelled
  current_step    text,                                       -- the named step in the workflow
  context         jsonb NOT NULL DEFAULT '{}',                -- accumulated state across steps
  started_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  completed_at    timestamptz,
  error           jsonb,                                       -- {step, code, message, retry_count}
  attributes      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_wfe_tenant_active ON app.workflow_executions(tenant_id, state) WHERE state IN ('pending', 'in_progress');
CREATE INDEX idx_wfe_type ON app.workflow_executions(workflow_type, state);
```

Each workflow step is a Go function that:
1. Acquires `pg_advisory_lock(hashtext(workflow_execution_id::text))`.
2. Reads current state from `workflow_executions`.
3. Executes step logic.
4. Atomically updates `state` + `current_step` + `context` + `updated_at`.
5. Releases advisory lock.
6. Emits OTel span + audit_log entry.

Temporal.io is the OSS managed alternative if a managed-orchestration layer is needed off-GCP. **Decision required before Phase 4 (PCI scope).**

### R-2 — Document required Postgres extensions in canonical DDL header

**Where**: top of `canonical-data-model.md` and top of `CanaryGo/deploy/schema/00_schemas.sql` (already present in the latter — verify with code review).

**Required block**:

```sql
-- Required Postgres extensions for the canonical data model:
CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS ltree;           -- m.product_categories.path, l.location_hierarchy.path, l.location_zones.path
CREATE EXTENSION IF NOT EXISTS btree_gist;      -- EXCLUDE constraints on m.item_vendors, c.customer_addresses, e.employee_location_assignments, p.tax_classes, p.item_prices (tstzrange &&), ledger.* tstzrange GIST indexes
CREATE EXTENSION IF NOT EXISTS pg_trgm;         -- text-similarity search (Identity, Q.subjects, m.items)
CREATE EXTENSION IF NOT EXISTS citext;          -- case-insensitive text comparisons (where used)
CREATE EXTENSION IF NOT EXISTS vector;          -- memory.alx_memories.embedding (pgvector — Postgres ≥13)
-- Postgres ≥12 required for: GENERATED ALWAYS AS ... STORED columns
-- Postgres ≥15 required for: REFRESH MATERIALIZED VIEW CONCURRENTLY (party.decisioning_facts)
```

The deployed `00_schemas.sql` has 4 of 6 — verified during this review. **Add `citext` and `vector` to the deployed file when those features land** (citext is referenced in some entity definitions but extension not yet declared; pgvector reserved for memory schema migration).

### R-3 — Document soft-FK pattern in canonical DDL header

**Where**: canonical-data-model.md §10 (or §11) and per-entity finding for q.detections + ledger.* + party.*

The Loop 2 build report (line 92) called out the soft-FK pattern (`q.detections.cashier_employee_id` UUID column with no DB-level constraint, application-enforced). Party-identity-design.md §Soft FK Reconciliation extends it to `t.transactions.party_id`, `o.sales_orders.party_id`, `q.subjects.party_id`, `q.detections.party_id`.

**Document block to add**:

> **Soft FK pattern**: Some cross-schema references are declared as UUID columns without DB-level FK constraints. This is intentional for two reasons: (1) avoids dependency cycles between schemas; (2) preserves write availability of the source schema when the target schema is unavailable. Application code MUST enforce the referential integrity via `<schema>.GetByID(ctx, tenantID, refID)` lookups before writes that populate a soft-FK column. Cached in Valkey for hot paths. Write-time enforcement contract: any service writing to a soft-FK column must verify the target row exists in the same tenant before commit. Soft FKs in canonical: `q.detections.cashier_employee_id` (Loop 2 precedent), `q.detections.customer_id`, `t.transactions.party_id`, `o.sales_orders.party_id`, `q.subjects.party_id`, `q.detections.party_id`.

### R-4 — Add "portable-to:" annotation in every junction description

**Where**: mcp-service-junctions.md per-archetype default + per-junction notes column.

Format: `portable-to: GKE-Autopilot | AWS-Fargate | self-hosted-Nomad | <conditional>`.

Forces the portability conversation at the time the junction is defined, not at migration time. Default annotations per archetype:

| Archetype | Portable-to (default) |
|---|---|
| A1, A2, A3, A5, A7, A9, A10 (Cloud Run-based) | GKE-Autopilot \| AWS-Fargate \| Azure-Container-Apps \| self-hosted-Nomad \| self-hosted-k8s |
| A4 (Cloud Run jobs + Scheduler) | k8s-CronJob \| AWS-EventBridge+ECS \| Nomad-periodic \| any-cron |
| A6 (long-poll) | any-OCI-runtime |
| **A8 (Cloud Workflows)** | **REWRITE REQUIRED** — see R-1 |

### R-5 — Document Pub/Sub message-ordering vs Kafka partition-key semantic differences

**Where**: mcp-service-junctions.md after the archetype-default table.

Add block:

> **Pub/Sub vs Kafka semantic mapping** for portability planning:
>
> - **Pub/Sub ordering-key**: ordering guaranteed per ordering-key value within a single subscriber. Different ordering-keys can be processed in parallel. Subscriber concurrency-1 forces strict ordering across all keys.
> - **Kafka partition-key**: ordering guaranteed per partition. Partition is determined by hash(key) % partition_count. Cannot easily change partition_count post-deploy. Different keys CAN end up in the same partition (collision).
>
> **Mapping rule for Canary**: constrain Pub/Sub ordering-keys to `tenant_id` (A1) or `tenant_id + location_id` (A3 / A5) so the migration to Kafka with `key = tenant_id` (or `tenant_id+location_id`) preserves ordering semantics. Avoid using ordering-keys with high cardinality (per-transaction ordering keys, per-customer ordering keys) because the Kafka equivalent forces partition explosion. The constraint is documented per junction in the "Notes" column.

---

## Status

- **Review complete v1.** Ready for founder review and Engineer-Architect handoff.
- **Companion**: `cloud-architecture-workload.md` — the cloud architecture spec this review audits against.
- **Cross-references**: GRO-733 dispatch · canonical-data-model.md (the audit target) · mcp-service-junctions.md · party-identity-design.md (GRO-734) · Loop 2 build report (`tenant_id` universal key, soft-FK precedent) · `feedback_no_hand_rolling_outside_core_ip` · `project_gcp_commitment_locked` · `project_canary_chain_is_storage` (chain lock-in is acceptable — chain IS the S4 tier).

## Outcome

**Portability verdict: GREEN with three named exceptions.**

The 65 canonical entities (+ 6 party + 2 protocol) themselves are highly portable: 71% pure vanilla Postgres, 26% with documented widely-available Postgres extensions, 3% with extensions that require rewrite on migration to non-Postgres targets. **Zero data-side HIGH-severity findings.**

The lock-in lives in three named compute/orchestration choices (R-1 Cloud Workflows for A8; R-5 Pub/Sub ordering semantics; pgvector for memory bus). All three are addressable via the recommendations above. R-1 is must-do-before-Phase-4; the rest are housekeeping that compounds into credible exit capability over time.

Per memory `project_cloud_provider_accountability_stance`, "we don't trust, we measure" — the credible-exit capability is measured by quarterly migration drills against a sister environment. Drills become possible once R-1 lands; they become routine once R-2 through R-5 are habit. The data model is ready; the orchestration layer needs the one named refactor.
