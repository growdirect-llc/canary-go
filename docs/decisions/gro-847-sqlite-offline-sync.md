# GRO-847 SQLite-on-Device Offline + Sync Layer — Assessment

**Date:** 2026-05-07
**Scope:** Whether to introduce on-device SQLite plus a server-sync layer for canary.go's mobile / POS clients to support continued operation during connectivity loss.
**Status:** Assessment — not yet decided.
**Linked filing:** [GRO-847](https://linear.app/growdirect/issue/GRO-847) — Architecture: SQLite-on-device offline + sync layer for POS.

---

## Background

POS terminals running canary.go's mobile surface need to keep functioning during connectivity loss — checkouts, transactions, item lookups — and reconcile with the server when connectivity returns. The canonical mobile-first POS pattern (Square POS, Shopify POS, Lightspeed) uses on-device SQLite as a write-through cache plus a sync layer that replays queued writes when reconnected.

Canary.go currently assumes server connectivity for every read and write. Connectivity loss is a hard failure mode for retail customers; this is not negotiable in the long term.

## Proposed pattern

| Layer | Role |
|---|---|
| On-device SQLite | Write-through cache for reads; queue for writes during connectivity loss; local-only state for in-progress transactions. |
| Sync layer | Replays queued writes on reconnect; resolves conflicts per data class; observes server state for cache invalidation. |
| Server (canary.go) | Authoritative state. Accepts sync writes with idempotency keys. Surfaces the server-side projection back to the device. |
| Conflict resolution | Per-data-class policy (see below). |

## Per-data-class conflict resolution

| Data class | Policy | Rationale |
|---|---|---|
| POS transactions | Append-only ledger. Replay on reconnect with idempotency keys. | Transactions are immutable once authored on the device; the server accepts them as-recorded. |
| Customer records | Last-writer-wins with conflict detection on the merge field set, OR vector clocks if the team can absorb the complexity. | Mutable. Concurrent edits across devices are rare but possible. |
| Inventory positions | Server-authoritative on read; device-authoritative on the local register's commits; reconcile on sync via movement-event replay (canary.go already models inventory as movements per `internal/inventory/`). | The movement model is naturally append-only; reconciling movements from a device is the same shape as reconciling from another server source. |
| Item catalog | Read-only from the device; cache invalidates on server push or TTL. | Canonical reference data. The device never authors. |
| Employee, role, store config | Read-only from the device; cache invalidates on push or TTL. | Same as item catalog. |
| Payment intent | Queue intent + show pending status + process on reconnect. **Never offline-finalize a payment.** | Regulatory: chargeback rules, network policies, EMV requirements all require online authorization for card-present payments. |

## Tooling shortlist

| Tool | Model | Pros | Cons |
|---|---|---|---|
| PowerSync | Postgres-native sync; reads existing schema; policy-driven sync rules. | Fits canary.go's Postgres-first stack. SQL-based sync rules. Open core. | Newer ecosystem; smaller community than Realm. |
| ElectricSQL | Postgres-native sync; CRDTs under the hood. | Strong conflict-resolution story (CRDTs). Postgres-native. | Migration ergonomics during rapid schema change. |
| Realm (Atlas Device SDK) | Mongo-native sync; offline-first object database. | Mature ecosystem, strong client SDKs, well-documented offline patterns. | Mongo-shaped data model; mismatch with canary.go's Postgres relational schema. |
| WatermelonDB | Hand-rolled sync layer over SQLite; React Native focused. | Lightweight; full control. | All sync logic is custom; team owns conflict resolution and schema migration. |
| Replicache | Operational transformation; key-value oriented. | Strong consistency story. | Key-value model is awkward for relational data. |

Recommended pick: **PowerSync or ElectricSQL** — both are Postgres-native and avoid the schema-translation cost of Mongo-shaped or KV-shaped tools. Decision between the two depends on the conflict-resolution model preferred (PowerSync's SQL-based sync rules vs ElectricSQL's CRDTs).

## Cost / risk surface

| Concern | Detail |
|---|---|
| Engineering investment | Sync layer is the hard part. Conflict resolution per data class. Schema-migration coordination across server + device. Estimated 1–2 engineers, 12–16 weeks for first production-ready surface. |
| Payment-offline regulatory | Payment processing offline carries chargeback exposure and network-policy violations. Standard mitigation: never offline-finalize. UX implication: cashier sees "pending" status; transaction completes when reconnected. |
| Schema migration across devices | Devices may run older schema versions. Sync layer must handle backward compatibility. |
| Conflict-resolution UX | When conflicts surface (e.g., customer record edited offline + edited on the server), the cashier needs a UX path to resolve. Not invisible. |
| Storage and battery on device | SQLite is lightweight; sync layer's network and CPU cost is the bigger concern on mobile hardware. |
| Test surface | Offline + sync requires explicit test coverage for connectivity loss, sync replay, conflict resolution, schema-migration paths. |

## Decision options

- **Adopt with PowerSync.** Pilot the POS register surface on PowerSync. Estimated 12–16 weeks. Operational cost: PowerSync runtime (managed or self-hosted).
- **Adopt with ElectricSQL.** Same pilot scope. CRDT-based conflict resolution.
- **Pilot only.** 4-week PoC on a single read-only surface (e.g., item catalog) to evaluate sync layer ergonomics before committing.
- **Decline.** Accept connectivity-required posture. Position offline support as a future-roadmap item.
- **Defer.** Re-assess after the mobile surface itself is stable in production.

## Cross-references

- `docs/conventions.md` §"HTTP handler conventions" — current API shape assumes online; pagination and tenant scope conventions carry forward to the sync API.
- `internal/inventory/` — movement-based modeling is naturally sync-friendly; movements replay cleanly.
- `internal/auth/` — JWT issuance must support long-lived offline tokens or refresh-on-reconnect; current token-lifetime policy (per `IDENTITY_JWKS_CACHE_TTL_SECONDS` env var) needs review.

## Recommended next step

4-week PoC on PowerSync, scoped to the POS register's item-catalog read surface (read-only data class first; lowest conflict-resolution complexity). Measure: sync-layer ergonomics, schema-migration friction, cashier UX during connectivity loss. Decision on production adoption after PoC.

Payment-offline policy is decided ahead of the PoC: queue intent, show pending, process on reconnect, never offline-finalize. This is regulatory and not a tooling choice.
