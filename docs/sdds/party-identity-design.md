---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-05-03
binary: party
port: 8094
mcp-server: canary-party
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Party Identity, Fingerprinting, and Householding

**Type:** Substrate Module — Identity Resolution + Decisioning Substrate
**Binary:** `cmd/party` → `:8094`
**MCP server:** `canary-party` (5 tools, see junction additions in `mcp-service-junctions.md`)
**Depends on:** `identity` (tenant scope), `external-identities` (POS-native customer sync), pgvector (lexical-similarity fallback for identity resolution)
**Feeds:** every downstream service that decisions about a who — `chirp` (LP detection), `analytics` (RFM/LTV), marketing (campaign scoping), commercial (OTB-by-segment), Hawk (case subject resolution), `fox` (Subjects.Resolve obviation — see §Migration §Phase 4)

---

When Canary lived inside Square, party resolution was free: Square's payment processor already stitched anonymous tap-to-pay traffic to known cardholders via its global card-fingerprint registry, and Canary inherited the resolution by reading Square's `customer_id` off the transaction. Multi-POS ends that. Stripe-direct, Counterpoint, RapidPOS, and wallet tenders each carry their own (incompatible) fingerprint scheme; the EMV `card_token` shape differs from the Stripe `payment_method` shape differs from the Counterpoint `AR_CUST` linkage; Square Cash, Apple Pay, Google Pay, and PayPal have device-fingerprint primitives that don't map onto card primitives at all. The decisioning question — *is the card that just tapped at the register the same person who shopped online last week?* — has no consistent answer in the current canonical model. This SDD designs the substrate that makes it consistent: a `party` schema that abstracts known-and-anonymous identity into a single graph, a normalized fingerprint registry across POS sources, identity-resolution rules with a documented confidence ladder, a per-tenant household model, and a decisioning-facts view that downstream services consume instead of recomputing party-level RFM/LTV/segment/risk on every read.

**Multi-tenant context.** Every party row is tenant-scoped. There is no cross-tenant party graph and there will never be one — the privacy posture of the platform per [[concept-party-taxonomy|the party taxonomy card]] requires that consumer identity stays inside the tenant that earned it. Cross-tenant identity (a consumer who shops at two merchants on the platform) is achievable only via consumer-explicit consent contracts per [[concept-identity-layer-triad]] and is out of scope here.

**Optional Features posture.** Party operates with all platform Optional Features (per `platform-overview.md`) disabled. Identity resolution runs on internal records and the fingerprint registry. When `PARTY_HOUSEHOLDS_ENABLED=true`, the household auto-detection job runs nightly. When `PARTY_DECISIONING_FACTS_ENABLED=true`, the decisioning-facts materialized view refreshes on the configured cadence. PCI-tokenized fingerprint storage requires `PARTY_TOKENIZATION_VAULT_ENABLED=true` and a configured vault endpoint; absent that, raw card-network fingerprints are not accepted at the API boundary.

**Scope boundary.** This SDD designs the **commercial / consumer party model** only — the Customer and Consumer party types per [[concept-party-taxonomy|the six-party taxonomy]]. Vendor, Auditor, Investigator, and MCP-Agent party types are governed by other SDDs (`receiving.md` / vendor master, `2026-05-02-platform-trust-boundary-architecture` / auditor + investigator + agent). The `party.parties` table is shaped to accommodate all six taxonomy types in the long run, but the resolution / fingerprinting / householding mechanics in this SDD apply only to the commercial subtypes.

---

## Business

### The Party-Resolution Problem

There are two failure modes the current canonical hits the moment Canary leaves Square's umbrella.

**Failure mode 1: anonymous traffic is invisible.** `c.customers` is shaped for known parties — a row exists when the merchant captured an email, phone, loyalty number, or business name. About 70% of SMB retail traffic is anonymous walk-in (no loyalty enrollment, no email at checkout). The current model treats those transactions as having `customer_id IS NULL` and walks away. There is no record of "these three transactions across two stores in three weeks were the same anonymous shopper" — even when the same payment card was tapped each time. Every analytic that wants to talk about repeat anonymous traffic, fraud-card-tied-to-multiple-employees, or scan-avoidance patterns by recurring shopper hits a wall.

**Failure mode 2: known identity stitching is per-POS.** When a consumer *is* known, they may be known via Square (`square_customer_id` in `c.customers.external_ids`), via Counterpoint (`AR_CUST_ID`), and via a Stripe-direct online checkout (`stripe_customer_id`) — three rows in `c.customers`, no relationship between them, despite being the same human. The merchant's loyalty engagement, marketing audience, and LP risk score fragment across the three records.

The substrate-level diagnosis is that the canonical conflated **party** (a stable identity) with **customer record** (a per-POS-source representation of that identity). They are not the same thing. The party is upstream of the customer record. One party may have many customer records; one anonymous party may have zero customer records and many fingerprints.

### Why the Current Schema Cannot Solve This in Place

| Existing surface | What it does | Why it can't carry party resolution |
|---|---|---|
| `c.customers` | Known-customer master | Assumes a known-identity row; cannot represent anonymous-but-recurring; per-POS rows fragment one human across multiple `c.customers.id` values |
| `t.transaction_tenders.card_token` + `card_last_4` + `card_brand` | Per-tender payment metadata | No normalization across POS sources; no de-duplication across tenders; no quality score; no link surface back to a party |
| `q.subjects` (LP) | Investigation subject (suspected/known) | Designed for LP-only; cross-FK to `c.customers` / `e.employees` / `m.vendors` is one-way and soft (no FK declared per Loop 2 Wave 1); cannot express "the anonymous tap-to-pay party at register 2 last Thursday" until a case is opened |
| `c.customers.customer_type='household'` | Single-row household placeholder | Not a relationship — just a flag. No way to attach individual members; no way to compute household-level metrics from member transactions |

The shape we need is **party-first**: party is the substrate, identifiers attach to party, customers/subjects/households are projections off party. This SDD adds that shape without breaking the existing tables.

### Business Rules

