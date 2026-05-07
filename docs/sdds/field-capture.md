---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: field-capture
port: 9087
mcp-server: canary-field-capture
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Field Capture

**Type:** Infrastructure Service — Semantic Field Mapping  
**Binary:** `cmd/field-capture` → `:9087`  
**MCP server:** `canary-field-capture` (6 tools)  
**Depends on:** `ollama` (embeddings), `field_registry_entries` table (pgvector)  
**Feeds:** factory-pipeline document ingestion, POS adapter normalization

Field Capture is the intelligence layer that maps arbitrary inbound field names to canonical Canary field registry names using pgvector semantic search. It solves the retail data heterogeneity problem: "Retail Price," "Unit Sell Price," and "MSRP" all resolve to `item.default_retail_price_sats` without hardcoding mappings or maintaining synonym dictionaries.

**Tenant scope: platform-level.** Field Capture is platform-internal infrastructure. The `field_registry_entries` table (pgvector-indexed canonical field names) lives in the `public` schema as global reference data — every tenant maps inbound fields to the same canonical registry. Per-tenant override mappings, if any, live in `tenant_{merchant_id}.field_overrides` for cases where a specific merchant needs custom resolution. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Field Capture is required core — it operates regardless of any Optional Feature flag state (per `platform-overview.md`). Semantic field resolution is foundational to ingestion of any source data into the canonical schema. No L402 / ILDWAC / blockchain anchor dependency.

---

## Business

### The Vocabulary Problem

Retail data arrives under wildly inconsistent naming. Two merchants on the same POS version may export the same field under different column names based on custom configuration. CSV exports from legacy systems use terminology dating to the 1990s. REST API schemas use camelCase; flat files use screaming snake case; ARTS POSLOG uses XML element names.

Keyword matching requires curating a synonym dictionary that grows without bound and never fully covers domain-specific local vocabulary. Semantic search over a pgvector-indexed field registry handles this naturally — it understands that "Cost of Goods" and "Vendor Invoice Price" and "Item Cost" are all the same concept, even when none of those exact strings appear in the registry.

### Where It's Used

| Consumer | Use case |
|---|---|
| Factory pipeline document ingestion | Mapping spreadsheet column headers to CRDM fields before import |
| Square adapter | Normalizing Square API response fields to canonical names |
| Counterpoint adapter | Mapping NCR Counterpoint REST API fields to canonical names |
| CSV import | Ad-hoc merchant file imports with unknown column naming |
| Agent data normalization | ALX/ALXjr querying field mappings before writing memory cards |

---

## Core Operation

1. Receive a list of unknown field names (from a spreadsheet header, API response schema, POS export, or agent query)
2. For each field name: generate an embedding via Ollama `qwen3-embedding:8b` (1024-dim)
3. Query `field_registry_entries` (pgvector, cosine similarity) for the nearest canonical field name
4. Apply override lookup: if `field_mapping_overrides` has a matching row for `(merchant_id, incoming_field)` or `(null, incoming_field)` (platform-wide), return the override without calling Ollama
5. Return mapping result for each field

**Mapping result shape:**
```json
{
  "incoming_field": "Retail Price",
  "canonical_name": "item.default_retail_price_sats",
  "similarity": 0.94,
  "confidence": "high",
  "source": "semantic"
}
```

**`source` values:** `override` (exact match from `field_mapping_overrides`), `semantic` (pgvector result), `unmatched` (similarity below minimum threshold, returned for logging only)

### Confidence Tiers

| Tier | Similarity Range | Disposition |
|---|---|---|
| `high` | > 0.85 | Auto-mapped. Consumer may proceed without human review. |
| `medium` | 0.70–0.85 | Mapped with flag. Consumer should surface for optional human confirmation. |
| `low` | < 0.70 | Not auto-mapped. Returned in `get_unmapped_fields` queue for human review. |

The thresholds are configurable via env vars. The `low` tier result is still stored in the session's `mappings` JSONB — it is not silently dropped.

### Why pgvector Over Keyword Matching

The domain vocabulary problem in retail is structural, not accidental:

- "Cost" in retail means vendor cost, landed cost, or retail cost depending on context and speaker
- "Transaction," "sale," "ticket," and "receipt" are the same entity across four different naming conventions
- "Inventory" can mean quantity on hand, a count event, or the entire catalog — context-dependent
- Merchant-specific custom fields have names that appear nowhere in any standard vocabulary

A keyword synonym dictionary for retail would require thousands of entries and still miss long-tail cases. The field registry's pgvector index generalizes from training semantics — close meaning → close vector → close cosine distance — without any per-synonym maintenance.

---

## Data Model

All tables in the `app` schema.

### `field_mapping_sessions`

One session per `map_fields` call. Stores the full input/output pair for audit, human review, and override training.

```sql
CREATE TABLE app.field_mapping_sessions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID,
    -- null for platform-level calls (factory pipeline, agent normalization)
    source_system   TEXT        NOT NULL,
    -- 'square' | 'counterpoint' | 'csv_import' | 'api' | 'agent'
    input_fields    TEXT[]      NOT NULL,
    mappings        JSONB       NOT NULL,
    -- [{incoming, canonical_name, similarity, confidence, source}, ...]
    human_reviewed  BOOL        NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_fms_merchant       ON app.field_mapping_sessions (merchant_id, created_at DESC)
    WHERE merchant_id IS NOT NULL;
CREATE INDEX idx_fms_source_system  ON app.field_mapping_sessions (source_system, created_at DESC);
CREATE INDEX idx_fms_unreviewed     ON app.field_mapping_sessions (created_at)
    WHERE human_reviewed = false;
```

### `field_mapping_overrides`

