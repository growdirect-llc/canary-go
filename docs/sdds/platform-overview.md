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

# Canary Platform Overview

**Type:** Product Context (top-level product SDD)
**Last reviewed:** 2026-04-28

---

## Purpose

This SDD captures the product-level context for Canary: what it is, who it serves, how it connects to POS systems, the module roadmap, and the security/compliance posture. It is the narrative layer — the architecture SDD covers service topology; this document covers product and market positioning.

Use this document when you need to understand **why** Canary exists and **what** it delivers, not **how** the services are wired together.

---

## Product Definition

Canary is a POS-agnostic retail operations platform for small and mid-size specialty retailers. It delivers loss prevention, case management, and store analytics to independent merchants at a price and operational footprint appropriate for one to thirty locations.

**Core value proposition:** The SMB retailer gets enterprise-grade detection, investigation, and compliance documentation — running on the POS they already have — with near-zero operational overhead. No dedicated LP staff. No manual report review. The platform runs the detection cycle, surfaces findings, and drives investigation workflows autonomously. The merchant reviews outcomes, not process.

**Design principle — agent-driven, minimal HIL:** Every workflow is designed to run without a human operator in the loop unless legally required. Canary's agents handle detection, evidence packaging, case initiation, and compliance tracking. Human-in-the-loop is an escalation path for ambiguous or high-stakes decisions — not the default operating mode. This is the only way Canary is viable for SMB: the merchant can't afford an LP department, so the platform is the LP department.

**Data contract — ARTS-native:** Canary's canonical retail data model is built against the ARTS POSLOG standard. NCR Counterpoint is the reference implementation of that standard — full inventory, receiving, purchase orders, paycode structures, EJ spine, multi-store transfers. Square is a lightweight subset: its entire data surface maps cleanly into the ARTS model Canary already handles. Any POS that speaks ARTS or a subset of it connects as an adapter projection. Building to the full ARTS surface means Square compatibility comes for free; the reverse is not true.

**Target users:** Small and mid-size specialty retailers (1–31 locations). NCR Counterpoint merchants reached via Counterpoint VARs (Rapid POS and others — garden centers, gun, feed-tack, beverage, wine verticals). Square merchants via Marketplace (prototype live, Marketplace-certified). Owner-operators who manage everything from inventory to payroll, often from a single device.

**Distribution model:** VAR co-sell. Counterpoint VARs bring the merchant relationships and domain expertise; Canary provides the product. The VAR becomes the delivery partner for Canary's capabilities into their existing install base. This is not a direct-to-merchant cold-start — it is a channel model with established trust already in place on the merchant side.

---

## The Problem

Retail shrink costs U.S. retailers ~$112B/year. Enterprise retailers address this with dedicated LP departments and six-figure software budgets. The SMB merchant has two options: manually review transaction reports (doesn't scale) or pay for enterprise LP software built for 500-store chains (doesn't fit).

The most common preventable loss for Square merchants is refund fraud: employees processing refunds for transactions that never occurred, refunding to cards they control, or processing refunds outside business hours. Square's native reporting surfaces data but does not flag patterns, generate alerts, or create case records.

---

## Platform Modules

| Module | Function | Status | Description |
|--------|----------|--------|-------------|
| **Chirp** | Detection Engine | **MVP** | Real-time detection via 37 Square rules + 25+ Counterpoint rules across 12 families. Plain-language alerts with transaction evidence and employee attribution. See Chirp SDD. |
| **Fox** | Evidence Chain | **MVP** | INSERT-only evidence locker, SHA-256 hash-chained, append-only timeline. EBR class inside Hawk. See Fox SDD. |
| **Hawk** | Case Management | **Phase 1** | Incident-typed investigation workflow with wizard FSM, dual-track action codes (DE/PV), compliance obligations, card factory. Supersedes Fox flat case lifecycle. See Hawk SDD. |
| **Bull** | Distribution Intel | **Phase 3 stub** | Transfer-loss reconciliation + multi-store distribution recommendations. Gated on Module D. See Bull SDD. |
| **Goose** | Bitcoin & Lightning | **Phase 2** | BTCPay Server, Lightning Network payments, LNURL-auth passwordless login, sat-denominated billing. See Goose SDD. |
| **Owl** | Analytics Oracle | **Phase 2** | Total Retail Loss dashboard, industry benchmarks, AI-powered analysis via local LLM inference. Hawk card corpus as Phase 4 recall surface. See Owl SDD. |

