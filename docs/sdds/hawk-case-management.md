---
spec-version: 1.1
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: handoff-ready
updated: 2026-04-28
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Hawk — Square POS Adapter (Reference Implementation of the POS Adapter Substrate)

**Implements:** pos-adapter-substrate.
**Feeds:** webhook-pipeline (events), TSP (parsed CRDM).

> **Role in the system:** Hawk is Canary's ops-contract case management system and card factory. It is also the go-to reference for how a complete, production-grade service is wired into the Canary platform: data model in four tiers, MCP tool surface, REST endpoints, FSM enforcement, and pgvector-backed recall. Square adapter implementation is covered in the POS Adapter Substrate SDD.

**Predecessor:** Fox (flat case model). Fox evidence tables remain operational alongside Hawk — they are not dropped or migrated.
**Migration:** `hawk_a00001` (schema + seed data live on `canary` DB)

---

## Purpose

Hawk introduces incident-typed case management where Fox had generic cases. The additions over Fox:

**Multi-tenant context.** Hawk tables (`hawk_cases`, `hawk_subjects`, `hawk_actions`, `hawk_compliance_obligations`, `hawk_cards`) live per-tenant in `tenant_{merchant_id}`. Cases are merchant-scoped; the wizard FSM, card factory, and compliance obligation tracking all operate within tenant boundaries. Cross-tenant compliance reporting (e.g., platform-wide regulatory submissions) flows through `analytics` schema rollups. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Hawk operates with all Optional Features (per `platform-overview.md`) disabled. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, every card generation and case status transition is anchored to a public L2 (per `infra-blockchain-evidence-anchor` Brain wiki and `blockchain-anchor.md` SDD) — making the case timeline externally verifiable for court / insurer / regulator. Anchor failures are non-blocking. When the flag is off, the case timeline remains internally hash-chained via the underlying Fox evidence locker.

| Dimension | Fox | Hawk |
|---|---|---|
| Case typing | Generic `case_type` enum | 63 incident types across 5 classes with wizard templates |
| Action model | Free-text action types | Coded actions on typed tracks derived from incident class |
| Resolution track | Single status machine | Dual-track: internal (DE) and external (PV) |
| Compliance | None | `hawk_compliance_obligations` with due dates and filing status |
| Card output | None | Structured Markdown cards with JSONB frontmatter, versioning, pgvector embedding |
| Wizard UI | None | Per-incident-type JSONB templates driving form field generation |

Fox's INSERT-only evidence tables (`fox_evidence`, `fox_evidence_access_log`), hash-chain integrity triggers, and access-logging discipline remain the evidentiary backbone. Hawk adds investigation structure on top.

---

## Dependencies

| Dependency | Required | Purpose |
|---|:---:|---|
| PostgreSQL 17 (`canary` DB, `app` schema) | Yes | All hawk_ tables, pgvector for card embeddings |
| Fox evidence tables | Yes | `fox_evidence`, `fox_evidence_access_log` — linked via `fox_case_id` |
| Valkey (DB 0) | Yes | Session backend for JWT auth |
| JWT middleware | Yes | Authentication and RBAC |
| MCP tool registry | Yes | Tool registration and endpoint generation |
| Embedding service (HTTP API) | Soft | Card embedding — async, non-blocking |

---

## [ARCHITECTURAL DIRECTION — not yet implemented] ILDWAC Dimension Mapping — Square Payload Fields

Hawk (Square adapter) is responsible for populating the `pos_port` and `device_id` envelope fields defined in the POS Adapter Substrate SDD. These fields are the Port and Device dimensions of the IL(Device/MCP/Port/)WAC cost model.

### Square → ILDWAC Field Map

| ILDWAC Dimension | Envelope Field | Square Source Field | Notes |
|---|---|---|---|
| Port | `pos_port` | — (hardcoded) | Always `"square"` for all events from this adapter |
| Device | `device_id` | `payment.device_details.device_id` | Present on payment events; null for events without device context (loyalty, disputes, payouts) |

### Square Payload Field Details

| Square Event Type | Device Field Path | Notes |
|---|---|---|
| `payment.created`, `payment.updated` | `data.object.payment.device_details.device_id` | Square Reader or Terminal device ID; present when card-present transaction |
| `order.created`, `order.updated` | `data.object.order.fulfillments[].pickup_details.placed_at` | No device_id in order events — set `device_id = null` |
| `refund.created` | `data.object.refund.device_id` | Present if refund initiated on device; may be null for manual refunds |
| `cash_drawer.shift.closed` | `data.object.cash_drawer_shift.device_name` | Device name, not device_id — do NOT use as `device_id`; set `device_id = null` |
| All other event types | — | Set `device_id = null` |

