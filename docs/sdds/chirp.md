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

# Chirp Detection Engine

## Detection Category → Go Service Mapping

Every Chirp detection category maps to its Go service owner and data owner. The ILDWAC row is architectural direction — the table and alert type are reserved for a future implementation pass.

| Category | Go Service | Data Owner (tables) | Alert Type |
|---|---|---|---|
| Refund abuse | Chirp Engine | `sales.transactions`, `sales.refund_links` | `REFUND_ABUSE` |
| Discount abuse | Chirp Engine | `sales.transactions`, `sales.transaction_line_items` | `DISCOUNT_ABUSE` |
| Void / void-after-close | Chirp Engine | `sales.transactions` (void types) | `VOID_ABUSE` |
| Cash drawer | Chirp Engine | `sales.cash_drawer_shifts`, `sales.cash_drawer_events` | `CASH_DRAWER` |
| Employee velocity | Chirp Engine | `sales.transactions` | `EMPLOYEE_VELOCITY` |
| Time-of-day anomaly | Chirp Engine | `sales.transactions` | `TIME_ANOMALY` |
| Basket anomaly | Chirp Engine | `sales.transactions`, `sales.transaction_line_items` | `BASKET_ANOMALY` |
| Item-level anomaly | Chirp Engine | `sales.transaction_line_items`, `app.items` | `ITEM_ANOMALY` |
| Cross-location anomaly | Chirp Engine | `sales.transactions` | `CROSS_LOCATION` |
| Threshold manipulation | Chirp Engine | `sales.transactions` | `THRESHOLD_MANIP` |
| ILDWAC cost anomaly *(architectural direction)* | Chirp Engine | `ledger.ilwac_positions` *(future)* | `COST_ANOMALY` |

---

## Purpose

Chirp is Canary's stateless detection rule engine. It evaluates merchant transactions, cash drawer shifts, gift card activities, loyalty events, disputes, and invoices against a catalog of detection rules. When a rule fires, Chirp produces an alert with a contextual risk score (0–100), writes it to the tenant's operational schema, and optionally auto-creates an investigation case (Fox/Hawk) for critical-severity rules. Chirp never mutates source transaction data.

**Multi-tenant context.** Chirp operates per-tenant — every detection cycle is scoped to a single merchant via `SET search_path TO tenant_{merchant_id}, public`. Detection rule reads (`detection_rules`, `merchant_rule_configs`, `location_rule_configs`) and alert writes happen inside the tenant schema. Cross-tenant pattern correlation (organized retail crime detection across stores) flows through scheduled rollups into the `analytics` schema, not direct cross-tenant scans. See `architecture.md` "Multi-Tenant Isolation" for the canonical pattern.

**Optional Features posture.** Chirp's core detection engine operates with all Optional Features (per `platform-overview.md`) disabled. The asset registry filter (excluding asset-class items from shrink-rule candidates per Module A) is required core. When `ILDWAC_ENABLED=true`, the cost-anomaly rule family becomes active — until then, those rule rows are inactive in the catalog. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, alert events are anchored asynchronously per `blockchain-anchor.md` — chain integrity holds either way.

### Multi-POS Rule Substrate

Chirp's 37 production rules were built against Square's data model. The Counterpoint adapter introduces additional rule families that exploit Counterpoint's richer audit surface:

| Substrate | Square (v1) | Counterpoint (Phase 1+) |
|---|---|---|
| Tender taxonomy | Payment.tender_type | PS_DOC_PMT.PAY_COD → canonical tender type (F module) |
| Drawer session | CashDrawerShift events | PS_DOC_AUDIT_LOG + DRW_SESSION_ID correlation |
| Audit log | Not available | PS_DOC_AUDIT_LOG — ACTIV codes + LOG_ENTRY strings |
| Pricing decisions | Payment.total vs catalog | PS_DOC_LIN_PRICE — per-line pricing rule justification |
| Margin targets | Not available | IM_CATEG_COD.MIN_PFT_PCT / TRGT_PFT_PCT |
| Tax compliance | Not available | PS_DOC_TAX — multi-authority jurisdiction stack |

Provider attribution on each canonical event (`source=counterpoint`) enables per-provider rule applicability.

## Dependencies

| Dependency | Type | Required | Purpose |
|---|---|---|---|
| PostgreSQL (`canary` DB) | Database | Yes | `app` schema (alerts, rule config, employees), `sales` schema (transactions, shifts, gift cards, loyalty, disputes, invoices), `metrics` schema (entity risk scores, period metrics) |
| Valkey (DB 0) | Cache | No (degrades gracefully) | Threshold cache (`chirp:thresholds:*`, `chirp:active_rules:*`, 300s TTL) |
| Webhook Pipeline (Sub4) | Internal caller | Yes | Triggers real-time evaluation on incoming POS webhooks |
| Fox Case Service | Internal callee | No (best-effort) | Auto-creates investigation cases for 6 critical rules |
| Risk Aggregator | Internal callee | No (best-effort) | Recalculates employee risk scores after alert batch |

## Data Flow

### What Enters

| Source | Fields |
|---|---|
| Sub4 detect stage | `amount_cents`, `transaction_type`, `transaction_date`, `employee_id`, `location_id`, `card_fingerprint`, `delay_action`, `approved_amount_cents`, `entry_method`, `external_id`, `merchant_id` |
| `sales` schema | Transaction, CashDrawerShift, GiftCardActivity, LoyaltyEvent, Dispute, Invoice rows loaded by rule evaluators |
| Threshold resolver | Per-merchant rule thresholds keyed by rule_id from Valkey / DB / catalog defaults |

### What's Stored (Chirp-Owned Tables)

All tables in `app` schema.

| Table | Key Columns | Notes |
|---|---|---|
| `detection_rules` | `rule_id`, `rule_name`, `description`, `category`, `severity`, `default_threshold`, `is_active` | Global catalog, 37 rules, not tenant-scoped |
| `merchant_rule_config` | `merchant_id`, `rule_id`, `is_enabled`, `custom_threshold` | JSON threshold overrides per merchant |

### What's Written (Cross-Domain)

Chirp writes to tables owned by the Alert domain:

| Table | Key Columns | Notes |
|---|---|---|
| `alerts` | `employee_id`, `location_id`, `details` (JSON), `amount_cents` | `details` is plaintext JSON — P0 security finding; must be encrypted before production |
| `alert_history` | `status="new"`, `changed_by=NULL` | Written atomically with alert INSERT |

### What's Read (Cross-Domain)

