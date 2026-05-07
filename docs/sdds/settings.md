---
spec-version: 1.1
updated: 2026-04-29
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go | go:embed
source: Canary Go platform — Settings module (GRO-617 gap-fill)
status: handoff-ready
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Settings

**Module Type:** Internal package + Identity service routes
**Related:** `identity.md` (host service), `raas.md` (flag consumers), `owl.md` (field registry consumer), `field-capture.md` (pgvector consumer), `go-module-layout.md` (no binary)

---

## Purpose

Settings is the configuration substrate for every other Canary module. It is not a standalone service — it is an `internal/settings` Go package imported by anything that needs to know what a merchant has turned on, what their vocabulary is, or whether an optional behavior should run. Without a coherent settings hierarchy, every module is either brittle (hardcoded) or duplicated (each service builds its own config layer). Settings prevents both.

**Four capabilities, one package:**

1. **Scope hierarchy** — Platform defaults → Merchant → Location → Node. Lower scope overrides higher. The resolver returns the effective value at any scope without the caller knowing the storage model.
2. **i18n / vocabulary packs** — Label resolution with vertical vocabulary overlays. A salon "appointment," a restaurant "ticket," a retailer "transaction" — same platform, different words, no code changes.
3. **Field registry** — The authoritative catalog of every platform field, with per-merchant visibility and pgvector embeddings for semantic field mapping by the field-capture agent.
4. **Feature flags** — Per-merchant flag overrides with Valkey-first caching. Flags gate external infrastructure; they default to `false` by design.

Settings routes are hosted in the Identity service. No new binary.

---

## Business Context

### The Configuration Gap Settings Closes

Every Canary merchant runs a different operation: different fiscal calendars, different notification schedules, different UI themes, different vocabulary for the same concepts, and different modules enabled. Without settings infrastructure, these differences become forks. Settings replaces the fork with a resolver.

**Three non-obvious capabilities worth naming explicitly:**

**i18n is vertical vocabulary, not translation.** The merchant-facing UI should say "appointment" in a salon, "ticket" in a restaurant, and "transaction" in a retail store. These are not different languages — they're different domain vocabularies for the same underlying concept. The vocab pack system handles this with locale-independent overlay files at startup and merchant-level label overrides stored in the database.

**The field registry is the runtime schema.** Canary does not hardcode which fields are searchable. The registry is populated from PostgreSQL's `information_schema` at startup and reconciled by migration. Agents that do field capture — mapping arbitrary inbound data to canonical platform fields — use pgvector embeddings over registry entries to find the right match. This is the machine-readable schema that keeps field capture accurate as the data model evolves.

**Feature flags gate architecture, not UI.** `feature.blockchain_anchor_enabled` and `feature.l402_enforcement_enabled` control whether external infrastructure (L2 blockchain, Lightning Network) is in the critical path for a merchant. They default to `false`. Store operations must never depend on either. Enabling them is a deliberate merchant-level decision with operational consequences, not a UI preference.

> A feature flag that defaults to false is a promise: this behavior will never happen unless you turn it on. Flags that gate external infrastructure (blockchain, Lightning) must always default to false. The store opens, sells, and reports regardless of what external systems are reachable.

---

## Technical Architecture

### Package Structure

```
internal/settings/
  resolver.go    — scope hierarchy resolution (platform → merchant → location → node)
  flags.go       — FlagClient: Valkey-first, DB fallback, 60s TTL
  i18n.go        — label resolution: merchant override → vocab pack → base locale → default
  registry.go    — field registry client: load, cache, query by canonical_name or embedding
  cache.go       — Valkey key patterns and TTL constants
  types.go       — SettingsScope, SettingsValue, FlagKey, FieldEntry types
```

Settings is `internal/` — it cannot be imported from outside the Canary Go module. Any external access goes through Identity service REST routes or MCP tools.

### Dependencies

