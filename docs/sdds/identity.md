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

# Identity

**Service Type:** App Service
**Last Code Review:** 2026-04-13
**Related:** `data-model.md` (full schema), `external-identities.md` (entity resolution)

---

## Purpose

The Identity domain owns merchant registration, user authentication, RBAC, OAuth token management, and tenant context injection. It is the security perimeter and tenant boundary for every other domain in Canary. No data flows into the system until Identity establishes a merchant via Square OAuth.

**Core principle:** Organization is business identity; Merchant is POS connection. One organization owns 1..N merchants (multi-location chains). Each merchant maps to exactly one OAuth token. All tenant-scoped tables carry `merchant_id` as a scoped foreign key.

---

## Dependencies

| Dependency | Type | Required | Notes |
|------------|------|----------|-------|
| PostgreSQL (`canary` DB, `app` schema) | Database | Yes | All identity tables |
| Valkey (DB 0) | Cache/Sessions | Yes | Server-side sessions, rate limiter state, OAuth CSRF state |
| Square OAuth2 API | External API | Yes | Token exchange, refresh, revocation |
| Square Merchants API | External API | Yes | Business name, currency, country during provisioning |
| Onboarding Coordinator | Internal Service | No | Webhook registration + initial sync (best-effort) |
| Rate limiter | Middleware | Yes | 2000/day, 500/hr default; stricter on OAuth endpoints (P1) |
| Security headers | Middleware | Yes | CSP, HSTS |
| CSRF protection | Middleware | Yes | All state-mutating form endpoints |
| AES-256-GCM | Crypto | Yes (prod) | Token encryption |

---

## Data Flow & PII Map

### What Enters

| Source | Data | Format |
|--------|------|--------|
| Square OAuth callback | Authorization code, merchant_id, access_token, refresh_token | HTTPS redirect + JSON token response |
| Square Merchants API | Business name, currency, country | JSON API response |
| User browser | Email (pre-OAuth capture), session cookie | Form POST, HTTP cookie |
| Internal agents | `X-API-Key` header, `merchant_id` in params | HTTP headers + JSON |

### What Is Stored

| Table | Field | PII Classification | Encryption | Notes |
|-------|-------|-------------------|------------|-------|
| `app.users` | `email` | sensitive | Plaintext | P0: encrypt at rest |
| `app.users` | `username` | sensitive | Plaintext | P0: encrypt at rest. Derived from email — same risk profile (linkable to identity, often identical to email local-part). |
| `app.users` | `display_name` | sensitive | Plaintext | P0: encrypt at rest. Derived from email and shares the same risk profile. |
| `app.users` | `last_login_at` | internal | Plaintext | |
| `app.organizations` | `billing_email` | sensitive | Plaintext | P0: encrypt at rest |
| `app.organizations` | `org_name` | internal | Plaintext | |
| `app.merchants` | `merchant_name` | internal | Plaintext | |
| `app.merchant_settings` | `notif_phone` | sensitive | Plaintext | P0: encrypt at rest |
| `app.square_oauth_tokens` | `access_token_encrypted` | restricted | AES-256-GCM | Encrypted at rest |
| `app.square_oauth_tokens` | `refresh_token_encrypted` | restricted | AES-256-GCM | Encrypted at rest |
| `app.interest_signups` | `email` | sensitive | Plaintext | P0: encrypt at rest |
| `app.employees` | `email` | sensitive | Plaintext | P1: encrypt at rest |
| `app.employees` | `phone` | sensitive | Plaintext | P1: encrypt at rest |
| `app.employees` | `name` | sensitive | Plaintext | P0: encrypt at rest. The `show_employee_names=false` setting is a runtime display mask applied at the presentation layer, not a classification downgrade — the underlying field is sensitive regardless of display state. |
| `app.audit_log` | `ip_address` | sensitive | Plaintext | P1: hash or mask |
| Valkey | Session data (user_id, roles, merchant_id) | internal | Plaintext | P2: enable TLS + AUTH |
| Valkey | OAuth CSRF state tokens | internal | Plaintext | 5-min TTL, one-time use |

### What Exits

| Destination | Data | Notes |
|-------------|------|-------|
| Square OAuth2 API | client_id, client_secret, access_token (in refresh), refresh_token (in refresh) | Token exchange, refresh, revocation |
| Request context | user_id, roles, merchant_id, merchant_ids, organization_id, display_name | Consumed by all downstream handlers via middleware |
| Browser session cookie | Session ID only (data is server-side in Valkey) | HttpOnly, SameSite=Lax, Secure (prod) |
| MCP tool responses | Merchant profile, employee list, location list | Read-only, tenant-scoped |

---

## API Contract

### Auth Routes

| Path | Method | Auth | Rate Limit | Description |
|------|--------|------|------------|-------------|
| `/auth/login-page` | GET | Public | Default | Landing page with "Connect with Square" button |
| `/auth/join` | GET | Public | Default | Alias for login-page |
| `/auth/start-connect` | POST | Public (CSRF exempt) | Default | Capture pre-OAuth email into session |
| `/auth/session-logout` | POST | Session | Default | Clear session, redirect to `/auth/join` |
| `/auth/clear-session` | POST | Public (CSRF exempt) | Default | Emergency session reset |
| `/auth/join-interest` | POST | Public (CSRF exempt) | Default | Capture email for beta interest list |

