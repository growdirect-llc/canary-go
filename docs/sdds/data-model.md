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

# Data Model

> **⚠ SUPERSEDED 2026-05-01 by [`canonical-data-model.md`](./canonical-data-model.md).**
>
> This v0 spec is preserved for historical reference. The canonical replacement is ARTS-anchored, SMB-2030 scoped, with TOM operational lifecycle bound to each entity. See [`canonical-data-model-delta.md`](./canonical-data-model-delta.md) for what changed and why, and [`mcp-service-junctions.md`](./mcp-service-junctions.md) for the L3 bus SLA inventory.

**Source SDDs:** SDD-023 through SDD-032, SDD-042, SDD-043, SDD-044, SDD-057

---

## Purpose

Cross-schema reference document covering all tables across the Canary platform. This document is the PII map anchor — all other Canary SDDs reference it for field-level data classification.

---

## Schema Strategy — Schema-per-Tenant

**Multi-tenant isolation in Canary Go is schema-per-tenant.** The DDL below describes the shape of every table; tenant onboarding instantiates the operational tables inside a dedicated Postgres schema named `tenant_{merchant_uuid}`. Application code sets `SET search_path TO tenant_{merchant_id}, public` at the start of every request based on the JWT `merchant_id` claim. See `architecture.md` "Multi-Tenant Isolation" for the canonical pattern.

### Schema Inventory

| Schema | Cardinality | Contents | Write authority |
|---|---|---|---|
| `public` | One global | Reference data: `source_systems`, `roles`, `detection_rule_definitions`, embedding model registry, country / state / currency lookups | Platform admin only |
| `tenant_{merchant_id}` | One per merchant | All operational tables — alerts, cases, employees, products, locations, transactions, evidence, ingestion log | Tenant role only (via `SET search_path`) |
| `audit` | One global, append-only | Authentication events, role changes, cross-tenant query audit, key rotations, encryption key operations | Audit-only role |
| `analytics` | One global, materialized | Cross-tenant rollups produced by scheduled jobs — never queried tenant-real-time | Analytics service only |
| `growdirect_memory` (separate DB) | One global | pgvector embeddings for memory bus | Memory bus only |

### Tenant Onboarding — Schema Materialization

When a merchant onboards, the identity service:

1. Creates the `tenant_{merchant_uuid}` schema in the `canary` database
2. Runs the operational migration set against that schema (creates all tables documented in the DDL below)
3. Grants `USAGE` and the appropriate role on the schema to the per-merchant service account
4. Records the schema name in `public.merchants.tenant_schema_name` for the routing layer

A separate per-tenant migration runner advances each tenant's schema independently. Schema versioning is per-tenant — a backfill or migration can run against one merchant at a time without locking the platform.

### Cross-Tenant Admin Queries

A dedicated admin role with `USAGE` on all `tenant_*` schemas. Cross-tenant queries use schema-qualified names and are logged to `audit.cross_tenant_query_log` with the actor identity, query fingerprint, and merchant scope touched. Cross-tenant analytics use the `analytics` schema (materialized rollups), not direct cross-tenant scans.

### Within-Tenant Refinement (Optional)

Within a tenant schema, Row-Level Security (RLS) is available for finer-grained constraints — typically used for the multi-merchant organization model per ADR-001 (one organization owns N merchants, combined-view query needs an array-aware merchant filter). Column-level GRANT/REVOKE handles the cases where specific Restricted-class fields require explicit grant beyond default tenant role.

### Legacy Note

The DDL below was originally drafted with shared schemas (`app`, `sales`, `metrics`) plus a `merchant_id` column on every operational table. The shape of the tables is the same under schema-per-tenant — what changes is **where** they live (per-tenant schema, no `merchant_id` column on operational tables, the column is implicit in the schema name). Reference data stays in `public` unchanged.

For migration: tenant tables do not need `merchant_id` columns once they live in tenant schemas (the schema IS the tenant scope). Cross-schema joins between `public` reference data and `tenant_X` operational data happen via natural keys (e.g., `source_code` joining `public.source_systems` to a tenant table).

### Optional Features Schema Note

Tables that back optional features (per `platform-overview.md` "Optional Features") exist regardless of flag state — the schema is created at tenant onboarding even when the runtime flag is off. This means a merchant can opt in later without a schema migration. Specifically: `otb_wallets`, `otb_transactions`, `otb_alerts`, `ildwac_packets`, `blockchain_anchor_receipts`, `vendor_contract_events` all live in `tenant_{merchant_id}` and accept writes when their respective env flag is on; otherwise they remain empty and are not queried.

---

## Legacy Schema Reference

The DDL below is grouped by the legacy schema names (`app`, `sales`, `metrics`) for ease of reading. Under schema-per-tenant, every table previously in `app` and `sales` lives in `tenant_{merchant_id}`; `metrics` rollups are split between `tenant_{merchant_id}.metrics_*` (per-tenant rollups) and `analytics.*` (cross-tenant rollups). The memory service lives in `growdirect_memory`.

## Dependencies

- **PostgreSQL 17** — databases: `canary` (schemas: `app`, `sales`, `metrics`) and `growdirect_memory`
- **Valkey 8** (DB 0) — sessions, cache, task queue
- **Embedding service** (`growdirect_ollama:11434`) — vector embeddings for memory schema (`alx_memories.embedding`, 768-dim via nomic-embed-text)

---

## Data Flow & PII Map

### Data Entry Points

| Source | What enters | Target schema |
|--------|------------|---------------|
| Square Webhooks (HMAC-verified) | Payment, refund, order, inventory, labor, dispute, payout events | `sales` (via TSP pipeline) |
| Square OAuth flow | Access/refresh tokens, merchant profile | `app` (identity domain) |
| Square Catalog/Team/Location APIs | Product catalog, employee profiles, store locations | `app` (identity domain) |
| User registration | User email, display name, login timestamps | `app.users` |
| Merchant onboarding | Org name, billing email, phone, settings | `app.organizations`, `app.merchant_settings` |
| Pre-launch join page | Prospect email | `app.interest_signups` |
| Fox case management | Subject names, investigation notes, evidence files | `app.fox_*` tables |
| Memory service | Session context, curated memories | `growdirect_memory.alx_*` |

### Data Exit Points

| Destination | What exits | PII risk |
|-------------|-----------|----------|
| Dashboard | Aggregated metrics, employee names (if `show_employee_names=true`), alert details | internal |
| Owl AI reports | Narrative summaries, heartbeat scores, findings | internal |
| Fox case exports | Subject names, evidence files, investigation notes | sensitive |
| Notification channels (email/SMS) | Alert summaries, recipient contact info | sensitive |
| Square API (token refresh) | Encrypted OAuth tokens (decrypted in-flight) | restricted |

### PII Classification

- **public**: Freely visible, no access control needed (product names, metric aggregates)
- **internal**: Visible to authenticated users within tenant (location names, alert counts)
- **sensitive**: Must be encrypted at rest, logged on access (email, phone, names, addresses)
- **restricted**: Encrypted at rest, RLS-gated, audited on every access (OAuth tokens, card data, investigation subjects)

### Comprehensive PII Map

This is the authoritative PII inventory for Canary. All other SDDs reference this table.

#### App Schema — Identity Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `organizations` | `org_name` | internal | NO | Business name |
| `organizations` | `billing_email` | sensitive | NO | Billing contact email — P0 encryption target |
| `organizations` | `billing_external_id` | internal | NO | Square subscription ID |
| `merchants` | `merchant_name` | internal | NO | Business display name |
| `merchants` | `source_merchant_id` | internal | NO | Square merchant external ID |
| `merchant_settings` | `notif_phone` | sensitive | NO | SMS phone number — P0 encryption target |
| `users` | `email` | sensitive | NO | User login email — P0 encryption target |
| `users` | `username` | sensitive | NO | User login name — P0 encryption target |
| `users` | `display_name` | sensitive | NO | User display name — P0 encryption target |
| `employees` | `employee_name` | sensitive | NO | Employee full name — P0 encryption target |
| `employees` | `email` | sensitive | NO | Employee email — P0 encryption target |
| `employees` | `phone` | sensitive | NO | Employee phone number — P0 encryption target |
| `employees` | `square_employee_id` | internal | NO | Square external employee ID |
| `customers` | `square_customer_id` | internal | NO | Square external customer ID (no PII stored by design) |
| `locations` | `address_line1` | sensitive | NO | Physical street address — P0 encryption target |
| `locations` | `address_line2` | sensitive | NO | Physical address line 2 — P0 encryption target |
| `locations` | `city` | internal | NO | City name |
| `locations` | `state` | internal | NO | State code |
| `locations` | `postal_code` | sensitive | NO | ZIP code — location fingerprinting risk |
| `locations` | `coordinates` | sensitive | NO | JSON lat/lng — precise geolocation |
| `square_oauth_tokens` | `access_token_encrypted` | restricted | YES (AES-256-GCM) | Only encrypted field in the system |
| `square_oauth_tokens` | `refresh_token_encrypted` | restricted | YES (AES-256-GCM) | Only encrypted field in the system |
| `interest_signups` | `email` | sensitive | NO | Prospect email — P0 encryption target |

#### App Schema — Alert & Notification Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `notification_log` | `recipient` | sensitive | NO | Email address or phone number — P0 encryption target |
| `audit_log` | `ip_address` | sensitive | NO | IPv4/IPv6 address — P1 hashing target |
| `audit_log` | `user_id` | internal | NO | FK to users — identity linkage |

#### App Schema — Fox Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `fox_subjects` | `name` | sensitive | NO | Investigation subject name — P0 encryption target |
| `fox_subjects` | `entity_id` | sensitive | NO | Cross-reference to employee/customer |
| `fox_cases` | `assigned_to` | internal | NO | User ID of investigator |
| `fox_cases` | `opened_by` | internal | NO | User ID |
| `fox_case_timeline` | `actor_id` | internal | NO | User ID who performed action |
| `fox_case_actions` | `performed_by` | internal | NO | User identity string |
| `fox_evidence` | `file_path` | internal | NO | Object storage path |
| `fox_evidence` | `uploaded_by` | internal | NO | User identity string |
| `fox_evidence_access_log` | `accessed_by` | internal | NO | User identity string |
| `fox_evidence_access_log` | `ip_address` | sensitive | NO | IPv4/IPv6 address — P1 hashing target |

