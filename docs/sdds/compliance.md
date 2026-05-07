---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-30
binary: compliance
port: 9091
mcp-server: canary-compliance
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Compliance — Item Authorization & Regulatory Zone Engine

**Type:** Authorization Service — Item Eligibility × Site Regulatory Zone × Operational Blocks  
**Binary:** `cmd/compliance` → `:9091`  
**MCP server:** `canary-compliance` (7 tools)  
**Depends on:** `identity` (merchant/location), `item` (item master — serialized flag, department classification), `employee` (associate certifications), `hawk` (case references for blocked-item incidents)  
**Feeds:** `store-brain` (session tool permission gates), `inventory-as-a-service` (transaction authorization before reservation), `tsp` (pre-commit authorization check on restricted items), `ops-dashboard` (compliance posture panel), `store-network-integrity` (cross-location regulatory deviation signals)

The compliance service answers one question before any restricted-item transaction completes: **is this item legally and operationally eligible to be sold at this location, by this associate, to this customer, right now?** It is the authorization contract that no other module in the spine was designed to own. Without it, restriction logic gets duplicated — or worse, silently skipped — across tsp, item, and hawk. With it, every POS integration, BOPIS fulfillment path, and agentic tool call routes through a single authoritative gate.

> **Why this is a separate service and not a rule in Chirp:** Chirp is a post-transaction detection engine — it fires after a sale is recorded and produces alerts. Compliance is a pre-transaction authorization gate — it blocks ineligible transactions before they commit. The failure mode is opposite. Chirp's false-positive is a spurious alert; Compliance's false-negative is an illegal sale. These cannot share a runtime, a data model, or a deployment lifecycle.

> **The Murdoch's proof case:** Murdoch's Ranch & Home Supply sells firearms (Sporting Goods: 3,016 SKUs), restricted-use pesticides (Farm/Ranch: ag-chemicals sub-dept), and live animals (Pets/Livestock: live-birds, 255 URLs). All three product families carry federal or state compliance obligations that differ by store location — a Laramie, WY store operates under Wyoming firearms transfer rules; a Amarillo, TX store operates under Texas ag-chemical dealer licensing. Item authorization is not uniform across the 47-store footprint. The compliance service owns that heterogeneity.

**Multi-tenant context.** All compliance tables live per-tenant in `tenant_{merchant_id}`. A merchant's regulatory zone configuration, restriction catalog, and associate certification records are fully isolated. Cross-tenant compliance benchmarks (e.g., network-wide FFL compliance posture) flow through `analytics` schema rollups only. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** The compliance service operates with all Optional Features disabled by default. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, every item authorization denial and every manual compliance override is hash-anchored to L2 for external verifiability — creating an immutable regulatory audit trail. When disabled, the internal hash-chain via Fox evidence locker still records every denial. Anchor failures are non-blocking.

---

## The Authorization Contract

The compliance decision is a three-factor AND gate:

```
AUTHORIZED = item_eligibility(item_id, location_id)
           AND site_regulatory_zone(location_id, item_class)
           AND operational_blocks(associate_id, item_id, timestamp)
```

All three factors must resolve `AUTHORIZED` for a transaction to proceed. Any factor returning `RESTRICTED` or `BLOCKED` short-circuits the evaluation and returns a denial with a reason code and remediation path.

### Factor 1 — Item Eligibility

Item-level attributes that constrain where and how an item may be sold:

| Attribute | Source | Behavior |
|---|---|---|
| `regulated_class` | `compliance_items.regulated_class` | Null = unrestricted; values: `FIREARM`, `NFA_ITEM`, `HAZMAT`, `RUP` (Restricted Use Pesticide), `OTC_PESTICIDE`, `LIVE_ANIMAL`, `CONTROLLED_SUBSTANCE`, `AGE_RESTRICTED` |
| `serialized` | `items.serialized` | Firearms must be serialized; serial number required at transaction commit |
| `ffl_required` | `compliance_items.ffl_required` | True for all `FIREARM` and `NFA_ITEM` — triggers FFL dealer check gate |
| `age_minimum` | `compliance_items.age_minimum` | 18 = long guns; 21 = handguns; 21 = tobacco/alcohol/vaping; state override possible |
| `license_required_buyer` | `compliance_items.license_required_buyer` | `PESTICIDE_APPLICATOR`, `HUNTING_LICENSE`, `FISHING_LICENSE`, null |
| `seasonal_windows` | `compliance_items.seasonal_windows` JSONB | Array of `{open_date, close_date, zone_id}` — e.g., live chick season Mar 15 – May 31 |
| `prohibited_in_zones` | `compliance_items.prohibited_in_zones` text[] | Array of `zone_id` values where item is outright prohibited |