| Dependency | Type | Required | Notes |
|------------|------|----------|-------|
| PostgreSQL (`canary` DB, `app` schema) | Database | Yes | All settings tables |
| Valkey (DB 0) | Cache | Yes | Feature flags (60s TTL), i18n overrides (300s), field registry (600s) |
| Ollama / qwen3-embedding:8b | Embedding | Yes (startup) | Field registry embeddings; populated during migration reconciliation |
| Identity service | Host | Yes | Settings REST routes mounted on Chi router at `/identity/settings/...` |
| pgvector-go | Library | Yes | Similarity search over `field_registry_entries.embedding` |

### Scope Hierarchy

The resolver evaluates settings top-down and returns the first non-null value at the most-specific scope. Callers pass `(merchant_id, location_id, node_id)` — any can be zero-value to skip that scope.

```
Resolution order (most specific wins):
  Node settings      → app.node_settings          (node_id + settings_key)
  Location settings  → app.location_settings       (location_id + settings_key)
  Merchant settings  → app.merchant_settings        (flat columns, 27 fields)
  Platform defaults  → compiled-in constants         (Go package-level vars)
```

```go
// resolver.go
type SettingsScope int
const (
    ScopePlatform SettingsScope = iota
    ScopeMerchant
    ScopeLocation
    ScopeNode
)

type ResolvedValue struct {
    Key      string
    Value    SettingsValue
    Scope    SettingsScope // which scope produced this value
    SourceID uuid.UUID     // merchant_id, location_id, or node_id
}

func (r *Resolver) Resolve(ctx context.Context, key string, merchantID, locationID, nodeID uuid.UUID) (ResolvedValue, error)
```

### Feature Flag Client

```go
// flags.go
type FlagKey string

const (
    FlagBlockchainAnchorEnabled FlagKey = "feature.blockchain_anchor_enabled"
    FlagL402EnforcementEnabled  FlagKey = "feature.l402_enforcement_enabled"
)

type FlagClient struct {
    db     *pgxpool.Pool
    valkey *redis.Client
}

// Check returns false if the key is not found — never errors on missing keys.
func (c *FlagClient) Check(ctx context.Context, merchantID uuid.UUID, key FlagKey) (bool, error)

// Invalidate removes cached flag for a merchant; called after PUT flag override.
func (c *FlagClient) Invalidate(ctx context.Context, merchantID uuid.UUID, key FlagKey) error
```

Cache miss path: query `app.merchant_feature_flags` JOIN `app.feature_flags` — if no merchant override exists, use `feature_flags.is_enabled` (platform default). Write-through to Valkey on miss.

### i18n / Vocabulary Pack System

Base locales are embedded at compile time. Vocab pack overlays are also embedded. Merchant label overrides are stored in the database and layered on top at runtime.

```go
// i18n.go — embedded files loaded at startup
//go:embed locales/en-US.json
//go:embed locales/vocab/bar.json
//go:embed locales/vocab/restaurant.json
//go:embed locales/vocab/retail.json
//go:embed locales/vocab/salon.json
//go:embed locales/vocab/whitelabel.json
```

Resolution order for any `(merchant_id, locale, i18n_key)` call:
1. `app.merchant_label_overrides` — merchant-specific customization
2. Vocab pack overlay for merchant's vertical (from `merchant_settings.vertical`)
3. Base locale file (`en-US.json`)
4. Return `i18n_key` itself as last resort (never panics)

```go
func (l *Localizer) Resolve(ctx context.Context, merchantID uuid.UUID, locale, key string) string
```

### Field Registry Client

The registry is loaded from `app.field_registry_entries` at startup and cached in Valkey. Per-merchant overrides (visibility, label) are fetched per-request from `app.merchant_field_overrides`.

```go
// registry.go
type FieldEntry struct {
    CanonicalName    string
    TableName        string
    ColumnName       string
    PgType           string
    SearchType       string  // "exact" | "range" | "text_search" | "vector"
    I18nKey          string
    ValidationRules  map[string]any
    EnabledByDefault bool
    Embedding        []float32 // vector(1024)
}

// FindByName returns a single entry; error if not found.
func (r *RegistryClient) FindByName(ctx context.Context, canonicalName string) (FieldEntry, error)

// FindBySimilarity runs a pgvector cosine similarity query over embeddings.
// Used by field-capture agent for fuzzy field mapping.
func (r *RegistryClient) FindBySimilarity(ctx context.Context, queryText string, limit int) ([]ScoredEntry, error)

// EffectiveFields returns field visibility for a specific merchant,
// merging EnabledByDefault with app.merchant_field_overrides.
func (r *RegistryClient) EffectiveFields(ctx context.Context, merchantID uuid.UUID) ([]MerchantField, error)
```

