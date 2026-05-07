# GRO-848 AtlasView Identity Integration — Contract Assessment

**Date:** 2026-05-07
**Scope:** Pin the contract by which AtlasView (Ruptiv's methodology orchestration platform; the configuration engine for ALX) delegates authentication to canary.go's `internal/identity` service. AtlasView decided in D-134 to delegate rather than re-implement; this GRO closes the contract questions identity service owns.
**Status:** Assessment — awaiting identity-service team review.
**Linked filing:** [GRO-848](https://linear.app/growdirect/issue/GRO-848) — AtlasView identity integration: contract pin.

---

## Background

AtlasView is the open reference implementation of corporate AI governance — multi-tenant, configuration-engine-shaped, emits substrate (AgentCard manifests, MCP tool catalogs, AuditLog) to downstream agent runtimes (ALX, Canary, future digital actors). It runs on Cloud Run with a Postgres + Neo4j data layer. AtlasView's spec library is in the Ruptiv repo at `spec/`; the Go-port architectural scope is captured in five SDDs (001 meta + 002–005 per-surface).

SDD-005 v0.1 specified AtlasView re-implementing 12 auth endpoints internally (login, logout, refresh, sso, mfa, forgot/reset password, otp, impersonate, check_email, csrf_token, me). The user surfaced this as overreach: the firm should run one identity service. canary.go already operates `internal/identity` at port 8086. AtlasView D-134 corrects the posture — delegate to canary.go identity rather than re-implement.

This GRO asks the identity-service team to pin the contract AtlasView consumes.

## What AtlasView needs from identity service

Six contract surfaces. Each surface lists what AtlasView reads and what assumptions it makes; identity service confirms or amends.

### 1. JWT minting

| Surface | AtlasView needs |
|---|---|
| Token endpoints | Login, refresh, MFA-verify endpoints that mint access + refresh tokens. |
| Access-token lifetime | 30 minutes (per OWASP L2; matches source application). |
| Refresh-token lifetime | 12 hours (per OWASP L2; matches source application). |
| Refresh rotation | Family-id tracking on rotation; reuse detection invalidates the family. |
| Audience naming | Token audience includes `atlasview` (or whatever string identity service uses for AtlasView consumers). Audience-narrowing supported (a token issued for canary.go alone is rejected by AtlasView). |

### 2. JWT verification (AtlasView side)

| Surface | AtlasView needs |
|---|---|
| JWKS endpoint | Public URL AtlasView fetches with TTL cache (default 1 hour; configurable). |
| Signing algorithm | RS256, ES256, or EdDSA — published in JWKS. Never `none`. |
| Claim structure | Required claims: `iss` (identity service URL), `aud` (includes `atlasview`), `exp`, `iat`, `sub` (Person id). AtlasView-specific claims: `org_id`, `person_id`, `user_type` ∈ {read_only, regular, power, admin, system}. Optional: `system: true` for system Persons. |
| Key rotation | Two-key rolling window during rotation (old + new keys both in JWKS for ≥ 24 hours). AtlasView's TTL cache catches the new key before old is rejected upstream. |
| Schema evolution | Identity service announces claim-shape changes ahead of release (PR review by AtlasView team or contract test in shared CI). |

### 3. WhoAmI RPC

AtlasView's `/v1/me` endpoint reads the canonical Person record from identity service, then enriches with per-tenant `OrganizationPeople` from AtlasView's Postgres.

| Surface | AtlasView needs |
|---|---|
| Endpoint | `GET {identity}/v1/me` (or equivalent path; subject to identity-service convention). |
| Auth | Bearer token (the same access token AtlasView verified locally). |
| Response shape | `{id, email, name, first_name, last_name, phone, picture_*, system}` — matches the source application's `PersonBase` shape (per `api/app/sqlmodels/person.py` in AtlasView's source-app reference). |
| Latency budget | p99 < 50 ms (AtlasView's `/v1/me` budget is < 100 ms; identity service should consume < half). |
| Caching | AtlasView caches the response per JWT `jti` (or token hash) for 60 seconds. Identity service can advise on cache-control semantics. |

### 4. Per-org SSO configuration

AtlasView's Sparring Partner cards include a per-org SSO concept (the source application stores `sso_config` per Organization with Microsoft Entra OIDC primitives). The identity service owns this storage and the OIDC dance.

| Surface | AtlasView needs |
|---|---|
| Per-org SSO config admin endpoints | Admin-only endpoints to register, update, delete Microsoft Entra config per Organization. AtlasView SPA (or downstream admin UI) calls these directly. |
| Org_id binding | SSO config is keyed on `org_id`. AtlasView's `Organization` and identity's `org_id` are the same identifier (UUID v4 from the source application's existing schema). |
| OIDC callback | Identity service runs the OIDC dance (state nonce, MS authorize redirect, code exchange, id_token verification). On success, mints AtlasView-audience JWTs and JIT-provisions the Person if not yet known. |

### 5. JIT provisioning

| Surface | AtlasView needs |
|---|---|
| First-time SSO flow | Identity service creates the Person record on first successful SSO auth; attaches to the Organization the SSO config belongs to; sets default `user_type`. |
| Audit log | JIT provisioning emits an audit event AtlasView can subscribe to (via Pub/Sub topic or webhook). The Person record is visible in AtlasView's WhoAmI immediately on first authenticated request. |
| Cross-product sync | A Person provisioned via Canary's flows is visible to AtlasView through the same Person id. The cross-product user record is the substrate of "one identity service across the firm." |

### 6. Operational surfaces

| Surface | AtlasView needs |
|---|---|
| Availability SLA | Identity service availability target (e.g., 99.9%). AtlasView's degraded-mode contract (cache-Person-record-on-503) depends on knowing what identity SLA to engineer against. |
| Latency at p99 | Endpoint latency for verification (JWKS fetch on cache miss; AtlasView budgets 5 ms warm) and WhoAmI (AtlasView budgets 50 ms). |
| Logout / token revocation | When identity service revokes a refresh token, the corresponding access token continues to be valid until `exp`. AtlasView accepts this; if shorter revocation is needed, identity service exposes a token-introspection endpoint or a revocation-events topic AtlasView subscribes to. |
| Test fixtures | Shared test-vector repository (or identity-service repo with CI subscription) for contract tests. AtlasView's verification middleware tests against committed fixture tokens. |

## Implementation effort

Estimated AtlasView-side work after the contract closes:

| Component | Effort |
|---|---|
| `internal/auth/middleware.go` | 1–2 days. JWT verification with JWKS caching; claim lifting; deny-on-error. |
| `internal/auth/identity_client.go` | 1–2 days. Thin HTTP client; WhoAmI; cached-with-TTL response; degraded-mode fallback. |
| `internal/auth/me.go` | 1 day. The `/v1/me` handler that calls WhoAmI and enriches with `OrganizationPeople`. |
| Contract tests | 1–2 days. Fixture tokens; happy-path; expired; bad audience; bad issuer; bad signature; rotation. |
| Documentation | 0.5 days. AtlasView-side ops runbook; degraded-mode behavior; logging fields. |

Total: 4–7 days for one engineer once the contract closes.

## Decision options

The asks of the identity-service team:

- **Confirm or amend the contract above.** The six surfaces named are AtlasView's read of what identity service owes. Identity-service team confirms each section, amends where AtlasView's read is wrong, or surfaces gaps where the surface doesn't yet exist.
- **Sequence the work.** Some surfaces may already exist; some may need building. Identity service team sequences against existing roadmap.
- **Nominate a contract test home.** Shared repo, subscription, or fixture-publishing convention.
- **Set the contract version.** Versioning convention for the contract document; how AtlasView pins against a contract version that may evolve.

## Cross-references

- Ruptiv repo: `spec/SDD-005-integration-layer.md` (v0.2 — auth section reworked to reflect this delegation).
- Ruptiv repo: `spec/SDD-001-source-application-go-port.md` §1.2 (auth surface marked partial).
- Ruptiv repo: `product/atlasview/decisions.md` — D-134 (this delegation decision).
- Ruptiv repo: `spec/standards/go.md` — Go 1.25+, Chi v5, pgx/v5, golang-jwt/jwt/v5.
- Canary.go: `CLAUDE.md` — `internal/identity` port 8086.
- Canary.go: `docs/conventions.md` — HTTP handler conventions, error envelope, status mapping (AtlasView aligned).

## Recommended next step

Identity-service team review of the six surfaces. Schedule a contract-pinning session within two sprints. The blocking dependency for AtlasView is contract surfaces 1–3 (JWT mint, verify, WhoAmI); surfaces 4–6 (SSO config admin, JIT provisioning, operational) are non-blocking for AtlasView's middleware implementation but blocking for end-to-end customer use.
