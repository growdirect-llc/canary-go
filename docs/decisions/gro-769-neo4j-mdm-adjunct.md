# GRO-769 Neo4j MDM Adjunct — Assessment

**Date:** 2026-05-07
**Scope:** Whether to introduce a Neo4j read adjunct on the metrics / master-data side of canary.go for graph-shaped data currently flattened into Postgres recursive CTEs and self-referential FKs.
**Status:** Assessment — not yet decided.
**Linked filing:** assign Linear ticket on adoption; this doc is the assessment substrate.

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