### Factor 2 — Site Regulatory Zone

Location-level configuration that defines which regulatory regime governs each store:

| Zone type | Description | Examples |
|---|---|---|
| `FFL_DEALER` | Store holds active FFL license — required to transfer firearms | Murdoch's Laramie, Murdoch's Billings |
| `PESTICIDE_DEALER` | State pesticide dealer license — required for RUP sales | CO, WY, MT, TX stores with ag chem departments |
| `LIVE_ANIMAL` | USDA-compliant live animal handling — required for poultry, livestock | Stores with live bird programs |
| `AGE_VERIFICATION` | State-specific age verification protocols (some states require electronic scan) | TX stores: tobacco age scan required |
| `HAZMAT_LICENSED` | DOT HAZMAT certified receiving / handling | Stores receiving bulk ag chemicals |
| `NO_RUP` | Store is not licensed to sell restricted-use pesticides | Typically urban/suburban locations |

Zone configuration is set per location during onboarding and updated when license status changes. A location can hold multiple zone types.

### Factor 3 — Operational Blocks

Runtime constraints that can block a transaction independent of item or zone:

| Block type | Trigger | Duration | Remediation |
|---|---|---|---|
| `ASSOCIATE_CERT_EXPIRED` | Associate's required certification lapsed | Until recertified | Hawk WC obligation, Cove training bridge |
| `FFL_FORM_PENDING` | ATF Form 4473 not yet complete for this serial | Per-transaction | Complete form before tendering |
| `HOLD_PENDING` | Active LP case hold on this serial number | Until case resolves | LP officer override with case reference |
| `SEASONAL_CLOSED` | Item outside its seasonal sale window | Until window opens | Inform associate; offer substitute if configured |
| `MANAGER_APPROVAL` | High-value or unusual transaction requires manager sign-off | Per-transaction | Manager PIN or biometric |
| `MANUAL_BLOCK` | Hawk operator placed an explicit block (e.g., pending recall investigation) | Until lifted | Hawk case resolution |

---

## Business Rules

1. **Every regulated item must have a `compliance_items` record.** Items with `regulated_class IS NOT NULL` cannot be activated without a compliance record. `item.activate_item()` calls `canary-compliance.check_item_ready()` as a pre-condition gate.

2. **FFL transfer is non-negotiable.** Any item with `ffl_required = true` must verify the store has an active `FFL_DEALER` zone before the transaction can open. This check runs at transaction start, not at tender — the associate must know before they begin the sale.

3. **Serial number is required at commit for firearms.** The compliance service does not store ballistic or ownership data — that lives on the ATF form. The compliance service records that a Form 4473 was initiated for this `(item_id, serial_number, transaction_id)` triple and what its disposition was. This is the minimum evidentiary record.

4. **RUP sales require pesticide applicator license verification.** For `PESTICIDE_APPLICATOR` items, the customer's license number is recorded on the transaction. The compliance service does not validate the license against a state database (that integration is a future enhancement) — it enforces that a license number was presented and recorded.

5. **Seasonal windows are location-aware.** A live chick availability window may differ between a Colorado store and a Montana store due to climate variance. `seasonal_windows` JSONB carries per-zone open/close dates. When no zone override exists, the network-default window applies.

6. **Associate certification is checked against the item class, not per-item.** An associate certified for firearms sales can sell any `FIREARM` class item. The certification table maps `(associate_id, cert_class, expiry_date)` — not individual SKUs.

7. **Every denial is recorded.** `compliance_decisions` table is INSERT-only — denials, overrides, and bypasses all get rows. Compliance posture is always derivable from the decision log.

8. **Overrides require authority and are evidence-linked.** A manager override on a `HOLD_PENDING` block requires a Hawk case reference ID. A manager override on `ASSOCIATE_CERT_EXPIRED` requires a Hawk compliance obligation reference. Overrides without a valid reference are rejected.

9. **Soft-delete only on zone configurations.** Zone deactivation (e.g., FFL license expired) sets `active = false` and `deactivated_at`. Historical transactions that were authorized under the zone remain valid — the record of what was true at the time of transaction is preserved.

