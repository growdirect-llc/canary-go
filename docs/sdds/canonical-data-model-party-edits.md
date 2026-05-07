---
spec-version: 1.0
status: proposed-edits
updated: 2026-05-03
authority: GRO-734
target: docs/sdds/go-handoff/canonical-data-model.md
companion: docs/sdds/go-handoff/party-identity-design.md
---

# Canonical Data Model — Party Identity Edits (Proposed)

Per **GRO-734**. Apply these edits to `canonical-data-model.md` after design review.

This document is intentionally not an in-place edit. The canonical data model carries 88 tables across 14 schemas and serves as the binding contract for the Go build; modifications are a founder-gated event. The intent here is to land the party-substrate design (per `party-identity-design.md`) into the canonical with the minimum disturbance to existing tables, and to give the founder a single concentrated diff to ratify before the canonical takes the change.

The edits group into five sections (A–E) plus a soft-FK reconciliation note. Apply in order.

---

## §A — Add a new domain section: §13 Party (Substrate)

Insert a new top-level section after the existing §12 Transaction Pipeline. This mirrors the pattern of how §10 Q-Schema and §11 Ledger are structured: a domain narrative plus per-entity DDL with provenance and operational lifecycle.

The §13 section's full DDL appears below; for narrative content, embed the §Business and §Part A through §Part E sections from `party-identity-design.md` directly into the canonical's §13. The shape:

```
## §13 — Party (Substrate Identity)

**Entities**: 6 (parties · identifiers · resolution_events · households · household_memberships · household_evidence) + 1 materialized view (decisioning_facts)
**Folded from sources**: net-new — no GSLM / CRDM / TOM source carries this primitive
**Schema**: `party`

### Domain narrative

[Lift §Business from party-identity-design.md, paragraphs 1-3]

### Why party is upstream of customer

[Lift the "party is upstream of the customer record" rationale from §Business / Failure Mode 2]
```

### DDL block to insert

