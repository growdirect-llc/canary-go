package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestOnboarding_IndexRedirects(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/onboarding", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/onboarding/connect" {
		t.Errorf("unexpected redirect: %q", rr.Header().Get("Location"))
	}
}

func TestOnboarding_AllSteps(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	for _, c := range []struct {
		path string
		want string
	}{
		{"/onboarding/connect", "Connect POS"},
		{"/onboarding/import", "Import Store Config"},
		{"/onboarding/rules", "Default Rules"},
		{"/onboarding/welcome", "Where to look for your first detection"},
	} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path=%s: expected 200 got %d", c.path, rr.Code)
			continue
		}
		if !strings.Contains(rr.Body.String(), c.want) {
			t.Errorf("path=%s: expected %q in body", c.path, c.want)
		}
	}
}

func TestOnboarding_RulesEnableRedirects(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/onboarding/rules/enable", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/onboarding/rules?flash=enabled" {
		t.Errorf("unexpected redirect: %q", rr.Header().Get("Location"))
	}
}

func TestOnboarding_ProgressBar(t *testing.T) {
	h := New(Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/onboarding/import", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "Step 2 of 4") {
		t.Errorf("expected step indicator")
	}
	if !strings.Contains(body, "width:50%") {
		t.Errorf("expected 50%% progress bar fill")
	}
}
