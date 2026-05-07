---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
binary: returns
port: 8097
mcp-server: canary-returns
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Returns — Return Processing and Fraud Detection

**Type:** Domain Service — Return Lifecycle + LP Signal Feed  
**Binary:** `cmd/returns` → `:8097`  
**MCP server:** `canary-returns` (9 tools)  
**Depends on:** `raas` (`return_eligible` check; return events appended to chain), `inventory-as-a-service` (restocking on approved returns), `ildwac` (cost adjustment on returned items), `pricing` (`effective_price_at` for refund amount at original purchase timestamp)  
**Feeds:** `hawk` (return fraud signals, auto-flag on score ≥ 70), `fox` (ecom return routing and status)

Returns are the most fraud-prone transaction in retail. A cashier can process a return with no receipt, an incorrect item, an inflated price, or for a friend. An ecom customer can claim non-delivery, return an empty box, or substitute a cheaper item. Return fraud costs US retailers approximately $25B annually — and at the SMB scale Canary serves, it is almost entirely invisible because the merchant has no system to surface the pattern. Canary's returns module addresses this by requiring every return to pass through `raas.return_eligible` (which checks the original transaction chain), applying configurable return policies per merchant, computing a fraud score on every request, and flagging anomalous patterns to hawk automatically.

**Multi-tenant context.** Returns tables (`returns`, `return_authorizations`, `return_fraud_scores`, `return_policy_configs`) live per-tenant in `tenant_{merchant_id}`. Return policies are merchant-specific; fraud scoring uses tenant-scoped customer history. Cross-tenant return fraud pattern detection (organized return-fraud rings operating across multiple merchants) flows through `analytics` schema rollups, surfaced through the Local Market Agent's social-threat signal feed. See `architecture.md` "Multi-Tenant Isolation".

**Optional Features posture.** Returns operates with all Optional Features (per `platform-overview.md`) disabled. The `raas.return_eligible` check, fraud scoring, and policy enforcement all run on internal records. When `BLOCKCHAIN_ANCHOR_ENABLED=true`, return-authorization events are anchored asynchronously, making return decisions externally verifiable for fraud disputes. When `L402_ENABLED=true`, premium return-fraud detection capabilities (real-time pattern correlation across stores) may be paid MCP tools — the core return processing does not depend on Lightning settlement.

---

## Business

### Why Returns Are the Fraud Aperture

The POS has no memory. A cashier presented with a sweater and a story processes the return. There is no system check on whether this sweater was sold to this customer, whether the return window has passed, whether this cashier has processed an unusually high return volume today, or whether this customer is on their fourth return this month. Each of these failure modes is a known fraud vector:

- **No-receipt returns at inflated price:** cashier looks up the current price (which may have increased since original purchase) and refunds at the higher amount.
- **Friend and family fraud:** cashier processes a return for merchandise that was never purchased at this store.
- **Wardrobing:** customer buys, uses, returns. Concentrated in apparel and electronics.
- **Ecom empty-box fraud:** customer claims the shipment arrived empty and requests a refund.
- **Cashier collusion:** cashier processes a return for a customer who surrenders nothing and splits the refund.

The returns module cannot prevent all of these — a determined colluding cashier will find a path. What it does is make every fraud vector leave a trace, ensure anomalies surface to hawk in real time, and create the evidence record for LP investigation.

### Return Types

| Type | Description | Receipt Required | Manager Override Required |
|---|---|---|---|
| `receipt_return` | Original transaction in RaaS chain, within return window | Yes | No (unless fraud score ≥ 90) |
| `no_receipt_return` | No transaction found; cash value estimated | No | Yes — always |
| `ecom_return` | Online purchase; may return in-store or by mail | Order reference | Only if fraud signals present |
| `exchange` | Return + new transaction; both chain-linked | Yes | No |
| `warranty_return` | Outside standard window; vendor RMA required | No | Yes |

### Fraud Scoring Model

Every return request receives a fraud score computed by `check_return_eligible`. The score is additive across signals. Signals are defined in `return_fraud_signals`.

| Signal | Score Delta | Trigger |
|---|---|---|
| No receipt | +40 | `return_type = 'no_receipt_return'` |
| Return > 30 days from purchase | +20 | `raas.return_eligible` returns window expired |
| Return value > 3× average transaction for this merchant | +25 | `total_refund_sats > 3 × avg_transaction_sats` |
| Same customer 3+ returns in 30 days | +35 | Count of `return_requests` for this associate or customer in rolling 30-day window |
| Return item not in original transaction | +50 | Item ID not found in original `raas_events` payload |
| Ecom-to-store return for cash (not original payment method) | +30 | `return_type = 'ecom_return'` AND `refund_method = 'cash'` |