```sql
-- §13.1 party.parties — substrate identity node
CREATE SCHEMA IF NOT EXISTS party;

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

-- §13.2 party.identifiers — every signal that ties to a party
CREATE TABLE party.identifiers (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              uuid NOT NULL REFERENCES app.tenants(id),
    party_id               uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    identifier_type        text NOT NULL,
    identifier_value_hash  text NOT NULL,
    source_system          text NOT NULL,
    quality_score          numeric(3,2) NOT NULL,
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

-- §13.3 party.resolution_events — append-only resolution decision log
CREATE TABLE party.resolution_events (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
    party_id            uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    event_type          text NOT NULL,
    source_event_type   text,
    source_event_id     uuid,
    rule_id             text,
    confidence_before   text,
    confidence_after    text,
    evidence            jsonb NOT NULL DEFAULT '{}',
    actor               text NOT NULL DEFAULT 'system',
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_resevents_party_created ON party.resolution_events(party_id, created_at);
CREATE INDEX idx_resevents_tenant_event ON party.resolution_events(tenant_id, event_type, created_at);
CREATE INDEX idx_resevents_source ON party.resolution_events(source_event_type, source_event_id) WHERE source_event_id IS NOT NULL;

-- §13.4 party.households — per-tenant household node
CREATE TABLE party.households (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_code  text NOT NULL,
    display_name    text,
    status          text NOT NULL DEFAULT 'active',
    formed_at       timestamptz NOT NULL DEFAULT now(),
    dissolved_at    timestamptz,
    attributes      jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, household_code)
);
CREATE INDEX idx_households_tenant_active ON party.households(tenant_id) WHERE status = 'active';

-- §13.5 party.household_memberships — many-to-many with effective dates
CREATE TABLE party.household_memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    household_id    uuid NOT NULL REFERENCES party.households(id) ON DELETE RESTRICT,
    party_id        uuid NOT NULL REFERENCES party.parties(id) ON DELETE RESTRICT,
    member_role     text NOT NULL DEFAULT 'member',
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

-- §13.6 party.household_evidence — append-only evidence log
CREATE TABLE party.household_evidence (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
    membership_id   uuid NOT NULL REFERENCES party.household_memberships(id) ON DELETE RESTRICT,
    evidence_type   text NOT NULL,
    evidence_payload jsonb NOT NULL DEFAULT '{}',
    source_event_id  uuid,
    confidence       numeric(3,2) NOT NULL,
    collected_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_hhev_membership ON party.household_evidence(membership_id);
CREATE INDEX idx_hhev_tenant_collected ON party.household_evidence(tenant_id, collected_at);

-- §13.7 party.decisioning_facts — materialized view (refresh cadence per party-identity-design.md §E)
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
    ARRAY[]::text[]                                   AS party_segment_tags,
    0.0::numeric(5,4)                                 AS party_fraud_risk,
    0.0::numeric(5,4)                                 AS party_churn_risk,
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

### Operational lifecycle (summary)

Producers / consumers / SLA targets are documented in full at `party-identity-design.md` §Technical and §Migration. Summary for the canonical:

**Producers**: `mcp.party.resolve-from-tender` · `mcp.party.merge-anonymous-to-known` · `mcp.party.identifier-add` · `mcp.party.household-detect` · `mcp.party.decisioning-recompute` (5 net-new junctions, all added to `mcp-service-junctions.md`).

**Consumers**: every downstream service that decisions about a party reads `party.decisioning_facts` (the materialized view) — never joins onto party tables directly. Consumer list: `chirp` (LP detection rules), `analytics` (RFM / LTV rollups), marketing (campaign audiences), commercial (segment-level OTB), Hawk (case subject resolution), Fox (replaces ad-hoc subject creation per the Loop 3 SDD-bug comment).

**Provenance**: net-new substrate. No source corpus (GSLM, CRDM, TOM, Retek, Square legacy) carries this primitive. The closest neighbor is ARTS Party (the abstract supertype mentioned at canonical §5 line 1485, intentionally not materialized) — the party schema here finally materializes the supertype, scoped to commercial/consumer parties only per `concept-party-taxonomy`.

---

## §B — `c.customers` updates

Add a `party_id` column. This is the ratification of "party is upstream of the customer record" — every `c.customers` row resolves to exactly one `party.parties` row, but a party may have multiple `c.customers` rows (one per POS-source identity).

```sql
-- Add to c.customers DDL in canonical §5:
ALTER TABLE c.customers
    ADD COLUMN party_id uuid REFERENCES party.parties(id);

CREATE INDEX idx_customers_party ON c.customers(party_id) WHERE party_id IS NOT NULL;
```

**Nullability**: `NULL` allowed during Phase 1–5 of the migration (per `party-identity-design.md` §Migration). Phase 6 may flip to `NOT NULL` for `c.customers` specifically (since `c` is a low-volume schema and backfill completes early). A founder gate at Phase 6 confirms the flip.

**FK posture**: HARD. Both `c` and `party` schemas FK to `app.tenants` already; no cycle risk, and the party module needs the inverse-lookup ("which customer rows belong to this party") at decisioning-facts compute time.

**Operational lifecycle update**:

- `mcp.customer.create` — must resolve-or-create a party first; sets `party_id` synchronously
- `mcp.customer.from-pos-native-sync` — same; the POS-native sync path is the most likely producer of multiple `c.customers` rows for one party (one per source POS), so resolve-by-existing-identifier is critical here

---

## §C — `t.transactions` updates

Add a `party_id` column. This is the highest-volume use of the party_id reference; every transaction completion resolves a party.

```sql
-- Add to t.transactions DDL in canonical §12:
ALTER TABLE t.transactions
    ADD COLUMN party_id uuid;

