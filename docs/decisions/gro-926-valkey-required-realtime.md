# GRO-926 Valkey Required for Real-Time Operational Capabilities

**Date:** 2026-05-09
**Decided:** 2026-05-09 — **Keep.** Valkey is required infrastructure.
**Scope:** Whether Valkey can be removed or made per-tier optional in the canary.go deployment topology.
**Status:** Closed — keep.
**Linked filing:** [GRO-926](https://linear.app/growdirect/issue/GRO-926).
**Paired with:** [`gro-846-neo4j-mdm-adjunct.md`](gro-846-neo4j-mdm-adjunct.md) — the second of two infrastructure-cost evaluations from the 2026-05-09 architecture review. Neo4j out (zero usage today, high cluster cost, no patent-claim role); Valkey in (deeply embedded, low cost, enables the differentiated real-time substrate).

---

## Decision

**Keep Valkey.** Required infrastructure for canary.go's real-time operational capabilities. Not optional, not per-tier configurable at this point in the platform lifecycle.

## Why required

Valkey delivers six distinct real-time capabilities across the codebase. Removing it requires replacing each:

1. **Protocol pipeline event bus** (`cmd/sub1-hash-seal` → `cmd/sub2-parse-route` → `cmd/sub3-merkle-ordinal` + `internal/protocol/publisher/`). Redis Streams `XAdd` / `XRead` / `XGroup` consumer-group semantics. The patent-claim audit-anchoring infrastructure runs on this. Replacement options (PG `LISTEN/NOTIFY` + cursor tables, NATS, GCP Pub/Sub) all carry their own ops cost or architectural complexity, none of them free.
2. **Inventory replenishment trigger** (`internal/replenishment/trigger.go` + `cmd/bull`). `inventory:replenish` stream feeds Min/Max replenishment task creation. Event-driven SOH change → replenishment task in the same heartbeat.
3. **Webhook backpressure + idempotency** (`internal/webhook/backpressure.go`, `internal/webhook/idempotency.go`). In-flight tracking and idempotency keys with TTL-cleanup-for-free that Postgres can match with manual cron but loses the operational simplicity.
4. **Reference-tier TTL cache** (`internal/web/middleware/refcache/`). Sub-100ms cache reads for hot configuration data. In-process LRU works per-binary but loses cross-binary cache hits.
5. **Change-feed + live SSE updates** (`internal/web/middleware/changefeed/`). Powers live operator dashboards and real-time notifications.
6. **Identity state cache** (`cmd/identity`). Refresh-token + per-org SSO state cache.

## Cost reality

| Deployment shape | Monthly cost (small/dev tier) |
|---|---|
| Self-hosted on existing growdirect_valkey container | ~zero marginal (shared infra) |
| Managed (GCP Memorystore Basic 1GB) | $30-50/month |
| Comparison: Neo4j Aura (declined) | $300+/month minimum (3-node HA cluster) |

Valkey is the cheap-and-load-bearing infrastructure piece. Cost is small relative to the differentiated value (real-time audit pipeline + live operator surfaces). Removing it would save ~$30-50/month and impose multi-week engineering cost plus performance regression on cache hit rate and stream throughput.

## What this rules in

- Valkey stays in the canonical canary.go deployment topology (Postgres 17 + Valkey 8 per `CLAUDE.md`).
- New features that benefit from real-time semantics (live SSE, event-driven workflows, TTL caching, idempotency tracking) reach for Valkey first.
- The protocol pipeline assumes Valkey Streams. Future protocol features (sub3 anchor confirmation polling, dead-letter retry scheduling, additional consumer groups) build on the same substrate.

## What this rules out

- "Valkey-free SMB tier" as a near-term product configuration. If the request emerges later, treat as a separate assessment with explicit scope (which features degrade, what the alternative substrate is, what the per-customer deployment shape becomes).
- Reflexive infrastructure-simplification proposals that target Valkey for removal without addressing its six distinct uses.
- Replacing Valkey Streams with PG `LISTEN/NOTIFY` + cursor tables for the protocol pipeline absent a specific scaling-floor justification.

## Re-evaluation triggers

Reconsider only if **at least one** of:

1. A specific deployment tier (e.g., on-premise enterprise customer with infra constraints) genuinely cannot run Valkey **and** the deployment shape justifies the engineering work to abstract the substrate.
2. Valkey monthly cost at production scale becomes a meaningful percentage of revenue (rough threshold: > 5% of recurring infra cost).
3. A Valkey-equivalent emerges in the GCP-native stack at substantially lower TCO with consumer-group semantics matching Redis Streams.

None are current.

## Decision pairing — context

This decision is the second half of the 2026-05-09 infrastructure review. The two decisions read together:

| Component | Status today | Patent-claim role | Cost | Decision |
|---|---|---|---|---|
| **Neo4j** | Zero usage (assessment only) | None | $300+/month minimum | Declined — see `gro-846` |
| **Valkey** | Six distinct uses | Yes (protocol pipeline) | $30-50/month or near-zero self-hosted | Keep — this doc |

The honest framing: simplification is a goal, but the right things to simplify are the unused or marginally-used dependencies (Neo4j), not the load-bearing real-time substrate (Valkey).

## Cross-references

- [`gro-846-neo4j-mdm-adjunct.md`](gro-846-neo4j-mdm-adjunct.md) — paired Neo4j decline.
- `CLAUDE.md` — repository operating rules; Valkey 8 dependency on `growdirect_valkey :6379` DB 2.
- `docs/conventions.md` — pgx/v5 + sqlc + Valkey 8 stack convention.
- `internal/protocol/sub1/`, `sub2/`, `sub3/`, `publisher/` — protocol pipeline using Valkey Streams.
- `internal/web/middleware/refcache/`, `changefeed/` — Valkey-backed real-time middleware.
- `internal/webhook/backpressure.go`, `idempotency.go` — Valkey-backed reliability layer.
- `internal/replenishment/trigger.go` — inventory event consumer feeding bull.