**Enforcement thresholds:**

| Score | Action |
|---|---|
| < 70 | Process normally; signals logged to `return_fraud_signals` |
| ≥ 70 | Auto-flag to hawk; continue processing unless manager overrides to deny |
| ≥ 90 | Auto-deny; require LP approval to proceed; return status set to `denied` |

**Return item not in original transaction (+50) auto-denies immediately** — this is the strongest fraud signal and has no legitimate normal-operations explanation. Score reaches 90 on this signal alone for any no-receipt return.

### Business Rules

1. `raas.return_eligible` is the ONLY authority for return window decisions. No other service may determine whether a return is within the allowed window. Returns that bypass this check are an audit failure.
2. `no_receipt_return` requires `manager_override = true` in the return record. The system does not auto-approve no-receipt returns regardless of fraud score.
3. Returns with `fraud_score > 70` are LP evidence and subject to 7-year retention. Records below 70 follow standard financial retention (also 7 years, but for different reasons).
4. `raas.return_eligible` is called before `create_return_request` writes any row. If `return_eligible` is unavailable, returns processing halts — the LP integrity of the return record cannot be guaranteed without chain verification. This is intentional: returns are not a real-time life-safety operation. A brief outage is acceptable.
5. The refund amount for `receipt_return` is always computed from `pricing.effective_price_at(item_id, timestamp=original_transaction_time)` — not the current price, not the price the cashier enters. The cashier cannot manually specify a refund price on receipt returns.

---

## Technical

### Service Boundaries

Returns owns two table groups. No other service writes to these tables.

| Group | Tables | Purpose |
|-------|--------|---------|
| Return Requests | `return_requests` | Full return lifecycle and fraud state |
| Fraud Signals | `return_fraud_signals` | Per-return signal log feeding hawk |

### Data Model

#### `return_requests`

```sql
CREATE TYPE return_type AS ENUM (
    'receipt_return', 'no_receipt_return', 'ecom_return', 'exchange', 'warranty_return'
);

CREATE TYPE return_status AS ENUM ('pending', 'approved', 'denied', 'completed');

CREATE TABLE return_requests (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id               UUID NOT NULL REFERENCES merchants(id),
    location_id               UUID NOT NULL,
    return_type               return_type NOT NULL,
    original_transaction_id   TEXT,             -- NULL for no_receipt_return; vendor ref for warranty
    original_raas_sequence    BIGINT,           -- sequence in the RaaS chain of the original txn
    raas_namespace            TEXT,             -- namespace the original transaction is chained under
    items                     JSONB NOT NULL,   -- [{item_id, qty, unit_price_sats, reason}]
    total_refund_sats         BIGINT NOT NULL CHECK (total_refund_sats >= 0),
    refund_method             TEXT NOT NULL,    -- "original_payment" | "store_credit" | "cash"
    status                    return_status NOT NULL DEFAULT 'pending',
    fraud_score               NUMERIC(5,2) NOT NULL DEFAULT 0,
    hawk_flagged              BOOLEAN NOT NULL DEFAULT false,
    associate_id              TEXT NOT NULL,
    manager_id                TEXT,             -- set on manager_override
    manager_override          BOOLEAN NOT NULL DEFAULT false,
    override_reason           TEXT,
    lp_approval_required      BOOLEAN NOT NULL DEFAULT false,  -- true when score ≥ 90
    processed_at              TIMESTAMPTZ,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_return_requests_merchant ON return_requests(merchant_id, created_at DESC);
CREATE INDEX idx_return_requests_merchant_status ON return_requests(merchant_id, status);
CREATE INDEX idx_return_requests_associate ON return_requests(associate_id, created_at DESC);
CREATE INDEX idx_return_requests_hawk ON return_requests(merchant_id, hawk_flagged)
    WHERE hawk_flagged = true;
CREATE INDEX idx_return_requests_original_txn ON return_requests(original_transaction_id)
    WHERE original_transaction_id IS NOT NULL;
```

**`items` JSONB schema:**

```json
[
  {
    "item_id": "uuid",
    "qty": 1,
    "unit_price_sats": 2000000,
    "original_unit_price_sats": 2000000,
    "reason": "defective | changed_mind | wrong_size | not_as_described | other",
    "condition": "new | opened | damaged | missing_parts"
  }
]
```