Manual corrections that take precedence over semantic search. Two scopes: merchant-specific and platform-wide (null `merchant_id`).

```sql
CREATE TABLE app.field_mapping_overrides (
    id              UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID,
    -- null = platform-wide override
    incoming_field  TEXT    NOT NULL,
    canonical_name  TEXT    NOT NULL REFERENCES app.field_registry_entries(canonical_name),
    created_by      TEXT    NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, incoming_field)
    -- The UNIQUE constraint treats null merchant_id as a distinct value (platform-wide scope)
    -- Platform-wide override: merchant_id IS NULL AND incoming_field = X
    -- Merchant override: merchant_id = Y AND incoming_field = X
    -- Merchant override takes precedence over platform-wide for the same incoming_field
);

CREATE INDEX idx_fmo_merchant_field ON app.field_mapping_overrides (merchant_id, incoming_field);
CREATE INDEX idx_fmo_platform       ON app.field_mapping_overrides (incoming_field)
    WHERE merchant_id IS NULL;
```

### `field_registry_entries` (pgvector)

The canonical field registry. Seeded from the CRDM package — one row per canonical field name. Each row carries a pgvector embedding of its canonical name + description + example values. This table is owned by the CRDM seeding process, not by field-capture — the service reads it, does not write it.

```sql
-- Owned by CRDM seed process; field-capture is a read-only consumer
CREATE TABLE app.field_registry_entries (
    canonical_name  TEXT        PRIMARY KEY,
    -- e.g., 'item.default_retail_price_sats'
    domain          TEXT        NOT NULL,
    -- 'item' | 'transaction' | 'inventory' | 'vendor' | 'customer' | ...
    description     TEXT,
    example_values  TEXT[],
    vector          VECTOR(1024) NOT NULL,
    -- pgvector embedding of (canonical_name || ' ' || description || ' ' || example_values)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_fre_vector ON app.field_registry_entries
    USING ivfflat (vector vector_cosine_ops);
```

---

## MCP Tools (`canary-field-capture` — `/field-capture/*`)

6 tools registered with the MCP tool registry.

| Tool | Required Params | Description |
|---|---|---|
| `map_fields` | `input_fields[]`, `source_system`, `merchant_id` (optional) | Main entry point. Maps each field name to a canonical name. Returns session ID + full mapping result. Applies overrides first, falls back to semantic search. |
| `get_mapping_session` | `session_id` | Retrieve a previous session's full mapping result, including confidence tiers and sources. |
| `approve_mapping` | `session_id` | Mark a session as `human_reviewed = true`. Signals that a human has validated the auto-mappings. |
| `override_mapping` | `incoming_field`, `canonical_name`, `created_by`, `merchant_id` (optional) | Create or update a `field_mapping_overrides` row. Merchant-scoped if `merchant_id` provided, platform-wide if omitted. |
| `get_unmapped_fields` | `merchant_id` (optional), `limit` | Return fields from recent sessions that fell into the `low` confidence tier. These are the queue for human review. Ordered by frequency of occurrence — most-seen unmapped fields first. |
| `rebuild_embeddings` | — | Reseed the `field_registry_entries` pgvector index. Runs the CRDM registry through the Ollama embedding service and updates all vectors. Slow (minutes); run after registry additions. |

---

## API Contract

### MCP Blueprint Endpoints

**Base path:** `/field-capture`

| Method | Path | Auth | Description |
|---|---|:---:|---|
| GET | `/field-capture/manifest` | No | Server manifest (name, version, tool count) |
| GET | `/field-capture/tools` | No | List all 6 tools with schemas |
| POST | `/field-capture/tools/<name>` | JWT | Invoke a tool by name |
| GET | `/field-capture/health` | No | Service health check |

**Health response:**
```json
{
  "service": "canary-field-capture",
  "healthy": true,
  "tools": 6,
  "registry_size": 312,
  "override_count": 47,
  "pending_review_count": 8
}
```

---

## Operations

### Startup Sequence

1. Verify `field_registry_entries` is seeded (count > 0). If empty, log ERROR and fail fast — the service cannot operate without a seeded registry.
2. Verify pgvector index exists on `field_registry_entries.vector`. Log WARNING if missing (queries will work but slowly).
3. Register MCP tools (6 tools).
4. Register route group under `/field-capture`.

### Failure Modes

| Failure | Impact | Behavior |
|---|---|---|
| Ollama unreachable | Semantic mapping fails | Return error for `map_fields` calls requiring semantic search. Override-only calls still succeed. |
| Empty field registry | All semantic queries fail | Service refuses to start (fail-fast). |
| PostgreSQL down | All operations fail | 503 on all endpoints |
| Unknown `canonical_name` in override | Override insert rejected | 400 with `"unknown canonical field: <name>"` |

### Configuration

| Env Var | Default | Description |
|---|---|---|
| `FIELD_CAPTURE_HIGH_CONFIDENCE_THRESHOLD` | `0.85` | Cosine similarity above which confidence = `high` |
| `FIELD_CAPTURE_MEDIUM_CONFIDENCE_THRESHOLD` | `0.70` | Cosine similarity above which confidence = `medium` (below = `low`) |
| `OLLAMA_BASE_URL` | `http://growdirect_ollama:11434` | Ollama embedding service endpoint |
| `OLLAMA_EMBEDDING_MODEL` | `qwen3-embedding:8b` | Embedding model name |

---

## Related SDDs

- **platform-overview.md** — CRDM package; `field_registry_entries` is seeded from CRDM canonical field definitions
- **pos-adapter-substrate.md** — POS adapter normalization uses field-capture to resolve incoming field names
- **raas.md** — field-capture is used during CRDM record construction before events enter RaaS
- **go-module-layout.md** — service structure, package conventions, deployment