| Schema | Table | Fields | Purpose |
|---|---|---|---|
| `sales` | `transactions` | `id`, `external_id`, `merchant_id`, `employee_id`, `location_id`, `amount_cents`, `transaction_type`, `transaction_date`, `card_fingerprint`, `entry_method`, `delay_action`, `approved_amount_cents`, `order_id` | Rule evaluation |
| `sales` | `refund_links` | `original_external_id`, `refund_external_id`, `refund_amount_cents` | C-001, C-502 refund correlation |
| `sales` | `transaction_tenders` | `transaction_id`, `payment_id` | C-006 split-tender, C-001 fallback lookup |
| `sales` | `transaction_line_items` | `gross_sales_cents`, `total_discount_cents`, `is_voided`, `item_name` | C-201/C-202/C-203 order rules |
| `sales` | `cash_drawer_shifts` | `id`, `employee_id`, `location_id`, `opened_at`, `cash_variance_cents` | C-101 through C-104 |
| `sales` | `cash_drawer_events` | `shift_id`, `event_type`, `amount_cents` | C-101 no-sale count, C-103 paid-out total |
| `sales` | `employee_timecards` | `employee_id`, `start_at`, `end_at`, `breaks` (JSON array), `location_id` | C-301/C-302/C-303 |
| `sales` | `gift_card_activities` | `gift_card_id`, `activity_type`, `amount_cents`, `occurred_at` | C-601/C-602 |
| `sales` | `loyalty_events` | `loyalty_account_id`, `event_type`, `points`, `occurred_at`, `location_id`, `employee_id` | C-801 through C-804 |
| `sales` | `loyalty_accounts` | `enrolled_by_employee_id`, `enrolled_at` | C-804 enrollment fraud |
| `sales` | `disputes` | `state`, `reason`, `amount_cents`, `payment_id`, `location_id`, `reported_at` | C-D01 through C-D03 |
| `sales` | `invoices` | `invoice_status`, `next_payment_amount_cents` | C-I01 through C-I03 |
| `app` | `employees` | `id`, `square_employee_id`, `risk_score` | POS ID resolution, risk score update |
| `app` | `locations` | `id`, `square_location_id` | POS ID resolution |
| `metrics` | `period_metrics` | `sra_pct_sales`, `sra_total_cents`, `gross_sales_cents` | C-901 SRA threshold breach |

## Threshold Resolution Hierarchy

Thresholds resolve in priority order (highest wins):

```
1. Merchant-specific override (merchant_rule_config.custom_threshold JSON, per merchant_id + rule_id)
2. Global catalog default (detection_rules.default_threshold / embedded rule catalog)
```

A rule is considered **disabled** if:
- `merchant_rule_config.is_enabled = false` for this merchant (merchant override wins)
- OR `detection_rules.is_active = false` globally (when no merchant override exists)

Disabled rules produce no alerts. The resolver must build a `disabledRules` set before evaluation begins.

**Cache contract:** Resolved thresholds are cached in Valkey at key `chirp:thresholds:{merchant_id}` with 300s TTL. On cache miss, load from DB. On Valkey failure, fall through to DB. If DB also fails, use catalog defaults — evaluation must not halt.

## Evaluation Tiers

| Tier | DB Access | Rule Count | Description |
|---|---|---|---|
| 1 (Stateless) | None — pure payload fields | 10 rules | Fires from a single parsed event with no lookups |
| 2 (Lightweight) | Single windowed count query or Valkey counter | ~12 rules | Rate/velocity checks with time window |
| 3 (Full DB) | Multi-table join or cross-reference | ~15 rules | Timecard lookups, order aggregates, batch-only |

Tier 1 rules: C-004, C-007, C-009, C-010, C-011, C-D01, C-D02, C-I01, C-I02, C-I03

## Rule Catalog (37 Rules, 10 Categories)

### Auto-Case Creation Rules

Six rules automatically trigger Fox case creation after alert commit:

| Rule ID | Name |
|---|---|
| C-009 | SQUARE_DELAY_HOLD |
| C-104 | AFTER_HOURS_DRAWER |
| C-204 | UNTENDERED_ORDER |
| C-301 | OFF_CLOCK_TRANSACTION |
| C-502 | POST_VOID_ALERT |
| C-602 | GIFT_CARD_DRAIN |

Auto-case creation is **best-effort** — failure must not block the alert pipeline.

---

### Payment Rules (C-001 through C-011)

---

### C-001 — RAPID_REFUND

| Field | Value |
|---|---|
| Code | C-001 |
| Category | payment |
| Severity | high |
| Trigger | Transaction of type RETURN or REFUND |
| Threshold | `seconds` — default 900 (15 minutes) |
| Evaluation window | Delta between refund timestamp and original sale timestamp |

**Detection logic:** A refund is issued within N seconds of the original sale. Requires a refund-link record correlating the refund's external ID to the original transaction. Lookup chain: refund_links → transactions by external_id or order_id → transaction_tenders by payment_id (fallback). If the time delta is in the range [0, threshold_seconds), the rule fires.

**SQL contract:**
```sql
-- Step 1: Find refund link
SELECT original_external_id, refund_amount_cents
FROM sales.refund_links
WHERE merchant_id = $1
  AND refund_external_id = $2;

-- Step 2: Find original sale (try external_id first, then order_id)
SELECT id, transaction_date, amount_cents, employee_id
FROM sales.transactions
WHERE merchant_id = $1
  AND (external_id = $2 OR order_id = $2)
LIMIT 1;

-- Step 3 (fallback): Find original via tender payment_id
SELECT transaction_id FROM sales.transaction_tenders
WHERE merchant_id = $1 AND payment_id = $2
LIMIT 1;
```

**Alert fields produced:** `rule_id=C-001`, `severity=high`, `employee_id` (refund employee), details: `delta_seconds`, `threshold_seconds`, `refund_amount_cents`, `original_amount_cents`

---

### C-002 — EXCESSIVE_REFUND_RATE

| Field | Value |
|---|---|
| Code | C-002 |
| Category | payment |
| Severity | high |
| Trigger | Any transaction with an associated employee_id |
| Threshold | `percent` default 15, `min_transactions` default 5 |
| Evaluation window | 24 hours rolling |

**Detection logic:** Employee refund rate exceeds N% of their total transactions in the last 24 hours. Only fires if total transaction count meets the minimum threshold (prevents false positives on new employees with one refund).

**SQL contract:**
```sql
-- Total transactions for employee in window
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND employee_id = $2
  AND created_at >= now() - interval '24 hours';

-- Refund transactions for employee in window
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND employee_id = $2
  AND created_at >= now() - interval '24 hours'
  AND transaction_type IN ('RETURN', 'REFUND');
```

