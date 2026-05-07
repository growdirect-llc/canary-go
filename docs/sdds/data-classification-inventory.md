---
spec-version: 1.0
target-implementation: Go
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Data Classification and Governance Inventory

> Consolidated inventory of every field across the Canary Go SDD library that
> touches personal, sensitive, restricted, or regulated data. Source of truth
> for compliance scope, encryption posture, retention windows, and regulatory
> applicability.

## Governing Thesis

Canary Go is a multi-tenant retail SaaS that ingests cardholder-adjacent payment data, employee identity data, customer contact data, vendor financial data, and merchant credentials. Sensitive and Restricted-class fields are encrypted at rest using AES-256-GCM per `go-security`; PII fields whose plaintext domain is enumerable (phone, email) are stored as keyed HMAC-SHA256 hashes per `platform-pii-hashing`; restricted fields in append-only tables use the per-subject DEK pattern per `platform-cryptographic-erasure`. Cross-tenant access is prevented by schema-per-tenant isolation per `architecture.md` "Multi-Tenant Isolation" — every operational table lives in `tenant_{merchant_id}`, and queries set `search_path` from the JWT merchant claim. Optional features (L402, ILDWAC, blockchain anchor) extend the data model but do not change the classification scheme — they are env-gated per `platform-overview.md` "Optional Features" and operate against the same tier definitions.

This inventory is the single document a CIO, a Big-4 audit partner, or a regulatory examiner can read to understand exactly what data Canary holds, under which classification, with what protection, and against which regulatory regime. P0 / P1 / P2 markers in the table below indicate where the encryption and retention controls in the spec are not yet fully implemented in code — those are the engineering punch list, not gaps in the design.

## Classification Scheme

Canary uses a four-tier classification scheme defined in `data-model.md` lines 55–61. The scheme is referenced consistently across the SDD library; where individual SDDs deviate the deviation is called out in Section D.

| Tier | Definition | Treatment | Examples |
|---|---|---|---|
| **public** | Freely visible, no access control needed | None | `event_hash`, `chain_hash`, `merkle_root`, product names, metric aggregates |
| **internal** | Visible to authenticated users within tenant | Tenant-scoped read; no encryption required | `org_name`, `merchant_name`, `location_name`, alert counts, opaque external IDs |
| **sensitive** | Must be encrypted at rest, logged on access | AES-256-GCM at rest; access audit | Names, emails, phone numbers, addresses, IP addresses, card BIN, card expiration |
| **restricted** | Encrypted at rest, RLS-gated, audited on every access | AES-256-GCM at rest; mandatory access log; RLS isolation | OAuth tokens, POS API credentials, raw webhook payloads, investigation subjects, card-on-file tokens |

Source: `data-model.md` § "PII Classification" (lines 55–61); `go-security.md` § "AES-256-GCM Field Encryption" and § "PII Hashing Keys".

A fifth implicit class — **PCI** — appears in `go-security.md` (line 29) and `ecom-channel.md` (line 580) for card-on-file tokens. PCI is treated as a peer of restricted; both demand AES-256-GCM and tightened access. The scheme above subsumes PCI under restricted with a "PCI scope" annotation in Section A.

---

## Section A — Master Data Inventory

The table below consolidates every classified field across the SDD library. Where a field appears in multiple SDDs, all source citations are listed. The "Protection at Rest" column reflects the **target** posture per the SDDs; the "Notes" column flags whether that posture is implemented (per `data-model.md` Open Security Findings) or still open (P0/P1/P2). Retention values come from the per-domain Compliance / Hat 4 sections; "none specified" means no SDD declares a retention policy for the field.

