// Package devops implements the platform-operator sysadmin module
// at /devops/<service>. Each service renders the canonical six-zone
// service-detail-page (header · capability · KPI strip · endpoints ·
// service body · activity · linked) inside a shared shell with a
// grouped service sidebar and a tenant/env top nav.
//
// T2.0 / GRO-840 of the sysadmin module epic. Phase 2 of
// the build sequence captured in
// docs/superpowers/specs/2026-05-07-sysadmin-module-design.md.
//
// Subsequent backfill tickets (T2.1–T2.33) wire one real service at a
// time. T2.0 ships only the chrome — every service body says "TODO".
//
// Path strategy: this package mounts /devops/<service> for the
// services it knows about. The pre-existing internal/devops/ package
// (DEV_CONSOLE-gated pipeline/square/api/releases) keeps its routes;
// they don't collide with the sysadmin paths. Phase 3 (T3B.3 +
// existing /admin/* fold-in) folds the legacy pages into the new
// shell.
package devops

import (
	"bufio"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Drift is a parsed summary of services/canary-protocol/manifest/build/drift-report.txt.
// The reconcile.py emitter writes a fixed-shape report; this struct captures
// the counts that the manifest viewer surfaces. Empty Drift = report missing
// (the page degrades to an explanatory placeholder).
//
// Field names match the reconcile.py "Counts" + "Categories" labels so the
// template can render them without translation.
type Drift struct {
	Loaded            bool
	ManifestEndpoints int
	MountedEndpoints  int
	OpenAPIEndpoints  int
	Matched           int
	ManifestOnly      int
	OpenAPIOnly       int
	RoutesOnly        int // unaccounted — Phase 1 Gate metric
	GatePass          bool
}

// loadDrift parses build/drift-report.txt summary block. Tolerant of missing
// file; returns zero-value Drift{Loaded: false} on any failure. The reconcile
// emitter format is stable per spec §"reconcile.py" — see services/canary-
// protocol/manifest/gen/reconcile.py.
func loadDrift(logger *zap.Logger) *Drift {
	candidates := []string{
		"services/canary-protocol/manifest/build/drift-report.txt",
		"../services/canary-protocol/manifest/build/drift-report.txt",
		"../../services/canary-protocol/manifest/build/drift-report.txt",
	}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		d := &Drift{Loaded: true}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "manifest endpoints:"):
				d.ManifestEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "mounted endpoints:"):
				d.MountedEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "openapi endpoints:"):
				d.OpenAPIEndpoints = parseTrailingInt(line)
			case strings.HasPrefix(line, "matched:"):
				d.Matched = parseTrailingInt(line)
			case strings.HasPrefix(line, "manifest-only:"):
				d.ManifestOnly = parseTrailingInt(line)
			case strings.HasPrefix(line, "openapi-only:"):
				d.OpenAPIOnly = parseTrailingInt(line)
			case strings.HasPrefix(line, "routes-only (UNACCOUNTED):"):
				d.RoutesOnly = parseTrailingInt(line)
			case strings.Contains(line, "Phase 1 Gate"):
				d.GatePass = strings.Contains(line, "PASS")
			}
		}
		_ = f.Close()
		abs, _ := filepath.Abs(c)
		logger.Info("manifest: drift-report loaded",
			zap.String("path", abs),
			zap.Int("manifest_endpoints", d.ManifestEndpoints),
			zap.Int("manifest_only", d.ManifestOnly),
			zap.Int("openapi_only", d.OpenAPIOnly),
			zap.Bool("gate_pass", d.GatePass),
		)
		return d
	}
	logger.Warn("manifest: drift-report.txt not found; manifest page Activity zone will degrade")
	return &Drift{}
}

// TierInfo is the per-tier metadata block surfaced on /devops/observability.
// Values are pinned to spec §"Per-tier infrastructure" — change here if the
// spec evolves. Field shape mirrors the table columns of that spec section.
type TierInfo struct {
	Name      string // tier key — matches manifest.yaml's `tiers` array entries
	Protocol  string
	Auth      string
	RateLimit string
	Health    string
	Cache     string
	Recovery  string
}

// TierCatalog is the canonical per-tier metadata. Order matches the cadence-
// ladder progression (fastest → slowest); this is the order the
// observability page renders the tier cards.
var TierCatalog = []TierInfo{
	{
		Name:      "stream",
		Protocol:  "SSE / WebSocket / Valkey XREAD tail",
		Auth:      "Long-lived token",
		RateLimit: "Concurrent connections",
		Health:    "Heartbeat (no msg in N sec)",
		Cache:     "N/A",
		Recovery:  "Replay from queue",
	},
	{
		Name:      "change-feed",
		Protocol:  "REST polling with cursor / sub with watermark",
		Auth:      "API key per request",
		RateLimit: "Requests/min per key",
		Health:    "Lag exceeded / queue depth",
		Cache:     "Short TTL (~5 min)",
		Recovery:  "Catch up from watermark",
	},
	{
		Name:      "daily-batch",
		Protocol:  "Cron-driven export",
		Auth:      "Service-to-service signed",
		RateLimit: "Rate-of-runs",
		Health:    "Schedule missed / checksum diff",
		Cache:     "N/A",
		Recovery:  "Rerun the job",
	},
	{
		Name:      "bulk-window",
		Protocol:  "Scheduled file drop / blob",
		Auth:      "Signed URL",
		RateLimit: "Window-bounded",
		Health:    "Didn't land / row count off",
		Cache:     "N/A",
		Recovery:  "Reschedule",
	},
	{
		Name:      "reference",
		Protocol:  "REST GET with strong cache headers",
		Auth:      "API key (anon-read possible)",
		RateLimit: "High requests/min",
		Health:    "Version drift",
		Cache:     "Long TTL (~60s hot)",
		Recovery:  "Force resync",
	},
}

// TierRollup is the rendered shape — one card per tier with services list
// rolled up across all axes. Used by observabilityPage.
type TierRollup struct {
	TierInfo
	EndpointCount int
	Services      []string // dedup-sorted across A/B/C axes for the tier
}

