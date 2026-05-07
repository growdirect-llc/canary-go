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

# External Identities

**Linear:** GRO-267
**Parent domain:** Identity
**Related:** `identity.md` (entity models, user federation modes), `data-model.md` (full schema reference)

---

## Scope Disambiguation

**This SDD covers POS-system entity resolution** — the canonical mapping between Canary's internal UUIDs and the identifiers used by each connected source system (Square's `TM5_NpkYj2BsDrbt`, Clover's `CLV-456`, etc.) for entities like employees, locations, devices, products, and customers.

**It does not cover IdP federation for human user authentication** (Okta, Azure AD, Google Workspace, SAML IdPs, LDAP). User federation is a different concern — it lives in `identity.md` under "User Federation Modes." The two share the word "external identity" but solve different problems:

| Concern | Subject | Lives in |
|---|---|---|
| POS entity bridging | Employees, locations, devices, products, customers in source POS systems | This SDD |
| IdP user federation | Human users authenticating via the customer's identity provider | `identity.md` |

Both produce stable Canary-native identifiers from external sources, but the protocols, lifecycle, and consumers are distinct.

---

## Purpose

External Identities is the POS-agnostic entity resolution subsystem. It maintains the canonical mapping between Canary's internal UUIDs and the identifiers used by each connected source system (Square, Clover, Toast, etc.). Every entity in Canary — employee, location, device, product, customer — has a permanent internal UUID that belongs to the merchant, not to any POS system. Source system IDs are metadata about the connection, not the identity itself.

The service completes the Identity Triangle:

```
source_systems          What POS platforms exist (square, clover, toast)
    |
merchant_sources        Which platforms this merchant has connected
    |
external_identities     How each entity maps to each platform's ID
```

**Owns:** `app.external_identities` table, identity resolution query patterns.

**Consumed by:** TSP parsers, Owl search, RaaS verification, Chirp rule engine, dashboard queries, Fox case references.

---

## Dependencies

| Dependency | Type | Status | Notes |
|------------|------|--------|-------|
| PostgreSQL (`canary` DB, `app` schema) | Database | Live | Table `app.external_identities` |
| `app.source_systems` | Reference data | Live | FK target for `source_code` |
| `app.merchant_sources` | Reference data | Live | Tracks active POS connections per merchant |
| Entity tables | Live | Live | `app.employees`, `app.locations`, `app.products`, `app.customers` |

No external service dependencies. No Valkey usage.

---

## Design Principles

### 1. Canary UUID Is the Primary Key — Always

Every entity table uses a Canary-generated UUID as its primary key. This UUID is the canonical identifier used in all internal lookups, cache keys, API responses, alert references, and case files. Source system IDs are never used as primary keys, foreign keys, or join targets between Canary-owned tables.

### 2. Source System IDs Are Metadata

A source system identifier (Square's `TM5_NpkYj2BsDrbt`, Clover's `CLV-456`) describes how an external system refers to an entity. It is stored in `external_identities`, not on the entity table itself. Entity tables should carry no vendor-prefixed columns (`square_*`, `clover_*`). Current state: `employees.square_employee_id` and `locations.square_location_id` exist as Phase 3 removal targets.

### 3. Identity Survives POS Migration

When a merchant disconnects Square and connects Clover, their employees, locations, devices, and products retain their Canary UUIDs. The old Square mappings remain in `external_identities` (marked `is_primary=false`). New Clover mappings are added. Every alert, case, risk score, and metric that referenced those entities continues to resolve correctly.

### 4. Resolution Is Bidirectional

Forward resolution (source ID → Canary UUID) serves inbound data: a webhook arrives with a Square employee ID and the parser resolves it to the canonical UUID before writing.

Reverse resolution (Canary UUID → source ID) serves outbound calls: Canary needs to refresh employee data from the Square API and must provide Square's native ID.

Cross-source resolution (Source A ID → Source B ID) serves multi-POS merchants: the same employee appears in two systems and Canary can bridge between them through the shared Canary UUID.

### 5. One Bridge Table, Not N Vendor Columns

Adding a new POS integration requires zero schema changes. Register a new `source_code` in `source_systems`, then `external_identities` rows are written by the new parser.

---

## Data Model

### app.external_identities