#### App Schema — Webhook Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `webhook_events` | `payload` | sensitive | NO | Raw JSON — may contain customer names, emails, addresses |

#### App Schema — Bank & Financial Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `bank_accounts` | `holder_name` | sensitive | NO | Legal name of account holder — P0 encryption target |
| `bank_accounts` | `routing_number` | sensitive | NO | Bank routing number — P0 encryption target |
| `bank_accounts` | `secondary_routing_number` | sensitive | NO | Secondary routing number — P0 encryption target |
| `bank_accounts` | `account_number_suffix` | internal | NO | Last 4 digits only (PCI-safe) |

#### App Schema — Card & Entity Domain

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `card_profiles` | `card_fingerprint` | sensitive | NO | Square-issued tokenized hash (not PAN) — unique per card and linkable across transactions, so classified sensitive even though the value is not the PAN itself. |
| `card_profiles` | `card_last4` | internal | NO | Last 4 digits (PCI-safe) |
| `gift_cards` | `gan` | internal | NO | Gift Account Number (safe per Square docs, not PAN) |
| `external_identities` | `external_id` | internal | NO | Source system native ID |

#### Sales Schema

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `transactions` | `card_fingerprint` | sensitive | NO | Square-issued tokenized hash (not PAN) — unique per card and linkable across transactions, so classified sensitive even though the value is not the PAN itself. |
| `transactions` | `card_last4` | internal | NO | Last 4 digits (PCI-safe) |
| `transactions` | `card_bin` | sensitive | NO | First 6 digits — issuer identification, fingerprinting risk |
| `transactions` | `card_exp_month` | sensitive | NO | Card expiration month — P0 encryption target |
| `transactions` | `card_exp_year` | sensitive | NO | Card expiration year — P0 encryption target |
| `transactions` | `statement_description` | internal | NO | Cardholder statement text |
| `transactions` | `payload` | sensitive | NO | Full webhook JSON — may contain PII |
| `transactions` | `employee_id` | internal | NO | Source system employee ID (cross-reference) |
| `transactions` | `customer_id` | internal | NO | Source system customer ID (cross-reference) |
| `transaction_tenders` | `card_last4` | internal | NO | Last 4 digits (PCI-safe) |
| `loyalty_accounts` | `phone_hash` | internal | NO | `HMAC-SHA256(PHONE_HASH_KEY, normalize(phone))` — keyed hash; plain SHA-256 prohibited (phone domain too low-entropy). See `go-security.md` → "PII Hashing Keys". **Value-vs-handler note:** the stored hash value is classified internal because it is irreversible against the keyed input space; the parser pipeline that handles plaintext phone numbers en route to this column is classified sensitive (see `tsp-parse.md` PII Handling table). The two classifications are not in conflict — they describe the at-rest value and the in-flight handler respectively. |
| `cash_drawer_shifts` | `employee_id` | internal | NO | Employee who opened drawer |
| `cash_drawer_events` | `employee_id` | internal | NO | Employee who initiated event |
| `disputes` | `payment_id` | internal | NO | Cross-reference to disputed payment |

#### Metrics Schema

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `dim_employee` | `employee_name` | sensitive | NO | Employee display name — plaintext (SCD Type 2 snapshot) |
| `dim_employee` | `square_employee_id` | internal | NO | Source system external ID |
| `dim_location` | `location_name` | internal | NO | Store name |

#### Memory Database (`growdirect_memory`)

| Table | Field | PII Classification | Encrypted at Rest? | Notes |
|-------|-------|-------------------|-------------------|-------|
| `alx_memories` | `content` | internal | NO | May contain session context with identifiers |
| `alx_memories` | `metadata` | internal | NO | JSONB — may reference project/domain info |
| `alx_sessions` | `context_snapshot` | internal | NO | Assembled session context |

---

## Schema Reference

### Cross-Cutting Column Patterns

Every table in the system uses the following baseline column set unless noted otherwise.

**Standard audit columns** (present on all tables that carry AuditMixin in the prototype):

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | Row creation timestamp |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | Row last-modified timestamp |
| `created_by` | UUID | NULLABLE | User who created the row |
| `modified_by` | UUID | NULLABLE | User who last modified the row |

**Standard soft-delete columns** (present on tables that carry SoftDeleteMixin):

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `db_status` | TEXT | NOT NULL, DEFAULT 'active', CHECK IN ('draft','active','archived') | Lifecycle state |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | Effective start date |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | Effective end date (NULL = currently active) |

**Tenant scope column** (present on all tables that carry TenantMixin):

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant boundary |

All primary keys are `UUID NOT NULL DEFAULT gen_random_uuid()` unless the table uses a natural key (e.g., `source_systems.code`).

---

### App Schema

58+ tables across 10 domain owners. All writes are schema-qualified (`app.<table>`).

#### Identity Domain (19 tables)

##### app.organizations

Root business entity. One org owns 1..N merchants.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `org_name` | TEXT | NOT NULL | Business name |
| `billing_email` | TEXT | NULLABLE | Billing contact email (P0: encrypt at rest) |
| `subscription_tier` | TEXT | NOT NULL, CHECK IN ('starter','professional','enterprise') | |
| `billing_provider` | TEXT | CHECK IN ('square','manual','none') | |
| `billing_external_id` | TEXT | NULLABLE | External billing system subscription ID |
| `billing_status` | TEXT | CHECK IN ('trialing','active','past_due','canceled','comped') | |
| `is_active` | BOOLEAN | NOT NULL, DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | Soft-delete lifecycle |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_organizations_is_active ON (is_active)`

Constraints:
- `ck_organizations_billing_status` CHECK `billing_status IN ('trialing','active','past_due','canceled','comped')`
- `ck_organizations_tier` CHECK `subscription_tier IN ('starter','professional','enterprise')`

##### app.merchants

POS connection entity. One merchant = one connected POS account = one OAuth token.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | Canary-internal UUID; used as tenant scope in all downstream tables |
| `organization_id` | UUID | NOT NULL, FK → app.organizations.id | Parent org |
| `source_merchant_id` | TEXT | NOT NULL, UNIQUE | External POS merchant identifier |
| `merchant_name` | TEXT | NOT NULL | Business display name (from POS API) |
| `currency` | CHAR(3) | NOT NULL, DEFAULT 'USD' | ISO 4217 |
| `is_active` | BOOLEAN | NOT NULL, DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_merchants_organization_id ON (organization_id)`
- `idx_merchants_source_merchant_id ON (source_merchant_id)` (also enforces UNIQUE)

Note: `merchants.id` is the `merchant_id` foreign key target for every tenant-scoped table in the system.

##### app.merchant_settings

Per-merchant configuration. One row per merchant.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, UNIQUE, FK → app.merchants.id | One row per merchant |
| `timezone` | TEXT | NOT NULL, DEFAULT 'UTC' | IANA timezone string |
| `language` | TEXT | NOT NULL, DEFAULT 'en' | |
| `date_format` | TEXT | NULLABLE | |
| `calendar_type` | TEXT | NOT NULL, CHECK IN ('nrf_454','calendar_month') | Fiscal calendar mode |
| `fiscal_year_start_month` | SMALLINT | NULLABLE | 1–12 |
| `fiscal_week_start_day` | SMALLINT | NULLABLE | 0=Sunday |
| `fiscal_pattern` | TEXT | NULLABLE | e.g. '4,5,4' |
| `notif_email_enabled` | BOOLEAN | NOT NULL, DEFAULT true | |
| `notif_sms_enabled` | BOOLEAN | NOT NULL, DEFAULT false | |
| `notif_in_app_enabled` | BOOLEAN | NOT NULL, DEFAULT true | |
| `notif_quiet_hours_start` | SMALLINT | NULLABLE | Hour 0–23 |
| `notif_quiet_hours_end` | SMALLINT | NULLABLE | Hour 0–23 |
| `notif_severity_threshold` | TEXT | NULLABLE | Minimum severity for notifications |
| `notif_daily_limit` | INTEGER | NULLABLE | Cap on notifications per day |
| `notif_phone` | TEXT | NULLABLE | SMS number (P0: encrypt at rest) |
| `theme` | TEXT | NULLABLE | UI theme preference |
| `show_employee_names` | BOOLEAN | NOT NULL, DEFAULT false | When false, mask employee names in all API responses |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_merchant_settings_merchant_id ON (merchant_id)` (also enforces UNIQUE)

##### app.users

User account. Multi-tenant via `app.user_roles`.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Primary tenant |
| `username` | TEXT | NOT NULL | Derived from email prefix |
| `email` | TEXT | NOT NULL | Login email (P0: encrypt at rest) |
| `display_name` | TEXT | NULLABLE | Display name (P0: encrypt at rest) |
| `is_active` | BOOLEAN | NOT NULL, DEFAULT true | |
| `last_login_at` | TIMESTAMPTZ | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_users_merchant_id ON (merchant_id)`
- `idx_users_email ON (email)`

Constraints:
- `uq_users_merchant_email` UNIQUE (merchant_id, email)

##### app.roles

Global RBAC definitions. Not tenant-scoped. Six roles seeded at startup.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `role_name` | TEXT | NOT NULL, UNIQUE | admin / owner / manager / operator / member / viewer |
| `description` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Seed values: `admin`, `owner`, `manager`, `operator`, `member`, `viewer`

##### app.user_roles