### OAuth Routes

| Path | Method | Auth | Rate Limit | Description |
|------|--------|------|------------|-------------|
| `/oauth/authorize` | GET | Public | 10/min (P1) | Redirect to Square OAuth consent screen with CSRF state |
| `/oauth/callback` | GET | Public | 10/min (P1) | Exchange auth code, provision merchant, run onboarding |
| `/oauth/status` | GET | JWT | Default | Check token connection status and expiry |
| `/oauth/refresh` | POST | JWT + admin | 5/min (P1) | Force-refresh access token via Square API |
| `/oauth/disconnect` | POST | JWT + owner/admin | Default | Revoke token and disconnect source |
| `/oauth/sandbox` | GET | Public (sandbox only) | Default | Dev shortcut: store sandbox tokens directly |
| `/oauth/reset-onboarding` | GET | Session + admin (sandbox only) | Default | Factory reset: truncate transactional data |
| `/oauth/merchant-reset` | POST | Session (sandbox only) | Default | Tenant-scoped reset |
| `/oauth/factory-reset` | POST | Session (sandbox only) | Default | Full data truncate + Valkey flush |

### Merchant REST

| Path | Method | Auth | Description |
|------|--------|------|-------------|
| `/api/merchants/profile` | GET | JWT | Get merchant profile |
| `/api/merchants/profile` | PUT | JWT | Update merchant profile |
| `/api/merchants/settings` | GET | JWT | Get merchant settings |
| `/api/merchants/settings` | PUT | JWT | Update merchant settings |

### Employee REST

| Path | Method | Auth | Description |
|------|--------|------|-------------|
| `/api/employees/` | GET | JWT | List employees (filterable) |
| `/api/employees/{id}` | GET | JWT | Get single employee |
| `/api/employees/{id}/transactions` | GET | JWT | Employee transaction history |
| `/api/employees/{id}/alerts` | GET | JWT | Employee alert history |
| `/api/employees/{id}/risk` | GET | JWT | Employee risk score |

### Location REST

| Path | Method | Auth | Description |
|------|--------|------|-------------|
| `/api/locations/` | GET | JWT | List locations |
| `/api/locations/{id}` | GET | JWT | Get single location |
| `/api/locations/{id}` | PUT | JWT | Update location |
| `/api/locations/{id}/stats` | GET | JWT | Location statistics |

### Identity MCP Tools

Read-only tools for internal agent access. Authenticated via `X-API-Key` header.

| Tool | PII Access | Description |
|------|:----------:|-------------|
| `get_merchant` | merchant_name | Get merchant profile |
| `get_settings` | notification prefs | Get merchant settings |
| `list_employees` | name, email, phone | List employees for merchant |
| `get_employee` | name, email, phone, risk_score | Get single employee detail |
| `list_locations` | address | List locations for merchant |
| `get_location` | address | Get single location detail |

Standard discovery endpoints: `/identity/manifest`, `/identity/tools`, `/identity/tools/{name}`, `/identity/health`.

---

## Data Model

All Identity tables live in the `app` schema. Full column-level specs are in `data-model.md`. This section documents relationships, access patterns, and business rules.

### Table Inventory

| Table | Domain Purpose | Access Pattern |
|-------|---------------|----------------|
| `app.organizations` | Root business entity; owns merchants | CRUD + soft-delete |
| `app.merchants` | POS connection; tenant boundary | CRUD |
| `app.merchant_settings` | Per-merchant config; one row per merchant | CRUD |
| `app.users` | User account; multi-tenant via user_roles | CRUD + soft-delete |
| `app.roles` | Global RBAC definitions (6 roles; seeded) | Read-mostly |
| `app.user_roles` | Tenant-scoped role assignments | CRUD |
| `app.employees` | Staff synced from POS | CRUD + soft-delete |
| `app.locations` | Stores synced from POS | CRUD + soft-delete |
| `app.location_hierarchy` | Multi-level location grouping | CRUD |
| `app.customers` | Customer IDs from POS; no PII stored | CRUD + soft-delete |
| `app.products` | Catalog items from POS | CRUD + soft-delete |
| `app.square_oauth_tokens` | Encrypted OAuth credentials; one per merchant | CRUD (service-managed) |
| `app.source_systems` | POS platform registry (seeded) | Read-mostly |
| `app.merchant_sources` | Merchant-to-POS connection status | CRUD |
| `app.interest_signups` | Pre-launch prospect list | Append-only |

### Key Relationships

```
app.organizations (1)
  └─ app.merchants (N)
       ├─ app.merchant_settings (1)
       ├─ app.square_oauth_tokens (1)
       ├─ app.merchant_sources (N)
       ├─ app.users (N)
       │    └─ app.user_roles (N) → app.roles (global)
       ├─ app.employees (N)
       │    └─ app.employee_location_assignments (N) → app.locations
       └─ app.locations (N)
            └─ app.location_hierarchy (N, self-referential)
```