Bridge table mapping Canary entities to source system identifiers.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `entity_type` | TEXT | NOT NULL, CHECK IN ('employee','location','device','product','customer') | |
| `entity_id` | UUID | NOT NULL | Canary's internal UUID for the entity |
| `source_code` | TEXT | NOT NULL, FK → app.source_systems.code | e.g., `'square'` |
| `external_id` | TEXT | NOT NULL | The source system's native ID for this entity |
| `is_primary` | BOOLEAN | NOT NULL, DEFAULT true | Marks the authoritative source |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_ext_id_lookup ON (merchant_id, source_code, entity_type, external_id)` — forward resolution
- `idx_ext_id_reverse ON (merchant_id, entity_type, entity_id)` — reverse resolution
- `idx_ext_id_merchant ON (merchant_id)` — tenant-level queries

Constraints:
- `uq_ext_id_merchant_source_entity` UNIQUE `(merchant_id, source_code, entity_type, external_id)` — one mapping per source system ID
- `uq_ext_id_merchant_entity_source` UNIQUE `(merchant_id, entity_type, entity_id, source_code)` — one source mapping per entity per system
- `ck_ext_id_entity_type` CHECK `entity_type IN ('employee','location','device','product','customer')`
- `fk_ext_id_source_code` FK → `app.source_systems(code)` — **P0: must be added in migration; not yet enforced in prototype**

**PII note:** The `external_id` column stores source system identifiers (e.g., Square's `TM5_NpkYj2BsDrbt`). These are opaque vendor IDs, not human-readable PII. However, they constitute a cross-reference that could be used to correlate records across systems. Classification: **internal**. The `entity_id` column points to entity tables that DO contain PII (employee names, location addresses). This table does not store PII directly but acts as a resolution bridge to tables that do.

---

## Resolution Patterns

### Forward Resolution (Source → Canary)

A webhook arrives with `employee_id = 'TM123'`. The parser resolves it to the canonical Canary UUID before any write to the sales ledger.

```sql
SELECT entity_id FROM app.external_identities
WHERE merchant_id = $1
  AND source_code  = 'square'
  AND entity_type  = 'employee'
  AND external_id  = 'TM123';
```

Used by: TSP parsers when processing inbound webhooks.

### Reverse Resolution (Canary → Source)

Canary needs to refresh employee data from the Square API and must provide Square's native ID.

```sql
SELECT external_id, source_code FROM app.external_identities
WHERE merchant_id = $1
  AND entity_type = 'employee'
  AND entity_id   = $2;
```

Used by: Outbound API calls that require the source system's native ID.

### Cross-Source Resolution (Source A → Source B)

The same employee appears in two POS systems. Canary bridges between them through the shared internal UUID.

```sql
SELECT b.external_id
FROM   app.external_identities a
JOIN   app.external_identities b
       ON  a.entity_id   = b.entity_id
       AND a.merchant_id = b.merchant_id
       AND a.entity_type = b.entity_type
WHERE  a.merchant_id = $1
  AND  a.source_code = 'square'
  AND  a.entity_type = 'employee'
  AND  a.external_id = 'TM123'
  AND  b.source_code = 'clover';