| # | Field | Table / Location | SDD Source | Classification | Protection at Rest | Retention | Regulatory Scope | MCP Tool Exposure | Notes |
|---|---|---|---|---|---|---|---|---|---|
| 1 | `email` | `app.users` | data-model.md L76, L306; identity.md L59 | sensitive | AES-256-GCM (target; **plaintext today** — P0-2) | Active accounts; deactivated +12 mo (proposed) | GDPR, CCPA, state breach | `get_employee` (identity); session JWT | P0 unfinished |
| 2 | `username` | `app.users` | data-model.md L77, L305 | sensitive | AES-256-GCM (target; plaintext today) | Same as user record | GDPR, CCPA | session JWT | Derived from email — same risk profile |
| 3 | `display_name` | `app.users` | data-model.md L78, L307 | sensitive | AES-256-GCM (target; plaintext today) | Same as user record | GDPR, CCPA | identity-bound responses | P0 unfinished |
| 4 | `last_login_at` | `app.users` | identity.md L62 | internal | Plaintext | Active account lifetime | GDPR (account activity record) | identity-bound | — |
| 5 | `billing_email` | `app.organizations` | data-model.md L71, L224; identity.md L63 | sensitive | AES-256-GCM (target; plaintext today) | Org lifetime | GDPR, CCPA, CAN-SPAM | identity-bound | P0-3 |
| 6 | `notif_phone` | `app.merchant_settings` | data-model.md L75, L288; identity.md L66 | sensitive | AES-256-GCM (target; plaintext today) | Setting lifetime | GDPR, CCPA, TCPA, CASL | `get_settings` (identity) | P1-3 in identity.md |
| 7 | `employee_name` | `app.employees` | data-model.md L79, L367; identity.md L72 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (employment record); not specified in spec | GDPR, CCPA, US/CA employment law | `list_employees`, `get_employee` (identity); `top-risks` (analytics) | P0-2; PII display toggle (`show_employee_names`) gates display |
| 8 | `email` | `app.employees` | data-model.md L80, L368 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (employment record) | GDPR, CCPA, US employment law | `list_employees`, `get_employee` (identity) | P1-2 in identity.md |
| 9 | `phone` | `app.employees` | identity.md L71 (cited as field) | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (employment record) | GDPR, CCPA, TCPA | `list_employees`, `get_employee` (identity) | Field documented in identity.md but not listed in data-model.md PII map — **inventory drift** |
| 10 | `square_employee_id` | `app.employees` | data-model.md L81, L366 | internal | Plaintext | Same as employee | None direct | identity tools | Cross-reference to Square; not PII alone |
| 11 | `square_customer_id` | `app.customers` | data-model.md L82, L443 | internal | Plaintext | Until cancellation | None direct | identity tools | "Privacy-first: no PII stored beyond external system ID" — but webhook payloads still carry customer PII (see Field 24) |
| 12 | `address_line1` | `app.locations` | data-model.md L83, L393 | sensitive | AES-256-GCM (target; plaintext today) | Location lifetime | GDPR, CCPA | `list_locations`, `get_location` (identity) | P0-2 |
| 13 | `address_line2` | `app.locations` | data-model.md L84, L394 | sensitive | AES-256-GCM (target; plaintext today) | Location lifetime | GDPR, CCPA | identity tools | P0-2 |
| 14 | `postal_code` | `app.locations` | data-model.md L87, L397 | sensitive | AES-256-GCM (target; plaintext today) | Location lifetime | GDPR, CCPA (location fingerprint) | identity tools | P0-2 |
| 15 | `coordinates` (JSON lat/lng) | `app.locations` | data-model.md L88, L398 | sensitive | AES-256-GCM (target; plaintext today) | Location lifetime | GDPR (precise geolocation) | identity tools | P0-2; consider hashing |
| 16 | `access_token_encrypted` | `app.square_oauth_tokens` | data-model.md L89, L487; identity.md L67 | restricted | AES-256-GCM (**implemented**) | Token lifetime; revoked rows deleted | OAuth provider TOS; PCI-adjacent | None directly; refresh path only | Decrypt must log to `audit_log` (P1-1 finding; **not yet implemented**) |
| 17 | `refresh_token_encrypted` | `app.square_oauth_tokens` | data-model.md L90, L488; identity.md L68 | restricted | AES-256-GCM (**implemented**) | Token lifetime | OAuth provider TOS | None | Same as #16 |
| 18 | `email` | `app.interest_signups` | data-model.md L91, L1452 | sensitive | AES-256-GCM (target; plaintext today) | Append-only; no purge | GDPR, CCPA, CAN-SPAM | None | P0-8 |
| 19 | `recipient` | `app.notification_log` | data-model.md L97, L741; alert.md L136, L583 | sensitive | AES-256-GCM (target; plaintext today) | 12 mo (proposed) | GDPR, CCPA, TCPA, CAN-SPAM | None directly; alert tools | P0-1 in alert.md |
| 20 | `details` | `app.alerts` | alert.md L92, L585 | sensitive | AES-256-GCM (target; plaintext today) | 24 mo (proposed) | GDPR (free-text may carry employee/customer PII) | alert MCP tools (`get_alert`, etc.) | P0-3 in alert.md |
| 21 | `ip_address` | `app.audit_log` | data-model.md L98, L1433 | sensitive | HMAC-SHA256 keyed hash or /24 truncate (target; plaintext today) | 24 mo (proposed) | GDPR (Art. 4 PII), CCPA | None | P1-1 / P1-6; not yet implemented |
| 22 | `ip_address` | `app.fox_evidence_access_log` | data-model.md L114, L976; fox.md L243 | sensitive | One-way hash (SHA-256 or HMAC) per fox.md L243 | INSERT-only; 7 yr (LP evidence) | GDPR, CCPA | None directly | Fox spec **mandates** hash; data-model says P1 — drift (Section D) |
| 23 | `ip_address` | `app.ingestion_log`; `sales.devices` | tsp.md L150; webhook-pipeline.md L96; tsp-parse.md L327 | sensitive | HMAC hash with rotating key (target; plaintext today) | 90 d (proposed) | GDPR, CCPA | None | P0-TSP-05 |
| 24 | `name` | `app.fox_subjects` | data-model.md L105, L995; fox.md L128 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (LP evidence) | GDPR, CCPA, US/CA employment law | fox MCP tools (`get_case`, etc.) | P0-7; investigation subject |
| 25 | `entity_id` | `app.fox_subjects` | data-model.md L106, L994 | sensitive | Plaintext (cross-reference) | Same as subject | GDPR (linkage to employee) | fox tools | P1: integrity validation |
| 26 | `role_in_case` | `app.fox_subjects` | fox.md L129 | sensitive | AES-256-GCM (target) | Same as subject | GDPR | fox tools | Free text — may carry HR / legal context |
| 27 | `description` (case narrative) | `app.fox_cases` | fox.md L84 | sensitive | AES-256-GCM (target) | 7 yr (LP evidence) | GDPR, US/CA employment law | fox tools | Free text |
| 28 | `resolution` | `app.fox_cases` | fox.md L92 | sensitive | AES-256-GCM (target) | 7 yr | GDPR, US/CA employment law | fox tools | HR decision text |
| 29 | `description` / `outcome` | `app.fox_case_actions` | fox.md L145, L148 | sensitive | AES-256-GCM (target) | 7 yr | GDPR, US/CA employment law | fox tools | HR action outcomes |
| 30 | `external_name` | `app.hawk_subjects` | data-model.md L1076 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (LP evidence) | GDPR, CCPA | hawk tools | Inherits fox_subjects classification |
| 31 | `payload` (raw POS JSON) | `app.webhook_events` | data-model.md L120, L1195; tsp.md L142; multi-pos-architecture-proof.md L65 | restricted | AES-256-GCM at rest or PII redaction (target; plaintext today) | 12 mo (proposed) | GDPR, CCPA, PCI DSS | tsp tools | P0-10; full POS payload — names, emails, cards, IPs |
| 32 | `holder_name` | `app.bank_accounts` | data-model.md L126, L628 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (financial record) | GDPR, CCPA, GLBA, Reg P | identity-bound | P0-5; financial PII |
| 33 | `routing_number` | `app.bank_accounts` | data-model.md L127, L629 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (financial record) | GLBA, Reg P, state breach | identity-bound | P0-5 |
| 34 | `secondary_routing_number` | `app.bank_accounts` | data-model.md L128, L630 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr (financial record) | GLBA, Reg P | identity-bound | P0-5 |
| 35 | `account_number_suffix` | `app.bank_accounts` | data-model.md L129, L631 | internal | Plaintext (last 4 only — PCI-safe) | 7 yr | GLBA | identity-bound | Last 4 only |
| 36 | `card_fingerprint` | `app.card_profiles`; `sales.transactions` | data-model.md L135, L144, L1288, L1483 | internal | Plaintext | 7 yr (financial record) | PCI DSS (pseudonymous, not PAN) | tsp / fox tools | "PCI-safe hash" — but linkable across transactions |
| 37 | `card_last4` | `app.card_profiles`; `sales.transactions`; `sales.transaction_tenders` | data-model.md L136, L145, L153, L1289, L1485, L1530 | internal | Plaintext | 7 yr | PCI DSS (last 4 carve-out per DSS 3.3) | tsp / fox tools | Per PCI DSS 3.3, last 4 may be displayed unmasked |
| 38 | `card_bin` | `sales.transactions` | data-model.md L146, L1484; webhook-pipeline.md L91; tsp.md L145 | sensitive | AES-256-GCM or HMAC (target; plaintext today) | 7 yr | PCI DSS, GLBA | tsp tools | P0-12; first 6 — issuer fingerprint |
| 39 | `card_exp_month` | `sales.transactions` | data-model.md L147, L1486 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr | PCI DSS (cardholder data) | tsp tools | P0-13; combined with last4 approaches PAN |
| 40 | `card_exp_year` | `sales.transactions` | data-model.md L148, L1487 | sensitive | AES-256-GCM (target; plaintext today) | 7 yr | PCI DSS | tsp tools | P0-13 |
| 41 | `payload` (sales) | `sales.transactions`; `sales.dead_letter_queue` | data-model.md L150, L1490, L1656; tsp.md L142–143; tsp-seal.md L165 | restricted | AES-256-GCM (target; plaintext today) | 7 yr | PCI DSS, GDPR, CCPA | None directly | P0-10/P0-TSP-01; full webhook copy |
| 42 | `parsed_payload` | `sales.evidence_records` | tsp.md L141; tsp-seal.md L165, L197 | restricted | AES-256-GCM (target; plaintext today) | INSERT-only; 7 yr | PCI DSS, GDPR, CCPA | None | P0-TSP-01 — never updated; cryptographic erasure required for GDPR |
| 43 | `raw_payload` | `sales.evidence_records` | tsp.md L140; tsp-seal.md L165 | restricted | AES-256-GCM (target; plaintext today) | INSERT-only; 7 yr | PCI DSS, GDPR, CCPA | `/receipt/by-hash` returns hashes only | P0-TSP-01; verbatim wire bytes |
| 44 | `phone_hash` | `sales.loyalty_accounts` | data-model.md L154, L1617; tsp-parse.md L307; tsp.md L149; webhook-pipeline.md L93 | internal | HMAC-SHA256 keyed (`PHONE_HASH_KEY`) — **implemented** | 7 yr | GDPR (pseudonym; one-way) | tsp tools | Recent fix; plain SHA-256 prohibited |
| 45 | `customer_email` | `ecom.ecom_orders`; `ecom.ecom_subscriptions` | ecom-channel.md L203, L246, L578 | sensitive | AES-256-GCM (target — `ECOM_ENCRYPTION_KEY`) | 7 yr or until GDPR erasure | GDPR, CCPA, CAN-SPAM | `get_ecom_orders`, `get_order_detail`, `get_subscription` (canary-ecom) | Cryptographic erasure path documented (ecom-channel.md L596–621) |
| 46 | `customer_name` | `ecom.ecom_orders` | ecom-channel.md L204, L579 | sensitive | AES-256-GCM (target) | 7 yr or until GDPR erasure | GDPR, CCPA | canary-ecom tools | — |
| 47 | `customer_email_hash` | RaaS chain events; `ecom.ecom_orders` (implicit) | ecom-channel.md L326, L330–334, L583; go-security.md L177 | internal | HMAC-SHA256 keyed (`EMAIL_HASH_KEY`) — **specified** | Forever (immutable chain) | GDPR (pseudonym; one-way; chain payload carries `key_version`) | RaaS chain reads | Recent fix; key rotation envelope in spec |
| 48 | `card_token_ciphertext` | `ecom.ecom_subscriptions` | ecom-channel.md L250, L580, L587 | restricted (PCI) | AES-256-GCM (target; key rotated on card update) | Until card update | PCI DSS (Square card-on-file ref, not raw PAN) | `get_subscription` (excludes ciphertext) | Square is the card vault — Canary stores token only |
| 49 | `credentials_ciphertext` | `ecom.ecom_channels` | ecom-channel.md L181, L581 | sensitive | AES-256-GCM (target) | Until disconnection | OAuth provider TOS, GLBA-adjacent | None | Provider OAuth/API keys |
| 50 | `raw_payload` | `ecom.ecom_webhook_events`; `ecom.ecom_orders.raw_payload` | ecom-channel.md L214, L299, L582 | restricted | Per-event-type classification; redact cardholder data before storage | 90 d (webhook); 7 yr (order) | PCI DSS, GDPR, CCPA | None | "redact BIN, last4, expiry before storage" — implementation gap |
| 51 | `credentials_enc` | `app.pos_tenant_credentials` | pos-adapter-substrate.md L304–342, L368–415; bull.md L53–61 | restricted | AES-256-GCM (key derived from app secret + per-row salt) | Active credential lifetime | OAuth/API provider TOS; GLBA-adjacent | None | "must NEVER appear in query results that feed logs" — strong directive |
| 52 | `metadata.webhook_signature_key` | `app.raas_source_registrations` | raas.md L122, L403 | restricted | AES-256-GCM at rest | Active subscription; old key 30-day replay window | OAuth/Square TOS | None | Spec mandates encryption |
| 53 | `external_merchant_id` | `app.raas_source_registrations` | raas.md L402; go-security.md L278 | sensitive | AES-256-GCM at rest | Same as registration | None direct | Only RaaS service decrypts | Strong directive in go-security.md L280 — never in logs/responses |
| 54 | `payload` (RaaS chain) | `app.raas_events` | raas.md L142, L401; raas.md L420–445 | varies (per event_type) | Per-subject AES-256-GCM key (cryptographic erasure pattern) | 7 yr | GDPR, PCI DSS (when payload is transaction) | None directly | Cryptographic erasure on right-to-deletion |
| 55 | `subject_id` | `app.raas_subject_keys` | raas.md L425 | sensitive | Pseudonymous hash of subject identifier | 7 yr | GDPR (one-way) | None | Tombstoned on erasure |
| 56 | `customer_presence_log` content | `app.customer_presence_log` | store-brain.md L203, L298 | sensitive | AES-256-GCM at rest (target) | 90 d | GDPR, CCPA, biometric (state laws) | `canary-brain` tools | "PII — encrypt at rest, 90-day retention" |
| 57 | `context_json` (session content) | store-brain session table | store-brain.md L184, L299, L302 | sensitive | Cryptographic erasure on right-to-deletion (target) | 90 d (DB); Valkey TTL primary | GDPR Art. 17, CCPA §1798.105 | `get_session`, `check_tool_permission` (canary-brain) | Tombstone replacement on erasure |
| 58 | `brain.session.opened` / `brain.session.closed` chain events | RaaS chain | store-brain.md L301 | sensitive | RaaS subject-key encryption (per raas.md) | 7 yr (chain retention) | GDPR (presence-at-location is sensitive) | RaaS read endpoints | "Establishes a named individual was present at a location at a specific time — evidence-grade" |
| 59 | `tech_phone` / `tech_email` | `app.on_call_rotations` | ops-dashboard.md L141, L209, L216 | sensitive | AES-256-GCM (target — not explicit in spec) | Active rotation | GDPR, CCPA | `get_oncall` — admin role only | "must not be visible to store-level users" |
| 60 | `merchant_label_overrides.label`; `merchant_field_overrides.label_override` | `app.merchant_label_overrides`; `app.merchant_field_overrides` | settings.md L479, L516 | sensitive | AES-256-GCM (target; plaintext today) | Setting lifetime | GDPR, CCPA | settings tools | P0-1 settings.md |
| 61 | `vendors.contact` (JSONB: name, email, phone, account_rep) | `app.vendors` | commercial.md L114, L329 | sensitive | Plaintext (target classification stated; encryption not specified) | 7 yr after `active=false` | GDPR, CCPA, B2B contact rules | commercial tools | Vendor contact PII |
| 62 | `invoice_id` | `app.vendor_deductions` | commercial.md L330 | sensitive | Plaintext | 7 yr | Financial record (IRS) | commercial tools | "Do not log in plaintext at DEBUG" |
| 63 | `override_reason` | `app.return_requests` | returns.md L350 | sensitive (legal) | Plaintext (access restricted by role) | 7 yr (LP evidence) | GDPR, US legal | returns tools | LP / management roles only |
| 64 | `associate_id` | `app.receiving_events` | receiving.md L318 | internal (employee ref) | Employee ID reference; no plaintext PII in this table | 7 yr (financial record) | GDPR, CCPA | receiving tools | "Do not log in plain text" |
| 65 | `order_reference` | `app.inventory_reservations` | inventory-as-a-service.md L544 | varies | Truncate in error messages | None specified | GDPR (may be customer session ID) | iaas tools | "May contain PII" — under-classified |
| 66 | `vendor_entity_id` | `app.hawk_subjects` | data-model.md L1075 | sensitive | Plaintext | 7 yr (LP evidence) | B2B confidentiality | hawk tools | Vendor relationship indicator |
| 67 | `actor_id` (chain events) | RaaS chain events | raas.md L266; ops-dashboard.md L215 | internal | Pseudonymous UUID | 7 yr | GDPR (linkage) | All chain-reading tools | Resolved to entity only in authenticated tool calls |
| 68 | `inscription_hash` / Merkle root | `sales.event_inscriptions`; `sales.inscription_pool`; blockchain L2 | tsp-merkle.md L177–185, L296–306; blockchain-anchor.md L48–88 | public | None — published to public blockchain | Forever (on-chain) | None (hashes only — no PII) | `verify_merkle`, `submit_anchor_batch` (canary-anchor) | **Forever-public** — see Section G |
| 69 | `chain_hash` / `event_hash` / `payload_hash` | `sales.evidence_records`; `app.raas_events`; `ledger.rib_batches` | tsp-seal.md L162–164; raas.md L139–141; ildwac.md L93–95 | public | SHA-256 — not PII | INSERT-only; 7 yr | None | All chain-reading tools; `/receipt/*` | Hashes only — no PII content |
| 70 | `embedding` (semantic vectors) | `growdirect_memory.alx_memories.embedding`; `app.hawk_cards.vector` | data-model.md L1735, L1159 | internal | Plaintext (1024-dim vectors) | None specified | GDPR (if reversible to PII content) | memory_recall, semantic search | `alx_memories.content` may carry context with identifiers |

