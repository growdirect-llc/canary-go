---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
type: shared-library
package: internal/security
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# go-security — Authentication, Encryption, and Access Control

**Package:** `internal/security`  
**Imported by:** `internal/runtime` (AuthMiddleware), every service that handles encrypted fields, raas (HMAC verification), webhook-pipeline (HMAC verification)  
**Depends on:** `crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/sha256`, `crypto/rand` (all stdlib), `github.com/golang-jwt/jwt/v5`, `github.com/google/uuid`

Security controls implemented once are enforced everywhere. Security controls duplicated per service drift apart — one service uses AES-128 where the spec requires AES-256, one uses `==` for HMAC comparison and introduces a timing attack. `internal/security` closes that gap: every cryptographic operation in the platform runs through this package, and the package enforces the constraints structurally rather than by convention.

---

## Business

### The Problem This Solves

The platform handles merchant financial data, POS credentials, and retail event records that regulators treat as business records. Three specific obligations drive this package:

1. **Field-level encryption at rest.** POS credentials, cardholder-adjacent event payloads, and external merchant IDs are Restricted fields — they cannot be stored in plaintext in the database. AES-256-GCM is the mandated cipher.
2. **Webhook authenticity.** Every inbound event from Square, NCR, or Shopify is HMAC-verified before processing. An event that fails HMAC validation is rejected at the perimeter — it never enters the chain.
3. **JWT-based session authority.** Every internal API call carries a signed JWT. The JWT declares the actor type (human / agent / system), their roles, and their merchant scope. Role checks happen in handlers using the helpers in this package — not in ad hoc `if` statements.

### What Breaks Without It

- AES key length inconsistency: one service uses 16-byte keys (AES-128) when the database was encrypted with 32-byte keys (AES-256) — field decryption fails silently and returns garbage
- String-based HMAC comparison (`signature == expected`) is vulnerable to timing attacks — an attacker can oracle the correct signature byte-by-byte
- JWT role checks written per-handler drift: some handlers check `claims.Roles` correctly, others check `claims.ActorType` only, others have no check — access control gaps accumulate
- Encryption key in a config file committed to git (has happened in prototype) — key rotation required across all affected merchants

---

## Technical

### AES-256-GCM Field Encryption

Used for all fields classified as "Restricted" or "PCI" in the data model. The nonce is prepended to the ciphertext in the stored value — one column stores both, and the Decrypt function splits them.

```go
// internal/security/encrypt.go

// Encrypt encrypts plaintext with AES-256-GCM.
// Returns: nonce (12 bytes) || ciphertext || GCM auth tag.
// Key must be exactly 32 bytes (256-bit). Returns error if key is wrong length.
func Encrypt(plaintext []byte, key []byte) ([]byte, error)

// Decrypt decrypts a value produced by Encrypt.
// Input: nonce (12 bytes) || ciphertext || GCM auth tag.
// Returns error if the auth tag fails — this means tampering or wrong key.
func Decrypt(ciphertext []byte, key []byte) ([]byte, error)

// MustEncryptString is a convenience wrapper for string fields.
// Panics if key is wrong length — call site is always at application startup
// with a key read from env, so wrong-length key is a misconfiguration, not a runtime condition.
func MustEncryptString(plaintext string, key []byte) []byte

// DecryptString decrypts to string. Returns error on auth tag failure.
func DecryptString(ciphertext []byte, key []byte) (string, error)
```

**Key management rules:**

| Rule | Rationale |
|------|-----------|
| Keys are read from `CANARY_ENCRYPTION_KEY` env var only | Keys in DB, config files, or code are a one-commit exposure incident |
| Key must be exactly 32 bytes (256-bit) | AES-128 (16 bytes) or AES-192 (24 bytes) are prohibited — shorter keys do not meet the platform's encryption standard |
| Key rotation: re-encrypt affected rows with new key, then retire old key | There is no in-place key rotation — the old key must remain available until all rows are re-encrypted |
| Per-subject key for PCI fields | See `raas.md` cryptographic erasure pattern — GDPR deletion re-encrypts the event subject key with a tombstone value, making historical events unreadable without deleting them |

**Trade-off: AES-256-GCM vs AES-256-CBC + HMAC**

