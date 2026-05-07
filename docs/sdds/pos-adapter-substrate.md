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

# Canary — POS Adapter Substrate (Multi-POS Architecture)

**Owns:** Adapter interface contract.
**Implemented by:** Hawk (Square), Bull (NCR Counterpoint).

The substrate that makes Canary genuinely multi-POS. Every POS integration — current (Square, NCR Counterpoint) and future — is implemented as an adapter that satisfies this contract. From Sub1 onward, no downstream component knows or cares which POS produced the event.

**Tenant context.** Every adapter operates within a single tenant scope per call. The adapter does not cross tenant boundaries — credentials are per-merchant, polling and webhook handlers carry the merchant context, and emitted `CanonicalEvent` values carry `tenant_id`. The TSP pipeline routes events into the correct tenant schema (`tenant_{merchant_id}`) based on this field. See `architecture.md` "Multi-Tenant Isolation" for the canonical schema-per-tenant model.

**Optional features posture.** This SDD describes the adapter substrate. It does not depend on L402, ILDWAC, blockchain anchoring, or vendor smart contracts — adapters operate identically with all of those flags off. Where downstream services consume adapter events under those features, the cost-attribution and anchoring layers add fields to the chain payload but do not change the adapter contract.

---

## Architecture Context

```
          Webhook ingress              Poll loop
          (Square today)          (Counterpoint today)
                │                         │
                ▼                         ▼
      ┌──────────────────┐   ┌──────────────────────┐
      │ webhook_dispatch  │   │ poll_consumer         │
      │ (source-keyed)   │   │ (per-tenant scheduled) │
      └────────┬─────────┘   └──────────┬────────────┘
               │                        │
               ▼                        ▼
          CanonicalEvent ───────────────────────────────▶
                                  │
                                  ▼
                        canary:events (Valkey stream)
                                  │
                      ┌───────┬───┴────┬──────────┐
                      ▼       ▼        ▼          ▼
                   Sub1    Sub2     Sub3       Sub4
                 (seal)  (parse) (Merkle)  (detect)
```

The adapter layer is the seam. Everything downstream of the Valkey stream is POS-agnostic.

---

## Core Data Structures

### CanonicalEvent

The provider-agnostic event that flows from an adapter into the TSP pipeline. Immutable once created. The `payload` dict contains parsed, source-specific data; the raw provider payload lives in the Sub1 evidence record.

```
CanonicalEvent {
    provider:       string    // "square" | "counterpoint" | future codes
    event_type:     string    // "transaction.created" | "customer.upserted" | ...
    tenant_id:      string    // Canary merchant UUID
    occurred_at:    timestamp // Business event time (NOT ingestion time)
    external_id:    string    // Provider's native ID for this event/entity
    payload:        map       // Parsed, provider-specific data (shape varies by event_type)
    company_alias:  string?   // Multi-company providers (Counterpoint); nil for Square
}

Invariants:
  - provider, event_type, tenant_id are required (empty string is invalid)
  - occurred_at is the POS-reported transaction time, not the ingestion timestamp
  - payload shape per event_type is documented in per-adapter SDDs
```

### Fixture

Seed data batch for a freshly connected tenant. Used during reference-data seeding at install time.

```
Fixture {
    entity_type:    string    // "store" | "item" | "customer" | "category" | ...
    rows:           []map     // Rows to insert into reference tables
    source:         string    // Provider code
    tenant_id:      string
    company_alias:  string?
}
```

### PollResult

Result of one poll invocation for one entity type.

```
PollResult {
    entity_type:    string
    events:         []CanonicalEvent
    new_watermark:  timestamp?  // nil = no update (no events returned)
    pages_fetched:  int
    error:          error?      // nil = success
}
```

---

## POS Adapter Interface

Every POS adapter must implement this interface. Partial adapters are not permitted — an adapter that has neither a webhook surface nor a poll surface fails validation at startup.