Tenant-scoped role assignments. A user can hold different roles across merchants.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `user_id` | UUID | NOT NULL, FK → app.users.id | |
| `role_id` | UUID | NOT NULL, FK → app.roles.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_user_roles_merchant_id ON (merchant_id)`
- `idx_user_roles_user_id ON (user_id)`

##### app.employees

Staff records synced from the POS system. Tenant-scoped.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_employee_id` | TEXT | NOT NULL | External POS employee ID (Phase 3 removal target once external_identities is fully adopted) |
| `employee_name` | TEXT | NOT NULL | Full name (P0: encrypt at rest) |
| `email` | TEXT | NULLABLE | Employee email (P0: encrypt at rest) |
| `risk_score` | NUMERIC(4,3) | NOT NULL, DEFAULT 0.0, CHECK BETWEEN 0.0 AND 1.0 | Current risk score |
| `is_active` | BOOLEAN | NOT NULL, DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_employees_merchant_id ON (merchant_id)`
- `idx_employees_square_employee_id ON (merchant_id, square_employee_id)`

##### app.locations

Physical store locations synced from the POS system.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_location_id` | TEXT | NOT NULL | External POS location ID (Phase 3 removal target) |
| `location_name` | TEXT | NOT NULL | Store display name |
| `address_line1` | TEXT | NULLABLE | Street address (P0: encrypt at rest) |
| `address_line2` | TEXT | NULLABLE | Address line 2 (P0: encrypt at rest) |
| `city` | TEXT | NULLABLE | |
| `state` | TEXT | NULLABLE | State/province code |
| `postal_code` | TEXT | NULLABLE | ZIP/postal code (P0: encrypt at rest) |
| `coordinates` | JSONB | NULLABLE | `{lat, lng}` — precise geolocation (P0: encrypt at rest) |
| `is_active` | BOOLEAN | NOT NULL, DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_locations_merchant_id ON (merchant_id)`
- `idx_locations_square_location_id ON (merchant_id, square_location_id)`

##### app.location_hierarchy

Multi-level location grouping (region → district → store).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `name` | TEXT | NOT NULL | Group name |
| `level` | SMALLINT | NOT NULL | Hierarchy depth (1 = top) |
| `parent_id` | UUID | NULLABLE, FK → app.location_hierarchy.id | Self-referential |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_location_hierarchy_merchant_id ON (merchant_id)`
- `idx_location_hierarchy_parent_id ON (parent_id)`

##### app.customers

Customer records synced from POS. Privacy-first: no PII stored beyond the external system ID.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_customer_id` | TEXT | NOT NULL | External POS customer ID |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_customers_merchant_id ON (merchant_id)`
- `idx_customers_square_customer_id ON (merchant_id, square_customer_id)`

##### app.products

Catalog items synced from POS.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_item_id` | TEXT | NOT NULL | External POS item ID |
| `product_name` | TEXT | NOT NULL | |
| `sku` | TEXT | NULLABLE | |
| `upc` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_products_merchant_id ON (merchant_id)`

##### app.square_oauth_tokens

Encrypted OAuth credentials. One per merchant. Only table with encrypted fields at rest.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `access_token_encrypted` | TEXT | NOT NULL | AES-256-GCM ciphertext. Format: `"GCM:<base64(nonce + ciphertext + tag)>"` |
| `refresh_token_encrypted` | TEXT | NULLABLE | AES-256-GCM ciphertext. Same format. |
| `token_type` | TEXT | NOT NULL, DEFAULT 'bearer' | |
| `expires_at` | TIMESTAMPTZ | NOT NULL | Token expiration (UTC) |
| `scopes` | TEXT | NULLABLE | Comma-separated granted scopes |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_square_oauth_tokens_merchant_id ON (merchant_id)`

Encryption contract: Key is `CANARY_ENCRYPTION_KEY` env var (base64-encoded 32 bytes). Nonce is 12 random bytes. Tag is 16 bytes (GCM mode). Every decrypt must be logged to `app.audit_log`. Every read of raw token is a restricted-access event.

##### app.source_systems

Reference catalog of POS platforms. Not tenant-scoped. Seeded at startup.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `code` | TEXT | PRIMARY KEY | Natural key: `square`, `clover`, `toast` |
| `display_name` | TEXT | NOT NULL | |
| `category` | TEXT | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

##### app.merchant_sources

Junction between merchants and source systems. Tracks connection status and granted scopes.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `source_code` | TEXT | NOT NULL, FK → app.source_systems.code | |
| `raas_namespace` | TEXT | NULLABLE | Namespace identifier in the receipt verification system |
| `status` | TEXT | NOT NULL, CHECK IN ('active','disconnected') | |
| `metadata_json` | JSONB | NULLABLE | Granted scopes, onboarded flag, etc. |
| `disconnected_at` | TIMESTAMPTZ | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_merchant_sources_merchant_id ON (merchant_id)`

##### app.external_identities

POS-agnostic entity bridge. Maps Canary UUIDs to source system native IDs. See `external-identities.md` for full spec.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `entity_type` | TEXT | NOT NULL, CHECK IN ('employee','location','device','product','customer') | |
| `entity_id` | UUID | NOT NULL | Canary's internal UUID for the entity |
| `source_code` | TEXT | NOT NULL, FK → app.source_systems.code | |
| `external_id` | TEXT | NOT NULL | Source system's native ID |
| `is_primary` | BOOLEAN | NOT NULL, DEFAULT true | Marks authoritative source |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_ext_id_lookup ON (merchant_id, source_code, entity_type, external_id)` — forward resolution
- `idx_ext_id_reverse ON (merchant_id, entity_type, entity_id)` — reverse resolution
- `idx_ext_id_merchant ON (merchant_id)`

Constraints:
- `uq_ext_id_merchant_source_entity` UNIQUE (merchant_id, source_code, entity_type, external_id)
- `uq_ext_id_merchant_entity_source` UNIQUE (merchant_id, entity_type, entity_id, source_code)

##### app.user_employee_links

Maps Canary user accounts to POS employee records.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `user_id` | UUID | NOT NULL, FK → app.users.id | |
| `employee_id` | UUID | NOT NULL, FK → app.employees.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_user_employee_links_merchant_id ON (merchant_id)`

##### app.employee_location_assignments

Employee-to-location assignment.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `employee_id` | UUID | NOT NULL, FK → app.employees.id | |
| `location_id` | UUID | NOT NULL, FK → app.locations.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_emp_loc_assignments_merchant_id ON (merchant_id)`
- `idx_emp_loc_assignments_employee_id ON (employee_id)`

##### app.gift_cards

Mutable gift card entity. GAN stored (safe per Square docs, not a PAN).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_gift_card_id` | TEXT | NOT NULL | External POS gift card ID |
| `gan` | TEXT | NOT NULL | Gift Account Number |
| `state` | TEXT | NOT NULL | Current card state |
| `balance_cents` | BIGINT | NOT NULL, DEFAULT 0 | Current balance in cents |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_gift_cards_merchant_id ON (merchant_id)`

##### app.bank_accounts

Merchant bank account records. Multiple PII fields — all P0 encryption targets.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_bank_account_id` | TEXT | NOT NULL | External POS bank account ID |
| `holder_name` | TEXT | NOT NULL | Legal name of account holder (P0: encrypt at rest) |
| `routing_number` | TEXT | NOT NULL | Bank routing number (P0: encrypt at rest) |
| `secondary_routing_number` | TEXT | NULLABLE | Secondary routing number (P0: encrypt at rest) |
| `account_number_suffix` | TEXT | NOT NULL | Last 4 digits only (PCI-safe) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_bank_accounts_merchant_id ON (merchant_id)`

---

#### Chirp Domain (2 tables)

##### app.detection_rules

Global detection rule catalog. Not tenant-scoped. Seeded at startup; not modified at runtime.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `rule_id` | TEXT | NOT NULL, UNIQUE | Short rule code (e.g., `C-101`) |
| `category` | TEXT | NOT NULL | Rule category |
| `severity` | TEXT | NOT NULL, CHECK IN ('critical','high','medium','low','info') | |
| `default_threshold` | NUMERIC | NULLABLE | Default trigger threshold |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_detection_rules_rule_id ON (rule_id)` (also enforces UNIQUE)

##### app.merchant_rule_config

Per-merchant threshold overrides for detection rules.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `rule_id` | UUID | NOT NULL, FK → app.detection_rules.id | |
| `is_enabled` | BOOLEAN | NOT NULL, DEFAULT true | |
| `custom_threshold` | NUMERIC | NULLABLE | Overrides `detection_rules.default_threshold` when set |
| `notify_enabled` | BOOLEAN | NOT NULL, DEFAULT true | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_merchant_rule_config_merchant_id ON (merchant_id)`

---

#### Alert Domain (4 tables)

##### app.alerts

Alert records. Immutable once written by the detection engine.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `rule_id` | UUID | NOT NULL, FK → app.detection_rules.id | |
| `severity` | TEXT | NOT NULL, CHECK IN ('critical','high','medium','low','info') | |
| `source_table` | TEXT | NOT NULL | Table that triggered the alert |
| `source_id` | UUID | NOT NULL | Row in source_table that triggered |
| `employee_id` | UUID | NULLABLE, FK → app.employees.id | |
| `location_id` | UUID | NULLABLE, FK → app.locations.id | |
| `impact_cents` | BIGINT | NOT NULL, DEFAULT 0 | Estimated financial impact |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Access: Append-only. Immutable once written.

Indexes:
- `idx_alerts_merchant_id ON (merchant_id)`
- `idx_alerts_employee_id ON (employee_id)`
- `idx_alerts_location_id ON (location_id)`
- `idx_alerts_created_at ON (merchant_id, created_at DESC)`

##### app.alert_history

Alert status transitions. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `alert_id` | UUID | NOT NULL, FK → app.alerts.id | |
| `status` | TEXT | NOT NULL | New status |
| `changed_by` | UUID | NOT NULL, FK → app.users.id | |
| `notes` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_alert_history_alert_id ON (alert_id)`

##### app.notification_log

