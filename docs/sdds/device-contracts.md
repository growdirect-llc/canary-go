---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: device-contracts
port: 9083
mcp-server: canary-devices
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Device Contracts — Smart Contract Enforcement for Cost and Profit Center Devices

**Type:** Infrastructure Service — Device SLA Enforcement + Cost Allocation  
**Binary:** `cmd/device-contracts` → `:9083`  
**MCP server:** `canary-devices` (8 tools)  
**Depends on:** `inventory-as-a-service` (device registry), `identity` (merchant existence), `raas` (chain hash primitive), `l402-otb` (wallet balances)  
**Feeds:** `ildwac` (SLA status + penalty packets), `hawk-case-management` (breach events as LP evidence), `receiving` (sensor SLA affects receiving cost accuracy)

Device Contracts is the smart contract enforcement layer for all cost and profit center devices in the Canary platform. The governing insight: a device that fails silently is a hidden cost — it produces bad data, mis-receives inventory, and inflates shrink without leaving a traceable cause. Device Contracts makes the failure visible, attributable, and cost-bearing. Every device has a contract. Every breach has a penalty. Every penalty is a packet in the ILDWAC cost ledger.

**Status: opt-in architectural direction.** Device Contracts is one of several optional features per `platform-overview.md` "Optional Features" — gated by `ILDWAC_ENABLED` (cost packet generation) and optionally `VENDOR_CONTRACTS_ENABLED` (vendor-supplied device terms on the smart contract layer). The schema for `device_contracts`, `device_breach_events`, and `device_penalty_packets` exists at tenant onboarding; writes happen when the flags are on, remain empty when off. With both flags off, devices still register and emit health signals (per `ops-dashboard.md`), but no contract enforcement or penalty cost allocation occurs.

**Multi-tenant context.** Device Contracts tables live per-tenant in `tenant_{merchant_id}`. Each merchant's device fleet is isolated; cross-tenant device performance benchmarking (across the platform) flows through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

---

## Business

### What It Is

Every device — a POS terminal, a receiving sensor, a BOPIS hold station, a self-checkout kiosk, an automated inventory scanner — has a contract that defines:

1. What the device must do (SLA: throughput, accuracy, uptime)
2. What it costs to operate (operating cost, funded by the profit center that depends on it)
3. What happens when it fails (penalty cost allocation, breach event in the chain)
4. Whether it is a cost center (operating cost only) or a profit center (generates revenue, has its own OTB wallet)

Devices must be funded to operate. A profit center that depends on a receiving sensor must fund the sensor's operating costs. If the sensor is not funded, it cannot perform its SLA — and the profit center bears the mis-receive cost. The contract makes this dependency explicit and enforceable. This is not a theoretical model; it is the mechanism by which LP investigations trace receiving discrepancies back to device failures rather than employee error.

### Business Rules

1. Every device in the ILDWAC system has a contract. No uncontracted devices.
2. Contracts define SLA tiers: `met` (within spec), `degraded` (within tolerance), `breached` (outside tolerance).
3. SLA monitoring is continuous. Breach detection triggers a cost packet (penalty) within the monitoring window (default: 5 minutes).
4. Profit-center devices must carry an L402 wallet balance above the operating cost reserve. Balance below reserve triggers a `funding_alert` event — not a shutdown (per the platform governing rule: devices never block store operations).
5. Cost-center devices (sensors, automated receivers) are funded by the profit center they serve. Funding relationship is explicit in `device_funding_links`.
6. Contract terms are stored in the chain (append-only). Contract amendments are new contract versions, not edits to existing records.

### Device Types and SLA Dimensions

| Device type | SLA dimensions | Default `met` tier |
|-------------|---------------|-------------------|
| `pos_terminal` | transactions_per_hour, uptime_pct, auth_latency_ms | >60 tph, >99.5% up, <2s |
| `receiving_sensor` | scans_per_hour, read_accuracy_pct, uptime_pct | >200 scph, >99%, >99% |
| `bopis_station` | holds_per_hour, pickup_confirmation_rate, uptime_pct | >20 hph, >95%, >99% |
| `inventory_scanner` | scans_per_hour, match_accuracy_pct, battery_pct | >100 scph, >98%, >20% |
| `self_checkout` | transactions_per_hour, shrink_rate_pct, uptime_pct | >15 tph, <2%, >99% |
| `automated_receiver` | items_per_hour, match_accuracy_pct, discrepancy_rate_pct | >500 iph, >99.5%, <0.5% |
| `ecom_fulfillment_station` | picks_per_hour, accuracy_pct, uptime_pct | >30 pph, >99.8%, >99% |

