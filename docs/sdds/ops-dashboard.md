---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST + SSE | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: ops-dashboard
port: 9084
mcp-server: canary-ops
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Ops Dashboard — Store NOC Interface

**Type:** Operational Service — Real-Time Device Health + MCP Observability Surface  
**Binary:** `cmd/ops-dashboard` → `:9084`  
**MCP server:** `canary-ops` (7 tools)  
**Depends on:** `raas` (chain events), `device-contracts` (device registry + SLA state), `ildwac` (wallet state), `identity` (merchant/location)  
**Feeds:** `canary-chirp` (alert escalation), all MCP servers (probed on 10s interval)

> **Solex is illustrative for the merchant admin surface:** Solex's `solex/routes/admin_*.py` (8 admin routes — catalog, customers, inventory, orders, returns, subscriptions, utils, auth) is a working example of the merchant-facing operational console this dashboard is designed to be. The controls a merchant operator needs over catalog, customers, inventory, orders, returns, and subscriptions are already enumerated and exercised there. The Go ops-dashboard exposes these over REST + SSE rather than Flask templates, but the surface area Solex proves out is the surface area ops-dashboard surfaces to merchants. See ecom-channel.md → "Solex Asset Reuse Beyond ecom-channel" for the full cross-module map.

The Ops Dashboard is the platform's Network Operations Center surface — the single pane of glass where every device in a merchant's store is visible, every MCP server's health is live, and every SLA breach is tracked to resolution. It has two audiences sharing one interface: the service technician who needs device logs and breach timelines, and the store manager who needs to know which problems are costing money and which jobs are in flight.

**Multi-tenant context.** Ops Dashboard is per-merchant — every operator views only their own merchant's devices, MCP servers, and SLA state. Tables (`device_status_snapshots`, `mcp_health_log`, `sla_breach_events`, `oncall_assignments`) live per-tenant in `tenant_{merchant_id}`. Platform-wide ops health (across all merchants, for the GrowDirect platform team) flows through a separate platform-admin dashboard that reads from the `analytics` schema, never via cross-tenant queries from this service. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Ops Dashboard operates with all Optional Features (per `platform-overview.md`) disabled — device health monitoring, MCP probe results, SLA tracking all run on standard internal records. When `L402_ENABLED=true`, the wallet state widget surfaces L402 OTB balance per `l402-otb.md`. When `ILDWAC_ENABLED=true`, the device profitability widget shows per-device cost attribution. When the flags are off, those widgets render their respective "feature not enabled" states without errors.

---

## UI Layout

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│  CANARY OPS  │  Merchant: Green Valley Garden Supply  │  Location: [Main ▼]    │
│              │  2026-04-29  14:32:07 PDT              │  ● HEALTHY             │
│              │                              On-call: J. Torres  📞 click-to-call │
├──────────────────┬──────────────────────────────────────┬───────────────────────┤
│ DEVICE REGISTRY  │        MCP HEALTH GRID               │      LOG TAIL         │
│ ──────────────── │ ──────────────────────────────────── │ ───────────────────── │
│ ● A3             │         inv  raas  ildw  dev  ecom  │ [device: PEG-47     ] │
│   terminal       │ ping    ██   ██   ██   ██   ██      │ Filter: [all ▼][5m ▼] │
│   POS / front    │ read    ██   --   --   --   ██      │ ──────────────────── │
│   cost: register │ write   ██   --   ██   --   ██      │ 14:31:58 | recv.scan  │
│   raas:mrch_id.. │ verify  --   ██   --   --   --      │ seq 4421 | device_A   │
│   canary-inv     │ chain   --   ██   --   --   --      │ ✓ OK                 │
│ ──────────────── │ health  ██   ██   ██   ██   ██      │                       │
│ ● PEG-47         │                                      │ 14:31:44 | sla.check  │
│   sensor         │         set  ops  roas ildw_s       │ seq 4420 | device_B   │
│   Shelf B / peg  │ ping    ██   ██   ██   ██            │ ⚠ DEGRADED           │
│   profit: peg    │ config  ██   --   --   --            │                       │
│   raas:mrch_id.. │ probe   --   ██   --   --            │ 14:31:31 | tx.void    │
│   canary-dev     │ ack     --   ██   --   --            │ seq 4419 | assoc_03   │
│ ──────────────── │                                      │ ✓ OK                 │
│ ⚠ RECV-SENSOR-1  │  ██ healthy  ░░ degraded  ✕ breach  │                       │
│   sensor         │  -- n/a for server                  │ 14:31:18 | sla.breach │
│   Backroom / rcv │                                      │ seq 4418 | device_C   │
│   cost: recv     │  Hover any cell → latency / count   │ ✗ BREACH             │
│   raas:mrch_id.. │  / last error                       │                       │
│   canary-dev     │                                      │ [auto-scroll ■ pause] │
│ ──────────────── │                                      │                       │
│  + 12 more...    │                                      │                       │
├──────────────────┴──────────────────────────────────────┴───────────────────────┤
│ ACTIVE ALERTS                                                                   │
│  ✗ RECV-SENSOR-1  │ SLA breach: missed read window  │ 0:04:12  │ 320 sats  │ ▶ │
│  ⚠ PEG-47         │ P99 degraded: latency elevated  │ 0:00:47  │ —         │ ▶ │
│                                                           Assigned: J. Torres   │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### Panel descriptions

