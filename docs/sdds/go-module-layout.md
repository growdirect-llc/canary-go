---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: active-build-spec
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Go Module Layout & Service Conventions

> Read this before creating any file in `GrowDirect-RapidPOS`. This document defines the repo structure, module name, package naming conventions, and service boundaries. Every engineer must follow this layout — consistency across 19 services is not optional.

---

## Module Name

```
module github.com/growdirect-llc/rapidpos
```

Single `go.mod` at repo root. Monorepo — all services share one module. No workspace files, no multi-module complexity at this stage.

---

## Repo Layout

```
GrowDirect-RapidPOS/
├── go.mod
├── go.sum
├── CLAUDE.md
│
├── cmd/                        # One binary per service — each has its own main.go
│   ├── tsp/        main.go     # :8080  Transaction Stream Processor
│   ├── chirp/      main.go     # :8081  Detection Engine
│   ├── hawk/       main.go     # :8082  Case Management
│   ├── fox/        main.go     # :8083  Evidence Chain
│   ├── owl/        main.go     # :8084  Analytics Oracle
│   ├── bull/       main.go     # :8085  Distribution Intelligence
│   ├── identity/   main.go     # :8086  Auth / Tenant / Session
│   ├── alert/      main.go     # :8087  Alert Lifecycle
│   ├── analytics/  main.go     # :8088  Metric Rollups / Risk Scoring
│   ├── asset/      main.go     # :8089  Asset Management
│   ├── item/       main.go     # :8090  Item & Catalog Management
│   ├── inventory/  main.go     # :8091  Inventory / Stock
│   ├── receiving/  main.go     # :8092  Purchase Orders / Receiving / Vendors
│   ├── transfer/   main.go     # :8093  Inter-Store Transfers / Distribution
│   ├── pricing/    main.go     # :8094  Price Rules / Promotions / Markdowns
│   ├── employee/   main.go     # :8095  Employee Records / Roles / Schedules
│   ├── customer/   main.go     # :8096  Customer Data / Loyalty
│   ├── returns/    main.go     # :8097  Return Management / Refund Authorization
│   ├── report/     main.go     # :8098  Standard Reports / Scheduled Exports
│   ├── raas/       main.go     # :8099  Resolution as a Service — namespace + chain hash backbone
│   │
│   ├── ecom-channel/             main.go     # :9080  Ecommerce channel adapter (Square Online, Shopify V2)
│   ├── inventory-as-a-service/   main.go     # :9081  Real-time inventory position engine (IaaS)
│   ├── ildwac/                   main.go     # :9082  Provenance-weighted cost model (Item × Location × Device × MCP × Port × WAC)
│   ├── device-contracts/         main.go     # :9083  Smart contract enforcement for cost/profit-center devices
│   ├── ops-dashboard/            main.go     # :9084  Store NOC interface, real-time device health + MCP observability
│   ├── store-brain/              main.go     # :9085  In-store AI context manager — presence resolution, session governance
│   ├── blockchain-anchor/        main.go     # :9086  Bitcoin L2 hash anchoring — external verifiability layer
│   ├── field-capture/            main.go     # :9087  Semantic field mapping — pgvector-backed registry
│   ├── store-network-integrity/  main.go     # :9088  Multi-store cross-location anomaly detection
│   ├── commercial/               main.go     # :9089  Vendor relationship layer — finance, rebates, chargebacks
│   ├── l402-otb/                 main.go     # :9090  L402-gated open-to-buy budget enforcement
│   ├── compliance/               main.go     # :9091  Item authorization × regulatory zone × operational blocks
│   │
│   └── edge/       main.go     # (no port) Counterpoint poller — deploys on-prem
│
├── internal/                   # Private to this module — not importable externally
│   ├── crdm/                   # Canonical Retail Data Model types (ARTS-native)
│   ├── arts/                   # ARTS POSLOG field constants and mapping helpers
│   ├── auth/                   # JWT middleware, RBAC, session handling
│   ├── db/                     # sqlc generated code + connection pool
│   ├── tenant/                 # Tenant isolation middleware, RLS enforcement
│   ├── config/                 # Tenant branding config (whitelabel), env loading
│   ├── pagination/             # Cursor-based pagination helpers
│   └── testutil/               # Test fixtures, factories, DB helpers
│
└── deploy/
    ├── cloud-run/              # GCP Cloud Run service configs
    ├── edge/                   # Edge agent deployment (Windows Service wrapper)
    └── migrations/             # golang-migrate SQL files, one dir per service
```

---

## Service Port Map