Fire if `total_count >= min_transactions AND (refund_count / total_count * 100) > percent_threshold`.

**Alert fields produced:** `rule_id=C-002`, `severity=high`, `employee_id`, details: `refund_rate_pct`, `threshold_pct`, `refund_count`, `total_count`

---

### C-003 — ROUND_AMOUNT_PATTERN

| Field | Value |
|---|---|
| Code | C-003 |
| Category | payment |
| Severity | medium |
| Trigger | Transaction with amount divisible by 100 (round dollar) and an employee_id present |
| Threshold | `count` default 5, `window_seconds` default 3600 |
| Evaluation window | Configurable rolling window |

**Detection logic:** N or more round-dollar transactions by the same employee in the time window. Structuring signal — cash skimming often involves round amounts.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND employee_id = $2
  AND created_at >= $3  -- window start = transaction_date - window_seconds
  AND amount_cents % 100 = 0
  AND amount_cents > 0;
```

**Alert fields produced:** `rule_id=C-003`, `severity=medium`, `employee_id`, details: `round_count`, `threshold_count`, `window_seconds`

---

### C-004 — AFTER_HOURS_TRANSACTION

| Field | Value |
|---|---|
| Code | C-004 |
| Category | payment |
| Severity | medium |
| Trigger | Any transaction |
| Threshold | `open_hour` default 6, `close_hour` default 22 |
| Evaluation window | None — single event check (Tier 1 stateless) |

**Detection logic:** Transaction timestamp hour falls outside [open_hour, close_hour). Evaluated purely from the parsed event timestamp — no DB lookup required.

**Alert fields produced:** `rule_id=C-004`, `severity=medium`, details: `hour`, `open_hour`, `close_hour`

---

### C-005 — CARD_VELOCITY

| Field | Value |
|---|---|
| Code | C-005 |
| Category | payment |
| Severity | high |
| Trigger | Transaction with a non-null card_fingerprint |
| Threshold | `count` default 5, `window_seconds` default 3600 |
| Evaluation window | Configurable rolling window |

**Detection logic:** Same card fingerprint used N or more times in the time window. Indicates card testing, cloned card usage, or repeated fraud attempts.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND card_fingerprint = $2
  AND created_at >= $3;  -- window start
```

**Alert fields produced:** `rule_id=C-005`, `severity=high`, details: `card_uses`, `threshold_count`, `window_seconds`, `card_fingerprint` (truncated to 8 chars + "...")

---

### C-006 — SPLIT_TENDER_PATTERN

| Field | Value |
|---|---|
| Code | C-006 |
| Category | payment |
| Severity | medium |
| Trigger | Transaction with an employee_id |
| Threshold | `count` default 3, `window_seconds` default 3600 |
| Evaluation window | Configurable rolling window |

**Detection logic:** N or more split-tender transactions (transactions with 2+ tender records) by the same employee in the time window. Structuring signal.

**SQL contract:**
```sql
SELECT tt.transaction_id
FROM sales.transaction_tenders tt
JOIN sales.transactions t ON t.id = tt.transaction_id
WHERE t.merchant_id = $1
  AND t.employee_id = $2
  AND t.created_at >= $3
GROUP BY tt.transaction_id
HAVING count(tt.id) >= 2;
```

Fire if count of returned rows >= count threshold.

**Alert fields produced:** `rule_id=C-006`, `severity=medium`, `employee_id`, details: `split_count`, `threshold_count`, `window_seconds`

---

### C-007 — HIGH_VALUE_REFUND

| Field | Value |
|---|---|
| Code | C-007 |
| Category | payment |
| Severity | high |
| Trigger | Transaction of type RETURN or REFUND (Tier 1 stateless) |
| Threshold | `amount_cents` default 10000 ($100) |
| Evaluation window | None — single event check |

**Detection logic:** Absolute value of refund amount meets or exceeds the threshold. No DB lookup — evaluated from payload amount field.

**Alert fields produced:** `rule_id=C-007`, `severity=high`, details: `amount_cents`, `threshold_cents`

---

### C-008 — MANUAL_ENTRY_SPIKE

| Field | Value |
|---|---|
| Code | C-008 |
| Category | payment |
| Severity | medium |
| Trigger | Transaction with entry_method in (KEYED, MANUAL) and employee_id present |
| Threshold | `count` default 5 |
| Evaluation window | 24-hour rolling window |

**Detection logic:** N or more keyed-in card transactions by the same employee in the last 24 hours. Card-not-present risk indicator.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND employee_id = $2
  AND created_at >= now() - interval '24 hours'
  AND entry_method IN ('KEYED', 'MANUAL');
```

**Alert fields produced:** `rule_id=C-008`, `severity=medium`, `employee_id`, details: `manual_count`, `threshold_count`

---

### C-009 — SQUARE_DELAY_HOLD

| Field | Value |
|---|---|
| Code | C-009 |
| Category | payment |
| Severity | critical |
| Trigger | Transaction with a non-null delay_action field AND transaction_type NOT IN (SALE, RETURN, VOID, POST_VOID) — Tier 1 stateless |
| Threshold | None |
| Evaluation window | None — single event check |

**Detection logic:** The POS has flagged this payment with a delay action — funds are being held. The rule amplifies the POS risk signal. Does not fire on completed sales or voided/returned transactions. Only fires when the payment is in an actively-held state.

**Alert fields produced:** `rule_id=C-009`, `severity=critical`, details: `delay_action`, `delay_duration`, signal description

---

### C-010 — PARTIAL_AUTHORIZATION

| Field | Value |
|---|---|
| Code | C-010 |
| Category | payment |
| Severity | high |
| Trigger | Transaction where approved_amount_cents < amount_cents (Tier 1 stateless) |
| Threshold | `variance_cents` default 0 |
| Evaluation window | None — single event check |

**Detection logic:** The issuing bank approved less than the requested amount. Indicates the card is near its limit, the account is flagged, or a fraud signal from the card network. Fires when `(amount_cents - approved_amount_cents) > variance_cents`.

**Alert fields produced:** `rule_id=C-010`, `severity=high`, details: `requested_cents`, `approved_cents`, `shortfall_cents`

---

### C-011 — NO_SALE_DETECTED

| Field | Value |
|---|---|
| Code | C-011 |
| Category | payment |
| Severity | high |
| Trigger | Transaction with transaction_type = NO_SALE (Tier 1 stateless) |
| Threshold | None |
| Evaluation window | None — single event check |

**Detection logic:** Cash drawer was opened with no associated sale. Fires whenever the parsed event type is NO_SALE — no DB lookup required. Note: distinct from C-101 (NO_SALE_ABUSE), which fires after repeated no-sale events in a shift.

**Alert fields produced:** `rule_id=C-011`, `severity=high`, `employee_id`, `location_id`

---

### Cash Drawer Rules (C-101 through C-104)

---

### C-101 — NO_SALE_ABUSE

| Field | Value |
|---|---|
| Code | C-101 |
| Category | cash_drawer |
| Severity | high |
| Trigger | Cash drawer shift event |
| Threshold | `count` default 5 |
| Evaluation window | Per shift |

**Detection logic:** N or more NO_SALE drawer open events in the current shift.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.cash_drawer_events
WHERE shift_id = $1
  AND event_type = 'NO_SALE';
```