// PipelineStage is one cell of the TSP pipeline flow rendered on
// /devops/pipeline. The stages run left-to-right:
//   Webhook Receipt → Valkey Stream → Sub1 (Seal) → Sub2 (Parse) → Sub3 (Merkle)
//
// Content is pinned to the documented flow in the Python prior art
// (Canary/canary/blueprints/devops_monitor.py) and the Webhook Pipeline
// SDD — operators read this page to understand the contract, not the
// runtime state. Live throughput/depth/lag wire in a follow-on once the
// pipeline package exposes a Stats() API.
type PipelineStage struct {
	Index    int    // 1-based for rendering ("Stage 1", etc.)
	Name     string // "Webhook Receipt"
	Role     string // one-sentence operator-facing description
	Packages []string // Go package paths backing the stage
	Tier     string // cadence-ladder tier this stage operates on
}

// ToolCategory is one MCP tool grouping surfaced on /devops/qa-agent.
// Pinned to the system prompt content in
// Canary/canary/services/qa_agent/agent.py (lines 45-55) — the canonical
// taxonomy operators see when the agent surfaces "what tools are available."
//
// T3B.4 / GRO-887. Live MCP tool dispatch is out of scope; this page renders
// the discoverable contract (categories + tools + roles) so operators
// understand the agent's capability surface before the runtime ships.
type ToolCategory struct {
	Name  string   // "Alerts"
	Role  string   // one-sentence description
	Tools []string // tool names exactly as the system prompt declares them
}

// QAToolCatalog is the canonical tool catalog. Order matches the system
// prompt's category-list order — change here when the agent's tool surface
// evolves.
var QAToolCatalog = []ToolCategory{
	{
		Name: "Atlas",
		Role: "System architecture diagrams + figure search (Mermaid engine).",
		Tools: []string{"atlas_figure", "atlas_search", "atlas_validate", "atlas_index"},
	},
	{
		Name: "Alerts",
		Role: "LP alert listing, ranking, and lifecycle introspection.",
		Tools: []string{"list_alerts", "get_alert", "lifecycle_summary", "rank_alerts"},
	},
	{
		Name: "Analytics",
		Role: "KPI rollups, trends, top risks, scoring.",
		Tools: []string{"get_dashboard", "get_trends", "get_top_risks", "score_metrics"},
	},
	{
		Name: "Chirp",
		Role: "Detection rule catalog + threshold validation.",
		Tools: []string{"get_rules", "get_rule", "get_config_summary", "validate_thresholds"},
	},
	{
		Name: "Fox",
		Role: "Case management + evidence chain verification.",
		Tools: []string{"list_cases", "get_case", "get_timeline", "verify_chain"},
	},
	{
		Name: "Identity",
		Role: "Merchant + employee + location lookup.",
		Tools: []string{"get_merchant", "list_employees", "list_locations"},
	},
	{
		Name: "Owl",
		Role: "Semantic search + Q&A + payment scoring (pgvector + EJ spine).",
		Tools: []string{"search", "ask", "knowledge_search", "score_payment"},
	},
	{
		Name: "TSP",
		Role: "Pipeline health + ingestion stats + dead-letter triage.",
		Tools: []string{"get_stream_health", "get_ingestion_stats", "get_dead_letters"},
	},
	{
		Name: "Scenarios",
		Role: "Scripted test scenarios — fire, poll, list, threshold reads.",
		Tools: []string{"fire_scenario", "poll_scenario", "list_scenarios", "get_thresholds"},
	},
	{
		Name: "Bug filing",
		Role: "One-shot Linear bug filing — title + description, no confirmation needed.",
		Tools: []string{"file_linear_bug"},
	},
}

// PosLabMode is one mode of the /devops/pos-lab single-transaction
// firing range. Pinned to chirp_lab.py docstring lines 1-15: Local Fire
// (direct DB + Chirp eval) vs Live Fire (Square sandbox round-trip).
//
// T3B.9 / GRO-892.
type PosLabMode struct {
	Name    string
	Latency string // human-readable latency target
	Path    string // request path narrative
	Tests   string // what this mode validates
}

var PosLabModes = []PosLabMode{
	{
		Name: "Local Fire",
		Latency: "~50ms",
		Path: "Direct INSERT into canary_sales → ChirpRuleEngine.evaluate_readonly() → write_alerts_to_session()",
		Tests: "Chirp's brain — rule logic, threshold math, alert generation. No Square API calls.",
	},
	{
		Name: "Live Fire",
		Latency: "~3-5s",
		Path: "Square Payments API → webhook receiver → TSP pipeline → Chirp evaluation → alerts",
		Tests: "End-to-end pipe — Square integration, webhook signature, idempotency, full TSP flow.",
	},
}

// PosLabTransactionTypes is the canonical transaction-type parameter
// space — pinned to chirp_lab.py TRANSACTION_TYPES.
var PosLabTransactionTypes = []string{
	"SALE", "REFUND", "RETURN", "VOID", "POST_VOID",
	"NO_SALE", "PAID_IN", "PAID_OUT", "EXCHANGE",
}

// PosLabEntryMethods — pinned to ENTRY_METHODS.
var PosLabEntryMethods = []string{
	"CHIP", "CONTACTLESS", "KEYED", "SWIPED", "MANUAL", "ON_FILE",
}

// PosLabCardBrands — pinned to CARD_BRANDS.
var PosLabCardBrands = []string{
	"VISA", "MASTERCARD", "AMEX", "DISCOVER", "JCB",
}

// Scenario is one scripted scenario in the /devops/scenarios catalog.
// Pinned to Canary/canary/services/scenario_runner.py SCENARIOS dict —
// keep field shape in lockstep when the catalog evolves.
//
// T3B.8 / GRO-891.
type Scenario struct {
	Key            string
	Name           string
	Desc           string
	ExpectedRules  []string // empty = no rule expected to fire
	VerificationQs []string // queries that should return ≥ expect_min after the run
}

