---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
patent: "Application #63/991,596 (hash-before-parse, chain hash, Merkle inscription)"
---

# TSP Stage 3 — Merkle Batcher

> **Type:** Pipeline Consumer Worker — Batch Accumulation and Blockchain Inscription
> **Parent SDD:** `tsp.md` (pipeline overview)
> **Consumer Group:** `sub3-merkle` on stream `canary:events`
> **Patent Scope:** Merkle inscription of batched event hashes — patent Application #63/991,596

---

## Governing Thesis

Stage 3 converts the stream of sealed event hashes into a compact, blockchain-verifiable commitment. The Merkle tree is the data structure that makes this economically viable: a single L2 transaction anchors up to 1,000 events, not 1. The root hash is the anchor; the per-event proof paths are the receipts. Any event can later be proven as a member of a specific batch with a O(log n) proof, without revealing any other event in the batch.

This is the evidentiary layer that Stage 1 makes possible. Stage 1 seals the event. Stage 3 publishes the proof to a public ledger. Together they complete the chain: hash → chain → Merkle → blockchain inscription.

---

## Business Hat

### Why This Stage Exists

A loss prevention record stored only on the retailer's server is vulnerable to the same insider threat it's meant to detect. The operator could argue the records were altered after the fact. The Merkle inscription closes that argument: the batch root hash is published to a public blockchain before any investigation begins. Retroactive alteration of the sealed records would produce a different Merkle root, falsifying the batch.

For the SMB retailer, the practical value is insurance claims and employee termination defense. A Bitcoin-anchored Merkle proof of a transaction record is admissible evidence that the record was not fabricated after the incident. That proof costs fractions of a cent per event at batch scale.

### Business Invariants

| Invariant | Why It Matters |
|-----------|---------------|
| Batch root inscribed before evidence is cited in a case | Inscription after allegation has much weaker evidentiary weight |
| Proof path stored per event | Any individual event can be verified without the full batch |
| Batch flush is non-blocking on inscription service | Inscription delays do not stall the pipeline |
| Batch accumulation is Valkey-backed | Accumulator survives worker restarts; in-flight IDs recovered via PEL |

### Trade-Off: Batch Size vs. Inscription Latency

The V1 default is 1,000 events OR 5 minutes, whichever comes first. The business interpretation:

- **Smaller batches** (e.g., 100 events / 1 minute): faster confirmation windows per event, higher inscription cost per event, more blockchain transactions.
- **Larger batches** (e.g., 5,000 events / 30 minutes): lower cost per event, longer window before any event in the batch is publicly anchored.

The 1,000 / 5-minute default is calibrated for a merchant doing ~200 transactions/hour. At that rate, batches flush on the time trigger more often than the count trigger during business hours. The count trigger fires during peak hours (lunch rush, weekend). Adjust `BATCH_COUNT_THRESHOLD` and `BATCH_TIME_THRESHOLD_SECONDS` per deployment.

---

## Technical Hat

### Consumer Loop

Stage 3 is a **single-worker** stage. The batch accumulator in Valkey is a sorted set — multiple workers would race on the flush trigger and produce duplicate inscriptions or corrupt the accumulator state.

1. Call `XREADGROUP GROUP sub3-merkle worker-1 COUNT 50 BLOCK 2000 STREAMS canary:events >`
2. For each message: extract `event_hash`, add to accumulator (step 3–4).
3. Track message ID in local pending set (do NOT ACK yet).
4. Check flush condition. If flush triggered: execute flush (see Flush Sequence).
5. Heartbeat write every iteration.

**PEL recovery at startup:** Read pending IDs from the PEL. For each pending message, re-extract `event_hash` and add to the accumulator. The sorted set is idempotent for repeated hashes (ZADD NX). After recovery, the accumulator reflects the correct pending state.

### Batch Accumulation

The Valkey sorted set key is `canary:batch:current`. Each event hash is stored with its Unix timestamp as the score:

```
ZADD canary:batch:current NX <unix_timestamp> <event_hash_hex>
```

The timestamp score enables the time-based flush trigger: compare the score of the lowest-scored member (oldest event in the batch) to now.