**Alert fields produced:** `rule_id=C-101`, `severity=high`, `employee_id` (from shift), `location_id` (from shift), details: `no_sale_count`

---

### C-102 — CASH_VARIANCE

| Field | Value |
|---|---|
| Code | C-102 |
| Category | cash_drawer |
| Severity | high |
| Trigger | Cash drawer shift close |
| Threshold | `amount_cents` default 2000 ($20) |
| Evaluation window | Per shift |

**Detection logic:** The shift's cash variance (expected vs actual cash) exceeds the threshold in absolute value. Fires on `abs(cash_variance_cents) >= threshold`.

**Alert fields produced:** `rule_id=C-102`, `severity=high`, `employee_id`, `location_id`, details: `variance_cents`

---

### C-103 — PAID_OUT_ANOMALY

| Field | Value |
|---|---|
| Code | C-103 |
| Category | cash_drawer |
| Severity | medium |
| Trigger | Cash drawer shift |
| Threshold | `amount_cents` default 5000 ($50) |
| Evaluation window | Per shift |

**Detection logic:** Total PAID_OUT events in the shift exceed the threshold without a manager override record.

**SQL contract:**
```sql
SELECT coalesce(sum(amount_cents), 0)
FROM sales.cash_drawer_events
WHERE shift_id = $1
  AND event_type = 'PAID_OUT';
```

**Alert fields produced:** `rule_id=C-103`, `severity=medium`, `employee_id`, `location_id`, details: `paid_out_cents`, `threshold_cents`

---

### C-104 — AFTER_HOURS_DRAWER

| Field | Value |
|---|---|
| Code | C-104 |
| Category | cash_drawer |
| Severity | critical |
| Trigger | Cash drawer shift open (Tier 3 — needs DB shift record) |
| Threshold | `open_hour` default 6, `close_hour` default 22 |
| Evaluation window | None — single shift open event |

**Detection logic:** Cash drawer opened outside business hours. Evaluated from `shift.opened_at` timestamp hour. Auto-creates Fox case on fire.

**Alert fields produced:** `rule_id=C-104`, `severity=critical`, `employee_id`, `location_id`, details: `hour`

---

### Order Rules (C-201 through C-204)

---

### C-201 — EXCESSIVE_DISCOUNT_RATE

| Field | Value |
|---|---|
| Code | C-201 |
| Category | order |
| Severity | high |
| Trigger | Transaction with associated order line items |
| Threshold | `percent` default 50 |
| Evaluation window | Per order |

**Detection logic:** Average order discount rate exceeds N% of gross sales. Sweethearting signal.

```
discount_rate = (sum(total_discount_cents) / sum(gross_sales_cents)) * 100
```

Fire if `gross_sales_cents > 0 AND discount_rate > percent_threshold`.

**Alert fields produced:** `rule_id=C-201`, `severity=high`, details: `discount_rate` (rounded to 1 decimal), `gross_sales_cents`, `total_discount_cents`

---

### C-202 — LINE_ITEM_VOID_RATE

| Field | Value |
|---|---|
| Code | C-202 |
| Category | order |
| Severity | high |
| Trigger | Transaction with associated order line items |
| Threshold | `percent` default 10, `min_items` default 10 |
| Evaluation window | Per order |

**Detection logic:** Employee line item void rate exceeds N% of total items, only evaluated when item count meets the minimum.

```
void_rate = (count(is_voided=true) / total_items) * 100
```

Fire if `total_items >= min_items AND void_rate > percent_threshold`.

**Alert fields produced:** `rule_id=C-202`, `severity=high`, details: `void_rate`, `voided`, `total` (item counts)

---

### C-203 — SWEETHEARTING

| Field | Value |
|---|---|
| Code | C-203 |
| Category | order |
| Severity | high |
| Trigger | Transaction with associated order line items |
| Threshold | `amount_cents` default 2000 ($20) |
| Evaluation window | Per line item |

**Detection logic:** Discount applied to a single line item exceeds the threshold amount. Fires once per qualifying line item (multiple alerts possible per order).

Fire if `line_item.total_discount_cents >= threshold`.

**Alert fields produced:** `rule_id=C-203`, `severity=high`, details: `discount_cents`, `threshold_cents`, `item_name`, `gross_sales_cents`

---

### C-204 — UNTENDERED_ORDER

| Field | Value |
|---|---|
| Code | C-204 |
| Category | order |
| Severity | critical |
| Trigger | Batch sweep only — not per-transaction |
| Threshold | `stale_hours` default 24 |
| Evaluation window | Batch sweep window |

**Detection logic:** Order created with no corresponding payment after the stale threshold. Orders are identified by `order_id` on the transaction record — an order_id with exactly one transaction and no payment transaction means the order was never tendered. Sweethearting, open-ticket drift, or unresumed-suspend signal.

**SQL contract:**
```sql
SELECT t.order_id,
       count(t.id) AS txn_count,
       min(t.created_at) AS first_seen
FROM sales.transactions t
WHERE t.merchant_id = $1
  AND t.order_id IS NOT NULL
  AND t.order_id != ''
GROUP BY t.order_id
HAVING count(t.id) = 1
   AND min(t.created_at) < now() - ($2 * interval '1 hour');
-- $2 = stale_hours threshold
```

Retrieves the actual transaction row for alert details after identifying stale order_ids.

**Alert fields produced:** `rule_id=C-204`, `severity=critical`, details: `order_id`, `age_hours`, `stale_threshold_hours`

---

### Timecard Rules (C-301 through C-303)

All timecard rules share a common prerequisite: a timecard lookup. If no open timecard is found, C-301 fires and C-302/C-303 evaluation is skipped (no timecard = cannot check break or location).