---

## Data Model

### Existing Tables (Prototype — no changes)

| Table | Purpose | Notes |
|-------|---------|-------|
| `app.merchant_settings` | 27-column flat table per merchant | Fiscal calendar, notifications, UI, analytics settings |
| `app.feature_flags` | Platform-level flag catalog | `flag_key` (unique), name, description, category, `is_enabled` (default false) |
| `app.merchant_feature_flags` | Per-merchant flag overrides | FK → `feature_flags.flag_key`, FK → `merchants.id` |

### New Tables

#### app.location_settings

Per-location key-value settings. Overrides merchant defaults at the location scope.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK, DEFAULT gen_random_uuid() | |
| `location_id` | UUID | NOT NULL, FK → `app.locations.id` | Tenant-scoped via location |
| `settings_key` | TEXT | NOT NULL | Dotted key (e.g., `notifications.alerts_enabled`) |
| `settings_value` | JSONB | NOT NULL | Any scalar or object |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Unique: `(location_id, settings_key)`
Index: `idx_location_settings_location_id ON (location_id)`

#### app.node_settings

Per-node key-value settings. A node is an individual POS terminal or integration endpoint. Overrides location and merchant scopes.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK, DEFAULT gen_random_uuid() | |
| `node_id` | UUID | NOT NULL | External node identifier (POS terminal UUID or integration endpoint ID) |
| `settings_key` | TEXT | NOT NULL | |
| `settings_value` | JSONB | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Unique: `(node_id, settings_key)`
Index: `idx_node_settings_node_id ON (node_id)`

#### app.settings_audit

Immutable change history for all settings modifications. Required for SOC 2 Type II change management control.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → `app.merchants.id` | Always present — every change is scoped to a merchant |
| `location_id` | UUID | NULLABLE, FK → `app.locations.id` | Present for location-scope changes |
| `node_id` | UUID | NULLABLE | Present for node-scope changes |
| `settings_key` | TEXT | NOT NULL | |
| `old_value` | JSONB | NULLABLE | NULL on first write |
| `new_value` | JSONB | NOT NULL | |
| `changed_by` | UUID | NOT NULL | Actor UUID (user_id or agent system UUID) |
| `changed_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Index: `idx_settings_audit_merchant_id ON (merchant_id)`
Index: `idx_settings_audit_changed_at ON (changed_at)`

#### app.field_registry_entries

The canonical catalog of every queryable platform field. Populated from `information_schema` during migration reconciliation; embeddings generated by Ollama at startup.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `canonical_name` | TEXT | PRIMARY KEY | Dotted path: `sales.transaction_amount` |
| `table_name` | TEXT | NOT NULL | PostgreSQL table name |
| `column_name` | TEXT | NOT NULL | PostgreSQL column name |
| `pg_type` | TEXT | NOT NULL | PostgreSQL data type |
| `search_type` | TEXT | NOT NULL, CHECK IN ('exact','range','text_search','vector') | How this field is queried by Owl |
| `i18n_key` | TEXT | NOT NULL | Key into locale files for UI label |
| `validation_rules` | JSONB | NOT NULL DEFAULT '{}' | JSON Schema fragment for input validation |
| `enabled_by_default` | BOOLEAN | NOT NULL DEFAULT true | Whether new merchants see this field |
| `embedding` | vector(1024) | NULLABLE | qwen3-embedding:8b over `canonical_name + pg_type` |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Index: `idx_field_registry_embedding ON USING ivfflat (embedding vector_cosine_ops)` — required for `FindBySimilarity` performance

#### app.merchant_field_overrides

Per-merchant visibility and label customization over the field registry.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `merchant_id` | UUID | NOT NULL, FK → `app.merchants.id` | |
| `canonical_name` | TEXT | NOT NULL, FK → `app.field_registry_entries.canonical_name` | |
| `is_enabled` | BOOLEAN | NOT NULL | Whether this merchant can see/query this field |
| `label_override` | TEXT | NULLABLE | Merchant-specific UI label; NULL = use i18n resolution |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Primary key: `(merchant_id, canonical_name)`

#### app.merchant_label_overrides

Merchant-specific i18n label customizations. Outermost layer in i18n resolution.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `merchant_id` | UUID | NOT NULL, FK → `app.merchants.id` | |
| `locale` | TEXT | NOT NULL | BCP 47 locale tag (e.g., `en-US`) |
| `i18n_key` | TEXT | NOT NULL | Key from locale files |
| `label` | TEXT | NOT NULL | Merchant's custom label |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Primary key: `(merchant_id, locale, i18n_key)`

### Table Relationship Diagram

```
app.merchants (1)
  ├─ app.merchant_settings (1)          — flat config, existing
  ├─ app.merchant_feature_flags (N)     — flag overrides, existing
  ├─ app.location_settings (N)          — via location_id
  ├─ app.settings_audit (N)             — change history
  ├─ app.merchant_field_overrides (N)   — per-merchant field visibility
  └─ app.merchant_label_overrides (N)   — per-merchant UI labels

