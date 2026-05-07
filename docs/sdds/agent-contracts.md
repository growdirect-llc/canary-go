---
spec-version: 1.0
target-implementation: Go
stack: PostgreSQL 17 + pgx + sqlc | Chi HTTP | REST | go-redis | pgvector-go
status: handoff-ready
updated: 2026-04-29
license: Apache-2.0
copyright: "Copyright (c) 2026 GrowDirect LLC"
---

# Agent Smart Contracts

## Purpose

The Canary Go platform is operated by an autonomous agent network. Agents make decisions, write records, and trigger downstream actions — often without a human in the loop. The question every audit asks is: _which agent decided what, under what authority, by what rule?_

An **agent smart contract** is the formal answer to that question for any given workflow. It is the agent-side equivalent of a REST API spec: a binding interface definition between two parties in the agent network that specifies inputs, outputs, SLA commitments, the scope of autonomous action, and the conditions that trigger escalation to a human. Every contract execution leaves a traceable audit trail anchored in the `actor_type` field on every DB write.

This SDD defines the contract schema, four reference contracts covering Canary's core workflows, the enforcement model, and the MCP tool pattern that activates each contract.

**What this SDD is not:** a topology map of the full agent network, a spec for the RaaS namespace resolver, or a Go implementation guide for L402 middleware. Those are forward-referenced at the end of this document.

**Tenant context.** Every contract execution operates within a single tenant scope. Contracts carry `merchant_id` in the `actor_type` audit trail; DB writes happen in the contracted tenant's schema (`tenant_{merchant_id}`); cross-tenant contract execution is forbidden. See `architecture.md` "Multi-Tenant Isolation" for the canonical schema-per-tenant pattern.

**Optional features.** Contract execution is independent of L402, ILDWAC, blockchain anchoring, and vendor smart contracts. The contract schema below describes the audit trail every execution produces; when Optional Features (per `platform-overview.md`) are enabled, additional fields appear on the audit trail (L402 macaroon hash, ILDWAC cost packet ID, anchor receipt hash) — but the contract itself does not depend on those features being on.

---

## Agent Network — Context

The network has three tiers. Understanding the tier structure is prerequisite for reading the escalation paths in each contract.

### Tier 1 — Controller

Single agent with full network view. The founder's primary interface for status, escalation, and cross-domain coordination. The Controller is the terminal escalation point before the human founder. It does not own any domain — it routes, monitors SLA compliance, and surfaces decisions that exceed any domain agent's autonomous scope.

### Tier 2 — L3 Domain Agents

Twenty-seven agents, one per module in the Canary Go 13-module spine plus Foundation modules. Each carries dual authority: business domain knowledge and technical ownership of its module's code, schema, and MCP tools. Domain agents are the primary contract executors for business workflows.

| Domain | Module Code | Primary Contract Role |
|---|---|---|
| Loss Prevention | Q | Alert triage, escalation decisions |
| Fox Case Management | fox-Q | Evidence assembly, case progression |
| Analytics | A | Baseline computation, anomaly signaling |
| Chirp Detection | C | Rule evaluation, alert creation |
| Hawk Ops | hawk-Q | Wizard FSM advancement, card factory |
| (remaining 22 domains) | T, N, R, P, S, D, J, F, L, W, + Foundation | per-module contracts |

### Tier 3 — Infrastructure Agents

Agents that operate horizontally across all domains:

| Agent | Role |
|---|---|
| Controller | Network-wide oversight, SI gating |
| DBA | Schema migrations, query health |
| Storage | File systems, evidence file handling |
| Data Governance | PII classification, retention policy |
| Legal & Compliance | Civil referral decisions, compliance obligations |
| Security | Access audits, anomaly detection on agent behavior |
| Accountant / CPA | Financial record validation |
| Cloud Ingress/Egress | External API calls, webhook management |
| Network | Connectivity, retry policy |
| Scheduling | Cron triggers, SLA monitoring, breach escalation |
| MCP | Tool catalog management, JWT issuance |

### Memory Substrate

