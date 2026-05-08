//go:build integration

package web_test

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/web"
)

// withTestAuth attaches a fixed-tenant MerchantResolver to deps so
// integration tests routed through h.Mount(r) bypass the T-B
// requireTenant gate. The integration suites exercise handler
// behavior against a real database — auth plumbing is out of scope
// for them.
func withTestAuth(d web.Deps) web.Deps {
	d.MerchantResolver = func(*http.Request) (uuid.UUID, bool) {
		return uuid.MustParse("00000000-0000-0000-0000-000000000001"), true
	}
	return d
}