A secondary Valkey key `canary:batch:pending_ids` (a list or set) tracks the Valkey stream message IDs of all events in the current batch. These IDs are not ACKed until the flush succeeds.

### Flush Conditions

Either condition triggers a flush:

| Trigger | Condition | Config Variable |
|---------|-----------|----------------|
| Count | `ZCARD canary:batch:current >= BATCH_COUNT_THRESHOLD` | `BATCH_COUNT_THRESHOLD` (default 1000) |
| Time | `now() - min_score >= BATCH_TIME_THRESHOLD_SECONDS` | `BATCH_TIME_THRESHOLD_SECONDS` (default 300) |

The time threshold check runs on every iteration even when no new messages arrive (the XREADGROUP block timeout ensures the loop ticks at least every `STAGE3_BLOCK_MS` milliseconds).

### Flush Sequence

```
1. FETCH all entries from canary:batch:current via ZRANGE ... BYSCORE.
   → Sort by hex value ascending (deterministic leaf ordering).

2. FETCH all pending stream message IDs from canary:batch:pending_ids.

3. BUILD MERKLE TREE (see Merkle Tree Construction below).
   → Compute Merkle root.
   → Compute per-leaf proof paths.

4. GENERATE batch_id: ULID.

5. BEGIN PostgreSQL transaction:
   a. CHECK existence: SELECT 1 FROM inscription_pool WHERE batch_id = $1.
      → If exists (interrupted flush recovery): skip INSERT, proceed to step 6.
   b. INSERT inscription_pool record.
   c. INSERT event_inscriptions records (one per leaf, with proof path).
   COMMIT.

6. PUBLISH inscription request (non-blocking):
   → If MOCK_INSCRIPTION=true: generate deterministic fake txid and block.
   → If MOCK_INSCRIPTION=false: call OrdinalsBot API (async; result updates inscription_pool).
   → Inscription service unavailable: log warning, mark batch as pending_inscription.
     Batch IS written to DB regardless. Inscription is retried separately.

7. XACK all pending stream message IDs in canary:batch:pending_ids.

8. CLEAR accumulator:
   DEL canary:batch:current
   DEL canary:batch:pending_ids

9. EMIT metric: batch_flushed{count=N, trigger=count|time, batch_id=...}
```

**Step 5a (existence check) is critical.** If the worker crashes after the DB write but before ACKing the stream messages, the PEL recovery on restart will re-add the same event hashes to the accumulator. When the next flush triggers, step 5a detects the existing `batch_id` and skips the INSERT, but still ACKs the stream messages. This prevents duplicate inscription records.

### Merkle Tree Construction

Algorithm version 1. Deterministic. Stored in `inscription_pool.tree_algorithm_version`.

```go
func buildMerkleTree(leaves [][]byte) (root []byte, proofs []MerkleProof) {
    // 1. Sort leaves by hex value ascending (deterministic ordering)
    sort.Slice(leaves, func(i, j int) bool {
        return hex.EncodeToString(leaves[i]) < hex.EncodeToString(leaves[j])
    })

    // 2. Pad to nearest power of 2 by duplicating last leaf
    n := len(leaves)
    for !isPowerOfTwo(n) {
        leaves = append(leaves, leaves[len(leaves)-1])
        n++
    }

    // 3. Double-hash each leaf (second-preimage attack prevention)
    //    leafHash = SHA-256(SHA-256(rawLeaf))
    hashedLeaves := make([][]byte, n)
    for i, leaf := range leaves {
        h := sha256.Sum256(leaf)
        h2 := sha256.Sum256(h[:])
        hashedLeaves[i] = h2[:]
    }

    // 4. Build tree iteratively, hashing adjacent pairs
    //    parentHash = SHA-256(leftChild || rightChild)
    tree := buildLevels(hashedLeaves)

    // 5. Root is the single node at the top level
    root = tree[len(tree)-1][0]

    // 6. Compute per-leaf proof paths (sibling hashes at each level)
    proofs = computeProofPaths(tree, len(leaves))

    return root, proofs
}
```

**Proof path format** (stored in `event_inscriptions.merkle_proof_path`):

```json
{
  "leaf_hash": "<sha256-hex>",
  "leaf_index": 3,
  "depth": 4,
  "root": "<sha256-hex>",
  "siblings": ["<sha256-hex>", "<sha256-hex>", "<sha256-hex>", "<sha256-hex>"]
}
```