GCM provides authenticated encryption — it simultaneously encrypts and authenticates the ciphertext. CBC+HMAC achieves the same but requires implementing the MAC verification correctly (including constant-time comparison) in two places. GCM is the correct choice: one primitive, fewer implementation surfaces, native authentication. The Go standard library's GCM implementation is well-audited.

GCM's constraint is nonce uniqueness — reusing a nonce with the same key breaks both confidentiality and integrity. This package uses `crypto/rand.Read` for nonce generation, which is safe for this purpose: the probability of a 12-byte random nonce collision across 2³² encryptions is negligible (~10⁻¹⁴), well below any operational risk threshold.

### JWT Validation

```go
// internal/security/jwt.go

type Claims struct {
    MerchantID uuid.UUID `json:"merchant_id"`
    ActorID    string    `json:"actor_id"`
    ActorType  string    `json:"actor_type"` // "human" | "agent" | "system"
    Roles      []string  `json:"roles"`
    SessionID  uuid.UUID `json:"session_id"` // store-brain session; uuid.Nil if not present
    jwt.RegisteredClaims
}

// ValidateJWT parses and validates a JWT string.
// Returns ErrUnauthorized if the token is missing, expired, or has an invalid signature.
// Returns ErrForbidden if the token is valid but the merchant scope is mismatched.
func ValidateJWT(tokenString string, secret []byte) (*Claims, error)

// HasRole returns true if claims contains the exact role string.
func HasRole(claims *Claims, role string) bool

// HasAnyRole returns true if claims contains at least one of the specified roles.
func HasAnyRole(claims *Claims, roles ...string) bool
```

Token signing algorithm: HMAC-SHA256 (HS256). The signing secret is read from `JWT_SECRET` env var — minimum 32 bytes enforced at startup by `runtime.InitSecurity()`.

**Token TTL:**
- Human sessions: 1 hour
- Agent sessions (`ActorType == "agent"`): 24 hours
- System machine-to-machine (`ActorType == "system"`): 24 hours

**Refresh policy:** `AuthMiddleware` issues a new token on any request where the remaining TTL is <15 minutes. The new token is returned in the `X-Refresh-Token` response header. Clients that ignore this header will be logged out when their token expires — this is the correct behaviour.

**Trade-off: HS256 vs RS256**

RS256 (asymmetric) allows token verification without the signing key — useful for microservices that verify but don't issue tokens. HS256 requires the same secret at every verification point. For the current platform topology, every service that verifies JWTs is also a trusted internal service that legitimately shares the secret. RS256 would add key distribution complexity with no security benefit at this scale. Revisit if a third-party or untrusted service needs to verify platform JWTs.

### HMAC Webhook Signature Verification

Used by `webhook-pipeline` for all inbound POS events, and by any endpoint that accepts external webhooks.

```go
// internal/security/hmac.go

// VerifyWebhookHMAC verifies that an inbound webhook payload matches the
// expected HMAC-SHA256 signature.
//
// signature: the hex-encoded signature from the webhook header (e.g., X-Square-Signature)
// secret: the webhook signing secret for this merchant/provider pair
//
// Returns true only if the signature is valid. Uses constant-time comparison.
func VerifyWebhookHMAC(payload []byte, signature string, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    expected := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(signature))
}
```

**The `hmac.Equal` requirement is non-negotiable.** Using `==` or `strings.Compare` for signature comparison is a timing attack: the comparison short-circuits on the first differing byte, and an attacker can measure response times to determine where the signature differs from the expected value — eventually recovering the correct signature without the secret. `hmac.Equal` compares in constant time regardless of where the first difference occurs.

### PII Hashing Keys

Some PII fields are stored as keyed hashes rather than encrypted ciphertext — when the field needs to be queryable for deduplication or cross-reference (e.g., loyalty phone lookup) but the plaintext must never round-trip back. Keyed hashing — HMAC-SHA256 with a server-side secret — is the correct primitive for this case. **Plain SHA-256 is prohibited for any PII whose plaintext domain is small enough to enumerate.**

```go
// internal/security/pii_hash.go

// HashPII produces a keyed one-way hash of a normalized PII value.
// The same input always produces the same output for a given key,
// preserving deduplication and cross-reference semantics.
//
// Input MUST be normalized before this call — normalization rules
// are field-specific (E.164 for phone, lowercase + trim for email).
//
// Use HashPII when the plaintext domain is small enough to enumerate
// (phone numbers, emails, ZIP codes) and the field needs equality lookup.
// Use MustEncryptString when the plaintext must round-trip.
func HashPII(normalized string, key []byte) []byte {
    mac := hmac.New(sha256.New, key)
    mac.Write([]byte(normalized))
    return mac.Sum(nil)
}
```

