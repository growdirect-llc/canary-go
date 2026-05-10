---
title: "GRO-952 — PII / Payment-Data Redaction Policy at Protocol Ingestion"
date: 2026-05-10
status: substrate landed; pipeline wiring + crypto-erasure phased
authors: Geoff Lyle (with Claude Opus 4.7)
---

# Why

`protocol.evidence.raw_payload` is append-only. A schema trigger blocks
`UPDATE`, `DELETE`, and `TRUNCATE` against the row — that's the
evidentiary contract. But it also means anything written there is
effectively undeletable.

Inbound POS payloads carry shapes that **must not** become immutable:

- **Customer PII**: name, email, phone, address, DOB.
- **Payment data**: card_number / PAN, CVV, account_number,
  routing_number.
- **Employee PII**: SSN, DOB, employee name when adjacent to
  performance signals.
- **PHI** (in pharmacy/clinic contexts): drug names, prescriber, dose.

GDPR Article 17, CCPA right-to-delete, US state privacy regs, and
PCI DSS all require either:

1. The data not be persisted in raw form, or
2. It be removable on demand.

`raw_payload` violates both today.

# Decision

Insert a **redaction layer at the ingestion boundary** between webhook
HMAC verification and `protocol.evidence` write. Combine with
**crypto-erasure** for any per-source residual that the protocol still
needs the option to retrieve.

The proof chain is preserved by anchoring `event_hash` to the
**original** payload bytes (so source-side signatures still verify
end-to-end), while what we persist on `raw_payload` is the **redacted
projection** plus a hash commitment to the original. The original
itself is either:

- Discarded entirely (most cases), or
- Held in a per-tenant-key encrypted blob outside `protocol.evidence`,
  where deletion = key destruction.

# Multi-phase implementation

This ticket (GRO-952) lands the substrate. Wiring + storage changes
are phased into follow-up tickets so each PR remains independently
reviewable.

## Phase 1 — Substrate (this PR)

Lands `internal/protocol/redaction`:

- `Policy`: per-source-code allowlist of fields safe to keep verbatim.
  Default-deny; entries are dot-paths with `*` wildcards for array
  indices.
- `Redactor.Redact(payload []byte) ([]byte, error)`: walks JSON,
  drops or replaces non-allowlisted fields.
- `DefaultDenyPolicy`: empty allowlist — output is `{}`.
- `SquarePolicy`: starter list for Square webhooks (`event_id`,
  `merchant_id`, `location_id`, `type`, `created_at`, `data.id`,
  `data.type`, `data.object.id`).

**Acceptance** (probes in `redactor_test.go`):

- Common PII / payment / employee fields never appear in redacted
  output (substring-match on the bytes).
- Allowlisted fields survive verbatim.
- Wildcard paths match array elements.
- Invalid JSON returns `ErrInvalidJSON`.

**Not** wired into the ingestion path yet. Follow-up tickets land that.

## Phase 2 — Wire at webhook ingestion

Goal: `protocol.evidence.raw_payload` only ever contains redacted
bytes; `event_hash` continues to commit to the original.

Changes:

- `internal/protocol/webhook/handler.go`: after HMAC verification,
  apply the per-source `Redactor` BEFORE building the
  `publisher.Event`. The `Payload` field carries the redacted bytes
  forward.
- `event_hash` is computed on the ORIGINAL payload (no change), so
  source-side signatures still verify against the inscribed hash.
- A new `original_payload_digest` (sha256 of original) is stored
  alongside `event_hash` for an additional commitment that doesn't
  require holding the raw payload.

Acceptance:

- Square webhook with embedded customer PII produces an
  `evidence.raw_payload` row whose JSON contains only the
  SquarePolicy-allowlisted fields.
- `event_hash` matches what the source signed.
- A regression test inputs a payload with `customer.email`,
  `payment.card_number`, etc. and asserts none of those values
  appear in `raw_payload`.

## Phase 3 — Stop sub2 from copying full env.Payload into transaction attributes

Today `internal/protocol/sub2/store_inserts.go` copies the adapter's
`env.Payload` wholesale into `transaction_attributes`. That duplicates
the PII problem one tier deeper.

Changes:

- `sub2`: replace the wholesale copy with a per-attribute allowlist
  configured per source (similar shape to `redaction.Policy` but
  scoped to the transaction-attribute schema).
- Adapter contracts: each adapter (Counterpoint, Square, etc.) declares
  which canonical attributes it produces; sub2 writes only those.

Acceptance:

- A test that ingests a Counterpoint payload with `customer.email`,
  `cashier.ssn` reads back `transaction_attributes` and asserts
  neither value is present.

## Phase 4 — Crypto-erasure for retained-original payloads

For events where the original payload IS legitimately needed for
forensic replay (e.g. fraud detection downstream re-runs evidence),
introduce a per-tenant key that wraps the original. Stored in a
`protocol.payload_keystore` row keyed by `(tenant_id, event_id)`.
Right-to-delete = drop the key row; the encrypted bytes become
unrecoverable noise.

Changes:

- New table `protocol.encrypted_payload_blobs` (separate from
  `evidence` so the trigger doesn't apply).
- `payload_keystore` rotates per tenant on a configurable schedule.
- A "delete tenant data" admin RPC drops the keystore row(s) for the
  tenant — the immutable `evidence` rows survive but their original
  payloads are now ciphertext-without-key.

Acceptance:

- A test that ingests, rotates the key, deletes the key row, then
  verifies `event_hash` still verifies (it commits to the original)
  but the original payload cannot be reconstructed.

## Phase 5 — Retention + legal hold

- Retention policy expressed as `(tenant_id, source_code) → max_age`.
- A scheduled job decrypts and re-redacts (using current policy) for
  events older than max_age, then drops the encrypted-blob row.
- A "legal hold" flag on `protocol.events` blocks the cleanup job
  from touching held events.

# Out of scope for this dispatch

- Specific cryptographic primitives for crypto-erasure (Phase 4) —
  decided in a follow-up. Likely AES-256-GCM with per-tenant
  KEK in Secret Manager and per-event DEK derived from event_id.
- Retention defaults per source — needs legal review.
- Schema design for `encrypted_payload_blobs` — separate ticket.

# Risks called out

- **Allowlist drift**: every new POS adapter requires a thoughtful
  policy. A "ship now, classify later" deploy is exactly the failure
  mode this ticket fixes; CI should fail loudly if a new adapter
  registers without a paired `redaction.Policy`.
- **Hash chain integrity**: Phase 2 must verify `event_hash` is still
  computed against the ORIGINAL payload, never the redacted output.
  Testing this is the load-bearing assertion in the Phase 2 PR.
- **Source-side replay**: external verifiers handed a redacted
  payload won't reproduce `event_hash`. Phase 4's crypto-erasure
  envelope is what lets us hand a verifier the original under
  controlled access; without it, the protocol's external-replay
  story narrows to "the source signed it, you trust the inscription".

# References

- Substrate: `internal/protocol/redaction/redactor.go`
- Pipeline integration points (Phase 2): `internal/protocol/webhook/handler.go:111-124`,
  `internal/protocol/sub1/seal.go:146-160`
- Schema (Phase 4): `deploy/schema/11_protocol.sql:29-40` and `:68-80`
- Adapter wholesale copy (Phase 3): `internal/protocol/sub2/store_inserts.go:28-56`,
  `internal/adapters/counterpoint/parser.go:120-129` and `:131-147`