app.feature_flags (global catalog)
  └─ app.merchant_feature_flags (N)

app.field_registry_entries (global catalog)
  └─ app.merchant_field_overrides (N)
```

---

## Valkey Cache Contract

All keys are namespaced under `settings:`. TTLs are conservative — prefer a stale read over a round-trip on every handler invocation.

| Key Pattern | TTL | Purpose | Invalidation |
|-------------|-----|---------|--------------|
| `settings:flags:{merchant_id}:{flag_key}` | 60s | Per-merchant feature flag | On PUT flag override |
| `settings:flags:platform:{flag_key}` | 60s | Platform-level default flag | On platform flag update |
| `settings:i18n:{merchant_id}:{locale}:{i18n_key}` | 300s | Merchant label override | On PUT label override |
| `settings:registry:{canonical_name}` | 600s | Field registry entry | On registry reconciliation |

Cache stampede protection: use `SETNX` with a short lock TTL (2s) before the DB query. If the lock is already held, return the stale value if present, or wait briefly and retry.

---

## API Routes

All routes are mounted on the Identity service Chi router. No new binary. Auth model inherits from Identity — session or API key.

### Merchant Settings

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/identity/settings/merchant/{merchant_id}` | JWT / session | All merchant settings (flat + effective) |
| PUT | `/identity/settings/merchant/{merchant_id}` | JWT owner/admin | Update merchant settings; writes audit row |

### Location Settings

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/identity/settings/location/{location_id}` | JWT / session | Location settings at this scope |
| PUT | `/identity/settings/location/{location_id}` | JWT owner/admin | Update location settings; writes audit row |

### Feature Flags

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/identity/settings/flags/{merchant_id}` | JWT / session | All flags for merchant (effective: override + platform default) |
| PUT | `/identity/settings/flags/{merchant_id}/{flag_key}` | JWT platform-admin | Override flag for merchant; invalidates Valkey; writes audit row |

### i18n Label Overrides

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/identity/settings/i18n/{merchant_id}/{locale}` | JWT / session | All label overrides for merchant + locale |
| PUT | `/identity/settings/i18n/{merchant_id}/{locale}/{key}` | JWT owner/admin | Set a label override; invalidates Valkey |

### Field Registry

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/identity/settings/registry` | JWT / API key | Full field registry (global catalog) |
| GET | `/identity/settings/registry/{canonical_name}` | JWT / API key | Single field entry |
| GET | `/identity/settings/fields/{merchant_id}` | JWT / session | Effective field visibility for merchant |

### Response contract