```
POSAdapter interface {
    // Identity
    Provider() string    // Returns "square" | "counterpoint" | etc.

    // Webhook surface
    // Returns the set of event_type strings this adapter accepts on webhook ingress.
    // Return empty set for poll-only adapters.
    WebhookEventTypes() []string

    // Parse a raw webhook payload into a CanonicalEvent.
    // Return nil to discard (unsupported event type, test ping, etc.).
    // Return error to dead-letter (malformed payload that should not be retried silently).
    ParseWebhook(rawEvent map[string]any) (*CanonicalEvent, error)

    // Poll surface
    // Returns per-entity-type polling cadence as a map of entity_type → interval.
    // Return empty map for webhook-only adapters.
    // Keys must match poll_watermarks.entity_type values.
    PollIntervals() map[string]time.Duration

    // Pull events for one entity_type since the watermark.
    // Must be idempotent: calling Poll twice with the same `since` produces
    // the same events or a superset — never fewer events.
    // credentials: decrypted credential map for this tenant
    // companyAlias: nil for Square; required for Counterpoint multi-company
    Poll(
        tenantID     string,
        entityType   string,
        since        time.Time,
        credentials  map[string]any,
        companyAlias *string,
    ) PollResult

    // Credential management
    // Returns the auth flow handler for this adapter (handles connection testing and
    // credential collection during onboarding).
    AuthFlowHandler() AuthFlowHandler

    // Seed reference data for a freshly connected tenant.
    // Yields Fixture batches in activation-ordering sequence:
    //   1. Stores/locations
    //   2. Categories
    //   3. Items
    //   4. Customers
    //   5. (other entity types as needed)
    // Called once per tenant activation.
    SeedData(
        ctx          context.Context,
        tenantID     string,
        credentials  map[string]any,
        companyAlias *string,
    ) (<-chan Fixture, <-chan error)

    // Verify credentials and API reachability.
    // Called during onboarding before writing to pos_tenant_credentials.
    // Returns error with user-visible message on failure.
    TestConnection(
        ctx          context.Context,
        credentials  map[string]any,
        companyAlias *string,
    ) error
}
```

### AuthFlowHandler Interface

```
AuthFlowHandler interface {
    // Returns the credential fields required for onboarding UI.
    // Field definitions: label, type (text/password/url/checkbox), required, hint.
    CredentialFields() []CredentialField

    // Validate the provided credential map before attempting TestConnection.
    ValidateCredentials(credentials map[string]any) error

    // For OAuth-based adapters (Square): returns the authorization URL.
    // For key-based adapters (Counterpoint): returns nil.
    AuthorizationURL(state string) *string

    // For OAuth-based adapters: exchange authorization code for tokens.
    // For key-based adapters: no-op, return nil.
    ExchangeCode(ctx context.Context, code string) (map[string]any, error)
}
```

---

## Adapter Registry

The registry maps provider codes to adapter implementations. Loaded at application startup via explicit registration — no auto-discovery.

```
// Registration (called at startup for each adapter)
RegisterAdapter(provider string, adapter POSAdapter) error
// Error if provider already registered or provider string is empty.

// Lookup
GetAdapter(provider string) (POSAdapter, error)
// Error (KeyError equivalent) if provider not registered.

// List all registered providers
Providers() []string
```

**Startup validation:** After all adapters register, validate that:
1. `Providers()` contains at least `["square", "counterpoint"]`.
2. Every adapter has either a non-empty `WebhookEventTypes()` or a non-empty `PollIntervals()` (or both).
3. No (provider, event_type) pair is registered by two different adapters.

---

## Webhook Dispatch

The webhook dispatch layer routes raw inbound payloads to the correct adapter using a compound `(provider, event_type) → parser` dispatch table.

```
Dispatch table key: (provider string, event_type string)
Dispatch table value: func(rawEvent map[string]any) (*CanonicalEvent, error)
```

**Registration:** At startup, iterate all registered adapters. For each adapter, for each event_type in `WebhookEventTypes()`, register `(provider, event_type) → adapter.ParseWebhook`.

