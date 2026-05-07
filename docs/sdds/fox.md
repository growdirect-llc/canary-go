---
spec-version: 1.1
updated: 2026-04-28
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
source: Curated from Canary Python prototype SDDs (GRO-617)
status: handoff-ready
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Fox — Case Management & Evidence Locker

## Purpose

Fox is Canary's case management and evidence locker domain — "The Vault." It bridges automated anomaly detection (Chirp alerts) and human investigation workflows. When a merchant decides an alert warrants investigation, Fox creates a case, links originating alerts, tracks subjects of interest, stores evidence with cryptographic chain-of-custody integrity, and maintains an append-only audit timeline.

**Multi-tenant context.** Fox tables (`fox_cases`, `fox_subjects`, `fox_evidence`, `fox_timeline`, `fox_evidence_access_log`) live per-tenant in `tenant_{merchant_id}`. Evidence chain integrity is per-tenant — every merchant's chain is isolated. Cross-tenant ORC pattern correlation surfaces through the Local Market Agent and `signal-social-threat` (per the Brain wiki cards), not via cross-tenant Fox queries. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Fox's hash-chained evidence locker is required core — chain integrity is the platform's evidentiary backbone (per `raas.md`) and operates independently of all Optional Features. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, every case's chain hash is anchored asynchronously to a public L2 (per `blockchain-anchor.md`) — making the case timeline externally verifiable for court / insurer / auditor without trusting the platform. When the flag is off, the chain remains internally verifiable. Failures of the anchor service are non-blocking by design.

### Hawk Positioning (Phase 1+)

Fox is an **evidence-based record (EBR)** class inside the Hawk ops-contract system. Hawk introduces a card-based investigation model with a wizard FSM, structured card pipeline, and multi-entity tracking that supersedes Fox's flat case lifecycle. Fox's INSERT-only evidence chain, hash-chain integrity, and access-logging disciplines carry forward unchanged as the evidentiary backbone of every Hawk card. See `docs/sdds/go-handoff/hawk.md` for the full ops-contract specification.

## Dependencies

| Dependency | Type | Required | Purpose |
|---|---|---|---|
| PostgreSQL 17 (`canary` DB, `app` schema) | Database | Yes | All Fox tables, INSERT-ONLY triggers, `verify_hash_chain()` function |
| Valkey (DB 0) | Cache | Yes | Session backend for auth |
| JWT middleware | Internal | Yes | Authentication and RBAC on all routes |
| Alert domain (`alert_history`) | Internal | Soft | Writes alert history when linking alerts to cases |
| Employee records | Internal | Soft | Entity resolution for employee subjects |

## Data Flow & PII Map

### What Enters

| Source | Data | Format |
|---|---|---|
| REST API | Case title, description, priority, assigned_to, alert_id | JSON POST |
| REST API | Evidence files (screenshots, PDFs, video) | Multipart file upload |
| REST API | Subject details (type, entity_id, name, role) | JSON POST |
| Owl action dispatcher | Case creation triggers from automated detection | Internal service call |
| MCP tools | All case CRUD operations via 8 registered tools | MCP tool invocation |

### What Is Stored

| Table | PII Fields | Classification | Required Encryption |
|---|---|---|---|
| `fox_cases` | `title`, `description`, `resolution` | internal | Yes — may contain employee names, investigation details |
| `fox_cases` | `assigned_to`, `opened_by` | internal | No — user IDs |
| `fox_subjects` | `name` | **sensitive** | Yes — employee/suspect names |
| `fox_subjects` | `role_in_case` | internal | Yes — may contain sensitive context |
| `fox_evidence` | `file_name`, `description` | internal | No — metadata only |
| `fox_evidence` | `uploaded_by` | internal | No — investigator identity |
| `fox_case_timeline` | `actor_id`, `description`, `meta_data` | internal | No — audit trail |
| `fox_case_actions` | `performed_by`, `description`, `outcome` | internal | Yes — HR actions (suspend/terminate) with employee context |
| `fox_evidence_access_log` | `accessed_by`, `ip_address` | **sensitive** | ip_address must be hashed one-way before storage |

### What Exits

| Destination | Data | Notes |
|---|---|---|
| REST API responses | Case details, evidence metadata, timeline entries | JSON — no file content in responses |
| MCP tool responses | Same as REST | Agent consumption |
| Alert domain | `alert_history` records (status=case_opened) | Cross-domain write when linking alerts |

## Data Model

All tables in the `app` schema. Seven tables in three logical tiers.

---

### Tier 1 — Investigation Records (CRUD + soft-delete)

#### `fox_cases`

