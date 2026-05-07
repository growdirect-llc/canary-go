package web

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/alert"
	"github.com/ruptiv/canary/internal/asset"
	"github.com/ruptiv/canary/internal/billing"
	"github.com/ruptiv/canary/internal/casemgmt"
	"github.com/ruptiv/canary/internal/chirp"
	"github.com/ruptiv/canary/internal/customer"
	"github.com/ruptiv/canary/internal/employee"
	"github.com/ruptiv/canary/internal/hierarchy"
	"github.com/ruptiv/canary/internal/inventory"
	"github.com/ruptiv/canary/internal/item"
	lpPkg "github.com/ruptiv/canary/internal/lp"
	"github.com/ruptiv/canary/internal/mcp"
	"github.com/ruptiv/canary/internal/owl"
	"github.com/ruptiv/canary/internal/po"
	"github.com/ruptiv/canary/internal/pricing"
	"github.com/ruptiv/canary/internal/protocol/audit"
	"github.com/ruptiv/canary/internal/protocol/namespace"
	"github.com/ruptiv/canary/internal/protocol/validate"
	"github.com/ruptiv/canary/internal/supplier"
	"github.com/ruptiv/canary/internal/task"
	"github.com/ruptiv/canary/internal/transaction"
	"github.com/ruptiv/canary/internal/workflow"
)

// MerchantResolver derives the authenticated merchant UUID from a
// request. The web handler does not import squareauth — gateway main
// passes squareSvc.MerchantFromRequest in. Returns (uuid.Nil, false)
// when no valid session cookie is present. T-B.
type MerchantResolver func(r *http.Request) (uuid.UUID, bool)

// Deps holds all backend store dependencies for the web handler.
// Each field is optional (nil = use stub data for that domain).
type Deps struct {
	AlertStore       *alert.Store
	CaseStore        *casemgmt.Store
	ChirpStore       chirp.Store // interface
	CustomerStore    *customer.Store
	SubstrateStore   *lpPkg.SubstrateStore
	AllowListStore   *lpPkg.AllowListStore
	TransactionStore *transaction.Store
	ValidateStore    validate.ValidationStore // interface
	InventoryStore   *inventory.Store
	ItemStore        item.Store    // interface
	PricingStore     pricing.Store // interface
	EmployeeStore    *employee.Store
	WorkflowStore    *workflow.Store
	MCPRegistry      *mcp.Registry

	// Protocol portal — concrete pgx-backed stores for the cryptographic
	// substrate readouts. Separate from ValidateStore (which is the
	// L402 charge-flow interface used by gateway POST /v1/validate).
	ProtocolValidate  *validate.PgxStore
	ProtocolNamespace *namespace.Store

	// Owl intelligence portal — tenant-scoped dashboard reads over
	// party.decisioning_facts and detection.detections / detection.cases.
	// Wired W6. Separate from the merchant-keyed Aggregator
	// behind /v1/owl/* JSON API (cmd/owl) — that surface is for external
	// callers; the portal stays tenant-scoped to match every other
	// internal/web/ handler.
	OwlDashboard *owl.DashboardStore

	// Operator workflow surfaces — directed-task queue + L402 OTB budgets.
	// Wired W5 for the /tasks page, /reports/otb action buttons,
	// and the /receiving close / discrepancy POST handlers.
	TaskStore    *task.Store
	BillingStore *billing.Store

	// Asset registry portal — read-only inventory-positions view + lots.
	// Wired W8. Note: today's "asset" surface wraps
	// inventory.inventory_positions; the hardware-asset taxonomy in
	// canary-asset.md (app.assets, asset_lifecycle_events, asset_types)
	// has no migration today and is future work.
	AssetStore *asset.Store

	// Compliance + admin portal — audit log read access. Wired W9 /
	// GRO-828. ISO27001 / users / config-health surfaces stay
	// placeholder pending GRO-769 (identity middleware) + GRO-770
	// (admin module).
	AuditReader *audit.PgxInserter

	// Multi-store intelligence — wired W10. Reads
	// app.location_hierarchy + app.locations and renders the
	// /admin/hierarchy + /dashboards/cross-store + /admin/network-integrity
	// surfaces.
	HierarchyStore *hierarchy.Store

	// Procurement portal — supplier + purchase order lifecycle.
	// Wired W11 over new app.suppliers, app.purchase_orders,
	// app.purchase_order_lines tables (migration 030).
	SupplierStore *supplier.Store
	POStore       *po.Store

	// MerchantResolver derives the authenticated merchant UUID from
	// the session cookie. Wired by gateway main to
	// squareSvc.MerchantFromRequest. When nil (e.g. tests that don't
	// exercise auth), the tenant middleware degrades to the legacy
	// "no resolved tenant" path and tenantIDFromCtx returns uuid.Nil
	// — preserving existing handler behavior. T-B.
	MerchantResolver MerchantResolver
}