`unit_price_sats` is the actual refund price (from `pricing.effective_price_at` at the original transaction timestamp). `original_unit_price_sats` is what the customer paid, as read from the RaaS chain payload. These must match on `receipt_return` — a discrepancy is a fraud signal.

#### `return_fraud_signals`

```sql
CREATE TABLE return_fraud_signals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id    UUID NOT NULL REFERENCES return_requests(id),
    signal_type  TEXT NOT NULL,      -- matches the signal names in the fraud scoring model
    signal_value TEXT NOT NULL,      -- the value that triggered the signal (e.g., "35 days since purchase")
    weight       NUMERIC(5,2) NOT NULL,  -- the score delta this signal contributed
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_return_fraud_signals_return ON return_fraud_signals(return_id);
CREATE INDEX idx_return_fraud_signals_type ON return_fraud_signals(signal_type, created_at DESC);
```

One row per signal per return. A return with three active signals has three rows. This structure feeds hawk's pattern analysis — it can query by `signal_type` across all returns to detect systematic fraud patterns (e.g., a spike in `return_item_not_in_original_txn` signals at a specific location).

### Fraud Score Computation — `check_return_eligible`

The fraud score is computed in the `check_return_eligible` handler before any row is written. The computation is stateless given the return request parameters and the RaaS chain lookup.

```
check_return_eligible(return_request):

score = 0
signals = []

1. Call raas.return_eligible(namespace, original_transaction_id)
   → NOT ELIGIBLE (window expired):
       score += 20
       signals.append({type: "return_window_expired", value: "N days since purchase", weight: 20})
   → NOT FOUND (no transaction in chain):
       if return_type != 'no_receipt_return': return 422 (transaction not found)

2. If no_receipt_return:
       score += 40
       signals.append({type: "no_receipt", value: "no_receipt_return", weight: 40})

3. Check total_refund_sats vs merchant avg:
   SELECT AVG(total_refund_sats) FROM return_requests
   WHERE merchant_id = $merchant_id AND created_at > NOW() - INTERVAL '90 days'
   IF total_refund_sats > 3 × avg:
       score += 25
       signals.append({type: "high_value_return", value: "N× avg", weight: 25})

4. Check rolling return count for associate_id:
   SELECT COUNT(*) FROM return_requests
   WHERE associate_id = $associate_id AND created_at > NOW() - INTERVAL '30 days'
   IF count >= 3:
       score += 35
       signals.append({type: "high_return_frequency", value: "N returns in 30 days", weight: 35})

5. Validate items against original transaction (if original_transaction_id present):
   Read original transaction payload from raas_events
   FOR each item in return_request.items:
       IF item.item_id NOT IN original_transaction.items:
           score += 50
           signals.append({type: "item_not_in_original_txn", value: item.item_id, weight: 50})
           BREAK  -- one mismatch is enough; don't need to enumerate all

6. Ecom cash refund check:
   IF return_type == 'ecom_return' AND refund_method == 'cash':
       score += 30
       signals.append({type: "ecom_cash_refund", value: "ecom→cash", weight: 30})

Return: {eligible: true, fraud_score: score, signals: signals, auto_deny: score >= 90}
```

The returned score and signals are passed to `create_return_request` which writes them atomically with the return row.

### API Contract

All routes require JWT auth except `/returns/healthz` and `/returns/readyz`.

```
GET  /returns/healthz                          → 200
GET  /returns/readyz                           → 200 | 503
POST /returns/check-eligible                   → 200 {eligible, fraud_score, auto_deny, signals}
POST /returns                                  → 201 {return_id, status}
PUT  /returns/{id}/approve                     → 200 {return}
PUT  /returns/{id}/deny                        → 200 {return}
POST /returns/{id}/refund                      → 201 {refund_id}
GET  /returns/{merchant_id}/history            → 200 [{return}]
GET  /returns/{return_id}/fraud-signals        → 200 [{signal}]
PUT  /returns/{id}/manager-override            → 200 {return}
GET  /returns/{merchant_id}/analytics          → 200 {return_analytics}
```

### MCP Tool Surface — `canary-returns` (9 tools)

