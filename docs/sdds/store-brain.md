---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: store-brain
port: 9085
mcp-server: canary-brain
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Store Brain — In-Store AI Context Manager

**Type:** Infrastructure Service — Presence Resolution + Session Governance  
**Binary:** `cmd/store-brain` → `:9085`  
**MCP server:** `canary-brain` (9 tools)  
**Depends on:** `identity` (merchant/location), `raas` (session chain events), `device-contracts` (entry sensor events), `inventory-as-a-service` (in-stock context), `ecom-channel` (BOPIS holds)  
**Feeds:** All MCP servers (session context + tool permission gating), `ops-dashboard` (occupancy data), `canary-chirp` (greeting delivery)

> **Catalog and cart are cross-channel primitives:** Solex's `solex/services/cart.py` + `checkout.py` were built for online ordering, but the same primitives power in-store ordering — kiosk flows, associate-assisted special orders, BOPIS extensions, on-the-floor cart entry. Store-brain reuses the catalog read-path (via `cmd/item`) and the inventory reservation contract (via `cmd/inventory-as-a-service`) that ecom-channel exercises; only the UX surface and channel attribution differ. The "online order" and the "associate-rung special order" share backing primitives. See ecom-channel.md → "Solex Asset Reuse Beyond ecom-channel" for the full cross-module map.

The Store Brain is the context-setting infrastructure layer that fires before any other interaction in a Canary-connected store. Its governing thesis: the MCP greets first. When a customer walks through the door, when an associate starts their shift, when a device boots — the brain fires `presence_detected`, assembles everything the store knows about that subject in parallel, creates a scoped session, and publishes the result to Valkey before any associate speaks, any kiosk renders, or any POS transaction begins. Every MCP tool call that follows runs inside that session's permission scope.

---

## Three Presence Types

| Presence type | Trigger | Session type | Context assembled |
|--------------|---------|--------------|-------------------|
| Customer entry | Entry sensor, mobile app check-in, loyalty tap | `customer` | Identity (loyalty member or unknown), purchase history, active BOPIS holds, personalized promotions, in-stock position for likely items |
| Associate shift start | POS login, badge scan, mobile login | `associate` | Role (cashier / manager / LP officer), zone assignment, active LP cases (if LP role), device assignments for shift |
| Device boot | Device registration event from device-contracts | `device` | Contract terms, SLA requirements, funding status, linked profit/cost center |

---

## Session Model

```go
type StoreSession struct {
    SessionID       uuid.UUID
    MerchantID      uuid.UUID
    LocationID      uuid.UUID
    SessionType     string        // "customer" | "associate" | "device"
    SubjectID       string        // customer_id | associate_id | device_id
    SubjectType     string        // "loyalty_member" | "unknown_customer" | "associate" | "device"
    Context         SessionContext
    ToolPermissions []string      // which MCP tools are callable in this session
    ExpiresAt       time.Time
    CreatedAt       time.Time
    ClosedAt        *time.Time
}

type SessionContext struct {
    // Customer context (populated for customer sessions)
    CustomerName       string
    LoyaltyPoints      int64
    LastVisitAt        *time.Time
    LastBasket         []BasketItem
    ActiveBOPISHolds   []BOPISHold
    PersonalizedOffers []Offer

    // Associate context (populated for associate sessions)
    AssociateName    string
    Role             string
    ZoneAssignment   string
    ActiveLPCases    []string  // case IDs; only populated for LP officer role

    // Store state (always included regardless of session type)
    LocationName           string
    CurrentInventoryAlerts []InventoryAlert
    ActivePromotions       []Promotion
    OnCallTech             string

    // Device context (populated for device sessions)
    DeviceContract *DeviceContract
    SLAStatus      string
    FundingStatus  string
}
```

---

## Tool Permissions — "Rules of the Game"

The `ToolPermissions` array is a literal allowlist established at session creation. Every sensitive MCP tool call in the platform must invoke `canary-brain.check_tool_permission` before executing. The brain enforces scope; the tool enforces the contract; the chain records the result.

```go
var sessionToolPermissions = map[string][]string{
    "unknown_customer": {
        "canary-inventory.get_positions_bulk",    // what's in stock
        "canary-ecom.get_ecom_orders",            // order status by email
        "canary-inventory.route_fulfillment",      // BOPIS availability check
    },
    "loyalty_member": {
        // all unknown_customer tools, plus:
        "canary-brain.get_customer_context",       // full purchase history
        "canary-inventory.reserve_inventory",       // cart reservation
        "canary-ildwac.get_wac",                   // price/cost lookup
    },
    "associate_cashier": {
        // all loyalty_member tools, plus:
        "canary-inventory.record_adjustment",       // manual adjustment (within limits)
        "canary-inventory.update_bopis_status",     // BOPIS pick / ready / fulfill
        "canary-raas.append_event",                 // transaction events
    },
    "associate_manager": {
        // all cashier tools, plus:
        "canary-ildwac.submit_packet",             // cost adjustments
        "canary-devices.amend_contract",           // device contract changes
        "canary-ops.acknowledge_alert",            // alert acknowledgement
    },
    "associate_lp_officer": {
        // all manager tools, plus:
        "canary-raas.verify_chain",                // chain verification (audit)
        "canary-ildwac.shrink_by_device",          // shrink analysis
        "canary-devices.get_contract_events",      // device breach history
    },
}
```