Outbound notification record. Append-only. `recipient` is PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `alert_id` | UUID | NULLABLE, FK → app.alerts.id | |
| `channel` | TEXT | NOT NULL, CHECK IN ('email','sms','in_app') | |
| `status` | TEXT | NOT NULL, CHECK IN ('pending','sent','failed') | |
| `recipient` | TEXT | NOT NULL | Email address or phone number (P0: encrypt at rest) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_notification_log_merchant_id ON (merchant_id)`
- `idx_notification_log_alert_id ON (alert_id)`

##### app.notification_schedule

Per-merchant, per-category notification frequency configuration.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `alert_category` | TEXT | NOT NULL | Rule category |
| `freq_critical` | TEXT | NULLABLE | Frequency for critical alerts |
| `freq_high` | TEXT | NULLABLE | |
| `freq_medium` | TEXT | NULLABLE | |
| `freq_low` | TEXT | NULLABLE | |
| `freq_info` | TEXT | NULLABLE | |
| `hourly_cap` | INTEGER | NULLABLE | |
| `daily_cap` | INTEGER | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_notification_schedule_merchant_id ON (merchant_id)`

---

#### Owl Domain (4 tables)

##### app.owl_sessions

AI analysis session record. Delta-chained via `previous_session_id`. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `session_type` | TEXT | NOT NULL | Type of analysis run |
| `heartbeat_score` | NUMERIC(4,3) | NULLABLE | 0.0–1.0 health score |
| `heartbeat_band` | TEXT | NULLABLE | Band label (green/yellow/red/critical) |
| `previous_session_id` | UUID | NULLABLE, FK → app.owl_sessions.id | Delta chain link |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_owl_sessions_merchant_id ON (merchant_id)`
- `idx_owl_sessions_previous_session_id ON (previous_session_id)`

##### app.owl_findings

One row per Chirp category per Owl session. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `session_id` | UUID | NOT NULL, FK → app.owl_sessions.id | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `category` | TEXT | NOT NULL | Chirp rule category |
| `severity` | TEXT | NOT NULL | |
| `finding_text` | TEXT | NOT NULL | Narrative finding |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_owl_findings_session_id ON (session_id)`
- `idx_owl_findings_merchant_id ON (merchant_id)`

##### app.owl_merchant_memory

One row per merchant. Always-current context for the Owl system.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, UNIQUE, FK → app.merchants.id | One row per merchant |
| `latest_session_id` | UUID | NULLABLE, FK → app.owl_sessions.id | |
| `running_summary` | TEXT | NULLABLE | Accumulated context summary |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_owl_merchant_memory_merchant_id ON (merchant_id)` (also enforces UNIQUE)

##### app.owl_action_log

Merchant responses to Owl findings. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `finding_id` | UUID | NOT NULL, FK → app.owl_findings.id | |
| `action_type` | TEXT | NOT NULL | Type of action taken |
| `outcome` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_owl_action_log_merchant_id ON (merchant_id)`
- `idx_owl_action_log_finding_id ON (finding_id)`

---

#### Fox Domain (7 tables)

Fox is the case management system. Evidence tables enforce INSERT-only discipline. The `fox_case_timeline` table IS the audit trail and has no audit columns of its own.

##### app.fox_cases

Investigation case record.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `case_number` | TEXT | NOT NULL, UNIQUE | Human-readable case identifier |
| `status` | TEXT | NOT NULL, CHECK IN ('open','in_progress','closed','dismissed') | |
| `priority` | TEXT | NOT NULL, CHECK IN ('critical','high','medium','low') | |
| `assigned_to` | UUID | NULLABLE, FK → app.users.id | Investigator |
| `opened_by` | UUID | NOT NULL, FK → app.users.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_fox_cases_merchant_id ON (merchant_id)`
- `idx_fox_cases_assigned_to ON (assigned_to)`

##### app.fox_case_alerts

Junction table linking alerts to cases.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.fox_cases.id | |
| `alert_id` | UUID | NOT NULL, FK → app.alerts.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_fox_case_alerts_case_id ON (case_id)`

##### app.fox_case_timeline

Immutable audit trail. No audit mixin — this table IS the audit. INSERT-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.fox_cases.id | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `event_type` | TEXT | NOT NULL | Event classification |
| `actor_id` | UUID | NOT NULL | User who performed the action |
| `description` | TEXT | NOT NULL | |
| `entry_hash` | TEXT | NULLABLE | SHA-256 hash of this row's content (P1: implement hash chain) |
| `previous_hash` | TEXT | NULLABLE | Hash of previous timeline entry (P1: implement hash chain) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only. No UPDATE or DELETE permitted at the application layer.

Indexes:
- `idx_fox_case_timeline_case_id ON (case_id)`
- `idx_fox_case_timeline_created_at ON (case_id, created_at DESC)`

##### app.fox_case_actions

Case action log. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.fox_cases.id | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `action_type` | TEXT | NOT NULL | |
| `performed_by` | TEXT | NOT NULL | User identity string |
| `outcome` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_fox_case_actions_case_id ON (case_id)`
- `idx_fox_case_actions_merchant_id ON (merchant_id)`

##### app.fox_evidence

Hash-chained evidence locker. INSERT-only; no UPDATE or DELETE ever.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `case_id` | UUID | NOT NULL, FK → app.fox_cases.id | |
| `evidence_type` | TEXT | NOT NULL | Type classification |
| `file_path` | TEXT | NOT NULL | Object storage path (P2: migrate to S3/blob) |
| `file_hash` | TEXT | NOT NULL | SHA-256 of file content |
| `chain_hash` | TEXT | NOT NULL | SHA-256 of (previous_chain_hash + file_hash) for chain integrity |
| `uploaded_by` | TEXT | NOT NULL | User identity string |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only. Application must enforce — no UPDATE or DELETE.

Indexes:
- `idx_fox_evidence_case_id ON (case_id)`
- `idx_fox_evidence_merchant_id ON (merchant_id)`

##### app.fox_evidence_access_log

Evidence access audit. INSERT-only. `ip_address` is PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `evidence_id` | UUID | NOT NULL, FK → app.fox_evidence.id | |
| `accessed_by` | TEXT | NOT NULL | User identity string |
| `access_type` | TEXT | NOT NULL | Type of access |
| `ip_address` | TEXT | NOT NULL | IPv4/IPv6 address (P1: hash or mask) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only.

Indexes:
- `idx_fox_evidence_access_log_evidence_id ON (evidence_id)`

##### app.fox_subjects

Investigation subjects. `name` is PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `case_id` | UUID | NOT NULL, FK → app.fox_cases.id | |
| `subject_type` | TEXT | NOT NULL, CHECK IN ('employee','customer','unknown') | |
| `entity_id` | TEXT | NULLABLE | Cross-reference to entity table (P1: add referential integrity validation) |
| `name` | TEXT | NOT NULL | Subject name (P0: encrypt at rest) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_fox_subjects_merchant_id ON (merchant_id)`
- `idx_fox_subjects_case_id ON (case_id)`

---

#### Hawk Domain (8 tables)

Hawk supersedes Fox's flat case model with incident-typed investigations, dual-track action codes, compliance obligations, and a structured card factory. Fox evidence tables remain the evidentiary backbone.

##### app.hawk_incident_types

63 incident types across 5 incident classes. Seed data; read-only at runtime.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `type_code` | TEXT | PRIMARY KEY | Natural key (e.g., `THEFT_EMPLOYEE`) |
| `incident_class` | TEXT | NOT NULL | One of 5 classes |
| `de_pv_flag` | BOOLEAN | NOT NULL, DEFAULT false | Whether incident is DE/PV trackable |
| `wizard_template` | JSONB | NULLABLE | UI wizard configuration |
| `resolution_track` | TEXT | NOT NULL | `disciplinary` or `legal` |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

##### app.hawk_sources

31 investigation sources (CCTV, EBR_*, TIP_*, AUDIT_*). Seed data; read-only at runtime.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `source_code` | TEXT | NOT NULL, UNIQUE | Natural code (e.g., `CCTV`, `EBR_REGISTER`) |
| `source_class` | TEXT | NOT NULL | Source classification |
| `display_name` | TEXT | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_hawk_sources_source_code ON (source_code)` (also enforces UNIQUE)

##### app.hawk_cases

Root investigation record. Links to Fox for evidence chain.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `location_id` | UUID | NULLABLE, FK → app.locations.id | |
| `incident_class` | TEXT | NOT NULL | |
| `incident_type` | TEXT | NOT NULL, FK → app.hawk_incident_types.type_code | |
| `case_status` | TEXT | NOT NULL | |
| `card_id` | UUID | NULLABLE, FK → app.hawk_cards.id | Current active case card |
| `fox_case_id` | UUID | NULLABLE, FK → app.fox_cases.id | Linked Fox case for evidence chain |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_hawk_cases_merchant_id ON (merchant_id)`
- `idx_hawk_cases_fox_case_id ON (fox_case_id)`

##### app.hawk_subjects

Investigation subjects for Hawk cases. Exactly-one-identifier constraint enforced at application layer.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.hawk_cases.id | |
| `subject_type` | TEXT | NOT NULL, CHECK IN ('employee','vendor','external') | |
| `employee_id` | UUID | NULLABLE, FK → app.employees.id | Set when subject_type = 'employee' |
| `vendor_entity_id` | UUID | NULLABLE | Set when subject_type = 'vendor' |
| `external_name` | TEXT | NULLABLE | Set when subject_type = 'external' |
| `notes` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Invariant: Exactly one of `employee_id`, `vendor_entity_id`, `external_name` must be non-null.

Indexes:
- `idx_hawk_subjects_case_id ON (case_id)`

##### app.hawk_actions

Coded actions against a Hawk case. Append-only. Actions are validated against the incident class action track.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.hawk_cases.id | |
| `action_code` | TEXT | NOT NULL | Coded action identifier |
| `action_track` | TEXT | NOT NULL, CHECK IN ('disciplinary','legal') | |
| `actioned_by` | UUID | NOT NULL, FK → app.users.id | |
| `actioned_at` | TIMESTAMPTZ | NOT NULL | |
| `notes` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: Append-only.

Indexes:
- `idx_hawk_actions_case_id ON (case_id)`

##### app.hawk_timeline

INSERT-only audit trail for Hawk cases. Inherits INSERT-only discipline from Fox pattern.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.hawk_cases.id | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `event_type` | TEXT | NOT NULL | |
| `actor_id` | UUID | NOT NULL | |
| `description` | TEXT | NOT NULL | |
| `event_data` | JSONB | NULLABLE | Structured event payload |
| `occurred_at` | TIMESTAMPTZ | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only.

