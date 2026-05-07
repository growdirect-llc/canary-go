---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: active-build-spec
updated: 2026-04-28
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Canary — Go Build Specification

This corpus is the complete functional specification for the Canary production build. It was derived from a working Python prototype (GRO-617), scrubbed of all implementation-language specifics, and extended with spec additions the prototype left implicit. Every SDD here describes **what the system does** — not how any particular language does it.

**Commercial context:** Canary is being built as an agent-driven retail operations platform targeting SMB merchants on NCR Counterpoint via VAR co-sell (Rapid POS and other Counterpoint VARs). The Python prototype proved the detection model and evidence chain. The Go build is the production product — clean from design, built to run with minimal human-in-the-loop intervention. Square remains a supported connector (Marketplace-certified prototype exists); NCR Counterpoint is the primary engagement path for the Go build.

The implementation is Go. Stack decisions are final:

| Layer | Choice |
|---|---|
| Database | PostgreSQL 17 |
| Driver | pgx (native Go) |
| Query layer | sqlc (type-safe Go from SQL) |
| HTTP router | Chi |
| Internal comms | REST |
| Vector store | pgvector (in-database, via pgvector-go) |
| Cache / queue | go-redis (Valkey-compatible) |
| Migrations | golang-migrate or goose |

---

## Read in this order

### 1. Product definition
- **[platform-overview.md](platform-overview.md)** — What Canary is, who it serves, what it does. Read this first. Everything else is the implementation of this document.
- **[multi-pos-architecture-proof.md](multi-pos-architecture-proof.md)** — Why the design is POS-agnostic. Read before touching any adapter code.

### 2. System architecture
- **[microservice-architecture.md](microservice-architecture.md)** — 10 Go microservices: ports, REST APIs, table ownership, inter-service communication, deployment topology. Read this before any other architecture doc.
- **[architecture.md](architecture.md)** — Logical service design, runtime topology, communication patterns.

### 3. Data model
- **[data-model.md](data-model.md)** — All 82 tables across app / sales / metrics schemas. Schema contracts (column names, types, constraints, indexes). This is the source of truth for sqlc queries.
- **[external-identities.md](external-identities.md)** — How POS-native IDs map to Canary canonical IDs. Critical for multi-POS correctness.
- **[identity.md](identity.md)** — Merchant, user, and tenant model. Auth contract. Session contract.

### 4. Ingestion pipeline
- **[webhook-pipeline.md](webhook-pipeline.md)** — Full pipeline: webhook receipt → HMAC verify → stream → seal → parse → merkle → detect. Read this before tsp.md.
- **[tsp.md](tsp.md)** — TSP orchestration detail. 4 pipeline stages with message envelope schema, idempotency contract, consumer group semantics, and backpressure contract.

### 5. Detection and cases
- **[chirp.md](chirp.md)** — 37 detection rules across 10 categories. Every rule has: detection logic, SQL contract, threshold table, alert fields produced.
- **[alert.md](alert.md)** — Alert lifecycle state machine. REST endpoints for each transition.
- **[fox.md](fox.md)** — Case management. Evidence hash chain. Append-only invariant enforced by DB trigger (trigger DDL is included — deploy it).

### 6. Intelligence layer
- **[owl.md](owl.md)** — pgvector search, Risk Dictionary, EJ Spine entity resolution.
- **[analytics.md](analytics.md)** — Metric rollups, risk scoring, baseline computation. Scheduled job contracts.

### 7. POS adapters
- **[pos-adapter-substrate.md](pos-adapter-substrate.md)** — The adapter interface every POS integration must implement. Read this before hawk.md or bull.md.
- **[hawk.md](hawk.md)** — Square adapter. Reference implementation of the substrate. 8 tables.
- **[bull.md](bull.md)** — NCR Counterpoint adapter. Reference implementation. REST API key auth, polling model (no native webhooks).

### 8. Ecommerce channel
- **[ecom-channel.md](ecom-channel.md)** — Ecommerce channel integration service. Channel adapter pattern (Square Online V1; Shopify/WooCommerce V2+), order ingest into RaaS, catalog sync (Canary-master conflict resolution), subscription/autoship management, multi-tier fulfillment routing, and channel webhook processing. Port 9080. 7 MCP tools. Solex (`/Users/gclyle/GrowDirect/Solex/`) is the illustrative reference, not a literal port target.

### 9. Pipeline stages (TSP sub-modules)
- **[tsp-seal.md](tsp-seal.md)** — Stage 1: Hash & Seal — write-once evidence sealing with chain hashes. Patent-critical primitives.
- **[tsp-parse.md](tsp-parse.md)** — Stage 2: Parse & Route — CRDM record creation, source-agnostic routing.
- **[tsp-merkle.md](tsp-merkle.md)** — Stage 3: Merkle Batcher — batch accumulation and blockchain inscription.
- **[tsp-detect.md](tsp-detect.md)** — Stage 4: Chirp Detection — real-time exception engine.

### 10. Resolution and identity backbone
- **[raas.md](raas.md)** — Resolution as a Service — namespace resolution, chain hash primitive. Port 8099. 9 MCP tools.
- **[external-identities.md](external-identities.md)** — POS-native ID → Canary canonical ID mapping.

### 11. Inventory, item, and pricing spine
- **[item.md](item.md)** — Item master, per-store assortment metadata. Port 8090.
- **[inventory-as-a-service.md](inventory-as-a-service.md)** — Real-time inventory position engine, multi-tier assortment model. Port 9081.
- **[receiving.md](receiving.md)** — Dock control + inventory intake. Port 8092.
- **[returns.md](returns.md)** — Return processing + fraud detection. Port 8097.
- **[pricing.md](pricing.md)** — Price history + promotion engine. Port 8094.
- **[three-way-match.md](three-way-match.md)** — PO-receipt-invoice reconciliation.