---

## Square Integration

Three API surfaces:

| API | Purpose | Detail |
|-----|---------|--------|
| **OAuth 2.0** | Authorization | Single-click install. Scopes: MERCHANT_PROFILE_READ, PAYMENTS_READ, PAYMENTS_WRITE, ITEMS_READ, CUSTOMERS_READ, ORDERS_READ. See Identity-Square SDD. |
| **Payments API** | Transaction Ingestion | Date-range filtered fetch. Extracts card fingerprint, employee ID, location, amount. |
| **Webhooks API** | Real-Time Processing | payment.created, payment.updated, refund.created, order.created. HMAC-SHA256 verification on every event. See Webhook Pipeline SDD. |

**Security posture:**
- HMAC-SHA256 webhook signature verification on every inbound event
- AES-256-GCM encrypted access/refresh tokens in merchant sources table
- No Square credentials logged or in error responses
- HTTPS enforced on all production routes

---

## Merchant Experience

Zero-configuration: connect Square, receive alerts.

1. Merchant finds Canary in Square App Marketplace → clicks Install
2. Square OAuth authorization → merchant approves permissions
3. Canary ingests past 30 days of transaction data
4. First dashboard with Chirp alerts already generated
5. **Time from discovery to first alert: under 5 minutes**

Dashboard shows active alerts sorted by severity. Each alert: triggering transaction, detection rule, employee ID, amount. Merchants acknowledge, investigate, or resolve without leaving Canary.

HIGH and CRITICAL alerts trigger SMS notification within 30 seconds via Twilio.

---

## Security and Compliance

| Control | Status | Detail |
|---------|--------|--------|
| OAuth 2.0 Authorization | Implemented | Square OAuth, CSRF state parameter |
| HMAC-SHA256 Webhook Verification | Implemented | Every webhook verified before processing |
| TLS / HTTPS Enforcement | Production | Let's Encrypt, HTTP→HTTPS redirect |
| Encrypted Credential Storage | Implemented | AES-256-GCM encrypted tokens |
| Row-Level Security | Implemented | Database-level RLS, cross-tenant prevention |
| Immutable Audit Log | Implemented | INSERT-only with SHA-256 hash chain |
| No PII Beyond Square Policy | Verified | Only permitted fields stored |
| Startup Config Validation | Implemented | Fails fast on missing env vars |
| Credential Scan | CI/CD Gate | CI pipeline confirms no keys in code/history |
| Privacy Policy | Pre-launch | Required for Marketplace submission |
| Terms of Service | Pre-launch | Required for Marketplace submission |

---

## Data Integrity Principle

Canary analyzes people's livelihoods. Every accusation must be supported by verifiable facts.

- Evidence tables are INSERT-only at the database level — enforced by triggers, not application convention
- No record can be modified or deleted once written, by any actor, including administrators
- SHA-256 hash chain provides cryptographic proof of record integrity
- Three-schema separation (`app`, `sales`, `fox`) isolates operational, transactional, and evidentiary data

See Data Model SDD for schema details.

---

## Roadmap

| Phase | Focus | Deliverables |
|-------|-------|-------------|
| **Phase 1 — Go Foundation** | Core pipeline + Counterpoint-first | TSP ingestion (Counterpoint poll + Square webhook), Chirp detection engine, Fox evidence chain, Hawk case management, merchant dashboard, multi-tenant auth |
| **Phase 2 — Agent Layer** | Autonomous operations | ALX MCP server, agent-driven investigation workflows, Owl analytics oracle, automated Hawk card generation |
| **Phase 3 — Distribution Intel** | Bull + multi-store | Bull distribution analytics, multi-store reconciliation, VAR multi-tenant management surface |
| **Phase 4 — Ecosystem** | Vertical depth + payments | Vertical rule expansions (gun, feed-tack, beverage, wine), Goose payment layer, Chain of Custody Ordinals |

---

## Open Source

Canary will be released as open source. Transparency is the brand; expertise and execution are the moat. Licensing strategy under review with legal counsel.

---

## Granularity Enabled by Technology

Canary can see things legacy LP tools cannot. The combination of pgvector, RIB-batched cost accounting, ILDWAC satoshi precision, and the merkle evidence chain produces resolution of detail that is structurally impossible with conventional retail analytics.