Indexes:
- `idx_hawk_timeline_case_id ON (case_id)`
- `idx_hawk_timeline_occurred_at ON (case_id, occurred_at DESC)`

##### app.hawk_compliance_obligations

Regulatory and policy obligations with filing status.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.hawk_cases.id | |
| `obligation_type` | TEXT | NOT NULL | |
| `due_date` | TIMESTAMPTZ | NULLABLE | |
| `filed_at` | TIMESTAMPTZ | NULLABLE | |
| `notes` | TEXT | NULLABLE | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_hawk_compliance_case_id ON (case_id)`

##### app.hawk_cards

Structured case summaries with vector embeddings. Versioned: previous versions are invalidated, not deleted.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `case_id` | UUID | NOT NULL, FK → app.hawk_cases.id | |
| `card_body` | TEXT | NOT NULL | Structured case summary (markdown) |
| `frontmatter` | JSONB | NOT NULL | Structured metadata |
| `card_version` | INTEGER | NOT NULL | Monotonically increasing per case |
| `generated_at` | TIMESTAMPTZ | NOT NULL | |
| `invalidated_at` | TIMESTAMPTZ | NULLABLE | Set when a newer version supersedes this card |
| `vector` | VECTOR(1024) | NULLABLE | pgvector embedding for semantic search |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_hawk_cards_case_id ON (case_id)`
- `idx_hawk_cards_vector` USING hnsw ON (vector vector_cosine_ops)

Access: Append (versioned). Application sets `invalidated_at` on previous version before inserting new one.

---

#### Bull Domain (Phase 3 stub — tables planned, no migration yet)

Bull covers Module D intelligence: transfer-loss reconciliation (D.4) and multi-store distribution recommendations (D.5). Schema planned for Phase 3.

| Table (planned) | Key Columns | Purpose |
|-----------------|-------------|---------|
| `bull_transfer_variances` | id, xfer_doc_id, recvr_doc_id, item_id, initiated_qty, received_qty, variance, match_confidence | Per-XFER-item variance with deterministic/heuristic match flag |
| `bull_unattributed_movements` | id, merchant_id, location_id, item_id, snapshot_before, snapshot_after, delta, attributed | SOH deltas not explained by known documents |
| `bull_distribution_recommendations` | id, merchant_id, from_location, to_location, item_id, excess_qty, deficit_qty, transfer_cost, score | Rebalancing suggestions ranked by savings |
| `bull_transfer_costs` | id, merchant_id, from_location, to_location, cost_per_unit | Per-route configurable transfer costs |

---

#### Webhook Pipeline Domain (3 tables)

##### app.webhook_events

Raw inbound events. `payload` contains raw POS JSON with potential PII. Append-only.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `event_id` | TEXT | NOT NULL, UNIQUE | Source system event ID (idempotency key) |
| `event_type` | TEXT | NOT NULL | |
| `payload` | JSONB | NOT NULL | Raw POS webhook JSON (P0: redact PII before storage or encrypt column) |
| `processing_status` | TEXT | NOT NULL, CHECK IN ('pending','processing','processed','failed') | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_webhook_events_merchant_id ON (merchant_id)`
- `idx_webhook_events_event_id ON (event_id)` (also enforces UNIQUE)
- `idx_webhook_events_processing_status ON (merchant_id, processing_status)`

##### app.schema_fingerprints

SHA-256 schema signature for POS webhook payloads. Not tenant-scoped.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `event_type` | TEXT | NOT NULL | |
| `payload_hash` | TEXT | NOT NULL, UNIQUE | SHA-256 of schema structure |
| `occurrence_count` | INTEGER | NOT NULL, DEFAULT 1 | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

##### app.schema_drift_alerts

POS API structure change detection.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `event_type` | TEXT | NOT NULL | |
| `new_fields` | JSONB | NULLABLE | Fields present in new payload not in baseline |
| `missing_fields` | JSONB | NULLABLE | Fields in baseline absent from new payload |
| `is_resolved` | BOOLEAN | NOT NULL, DEFAULT false | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

---

#### UI/BFF Domain (5 tables)

##### app.feature_flags

Global feature toggles. Not tenant-scoped.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `flag_key` | TEXT | NOT NULL, UNIQUE | |
| `flag_name` | TEXT | NOT NULL | |
| `is_enabled` | BOOLEAN | NOT NULL, DEFAULT false | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

##### app.merchant_feature_flags

Per-merchant flag overrides.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `flag_key` | TEXT | NOT NULL, FK → app.feature_flags.flag_key | |
| `is_enabled` | BOOLEAN | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_merchant_feature_flags_merchant_id ON (merchant_id)`

##### app.app_config

Runtime configuration. Secrets are masked in API responses.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `config_key` | TEXT | NOT NULL, UNIQUE | |
| `config_value` | TEXT | NOT NULL | |
| `is_secret` | BOOLEAN | NOT NULL, DEFAULT false | When true, value is masked in any API response |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

##### app.card_profiles

PCI-safe card fingerprints. No PAN stored.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `card_fingerprint` | TEXT | NOT NULL | PCI-safe hash (not PAN) |
| `card_last4` | TEXT | NOT NULL | Last 4 digits |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_card_profiles_merchant_id ON (merchant_id)`

##### app.blocked_entities

Merchant-blocked entities.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `entity_type` | TEXT | NOT NULL | |
| `entity_id` | TEXT | NOT NULL | |
| `blocked_by` | UUID | NOT NULL, FK → app.users.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |
| `db_status` | TEXT | NOT NULL, DEFAULT 'active' | |
| `db_effective_from` | TIMESTAMPTZ | NULLABLE | |
| `db_effective_to` | TIMESTAMPTZ | NULLABLE | |

Indexes:
- `idx_blocked_entities_merchant_id ON (merchant_id)`

---

#### RaaS Domain (2 tables)

##### app.namespace_registrations

Receipt-as-a-Service namespace bridge.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `namespace_name` | TEXT | NOT NULL | Human-readable namespace |
| `namespace_guid` | TEXT | NOT NULL, UNIQUE | Globally unique namespace identifier |
| `status` | TEXT | NOT NULL, CHECK IN ('active','inactive') | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `created_by` | UUID | NULLABLE | |
| `modified_by` | UUID | NULLABLE | |

Indexes:
- `idx_namespace_registrations_merchant_id ON (merchant_id)`

##### app.namespace_aliases

Alias resolution cache for namespace lookups.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `alias_name` | TEXT | NOT NULL, UNIQUE | |
| `namespace_guid` | TEXT | NOT NULL, FK → app.namespace_registrations.namespace_guid | |
| `status` | TEXT | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

---

#### Vault Domain (1 table)

##### app.vault_memories

Owl's long-term memory. Sealed — INSERT-only, no edits.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `memory_type` | TEXT | NOT NULL | |
| `source_type` | TEXT | NOT NULL | |
| `payload` | JSONB | NOT NULL | Structured memory content |
| `narrative` | TEXT | NULLABLE | Human-readable summary |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only.

Indexes:
- `idx_vault_memories_merchant_id ON (merchant_id)`

---

#### Subscription & Transfer Domain (2 tables)

##### app.subscriptions

Recurring billing tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_subscription_id` | TEXT | NOT NULL | External subscription ID |
| `customer_id` | UUID | NULLABLE, FK → app.customers.id | |
| `status` | TEXT | NOT NULL | |
| `card_id` | UUID | NULLABLE, FK → app.card_profiles.id | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_subscriptions_merchant_id ON (merchant_id)`

##### app.transfer_orders

Inter-location inventory movement.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_transfer_order_id` | TEXT | NOT NULL | External transfer order ID |
| `from_location_id` | UUID | NOT NULL, FK → app.locations.id | |
| `to_location_id` | UUID | NOT NULL, FK → app.locations.id | |
| `state` | TEXT | NOT NULL | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_transfer_orders_merchant_id ON (merchant_id)`

---

#### Cross-Cutting (2 tables)

##### app.audit_log

SHA-256 hash-chained tamper-evident log. Append-only. `ip_address` is PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `action` | TEXT | NOT NULL | Action description |
| `resource_type` | TEXT | NOT NULL | |
| `resource_id` | UUID | NULLABLE | |
| `user_id` | UUID | NULLABLE, FK → app.users.id | |
| `ip_address` | TEXT | NULLABLE | IPv4/IPv6 (P1: hash or mask — GDPR/CCPA risk) |
| `entry_hash` | TEXT | NOT NULL | SHA-256 of this row's content |
| `previous_hash` | TEXT | NULLABLE | Hash of previous audit entry (NULL on first row) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: Append-only. Hash chain must be verified on startup.

Indexes:
- `idx_audit_log_merchant_id ON (merchant_id)`
- `idx_audit_log_user_id ON (user_id)`
- `idx_audit_log_created_at ON (merchant_id, created_at DESC)`

##### app.interest_signups

Pre-launch prospect list. `email` is PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `email` | TEXT | NOT NULL | Prospect email (P0: encrypt at rest) |
| `source` | TEXT | NULLABLE | How they found us |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: Append-only.

---

### Sales Schema

27+ tables. **ALL WRITE-ONCE IMMUTABLE** (exceptions noted). Data enters exclusively via the TSP webhook pipeline. All writes are schema-qualified (`sales.<table>`).

Immutability contract: A `BEFORE UPDATE OR DELETE` PostgreSQL trigger on all immutable sales tables raises an exception with the message `IMMUTABILITY VIOLATION`. Application must never attempt to UPDATE or DELETE from these tables. Corrections use compensating INSERTs.

Exceptions: `loyalty_accounts` (balance updates permitted), `cash_drawer_shifts` (state changes on close permitted).

#### Transaction Core (4 tables)

##### sales.transactions

Primary fact table. `card_bin`, `card_exp_*`, and `payload` are PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `external_id` | TEXT | NOT NULL, UNIQUE | Source system transaction ID |
| `transaction_type` | TEXT | NOT NULL | |
| `amount_cents` | BIGINT | NOT NULL | |
| `employee_id` | TEXT | NULLABLE | Denormalized source system employee ID |
| `location_id` | TEXT | NULLABLE | Denormalized source system location ID |
| `card_fingerprint` | TEXT | NULLABLE | PCI-safe hash (not PAN) |
| `card_bin` | TEXT | NULLABLE | First 6 digits — fingerprinting risk (P0: encrypt or hash) |
| `card_last4` | TEXT | NULLABLE | Last 4 digits (PCI-safe) |
| `card_exp_month` | SMALLINT | NULLABLE | Expiration month (P0: encrypt at rest) |
| `card_exp_year` | SMALLINT | NULLABLE | Expiration year (P0: encrypt at rest) |
| `statement_description` | TEXT | NULLABLE | |
| `customer_id` | TEXT | NULLABLE | Denormalized source system customer ID |
| `payload` | JSONB | NULLABLE | Full webhook JSON (P0: redact PII before storage) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_transactions_merchant_id ON (merchant_id)`
- `idx_transactions_external_id ON (external_id)` (also enforces UNIQUE)
- `idx_transactions_employee_id ON (merchant_id, employee_id)`
- `idx_transactions_location_id ON (merchant_id, location_id)`
- `idx_transactions_created_at ON (merchant_id, created_at DESC)`