`check_tool_permission` denials are logged with session_id, tool_name, and caller identity. Repeated denials on a single session — the same associate attempting the same out-of-scope call — are an anomaly signal surfaced to the LP officer role.

---

## Customer Greeting Sequence

```
Entry sensor fires:
  { device_id: "ENTRY-SENSOR-1", event_type: "presence.detected",
    payload: { method: "door_sensor" } }
    │
    ▼
store-brain receives via canary-brain.presence_detected tool call
    │
    ▼
Identity resolution (parallel fan-out):
    ├── Loyalty lookup: any tap, mobile check-in, or email match in last 24h?
    ├── BOPIS check: any open holds at this location?
    └── Promotion assembly: what's active today for this location?
    │
    ▼
Session created:
  StoreSession{ type: "customer",
                subject_type: "loyalty_member" | "unknown_customer" }
    │
    ▼
Context assembled:
  name, points, last basket, BOPIS holds,
  personalized offers, in-stock status for likely items
    │
    ▼
Session published to Valkey:
  brain:session:{session_id}  TTL: 4 hours
    │
    ▼
Greeting event appended to RaaS chain:
  event_type: "brain.session.opened"
    │
    ▼
Downstream consumers pick up session:
  → Associate POS terminal: "Welcome back, [name]. Your order is ready at pickup."
  → Store kiosk: renders personalized landing page
  → Associate mobile: shows customer context card
```

Identity resolution is a best-effort parallel lookup with a 200ms timeout. If no loyalty match is found within the window, the session is created as `unknown_customer` and upgraded to `loyalty_member` if the customer subsequently taps or provides email.

---

## Data Model

```sql
-- Store sessions
CREATE TABLE store_sessions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL,
    location_id      UUID NOT NULL,
    session_type     TEXT NOT NULL,    -- 'customer' | 'associate' | 'device'
    subject_id       TEXT NOT NULL,    -- customer_id | associate_id | device_id
    subject_type     TEXT NOT NULL,
    context_json     JSONB NOT NULL,
    tool_permissions TEXT[] NOT NULL,
    opened_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at        TIMESTAMPTZ,
    close_reason     TEXT,
    -- 'exit' | 'timeout' | 'explicit_close' | 'shift_end'
    raas_open_seq    BIGINT,           -- chain seq for brain.session.opened
    raas_close_seq   BIGINT,           -- chain seq for brain.session.closed
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sessions_location_active
    ON store_sessions(location_id, opened_at)
    WHERE closed_at IS NULL;

CREATE INDEX idx_sessions_subject
    ON store_sessions(subject_id, opened_at DESC);

-- Customer presence detection log (PII — encrypt at rest, 90-day retention)
CREATE TABLE customer_presence_log (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL,
    location_id      UUID NOT NULL,
    device_id        UUID REFERENCES inventory_devices(id),
    detection_method TEXT NOT NULL,
    -- 'door_sensor' | 'mobile_checkin' | 'loyalty_tap' | 'email_lookup'
    customer_id      UUID,            -- NULL if unresolved
    session_id       UUID REFERENCES store_sessions(id),
    detected_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Store promotions (assembled into customer session context)
CREATE TABLE store_promotions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL,
    location_id     UUID,            -- NULL = all locations
    promotion_type  TEXT NOT NULL,
    -- 'discount' | 'bogo' | 'loyalty_bonus' | 'clearance'
    applies_to      JSONB NOT NULL,  -- item_ids, category codes, or {"all": true}
    discount_pct    NUMERIC(5,2),
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_promotions_active
    ON store_promotions(merchant_id, starts_at, ends_at)
    WHERE active = true;
```

---

## Valkey Session Cache

```
brain:session:{session_id}       → SessionContext JSON, TTL 4 hours
brain:active:{location_id}       → SET of active session_ids at this location
brain:customer:{customer_id}:loc → location_id of current visit, TTL 8 hours
brain:presence:{location_id}     → count of active customer sessions (occupancy)
```

The Valkey TTL is the primary expiry mechanism for active sessions. The `store_sessions` table is the durable record. On expiry, a background watcher reconciles any sessions in Valkey that expired without an explicit close event and writes a `close_reason: 'timeout'` record to the DB.

---

## API Contract

```
POST   /brain/presence                           → 201: session created
GET    /brain/session/{session_id}               → current session context
PUT    /brain/session/{session_id}/activity      → 200: TTL refreshed
DELETE /brain/session/{session_id}               → 200: session closed
GET    /brain/active/{location_id}               → all active sessions at location
GET    /brain/healthz                            → shallow liveness (no DB)
GET    /brain/readyz                             → DB + Valkey connectivity check
```