Root investigation record. One per investigation. Tenant-scoped.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id (tenant isolation) |
| `case_number` | VARCHAR(20) | No | Format: `CASE-{YYYY}-{NNNNN}`, unique per merchant per year |
| `title` | TEXT | No | Case title (encrypt at rest) |
| `description` | TEXT | Yes | Investigation narrative (encrypt at rest) |
| `case_type` | VARCHAR(30) | No | theft / fraud / policy_violation / cash_variance / return_abuse / transaction_review / other |
| `priority` | VARCHAR(10) | No | low / medium / high / critical |
| `status` | VARCHAR(20) | No | open / investigating / pending_review / escalated / closed / referred_to_le |
| `assigned_to` | VARCHAR(36) | Yes | User ID of assigned investigator |
| `opened_by` | VARCHAR(36) | No | User ID or "chirp" (auto-created) |
| `opened_at` | TIMESTAMPTZ | No | Case creation time |
| `closed_at` | TIMESTAMPTZ | Yes | Terminal state timestamp |
| `resolution` | TEXT | Yes | Resolution narrative (encrypt at rest) |
| `total_loss_cents` | INTEGER | Yes | Confirmed loss amount |
| `created_at` | TIMESTAMPTZ | No | Row creation time |
| `updated_at` | TIMESTAMPTZ | No | Last update time |
| `deleted_at` | TIMESTAMPTZ | Yes | Soft-delete timestamp (NULL = active) |

**Indexes:** `(merchant_id, status)`, `(merchant_id, case_type)`, `(merchant_id, assigned_to)`, `UNIQUE (case_number)`

**Case number generation:** Format `CASE-{year}-{sequence:05d}` per merchant per year. Use a PostgreSQL sequence (per merchant per year) rather than `SELECT count(*)` — the count approach has a race condition under concurrent requests. Uniqueness also enforced by the UNIQUE index (catches any gap in sequence logic).

#### `fox_case_alerts`

Junction table linking alerts to cases. Idempotent — linking the same alert_id twice must not create a duplicate row.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `case_id` | UUID | No | FK → fox_cases.id |
| `alert_id` | UUID | No | FK → alerts.id (cross-schema reference) |
| `linked_at` | TIMESTAMPTZ | No | Time of linkage |
| `linked_by` | VARCHAR(36) | No | User ID or "chirp" |
| `notes` | TEXT | Yes | Optional linkage notes |

**Unique index:** `(case_id, alert_id)` to enforce idempotency.

#### `fox_subjects`

Persons/entities of interest linked to a case.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id |
| `case_id` | UUID | No | FK → fox_cases.id |
| `subject_type` | VARCHAR(20) | No | employee / customer / vendor / unknown |
| `entity_id` | VARCHAR(36) | Yes | Cross-reference to employee or other entity record |
| `name` | TEXT | Yes | Subject name — **must be encrypted at rest** |
| `role_in_case` | TEXT | Yes | Free text describing suspect/witness role — **encrypt at rest** |
| `is_primary_suspect` | BOOLEAN | No | Whether this is the primary suspect |
| `created_at` | TIMESTAMPTZ | No | — |
| `updated_at` | TIMESTAMPTZ | No | — |
| `deleted_at` | TIMESTAMPTZ | Yes | Soft-delete |

#### `fox_case_actions`

Investigation actions recorded against a case.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `case_id` | UUID | No | FK → fox_cases.id |
| `merchant_id` | UUID | No | FK → merchants.id |
| `action_type` | VARCHAR(30) | No | investigate / interview / suspend / terminate / refer_to_le / refer_to_hr / coaching / no_action / status_change |
| `description` | TEXT | Yes | Action description — **encrypt at rest** |
| `performed_by` | VARCHAR(36) | No | User ID |
| `performed_at` | TIMESTAMPTZ | No | When action was taken |
| `outcome` | TEXT | Yes | Action outcome — **encrypt at rest** (may contain HR decisions) |
| `created_at` | TIMESTAMPTZ | No | — |
| `deleted_at` | TIMESTAMPTZ | Yes | Soft-delete |

---

### Tier 2 — Immutable Audit Trail (APPEND-ONLY)

#### `fox_case_timeline`

Append-only event log. This table IS the audit trail — never add soft-delete or update capability.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `case_id` | UUID | No | FK → fox_cases.id |
| `merchant_id` | UUID | No | FK → merchants.id |
| `event_type` | VARCHAR(30) | No | created / status_change / note_added / evidence_added / assigned / escalated / closed / alert_linked / action_added |
| `actor_id` | VARCHAR(36) | No | User ID or "chirp" / "system" |
| `description` | TEXT | No | Human-readable event description |
| `meta_data` | JSONB | Yes | Structured event context (see Meta Data Schema) |
| `occurred_at` | TIMESTAMPTZ | No | Event timestamp |

