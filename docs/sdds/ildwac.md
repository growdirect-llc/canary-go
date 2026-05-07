---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: ildwac
port: 9082
mcp-server: canary-ildwac
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# ILDWAC — Item × Location × Device × MCP × Port × Weighted Average Cost

**Type:** Infrastructure Service — Provenance-Weighted Cost Model  
**Binary:** `cmd/ildwac` → `:9082`  
**MCP server:** `canary-ildwac` (9 tools)  
**Depends on:** `inventory` (event source via `inventory_ledger`), `identity` (merchant/location existence), `raas` (chain hash primitive)  
**Feeds:** `l402-otb` (profit-center device WAC → OTB wallet), `hawk-case-management` (serial unit cost records for LP evidence), `tsp` (sales-driven WAC recalculation), `receiving` (receipt cost → initial WAC)

ILDWAC is the provenance-weighted cost model for the Canary platform. It extends the standard retail ILWAC (item × location WAC) with three additional provenance dimensions — device, MCP tool, and POS port — that make every cost record self-auditing. No reconstruction is needed to dispute a cost figure: the audit trail is encoded in the dimensions themselves. Every WAC entry knows not just what it costs, but where the stock sits, which tool moved it, and which POS system reported the event.

**Status: opt-in architectural direction.** ILDWAC is one of several optional features per `platform-overview.md` "Optional Features" — gated by `ILDWAC_ENABLED` env flag (default `false`). When the flag is off, the platform operates with standard ILWAC (item × location × WAC) using fiat denomination via the existing MAC infrastructure (per `retail-inventory-valuation-mac` and `data-model.md` MAC schema). The five-dimension provenance schema described below exists in the data model in either mode — tables are created at tenant onboarding and accept writes when the flag is on, remain empty otherwise. A merchant can opt in later without a schema migration.

**IP scope.** Patent application **#63/991,596** covers the algorithm that combines hash-chain anchoring, WAC computation with provenance weighting, and Bitcoin denomination for retail evidence. This SDD describes the implementation; patent scope is documented in Compliance below.

**Multi-tenant context.** ILDWAC tables (`ildwac_packets`, `rib_batches`, `endpoint_fees`, `ildwac_positions`, `ildwac_serial_units`) live per-tenant in `tenant_{merchant_id}`. Cost packets are merchant-scoped; cross-tenant cost analytics flow through scheduled rollups into the `analytics` schema, never via direct cross-tenant queries. See `architecture.md` "Multi-Tenant Isolation".

---

## Business

### The Cost Basis Problem

Standard retail WAC collapses cost into two dimensions: item and location. That is sufficient when stock is uniform — one bin, one system, one source of truth. It is insufficient when a single backroom holds bin stock (receiving), peg stock (floor-ready), and serialized high-value units (electronics, firearms, luxury goods), each with a distinct cost basis, each potentially sourced from a different POS connector or agent tool. Averaging across those positions destroys the cost signal precisely where it matters most: shrink detection, LP case evidence, and OTB margin accounting.

ILDWAC holds the line at five dimensions. An LP agent can query "what was the unit cost of item X on device Y at location Z when the shrink event was recorded via the Square connector" and get a deterministic answer backed by a sealed, hash-chained audit record. No reconstruction, no estimation, no "we think it was around."

### Bitcoin Standard

Canary is on a Bitcoin standard. WAC is computed and stored in satoshis; fiat display is a presentation-layer concern (`sats × fx_rate / 100_000_000`). This is not a cryptocurrency feature — it is a denominator choice. Integer satoshi arithmetic eliminates floating-point rounding in financial calculations and makes WAC figures unambiguous across currencies. A merchant operating in USD, CAD, and MXN shares one cost ledger in satoshis; the fx_rate snapshot converts to fiat at report time, and the satoshi figure is always the authoritative number.

### Business Rules