| Port | Service | Domain |
|------|---------|--------|
| 8080 | tsp | Transaction ingestion — Counterpoint poll + Square webhook |
| 8081 | chirp | Detection engine — rule evaluation, alert generation |
| 8082 | hawk | Case management — incident types, wizard FSM, compliance |
| 8083 | fox | Evidence chain — INSERT-only, hash-chained, append-only |
| 8084 | owl | Analytics oracle — pgvector search, EJ spine, risk dictionary |
| 8085 | bull | Distribution intelligence — transfer-loss reconciliation |
| 8086 | identity | Auth, tenant model, session, merchant onboarding |
| 8087 | alert | Alert lifecycle state machine |
| 8088 | analytics | Metric rollups, risk scoring, baseline computation |
| 8089 | asset | Asset registry, lifecycle, location binding |
| 8090 | item | Item master, catalog, UPC, pricing tiers |
| 8091 | inventory | Stock levels, adjustments, cycle counts |
| 8092 | receiving | Purchase orders, receiving, vendor management |
| 8093 | transfer | Inter-store transfers, distribution |
| 8094 | pricing | Price rules, promotions, markdowns |
| 8095 | employee | Employee records, roles, schedules |
| 8096 | customer | Customer data, loyalty, purchase history |
| 8097 | returns | Return management, refund authorization |
| 8098 | report | Standard retail reports, scheduled exports |
| 8099 | raas | Resolution as a Service — namespace resolution, chain hash primitive |
| —    | edge | Counterpoint poller — on-prem, no inbound port |

### Extended Service Block (post-spine)

The 9080–9099 block is reserved for services that extend the original 13-module spine. These are SDD'd separately and run on the same Cloud Run topology. SDDs live in `docs/sdds/go-handoff/`.