##### sales.transaction_line_items

Order-level line item detail. No PII.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `transaction_id` | UUID | NOT NULL, FK → sales.transactions.id | |
| `catalog_object_id` | TEXT | NULLABLE | Source system catalog item ID |
| `quantity` | NUMERIC | NOT NULL | |
| `base_price_cents` | BIGINT | NOT NULL | |
| `is_voided` | BOOLEAN | NOT NULL, DEFAULT false | |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_transaction_line_items_transaction_id ON (transaction_id)`
- `idx_transaction_line_items_merchant_id ON (merchant_id)`

##### sales.transaction_tenders

Split payment detail. `card_last4` is PCI-safe.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `transaction_id` | UUID | NOT NULL, FK → sales.transactions.id | |
| `tender_type` | TEXT | NOT NULL | |
| `amount_cents` | BIGINT | NOT NULL | |
| `card_last4` | TEXT | NULLABLE | Last 4 digits (PCI-safe) |
| `team_member_id` | TEXT | NULLABLE | Source system team member ID |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_transaction_tenders_transaction_id ON (transaction_id)`

##### sales.refund_links

Compensating INSERT pattern for refund attribution.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `refund_external_id` | TEXT | NOT NULL | Source system refund ID |
| `original_external_id` | TEXT | NOT NULL | Source system original transaction ID |
| `refund_amount_cents` | BIGINT | NOT NULL | |
| `employee_id` | TEXT | NULLABLE | Denormalized source employee ID |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_refund_links_merchant_id ON (merchant_id)`

---

#### Order Detail (6 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.line_item_discounts` | id, merchant_id, line_item_id (FK), discount_type, amount_cents | Discount detail per line item |
| `sales.line_item_taxes` | id, merchant_id, line_item_id (FK), tax_name, amount_cents | Tax detail per line item |
| `sales.line_item_modifiers` | id, merchant_id, line_item_id (FK), modifier_name, amount_cents | Modifier detail per line item |
| `sales.service_charges` | id, merchant_id, transaction_id (FK), charge_name, amount_cents | Service charges per order |
| `sales.order_rewards` | id, merchant_id, transaction_id (FK), reward_id | Reward redemptions per order |
| `sales.order_returns` | id, merchant_id, transaction_id (FK), return_line_items (JSONB) | Return details per order |

All tables: UUID PK, `merchant_id`, `created_at`. All immutable (trigger-enforced).

---

#### Cash Management (2 tables)

##### sales.cash_drawer_shifts

**Exception: updatable** (state changes on close).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `square_shift_id` | TEXT | NOT NULL | External POS shift ID |
| `employee_id` | TEXT | NOT NULL | Denormalized source system employee ID |
| `state` | TEXT | NOT NULL, CHECK IN ('open','closed') | |
| `expected_cash_cents` | BIGINT | NOT NULL, DEFAULT 0 | |
| `closed_cash_cents` | BIGINT | NULLABLE | Set on close |
| `cash_variance_cents` | BIGINT | NULLABLE | Computed on close |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_cash_drawer_shifts_merchant_id ON (merchant_id)`

##### sales.cash_drawer_events

Append-only within a shift.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | |
| `shift_id` | UUID | NOT NULL, FK → sales.cash_drawer_shifts.id | |
| `event_type` | TEXT | NOT NULL | |
| `amount_cents` | BIGINT | NOT NULL | |
| `employee_id` | TEXT | NOT NULL | Denormalized source system employee ID |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_cash_drawer_events_shift_id ON (shift_id)`

---

#### Gift Card & Loyalty (3 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.gift_card_activities` | id, merchant_id, gift_card_id (TEXT), activity_type, amount_cents, balance_after_cents | Feeds detection rules C-601/C-602. Immutable. |
| `sales.loyalty_accounts` | id, merchant_id, square_loyalty_id, phone_hash (HMAC-SHA256 with `PHONE_HASH_KEY`, BYTEA), points_balance, lifetime_points | **Exception: updatable** (balance updates). phone_hash uses keyed HMAC — plain SHA-256 was rejected as brute-forceable on a phone-number domain. See `go-security.md` → "PII Hashing Keys". |
| `sales.loyalty_events` | id, merchant_id, loyalty_account_id (FK), event_type, points | Feeds detection rules C-801..C-804. Immutable. |

---

#### Disputes & Payouts (3 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.disputes` | id, merchant_id, square_dispute_id, payment_id, state, amount_cents, reason | Chargeback lifecycle. Immutable. |
| `sales.evidence_records` | id, merchant_id, dispute_id (FK), evidence_type, evidence_text | Dispute response documentation. Immutable. |
| `sales.payouts` | id, merchant_id, square_payout_id, status, amount_cents, arrival_date | Square settlement disbursements. Immutable. |

---

#### Inventory & Labor (2 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.inventory_adjustments` | id, merchant_id, product_id (TEXT), location_id (TEXT), adjustment_type, quantity_change | Types: PHYSICAL_COUNT, SALE, RECEIVE, SHRINKAGE, TRANSFER. Immutable. |
| `sales.employee_timecards` | id, merchant_id, employee_id (TEXT), clock_in_at, clock_out_at, overtime_hours | Feeds detection rules C-301..C-303. Immutable. |

---

#### Terminal (2 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.terminal_checkouts` | id, merchant_id, square_checkout_id, amount_cents, status | Square Terminal checkout records. Immutable. |
| `sales.terminal_refunds` | id, merchant_id, square_refund_id, amount_cents, status | Square Terminal refund records. Immutable. |

---

#### Pipeline Infrastructure (3 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.ingestion_log` | id, merchant_id, batch_id (FK), source_system, event_type, status, target_table | One row per webhook processing attempt. |
| `sales.etl_batches` | id, merchant_id, batch_type, status, total_events, processed_events, failed_events | Batch grouping for TSP pipeline. |
| `sales.dead_letter_queue` | id, merchant_id, source_system, event_type, payload (JSONB), error_message, retry_count | Failed event queue. payload may contain PII from failed events (P0: redact). |

---

#### Event Journal (5 tables)

| Table | Key Columns | Notes |
|-------|------------|-------|
| `sales.event_inscriptions` | id, merchant_id, event_type, source_id, inscription_hash | TSP Merkle-tree integrity records. |
| `sales.inscription_pool` | id, merchant_id, pool_hash, event_count | Pool aggregation for Merkle verification. |
| `sales.ej_links` | id, merchant_id, source_order_id, transaction_id | EJ spine: links source order to local transaction. |
| `sales.devices` | id, merchant_id, device_id (TEXT), device_name, location_id (TEXT) | Terminal device registry. |
| `sales.invoices` | id, merchant_id, square_invoice_id, status, amount_cents | Square invoice records. |

---

### Metrics Schema

21 tables. Star schema. Writes exclusively from ETL batch aggregation; read-heavy for dashboards and reporting. Fully re-derivable from the sales schema — never the source of truth. All writes are schema-qualified (`metrics.<table>`).

#### Fact Tables (6 tables)

| Table | Grain | Notes |
|-------|-------|-------|
| `metrics.daily_metrics` | merchant × location × date | Primary aggregation. 40+ KPI columns including gift card, loyalty, dispute, invoice, shrinkage metrics. |
| `metrics.hourly_metrics` | merchant × location × date × hour | Intraday pattern analysis. |
| `metrics.period_metrics` | merchant × location × fiscal period | NRF 4-5-4 period aggregation. SRA v2 computed here. |
| `metrics.employee_daily_metrics` | merchant × employee × location × date | Per-employee daily aggregates with off-clock and discount decomposition. |
| `metrics.employee_period_metrics` | merchant × employee × period | Per-employee period aggregates with risk scoring. |
| `metrics.product_daily_metrics` | merchant × product × location × date | Per-product daily aggregates with return rate. |

#### Dimension Tables (3 tables)

| Table | Notes |
|-------|-------|
| `metrics.dim_date` | Pre-populated calendar dimension. Fiscal fields populated by fiscal calendar service. |
| `metrics.dim_location` | SCD Type 2. `location_name` is internal, no PII. |
| `metrics.dim_employee` | SCD Type 2. `employee_name` is PII (sensitive) — P0 encryption target or replace with FK reference to `app.employees`. |

#### ML Feature Store (3 tables)

| Table | Notes |
|-------|-------|
| `metrics.transaction_features` | Per-transaction ML feature vectors. No direct PII. |
| `metrics.feature_definitions` | Feature catalog with derivation logic. No PII. |
| `metrics.ml_models` | Model registry. No PII. |

#### Risk Scoring (2 tables)

| Table | Notes |
|-------|-------|
| `metrics.entity_risk_scores` | Current risk per entity. `entity_id` can cross-reference employees/cards. |
| `metrics.risk_score_history` | Risk score timeline. Append-only. |

#### Baselines & Scorecards (7 tables)