Every agent session is instantiated via `memory_recall()` against the `growdirect_memory` database (pgvector, `qwen3-embedding:8b`). Agents do not operate from hardcoded knowledge — they load domain context from the memory bus at session start. This is a hard requirement, not a nice-to-have: an agent that skips context loading is operating blind.

```
memory_recall("canary alert triage LP domain rules")
memory_recall("fox case evidence chain hash integrity")
context_assemble(topic="canary retail spine")
```

---

## Contract Schema

Every agent smart contract carries the following fields. All fields are required unless marked optional.

| Field | Type | Description |
|---|---|---|
| `contract_id` | string | Unique identifier: `agent:<module>.<action>` format. Example: `agent:Q-lp.triage_alert` |
| `parties.initiator` | string | Who triggers the contract. Can be an event source, another agent, or a scheduled job. |
| `parties.executor` | string | Which agent fulfills the contract. Uses `agent:<module>-<role>` format. |
| `trigger` | string | The event or condition that activates this contract. Must be observable and unambiguous. |
| `input_schema` | object | Fields, types, and constraints the executor receives. See per-contract tables below. |
| `output_schema` | object | Fields, types, and guarantees the executor produces. See per-contract tables below. |
| `sla.triage_minutes` | integer | Maximum time from trigger to executor acknowledgment. |
| `sla.resolution_hours` | integer | Maximum time from acknowledgment to output delivery or escalation. |
| `sla.escalation_hours` | integer | Time after which automatic escalation fires if resolution has not occurred. |
| `autonomous_scope` | list | Specific decisions and DB writes the executor may make without human input. |
| `escalation_path` | list | Ordered chain: first-level escalation through to human. Conditions that trigger each step. |
| `actor_type` | string | Identity string written to every DB record produced during this contract. Format: `agent:<module>-<role>`. |
| `evidence_requirement` | list | Records that must be written to the audit trail for this execution to be considered complete. |

### Contract ID Convention

```
agent:<module-code>-<role>.<action_verb>_<object>

Examples:
  agent:Q-lp.triage_alert
  agent:fox-Q.investigate_case
  agent:analytics.update_baselines
  agent:controller.gate_service_introduction
```

The `module-code` matches the module spine codes (Q, fox-Q, A, C, hawk-Q, etc.). The `role` suffix clarifies which function within that module the executor is performing (`lp` = loss prevention judgment, `Q` = Q-domain subordinate, etc.). The `action_verb_object` is a snake_case description of what the contract does.

---

## Contract A — Alert Triage

**Contract ID:** `agent:Q-lp.triage_alert`

The foundational contract. Every alert created by Chirp passes through this contract before a human ever sees it. The Q agent's job is to separate signal from noise at scale so the merchant's attention lands on real events, not false positives.

### Parties

| Role | Party |
|---|---|
| Initiator | Chirp Engine (alert creation event) |
| Executor | Q Loss Prevention domain agent |

### Trigger

Alert row created in `app.alerts` with status `new`. The Chirp pipeline writes the row; the alert creation event activates this contract.

### Input Schema

| Field | Type | Constraints |
|---|---|---|
| `alert_id` | uuid | Must exist in `app.alerts` |
| `rule_code` | string | Must match a known Chirp rule family (e.g. `C.002`, `E.101`) |
| `severity` | string | Enum: `low` / `medium` / `high` / `critical` |
| `merchant_id` | uuid | Must exist in `app.merchant_sources` |
| `evidence_ids` | uuid[] | One or more records in `app.alert_evidence`; minimum 1 |

### Output Schema

| Field | Type | Guarantees |
|---|---|---|
| `triage_decision` | string | Enum: `NOISE` / `SIGNAL` / `ESCALATE` |
| `confidence_score` | float | Range 0.0–1.0; must be set even for NOISE decisions |
| `recommended_action` | string | Human-readable next step; required for SIGNAL and ESCALATE decisions |
| `evidence_summary` | string | Optional — brief characterization of the evidence reviewed |

### SLA

| Phase | Bound |
|---|---|
| Triage (trigger → acknowledgment) | 15 minutes |
| Resolution (close or escalate) | 4 hours |
| Escalation fires automatically if resolution has not occurred | 4 hours post-trigger |

### Autonomous Scope

The Q agent may, without human input:

- Mark alerts `NOISE` and transition status to `dismissed` (actor: `agent:Q-lp`)
- Add supplemental evidence records to `app.alert_evidence`
- Transition alert status from `new` to `agent_reviewing`
- Transition alert status from `agent_reviewing` to `closed` (on NOISE) or `human_escalated` (on ESCALATE)
- Write confidence score and recommended action to the alert record

The Q agent may **not** autonomously:

- Transition to `case_opened` (requires Fox contract activation, which is a separate contract execution)
- Set status to `referred_to_le`
- Modify any existing alert history row

### Escalation Path

| Step | Condition | Target |
|---|---|---|
| 1 | ESCALATE decision or no resolution within 4 hours | Q domain agent re-evaluates with updated context |
| 2 | Still unresolved after 4 hours | Controller — surfaces to merchant dashboard as requiring human review |
| 3 | Legal or civil referral warranted per Q agent judgment | Founder — Controller cannot authorize a civil referral autonomously |

### Actor Type

`agent:Q-lp` — written to every `alert_history` row created during this contract execution.

### Evidence Requirement

| Record | Table | Required Fields |
|---|---|---|
| Initial status transition (`new` → `agent_reviewing`) | `app.alert_history` | `alert_id`, `status = agent_reviewing`, `actor = agent:Q-lp`, `created_at` |
| Triage decision | `app.alert_history` | `alert_id`, `status = closed OR human_escalated`, `actor = agent:Q-lp`, `notes` containing `confidence_score` and `triage_decision` |

Both records must exist before the contract is considered resolved. A triage decision with no `agent_reviewing` history entry is an evidence gap and will fail SLA audit.

---

## Contract B — Fox Case Investigation

**Contract ID:** `agent:fox-Q.investigate_case`

When Q elevates an alert to a case, the Fox Case Management agent takes ownership. Its job is evidence assembly: gather what can be gathered autonomously, hash-chain it, and produce a recommendation that either closes the case or sends it to a human investigator or legal escalation.

### Parties

| Role | Party |
|---|---|
| Initiator | Q agent (case opened from alert) |
| Executor | Fox Case Management domain agent |

### Trigger

`fox_cases` row created with status `OPEN` and a linked `alert_ids` array.

### Input Schema

| Field | Type | Constraints |
|---|---|---|
| `case_id` | uuid | Must exist in `fox.fox_cases` |
| `alert_ids` | uuid[] | Minimum 1; all must exist in `app.alerts` |
| `merchant_id` | uuid | Must exist in `app.merchant_sources` |
| `evidence_type` | string | Enum: `transaction` / `employee` / `video` / `document` / `system` |
| `escalation_threshold` | float | 0.0–1.0; confidence score above which Fox escalates to human review |

### Output Schema

| Field | Type | Guarantees |
|---|---|---|
| `case_id` | uuid | Same as input; confirms which case this output applies to |
| `evidence_chain_hash` | string | SHA-256 hash of the full evidence chain as of output time |
| `recommendation` | string | Enum: `CLOSE` / `HUMAN_REVIEW` / `CIVIL_REFERRAL` |
| `evidence_record_count` | integer | Count of records appended during this execution |
| `reasoning` | string | Required for `HUMAN_REVIEW` and `CIVIL_REFERRAL`; may be empty for `CLOSE` |

### SLA

| Phase | Bound |
|---|---|
| Initial evidence assembly | 30 minutes from trigger |
| Recommendation produced | 8 hours from trigger |
| Escalation fires automatically | 8 hours post-trigger if no recommendation |

### Autonomous Scope

The Fox agent may, without human input:

- Append evidence records to `fox.fox_evidence` (INSERT-only — no UPDATE or DELETE)
- Link transactions to the case via `fox.fox_case_transactions`
- Link subjects of interest to the case via `fox.fox_subjects`
- Compute and record the evidence chain hash
- Update case status to `EVIDENCE_ASSEMBLED` or `AGENT_REVIEWING`
- Produce a `CLOSE` recommendation and transition case to `CLOSED`

The Fox agent may **not** autonomously:

- Produce a `CIVIL_REFERRAL` recommendation without Controller review
- Delete or modify any existing evidence record (the INSERT-only constraint is enforced at the DB trigger level — this is not just policy, it is structural)
- Transition case to `REFERRED_TO_LE`

### Escalation Path

| Step | Condition | Target |
|---|---|---|
| 1 | `HUMAN_REVIEW` recommendation | Q agent — notifies merchant LP officer for manual case review |
| 2 | `CIVIL_REFERRAL` recommendation | Controller — reviews civil referral conditions before surfacing to founder |
| 3 | Controller confirms civil referral warranted | Legal & Compliance infrastructure agent → Founder |
| 4 | No recommendation within 8 hours | Controller — SLA breach alert |

### Actor Type

`agent:fox-Q` — written to every `fox.fox_evidence`, `fox.fox_case_timeline`, and `fox.fox_case_actions` row created during this execution.

### Evidence Requirement

| Record | Table | Required Fields |
|---|---|---|
| Each evidence item appended | `fox.fox_evidence` | `case_id`, `evidence_type`, `actor_id = agent:fox-Q`, `created_at`, `hash` |
| Final recommendation | `fox.fox_case_timeline` | `case_id`, `event = recommendation_issued`, `actor_id = agent:fox-Q`, `meta_data` containing recommendation and reasoning |
| Evidence chain hash | `fox.fox_cases` | `evidence_chain_hash`, `hash_updated_at` |

The DB trigger on `fox.fox_evidence` blocks any UPDATE or DELETE. If the trigger fires an error during this contract execution, the execution must halt and report to the Controller — do not retry around the trigger constraint.

---

## Contract C — Analytics Baseline Update

**Contract ID:** `agent:analytics.update_baselines`

Detection rules are only as good as the baselines they compare against. This contract keeps baselines current without requiring human scheduling or oversight. It runs on a schedule, operates entirely autonomously unless data quality fails, and emits anomaly signals back into the Chirp pipeline.

### Parties

| Role | Party |
|---|---|
| Initiator | Scheduling infrastructure agent (cron trigger) |
| Executor | Analytics domain agent |

### Trigger

Scheduled job fires at merchant off-hours (configurable per merchant timezone). The Scheduling agent creates a `scheduled_job_log` entry with status `TRIGGERED` before handing off to the Analytics agent.

### Input Schema

| Field | Type | Constraints |
|---|---|---|
| `merchant_id` | uuid | Must exist in `app.merchant_sources` |
| `lookback_days` | integer | Minimum 7; maximum 90; default 30 |
| `metric_types` | string[] | Subset of: `refund_rate`, `void_rate`, `after_hours_txn`, `employee_variance`, `shrink_trend`, `transfer_loss` |

### Output Schema

| Field | Type | Guarantees |
|---|---|---|
| `baseline_record_ids` | uuid[] | IDs of all baseline records written or updated during this execution |
| `updated_at` | timestamp | Execution completion time |
| `anomaly_signals` | object[] | Zero or more signals; each: `{metric_type, signal_strength, alert_recommended}` |
| `partial` | boolean | True if execution completed fewer than all requested metric types |
| `partial_reason` | string | Required if `partial = true`; explains which metric types were skipped and why |

### SLA

| Phase | Bound |
|---|---|
| Complete all baseline computations | 2 hours from trigger |
| Partial completion report | If incomplete at 2 hours, report partial results and reschedule |
| Escalation fires | If no output (partial or complete) within 2 hours |

Partial completion is acceptable — it is not a failure state as long as it is reported accurately. Silence is a failure state.

### Autonomous Scope

The Analytics agent may, without human input:

- Read all transaction records for the merchant within the lookback window
- Compute and write all baseline records (INSERT or UPDATE)
- Emit anomaly signals to the Chirp engine's signal intake queue
- Reschedule a follow-up job for skipped metric types
- Mark the `scheduled_job_log` entry as `COMPLETE` or `PARTIAL_COMPLETE`

The Analytics agent may **not** autonomously:

- Write alerts directly — anomaly signals are emitted to Chirp, which evaluates them against rules and creates alerts via Contract A
- Modify merchant configuration (detection thresholds, notification preferences)
- Escalate to the founder without passing through the Controller