**Timecard lookup:**
```sql
SELECT id, start_at, end_at, breaks, location_id
FROM sales.employee_timecards
WHERE merchant_id = $1
  AND employee_id = $2
  AND start_at <= $3            -- $3 = transaction_date
  AND (end_at IS NULL OR end_at >= $3)
LIMIT 1;
```

Note: Use `transaction_date` (actual POS timestamp), not `created_at` (DB insert time), for timecard boundary comparisons.

---

### C-301 — OFF_CLOCK_TRANSACTION

| Field | Value |
|---|---|
| Code | C-301 |
| Category | timecard |
| Severity | critical |
| Trigger | Transaction with employee_id (Tier 3 — DB lookup required) |
| Threshold | None |
| Evaluation window | Transaction timestamp vs timecard open window |

**Detection logic:** Payment processed by an employee with no open timecard at the time of the transaction. Ghost employee or post-clock-out access signal. Auto-creates Fox case on fire.

**Alert fields produced:** `rule_id=C-301`, `severity=critical`, `employee_id`, details: `employee_id`, `transaction_time`, note

---

### C-302 — BREAK_TRANSACTION

| Field | Value |
|---|---|
| Code | C-302 |
| Category | timecard |
| Severity | high |
| Trigger | Transaction where timecard exists (C-301 did not fire) |
| Threshold | None |
| Evaluation window | Transaction timestamp vs declared break windows |

**Detection logic:** Payment processed during an employee's declared break window. The `breaks` field on the timecard is a JSON array of `{start, end}` (or `{start_at, end_at}`) objects. If the transaction timestamp falls within any break window, the rule fires.

**Alert fields produced:** `rule_id=C-302`, `severity=high`, `employee_id`, details: `transaction_time`, `break_start`, `break_end`

---

### C-303 — WRONG_LOCATION

| Field | Value |
|---|---|
| Code | C-303 |
| Category | timecard |
| Severity | high |
| Trigger | Transaction where timecard exists and both transaction and timecard have location_id |
| Threshold | None |
| Evaluation window | Per transaction |

**Detection logic:** Employee is clocked in at Location A but the transaction occurred at Location B. Location mismatch between `transaction.location_id` and `timecard.location_id`.

**Alert fields produced:** `rule_id=C-303`, `severity=high`, `employee_id`, details: `txn_location`, `timecard_location`

---

### Void Rules (C-501 through C-502)

---

### C-501 — HIGH_VOID_RATE

| Field | Value |
|---|---|
| Code | C-501 |
| Category | void |
| Severity | high |
| Trigger | Transaction of type VOID or POST_VOID |
| Threshold | `count` default 5 |
| Evaluation window | 24-hour rolling window |

**Detection logic:** N or more void/post-void transactions by the same employee in the last 24 hours. Concealment technique indicator.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.transactions
WHERE merchant_id = $1
  AND employee_id = $2
  AND created_at >= now() - interval '24 hours'
  AND transaction_type IN ('VOID', 'POST_VOID');
```

**Alert fields produced:** `rule_id=C-501`, `severity=high`, `employee_id`, details: `void_count`, `threshold`, `window_hours`

---

### C-502 — POST_VOID_ALERT

| Field | Value |
|---|---|
| Code | C-502 |
| Category | void |
| Severity | high or critical (tier-dependent) |
| Trigger | Transaction of type RETURN where a refund link exists to a completed SALE |
| Threshold | `immediate_max_seconds` default 120, `watch_max_seconds` default 900, `suspicious_max_seconds` default 28800, `self_refund_score_boost` default 10, `off_clock_score_boost` default 15 |
| Evaluation window | Delta between refund and original sale timestamps |

**Detection logic:** Tiered full-refund detection based on time gap between sale and refund:

| Gap | Tier | Severity |
|---|---|---|
| < 2 minutes (< immediate_max_seconds) | Legitimate correction | No alert |
| 2–15 minutes (watch_max_seconds) | WATCH | high |
| 15 min–8 hours (suspicious_max_seconds) | SUSPICIOUS | critical |
| > 8 hours | Too old | No alert |

Self-refund detection: if the same employee processed both the original sale and the refund, add `self_refund_score_boost` to the risk score. Only fires if `refund_amount_cents >= original.amount_cents` (full refund). Auto-creates Fox case on fire.

**SQL contract:**
```sql
-- Find refund link
SELECT original_external_id, refund_amount_cents
FROM sales.refund_links
WHERE merchant_id = $1
  AND refund_external_id = $2;

-- Find original sale
SELECT id, amount_cents, transaction_type, transaction_date, employee_id
FROM sales.transactions
WHERE merchant_id = $1
  AND external_id = $2
  AND transaction_type = 'SALE';
```

**Alert fields produced:** `rule_id=C-502`, `severity` (tier-dependent), details: `original_txn_id`, `original_amount_cents`, `refund_amount_cents`, `gap_seconds`, `tier`, `is_self_refund`, `original_employee_id`, `refund_employee_id`, `score_boost`

---

### Gift Card Rules (C-601 through C-602)

---

### C-601 — GIFT_CARD_LOAD_VELOCITY

| Field | Value |
|---|---|
| Code | C-601 |
| Category | gift_card |
| Severity | high |
| Trigger | GiftCardActivity of type LOAD |
| Threshold | `count` default 3, `window_seconds` default 3600 |
| Evaluation window | Configurable rolling window |

**Detection logic:** N or more LOAD events on the same gift card in the time window. Money laundering signal.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.gift_card_activities
WHERE merchant_id = $1
  AND gift_card_id = $2
  AND activity_type = 'LOAD'
  AND occurred_at >= $3          -- window start = event.occurred_at - window_seconds
  AND occurred_at <= $4;         -- event.occurred_at
```

**Alert fields produced:** `rule_id=C-601`, `severity=high`, details: `load_count`, `threshold_count`, `window_seconds`, `gift_card_id`

---

### C-602 — GIFT_CARD_DRAIN

| Field | Value |
|---|---|
| Code | C-602 |
| Category | gift_card |
| Severity | critical |
| Trigger | GiftCardActivity of type REDEEM |
| Threshold | `seconds_after_load` default 1800 (30 minutes) |
| Evaluation window | Window before redemption |

**Detection logic:** Full gift card redemption immediately after load. Fires if a LOAD event for the same card exists within the configured window and `abs(redeem_amount_cents) >= load_amount_cents`. Auto-creates Fox case on fire.