CREATE INDEX idx_tx_party ON t.transactions(party_id) WHERE party_id IS NOT NULL;
```

**Nullability**: stays `NULL`-allowed permanently. Offline-mode transactions that complete before the party module is reachable will have `NULL` party_id; a backfill job resolves them on next sync. The decisioning_facts view's `LEFT JOIN` on `t.party_id` handles `NULL` correctly.

**FK posture**: SOFT (no DB-level FK declaration). Per Loop 2's `q.detections.cashier_employee_id` precedent. The application contract: `party.GetByID(ctx, tenantID, partyID)` validates before write; the party module guarantees its rows are not deleted (merge replaces delete), so the contract is durable even without a DB-enforced FK.

**Operational lifecycle update**:

- `mcp.transaction.complete` (existing junction) — extended to call `mcp.party.resolve-from-tender` synchronously inside the same transaction; sets `party_id` from the resolve result
- `mcp.transaction.return-from-receipt` — inherits `party_id` from the parent transaction (no fresh resolve)
- `mcp.transaction.void` — inherits `party_id` from the parent (no fresh resolve)

---

## §D — `q.subjects` updates

Add a `party_id` column. This obviates the soft-FK pattern documented at canonical §10 (where `q.subjects` carries `related_employee_id`, `related_customer_id`, `related_vendor_id` as soft FKs).

```sql
-- Add to q.subjects DDL in canonical §10:
ALTER TABLE q.subjects
    ADD COLUMN party_id uuid;

CREATE INDEX idx_qsub_party ON q.subjects(party_id) WHERE party_id IS NOT NULL;
```

**Deprecation note (do NOT remove the related_* columns yet)**: `related_employee_id`, `related_customer_id`, `related_vendor_id` stay in place during Phases 1–5. Once `party_id` is fully populated and `mcp.party.subjects:resolve` is the canonical creation path (Phase 4), the `related_*` columns become redundant — every party already encodes which underlying entity it represents through its identifier set. Phase 7 (post-Phase-6, separate ratification) deprecates the columns. Until then, they remain for read-path compatibility.

**Fox SDD-bug fix**: per `internal/fox/handler.go:subjectFromDetection`, the comment names the Loop 3 work as "introduce a Subjects.Resolve(tenantID, kind, refID) UPSERT keyed on q.subjects.related_employee_id / related_customer_id". The party-substrate design absorbs that work — `mcp.party.subjects:resolve` does the upsert keyed on `party_id` instead, which subsumes the related_* keying. Fox's `subjectFromDetection(det)` becomes:

```go
func subjectFromDetection(ctx context.Context, det *types.Detection) *uuid.UUID {
    partyID := party.ResolveFromDetection(ctx, det)  // resolves to party
    if partyID == nil { return nil }
    subjectID := party.ResolveSubject(ctx, det.TenantID, *partyID)  // upsert q.subjects
    return &subjectID
}
```

**FK posture**: SOFT (no DB-level FK).

---

## §E — `o.sales_orders` updates

Add a `party_id` column. Mirrors the `t.transactions` case — sales orders carry the party identity for downstream decisioning.

```sql
-- Add to o.sales_orders DDL in canonical §7:
ALTER TABLE o.sales_orders
    ADD COLUMN party_id uuid;

CREATE INDEX idx_so_party ON o.sales_orders(party_id) WHERE party_id IS NOT NULL;
```

**Nullability**: stays `NULL`-allowed. Guest orders may resolve only at fulfillment (when shipping address is captured) rather than at create. The `customer_id` column stays in place for the same reason it does on `t.transactions` — operational reads (display, address) work off `c.customers` as today, decisioning reads work off `party.decisioning_facts`.

**FK posture**: SOFT.

**Operational lifecycle update**:

- `mcp.orders.sales-order.create-from-web` / `create-from-bopis` / `create-from-phone` / `create-from-marketplace` (existing junctions) — extended to call `mcp.party.resolve-from-tender` (or its order-context variant) synchronously; sets `party_id` from the result. For guest orders without a payment method at create, defer the resolve to first-payment-attached event.

---

## §F — `q.detections` updates (optional but recommended)

Add a `party_id` column. This makes party-level fraud_risk computation a direct read against `q.detections` GROUP BY party_id rather than a multi-step join through cashier or customer.

```sql
-- Add to q.detections DDL in canonical §10:
ALTER TABLE q.detections
    ADD COLUMN party_id uuid;