1. Every `c.customers`, `q.subjects`, `t.transactions`, and `o.sales_orders` row resolves (eventually) to exactly one `party.parties` row. The resolution may be lazy (computed on first read) or eager (resolved at write time) per the migration phase.
2. A party is **tenant-scoped and immutable as identity** — a party's `id` never changes; a party's resolution to a specific consumer human may change via merge/de-merge events, which are themselves chain-anchored.
3. The substrate is **append-evidence** for resolution decisions: every party-resolution event (resolve, merge, de-merge, identifier-add) is logged in `party.resolution_events` with the source signal, the rule that fired, and the confidence band. Decisions are auditable and reversible.
4. A fingerprint never traverses across tenants. A `party.identifiers` row's hash space is namespaced by `tenant_id`; a card fingerprint that recurs across two tenants resolves to two distinct parties (one per tenant) with no link.
5. A household is a per-tenant relational primitive that groups parties. Membership is *additive* — a party may join a household but never moves between households without an explicit unmerge. Household evidence (shared shipping address, shared payment instrument) is captured as `party.household_evidence` rows, never inferred silently.
6. The decisioning-facts view (`party.decisioning_facts`) is the **only** sanctioned read surface for downstream services that need party-level value/risk/segment data. Direct joins from `chirp`/`analytics`/marketing onto the party tables are an architectural smell — they bypass the cadence and refresh discipline.
7. PCI-scope card data is never stored raw at the party layer. A card fingerprint is the SHA-256 of (tenant_id || card-network-issued-fingerprint || optional salt) — the network-issued fingerprint is itself a tokenized derivative, never the PAN. The raw network fingerprint, where it appears in the API request, must be tokenized at the boundary by a vault service when `PARTY_TOKENIZATION_VAULT_ENABLED=true`.
8. A party with `status='merged'` is read-through to `merged_into` — application code must follow the merge pointer to the surviving party for any decisioning read.
9. Deletes are forbidden on `party.parties`, `party.identifiers`, `party.resolution_events`, and `party.household_evidence`. Merge replaces delete; de-merge restores. Seven-year retention applies (financial-record class, since party identity attaches to transactions).
10. Subjects.Resolve (the `fox` Loop 3 obviation per `internal/fox/handler.go:subjectFromDetection`) is implemented as `party.resolveSubject(tenant_id, party_id) → q.subjects.id` — Q-module callers no longer carry the subject-creation responsibility; the party module mints the subject row on first reference and returns its id.

---

## Part A — Schema Design (the six entities)

### Entity inventory

| Entity | Purpose | Cardinality |
|---|---|---|
| `party.parties` | The party node — stable identity per tenant; one row per resolved entity | 1 per resolved entity |
| `party.identifiers` | Identifier registry — every signal that ties to a party (card fingerprint, email hash, phone hash, POS-native ID, loyalty number, device fingerprint) | many per party; one per (tenant, identifier_type, identifier_value_hash) |
| `party.resolution_events` | Append-only log of resolution decisions (resolve/merge/de-merge/identifier-add) — audit + replay surface | many per party (one per decision event) |
| `party.households` | Per-tenant household node — groups parties under a shared household identity | 1 per household (typically: per shipping address with multiple individual parties resolved against it) |
| `party.household_memberships` | Party-to-household membership with effective dates and evidence reference | many per household; many per party (rare — most parties belong to one household over time) |
| `party.household_evidence` | Append-only log of evidence supporting household membership (shared address, shared payment instrument, explicit declaration) | many per membership |

### Relationship diagram

```
                   ┌──────────────────────────────────────┐
                   │         party.parties                │
                   │  id · tenant_id · party_type ·       │
                   │  display_name · status · merged_into │
                   └─┬───────────┬──────────┬────────────┬┘
                     │           │          │            │
              (many) │     (many)│    (many)│      (many)│
                     ▼           ▼          ▼            ▼
        ┌────────────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────────┐
        │party.identifier│ │party.    │ │c.customers│ │q.subjects        │
        │ tenant_id     │ │resolution│ │ party_id  │ │ party_id         │
        │ identifier_   │ │_events   │ │ (soft FK) │ │ (soft FK)        │
        │   type        │ │ event_   │ └──────────┘ └──────────────────┘
        │ identifier_   │ │   type   │
        │   value_hash  │ │ rule_id  │
        │ quality_score │ │ confide- │
        │ source_system │ │   nce    │
        └────────────────┘ └──────────┘

                   ┌──────────────────────────────────────┐
                   │         party.households             │
                   │  id · tenant_id · household_code ·   │
                   │  display_name · status               │
                   └─┬───────────────────────┬────────────┘
                     │                       │
                (many)│                  (many)│
                     ▼                       ▼
        ┌──────────────────────┐   ┌────────────────────────┐
        │party.household_      │   │party.household_evidence│
        │   memberships        │   │ membership_id          │
        │ household_id         │   │ evidence_type          │
        │ party_id             │   │ evidence_payload       │
        │ effective_start/end  │   │ source_event_id        │
        └──────────────────────┘   └────────────────────────┘

   t.transactions.party_id      (soft FK, populated at completion)
   o.sales_orders.party_id      (soft FK, populated at order create)
```

The hard FKs (with arrows above) live entirely inside the `party` schema. The soft FKs into `c.customers` / `q.subjects` / `t.transactions` / `o.sales_orders` are documented in §B of the proposed canonical-data-model edits — they are intentionally *not* declared as DB-level FKs, to preserve the cross-schema independence pattern Loop 2 ratified for `q.detections.cashier_employee_id`.

### Column-by-column design

Full DDL appears in §Technical / Data Model below. The shape rationale:

`party.parties` carries `tenant_id`, `party_type` (consumer | customer | household-aggregate | reserved-for-vendor/auditor/investigator/agent), `display_name` (computed when known, "Anonymous-XXXX" suffix when not), `status` (active | merged | suppressed), `merged_into` (self-FK for soft-merge replacement), `first_seen_at`, `last_seen_at`, and a `confidence` band (`anonymous` | `weak` | `probable` | `strong`) reflecting the strongest tier of evidence currently bound. A tenant-scoped `party_code` is surface-visible (so merchants can reference the party in support cases) and follows the format `P-<tenant-prefix>-<base32-rand>`.

`party.identifiers` is the identifier registry. Columns: `id`, `tenant_id`, `party_id`, `identifier_type` (card_fingerprint | card_last4 | email_hash | phone_hash | pos_native_customer_id | loyalty_number | device_fingerprint | wallet_account_id | device_advertising_id), `identifier_value_hash` (SHA-256 of the canonical normalized value), `source_system` (square | stripe | counterpoint | rapidpos | wallet_apple | wallet_google | manual | self_computed), `quality_score` (numeric 0.0–1.0 — see §B fingerprint quality matrix), `first_seen_at`, `last_seen_at`, `attributes` JSONB for source-specific metadata (issuer BIN, device OS, etc.), and a unique constraint on `(tenant_id, identifier_type, identifier_value_hash)`.

`party.resolution_events` is the append-only audit log. Columns: `id`, `tenant_id`, `party_id`, `event_type` (resolve | merge | de_merge | identifier_add | identifier_dispute | confidence_upgrade | confidence_downgrade | fingerprint_recompute), `source_event_id` (the upstream event that triggered the resolution — typically a `t.transactions.id`), `rule_id` (which resolution rule fired — see §C), `confidence_before` / `confidence_after`, `evidence` JSONB carrying the matched signals, `actor` (system | manager_override | merchant_admin), `created_at`. Append-only; no UPDATE or DELETE.

`party.households` carries `tenant_id`, `household_code` (`H-<tenant-prefix>-<base32-rand>`), `display_name`, `status` (active | merged | dissolved), `formed_at`, `dissolved_at`, plus `attributes` JSONB. Households have no FK back to parties — the relationship lives entirely in `party.household_memberships`.