### 12. Cost, finance, and accountability
- **[ildwac.md](ildwac.md)** — Provenance-weighted cost model on Bitcoin standard (patent #63/991,596). Port 9082.
- **[l402-otb.md](l402-otb.md)** — L402-gated open-to-buy budget enforcement. Port 9090.
- **[commercial.md](commercial.md)** — Vendor relationship layer — finance, rebates, chargebacks. Port 9089.
- **[blockchain-anchor.md](blockchain-anchor.md)** — Bitcoin L2 hash anchoring (patent #63/991,596). Port 9086.

### 13. Devices, presence, and ops surface
- **[device-contracts.md](device-contracts.md)** — Smart contract enforcement for cost/profit-center devices. Port 9083.
- **[store-brain.md](store-brain.md)** — In-store AI context manager — presence resolution, session governance. Port 9085.
- **[ops-dashboard.md](ops-dashboard.md)** — Store NOC interface, device health + MCP observability. Port 9084.
- **[field-capture.md](field-capture.md)** — Semantic field mapping, pgvector-backed registry. Port 9087.

### 14. Multi-store integrity and case management
- **[store-network-integrity.md](store-network-integrity.md)** — Multi-store cross-location anomaly detection. Port 9088.
- **[hawk-case-management.md](hawk-case-management.md)** — LP case lifecycle, incident types, wizard FSM, compliance.

### 15. Test data and architectural proofs
- **[retail-lifecycle-test-data.md](retail-lifecycle-test-data.md)** — Integration testing dataset spanning the full retail lifecycle.
- **[multi-pos-architecture-proof.md](multi-pos-architecture-proof.md)** — Empirical demonstration of POS-agnostic design.

---

## Key invariants — never violate these

1. **UUID primary keys everywhere.** `gen_random_uuid()` default. Never use POS-native IDs as Canary row identifiers.
2. **Schema-qualified writes.** Every INSERT is `app.table`, `sales.table`, or `metrics.table`. Never unqualified.
3. **Tenant isolation.** Every query that touches merchant data has `WHERE merchant_id = $1`. Row-level security is a backstop, not a substitute.
4. **Append-only evidence.** `fox.evidence_records` has a DB trigger that blocks UPDATE and DELETE. Do not remove it.
5. **Idempotent pipeline.** Every TSP stage is idempotent. Duplicate events produce the same result, not duplicate rows.
6. **pgvector in-database.** Vectors live in PostgreSQL via pgvector. No external vector database.
7. **REST throughout.** No gRPC. Internal service calls are REST, same pattern as external POS API calls.
8. **Agent-driven by default.** No workflow should require a human operator in the loop unless legally mandated. Alerts surface to humans; investigations are driven by agents. HIL is an escalation path, not the default path.

---

## Spec additions

Sections marked `[SPEC ADDITION — not in prototype]` were added during curation to fill gaps the prototype left implicit. These are authoritative — implement them.

Key additions:
- `tsp.md` / `webhook-pipeline.md` — message envelope schema, idempotency key strategy, backpressure contract, consumer group semantics
- `pos-adapter-substrate.md` — rate limit/retry contract, dead-letter contract, poll watermark table
- `chirp.md` — SQL contracts for all 37 rules (derived from prototype code, not just the SDD)

---

### 9. Agent layer

- **[agent-contracts.md](agent-contracts.md)** — Agent smart contract schema and four reference contracts (alert triage, Fox investigation, analytics baseline, Service Introduction gate). Read before implementing any agent-driven workflow.

---

## v1.1 additions (GRO-668 — 2026-04-28)

Six conceptual layers were woven into the corpus in this pass:

| Layer | Primary SDDs updated |
|---|---|
| ILDWAC — IL(Device/MCP/Port/)WAC on Bitcoin standard | platform-overview, data-model, analytics, fox, chirp, pos-adapter-substrate, hawk, bull, go-module-layout |
| RaaS interface contract | external-identities, microservice-architecture, architecture, multi-pos-architecture-proof, webhook-pipeline, owl |
| Agent nodes and MCP ethos | platform-overview, architecture, microservice-architecture, alert, fox, go-module-layout |
| Process decomp → Go subsystem mapping | microservice-architecture, chirp, webhook-pipeline, tsp, pos-adapter-substrate, hawk, bull |
| Agent smart contracts | fox, alert, tsp, architecture, agent-contracts (new) |
| Granularity enabled by technology | platform-overview, analytics, webhook-pipeline, fox |

**New SDD:** `agent-contracts.md` — agent smart contract schema and reference implementations.

**pgvector seed status:** This corpus seeds `growdirect_memory` after this commit. Every `##` section is chunk-ready — self-contained, specific header, table-preferred.

---

## What is NOT in this corpus

These are intentionally excluded — internal tooling that requires its own SDD before the relevant Go service can be built:

| Excluded service | Status | Blocks |
|---|---|---|
| Agent memory system (ALX) | Rebuild separately | M3+ agent workflows |
| Namespace resolution (RaaS) | `raas-go.md` needed — Python SDD exists at `docs/sdds/canary/raas.md` | GRO-639, GRO-642 |
| Payment middleware (Goose / L402) | `goose-go.md` needed | Phase 4+ L402-gated spend |
| ILDWAC stock ledger engine | `ildwac-go.md` needed — architectural direction only | Phase 4 Module V/F cost model |
| elJeffe Bitcoin anchor | `eljeffe-anchor-go.md` needed | Phase 5+ evidentiary rail |
| Agent topology catalog | `agent-topology.md` needed | All agent-driven sessions |
| Ops console | Rebuild separately | M6 ops handoff |
| Python prototype infra | Not applicable | — |