var ScenariosCatalog = []Scenario{
	{Key: "happy_path_payment", Name: "Happy Path Payment",
		Desc: "Clean sale — appears on merchant transactions view.",
		ExpectedRules: []string{},
		VerificationQs: []string{"last transaction"}},
	{Key: "refund_detection", Name: "Rapid Refund",
		Desc: "Sale + rapid refund — triggers C-001 RAPID_REFUND.",
		ExpectedRules: []string{"C-001"},
		VerificationQs: []string{"refunds today"}},
	{Key: "void_pattern", Name: "Void Pattern",
		Desc: "Sale followed by void — shows as voided in transaction detail.",
		ExpectedRules: []string{},
		VerificationQs: []string{"voids today"}},
	{Key: "cash_drawer_variance", Name: "Cash Drawer Variance",
		Desc: "Cash drawer closes short — triggers C-102 alert.",
		ExpectedRules: []string{"C-102"},
		VerificationQs: []string{"high severity alerts"}},
	{Key: "employee_risk_score", Name: "Employee Risk Score",
		Desc: "Multiple refunds by one employee — triggers C-002.",
		ExpectedRules: []string{"C-002"},
		VerificationQs: []string{"refunds today"}},
	{Key: "multi_location", Name: "Multi-Location",
		Desc: "Transactions across all 3 locations in a short window.",
		ExpectedRules: []string{},
		VerificationQs: []string{"how many transactions today"}},
	{Key: "fox_case_lifecycle", Name: "Fox Case Lifecycle",
		Desc: "Alert escalated to Fox case with evidence.",
		ExpectedRules: []string{"C-001"},
		VerificationQs: []string{"high severity alerts"}},
	{Key: "pipeline_e2e", Name: "Full Pipeline E2E",
		Desc: "Transaction + alert + case + evidence chain.",
		ExpectedRules: []string{"C-007"},
		VerificationQs: []string{"refunds today", "high severity alerts"}},
}

// TestLabTab is one tab on the /devops/test-lab console. Pinned to the
// two tabs in templates/ops/test_lab.html: "Scenarios" + "Live Fire".
//
// T3B.7 / GRO-890.
type TestLabTab struct {
	Name string
	Role string
	Tags []string // implementation tags shown as pills
}

// TestLabStage is one stage of the pipeline progress visualization
// on the /devops/test-lab console. Pinned to the 5 stages rendered in
// the Python prototype's `pipeline-stages` div.
type TestLabStage struct {
	Index int
	Name  string
	Role  string
}

var TestLabTabs = []TestLabTab{
	{
		Name: "Scenarios",
		Role: "Scripted multi-payment scenarios — fire a labeled batch, watch alerts trigger, verify Owl scoring. The fast path for rule + threshold testing.",
		Tags: []string{"scripted", "multi-payment", "auto-verify"},
	},
	{
		Name: "Live Fire",
		Role: "Single-payment trace — pick amount + tender + employee, fire one Square sandbox call, watch the event traverse Webhook → Chirp → Owl.",
		Tags: []string{"single-payment", "manual", "step-by-step"},
	},
}

var TestLabStages = []TestLabStage{
	{Index: 1, Name: "Square API", Role: "SDK call to Square sandbox creates the payment server-side."},
	{Index: 2, Name: "Webhook", Role: "Square posts the event to the gateway's webhook receiver (HMAC verified)."},
	{Index: 3, Name: "Chirp", Role: "Detection rules evaluate the canonicalized event; alerts surface."},
	{Index: 4, Name: "Complete", Role: "Pipeline confirms persistence to canary_sales + protocol.evidence."},
	{Index: 5, Name: "Owl Verify", Role: "Optional: Owl re-scores the event for risk-detection coverage."},
}

// GooseModule is one Python module in Canary/canary/services/goose/
// surfaced on /devops/wallet. The 9 modules collectively form the
// L402 wallet + treasury + Strike Lightning credit system.
//
// T3B.6 / GRO-889. Live wallet ledger + Strike client + L402 middleware
// runtime port to Go is captured as follow-on dispatches.
type GooseModule struct {
	Name string // "wallet_service.py"
	Role string // one-sentence operator-facing description
	Owns []string // key types/operations
}

// WalletState is one node in the wallet status state machine — pinned to
// the comment in wallet_service.py: "active → warning → depleted → suspended".
type WalletState struct {
	Name        string
	Description string
	Transition  string // condition for entering this state
}

// L402Step is one row in the L402 paywall handshake flow rendered on
// /devops/wallet. Order matches the documented request/response sequence.
type L402Step struct {
	Index int
	Name  string
	Role  string
}

// GooseModules is the canonical 9-module catalog. Order: state-machine
// owners first, then payment rail, then the L402 stack, then onboarding.
var GooseModules = []GooseModule{
	{
		Name: "wallet_service.py",
		Role: "Credit/debit operations on merchant sat wallets. Owns the status state machine.",
		Owns: []string{"WalletService", "credit/debit", "balance check"},
	},
	{
		Name: "treasury.py",
		Role: "Treasury funding (founder → merchant) + cumulative outflow tracking + emergency reserve floor.",
		Owns: []string{"TreasuryService", "fund_merchant", "DEFAULT_EMERGENCY_RESERVE_USD"},
	},
	{
		Name: "strike_client.py",
		Role: "Strike Lightning API client — invoice creation + deposit polling.",
		Owns: []string{"StrikeClient", "create_invoice", "poll_invoice"},
	},
	{
		Name: "l402_middleware.py",
		Role: "L402 paywall middleware — issues 402 with Lightning invoice, verifies macaroon on retry.",
		Owns: []string{"L402Middleware", "challenge", "verify"},
	},
	{
		Name: "macaroon_service.py",
		Role: "Macaroon mint + verify + revoke. Macaroon = signed authorization token redeemed after invoice payment.",
		Owns: []string{"MacaroonService", "issue", "verify", "revoke"},
	},
	{
		Name: "gas_meter.py",
		Role: "Per-request cost (gas) computation — maps endpoint cell × tier × payload size to sat cost.",
		Owns: []string{"GasMeter", "compute_cost"},
	},
	{
		Name: "gas_metered.py",
		Role: "Decorator wrapping handlers with gas metering — debits wallet pre-call, refunds on error.",
		Owns: []string{"@gas_metered"},
	},
	{
		Name: "onboarding.py",
		Role: "Merchant onboarding — creates wallet, seeds initial credit from treasury.",
		Owns: []string{"OnboardingService", "create_wallet"},
	},
	{
		Name: "seed.py",
		Role: "Initial state seeder — bootstraps treasury + sample merchants for dev/test.",
		Owns: []string{"seed_dev_treasury"},
	},
}

// WalletStateMachine is the 4-state lifecycle from wallet_service.py.
var WalletStateMachine = []WalletState{
	{
		Name:        "active",
		Description: "Healthy balance — requests served normally.",
		Transition:  "Initial state; remains while balance ≥ warning threshold.",
	},
	{
		Name:        "warning",
		Description: "Low balance — requests still served, dashboard surfaces a top-up nudge.",
		Transition:  "Balance drops below merchant-configured warning threshold (default 1,000 sats).",
	},
	{
		Name:        "depleted",
		Description: "Zero or negative balance — grace window active, requests still served on credit.",
		Transition:  "Balance ≤ 0; grace window starts (default 7 days).",
	},
	{
		Name:        "suspended",
		Description: "Grace expired — read-only access until top-up. Cron-driven (separate dispatch).",
		Transition:  "Balance < 0 for > 7 days; suspended until next credit lands.",
	},
}