**Total fields inventoried:** 70 distinct field instances across 24 schemas/tables/locations.

---

## Section B — Regulatory Scope

### GDPR (EU/UK General Data Protection Regulation)

**Applicability trigger.** Canary stores email addresses (Fields 1, 5, 8, 18, 45), phone numbers (6, 9, presence in 44, 47), names (3, 7, 24, 30, 46), physical addresses (12–15), IP addresses (21–23), payment cardholder-adjacent data (31, 38–43, 48), employee data (7–10), and presence-at-location records (56–58). Any of these collected from an EU/UK data subject brings Canary into GDPR scope. The platform's stated US/CA initial market does not exempt the system: SDDs do not exclude EU/UK customers and the architecture has no jurisdictional gating. The DPA-style commitments below are required as soon as the first EU/UK merchant is onboarded — and ideally documented before then.

**Specific obligations and current spec posture.**

| Obligation | Source | Current spec posture |
|---|---|---|
| Lawful basis for processing | GDPR Art. 6 | **Not addressed in any SDD** |
| Data subject access (Art. 15) | GDPR | **Not addressed** — no DSAR endpoint, no consolidated subject view |
| Right to rectification (Art. 16) | GDPR | Partially — `app` schema is mutable; `sales` schema is immutable (compensating INSERTs) |
| Right to erasure (Art. 17) | GDPR | Partially specified — `ecom-channel.md` L596–621 documents cryptographic erasure for ecom orders; `raas.md` L416–445 documents tombstoning for chain events; `store-brain.md` L302 specifies tombstone for session content. **No platform-wide DSAR pipeline.** |
| Right to data portability (Art. 20) | GDPR | **Not addressed** |
| Right to object / automated decision-making (Art. 22) | GDPR | Not addressed — Owl AI personality routing and risk scoring are automated; no opt-out path |
| Privacy by design / DPIA (Art. 25, 35) | GDPR | DPIA required for: ILDWAC five-dimension cost model (employee×device×time triangulation), customer presence detection (`store-brain`), risk scoring on individual employees (`metrics.entity_risk_scores`). **No DPIA on file.** |
| Records of processing (Art. 30) | GDPR | This inventory becomes the substrate; needs translation into Art. 30 register |
| Breach notification within 72h (Art. 33) | GDPR | Not addressed — no incident-response runbook, no DPA contact protocol |
| Data Processing Agreement (Art. 28) | GDPR | **Not addressed** — Canary acts as processor for merchant data; needs DPA template |
| Cross-border transfer mechanism (Ch. V) | GDPR | Single-region architecture today (RDS US); SCCs / adequacy decision required before EU customer onboarding |
| DPO appointment (Art. 37) | GDPR | Not specified — likely required given large-scale processing of employee data |

**Triggering fields (subset):** 1–18, 21–30, 45–47, 56–58, 67. The customer-email-hash (47) is a one-way pseudonym and does not constitute personal data under GDPR Recital 26 *only if* the key is sufficiently protected and rotation history is maintained — both conditions are specified but not yet implemented.

### CCPA / CPRA (California Consumer Privacy Act / Privacy Rights Act)

**Applicability trigger.** California consumers' personal information (broad definition including IP addresses and household identifiers). Same field set as GDPR plus IP addresses (21–23) explicitly enumerated.

| Obligation | Specific |
|---|---|
| Right to know (§1798.110) | Same DSAR gap as GDPR |
| Right to delete (§1798.105) | Same erasure gap; additionally requires confirmation of third-party deletion (Square, NCR — **not addressed**) |
| Right to opt out of sale/sharing (§1798.120, .121) | Canary does not "sell" data to third parties per the SDDs but cross-system data flow to Square/NCR may qualify as "sharing" under CPRA. **Spec is silent.** |
| Limit use of sensitive PI (§1798.121) | CPRA category. Bank account routing (33–34), precise geolocation (15), employee data — all fall in scope. **Not addressed.** |
| Privacy notice and contract terms | Required at collection. SDDs do not specify the merchant-facing privacy notice or whether prospect emails (18) carry consent text. |
| Service provider contract requirement | Canary must execute a CCPA-compliant service-provider addendum with each merchant. **Not in SDD library.** |

### State Breach-Notification Laws (US)

**Applicability trigger.** All 50 states + DC have breach laws. Triggers include: name + SSN/driver's license/financial account number, name + medical/biometric data, sometimes username + password.