**Dispatch flow:**
1. Receive inbound request. Identify `provider` from URL path or signature key lookup.
2. Extract `event_type` from raw payload (field path varies by provider).
3. Look up parser in dispatch table using `(provider, event_type)`.
4. If no parser found: discard (unknown event type for this provider). Return 200 to sender.
5. Call parser: if it returns nil, discard. If it returns error, dead-letter. If it returns a CanonicalEvent, publish to `canary:events` Valkey stream.

Square webhooks route through `provider = "square"`. This is a non-breaking change — Square behavior is unchanged.

**Webhook verification (HMAC):** Each adapter's `ParseWebhook` is responsible for verifying the request signature before parsing. If verification fails, return an error — the dispatch layer will dead-letter. Never process payloads from unverified sources.

---

## Transaction Normalization — Canonical Retail Data Model (CRDM)

Every adapter must populate these fields when producing CanonicalEvent payloads for `transaction.created` and `transaction.updated` event types. This is the CRDM contract — the fields that downstream TSP parsers, Chirp rules, and Analytics depend on.

| CRDM Field | Type | Required | Description |
|---|---|:---:|---|
| `external_id` | string | YES | POS-native transaction ID |
| `merchant_id` | string | YES | Canary tenant UUID |
| `source_merchant_id` | string | YES | POS-native merchant/store ID |
| `location_id` | string | YES | POS-native location/store identifier |
| `amount_cents` | integer | YES | Transaction total in cents (integer only; no decimals) |
| `currency` | string | YES | ISO 4217 currency code (e.g., "USD") |
| `occurred_at` | timestamptz | YES | POS-reported transaction time (NOT ingestion time) |
| `transaction_type` | enum | YES | `sale`, `refund`, `void`, `exchange` |
| `tender_type` | enum | YES | `card`, `cash`, `other`, `split` |
| `card_last4` | string | NO | Last 4 digits of card; nil for cash transactions |
| `card_brand` | string | NO | `VISA`, `MASTERCARD`, `AMEX`, etc. |
| `employee_id` | string | NO | POS-native employee identifier |
| `employee_name` | string | NO | Employee display name (used for alert attribution) |
| `item_count` | integer | NO | Number of line items |
| `discount_amount_cents` | integer | NO | Total discount applied, in cents |
| `refund_reason` | string | NO | For refund transactions: reason code or text |
| `void_reason` | string | NO | For void transactions: reason code |
| `parent_transaction_id` | string | NO | For refunds/voids: external_id of original transaction |
| `raw_provider_ref` | string | NO | Provider-specific opaque reference for reconciliation |

**Monetary invariant:** All monetary fields use integer cents. Never store decimal dollars. Adapters must convert provider decimal amounts to cents before populating CRDM fields.

---

## Schema Contracts

### `app.source_systems` — Provider Catalog

One row per registered adapter. Seeded by migration. `source_code` is the FK target for `external_identities.source_code`.

```sql
CREATE TABLE app.source_systems (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_code      TEXT        NOT NULL UNIQUE,       -- 'square' | 'counterpoint'
    display_name     TEXT        NOT NULL,
    adapter_ref      TEXT        NOT NULL,              -- implementation reference (documentation only)
    supports_webhook BOOLEAN     NOT NULL DEFAULT false,
    supports_poll    BOOLEAN     NOT NULL DEFAULT false,
    is_active        BOOLEAN     NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed rows (idempotent)
INSERT INTO app.source_systems (source_code, display_name, adapter_ref, supports_webhook, supports_poll)
VALUES
    ('square',       'Square',           'pos/square',       true,  false),
    ('counterpoint', 'NCR Counterpoint', 'pos/counterpoint', false, true)
ON CONFLICT (source_code) DO NOTHING;
```

### `app.pos_tenant_credentials` — Credential Storage

One row per `(merchant_id, source_code, company_alias)`. For Square: one row per merchant, `company_alias` IS NULL. For Counterpoint: one row per company alias per merchant.