// L402Flow is the canonical 5-step paywall handshake.
var L402Flow = []L402Step{
	{Index: 1, Name: "Request", Role: "Client calls a paywalled endpoint without macaroon."},
	{Index: 2, Name: "402 + invoice", Role: "Server responds 402 Payment Required with a Lightning invoice + opaque macaroon."},
	{Index: 3, Name: "Pay (Lightning)", Role: "Client pays the invoice via any LN wallet; payment hash returned."},
	{Index: 4, Name: "Macaroon issued", Role: "Server verifies payment hash, issues a signed macaroon scoped to the resource."},
	{Index: 5, Name: "Retry", Role: "Client retries the original request with the macaroon; server admits + debits gas."},
}

// ETLStage represents one stage of the metrics ETL pipeline. Pinned to
// the structure documented in Canary/canary/services/etl_runner.py and
// implemented across metrics_etl.py + period_aggregation.py.
//
// Order matches the run sequence in run_full_etl(): daily aggregation
// must complete before period rollup. T3B.5 / GRO-888.
type ETLStage struct {
	Index int
	Name  string
	Role  string
	Tables []string // fact tables this stage writes
	Source string  // Python source file
}

// ETLTable describes one metrics fact table. Pinned to METRICS_TABLES in
// etl_runner.py — keep in lockstep when the metrics schema evolves.
type ETLTable struct {
	Name      string // "daily_metrics"
	Stage     int    // which ETL stage writes it (1=daily, 2=period)
	Cadence   string // "Daily" | "Period (week/month/quarter)"
	Role      string // one-sentence operator-facing description
	GroupedBy string // grouping key tuple, e.g. "merchant × location × date"
}

// ETLPipeline is the 2-stage canonical flow.
var ETLPipeline = []ETLStage{
	{
		Index: 1, Name: "Daily aggregation",
		Role:   "Reads canary_sales transactions and rolls up into 4 daily fact tables. Idempotent — deletes existing rows for the date range before inserting.",
		Tables: []string{"daily_metrics", "hourly_metrics", "employee_daily_metrics", "product_daily_metrics"},
		Source: "Canary/canary/services/metrics_etl.py",
	},
	{
		Index: 2, Name: "Period rollup",
		Role:   "Aggregates daily facts into period facts (week / month / quarter / YTD). Reads daily_metrics + employee_daily_metrics; writes period_metrics + employee_period_metrics.",
		Tables: []string{"period_metrics", "employee_period_metrics"},
		Source: "Canary/canary/services/period_aggregation.py",
	},
}

// ETLTableCatalog is the canonical 6-table set. Order matches stage order
// (Stage 1 tables first, then Stage 2).
var ETLTableCatalog = []ETLTable{
	{
		Name: "daily_metrics", Stage: 1, Cadence: "Daily",
		Role:      "Per-location daily KPIs — gross/net sales, transactions, refunds, voids, comps.",
		GroupedBy: "merchant × location × date",
	},
	{
		Name: "hourly_metrics", Stage: 1, Cadence: "Daily",
		Role:      "Hour-of-day distribution for staffing + traffic analysis.",
		GroupedBy: "merchant × location × date × hour",
	},
	{
		Name: "employee_daily_metrics", Stage: 1, Cadence: "Daily",
		Role:      "Per-employee daily KPIs — sales-per-hour, void rate, comp rate, shift hours.",
		GroupedBy: "merchant × location × employee × date",
	},
	{
		Name: "product_daily_metrics", Stage: 1, Cadence: "Daily",
		Role:      "Per-item daily KPIs — units sold, revenue, returns, on-hand snapshot.",
		GroupedBy: "merchant × location × item × date",
	},
	{
		Name: "period_metrics", Stage: 2, Cadence: "Period (week/month/quarter)",
		Role:      "Period rollups for finance + planning surfaces. Each row covers a closed period.",
		GroupedBy: "merchant × location × period",
	},
	{
		Name: "employee_period_metrics", Stage: 2, Cadence: "Period (week/month/quarter)",
		Role:      "Period rollups of employee performance — feeds the labor + scorecard surfaces.",
		GroupedBy: "merchant × location × employee × period",
	},
}

// PipelineFlow is the canonical TSP pipeline flow. Phase 3 / T3B.3 / GRO-885.
// Order matches the documented data flow; do not reorder without updating
// the Webhook Pipeline SDD.
var PipelineFlow = []PipelineStage{
	{
		Index: 1, Name: "Webhook Receipt", Tier: "stream",
		Role:     "HTTP receiver — HMAC verifies inbound POS events, idempotency-keys deduplicate, raw envelope persisted to protocol.evidence_inbox.",
		Packages: []string{"internal/protocol/webhook", "internal/protocol/hmac"},
	},
	{
		Index: 2, Name: "Valkey Stream", Tier: "stream",
		Role:     "Buffer — events fan out via XADD to durable stream; downstream workers consume with consumer groups (XREADGROUP) for at-least-once delivery.",
		Packages: []string{"internal/protocol/publisher"},
	},
	{
		Index: 3, Name: "Sub1 — Seal", Tier: "stream",
		Role:     "Sealer — appends content-addressable hash + monotonic ordinal to each event, writes to protocol.evidence (append-only). Establishes the cryptographic anchor.",
		Packages: []string{"internal/protocol/sub1"},
	},
	{
		Index: 4, Name: "Sub2 — Parse", Tier: "change-feed",
		Role:     "Canonicalizer — parses POS-vendor envelope into the Canary canonical event taxonomy, dispatches to per-domain stores (sales, inventory, customer).",
		Packages: []string{"internal/protocol/sub2"},
	},
	{
		Index: 5, Name: "Sub3 — Merkle", Tier: "daily-batch",
		Role:     "Anchor — batches sealed events into Merkle trees, writes root hashes to Bitcoin L2 ordinals. Generates inclusion proofs for downstream evidence queries.",
		Packages: []string{"internal/protocol/sub3"},
	},
}

// parseTrailingInt extracts the trailing integer from lines like
// "manifest endpoints:   80" or "manifest-only:                 78".
// Returns 0 on parse failure (the field stays zero-valued — the template
// renders 0, indistinguishable from a real zero count).
func parseTrailingInt(line string) int {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0
	}
	last := fields[len(fields)-1]
	// Strip parens / commas if present (reconcile output is plain ints today,
	// but be defensive against future format tweaks).
	last = strings.TrimRight(last, ".,)")
	last = strings.TrimLeft(last, "(")
	n, err := strconv.Atoi(last)
	if err != nil {
		return 0
	}
	return n
}