1. Every inventory-moving event that changes cost basis must produce an ILDWAC ledger entry. Read-only events (position queries, reserve/release) do not trigger recalculation.
2. WAC is recalculated per (item, location, device) at packet creation — immediately, in the same transaction as the INSERT. RIB batch processing is the settlement and chain-anchoring layer, not the WAC update trigger.
3. Serialized items (`device_type = serial`) use unit-level WAC — each physical unit has its own cost history. Qty is always 1. Cost does not average across units.
4. Profit-center devices contribute their WAC to OTB wallet accounting. Cost-center devices feed shrink reporting but do not participate in OTB.
5. RIB (Retail Inventory Batch) sealing: sealed packets are grouped by domain into batched JSON messages, SHA-256 sealed, then settled into the chain. WAC has already been updated by the time RIB runs. The seal hash is the audit anchor — same primitive as the EJ Spine hash chain.
6. WAC recalculation must be idempotent: replaying the same RIB batch must produce the same WAC result. The batch_seal is the idempotency key.

### Five Dimensions

| Dimension | What it captures | Example values |
|-----------|----------------|----------------|
| Item | SKU / canonical item ID | `item:uuid` |
| Location | Store or warehouse location | `loc:uuid` |
| Device | Stock-holding unit within the location | bin A3, peg 47, shelf B2, serial #XYZ |
| MCP | Agent tool that triggered the cost event | `record_adjustment`, `commit_reservation`, `append_event` |
| Port | POS connector source | `square`, `counterpoint`, `rapidpos`, `manual` |

### Device Type WAC Computation Method

| Device type | WAC method | Qty semantics |
|-------------|-----------|---------------|
| `bin` | Standard WAC: (prior_cost × prior_qty + new_cost × new_qty) / (prior_qty + new_qty) | Integer unit count |
| `shelf` | Standard WAC | Integer unit count |
| `peg` | Standard WAC | Integer unit count (per hook) |
| `barrel` | Standard WAC with count-based approximation | Float (weight or approximate count) |
| `chip` | Face-value tracking: cost = face_value_cents | Integer count; WAC = face value |
| `serial` | Unit-level: each unit has its own cost_basis_sats | Always 1; never averages |

---

## Technical

### Packet — The Atomic Cost Unit

A packet is the atomic cost event. Every inventory-moving event is a packet. The packet carries its own cost adjustment at the moment of creation — WAC is updated immediately, not at batch time.

**Packet structure:**

```go
type CostPacket struct {
    PacketID           uuid.UUID
    EventType          string       // "received" | "sold_instore" | "sold_online" | "returned" | "adjusted" | "shrink_writeoff"
    ItemID             uuid.UUID
    LocationID         uuid.UUID
    DeviceID           *uuid.UUID   // nil for location-level events
    MCPTool            string       // agent tool that triggered this packet
    POSPort            string       // "square" | "counterpoint" | "rapidpos" | "manual" | "sensor:{device_code}"
    QtyDelta           float64      // positive = received/returned, negative = sold/written-off
    CostSats           int64        // raw cost of this event (PO cost, transfer cost, etc.)
    CostAdjustmentSats int64        // delta to apply to WAC position (may differ from CostSats if SLA penalty applied)
    SLAStatus          string       // "met" | "degraded" | "breached" — device SLA at packet creation time
    SLAPenaltySats     int64        // non-zero only if SLAStatus = "breached"; charged to device cost center
    OccurredAt         time.Time
    PayloadHash        string       // SHA-256(canonical_json(packet without hash fields))
    ChainHash          string       // SHA-256(payload_hash + "|" + occurred_at + "|" + sequence + "|" + prior_hash)
    PriorHash          string
}
```

**Dual-side cost tracking for devices:**

Every device appears in the cost model on two sides simultaneously:

| Side | What it tracks | Who bears it |
|------|---------------|--------------|
| Cost center | Device operating cost (power, maintenance, calibration, SLA penalties) | The cost center that owns/operates the device |
| Profit center | Cost component contribution to WAC via packets this device produces | The profit center that depends on this device's output |

A receiving sensor is a cost device. Its cost center bears the operating cost. The profit center that depends on it — the location receiving inventory — bears the WAC adjustment from every packet the sensor produces. If the sensor works correctly, the cost adjustment is accurate. If the sensor breaches SLA (misses scans, produces bad reads), two things happen:
1. A SLA penalty cost packet is charged to the device's cost center
2. The affected inventory packets carry a `sla_status = "breached"` flag and a correction cost adjustment, traceable to the device failure