**SQL contract:**
```sql
SELECT id, amount_cents, occurred_at
FROM sales.gift_card_activities
WHERE merchant_id = $1
  AND gift_card_id = $2
  AND activity_type = 'LOAD'
  AND occurred_at >= $3          -- redeem.occurred_at - seconds_after_load
  AND occurred_at <= $4          -- redeem.occurred_at
ORDER BY occurred_at DESC
LIMIT 1;
```

Fire if record found AND `abs(redeem_cents) >= load_cents`.

**Alert fields produced:** `rule_id=C-602`, `severity=critical`, details: `redeem_cents`, `load_cents`, `seconds_apart`, `gift_card_id`

---

### Loyalty Rules (C-801 through C-804)

---

### C-801 — RAPID_POINT_ACCUMULATION

| Field | Value |
|---|---|
| Code | C-801 |
| Category | loyalty |
| Severity | medium |
| Trigger | LoyaltyEvent of type ACCUMULATE |
| Threshold | `count` default 5, `window_seconds` default 3600 |
| Evaluation window | Configurable rolling window |

**Detection logic:** N or more ACCUMULATE events on the same loyalty account in the time window.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.loyalty_events
WHERE merchant_id = $1
  AND loyalty_account_id = $2
  AND event_type = 'ACCUMULATE'
  AND occurred_at >= $3
  AND occurred_at <= $4;
```

**Alert fields produced:** `rule_id=C-801`, `severity=medium`, details: `accumulate_count`, `threshold_count`, `window_seconds`, `loyalty_account_id`

---

### C-802 — BULK_REDEMPTION

| Field | Value |
|---|---|
| Code | C-802 |
| Category | loyalty |
| Severity | high |
| Trigger | LoyaltyEvent of type REDEEM |
| Threshold | `points` default 5000 |
| Evaluation window | Single event check |

**Detection logic:** Points redeemed in a single REDEEM event exceed the threshold. `abs(event.points) >= points_threshold`.

**Alert fields produced:** `rule_id=C-802`, `severity=high`, details: `points_redeemed`, `threshold_points`, `loyalty_account_id`

---

### C-803 — CROSS_LOCATION_VELOCITY

| Field | Value |
|---|---|
| Code | C-803 |
| Category | loyalty |
| Severity | high |
| Trigger | LoyaltyEvent with a non-null location_id |
| Threshold | `location_count` default 3, `window_seconds` default 7200 |
| Evaluation window | Configurable rolling window |

**Detection logic:** Loyalty events for the same account appear at N or more distinct locations in the time window. Stolen loyalty credential signal.

**SQL contract:**
```sql
SELECT count(distinct location_id)
FROM sales.loyalty_events
WHERE merchant_id = $1
  AND loyalty_account_id = $2
  AND occurred_at >= $3
  AND occurred_at <= $4
  AND location_id IS NOT NULL;
```

**Alert fields produced:** `rule_id=C-803`, `severity=high`, details: `distinct_locations`, `threshold_locations`, `window_seconds`, `loyalty_account_id`

---

### C-804 — ENROLLMENT_FRAUD

| Field | Value |
|---|---|
| Code | C-804 |
| Category | loyalty |
| Severity | medium |
| Trigger | LoyaltyEvent of type CREATE with employee_id present |
| Threshold | `count` default 10, `window_seconds` default 86400 (24 hours) |
| Evaluation window | 24-hour rolling window |

**Detection logic:** N or more loyalty enrollments created by the same employee in the time window. Loyalty enrollment fraud signal. Queries `loyalty_accounts` table, not `loyalty_events`.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.loyalty_accounts
WHERE merchant_id = $1
  AND enrolled_by_employee_id = $2
  AND enrolled_at >= $3          -- event.occurred_at - window_seconds
  AND enrolled_at <= $4;         -- event.occurred_at
```

**Alert fields produced:** `rule_id=C-804`, `severity=medium`, `employee_id`, details: `enrollment_count`, `threshold_count`, `window_seconds`

---

### Composite Rules

---

### C-901 — SRA_THRESHOLD_BREACH

| Field | Value |
|---|---|
| Code | C-901 |
| Category | composite |
| Severity | high |
| Trigger | Batch sweep only |
| Threshold | `sra_pct_sales_max` default 3.0 (percent) |
| Evaluation window | Latest fiscal period |

**Detection logic:** Shrink Risk Assessment (SRA) percentage of net sales exceeds the configured threshold. Reads from the `period_metrics` materialized aggregate table. Fires on the latest period only.

**SQL contract:**
```sql
SELECT id, sra_pct_sales, sra_total_cents, gross_sales_cents,
       fiscal_year, fiscal_period
FROM metrics.period_metrics
WHERE merchant_id = $1
ORDER BY fiscal_year DESC, fiscal_period DESC
LIMIT 1;
```

Fire if `sra_pct_sales > sra_pct_sales_max`.

Risk score formula: `min(100, int(sra_pct_sales / sra_pct_sales_max * 70))` blended with severity base.

**Alert fields produced:** `rule_id=C-901`, `severity=high`, details: `sra_pct_sales`, `sra_total` (dollars), `gross_sales` (dollars), `threshold_pct`, `fiscal_year`, `fiscal_period`

---

### Dispute Rules (C-D01 through C-D03)

---

### C-D01 — DISPUTE_CREATED

| Field | Value |
|---|---|
| Code | C-D01 |
| Category | dispute |
| Severity | high |
| Trigger | Dispute with state in (EVIDENCE_REQUIRED, INQUIRY_EVIDENCE_REQUIRED, INQUIRY_PROCESSING, PROCESSING) — Tier 1 stateless |
| Threshold | None |
| Evaluation window | Single event check |

**Detection logic:** A new dispute has been filed that requires merchant action. Fires on any dispute state that puts the merchant on the clock.

**Alert fields produced:** `rule_id=C-D01`, `severity=high`, `location_id`, details: `state`, `reason`, `amount_cents`, `payment_id`

---

### C-D02 — DISPUTE_LOST

| Field | Value |
|---|---|
| Code | C-D02 |
| Category | dispute |
| Severity | critical |
| Trigger | Dispute with state = LOST — Tier 1 stateless |
| Threshold | None |
| Evaluation window | Single event check |

**Detection logic:** Dispute resolved as LOST. Confirmed financial loss — chargeback is final.

**Alert fields produced:** `rule_id=C-D02`, `severity=critical`, `location_id`, details: `state`, `reason`, `amount_cents`, `payment_id`

---

### C-D03 — DISPUTE_VELOCITY

| Field | Value |
|---|---|
| Code | C-D03 |
| Category | dispute |
| Severity | high |
| Trigger | Dispute with non-null location_id and reported_at |
| Threshold | `count` default 3, `window_days` default 30 |
| Evaluation window | Rolling window in days |