// Catalog is the JSON shape emitted by parse_manifest.py's
// build_catalog() — see services/canary-protocol/manifest/gen/.
// Keep the field tags in lockstep with that emitter.
//
// Deliberately no GeneratedAt field — the catalog is content-addressable
// via the input-file SHAs in manifest.yaml's generated_from. Including a
// wall-clock timestamp causes git churn on every `make manifest` run.
type Catalog struct {
	Axes     []CatalogAxis `json:"axes"`
	Tiers    []string      `json:"tiers"`
	Cells    []CatalogCell `json:"cells"`
	Services []CatalogSvc  `json:"services"`
	Totals   CatalogTotals `json:"totals"`
}

type CatalogAxis struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type CatalogCell struct {
	Axis          string   `json:"axis"`
	Tier          string   `json:"tier"`
	EndpointCount int      `json:"endpoint_count"`
	Services      []string `json:"services"`
}

type CatalogSvc struct {
	Name           string   `json:"name"`
	Port           *int     `json:"port"`
	Priority       string   `json:"priority"`
	Category       string   `json:"category"`
	Scope          string   `json:"scope"`
	Owner          string   `json:"owner"`
	Cells          []string `json:"cells"`
	EndpointCount  int      `json:"endpoint_count"`
}

type CatalogTotals struct {
	ServiceCount  int `json:"service_count"`
	EndpointCount int `json:"endpoint_count"`
}

//go:embed templates
var embedFS embed.FS

// Service is the metadata block rendered in the service-detail-page
// header + capability zone. T2.0 carries hardcoded definitions for
// the 5 Phase-1 skeleton services. T2.34 (catalog page) loads the
// full set from manifest.yaml.
type Service struct {
	Name           string   // catalog
	Port           int      // 9100
	Owner          string   // ALX
	Priority       string   // P0
	Scope          string   // cross-tenant
	Category       string   // cross-tenant infra
	Cells          []string // ["B × reference"]
	Status         string   // one-line description of the service surface
}

// Group is a sidebar grouping. Order in the sidebar follows the
// order of Categories below.
type Group struct {
	Title    string
	Services []Service
}

// Categories drives sidebar ordering. The full set is per spec
// §"Service inventory"; T2.0 lists only the 5 skeletons. Phase 2
// backfill tickets append.
var Categories = []Group{
	{
		Title: "Cross-tenant infra",
		Services: []Service{
			{
				Name: "catalog", Port: 9100, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "3×5 grid heat-map of every endpoint by axis × tier — wired in T2.34.",
			},
			{
				Name: "manifest", Port: 9101, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "Manifest editor + validator + history viewer — wired in T3B.1.",
			},
			{
				Name: "observability", Port: 9102, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × change-feed", "B × reference"},
				Status: "Five-tier health rollup with per-service drill-down — wired in T3B.2.",
			},
			{
				Name: "pipeline", Port: 9103, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × change-feed"},
				Status:       "TSP pipeline visualization (Webhook → Sub1 → Sub2 → Sub3) — wired in T3B.3.",
			},
			{
				Name: "qa-agent", Port: 9104, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"C × change-feed"},
				Status:       "Page-aware operator agent with cross-service MCP tools — wired in T3B.4.",
			},
			{
				Name: "api-docs", Port: 9105, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "Redoc-rendered OpenAPI 3.0 spec — wired in T2.35.",
			},
			{
				Name: "evidence", Port: 9201, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				Status:       "Append-only protocol audit log query UI — Phase 3 wires the operator workflow.",
			},
			{
				Name: "anchor", Port: 9202, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:          []string{"B × reference"},
				Status:       "Merkle anchor batch viewer + Bitcoin L2 proof inspector — Phase 3.",
			},
			{
				Name: "mcp", Port: 9203, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"C × change-feed"},
				Status: "MCP tool catalog + per-tenant usage rollup — Phase 3 builds drill-down.",
			},
			{
				Name: "etl", Port: 9330, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × daily-batch"},
				Status: "Daily-batch metrics ETL — 2 stages (daily + period), 6 fact tables. Wired in T3B.5.",
			},
			{
				Name: "wallet", Port: 9331, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "cross-tenant infra",
				Cells:    []string{"B × reference"},
				Status: "L402 wallet + treasury + Strike Lightning credit system. 9 modules, 4-state lifecycle. Wired in T3B.6.",
			},
		},
	},
	{
		Title: "Test & validation",
		Services: []Service{
			{
				Name: "test-lab", Port: 9332, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "test & validation",
				Cells:    []string{"B × stream"},
				Status: "Sandbox-guarded testing console — scenarios + live-fire tabs over the full TSP pipeline. Wired in T3B.7.",
			},
			{
				Name: "scenarios", Port: 9333, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "test & validation",
				Cells:    []string{"B × stream"},
				Status: "Scripted scenario catalog (8 scenarios) + randomizer mode for stress + discovery testing. Wired in T3B.8.",
			},
			{
				Name: "pos-lab", Port: 9334, Owner: "ALX",
				Priority: "P0",
				Scope: "cross-tenant", Category: "test & validation",
				Cells:    []string{"B × stream"},
				Status: "Single-transaction firing range — Local Fire (direct DB + Chirp) vs Live Fire (Square round-trip). Wired in T3B.9.",
			},
		},
	},
}

// Handler renders the sysadmin module shell.
type Handler struct {
	tmpl       *template.Template
	logger     *zap.Logger
	index      map[string]*Service // name → metadata, built once at New()
	openAPIRaw []byte              // openapi.yaml loaded once at boot for /devops/api-docs/openapi.yaml
	catalog    *Catalog            // devops-catalog.json loaded once at boot for /devops/catalog
	drift      *Drift              // build/drift-report.txt summary, loaded once at boot for /devops/manifest
}