| Technology layer | What it enables |
|---|---|
| **pgvector semantic search** | Owl can retrieve past case facts, detection patterns, and policy precedents by meaning — not keyword match. An LP investigator asking "suspicious refunds at closing time" retrieves all structurally similar historical patterns, not just exact-string matches. |
| **RIB batch domain organization** | Cost events carry their domain origin (T, V, M, D). A WAC recalculation knows whether an adjustment came from a receiving event, a transfer, or a sale — not just that it happened. Domain attribution is embedded in the cost, not appended as metadata. |
| **ILDWAC satoshi precision** | Weighted average cost is denominated in satoshis — no fiat rounding, no period-end currency conversion, no embedded exchange rate risk. The cost model operates at sub-cent precision. When provenance dimensions (Device, MCP, Port) are added, the cost carries its own audit trail as dimensions, not as attached notes. See the ILDWAC section below. |
| **Merkle evidence chain** | SHA-256 sealed batches mean any tampering with the cost or event record is cryptographically detectable. The evidence chain is not an audit log you can edit — it is a mathematical proof of what happened and when. |

The product differentiator is not any single capability. It is the stack: every layer is precise, every record is sealed, and the system can answer questions about provenance that no conventional LP platform can even form.

Legacy LP tools aggregate. Canary traces.

---

## Optional Features — Architectural Direction

The following features are **opt-in architectural direction**, not platform-required. Schema and SDD design exist; runtime behavior is gated by environment flags. The platform operates correctly with **all of these disabled** — every module's core function works without any Bitcoin / Lightning / blockchain / smart-contract dependency.

| Feature | Env flag | Default | What's affected when off |
|---|---|---|---|
| **L402 enforcement** (Lightning settlement gates on paid tool calls) | `L402_ENABLED` | `false` | Agent MCP calls authenticate via platform JWT only; OTB tracked but not Lightning-settled |
| **L402 OTB hard-enforcement** (PO blocking when wallet exhausted) | `feature.l402_enforcement_enabled` (per-merchant setting) | `false` (tracking-only) | OTB drift surfaces as alerts; never blocks PO creation |
| **ILDWAC five-dimension cost model** (Item × Location × Device × MCP × Port × WAC) | `ILDWAC_ENABLED` | `false` | Standard ILWAC (Item × Location × WAC) runs; provenance dimensions are not captured at cost calculation |
| **Satoshi denomination at accounting layer** | `BITCOIN_STANDARD_ENABLED` | `false` | Fiat MAC is the canonical accounting unit; satoshi remains parallel substrate (not the source of truth) |
| **Blockchain evidence anchoring** (publishing chain hashes to a public L2) | `BLOCKCHAIN_ANCHOR_ENABLED` | `false` | Internal SHA-256 hash chain still operates; public anchoring queue is dormant; merchant receipts remain hash-verified internally |
| **Vendor smart contracts** (private subnet vendor agreements) | `VENDOR_CONTRACTS_ENABLED` | `false` | Vendor compliance enforced via standard chargeback workflow; clauses live in DB, not on-chain |

**The closed-loop economy degrades gracefully.** With all flags off:

```
SHA-256 seals receipt → receipt records the event → RaaS owns the namespace
```

That's the required loop. L402 and the blockchain anchor are extensions to it, enabled per merchant when both the merchant and the regulatory environment are ready. The SDDs below describe the schema and the optional runtime behavior; the runtime is opt-in.

**Schema stays in either mode.** Tables for `otb_wallets`, `otb_transactions`, `ildwac_packets`, `blockchain_anchor_receipts` exist in the data model regardless of flag state. Disabling a feature stops the writes; it does not drop the tables. This means a merchant can opt in later without a schema migration.

---

## ILDWAC — Extended Cost Model (Architectural Direction)

> **Status: architectural direction, not yet implemented.** No current code declares a dependency on this model. A formal design pass will produce GRO tickets before implementation begins.

Standard ILWAC (Item × Location × Weighted Average Cost) is the retail industry baseline. Canary's architectural direction extends this to IL(Device/MCP/Port/)WAC — adding three provenance dimensions — and denominates the calculation in satoshis.

### Extended dimension schema