```

Used by: Multi-POS merchants and receipt verification when the caller provides a source-specific ID and the receipt originated from a different system.

---

## Service API Contract

External Identities has no HTTP endpoints. All access is through internal service functions called by other services. No public REST routes.

### Register a Mapping (Idempotent Upsert)

```
RegisterExternalID(
    merchantID  UUID,
    entityType  string,  -- 'employee' | 'location' | 'device' | 'product' | 'customer'
    entityID    UUID,
    sourceCode  string,
    externalID  string,
    isPrimary   bool,
) → ExternalIdentity, error
```

Behavior: If a mapping already exists for `(merchant_id, entity_type, entity_id, source_code)`, updates `external_id` and `is_primary`. Otherwise creates a new row.

Implementation note: Use `INSERT ... ON CONFLICT (merchant_id, entity_type, entity_id, source_code) DO UPDATE` for batch efficiency. Never use N+1 select-then-insert.

### Bulk Register (Batch Onboarding)

```
BulkRegister(
    merchantID UUID,
    sourceCode string,
    mappings   []struct{ EntityType, EntityID, ExternalID string },
) → (created int, err error)
```

Implementation note: Must use `INSERT ... ON CONFLICT DO NOTHING` with a single batch statement. Single round-trip per batch regardless of mapping count. The prototype's N+1 loop will timeout on large merchant syncs.

### Forward Resolve

```
ResolveToCanary(
    merchantID  UUID,
    sourceCode  string,
    entityType  string,
    externalID  string,
) → (entityID UUID, found bool, err error)
```

Returns `(zero, false, nil)` if not found. Callers must handle the not-found case explicitly. Do not conflate "not found" with error.

### Reverse Resolve

```
ResolveToExternal(
    merchantID  UUID,
    entityType  string,
    entityID    UUID,
    sourceCode  string,  -- optional: filter to specific source
) → []struct{ SourceCode, ExternalID string; IsPrimary bool }, error
```

---

## Entity Type Coverage

| Entity Type | Cardinality | Implementation Status | Description |
|-------------|-------------|----------------------|-------------|
| `employee` | 1 per employee per source | Live (parser dual-write active) | Staff records — cashiers, managers, supervisors |
| `location` | 1 per location per source | Live (parser dual-write active) | Physical stores, warehouses, mobile units |
| `customer` | 1 per customer per source | Live (parser dual-write active) | Customer profiles for loyalty and return tracking |
| `device` | 1 per device per source | Not yet implemented | POS terminals, card readers |
| `product` | 1 per product per source | Not yet implemented | Catalog items, SKUs, PLUs |

---

## Transaction Table — Denormalized Source IDs

The `sales.transactions` table carries `location_id`, `employee_id`, and `device_id` as denormalized source system IDs (TEXT columns, not UUIDs) for query performance on the append-only ledger. These are the source system's native identifiers, written at ingestion time for fast filtering without joins.

The entity tables hold the canonical identity. `external_identities` is the bridge between them. When a second POS source writes to the sales ledger, a `source_code` column is added to `sales.transactions` to disambiguate which system generated each row.

---

## Implementation Phases

### Phase 1: Foundation — Complete

- `app.external_identities` table and migration
- Resolver functions (forward, reverse, cross-source)
- Unit tests for model structure and resolver imports

### Phase 2: Parser Dual-Write — Partial

- Employee, customer, location entity parsers register mappings — Live
- Device and product entity parsers — Not yet implemented
- Backfill from existing entity tables — Not yet verified complete

### Phase 3: Consumer Migration — Not Started

- Owl search builder migrates entity joins through `external_identities`
- Dashboard and Chirp queries routed through bridge table
- Drop vendor-prefixed columns (`employees.square_employee_id`, `locations.square_location_id`)

### Phase 4: Transaction Source Tagging — Not Started

- `source_code` column on `sales.transactions` (DEFAULT 'square')
- Multi-POS transaction disambiguation
- Cross-source Chirp rules activated

---

## RaaS Integration

### Namespace Resolution Layers

**Merchant level:**
```
.jeffe GUID → namespace_registrations → merchant_id → merchant_sources → source_code
```

**Entity level:**
```
.jeffe GUID → merchant_id → external_identities → entity resolution
```

### Receipt Verification Flow

```
POST /v1/verify
  { event_hash: "abc...", identifier: "sunrise-coffee.jeffe" }

1. Resolve "sunrise-coffee.jeffe" → namespace_guid → merchant_id
2. Verify event_hash against L1 inscription
3. If include_entities: true, resolve entity IDs in the receipt:
   - employee_id "TM123" → external_identities → Canary employee UUID
   - device_id "DEV456" → external_identities → Canary device UUID
   - location_id "LOC789" → external_identities → Canary location UUID
4. Response includes canonical Canary UUIDs alongside source IDs
```

A third-party verifying a receipt does not need to know which POS system generated it. They receive Canary's canonical entity UUIDs — permanent, POS-agnostic identifiers that resolve consistently regardless of whether the merchant later changes systems.

### Device Attestation

When `include_device: true`, the source-specific device ID in the receipt is resolved through `external_identities` to the Canary device entity, and the device's attestation status is returned. The attestation is bound to the canonical device, not to any particular POS system's identifier.

---

## RaaS Service Interface — Go Service Contract

RaaS (Resolution as a Service) is a separate Python service that owns namespace registration, source registration, and merchant onboarding orchestration. The Canary Go services are **consumers** of RaaS — they call it via REST. They must never query the RaaS database directly.

**Implementation status:** RaaS is functional in the Python prototype (`docs/sdds/canary/raas.md`). It is **excluded from the Go rebuild** — rebuild separately per the architecture decision. This section specifies the interface contract so Go services know how to call it.

### Data Model Boundary

These tables belong to different systems and must not be conflated:

| Table | Owner | Location | Purpose |
|-------|-------|----------|---------|
| `app.namespace_registrations` | RaaS service | `canary` DB, `app` schema | Canonical namespace records — written by RaaS only |
| `app.namespace_aliases` | RaaS service | `canary` DB, `app` schema | Alias resolution cache — written by RaaS only |
| `app.merchant_sources` | Identity domain (shared) | `canary` DB, `app` schema | POS connection status — written by both Identity and RaaS |
| `app.external_identities` | External Identities domain (Go) | `canary` DB, `app` schema | Entity UUID bridge — written by Go parsers |

They join on `merchant_id`. A Go service resolves a namespace to a `merchant_id` by calling RaaS; it then uses that `merchant_id` to look up `external_identities` directly.

### How a Go Service Resolves a Namespace

A Go service that receives a human-readable namespace string (e.g., `"sunrise-coffee.jeffe"`) and needs the corresponding `merchant_id` must call RaaS via REST.

**Endpoint:** `GET {RAAS_SERVICE_URL}/raas/tools/resolve_namespace` (MCP tool invoke pattern)

**Method:** `POST {RAAS_SERVICE_URL}/raas/tools/resolve_namespace`

**Request:**
```json
{
  "merchant_id": "<uuid>"
}
```

**Response:**
```json
{
  "namespace": "raas:<merchant_uuid>",
  "resolved": true
}
```

When `resolved` is `false`, the merchant has no active namespace. The calling Go service must treat this as a non-fatal condition — the merchant may be onboarding or may have no registered sources yet.

### What Go Services Must NOT Do

| Prohibited | Reason |
|-----------|--------|
| Query `app.namespace_registrations` directly | RaaS owns that table; direct reads bypass RaaS cache and consistency guarantees |
| Query `app.namespace_aliases` directly | Alias resolution logic lives in RaaS, not in the alias table |
| Write to `app.namespace_registrations` or `app.namespace_aliases` | RaaS is the sole writer; any other writer creates split-brain |
| Cache namespace → merchant_id mappings indefinitely | Namespaces can be transferred or reassigned; use short TTL if caching at all |

### Configuration

| Env Var | Purpose |
|---------|---------|
| `RAAS_SERVICE_URL` | Base URL for the RaaS service (e.g., `http://canary-raas:5001`) |
| `RAAS_API_KEY` | JWT or API key for authenticating RaaS MCP tool calls |