**No `updated_at`, no `deleted_at`.** Protected by INSERT-ONLY database trigger. Every case state transition must write a timeline entry.

**Meta data schema by event_type:**

| Event Type | Required meta_data Keys |
|---|---|
| `created` | `priority`, `case_type` |
| `status_change` | `from_status`, `to_status` |
| `note_added` | `note_preview` (first 100 chars) |
| `evidence_added` | `evidence_id`, `file_name`, `evidence_type` |
| `assigned` | `assigned_to`, `assigned_by` |
| `escalated` | `escalated_to`, `reason` |
| `closed` | `resolution_summary` |
| `alert_linked` | `alert_id`, `rule_id` |
| `action_added` | `action_type`, `performed_by` |

**INSERT-ONLY DB trigger requirement:** A PostgreSQL trigger must enforce that no UPDATE or DELETE can be issued against `fox_case_timeline`. This trigger must be deployed in a versioned migration — not applied manually.

```sql
CREATE OR REPLACE FUNCTION prevent_timeline_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'fox_case_timeline is append-only: % on % is not permitted',
        TG_OP, TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_timeline_immutability
BEFORE UPDATE OR DELETE ON app.fox_case_timeline
FOR EACH ROW EXECUTE FUNCTION prevent_timeline_modification();
```

---

### Tier 3 — Evidentiary Chain (INSERT-ONLY, hash-chained)

#### `fox_evidence`

INSERT-ONLY evidence records with cryptographic hash chain. Every INSERT is linked to the previous record for the same case via hash chain.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `merchant_id` | UUID | No | FK → merchants.id |
| `case_id` | UUID | No | FK → fox_cases.id |
| `evidence_type` | VARCHAR(20) | No | screenshot / document / video / audio / note / other |
| `file_name` | VARCHAR(255) | Yes | Original file name |
| `file_path` | VARCHAR(500) | Yes | Object storage path (S3 key). Must be populated — empty string not acceptable in production. |
| `file_hash` | VARCHAR(64) | Yes | SHA-256 hex digest of file content (computed by application before INSERT) |
| `file_size_bytes` | INTEGER | Yes | File size in bytes |
| `content_type` | VARCHAR(100) | Yes | MIME type |
| `description` | TEXT | Yes | Evidence description |
| `uploaded_by` | VARCHAR(36) | No | User ID |
| `uploaded_at` | TIMESTAMPTZ | No | Upload timestamp |
| `previous_chain_hash` | VARCHAR(64) | Yes | SHA-256 of preceding evidence record's chain_hash (NULL for genesis record) |
| `chain_hash` | VARCHAR(64) | No | SHA-256 computed by DB trigger — application sends placeholder value |

**No `updated_at`, no `deleted_at`.** Protected by INSERT-ONLY DB trigger (see below).

**File size limit:** Enforce a maximum file size (50 MB recommended) at the HTTP layer before reading the file body into memory. Stream large files directly to object storage — do not buffer in memory.

#### `fox_evidence_access_log`

INSERT-ONLY access audit. Every evidence view, download, or export is logged.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | UUID | No | PK |
| `evidence_id` | UUID | No | FK → fox_evidence.id |
| `accessed_by` | VARCHAR(36) | No | User ID |
| `access_type` | VARCHAR(10) | No | view / download / print / export / upload |
| `accessed_at` | TIMESTAMPTZ | No | Access timestamp |
| `ip_address` | VARCHAR(64) | Yes | **Must be stored as one-way hash (SHA-256), not raw IP** |

**No `updated_at`, no `deleted_at`.** Protected by INSERT-ONLY DB trigger.

---

## Hash Chain Architecture

### Evidence Chain Algorithm

The `fox_evidence` chain uses a PostgreSQL BEFORE INSERT trigger as the single source of trust. The application layer cannot forge a valid chain — even a fully compromised application cannot produce a valid chain_hash because it does not have access to the trigger's computation at INSERT time.

**Chain hash input:**
```
input = sorted_json(evidence_record_fields) + "|" + previous_chain_hash
chain_hash = SHA-256(input) → 64-char hex string
```

Where `sorted_json` means the evidence record's stable fields serialized as JSON with keys sorted alphabetically (for determinism). The `previous_chain_hash` for the genesis record (first evidence on a case) is the empty string or NULL.

**Application contract:**
1. Application computes `file_hash = SHA-256(file_bytes)` before INSERT
2. Application sets `chain_hash = 'TRIGGER_WILL_OVERWRITE'` as placeholder
3. INSERT executes — DB trigger fires BEFORE INSERT, overwrites `chain_hash` with the real value
4. After INSERT, application reads back the trigger-computed `chain_hash` via a SELECT (or RETURNING clause if the trigger exposes it)