10. **Pre-transaction gate, not post-transaction detection.** This service has no Chirp-style alert output. It makes synchronous authorization decisions. Response SLA: < 40ms P99 for in-cache decisions; < 120ms P99 for cold path (DB + associate cert lookup).

---

## Technical

### Service Boundaries

The compliance service owns authorization decisions, regulatory zone configuration, associate certification records, and the compliance decision log. It does not own:

- Item classification source records (`items` table — owned by `item` service)
- Associate employment records (`employees` table — owned by `employee` service)
- Incident case records (`hawk_cases` — owned by `hawk` service)
- Inventory positions — never blocked by this service; `inventory-as-a-service` executes the reservation after compliance authorizes

### Owned Tables

| Table | Purpose |
|---|---|
| `compliance_items` | Per-item compliance attributes — regulated class, age minimums, FFL flag, seasonal windows |
| `compliance_zones` | Per-location regulatory zone membership |
| `compliance_certifications` | Associate-level certification records — class, issuing body, expiry |
| `compliance_decisions` | INSERT-only decision log — every authorization check with outcome and reason |
| `compliance_blocks` | Active manual blocks — operator-placed holds with Hawk case reference |
| `compliance_seasonal_windows` | Network-default seasonal windows, overridden per zone |
| `compliance_overrides` | Logged overrides — authority, reason, case reference, timestamp |

### Data Model

```sql
-- Per-tenant: SET search_path = tenant_{merchant_id}

CREATE TABLE compliance_items (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id               uuid NOT NULL REFERENCES items(id),
    regulated_class       text,  -- FIREARM | NFA_ITEM | HAZMAT | RUP | OTC_PESTICIDE |
                                 -- LIVE_ANIMAL | CONTROLLED_SUBSTANCE | AGE_RESTRICTED
    ffl_required          boolean NOT NULL DEFAULT false,
    age_minimum           int,   -- null = no minimum; 18 or 21
    license_required_buyer text, -- PESTICIDE_APPLICATOR | HUNTING_LICENSE | null
    seasonal_windows      jsonb, -- [{zone_id, open_date, close_date}]
    prohibited_in_zones   text[], -- zone_id values
    notes                 text,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE(item_id)
);

CREATE TABLE compliance_zones (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id       uuid NOT NULL,   -- references identity.locations
    zone_type         text NOT NULL,   -- FFL_DEALER | PESTICIDE_DEALER | LIVE_ANIMAL |
                                       -- AGE_VERIFICATION | HAZMAT_LICENSED | NO_RUP
    license_number    text,            -- state/federal license number for this zone type
    license_expiry    date,            -- null = no expiry required
    active            boolean NOT NULL DEFAULT true,
    activated_at      timestamptz NOT NULL DEFAULT now(),
    deactivated_at    timestamptz,
    notes             text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE(location_id, zone_type)     -- one record per (location, zone_type)
);

CREATE TABLE compliance_certifications (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    associate_id      uuid NOT NULL,   -- references employee.associates
    cert_class        text NOT NULL,   -- FIREARMS_SALES | PESTICIDE_APPLICATOR |
                                       -- LIVE_ANIMAL_HANDLING | HAZMAT_HANDLER
    issuing_body      text NOT NULL,   -- ATF | STATE_AG_DEPT | USDA | company
    cert_number       text,
    issued_at         date NOT NULL,
    expiry_date       date,            -- null = no expiry
    active            boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- INSERT-ONLY — no UPDATE, no DELETE
CREATE TABLE compliance_decisions (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       uuid NOT NULL,
    location_id       uuid NOT NULL,
    item_id           uuid NOT NULL,
    session_id        uuid,            -- store-brain session
    associate_id      uuid,
    customer_id       uuid,            -- null for unknown customers
    transaction_id    uuid,            -- null if decision pre-dates transaction open
    decision          text NOT NULL,   -- AUTHORIZED | RESTRICTED | BLOCKED | OVERRIDE
    factor            text,            -- which factor denied: ITEM | ZONE | OPERATIONAL
    reason_code       text NOT NULL,   -- see reason code table below
    reason_detail     text,
    override_id       uuid REFERENCES compliance_overrides(id),
    decided_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX compliance_decisions_item_decided ON compliance_decisions(item_id, decided_at DESC);
CREATE INDEX compliance_decisions_location ON compliance_decisions(location_id, decided_at DESC);

CREATE TABLE compliance_blocks (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id           uuid,            -- null = block applies to all items in category
    serial_number     text,            -- null = block applies to all serials of item
    location_id       uuid,            -- null = network-wide block
    block_type        text NOT NULL,   -- HOLD_PENDING | MANUAL_BLOCK | RECALL
    hawk_case_id      uuid NOT NULL,   -- Hawk case reference — required, no orphan blocks
    placed_by         uuid NOT NULL,   -- associate_id of LP officer or manager
    reason            text NOT NULL,
    active            boolean NOT NULL DEFAULT true,
    placed_at         timestamptz NOT NULL DEFAULT now(),
    lifted_at         timestamptz,
    lifted_by         uuid
);

CREATE TABLE compliance_seasonal_windows (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    regulated_class   text NOT NULL,   -- which item class this window governs
    zone_id           text,            -- null = network default
    open_month        int NOT NULL,    -- 1–12
    open_day          int NOT NULL,
    close_month       int NOT NULL,
    close_day         int NOT NULL,
    notes             text,
    active            boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE compliance_overrides (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_id       uuid NOT NULL,   -- the RESTRICTED/BLOCKED decision being overridden
    authorized_by     uuid NOT NULL,   -- associate_id — must have manager or LP role
    hawk_case_id      uuid,            -- required for HOLD_PENDING and MANUAL_BLOCK overrides
    hawk_obligation_id uuid,           -- required for ASSOCIATE_CERT_EXPIRED overrides
    reason            text NOT NULL,
    override_class    text NOT NULL,   -- MANAGER_AUTH | LP_OFFICER_AUTH | EMERGENCY
    created_at        timestamptz NOT NULL DEFAULT now()
);
```