---

## Workflows

### Merchant Onboarding (Square OAuth)

This is the only entry point for merchants. No data flows until OAuth completes.

#### Step 1: Authorize

1. User arrives at `/auth/join` — landing page.
2. `GET /oauth/authorize` generates CSRF state token (32 bytes, cryptographically random).
3. State stored in Valkey with 5-minute TTL at key `canary:oauth_state:{state}`, plus session fallback for environments where Valkey is unavailable.
4. Redirect to `https://connect.squareup[sandbox].com/oauth2/authorize` with:
   - `client_id`: Square application ID
   - `scope`: `MERCHANT_PROFILE_READ,PAYMENTS_READ,ORDERS_READ,EMPLOYEES_READ,TIMECARDS_READ,ITEMS_READ,INVENTORY_READ,GIFTCARDS_READ,CUSTOMERS_READ,CASH_DRAWER_READ`
   - `state`: CSRF token
   - `redirect_uri`: configured OAuth callback URL

#### Step 2: Callback (Token Exchange + Provisioning)

1. Square redirects to `GET /oauth/callback` with `code` and `state`.
2. CSRF state validation: check Valkey first (survives TLS proxy redirects), session fallback. Mismatch → redirect with error.
3. Exchange `code` for `access_token` + `refresh_token` via `POST https://connect.squareup[sandbox].com/oauth2/token`.
4. **Provision merchant:**
   - Check if `app.merchants` row exists for this Square merchant ID — return existing UUID if found.
   - Fetch business name, currency, country from Square Merchants API.
   - Create `app.merchants` row (new Canary-internal UUID).
   - Create `app.merchant_settings` row with defaults (timezone inferred from country).
5. **Store token:** Encrypt `access_token` and `refresh_token` using AES-256-GCM. Write to `app.square_oauth_tokens`.
6. **Register source:** Create/update `app.merchant_sources` row (status: `active`).
7. **Store granted scopes:** Write to `app.merchant_sources.metadata_json`.
8. Commit all writes atomically.

#### Step 3: User Provisioning

1. Upsert `app.users` row by `(merchant_id, email)`.
2. First user for a merchant gets `owner` role; subsequent users get `viewer`.
3. Concurrent login race: if INSERT fails with unique violation, fetch existing row.
4. Best-effort auto-match: link user to employee record if emails match.
5. Fallback: if provisioning fails, assign system user UUID with `viewer` role — do not block login.

#### Step 4: Session Creation

1. Populate session: `user_id`, `roles`, `merchant_id`, `display_name`, `theme`.
2. Admin elevation: if merchant's `source_merchant_id` is in `CANARY_ADMIN_MERCHANTS` env var, append `admin` role.
3. Session lifetime: 7 days.
4. Redirect to `/welcome` (new merchant) or `/chirps` (returning merchant).

#### Step 5: Onboarding Pipeline (best-effort)

1. Register Square webhook subscriptions for all event types (payment, order, cash_drawer, inventory, gift_card, loyalty, timecard, dispute).
2. Optional: trigger initial data sync (employees, locations, catalog).
3. Optional: auto-trigger first health check if merchant has no existing Owl session.
4. Never blocks the OAuth callback — all errors are logged and swallowed.

#### Onboarding State Machine

```
not_connected → authorizing → connected → syncing → active

Error states:
  token_expired → refreshing → active
  token_revoked → disconnected
  sync_failed   → error (retry available)
```

---

### Token Lifecycle

**Automatic refresh:** On every request that requires a Square API call, check `expires_at < now()`. If expired, call `POST /oauth2/token` with `grant_type=refresh_token`. 30-second timeout on Square API call. Re-encrypt and store the new token pair.

**Manual refresh:** `POST /oauth/refresh` (admin role required). Calls refresh flow directly.

**Revocation:** `POST /oauth/disconnect` (owner or admin role required):
1. Call `POST https://connect.squareup[sandbox].com/oauth2/revoke` — best-effort; continue even if Square API fails.
2. Delete local `square_oauth_tokens` row.
3. Update `merchant_sources` status to `disconnected`.

---

### Session Contract

Sessions are stored server-side in Valkey (DB 0). The session cookie contains only an opaque session ID — no data.

**Session keys:**

| Key | Type | Description |
|-----|------|-------------|
| `user_id` | UUID string | Authenticated user's Canary UUID |
| `roles` | []string | User's roles for this merchant |
| `merchant_id` | UUID string | Active merchant UUID |
| `merchant_ids` | []string | All merchants this user can access |
| `organization_id` | UUID string | Parent organization UUID |
| `display_name` | string | Display name for UI |
| `theme` | string | UI theme preference |
| `join_email` | string | Temporary, pre-OAuth only |

**Session configuration:**

| Parameter | Value |
|-----------|-------|
| Key prefix | `canary:session:` |
| Cookie flags | HttpOnly, SameSite=Lax, Secure (prod) |
| Lifetime | 7 days |
| Degradation | If Valkey unreachable: degrade to null session (log warning) |