```sql
CREATE TABLE app.pos_tenant_credentials (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID        NOT NULL REFERENCES app.merchants(id),
    source_code      TEXT        NOT NULL REFERENCES app.source_systems(source_code),
    company_alias    TEXT,               -- NULL for Square; required for Counterpoint
                                         -- A merchant may have multiple CP companies

    -- AES-256-GCM encrypted credential blob.
    -- Shape varies by provider (see Credential Shapes section).
    -- credentials_enc must NEVER appear in query result sets that feed logs or audit tables.
    credentials_enc  BYTEA       NOT NULL,

    -- Lifecycle
    status           TEXT        NOT NULL DEFAULT 'active',
                     -- active | credential_error | suspended | disconnected
    status_reason    TEXT,
    last_tested_at   TIMESTAMPTZ,
    last_polled_at   TIMESTAMPTZ,
    error_count      INTEGER     NOT NULL DEFAULT 0,
    last_error_at    TIMESTAMPTZ,
    last_error_msg   TEXT,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (merchant_id, source_code, company_alias)
    -- Note: PostgreSQL NULL-distinct semantics apply — two rows with
    -- (merchant_id=X, source_code='counterpoint', company_alias=NULL) would collide,
    -- but (company_alias='MAINCO') and (company_alias='REGIONCO') coexist.
    -- Square rows use company_alias IS NULL; this is safe under UNIQUE with NULLs.
);

CREATE INDEX idx_pos_creds_merchant_source
    ON app.pos_tenant_credentials (merchant_id, source_code);

CREATE INDEX idx_pos_creds_active_source
    ON app.pos_tenant_credentials (source_code, status)
    WHERE status = 'active';
```

### `app.poll_watermarks` — Poll State

Tracks the last successfully processed event timestamp per (merchant, source, company_alias, entity_type). The poll consumer reads and advances watermarks.

```sql
CREATE TABLE app.poll_watermarks (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID        NOT NULL REFERENCES app.merchants(id),
    source_code      TEXT        NOT NULL REFERENCES app.source_systems(source_code),
    company_alias    TEXT,
    entity_type      TEXT        NOT NULL,   -- "transaction" | "customer" | "item" | "store" | ...
    last_event_ts    TIMESTAMPTZ,            -- NULL = never polled (use epoch as since)
    last_polled_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (merchant_id, source_code, company_alias, entity_type)
);
```

---

## Credential Shapes (per provider)

Credentials are stored encrypted in `credentials_enc` as a JSON blob. The fields below are the decrypted shape.

**Square:**
```json
{
  "access_token":       "EAAAl...",
  "refresh_token":      "EQAAl...",
  "token_type":         "bearer",
  "expires_at":         "2026-07-01T00:00:00Z",
  "merchant_id_square": "MLXXXXXXXXX"
}
```

**NCR Counterpoint:**
```json
{
  "host":          "https://pos.retailerstore.com:81",
  "username":      "MGMT",
  "password":      "...",
  "api_key":       "...",
  "company_alias": "MAINCO",
  "verify_ssl":    true
}
```

---

## Credential Service Contract

```
CredentialService {
    // Encrypt and persist credentials. Returns credential row ID.
    Store(merchantID, sourceCode, companyAlias string, credentials map[string]any) (string, error)

    // Load and decrypt credentials.
    // Returns ErrCredentialNotFound if no row exists for this key.
    Load(merchantID, sourceCode, companyAlias string) (map[string]any, error)

    // Replace encrypted blob without changing the row ID.
    // Used for OAuth token refresh.
    Rotate(credentialID string, newCredentials map[string]any) error

    // Set status = 'credential_error'. Halts polling for this source.
    Invalidate(credentialID, reason string) error
}
```

**Encryption:** AES-256-GCM. Key derived from application secret + per-row salt. `credentials_enc` must never appear in query results that feed logging or audit tables. Credentials are never logged.

---

## Merchant Source Status Lifecycle

