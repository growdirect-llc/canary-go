// Package identity — canonical API-key scope vocabulary.
//
// Scopes are short verb:resource strings stored in app.api_keys.scopes
// and checked at the handler boundary via RequireScope (handler-level)
// or RequireScopeMiddleware (chi middleware). The vocabulary lives
// here as constants so that every call site references a single
// declaration — preventing the silent-typo failure mode where a route
// requires "transactions:read" while a key is granted "transaction:read"
// and the mismatch passes 403 silently.
//
// Naming convention:
//
//	<resource>:<action>    e.g. transaction:read, alert:write
//
//	<resource>             singular noun, lowercase, no underscores;
//	                       matches the URL path segment when applicable
//	                       (e.g. /v1/alerts → alert:*)
//	<action>               read | write | admin
//	                       — read covers GET/HEAD
//	                       — write covers POST/PUT/PATCH/DELETE
//	                       — admin is a strict superset, reserved for
//	                         operations that bypass tenant boundaries
//	                         (DLQ replay, identity rotation, etc.)
//
// Where the convention deviates (e.g. dlq:replay rather than dlq:write),
// the deviation is documented next to the constant. Most pre-existing
// scope strings in the seed and tests fit the read/write pattern; the
// few that don't are kept verbatim for backward compatibility.
package identity

// ── Resource: transaction ──────────────────────────────────────────────
//
// /v1/transactions/* — Module T (cmd/transaction).
const (
	ScopeTransactionRead  = "transaction:read"
	ScopeTransactionWrite = "transaction:write"
)

// ── Resource: customer ─────────────────────────────────────────────────
//
// /v1/customers/* — Module C (cmd/customer).
const (
	ScopeCustomerRead  = "customer:read"
	ScopeCustomerWrite = "customer:write"
)

// ── Resource: employee ─────────────────────────────────────────────────
//
// /v1/employees/* — Module E (cmd/employee).
const (
	ScopeEmployeeRead  = "employee:read"
	ScopeEmployeeWrite = "employee:write"
)

// ── Resource: asset ────────────────────────────────────────────────────
//
// /v1/assets/* — Module A (cmd/asset).
const (
	ScopeAssetRead  = "asset:read"
	ScopeAssetWrite = "asset:write"
)

// ── Resource: analytics ────────────────────────────────────────────────
//
// /v1/analytics/* — Module An (cmd/analytics). Read-only surface today;
// no write actions in this binary. ScopeAnalyticsWrite reserved for
// future drill-down save endpoints.
const (
	ScopeAnalyticsRead  = "analytics:read"
	ScopeAnalyticsWrite = "analytics:write"
)

// ── Resource: returns ──────────────────────────────────────────────────
//
// /v1/returns/* — Module R (cmd/returns).
const (
	ScopeReturnsRead  = "returns:read"
	ScopeReturnsWrite = "returns:write"
)

// ── Resource: report ───────────────────────────────────────────────────
//
// /v1/reports/* — Module Rep (cmd/report). create-job is a write;
// list/get are reads.
const (
	ScopeReportRead  = "report:read"
	ScopeReportWrite = "report:write"
)

// ── Resource: owl ──────────────────────────────────────────────────────
//
// /v1/owl/* — Module O (cmd/owl). Decisioning facts refresh is a write;
// dashboards / RFM lookups are reads.
const (
	ScopeOwlRead  = "owl:read"
	ScopeOwlWrite = "owl:write"
)

// ── Resource: alert ────────────────────────────────────────────────────
//
// /v1/alerts/* — Module Al (cmd/alert). acknowledge / resolve / suppress
// are writes; list / stats / get are reads.
const (
	ScopeAlertRead  = "alert:read"
	ScopeAlertWrite = "alert:write"
)

// ── Resource: task ─────────────────────────────────────────────────────
//
// /v1/tasks/* — Module B's directed-work queue (cmd/bull, internal/task).
const (
	ScopeTaskRead  = "task:read"
	ScopeTaskWrite = "task:write"
)

// ── Resource: billing ──────────────────────────────────────────────────
//
// /v1/billing/* — Module B's L402 + OTB surface (cmd/bull, internal/billing).
const (
	ScopeBillingRead  = "billing:read"
	ScopeBillingWrite = "billing:write"
)

// ── Pre-existing resources (kept verbatim for backward compatibility) ──
//
// These predate the GRO-906 vocabulary and are referenced by the seed,
// the gateway admin, hawk, the protocol pipeline, and identity itself.
// Listed here so a single grep tells the full story.
const (
	// Hawk — case management.
	ScopeCaseRead  = "case:read"
	ScopeCaseWrite = "case:write"

	// Gateway admin — DLQ inspection + replay.
	ScopeDLQRead   = "dlq:read"
	ScopeDLQReplay = "dlq:replay" // verb deviates from read/write — operational replay action.

	// Protocol pipeline — sub1 / sub2 / sub3 evidence, gateway internal.
	ScopeEvidenceRead   = "evidence:read"
	ScopeEvidenceWrite  = "evidence:write"
	ScopeWebhookWrite   = "webhook:write"
	ScopeWebhookBP      = "webhook:bp"          // bonded-payload bridge.
	ScopeWebhookIdem    = "webhook:idempotency" // idempotency reservation.
	ScopeGatewayNonce   = "gateway:nonce"       // nonce mint.
	ScopeProtocolEvents = "protocol:events"     // raw event ingest.

	// Identity surface — self-introspection + admin.
	ScopeIdentityMe    = "identity:me"
	ScopeIdentityAdmin = "identity:admin"

	// Ledger — L402 OTB ledger reads.
	ScopeLedgerRead = "ledger:read"

	// Inventory — replenishment trigger publish.
	ScopeInventoryReplenish = "inventory:replenish"
)