**PostgreSQL trigger implementation:**

```sql
CREATE OR REPLACE FUNCTION compute_entry_hash()
RETURNS TRIGGER AS $$
DECLARE
    prev_hash TEXT;
    entry_json TEXT;
    combined TEXT;
BEGIN
    -- Get previous chain hash for this case
    SELECT chain_hash INTO prev_hash
    FROM app.fox_evidence
    WHERE case_id = NEW.case_id
      AND merchant_id = NEW.merchant_id
      AND id != NEW.id
    ORDER BY uploaded_at DESC
    LIMIT 1;

    prev_hash := COALESCE(prev_hash, '');

    -- Build sorted JSON of stable fields
    entry_json := row_to_json(ROW(
        NEW.id, NEW.merchant_id, NEW.case_id,
        NEW.evidence_type, NEW.file_name, NEW.file_hash,
        NEW.file_size_bytes, NEW.uploaded_by, NEW.uploaded_at
    ))::TEXT;

    combined := entry_json || '|' || prev_hash;
    NEW.chain_hash := encode(digest(combined, 'sha256'), 'hex');
    NEW.previous_chain_hash := COALESCE(prev_hash, NULL);

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER compute_fox_evidence_hash
BEFORE INSERT ON app.fox_evidence
FOR EACH ROW EXECUTE FUNCTION compute_entry_hash();
```

**INSERT-ONLY immutability trigger:**

```sql
CREATE OR REPLACE FUNCTION prevent_evidence_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'fox_evidence is INSERT-ONLY: % operation not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER enforce_evidence_immutability
BEFORE UPDATE OR DELETE ON app.fox_evidence
FOR EACH ROW EXECUTE FUNCTION prevent_evidence_modification();

-- Same pattern for fox_evidence_access_log
CREATE TRIGGER enforce_access_log_immutability
BEFORE UPDATE OR DELETE ON app.fox_evidence_access_log
FOR EACH ROW EXECUTE FUNCTION prevent_evidence_modification();
```

**Both triggers must be deployed in a versioned migration** — not applied manually. Verify trigger existence at service startup:

```sql
SELECT count(*) FROM information_schema.triggers
WHERE trigger_schema = 'app'
  AND event_object_table IN ('fox_evidence', 'fox_evidence_access_log', 'fox_case_timeline')
  AND event_manipulation IN ('UPDATE', 'DELETE');
-- Expected: >= 3 (one per protected table per blocked operation)
```

If triggers are missing, log a CRITICAL error and refuse to accept evidence uploads.

### Chain Verification

PostgreSQL server-side function for chain integrity verification:

```sql
CREATE OR REPLACE FUNCTION app.verify_hash_chain(
    p_case_id UUID,
    p_merchant_id UUID
)
RETURNS TABLE(
    evidence_id UUID,
    chain_position INTEGER,
    is_valid BOOLEAN,
    expected_hash TEXT,
    stored_hash TEXT
) AS $$
DECLARE
    rec RECORD;
    pos INTEGER := 0;
    prev_hash TEXT := '';
    expected TEXT;
    entry_json TEXT;
BEGIN
    FOR rec IN
        SELECT * FROM app.fox_evidence
        WHERE case_id = p_case_id AND merchant_id = p_merchant_id
        ORDER BY uploaded_at ASC
    LOOP
        pos := pos + 1;
        entry_json := row_to_json(ROW(
            rec.id, rec.merchant_id, rec.case_id,
            rec.evidence_type, rec.file_name, rec.file_hash,
            rec.file_size_bytes, rec.uploaded_by, rec.uploaded_at
        ))::TEXT;
        expected := encode(digest(entry_json || '|' || prev_hash, 'sha256'), 'hex');

        evidence_id := rec.id;
        chain_position := pos;
        is_valid := (expected = rec.chain_hash);
        expected_hash := expected;
        stored_hash := rec.chain_hash;

        RETURN NEXT;
        prev_hash := rec.chain_hash;
    END LOOP;
END;
$$ LANGUAGE plpgsql;
```

If the function is not deployed (migration failure), chain verification must return `valid: null` with a diagnostic message — never crash.

**What breaks the chain:**
- Direct SQL UPDATE or DELETE on `fox_evidence` rows (blocked by trigger)
- Database restore from backup without corresponding chain state
- Manual insertion bypassing the trigger
- Gap in `previous_chain_hash` linkage

---

## Case Lifecycle State Machine

```
open ──► investigating ──► pending_review ──► escalated ──► closed [TERMINAL]
                                         │               └──► referred_to_le [TERMINAL]
                                         ├──► closed [TERMINAL]
                                         └──► referred_to_le [TERMINAL]

closed         (terminal — no transitions out)
referred_to_le (terminal — no transitions out)
```