---

## MCP Tools — `canary-brain` (9 tools)

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `ping` | (none) | `{ok: true}` | Required on every MCP server |
| `presence_detected` | `location_id, device_id, detection_method, customer_hint?` | `{session_id, subject_type, context_summary}` | Main entry point; fires on entry sensor, badge scan, device boot |
| `get_session` | `session_id` | `{session, context, tool_permissions}` | Full session state for downstream consumers |
| `get_customer_context` | `session_id` | `{name, points, last_basket, bopis_holds, offers}` | Customer-facing context; requires `loyalty_member` subject_type |
| `get_associate_context` | `session_id` | `{name, role, zone, active_cases, assigned_devices}` | Associate-facing context |
| `get_store_state` | `location_id` | `{active_sessions_count, active_alerts, on_call_tech, promotions}` | Store-level status snapshot; feeds ops-dashboard occupancy panel |
| `check_tool_permission` | `session_id, tool_name` | `{permitted: bool, reason?}` | Gates all sensitive tool calls; denials logged |
| `close_session` | `session_id, reason` | `{closed_at}` | Explicit close on exit, shift end, or device shutdown |
| `get_presence_count` | `location_id` | `{customer_count, associate_count}` | Live occupancy; feeds ops-dashboard |

---

## Business

The Store Brain answers a question retail has never been able to answer with precision: **who is in my store right now, and what do they need before they ask?** Traditional POS systems know what a customer bought after the transaction completes. CRM systems know what they bought last quarter. Neither knows that this customer — the one who just walked through the door — has a BOPIS order waiting at the pickup counter, qualifies for a loyalty tier discount on an item that happens to be in stock right now, and last visited six weeks ago with a basket that suggests they're a seasonal buyer.

The brain knows all of that before the first word is spoken.

The MCP greeting is not a UX feature; it is a data infrastructure event. When `presence_detected` fires, it kicks off a parallel resolution sequence that assembles identity, transaction history, open orders, active promotions, and live inventory position in a single sub-200ms window. The result is a scoped session — a structured context object plus a permissions allowlist — published to Valkey and ready for any downstream consumer: POS terminal, store kiosk, associate mobile, or an AI model running a customer interaction.

"Setting the rules of the game" is architecturally precise. The `tool_permissions` array in the session is a literal allowlist. Before any sensitive MCP tool executes, it calls `check_tool_permission` with the session_id and tool name. The brain returns `{permitted: true}` or `{permitted: false, reason: "role insufficient"}`. An unknown customer cannot call `canary-ildwac.get_wac` (cost data). A cashier cannot call `canary-raas.verify_chain` (audit tool). The brain establishes scope at session creation; scope is enforced at every tool invocation for the life of the session. There is no way to circumvent it without creating an explicit chain record of the attempt.

This is the commercial case for the brain module: it converts every presence event — a door opening, a badge tap, a device boot — into a structured data asset that governs the entire interaction that follows. The alternative is what every retailer has today: an associate who may or may not know the customer, a POS that knows nothing until the transaction is keyed, and no connection between who walked in and what happened next.

---

## Compliance

- **`customer_presence_log`** contains detection method and customer_id when resolved. PII. Encrypt at rest. 90-day retention. Delete on right-to-erasure request.
- **Session context (`context_json`)** contains loyalty points, purchase history, and BOPIS hold details. PII. Valkey TTL is the primary expiry; DB records retained 90 days then purged. Apply cryptographic erasure to `context_json` for identified customers subject to right-to-erasure requests — the session row shell (IDs, timestamps, chain sequences) is retained as an audit artifact; the personal content is zeroed.
- **`check_tool_permission` denials** are logged with `session_id`, `tool_name`, and `attempted_at`. Repeated denials on the same session are an LP anomaly signal and must be accessible to `associate_lp_officer` sessions via `get_associate_context`.
- **`brain.session.opened` and `brain.session.closed` RaaS chain events** establish that a named individual was present at a location at a specific time. Treat as sensitive, same classification as transaction records. These events are evidence-grade; they cannot be corrected — only superseded by a new chain event.
- **GDPR right-to-erasure:** Chain events for an identified customer are preserved (the fact of presence is the audit record); only `context_json` content is erased. Implement as a background erasure job that replaces `context_json` with a tombstone `{"erased": true, "erased_at": "..."}` and records the erasure in a separate log.

---

## Related SDDs

- `device-contracts.md` — entry sensors trigger `presence_detected`; device boot events create device sessions
- `inventory-as-a-service.md` — `get_positions_bulk` called during context assembly for in-stock position
- `ecom-channel.md` — BOPIS holds assembled into customer session context
- `raas.md` — `brain.session.opened` and `brain.session.closed` events appended to chain; permission denial events are chain-recorded
- `ops-dashboard.md` — `get_store_state` and `get_presence_count` feed the ops dashboard occupancy panel
- `settings.md` — `check_flag('feature.brain_greeting_enabled')` gates whether automated greeting fires per merchant
