package web

import (
	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/casemgmt"
	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/customer"
	"github.com/growdirect-llc/rapidpos/internal/employee"
	"github.com/growdirect-llc/rapidpos/internal/inventory"
	"github.com/growdirect-llc/rapidpos/internal/item"
	lpPkg "github.com/growdirect-llc/rapidpos/internal/lp"
	"github.com/growdirect-llc/rapidpos/internal/pricing"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/transaction"
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
}