### Reason Code Table

| Reason Code | Factor | Description |
|---|---|---|
| `ITEM_NOT_ELIGIBLE_IN_ZONE` | ITEM | Item is in `prohibited_in_zones` for this location |
| `FFL_NOT_ACTIVE` | ZONE | Location does not have an active FFL_DEALER zone |
| `PESTICIDE_LICENSE_NOT_ACTIVE` | ZONE | Location does not have active PESTICIDE_DEALER zone |
| `LIVE_ANIMAL_LICENSE_NOT_ACTIVE` | ZONE | Location does not have LIVE_ANIMAL zone |
| `SEASONAL_WINDOW_CLOSED` | ITEM | Item is outside its seasonal sale window for this zone |
| `AGE_MINIMUM_NOT_VERIFIED` | ITEM | Minimum age not yet verified on this transaction |
| `BUYER_LICENSE_REQUIRED` | ITEM | Customer license number not presented/recorded |
| `ASSOCIATE_CERT_EXPIRED` | OPERATIONAL | Associate's cert for this class has expired or is missing |
| `FFL_FORM_PENDING` | OPERATIONAL | ATF Form 4473 not completed for this serial |
| `SERIAL_ON_HOLD` | OPERATIONAL | Active Hawk case hold on this serial number |
| `ITEM_BLOCKED` | OPERATIONAL | Operator-placed manual block (MANUAL_BLOCK or RECALL type) |
| `MANAGER_APPROVAL_REQUIRED` | OPERATIONAL | Transaction value or type requires manager authorization |
| `ZONE_LICENSE_EXPIRED` | ZONE | Location's zone license has passed expiry date |

---

## MCP Tool Surface

**MCP server:** `canary-compliance`  
**Protocol:** stdio (local) and HTTP SSE (remote) via Chi router

| Tool | Description | SLA |
|---|---|---|
| `check_item_authorization` | Full three-factor authorization decision for a given item × location × associate × customer | < 40ms P99 (warm) / < 120ms P99 (cold) |
| `get_item_compliance_record` | Return full compliance attributes for an item | < 30ms P99 |
| `get_zone_config` | Return all active regulatory zones for a location | < 30ms P99 |
| `check_associate_certifications` | Return certification status for an associate — by class or all | < 30ms P99 |
| `seasonal_window_status` | Return open/closed status for a regulated class at a location and date | < 20ms P99 |
| `list_active_blocks` | List all active operational blocks for a location or item | < 30ms P99 |
| `compliance_export` | Export compliance decision log for a location and date range — for regulatory audit | < 2s for 30-day window |