**Extraction rule:** If `device_id` is absent from the payload or resolves to an empty string, set the envelope field to null — never an empty string.

### Why This Matters

A payment processed on a fixed Square Terminal (`device_id = "TERMINAL_ABC123"`) authorized by the `register_source` MCP tool produces a distinct ILDWAC provenance signature from the same item processed on a Square Reader mobile device. The cost model can attribute inventory adjustments to specific hardware endpoints. See `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md`.

---

## Data Model

All tables in the `app` schema. Eight tables in four logical tiers. Migration: `hawk_a00001`.

### Schema Contract Summary

| Table | Tier | Purpose | Access Pattern |
|---|---|---|---|
| `hawk_incident_types` | 0 — Reference | 63 incident type definitions | Read-only after seed |
| `hawk_sources` | 0 — Reference | 31 case origin sources | Read-only after seed |
| `hawk_cases` | 1 — Investigation | Root investigation record | Read/Write, tenant-scoped |
| `hawk_subjects` | 1 — Investigation | Persons/entities of interest | Read/Write, case-scoped |
| `hawk_actions` | 1 — Investigation | Coded investigation actions | Read/Write, case-scoped |
| `hawk_compliance_obligations` | 1 — Investigation | Regulatory/policy obligations | Read/Write, case-scoped |
| `hawk_timeline` | 2 — Audit Trail | Append-only event log | INSERT-only |
| `hawk_cards` | 3 — Card Factory | Versioned case summary documents | Insert + soft invalidate |

---

### Tier 0 — Reference Data (Seed-Populated)

#### `hawk_incident_types`

Defines the 63 incident types that structure every Hawk case.

