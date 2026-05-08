package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestLogin_PageRendersStandalone confirms /login is a public route that
// returns 200 with a "Connect Your POS" call-to-action. Standalone
// template (no merchant sidebar) — first-time users haven't authenticated.
//
// Bug context: prior to this fix, no /login route existed and unauthenticated
// users were dumped onto /connect (the data-sync picker) which renders the
// merchant sidebar. The user reported "land on data sync picker no login".
func TestLogin_PageRendersStandalone(t *testing.T) {
	h := New(Deps{}, nil) // no auth resolver — login must be public
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}

	body := rr.Body.String()
	for _, want := range []string{
		"Canary",                    // brand
		"Connect Your Square",       // primary CTA
		`href="/auth/square"`,       // OAuth start link
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}

	// Standalone — should NOT render the merchant sidebar (which would
	// link to /alerts, /chirps, /dashboard, etc.). Tests that the user
	// isn't seeing a logged-in shell while not logged in.
	for _, mustNotContain := range []string{
		`href="/alerts"`,
		`href="/chirps"`,
		`href="/dashboard"`,
	} {
		if strings.Contains(body, mustNotContain) {
			t.Errorf("login page should not render merchant sidebar (found %q)", mustNotContain)
		}
	}
}

// TestLogout_ClearsCookieAndRedirects confirms GET /auth/logout clears the
// demo_merchant session cookie and redirects to /login. The sidebar in
// base.html links to /auth/logout — prior to this fix it 404'd.
func TestLogout_ClearsCookieAndRedirects(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	// Pretend we had a session.
	req.AddCookie(&http.Cookie{Name: "demo_merchant", Value: "some-signed-value"})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound && rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 302/303 got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("logout redirect = %q, want /login", loc)
	}

	// Clearing means MaxAge<0 OR Expires in the past; net/http's cookie
	// header writer accepts either. Just verify the cookie is in the
	// response Set-Cookie with empty value.
	cookies := rr.Result().Cookies()
	var cleared bool
	for _, c := range cookies {
		if c.Name == "demo_merchant" {
			if c.Value == "" || c.MaxAge < 0 {
				cleared = true
			}
		}
	}
	if !cleared {
		t.Errorf("logout did not emit a clearing Set-Cookie for demo_merchant; got cookies %+v", cookies)
	}
}

// TestRequireTenantMiddleware_RedirectsToLogin checks that when the
// tenant gate fires (no merchant resolved), the redirect target is
// /login (not /connect). /connect is the post-OAuth data-sync picker
// — sending unauthenticated users there shows them a configuration UI
// dressed up as a logged-in merchant. /login is the right landing.
func TestRequireTenantMiddleware_RedirectsToLogin(t *testing.T) {
	// No MerchantResolver — tenant stays uuid.Nil — middleware should
	// redirect.
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302 got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("/dashboard (no tenant) redirect = %q, want /login", loc)
	}
}
