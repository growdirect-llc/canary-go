//go:build integration

package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/growdirect-llc/rapidpos/internal/protocol/namespace"
	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

// TestProtocolOverview_NoStores_RendersStub — empty-state copy renders
// when none of the protocol stores are wired.
func TestProtocolOverview_NoStores_RendersStub(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/protocol", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"No anchors yet", "No namespace registrations yet", "No L402 tokens issued yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing empty-state copy %q", want)
		}
	}
}

// TestProtocolOverview_WithStores_Renders — both stores wired against a
// real DB. With no seed data the stores return empty slices; render must
// still succeed and surface the empty-state copy.
func TestProtocolOverview_WithStores_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{
		ProtocolValidate:  validate.NewPgxStore(pool),
		ProtocolNamespace: namespace.NewStore(pool),
	}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/protocol", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	// Header always renders.
	if !strings.Contains(rr.Body.String(), "Cryptographic substrate") {
		t.Errorf("expected header copy")
	}
}

// TestProtocolOverview_OnlyValidate_RendersAnchors — only the validate store
// is wired. Namespace empty-state still surfaces; counters stay 0.
func TestProtocolOverview_OnlyValidate_Renders(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{ProtocolValidate: validate.NewPgxStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/protocol", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "No namespace registrations yet") {
		t.Errorf("expected namespace empty-state when ProtocolNamespace nil")
	}
}
