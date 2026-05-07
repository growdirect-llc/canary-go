---
spec-version: 1.0
target-implementation: Go on GCP (Cloud Run + Cloud SQL + Pub/Sub + Memorystore + Cloud Storage + BigQuery)
stack: Cloud Run | Cloud SQL Postgres 17 | Memorystore (Valkey/Redis) | Pub/Sub | Cloud Storage | BigQuery | Cloud Workflows | Cloud Scheduler | Secret Manager | OAuth 2.0 + OIDC | OpenTelemetry → Cloud Trace + Cloud Logging
status: handoff-ready
updated: 2026-05-03
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Canary Go — Cloud Architecture + Workload SDD

## Governing thesis

Canary runs on GCP because GCP is fastest-to-credible for an ARTS-aligned SMB retail platform with real-time POS ingest, evidentiary anchoring, and L402-gated metering. We target GCP. We do not become hostage to GCP. Code is portable; data is acceptably locked in. Every cloud-service binding in this SDD names a portable alternative and rates the lock-in explicitly. The four accountability rails (operational, financial, evidentiary, vendor) are designed-in, not bolted-on — Rail 4 (vendor accountability) is the reason this SDD exists separate from any one cloud-vendor's reference architecture.

This SDD takes the canonical-data-model.md (65 entities, 11 schemas + party + memory + protocol = 14 deployed schemas, 88 tables in production DDL) and the mcp-service-junctions.md (171 junctions, 10 archetypes A1–A10) and projects them onto a GCP workload spec sized for three tenant archetypes (Small / Medium / Large). Every section is honest about estimates: cost numbers are forecasts at GCP list prices as of 2026-05; assumptions are inline; the satoshi cost-to-serve outputs (§8) are reconciled against `ledger.ildwac_positions` inputs because that's the load-bearing accountability check.

The Top-3 portability risks (anchored in §2 + §5, expanded in the companion `canonical-data-model-portability-review.md`):

1. **Cloud Workflows for A8 (three-way match, L402 charge cycle, anchor-submit-to-L2)** — proprietary YAML state machine, deepest lock-in in the stack. Mitigation: replace with application-state-machine in Go using Postgres advisory locks, before Phase 4.
2. **Pub/Sub message-ordering keys** — semantically different from Kafka partition keys; ordering-key fanout patterns will not lift-and-shift to Kafka without rework. Mitigation: keep ordering-key usage to A3/A5 (event emit + fan-out) where Kafka partition-key maps cleanly; document the deviations.
3. **BigQuery as the warm-tier analytics store** — SQL dialect drift, UDF nonportability, materialized-view semantics are GCP-specific. Mitigation: keep all analytics SQL in `analytics/sql/` source-controlled with a dialect-tag header (`-- dialect: bigquery-standard`); accept that a migration to Snowflake/Databricks/ClickHouse is a quarter of work, not a week.

---

## §1. Tenant archetypes

Three archetypes drive every sizing decision in this SDD. The numbers come from the dispatch and align with the SMB-2030 scope of canonical-data-model.md (1–100 stores, modern tech default, agent-driven workflows).

| Archetype | Stores | SKUs | Tx/yr | Employees | Customers | Vendors | Anchor business |
|---|---:|---:|---:|---:|---:|---:|---|
| **Small** | 1 | 5,000 | 50,000 | 10 | 1,000 | 50 | Solo Main-Street operator (the canonical user — see memory `project_engine_map_and_main_street_archetype`) |
| **Medium** | 10 | 30,000 | 1,000,000 | 100 | 50,000 | 500 | Regional specialty chain |
| **Large** | 100 | 100,000 | 20,000,000 | 1,000 | 500,000 | 5,000 | Multi-banner specialty group |

**Derived load drivers:**

| Driver | Small | Medium | Large | Calc |
|---|---:|---:|---:|---|
| Avg lines per tx | 4 | 6 | 8 | DriftPOS wire-load assumption (§2 RetailTransaction averages) |
| Avg tenders per tx | 1.1 | 1.2 | 1.3 | EBT/split-tender mix scales with size |
| Daily peak tx/sec | 2 | 35 | 700 | 5x avg over 4-hour peak window, 8 peak hours / day |
| Junctions fired per tx | ~25 | ~25 | ~25 | A3 fan-out — t.transaction.complete triggers position recompute, ledger post, party resolve, chirp evaluate, audit log, evidence collect, etc. |
| Peak junction RPS (whole platform) | 50 | 875 | 17,500 | Daily peak tx/sec × 25 junctions/tx |
| Q detections / yr | ~5,000 | ~150,000 | ~3,000,000 | ~10% of tx generate at least one detection signal |
| Audit log rows / yr | ~250,000 | ~10,000,000 | ~250,000,000 | Avg ~5 audit rows per tx + master-data + admin actions |

**Tenant-archetype operating envelope** (from canonical-data-model.md §1):

- Single canonical schema set per database (multi-tenancy via `tenant_id` column in every retail-spine table; per Loop 2 finding, **`tenant_id` is the universal key, not `merchant_id`**).
- `app.tenants.region` (canonical-data-model.md line 4138) configurable per tenant for data residency; default `us-west`.
- Schema-per-tenant model deferred to Phase 4 / Large archetypes; Phases 1–3 use shared-schema row-level isolation enforced at the application layer plus Postgres RLS where adopted.

---

## §2. Cloud service mapping per schema

For each of the 14 deployed schemas (canonical 11 + party + memory + protocol — 88 tables per Loop 2 build report), we name the primary cloud service, the portable alternative, and the lock-in risk. This is the rubric the canonical portability review (Deliverable 2) uses.