**Session validation on every request:** Check that `user_id` exists and `is_active=true` in `app.users`. If the user record is missing or inactive, clear the session. On DB error: fail closed — do not allow access on infrastructure failure (P1 finding from prototype that must be corrected in Go implementation).

---

### Authentication Middleware Contract

Every protected handler receives an authenticated merchant context injected by middleware. The context carries:

| Field | Type | Description |
|-------|------|-------------|
| `UserID` | UUID | Authenticated user |
| `Roles` | []string | User's roles |
| `MerchantID` | UUID | Active tenant scope |
| `MerchantIDs` | []UUID | All accessible tenants |
| `OrganizationID` | UUID | Parent org |
| `DisplayName` | string | |

**Auth modes:**

1. **Session mode** (browser): `load_session_user()` reads Valkey session, validates user in DB, injects context.

2. **API key mode** (agent-to-agent): `X-API-Key` header matches `CANARY_MCP_API_KEY` env var. Grants admin access with a synthetic user ID. **P0: must be replaced with per-agent scoped keys before production.**

3. **JWT Bearer mode:** Bearer token in `Authorization` header.
   - **Development:** Token value exactly matches `CANARY_DEV_JWT_SECRET` env var. Grants admin access. Not a real JWT — no claims or expiry. Must not reach production.
   - **Production:** JWT RS256 validation against an IdP JWKS endpoint. **P0: Not yet implemented in prototype. Go implementation must implement this before production.**

**Role enforcement:**

| Decorator | Behavior |
|-----------|----------|
| `RequireAuth` | Any authenticated user |
| `RequireRole(role)` | Exact role match; 403 on mismatch |
| `RequireAnyRole(roles...)` | Any of the listed roles; 403 if none match |

---

### RBAC Role Hierarchy

| Role | Capabilities |
|------|-------------|
| `admin` | Full access: all CRUD, config, user management, flush sessions |
| `owner` | Alert disposition, rule config, case management, settings, billing |
| `manager` | Alert disposition (acknowledge, dismiss), case management |
| `operator` | Case management, open cases from alerts |
| `member` | Read-only with limited actions |
| `viewer` | Read-only access (dashboard only) |

Roles are seeded at startup into `app.roles`. Not tenant-scoped — the role catalog is global. Assignments are tenant-scoped via `app.user_roles`.

---

### User Federation Modes — Home-Grown Identity Service

Canary identity is home-grown. The platform does not depend on Identity Platform, Auth0, or any external IdP-as-a-service. Federation happens at the perimeter via standard protocol libraries; claim mapping and platform JWT issuance are platform-owned.

| Mode | Customer setup | Identity service does | Library (buy) |
|---|---|---|---|
| **Canary-native** | None — magic link or password | Issues platform JWT directly | stdlib `crypto/rand` + `crypto/bcrypt` |
| **OIDC federation** | Customer registers their IdP (Okta, Azure AD, Google Workspace, Auth0, custom OIDC) per merchant | Validates federated ID token via discovery + JWKS, maps claims to platform roles, issues platform JWT | `coreos/go-oidc/v3` + `golang.org/x/oauth2` |
| **SAML federation** | Customer registers their SAML IdP per merchant | Accepts SAML assertion, validates, maps attributes to platform roles, issues platform JWT | `crewjam/saml` |
| **LDAP / AD direct bind** | Customer's LDAP directory | LDAP search and bind. Recommend the customer front LDAP with Authentik or Keycloak; direct bind is supported but discouraged | `go-ldap/ldap/v3` |
| **SCIM provisioning** | Customer's IdP pushes user lifecycle events | User and group records created / updated automatically; role mapping applies | `elimity-com/scim` |

**Why home-grown, not Identity Platform:**

1. Actor accountability binding — every platform JWT carries `actor_type` (`human` / `agent` / `system`). External IdP-as-a-service products have no native equivalent.
2. L402 macaroon issuance — agent MCP calls authorize via L402 (HTTP 402 + macaroon + Lightning preimage). The identity service holds both ends of token issuance and Lightning settlement.
3. Sovereignty — when the merchant leaves, their identity model goes with them. Nothing stays at GCP we couldn't migrate.

**Per-merchant claim-to-role mapping** is the platform's domain logic. The mapping is configuration the merchant owns:

```yaml
# Example: Acme Corp federated via Okta
idp_group_to_canary_role:
  "Retail-Managers":  "store_manager"
  "Loss-Prevention":  "lp_officer"
  "Buyers":           "buyer"
  "Finance":          "admin"
```

This config lives per `merchant_id` in `app.federation_configs` (see Data Model — Federation Tables below). The mapping applies after federated-token validation, before platform JWT issuance.

---

### Platform JWT Claims Structure

The internal platform JWT (HS256, signed with `JWT_SECRET`) is the post-federation token everyone consumes. Per `go-security`:

```go
type Claims struct {
    MerchantID uuid.UUID `json:"merchant_id"`
    ActorID    string    `json:"actor_id"`
    ActorType  string    `json:"actor_type"` // "human" | "agent" | "system"
    Roles      []string  `json:"roles"`
    SessionID  uuid.UUID `json:"session_id"` // store-brain session; uuid.Nil if not present
    jwt.RegisteredClaims
}
```