---

## Technical

### Data Model

```sql
-- Device contracts (append-only; amendments = new rows)
CREATE TABLE device_contracts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id       UUID NOT NULL REFERENCES inventory_devices(id),
    merchant_id     UUID NOT NULL,
    version         INT NOT NULL DEFAULT 1,
    contract_type   TEXT NOT NULL,  -- 'standard' | 'custom' | 'sensor_sla'
    sla_tiers       JSONB NOT NULL, -- {met: {}, degraded: {}, breached: {}} per dimension
    operating_cost_sats_per_hour BIGINT NOT NULL DEFAULT 0,
    funded_by_device_id UUID REFERENCES inventory_devices(id),  -- profit center funding this cost device
    l402_reserve_sats   BIGINT NOT NULL DEFAULT 0,  -- minimum wallet balance required
    monitoring_window_minutes INT NOT NULL DEFAULT 5,
    penalty_sats_per_breach BIGINT NOT NULL DEFAULT 0,
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at   TIMESTAMPTZ,   -- null = current version
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_dc_device_current ON device_contracts(device_id) WHERE superseded_at IS NULL;

-- Device funding links (cost device funded by profit center)
CREATE TABLE device_funding_links (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cost_device_id  UUID NOT NULL REFERENCES inventory_devices(id),
    profit_device_id UUID NOT NULL REFERENCES inventory_devices(id),
    allocation_pct  NUMERIC(5,2) NOT NULL DEFAULT 100.00,  -- if multiple profit centers share one sensor
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at        TIMESTAMPTZ,
    CONSTRAINT allocation_positive CHECK (allocation_pct > 0 AND allocation_pct <= 100)
);

-- SLA monitoring records (time-series; one row per monitoring window)
CREATE TABLE device_sla_readings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id       UUID NOT NULL REFERENCES inventory_devices(id),
    contract_id     UUID NOT NULL REFERENCES device_contracts(id),
    window_start    TIMESTAMPTZ NOT NULL,
    window_end      TIMESTAMPTZ NOT NULL,
    readings        JSONB NOT NULL,  -- actual values per SLA dimension
    sla_status      TEXT NOT NULL,   -- met | degraded | breached
    penalty_applied_sats BIGINT NOT NULL DEFAULT 0,
    breach_packet_id UUID,           -- ildwac_packets.id if penalty packet was created
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sla_readings_device_window ON device_sla_readings(device_id, window_start);
CREATE INDEX idx_sla_readings_breaches ON device_sla_readings(sla_status, window_start) WHERE sla_status = 'breached';

-- Device funding wallet state (mirrors L402 wallet, tracked here for contracts)
CREATE TABLE device_wallet_state (
    device_id       UUID PRIMARY KEY REFERENCES inventory_devices(id),
    balance_sats    BIGINT NOT NULL DEFAULT 0,
    reserve_sats    BIGINT NOT NULL DEFAULT 0,
    last_funded_at  TIMESTAMPTZ,
    funding_status  TEXT NOT NULL DEFAULT 'funded',  -- funded | below_reserve | unfunded
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Contract events (append-only chain; breach, funding alerts, amendments)
CREATE TABLE device_contract_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id       UUID NOT NULL REFERENCES inventory_devices(id),
    contract_id     UUID NOT NULL REFERENCES device_contracts(id),
    event_type      TEXT NOT NULL,  -- 'sla_breach' | 'sla_degraded' | 'sla_recovered' | 'funding_alert' | 'funding_restored' | 'contract_amended'
    payload         JSONB NOT NULL,
    raas_sequence   BIGINT,         -- appended to RaaS chain as 'device.contract.event'
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_dce_device_type ON device_contract_events(device_id, event_type, occurred_at);
```

### SLA Monitoring Loop (Go Implementation)

The monitor runs as a goroutine per active device. It wakes every `monitoring_window_minutes`, collects readings against the contract's SLA tiers, records the result, and fires a penalty packet if the device is in breach.

