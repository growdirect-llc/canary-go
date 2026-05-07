package web

import (
	"github.com/growdirect-llc/rapidpos/internal/alert"
	"github.com/growdirect-llc/rapidpos/internal/casemgmt"
	"github.com/growdirect-llc/rapidpos/internal/chirp"
	"github.com/growdirect-llc/rapidpos/internal/customer"
	lpPkg "github.com/growdirect-llc/rapidpos/internal/lp"
)

// Deps holds all backend store dependencies for the web handler.
// Each field is optional (nil = use stub data for that domain).
type Deps struct {
	AlertStore     *alert.Store
	CaseStore      *casemgmt.Store
	ChirpStore     chirp.Store // interface
	CustomerStore  *customer.Store
	SubstrateStore *lpPkg.SubstrateStore
	AllowListStore *lpPkg.AllowListStore
}