| Tool | Input | Output | SLA | Notes |
|------|-------|--------|-----|-------|
| `create_return_request` | `merchant_id, location_id, return_type, original_transaction_id?, items, refund_method, associate_id` | `{return_id, status, fraud_score, auto_deny}` | <2s P99 | Calls `raas.return_eligible` + `pricing.effective_price_at` internally; writes return + signals atomically |
| `check_return_eligible` | `namespace, original_transaction_id, return_type, items, associate_id, total_refund_sats` | `{eligible, fraud_score, auto_deny, signals}` | <1s P99 | Read-only; does not write state; safe to call before `create_return_request` for UI pre-check |
| `approve_return` | `return_id, approver_id` | `{return}` | <500ms | Status: `pending` → `approved`; triggers inventory restock |
| `deny_return` | `return_id, denier_id, reason` | `{return}` | <500ms | Status: `pending` → `denied` |
| `process_refund` | `return_id, payment_reference` | `{refund_id}` | <500ms | Status: `approved` → `completed`; requires `approved` status |
| `get_return_history` | `merchant_id, location_id?, from?, to?, status?` | `[]{return}` | <500ms | Cursor-based; max 200 rows |
| `get_fraud_signals` | `return_id` | `[]{signal}` | <100ms | Reads `return_fraud_signals` for LP review |
| `override_with_manager` | `return_id, manager_id, override_reason` | `{return}` | <500ms | Sets `manager_override = true`; required for `no_receipt_return` and score ≥ 90 LP override |
| `get_return_analytics` | `merchant_id, from, to` | `{return_count, fraud_rate, avg_score, top_signals}` | <2s | Aggregation over `return_requests` + `return_fraud_signals` |

### Trade-off: Pre-Check vs. Inline Score Computation

**Option A (chosen): Score computed inside `create_return_request`, with a separate `check_return_eligible` read-only tool for UI pre-checks.** This ensures the fraud score written to the return record is always computed at write time from the current state of the chain — it cannot be stale or replayed. The `check_return_eligible` call before the POS screen submits is advisory; the definitive score is always recomputed at `create_return_request` time. This adds ~1s to the `create_return_request` path (two external service calls: raas + pricing) but guarantees data integrity.

**Option B: Accept the pre-check score as authoritative, skip recomputation on write.** Faster write path, but vulnerable to a race condition where the state changes between pre-check and write (e.g., the same customer processes a second return in the window between the pre-check and the write). The +35 "high return frequency" signal would be missed. Rejected.

**The cost of Option A:** `create_return_request` P99 is ~2s — measurably slower than the other write tools in the module spine. This is acceptable for an LP-critical path. A cashier processing a return can wait 2 seconds.

### Go Implementation Notes

- `create_return_request` uses a `pgx` transaction that spans: (1) the `raas.return_eligible` read call (HTTP to raas service — outside the DB transaction), (2) the `pricing.effective_price_at` call (HTTP to pricing service — also outside), (3) INSERT into `return_requests`, (4) batch INSERT into `return_fraud_signals`, (5) conditional `hawk.flag_return` call if `fraud_score ≥ 70`. Steps 3 and 4 are inside the DB transaction; steps 1, 2, and 5 are outside. If steps 1 or 2 fail, do not open the transaction. If step 5 fails, commit the DB transaction anyway and log the hawk failure.
- Auto-deny at `fraud_score ≥ 90`: set `status = 'denied'` and `lp_approval_required = true` at INSERT time. The return is created in denied state — the LP agent must explicitly call `override_with_manager` to reverse this.
- `approve_return` triggers an inventory restock via an HTTP call to `inventory-as-a-service`. This is a best-effort call: if inventory service is unavailable, `approve_return` still completes and logs the restock failure for manual reconciliation.
- The rolling return frequency check in fraud scoring uses an in-handler query, not a Valkey counter. For the current ICP (low-volume stores), the query is fast enough. If a merchant exceeds ~100 returns/day, add a Valkey counter with a sliding window.

---

## Ops

### SLA Commitments

| Operation | P50 | P99 | Hard Limit | Breach Action |
|-----------|-----|-----|------------|---------------|
| `check_return_eligible` | <300ms | <1s | 3s | Alert; raas or pricing dependency may be slow |
| `create_return_request` | <600ms | <2s | 5s | Alert |
| `approve_return` | <100ms | <500ms | 2s | Alert |
| `get_fraud_signals` | <30ms | <100ms | 500ms | Alert |
| `get_return_analytics` | <500ms | <2s | 10s | Alert; aggregation query |

### Health Endpoints