**Packet vs RIB:**

Packets are the cost adjustment unit — WAC updates immediately on packet creation. RIB is the settlement and chain-anchoring layer — it batches sealed packets into a chain entry for auditability and bulk verification. The distinction is the same as transaction clearing vs. settlement in banking: the cost moves on the packet; the anchor is settled in the RIB batch.

### RIB (Retail Inventory Batch) — The Event Pipeline

Events flow into WAC recalculation via RIB batching, not individual event processing. This matches the EJ Spine hash-chain sealing model and provides two guarantees: deterministic recalculation (replay produces the same result) and tamper evidence (any post-seal modification produces a different hash).

```
Device / POS / Agent generates inventory event
    │
    ▼
CostPacket created: item + location + device + mcp_tool + pos_port + qty_delta + cost_sats
    │   SLA status checked against device_contracts; penalty applied if breached
    ▼
INSERT ildwac_packets → WAC updated immediately in ildwac_positions (packet-level cost adjustment)
    │
    ▼  (async, within 60s window)
RIB aggregator: group unsettled packets by domain
    │   Domains: receiving | sales | returns | adjustments | transfers
    ▼
RIB batch: { domain, packet_ids[], batch_seal: SHA-256(canonical_json(packets[])) }
    │   ildwac_packets.rib_batch_id populated; rib_batches row inserted
    ▼
ildwac_chain: append chain entry linking this RIB batch (same hash algorithm as RaaS)
```

The RIB batch seal guarantees: given the same batch of packets in the same order, the audit record is deterministic and verifiable.

### Service Boundaries

ILDWAC owns four table groups. No other service writes to these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Packets | `ildwac_packets` | Atomic cost events; WAC updated on INSERT |
| Positions | `ildwac_positions` | Current WAC snapshot per (item, location, device) |
| Serial units | `ildwac_serial_units` | Unit-level cost history for serialized items |
| Batches | `rib_batches` | Sealed RIB settlement groups |
| Chain | `ildwac_chain` | Hash chain linking settled batches in sequence |

### Data Model