| Dimension | Standard ILWAC | Extended ILDWAC |
|---|---|---|
| **Item** | SKU | SKU — unchanged |
| **Location** | Store / warehouse | Store / warehouse — unchanged |
| **Device** | (absent) | Terminal or mobile device that processed the originating event |
| **MCP** | (absent) | MCP tool call that authorized the action — which agent, which server, which tool |
| **Port** | (absent) | POS connector: Square, Counterpoint, Lightspeed, or any registered source |
| **WAC** | Fiat currency | Weighted average cost denominated in satoshis |

### Satoshi standard

All cost accounting at the system level runs in satoshis. Fiat amounts displayed in the UI are computed at the presentation layer using the exchange rate at event time — the same pattern used in every Bitcoin-standard accounting system. This matters because:

- L402 Lightning payments are already denominated in satoshis. The payment loop and the cost loop close in the same unit — no currency conversion at the system level.
- OTB wallets are Lightning wallet balances. Overspending is mathematically impossible, not merely prohibited by policy.
- COGS postings to Module F run in satoshis. Fiat equivalents are a display transformation, not an accounting transformation.

### RIB batch inputs

Cost recalculation is batch-driven, not event-driven. Each domain module (T, V, M, D, etc.) produces structured JSON RIB (Retail Inventory Batch) messages for its inventory adjustment events. Each batch is SHA-256 sealed before it reaches the WAC engine. The domain origin is preserved as an attribute — a receiving event from Module M carries different provenance than a transfer from Module D or a sale from Module T.

### Cost center cross-charge

Each endpoint dimension (Device, MCP tool call, Port connector) carries a fee denominated in satoshis, settled immediately via L402 at the moment the event occurs. The cost center is an L402 wallet — its balance is the real-time P&L position for that cost center, with no period close required.

| Endpoint dimension | Fee mechanism | Settlement |
|---|---|---|
| Device | Per-call or per-transaction terminal fee | L402 → cost center wallet, immediate |
| MCP | Per-agent-action compute cost | L402 → cost center wallet, immediate |
| Port | Per-event connector license cost | L402 → cost center wallet, immediate |

### What does not exist yet

The Device, MCP, and Port dimensions are not yet added to the WAC calculation. Satoshi denomination at the accounting level is not yet implemented (fiat is current, satoshi is a parallel substrate). The unified ILDWAC recalculation engine has not been built. This section records the architectural direction before implementation begins.

**Related:** `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md` — full founder intent note · `docs/sdds/canary/goose.md` — L402 payment middleware

---

## Agent Network and MCP Ethos

Canary Go is operated by an autonomous agent network. Understanding this architecture is prerequisite to understanding how the platform runs without a dedicated LP staff.

**Three layers:**

| Layer | Description |
|---|---|
| **Controller** | Single agent with full network view. Founder interface. Sequences Service Introduction gates. Escalation terminus for all domain agents. |
| **27 Domain (L3) PMO Agents** | One per subsystem. Dual authority: business domain knowledge + technical module ownership. Each carries SDD, Go service contract, sqlc queries, data model, and upstream/downstream API surface. |
| **Infrastructure Agents** | Cross-cutting: DBA, Security, Data Governance, Legal & Compliance, Accountant, CPA, Network, Scheduling, MCP fabric. |

**MCP is the crossover between the event bus and technology.** Every agent exposes its context and capabilities as MCP tools. The MCP infrastructure agent routes context between agents and across sessions. An agent's memory is not a prompt — it is a seeded pgvector document, callable at any session via `memory_recall()`.

**Service Introduction** is the only Human-in-the-Loop gate. All other lifecycle phases (Spec → Build → VAR Delivery → Hardening → Support) are agent-owned. The founder signs off at Service Introduction; this is the moment the platform partner formally accepts operational ownership.

Full topology, node inventory, and lifecycle model: `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md`

---

## Related SDDs

- **Architecture** — Service mesh, startup order, dependencies
- **Chirp** — Detection rule catalog, 3 tiers
- **Fox** — Case management, evidence chain
- **Owl** — AI analytics, LLM inference
- **Goose** — Treasury, Bitcoin/L402
- **Identity-Square** — Square OAuth, token storage
- **Webhook Pipeline** — Ingestion, HMAC validation
- **Data Model** — 60+ models, PII map
- **Agent PMO Architecture** — `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md`
- **RaaS** — Namespace resolution, merchant onboarding, source registration