**Key classes:**

| Key | Domain | Used by |
|-----|--------|---------|
| `PHONE_HASH_KEY` | Phone numbers (loyalty, customer contact) | `tsp-parse.md` loyalty parser → `loyalty_accounts.phone_hash` |
| `EMAIL_HASH_KEY` | Email addresses (when stored as a lookup hash rather than encrypted plaintext) | `ecom-channel.md` → `customer_email_hash` written into RaaS chain events for cross-channel correlation. Chain payload carries `{key_version, hash}` so rotation does not break historical verification — old key versions are retired from new writes but never deleted. |

**Why per-domain keys, not one master key:**

A compromised key invalidates every hash produced under it. Per-domain keys contain blast radius — a leaked `PHONE_HASH_KEY` exposes phone numbers but not emails. Per-domain keys also rotate independently: phone-number hashes can be re-hashed during a phone-key rotation without touching email hashes.

**Key management rules (apply to every PII hash key):**

| Rule | Rationale |
|------|-----------|
| Each PII hash key is loaded from Secrets Manager, exposed as its own env var (`PHONE_HASH_KEY`, `EMAIL_HASH_KEY`, etc.) | Same isolation rationale as `CANARY_ENCRYPTION_KEY` — never in DB, config files, or code |
| Each key must be exactly 32 bytes (256-bit) — same constraint as the encryption key | Match HMAC-SHA256's optimal key length; same generation procedure (`openssl rand -hex 32`) |
| PII hash keys MUST NOT be reused for any other purpose | A key reused for both HMAC-PII and JWT-signing creates a cross-protocol attack surface |
| Rotation: store new-key-hash alongside old-key-hash for the rotation window, then drop the old column | There is no in-place rotation — the column doubles during the window so live writes go to both, and queries can match either |

**Why these are not the same as `CANARY_ENCRYPTION_KEY`:**

`CANARY_ENCRYPTION_KEY` is for AES-256-GCM (reversible — plaintext can be recovered with the key). PII hash keys are for HMAC-SHA256 (irreversible — the original plaintext cannot be recovered even with the key, only equality-tested against a candidate). Using the same key for both creates two failure modes from one compromise; separating them keeps the blast radius bounded to one primitive.

### Role Permission Matrix

Authoritative definition is in `store-brain.md` (`sessionToolPermissions`). This table is a summary for quick reference. Any discrepancy between this table and `store-brain.md` is a bug — fix both.

| Role | Allowed operations |
|------|--------------------|
| `owner` | All operations including billing, settings, user management |
| `admin` | All operations except billing and user management |
| `cashier` | Transaction records, BOPIS fulfillment updates, inventory reads |
| `lp_officer` | LP case management, chain verification reads, shrink writeoff |
| `inventory_manager` | Inventory adjustments, receiving records, device management |
| `api_service` | Machine-to-machine: all reads, event append, no billing/settings/user management |

Role checks in handlers use `security.HasRole` or `security.HasAnyRole`. They do not inspect `claims.Roles` directly — that bypasses the helper's nil-safe logic and creates an inconsistency if the `Claims` struct changes.

**Example handler role check:**

```go
claims := security.ClaimsFromContext(ctx) // injected by AuthMiddleware
if !security.HasAnyRole(claims, "owner", "admin", "lp_officer") {
    cerrors.WriteError(w, cerrors.New(cerrors.ErrForbidden, "insufficient role",
        map[string]any{"required": []string{"owner", "admin", "lp_officer"}, "actor_id": claims.ActorID}))
    return
}
```

---

## Ops

### Initialization

`runtime.InitSecurity()` is called at service startup before the HTTP server starts. It validates:

- `CANARY_ENCRYPTION_KEY` is present and exactly 32 bytes
- `JWT_SECRET` is present and at least 32 bytes
- `PHONE_HASH_KEY` is present and exactly 32 bytes (required by services that handle loyalty: `tsp` Stage 2, any service reading `loyalty_accounts.phone_hash`)
- `EMAIL_HASH_KEY` is present and exactly 32 bytes (required by `ecom-channel` and any service that reads or writes `customer_email_hash` in RaaS chain events)