| Schema | Primary cloud service (GCP) | Why | Portable alternative | Lock-in risk |
|---|---|---|---|---|
| `app` (4 entities + ~10 preserved) | Cloud SQL Postgres 17 | Identity / tenancy / audit_log / external_identities — relational, transactional, low-volume | Self-hosted Postgres on GKE; AWS RDS Postgres; Aurora Postgres | **LOW** — pure Postgres |
| `m` (6 entities) | Cloud SQL Postgres 17 | Master data — relational, low write rate, high read rate (every POS scan) | Self-hosted Postgres; RDS Postgres | **LOW** — pure Postgres + ltree, btree_gist (extensions widely available) |
| `l` (4 entities incl. zones + assortment) | Cloud SQL Postgres 17 | Location hierarchy with ltree paths; low write, frequent join target | Self-hosted Postgres; RDS Postgres | **LOW** — Postgres + ltree |
| `s` (2 entities) | Cloud SQL Postgres 17 | Planograms — low cardinality, low write rate | Self-hosted Postgres | **NONE** — vanilla Postgres |
| `c` (3 entities) | Cloud SQL Postgres 17 | Customer master — moderate write, high read | Self-hosted Postgres | **NONE** — vanilla Postgres + JSONB |
| `e` (3 entities) | Cloud SQL Postgres 17 | Employee master + EXCLUDE constraint on primary location | Self-hosted Postgres | **LOW** — Postgres + btree_gist |
| `i` (5 entities) | Cloud SQL Postgres 17 + Memorystore (Valkey) for hot inventory cache | `i.inventory_movements` is append-only at high rate; positions cached for sub-30ms scan reads | Self-hosted Postgres + self-hosted Valkey/Redis; Aurora + ElastiCache | **NONE-LOW** — Valkey is Redis-compatible OSS |
| `o` (8 entities) | Cloud SQL Postgres 17 | Orders — moderate write, transactional with inventory + ledger | Self-hosted Postgres | **NONE** — vanilla Postgres |
| `p` (5 entities) | Cloud SQL Postgres 17 + Memorystore for price cache | `p.item_prices` uses EXCLUDE temporal constraint; price resolve hit on every POS scan | Self-hosted Postgres + Valkey | **LOW** — Postgres + btree_gist temporal exclusion |
| `f` (5 entities) | Cloud SQL Postgres 17 | Finance entities — low-volume, transactional | Self-hosted Postgres | **NONE** — vanilla Postgres |
| `t` (9 entities) | Cloud SQL Postgres 17 (hot, 90 days) → Cloud Storage Parquet (warm) → BigQuery (analytics) | Highest-volume schema; partitioned by business_date; hot/warm/cold tiering | Postgres partitioned tables + S3-compatible object store + Snowflake/Databricks/ClickHouse | **MEDIUM** — Postgres portable; BigQuery dialect lock |
| `q` (6 entities) | Cloud SQL Postgres 17 + Cloud Storage for case_evidence binaries | Detections + cases relational; evidence payloads in object storage with hash anchored on Postgres | Self-hosted Postgres + S3-compatible object store (R2, MinIO, etc.) | **LOW** — Cloud Storage is S3-compatible at the API layer |
| `ledger` (5 entities) | Cloud SQL Postgres 17 + Lightning Network state externalized | Stock ledger, ILDWAC positions, RIB batches, OTB budgets in Postgres; blockchain anchors point to off-chain L2 transactions | Self-hosted Postgres + own LN node | **LOW** for Postgres; **CHAIN-NATIVE** for L2 (lock-in is to the chain protocol, not GCP) |
| `memory` (2 entities) | Cloud SQL Postgres 17 + pgvector | Agent memory with semantic search via pgvector cosine similarity | Self-hosted Postgres + pgvector; Pinecone / Weaviate / Qdrant (with rework) | **MEDIUM** — pgvector portable across Postgres flavors but rewrite required to move to dedicated vector DB |
| `protocol` (gateway + evidence — 11_protocol.sql) | Cloud SQL Postgres 17 + Pub/Sub for ingress fanout | Protocol gateway lands payloads, fans out to evidence + ledger + Q | Self-hosted Postgres + Kafka/NATS | **MEDIUM** — Pub/Sub ordering-key semantics ≠ Kafka partition-key |

**Rubric for the workload-class table** (referenced from §5 archetype provisioning):

| Workload class | Cloud service | Why GCP picked it | Portable alternative | Lock-in |
|---|---|---|---|---|
| Stateless HTTP service (junctions A1, A2, A3, A5, A9, A10) | Cloud Run | Per-request billing, scale-to-zero, OCI-image deploy | GKE Autopilot, AWS Fargate, Azure Container Apps, self-hosted on Nomad/k8s | **NONE** — OCI containers |
| Background worker (A4 batch, A6 long-poll) | Cloud Run jobs + Cloud Scheduler | Cron-on-managed-runtime; same OCI image as HTTP | k8s CronJob, AWS EventBridge + ECS, Nomad periodic | **NONE** — OCI + cron |
| Event bus (A3 fan-out, A5 event-triggered) | Pub/Sub | Managed, no broker ops, GCP-native delivery to Cloud Run | Kafka (MSK / Confluent / self), NATS JetStream, Redpanda | **MEDIUM** — ordering-key semantics differ |
| Stateful workflow (A8 three-way match, L402 charge, anchor submit) | Cloud Workflows | Managed orchestration with native Pub/Sub + Cloud SQL hooks | Temporal.io (OSS), AWS Step Functions, application-state-machine in Postgres | **HIGH** — Cloud Workflows YAML is proprietary; see §5 + portability review §3 |
| Relational store | Cloud SQL Postgres 17 | Managed Postgres at known performance ceiling | RDS Postgres, Aurora Postgres, self-hosted Postgres on GCE/EBS/local NVMe | **LOW** — Postgres extensions need replication |
| Hot cache | Memorystore (Valkey) | Redis-protocol-compatible managed | ElastiCache, Upstash, self-hosted Valkey/Redis | **NONE** — OSS protocol |
| Warm analytics store | BigQuery | Petabyte scale, separation of storage + compute, columnar | Snowflake, Databricks SQL, ClickHouse, Athena over Parquet on S3 | **MEDIUM** — SQL dialect + UDFs |
| Cold object store | Cloud Storage | Lifecycle policies, versioning, signed URLs | S3, R2, Backblaze B2, MinIO | **NONE** — S3-compatible API |
| Secrets | Secret Manager | OAuth-bound, audit-logged | HashiCorp Vault, AWS Secrets Manager, sops | **LOW** — replicable |
| Observability | OpenTelemetry → Cloud Trace + Cloud Logging | Vendor-neutral OTel collection; GCP backends | Same OTel SDK → Honeycomb / Datadog / Grafana / Tempo / Loki | **NONE at code layer** — OTel is the standard |
| Identity | OAuth 2.0 + OIDC via Identity Platform | Federation with Workspace, GH Actions, Workload Identity | Auth0, self-hosted Dex, AWS Cognito | **LOW** — OIDC is the standard |