```sql
CREATE TABLE app.hawk_incident_types (
    type_code        TEXT        PRIMARY KEY,
    -- e.g., 'cash_theft', 'grab_and_run', 'register_discrepancies'

    incident_class   TEXT        NOT NULL,
    -- 'critical_smart_alert' | 'external' | 'internal_de' | 'internal_pv' | 'incident'

    de_pv_flag       TEXT,
    -- 'de' | 'pv' | NULL — determines action track

    wizard_template  JSONB       NOT NULL DEFAULT '{}',
    -- Form field definitions per incident type. Shape varies.
    -- Example field: {"name": "drawer_id", "type": "text", "required": true, "label": "Cash Drawer ID"}

    resolution_track TEXT        NOT NULL,
    -- 'internal' | 'external' | 'incident'

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Seed data: 63 rows across 5 classes**

| Class | Count | Examples |
|---|:---:|---|
| `critical_smart_alert` | 17 | `bulk_sweeper`, `grab_and_run`, `register_manipulation`, `organized_retail_crime` |
| `external` | 7 | `shoplifting_adult`, `shoplifting_juvenile`, `robbery`, `burglary` |
| `internal_de` | 15 | `cash_theft`, `merchandise_theft`, `time_theft`, `vendor_fraud` |
| `internal_pv` | 15 | `register_discrepancies`, `procedural_non_compliance`, `safety_violation` |
| `incident` | 9 | `slip_and_fall`, `property_damage`, `medical_emergency` |

#### `hawk_sources`

Where the case originated.

```sql
CREATE TABLE app.hawk_sources (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_code  TEXT        NOT NULL UNIQUE,
    -- e.g., 'CCTV', 'EBR_TRANSACTION_REVIEW', 'TIP_ANONYMOUS'

    source_class TEXT        NOT NULL,
    -- 'surveillance' | 'electronic' | 'human' | 'audit' | 'external'

    display_name TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Seed data: 31 rows across 5 classes**

| Class | Count | Examples |
|---|:---:|---|
| `surveillance` | 4 | `CCTV`, `LPTV`, `BODY_CAMERA`, `COVERT_CAMERA` |
| `electronic` | 7 | `EBR_TRANSACTION_REVIEW`, `EBR_REFUND_ANALYSIS`, `EAS_ALARM`, `INVENTORY_AUDIT_SYSTEM` |
| `human` | 8 | `TIP_ANONYMOUS`, `TIP_NAMED`, `OBSERVATION_EMPLOYEE`, `OBSERVATION_CUSTOMER` |
| `audit` | 7 | `AUDIT_FINDING`, `CASH_AUDIT`, `INVENTORY_AUDIT`, `RECEIVING_AUDIT` |
| `external` | 5 | `POLICE_REPORT`, `VENDOR_REPORT`, `INSURANCE_CLAIM`, `CUSTOMER_COMPLAINT` |

---

### Tier 1 — Investigation Records

#### `hawk_cases`

Root investigation record. One row per investigation.

```sql
CREATE TABLE app.hawk_cases (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID        NOT NULL REFERENCES app.merchants(id),
    location_id     UUID        REFERENCES app.locations(id),
    incident_class  TEXT        NOT NULL,
    -- Denormalized from incident_type for fast filtering
    incident_type   TEXT        NOT NULL REFERENCES app.hawk_incident_types(type_code),
    case_status     TEXT        NOT NULL DEFAULT 'open',
    -- Valid values: 'open' | 'investigating' | 'pending_review' | 'escalated' | 'closed' | 'referred_to_le'
    source_code     TEXT        REFERENCES app.hawk_sources(source_code),
    narrative       TEXT,
    assigned_to     UUID        REFERENCES app.users(id),
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ,
    card_id         UUID        REFERENCES app.hawk_cards(id),
    -- Set when first card is generated. Updated to latest valid card_id on regeneration.
    fox_case_id     UUID        REFERENCES app.fox_cases(id),
    -- Optional backward link to Fox evidence chain.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hawk_cases_merchant_status    ON app.hawk_cases (merchant_id, case_status);
CREATE INDEX idx_hawk_cases_merchant_class     ON app.hawk_cases (merchant_id, incident_class);
CREATE INDEX idx_hawk_cases_incident_type      ON app.hawk_cases (incident_type);
CREATE INDEX idx_hawk_cases_merchant_opened    ON app.hawk_cases (merchant_id, opened_at DESC);
```

#### `hawk_subjects`

Persons/entities of interest linked to a case.

```sql
CREATE TABLE app.hawk_subjects (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id          UUID        NOT NULL REFERENCES app.hawk_cases(id),
    subject_type     TEXT        NOT NULL,
    -- 'employee' | 'vendor' | 'external'
    employee_id      UUID        REFERENCES app.employees(id),
    vendor_entity_id UUID,
    external_name    TEXT,
    -- Exactly one of employee_id / vendor_entity_id / external_name must be non-null.
    -- Enforced by CHECK constraint:
    CONSTRAINT chk_exactly_one_identifier CHECK (
        (CASE WHEN employee_id IS NOT NULL THEN 1 ELSE 0 END
         + CASE WHEN vendor_entity_id IS NOT NULL THEN 1 ELSE 0 END
         + CASE WHEN external_name IS NOT NULL THEN 1 ELSE 0 END) = 1
    ),
    notes            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hawk_subjects_case ON app.hawk_subjects (case_id);
```

#### `hawk_actions`

Investigation actions on coded tracks.

```sql
CREATE TABLE app.hawk_actions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id       UUID        NOT NULL REFERENCES app.hawk_cases(id),
    action_code   TEXT        NOT NULL,
    -- Internal track: CLOSED_UNFOUNDED | CORRECTIVE_ACTION | INTERVIEWED_NO_CASE |
    --                 QUIT_PRIOR_TO_INTERVIEW | QUIT_PRIOR_TO_RESOLUTION |
    --                 REPORTED_TO_ATF | TERMINATED_NO_PROSECUTION |
    --                 TERMINATED_WITH_PROSECUTION | UNDER_INVESTIGATION
    -- External track: CLOSED_UNFOUNDED | PROSECUTED | RELEASED_TO_ADULT |
    --                 RELEASED_TO_GUARDIAN | RELEASED_TO_POLICE | UNDER_INVESTIGATION
    action_track  TEXT        NOT NULL,
    -- 'internal' | 'external' — derived from incident class at validation, not caller-supplied
    actioned_by   UUID        NOT NULL REFERENCES app.users(id),
    actioned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hawk_actions_case ON app.hawk_actions (case_id);
```

**Action track validation:** When inserting a `hawk_actions` row, the service layer must validate that the `action_code` is permitted for the case's `incident_class`:

| Incident class | Permitted track | Action codes |
|---|---|---|
| `internal_de` | internal | CLOSED_UNFOUNDED, CORRECTIVE_ACTION, INTERVIEWED_NO_CASE, QUIT_PRIOR_TO_INTERVIEW, QUIT_PRIOR_TO_RESOLUTION, REPORTED_TO_ATF, TERMINATED_NO_PROSECUTION, TERMINATED_WITH_PROSECUTION, UNDER_INVESTIGATION |
| `external` | external | CLOSED_UNFOUNDED, PROSECUTED, RELEASED_TO_ADULT, RELEASED_TO_GUARDIAN, RELEASED_TO_POLICE, UNDER_INVESTIGATION |
| `internal_pv` | internal | Same as `internal_de` |
| `critical_smart_alert` | either | Both track code sets permitted |
| `incident` | either | Both track code sets permitted |

#### `hawk_compliance_obligations`

Regulatory/policy obligations attached to cases.

```sql
CREATE TABLE app.hawk_compliance_obligations (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id          UUID        NOT NULL REFERENCES app.hawk_cases(id),
    obligation_type  TEXT        NOT NULL,
    -- e.g., 'police_report_filing' | 'insurance_claim' | 'hr_documentation'
    due_date         DATE,
    filed_at         TIMESTAMPTZ,   -- Set when obligation is satisfied
    notes            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hawk_obligations_case ON app.hawk_compliance_obligations (case_id);
```

---

### Tier 2 — Immutable Audit Trail

#### `hawk_timeline`

Append-only event log. No UPDATE or DELETE is permitted on this table. Enforce with a trigger.

```sql
CREATE TABLE app.hawk_timeline (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id      UUID        NOT NULL REFERENCES app.hawk_cases(id),
    merchant_id  UUID        NOT NULL REFERENCES app.merchants(id),
    event_type   TEXT        NOT NULL,
    -- 'created' | 'status_change' | 'subject_added' | 'action_taken' |
    -- 'card_generated' | 'obligation_created' | 'obligation_filed' | 'note_added'
    actor_id     UUID        REFERENCES app.users(id),
    description  TEXT,
    event_data   JSONB,      -- Structured metadata per event_type
    -- status_change: {"old_status": "open", "new_status": "investigating"}
    -- card_generated: {"card_id": "...", "card_version": 2}
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now()
    -- No created_at/updated_at — occurred_at is the immutable timestamp
);

CREATE INDEX idx_hawk_timeline_case        ON app.hawk_timeline (case_id, occurred_at DESC);
CREATE INDEX idx_hawk_timeline_merchant    ON app.hawk_timeline (merchant_id, occurred_at DESC);

-- INSERT-only trigger (add to migration hawk_a00002 if not in hawk_a00001)
CREATE OR REPLACE FUNCTION prevent_hawk_timeline_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'hawk_timeline is append-only: UPDATE and DELETE are not permitted';
END;
$$;

CREATE TRIGGER hawk_timeline_immutable
BEFORE UPDATE OR DELETE ON app.hawk_timeline
FOR EACH ROW EXECUTE FUNCTION prevent_hawk_timeline_mutation();
```

---

### Tier 3 — Card Factory

#### `hawk_cards`

Versioned case summary documents with pgvector embedding for Owl recall.

```sql
CREATE TABLE app.hawk_cards (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id         UUID         NOT NULL REFERENCES app.hawk_cases(id),
    card_body       TEXT         NOT NULL,  -- Markdown narrative
    frontmatter     JSONB        NOT NULL,
    -- Structured metadata. Required fields:
    -- {
    --   "card_type": "investigation_summary",
    --   "case_id": "...",
    --   "merchant_id": "...",
    --   "incident_class": "internal_de",
    --   "de_pv_flag": "de",
    --   "subject_types": ["employee"],
    --   "generated_by": "system"
    -- }
    card_version    INTEGER      NOT NULL DEFAULT 1,
    generated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    invalidated_at  TIMESTAMPTZ,
    -- NULL = current valid version. Set to now() when a newer version is generated.
    -- Soft invalidation only — cards are never deleted.
    vector          VECTOR(1024),
    -- pgvector embedding (1024-dim) for Owl memory bus recall.
    -- Populated asynchronously by the embedding service after card creation.
    -- May be NULL for newly created cards. Owl recall filters WHERE invalidated_at IS NULL.
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_hawk_cards_case         ON app.hawk_cards (case_id, card_version DESC);
CREATE INDEX idx_hawk_cards_current      ON app.hawk_cards (case_id) WHERE invalidated_at IS NULL;
-- pgvector index for Owl recall:
CREATE INDEX idx_hawk_cards_vector       ON app.hawk_cards
    USING ivfflat (vector vector_cosine_ops)
    WHERE invalidated_at IS NULL;
```

**Vector search contract (Owl recall surface):**
```sql
SELECT id, case_id, card_body, frontmatter,
       1 - (vector <=> $1::vector) AS similarity
FROM app.hawk_cards
WHERE invalidated_at IS NULL
ORDER BY vector <=> $1::vector
LIMIT $2;
```
`$1` = query embedding (1024-dim float array), `$2` = result limit.

Filtered recall using frontmatter fields:
```sql
WHERE invalidated_at IS NULL
  AND (frontmatter->>'incident_class') = $incident_class
  AND (frontmatter->>'de_pv_flag') = $de_pv_flag
ORDER BY vector <=> $query_vector::vector
LIMIT $limit;
```

---

## Workflows

### Case Lifecycle FSM

```
open → investigating
investigating → pending_review | escalated
pending_review → escalated | closed | referred_to_le
escalated → closed | referred_to_le
closed          (terminal)
referred_to_le  (terminal)
```

**FSM validation table:**

| From | To | Permitted |
|---|---|:---:|
| `open` | `investigating` | Yes |
| `investigating` | `pending_review` | Yes |
| `investigating` | `escalated` | Yes |
| `pending_review` | `escalated` | Yes |
| `pending_review` | `closed` | Yes |
| `pending_review` | `referred_to_le` | Yes |
| `escalated` | `closed` | Yes |
| `escalated` | `referred_to_le` | Yes |
| any | `open` | No |
| terminal → any | — | No |

Every valid transition writes a `hawk_timeline` row with `event_type = 'status_change'` and `event_data = {"old_status": "...", "new_status": "..."}`.

### Card Generation Pipeline

1. `generate_card(case_id, actor_id)` tool invoked.
2. Query: case + all subjects + all actions + timeline summary (last 10 events).
3. Render Markdown narrative from template (incident-type-aware; template varies by `incident_class`).
4. Build JSONB frontmatter with required fields.
5. If previous valid card exists (`invalidated_at IS NULL`): set `invalidated_at = now()`.
6. INSERT new `hawk_cards` row with `card_version = MAX(previous version) + 1`.
7. Write `hawk_timeline` row with `event_type = 'card_generated'`, `event_data = {"card_id": "...", "card_version": N}`.
8. Return `{card_id, card_version, card_body_preview}`.
9. **Asynchronously:** POST card_body to embedding service → store returned vector in `hawk_cards.vector`. This step must not block the API response.

**Embedding service call (async, non-blocking):**
```
POST {EMBEDDING_BASE_URL}/api/embeddings
Content-Type: application/json

{
  "model": "<configured embedding model>",
  "prompt": "<card_body text>"
}

Response: { "embedding": [float64 × 1024] }

On success: UPDATE hawk_cards SET vector = $embedding WHERE id = $card_id
On failure: Log warning; card persists without vector; retry on next memory bus cycle.
```

### Wizard Template Resolution

Each incident type carries a `wizard_template` JSONB field. The `get_wizard_template` tool returns this field for a given `incident_type`. The client uses this to render the case creation form. Template fields are incident-type-specific — a `grab_and_run` wizard asks for entry/exit points, while a `register_discrepancies` wizard asks for drawer ID and variance amount.

---

## API Contract

### MCP Tools (`canary-hawk` — `/hawk/*`)

9 tools registered with the MCP tool registry.

| Tool | Category | Required Params | Description |
|---|---|---|---|
| `create_case` | cases | `merchant_id`, `incident_type`, `narrative`, `source_code` | Open a case. Validates `incident_type` against seed data. Returns case object. |
| `get_case` | cases | `case_id` | Full case details — incident type, status, subjects, actions, card reference. |
| `list_cases` | cases | `merchant_id` | List cases. Optional filters: `status`, `incident_class`, `limit` (default 20, max 100). |
| `advance_workflow` | cases | `case_id`, `new_status`, `actor_id` | Transition status through FSM. Invalid transitions return 400. Writes timeline. |
| `add_subject` | cases | `case_id`, `subject_type`, one of (`employee_id` / `vendor_entity_id` / `external_name`) | Link subject to case. Validates exactly-one-identifier. |
| `add_action` | cases | `case_id`, `action_code`, `actioned_by` | Add coded action. Validates `action_code` against incident class. Writes timeline. |
| `generate_card` | cards | `case_id`, `actor_id` | Generate/regenerate summary card. Versions and soft-invalidates previous. |
| `get_timeline` | cases | `case_id` | Append-only event log for a case. |
| `get_wizard_template` | reference | `incident_type` | Return `wizard_template` JSONB for the given incident type. |

### MCP Blueprint Endpoints

**Base path:** `/hawk`

| Method | Path | Auth | Description |
|---|---|:---:|---|
| GET | `/hawk/manifest` | No | Server manifest (name, version, tool count) |
| GET | `/hawk/tools` | No | List all 9 tools with schemas |
| POST | `/hawk/tools/<name>` | JWT | Invoke a tool by name |
| GET | `/hawk/health` | No | Service health check |

**Health response:**
```json
{
  "service": "canary-hawk",
  "healthy": true,
  "tools": 9,
  "seed_data": {
    "incident_types": 63,
    "sources": 31
  }
}
```

### REST Endpoints

**Base path:** `/api/hawk`

REST endpoints mirror the MCP tool surface for UI/BFF consumption. All require JWT authentication. Tenant isolation is enforced by `merchant_id` from the JWT — callers cannot supply a different `merchant_id`.

| Method | Path | Description |
|---|---|---|
| POST | `/api/hawk/cases` | Create case (`create_case` equivalent) |
| GET | `/api/hawk/cases/:id` | Get case (`get_case` equivalent) |
| GET | `/api/hawk/cases?merchant_id=&status=&class=` | List cases (`list_cases` equivalent) |
| POST | `/api/hawk/cases/:id/advance` | Advance FSM (`advance_workflow` equivalent) |
| POST | `/api/hawk/cases/:id/subjects` | Add subject (`add_subject` equivalent) |
| POST | `/api/hawk/cases/:id/actions` | Add action (`add_action` equivalent) |
| POST | `/api/hawk/cases/:id/card` | Generate card (`generate_card` equivalent) |
| GET | `/api/hawk/cases/:id/timeline` | Get timeline (`get_timeline` equivalent) |
| GET | `/api/hawk/incident-types/:type_code/wizard` | Get wizard template |

---

## Operations

### Service Startup

Hawk registers its route groups during Canary HTTP server startup. Seed data (63 incident types, 31 sources) is verified at startup — missing seed rows emit a WARNING log, not a crash. Startup sequence:

1. Register MCP tool registry entries (9 tools).
2. Register REST route group under `/api/hawk`.
3. Register MCP route group under `/hawk`.
4. Verify seed data count. Log WARNING if `hawk_incident_types` count ≠ 63 or `hawk_sources` count ≠ 31.

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| PostgreSQL down | All Hawk operations fail | 503 on all endpoints |
| Missing seed data | Case creation fails for unknown `incident_type` | 400 with `"unknown incident type: <code>"` |
| Card generation with no subjects/actions | Card generated but sparse | Permitted — card body reflects empty investigation state |
| Embedding service unavailable | Card embedding deferred | Card persists with `vector = NULL`; embedding retried on next memory bus cycle |
| Fox evidence tables missing | Evidence linking fails | Hawk cases can still be created; evidence chain operations return 503 |

### Configuration

No Hawk-specific environment variables. All config inherited from Canary application. Seed data loaded by migration `hawk_a00001`.

---

## Relationship to Fox

Hawk does not drop or replace Fox tables.

1. **`hawk_cases.fox_case_id`** — optional FK to `fox_cases.id`. When a Hawk case needs evidence chain operations, it links to a Fox case. Fox's INSERT-only evidence tables handle all evidentiary storage.
2. **Fox remains operational** for existing cases. No migration of historical Fox cases to Hawk.
3. **New cases** created via Hawk MCP tools or UI get incident-typed structure. Evidence attachment routes through Fox's evidence chain.
4. Fox's INSERT-only trigger, hash-chain integrity, and access-logging discipline are preserved unchanged.

---

## Production Readiness Checklist

- [x] Schema deployed — `hawk_a00001` migration live
- [x] Seed data loaded — 63 incident types, 31 sources
- [x] FSM validated — status transitions enforced at service layer
- [x] Fox evidence chain preserved — INSERT-only triggers unchanged
- [ ] MCP tools implemented — pending (service + route code)
- [ ] REST endpoints implemented — pending
- [ ] Card generation pipeline implemented — pending
- [ ] Card embedding wired to embedding service — pending
- [ ] Hawk timeline INSERT-only trigger — pending (add to migration if not in hawk_a00001)
- [ ] Wizard template UI consumption — pending (frontend)
- [ ] Rate limiting on MCP and REST endpoints — pending
- [ ] Audit logging for case access — pending
