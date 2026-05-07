---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis
status: handoff-ready
updated: 2026-04-29
binary: blockchain-anchor
port: 9086
mcp-server: canary-anchor
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
patent: "Application #63/991,596"
---

# Blockchain Anchor

**Type:** Infrastructure Service — External Verifiability Layer  
**Binary:** `cmd/blockchain-anchor` → `:9086`  
**MCP server:** `canary-anchor` (6 tools)  
**Depends on:** `raas` (chain hash events), `ildwac` (RIB batch seals)  
**Feeds:** nothing — terminal consumer

Blockchain Anchor is a non-blocking consumer of RaaS chain hashes and ILDWAC RIB batch seals. It publishes them to an L2 blockchain (Bitcoin via OP_RETURN, or a compatible L2), making the tamper-evidence chain externally verifiable. Any party with a chain hash can verify against a public blockchain without trusting Canary's servers.

**Status: opt-in architectural direction.** Blockchain anchoring is one of several optional features per `platform-overview.md` "Optional Features" — gated by `BLOCKCHAIN_ANCHOR_ENABLED` env flag (default `false`). When the flag is off, the internal SHA-256 hash chain operates normally, receipts are still hash-verified internally, and merchant chain integrity holds — only the public L2 anchoring queue is dormant. The internal chain is the required core (per `raas.md`); the public anchor is the optional extension that converts internal verifiability into externally verifiable evidence.

**Chain-of-record split.** Two chains, two purposes (resolves the open question from the Wave 2 dispatch):
- **Public evidence anchor** (this SDD) — Base or Polygon PoS for decentralization, externally verifiable, sub-cent inscription cost
- **Vendor smart contracts** (per `agent-contracts.md`) — AVAX private subnet for vendor-only contracts, low-cost EVM, private to the vendor relationship

Both gated by their respective env flags. Neither is required for platform operation.

**Multi-tenant context.** Anchor receipt tables (`anchor_receipts`, `anchor_queue`) live per-tenant in `tenant_{merchant_id}` — every merchant's chain hashes are anchored under their own tenant scope. The on-chain inscription itself is public, but it contains only the chain root hash — no merchant-identifiable content. See `architecture.md` "Multi-Tenant Isolation".

---

## Critical Platform Rule

**Blockchain Anchor is never in the critical path.**

It is a consumer, not a dependency. `raas.append_event` completes successfully whether or not the anchor service is reachable. `feature.blockchain_anchor_enabled` defaults to `false`. Disabling or losing this service has zero impact on loss prevention operations — it only defers external verifiability.

This rule is structural, not advisory. No service in the Canary platform may take a synchronous dependency on `blockchain-anchor`. All calls to this service are fire-and-forget.

---

## Business

### The External Verifiability Problem

RaaS maintains a hash-chained event log where each event's hash incorporates the previous event's hash. This makes the log tamper-evident: any modification to a historical record breaks the chain from that point forward. The chain is verifiable — but only by trusting Canary's servers.

For high-stakes scenarios (insurance claims, legal proceedings, regulatory audits), "trust Canary's servers" is insufficient. A party adverse to the merchant — or to Canary — has no independent anchor. Blockchain Anchor solves this by publishing Merkle roots of hash batches to a public blockchain where no party, including Canary, can alter the record.

### What Gets Anchored

Two event classes flow into the anchor pipeline:

| Source | Description | Frequency |
|---|---|---|
| RaaS chain events | Hash of each append-only receipt chain entry | Per transaction |
| ILDWAC RIB batch seals | Merkle root of each RIB reconciliation batch | Per settlement cycle |

Neither raw transaction data nor PII enters the blockchain. Only cryptographic hashes — the Merkle root of a batch — are published.

---

## Architecture

### Event-Driven Consumer Pattern

Blockchain Anchor subscribes to two event types:

1. **RaaS chain events** — emitted when new sequence numbers are assigned in `raas.receipt_chains`
2. **ILDWAC RIB batch completions** — emitted when `rib_batches.status` transitions to `sealed`

Subscription is via a Valkey pub/sub channel. The anchor service accumulates incoming hashes in memory and flushes them as a batch to the blockchain on either of two conditions:

- **Count trigger:** 100 hashes accumulated (default, configurable)
- **Time trigger:** 30 seconds elapsed since batch started (default, configurable)

Whichever fires first initiates the flush. The service computes a Merkle root over the batch and submits one L2 transaction.

### Batch → Merkle → Anchor

```
incoming hashes [h1, h2, ..., hN]
         ↓
   Merkle tree construction
         ↓
      merkle_root
         ↓
   L2 tx submission (OP_RETURN payload = merkle_root)
         ↓
   anchor_batches.l2_tx_id populated on confirmation
```

Each hash in the batch is stored in `anchor_records` with its position in the tree — enabling later Merkle proof generation for any individual hash.

---

## Data Model

All tables in the `app` schema.

```sql
CREATE TABLE app.anchor_batches (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID,
    -- null for cross-merchant batches (time-triggered mixed batches)
    batch_type    TEXT        NOT NULL,
    -- 'raas_chain' | 'rib_batch' | 'mixed'
    hash_count    INT         NOT NULL,
    merkle_root   TEXT        NOT NULL,
    -- Merkle root of all hashes in this batch
    l2_tx_id      TEXT,
    -- null until confirmed on L2
    l2_network    TEXT        NOT NULL DEFAULT 'bitcoin_mainnet',
    -- 'bitcoin_mainnet' | 'bitcoin_testnet' | 'mock'
    submitted_at  TIMESTAMPTZ,
    confirmed_at  TIMESTAMPTZ,
    status        TEXT        NOT NULL DEFAULT 'pending',
    -- 'pending' | 'submitted' | 'confirmed' | 'failed'
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_anchor_batches_status      ON app.anchor_batches (status);
CREATE INDEX idx_anchor_batches_merchant    ON app.anchor_batches (merchant_id, created_at DESC)
    WHERE merchant_id IS NOT NULL;

CREATE TABLE app.anchor_records (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id         UUID NOT NULL REFERENCES app.anchor_batches(id),
    source_type      TEXT NOT NULL,
    -- 'raas_event' | 'rib_batch' | 'device_contract_event'
    source_id        TEXT NOT NULL,
    -- raas sequence number, rib_batch.id, etc.
    hash_value       TEXT NOT NULL,
    position_in_batch INT NOT NULL
);

CREATE INDEX idx_anchor_records_batch       ON app.anchor_records (batch_id, position_in_batch);
CREATE INDEX idx_anchor_records_source      ON app.anchor_records (source_type, source_id);
```

---

## L2 Strategy

### Trade-off Analysis

| Option | Cost per anchor | Confirmation latency | Security | Queryability |
|---|---|---|---|---|
| Bitcoin OP_RETURN | $0.50–2.00 | ~10 min | L1 (strongest) | Minimal |
| Ordinals inscription | $2–20 | ~10 min | L1 (strongest) | Rich |
| Stacks (BTC L2) | $0.001–0.01 | ~10 sec | L2 (strong) | Moderate |
| Rootstock (BTC merge-mine) | $0.001–0.01 | ~30 sec | L2 (strong) | Moderate |
| Mock (dev/test) | $0 | Immediate | None | Full |

### V1 Recommendation: Bitcoin OP_RETURN

Publish a single L1 transaction per batch carrying the 32-byte Merkle root of up to 100 chain hashes in the `OP_RETURN` output. At current fee rates:

- Cost per batch: ~$0.50–2.00
- Cost per event anchored (100-hash batch): ~$0.01–0.02
- Confirmation latency: ~10 minutes (1 block)
- Security: highest possible — L1 Bitcoin finality

The 80-byte OP_RETURN limit comfortably holds a 32-byte Merkle root plus a 4-byte batch ID prefix for lookup. No inscription needed for V1.

### Upgrade Path

If confirmation latency becomes operationally significant (e.g., for real-time audit requirements), the Stacks or Rootstock adapters can be enabled without changing the data model. `l2_network` field accommodates multi-network routing.

### Network Configuration

`l2_network` is set per deployment:

| Environment | Default network |
|---|---|
| Production | `bitcoin_mainnet` |
| Staging | `bitcoin_testnet` |
| Dev / test | `mock` |