All GET routes return `{"data": {...}}` on success, `{"error": "...", "code": "..."}` on failure. PUT routes return `{"data": {"updated": true, "audit_id": "..."}}`. No 2xx without an audit write on any state-mutating call.

---

## MCP Tool Surface — canary-settings

Six tools. Read-heavy; one write tool. Authenticated via `X-API-Key` header. All reads are Valkey-first.

| Tool | Input | Output | Notes |
|------|-------|--------|-------|
| `get_settings` | `merchant_id`, `scope` (merchant/location/node), `scope_id` | `{key: value}` map | Scope resolver; returns effective values at the named scope |
| `check_flag` | `merchant_id`, `flag_key` | `{enabled: bool}` | Valkey-first, 60s TTL; never errors on unknown key (returns false) |
| `resolve_label` | `merchant_id`, `locale`, `i18n_key` | `{label: string}` | Merchant override → vocab pack → base locale → key literal |
| `list_fields` | `merchant_id` | `[{canonical_name, is_enabled, label}]` | Per-merchant effective field registry |
| `find_field` | `query_text` | `[{canonical_name, similarity, i18n_key}]` | pgvector cosine similarity over `field_registry_entries.embedding` |
| `audit_settings_change` | `merchant_id`, `settings_key`, `old_value`, `new_value`, `actor_id` | `{audit_id}` | Write to `app.settings_audit`; used by agents that mutate settings programmatically |

Standard discovery endpoints: `/identity/settings/manifest`, `/identity/settings/tools`, `/identity/settings/tools/{name}`, `/identity/settings/health`.

---

## Workflows

### Startup Sequence

1. Load and validate `internal/settings` package configuration (Valkey URL, DB pool, Ollama endpoint).
2. Load base locale and vocab pack files via `go:embed` — fatal if any file is missing at compile time.
3. Load field registry from `app.field_registry_entries` into memory cache. If table is empty, run reconciliation (see below).
4. Warm Valkey cache for platform-level feature flags — one pass at startup to avoid cold-start latency on the first merchant request.
5. Register settings routes on Identity Chi router.

### Field Registry Reconciliation

Runs at startup if `field_registry_entries` is empty, or on demand via internal trigger after migrations.

1. Query `information_schema.columns` filtered to `canary_go` DB, `app` schema, configured table allowlist.
2. For each column not already in `field_registry_entries`: INSERT with `enabled_by_default = true`, derive `search_type` from `pg_type`, set `i18n_key` to `canonical_name`.
3. For each entry already in the registry: verify `table_name` + `column_name` still exist; soft-flag stale entries (do not delete — agents may have references).
4. Generate embeddings: for each entry with `embedding IS NULL`, call Ollama `qwen3-embedding:8b` on `"{canonical_name}: {pg_type}"`. Batch in groups of 50. Write results back.
5. Log reconciliation summary: new entries, stale entries, embeddings generated.

### Feature Flag Override Flow

1. Platform admin calls `PUT /identity/settings/flags/{merchant_id}/{flag_key}` with `{"is_enabled": true}`.
2. Auth check: requires `platform-admin` role (not merchant owner/admin).
3. Upsert `app.merchant_feature_flags` row.
4. Write to `app.settings_audit` with `changed_by = actor_id`.
5. Invalidate Valkey keys: `settings:flags:{merchant_id}:{flag_key}` and `settings:flags:platform:{flag_key}`.
6. Return `{audit_id}`.

No anonymous flag flips. Every flag change has a `changed_by` actor.

---

## Operations

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `CANARY_SETTINGS_VALKEY_TTL_FLAGS` | No | Override flag cache TTL (default 60s) |
| `CANARY_SETTINGS_VALKEY_TTL_I18N` | No | Override i18n cache TTL (default 300s) |
| `CANARY_SETTINGS_VALKEY_TTL_REGISTRY` | No | Override registry cache TTL (default 600s) |
| `CANARY_OLLAMA_ENDPOINT` | Yes | Ollama endpoint for embedding generation (e.g., `http://growdirect_ollama:11434`) |
| `CANARY_SETTINGS_REGISTRY_TABLES` | No | Comma-separated table allowlist for registry reconciliation; defaults to all `app` schema tables |