CREATE INDEX idx_qdet_party ON q.detections(party_id) WHERE party_id IS NOT NULL;
```

**Why optional**: `q.detections` already carries `cashier_employee_id` and `customer_id` (both soft FKs). Party-level fraud rate can be computed via those joins without `q.detections.party_id`. The direct column is a performance optimization for the `party.decisioning_facts.party_fraud_risk` recompute job. Recommend adding; explicitly call out the perf rationale in the column comment.

**FK posture**: SOFT.

---

## Soft-FK Reconciliation Note

This section reconciles the party_id additions with Loop 2's soft-FK pattern. The pattern: cross-schema references that would otherwise create cycles, or that span schemas owned by independently-shipping services, are declared as UUID columns without DB-level FK constraints. Application code enforces.

Decision matrix for the party_id additions:

| Source column | Target | Hard or soft FK | Rationale |
|---|---|---|---|
| `c.customers.party_id` | `party.parties.id` | **HARD** | `c` is single-service-owned; party is a peer schema also single-service-owned; both FK `app.tenants` already; no cycle risk. The hard FK gives DB-level guarantee that customer.party_id always points at a real party — useful at decisioning-facts join time. |
| `t.transactions.party_id` | `party.parties.id` | **SOFT** | `t` is the highest-volume schema (every POS scan); transaction-complete must not block on party-table availability. Application enforcement: `party.GetByID(ctx, tenant, party_id)` is the pre-write check; party module guarantees `parties` rows are immutable-by-id (merges set merged_into, never DELETE). |
| `o.sales_orders.party_id` | `party.parties.id` | **SOFT** | Same rationale as `t`; orders can complete in offline-mode and resolve later. |
| `q.subjects.party_id` | `party.parties.id` | **SOFT** | Established Loop 2 pattern for q-schema; q-schema soft FKs across the board. |
| `q.detections.party_id` | `party.parties.id` | **SOFT** | Same; precedent already set on `cashier_employee_id` / `customer_id`. |

**Application enforcement contract** (apply uniformly to all soft-FK callers):

1. Before any write to a soft-FK column, call `party.GetByID(ctx, tenantID, partyID)` — fast indexed lookup; cached in Valkey for hot parties.
2. If the party exists with `status='merged'`, follow the `merged_into` pointer to the surviving party and write *that* id instead. (Never store a merged party_id.)
3. If the party does not exist, the write fails with `party_not_found` — caller is responsible for resolving the party first.
4. Reads must follow the merge pointer at read time too. The party module exposes a `party.ResolveCurrent(ctx, tenantID, partyID) → (currentPartyID, status)` helper.

**Why the c.customers path is hard but t.transactions is soft**: the read pattern. `c.customers` reads happen on *operator queries* — looking up a customer for support, marketing, or analytics — and tolerate the strictness of a hard FK. `t.transactions` reads happen on *every POS scan, every metric rollup, every Chirp rule evaluation* — billions per year per large tenant — and the additional FK-validation overhead at write time is not justified given the application contract is already strong.

---

## Apply order

If the founder ratifies, apply in this order to avoid mid-application inconsistency:

1. §A (create the `party` schema and 6 tables + materialized view)
2. §B (add `c.customers.party_id`, hard FK)
3. §C (add `t.transactions.party_id`, soft FK)
4. §D (add `q.subjects.party_id`, soft FK)
5. §E (add `o.sales_orders.party_id`, soft FK)
6. §F (add `q.detections.party_id`, soft FK) — optional
7. Update operational lifecycle documentation in canonical §5 / §7 / §10 / §12 to reference the new junctions

The migration phasing in `party-identity-design.md` §Migration uses these schema changes — Phase 1 lands §A, Phase 3 lands §B–§F.

---

## Cross-references

- **Driving SDD**: `docs/sdds/go-handoff/party-identity-design.md` (this dispatch)
- **Junctions**: `docs/sdds/go-handoff/mcp-service-junctions.md` (5 new entries added)
- **Brain card**: [[concept-party-taxonomy]] — the substrate-level taxonomy
- **Loop 2 finding**: `Brain/wiki/cards/loop2-build-report.md` — `merchant_id`↔`tenant_id` and soft-FK precedent
- **Fox SDD-bug**: `CanaryGo/internal/fox/handler.go:subjectFromDetection` — the in-code comment naming this work
- **GRO ticket**: GRO-734