```go
// Runs as a goroutine per active device, wakes every monitoring_window_minutes
func (m *Monitor) MonitorDevice(ctx context.Context, deviceID uuid.UUID) {
    contract := m.loadCurrentContract(deviceID)
    ticker := time.NewTicker(time.Duration(contract.MonitoringWindowMinutes) * time.Minute)

    for {
        select {
        case <-ticker.C:
            readings := m.collectReadings(deviceID, contract.SLATiers)
            status := m.evaluateSLA(readings, contract.SLATiers)
            m.recordReading(deviceID, contract, readings, status)

            if status == "breached" {
                penalty := m.applyPenalty(deviceID, contract)
                // penalty = cost packet charged to cost center
                // triggers ildwac.submit_packet with sla_status=breached, sla_penalty_sats=N
            }

            m.publishEvent(deviceID, contract, status)
            // appended to RaaS chain as 'device.contract.event'

        case <-ctx.Done():
            return
        }
    }
}
```

### Funding Requirement

A profit center that depends on a cost device funds it via `device_funding_links`. Operating cost is debited from the profit center's OTB wallet every monitoring window:

```
hourly_debit = operating_cost_sats_per_hour × (monitoring_window_minutes / 60)
               × allocation_pct / 100
```

If `device_wallet_state.balance_sats < reserve_sats`, emit a `funding_alert` event. **Do not shut down the device.** The store operates; the alert triggers a funding action. This is the platform governing rule: no device contract can block store operations.

### API Contract

All routes require JWT auth. Contract amendments and funding operations additionally require `inventory_manager` or `admin` role.

```
POST /devices/contracts                    → 201 (create contract)
GET  /devices/contracts/{device_id}        → current contract
GET  /devices/contracts/{device_id}/history → all versions
GET  /devices/{device_id}/sla             → current SLA status + recent readings
GET  /devices/{device_id}/wallet          → wallet state + funding status
POST /devices/{device_id}/fund            → add balance to device wallet
GET  /devices/breaches                    → all breached devices for merchant (operational dashboard)
GET  /devices/healthz                     → shallow liveness
GET  /devices/readyz                      → DB + Valkey check
```

### MCP Tools — `canary-devices` (8 tools)

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `get_device_contract` | device_id | {contract, sla_tiers, funding_links} | Current contract version |
| `check_device_sla` | device_id | {status, readings, last_breach_at} | Live SLA status |
| `get_device_wallet` | device_id | {balance_sats, reserve_sats, funding_status} | Wallet state |
| `fund_device` | device_id, amount_sats, source_device_id | {new_balance_sats} | Transfer from profit center wallet |
| `list_breaches` | merchant_id, since? | [{device_id, breach_count, penalty_sats_total}] | Operational dashboard |
| `amend_contract` | device_id, sla_tiers?, operating_cost_sats_per_hour?, penalty_sats_per_breach? | {contract_id, version} | Creates new contract version |
| `get_sla_history` | device_id, from, to | [{window, status, readings, penalty}] | Time-series SLA readings |
| `get_contract_events` | device_id, event_type? | [{event}] | Breach/funding/amendment event chain |

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `check_device_sla` (Valkey hit) | <5ms | <20ms | 100ms | Alert + fallback to DB |
| `get_device_contract` | <10ms | <50ms | 200ms | Alert |
| `list_breaches` (merchant dashboard) | <50ms | <200ms | 1s | Alert |
| SLA monitoring cycle (per device) | — | — | monitoring_window_minutes + 30s | Alert if monitoring goroutine stalls |
| Penalty packet creation (on breach) | <100ms | <500ms | 2s | Alert; retry once, then dead-letter |

### Health Endpoints

```
GET /devices/healthz

Shallow liveness — returns 200 if the process is up.

Response 200:
{ "status": "ok" }
```