### Escalation Path

| Step | Condition | Target |
|---|---|---|
| 1 | Data quality issue prevents any baseline computation | Analytics agent reports to Controller with specific error |
| 2 | Controller cannot resolve automatically (e.g. source data corruption) | Controller surfaces to merchant with a data quality alert |
| 3 | No output within 2 hours (no partial report, no completion) | Scheduling agent fires SLA breach; Controller notified |

### Actor Type

`agent:analytics` — written to `scheduled_job_log` entries and baseline records created during this execution.

### Evidence Requirement

| Record | Table | Required Fields |
|---|---|---|
| Job start | `scheduled_job_log` | `job_id`, `merchant_id`, `status = RUNNING`, `actor = agent:analytics`, `started_at` |
| Job completion | `scheduled_job_log` | `job_id`, `status = COMPLETE OR PARTIAL_COMPLETE`, `completed_at`, `duration_ms`, `baseline_record_count`, `anomaly_signal_count` |

The `scheduled_job_log` entry must be closed (non-RUNNING status) before the contract is considered complete. An open RUNNING entry after the SLA window is treated as a missing execution.

---

## Contract D — Service Introduction Gate

**Contract ID:** `agent:controller.gate_service_introduction`

The only contract in the network that requires human sign-off before the executor may issue approval. A domain agent claiming readiness for service introduction presents its hardening checklist to the Controller. The Controller verifies, then surfaces to the founder. The founder approves. No agent may issue SI approval autonomously — this is a structural constraint, not a soft guideline.

### Parties

| Role | Party |
|---|---|
| Initiator | Domain agent (any module claiming SI readiness) |
| Executor | Controller agent |

### Trigger

Domain agent reports all hardening checklist items complete and files an SI gate request with the Controller.

### Input Schema

| Field | Type | Constraints |
|---|---|---|
| `module_id` | string | Module spine code (e.g. `Q`, `fox-Q`, `A`, `C`) |
| `checklist_results` | object | Key: checklist item ID; Value: `{passed: bool, evidence_path: string}` |
| `sla_baseline` | object | `{p50_ms, p95_ms, p99_ms, error_rate_pct}` — measured over minimum 72-hour window |
| `runbook_path` | string | Path to runbook in repo — must exist and be non-empty |
| `dependency_status` | object | Key: dependency name; Value: `{status: UP|DEGRADED|DOWN, verified_at: timestamp}` |

### Output Schema

| Field | Type | Guarantees |
|---|---|---|
| `gate_decision` | string | Enum: `APPROVED` / `REJECTED` |
| `rejection_reasons` | string[] | Empty if APPROVED; one entry per failed checklist item if REJECTED |
| `approved_by` | string | `founder` — cannot be any other value; set only on APPROVED |
| `approved_at` | timestamp | Set only on APPROVED |
| `remediation_steps` | object[] | Required for each rejection reason: `{item_id, required_action, reference_sdd}` |

### SLA

| Phase | Bound |
|---|---|
| Controller presents gate to founder | 24 hours from SI request |
| Human sign-off window | No automated timeout — the founder decides when ready |
| Rejection remediation re-submission | Domain agent may re-submit after addressing all rejection reasons |

### Autonomous Scope

The Controller agent may, without human input:

- Verify all checklist items against submitted evidence
- Reject the SI request with specific remediation steps (a REJECTED gate does not require human sign-off)
- Compute checklist pass rate and SLA baseline compliance
- Schedule the founder review session

The Controller agent may **not** autonomously:

- Issue an APPROVED gate decision — human sign-off is mandatory
- Waive any checklist item
- Modify the submitted SLA baseline numbers

This is the hard constraint that distinguishes Contract D from all other contracts. Any code path that issues `gate_decision = APPROVED` without a confirmed `approved_by = founder` record is a defect, not a configuration option.

### Escalation Path

| Step | Condition | Target |
|---|---|---|
| 1 | All checklist items pass | Controller presents to founder for sign-off |
| 2 | Any checklist item fails | Controller issues REJECTED with remediation steps; domain agent addresses and re-submits |
| 3 | Founder approves | Domain agent transitions to support mode |
| 4 | Founder rejects (rare — usually after Controller has already issued REJECTED) | Domain agent receives additional remediation requirements |