```
GET /returns/healthz
→ 200 { "status": "ok" }
Shallow liveness. No DB or external service check.

GET /returns/readyz
→ 200 { "status": "ok", "db_ok": true, "raas_reachable": true, "pricing_reachable": true }
→ 503 if DB unreachable.
Note: raas and pricing reachability are checked at readyz time but do not cause 503
on their own — the module can boot in a degraded state where it accepts return requests
and returns 503 only when `raas.return_eligible` is called and fails.
```

### Failure Modes

| Failure | Behavior | Recovery |
|---------|----------|---------|
| `raas` unreachable at `create_return_request` | Return 503; do not write partial return record. | Alert. Manual return processing (paper trail) until raas recovers. |
| `pricing` unreachable at `create_return_request` | Return 503. | Same as raas failure — refund price cannot be computed accurately. |
| `hawk` call failure on score ≥ 70 | Return record is committed. Hawk failure logged and metered. | Alert on hawk failure rate > 5%. Manual hawk review queue for missed signals. |
| `inventory-as-a-service` failure on `approve_return` | Approval committed. Restock failure logged for manual reconciliation. | Alert. Inventory count correction required. |
| DB unreachable | All tools return 503. | Auto-recovery via pgx pool. |

### Graceful Shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
srv.Shutdown(ctx) // 30s drain
pool.Close()
```

In-flight `create_return_request` calls that have opened a DB transaction but not yet committed will roll back cleanly. The external service calls (raas, pricing) are read-only and safe to abandon mid-flight.

### Valkey Key Space

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `returns:freq:{associate_id}` | 1800s | Optional: high-volume store optimization for rolling return frequency count; NOT implemented at launch |

No Valkey usage at launch. The rolling frequency check queries the DB directly. Add Valkey counters if a merchant's return volume justifies it.

### Monitoring

Alert on:
- `create_return_request` P99 > 2s sustained for 1 minute
- hawk signal call failure rate > 5% over 5 minutes
- `return_requests WHERE lp_approval_required = true AND status = 'denied' AND created_at < NOW() - INTERVAL '24 hours'` — LP queue is backing up
- `return_requests WHERE hawk_flagged = true AND status = 'pending' AND created_at < NOW() - INTERVAL '4 hours'` — flagged returns awaiting manager review

---

## Compliance

### PII Classification

| Field | Table | Classification | Required Treatment |
|-------|-------|---------------|-------------------|
| `associate_id` | `return_requests` | Internal | Employee identifier. Do not expose in merchant-facing analytics exports. |
| `manager_id` | `return_requests` | Internal | Same treatment as `associate_id`. |
| `items` JSONB | `return_requests` | Operational | May contain item descriptions that constitute personal property context. Do not include in aggregate analytics responses. |
| `override_reason` | `return_requests` | Sensitive — Legal | Manager override reasons may contain dispute context. Restrict to LP and management roles. |

### LP Evidence Retention

Returns with `fraud_score > 70` are LP evidence. The following applies to all such records:

- Minimum retention: 7 years from transaction date
- `return_fraud_signals` rows linked to these returns: same retention as the parent return
- No deletion path in application code. Deletion requires DBA agent + Legal & Compliance review.
- These records feed hawk's LP investigation queue. Any deletion without hawk acknowledgement is a compliance failure.

### Audit Posture

Every return — regardless of fraud score — generates a `returns.return_processed` event appended to the RaaS chain via `raas.append_event`. This event includes the return ID, status, fraud score, and whether manager override was applied. The chain record is tamper-evident and is the authoritative audit log. The `return_requests` table is the operational record; the RaaS chain is the evidentiary record.

### Retention

| Data | Minimum Retention | Authority |
|------|------------------|-----------|
| `return_requests` (all) | 7 years | Financial record |
| `return_requests` (fraud_score > 70) | 7 years minimum; LP hold may extend | LP evidence |
| `return_fraud_signals` | 7 years | LP evidence chain |

---

## Related SDDs

- `raas.md` — `return_eligible` is the authoritative return window check; return events appended to chain
- `pricing.md` — `effective_price_at` computes refund amount at original transaction timestamp
- `inventory-as-a-service.md` — receives restock signal on `approve_return`
- `ildwac.md` — cost adjustment on returned inventory feeds into cost basis recalculation
- `hawk.md` — receives fraud signals on score ≥ 70; LP investigation queue
- `fox.md` — ecom return routing; `ecom_return` type originates here
- `identity.md` — `merchants` and `locations` FK chain