```
GET /devices/readyz

Deep readiness — verifies DB connection pool and Valkey reachability.

Response 200:
{
  "status": "ok",
  "db_ok": true,
  "valkey_ok": true,
  "monitored_devices": 142,
  "breached_devices": 2
}

Response 503 if DB or Valkey unreachable.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| Monitoring goroutine stalls for a device | Alert fires after `monitoring_window_minutes + 30s`. SLA readings gap in `device_sla_readings`. No penalty packet issued for the gap window. | Restart goroutine. Log the gap as `sla_monitoring_gap` event in `device_contract_events`. |
| ILDWAC unavailable at breach time | Penalty packet cannot be created. Log `penalty_deferred` in `device_contract_events` with full packet payload. Retry on ILDWAC recovery. | ILDWAC recovery triggers deferred penalty submission. Alert if deferred > 15 minutes. |
| Funding wallet below reserve | `funding_alert` event emitted. Device continues operating. OTB wallet funding action required. | Founder/operator funds via `POST /devices/{device_id}/fund`. Alert clears on `funding_restored` event. |
| Contract version conflict (concurrent amendment) | 409 returned. Amendment is rejected. | Retry with current version as base. Amendments are serialized per device_id. |

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `devices:contract:{device_id}` | 300s | Current contract cache |
| `devices:sla:{device_id}` | 60s | Live SLA status cache (short TTL — monitoring updates frequently) |
| `devices:wallet:{device_id}` | 120s | Wallet state cache |
| `devices:breaches:{merchant_id}` | 30s | Breach dashboard cache |

### Monitoring

Alert on:

- Any monitoring goroutine stall > `monitoring_window_minutes + 30s`
- Deferred penalty packets outstanding > 15 minutes
- Any `device_wallet_state.funding_status = 'unfunded'` sustained > 30 minutes (device is entirely unfunded, not just below reserve)
- `device_contract_events` breach rate > 5 per merchant per hour (possible device failure cascade)
- Any `device_contracts` amendment to a device that had a breach in the prior 24 hours (amendment should be preceded by a post-mortem, not a spec relaxation)

---

## Compliance

### Immutability Invariants

`device_contracts` is append-only. No UPDATE or DELETE on contract rows. Amendments are new rows; `superseded_at` is the only permitted UPDATE field (and only populated once, at amendment time).

```sql
REVOKE UPDATE, DELETE ON device_contracts FROM canary_app;
-- permit only: UPDATE device_contracts SET superseded_at = now() WHERE superseded_at IS NULL AND id = $1
```

`device_contract_events` rows are appended to the RaaS chain as `device.contract.event`. They are evidence: a breach event that preceded a receiving discrepancy is LP-relevant context — the causal chain runs from device failure to mis-receive to cost discrepancy.

### LP Evidence Chain

The audit path for a receiving discrepancy caused by sensor failure:

```
device_sla_readings (breach window) 
    → breach_packet_id → ildwac_packets (penalty packet, sla_status=breached)
    → device_contract_events (sla_breach event, raas_sequence populated)
    → hawk_case_evidence (linked when LP investigator opens a case)
```

SLA penalty packets in `ildwac_packets` with `sla_status = 'breached'` trace to a `device_sla_readings` row. The audit chain is complete: breach reading → penalty packet → ildwac chain entry → hawk case evidence.

### Governing Rule — No Device Blocks Operations

Funding alerts do not block operations. Logging a `funding_alert` event is required; taking action that prevents a transaction is prohibited. This rule is enforced at the application layer (no circuit-breaker pattern on device operations) and documented here for implementors.

No device contract state — unfunded, breached, degraded — may cause a 4xx or 5xx response on a POS transaction, receiving event, or inventory query. Device Contracts is an observation and cost-allocation layer, not an operational gate.

### Patent Scope

Device-level cost tracking at packet granularity, with SLA-linked cost adjustment, is within the scope of patent application #63/991,596. The linkage between `device_sla_readings`, `ildwac_packets.sla_penalty_sats`, and `ildwac_chain` is a covered claim. Do not modify the penalty packet structure or the breach-to-chain event path without legal review.

---

## Related SDDs

- `ildwac.md` — SLA status and `sla_penalty_sats` propagate into `CostPacket`; device operating costs are ILDWAC cost events; `ildwac_packets.breach_packet_id` links back to `device_sla_readings`
- `inventory-as-a-service.md` — `inventory_devices` is the device registry; `device_category` (`cost_center` | `profit_center`) is set there; device-contracts consumes but does not own this table
- `raas.md` — contract events appended to RaaS chain as `device.contract.event`; same hash primitive as `ildwac_chain`
- `l402-otb.md` — profit-center device wallet balance is the L402 OTB tracking surface; `device_wallet_state` mirrors the L402 position for contract enforcement
- `receiving.md` — `automated_receiver` and `receiving_sensor` devices are the primary SLA-monitored devices in the receiving workflow; sensor breach during a PO receipt produces a `sla_status=breached` packet on every affected receive line
- `hawk-case-management.md` — SLA breach events preceding shrink or discrepancy events are LP case evidence; `device_contract_events` rows are linkable to `hawk_case_evidence` by `occurred_at` and `device_id`
