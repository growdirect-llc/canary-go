//go:build integration

package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	lpPkg "github.com/ruptiv/canary/internal/lp"
	"github.com/ruptiv/canary/internal/testutil"
	"github.com/ruptiv/canary/internal/web"
)

// allLPSettingsPaths covers the 10 W1 settings screens.
var allLPSettingsPaths = []string{
	"/settings/allowlist/dead-count",
	"/settings/allowlist/discounts",
	"/settings/allowlist/voids",
	"/settings/allowlist/comps",
	"/settings/training-mode",
	"/settings/alert-routing",
	"/settings/store/drawer",
	"/settings/store/discounts",
	"/settings/store/void-reasons",
	"/settings/store/comp-reasons",
}

// TestLPSettingsPages_RenderWithStore verifies every GET handler returns 200
// when the AllowListStore is wired (real DB, empty seed).
func TestLPSettingsPages_RenderWithStore(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{
		AllowListStore: lpPkg.NewAllowListStore(pool),
	}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, path := range allLPSettingsPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s: expected 200 got %d body=%s", path, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestLPSettings_CreateRoundTrip posts a form to each create endpoint, verifies
// the redirect, then GETs the list and confirms the entry surfaces.
func TestLPSettings_CreateRoundTrip(t *testing.T) {
	pool := testutil.MustConnect(t)
	store := lpPkg.NewAllowListStore(pool)
	deps := web.Deps{AllowListStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	cases := []struct {
		path   string
		form   url.Values
		expect string // substring expected in the GET response after create
	}{
		{
			path:   "/settings/allowlist/dead-count",
			form:   url.Values{"cashier_id": {"C-9001"}, "store": {"STR-A1"}, "reason": {"smoke"}},
			expect: "C-9001",
		},
		{
			path:   "/settings/allowlist/discounts",
			form:   url.Values{"reason_code": {"DC-9002"}, "max_pct": {"15"}, "scope": {"STR-A1"}},
			expect: "DC-9002",
		},
		{
			path:   "/settings/allowlist/voids",
			form:   url.Values{"reason_code": {"VO-9003"}, "description": {"smoke void"}},
			expect: "VO-9003",
		},
		{
			path:   "/settings/allowlist/comps",
			form:   url.Values{"reason_code": {"CO-9004"}, "description": {"smoke comp"}},
			expect: "CO-9004",
		},
		{
			path:   "/settings/training-mode",
			form:   url.Values{"enabled": {"true"}},
			expect: "ON", // template renders ON/OFF based on .Enabled
		},
		{
			path:   "/settings/alert-routing",
			form:   url.Values{"severity": {"high"}, "alert_type": {"AR-9006"}, "store": {"STR-A1"}, "destination": {"queue-9006"}},
			expect: "AR-9006",
		},
		{
			path:   "/settings/store/drawer",
			form:   url.Values{"store": {"STR-A1"}, "threshold": {"99.99"}},
			expect: "99.99",
		},
		{
			path:   "/settings/store/discounts",
			form:   url.Values{"reason_code": {"DCAP-9008"}, "cap_pct": {"25"}, "store": {"STR-A1"}},
			expect: "DCAP-9008",
		},
		{
			path:   "/settings/store/void-reasons",
			form:   url.Values{"reason_code": {"VR-9009"}, "description": {"smoke vr"}},
			expect: "VR-9009",
		},
		{
			path:   "/settings/store/comp-reasons",
			form:   url.Values{"reason_code": {"CR-9010"}, "description": {"smoke cr"}},
			expect: "CR-9010",
		},
	}

	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			// POST the form.
			body := strings.NewReader(c.form.Encode())
			postReq := httptest.NewRequest(http.MethodPost, c.path, body)
			postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			postRR := httptest.NewRecorder()
			r.ServeHTTP(postRR, postReq)
			if postRR.Code != http.StatusSeeOther {
				t.Fatalf("POST %s: expected 303, got %d body=%s", c.path, postRR.Code, postRR.Body.String())
			}
			loc := postRR.Header().Get("Location")
			if loc != c.path {
				t.Errorf("POST %s: redirect = %q, want %q", c.path, loc, c.path)
			}

			// GET the list and confirm the new value renders.
			getReq := httptest.NewRequest(http.MethodGet, c.path, nil)
			getRR := httptest.NewRecorder()
			r.ServeHTTP(getRR, getReq)
			if getRR.Code != http.StatusOK {
				t.Fatalf("GET %s: expected 200, got %d", c.path, getRR.Code)
			}
			if !strings.Contains(getRR.Body.String(), c.expect) {
				t.Errorf("GET %s body missing %q", c.path, c.expect)
			}
		})
	}
}

// TestLPSettings_DeleteRoundTrip seeds an entry, deletes via POST, confirms gone.
func TestLPSettings_DeleteRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := testutil.MustConnect(t)
	store := lpPkg.NewAllowListStore(pool)
	deps := web.Deps{AllowListStore: store}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Seed under tenant Nil (the same one tenantIDFromCtx returns).
	pattern, _ := lpPkg.NewPattern(lpPkg.PatternTypeAllowlist, lpPkg.KindDeadCount, map[string]any{
		"cashier_id": "C-DEL-9999",
	})
	created, err := store.Create(ctx, lpPkg.CreateInput{
		TenantID: uuid.Nil,
		Pattern:  pattern,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// POST delete.
	delPath := "/settings/allowlist/dead-count/" + created.ID.String() + "/delete"
	delReq := httptest.NewRequest(http.MethodPost, delPath, nil)
	delRR := httptest.NewRecorder()
	r.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusSeeOther {
		t.Fatalf("DELETE %s: expected 303, got %d", delPath, delRR.Code)
	}

	// GET the list — entry should not appear.
	getReq := httptest.NewRequest(http.MethodGet, "/settings/allowlist/dead-count", nil)
	getRR := httptest.NewRecorder()
	r.ServeHTTP(getRR, getReq)
	if strings.Contains(getRR.Body.String(), "C-DEL-9999") {
		t.Errorf("entry still present after delete")
	}
}

// TestLPSettings_DeleteUnknownIDIsIdempotent confirms a delete on a nonexistent
// id redirects rather than returning an error.
func TestLPSettings_DeleteUnknownIDIsIdempotent(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{AllowListStore: lpPkg.NewAllowListStore(pool)}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	delPath := "/settings/allowlist/dead-count/" + uuid.New().String() + "/delete"
	req := httptest.NewRequest(http.MethodPost, delPath, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 for missing id, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestLPSettings_PostMissingFieldRedirectsWithError ensures form validation
// failures redirect with an ?error= token rather than a 5xx.
func TestLPSettings_PostMissingFieldRedirectsWithError(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{AllowListStore: lpPkg.NewAllowListStore(pool)}
	h := web.New(withTestAuth(deps), nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Empty form — required field missing.
	body := strings.NewReader(url.Values{}.Encode())
	req := httptest.NewRequest(http.MethodPost, "/settings/allowlist/dead-count", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on missing field, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("redirect = %q, want ?error= token", loc)
	}
}
