package web

import (
	"net/http"

	"github.com/google/uuid"
)

// testTenant is a fixed UUID handler tests use as the resolved
// merchant for requests routed through Mount(). Pre-T-B the tests
// implicitly ran with tenant=Nil; T-B Phase 2 wraps every protected
// route in requireTenantMiddleware, so tests must inject a tenant
// upstream. withTestAuth() does that without each test having to
// fake a session cookie.
var testTenant = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// withTestAuth attaches a stub MerchantResolver to deps so that
// requireTenantMiddleware sees a resolved tenant and lets the
// request through. Handler tests should use this to focus on page
// rendering, not auth plumbing.
//
// Production wiring (cmd/gateway/main.go) supplies the real resolver
// from squareSvc.MerchantFromRequest — this stub never runs there.
func withTestAuth(d Deps) Deps {
	d.MerchantResolver = func(*http.Request) (uuid.UUID, bool) {
		return testTenant, true
	}
	return d
}