```
(onboarding) → TestConnection()
                    │ success
                    ▼
               ┌─────────┐
               │ active  │◀──────────────────────┐
               └────┬────┘                       │
                    │ poll error                  │ credential rotated
                    ▼                             │ + TestConnection ok
          ┌──────────────────┐                   │
          │ credential_error │───────────────────┘
          └──────────────────┘
                    │
                    │ operator disconnects
                    ▼
            ┌─────────────┐
            │ disconnected │
            └─────────────┘
```

**Error escalation rules:**
- 3 or more consecutive failed `Poll()` calls within a 1-hour window → transition status to `credential_error`.
- Any single 401 or 403 response from the POS API → immediate `credential_error` (no grace period for auth failures).
- On `credential_error`: halt all polling for this credential; emit notification event to merchant's primary user.
- Re-activation requires calling `TestConnection()` with new credentials followed by `Rotate()`.

---

## Poll Consumer Loop

The poll consumer is a background process (goroutine or separate service instance) that drives all poll-based adapters.

**Algorithm:**
```
loop:
  active_creds = SELECT * FROM app.pos_tenant_credentials ptc
                 JOIN app.source_systems ss ON ptc.source_code = ss.source_code
                 WHERE ptc.status = 'active'
                   AND ss.supports_poll = true

  for each cred in active_creds:
    adapter = GetAdapter(cred.source_code)
    intervals = adapter.PollIntervals()

    for each (entity_type, interval) in intervals:
      watermark = LoadWatermark(cred.merchant_id, cred.source_code,
                                cred.company_alias, entity_type)

      if watermark.last_polled_at + interval > now():
        continue  // not due yet

      credentials = CredentialService.Load(cred.merchant_id, cred.source_code,
                                           cred.company_alias)
      result = adapter.Poll(
        tenantID:     cred.merchant_id,
        entityType:   entity_type,
        since:        watermark.last_event_ts ?? epoch,
        credentials:  credentials,
        companyAlias: cred.company_alias,
      )

      if result.error == nil:
        for event in result.events:
          PublishToStream(event)     // write to canary:events Valkey stream
        AdvanceWatermark(watermark, result.new_watermark)
        ResetErrorCount(cred)
      else:
        HandlePollError(cred, result.error)

  sleep(POLL_CONSUMER_TICK)         // default 30s outer loop
```

**POLL_CONSUMER_TICK** (30s) is the outer loop frequency. Actual poll frequency per entity is governed by `PollIntervals()` + watermark state — the outer loop tick is just the wakeup cadence.

**On-demand poll (fast path):** [SPEC ADDITION — not in prototype] For cache-miss scenarios (e.g., an item lookup finds no local record), the TSP parser publishes a demand signal to a `canary:poll_demand` stream. The poll consumer drains this stream before sleeping, bypassing the interval check for the demanded entity. This reduces cache-miss latency from up to `interval` time to near-real-time.

---

## [SPEC ADDITION — not in prototype] POS Adapter Contract Tests

Every registered adapter must pass the following contract test suite. The suite is parameterized across all registered adapters and runs green with zero adapters (vacuously true). It begins failing as adapters register without full conformance.

**Required tests per adapter:**

| Test | Assertion |
|---|---|
| `TestProviderSet` | `Provider()` returns a non-empty string |
| `TestWebhookEventTypesType` | `WebhookEventTypes()` returns `[]string` (may be empty) |
| `TestPollIntervalsType` | `PollIntervals()` returns `map[string]time.Duration` (may be empty) |
| `TestHasAtLeastOneSurface` | At least one of `WebhookEventTypes()` or `PollIntervals()` is non-empty |
| `TestAuthFlowHandler` | `AuthFlowHandler()` returns a non-nil handler |
| `TestNoProviderEventTypeCollision` | No two adapters register the same `(provider, event_type)` pair |
| `TestPollIdempotent` | Calling `Poll()` twice with the same `since` returns the same events or a superset |
| `TestTestConnectionReturnsTypedError` | `TestConnection()` with bad credentials returns a typed error with user-visible message |
| `TestSeedDataYieldsFixtures` | `SeedData()` yields at least one Fixture for a tenant with reference data |
| `TestCanonicalEventInvariantsOnWebhook` | `ParseWebhook()` returns events with non-empty provider, event_type, tenant_id |