**Validation:** All transitions must be validated against an explicit allowed-transitions map. Invalid transitions return HTTP 400. Every valid transition writes:
1. A `fox_case_timeline` entry (event_type=status_change, meta_data with from/to status)
2. A `fox_case_actions` entry (action_type=status_change)

Both writes must be **atomic** in the same transaction.

**Valid transition map:**

| From | Allowed To |
|---|---|
| open | investigating, closed, referred_to_le |
| investigating | pending_review, escalated, closed, referred_to_le |
| pending_review | escalated, closed, referred_to_le |
| escalated | closed, referred_to_le |
| closed | (none) |
| referred_to_le | (none) |

**Valid enum values (enforce in application, not DB constraint only):**

| Enum | Values |
|---|---|
| case_type | theft, fraud, policy_violation, cash_variance, return_abuse, transaction_review, other |
| priority | low, medium, high, critical |
| status | open, investigating, pending_review, escalated, closed, referred_to_le |
| action_type | investigate, interview, suspend, terminate, refer_to_le, refer_to_hr, coaching, no_action, status_change |
| subject_type | employee, customer, vendor, unknown |
| event_type | created, status_change, note_added, evidence_added, assigned, escalated, closed, alert_linked, action_added |
| access_type | view, download, print, export, upload |

Validation errors must return the invalid value and the sorted list of valid options.

---

## REST API Contract

All endpoints require JWT authentication. Write operations require `owner`, `operator`, or `admin` roles.

### Cases

| Method | Path | Role Gate | Purpose |
|---|---|---|---|
| GET | `/api/fox/cases` | any authenticated | List cases, paginated. Filters: `status`, `created_after`. |
| POST | `/api/fox/cases` | owner/operator/admin | Create case. `title` required, `alert_id` optional. |
| GET | `/api/fox/cases/{case_id}` | any authenticated | Get case details. Must filter by merchant_id. |
| PUT | `/api/fox/cases/{case_id}` | owner/operator/admin | Update case status (validated transition). |

### Subjects

| Method | Path | Role Gate | Purpose |
|---|---|---|---|
| POST | `/api/fox/cases/{case_id}/subjects` | owner/operator/admin | Add subject. `subject_type` + `entity_id` required. |

### Evidence

| Method | Path | Role Gate | Purpose |
|---|---|---|---|
| POST | `/api/fox/cases/{case_id}/evidence` | owner/operator/admin | Upload evidence file (multipart). Enforce 50MB max. Stream to object storage — do not buffer in memory. |
| GET | `/api/fox/cases/{case_id}/evidence` | any authenticated | List evidence. Must filter by merchant_id via case ownership check. |
| GET | `/api/fox/cases/{case_id}/evidence/{eid}` | any authenticated | Get single evidence item. Logs access to fox_evidence_access_log. Must filter by merchant_id. |
| GET | `/api/fox/cases/{case_id}/evidence/verify` | any authenticated | Verify evidence hash chain. Calls `verify_hash_chain()` DB function. |

### Timeline

| Method | Path | Role Gate | Purpose |
|---|---|---|---|
| GET | `/api/fox/cases/{case_id}/timeline` | any authenticated | Get merged timeline: fox_case_timeline entries + fox_evidence_access_log entries, sorted by timestamp. |

### Actions

| Method | Path | Role Gate | Purpose |
|---|---|---|---|
| POST | `/api/fox/cases/{case_id}/actions` | owner/operator/admin | Add investigation action. |

**Error responses:** 400 (validation failure, invalid state transition), 404 (not found or wrong merchant), 413 (file too large), 500 (unhandled — log server-side, return generic message, never leak stack trace).

**Tenant isolation invariant:** Every query that returns case, evidence, subject, or action data MUST include a `merchant_id` filter. This applies to all REST routes and MCP tool handlers — failure to filter by merchant_id enables cross-tenant data leakage.

---

## Evidence Upload Workflow

```
1. POST /api/fox/cases/{case_id}/evidence — multipart/form-data
2. Validate: file size <= 50MB (enforce before reading body)
3. Read file bytes in chunks / stream directly to object storage (S3)
4. Compute file_hash = SHA-256(file_bytes) — application-layer
5. Store file in object storage, get back storage_path (S3 key)
6. INSERT fox_evidence with:
     file_hash = computed_sha256
     file_path = storage_path
     chain_hash = 'TRIGGER_WILL_OVERWRITE'   ← placeholder
   DB trigger fires BEFORE INSERT, overwrites chain_hash
7. Read back trigger-computed chain_hash
8. INSERT fox_case_timeline (event_type=evidence_added, meta_data includes evidence_id)
9. INSERT fox_evidence_access_log (access_type=upload, ip_address=hash(client_ip))
10. Return evidence metadata (no file content in response)
```