**Top bar:** Merchant name; location selector (dropdown, all locations the authenticated user can see); wall-clock time; global health indicator (green = all devices healthy, yellow = one or more degraded, red = one or more breached); current on-call technician name with click-to-call (tel: link, mobile browser) or click-to-email.

**Left panel — Device Registry:** Each device is a row. Status dot green/yellow/red reflects SLA tier status from `device_sla_readings`. Device type icon (sensor, terminal, scanner, station). Device code (human-assigned, e.g., A3, PEG-47, RECV-SENSOR-1). Sub-location (shelf / backroom / hold area). Category badge (cost center or profit center, from device contract). Namespace (raas:{merchant_id}, abbreviated to fit). MCP server assignment (which canary-* server manages this device). Click any row → log tail panel filters to that device and expands.

**Center panel — MCP Health Grid:** Columns are registered MCP servers (canary-inventory, canary-raas, canary-ildwac, canary-devices, canary-ecom, canary-settings, canary-ops, canary-roas). Rows are individual tools within each server. Cell color: green (healthy, under P99 SLA threshold), yellow (degraded, P99 elevated), red (breached or unreachable). `--` means that tool does not exist on that server. Hover any cell: popover shows last call latency, call count in last 5 minutes, and last error message if any. Grid is a snapshot refreshed every 10 seconds via the active probe loop.

**Right panel — Log Tail:** SSE stream from the RaaS chain filtered by the selected device_id. Each line: timestamp | event_type | sequence_num | actor_id | status. Color-coded: green for normal events, yellow for SLA degraded events, red for breach or error events. Auto-scrolls; pauses on hover. Filter bar: by event_type (dropdown), by time range (last 5m / 15m / 1h / custom), by actor_type (device / associate / system).

**Bottom bar — Active Alerts:** Current SLA breaches and degraded devices, sorted by severity then duration. Each alert row: device name, breach type, duration since breach started, penalty_sats accumulating from device wallet, assigned tech (click to reassign). Clicking the `▶` expand arrow opens the full breach timeline in the log tail panel.

---

## Data Sources

| Panel | Primary source | Update mechanism |
|-------|---------------|-----------------|
| Device registry | `inventory_devices` + `device_contracts` | Polling every 30s |
| SLA status | `device_sla_readings` + `device_wallet_state` | SSE push on state change |
| MCP health grid | Active probe per registered MCP tool | Probe loop every 10s |
| Log tail | RaaS chain SSE stream filtered by `device_id` | SSE (real-time) |
| Active alerts | `device_contract_events` WHERE `event_type = 'sla_breach'` | SSE push |
| On-call tech | `on_call_rotations` | Polling every 5 min |

---

## SSE Endpoints

```
GET /ops/stream/devices/{merchant_id}/{location_id}  → SSE: device state changes
GET /ops/stream/logs/{device_id}                     → SSE: RaaS events for this device
GET /ops/stream/alerts/{merchant_id}                 → SSE: breach events
GET /ops/health-grid/{merchant_id}                   → JSON snapshot of MCP health grid
GET /ops/devices/{merchant_id}/{location_id}         → JSON device registry
```

All SSE endpoints require a valid JWT either as a `Bearer` header or `token` query parameter. Unauthenticated connections are rejected with 401 before any data is sent.

---

## MCP Health Probe

The ops-dashboard service actively probes each registered MCP server every 10 seconds using a lightweight `ping` tool call. Every MCP server in the Canary Go platform must implement a `ping` tool that returns `{ok: true, latency_ms: N}`. If ping fails or latency exceeds the P99 SLA threshold configured for that server, the grid cell transitions to yellow (degraded) or red (unreachable). Results are written to `mcp_tool_health` (time-series) and held in the `MCPHealthProber.results` sync.Map for zero-latency grid snapshot reads.

```go
type MCPHealthProber struct {
    servers  []MCPServerConfig
    results  sync.Map  // key: "server:tool" → HealthResult
    interval time.Duration
}

type HealthResult struct {
    Status      string    // "healthy" | "degraded" | "unreachable"
    LatencyMs   int64
    LastCallAt  time.Time
    LastError   string
    CallCount5m int
}
```

The probe goroutine runs on startup and restarts automatically on panic. Each probe cycle fans out concurrently across all registered servers using a bounded worker pool (max 16 concurrent probes). A server that fails 3 consecutive pings is marked `unreachable`; a single successful ping restores it to `healthy` or `degraded` based on latency.