---

## [SPEC ADDITION — not in prototype] Rate Limit and Retry Contract

Every adapter's `Poll()` implementation must handle POS API rate limits gracefully:

| Scenario | Required behavior |
|---|---|
| HTTP 429 from POS API | Backoff using Retry-After header value, or exponential backoff (base 2s, max 60s). Return partial PollResult with `pages_fetched > 0` if any data was collected. |
| HTTP 5xx from POS API | Retry up to 3 times with 2s exponential backoff. After 3 failures, return PollResult with error. |
| Network timeout | Return PollResult with error. Do not advance watermark. |
| Partial page success | Return events collected so far with `new_watermark` pointing to the last successful page boundary. |

---

## [SPEC ADDITION — not in prototype] Dead-Letter Contract

Events that fail validation or parsing are dead-lettered rather than dropped silently.

**Dead-letter record:**
```
DeadLetterRecord {
    id:           UUID
    provider:     string
    raw_payload:  bytes         // original payload, never modified
    error_reason: string        // human-readable failure description
    source:       string        // "webhook" | "poll"
    received_at:  timestamptz
    retry_count:  int
    last_retry_at: timestamptz?
}
```

Dead-lettered events are written to a `canary:dead_letter` Valkey stream (or a database table if the volume warrants). An operations endpoint surfaces dead-letter queue depth. Dead-letter records older than 30 days are pruned.

---

## File Structure (Target)

```
pos/
├── adapter.go          // POSAdapter interface, AuthFlowHandler interface
├── canonical.go        // CanonicalEvent, Fixture, PollResult structs
├── registry.go         // RegisterAdapter, GetAdapter, Providers
├── poll_consumer.go    // Poll loop goroutine
├── credential_service.go // AES-256-GCM encrypt/decrypt for pos_tenant_credentials
├── webhook_dispatch.go // Compound (provider, event_type) dispatch table
│
├── square/
│   ├── adapter.go      // SquareAdapter (registered on package init)
│   ├── oauth.go        // OAuth2 flow, token refresh
│   ├── verify.go       // HMAC signature verification
│   └── parsers/
│       └── ...         // One file per Square event type
│
└── counterpoint/
    ├── adapter.go      // CounterpointAdapter (registered on package init)
    ├── auth.go         // Basic Auth + API key flow
    ├── client.go       // HTTP client with auth headers
    └── parsers/
        ├── document.go // XFER, RECVR, PO, RA documents
        ├── customer.go
        ├── item.go
        └── store.go
```

---

## New Tables — Migration Checklist

| Table | Schema | Action | Notes |
|---|---|---|---|
| `source_systems` | app | CREATE + seed | Provider catalog; FK target for external_identities |
| `pos_tenant_credentials` | app | CREATE | Encrypted credentials + lifecycle |
| `poll_watermarks` | app | CREATE | Per-entity poll state |
| `external_identities.source_code` | app | ADD FK | FK to source_systems.source_code |

---

## Acceptance Criteria

**AC-POS-01 — Registry:** After startup, `Providers()` returns at least `["square", "counterpoint"]`. `GetAdapter("unknown")` returns an error.

**AC-POS-02 — Contract tests:** The full contract test suite passes with both Square and Counterpoint registered.

**AC-POS-03 — Square non-regression:** After the refactor, all existing Square integration tests pass without modification. Square webhook dispatch produces identical CanonicalEvents as before.

**AC-POS-04 — Credential storage:** `CredentialService.Store()` encrypts credentials such that `credentials_enc` contains no plaintext passwords or API keys. `CredentialService.Load()` round-trips correctly.