### Failure Behavior

If RaaS is unreachable, Go services that require namespace resolution must fail the request and return a 503. They must not fall back to direct database queries. Log the failure with the merchant context so RaaS availability issues surface in monitoring.

---

## Monitoring

Recommended integrity checks (no current monitoring configured):

| Check | Query | Frequency |
|-------|-------|-----------|
| Orphaned mappings | `external_identities` rows where `entity_id` has no match in the corresponding entity table | Daily |
| Missing mappings | Entity rows (employees, locations) with no corresponding `external_identities` entry | Daily |
| Stale primary flags | Multiple `is_primary=true` entries for the same entity | Daily |

---

## Open Findings

### P0 — Blocks Production

**P0-1: No FK constraint from `external_identities.source_code` to `source_systems.code`**

Any string can be inserted as a source code. The Go implementation must enforce this FK in the migration before the table is live.

Fix: Add FK constraint `fk_ext_id_source_code` referencing `app.source_systems(code)` in the CREATE TABLE or ALTER TABLE migration.

**P0-2: No FK from `entity_id` to any entity table — orphaned mappings possible**

A polymorphic FK across 5 entity tables is not practical. Instead: implement a scheduled integrity check that detects orphans (`external_identities` rows where `entity_id` has no match in the entity table implied by `entity_type`). Log and flag for cleanup.

**P0-3: Bulk registration must use batch upsert — not N+1**

Use `INSERT INTO app.external_identities (...) VALUES (...), (...) ON CONFLICT (...) DO NOTHING` with a single statement per batch. A 5,000-product merchant onboarding sync must complete in a single round-trip, not 5,000+.

### P1 — Before GA

**P1-1: No integration tests for resolver functions**

Tests must exercise all four resolution operations (forward, reverse, cross-source, bulk register) against a real PostgreSQL instance with constraint enforcement. The unique constraint idempotency behavior is untested.

**P1-2: Device and product dual-write not implemented**

Forward resolution fails for device and product entity types until parser dual-write is implemented.

**P1-3: Backfill for pre-existing entities not verified**

Existing entities created before GRO-267 may lack `external_identities` rows. Forward resolution will fail for those entities. Run and verify backfill before declaring Phase 2 complete.

**P1-4: No audit logging on identity resolution operations**

Registration and update operations must write structured logs. The `created_by` and `modified_by` columns must be populated from the authenticated request context.

**P1-5: No data retention policy for stale mappings**

Non-primary mappings older than 24 months should be archived. Define a retention policy and implement a scheduled cleanup.

### P2 — Post-Launch

**P2-1: Vendor-prefixed columns on entity tables not yet removed**

`employees.square_employee_id` and `locations.square_location_id` duplicate data in `external_identities`. Dual-write means data could diverge if one write succeeds and the other fails. Complete Phase 3 to eliminate the duplication.

**P2-2: No index on `is_primary` for filtered queries**

Add composite index `(merchant_id, entity_type, entity_id, is_primary)` for filtered reverse lookups when a caller specifically wants only the primary source.

**P2-3: Forward resolution should handle MultipleResultsFound defensively**

If the unique constraint is somehow violated, the forward resolve query returns multiple rows. The implementation should catch this and log it as a data integrity error rather than returning an ambiguous result.