---

## On-Call Routing

```sql
CREATE TABLE on_call_rotations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL,
    location_id      UUID,           -- NULL = applies to all locations
    tech_name        TEXT NOT NULL,
    tech_phone       TEXT NOT NULL,
    tech_email       TEXT NOT NULL,
    on_call_from     TIMESTAMPTZ NOT NULL,
    on_call_until    TIMESTAMPTZ NOT NULL,
    escalation_order INT NOT NULL DEFAULT 1,  -- 1 = primary, 2 = secondary, etc.
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_on_call_active ON on_call_rotations(merchant_id, on_call_from, on_call_until);
```

Auto-escalation rule: any SLA breach that remains unacknowledged for 15 minutes triggers escalation to the next `escalation_order` tier. Escalation is dispatched via `canary-chirp` (notification service). If no higher tier exists, the breach is flagged as `escalation_exhausted` and surfaced in the alert bar with a distinct badge.

---

## Data Model

```sql
-- MCP server registry (what servers are registered for this merchant)
CREATE TABLE mcp_server_registrations (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL,
    server_name           TEXT NOT NULL,   -- "canary-inventory", "canary-raas", etc.
    base_url              TEXT NOT NULL,
    registered_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_ping_at          TIMESTAMPTZ,
    last_ping_latency_ms  INT,
    status                TEXT NOT NULL DEFAULT 'registered',
    -- 'registered' | 'healthy' | 'degraded' | 'unreachable'
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(merchant_id, server_name)
);

-- MCP tool health readings (time-series, 30-day retention)
CREATE TABLE mcp_tool_health (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_name TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    merchant_id UUID NOT NULL,
    latency_ms  INT,
    status      TEXT NOT NULL,   -- "healthy" | "degraded" | "unreachable"
    error       TEXT,
    probed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_mcp_tool_health_recent
    ON mcp_tool_health(server_name, tool_name, probed_at DESC);

-- Retention: rows older than 30 days are purged by a nightly cleanup job.
-- Operational data only; not evidence-grade. Do not chain these readings.
```

---

## MCP Tools — `canary-ops` (7 tools)

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `ping` | (none) | `{ok: true, latency_ms: N}` | Required on every MCP server; also self-probed |
| `get_device_health` | `location_id` | `[{device_id, status, sla_tier, last_event_at}]` | Full device registry with health status |
| `get_mcp_grid` | `merchant_id` | `[{server, tools: [{name, status, latency_ms}]}]` | MCP health grid snapshot from in-memory results |
| `get_log_tail` | `device_id, limit?, after_seq?` | `[{seq, event_type, actor_id, occurred_at, status}]` | RaaS events for a specific device |
| `get_active_alerts` | `merchant_id` | `[{device_id, breach_type, started_at, penalty_sats}]` | Current breach list sorted by severity |
| `get_oncall` | `merchant_id, location_id?` | `[{tech_name, tech_phone, escalation_order}]` | Current on-call rotation; restricted to admin role |
| `acknowledge_alert` | `device_id, alert_id, tech_id, note` | `{acknowledged_at}` | Records tech acknowledgement; resets escalation timer |

All tools require a valid session context. `get_oncall` additionally requires `role: admin` in the session — it exposes tech phone numbers and must not be visible to store-level users.

---

## Compliance

- **Log tail PII:** RaaS chain events may contain customer emails (ecom events), employee IDs (associate actions), and loyalty identifiers. The SSE stream strips raw payload fields before transmission to the browser. `actor_id` (pseudonymous UUID) is shown; the underlying entity is resolved only in authenticated tool calls with appropriate role.
- **On-call data:** `on_call_rotations` contains tech phone numbers and email addresses. `get_oncall` is gated to admin role. On-call data is not surfaced in SSE streams.
- **MCP health records:** Operational telemetry, not evidence. 30-day retention is sufficient. Records are not hash-chained and carry no evidentiary weight.
- **SSE authentication:** All `/ops/stream/*` endpoints require a valid JWT. The JWT must be presented as a `Bearer` header or `token` query parameter. Connections without valid auth are rejected with 401 before the SSE handshake completes — no data leaks on unauthenticated connection attempts.
- **Alert acknowledgements:** Written to `device_contract_events` as a chain event via RaaS. The acknowledgement is evidence-grade; the probe health readings that preceded it are not.

---

## Related SDDs

- `raas.md` — log tail streams RaaS chain events; breach events are chain-recorded
- `device-contracts.md` — device registry, SLA readings, and wallet state are primary data sources
- `ildwac.md` — wallet state (penalty_sats accumulating) feeds the active alerts panel
- `store-brain.md` — `get_store_state` and `get_presence_count` from canary-brain feed a future occupancy panel
- `settings.md` — `check_flag('feature.ops_dashboard_enabled')` gates dashboard access per merchant