### Actor Type

`agent:controller` — written to `service_introduction_log` entries.

### Evidence Requirement

| Record | Table | Required Fields |
|---|---|---|
| SI request received | `service_introduction_log` | `module_id`, `status = PENDING_REVIEW`, `submitted_at`, `actor = agent:controller` |
| Gate decision | `service_introduction_log` | `module_id`, `gate_decision`, `decided_at`, `approved_by` (if APPROVED), `rejection_reasons` (if REJECTED) |
| Domain mode transition | Domain-specific config or status table | `module_id`, `mode = support`, `transitioned_at`, `si_log_id` |

On APPROVED: domain agent writes its own mode transition record, referencing the `service_introduction_log` entry. The SI log entry and the mode transition record together constitute the complete evidentiary record for this contract.

---

## Contract Enforcement Model

Contracts are enforced through three mechanisms working together. None is sufficient alone.

### MCP Tool Call Record

Every contract activation is a tool call. Every tool call is timestamped and attributed by the MCP middleware before the business logic runs. This produces an immutable sequence of `(agent_identity, tool_name, input_hash, timestamp)` tuples that is the ground-truth audit log. Business logic may add additional records; the MCP call record cannot be retroactively modified.

### SLA Monitoring via Scheduling Agent

The Scheduling infrastructure agent holds the SLA clock. When a contract activates:

1. Scheduling creates a `contract_execution_log` entry with `status = ACTIVE` and the expected resolution deadline.
2. If the deadline passes without a completion record, Scheduling fires an automatic escalation to the next party in the escalation path.
3. Escalation is not advisory — Scheduling writes the escalation record and notifies the next party directly.

A domain agent cannot suppress a Scheduling escalation by updating its own status. The Scheduling agent reads `contract_execution_log`, not domain-specific status tables.

### Actor Type on Every DB Write

The `actor_type` field on every row written during a contract execution is the audit trail anchor. The enforcement rule is simple: if a row in `alert_history`, `fox_evidence`, `fox_case_timeline`, `scheduled_job_log`, or `service_introduction_log` does not have a valid `actor_type` value, the write is rejected at the application layer (not the DB layer — the DB schema allows null for human-authored records).

The `actor_type` format is `agent:<module>-<role>`. Human-authored records use `user:<user_id>`. System-generated records (TTL archival, hash chain triggers) use `system:<process_name>`.

### Contract Violation Reporting

A contract violation occurs when:

- The executor produces output that does not conform to the output schema
- A required evidence record is missing at resolution time
- The SLA breach fires because no output was produced

In all three cases, the Scheduling agent reports the violation to the Controller. The Controller categorizes the violation and determines remediation. Repeated violations from the same domain agent trigger a Security infrastructure agent review.

---

## MCP Tool Pattern

Each contract executor exposes MCP tools that match its contract input/output. The tool call is the contract activation event. The MCP middleware records the call before routing to business logic.

### JWT Requirement

All MCP tools are JWT-protected. The JWT payload carries `sub` (agent identity in `agent:<module>-<role>` format) and `scope` (the permitted tool names for this token). A tool call with a JWT whose `scope` does not include the requested tool name is rejected with `403 Forbidden` before any business logic executes.

### Tool Naming Convention

```
POST /<module>/tools/<action_verb>_<object>

Examples:
  POST /Q/tools/triage_alert
  POST /fox-Q/tools/investigate_case
  POST /analytics/tools/update_baselines
  POST /controller/tools/gate_service_introduction
```

### Reference Tool Signatures

**Alert Triage** (`agent:Q-lp.triage_alert`):

```
POST /Q/tools/triage_alert

Request:
{
  "alert_id":           "uuid",
  "escalation_override": false
}

Response:
{
  "decision":       "NOISE | SIGNAL | ESCALATE",
  "confidence":     0.0–1.0,
  "action":         "string",
  "evidence_ids":   ["uuid", ...]
}
```

**Fox Investigation** (`agent:fox-Q.investigate_case`):

