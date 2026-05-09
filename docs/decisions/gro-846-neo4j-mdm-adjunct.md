# GRO-846 Neo4j MDM Adjunct — Assessment

**Date:** 2026-05-07
**Decided:** 2026-05-09 — **Declined.** See "Decision" section below.
**Scope:** Whether to introduce a Neo4j read adjunct on the metrics / master-data side of canary.go for graph-shaped data currently flattened into Postgres recursive CTEs and self-referential FKs.
**Status:** Closed — declined.
**Linked filing:** [GRO-846](https://linear.app/growdirect/issue/GRO-846) — Architecture: Neo4j MDM read-adjunct.

---

## Decision (2026-05-09)

**Declined.** canary.go does not adopt Neo4j as a read adjunct. Customer 360 dedup, product-variant trees, location hierarchies, employee reporting structures, and merchant org hierarchies remain on Postgres recursive CTEs and self-referential FKs.

### Reasoning

- **Operational cost vs current scale.** Running a Neo4j cluster monthly is not justified by current customer volume or query workload. The cost surface enumerated below (cluster ops, backup, monitoring, DR, engineering ramp on Cypher / neo4j-go-driver) does not amortize against the read-latency win at our scale.
- **Postgres extension headroom.** `ltree` for hierarchies, recursive CTEs with proper indexing, `jsonb` + GIN for flexible attributes, and `pgvector` cover the traversal-shaped reads identified in this assessment. Apache AGE remains an in-database escape valve for Cypher-shaped queries if a workload genuinely needs it without leaving Postgres.
- **Deployment simplification is a long-term play.** Single-store deployment is operationally cheaper, observability-cheaper, and engineering-cheaper. Multi-store complexity (CDC pipeline, dual-store soft-FKs, eventual consistency, dual-driver constructors) compounds over time even when each individual concern looks tractable.
- **AtlasView reference precedent.** Per the 2026-05-09 Grok architectural review (`/Users/gclyle/Downloads/grok-thread-design-review.md`), the firm's working assumption is that AtlasView itself will drop Neo4j and consolidate on Postgres + extensions. Even if that decision goes the other way, canary.go's case at its own scale stands independently.

### What this rules in

- Future graph-shaped read pressure handled via PG extensions (`ltree`, `pgvector`, `pg_trgm`) before any cross-store consideration.
- If a specific Cypher-shaped query lands a future ticket and CTEs cannot meet latency targets, evaluate Apache AGE in-database before considering an external graph store.

### What this rules out

- Neo4j cluster deployment.
- CDC pipeline (Debezium / logical replication slot projection workers) feeding a graph projection store.
- `neo4j-go-driver` dependency.
- Dual-store soft-FK + eventual-consistency complexity.

### Re-evaluation triggers

Reconsider only if all three of the following hold:
1. A specific traversal-shaped query latency target is unmet by Postgres CTEs / AGE / proper indexing.
2. The query volume amortizes Neo4j operational cost (rough threshold: enterprise-tier customer with sustained graph-traversal workload).
3. AtlasView has already adopted (or retained) Neo4j in production, providing a firm-wide skill base.

---

---

## Background

Canary.go currently runs Postgres + Valkey. No graph database. Several domains carry graph-shaped relationships modeled relationally:

- Customer 360 / dedup graphs — `related_customer_id` soft-FKs and merge histories.
- Product-variant trees — parent SKU → child SKUs → barcodes; `external_identifiers` resolution.
- Location hierarchies — corporate → region → store → register → bin.
- Employee reporting structures — `subjects.cashier_employee_id` and `related_employee_id` soft-FKs (Loop 3 backlog #12).
- Merchant org hierarchies — multi-tenant parent/child relationships across `app.tenants`.

Recursive CTEs in Postgres handle these but cost on read latency and query-shape complexity. The same shape is routine in commercial MDM and large-retailer customer-360 deployments where Neo4j typically lands as a read adjunct (Postgres-as-system-of-record, CDC projects subgraphs into Neo4j, traversal-shaped reads served from Neo4j, no direct writes to Neo4j).

The reference implementation already in the firm: AtlasView (the configuration engine for ALX) runs Postgres + Neo4j with cross-store soft-FK and the no-atomic-cross-store-write rule. Driver pinned at `github.com/neo4j/neo4j-go-driver/v5` per `spec/standards/go.md` in the Ruptiv repo.

## Proposed pattern

| Layer | Role |
|---|---|
| Postgres | System-of-record. OLTP path. All writes land here. Source of truth for compliance, audit, and reporting. |
| CDC | Streams master-data subgraphs from Postgres into Neo4j. Pattern candidates: Debezium → Kafka/Pub-Sub → Neo4j sink; logical replication slots driving a Go projection worker. |
| Neo4j | Read adjunct. Holds the graph projections. Serves traversal-shaped reads. Never written to directly from service code. |
| Service handler | Reads route by query shape — Postgres for relational reads, Neo4j for traversal reads. |

Cross-store consistency: Neo4j is eventually consistent with Postgres on the projection lag of the CDC pipeline. Read paths must tolerate a small staleness window.

## Domains to assess in priority order

1. **Customer 360 / dedup.** Highest commercial value. Deduplication trees and merge histories are canonical Neo4j use cases.
2. **Product-variant trees.** Parent SKU resolution under `external_identifiers` lookup. Read-heavy. Adapter modules (`internal/adapters/<source>/lookup.go`) currently handle this with vendor-id translation.
3. **Location hierarchies.** Multi-level corporate-to-bin traversal. Frequency depends on enterprise customers; SMB tenants may not need.
4. **Employee reporting structures.** Lower commercial value at current customer mix but likely value for risk and case management (`internal/casemgmt/`).
5. **Merchant org hierarchies.** Useful for white-label and franchise customers; lower urgency.

## Cost / risk surface

| Concern | Detail |
|---|---|
| Operational | Run a Neo4j cluster in GCP. Backup, monitoring, disaster recovery. Cost increases linearly with data volume. |
| CDC pipeline | Build and maintain. Failure modes: lag, dropped events, schema drift. Observability required (lag metrics, projection-error counters). |
| Dual-store observability | Tracing must propagate across Postgres → CDC → Neo4j boundaries. OpenTelemetry already pinned per `docs/conventions.md`. |
| Engineering ramp | Cypher fluency on the team. Neo4j-go-driver patterns. Graph-modeling discipline. |
| Read-path complexity | Service handlers must route reads correctly. Mistaken Neo4j reads on stale projections produce read-after-write inconsistency for the user. |
| Query design | Graph-modeling decisions are durable; bad initial models cost rework. |

## Decision options

- **Adopt.** Pilot one domain (recommend Customer 360), evaluate, expand. Estimated pilot effort: 1 engineer, 6–10 weeks, plus ongoing Neo4j operational cost.
- **Pilot only.** Scope a 2-week proof-of-concept on Customer 360 using a dev Neo4j instance. Defer adoption decision to post-PoC.
- **Decline.** Continue with Postgres recursive CTEs. Accept the latency and query-shape cost. Re-evaluate at a future enterprise-scale inflection.
- **Defer.** Re-assess after M2 milestone closes. Park as an open architectural seed.

## Cross-references

- `docs/conventions.md` §"Soft-FK convention" — names the soft-FKs likely candidate for graph projection.
- `docs/conventions.md` §"sqlc rule reconciliation" — Neo4j has no sqlc equivalent; Cypher queries live as named constants in service code.
- AtlasView reference (Ruptiv repo): `spec/standards/go.md` documents the Postgres + Neo4j pattern as currently implemented in the source application.
- AtlasView Sparring Partner cards (Ruptiv repo): `atlasview/sparring-partners/{zone,org-unit,organization,team,role,position,person}.md` — examples of graph-shaped accountability modeling.

## Recommended next step

Pilot scope a 2-week PoC on Customer 360 dedup. Build a minimal CDC projection from `app.customers` and any `related_customer_id` graph into a dev Neo4j instance. Implement one read endpoint (e.g., "find all duplicates of customer X within N hops"). Measure latency improvement vs the equivalent Postgres recursive CTE. Decision after PoC.