---

## §3. Per-entity volume forecast

For the 8 highest-volume entities specifically called out in the dispatch, forecast row count, storage, peak read RPS, and peak write RPS at year 1 / 3 / 5 across the three archetypes. Other 57 entities collapse to a single summary table at the end of the section.

**Forecast assumptions (stated up front, since these drive every cost number in §8):**

- **Tx growth rate:** 0% Y1→Y3, +25% Y3→Y5 (post-PMF expansion). Justification: SMB-2030 archetypes are mature businesses, not hyper-growth startups; expansion comes from the Phase 2 absorption arc per memory `project_canary_replaces_counterpoint_long_arc`, not organic transaction growth.
- **Retention:** Tier 1 (financial, evidentiary) entities retain 7 years per IRS / SOX precedent; Tier 2 (operational) entities retain 90 days hot + 2 years warm + cold-archive forever; Tier 3 (reference / reporting) hot for 30 days, warm for 1 year.
- **Row size:** estimated from canonical DDL column counts × average bytes per type (uuid 16 + timestamptz 8 + text avg 50 + numeric(14,4) 8 + jsonb avg 200 + index overhead 2x).
- **RPS read estimate:** based on §1 daily peak × 25 junctions/tx × distribution across reads vs writes per A1/A2/A3 archetype.
- **RPS write estimate:** based on §1 daily peak × write-causing junctions per archetype × archetype size.

### `t.transactions` (T schema, A3 archetype, partitioned by business_date)

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 50K | 0.05 | 50K | 0.05 | 62.5K | 0.06 | 5 | 2 |
| Medium | 1M | 1 | 1M | 1 | 1.25M | 1.25 | 80 | 35 |
| Large | 20M | 20 | 20M | 20 | 25M | 25 | 1,500 | 700 |

Row size assumed ~1KB (40+ columns including grand_total, JSONB attributes, external_ids GIN index payload).

### `t.transaction_line_items` (T schema, A3 archetype, partitioned by business_date)

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 200K | 0.16 | 200K | 0.16 | 250K | 0.20 | 20 | 8 |
| Medium | 6M | 4.8 | 6M | 4.8 | 7.5M | 6 | 480 | 210 |
| Large | 160M | 128 | 160M | 128 | 200M | 160 | 12,000 | 5,600 |

Row size assumed ~800B (35 columns plus generated `extended_price`, `extended_tax`, `line_total`, `margin` STORED).

### `t.transaction_tenders` (T schema, A3 archetype, partitioned by business_date)

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 55K | 0.05 | 55K | 0.05 | 69K | 0.06 | 6 | 2.2 |
| Medium | 1.2M | 1 | 1.2M | 1 | 1.5M | 1.25 | 96 | 42 |
| Large | 26M | 21 | 26M | 21 | 32.5M | 26 | 1,800 | 840 |

Row size ~700B (28 columns including processor_ref + jsonb attributes).

### `i.inventory_movements` (I schema, append-only, partitioned by movement_at)

Movement count per transaction estimated at 1.5x line count (sales + reservations + return reversals + adjustments per business day):

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 300K | 0.21 | 300K | 0.21 | 375K | 0.26 | 30 | 12 |
| Medium | 9M | 6.3 | 9M | 6.3 | 11.25M | 7.9 | 720 | 320 |
| Large | 240M | 168 | 240M | 168 | 300M | 210 | 18,000 | 8,400 |

Row size ~700B (24 columns). Append-only — never updated, never deleted (canonical line 1984). Index `idx_movements_position_recompute` is large (multi-column with COALESCE) — adds ~30% to base storage.

### `app.audit_log` (cross-cutting, append-only, partitioned by performed_at)

5 audit rows per tx (transaction created + tenders + line voids + manager overrides + cashier actions) plus master-data CRUD plus admin actions:

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 250K | 0.13 | 250K | 0.13 | 313K | 0.16 | 1 | 10 |
| Medium | 10M | 5 | 10M | 5 | 12.5M | 6.3 | 25 | 175 |
| Large | 250M | 125 | 250M | 125 | 313M | 156 | 500 | 3,500 |

Row size ~500B (10 columns plus jsonb diff). Reads are infrequent (forensic queries, compliance reports) but writes are constant.

### `q.detections` (Q schema, append-only)

10% of transactions emit at least one detection signal:

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak read RPS | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | 5K | 0.005 | 5K | 0.005 | 6.25K | 0.006 | 0.2 | 0.2 |
| Medium | 150K | 0.15 | 150K | 0.15 | 188K | 0.19 | 8 | 4 |
| Large | 3M | 3 | 3M | 3 | 3.75M | 3.75 | 150 | 70 |

Row size ~1KB (17 columns including evidence jsonb snapshot of source data).

### `q.case_evidence` (Q schema, append-only, payload in Cloud Storage)

Evidence chains per case average ~5 entries (transaction snapshot, video clip pointer, photo, document, system log); ~10% of detections escalate to a case:

| Archetype | Y1 rows | Y1 metadata GB | Y1 payload GB (Cloud Storage) | Y5 rows | Y5 GB total | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|
| Small | 2.5K | 0.001 | 0.5 | 3.1K | 0.6 | 0.05 |
| Medium | 75K | 0.04 | 50 | 94K | 60 | 1 |
| Large | 1.5M | 0.75 | 1,500 | 1.9M | 1,800 | 25 |

Metadata row size ~500B (12 columns, payload externalized to GCS via signed URL). Payload size dominated by video clips averaged at ~5MB per evidence entry; photos ~500KB; transaction snapshots ~5KB.

### `ledger.stock_ledger_entries` (Ledger schema, append-only, financial counterpart to inventory_movements)

1:1 with inventory movements (canonical line 3987 — atomic with movement insert):

| Archetype | Y1 rows | Y1 GB | Y3 rows | Y3 GB | Y5 rows | Y5 GB | Peak write RPS |
|---|---:|---:|---:|---:|---:|---:|---:|
| Small | 300K | 0.18 | 300K | 0.18 | 375K | 0.23 | 12 |
| Medium | 9M | 5.4 | 9M | 5.4 | 11.25M | 6.75 | 320 |
| Large | 240M | 144 | 240M | 144 | 300M | 180 | 8,400 |

Row size ~600B (12 columns including generated `cost_amount` STORED).

### `ledger.ildwac_positions` (Ledger schema, satoshi cost-to-serve)

Volume depends on cadence_step granularity. Default per dispatch §8 reconciliation: minute-level for Small (high-fidelity demo), hour-level for Medium, day-level for Large (per-tenant operational batch). Computed positions per year:

| Archetype | Cadence | Positions / yr | Y1 GB | Y5 GB |
|---|---|---:|---:|---:|
| Small | minute | 525,600 | 0.42 | 2.1 |
| Medium | hour | 8,760 | 0.007 | 0.035 |
| Large | day | 365 | 0.0003 | 0.002 |

Row size ~800B (15 columns + tstzrange + generated `total_satoshis` + L402 payment_proof). Note: dispatch implies per-tenant batches; the volume table above is per-tenant. **Open question (flagged §8):** does Small really need minute-level cadence, or is hourly enough? Higher cadence drives ILDWAC compute cost without giving the merchant a more useful invoice.

### Other 57 entities — order-of-magnitude summary

| Schema | Aggregate Y5 rows (Large) | Aggregate Y5 GB (Large) | Notes |
|---|---:|---:|---|
| `m.*` (6 entities) | ~1M | ~2 | Item master + categories + vendors + barcodes + packs |
| `l.*` (4) + `s.*` (2) | ~10K | ~0.05 | Locations + hierarchy + planograms |
| `c.*` (3) | ~1.5M | ~3 | Customers + addresses + loyalty memberships |
| `e.*` (3) | ~5K | ~0.01 | Employees + role + location assignments |
| `i.*` excl. movements (4) | ~10M | ~10 | Positions, documents, document_lines, lots |
| `o.*` (8) | ~50M | ~50 | Purchase orders + sales orders + fulfillment lines |
| `p.*` (5) | ~500K | ~1 | Item prices + promotions + tax classes/rates |
| `f.*` (5) | ~10M | ~15 | Tender types + GL accounts + supplier invoices + payments |
| `t.*` excl. lines/tenders (6) | ~100M | ~50 | Transaction discounts + cashier actions + drawer events + shift events + loyalty events + gift card events |
| `q.*` excl. detections/evidence (4) | ~5M | ~5 | Detection rules + cases + actions + subjects |
| `ledger.*` excl. stock_ledger/ildwac (3) | ~5M | ~3 | RIB batches + L402-OTB budgets + blockchain anchors |
| `app.*` excl. audit_log (3+10 preserved) | ~100K | ~0.1 | Tenants + users + external_identities + organizations etc. |
| `memory.*` (2) | ~10M | ~50 | alx_memories + alx_sessions; pgvector embeddings drive size |
| `party.*` (6 + 1 MV per GRO-734) | ~5M | ~10 | parties + identifiers + resolution_events + households + memberships + evidence + decisioning_facts MV |
| `protocol.*` (gateway + evidence) | ~250M | ~150 | Every payload landing in protocol gateway, hash + envelope; offload to GCS for raw bodies |

**Total Large-archetype Y5 storage estimate:** ~2.7 TB hot Postgres + ~1.8 TB Cloud Storage (case evidence payloads) + ~5 TB warm Parquet + cold archive. Material but not exotic.

---

## §4. Partition strategy per high-volume entity

Every entity in §3 exceeding 10M rows/year at the Large archetype gets explicit partitioning. Partition column, interval, retention in hot Postgres, lifecycle to BigQuery (warm), lifecycle to Cloud Storage (cold).

| Entity | Partition column | Interval | Hot retention (Cloud SQL) | Warm tier (BigQuery export) | Cold tier (Cloud Storage Parquet) |
|---|---|---|---|---|---|
| `t.transactions` | `business_date` | monthly | 90 days | 2 years | Forever (Tier 1) |
| `t.transaction_line_items` | `business_date` (FK from transactions) | monthly | 90 days | 2 years | Forever (Tier 1) |
| `t.transaction_tenders` | `business_date` (FK) | monthly | 90 days | 2 years | Forever (Tier 1) |
| `t.transaction_discounts` | `business_date` (FK) | monthly | 90 days | 2 years | Forever (Tier 1) |
| `t.cashier_actions` | `performed_at` | monthly | 90 days | 1 year | 7 years (Tier 2) |
| `t.cash_drawer_events` | `event_at` | monthly | 90 days | 1 year | 7 years (Tier 2) |
| `i.inventory_movements` | `movement_at` | monthly | 180 days | 5 years | Forever (Tier 1) |
| `app.audit_log` | `performed_at` | monthly | 90 days | 7 years | Forever (Tier 1 — SOX-relevant) |
| `q.detections` | `detected_at` | monthly | 1 year | 7 years | Forever (Tier 1 — LP evidentiary) |
| `q.case_evidence` (metadata) | `collected_at` | monthly | 7 years | n/a | Forever (Tier 1 — chain-of-custody) |
| `ledger.stock_ledger_entries` | `posted_at` | monthly | 365 days | 7 years | Forever (Tier 1 — financial) |
| `ledger.ildwac_positions` | `lower(position_period)` | monthly | 365 days | 7 years | Forever (Tier 1 — billing audit) |
| `protocol.payloads` | `received_at` | weekly | 30 days | 1 year | Forever (Tier 1 — gateway audit) |