To verify: reconstruct the root by hashing the leaf with each sibling in order, alternating left/right based on leaf_index bit. If the reconstructed root matches the `root` field, inclusion is proven.

---

## Data Model

### `canary_sales.inscription_pool`

One row per Merkle batch. The unit of blockchain inscription.

```sql
CREATE TABLE canary_sales.inscription_pool (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id                TEXT NOT NULL UNIQUE,               -- ULID generated at flush
    merkle_root             BYTEA NOT NULL,
    tree_algorithm_version  INT NOT NULL DEFAULT 1,
    event_count             INT NOT NULL,
    inscription_id          TEXT,                               -- OrdinalsBot inscription ID (NULL until inscribed)
    bitcoin_txid            TEXT,                               -- Bitcoin txid (NULL until confirmed)
    bitcoin_block           BIGINT,                             -- Block height (NULL until confirmed)
    inscription_status      TEXT NOT NULL DEFAULT 'pending',    -- pending | inscribed | mocked | failed
    mock_inscription        BOOLEAN NOT NULL DEFAULT false,
    first_event_at          TIMESTAMPTZ,                        -- earliest event in batch
    last_event_at           TIMESTAMPTZ,                        -- latest event in batch
    flushed_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_inscription_pool_status ON canary_sales.inscription_pool (inscription_status);
CREATE INDEX idx_inscription_pool_flushed ON canary_sales.inscription_pool (flushed_at DESC);
```

### `canary_sales.event_inscriptions`

One row per event per batch. Stores the Merkle proof path for independent event verification.

```sql
CREATE TABLE canary_sales.event_inscriptions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id            TEXT NOT NULL REFERENCES canary_sales.inscription_pool(batch_id),
    event_id            TEXT NOT NULL,                          -- ULID from gateway
    merchant_id         TEXT NOT NULL,
    event_hash          BYTEA NOT NULL,                         -- SHA-256 from evidence_records
    leaf_index          INT NOT NULL,                           -- position in sorted leaf array
    merkle_proof_path   JSONB NOT NULL,                         -- sibling hashes + depth + root
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_event_inscription_batch_event UNIQUE (batch_id, event_id)
);

CREATE INDEX idx_event_inscriptions_event_id ON canary_sales.event_inscriptions (event_id);
CREATE INDEX idx_event_inscriptions_merchant ON canary_sales.event_inscriptions (merchant_id);
CREATE INDEX idx_event_inscriptions_hash ON canary_sales.event_inscriptions USING hash (event_hash);
```

---

## API Contract

Stage 3 does not expose HTTP endpoints. It is a pure stream consumer and DB writer.

The inscription status is surfaced through the receipt endpoints in the TSP service (see `tsp.md`):

**`GET /receipt/by-hash/{event_hash_hex}`** — returns `inscription_status` and `merkle_proof` fields from `event_inscriptions`, populated after a flush that includes this event.

**`GET /receipt/verify-merkle`** — given `event_hash` and `batch_id`, reconstructs the Merkle root from the stored proof path and compares to `inscription_pool.merkle_root`. Returns `{valid: true|false, root: "<hex>"}`.

### MCP Tool Surface

| Tool | Write | Description |
|------|:-----:|-------------|
| `verify_merkle` | No | Verify Merkle inclusion proof for a given event hash |
| `get_stream_health` | No | Includes `canary:batch:current` accumulator depth |

---

## SLA

| Metric | P50 | P99 | Hard Limit |
|--------|-----|-----|------------|
| Per-event accumulation latency (ZADD + track ID) | <1ms | 3ms | 20ms |
| Flush: Merkle tree construction (1000 leaves) | 5ms | 20ms | 100ms |
| Flush: DB transaction (INSERT pool + 1000 inscriptions) | 40ms | 150ms | 2000ms |
| Flush: inscription publish (non-blocking, mock) | <1ms | 5ms | 50ms |
| Flush: ACK 1000 stream message IDs | 10ms | 50ms | 500ms |
| Flush end-to-end | 60ms | 250ms | 3000ms |
| Batch confirmation window (time to inscription for any event) | 1–5 min | 5 min | — |