```sql
-- Current WAC positions (snapshot updated per-batch)
CREATE TABLE ildwac_positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    item_id         UUID NOT NULL,
    location_id     UUID NOT NULL,
    device_id       UUID REFERENCES inventory_devices(id),  -- null = location-level WAC
    wac_sats        BIGINT NOT NULL DEFAULT 0,  -- weighted average cost in satoshis
    qty_on_hand     NUMERIC(12,3) NOT NULL DEFAULT 0,  -- float for barrel/weight; int for others
    total_cost_sats BIGINT NOT NULL DEFAULT 0,  -- wac_sats * qty_on_hand
    last_receipt_sats BIGINT,                   -- cost of most recent receipt
    last_batch_id   UUID,                       -- RIB batch that last updated this position
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, item_id, location_id, COALESCE(device_id, '00000000-0000-0000-0000-000000000000'::UUID))
);

CREATE INDEX idx_ildwac_pos_merchant_item ON ildwac_positions(merchant_id, item_id);
CREATE INDEX idx_ildwac_pos_device ON ildwac_positions(device_id) WHERE device_id IS NOT NULL;

-- Serialized unit cost records (device_type = serial only)
CREATE TABLE ildwac_serial_units (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    item_id         UUID NOT NULL,
    device_id       UUID NOT NULL REFERENCES inventory_devices(id),
    serial_number   TEXT NOT NULL,
    cost_basis_sats BIGINT NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL,
    sold_at         TIMESTAMPTZ,
    sold_order_ref  TEXT,
    status          TEXT NOT NULL DEFAULT 'in_stock',  -- in_stock | sold | returned | written_off
    UNIQUE(merchant_id, serial_number)
);

-- RIB batches (the sealed event groups)
CREATE TABLE rib_batches (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    domain          TEXT NOT NULL,  -- receiving | sales | returns | adjustments | transfers
    event_count     INT NOT NULL,
    batch_seal      TEXT NOT NULL,  -- SHA-256 of canonical_json(events[])
    prior_batch_id  UUID REFERENCES rib_batches(id),
    processed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    wac_positions_updated INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_rib_batches_merchant_domain ON rib_batches(merchant_id, domain, processed_at);

-- ILDWAC chain (links batches in sequence — same primitive as RaaS chain)
CREATE TABLE ildwac_chain (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    sequence_num    BIGINT NOT NULL,
    rib_batch_id    UUID NOT NULL REFERENCES rib_batches(id),
    chain_hash      TEXT NOT NULL,  -- SHA-256(batch_seal + "|" + processed_at + "|" + sequence_num + "|" + prior_hash)
    prior_hash      TEXT NOT NULL DEFAULT 'GENESIS',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, sequence_num)
);

-- Individual cost packets (append-only; WAC updated on INSERT)
CREATE TABLE ildwac_packets (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL,
    item_id               UUID NOT NULL,
    location_id           UUID NOT NULL,
    device_id             UUID REFERENCES inventory_devices(id),
    event_type            TEXT NOT NULL,
    mcp_tool              TEXT,
    pos_port              TEXT,
    qty_delta             NUMERIC(12,3) NOT NULL,
    cost_sats             BIGINT NOT NULL DEFAULT 0,
    cost_adjustment_sats  BIGINT NOT NULL DEFAULT 0,
    sla_status            TEXT NOT NULL DEFAULT 'met',  -- met | degraded | breached
    sla_penalty_sats      BIGINT NOT NULL DEFAULT 0,
    rib_batch_id          UUID REFERENCES rib_batches(id),  -- populated when settled
    payload_hash          TEXT NOT NULL,
    chain_hash            TEXT NOT NULL,
    prior_hash            TEXT NOT NULL DEFAULT 'GENESIS',
    occurred_at           TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, payload_hash)  -- content-addressable dedup
);

CREATE INDEX idx_ildwac_packets_device ON ildwac_packets(device_id, occurred_at) WHERE device_id IS NOT NULL;
CREATE INDEX idx_ildwac_packets_sla ON ildwac_packets(sla_status, occurred_at) WHERE sla_status != 'met';
CREATE INDEX idx_ildwac_packets_unsettled ON ildwac_packets(merchant_id, occurred_at) WHERE rib_batch_id IS NULL;
```

### API Contract

All routes require JWT auth. Batch submission additionally requires `inventory_manager` or `admin` role.

```
POST /ildwac/packet                                                 → 201 (submit cost packet; WAC updated synchronously)
POST /ildwac/rib                                                    → 202 (settle packets into RIB batch + chain; async)
GET  /ildwac/wac/{merchant_id}/{item_id}                            → current WAC across all locations and devices
GET  /ildwac/wac/{merchant_id}/{item_id}/{location_id}              → location-level WAC
GET  /ildwac/wac/{merchant_id}/{item_id}/{location_id}/{device_id}  → device-level WAC
GET  /ildwac/serial/{serial_number}                                 → unit cost history for serialized item
GET  /ildwac/chain/{merchant_id}/verify                             → verify RIB chain integrity
GET  /ildwac/healthz                                                → shallow liveness
GET  /ildwac/readyz                                                 → DB + Valkey check
```

### MCP Tool Surface — `canary-ildwac` (9 tools)

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `get_wac` | merchant_id, item_id, location_id?, device_id? | {wac_sats, qty, total_cost_sats, last_updated} | Scope-aware: all → location → device |
| `get_serial_cost` | serial_number | {cost_basis_sats, received_at, status} | Serial-unit cost lookup |
| `submit_rib` | merchant_id, domain, events[] | {batch_id, batch_seal, event_count} | Queues batch for WAC recalc |
| `submit_packet` | item_id, location_id, device_id?, event_type, qty_delta, cost_sats, mcp_tool, pos_port | {packet_id, payload_hash, wac_updated_sats} | Atomic: inserts packet + updates WAC position in one transaction |
| `get_rib_status` | batch_id | {status, processed_at, positions_updated} | Batch processing state |
| `get_device_wac` | device_id | {device_type, wac_sats, qty, profit_center} | Device-level position + WAC |
| `list_devices` | location_id | [{device_id, device_type, device_code, category, wac_sats}] | All devices at a location |
| `verify_rib_chain` | merchant_id, from_seq, to_seq | {valid, checked, first_bad_seq} | Audit interface |
| `shrink_by_device` | location_id, date_from, date_to | [{device_id, shrink_sats, shrink_qty}] | LP shrink analysis at device level |