| Table | Notes |
|-------|-------|
| `metrics.metric_baselines` | Statistical baselines for anomaly detection. |
| `metrics.velocity_baselines` | Rate-of-change baselines with day/hour granularity. |
| `metrics.scorecard_thresholds` | Heatmap band boundaries. Seeded on merchant onboarding. |
| `metrics.weekly_scorecard` | Weekly KPI snapshots. |
| `metrics.monthly_scorecard` | Monthly KPI snapshots. |
| `metrics.dashboard_config` | Per-merchant dashboard widget layout. |

---

### Memory Schema (`growdirect_memory` database)

2 tables in a separate database. Managed by the memory service.

#### memory.alx_memories

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `content` | TEXT | NOT NULL | Memory content (4000 character max for embedding) |
| `memory_type` | TEXT | NOT NULL, CHECK IN ('architecture','decision','finding','context','work_product','team_profile','context_block','foundation','session_summary','procedure') | |
| `metadata` | JSONB | NULLABLE | Structured metadata |
| `embedding` | VECTOR(768) | NULLABLE | Semantic embedding (nomic-embed-text). HNSW cosine index. |
| `narrative` | TEXT | NULLABLE | Five-narratives classification |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_alx_memories_embedding` USING hnsw ON (embedding vector_cosine_ops)
- `idx_alx_memories_memory_type ON (memory_type)`

#### memory.alx_sessions

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `session_id` | TEXT | NOT NULL, UNIQUE | Format: `alx-YYYYMMDD-HHMMSS-<hash>` |
| `gro_issues` | JSONB | NULLABLE | Linear issues loaded for this session |
| `context_snapshot` | TEXT | NULLABLE | Assembled context at session start |
| `summary` | TEXT | NULLABLE | End-of-session summary |
| `started_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `closed_at` | TIMESTAMPTZ | NULLABLE | NULL until session ends |

Indexes:
- `idx_alx_sessions_session_id ON (session_id)` (also enforces UNIQUE)

---

## Cross-Cutting Patterns

### Audit Column Pattern

Tables that require audit tracking carry these four columns (in addition to `created_at`/`updated_at`):

```sql
created_by  UUID REFERENCES app.users(id),
modified_by UUID REFERENCES app.users(id)
```

### Soft-Delete Pattern

Tables that support soft deletion carry:

```sql
db_status         TEXT NOT NULL DEFAULT 'active'
                  CHECK (db_status IN ('draft','active','archived')),
db_effective_from TIMESTAMPTZ,
db_effective_to   TIMESTAMPTZ
```

Active rows have `db_status = 'active'` and `db_effective_to IS NULL`. All queries on soft-deleted tables must filter `WHERE db_status = 'active'`.

### Tenant Scope Pattern

Every merchant-scoped table carries `merchant_id UUID NOT NULL REFERENCES app.merchants(id)` with an index. All queries must be scoped to the authenticated merchant context. Application layer must enforce — PostgreSQL RLS policies are planned but not yet implemented (P1).

When RLS is implemented, the pattern is:

```sql
-- Set session variable before any query
SET LOCAL canary.current_merchant_id = '<merchant_uuid>';

-- RLS policy on each table
CREATE POLICY tenant_isolation ON app.<table>
  USING (merchant_id = current_setting('canary.current_merchant_id')::UUID);
```

### Immutability Pattern (Sales Schema)

Sales schema tables (except `loyalty_accounts` and `cash_drawer_shifts`) are protected by a PostgreSQL trigger:

```sql
CREATE OR REPLACE FUNCTION prevent_update_delete()
RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'IMMUTABILITY VIOLATION: % on % is not permitted',
    TG_OP, TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
```

Corrections use compensating INSERTs. For example, a refund is a new row in `sales.transactions` (negative amount) linked via `sales.refund_links`, not a modification of the original.

### Hash Chain Pattern

`app.audit_log`, `app.fox_case_timeline`, and `app.fox_evidence` use SHA-256 hash chains for tamper-evident sequencing.

For `audit_log`:
- `entry_hash` = SHA-256 of (all column values concatenated)
- `previous_hash` = `entry_hash` of the immediately preceding row (NULL for first row)
- Hash chain integrity must be verified on service startup

For `fox_evidence`:
- `chain_hash` = SHA-256 of (previous `chain_hash` + current `file_hash`)

### UUID Resolution Pattern

External POS system IDs (Square, Clover, Toast) are translated to Canary internal UUIDs before any write. The translation layer is `app.external_identities`. Denormalized source IDs remain in the sales schema transaction tables for query performance but are never used as join keys between Canary-owned tables.

---

## Operations

### Startup Sequence

1. PostgreSQL must be running with `canary` database and all three schemas created
2. Migrations run before accepting traffic
3. Detection rules seeded if `detection_rules` table is empty
4. Fiscal calendar `dim_date` populated if empty
5. Hash chain integrity verified for `app.audit_log` on startup

### Failure Modes

| Failure | Impact | Recovery |
|---------|--------|----------|
| PostgreSQL down | All reads/writes fail | Connections auto-reconnect via pool on recovery |
| Hash chain broken (audit_log) | Service must halt — tamper detected | Investigate tampered rows, restore from backup |
| Immutability trigger fires | UPDATE/DELETE on sales schema rejected | Use compensating INSERT pattern instead |
| Tenant isolation breach | Data leak across merchants | Implement PostgreSQL RLS (currently application-layer only) |

### Monitoring

| Metric | Normal | Alert threshold |
|--------|--------|-----------------|
| Sales table row counts | Growing with webhook volume | Delta = 0 for >1 hour during business hours |
| Dead letter queue depth | < 10 | > 50 unresolved entries |
| Hash chain verification | Passes on every startup | Any failure = P0 |
| FK constraint violations | 0 | Any > 0 |

### Configuration

| Env Var | Purpose |
|---------|---------|
| `DATABASE_URL` | PostgreSQL connection string for `canary` database |
| `MEMORY_DATABASE_URL` | PostgreSQL connection string for `growdirect_memory` database |
| `CANARY_ENCRYPTION_KEY` | Base64-encoded 32 bytes for AES-256-GCM token encryption |

### Production Infrastructure Target

| Component | Service | Notes |
|-----------|---------|-------|
| PostgreSQL | RDS for PostgreSQL 17 | Multi-AZ, encrypted at rest |
| Encryption keys | Secrets Manager | `CANARY_ENCRYPTION_KEY` — not in environment files |
| Backups | RDS automated snapshots | 7-day retention, point-in-time recovery |

---

## Agent Memory Boundary

Agent profiles for the Canary Go PMO network are seeded documents stored in the `growdirect_memory` database (schema: `memory`, tables: `alx_memories`, `alx_sessions`). This is a separate database from `canary` and operates under a separate Docker service (`growdirect_ollama` for embeddings, `growdirect_postgres` for persistence).

**Data model boundary — critical:** Agent memory is NOT in the `canary` app/sales/metrics schemas. Do not create agent-related tables in the `canary` database. The boundary is enforced by database separation.

| Concern | Location | Notes |
|---------|----------|-------|
| Agent profiles and domain context | `growdirect_memory.alx_memories` | Seeded documents, 1024-dim pgvector embeddings |
| Session state and context snapshots | `growdirect_memory.alx_sessions` | One row per agent session |
| Merchant operational data | `canary` (app/sales/metrics schemas) | All Canary business data |
| Agent authorization records | `canary.app.audit_log` | Agent API key access logged here |

Agent session instantiation uses `memory_recall()` / `context_assemble()` calls against the memory bus at `http://127.0.0.1:8003/mcp`. Canary Go services call the memory bus via the same REST interface — they do not query `growdirect_memory` directly.

---

## ILDWAC Stock Ledger — Architectural Direction (not yet implemented)

**What this is:** IL(Device/MCP/Port/)WAC extends the retail industry standard ILWAC (Item × Location × Weighted Average Cost) with three provenance dimensions: the Device that processed the event, the MCP tool call that authorized it, and the POS Port (connector) it came through. Cost is denominated in satoshis — fiat values are the display layer. This section specifies the target schema so a future builder has the tables and can execute a formal GRO design pass without reconstructing intent.

**Status:** Architectural direction only. No migration exists. No Go service targets these tables yet. Do not declare a hard dependency on this schema until a GRO ticket formalizes the design.

**Source:** `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md` — read before implementing.

### Why ILDWAC

Standard ILWAC is device-agnostic, channel-blind, and agent-invisible. A sale through the Square connector on a mobile device authorized by an MCP agent produces an identical WAC input as a receipt through NCR Counterpoint on a fixed terminal. IL(Device/MCP/Port/)WAC records all three dimensions, seals each batch with SHA-256, and denominates cost in satoshis. The result: a cost basis that carries its own audit trail — not appended as metadata, but embedded as dimensions in the calculation itself.

| Dimension | Captures |
|-----------|---------|
| **Item** | SKU — unchanged from standard ILWAC |
| **Location** | Store or warehouse — unchanged from standard ILWAC |
| **Device** | Terminal, mobile device, or hardware that processed the originating event |
| **MCP** | MCP tool call that authorized the action — which agent, which server, which tool |
| **Port** | POS connector — `square`, `counterpoint`, `lightspeed`, or any future source |
| **WAC** | Weighted average cost, recalculated per RIB batch, denominated in satoshis |

### RIB Batches

RIB (Retail Inventory Batch) messages are domain-organized JSON batches of inventory adjustment events. Each domain in the module spine (T, V, M, D, etc.) produces structured batches. The domain origin is preserved. SHA-256 seals each batch before it touches the cost model, producing a tamper-evident input to the WAC recalculation.

### Target Schema — `ledger` Schema (new schema, not yet created)

These tables belong in a new `ledger` schema in the `canary` database, separate from `app`, `sales`, and `metrics`.

#### ledger.rib_batches