| Field combination triggering notification | Where stored |
|---|---|
| `holder_name` + `routing_number` (#32 + #33) | `app.bank_accounts` — plaintext today |
| `employee_name` + email/phone (#7 + #8/#9) | `app.employees` — plaintext today |
| `customer_name` + cardholder-adjacent data (#46 + #38–40) | `ecom.ecom_orders` + `sales.transactions` |
| User credentials (`app.users.email` + Valkey session) | Plaintext email today |

Specific obligations: written breach notification to affected residents and state AGs within 30–90 days depending on state (CA: 45 days for most; NY: shortest reasonable time; MA: 30 days max). Several states (CA, NY, MA) require breach response plan documentation. **Canary has no documented breach plan.**

### PCI DSS (Payment Card Industry Data Security Standard)

**Applicability trigger.** Canary stores cardholder data (PAN-adjacent: BIN, last 4, fingerprint, expiry) and a card-on-file token. This places Canary in **PCI DSS SAQ-D Service Provider** scope at a minimum, though the analysis is nuanced — Square is the card vault for both POS and ecom-channel card-on-file tokens, which **may** allow Canary to claim merchant exemption for the cardholder data itself, but not for the storage of card-fingerprint, BIN, expiry, and last-4 fields.

| Field | DSS Requirement | Status |
|---|---|---|
| `card_bin` (#38) | Req 3.5 — protect stored cardholder data | Plaintext today; P0-12 unfinished |
| `card_exp_month/year` (#39, #40) | Req 3.5 | Plaintext today; P0-13 unfinished |
| `card_last4` (#37) | Req 3.3 carve-out — last 4 may be displayed | Compliant (plaintext acceptable) |
| `card_fingerprint` (#36) | Req 3.4 — pseudonymous tokenization | Compliant per Square docs |
| `card_token_ciphertext` (#48) | Req 3.5 — protect tokens | AES-256-GCM specified, not yet implemented |
| `payload` containing card data (#31, #41–43, #50) | Req 3.5 + Req 3.4 | Plaintext today; P0-10/P0-TSP-01 unfinished |
| HMAC verification on inbound (multiple) | Req 4 — encryption in transit + integrity | Implemented |
| Encryption keys (Req 3.6) | Key management | Spec calls for Secrets Manager; today in env files (P0-3 / P0-11) |
| Audit trail (Req 10) | Logging cardholder data access | Hash-chained `audit_log` exists; P1-1 token decrypt logging unfinished |
| Quarterly vulnerability scan (Req 11.2) | ASV scan | Not addressed |
| Annual penetration test (Req 11.3) | Pen test | Not addressed |
| PCI assessment | Self-assessment or QSA | **Not addressed in any SDD** |

A PCI DSS Report on Compliance (RoC) or Self-Assessment Questionnaire D (SAQ-D Service Provider) is required before live cardholder-adjacent data is processed. A QSA scoping engagement is the right next step.

### GLBA (Gramm-Leach-Bliley Act) and Reg P

**Applicability trigger.** Bank account information (Fields 32–35) collected from merchants. GLBA applies because Canary is a service provider to merchants who are themselves not financial institutions, but Canary collects "nonpublic personal information" (NPI) about consumers (the merchant's bank account holder). The Safeguards Rule (16 CFR Part 314) imposes specific security requirements on this data.

| Obligation | Status |
|---|---|
| Written information security program | Not in SDD library — needed |
| Access controls | RBAC specified; merchant-scoped access; spec is sound |
| Encryption of NPI in transit and at rest | Required at rest; **plaintext today (P0-5)** |
| Multi-factor auth for any individual accessing customer info | Identity spec mentions MFA only by absence — JWT only |
| Vendor risk assessment | Not addressed |
| Incident response plan | Not addressed |
| Annual program reassessment | Not addressed |

### CAN-SPAM (US) and CASL (Canada) — Marketing Email/SMS

**Triggering fields:** `interest_signups.email` (#18), `notification_log.recipient` (#19), `notif_phone` (#6), `customer_email` (#45), `customer_email_hash` (#47), `billing_email` (#5).

| Obligation | Status |
|---|---|
| Express consent before sending | Not addressed in any SDD — `interest_signups` is captured at "join" with no consent text spec |
| Sender identification | Not addressed |
| Opt-out mechanism in every message | Not addressed |
| Honor opt-outs within 10 days (CAN-SPAM) / immediately (CASL) | Not addressed |
| CASL: explicit, not implied, consent | Not addressed (relevant if Canada market opens) |

### TCPA (US) — SMS/Calling

**Triggering fields:** `notif_phone` (#6), `loyalty.phone_hash` (#44 — original phone in webhook payload), `tech_phone` (#59).

| Obligation | Status |
|---|---|
| Prior express written consent for marketing SMS | Not addressed |
| Internal Do-Not-Call list | Not addressed |
| Caller ID / sender identification | Not addressed |

### US/CA Federal and State Employment Law

**Triggering fields:** All employee-related fields (#7–10), Fox/Hawk subject investigation records (#24–30), employee timecard data (`sales.employee_timecards`), risk scores (`metrics.entity_risk_scores`).

| Concern | Status |
|---|---|
| Employee privacy / surveillance disclosure (CA Lab Code §435; IL BIPA-adjacent for biometric presence) | `store-brain` presence detection is not flagged |
| Adverse action triggered by automated risk score (FCRA / state equivalents) | `metrics.entity_risk_scores` produces scores used in alerts; no human-in-the-loop gate specified for adverse action |
| Investigation records (HR / employment defense) | 7-yr retention is reasonable; access controls specified; encryption gap is the issue |
| Off-clock monitoring (e.g., loyalty correlations) | ILDWAC five-dimension model could correlate employee activity post-shift; not flagged for DPIA |

### SOX (Sarbanes-Oxley) — Financial Records

Canary is not itself publicly held, but merchants serving public-co retailers may need SOX-grade financial record integrity. Hash-chained evidence and 7-year retention windows (specified in `pricing.md`, `receiving.md`, `three-way-match.md`, `commercial.md`, `inventory-as-a-service.md`, `tsp-detect.md`) align with SOX expectations. The append-only / immutable triggers in the `sales` schema (`data-model.md` L1462) provide tamper-evidence acceptable to SOX auditors. **Posture is good; no specific gap.**

### Patent #63/991,596 — Reference Only

Patent application covers hash-before-parse, chain hash, Merkle inscription, ILDWAC five-dimension cost model. Not a regulatory regime; noted here because patent-scoped fields (event_hash, chain_hash, Merkle root) are public-classified and forever-public via blockchain anchor.

---

## Section C — Gap Analysis

### M.1 Encryption Gaps — Fields Classified Sensitive/Restricted with No At-Rest Encryption Today

The SDD library is honest about this: it documents the gap as a P0 punch list inside `data-model.md` L2042–2057 and replicates it in service-level SDDs. The gaps remain open as of the spec's `updated: 2026-04-29` date.

| Gap | Source line | Affected fields |
|---|---|---|
| User identity plaintext | data-model.md L2045 | `users.email`, `users.username`, `users.display_name` (#1–3) |
| Employee PII plaintext | data-model.md L2046; identity.md L575 | `employees.employee_name`, `employees.email`, `employees.phone` (#7–9) |
| Org billing email plaintext | data-model.md L2047 | `organizations.billing_email` (#5) |
| Notification phone plaintext | data-model.md L2048 | `merchant_settings.notif_phone` (#6) |
| Bank account PII plaintext | data-model.md L2049 | `bank_accounts.holder_name`, `routing_number`, `secondary_routing_number` (#32–34) |
| Notification recipient plaintext | data-model.md L2050; alert.md L583 | `notification_log.recipient` (#19) |
| Fox subject names plaintext | data-model.md L2051; fox.md | `fox_subjects.name`, `role_in_case` (#24, #26) |
| Interest signup email plaintext | data-model.md L2052 | `interest_signups.email` (#18) |
| Location address plaintext | data-model.md L2053 | `locations.address_line1/2`, `coordinates`, `postal_code` (#12–15) |
| Webhook payload raw PII | data-model.md L2054; tsp-seal.md L197; webhook-pipeline.md L88; multi-pos-architecture-proof.md L65–66 | `webhook_events.payload`, `transactions.payload`, `evidence_records.raw_payload`, `dead_letter_queue.payload`, `evidence_records.parsed_payload` (#31, #41–43, #50) |
| Encryption keys in env files | data-model.md L2055; identity.md L531 | All encrypted fields |
| Card BIN plaintext | data-model.md L2056 | `transactions.card_bin` (#38) |
| Card expiration plaintext | data-model.md L2057 | `transactions.card_exp_month/year` (#39, #40) |
| dim_employee plaintext name | data-model.md L2058 | `metrics.dim_employee.employee_name` (linked to #7) |
| Alert details plaintext | alert.md L585 | `alerts.details` (#20) |
| Settings label overrides plaintext | settings.md L479 | `merchant_label_overrides.label`, `merchant_field_overrides.label_override` (#60) |

### M.2 Retention Windows Not Declared

Several services state retention windows; many do not. Where retention is silent, GDPR storage-limitation principle defaults to "no longer than necessary" which is unenforceable without a stated window.

| Domain | Field / Table | Stated retention | Gap |
|---|---|---|---|
| `app.audit_log` | data-model.md L2065 | 24 mo (proposed) | Not implemented |
| `app.notification_log` | alert.md L590 | 12 mo (proposed) | Not implemented |
| `app.alerts` | alert.md L590; chirp.md L1264 | 24 mo (proposed) | Not implemented |
| `sales.dead_letter_queue` | data-model.md L2065 | 90 d (proposed) | Not implemented |
| `app.webhook_events` | data-model.md L2065 | 12 mo (proposed) | Not implemented |
| `growdirect_memory.alx_memories` | data-model.md (memory) | None specified | Gap — content may carry PII |
| `growdirect_memory.alx_sessions` | data-model.md | None specified | Gap |
| `app.audit_log` access events for tokens | identity.md L597 | None specified | Gap |
| `metrics.entity_risk_scores` / `risk_score_history` | data-model.md | None specified | GDPR / employment-law concern — automated adverse decision history with no retention |
| `app.namespace_aliases` | data-model.md | None specified | Gap |
| `app.vault_memories` | data-model.md | INSERT-only; no purge | Gap if PII content present |
| `ecom.ecom_subscriptions` (after cancellation) | ecom-channel.md L632 | "Until cancellation + 3 yrs" | Stated, OK |
| `app.interest_signups` | data-model.md | Append-only | Gap — pre-launch list grows forever |
| `app.fox_evidence_access_log` | data-model.md | INSERT-only | OK (LP evidence) |
| `app.hawk_*` tables | data-model.md | None specified | Gap — investigation records |

### M.3 MCP Tools That Expose PII Without the SDD Calling It Out

| Tool | Server | Fields exposed | SDD coverage |
|---|---|---|---|
| `list_employees`, `get_employee` | canary-identity | name, email, phone, risk_score | identity.md L151–152 — **mentions PII access in table** |
| `top-risks`, `drilldown` (analytics REST) | canary-analytics | `employee_name` decrypted at presentation | analytics.md L71 — **calls out "PII access logged"; logging gap noted** |
| `get_oncall` | canary-ops | tech_phone, tech_email | ops-dashboard.md L209, L216 — **gated to admin role** |
| `get_session`, `check_tool_permission` | canary-brain | `context_json` (loyalty / purchase history / hold details) | store-brain.md L299 — **flagged sensitive** |
| `get_ecom_orders`, `get_order_detail` | canary-ecom | `customer_email`, `customer_name`, line items | ecom-channel.md L483 — gated by JWT merchant scope |
| Owl `/owl/tools/<name>` generic invocation | canary-owl | merchant context with employee-attributed alert summaries | owl.md L90 — flagged sensitive but **audit logging is P1 gap (line 230, L568)** |
| Memory bus `memory_recall`, `context_assemble` | memory-bus | `alx_memories.content` may contain identifiers | data-model.md L171 — content classified internal but **no PII review specified** |
| Fox tools (`get_case`, `add_evidence`, etc.) | canary-fox | subject names, narrative, IP addresses | fox.md L243 — IP must be hashed; subject names target encryption (gap) |
| `get_alert` (canary-alert) | canary-alert | `details` JSON, recipient | alert.md L585 — flagged P0 |
| `get_snapshot`, `record_adjustment` (canary-inventory) | canary-inventory | `order_reference` may contain PII | inventory-as-a-service.md L544 — flagged but no encryption mandate |

**Most MCP tools have JWT + merchant-scope gating specified.** The systemic gap is access auditing: P1-1 in `identity.md` (line 571), P1 in `analytics.md` (line 71), P1 in `owl.md` (line 230). Today, MCP tool invocations that expose PII are not consistently logged.

### M.4 Cross-SDD Inconsistencies

See Section D for the full table.

### M.5 Missing Cryptographic-Erasure Paths for GDPR Right to Deletion

| Domain | Erasure path stated | Gap |
|---|---|---|
| `ecom.ecom_orders`, `ecom.ecom_subscriptions` | ecom-channel.md L596–621 | **OK** — DEK-overwrite pattern documented |
| RaaS `raas_events` | raas.md L416–445 | **OK** — per-subject key tombstoning |
| store-brain `context_json` | store-brain.md L302 | **OK** — tombstone replacement |
| `app.users` / `app.employees` | identity.md | **No erasure path** — plaintext today; even after encryption, no per-user key partitioning |
| `app.fox_subjects` | fox.md | **No erasure path** — and Fox is INSERT-only; deletion conflicts with append-only invariant |
| `app.notification_log` | — | **No erasure path** |
| `app.audit_log` | data-model.md (append-only) | **Conflict** — append-only forbids delete, but GDPR Art. 17 requires it. Cryptographic-erasure-via-per-subject-key not specified |
| `sales.transactions` (immutable) | — | **Conflict** — same as audit_log; spec is silent on resolution |
| `sales.evidence_records` (raw payload contains PII) | tsp-seal.md | **Conflict** — append-only; no per-subject DEK pattern |
| `growdirect_memory.alx_memories` | — | **No erasure path** |
| Blockchain anchor (Merkle root on L2) | blockchain-anchor.md L54 | "no PII enters the blockchain" — true for raw PII; but `customer_email_hash` rotation across the chain boundary is partially addressed (ecom-channel.md L334) |

The append-only / right-to-deletion conflict is the platform's structural compliance challenge. **The spec partially addresses it** (per-subject keys in raas.md / ecom-channel.md), but does not provide a unified pattern. Any merchant onboarding an EU/UK consumer who later requests erasure will hit unspecified behavior in audit_log, transactions, evidence_records, and webhook_events.

### M.6 Missing DPA-Style Fields and Process

| Concern | Status |
|---|---|
| Data Processing Agreement (DPA) template | Not in repo |
| Subprocessor list (Square, NCR, AWS RDS, ElastiCache, Ollama, blockchain anchor) | Not declared |
| Subprocessor change notification process | Not specified |
| Data residency commitments | Single-region (US RDS) implied but not declared |
| Standard Contractual Clauses (SCCs) for EU transfer | Not specified |
| Audit rights (customer right to audit Canary) | Not specified |
| Breach-notification SLA (to merchant, then merchant to consumer) | Not specified |

### M.7 Other Concrete Gaps

| Gap | Source |
|---|---|
| `employees.phone` in identity.md (L71) but not in `data-model.md` PII map | identity.md L71 vs. data-model.md L79–81 |
| `ip_address` retention 24 mo (audit_log) vs. 90 d (ingestion_log) — no aligned platform retention schedule | data-model.md L2065; tsp-parse.md L428 |
| Risk score history retention silent; FCRA / employment-law adverse-decision implications | data-model.md L1707 |
| `alx_memories.content` PII review never performed | data-model.md L171, L2081 (P2) |
| Memory bus `growdirect_memory` has no tenant boundary documented (cross-merchant memory possible) | data-model.md L1880–1893 — boundary stated in prose; not enforced by RLS |
| RLS not implemented anywhere; tenant isolation is application-layer | data-model.md L1787 |
| Token decrypt audit log: stated as P1 in three places, not implemented | data-model.md L500, L2066; identity.md L464 |
| Per-agent API key scoping: P0-4 in identity.md (L563) — single static `CANARY_MCP_API_KEY` grants admin to all merchants | identity.md L563 |
| Production JWT validation: P0-1 in identity.md — RS256 against IdP not implemented | identity.md L549 |
| Field-capture / store-brain biometric presence detection not flagged for state biometric law (BIPA, CA Lab Code) | store-brain.md L203, L298 |
| ILDWAC `mcp_tool_call` column persistently records which agent action authorized which cost event — agent-attribution PII for any agent backed by a human user | data-model.md L1959, L1989; ildwac.md L188 |

---

## Section D — Cross-SDD Consistency Check

| Field | Inconsistency | Affected SDDs | Drift |
|---|---|---|---|
| `users.email` | data-model.md classifies sensitive; identity.md says sensitive (L59) — **consistent** | data-model.md, identity.md | None |
| `users.username` | data-model.md sensitive (L77); identity.md internal (L60) | data-model.md vs. identity.md | **Drift — username is derived from email and same risk profile; should be sensitive** |
| `users.display_name` | data-model.md sensitive (L78); identity.md internal (L61) | data-model.md vs. identity.md | **Drift — same as above** |
| `employees.phone` | identity.md says sensitive (L71); data-model.md does not list it | data-model.md vs. identity.md | **Drift — data-model.md PII map missing `phone`** |
| `employees.name` | identity.md (L72) "internal — masked when `show_employee_names=false`"; data-model.md L79 "sensitive — P0 encrypt" | data-model.md vs. identity.md | **Classification drift — sensitive vs. internal** |
| `audit_log.ip_address` | data-model.md sensitive, P1 hash (L98); fox.md says "must be one-way hash" (L243) | data-model.md vs. fox.md | **Severity drift — fox treats as mandatory; data-model treats as P1 (proposed)** |
| `card_fingerprint` | data-model.md classifies internal (L144); webhook-pipeline.md classifies sensitive (L89); tsp.md classifies sensitive (L144) | data-model.md vs. tsp.md, webhook-pipeline.md | **Classification drift — internal vs. sensitive. The fingerprint is unique-per-card and linkable across transactions; sensitive is the correct call** |
| `card_last4` | data-model.md internal (L145); webhook-pipeline.md internal (L90) | All consistent | None |
| `card_exp_month/year` | data-model.md sensitive (L147–148); tsp.md sensitive (L146); webhook-pipeline.md internal (L92) | data-model.md vs. webhook-pipeline.md | **Classification drift — webhook-pipeline.md says internal, data-model.md says sensitive. The combination of last4 + expiry approaches PAN reconstruction; sensitive is correct** |
| `phone_hash` | data-model.md internal (L154 — "HMAC-SHA256 keyed; preserves dedup"); tsp-parse.md sensitive (L394) | data-model.md vs. tsp-parse.md | **Classification drift — data-model treats the hash as internal (irreversible); tsp-parse treats the underlying field as sensitive. Both are correct from different viewpoints — the hashed value is internal, but the parser handling is sensitive (the plaintext flows through it). Reconcile language.** |
| `customer_email_hash` | ecom-channel.md L583 internal (HMAC-SHA256 keyed); go-security.md L177 keyed pii_hash | All consistent | None |
| `webhook_events.payload` | data-model.md restricted (L120, L1195); webhook-pipeline.md restricted (L88); tsp.md restricted (L142). All P0 plaintext today | All consistent | None |
| `bank_accounts` PII fields | data-model.md sensitive (L126–129); not mentioned in identity.md | data-model.md only | None — bank_accounts is in identity domain but identity.md doesn't enumerate |
| `ip_address` retention | audit_log: 24 mo (data-model.md L2065). ingestion_log: 90 d (tsp-parse.md L428). fox_evidence_access_log: 7 yr (LP) | All three SDDs | **Three different retention windows for the same data type** |
| Encryption key class | All SDDs reference `CANARY_ENCRYPTION_KEY` (32 bytes). ecom-channel.md introduces `ECOM_ENCRYPTION_KEY` (also 32 bytes) as a peer | ecom-channel.md L507 vs. go-security.md | Consistent in shape; **separate keys for separate domains is good practice (blast-radius isolation)**, but the relationship between the two keys is not documented |
| JWT secret | go-security.md `JWT_SECRET` ≥ 32 bytes (L111); architecture.md and identity.md reference `CANARY_DEV_JWT_SECRET` for dev — different keys | go-security.md vs. identity.md | Acceptable; dev key is documented as not-for-prod (P2-4 identity.md L613) |

### Section D resolution log (2026-05-01)

Closing pass before SDD handoff to receiving team (GRO-720). Each drift point above is addressed below; the canonical classification picked is reflected in the live SDD body text.

| Drift point | Resolution | Canonical classification | Edits applied |
|---|---|---|---|
| `users.username` | Resolved — `data-model.md` was correct. | sensitive | `identity.md` row updated from internal → sensitive with note that username is derived from email and shares the same risk profile (P0 encrypt). |
| `users.display_name` | Resolved — `data-model.md` was correct. | sensitive | `identity.md` row updated from internal → sensitive (P0 encrypt). |
| `employees.phone` | Resolved — field added to authoritative PII map. | sensitive | `data-model.md` Identity Domain table now includes `employees.phone` (P0 encryption target) alongside `email` and `employee_name`. |
| `employees.name` | Resolved — `data-model.md` was correct. | sensitive | `identity.md` row updated from internal → sensitive; clarifying note added that `show_employee_names=false` is a runtime display mask, not a classification downgrade. |
| `card_fingerprint` | Resolved — exception case: `data-model.md` was wrong, `tsp.md` and `webhook-pipeline.md` were correct. Per Section D analysis, the fingerprint is unique-per-card and linkable across transactions, so sensitive is the right call. | sensitive | `data-model.md` updated in **two** rows (`card_profiles` L186, `transactions` L195) from internal → sensitive with rationale note on linkability. |
| `card_exp_month` / `card_exp_year` | Resolved — `data-model.md` and `tsp.md` were correct. | sensitive | `webhook-pipeline.md` row updated from internal → sensitive (P0 encrypt) with note that last4 + expiry approaches PAN reconstruction. |
| `phone_hash` value-vs-handler language | Resolved — both classifications are correct from different viewpoints; reconciled the language only. The stored hash value is internal (irreversible against keyed input space); the parser handler that processes plaintext is sensitive. | value: internal · handler: sensitive | `data-model.md` `loyalty_accounts.phone_hash` note appended with value-vs-handler clarifier; `tsp-parse.md` PII handling row for `phone` (loyalty) appended with the symmetric clarifier. Cross-reference between the two rows is now explicit. |

**Deferred to receiving team** — these need a real decision, not a text fix; flagged here so they don't slip through handoff:

- `audit_log.ip_address` severity: `data-model.md` treats as P1 (proposed); `fox.md` says "must be one-way hash" (mandatory). Receiving team to pick the canonical posture and update both SDDs accordingly.
- `ip_address` retention windows: three different windows for the same data type — `audit_log` 24 mo (`data-model.md` L2065), `ingestion_log` 90 d (`tsp-parse.md` L428), `fox_evidence_access_log` 7 yr (LP). Receiving team to either justify each window with its retention rationale or unify them.

---

## Section E — MCP Tool Exposure Map

The Canary platform exposes 15+ MCP servers. The table below lists every MCP tool that returns or operates on PII-bearing data, the fields it touches, classification, and the role required to call it. Tools that operate purely on aggregates, IDs, or hashes (e.g., `verify_merkle`, `get_anchor_status`) are omitted.

| MCP Server | Tool | Fields exposed | Classification | Role required |
|---|---|---|---|---|
| canary-identity | `get_merchant` | merchant_name | internal | session JWT |
| canary-identity | `get_settings` | notif_phone, notif prefs | sensitive | session JWT |
| canary-identity | `list_employees` | employee_name, email, phone | sensitive | session JWT (tenant-scoped) |
| canary-identity | `get_employee` | employee_name, email, phone, risk_score | sensitive | session JWT |
| canary-identity | `list_locations` | address fields | sensitive | session JWT |
| canary-identity | `get_location` | address fields | sensitive | session JWT |
| canary-tsp | (admin tools — 7 ops) | retry / replay / disable webhook subscriptions | restricted (operational) | admin |
| canary-fox | `get_case`, `list_cases` | subject names, case narrative | sensitive | owner / lp_officer / admin |
| canary-fox | `add_evidence`, `get_evidence` | file_path, file_hash, narrative | sensitive | lp_officer / admin |
| canary-hawk (`canary-hawk` 9 tools, hawk-case-management.md L466) | `get_case`, `generate_card`, etc. | hawk subject names, incident details | sensitive | lp_officer / admin |
| canary-alert | `get_alert`, `acknowledge_alert` | recipient, details JSON | sensitive | owner / manager / admin |
| canary-analytics | `top-risks`, `drilldown` | employee_name (decrypted at edge) | sensitive | session JWT |
| canary-owl | `/owl/tools/<name>` generic | merchant context, employee-attributed summaries | sensitive | session JWT |
| canary-raas | `append_event`, `receipt_hash`, `verify_chain`, etc. (9 tools) | event payload (varies by type — may include cardholder data) | varies | api_service / agent |
| canary-ecom | `get_ecom_orders`, `get_order_detail` | customer_email, customer_name, line items | sensitive | session JWT (merchant-scoped) |
| canary-ecom | `get_subscription` | customer_email, charge history (no card token) | sensitive | session JWT |
| canary-ecom | `pause_subscription`, `resume_subscription` | subscription state mutations | sensitive | owner / admin |
| canary-inventory | `get_snapshot`, `record_adjustment` | order_reference (may carry PII) | varies | session JWT + role |
| canary-pricing | `set_price`, `get_price_history` | price-only — no direct PII | internal | manager / admin |
| canary-ildwac | `submit_packet`, `get_wac` | item × location × device × MCP × port WAC | internal (financial) | api_service |
| canary-otb (l402-otb) | OTB controls | reference IDs only | operational | manager / admin |
| canary-anchor | `submit_anchor_batch`, `get_anchor_analytics` | hashes only | public | api_service |
| canary-devices | `check_device_sla`, `list_breaches`, `get_contract_events` | device IDs and SLA events; no direct PII | internal | admin |
| canary-ops | `get_active_alerts`, `get_oncall` (admin-only) | tech_phone, tech_email | sensitive | admin |
| canary-brain | `get_session`, `presence_detected`, `check_tool_permission`, etc. (9 tools) | context_json (loyalty / purchase / hold details), customer_id | sensitive | session-context-bound |
| canary-field-capture | (6 tools) | semantic field mapping — no direct PII | internal | api_service / admin |
| canary-sni (store-network-integrity) | (6 tools) | cross-location anomalies — no direct PII | internal | admin |
| canary-commercial | (commercial tools) | vendor.contact (name, email, phone) | sensitive | api_service / manager |
| canary-item | (9 tools) | item master — no direct PII | internal | session JWT |
| canary-receiving | (8 tools) | associate_id, vendor_id | internal | manager / inventory_manager |
| canary-returns | (returns tools) | override_reason (manager only) | sensitive | lp_officer / manager |
| memory-bus | `memory_recall`, `context_assemble`, `domain_context`, `memory_search` | alx_memories.content (may carry context with identifiers) | internal | api_service / agent |

**Tenant scope.** Every JWT-gated tool validates `merchant_id` against the JWT-bound merchant (identity.md L495, ecom-channel.md L483, etc.). Cross-merchant calls return 403. **However**: the `CANARY_MCP_API_KEY` API-key bypass (identity.md P0-4, L563) grants admin access to all merchants and is in scope for production. **This is the highest-priority remediation** in the MCP exposure surface — see Section H.

---

## Section F — Retention and Deletion Posture

| Data category | Stated retention | Deletion procedure | Right-to-deletion handling | Append-only conflict |
|---|---|---|---|---|
| OAuth tokens (`square_oauth_tokens`) | Token lifetime; revoked rows deleted | Hard delete on `/oauth/disconnect` | N/A (merchant credential, not consumer PII) | None |
| User identity (`users`, `user_roles`) | Active accounts; deactivated +12 mo proposed | Soft-delete via `db_status = 'archived'` | **Not specified** — no per-user-key cryptographic erasure | None today (mutable); becomes append-only conflict if audit_log retains user references |
| Employee data (`employees`) | 7 yr typical (employment record); not specified in spec | Soft-delete | **Not specified** | Conflict — alerts and Fox cases reference employees forever |
| Customer presence (`customer_presence_log`, store-brain session content) | 90 d (DB); Valkey TTL primary | Tombstone replacement of `context_json` on right-to-deletion | **OK** — store-brain.md L302 documents | RaaS chain events (`brain.session.opened/closed`) are forever |
| Ecom orders (`ecom_orders`) | 7 yr (financial) or until GDPR erasure | Cryptographic erasure (DEK overwrite) | **OK** — ecom-channel.md L596–621 | Order row remains; PII columns nulled. Chain event remains via hash only |
| Ecom subscriptions (`ecom_subscriptions`) | Until cancellation + 3 yr | Same as orders + card token destruction | **OK** | None |
| RaaS events (`raas_events`) | 7 yr | Per-subject key tombstoning (key destroyed) | **OK** — raas.md L416–445 | Append-only is the design; cryptographic erasure preserves chain integrity |
| Audit log (`app.audit_log`) | 24 mo proposed | None today; would archive to cold storage | **Conflict** — append-only + hash chain forbids row delete or update. **No documented erasure path.** | Hard conflict; needs per-subject-key encryption pattern |
| Webhook events (`webhook_events`) | 12 mo proposed | None today | **Conflict** — payload contains PII; append-only | Hard conflict |
| Sales `transactions` and `evidence_records` | 7 yr (financial) | None today | **Conflict** — immutable trigger; payload contains PII | Hard conflict — see M.5 |
| Dead letter queue (`dead_letter_queue`) | 90 d proposed | Time-based purge | OK (purge handles erasure incidentally if within 90 d) | Bounded by retention; if request comes in <90 d, must crypto-erase |
| Notification log (`notification_log`) | 12 mo proposed | None today | Conflict — recipient PII; append-only | Hard conflict |
| Fox / Hawk cases and evidence | 7 yr (LP) | INSERT-only; no delete path | **Conflict** — investigation records vs. consumer right-to-erasure | Conflict; arguably overridden by legitimate-interest + legal-hold under GDPR Art. 17(3)(e) |
| Memory bus (`alx_memories`, `alx_sessions`) | None specified | None | **Not specified** | Implicit conflict if content carries identifiers |
| Risk scoring history (`metrics.entity_risk_scores`, `risk_score_history`) | None specified | None | **Not specified** | FCRA / employment-law adverse-decision retention — needs explicit window |
| ILDWAC chain (`ldger.ilwac_*`, `ildwac_chain`) | 7 yr | None | Subject-key pattern not implemented for ILDWAC; chain hash provides no PII so erasure unneeded directly | Append-only; agent-attribution (`mcp_tool_call`) is the indirect concern |
| Blockchain L2 anchor | Forever (on-chain) | None possible | OK only because hashes only — no PII on-chain | Forever-public; see Section G |

**The append-only / right-to-deletion conflict is the platform's single largest unresolved compliance question.** The pattern that resolves it (per-subject AES-256-GCM key, key destruction on erasure request) is documented for `raas_events` and `ecom_orders`. It is **not** documented for `audit_log`, `webhook_events`, `evidence_records`, `transactions`, `notification_log`, or any sales schema table. Implementing the pattern uniformly across these tables is the load-bearing P0 work for GDPR readiness.

---

## Section G — Data Flow and Cross-Border

### G.1 Data Flow Map

```
External POS / Channel
   │
   ├── Square Webhooks (HMAC-verified)              ─┐
   ├── NCR Counterpoint (poll + API key)             │
   └── Square Online / future ecom adapters          │
                                                     ▼
                                          [webhook-pipeline / tsp]
                                                     │
                                                     │ HMAC verify → hash-before-parse →
                                                     │ Valkey stream `canary:events`
                                                     │
                                                     ▼
                                          [Stage 1 Seal]  → sales.evidence_records (raw_payload, restricted)
                                          [Stage 2 Parse] → sales.transactions, transaction_tenders, loyalty_accounts (PII fields)
                                                          → sales.dead_letter_queue (failures, restricted)
                                          [Stage 3 Merkle] → sales.event_inscriptions, inscription_pool
                                          [Stage 4 Detect] → app.alerts (sensitive details)

                                                     │
                                                     ▼
                                                    raas
                                                     │
                                                     │ chain hash + per-subject encrypted payload
                                                     ▼
                                                  raas_events
                                                     │
                                                     ▼
                                          blockchain-anchor (Bitcoin L2 / OP_RETURN)
                                                     │
                                                     ▼
                                                Public ledger (forever)
                                                — Merkle roots only —
                                                — no PII —

[parallel flow]
ecom-channel  ─→ ecom_orders, ecom_subscriptions (customer_email, customer_name, card_token)
              ─→ raas append (ecom.order.placed with customer_email_hash)

[reads]
owl       ←── reads alerts, employee names (PII), merchant context for AI inference
analytics ←── reads metrics, decrypts employee_name at presentation
fox/hawk  ←── reads alerts; subject names; investigation narrative
ops-dash  ←── reads device events, MCP health; on-call PII (admin only)
store-brn ←── reads customer presence, session content (90-d retention)

[memory]
memory bus  ←── separate database `growdirect_memory`
            ←── alx_memories.content may carry context with identifiers
```

### G.2 Cross-Tenant Flows (none should exist)

| Check | Status |
|---|---|
| RDS tenant isolation | Application-layer `WHERE merchant_id = $1` only; PostgreSQL RLS not implemented (data-model.md L1787) |
| MCP tools cross-merchant | Most tools enforce JWT merchant scope; `CANARY_MCP_API_KEY` admin bypass is a P0 hole |
| Memory bus tenant boundary | `growdirect_memory` is a single database; **no per-merchant scoping documented**. Risk: agent memory could leak context across merchants |
| Cross-merchant alert correlation | SNI (store-network-integrity.md) is intra-merchant only — confirmed |

### G.3 Cross-Region

The architecture is single-region (US). RDS PostgreSQL 17 with Multi-AZ (data-model.md L1874) implies same-region failover, not cross-region replication. **No SDD references multi-region or EU-region deployment.** Onboarding an EU/UK merchant requires either (a) EU-region deployment with data residency commitment in the DPA, or (b) Standard Contractual Clauses for the US transfer plus a Transfer Impact Assessment.

### G.4 Forever-Public Data

The blockchain L2 anchor (blockchain-anchor.md) publishes Merkle roots of batched event hashes to Bitcoin via OP_RETURN. **No PII enters the blockchain** — only 32-byte SHA-256 Merkle roots (blockchain-anchor.md L54). **This claim is correct as specified**: the on-chain payload is hashes only. However:

- `customer_email_hash` (HMAC-SHA256 keyed) is included in RaaS chain events that are themselves anchored. Because `EMAIL_HASH_KEY` is the keyed input, the hash is reversible only to a holder of the key. **If the key is leaked, all customer emails for that key version are recoverable from the chain.** Key rotation specified in ecom-channel.md L334 mitigates forward exposure but not historical exposure.
- `phone_hash` (HMAC-SHA256 with `PHONE_HASH_KEY`) — same reasoning.
- `subject_id` in `raas_subject_keys` (raas.md L425) is a "pseudonymous hash" — same concern if the hash key leaks.
- ILDWAC `mcp_tool_call` column persistently records which agent action authorized which cost event. Where the agent identity is bound to a human user, this is forever-public agent-attribution data. Patent #63/991,596 is referenced for the model; the privacy implication is not.

The architecture's tamper-evidence guarantee depends on PII never reaching the chain in the clear. **The keyed-hash design holds that guarantee under the assumption of key confidentiality.** That assumption needs an explicit threat model in the DPA.

---

## Section H — Recommendations

### P0 — Must address before production

| # | Recommendation | Affected SDD(s) | Why |
|---|---|---|---|
| H.P0.1 | **Implement field-level AES-256-GCM encryption for all sensitive fields enumerated in Section A items 1–9, 12–15, 18–20, 24–30, 32–34, 38–40, 60.** This is the consolidated P0 punch list from data-model.md, identity.md, alert.md, fox.md, settings.md. | data-model.md L2042–2058; identity.md L555–567; alert.md L583–585; settings.md L479; fox.md | Without it, the platform is not GDPR / CCPA / state-breach-law / GLBA / PCI compliant. |
| H.P0.2 | **Move all encryption and HMAC keys to Secrets Manager** (`CANARY_ENCRYPTION_KEY`, `JWT_SECRET`, `PHONE_HASH_KEY`, `EMAIL_HASH_KEY`, `ECOM_ENCRYPTION_KEY`, OAuth client secrets, webhook signing keys, MCP API keys). | identity.md L531; data-model.md L2055; go-security.md L264–268 | Env-file storage is one accidental commit from full key compromise. |
| H.P0.3 | **Replace static `CANARY_MCP_API_KEY` with per-agent scoped keys** stored in DB with `created_at`/`last_used_at`/`revoked_at`. Log all API-key authentications to `audit_log`. | identity.md P0-4 (L563); architecture.md | Single key = full-platform admin to all merchants; no audit trail; no revocation. |
| H.P0.4 | **Implement production JWT validation (RS256 against IdP JWKS).** Identity P0-1 (line 549). | identity.md L549 | Production mode currently 401s all bearer tokens; API access blocked or routed to the API-key bypass (H.P0.3). |
| H.P0.5 | **Redact or encrypt PII in webhook / transaction / DLQ payloads before persistence.** Implement at TSP Stage 1 (Seal) per architecture.md P0-2 (L647). | tsp-seal.md L197; webhook-pipeline.md L88; multi-pos-architecture-proof.md L65–66; data-model.md L2054 | Raw payloads in `evidence_records.raw_payload` and `transactions.payload` carry full PII forever and resist GDPR erasure. |
| H.P0.6 | **Resolve the append-only / right-to-deletion conflict for `audit_log`, `webhook_events`, `evidence_records`, `transactions`, `notification_log` by implementing the per-subject AES-256-GCM key pattern documented in `raas.md` L420–445 across all five tables.** | data-model.md (cross-cutting); raas.md L416–445 | Without this, the platform cannot honor a GDPR Art. 17 request. The pattern exists in spec; it needs to be applied uniformly. |
| H.P0.7 | **Document and execute a PCI DSS scoping engagement.** The platform is in SAQ-D Service Provider scope at minimum; cardholder-adjacent data (BIN, expiry, full-payload) is stored. A QSA engagement is the gating step for production. | data-model.md L2056–2057; ecom-channel.md L585–594 | No SDD currently states the platform's PCI scope or compliance plan. |
| H.P0.8 | **Author a DPA template, subprocessor list, breach-response runbook, and incident-response plan.** None exists in the SDD library today. | New artifacts; reference existing security findings | GDPR / GLBA / state breach laws all require these. |
| H.P0.9 | **Reconcile cross-SDD classification drift** (Section D) — ratify a single answer for `username`/`display_name`, `employees.phone`, `employees.name`, `card_fingerprint`, `card_exp_*`, `phone_hash` classification language, `audit_log.ip_address` severity. data-model.md is the authority anchor; update other SDDs to match. | All SDDs in Section D | Classification drift is an audit finding. Each driftpoint becomes a question from the assessor. |
| H.P0.10 | **Implement audit logging for every PII-bearing MCP tool invocation** (per identity.md P1-1 at L571, owl.md P1 at L230, analytics.md P1 at L71). The current state — spec says do it, no service does it — is worse than not specifying it. | identity.md, owl.md, analytics.md, ecom-channel.md, fox.md | Required by GLBA, recommended by GDPR (Art. 32), required by PCI DSS Req 10. |

### P1 — Before GA

| # | Recommendation | Affected SDD(s) | Why |
|---|---|---|---|
| H.P1.1 | **Implement PostgreSQL RLS** with `SET LOCAL canary.current_merchant_id` pattern (data-model.md L1787, P1-4 L2067). Application-layer `WHERE merchant_id` is one bug from a cross-tenant breach. | data-model.md L1787 | Defense in depth. |
| H.P1.2 | **Hash IP addresses (HMAC-SHA256 keyed or /24 truncate) at write time** in `audit_log`, `ingestion_log`, `fox_evidence_access_log`, `devices.ip_address`. Standardize the procedure across all five tables; reconcile retention windows. | data-model.md L2064; tsp-parse.md L426; fox.md L243 | GDPR / CCPA explicitly classify IP as PII. |
| H.P1.3 | **Implement comprehensive data retention windows with automated purge jobs** for: `audit_log` (24 mo), `webhook_events` (12 mo), `notification_log` (12 mo), `dead_letter_queue` (90 d), `app.interest_signups` (24 mo from signup, sooner on opt-out), `growdirect_memory.alx_*` (TBD), `metrics.risk_score_history` (TBD — flag FCRA implications). | data-model.md L2065; alert.md L590; identity.md L597 | GDPR storage-limitation principle; CCPA reasonable retention. |
| H.P1.4 | **Encrypt `growdirect_memory.alx_memories.content` and audit access**, or classify and field-redact. Today it is internal-classified but contains arbitrary session context that may carry employee/customer identifiers. Memory bus is consumed by every agent — high blast radius. | data-model.md L2081 (P2 in spec; recommend lifting to P1); store-brain.md | Memory leakage across sessions is a hidden cross-tenant risk if `growdirect_memory` lacks per-merchant scoping. |
| H.P1.5 | **Add `employees.phone` to the data-model.md PII map** (identity.md L71 documents it; data-model.md does not). | data-model.md (PII map); identity.md L71 | Inventory drift; closes Section D drift. |
| H.P1.6 | **Document and implement a DPIA for**: (a) ILDWAC five-dimension cost model, (b) `customer_presence_log`, (c) employee risk scoring (`metrics.entity_risk_scores`), (d) Owl personality routing on employee-attributed alerts. | New artifacts; ildwac.md, store-brain.md, analytics.md, owl.md | GDPR Art. 35 — large-scale processing of employee data and behavioral inference triggers DPIA. |
| H.P1.7 | **Implement cryptographic erasure for `app.users` and `app.employees` via per-subject DEK pattern**, mirroring raas.md / ecom-channel.md. Allows GDPR right-to-erasure without breaking referential integrity in alerts, fox cases, and historical records. | identity.md (new); raas.md L420–445 (pattern reference) | Today, deactivating a user has no erasure semantics. |
| H.P1.8 | **Add token-decrypt audit logging** (P1-1 in identity.md L464; P1-3 in data-model.md L2066). | identity.md, data-model.md | Restricted-access fields require access logging. |
| H.P1.9 | **Document subprocessor list and notification process.** | New artifact | Required by GDPR Art. 28 and most enterprise customer contracts. |
| H.P1.10 | **Reconcile retention windows for `ip_address`** across `audit_log` (24 mo), `ingestion_log` (90 d), and `fox_evidence_access_log` (7 yr). The 90× spread suggests the data-model authors did not coordinate; pick a coherent schedule by classification, not by table. | data-model.md, tsp-parse.md, fox.md | One IP, three retentions. |

### P2 — Post-launch

| # | Recommendation | Affected SDD(s) | Why |
|---|---|---|---|
| H.P2.1 | **Document a key rotation procedure and test it end-to-end.** The procedure is sketched in go-security.md L237–246 and ecom-channel.md L334; it has not been exercised. | go-security.md L237; identity.md P2-1 (L601) | Audit finding waiting to happen. |
| H.P2.2 | **Enable Valkey AUTH and TLS** (`rediss://`) in production. Sessions today are unauthenticated to the cache. | identity.md P2-2 (L605); data-model.md L2077 | Lateral-movement defense. |
| H.P2.3 | **Add field-level access audit for encrypted PII columns** (data-model.md L2078). Beyond H.P0.10 (tool-level), this catches out-of-band DBA reads. | data-model.md L2078 | PCI Req 10.2; SOC 2 CC7.2. |
| H.P2.4 | **Migrate evidence files in `fox_evidence` from local filesystem to object storage** with server-side encryption (data-model.md L2079). | data-model.md L2079; fox.md | Single-host loss vector today. |
| H.P2.5 | **Add anti-virus scanning on evidence file upload** (data-model.md L2080). | fox.md (new); | Defense in depth. |
| H.P2.6 | **Threat-model the keyed-hash leakage scenario for `customer_email_hash` and `phone_hash`** as part of the DPA threat model. Document the rotation/expiry expectations and the consequences of `EMAIL_HASH_KEY` / `PHONE_HASH_KEY` compromise. | go-security.md L172–194; ecom-channel.md L334; new threat model | Forever-public data via the chain anchor depends on key confidentiality. |
| H.P2.7 | **Document data-residency and cross-region commitments** in advance of EU/UK customer onboarding. | New artifact | Triggered by first non-US customer. |
| H.P2.8 | **Audit `growdirect_memory` for tenant boundary enforcement** and add per-merchant scoping if needed. | data-model.md L1880–1893 | Memory bus is shared across all merchants; current spec relies on database separation but does not isolate per-merchant. |

---

*Canary | GrowDirect LLC | Confidential*