// New constructs a Handler. Returns an error if templates fail to
// parse — main.go logs and continues without mounting. openAPIPath
// is the absolute or repo-relative path to openapi.yaml; if empty
// the loader tries a couple of common locations. Failure to load
// the spec is logged and api-docs degrades to a placeholder, but
// the rest of the shell still mounts.
func New(logger *zap.Logger) (*Handler, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	tmpl, err := template.ParseFS(embedFS,
		"templates/shell.html",
		"templates/sidebar.html",
		"templates/api_docs.html",
		"templates/catalog.html",
		"templates/manifest.html",
		"templates/observability.html",
		"templates/pipeline.html",
		"templates/qa_agent.html",
		"templates/etl.html",
		"templates/wallet.html",
		"templates/test_lab.html",
		"templates/scenarios.html",
		"templates/pos_lab.html",
	)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]*Service)
	for gi := range Categories {
		for si := range Categories[gi].Services {
			s := &Categories[gi].Services[si]
			idx[s.Name] = s
		}
	}
	h := &Handler{tmpl: tmpl, logger: logger, index: idx}
	h.openAPIRaw = loadOpenAPI(logger)
	h.catalog = loadCatalog(logger)
	h.drift = loadDrift(logger)
	return h, nil
}

// loadCatalog reads devops-catalog.json from the same candidate set as
// loadOpenAPI. Returns nil if the file is missing or unparseable; the
// catalog page degrades to a placeholder.
func loadCatalog(logger *zap.Logger) *Catalog {
	candidates := []string{
		"services/canary-protocol/manifest/devops-catalog.json",
		"../services/canary-protocol/manifest/devops-catalog.json",
		"../../services/canary-protocol/manifest/devops-catalog.json",
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		var cat Catalog
		if err := json.Unmarshal(data, &cat); err != nil {
			abs, _ := filepath.Abs(c)
			logger.Warn("catalog: devops-catalog.json malformed",
				zap.String("path", abs), zap.Error(err))
			continue
		}
		abs, _ := filepath.Abs(c)
		logger.Info("catalog: devops-catalog.json loaded",
			zap.String("path", abs),
			zap.Int("services", cat.Totals.ServiceCount),
			zap.Int("endpoints", cat.Totals.EndpointCount),
		)
		return &cat
	}
	logger.Warn("catalog: devops-catalog.json not found in any candidate location " +
		"(catalog page will render placeholder; run `make manifest`)")
	return nil
}

// loadOpenAPI reads openapi.yaml from a couple of candidate locations
// relative to the current working directory. Gateway boots with cwd
// set by deploy/start scripts; the candidates cover repo-root runs and
// CanaryGo-rooted runs. Returns nil if no file is found — the api-docs
// page degrades gracefully.
func loadOpenAPI(logger *zap.Logger) []byte {
	candidates := []string{
		"services/canary-protocol/openapi/openapi.yaml",
		"../services/canary-protocol/openapi/openapi.yaml",
		"../../services/canary-protocol/openapi/openapi.yaml",
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		data, err := os.ReadFile(c)
		if err == nil && len(data) > 0 {
			logger.Info("api-docs: openapi.yaml loaded",
				zap.String("path", abs),
				zap.Int("bytes", len(data)),
			)
			return data
		}
	}
	logger.Warn("api-docs: openapi.yaml not found in any candidate location " +
		"(api-docs page will render placeholder)")
	return nil
}

// Mount registers a route per known service. The runner registers
// these specifically (rather than a /devops/{service} catch-all) so
// chi can detect collisions cleanly during boot and so the existing
// internal/devops/ package's specific routes (/devops/square,
// /devops/api, /devops/releases) keep working.
//
// Special services with custom handlers (api-docs renders Redoc, not
// the generic shell) are routed before the generic loop.
func (h *Handler) Mount(r chi.Router) {
	// Special routes — rendered with custom handlers, NOT the generic
	// six-zone shell. T2.35: api-docs serves Redoc + raw openapi.yaml.
	// T2.34: catalog renders the 3×5 grid heat-map from devops-catalog.json.
	r.Get("/devops/api-docs", h.apiDocsPage)
	r.Get("/devops/api-docs/openapi.yaml", h.apiDocsSpec)
	r.Get("/devops/catalog", h.catalogPage)
	r.Get("/devops/manifest", h.manifestPage)
	r.Get("/devops/observability", h.observabilityPage)
	r.Get("/devops/pipeline", h.pipelinePage)
	r.Get("/devops/qa-agent", h.qaAgentPage)
	r.Get("/devops/etl", h.etlPage)
	r.Get("/devops/wallet", h.walletPage)
	r.Get("/devops/test-lab", h.testLabPage)
	r.Get("/devops/scenarios", h.scenariosPage)
	r.Get("/devops/pos-lab", h.posLabPage)

	for name := range h.index {
		switch name {
		case "api-docs", "catalog", "manifest", "observability", "pipeline", "qa-agent", "etl", "wallet", "test-lab", "scenarios", "pos-lab":
			continue // mounted above with custom handlers
		}
		path := "/devops/" + name
		r.Get(path, h.servicePage)
	}
	h.logger.Info("sysadmin module mounted",
		zap.Int("services", len(h.index)),
		zap.String("path_prefix", "/devops/"),
	)
}

// KnownServices returns the set of service names the shell knows
// about. Useful for tests and for the Phase 2 catalog page when it
// needs to enumerate manifest entries.
func (h *Handler) KnownServices() []string {
	out := make([]string, 0, len(h.index))
	for k := range h.index {
		out = append(out, k)
	}
	return out
}