### `check_item_authorization` Contract

```json
{
  "tool": "check_item_authorization",
  "input": {
    "item_id": "uuid",
    "location_id": "uuid",
    "associate_id": "uuid",
    "customer_id": "uuid | null",
    "serial_number": "string | null",
    "transaction_id": "uuid | null",
    "check_timestamp": "ISO8601 | null"  // defaults to now()
  },
  "output": {
    "authorized": "boolean",
    "decision": "AUTHORIZED | RESTRICTED | BLOCKED",
    "factor": "ITEM | ZONE | OPERATIONAL | null",
    "reason_code": "string | null",
    "reason_detail": "string | null",
    "remediation": {
      "path": "COMPLETE_FFL_FORM | VERIFY_BUYER_AGE | VERIFY_BUYER_LICENSE |
               RECERTIFY_ASSOCIATE | MANAGER_APPROVAL | LP_CASE_REQUIRED | null",
      "description": "string",
      "hawk_incident_type": "string | null"  // which Hawk incident type to open if blocked
    },
    "decision_id": "uuid"  // always recorded; use in override requests
  }
}
```

### Store Brain Integration

Store Brain calls `check_item_authorization` during associate sessions when items are scanned or looked up. The result is attached to the session event stream and surfaced in the Store Operations Suite before the associate has framed the sale. The tool is in the `associate` session's `ToolPermissions` allowlist for any associate with POS or LP role.

For customer kiosk sessions (`unknown_customer` or `loyalty_member` session types), Store Brain calls `seasonal_window_status` and `get_item_compliance_record` but not the full `check_item_authorization` — customer-facing surfaces show availability and season windows, not the internal compliance decision detail.

---

## REST API

All endpoints are per-tenant, authenticated via JWT middleware. `merchant_id` is extracted from the JWT `merchant_id` claim and used to set `search_path`.

### Authorization

```
POST   /v1/compliance/authorize
       Body: { item_id, location_id, associate_id, customer_id?, serial_number?, transaction_id? }
       Response: AuthorizationDecision (same shape as MCP tool output)
```

### Item Compliance Records

```
GET    /v1/compliance/items/{item_id}
POST   /v1/compliance/items           (create compliance record for item)
PATCH  /v1/compliance/items/{item_id} (update compliance attributes)
```

### Zone Management

```
GET    /v1/compliance/zones?location_id={uuid}
POST   /v1/compliance/zones           (add zone to location — onboarding)
PATCH  /v1/compliance/zones/{id}      (deactivate or update license)
```

### Certifications

```
GET    /v1/compliance/certifications?associate_id={uuid}
POST   /v1/compliance/certifications  (record new cert)
PATCH  /v1/compliance/certifications/{id} (mark expired or renewed)
```

### Blocks

```
GET    /v1/compliance/blocks?location_id={uuid}
GET    /v1/compliance/blocks?item_id={uuid}
POST   /v1/compliance/blocks          (place block — hawk_case_id required)
DELETE /v1/compliance/blocks/{id}     (lift block — records lifted_by + lifted_at)
```

### Decision Log

```
GET    /v1/compliance/decisions?location_id={uuid}&from={ISO8601}&to={ISO8601}
GET    /v1/compliance/decisions/{id}
POST   /v1/compliance/decisions/{id}/override  (manager override — hawk_case_id or hawk_obligation_id required)
```

---

## Agent Integration — Canary Agent Contracts

The compliance service exposes three domain agents in the L3 agent network (see `agent-contracts.md`):

### `compliance-gate-agent`

```yaml
contract_id: compliance-gate-agent-v1
parties:
  provider: canary-compliance
  consumer: tsp, store-brain, inventory-as-a-service
trigger: item_scan or transaction_open event for any item with regulated_class != null
input:
  schema: check_item_authorization input (see MCP contract above)
output:
  schema: AuthorizationDecision with remediation path
sla:
  p99_latency_ms: 120
  availability: 99.9%
autonomous_scope:
  - Call compliance_decisions INSERT on every check
  - Attach decision_id to transaction envelope
  - Publish denial events to canary:compliance:denials Valkey stream
escalation_path:
  - On RESTRICTED/BLOCKED: surface remediation path to associate via store-brain session
  - On FFL_NOT_ACTIVE or ZONE_LICENSE_EXPIRED: escalate to manager session immediately
actor_type: agent
evidence_requirement: compliance_decisions row + optional blockchain anchor
```

