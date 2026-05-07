package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestMobile_NoStores_RenderAllPages(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	for _, c := range []struct {
		path string
		want string
	}{
		{"/m/tasks", "Tasks"},
		{"/m/receiving", "Receiving"},
		{"/m/cycle-count", "Cycle Count"},
		{"/m/alerts/" + uuid.NewString(), "Alert"},
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
		// Mobile shell should NOT carry the desktop sidebar.
		if strings.Contains(rr.Body.String(), `<aside class="sidebar">`) {
			t.Errorf("path=%s: mobile page leaked desktop sidebar", c.path)
		}
		// Should have viewport meta for mobile.
		if !strings.Contains(rr.Body.String(), "width=device-width") {
			t.Errorf("path=%s: missing viewport meta", c.path)
		}
	}
}

func TestMobileTasks_OpenStateRenders(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/m/tasks", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No open tasks") {
		t.Errorf("expected empty-state copy")
	}
}

func TestMobileAlertDetail_BadID_RendersNotFoundShell(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/m/alerts/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}