`party.household_memberships` is the many-to-many. Columns: `id`, `tenant_id`, `household_id`, `party_id`, `member_role` (head | member | dependent), `effective_start`, `effective_end` (NULL = current), `attributes` JSONB. UNIQUE constraint on `(tenant_id, household_id, party_id, effective_start)`. EXCLUDE constraint enforcing one current head per household.

`party.household_evidence` is append-only. Columns: `id`, `tenant_id`, `membership_id`, `evidence_type` (shared_shipping_address | shared_payment_instrument | shared_loyalty_number | explicit_declaration | shared_wifi_device | shared_phone_area_local), `evidence_payload` JSONB (the actual matched signal), `source_event_id` (the upstream event reference), `confidence` numeric 0–1, `collected_at`, `expires_at` (some evidence is time-bounded, e.g. shared_wifi_device decays after 90 days of non-recurrence).

### Key indexes

Beyond the obvious tenant + PK indexes, the load-bearing ones:

- `party.identifiers (tenant_id, identifier_type, identifier_value_hash)` UNIQUE — the resolution lookup index; every fingerprint resolve hits this exact path
- `party.identifiers (tenant_id, party_id)` — reverse lookup ("show me all identifiers for this party")
- `party.parties (tenant_id, status)` partial WHERE `status='active'` — most reads filter to active
- `party.parties (merged_into)` partial WHERE `merged_into IS NOT NULL` — merge pointer chase
- `party.resolution_events (party_id, created_at)` — audit timeline per party
- `party.household_memberships (party_id) WHERE effective_end IS NULL` — current-membership lookup
- `party.household_memberships (household_id) WHERE effective_end IS NULL` — current-members lookup

---

## Part B — Fingerprinting Strategy

### Source matrix

Each row is a kind of fingerprint primitive Canary may receive. The `quality_score` is the substrate's confidence that two appearances of the same value at this tier represent the same human, scored 0.0 (worthless) to 1.0 (cryptographic certainty).

| Source | Identifier shape | Native to | quality_score | Identity-binding strength | Notes |
|---|---|---|---|---|---|
| **Square payment fingerprint** | Square's `card.fingerprint` (Square hashes PAN+expiry) | Square | 0.95 | Strong — same physical card across transactions | Shape: 64-char hex; emitted on every `payment.created` webhook |
| **Stripe payment fingerprint** | Stripe's `payment_method.card.fingerprint` | Stripe | 0.95 | Strong — Stripe hashes PAN; stable across the same card | Same shape across customers; Stripe explicitly states fingerprint stability |
| **Counterpoint AR_CUST linkage** | `AR_CUST.CUST_ID` (numeric) | Counterpoint | 0.90 | Strong — but only when the cashier explicitly attaches the customer at register | Many transactions stay anonymous (no cashier action); known parties get attached |
| **EMVCo card token** | EMV `Application Identifier` + masked PAN | RapidPOS / NCR pinpads | 0.85 | Strong-but-noisy — same physical card, but EMV-token shape varies by terminal vendor | Requires terminal-vendor-specific normalization at the adapter (handled by `pos-adapter-substrate`) |
| **Wallet device fingerprint** | Apple Pay / Google Pay device account number (DPAN) | Wallet | 0.80 | Strong for *device*, weak for *human* — one human may have multiple devices, one device may be shared (rare) | Apple Pay DPAN ≠ underlying card PAN; multi-device users will register as distinct fingerprints unless cross-linked via loyalty |
| **Self-computed fingerprint** | SHA-256 of `(card_brand || card_last_4 || zip_code)` | Canary (fallback) | 0.40 | Weak — millions of collisions across the country, narrowed-but-not-disambiguated within a single SMB merchant tenant | Used only when no payment-network fingerprint is available; quality is good enough to support "same approximate card across tenders within this tenant" |
| **Email hash (lowercased)** | SHA-256 of `lower(trim(email))` | Application | 0.95 | Strong when present | Captured at loyalty enrollment, e-comm checkout, BOPIS |
| **Phone hash (E.164)** | SHA-256 of E.164-normalized phone | Application | 0.90 | Strong-but-shareable — household members may share landline; shared business phone | Normalization is the trick: stripping formatting variation to canonical E.164 before hashing |
| **POS-native customer_id** | source_system + source_customer_id | POS adapter | 0.95 | Strong within source — the source system already resolved | Cross-source linkage requires either a shared identifier (email/phone) or a manual merge |
| **Device fingerprint (web/mobile)** | UA + IP + advertising_id composite | Field-capture / e-comm | 0.50 | Moderate — useful for fraud signals, low for stable identity | Browser fingerprints decay (privacy features, IP rotation); useful as supporting evidence not primary |

### `party.compute_fingerprint()` function

```go
// In internal/party/fingerprint.go
type FingerprintInput struct {
    SourceSystem    string                 // "square" | "stripe" | "counterpoint" | "rapidpos" | "wallet_apple" | "wallet_google" | "self_computed"
    PaymentMetadata map[string]interface{} // raw per-source metadata
    TenantID        uuid.UUID
}

type FingerprintResult struct {
    FingerprintValue string  // SHA-256 hex of normalized payload (already tenant-salted)
    QualityScore    float64  // 0.0 - 1.0 per matrix above
    IdentifierType  string   // "card_fingerprint" | "device_fingerprint" | etc.
    SourceMetadata  map[string]interface{} // attributes for party.identifiers.attributes
}

// ComputeFingerprint normalizes per-source-system payment metadata into a
// canonical fingerprint shape. The returned FingerprintValue is already
// tenant-scoped — callers do not re-salt.
func ComputeFingerprint(ctx context.Context, in FingerprintInput) (FingerprintResult, error)
```

Per-source normalization rules:

- `square`: pull `payment.card_details.card.fingerprint`; normalize-by-lowercase; tenant-salt; SHA-256
- `stripe`: pull `payment_method.card.fingerprint`; normalize-by-lowercase; tenant-salt; SHA-256
- `counterpoint`: pull `AR_CUST.CUST_ID`; this is identifier_type=`pos_native_customer_id` not `card_fingerprint` — counterpoint surfaces customer-level not card-level identity
- `rapidpos`: pull EMVCo `tlvData.5A` (PAN) — TOKENIZE FIRST via vault if `PARTY_TOKENIZATION_VAULT_ENABLED=true`; if vault disabled, reject the request with `vault_required` error
- `wallet_apple` / `wallet_google`: pull DPAN; tenant-salt; SHA-256; identifier_type=`wallet_account_id`
- `self_computed`: build composite `(card_brand || ":" || card_last_4 || ":" || zip_5)`; tenant-salt; SHA-256; quality_score=0.4

The quality_score is returned *with* the fingerprint and persisted on the `party.identifiers` row. Resolution rules (§C) gate decisions on the quality_score.