### Satoshi Arithmetic Rule

All monetary values in the ILDWAC system are in satoshis (integer). No floating-point money arithmetic. Fiat display:

```go
func SatsToFiat(sats int64, fxRateSatsPerFiat float64) float64 {
    return float64(sats) / fxRateSatsPerFiat
}
// fxRateSatsPerFiat for USD: 100_000_000 / btc_usd_price
// Store the fx rate snapshot with each batch for auditability
```

### Go Implementation Notes

- `submit_packet` is the atomic WAC update path: INSERT ildwac_packets + UPDATE ildwac_positions in a single transaction. The packet's `payload_hash` is the idempotency key — duplicate packet submissions with the same hash are accepted and return the existing packet_id (no reprocessing).
- `submit_rib` must compute `batch_seal = SHA-256(canonical_json(packets[]))` before settling. Canonical JSON: keys sorted, no whitespace, UTF-8. The seal is the RIB idempotency key — duplicate batch submissions with the same seal are accepted and return the existing batch_id (no reprocessing).
- WAC position updates within a batch are already applied (by prior packet submissions). RIB processes packets in `occurred_at` order for chain integrity only. Out-of-order packets within a batch are sorted before chain entry, not rejected.
- `ildwac_positions` UNIQUE constraint uses `COALESCE(device_id, '00000000-...')` to handle nullable device_id in a composable unique index. Postgres does not treat two NULL values as equal in UNIQUE constraints without this workaround.
- `ildwac_serial_units` rows are INSERT-only on receipt; status transitions (in_stock → sold, sold → returned, in_stock → written_off) are UPDATE-only. There is no DELETE path.
- The chain hash computation: `SHA-256(batch_seal + "|" + processed_at.RFC3339Nano + "|" + strconv.FormatInt(sequence_num, 10) + "|" + prior_hash)`. Use the same delimiter and formatting as `raas.md` — the primitives must be consistent across services.
- RIB batch processing is async: `POST /ildwac/rib` returns 202 with `batch_id`. The caller polls `get_rib_status` or listens for a webhook event. Do not block the HTTP response on WAC recalculation.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `get_wac` (location-level, Valkey hit) | <5ms | <20ms | 100ms | Alert + fallback to DB |
| `get_wac` (device-level) | <10ms | <50ms | 200ms | Alert |
| `get_serial_cost` | <10ms | <50ms | 200ms | Alert |
| `submit_rib` (batch intake) | <50ms | <200ms | 1s | Alert |
| RIB processing (async, per event) | <200ms | <1s | — | Alert if queue depth > 500 |
| `verify_rib_chain` (per 1000 entries) | <500ms | <2s | 10s | Alert |

### Health Endpoints

```
GET /ildwac/healthz

Shallow liveness — returns 200 if the process is up.

Response 200:
{ "status": "ok" }
```