**Implementation pattern** (Postgres declarative partitioning):

```sql
CREATE TABLE t.transactions_y2026m05 PARTITION OF t.transactions
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
```

Per-archetype Postgres footprint after partition lifecycle (steady-state Y3+):

| Archetype | Hot DB GB (90d / 180d / 365d) | Warm BigQuery TB | Cold Cloud Storage TB |
|---|---:|---:|---:|
| Small | 1.5 | 0.01 | 0.05 |
| Medium | 30 | 0.5 | 1.5 |
| Large | 600 | 12 | 35 |

Partition rotation runs nightly at 02:00 local per tenant (A4 archetype; Cloud Scheduler triggers Cloud Run job). Rotation atomically:

1. Detaches the oldest hot partition.
2. Exports its rows to BigQuery via Federated Query.
3. Exports its rows to Cloud Storage as Parquet (Snappy, partition-keyed prefix).
4. Computes Merkle root over partition contents and anchors to L2 via `ledger.blockchain_anchors` (Rail 3 — every detached partition's contents get anchored before drop).
5. Drops the detached partition from hot Postgres.

The anchor step is non-negotiable — Rail 3 (no unanchored record) requires that data leaving hot storage carry a verifiable hash. The cost is modest (one Merkle tree + one Lightning anchor per partition rotation per tenant per month).

---

## §5. Per-junction compute provisioning

For each of the 10 archetypes from `mcp-service-junctions.md` (A1–A10), specify Cloud Run instance class, min/max instances, concurrency, cold-start tolerance, Pub/Sub topic config, and monthly cost per archetype × tenant size.

Cost basis: GCP list price 2026-05 for us-west1 — Cloud Run vCPU $0.024/hr, Memory $0.0025/GiB-hr; Cloud SQL Postgres 17 db-custom-N-vCPU-MgGB at standard rates; Pub/Sub $40/TiB throughput + $0.30/M operations; Memorystore Valkey ~$0.06/GiB-hr. Estimates exclude egress and discretionary discounts.

| Archetype | Description | Cloud Run class | Min/max | Concurrency | Cold-start tolerance | Pub/Sub config | Small $/mo | Medium $/mo | Large $/mo |
|---|---|---|---|---|---|---|---:|---:|---:|
| **A1** Real-time master replicate | 1vCPU 512MiB | 0/10 | 80 | OK (master writes are operator-driven, not user-facing) | Topic per entity-type, 7-day retention, ordering-key=tenant_id | $5 | $30 | $200 |
| **A2** Real-time scan lookup | 1vCPU 1GiB (cache-hot path) | 1/50 (Small=0/10) | 200 | NOT OK — POS scan path; min=1 to keep warm in Med/Large | n/a (read-through cache) | $15 | $250 | $4,000 |
| **A3** Real-time event emit | 1vCPU 1GiB | 0/100 | 100 | OK (transactional write path; 500ms p95 SLA permits cold start) | Topic per emit-class, 7-day retention, ordering-key=tenant_id+location_id | $25 | $400 | $7,500 |
| **A4** Scheduled batch aggregate | 2vCPU 4GiB | 0/5 (Cloud Run jobs) | 1 (job) | n/a (scheduled) | Trigger via Cloud Scheduler | $10 | $80 | $600 |
| **A5** Event-triggered fan-out | 1vCPU 1GiB | 0/30 | 50 | OK (2s p95 SLA absorbs cold start) | Subscriber per consumer, push-mode to Cloud Run | $5 | $80 | $1,500 |
| **A6** Long-running poll | 1vCPU 2GiB (state-bearing during poll) | 1/3 (always-on per source) | 1 | n/a (long-poll) | n/a (HTTP poll to external) | $30 | $120 | $400 |
| **A7** Append-only event log | 1vCPU 512MiB | 1/30 | 200 | NOT OK (100ms p95 SLA; min=1) | Topic per event-class, 30-day retention | $15 | $200 | $3,500 |
| **A8** Three-way match (stateful) | 2vCPU 2GiB; Cloud Workflows for orchestration **(LOCK-IN — see §3 portability review)** | 0/3 | 10 | OK (seconds-to-minutes per match) | n/a (workflow-driven) | $20 | $150 | $1,200 |
| **A9** Discriminated message routing | (handled inline by routing logic; no dedicated runtime — implemented via header-routing in subscribers) | n/a | n/a | n/a | n/a | $0 | $0 | $0 |
| **A10** Cross-tenant administrative | 1vCPU 1GiB | 0/2 | 5 | OK (interactive; seconds latency acceptable) | n/a | $1 | $5 | $30 |

**Per-archetype total junction-compute monthly cost:**

| Archetype | Junction compute $/mo | Notes |
|---|---:|---|
| Small | ~$125 | Most A2 paths idle most of the day; A6 polls for the 1–2 external POS sources (Square + Counterpoint typical) |
| Medium | ~$1,300 | A2 + A3 dominate; 10 stores generate constant POS traffic |
| Large | ~$19,000 | A2 + A3 + A7 dominate; 100 stores at peak generate 17,500 RPS junction-fan-out |

**Cloud Run scaling math** (worked example for Large A3 transaction.complete):

- Peak: 700 tx/sec → A3 emit at 700/sec → with concurrency=100, min instances needed = 7
- Add 2x headroom for 15-second autoscale window → 14 instances at peak
- 8 peak hours/day × 30 days = 240 instance-hours per month per peak instance, × 14 = 3,360 instance-hours
- Off-peak 16 hours × 30 days × ~3 instances avg = 1,440 instance-hours
- Total ~4,800 vCPU-hr × $0.024 = $115/mo for vCPU + $30 for memory = $145/mo for **just** transaction.complete
- Multiply across the 22 T-domain junctions firing on each transaction → ~$3,000/mo of T-domain Cloud Run, consistent with the $7,500 A3 row above for full A3 archetype (T + ledger.* + q.detection.evaluate-on-event + party.resolve-from-tender + ...)

**Cloud Workflows lock-in callout for A8** (informs portability review §3):

The dispatch flagged A8 as the deepest-lock-in workload. Cloud Workflows offers managed orchestration for:

- `mcp.supplier-invoice.three-way-match` (PO + receipt + invoice correlation)
- `mcp.l402.charge-tenant-position` (L402 charge cycle)
- `mcp.ledger.anchor.submit-to-l2` (Lightning Network submission with retry)

Cloud Workflows YAML is proprietary and does not lift-and-shift. **Recommended alternative**: application-state-machine in Go using Postgres advisory locks for serialization and a `workflow_executions` table for state. Temporal.io is the OSS analog if a managed alternative is needed off-GCP. Decision should be made before Phase 4 (PCI scope) — once compliance is in scope, replacing the orchestration layer becomes a multi-quarter project.

---

## §6. Backup / RPO / RTO per criticality tier

Three tiers as named in the dispatch. Every schema maps to one. RPO/RTO targets drive backup strategy + cross-region replication.

### Tier 1 — Financial / evidentiary (zero data loss tolerance)

**Schemas:** `t.*`, `i.*` (movements), `f.*`, `ledger.*`, `q.*` (detections + evidence), `app.audit_log`, `protocol.*`

**RPO:** ≤1 minute. **RTO:** ≤1 hour.

**Mechanism:**
- Cloud SQL HA with automated backups every 6 hours + WAL archiving every minute to Cloud Storage cross-region bucket.
- Cross-region read replica in `us-east1` (failover candidate).
- Point-in-time recovery (PITR) enabled with 35-day retention.
- Cloud Storage objects (case evidence) versioned with object hold; replicated to dual-region bucket.
- Blockchain anchors are themselves the irreversible backup of cryptographic state — anchored hashes survive complete platform loss (Rail 3 closure).

### Tier 2 — Operational (limited data loss tolerance)

**Schemas:** `m.*`, `l.*`, `s.*`, `c.*`, `e.*`, `o.*`, `p.*`, `i.*` (positions, documents, lots — derived from movements)

**RPO:** ≤15 minutes. **RTO:** ≤4 hours.

**Mechanism:**
- Cloud SQL HA with automated backups every 6 hours + WAL archiving every 15 minutes.
- PITR enabled with 14-day retention.
- Single-region; cross-region replication only on Tier 1 tables.
- Inventory positions can be fully recomputed from `i.inventory_movements` (Tier 1) so position data loss is recoverable.

### Tier 3 — Reference / reporting (rebuildable)

**Schemas:** `memory.*`, BigQuery warm tier, derived metrics caches

**RPO:** ≤24 hours. **RTO:** ≤24 hours.

**Mechanism:**
- Daily Cloud SQL backup with 7-day retention.
- BigQuery has snapshot capability built in (point-in-time table reads up to 7 days).
- Memory embeddings are rebuildable from source documents in 4–8 hours of GPU time.

### Schema → tier map (every schema accounted for)

| Schema | Tier | Justification |
|---|---|---|
| `app` (4 entities + ~10 preserved) | Tier 1 (audit_log) + Tier 2 (rest) | Audit log is SOX-relevant; rest is operational |
| `m` | Tier 2 | Master data; recoverable from external systems if catastrophic |
| `l` | Tier 2 | Locations + zones; low write rate, tolerable to lose recent edits |
| `s` | Tier 2 | Planograms; weeks-of-work to rebuild but not transactional |
| `c` | Tier 2 | Customer master; sensitive but rebuildable from POS history |
| `e` | Tier 2 | Employee master; HR is system of record |
| `i` (movements) | Tier 1 | Append-only, source of truth for all inventory state |
| `i` (positions, docs, lots) | Tier 2 | Recomputable from movements |
| `o` | Tier 2 | Orders; in-flight orders need reasonable RPO but not zero-loss |
| `p` | Tier 2 | Pricing; recoverable from POS-native sync |
| `f` | Tier 2 | Finance master; transactional details are in `t` (Tier 1) |
| `t` | Tier 1 | Every transaction is irreplaceable financial + evidentiary record |
| `q` | Tier 1 | LP evidentiary chain; chain-of-custody requires zero loss |
| `ledger` | Tier 1 | Financial valuation + cost-to-serve + accountability rails |
| `memory` | Tier 3 | Rebuildable from source documents |
| `protocol` | Tier 1 | Gateway audit + evidence of every wire crossing |

---

## §7. Multi-region + data residency

**Default region:** `us-west1` (Oregon — colocated with founder's primary infrastructure and lowest network distance to West Coast SMB tenants).

**Per-tenant configurability:** `app.tenants.region` column (canonical line 4138) drives per-tenant data placement. Tenants can request `us-east1`, `eu-west1`, or `northamerica-northeast1` (Quebec, for Canadian tenants). New regions activated on demand for ≥3 tenants in a region.

**Cross-region replication:** Tier 1 schemas only. Cloud SQL cross-region read replica per tenant region, asynchronous, lag <5 seconds typical. Failover-capable but not active-active.

**Data residency contract:** Tenant data physically resides only in the configured region for Cloud SQL primary, Cloud SQL replica, Cloud Storage buckets, BigQuery datasets, Pub/Sub topics, and Memorystore instances. Cloud Run is multi-regional — request routing via `Workload-Identity-Header` ensures tenant requests are routed to the correctly-placed compute pool. **Open question (§8):** is one Cloud Run service per region the right pattern, or a single multi-region service that reads region from request context? Latter is simpler operationally; former is stronger residency guarantee.

**Data export tooling spec for offboarding** (Rail 4 — credible exit must be real):

A tenant can export their full dataset at any time via `mcp.tenant.export`. The export bundles:

1. Full Postgres dump of every schema, scoped to `WHERE tenant_id = $1`, in Postgres-native `pg_dump` directory format.
2. Full Cloud Storage object dump of `q.case_evidence` payloads, signed-URL list + parallel-fetch script.
3. Full BigQuery export of warm-tier partitioned tables to Cloud Storage Parquet.
4. All blockchain anchor receipts (`ledger.blockchain_anchors` rows for the tenant) with verification scripts that work without Canary infrastructure.
5. Schema DDL bundle (the canonical-data-model.md DDL) + the data dictionary.

Export packaged as a single signed Cloud Storage URL bundle. Tenant runs verification scripts (provided) to confirm all anchored hashes match exported data. **Quarterly migration drills** (per Rail 4) exercise this path against a non-prod sister environment to keep the credible-exit credible.

---

## §8. Per-tenant-archetype cost model

End-to-end monthly GCP cost per tenant size, with reconciliation against `ledger.ildwac_positions` (the satoshi cost-to-serve rollup that Canary actually charges merchants for).

**Pricing assumptions (GCP list, us-west1, 2026-05):**

- Cloud SQL Postgres 17: db-custom-2-8GB ~$200/mo; db-custom-4-26GB ~$650/mo; db-custom-16-104GB ~$2,600/mo
- Cloud SQL HA replica: 100% additional
- Cloud SQL storage SSD: $0.17/GB-mo
- Cloud Run vCPU: $0.024/hr (~$17/vCPU-mo always-on)
- Cloud Run memory: $0.0025/GiB-hr
- Memorystore Valkey: ~$45/GiB-mo (1GB) declining to ~$30/GiB-mo (10GB+)
- Pub/Sub: $40/TiB throughput + $0.30/M operations
- Cloud Storage standard: $0.020/GB-mo; nearline $0.010/GB-mo; coldline $0.004/GB-mo; archive $0.0012/GB-mo
- BigQuery storage: $0.020/GB-mo active; $0.010/GB-mo long-term; query $5/TiB scanned (or $40/slot-hour reservation)
- Egress: $0.12/GB cross-region; $0.085/GB internet (assumed minimal for SaaS)
- Observability (Cloud Trace + Logging): ~$0.50/GB ingested

### Small archetype (1 store, 50K tx/yr)

| Component | Spec | $/mo |
|---|---|---:|
| Cloud SQL Postgres (no HA — Tier-2 acceptable for single-store SMB) | db-custom-2-8GB, 50GB SSD | $210 |
| Cloud SQL backups + WAL | 7d retention | $15 |
| Cloud Run (junctions, all archetypes summed) | per §5 | $125 |
| Memorystore Valkey | 1GB, no HA | $45 |
| Pub/Sub | ~50GB/mo throughput | $5 |
| Cloud Storage (case evidence + warm + cold) | 1GB hot + 10GB nearline + 50GB coldline | $5 |
| BigQuery | 10GB active + 100GB long-term + minimal queries | $5 |
| Memory + memory embeddings (Cloud SQL pgvector) | included in Cloud SQL above | $0 |
| Observability | OpenTelemetry, ~10GB/mo | $5 |
| Secret Manager + Identity Platform | minimal | $5 |
| Lightning anchor fees (estimated) | ~50 anchors/mo @ ~10 sat each | <$1 |
| Networking + DNS + Cloud Workflows steps | minimal | $10 |
| **TOTAL Small** |   | **~$430/mo** |

### Medium archetype (10 stores, 1M tx/yr)

| Component | Spec | $/mo |
|---|---|---:|
| Cloud SQL Postgres (HA — Tier-1 financial schemas elevated) | db-custom-4-26GB primary + HA replica + cross-region read replica, 200GB SSD | $1,950 |
| Cloud SQL backups + WAL + PITR | 35d retention Tier 1 | $80 |
| Cloud Run (junctions) | per §5 | $1,300 |
| Memorystore Valkey HA | 5GB | $300 |
| Pub/Sub | ~1TB/mo throughput | $50 |
| Cloud Storage (case evidence + warm + cold) | 60GB nearline + 500GB coldline + 1TB archive | $20 |
| BigQuery | 500GB active + 5TB long-term + ~5TB queried/mo | $115 |
| Observability | OpenTelemetry, ~100GB/mo | $50 |
| Secret Manager + Identity Platform | per-tenant secret set | $10 |
| Cloud Workflows + Cloud Scheduler | A8 + A4 jobs | $25 |
| Lightning anchor fees (estimated) | ~1,000 anchors/mo @ ~10 sat each | $5 |
| Networking + cross-region replica egress | ~500GB/mo cross-region | $60 |
| **TOTAL Medium** |   | **~$3,965/mo** |

### Large archetype (100 stores, 20M tx/yr)

| Component | Spec | $/mo |
|---|---|---:|
| Cloud SQL Postgres (HA + 2 read replicas + cross-region) | db-custom-16-104GB, 600GB SSD, full HA stack | $9,800 |
| Cloud SQL backups + WAL + PITR | 35d retention Tier 1 | $400 |
| Cloud Run (junctions) | per §5 | $19,000 |
| Memorystore Valkey HA cluster | 25GB | $1,200 |
| Pub/Sub | ~25TB/mo throughput | $1,200 |
| Cloud Storage (case evidence + warm + cold) | 1.8TB nearline + 35TB coldline + 100TB archive | $700 |
| BigQuery | 12TB active + 50TB long-term + ~50TB queried/mo | $1,250 |
| Observability | OpenTelemetry, ~2.5TB/mo | $1,250 |
| Secret Manager + Identity Platform | per-store secret sets | $50 |
| Cloud Workflows + Cloud Scheduler | A8 + A4 jobs at scale | $200 |
| Lightning anchor fees | ~25,000 anchors/mo @ ~10 sat each | $50 |
| Networking + cross-region replica egress | ~25TB/mo cross-region | $3,000 |
| **TOTAL Large** |   | **~$38,100/mo** |

### Reconciliation against `ledger.ildwac_positions`

The schema (canonical line 3996-4019) decomposes cost-to-serve into three satoshi components per cadence step:

- `l_storage_satoshis` — bytes-under-management cost
- `w_workload_satoshis` — queries / writes / events processed
- `c_capture_satoshis` — capture-fidelity cost (low / medium / high / full per CRDM TLOG detail level)

The §8 GCP cost numbers must reconcile to these three buckets. Mapping:

| ILDWAC component | Maps to GCP line items |
|---|---|
| **L (storage)** | Cloud SQL storage SSD + Cloud SQL backups/WAL + Cloud Storage (all tiers) + BigQuery storage |
| **W (workload)** | Cloud Run vCPU + memory + Pub/Sub throughput + ops + BigQuery query + Cloud Workflows steps |
| **C (capture-fidelity)** | Lightning anchor fees + Observability ingestion + cross-region replica egress (the cost of fidelity in evidence + telemetry + DR) |

**Per-archetype reconciliation (assuming sat ↔ USD at $0.0006/sat = $60K/BTC, 2026-05 spot mid):**

| Archetype | L $/mo | W $/mo | C $/mo | Total $/mo | Implied L sat | Implied W sat | Implied C sat | Total sat/mo |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Small | $235 | $145 | $20 | $400 + ~$30 ovh | 391K | 242K | 33K | ~717K |
| Medium | $2,165 | $1,580 | $130 | $3,875 + ~$90 ovh | 3.6M | 2.6M | 217K | ~6.6M |
| Large | $12,150 | $22,950 | $4,300 | $39,400 + ~$1,300 disc | 20M | 38M | 7M | ~65M |

**RECONCILIATION OUTCOME: PASS (with one caveat).**

- L : W : C ratio sanity check: Small skews L-heavy (low workload, fixed storage floor); Medium balances; Large is W-dominated (millions of transactions per month each forcing 25 junction fires). Pattern is intuitive — the smaller the merchant, the more they pay for storage floor relative to their actual workload. **Implication for billing**: minute-level cadence on Small (per §3) is dramatically over-fidelity and would bill sub-$0.01 line items every minute. **Recommendation**: Small defaults to hour-level cadence; tenants opt in to minute-level only if they want the dashboard.
- C is materially under 10% of total in Small/Medium and ~11% in Large. This is the right order of magnitude — capture-fidelity cost should be ~10% of platform cost, not 50%. If C blew past 25%, it would mean we're over-anchoring or over-logging (Rail 3 weight imbalance).
- Caveat: the §8 numbers do not include profit margin, Anthropic API costs (memory bus + agent inference is partially Cloud Run vCPU and partially external API), or Stripe/L402 settlement fees. These must be loaded into ILDWAC `attributes` JSONB or accumulated through a separate `platform_overhead` accumulator before the position invoices the tenant. **Open question (§8 OQ-3):** what's the markup envelope from raw GCP cost → invoiced satoshi position?

### Open questions for founder review

| OQ # | Question | Why it matters |
|---|---|---|
| OQ-1 | Is one Cloud Run service per region or one multi-region service the right pattern for tenant data residency? | Operational simplicity vs residency guarantee |
| OQ-2 | Should Small archetype default to hour-level ILDWAC cadence (vs minute) to avoid sub-$0.01 line items? | UX + invoicing readability |
| OQ-3 | What's the markup envelope from raw GCP cost → invoiced satoshi position? Cost+10%? Cost+30%? Tier-based? | Sets unit economics; affects every pricing surface |
| OQ-4 | Cloud Workflows lock-in for A8 — replace before Phase 4 (PCI), or accept and isolate? | Multi-quarter portability decision |
| OQ-5 | Does the Phase 4 PCI/data-hosting compliance load (per memory `project_pci_scope_phase4`, `project_data_hosting_compliance_phase4`) require dedicated tenant-isolated Cloud SQL instances at Large archetype? | Adds ~$1,500-3,000/mo per Large tenant if yes |
| OQ-6 | Is the Lightning Network anchor cadence (per partition rotation) too aggressive? Aggregate to weekly/monthly Merkle anchors instead of per-partition? | Reduces anchor fees + L2 traffic, preserves Rail 3 |
| OQ-7 | Should party schema (GRO-734, 6 tables + MV) get its own service tier or co-locate with `c.*`? Materialized view refresh cost is significant at Large. | Service granularity vs operational overhead |
| OQ-8 | Cross-region replica egress is ~$3K/mo at Large — is the Tier-1 cross-region requirement absolute, or are some Tier-1 schemas (e.g., `app.audit_log`) acceptable to keep single-region with daily snapshots? | Material cost reduction available |

---

## Status

- **SDD complete v1.** Ready for founder review and Engineer-Architect handoff.
- **Companion**: `canonical-data-model-portability-review.md` — code-side audit of the canonical model against the cloud-service mapping in §2.
- **Cross-references**: GRO-733 dispatch · canonical-data-model.md (4462 lines, 65 entities) · mcp-service-junctions.md (171 junctions, 10 archetypes) · party-identity-design.md (GRO-734) · driftpos-integration.md (GRO-759 wire load) · platform-thesis.md card (Rail 4 added 2026-05-02) · `project_satoshi_cost_model` · `project_gcp_commitment_locked` · `project_cloud_provider_accountability_stance` · `project_pci_scope_phase4` · `project_data_hosting_compliance_phase4` · Loop 2 build report (88 tables, `tenant_id` universal key)