---

## Alert-to-Case Flow

```
1. Chirp fires alert
2. Merchant or Owl dispatcher initiates case creation
3. Create case:
   a. Generate case_number via PostgreSQL sequence
   b. INSERT fox_cases
   c. INSERT fox_case_timeline (event_type=created)
   d. If alert_id provided:
      - INSERT fox_case_alerts (idempotent)
      - INSERT alert_history(status='case_opened') in alert domain — same transaction
4. Return case_id, case_number
```

Case creation from Owl dispatcher (`create_case_from_context`) may link multiple alerts at once. For each linked alert, write one `alert_history` row with `status='case_opened'`. All writes must be atomic.

---

## MCP Tools (canary-fox server)

8 tools. Each opens its own DB connection and closes it in a deferred cleanup — no shared connection across tool calls.

| Tool | PII Access | Description |
|---|---|---|
| `create_case` | Writes actor_id as opened_by | Open a case with title, description, priority, optional alert link |
| `get_case` | Reads case details | Get full case details by ID. Must include merchant_id in query. |
| `list_cases` | Reads case list | List cases with optional status/date filters (max 50/page) |
| `update_case_status` | Writes actor_id | Transition status through lifecycle state machine |
| `add_subject` | Writes subject name, entity_id | Link employee/vendor/external party to a case |
| `get_timeline` | Reads actor IDs, access logs | Get append-only audit trail for a case |
| `verify_chain` | No PII | Verify evidence hash chain via PostgreSQL function |
| `link_alert` | Writes actor_id | Link an alert to an existing case (idempotent) |

**Tenant isolation:** All MCP tool handlers that read or write case data must validate merchant_id. Operations on `case_id` alone without merchant_id validation are cross-tenant vulnerabilities.

### MCP Blueprint Endpoints

| Endpoint | Purpose |
|---|---|
| GET `/fox/manifest` | Server manifest (name, version, tool count) |
| GET `/fox/tools` | List all 8 tools with schemas |
| POST `/fox/tools/{name}` | Invoke a tool by name |
| GET `/fox/health` | Health check (verifies DB connectivity, trigger existence) |

---

## Health Check

```
GET /fox/health
```

Must verify:
1. PostgreSQL is reachable (test query)
2. INSERT-ONLY triggers exist on all three protected tables (see trigger verification SQL above)
3. `verify_hash_chain()` function is deployed

**Response:**
```json
{
  "service": "canary-fox",
  "healthy": true,
  "tools": 8,
  "checks": {
    "database": "ok",
    "triggers": "ok",
    "hash_chain_fn": "ok"
  }
}
```

If triggers are missing: `healthy: false`, `triggers: "MISSING — evidence integrity unprotected"`.

---

## Failure Modes

| Failure | Impact | Required Behavior |
|---|---|---|
| PostgreSQL down | All Fox operations fail | 503 on all endpoints. Log error. |
| `verify_hash_chain()` DB function missing | Chain verification degraded | Return `{ "valid": null, "error": "verify_hash_chain function not deployed" }` — do not crash. |
| INSERT-ONLY triggers missing | Evidence integrity unprotected | Log CRITICAL error. Refuse evidence uploads until triggers are deployed. Health check returns unhealthy. |
| Valkey down | Auth fails for session-based auth; JWT still works | 401 for session users. JWT users unaffected. |
| Evidence file too large | Memory/OOM risk | Reject at HTTP layer before reading body. Return 413. |
| Concurrent case creation for same merchant/year | Potential duplicate case_number from count-based generation | Use PostgreSQL sequence. Unique index catches any race — return 409, log and surface to caller. |
| Object storage unavailable | Evidence files not persisted | Return 503. Do not write `fox_evidence` row if file_path cannot be populated — partial evidence records are worse than a failed upload. |

---

## ILDWAC Evidence in Fox Cases

> **Architectural direction — not current implementation.** The `cost_provenance_snapshot` field and the ILDWAC evidence vector are reserved for a future implementation pass tied to the `ledger.ilwac_positions` table. No GRO ticket exists for this yet.

When a Fox case is opened against an alert that originated from an ILDWAC cost anomaly (Chirp Category 11), the cost provenance chain is part of the evidence record. The ILDWAC vector — which device, which MCP tool, which POS port contributed to the cost anomaly — must be captured at case-open time, not reconstructed later.

### ILDWAC Evidence Schema Extension

The `fox_evidence` table gains a companion JSONB snapshot column on `fox_cases`:

```sql
-- New column on fox_cases (architectural direction — not yet in migration)
ALTER TABLE app.fox_cases
  ADD COLUMN cost_provenance_snapshot JSONB;
```

The `cost_provenance_snapshot` captures the ILDWAC vector at the moment the case is opened:

```json
{
  "captured_at": "2026-04-28T10:30:00Z",
  "alert_id": "<uuid>",
  "item_id": "<uuid>",
  "location_id": "<uuid>",
  "device_id": "<device identifier>",
  "mcp_tool": "<tool name that authorized the cost event>",
  "pos_port": "counterpoint | square | lightspeed",
  "wac_satoshis": 125000,
  "wac_fiat_cents": 4237,
  "fiat_exchange_rate_at_event": 0.03389,
  "baseline_wac_satoshis": 98000,
  "deviation_sigma": 2.7,
  "rib_batch_hash": "<SHA-256 hex of the RIB batch that produced this WAC>",
  "rib_batch_domain": "M"
}
```

### ILDWAC Evidence Workflow (architectural direction)

When a case is opened from a `COST_ANOMALY` alert type:

1. Read the ILDWAC vector from `ledger.ilwac_positions` WHERE `(item_id, location_id)` matches the alert's source record.
2. Capture the current snapshot into `fox_cases.cost_provenance_snapshot` at case-open time. This snapshot is **immutable after write** — it records the cost state at investigation initiation, not at any later time.
3. The RIB batch hash (`rib_batch_hash`) must be verified against the SHA-256 of the originating batch before the snapshot is committed. If the hash does not match, the snapshot is flagged as `integrity_check_failed`.
4. The `fox_case_timeline` entry for `created` event must include `cost_provenance_snapshot` in its `meta_data` field when the case type is cost-anomaly.

### ILDWAC Evidence in the Hash Chain

The cost provenance snapshot is referenced in the Fox evidence hash chain the same way any other evidence is referenced. The snapshot JSONB is serialized deterministically (keys sorted) before being included in the `chain_hash` input. This ensures the provenance chain is as tamper-evident as the file evidence chain.

---

## Agent-Driven Investigation Lifecycle

The Fox Case Management agent owns the investigation from open to close. Human involvement is the exception — the HIL gate fires only when evidence is incomplete or the agent determines that civil services referral is warranted.

### Agent Authority

| Phase | Agent Action | Human Required? |
|---|---|---|
| Case open | Agent opens case from COST_ANOMALY or behavioral alert; populates initial evidence via MCP tool calls | No |
| Evidence assembly | Agent calls `link_alert`, `add_subject`, uploads evidence via Fox MCP tools | No |
| Pattern analysis | Agent cross-references subjects and evidence against historical Fox cases via Owl semantic search | No |
| Recommendation | Agent writes recommendation to `fox_case_timeline` with `meta_data.recommendation` | No |
| Human escalation | Agent cannot resolve — evidence incomplete, subject is a protected employee class, or civil referral threshold met | Yes — HIL gate |
| Case close | Agent closes the case with `resolution` and `total_loss_cents` populated | No (unless civil referral) |

### Evidence Chain Authorship

The evidence chain is append-only and agent-authored. Every MCP tool call that writes to `fox_evidence` or `fox_case_timeline` must record the MCP tool call as the author:

- `uploaded_by` / `actor_id`: use the pattern `agent:fox-<module>` (e.g., `agent:fox-lp`)
- `meta_data.mcp_tool_call`: the tool name that produced the evidence entry (e.g., `link_alert`, `create_case`)

This makes the evidence chain auditable at the tool-call level — not just at the user level.

### Escalation Path

```
Fox Case Management Agent
  → Q (Loss Prevention) module agent — domain review
    → Controller — cross-module coordination
      → Founder (HIL) — final authority on civil referrals and protected-class actions
```

The agent escalates to a human LP investigator when:
- Evidence is incomplete after exhausting available MCP data sources
- The subject is flagged in `fox_subjects` as requiring protected-class review
- The recommendation is `civil_referral` — civil services referrals always require human sign-off

For all other outcomes (`close` or `human_review`), the agent has final authority.

### Service Introduction Gate

Case closure by the agent is not a Service Introduction gate. The Fox Case Management agent operates post-SI — it runs in the support lifecycle phase. The SI gate for the Fox domain is the acceptance of the Go Fox service itself (see agent-contracts.md — to be authored separately for the full contract schema).

---

## Agent-LP Smart Contract

The Fox case lifecycle is a formal contract between the Loss Prevention agent and the LP department. The contract governs investigation SLAs, output format, and escalation conditions.

### Contract Schema