```
POST /fox-Q/tools/investigate_case

Request:
{
  "case_id":              "uuid",
  "escalation_threshold": 0.0–1.0
}

Response:
{
  "case_id":              "uuid",
  "evidence_chain_hash":  "sha256-hex-string",
  "recommendation":       "CLOSE | HUMAN_REVIEW | CIVIL_REFERRAL",
  "evidence_record_count": integer,
  "reasoning":            "string"
}
```

**Analytics Baseline** (`agent:analytics.update_baselines`):

```
POST /analytics/tools/update_baselines

Request:
{
  "merchant_id":   "uuid",
  "lookback_days": 30,
  "metric_types":  ["refund_rate", "void_rate", ...]
}

Response:
{
  "baseline_record_ids": ["uuid", ...],
  "updated_at":          "timestamp",
  "anomaly_signals":     [{"metric_type": "string", "signal_strength": 0.0–1.0, "alert_recommended": bool}],
  "partial":             false,
  "partial_reason":      null
}
```

**Service Introduction Gate** (`agent:controller.gate_service_introduction`):

```
POST /controller/tools/gate_service_introduction

Request:
{
  "module_id":          "string",
  "checklist_results":  {"item_id": {"passed": bool, "evidence_path": "string"}},
  "sla_baseline":       {"p50_ms": int, "p95_ms": int, "p99_ms": int, "error_rate_pct": float},
  "runbook_path":       "string",
  "dependency_status":  {"dependency_name": {"status": "UP", "verified_at": "timestamp"}}
}

Response:
{
  "gate_decision":       "APPROVED | REJECTED",
  "rejection_reasons":   [],
  "approved_by":         "founder | null",
  "approved_at":         "timestamp | null",
  "remediation_steps":   [{"item_id": "string", "required_action": "string", "reference_sdd": "string"}]
}
```

### Middleware Audit Record

Every tool call produces a middleware-layer audit record before the handler runs:

```json
{
  "call_id":     "uuid",
  "agent_id":    "agent:Q-lp",
  "tool_name":   "triage_alert",
  "input_hash":  "sha256 of serialized request body",
  "jwt_sub":     "agent:Q-lp",
  "called_at":   "timestamp"
}
```

This record is written to `contract_execution_log`. The contract's business logic may add additional domain-specific records. The middleware record is the floor — it exists for every contract activation regardless of whether the handler succeeds or fails.

---

## Gap Forward References

The following SDDs must be authored before the full agent network described in this document can be implemented. Builders picking up a module should not wait for these SDDs — the contracts above can be implemented with stub interfaces for the missing pieces.

| SDD | File | Gap |
|---|---|---|
| Agent Topology | `agent-topology.md` | Full agent network map with MCP tool catalog per agent. The contracts in this SDD reference agent identities (`agent:Q-lp`, `agent:fox-Q`, etc.) that need a canonical registry. |
| RaaS Go | `raas-go.md` | Namespace resolution service that maps `agent:<module>-<role>` identifiers to live MCP endpoints. Required before multi-agent contract chains can route dynamically. |
| Goose Go | `goose-go.md` | L402 payment middleware Go implementation. Required before agent-to-agent contract activations can be metered and billed. |

Until these SDDs exist, implement contract activation using static routing (hardcoded MCP endpoint URLs per agent) and skip L402 gating. Flag any static routing with a `// TODO(GRO-668): replace with RaaS lookup` comment so the migration surface is visible.

---

## Related

- [[go-runtime]] — `actor_type` middleware that stamps every contract execution
- [[go-security]] — JWT issuance for agent identities; per-agent scoped roles
- [[go-observability]] — contract execution metrics, audit log fields
- [[architecture.md]] — multi-tenant isolation model that contract execution operates within
- [[microservice-architecture.md]] — service mesh that hosts contract executors
- [[platform-overview.md]] — Optional Features canonical section
- [[raas.md]] — namespace resolution that contract activations consume
- [[identity.md]] — agent JWT issuance and L402 macaroon (optional) layer
- [[l402-otb.md]] — financial gate for paid contract activations (optional)
- `docs/superpowers/specs/2026-04-28-canary-go-agent-pmo-architecture-design.md` — full agent network topology