`mock` network records batches with a synthetic `l2_tx_id` and immediately sets `status = 'confirmed'`. No real transaction is submitted.

---

## MCP Tools (`canary-anchor` — `/blockchain-anchor/*`)

6 tools registered with the MCP tool registry.

| Tool | Required Params | Description |
|---|---|---|
| `submit_anchor_batch` | `hash_values[]`, `source_type`, `merchant_id` (optional) | Manually trigger a batch submission. Normally called by the internal flush loop, not by agents directly. |
| `get_anchor_status` | `batch_id` | Current status of a batch: pending / submitted / confirmed / failed. Includes `l2_tx_id` and `confirmed_at` once confirmed. |
| `verify_anchor` | `batch_id` | Fetch the batch's Merkle root, look up the L2 transaction, and confirm the OP_RETURN payload matches. Returns `{verified: bool, l2_tx_id, block_height, block_time}`. |
| `get_anchor_records` | `source_id` | Return all `anchor_records` rows for a given `source_id`. Provides `batch_id`, `merkle_root`, `l2_tx_id` — enough to construct a Merkle proof. |
| `get_pending_batches` | — | List all batches in `pending` or `submitted` state. Diagnostic tool for monitoring anchor lag. |
| `get_anchor_analytics` | `merchant_id` (optional), `window_days` | Summary: batches anchored, hashes anchored, average confirmation latency, estimated cost, error rate. |

---

## API Contract

### MCP Blueprint Endpoints

**Base path:** `/blockchain-anchor`

| Method | Path | Auth | Description |
|---|---|:---:|---|
| GET | `/blockchain-anchor/manifest` | No | Server manifest (name, version, tool count) |
| GET | `/blockchain-anchor/tools` | No | List all 6 tools with schemas |
| POST | `/blockchain-anchor/tools/<name>` | JWT | Invoke a tool by name |
| GET | `/blockchain-anchor/health` | No | Service health check |

**Health response:**
```json
{
  "service": "canary-anchor",
  "healthy": true,
  "tools": 6,
  "pending_batches": 0,
  "network": "bitcoin_mainnet",
  "feature_enabled": false
}
```

---

## Operations

### Feature Flag

`feature.blockchain_anchor_enabled` is checked at startup and at each flush cycle. When `false`:

- The service starts and registers MCP tools (status queries still work)
- The Valkey subscription is established but events are discarded without accumulation
- No L2 transactions are submitted
- `get_anchor_analytics` returns zero counts

This allows the service to be deployed dark and activated without a restart.

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| L2 node unreachable | Anchor submission fails | Batch marked `failed`; retried on next cycle (exponential backoff) |
| Valkey pub/sub disconnect | Events missed during gap | Gap recorded in log; no retroactive anchoring of missed events |
| PostgreSQL down | Batch records not persisted | In-memory accumulation continues; flush deferred until DB recoverable |
| Feature flag disabled | All anchoring suspended | Silent — no errors, no batches queued |
| Mock network (dev) | Synthetic confirmation | `l2_tx_id = "mock:<uuid>"`, immediate confirmation |

### Configuration

| Env Var | Default | Description |
|---|---|---|
| `ANCHOR_BATCH_SIZE` | 100 | Max hashes per batch before forced flush |
| `ANCHOR_BATCH_TTL_SECONDS` | 30 | Max seconds before time-triggered flush |
| `ANCHOR_L2_NETWORK` | `bitcoin_mainnet` | Target network (`bitcoin_mainnet` / `bitcoin_testnet` / `mock`) |
| `ANCHOR_L2_NODE_URL` | — | RPC endpoint for L2 submission |
| `ANCHOR_L2_SIGNING_KEY` | — | Private key for L2 transaction signing (loaded from secret manager in prod) |
| `FEATURE_BLOCKCHAIN_ANCHOR_ENABLED` | `false` | Master feature flag |

---

## Related SDDs

- **raas.md** — source of chain hash events; `receipt_chains.chain_hash` is what gets anchored
- **ildwac.md** — source of RIB batch seals; `rib_batches.merkle_root` feeds the anchor pipeline
- **platform-overview.md** — Evidentiary accountability rail (one of three platform rails)
- `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md` — ILDWAC provenance model and Bitcoin-standard cost attribution