**AC-POS-05 — Poll consumer cadence:** For a Counterpoint tenant with `PollIntervals = {"transaction": 1m}`, the poll loop invokes `Poll()` at approximately 1-minute intervals. Jitter within ±10s is acceptable.

**AC-POS-06 — Credential error halt:** Three consecutive failed `Poll()` calls transition `pos_tenant_credentials.status` to `credential_error`. Subsequent poll loop iterations skip this credential. A notification event is emitted.

**AC-POS-07 — Multi-company:** A single merchant with two Counterpoint company aliases (`MAINCO` + `REGIONCO`) has two rows in `pos_tenant_credentials`. The poll consumer polls each independently with the correct alias. Events from both companies reach the `canary:events` stream with distinct `company_alias` values.

---

## [ARCHITECTURAL DIRECTION — not yet implemented] ILDWAC Dimension Fields in Event Envelopes

Every event envelope emitted by a POS adapter MUST include two ILDWAC input dimensions as part of the canonical event envelope passed to the TSP pipeline. These fields are captured at the adapter boundary — the only point in the system that knows the source connector and the originating device.

### Required Envelope Fields

| Field | Type | Required | Source | ILDWAC Dimension |
|---|---|:---:|---|---|
| `pos_port` | string | YES | Adapter identity — hardcoded per adapter | Port |
| `device_id` | string or null | NO | POS payload (terminal/device identifier if present) | Device |

### Field Contracts

**`pos_port`** — The adapter identifier string. Set by the adapter implementation, not extracted from the payload. This is a stable, system-assigned code:

| Adapter | `pos_port` value |
|---|---|
| Hawk (Square) | `"square"` |
| Bull (NCR Counterpoint) | `"counterpoint"` |
| Future adapters | Defined at registration time in `app.source_systems.source_code` |

**`device_id`** — The terminal or mobile device identifier, extracted from the POS payload if present. Null if the POS does not provide a device identifier for this event type, or if the event is a poll-derived batch that has no single device origin.

### Purpose

These fields are ILDWAC input dimensions. IL(Device/MCP/Port/)WAC extends the standard Item × Location × Weighted Average Cost model with three provenance dimensions: Device, MCP, and Port. The adapter layer is where Port and Device are first known; they must be preserved in the envelope so the stock ledger can populate the full ILDWAC vector. See `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md` for the full model.

The MCP dimension (which MCP tool call authorized the cost-affecting action) is NOT captured in the adapter or pipeline layers — it is captured by the MCP authorization middleware at the time of tool invocation.

### Adapter Contract Test Addition

The existing contract test suite (see `[SPEC ADDITION — not in prototype] POS Adapter Contract Tests`) must be extended with:

| Test | Assertion |
|---|---|
| `TestEnvelopePosPort` | `pos_port` field is a non-empty string on every emitted CanonicalEvent |
| `TestEnvelopeDeviceIdType` | `device_id` field is a string or null — never an empty string |

---

## Open Questions

| ID | Question | Impact |
|---|---|---|
| POS-OQ-01 | `UNIQUE (merchant_id, source_code, company_alias)` with `company_alias = NULL` for Square: PostgreSQL NULL-distinct semantics mean two NULL values are considered distinct in some index implementations. Verify the UNIQUE constraint prevents double-registration for Square. | Schema correctness |
| POS-OQ-02 | Credential encryption key management: application-secret derivation (current) vs. per-merchant KMS CMK. KMS adds operational complexity but narrows key-compromise scope to one tenant. | Security posture |
| POS-OQ-03 | Poll consumer deployment: goroutine spawned at startup vs. separate process vs. cron-triggered. Recommendation: goroutine with graceful shutdown signal, separate from HTTP server goroutines. | Deployment architecture |
| POS-OQ-04 | On-demand poll trigger latency. Cache-miss events in TSP parsers currently wait up to `interval` duration for the next poll cycle. The `canary:poll_demand` stream (see §Poll Consumer Loop fast path) is the proposed solution. Confirm stream-based design before implementation. | Cache-miss latency |