Settings shares all base infra config (`DATABASE_URL`, `VALKEY_URL`, `CANARY_ENV`) with the Identity service — no additional secrets required.

### Failure Modes

| Failure | Impact | Behavior |
|---------|--------|----------|
| Valkey down | Cache miss on every call | Fall through to DB; log warning; do not degrade UI |
| PostgreSQL down | Settings reads fail | Return 503 on settings endpoints; feature flags return `false` (safe default) |
| Ollama down | Embedding generation blocked | Registry reconciliation deferred; `FindBySimilarity` returns empty; field capture falls back to exact-name match |
| Missing flag key | No behavior change | `check_flag` returns `{enabled: false}` — missing = off |
| Missing i18n key | Label degrades to key literal | Acceptable fallback; never panics |

### Monitoring

| Metric | Alert Threshold | Notes |
|--------|----------------|-------|
| Flag cache miss rate | >50% over 5 min | Valkey may be down or TTLs too short |
| Registry reconciliation errors | Any | Field embeddings may be stale |
| `audit_settings_change` write failures | Any | Audit integrity at risk — escalate immediately |
| Ollama embedding latency | >5s per batch | Startup reconciliation will be slow |
| `find_field` similarity scores all <0.5 | Repeated | Embeddings may be stale or Ollama model mismatch |

### Health Check

`GET /identity/settings/health` → `{"service": "canary-settings", "healthy": true, "registry_size": N, "tools": 6}`

`registry_size` is the current count of entries in memory — a quick signal that reconciliation ran.

---

## Open Findings

### P0 — Blocks Production

**P0-1: Merchant label overrides may contain PII**

`app.merchant_label_overrides.label` and `app.merchant_field_overrides.label_override` can contain merchant trade names or personal identifiers entered by users. Both columns must be classified as Sensitive and encrypted at rest using AES-256-GCM (same key as Identity's token encryption).

**P0-2: Feature flag changes require actor attribution**

The `PUT /identity/settings/flags/{merchant_id}/{flag_key}` endpoint must reject requests without a resolvable `actor_id`. No anonymous flag flips reach the database.

### P1 — Before GA

**P1-1: pgvector IVFFlat index requires training**

`CREATE INDEX ... USING ivfflat` on `field_registry_entries.embedding` requires the table to have data before the index can be built. Migration must create the index after the initial reconciliation pass, not before.

**P1-2: Cache stampede on registry warm-up**

If multiple Identity service instances start simultaneously, each will attempt to populate Valkey from the DB. Implement distributed lock (Redis `SET NX`) for the startup warm-up pass.

**P1-3: No retention policy on settings_audit**

`app.settings_audit` is append-only and has no purge logic. Implement a retention policy: entries older than 24 months archived to cold storage, then deleted.

### P2 — Post-Launch

**P2-1: Vocab pack overlays are static at compile time**

Embedding vocab packs via `go:embed` means vertical vocabulary changes require a redeploy. A future iteration could load vocab packs from the database, compiled in as the fallback only.

**P2-2: Registry reconciliation is not incremental**

The startup reconciliation scans `information_schema` in full on every cold start. Add a `last_reconciled_at` timestamp and compare `information_schema` modification times where available to skip unchanged tables.

---

## Production Readiness Checklist

- [ ] Field registry populated and embeddings generated (P1-1 ordering enforced in migration)
- [ ] Merchant label override columns encrypted at rest (P0-1)
- [ ] Feature flag PUT requires authenticated actor (P0-2)
- [ ] Distributed startup lock for Valkey warm-up (P1-2)
- [ ] settings_audit retention policy (P1-3)
- [ ] Registry reconciliation logged and alertable
- [ ] `find_field` similarity threshold configurable (not hardcoded)
- [ ] Health check returns `registry_size > 0` before accepting traffic
- [ ] Platform-admin role check on flag override endpoints
- [ ] All PUT routes write to settings_audit before returning 200