| Field | Value |
|---|---|
| **Input** | `alert_id` (required), `evidence_type` (behavioral \| cost_anomaly \| composite), `escalation_threshold` (confidence score below which the agent escalates, default 0.70) |
| **Output** | `case_id`, `evidence_chain_hash` (SHA-256 of final chain head), `recommendation` (one of: `close` \| `human_review` \| `civil_referral`) |
| **SLA — triage** | Agent must open case and write initial evidence within 15 minutes of alert open |
| **SLA — resolution** | If no recommendation is written within 4 hours of case open, the agent automatically writes `recommendation: human_review` and escalates |
| **Escalation chain** | → Q (Loss Prevention) module agent → Controller → Founder (HIL) |

### SLA Enforcement

The SLA is enforced by the scheduling agent (infra layer). Two scheduled checks per case:

1. **T+15m**: Has the agent written at least one `fox_evidence` row? If not, escalate immediately to `human_review`.
2. **T+4h**: Has the agent written a recommendation? If not, write `recommendation: human_review` to the timeline and escalate.

Both checks write to `fox_case_timeline` with `actor_id = "system:sla"` and `event_type = "escalated"`.

### Contract Reference

The full agent contract schema — including input/output type definitions, error conditions, retry policy, and MCP tool authorization scope — will be authored in `agent-contracts.md` (a separate SDD in this corpus). This section captures the operational parameters. The full schema is the authoritative source.

---

## Monitoring

| Metric | Alert Threshold |
|---|---|
| Hash chain verification failures | Any `valid: false` — indicates tampering or DB corruption |
| Cases stuck in `open` > 30 days | Operational alert — needs background scheduler |
| 500 error rate on `/api/fox/*` | > 1% of requests |
| Evidence trigger health check | Any `triggers: MISSING` — P0 incident |

---

## Deployment Requirements

### Migration Order

Migrations must apply in this order. Do not apply triggers before tables exist.

1. Create all Fox tables
2. Deploy INSERT-ONLY triggers for `fox_evidence`, `fox_evidence_access_log`, `fox_case_timeline`
3. Deploy `compute_entry_hash()` trigger for `fox_evidence`
4. Deploy `verify_hash_chain()` DB function
5. Verify trigger existence via startup health check

### Object Storage (Required — Not Optional)

The prototype stores an empty string for `file_path`. This is a P0 defect. The Go implementation must:

1. Implement object storage upload (S3 or equivalent) before accepting any evidence files
2. Use server-side encryption (SSE-S3 or SSE-KMS)
3. Populate `file_path` with the full storage key on every evidence INSERT
4. Implement a download endpoint that streams from object storage

Evidence without a file_path is chain metadata without evidence — legally insufficient for chain of custody.

---

## Known Security and Quality Findings (Prototype)

| ID | Severity | Finding |
|---|---|---|
| P0-FOX-01 | Critical | `fox_subjects.name` stored in plaintext. Encrypt `name` and `role_in_case` at write, decrypt at read. |
| P0-FOX-02 | Critical | `fox_cases.description`, `fox_cases.resolution`, `fox_case_actions.description`, `fox_case_actions.outcome` stored in plaintext. Encrypt at rest. |
| P0-FOX-03 | Critical | `file_path` is empty in prototype — evidence files are not persisted. Implement object storage before accepting uploads. |
| P1-FOX-01 | High | No file size limit on evidence upload. Enforce 50MB max at HTTP layer. Stream to object storage — do not buffer in memory. |
| P1-FOX-02 | High | Evidence list and single-evidence endpoints do not filter by `merchant_id`. All evidence queries must include merchant ownership validation. |
| P1-FOX-04 | High | `fox_evidence_access_log.ip_address` stores raw IPv4/IPv6. Must be one-way hashed before storage. |
| P1-FOX-05 | High | No data retention policy. Closed cases > 7 years should be archived. Access logs > 24 months should be purged. Hash chain terminal hash must be preserved when archiving. |
| P1-FOX-06 | High | No rate limiting on Fox REST endpoints. Apply: 60/min reads, 10/min writes, 5/min evidence upload. |
| P1-FOX-07 | High | INSERT-ONLY triggers exist in prototype as manual scripts only — not in versioned migrations. Go implementation must include all triggers in versioned migrations and verify presence at startup. |
| P2-FOX-01 | Medium | Case number generation uses `SELECT count(*)` — race condition under concurrent creation. Use a PostgreSQL sequence. |
| P2-FOX-02 | Medium | No malware scanning on evidence files. Integrate scanning (ClamAV or cloud equivalent) after upload to object storage before making files available for download. |
| P2-FOX-05 | Medium | MCP tool handlers for `update_case_status` and `get_timeline` operate on `case_id` alone without `merchant_id` validation. All MCP handlers must enforce tenant isolation. |