Fatal if any required validation fails — a service running with an invalid or missing key is a security misconfiguration, not a degraded-mode scenario. Services that do not handle PII-hash fields skip the corresponding key's validation; the `runtime.InitSecurity()` caller passes the list of required keys.

### Key Rotation Procedure

1. Generate a new 32-byte key: `openssl rand -hex 32`
2. Deploy the new key as `CANARY_ENCRYPTION_KEY_NEW` alongside the existing `CANARY_ENCRYPTION_KEY`
3. Run the key rotation job: `cmd/key-rotation --from CANARY_ENCRYPTION_KEY --to CANARY_ENCRYPTION_KEY_NEW`
4. The rotation job reads each Restricted/PCI field, decrypts with the old key, re-encrypts with the new key, and writes back — in batches, with progress checkpointing
5. After rotation job completes, promote `CANARY_ENCRYPTION_KEY_NEW` to `CANARY_ENCRYPTION_KEY` and remove the old key
6. Deploy all services with the updated env var

Do not remove the old key before the rotation job completes. A service running with the new key will fail to decrypt rows encrypted with the old key.

### Failure Modes

| Failure | Behaviour |
|---------|-----------|
| `Decrypt` auth tag failure | Returns error — do not serve the field. Log at ERROR with merchant_id and field name. This indicates wrong key or tampered data. |
| JWT signature invalid | `ValidateJWT` returns `ErrUnauthorized`. AuthMiddleware returns 401. |
| JWT expired | Same as signature invalid — 401. Client should refresh or re-authenticate. |
| HMAC verification fails | `VerifyWebhookHMAC` returns false. Caller (webhook-pipeline) rejects the event and returns 401 to the POS system. Event is not enqueued. |
| `CANARY_ENCRYPTION_KEY` missing at startup | `InitSecurity` fatals. Service does not start. |

---

## Compliance

### Secrets Hygiene

No secret value may appear in:
- Source code (including test fixtures — use `TEST_ENCRYPTION_KEY` from environment in integration tests)
- Log output — `CANARY_ENCRYPTION_KEY`, `JWT_SECRET`, and HMAC secrets are never logged, not even at DEBUG level
- Error messages — `ErrInternal` for decryption failures; no key material in the message
- Git history — the `.gitignore` excludes `.env` files, but any accidental commit of a secret requires immediate key rotation

### PCI DSS Alignment

The `Encrypt`/`Decrypt` functions using AES-256-GCM with 256-bit keys satisfy PCI DSS Requirement 3.5 (cryptographic protection of stored cardholder data) at the algorithm level. Operational compliance (key storage, rotation frequency, access controls) is governed by the platform's broader compliance posture, not this package alone.

### GDPR Cryptographic Erasure

The per-subject key pattern (documented in `raas.md`) is implemented using the encryption primitives in this package. When a merchant exercises their right to erasure, the subject key for their namespace is re-encrypted with a random tombstone value. All events encrypted under the prior subject key become permanently unreadable without deletion. The tombstone value is generated via `crypto/rand.Read` and discarded — it is never stored.

### `external_merchant_id` as Restricted Field

`external_merchant_id` values in `raas_source_registrations` (the mapping between `raas:{merchant_id}` and POS-system-assigned merchant identifiers) are encrypted at rest using `Encrypt`. They must not appear in:
- Error messages
- Log fields
- API responses to unauthorized callers

The RaaS service is the only service that decrypts this field. Other services receive the canonical `raas:{merchant_id}` namespace and never see the external ID.

---

## Related

- [[go-runtime]] — `AuthMiddleware` and `RecoveryMiddleware` consume primitives from this package
- [[go-module-layout]] — `internal/security/` package location
- [[go-observability]] — log fields that must redact secret values
- [[go-errors]] — `ErrUnauthorized`, `ErrForbidden`, and `ErrInternal` error model
- [[data-classification-inventory]] — Restricted / Sensitive / Internal / Public tier definitions that drive encryption posture
- [[raas]] — per-subject DEK pattern for cryptographic erasure
- [[external-identities]] — JWT claims structure and namespace identity flow
- [[platform-overview]] — top-level security and compliance posture