### `cert-expiry-monitor-agent`

```yaml
contract_id: cert-expiry-monitor-agent-v1
parties:
  provider: canary-compliance
  consumer: hawk, store-brain
trigger: scheduled daily at 06:00 merchant local time
input:
  schema: { location_id, lookahead_days: 30 }
output:
  schema: { expiring: [{associate_id, cert_class, expiry_date, days_remaining}] }
sla:
  p99_latency_ms: 5000  -- batch, non-realtime
  availability: 99%
autonomous_scope:
  - Query compliance_certifications for certs expiring within lookahead_days
  - Create Hawk compliance_obligation records for each expiring cert (WC-10 class)
  - Publish to store-brain occupancy context for shift-start associate sessions
escalation_path:
  - Cert expired (days_remaining <= 0): ASSOCIATE_CERT_EXPIRED block placed immediately
  - Warning horizon (days_remaining <= 14): Cove training bridge notification
actor_type: agent
evidence_requirement: hawk_compliance_obligations row per associate per cert
```

### `zone-license-monitor-agent`

```yaml
contract_id: zone-license-monitor-agent-v1
parties:
  provider: canary-compliance
  consumer: hawk, ops-dashboard
trigger: scheduled daily at 06:00 merchant local time
input:
  schema: { merchant_id, lookahead_days: 60 }
output:
  schema: { expiring_zones: [{location_id, zone_type, license_expiry, days_remaining}] }
sla:
  p99_latency_ms: 5000
  availability: 99%
autonomous_scope:
  - Query compliance_zones where license_expiry within lookahead_days
  - Create Hawk compliance_obligation records (incident_type: compliance_license_renewal)
  - Surface warning in ops-dashboard compliance posture panel
escalation_path:
  - License expired: deactivate zone, place ZONE_LICENSE_EXPIRED blocks on all items requiring that zone
  - FFL expiry approaching: escalate to Urgent Hawk case immediately
actor_type: agent
evidence_requirement: hawk_compliance_obligations + compliance_decisions for any blocks placed
```

---

## Cove Governance Bridge

The compliance service is the primary Canary ↔ Cove integration point for stores operating as governed entities. The mapping is direct:

| Canary Concept | Cove Concept |
|---|---|
| Store location | Cove governed parcel |
| Regulatory zone license | Cove governing document (SOP) |
| Associate certification | Cove assessment obligation |
| Cert expiry notification | Cove due-date reminder |
| DM compliance review | Cove board communication thread |
| Network compliance export | Cove audit record |

**Associate certification training flow (WC-10 integration):**

1. `cert-expiry-monitor-agent` detects cert expiring in ≤ 14 days
2. Hawk `compliance_obligation` record created with `due_date = expiry_date`
3. Store Brain surfaces obligation in associate's shift-start session context
4. Cove Training tab in Store Operations Suite displays obligation with linked SOP
5. Associate completes training, manager records completion
6. `compliance_certifications.expiry_date` updated via PATCH `/v1/compliance/certifications/{id}`
7. `ASSOCIATE_CERT_EXPIRED` block cleared automatically by cert-expiry-monitor-agent on next run

---

## Hawk Incident Type Mapping

When `check_item_authorization` returns RESTRICTED or BLOCKED, the `remediation.hawk_incident_type` field tells the calling system which Hawk incident type to open. This creates the evidence chain link between the denial event and the resulting case.

| Reason Code | Hawk Incident Type | Track |
|---|---|---|
| `FFL_NOT_ACTIVE` | `compliance_license_lapsed` | DE |
| `ZONE_LICENSE_EXPIRED` | `compliance_license_lapsed` | DE |
| `ASSOCIATE_CERT_EXPIRED` | `compliance_training_gap` | DE |
| `SERIAL_ON_HOLD` | references existing case — no new case | — |
| `ITEM_BLOCKED` | `compliance_recall_hold` | DE |
| `BUYER_LICENSE_REQUIRED` | `compliance_buyer_verification` | DE |
| `AGE_MINIMUM_NOT_VERIFIED` | `compliance_age_verification` | DE |
| `FFL_FORM_PENDING` | `firearm_transfer_pending` | DE |

---

## Work Card Coverage

This service is the authorization backbone for Murdoch's work cards WC-01 through WC-08:

| Work Card | Item Class | Primary Factor | Compliance Service Gate |
|---|---|---|---|
| WC-01 Firearms Sale | FIREARM | ZONE: FFL_DEALER + OPERATIONAL: FFL_FORM_PENDING | `check_item_authorization` at scan |
| WC-02 Ag Chemical Sale | RUP | ZONE: PESTICIDE_DEALER + ITEM: BUYER_LICENSE_REQUIRED | `check_item_authorization` at sale open |
| WC-03 Live Animal Sale | LIVE_ANIMAL | ITEM: SEASONAL_WINDOW + ZONE: LIVE_ANIMAL | `seasonal_window_status` + full auth at tender |
| WC-04 Returns Processing | varies | OPERATIONAL: HOLD_PENDING on serial | `check_item_authorization` for serialized returns |
| WC-05 BOPIS Fulfillment | all | ITEM: full authorization before pick confirmation | `check_item_authorization` before BOPIS status → ready |
| WC-06 Receiving | HAZMAT | ZONE: HAZMAT_LICENSED + associate HAZMAT_HANDLER cert | `check_associate_certifications` before receiving |
| WC-07 Inventory Audit | all | Shrink writeoff requires LP case reference | Compliance gate on writeoff authorization |
| WC-08 Promotions | AGE_RESTRICTED | ITEM: age restriction gate before discount applies | `check_item_authorization` before promotion application |

---

## Service Registration

**Module layout addition to `go-module-layout.md`:**
```
├── compliance/  main.go  # :9091  Item authorization × regulatory zone × operational blocks
```

**Valkey streams published:**
- `canary:compliance:denials` — every RESTRICTED/BLOCKED decision; consumed by store-brain (session context), ops-dashboard (compliance panel), SNI (cross-location signal)
- `canary:compliance:cert_alerts` — cert-expiry-monitor-agent output; consumed by hawk (obligation creation), store-brain (shift-start context)

**Valkey cache keys:**
- `compliance:item:{merchant_id}:{item_id}` — TTL 5 minutes — item eligibility attributes
- `compliance:zones:{merchant_id}:{location_id}` — TTL 30 minutes — location zone set
- `compliance:certs:{merchant_id}:{associate_id}` — TTL 5 minutes — associate cert status
- `compliance:windows:{merchant_id}:{regulated_class}:{location_id}:{date}` — TTL 1 hour — seasonal window status
- Cache invalidated on any PATCH to corresponding records

---

## Migration

**Migration file:** `deploy/migrations/compliance/compliance_a00001.up.sql`

Runs against `canary` database. Tables land in `tenant_{merchant_id}` schema per the standard tenant isolation pattern. Migration seeds the `compliance_seasonal_windows` table with network defaults for `LIVE_ANIMAL` (chick season: Mar 15 – May 31) and `AGE_RESTRICTED` (no seasonal window — always on).

---

## Open Questions

| # | Question | Owner | Priority |
|---|---|---|---|
| 1 | BOPIS fulfillment path — does `inventory-as-a-service.reserve_inventory` call compliance synchronously before confirming the hold, or does the BOPIS confirmation webhook trigger a post-reservation compliance check? Synchronous pre-commit is preferred; verify with IaaS team. | Architecture | High |
| 2 | State pesticide license validation API — WY, CO, MT, TX each have state ag department APIs for license lookup. Should the compliance service integrate these for real-time validation, or accept operator-entered license numbers on trust? Phase I: operator-entered. Phase II: state API integration. | Product | Medium |
| 3 | ATF Form 4473 — does Canary generate or track the form content, or does it simply record that a form was initiated? Tracking form content creates significant regulatory data handling obligations. Recommended posture: record `(item_id, serial_number, transaction_id, form_initiated_at)` only. | Legal/Product | High |
| 4 | Multi-state FFL transfers — some Murdoch's customers purchase in one state but take delivery in another. The compliance gate currently evaluates the selling location's FFL. Cross-state transfer compliance (receiving FFL) is out of scope for Phase I. | Product | Low |
| 5 | Counterpoint `IM_ITEM.ITEM_RESTRICT_SALE` field — NCR Counterpoint has a native sale restriction flag on the item master. Should the edge agent sync this to `compliance_items.prohibited_in_zones`, or should compliance be maintained independently in Canary? Independent ownership is cleaner; sync on initial import only. | NCR Channel | Medium |