**Detection logic:** N or more disputes at the same location in the time window. Pattern fraud signal.

**SQL contract:**
```sql
SELECT count(id)
FROM sales.disputes
WHERE merchant_id = $1
  AND location_id = $2
  AND reported_at >= $3          -- dispute.reported_at - window_days
  AND reported_at <= $4;         -- dispute.reported_at
```

**Alert fields produced:** `rule_id=C-D03`, `severity=high`, `location_id`, details: `dispute_count`, `threshold_count`, `window_days`

---

### Invoice Rules (C-I01 through C-I03)

---

### C-I01 — INVOICE_OVERDUE

| Field | Value |
|---|---|
| Code | C-I01 |
| Category | invoice |
| Severity | medium |
| Trigger | Invoice with status = UNPAID — Tier 1 stateless |
| Threshold | None |
| Evaluation window | Single event check |

**Detection logic:** Invoice is past due with UNPAID status. Fires on any invoice.overdue or payment.created webhook where the invoice status resolves to UNPAID.

**Alert fields produced:** `rule_id=C-I01`, `severity=medium`, details: `invoice_status`, `next_payment_amount_cents`

---

### C-I02 — INVOICE_CHARGE_FAILED

| Field | Value |
|---|---|
| Code | C-I02 |
| Category | invoice |
| Severity | high |
| Trigger | Invoice event of type `invoice.scheduled_charge_failed` — Tier 1 stateless |
| Threshold | None |
| Evaluation window | Single event check |

**Detection logic:** A scheduled automatic charge on an invoice has failed. Payment collection at risk.

**Alert fields produced:** `rule_id=C-I02`, `severity=high`, details: `invoice_status`, `event_type`, `next_payment_amount_cents`

---

### C-I03 — HIGH_VALUE_INVOICE_UNPAID

| Field | Value |
|---|---|
| Code | C-I03 |
| Category | invoice |
| Severity | high |
| Trigger | Invoice with status in (UNPAID, PARTIALLY_PAID) — Tier 1 stateless |
| Threshold | `amount_cents` default 50000 ($500) |
| Evaluation window | Single event check |

**Detection logic:** Unpaid or partially-paid invoice amount meets or exceeds the threshold. High-value receivable at risk.

Fire if `next_payment_amount_cents >= amount_threshold`.

**Alert fields produced:** `rule_id=C-I03`, `severity=high`, details: `next_payment_amount_cents`, `threshold_cents`, `invoice_status`

---

## Risk Scoring

Contextual 0–100 score computed for every alert at creation.

```
base = severity_base[severity]           // critical=90, high=70, medium=45, low=20, info=10
amount_boost = 0
  if amount_cents >= 100_000: boost = 15  // $1000+
  if amount_cents >= 50_000:  boost = 10  // $500+
  if amount_cents >= 10_000:  boost = 5   // $100+
if details["score"] exists:
    base = int((base * 0.4) + (rule_score * 0.6))  // blend
result = clamp(base + amount_boost, 0, 100)
```

## Risk Aggregator

Entity-level risk score aggregation. Converts per-alert scores (0–100 int) into a normalized entity risk score (0.0–1.0 float) using recency-weighted averaging. Runs synchronously after alert commit. Must be scoped to a single DB transaction — caller commits or rolls back.

### Recency Weight Bands

| Age | Weight |
|---|---|
| Within 24h | 3.0× |
| 1–7 days | 2.0× |
| 7–30 days | 1.0× |
| Over 30 days | 0.5× |

### Score Categories

| Range | Category |
|---|---|
| < 0.3 | low |
| 0.3–0.6 | medium |
| 0.6–0.8 | high |
| ≥ 0.8 | critical |

### Aggregation Pipeline

```
1. Query Alert rows for entity (employee_id)
   WHERE status NOT IN (dismissed, false_positive) via AlertHistory subquery
   Limit to 12-month window
2. Per alert: compute_risk_score(severity, amount_cents, details) → raw score
3. Per alert: recency_weight(now - created_at) → weight band multiplier
4. aggregate: weighted_sum / (count * max_weight) → clamped [0.0, 1.0]
5. Persist:
   - employees.risk_score (app schema) → UPDATE
   - entity_risk_scores UPSERT (metrics schema)
   - risk_score_history INSERT (metrics schema, append-only)
```

## REST API Contract