| Port | Service | Domain |
|------|---------|--------|
| 9080 | ecom-channel | Ecommerce channel adapter (Square Online V1; Shopify, WooCommerce V2+) |
| 9081 | inventory-as-a-service | Real-time inventory position engine — superset of the spine `inventory` slot |
| 9082 | ildwac | Provenance-weighted cost model on Bitcoin standard (patent #63/991,596) |
| 9083 | device-contracts | Smart contract enforcement — cost/profit-center device SLAs |
| 9084 | ops-dashboard | Store NOC interface — device health + MCP observability (REST + SSE) |
| 9085 | store-brain | In-store AI context manager — presence resolution, session governance |
| 9086 | blockchain-anchor | Bitcoin L2 hash anchoring — external verifiability layer (patent #63/991,596) |
| 9087 | field-capture | Semantic field mapping — pgvector-backed schema registry |
| 9088 | store-network-integrity | Multi-store cross-location anomaly detection |
| 9089 | commercial | Vendor relationship layer — finance, rebates, chargebacks, deductions |
| 9090 | l402-otb | L402-gated open-to-buy budget enforcement |
| 9091 | compliance | Item authorization × regulatory zone × operational blocks |

> **Reconciliation note (2026-04-29):** The extended-block services were initially drafted with port assignments overlapping the 8080–8098 spine. The 9080+ block is the canonical assignment. SDD frontmatter and inline binary references have been aligned to this table.

---

## Package Naming Convention

Each service owns its domain package under `internal/`:

```
internal/crdm/          → github.com/growdirect-llc/rapidpos/internal/crdm
internal/auth/          → github.com/growdirect-llc/rapidpos/internal/auth
internal/db/            → github.com/growdirect-llc/rapidpos/internal/db
```

Service-specific business logic lives alongside `main.go` in `cmd/<service>/`:

```
cmd/chirp/
├── main.go
├── rules.go        # Detection rule implementations
├── evaluator.go    # Rule evaluation engine
└── rules_test.go
```

No service imports another service's `cmd/` package. Cross-service communication is REST only — never in-process function calls between service domains.

---

## CRDM Package

`internal/crdm/` contains Go struct definitions for every entity in the Canonical Retail Data Model. These are ARTS POSLOG-aligned. All services use these types — never define competing struct definitions in service packages.

```go
// internal/crdm/transaction.go
package crdm

type TransactionHeader struct {
    ID              uuid.UUID
    MerchantID      uuid.UUID
    ARTSBusinessDate time.Time
    ARTSWorkstationID string
    // ... ARTS POSLOG fields
}
```

---

## Database Conventions

- Every table is schema-qualified: `app.`, `sales.`, `metrics.`
- Every query has `WHERE merchant_id = $1` (tenant isolation)
- sqlc generates all query code from SQL — no hand-written query strings in service code
- Migrations live in `deploy/migrations/<service>/` — one directory per service, numbered sequentially
- UUID primary keys everywhere: `gen_random_uuid()` default in PostgreSQL

---

## Configuration

All config via environment variables. No config files. Each service reads its own env subset at startup and fails fast on missing required vars. Tenant branding config (whitelabel) is loaded from `internal/config/` at request time, not at startup — it is per-tenant, not per-deployment.

---

## Edge Agent

`cmd/edge/` is the Counterpoint poller. It runs as a Windows Service on the merchant's back-office server alongside Counterpoint + SQL Server. It:
- Polls the Counterpoint REST API at configurable intervals
- Processes raw POS data locally (detection rule pre-screening)
- Emits only derived intelligence packets to GCP (never raw transaction data)
- Has no inbound port — outbound only

The edge agent is a separate deployment target from the Cloud Run services. It is compiled as a Windows binary and distributed by the VAR during installation.

---

## Agent Lifecycle and Module Boundaries

Each `cmd/<service>/` binary corresponds to a domain PMO agent in the Canary Go agent network. The agent owns the service across its full lifecycle: Spec → Build → VAR Delivery → Hardening → Service Introduction → Support.

**Service Introduction** is the only Human-in-the-Loop gate. It is the moment the platform partner formally accepts operational ownership of that module. Before SI: the domain agent holds context. After SI: the ops team holds operational ownership; the domain agent shifts to support mode.

### Milestone-to-binary mapping

| Milestone | Binaries | PMO Agent(s) |
|---|---|---|
| M1 — Foundation | `identity`, CRDM package, Multi-POS substrate | Identity & Auth agent, CRDM agent |
| M2 — Detection Core | `tsp`, `chirp`, `fox` | TSP agent, Chirp agent, Fox/Hawk agent |
| M3 — Intelligence Layer | `owl`, `analytics` | Owl agent, Analytics agent |
| M4 — Module Spine | `hawk`, `bull`, `asset`, `item`, `inventory`, `receiving`, `transfer`, `pricing`, `employee`, `customer`, `returns`, `report` | 13 spine agents (T, R, N, A, Q, C, D, F, J, S, P, L, W) |
| M5 — VAR Delivery | `edge`, Bull NCR connector | Bull agent, Edge agent |
| M6 — Hardening / SI | All services | Controller + all domain agents |

Service Introduction gates fire in dependency order: Foundation first, Detection Core second, then spine modules in the dependency sequence (N before T, T before Q, etc.). The ops team receives modules as they clear — not a monolithic handoff.

Full lifecycle model and gate criteria: `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md`

---

## Agent Smart Contract Pattern

Every `cmd/<service>/` boundary is an agent-to-module smart contract. The pattern applies uniformly:

| Contract element | Where it lives |
|---|---|
| **Inputs** | REST API contract documented in `microservice-architecture.md` per service |
| **Outputs** | Table writes documented in database ownership table; REST calls to downstream services |
| **SLA** | Baselined during Hardening phase; accepted at Service Introduction |
| **Escalation path** | Domain agent → Controller → founder (Human-in-the-Loop) |

Full contract definitions will be written in `agent-contracts.md` (forthcoming). This layout document establishes the structural correspondence: one binary = one agent contract.

---

## CRDM Package — Source and Cost Dimensions

The `internal/crdm/` package is the single source of truth for canonical retail data types. Two fields that affect multiple future layers must be present on the `TransactionHeader` from the start:

| Field | Type | Why it matters |
|---|---|---|
| `SourceCode` | `string` | Multi-POS source attribution (Square, Counterpoint, etc.). This is the Port dimension in the ILDWAC model. Required for compound dispatch keys and source-aware Chirp rule confidence tiers. |
| `DeviceID` | `string` (ARTS WorkstationID) | Device attribution. This is the Device dimension in the ILDWAC model. Required for per-terminal cost accounting when ILDWAC is implemented. |

Both fields should be populated by the POS adapter (Hawk for Square, Bull/Edge for Counterpoint) before the CRDM record is written to `sales.transactions`. Leaving them null eliminates the provenance trail that ILDWAC and forensic investigation both require.

> **ILDWAC status: architectural direction, not yet implemented.** Including these fields now is a low-cost design decision that avoids a schema migration and a data backfill later.

---

## RaaS — Not in This Repo

RaaS (Resolution as a Service) is excluded from the `GrowDirect-RapidPOS` monorepo. It will be rebuilt as a separate service. However, every binary in `cmd/` that resolves a merchant namespace or constructs a Valkey key must call RaaS via REST — not build its own key construction logic.

**Key construction rule:** Use `raas.build_key(merchant_id, parts...)`. The pattern is `raas:{merchant_id}:{domain}:{key}`. No service constructs keys independently.

**Namespace resolution rule:** Use `raas.resolve_namespace(merchant_id)` to confirm active sources before any cross-POS query. `raas.ensure_namespace(merchant_id)` for pure string construction when no DB check is needed.

**Full interface contract:** `docs/sdds/canary/raas.md` — 7 MCP tools, JWT-gated, REST internally.

---

## Related SDDs

- **microservice-architecture.md** — inter-service call graph, REST API per service, process→service mapping
- **platform-overview.md** — product context, ARTS-native data contract, VAR distribution model, ILDWAC direction
- **data-model.md** — all 82+ tables, schema contracts, sqlc query targets
- **pos-adapter-substrate.md** — adapter interface all POS connectors implement
- **Agent PMO Architecture** — `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md`
- **RaaS SDD** — `docs/sdds/canary/raas.md`