`actor_type` is non-negotiable. Every token carries it. Logs, traces, and metrics distinguish human-driven from agent-driven traffic on the basis of this claim alone — making the agent accountability model auditable across the entire platform surface.

---

### Agent Authorization — Default JWT, Optional L402

Agents authenticate with the same platform JWT that humans use, with `actor_type: "agent"` and per-agent scoped roles. This is the **default mode** and the only mode required for the platform to operate.

L402 (HTTP 402 + macaroon + Lightning preimage) is an **opt-in architectural direction**, not a current requirement. It is one of several Bitcoin / Lightning / smart-contract features documented in `platform-overview.md` "Optional Features" — all gated by environment flags, all default `false`. The platform must run correctly with every one of them disabled — no module's core function depends on any of them. The schema for L402 wallets, ILDWAC cost packets, and blockchain anchor receipts exists; the runtime enforcement is opt-in.

**Configuration:**

| Variable | Default | Description |
|---|---|---|
| `L402_ENABLED` | `false` | Master switch. When `false`, all paid-tool gates are bypassed; agent MCP calls authenticate via platform JWT only |
| `L402_LIGHTNING_NODE_URL` | — | LND or LSP endpoint. Required when `L402_ENABLED=true`; ignored otherwise |
| `L402_MACAROON_KEY` | — | HMAC key for macaroon issuance. Required when `L402_ENABLED=true` |
| `L402_DEFAULT_PRICE_SATS` | `0` | Default per-call price; tool-specific overrides supersede |

**When `L402_ENABLED=false` (default):**

- Agent calls go through `AuthMiddleware` and present a platform JWT
- Tool handlers proceed without macaroon checks
- No Lightning dependency
- ILDWAC cost packets that reference an `mcp_call_id` carry a `payment_method: "off"` marker for clarity

**When `L402_ENABLED=true` (opt-in):**

1. Agent calls a paid MCP tool (e.g., `cmd/receiving.create_purchase_order`)
2. Service responds with HTTP 402 + `WWW-Authenticate` header containing macaroon and Lightning invoice
3. Agent settles the invoice via Lightning
4. Agent retries with `Authorization: L402 <macaroon>:<preimage>`
5. Identity service verifies preimage matches invoice payment hash and validates macaroon caveats (expiry, amount, action scope)

Macaroon caveats bind the authorization tightly: time-bounded ("expires in 1 hour"), amount-bounded ("good for 1000 sats of API"), action-bounded ("only authorizes inventory reads"). Third-party attenuation works without the issuer being involved.

**Why optional:**

- Lightning operations introduce real-time external dependency. Customers in regulatory environments where Lightning is uncomfortable, in early-stage pilots, or running offline must be able to operate without it.
- Cost attribution can run in fiat-only mode (per existing MAC) when L402 is off. ILDWAC's satoshi denomination becomes parallel substrate, not the active accounting layer.
- The whole closed-loop economy (SHA-256 → L402 → receipt → RaaS) degrades gracefully to (SHA-256 → receipt → RaaS) when L402 is off — the chain integrity is preserved; only the financial-rail enforcement layer is bypassed.

L402 is enabled in default-on environments only after the merchant explicitly opts in. See `l402-otb.md` for the OTB enforcement layer that consumes these macaroons when the switch is on.

---

### Membership Boundary — Identity Service vs Application Layer

The identity service is the **authority on identity claims**. The application layer is the **interface that consumes them via API**.

**Identity service owns:**

- User record (`app.users` — id, email, password hash, federation source)
- Role assignment (`app.user_roles`, hierarchy-scoped)
- Token issuance (platform JWT + L402 macaroons)
- Federation flow (OIDC / SAML / LDAP / SCIM via the library stack above)
- Audit trail of authentication events
- Multi-merchant association (one user can belong to many merchants — per ADR-001 organization → merchant model)
- Lifecycle states (invite, active, suspend, terminate)

**Application layer owns:**

- Profile UI (display name, avatar, bio)
- "Forgot password" UX (calls identity service `POST /identity/password-reset`)
- Account settings page
- Theme / locale / vocabulary preferences (per ADR-PLA-001)
- MFA enrollment UI (calls identity service `POST /identity/mfa/enroll`, `verify`)
- Onboarding wizard
- Cross-merchant context switcher (calls identity service `POST /identity/sessions/switch`)

**Pattern for membership operations triggered from UI:** identity service exposes the APIs; app layer initiates them. The app never stores user records, never holds password hashes, never inspects federated tokens directly.

**Domain user state — owned by the domain modules, not by identity:** user-keyed data such as Fox case favorites, dashboard widget layout, vendor watchlist subscriptions, saved KPI views are domain data scoped by `actor_id`. Each domain module (`cmd/fox`, `cmd/ops-dashboard`, `cmd/commercial`, `cmd/analytics`) owns its own user-keyed tables. Identity service is queried for the actor context; domain modules never duplicate that data.

---

### Merchant Org Hierarchy — Role Binding Model

