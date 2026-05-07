package web

import (
	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/asset"
	"github.com/growdirect-llc/rapidpos/internal/billing"
	"github.com/growdirect-llc/rapidpos/internal/casemgmt"
	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/customer"
	"github.com/growdirect-llc/rapidpos/internal/employee"
	"github.com/growdirect-llc/rapidpos/internal/hierarchy"
	"github.com/growdirect-llc/rapidpos/internal/inventory"
	"github.com/growdirect-llc/rapidpos/internal/item"
	"github.com/growdirect-llc/rapidpos/internal/mcp"
	lpPkg "github.com/growdirect-llc/rapidpos/internal/lp"
	"github.com/growdirect-llc/rapidpos/internal/owl"
	"github.com/growdirect-llc/rapidpos/internal/pricing"
	"github.com/growdirect-llc/rapidpos/internal/protocol/audit"
	"github.com/growdirect-llc/rapidpos/internal/protocol/namespace"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/task"
	"github.com/growdirect-llc/rapidpos/internal/transaction"
	"github.com/growdirect-llc/rapidpos/internal/workflow"
)

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
	// Wired W6 / GRO-825. Separate from the merchant-keyed Aggregator
	// behind /v1/owl/* JSON API (cmd/owl) — that surface is for external
	// callers; the portal stays tenant-scoped to match every other
	// internal/web/ handler.
	OwlDashboard *owl.DashboardStore

	// Operator workflow surfaces — directed-task queue + L402 OTB budgets.
	// Wired W5 / GRO-824 for the /tasks page, /reports/otb action buttons,
	// and the /receiving close / discrepancy POST handlers.
	TaskStore    *task.Store
	BillingStore *billing.Store

	// Asset registry portal — read-only inventory-positions view + lots.
	// Wired W8 / GRO-827. Note: today's "asset" surface wraps
	// inventory.inventory_positions; the hardware-asset taxonomy in
	// canary-asset.md (app.assets, asset_lifecycle_events, asset_types)
	// has no migration today and is future work.
	AssetStore *asset.Store

	// Compliance + admin portal — audit log read access. Wired W9 /
	// GRO-828. ISO27001 / users / config-health surfaces stay
	// placeholder pending GRO-769 (identity middleware) + GRO-770
	// (admin module).
	AuditReader *audit.PgxInserter

	// Multi-store intelligence — wired W10 / GRO-829. Reads
	// app.location_hierarchy + app.locations and renders the
	// /admin/hierarchy + /dashboards/cross-store + /admin/network-integrity
	// surfaces.
	HierarchyStore *hierarchy.Store
}