```
GET /ildwac/readyz

Deep readiness — verifies DB connection pool and Valkey reachability.

Response 200:
{
  "status": "ok",
  "db_ok": true,
  "valkey_ok": true,
  "rib_queue_depth": 12,
  "chain_sequence": 8847
}

Response 503 if DB or Valkey unreachable.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| RIB queue backed up (> 500 events) | Alert fires. New `submit_rib` calls are accepted (202) but processing lag grows. WAC positions become stale. | Scale up the RIB processor goroutine pool (default: 4 workers). Investigate upstream event volume spike. |
| DB unreachable during WAC recalculation | RIB batch processing pauses. Batch status stays `processing`. No data loss — events are in the batch row. | Auto-recovery via pgx connection pool retry. Processing resumes when DB is reachable. |
| Duplicate RIB batch submitted | Detected by `batch_seal` lookup. Returns existing batch_id with 200 (not 202). No reprocessing. | No action required — idempotency is the designed behavior. |
| Chain hash mismatch detected | `verify_rib_chain` returns `{valid: false, first_bad_seq: N}`. WAC positions after sequence N are suspect. | Alert LP and finance. Halt new chain entries. DBA investigation required — this indicates either a bug in chain hash computation or tampering. |

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `ildwac:wac:{merchant_id}:{item_id}:{location_id}` | 300s | Location-level WAC cache |
| `ildwac:wac:{merchant_id}:{item_id}:{location_id}:{device_id}` | 300s | Device-level WAC cache |
| `ildwac:serial:{serial_number}` | 300s | Serial unit cost cache |
| `ildwac:rib:queue:{merchant_id}` | — | RIB batch processing queue (no TTL — durable until processed) |

### Monitoring

Alert on:

- Any `ildwac_chain` sequence gap (gap between consecutive `sequence_num` values for a merchant)
- `verify_rib_chain` returning `valid: false` in production
- RIB queue depth > 500 events for any merchant sustained > 2 minutes
- Any `ildwac_serial_units` row with `status = 'written_off'` and no corresponding LP case in `hawk_cases` (cross-service integrity check, run nightly)
- WAC recalculation P99 > 1s sustained for 5 minutes

---

## Compliance

### Immutability Invariants

`ildwac_chain` is an audit chain — same immutability invariant as `raas_events`. Enforce at the database level:

```sql
REVOKE UPDATE, DELETE ON ildwac_chain FROM canary_app;
```

`rib_batches` rows are INSERT-only for the `batch_seal` and `event_count` fields. The `processed_at` and `wac_positions_updated` fields are updated once on processing completion — no subsequent updates.

### LP Evidence — Serial Units

Serial unit records (`ildwac_serial_units`) are evidence for LP cases involving high-value serialized items. Cross-reference to `hawk-case-management.md`. These records are:

- Retained for the full 7-year financial record period regardless of LP case status
- Not deletable via the application code (`REVOKE DELETE ON ildwac_serial_units FROM canary_app`)
- Linked to LP cases via `hawk_case_evidence` when a unit is reported stolen, damaged, or written off

### Patent Scope

Patent application #63/991,596 covers the hash-before-parse, chain hash, and Merkle elements. The `ildwac_chain` hash computation is within scope. The five-dimension WAC model (IL(Device/MCP/Port/)WAC) is a covered claim. Do not modify the chain hash algorithm or the dimension encoding without legal review.

### WAC Fiat Conversion Auditability

WAC figures in satoshis must be accompanied by the `fx_rate_snapshot` used for any fiat conversion in financial reports. The satoshi figure is authoritative; the fiat figure is derived. Each `rib_batches` row stores the `fx_rate_snapshot` at processing time. Financial reports must reference the batch's `fx_rate_snapshot`, not a live rate.

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `ildwac_positions` | Current only | Operational snapshot; historical positions reconstructed from RIB batches |
| `ildwac_serial_units` | 7 years | Financial record retention + LP evidence |
| `rib_batches` | 7 years | Financial record retention (cost basis audit trail) |
| `ildwac_chain` | 7 years | Tamper-evidence record; same retention as `raas_events` |

---

## Related SDDs

- `inventory-as-a-service.md` — `inventory_ledger` is the event source; `device_id`, `mcp_tool`, `pos_port` dimensions on ledger rows are consumed by ILDWAC for five-dimension cost attribution
- `raas.md` — `ildwac_chain` uses the same hash chain algorithm; RIB batch seals are appended to the RaaS chain as `ildwac.rib.sealed` events
- `receiving.md` — receiving events are the primary cost-basis-setting events (PO cost × received qty = initial WAC); all receiving RIB batches flow through ILDWAC before cost is considered settled
- `tsp.md` — sales events trigger WAC recalculation for profit-center devices; COGS is derived from device-level WAC at time of sale
- `l402-otb.md` — profit-center device WAC feeds OTB wallet accounting; ILDWAC is the cost source for L402 open-to-buy balance calculations
- `hawk-case-management.md` — serial unit cost records are LP case evidence for high-value shrink; `ildwac_serial_units` rows are linked to `hawk_case_evidence` on writeoff