---

## Part C — Identity Resolution Rules

Five rules, evaluated in order. The first rule that fires takes the decision; downstream rules are short-circuited. Every evaluation produces a `party.resolution_events` row regardless of outcome.

### Rule 1 — Strong (deterministic match)

A fingerprint with `quality_score >= 0.90` matches an existing `party.identifiers` row for the same `(tenant_id, identifier_type, identifier_value_hash)` → resolve to the existing party. No new party created. Confidence band → `strong`. Append `event_type='resolve'` with `confidence_after='strong'`.

```
IF identifier_match.quality_score >= 0.90
   AND identifier_match.tenant_id = input.tenant_id
   AND identifier_match.identifier_value_hash = input.fingerprint
THEN
   resolve to identifier_match.party_id
   IF party.confidence < 'strong': UPGRADE to 'strong'
END
```

### Rule 2 — Probable (cross-fingerprint suggested)

The input fingerprint has no exact match, but the tender carries a *secondary* identifier (email hash, phone hash, or loyalty number) that does match an existing party with `quality_score >= 0.85`. The card and the secondary identifier share a transaction context (same `t.transactions.id`). → resolve to the existing party AND attach the new fingerprint as an additional `party.identifiers` row. Confidence band → `probable` (it may already be `strong` from Rule 1's prior firing).

```
IF input.fingerprint not in identifiers
   AND input.transaction_context.email_hash matches identifier with quality >= 0.85
   AND input.transaction_context.email_hash.party_id = same as match
THEN
   resolve to identifier.party_id
   ATTACH input.fingerprint as new identifier with party_id
   confidence_after = max(party.confidence, 'probable')
END
```

### Rule 3 — Weak (newly-anonymous)

No matching identifiers; no secondary identifier in the transaction context. → CREATE a new party with `party_type='consumer'`, `display_name='Anonymous-' + base32(rand)`, `status='active'`, `confidence='weak'`. Attach the input fingerprint. Append `event_type='resolve'` with `evidence={"reason":"new_anonymous","quality_score":...}`.

```
IF no match found at any quality tier
THEN
   CREATE party (tenant_id, type='consumer', display='Anonymous-XXXX', confidence='weak')
   ATTACH input.fingerprint
END
```

### Rule 4 — Conflict (ambiguous match)

The input fingerprint matches AND a secondary identifier matches, but they resolve to different parties. → CREATE a `party.resolution_events` row with `event_type='identifier_dispute'` and `evidence` carrying both candidate party_ids. Resolve to the party with the higher max-quality identifier; the conflict is logged but not auto-merged (auto-merge would silently destroy LP-relevant distinctions). The dispute is queued for merchant review via the `mcp.party.merge-anonymous-to-known` junction. Confidence band → unchanged.

This rule encodes the platform's "no silent merges" posture. The ambiguous case is exactly when LP wants to know — two cards used by the same email address could be a fraud ring or a legitimate household; the merchant decides.

### Rule 5 — De-merge (manual reversal)

A merchant operator (or a Hawk case finding) determines that a prior merge was incorrect. → split the merged party into two via the `mcp.party.merge-anonymous-to-known` junction with `action='de_merge'` and a `target_identifiers[]` array specifying which identifiers belong to the new (split-off) party. Append two `party.resolution_events` rows: one `event_type='de_merge'` on the source party, one `event_type='resolve'` on the new party. Both rows reference the original merge event in `evidence.original_merge_event_id`.

### Decision matrix

| Input signal | Match found? | Secondary match? | Action | Rule | Confidence after |
|---|---|---|---|---|---|
| card_fingerprint q=0.95 | yes (same q) | n/a | resolve | 1 | strong |
| card_fingerprint q=0.95 | no | email q=0.95 same party | resolve + attach | 2 | strong |
| card_fingerprint q=0.40 | no | none | new anonymous party | 3 | weak |
| card_fingerprint q=0.95 | yes (party A) | email q=0.95 (party B) | dispute, queue review | 4 | unchanged |
| operator command | n/a | n/a | de_merge | 5 | recomputed from remaining identifiers |

---

## Part D — Householding Model

### Auto-detection rules

The household auto-detection job (`mcp.party.household-detect`) runs nightly per tenant when `PARTY_HOUSEHOLDS_ENABLED=true`. It evaluates these signals in order; the first match opens an evidence-collection cycle and the membership is created when evidence accumulates past threshold.

| Signal | Evidence weight | Threshold to auto-create | Notes |
|---|---|---|---|
| Two parties share a `c.customer_addresses` row (exact match on line_1, postal_code, city) | 0.5 | 1 confirmed event + 30 days | Shipping addresses are the cleanest household signal |
| Two parties share a `party.identifiers` row of type `phone_hash` AND it is a landline (heuristic: not flagged as mobile in BIN/carrier lookup) | 0.4 | 2 confirmed events | Less reliable — shared business phone is a false positive |
| Two parties share a `party.identifiers` row of type `card_fingerprint` AND share a shipping address | 0.7 | 1 confirmed event | Strong signal |
| Both parties have `c.loyalty_memberships` rows linked via merchant-defined `household_code` field | 1.0 | immediate | Merchant-explicit; trumps all other signals |
| Both parties' transactions consistently end at the same destination_location_id for BOPIS over a 60-day window | 0.3 | 5 confirmed events | Weak — could be a coworker |

### Manual assignment

Merchants can assert household membership directly via `mcp.party.household-detect` with `mode='manual'` and an explicit `member_party_ids[]` array. Manual assignments bypass the auto-detection threshold but still write `party.household_evidence` with `evidence_type='explicit_declaration'`.

### Privacy boundary

Households are **strictly per-tenant**. There is no cross-tenant household primitive and there will never be one. A consumer who is in a household at Tenant A and in a (correctly-distinct) household at Tenant B has two unrelated household_id values. This is the substrate-level expression of the platform's data-isolation thesis (per `feedback_publish_facts_not_gossip` + `project_data_hosting_compliance_phase4`).

### Use cases

1. **Marketing campaign de-duplication.** A "happy birthday" campaign should fire once per household, not once per party. The marketing service joins `party.household_memberships` and emits one mailing per household_id when any member's birth_date matches.
2. **LP investigation expansion.** When Hawk opens a case on a party, the investigator can pull all household members' transactions in scope under the same case via `mcp.party.household-detect` reverse-lookup. (Per [[concept-party-taxonomy|the party taxonomy]] §investigator: this requires the investigator-instrument substrate, out of scope here, but the data model supports it.)
3. **Returns-fraud ring detection.** Pattern: many returns from anonymous parties that all share a shipping address + payment fingerprint set. The household primitive is what lets a `chirp` rule express the pattern as "household-level return rate > 3x merchant-tenant baseline" rather than per-party.

---

## Part E — Decisioning Framework

The `party.decisioning_facts` view is the curated read surface for downstream services. Recomputing per-party RFM/LTV/segment in every `chirp` rule, `analytics` rollup, and marketing campaign was the bug we are explicitly designing out. One refresh cadence; one query path; one source of truth.

### View shape

| Field | Type | Computation | Refresh cadence | Primary consumer |
|---|---|---|---|---|
| `party_id` | uuid | natural key | n/a | all |
| `tenant_id` | uuid | natural key | n/a | all |
| `party_value` | numeric(14,4) | rolling 12-month total grand_total across all transactions resolved to this party | daily batch (02:00 local) | analytics, marketing |
| `party_recency` | int | days since `last_seen_at` | real-time on transaction.complete | chirp (dormancy alerts), marketing (win-back) |
| `party_frequency` | int | transactions in trailing 12 months | daily batch | analytics, marketing |
| `party_monetary` | numeric(14,4) | average transaction value, trailing 12 months | daily batch | analytics, marketing |
| `party_segment_tags` | text[] | computed from quintile bands of value/frequency/recency + merchant-defined rules | daily batch | marketing (audience scoping), commercial (segment-OTB) |
| `party_fraud_risk` | numeric(5,4) 0–1 | computed from `q.detections` involving this party + identifier_type quality scores + dispute rate | on-demand (computed at `mcp.party.decisioning-recompute`; cached 1 hour) | chirp, fox, hawk |
| `party_churn_risk` | numeric(5,4) 0–1 | model output: f(recency, frequency, value-trend) | weekly batch (Sunday 03:00 local) | marketing (retention campaigns) |

### Refresh discipline

Three cadences, one per row above:

1. **Real-time** (recency only) — updated synchronously when `mcp.party.resolve-from-tender` fires inside `t.transactions.complete`. The transaction emit is the trigger; the recency update is part of the same write.
2. **Daily batch** (value/frequency/monetary/segment) — `mcp.party.decisioning-recompute` runs at 02:00 local per tenant. Materialized view refreshed in place; old rows replaced atomically.
3. **On-demand or weekly** (fraud_risk on-demand with 1h cache; churn_risk weekly Sunday 03:00) — fraud_risk is recomputed inside `mcp.party.decisioning-recompute` when called with `compute_risk=true`; churn_risk is part of the weekly batch and read from the materialized cache otherwise.

The view is materialized, not a SQL view, because the per-party RFM math touches every transaction resolved to the party — for merchants over 100k transactions, a SQL view re-computes on every read and breaks the SLA. The materialized refresh is namespace-isolated (per-tenant batch) so a slow tenant doesn't starve fast tenants.

### Downstream consumption pattern

```
// In chirp/rules/dormancy.go
func (r *DormancyRule) Evaluate(ctx context.Context, partyID uuid.UUID) (*Detection, error) {
    facts, err := party.GetDecisioningFacts(ctx, partyID)  // hits the materialized view, p99 <30ms
    if err != nil {
        return nil, err
    }
    if facts.Recency > 90 && facts.Value > 500.0 {
        return &Detection{ /* high-value dormancy */ }, nil
    }
    return nil, nil
}
```

The downstream service never touches `t.transactions` directly to compute "days since last visit" — it asks the party module. This is the discipline that keeps downstream code small and the substrate cadence coherent.

---

## Technical

### Service Boundaries

The party module owns identity resolution, fingerprint storage, household relational primitives, and the decisioning-facts surface. It does *not* own the transactions, customer master, or LP subjects/cases — those remain with their respective services. Party reads from those services (via direct SQL within the same database; cross-schema reads, not cross-service HTTP) and writes only into the `party` schema.

| Owned schema | Tables |
|---|---|
| `party` | `parties` · `identifiers` · `resolution_events` · `households` · `household_memberships` · `household_evidence` · `decisioning_facts` (materialized view) |

What party reads (cross-schema; no FK declared either direction):

- `t.transactions` + `t.transaction_tenders` — to compute decisioning facts
- `c.customers` + `c.customer_addresses` + `c.loyalty_memberships` — for known-identity attachment and household evidence
- `q.detections` — for fraud_risk computation
- `o.sales_orders` — for channel-level facts

What party writes:

- Its own schema, exclusively
- `party.resolution_events` for every decision
- `party.decisioning_facts` materialized view on cadence

### Data Model

The DDL below targets `deploy/schema/12_party.sql` (a new file; part of the build, not a migration on existing data). The migration strategy in §Migration covers how existing rows back-populate.

```sql
-- 12_party.sql — Party Identity, Fingerprinting, Householding
-- Source: docs/sdds/go-handoff/party-identity-design.md
-- Schema: party (new)

CREATE SCHEMA IF NOT EXISTS party;

-- party.parties — the substrate identity node
CREATE TABLE party.parties (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    party_code      text NOT NULL,
    party_type      text NOT NULL DEFAULT 'consumer',
    -- party_type values: consumer | customer | household_aggregate
    -- (vendor | auditor | investigator | mcp_agent reserved for taxonomy expansion)
    display_name    text NOT NULL,
    status          text NOT NULL DEFAULT 'active',
    -- status values: active | merged | suppressed | dissolved
    merged_into     uuid REFERENCES party.parties(id),
    confidence      text NOT NULL DEFAULT 'anonymous',
    -- confidence values: anonymous | weak | probable | strong
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, party_code)
);
CREATE INDEX idx_parties_tenant_active ON party.parties(tenant_id) WHERE status = 'active';
CREATE INDEX idx_parties_merged_into ON party.parties(merged_into) WHERE merged_into IS NOT NULL;
CREATE INDEX idx_parties_confidence ON party.parties(tenant_id, confidence) WHERE status = 'active';
CREATE INDEX idx_parties_last_seen ON party.parties(tenant_id, last_seen_at);

-- party.identifiers — every signal that ties to a party
CREATE TABLE party.identifiers (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
    party_id               uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    identifier_type        text NOT NULL,
    -- identifier_type values: card_fingerprint | card_last4 | email_hash | phone_hash |
    --   pos_native_customer_id | loyalty_number | device_fingerprint | wallet_account_id |
    --   device_advertising_id
    identifier_value_hash  text NOT NULL,            -- SHA-256 hex of normalized value, tenant-salted
    source_system          text NOT NULL,
    -- source_system values: square | stripe | counterpoint | rapidpos | wallet_apple |
    --   wallet_google | manual | self_computed
    quality_score          numeric(3,2) NOT NULL,    -- 0.00 - 1.00
    first_seen_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at           timestamptz NOT NULL DEFAULT now(),
    occurrence_count       bigint NOT NULL DEFAULT 1,
    attributes             jsonb NOT NULL DEFAULT '{}',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, identifier_type, identifier_value_hash)
);
CREATE INDEX idx_identifiers_party ON party.identifiers(tenant_id, party_id);
CREATE INDEX idx_identifiers_type_quality ON party.identifiers(tenant_id, identifier_type, quality_score DESC);
CREATE INDEX idx_identifiers_last_seen ON party.identifiers(tenant_id, last_seen_at);

-- party.resolution_events — append-only resolution decision log
CREATE TABLE party.resolution_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
    party_id            uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    event_type          text NOT NULL,
    -- event_type values: resolve | merge | de_merge | identifier_add | identifier_dispute |
    --   confidence_upgrade | confidence_downgrade | fingerprint_recompute
    source_event_type   text,                        -- e.g. 't.transactions' | 'manual' | 'batch_recompute'
    source_event_id     uuid,                        -- e.g. t.transactions.id (soft FK)
    rule_id             text,                        -- which §C rule fired: 'rule_1_strong' | 'rule_4_conflict' | etc.
    confidence_before   text,
    confidence_after    text,
    evidence            jsonb NOT NULL DEFAULT '{}',
    actor               text NOT NULL DEFAULT 'system',
    -- actor values: system | manager_override | merchant_admin | dispatch_recompute
    created_at          timestamptz NOT NULL DEFAULT now()
);
-- INSERT-only. REVOKE UPDATE, DELETE at deployment.
CREATE INDEX idx_resevents_party_created ON party.resolution_events(party_id, created_at);
CREATE INDEX idx_resevents_tenant_event ON party.resolution_events(tenant_id, event_type, created_at);
CREATE INDEX idx_resevents_source ON party.resolution_events(source_event_type, source_event_id) WHERE source_event_id IS NOT NULL;

-- party.households — per-tenant household node
CREATE TABLE party.households (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_code  text NOT NULL,
    display_name    text,
    status          text NOT NULL DEFAULT 'active',
    -- status values: active | merged | dissolved
    formed_at       timestamptz NOT NULL DEFAULT now(),
    dissolved_at    timestamptz,
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, household_code)
);
CREATE INDEX idx_households_tenant_active ON party.households(tenant_id) WHERE status = 'active';

-- party.household_memberships — many-to-many with effective dates
CREATE TABLE party.household_memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_id    uuid NOT NULL REFERENCES party.households(id) ON DELETE RESTRICT,
    party_id        uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    member_role     text NOT NULL DEFAULT 'member',
    -- member_role values: head | member | dependent
    effective_start date NOT NULL DEFAULT CURRENT_DATE,
    effective_end   date,
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, household_id, party_id, effective_start),
    CONSTRAINT one_head_per_household
        EXCLUDE (household_id WITH =)
        WHERE (member_role = 'head' AND effective_end IS NULL)
);
CREATE INDEX idx_hhmem_party_current ON party.household_memberships(party_id) WHERE effective_end IS NULL;
CREATE INDEX idx_hhmem_household_current ON party.household_memberships(household_id) WHERE effective_end IS NULL;

-- party.household_evidence — append-only evidence log for memberships
CREATE TABLE party.household_evidence (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    membership_id   uuid NOT NULL REFERENCES party.household_memberships(id) ON DELETE RESTRICT,
    evidence_type   text NOT NULL,
    -- evidence_type values: shared_shipping_address | shared_payment_instrument |
    --   shared_loyalty_number | explicit_declaration | shared_wifi_device | shared_phone_area_local
    evidence_payload jsonb NOT NULL DEFAULT '{}',
    source_event_id  uuid,                            -- soft reference to triggering event
    confidence       numeric(3,2) NOT NULL,
    collected_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
-- INSERT-only. REVOKE UPDATE, DELETE at deployment.
CREATE INDEX idx_hhev_membership ON party.household_evidence(membership_id);
CREATE INDEX idx_hhev_tenant_collected ON party.household_evidence(tenant_id, collected_at);

-- party.decisioning_facts — materialized view, refreshed per cadence
CREATE MATERIALIZED VIEW party.decisioning_facts AS
SELECT
    p.id                                              AS party_id,
    p.tenant_id                                       AS tenant_id,
    p.confidence                                      AS confidence,
    COALESCE(SUM(t.grand_total) FILTER (
        WHERE t.business_date >= CURRENT_DATE - INTERVAL '12 months'
    ), 0)::numeric(14,4)                              AS party_value,
    COALESCE(EXTRACT(DAY FROM now() - p.last_seen_at)::int, 999) AS party_recency,
    COUNT(t.id) FILTER (
        WHERE t.business_date >= CURRENT_DATE - INTERVAL '12 months'
    )                                                 AS party_frequency,
    COALESCE(AVG(t.grand_total) FILTER (
        WHERE t.business_date >= CURRENT_DATE - INTERVAL '12 months'
    ), 0)::numeric(14,4)                              AS party_monetary,
    ARRAY[]::text[]                                   AS party_segment_tags,    -- populated by recompute job
    0.0::numeric(5,4)                                 AS party_fraud_risk,      -- populated by recompute job
    0.0::numeric(5,4)                                 AS party_churn_risk,      -- populated by recompute job
    now()                                             AS computed_at
FROM party.parties p
LEFT JOIN t.transactions t
    ON t.party_id = p.id AND t.tenant_id = p.tenant_id
WHERE p.status = 'active'
GROUP BY p.id, p.tenant_id, p.confidence, p.last_seen_at;

CREATE UNIQUE INDEX idx_dfacts_party ON party.decisioning_facts(party_id);
CREATE INDEX idx_dfacts_tenant_value ON party.decisioning_facts(tenant_id, party_value DESC);
CREATE INDEX idx_dfacts_tenant_recency ON party.decisioning_facts(tenant_id, party_recency);
CREATE INDEX idx_dfacts_tenant_segments ON party.decisioning_facts USING gin(party_segment_tags);

-- Append-only enforcement
REVOKE UPDATE, DELETE ON party.resolution_events FROM canary_app;
REVOKE UPDATE, DELETE ON party.household_evidence FROM canary_app;
```

### HTTP Endpoints

All routes require JWT auth except `/party/healthz` and `/party/readyz`.

```
GET  /party/healthz                                    → 200
GET  /party/readyz                                     → 200 | 503
POST /v1/party/resolve                                 → 200 {party_id, party_code, confidence, was_created}
POST /v1/party/merge                                   → 200 {surviving_party_id, merged_party_id, event_id}
POST /v1/party/identifier                              → 201 {identifier_id, party_id, was_attached}
GET  /v1/party/{id}                                    → 200 {party, identifiers[], household?, recent_resolution_events[]}
GET  /v1/party/{id}/decisioning-facts                  → 200 {decisioning_facts}
POST /v1/party/{id}/decisioning-facts:recompute        → 200 {decisioning_facts}
POST /v1/party/household:detect                        → 200 {household_id, members[], evidence_count}
POST /v1/party/subjects:resolve                        → 200 {subject_id, party_id}        -- the fox SDD-bug fix
GET  /v1/party?tenant_id=&confidence=&segment=         → 200 {parties[]}
```

### Performance NFRs

Per [[platform-performance-nfrs]]:

| Operation | P50 | P99 | Hard limit | Notes |
|---|---|---|---|---|
| `mcp.party.resolve-from-tender` (Rule 1 hit) | <15ms | <50ms | 200ms | called inside `t.transactions.complete`; sub-50ms p99 is the gate |
| `mcp.party.resolve-from-tender` (Rule 3 new party) | <30ms | <100ms | 500ms | INSERT path |
| `mcp.party.identifier-add` | <20ms | <80ms | 300ms | upsert into `party.identifiers` |
| `mcp.party.merge-anonymous-to-known` | <100ms | <500ms | 2s | involves multi-row updates + resolution event |
| `mcp.party.household-detect` (incremental) | <500ms | <2s | 10s | per-party detection inside batch |
| `mcp.party.decisioning-recompute` (single party) | <100ms | <500ms | 2s | on-demand fraud_risk recompute |
| `mcp.party.decisioning-recompute` (full tenant batch) | varies | varies | 30min | nightly job; not user-facing |
| `GET /v1/party/{id}/decisioning-facts` | <10ms | <30ms | 100ms | materialized-view read |

The 50ms p99 target on the inline-resolve path is the load-bearing one: party resolution sits inside `t.transactions.complete`, and Loop 2 ratified the transaction-complete p99 at <500ms (per `t.transactions` SLA in canonical model). Party must not eat more than 10% of that budget. The Rule 1 path is a single indexed lookup on `(tenant_id, identifier_type, identifier_value_hash)` — Postgres can serve that well under 15ms p99 against an indexed table in the millions-of-rows range.

### PCI Scope Considerations

The `card_fingerprint` identifier_type is a derivative of payment-network fingerprint material. Per `project_pci_scope_phase4`, Canary enters Service Provider scope at Phase 4 for storing any card-derived data, even tokenized.

The architecture honors the boundary as follows:

1. **Raw PAN never reaches the party module.** The `pos-adapter-substrate` tokenizes at the boundary. RapidPOS / EMVCo TLV data is forwarded to a vault service (or rejected at adapter ingress) before the party module sees it.
2. **Network-issued fingerprint** (Square `card.fingerprint`, Stripe `payment_method.card.fingerprint`) is *itself a tokenized derivative* — these are network-side tokens, not PAN. They are within scope but not the same as raw card data.
3. **Tenant-salted hash** is what we store. SHA-256 of `(tenant_id || network_fingerprint || optional_static_salt)` produces a value that is useless outside the tenant context and useless without the salt — a dump of `party.identifiers` across all tenants does not compromise card data because the hashes don't reverse.
4. **Tokenization vault posture.** When `PARTY_TOKENIZATION_VAULT_ENABLED=true`, raw card-network fingerprints go through a vault service (separate database, separate access control, separate compliance posture) before hashing. The party module sees only the post-vault tokenized form. This is the Phase 4 posture; before Phase 4, the flag is off and only network-pre-tokenized fingerprints are accepted.
5. **Audit log scope.** `party.resolution_events.evidence` JSONB should NEVER store raw network fingerprints — only the hash. A code-review checklist item plus an integration test verifies this. Loop 3 work item: add a CHECK constraint or trigger that scans for hex-string-shaped values longer than 32 chars in the JSONB and rejects.

The PCI scope diagnosis: at Phase 1–3, the party module is **not** in PCI scope because it stores only hashes of pre-tokenized network fingerprints. At Phase 4 (when Canary takes over the gateway path and may briefly handle raw card material at the ingress layer), the party module remains out of PCI scope because the vault service handles the raw material; party only ever sees the post-vault token. This is the architectural payoff of separating fingerprint computation (vault) from fingerprint storage (party).

### Soft FK Reconciliation

Per Loop 2 Wave 1's `q.detections.cashier_employee_id` precedent, cross-schema references that would create dependency cycles are declared as soft FKs (UUID columns without DB-level FK constraints). Application code enforces the link.

For party-id references from other schemas:

| Source column | Target | Hard or soft FK? | Rationale |
|---|---|---|---|
| `c.customers.party_id` | `party.parties.id` | HARD | `c` already FKs `app.tenants`; party is a peer schema; no cycle risk |
| `t.transactions.party_id` | `party.parties.id` | SOFT | `t` is the highest-volume schema; soft FK preserves party-schema independence and avoids transaction-completion blocking on party-table availability |
| `o.sales_orders.party_id` | `party.parties.id` | SOFT | same rationale as `t` |
| `q.subjects.party_id` | `party.parties.id` | SOFT | per Loop 2 pattern; q-schema soft FKs are the established convention |
| `q.detections.party_id` | `party.parties.id` | SOFT | same |

The application-level enforcement contract: any service writing to a soft-FK column must verify the target party exists in the same tenant before the write. Verification path is `party.GetByID(ctx, tenantID, partyID)` — fast indexed read; cached in Valkey for hot parties.

---

## Migration Strategy

Six phases, expected timeline 4–6 months end-to-end. Each phase is independently shippable and brings value before the full migration completes.

### Phase 1 — Schema land (week 1)

The `party` schema is created via `deploy/schema/12_party.sql`. The six tables and the materialized view materialize empty. The party service binary deploys to `:8094` with the five MCP tools; calls succeed against an empty schema (resolve always hits Rule 3 → new party). No FK columns are added to existing tables yet. No backfill runs.

This phase ships the substrate without disturbing existing reads or writes. Existing services continue to read `c.customers` directly; the party module is dark.

### Phase 2 — Inline resolve at transaction-complete (weeks 2–3)

`t.transactions.complete` is amended to call `mcp.party.resolve-from-tender` synchronously inside its commit transaction. The result is *captured but not stored* — there is no `t.transactions.party_id` column yet. This phase exists to validate p99 latency under real production load before adding the FK column. Telemetry: every resolve emits a `party_resolve_latency_ms` metric; the alarm is p99 > 50ms sustained 5 minutes.

### Phase 3 — Add nullable FK columns and start populating (weeks 4–5)

The proposed canonical-edits document lands. Migration adds `party_id uuid NULL` to `c.customers`, `t.transactions`, `o.sales_orders`, `q.subjects`, `q.detections`. New writes populate the column synchronously (the resolve from Phase 2 now persists). Old rows remain `NULL`. Reads are not yet allowed to assume the column is populated.

### Phase 4 — Backfill and Subjects.Resolve (weeks 6–9)

A backfill job walks existing `c.customers` rows and resolves each into the party graph: every `c.customers` row becomes one `party.parties` row with `confidence='strong'` and identifier rows for any present email/phone/external_ids. Existing `t.transactions` rows with non-NULL `customer_id` get their `party_id` set via the same path. Anonymous historical transactions (~70% of typical volume) get resolved via the per-tender card_fingerprint path against historical `t.transaction_tenders` data — the network fingerprints, where present, are normalized and a party row is materialized.

This phase also lands `mcp.party.subjects:resolve` — the `fox` SDD-bug fix per `internal/fox/handler.go:subjectFromDetection`. The Loop 3 work item that comment names ("introduce a Subjects.Resolve(tenantID, kind, refID) UPSERT keyed on q.subjects.related_employee_id / related_customer_id") is implemented as a thin wrapper: party module resolves the party (creating one if needed for the employee or customer), then upserts the q.subjects row keyed on `party_id`. Fox's `subjectFromDetection` returns `&partyID` after this junction is live.

### Phase 5 — Decisioning facts go live (weeks 10–12)

The `party.decisioning_facts` materialized view starts refreshing on the daily cadence. `chirp` rules, marketing campaigns, and analytics rollups migrate one consumer at a time onto the decisioning-facts read path. Every migrated consumer is verified against a "compute the same metric the old way and assert equality" shadow read for 7 days before cutover. Fraud_risk computation comes online; the `mcp.party.decisioning-recompute` junction starts taking on-demand calls.

### Phase 6 — Required FKs and household production (weeks 13–18)

With backfill complete and downstream consumers migrated, the `party_id` columns flip from `NULL`-allowed to `NOT NULL` (where appropriate — `t.transactions.party_id` may stay nullable for offline-mode transactions that complete before resolve runs). Household auto-detection switches on. Marketing and analytics migrate to household-level rollups where intended. Old direct-read paths from downstream services into `c.customers` for decisioning purposes are deprecated; only operational reads (display name, address) continue.

### Migration risk register

| Risk | Mitigation |
|---|---|
| Latency regression at transaction-complete from inline resolve | Phase 2 measures before Phase 3 commits; rollback is removing the call |
| Backfill volume too high for nightly window | Phase 4 chunks per-tenant per-night; 60-day backfill window built in |
| Resolution rule produces unwanted merges | Rule 4 (conflict) defaults to no-merge + queue; rule 5 (de-merge) is the recovery path; every decision logged |
| Materialized view refresh contention with OLTP | Per-tenant batch isolation; refresh windowed to 02:00–04:00 local; concurrency limit on background workers |
| Soft FK drift (party_id pointing at deleted party) | Deletes forbidden on `party.parties`; merge replaces delete; merge pointer chase is the application convention |

---

## Open Questions for Founder Review

1. **Anonymous historical backfill — yes or no?** Phase 4 proposes resolving historical anonymous transactions into party rows via card_fingerprint normalization on existing `t.transaction_tenders`. This produces the most useful decisioning data on day one but doubles backfill cost. Alternative: leave historical anonymous transactions unresolved, only resolve from cutover forward. Recommendation: do the backfill — the LP and marketing value of "we have repeat-anonymous-shopper data going back 12 months" is too high to forfeit.

2. **Self-computed fingerprint quality threshold.** The `self_computed` fingerprint (SHA-256 of `card_brand:last_4:zip5`) scores at 0.40 — below the Rule 1 (0.90) and Rule 2 (0.85) thresholds. It can never trigger a strong/probable resolution; it only attaches as supporting evidence. Should it be allowed at all? It produces noise without lifting confidence. Recommendation: keep it for the LP fraud-pattern-matching value (recurring weak-fingerprint within a window is a chirp signal), but never let it create a new party — fall through to Rule 3 with a marker.

3. **Household privacy default — opt-in vs opt-out at the tenant level.** Per the privacy posture, householding is per-tenant, but should it be **off by default** for new tenants and require explicit enablement? This is a CIO-conversation question more than a technical one. Recommendation: off by default; merchants who enable it sign a one-paragraph data-use addendum that documents household auto-detection.

4. **De-merge audit visibility.** When Rule 5 fires (de-merge), the audit trail is written but not visible in any merchant-facing UI. Should the merchant-facing party view show a "this party was previously merged with [other party] until [date]" annotation? It increases LP investigative capability at the cost of potentially exposing prior false-merge mistakes. Recommendation: visible in the LP investigator UI per [[concept-party-taxonomy|investigator party type]]; hidden in the marketing/analytics merchant UI.

5. **Subjects.Resolve eager vs lazy.** Phase 4 implements `party.subjects:resolve` as a junction. Should `q.detections` writes synchronously create the q.subjects row (eager) or should it stay nullable until a case escalates (lazy)? The eager path makes `chirp` rule clustering work immediately (the EscalationStore.FindOpenCaseBySubject path the fox comment names); the lazy path defers the q.subjects row creation cost. Recommendation: eager — the cost of subject creation is tiny (one indexed insert) and the immediate benefit to clustering is large.

---

## Cross-references

### Canonical data model
- `canonical-data-model.md` §5 (c.customers et al.) — the existing customer master that party sits *above*, not replaces
- `canonical-data-model.md` §10 (q.subjects et al.) — LP subjects that get a party_id soft-FK
- `canonical-data-model.md` §12 (t.transactions et al.) — transactions that resolve to parties at completion
- `canonical-data-model.md` §7 (o.sales_orders) — sales orders that resolve to parties at create
- `canonical-data-model-party-edits.md` (NEW, this dispatch) — proposed in-place edits for the founder to review

### MCP junctions
- `mcp-service-junctions.md` — 5 new entries added: `mcp.party.resolve-from-tender`, `mcp.party.merge-anonymous-to-known`, `mcp.party.identifier-add`, `mcp.party.household-detect`, `mcp.party.decisioning-recompute`. See archetype assignments in that file.

### Brain wiki
- [[concept-party-taxonomy|Party Taxonomy — Six Parties, One Substrate]] — the substrate-level taxonomy this SDD scopes-down to consumer/customer
- [[concept-identity-layer-triad]] — the identity model the taxonomy operates within; future cross-tenant party projection lives here, not here
- [[platform-performance-nfrs]] — SLA targets the party module honors
- [[loop2-build-report]] — the universal `merchant_id`↔`tenant_id` finding (this SDD uses `tenant_id` consistently); the `q.detections` soft-FK precedent

### Memory references
- `feedback_publish_facts_not_gossip` — household privacy posture; no cross-tenant party graph
- `project_data_hosting_compliance_phase4` — party data is high-PII; in-scope for Phase 4 SOC 2 / GDPR
- `project_canary_canonical_positioning` — Canary's job is to make decisions about parties; this SDD is the substrate
- `project_pci_scope_phase4` — informs the PCI scope diagnosis in §Technical / PCI Scope Considerations

### Code references (forward — not yet written)
- `internal/party/` — the party service implementation (Loop 3 / Loop 4 work)
- `internal/fox/handler.go:subjectFromDetection` — the SDD-bug comment naming this SDD as the Loop 3 fix; resolved in Phase 4
- `deploy/schema/12_party.sql` — DDL deployment file (Phase 1 lands this)
- `internal/db/sqlc/party.sql` — sqlc query definitions (Loop 3 work)