func (h *Handler) servicePage(w http.ResponseWriter, r *http.Request) {
	// Extract service name from the path. r.URL.Path is /devops/<name>;
	// cut the prefix.
	const prefix = "/devops/"
	name := r.URL.Path
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		name = name[len(prefix):]
	}
	svc, ok := h.index[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     name,
		"Tenant":     "all",
		"Env":        "lab",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "shell.html", view); err != nil {
		h.logger.Error("shell template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) apiDocsPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["api-docs"]
	view := map[string]any{
		"Service":     svc,
		"Categories":  Categories,
		"Active":      "api-docs",
		"Tenant":      "all",
		"Env":         "lab",
		"SpecURL":     "/devops/api-docs/openapi.yaml",
		"SpecLoaded":  len(h.openAPIRaw) > 0,
		"SpecBytes":   len(h.openAPIRaw),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "api_docs.html", view); err != nil {
		h.logger.Error("api-docs template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (h *Handler) apiDocsSpec(w http.ResponseWriter, r *http.Request) {
	if len(h.openAPIRaw) == 0 {
		http.Error(w, "openapi.yaml not loaded", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(h.openAPIRaw)
}

// catalogPage renders the 3×5 axis-tier heat-map plus a service list.
// Manifest-driven: data comes from devops-catalog.json loaded at boot.
// When the file is absent, a placeholder explains how to regenerate.
func (h *Handler) catalogPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["catalog"]
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     "catalog",
		"Tenant":     "all",
		"Env":        "lab",
		"Catalog":    h.catalog,
		"Loaded":     h.catalog != nil,
	}
	if h.catalog != nil {
		// Pre-arrange cells into a row-per-axis structure for the template:
		// rows[i] = the 5 cells of axis i in tier order.
		rows := make(map[string][]CatalogCell, len(h.catalog.Axes))
		for _, c := range h.catalog.Cells {
			rows[c.Axis] = append(rows[c.Axis], c)
		}
		// Compute max endpoint count for heat-color scaling.
		maxCount := 0
		for _, c := range h.catalog.Cells {
			if c.EndpointCount > maxCount {
				maxCount = c.EndpointCount
			}
		}
		view["GridRows"] = rows
		view["MaxCellCount"] = maxCount
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "catalog.html", view); err != nil {
		h.logger.Error("catalog template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// manifestPage renders the /devops/manifest viewer — the operator surface
// for understanding current manifest state. Sources:
//   - h.catalog (devops-catalog.json)         → service inventory + totals
//   - h.drift   (build/drift-report.txt)      → reconciliation summary
//
// Six zones per spec §"Service-detail-page common shape":
//   Header     — name + port + priority + scope (T2.0 pattern)
//   Capability — owner + cells (T2.0 pattern)
//   KPI strip  — services count · endpoints · gate status · drift
//   Endpoints  — links to source files (canary-go-portal.md, SDDs)
//   Body       — services table sorted by category (P0 first)
//   Activity   — drift-report summary counts
//   Linked     — catalog (consumes manifest), api-docs (consumes openapi)
//
// T3B.1 / GRO-879. Editor (write) and full git-log history viewer are
// captured as out-of-scope follow-ons.
func (h *Handler) manifestPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["manifest"]
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     "manifest",
		"Tenant":     "all",
		"Env":        "lab",
		"Catalog":    h.catalog,
		"CatalogLoaded": h.catalog != nil,
		"Drift":      h.drift,
		"DriftLoaded": h.drift != nil && h.drift.Loaded,
	}
	if h.catalog != nil {
		// Group services by category for the body table. P0 first, then P1, P2.
		byPriority := map[string][]CatalogSvc{}
		for _, s := range h.catalog.Services {
			byPriority[s.Priority] = append(byPriority[s.Priority], s)
		}
		view["P0Services"] = byPriority["P0"]
		view["P1Services"] = byPriority["P1"]
		view["P2Services"] = byPriority["P2"]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "manifest.html", view); err != nil {
		h.logger.Error("manifest template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// observabilityPage renders the /devops/observability viewer — five tier
// cards covering the full cadence ladder. Each tier card surfaces the
// per-tier infrastructure metadata (protocol/auth/rate-limit/health/
// cache/recovery) per spec §"Per-tier infrastructure", plus the dedup-
// sorted set of services declared in any cell of that tier.
//
// Live health checks and per-service drill-down are out of scope here —
// they land in T3A.4 ("per-tier health rollups" middleware ticket) and
// follow-on Phase 3 work respectively. This page renders the contract
// (what each tier means + which services occupy it), not live telemetry.
//
// T3B.2 / GRO-883.
func (h *Handler) observabilityPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["observability"]
	rollups := buildTierRollups(h.catalog)
	cellsCovered, cellsEmpty, servicesTracked := 0, 0, 0
	if h.catalog != nil {
		seen := map[string]bool{}
		for _, c := range h.catalog.Cells {
			if c.EndpointCount > 0 {
				cellsCovered++
			} else {
				cellsEmpty++
			}
			for _, s := range c.Services {
				if !seen[s] {
					seen[s] = true
					servicesTracked++
				}
			}
		}
	}
	view := map[string]any{
		"Service":         svc,
		"Categories":      Categories,
		"Active":          "observability",
		"Tenant":          "all",
		"Env":             "lab",
		"Catalog":         h.catalog,
		"CatalogLoaded":   h.catalog != nil,
		"Tiers":           rollups,
		"CellsCovered":    cellsCovered,
		"CellsEmpty":      cellsEmpty,
		"ServicesTracked": servicesTracked,
		"TotalTiers":      len(TierCatalog),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "observability.html", view); err != nil {
		h.logger.Error("observability template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// posLabPage renders the /devops/pos-lab discovery surface — the
// single-transaction firing range. Recovery target:
// Canary/canary/services/health_check/chirp_lab.py (1,045 LOC).
//
// T3B.9 / GRO-892.
func (h *Handler) posLabPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["pos-lab"]
	view := map[string]any{
		"Service":          svc,
		"Categories":       Categories,
		"Active":           "pos-lab",
		"Tenant":           "all",
		"Env":              "lab",
		"Modes":            PosLabModes,
		"TransactionTypes": PosLabTransactionTypes,
		"EntryMethods":     PosLabEntryMethods,
		"CardBrands":       PosLabCardBrands,
		"ModeCount":        len(PosLabModes),
		"TxnTypeCount":     len(PosLabTransactionTypes),
		"EntryMethodCount": len(PosLabEntryMethods),
		"CardBrandCount":   len(PosLabCardBrands),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "pos_lab.html", view); err != nil {
		h.logger.Error("pos-lab template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// scenariosPage renders the /devops/scenarios discovery surface —
// the scripted scenario catalog (8 entries) + randomizer mode that
// the test-lab fires against the TSP pipeline. Recovery target:
// Canary/canary/services/scenario_runner.py + scenario_fire.py +
// scenario_verify.py (~2,432 LOC). Runtime port lives in a follow-on.
//
// T3B.8 / GRO-891.
func (h *Handler) scenariosPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["scenarios"]
	// Compute distinct expected rules + verification query types.
	rules := map[string]bool{}
	queries := map[string]bool{}
	for _, s := range ScenariosCatalog {
		for _, rk := range s.ExpectedRules {
			rules[rk] = true
		}
		for _, q := range s.VerificationQs {
			queries[q] = true
		}
	}
	view := map[string]any{
		"Service":              svc,
		"Categories":           Categories,
		"Active":               "scenarios",
		"Tenant":               "all",
		"Env":                  "lab",
		"Scenarios":            ScenariosCatalog,
		"ScenarioCount":        len(ScenariosCatalog),
		"RandomizerDefault":    10,
		"DistinctRulesCovered": len(rules),
		"VerificationQTypes":   len(queries),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "scenarios.html", view); err != nil {
		h.logger.Error("scenarios template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// testLabPage renders the /devops/test-lab discovery surface — the
// sandbox-guarded operator console for end-to-end pipeline testing.
// Recovery target: Canary/canary/blueprints/ops_console.py +
// templates/ops/test_lab.html (~1,900 LOC). The actual sandbox SDK
// integration + scenario runtime + live-fire dispatch are Phase 3
// follow-on tickets (T3B.8 scenarios, T3B.10 live-fire).
//
// T3B.7 / GRO-890.
func (h *Handler) testLabPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["test-lab"]
	view := map[string]any{
		"Service":    svc,
		"Categories": Categories,
		"Active":     "test-lab",
		"Tenant":     "all",
		"Env":        "lab",
		"Tabs":       TestLabTabs,
		"Stages":     TestLabStages,
		"TabCount":   len(TestLabTabs),
		"StageCount": len(TestLabStages),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "test_lab.html", view); err != nil {
		h.logger.Error("test-lab template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// walletPage renders the /devops/wallet L402 + treasury + Strike Lightning
// discovery surface. Recovery target: Canary/canary/services/goose/ (9
// Python modules totaling 1,311 LOC). Live wallet ledger queries, Strike
// client port, L402 middleware port, and macaroon service port are
// captured as follow-on dispatches. This page makes legible the 9-module
// taxonomy, the 4-state wallet lifecycle, and the 5-step L402 handshake
// so operators understand the contract before the runtime ships.
//
// T3B.6 / GRO-889.
func (h *Handler) walletPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["wallet"]
	view := map[string]any{
		"Service":     svc,
		"Categories":  Categories,
		"Active":      "wallet",
		"Tenant":      "all",
		"Env":         "lab",
		"Modules":     GooseModules,
		"States":      WalletStateMachine,
		"L402Flow":    L402Flow,
		"ModuleCount": len(GooseModules),
		"StateCount":  len(WalletStateMachine),
		"L402Steps":   len(L402Flow),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "wallet.html", view); err != nil {
		h.logger.Error("wallet template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// etlPage renders the /devops/etl daily-batch metrics pipeline discovery
// surface. Recovery target: Canary/canary/services/metrics_etl.py +
// etl_runner.py + period_aggregation.py (~1,500 LOC of Python). The
// actual aggregation queries get ported to Go + sqlc in a separate
// multi-ticket effort. This page makes the contract legible: 2 stages,
// 6 fact tables, idempotent re-runs, daily-batch tier semantics.
//
// T3B.5 / GRO-888.
func (h *Handler) etlPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["etl"]
	view := map[string]any{
		"Service":     svc,
		"Categories":  Categories,
		"Active":      "etl",
		"Tenant":      "all",
		"Env":         "lab",
		"Stages":      ETLPipeline,
		"Tables":      ETLTableCatalog,
		"StageCount":  len(ETLPipeline),
		"TableCount":  len(ETLTableCatalog),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "etl.html", view); err != nil {
		h.logger.Error("etl template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// qaAgentPage renders the /devops/qa-agent discovery surface — the
// canonical six-zone page that explains what the agent is, what tools
// it has, and the sidecar contract by which Canary will dispatch chat
// requests to an Anthropic-SDK-backed runtime.
//
// Recovery target: Canary/canary/services/qa_agent/ (1,071 LOC across
// server.py + agent.py + tools.py + linear_client.py). Full chat runtime
// requires a Go sidecar + dispatch loop + MCP tool registry — separate
// ticket. This page makes the contract legible so operators understand
// the agent's capability surface before the runtime ships.
//
// T3B.4 / GRO-887.
func (h *Handler) qaAgentPage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["qa-agent"]
	totalTools := 0
	for _, c := range QAToolCatalog {
		totalTools += len(c.Tools)
	}
	view := map[string]any{
		"Service":         svc,
		"Categories":      Categories,
		"Active":          "qa-agent",
		"Tenant":          "all",
		"Env":             "lab",
		"ToolCategories":  QAToolCatalog,
		"CategoryCount":   len(QAToolCatalog),
		"TotalTools":      totalTools,
		"DailyMsgBudget":  200, // matches MAX_MESSAGES_PER_DAY in Python prior art
		"SessionMsgLimit": 50,  // matches MAX_MESSAGES_PER_SESSION
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "qa_agent.html", view); err != nil {
		h.logger.Error("qa-agent template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// pipelinePage renders the /devops/pipeline TSP visualization. Recovery
// from Canary/canary/blueprints/devops_monitor.py — ports the operator
// workflow forward (5-stage horizontal flow) without importing anything
// from the Python prototype. Live runtime stats (table counts, stream
// depth, throughput) are out of scope here; they wire when the pipeline
// package exposes a Stats() API.
//
// T3B.3 / GRO-885.
func (h *Handler) pipelinePage(w http.ResponseWriter, r *http.Request) {
	svc := h.index["pipeline"]
	// Count the unique Go packages backing the pipeline for the KPI strip.
	pkgs := map[string]bool{}
	for _, st := range PipelineFlow {
		for _, p := range st.Packages {
			pkgs[p] = true
		}
	}
	view := map[string]any{
		"Service":      svc,
		"Categories":   Categories,
		"Active":       "pipeline",
		"Tenant":       "all",
		"Env":          "lab",
		"Stages":       PipelineFlow,
		"StageCount":   len(PipelineFlow),
		"PackageCount": len(pkgs),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tmpl.ExecuteTemplate(w, "pipeline.html", view); err != nil {
		h.logger.Error("pipeline template", zap.Error(err))
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// buildTierRollups walks the Catalog cells and rolls services up by tier.
// A nil catalog yields rollups with empty service lists — the template
// still renders the tier metadata cards so the page is self-explanatory
// even when devops-catalog.json is absent.
func buildTierRollups(cat *Catalog) []TierRollup {
	out := make([]TierRollup, 0, len(TierCatalog))
	for _, ti := range TierCatalog {
		r := TierRollup{TierInfo: ti}
		if cat != nil {
			seen := map[string]bool{}
			for _, c := range cat.Cells {
				if c.Tier != ti.Name {
					continue
				}
				r.EndpointCount += c.EndpointCount
				for _, s := range c.Services {
					if !seen[s] {
						seen[s] = true
						r.Services = append(r.Services, s)
					}
				}
			}
		}
		out = append(out, r)
	}
	return out
}