All endpoints require JWT authentication. Write operations require `admin` or `owner` role.

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/chirp/rules` | List all rules (optional `?category=` filter) |
| GET | `/api/chirp/rules/{rule_id}` | Rule detail with merchant thresholds vs defaults |
| PUT | `/api/chirp/rules/{rule_id}/thresholds` | Set merchant-specific thresholds |
| DELETE | `/api/chirp/rules/{rule_id}/thresholds` | Reset rule to catalog defaults (must delete DB row, not just invalidate cache) |
| GET | `/api/chirp/config` | All active rules for merchant with effective thresholds |
| POST | `/api/chirp/sweep` | Trigger batch evaluation. Body: `{"since_minutes": 60}`. Returns job_id (async required for large merchants) |
| GET | `/api/chirp/categories` | List all rule categories |
| GET | `/api/chirp/sensitivity` | List sensitivity presets with multipliers |
| POST | `/api/chirp/sensitivity/preview` | Preview adjusted thresholds at a sensitivity level |
| POST | `/api/chirp/sensitivity/estimate` | Estimate closest sensitivity level for given thresholds |
| GET | `/api/chirp/templates` | List available configuration templates |
| POST | `/api/chirp/templates/preview` | Preview template output across all rules |
| POST | `/api/chirp/templates/apply` | Apply template to merchant |
| POST | `/api/chirp/validate` | Validate proposed thresholds before saving |
| GET | `/api/chirp/summary` | Summary of merchant config vs defaults |

### Sensitivity Presets

| Preset | Effect |
|---|---|
| `strict` | Lower thresholds, more alerts |
| `default` | Catalog defaults |
| `relaxed` | Higher thresholds, fewer alerts |
| `minimal` | Highest thresholds, lowest false positive rate |

### Configuration Templates

Industry-specific threshold profiles: `retail_standard`, `food_service`, `high_value`, `high_volume`, `new_merchant`. Each template adjusts thresholds and enable/disable flags across all 37 rules.

## Failure Modes

| Failure | Impact | Required Behavior |
|---|---|---|
| PostgreSQL down | No Tier 2/3 rule evaluation | Fall through to Tier 1 stateless rules only. Threshold loader falls back to catalog defaults. |
| Valkey down | Cache miss on every threshold lookup | Fall through to DB on every request. Correctness maintained, performance degrades. |
| Single rule check error | One rule fails | Per-rule error isolation — log and continue. Other rules must still evaluate. |
| Alert write fails | Alert lost | Per-alert error isolation — log and continue batch. Full batch failure triggers rollback. |
| Fox case creation fails | No investigation case | Best-effort only. Log warning. Never block the alert pipeline. |
| Risk score update fails | Employee risk score stale | Log error. Roll back only the failed metrics write. Alert persistence already committed and unaffected. |
| Threshold reset (DELETE) bug | Stale custom threshold served | The DELETE endpoint must DELETE the `merchant_rule_config` row (or nullify `custom_threshold`), not just invalidate the cache. Known prototype defect. |

## Monitoring

| Metric | Alert Threshold |
|---|---|
| Alerts per merchant per hour | Spike > 3× baseline = possible false positive storm |
| Rule evaluation errors | Any sustained errors = rule implementation bug |
| Threshold cache hit rate | < 50% = Valkey issue or excessive invalidation |
| Batch sweep duration | > 60s for < 1000 transactions = query performance issue |
| Risk update failures | Any = metrics schema issue |

## ILDWAC-Triggered Chirp Rules

> **Architectural direction — not current implementation.** The rule stubs below define Category 11 (ILDWAC cost anomaly) as a reserved rule family. No GRO ticket exists for implementation yet. These rules will be activated after a formal design pass produces the `ledger.ilwac_positions` table and the five-dimension WAC recalculation engine.

The existing 10 rule categories (Categories 1–10) cover behavioral signals derived from transaction events. Category 11 adds a cost-provenance signal layer: anomalies that appear only when the weighted average cost is tracked across the IL(Device/MCP/Port/) dimensions.

### Category 11 — ILDWAC Cost Anomaly Rules

| Rule ID | Name | Category | Severity | Trigger | Status |
|---|---|---|---|---|---|
| C-1101 | DEVICE_WAC_OUTLIER | cost_anomaly | high | WAC for an item on a specific terminal deviates > N sigma from the store-level baseline WAC for that item | Stub — architectural direction |
| C-1102 | PORT_WAC_MISMATCH | cost_anomaly | high | Same item has divergent WAC across Square vs Counterpoint connectors at the same location | Stub — architectural direction |
| C-1103 | MCP_COST_ATTRIBUTION_GAP | cost_anomaly | critical | Cost events authorized by an MCP tool call have no corresponding RIB batch in `ledger.ilwac_positions` | Stub — architectural direction |

#### C-1101 — DEVICE_WAC_OUTLIER *(stub)*

The WAC for a given (item, location, device) triple deviates more than N standard deviations from the merchant's baseline WAC for that (item, location) pair. Indicates a cost event was processed through a terminal with anomalous pricing authority — e.g., a receiving event keyed on a mobile device with a different cost basis than the fixed POS.

**Threshold:** `sigma` — default 2.0 (configurable per merchant via `merchant_rule_config`).
**Data source (future):** `ledger.ilwac_positions` WHERE `(item_id, location_id, device_id)` compared to aggregate over all device_ids.
**Alert type:** `COST_ANOMALY`
**Auto-case creation:** No (reserved for implementation pass).

#### C-1102 — PORT_WAC_MISMATCH *(stub)*

Same item and location, but WAC differs across POS connector (Square vs Counterpoint). Signals that cost events are being routed through connectors with inconsistent cost authorization — the canonical cost basis is ambiguous. This is a structural data quality signal as much as a fraud signal.

**Threshold:** `variance_pct` — default 5.0 (percent difference in WAC across ports triggers the rule).
**Data source (future):** `ledger.ilwac_positions` WHERE `(item_id, location_id)` GROUP BY `pos_port`.
**Alert type:** `COST_ANOMALY`

#### C-1103 — MCP_COST_ATTRIBUTION_GAP *(stub)*

A cost-affecting event was authorized by an MCP tool call (recorded in the ILDWAC provenance vector), but no corresponding RIB batch exists in `ledger.ilwac_positions` for that (item, location, mcp_tool, period) combination. The cost was authorized but not posted — an evidentiary gap in the cost chain.

**Threshold:** None — fires on any unmatched MCP authorization event.
**Data source (future):** Cross-reference `mcp_authorization_log` with `ledger.ilwac_positions` by `mcp_tool_call_id`.
**Alert type:** `COST_ANOMALY`
**Severity:** `critical` — a missing RIB batch breaks the hash chain provenance and must be investigated.

### Implementation Prerequisites for Category 11

Before any C-1101/C-1102/C-1103 rule can be activated, the following must be in place:

1. `ledger.ilwac_positions` table created with columns: `item_id`, `location_id`, `device_id`, `mcp_tool`, `pos_port`, `wac_satoshis`, `rib_batch_hash`, `effective_at`
2. RIB batch pipeline producing JSON batches per domain with SHA-256 seals
3. ILDWAC recalculation engine that updates `ledger.ilwac_positions` on each batch commit
4. Baseline WAC computation (item × location aggregate) seeded in `metrics.metric_baselines`

These prerequisites are tracked under the broader ILDWAC design pass. See `Brain/wiki/cards/ilwac-extended-bitcoin-standard.md` for the full architectural direction.

---

## Known Security Findings (Prototype)

These are documented defects in the prototype that the Go implementation must address:

| ID | Severity | Finding |
|---|---|---|
| P0-CHIRP-01 | Critical | `alerts.details` JSON stores employee IDs and card fingerprints in plaintext. Encrypt using AES-256-GCM. Truncate card_fingerprint to last-4 in details. |
| P0-CHIRP-02 | Critical | Employee IDs and partial card fingerprints appear in structured logs. Log only `rule_id`, `merchant_id`, and alert counts. |
| P1-CHIRP-01 | High | No rate limiting on REST endpoints. Apply: 60/min reads, 10/min writes, 1/min sweep. |
| P1-CHIRP-02 | High | No audit log for threshold changes. Write audit record on every upsert, template apply, and reset. |
| P1-CHIRP-03 | High | No data retention policy. Alerts accumulate without bound. Implement 24-month archive and 12-month Risk Aggregator query limit. |
| P1-CHIRP-06 | High | DELETE threshold endpoint in prototype only clears Valkey cache without deleting the DB row. The Go implementation must delete or nullify the row. |
| P2-CHIRP-02 | Medium | Batch sweep should be async — return a job ID, execute in background worker. |