The Canary Go platform supports a seven-layer operational hierarchy derived from the agent PMO architecture. This hierarchy maps to the role binding model: a user can hold a role scoped to a specific node in any of the three hierarchy trees (geography, category, legal entity). This extends the flat `app.user_roles` model to support multi-location chains where a regional manager holds authority over a subtree, not a single merchant.

**Seven operational layers:**

| Layer | Description | Hierarchy Type |
|-------|-------------|----------------|
| Sales Floor | Individual POS terminal / department floor | GEOGRAPHY (DEPARTMENT) |
| Backroom | Receiving, stockroom, prep | GEOGRAPHY (STORE) |
| Store | Full store unit | GEOGRAPHY (STORE) |
| Head Office | Regional or district management | GEOGRAPHY (REGION / DISTRICT) |
| Merchants | Multi-location merchant organization | LEGAL_ENTITY |
| Supply Chain | Distribution centers, vendor-side | GEOGRAPHY or CATEGORY |
| Org | Parent franchisor or holding company | LEGAL_ENTITY |

**Platform participants** (not merchant employees):
- VAR (e.g., Rapid POS / Bart's team) — access via VAR-scoped API key, not user_roles
- GrowDirect — platform admin access via `CANARY_ADMIN_MERCHANTS` elevation

**Role binding:** `user_roles(principal_id, hierarchy_type, hierarchy_node_id, role)` where `hierarchy_type ∈ {GEOGRAPHY | CATEGORY | LEGAL_ENTITY}`. A user with GEOGRAPHY role at a REGION node implicitly holds that role for all DISTRICT and STORE nodes below it. Inheritance is enforced at query time, not by duplicating rows.

### Extended Data Model — Hierarchy Tables

These tables extend the Identity domain to support the org hierarchy role binding. They live in the `app` schema alongside the existing identity tables.

#### app.geography_nodes

Hierarchical location tree for a merchant. Represents physical geography: region → district → store → department.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `node_type` | TEXT | NOT NULL, CHECK IN ('REGION','DISTRICT','STORE','DEPARTMENT') | Hierarchy level |
| `parent_id` | UUID | NULLABLE, FK → app.geography_nodes.id | Self-referential; NULL at root |
| `name` | TEXT | NOT NULL | Display name |
| `location_id` | UUID | NULLABLE, FK → app.locations.id | If this node maps to a physical location |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_geography_nodes_merchant_id ON (merchant_id)`
- `idx_geography_nodes_parent_id ON (parent_id)`
- `idx_geography_nodes_location_id ON (location_id)`

#### app.category_nodes

Hierarchical category tree for a merchant. Represents merchandising hierarchy: division → department → category → subcategory → SKU.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY, DEFAULT gen_random_uuid() | |
| `merchant_id` | UUID | NOT NULL, FK → app.merchants.id | Tenant scope |
| `node_type` | TEXT | NOT NULL, CHECK IN ('DIVISION','DEPARTMENT','CATEGORY','SUBCATEGORY','SKU') | Hierarchy level |
| `parent_id` | UUID | NULLABLE, FK → app.category_nodes.id | Self-referential; NULL at root |
| `name` | TEXT | NOT NULL | Display name |
| `product_id` | UUID | NULLABLE, FK → app.products.id | If this node maps to a catalog item |
| `created_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT now() | |

Indexes:
- `idx_category_nodes_merchant_id ON (merchant_id)`
- `idx_category_nodes_parent_id ON (parent_id)`

#### app.user_roles (extended)

The existing `app.user_roles` table is extended with hierarchy-scoped columns. The original `role_id` FK remains for backward compatibility. New rows use `hierarchy_type` + `hierarchy_node_id` for scoped role bindings.

New columns added to `app.user_roles`:

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `hierarchy_type` | TEXT | NULLABLE, CHECK IN ('GEOGRAPHY','CATEGORY','LEGAL_ENTITY') | Which hierarchy tree this role is scoped to |
| `hierarchy_node_id` | UUID | NULLABLE | FK to `geography_nodes.id` or `category_nodes.id` depending on `hierarchy_type`; NULL means merchant-wide scope |

**Backward compatibility:** Existing rows with `hierarchy_type IS NULL` and `hierarchy_node_id IS NULL` represent merchant-wide role assignments — behavior unchanged from the current model.

**LEGAL_ENTITY scope:** When `hierarchy_type = 'LEGAL_ENTITY'`, `hierarchy_node_id` is NULL and the role applies to the merchant as a legal entity (equivalent to merchant-wide). This type exists to distinguish organizational authority (e.g., owner of the legal entity) from geographic authority (e.g., manager of a region).

Indexes (additions):
- `idx_user_roles_hierarchy ON (merchant_id, hierarchy_type, hierarchy_node_id)` — for hierarchy-scoped role lookups

### Token Encryption Contract

OAuth tokens are encrypted at rest using AES-256-GCM.

**Key:** `CANARY_ENCRYPTION_KEY` environment variable — base64-encoded 32 bytes. Decoded at startup to a raw 256-bit key. Missing key in production must raise a startup error and prevent the service from accepting requests.

**Encrypt:**
1. Generate 12-byte random nonce.
2. AES-256-GCM encrypt plaintext with nonce.
3. Concatenate: `nonce (12 bytes) + ciphertext + tag (16 bytes)`.
4. Base64-encode the concatenation.
5. Store as `"GCM:<base64_value>"`.

**Decrypt:**
1. Strip `"GCM:"` prefix.
2. Base64-decode.
3. Split into: `nonce (first 12 bytes)`, `ciphertext+tag (remaining)`.
4. AES-256-GCM decrypt.

**Legacy format handling (migration from prototype):** The prototype stored some tokens with a `"UNENCRYPTED:"` prefix (test only) or as Fernet-encrypted values (no prefix). The Go implementation must handle transparent migration: detect format by prefix, decrypt with appropriate method, re-encrypt as GCM on next write.

**Every token decrypt must log to `app.audit_log`.** (P1 finding — currently missing in prototype.)

---

### PII Display Toggle

`merchant_settings.show_employee_names` gates employee name display across all API responses. When `false`, employee names must be masked before any data leaves the service — in JSON responses, in dashboard queries, and in alert detail views. Default: `false`.

Toggle exposed via `PUT /api/merchants/settings`.

---

## Operations

### Startup Sequence

1. PostgreSQL available and schema current
2. Valkey available (if unavailable, degrade to null sessions with logged warning)
3. Encryption key validated — fail startup if missing in production
4. Reference data verified: `app.roles` seeded, `app.source_systems` seeded
5. Identity routes registered on Chi router

### Health Check

`GET /identity/health` → `{"service": "canary-identity", "healthy": true, "tools": 6}`

### Failure Modes

| Failure | Impact | Behavior |
|---------|--------|----------|
| Valkey down | Sessions degrade to stateless | Log warning; allow temporary session degradation |
| PostgreSQL down | All auth fails | 500 on login; existing sessions must fail closed (check DB on each request — do not fail open) |
| Square OAuth API down | Cannot onboard new merchants | OAuth callback returns error redirect |
| Square token refresh fails | Square API calls stop working | Log error; raise to caller |
| Encryption key missing | Token storage blocked | Startup error; do not start service |
| CSRF state mismatch | OAuth callback rejected | Redirect to login with `oauth_csrf_mismatch` error |

### Monitoring

| Metric | Alert Threshold | Notes |
|--------|----------------|-------|
| OAuth callback errors | >5/hour | CSRF mismatches, token exchange failures |
| Token refresh failures | Any | Merchant loses Square API access |
| User provisioning failures | >3/hour | Login degraded to fallback user |
| Session validation failures | >10/min | May indicate DB connectivity issues |
| Valkey connection failures | Any | Sessions degrade to insecure mode |

### Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `CANARY_ENV` | Yes | `development` / `production` / `testing` |
| `SECRET_KEY` | Yes (prod) | Session signing key |
| `CANARY_ENCRYPTION_KEY` | Yes (prod) | Base64-encoded 32 bytes for AES-256-GCM |
| `SQUARE_APPLICATION_ID` | Yes | Square OAuth client ID |
| `SQUARE_APPLICATION_SECRET` | Yes | Square OAuth client secret |
| `SQUARE_ENVIRONMENT` | Yes | `sandbox` or `production` |
| `SQUARE_MERCHANT_ID` | No | Default merchant for sandbox/dev |
| `SQUARE_ACCESS_TOKEN` | No | Sandbox access token |
| `SQUARE_REDIRECT_URL` | No | OAuth callback URL |
| `CANARY_DOMAIN` | No | App domain (default: `http://localhost:5001`) |
| `CANARY_MCP_API_KEY` | No | API key for agent-to-agent auth (P0: replace with scoped keys) |
| `CANARY_DEV_JWT_SECRET` | No (dev only) | Bearer token value for dev-mode JWT — never use in production |
| `CANARY_ADMIN_MERCHANTS` | No | Comma-separated Square merchant IDs for admin elevation |
| `CANARY_DEFAULT_MERCHANTS` | No | Comma-separated default merchants for dev context |
| `VALKEY_URL` | No | Valkey connection string (default: `redis://growdirect_valkey:6379/0`) |

**All production secrets must be sourced from Secrets Manager, not environment files.** (P0 finding.)

### Production Infrastructure Target

Per `platform-stack-commitment` — GCP-native end to end.

| Component | Service | Notes |
|-----------|---------|-------|
| Application | Cloud Run | Single container per service; Identity routes included |
| PostgreSQL | Cloud SQL Postgres 17 | `canary` database, `app` schema |
| Valkey | Memorystore (Valkey/Redis-compatible) | DB 0 for sessions + rate limiter |
| Secrets | GCP Secret Manager | `CANARY_ENCRYPTION_KEY`, `JWT_SECRET`, `PHONE_HASH_KEY`, `EMAIL_HASH_KEY`, IdP client secrets, Square credentials |
| OAuth + federation callbacks | Cloud Load Balancing + Cloud DNS | HTTPS endpoints for Square OAuth redirect and IdP SSO callbacks |

---

## Open Security Findings

### P0 — Blocks Production

**P0-1: Production JWT authentication not implemented**

In the prototype, production mode rejects all Bearer tokens with 401. The Go implementation must implement the home-grown HS256 platform JWT (per `go-security`) plus federation-mode token validation (OIDC ID token via JWKS, SAML assertion, LDAP bind result, SCIM-provisioned user) before production. All `/api/*` endpoints are inaccessible via Bearer token in the prototype's production mode.

Fix: Implement the federation broker described in the User Federation Modes section above. Platform JWT issuance is HS256 with `JWT_SECRET` from Secret Manager. Federated tokens are validated against the merchant's configured IdP and translated into platform JWTs via the claim-to-role mapping. API key authentication for agents is L402-gated (per `l402-otb.md`), not a static shared secret.

**P0-2: User email stored plaintext**

`app.users.email` is stored as plaintext. Apply AES-256-GCM field-level encryption using the same key and format as OAuth token encryption.

**P0-3: Secrets in environment files, not Secrets Manager**

`CANARY_ENCRYPTION_KEY`, `SECRET_KEY`, `SQUARE_APPLICATION_SECRET`, `CANARY_MCP_API_KEY`, and `CANARY_DEV_JWT_SECRET` must be retrieved from Secrets Manager at startup for any non-local environment.

**P0-4: API key bypass grants unrestricted admin access**

A single static `CANARY_MCP_API_KEY` grants full admin access to all merchants. No per-agent scoping, no audit trail, no revocation capability.

Fix: Implement per-agent API keys with scoped roles, stored in the database with `created_at`/`last_used_at`/`revoked_at`. Log all API key authentications to `app.audit_log`.

### P1 — Before GA

**P1-1: No audit logging for authentication events**

Login, logout, token refresh, token revocation, role elevation, and session creation must be written to `app.audit_log`. The prototype writes only to application logs.

**P1-2: Employee and customer PII stored plaintext**

`app.employees.email`, `app.employees.phone` require field-level AES-256-GCM encryption. MCP tool handlers must decrypt on read.

**P1-3: Billing email and SMS phone stored plaintext**

`app.organizations.billing_email` and `app.merchant_settings.notif_phone` require field-level encryption.

**P1-4: No rate limiting on OAuth endpoints**

Apply stricter limits: `/oauth/authorize` at 10/min, `/oauth/callback` at 10/min, `/oauth/refresh` at 5/min.

**P1-5: Session validation must fail closed on DB error**

The prototype's `load_session_user` fails open — if the DB is unavailable, stale sessions remain valid. The Go implementation must fail closed: if DB validation fails, clear the session and require re-authentication.

**P1-6: IP addresses logged plaintext in `app.audit_log`**

Hash with HMAC-SHA256 (keyed) or truncate to /24 before storage. Store the hash for anomaly detection, not the raw IP.

**P1-7: No data retention policy**

Implement retention policies: `app.audit_log` entries >24 months archived, deactivated users purged >12 months after deactivation.

### P2 — Post-Launch

**P2-1: No encryption key rotation procedure**

Document and implement: generate new key, update Secrets Manager, run re-encryption pass against all `square_oauth_tokens` rows, remove old key.

**P2-2: Valkey sessions unencrypted and unauthenticated**

Enable Valkey AUTH and TLS in production. Use `rediss://` scheme in `VALKEY_URL`.

**P2-3: In-process merchant UUID cache has no TTL or size limit**

Any in-process cache for merchant UUID lookups must use an LRU cache with TTL (e.g., 5-minute expiry) or be removed in favor of a Valkey lookup.

**P2-4: Dev-mode JWT is not a real JWT**

Development mode accepts a static shared secret as the "token." Acceptable for local development only. Add a startup check that fails if `CANARY_DEV_JWT_SECRET` is configured on a non-localhost listener.

**P2-5: MCP tool error responses may leak internal details**

Log full errors server-side. Return generic error messages to external callers.

**P2-6: CSRF exemptions on several auth endpoints**

`/auth/clear-session` and `/auth/start-connect` should not be CSRF-exempt. Remove exemptions; keep them only on endpoints using their own token-based auth.

---

## Production Readiness Checklist

- [x] OAuth tokens encrypted at rest (AES-256-GCM)
- [x] CSRF protection on form endpoints
- [x] Session cookies: HttpOnly, SameSite=Lax, Secure (prod)
- [x] Server-side sessions in Valkey
- [x] Token migration path (legacy format → AES-256-GCM, transparent)
- [x] Health check endpoint at `/identity/health`
- [ ] User email encrypted at rest (P0-2)
- [ ] Employee/customer PII encrypted at rest (P1-2)
- [ ] Billing email and SMS phone encrypted at rest (P1-3)
- [ ] Secrets in Secrets Manager, not environment files (P0-3)
- [ ] Audit logging for authentication events (P1-1)
- [ ] Data retention policy (P1-7)
- [ ] Stricter rate limiting on OAuth endpoints (P1-4)
- [ ] Production JWT validation implemented (P0-1)
- [ ] Per-agent API key scoping (P0-4)
- [ ] Encryption key rotation procedure (P2-1)
- [ ] Valkey AUTH + TLS in production (P2-2)
- [ ] Session validation fails closed on DB error (P1-5)
- [ ] IP address hashing in audit log (P1-6)