One row per sealed batch. A batch groups all inventory adjustment events from a single domain in a single processing run.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `domain` | TEXT | NOT NULL | Module domain origin: `T`, `V`, `M`, `D`, or other spine module code |
| `batch_seq` | BIGINT | NOT NULL | Monotonically increasing per (merchant_id, domain) |
| `event_count` | INT | NOT NULL | Number of ledger entries in this batch |
| `batch_hash` | TEXT | NOT NULL | SHA-256 of all entries in batch — tamper-evident seal |
| `sealed_at` | TIMESTAMPTZ | NOT NULL | When the batch was sealed |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Constraints:
- `uq_rib_batches_merchant_domain_seq` UNIQUE (merchant_id, domain, batch_seq)

Indexes:
- `idx_rib_batches_merchant_id ON (merchant_id)`
- `idx_rib_batches_merchant_domain ON (merchant_id, domain, batch_seq DESC)`

#### ledger.stock_ledger_entries

One row per inventory adjustment event. The five provenance dimensions (item, location, device, MCP, port) are all recorded. Cost is in satoshis at the time of the event. The fiat equivalent is a display-layer snapshot at event time.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `item_id` | UUID | NOT NULL, FK → app.products.id | Item (SKU) |
| `location_id` | UUID | NOT NULL, FK → app.locations.id | Store or warehouse |
| `device_id` | UUID | NULLABLE | Terminal or mobile device UUID (FK → app.devices when Device module is live) |
| `mcp_tool_call` | TEXT | NULLABLE | MCP tool name that authorized this event (e.g., `register_source`, `process_receipt`) |
| `pos_port` | TEXT | NOT NULL | POS connector: `square`, `counterpoint`, `lightspeed`, etc. |
| `event_type` | TEXT | NOT NULL, CHECK IN ('sale','receipt','transfer','adjustment') | Inventory event classification |
| `rib_batch_id` | UUID | NOT NULL, FK → ledger.rib_batches.id | Batch that sealed this entry |
| `domain` | TEXT | NOT NULL | Module domain origin — must match `rib_batches.domain` |
| `quantity_delta` | NUMERIC | NOT NULL | Positive = stock increase, negative = stock decrease |
| `cost_satoshis` | BIGINT | NOT NULL | Cost basis in satoshis at event time |
| `fiat_equivalent` | NUMERIC | NULLABLE | Fiat amount at event time — display layer only |
| `fiat_currency` | TEXT | NOT NULL, DEFAULT 'USD' | ISO 4217 currency code for fiat_equivalent |
| `hash` | TEXT | NOT NULL | SHA-256 of the RIB batch containing this entry (denormalized from rib_batches.batch_hash for fast verification) |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Access: INSERT-only. No UPDATE or DELETE. Corrections use compensating INSERTs.

Indexes:
- `idx_sle_merchant_item_location ON (merchant_id, item_id, location_id)`
- `idx_sle_rib_batch ON (rib_batch_id)`
- `idx_sle_merchant_created ON (merchant_id, created_at DESC)`

#### ledger.ilwac_positions

Current ILDWAC position per (item, location, device, MCP, port) vector. Recalculated on every RIB batch seal. One row per unique provenance combination per merchant. The WAC for an item at a location is not one number — it is a vector: one value per (Device, MCP, Port) combination that has contributed to the cost basis.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `item_id` | UUID | NOT NULL, FK → app.products.id | Item (SKU) |
| `location_id` | UUID | NOT NULL, FK → app.locations.id | Store or warehouse |
| `device_id` | UUID | NULLABLE | Terminal or mobile device UUID |
| `mcp_tool_call` | TEXT | NULLABLE | MCP tool name — provenance dimension |
| `pos_port` | TEXT | NOT NULL | POS connector — provenance dimension |
| `quantity_on_hand` | NUMERIC | NOT NULL | Current stock quantity for this provenance vector |
| `wac_satoshis` | NUMERIC | NOT NULL | Weighted average cost in satoshis for this vector |
| `wac_fiat` | NUMERIC | NULLABLE | WAC expressed in fiat — display layer |
| `last_rib_batch_id` | UUID | NOT NULL, FK → ledger.rib_batches.id | Last batch that updated this position |
| `computed_at` | TIMESTAMPTZ | NOT NULL | When this position was last recalculated |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Constraints:
- `uq_ilwac_positions_vector` UNIQUE (merchant_id, item_id, location_id, COALESCE(device_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(mcp_tool_call, ''), pos_port) — enforces one row per unique provenance vector

Indexes:
- `idx_ilwac_positions_merchant_item_location ON (merchant_id, item_id, location_id)`
- `idx_ilwac_positions_last_batch ON (last_rib_batch_id)`

### What Exists Today

| Component | Status | Location |
|-----------|--------|---------|
| ILWAC (Item × Location × WAC) | Functional | Module V — `Canary/docs/sdds/v2/module-v.md` |
| SHA-256 hash chain | Functional | Sub 1 webhook pipeline — `Canary/docs/sdds/v2/webhook-pipeline.md` |
| RaaS namespace resolution | Functional | `docs/sdds/canary/raas.md` |
| L402 middleware (Goose) | Documented | `docs/sdds/canary/goose.md` |
| Device, MCP, Port dimensions in WAC | **Not implemented** | Requires formal GRO design pass |
| Batched JSON RIB message format | **Not implemented** | Requires formal GRO design pass |
| Satoshi denomination at accounting level | **Not implemented** | Currently fiat with satoshi as parallel substrate |
| `ledger` schema | **Not implemented** | Migrations not yet created |

---

## Agent-to-Module Smart Contracts

Each agent-to-module interaction in the Canary Go PMO network follows a contract pattern: defined inputs, defined outputs, SLA targets, and an escalation path. These contracts are not ad-hoc — they are the authority surface for cross-module coordination. The formal specification for agent contracts is `agent-contracts.md` (to be authored separately). This data model SDD defers contract content to that document; the data model provides the substrate (memory bus, audit log, `mcp_tool_call` column on ledger entries) that makes contracts observable and enforceable.

---

## Granularity Enabled by Technology

The five-dimension ILDWAC vector — Item × Location × Device × MCP × Port — produces a resolution of cost and event attribution that is structurally impossible with any legacy retail database. Prior systems record what happened and to what item; this system records what happened, to what item, through which channel, by which agent action, on which device, sealed by which batch.

pgvector in-database enables semantic search over the evidence record: Fox case investigators can retrieve related events by conceptual similarity, not just exact-match query. Cost anomalies surface through vector proximity rather than manual rule construction.

SHA-256 sealed RIB batches and the Merkle evidence chain (inherited from the TSP webhook pipeline) produce a cost basis that is not just a number — it is a hashed, domain-attributed, provenance-stamped value anchored to the event that created it. The chain is verifiable at any point without trusting the application layer.

Together these three layers — provenance-dimensional cost, in-database vector search, and cryptographic event sealing — produce an audit and investigation capability that no retail analytics platform currently offers in a single system.

---

## Open Security Findings

### P0 — Blocks Production

| # | Finding | Affected Tables | Required Fix |
|---|---------|----------------|--------------|
| 1 | User email stored plaintext | `users.email`, `users.username`, `users.display_name` | Field-level AES-256-GCM encryption |
| 2 | Employee PII stored plaintext | `employees.employee_name`, `employees.email` | Field-level AES-256-GCM encryption |
| 3 | Organization billing email plaintext | `organizations.billing_email` | Field-level encryption |
| 4 | Merchant settings phone plaintext | `merchant_settings.notif_phone` | Field-level encryption |
| 5 | Bank account PII plaintext | `bank_accounts.holder_name`, `routing_number`, `secondary_routing_number` | Field-level encryption — financial PII |
| 6 | Notification recipient plaintext | `notification_log.recipient` | Field-level encryption |
| 7 | Fox subject names plaintext | `fox_subjects.name` | Field-level encryption — investigation subject PII |
| 8 | Interest signup email plaintext | `interest_signups.email` | Field-level encryption |
| 9 | Location addresses plaintext | `locations.address_line1/2`, `coordinates`, `postal_code` | Field-level encryption; consider hashing coordinates |
| 10 | Webhook payload raw PII | `webhook_events.payload`, `transactions.payload`, `dead_letter_queue.payload` | Redact PII fields before storage or encrypt column |
| 11 | Encryption keys in environment files | All encrypted fields | Move to Secrets Manager |
| 12 | Transaction card_bin plaintext | `transactions.card_bin` | Encrypt or hash — fingerprinting risk |
| 13 | Card expiration data plaintext | `transactions.card_exp_month`, `card_exp_year` | Encrypt — approaches PAN reconstruction with card_last4 |
| 14 | dim_employee plaintext names | `metrics.dim_employee.employee_name` | Encrypt or replace with FK to `app.employees` |

### P1 — Before GA

| # | Finding | Required Fix |
|---|---------|--------------|
| 1 | IP addresses logged plaintext | Hash with HMAC-SHA256 (keyed) or truncate to /24 |
| 2 | No data retention policy | Automated purge: audit_log >24mo, dead_letter_queue >90d, webhook payloads >12mo, notification_log >12mo |
| 3 | No audit trail for token access | Log every decrypt of `square_oauth_tokens` to `audit_log` |
| 4 | RLS policies not implemented | PostgreSQL RLS with `SET LOCAL canary.current_merchant_id` pattern |
| 5 | `merchants` table missing audit columns | Add `created_by`/`modified_by` tracking |
| 6 | No key rotation procedure | Document and implement bulk re-encryption on key rotation |
| 7 | Fox subject entity linking unvalidated | Integrity check: verify `entity_id` references resolve to real entities |
| 8 | Fox case timeline hash chain not implemented | Implement `entry_hash`/`previous_hash` or remove claim from spec |

### P2 — Post-Launch

| # | Finding | Required Fix |
|---|---------|--------------|
| 1 | Valkey session keys unencrypted | Enable Valkey AUTH + TLS in production |
| 2 | No PII access logging | Field-level access audit for encrypted PII columns |
| 3 | Evidence files on local filesystem | Migrate to object storage (S3/equivalent) with server-side encryption |
| 4 | No virus scanning on evidence upload | Add scanning before storage |
| 5 | Memory bus content unclassified | Classify and potentially encrypt `alx_memories.content` |