**Note:** The hard limit on flush end-to-end (3000ms) reflects that the Valkey advisory accumulator lock is held for the duration of a flush. During flush, new messages pile up in the PEL. The flush must complete before the next iteration resumes accumulation.

---

## Failure Modes

| Failure | Detection | Impact | Recovery |
|---------|-----------|--------|----------|
| Accumulator ZADD failure | Valkey error | No ACK; message in PEL | Retry on next iteration; accumulator is idempotent |
| DB transaction failure during flush | pgx error | No ACK on any pending IDs; accumulator intact | Auto-retry on next flush trigger |
| Flush interrupted (crash after DB write, before ACK) | PEL recovery on restart | Accumulator repopulated from PEL; step 5a detects existing batch_id | Skip INSERT, proceed to ACK |
| Inscription service unavailable | HTTP error or timeout | Batch written to DB with `inscription_status=pending` | Separate retry job polls `pending` batches and submits to inscription service |
| Merkle tree construction error | Algorithmic failure | No flush; no ACK | Log error, alert; this should never happen with valid input |
| Accumulator exceeds MAXLEN | `ZCARD canary:batch:current` growth | Flush triggers on count before time; normal behavior | Not a failure; indicates high throughput |
| Duplicate batch_id (extremely unlikely) | ULID collision | DB unique constraint violation on flush | ULID probability makes this negligible; handle as any flush failure |

---

## Compliance

### Patent Scope

Patent Application #63/991,596 covers Merkle inscription of batched event hashes as an evidentiary anchor. The specific claims cover:

1. Batching sealed event hashes (produced by the hash-before-parse primitive in Stage 1) into a Merkle tree.
2. Computing a Merkle root from those hashes using a deterministic algorithm.
3. Publishing that root to a public blockchain for tamper-evident timestamping.

Algorithm version 1 (double-hash leaves, sort by hex value, pad to power of 2) is the reference implementation. Any change to the Merkle construction algorithm must be versioned in `inscription_pool.tree_algorithm_version` and must not retroactively alter existing records.

### PII Handling

Stage 3 handles only `event_hash` values — SHA-256 digests that contain no PII. The `inscription_pool` and `event_inscriptions` tables contain no PII fields. The batch accumulator in Valkey (`canary:batch:current`) contains only hex-encoded SHA-256 hashes.

The `canary:events` stream message that Stage 3 reads contains `raw_payload` (which contains PII), but Stage 3 reads only the `event_hash` field. It does not process, store, or forward the `raw_payload`.

### Append-Only for Inscription Records

`inscription_pool` and `event_inscriptions` records are intended to be immutable once written. The `inscription_status` field in `inscription_pool` is the only mutable field — it transitions from `pending` to `inscribed` or `mocked` when the blockchain anchor is confirmed. No other fields in these tables should be updated after creation.

---

## Configuration

| Variable | Default | Stage 3 Usage |
|----------|---------|---------------|
| `STAGE3_BLOCK_MS` | `2000` | XREADGROUP blocking timeout (shorter = faster time-trigger check) |
| `VALKEY_STREAM` | `canary:events` | Source stream |
| `BATCH_COUNT_THRESHOLD` | `1000` | Flush on N accumulated events |
| `BATCH_TIME_THRESHOLD_SECONDS` | `300` | Flush on N seconds since first event in batch |
| `MOCK_INSCRIPTION` | `true` | Use deterministic fake Bitcoin values; set false when OrdinalsBot spend gate approved |
| `DATABASE_URL` | (required) | PostgreSQL connection |

---

## Open Items (Carry Forward to Go)

| # | Priority | Item |
|---|---------|------|
| P2-TSP-03 | P2 | Complete OrdinalsBot API integration (gated on spend approval from founder) |
| P1-TSP-05 | P1 | Structured JSON logging with `batch_id`, `event_count`, `stage` |
| — | P1 | Implement retry job for `inscription_status=pending` batches (separate process, not Stage 3 itself) |
| — | P2 | Expose `canary:batch:current` accumulator depth as a Prometheus metric for monitoring batch fill rate |

---

*Canary | GrowDirect LLC | Confidential*
