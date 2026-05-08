package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TestTasksList_NoStore_RendersEmptyState — /tasks renders the queue
// page with the empty-state copy when TaskStore is nil.
func TestTasksList_NoStore_RendersEmptyState(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Directed Tasks", "No tasks in this view"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestTasksList_StatusFilter — every filter renders 200; no panic on
// unknown values.
func TestTasksList_StatusFilter(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	for _, status := range []string{"", "open", "queued", "assigned", "in_progress", "complete", "verified", "garbage"} {
		req := httptest.NewRequest(http.MethodGet, "/tasks?status="+status, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status=%q: expected 200 got %d", status, rr.Code)
		}
	}
}

// TestTasksList_FlashRenders — flash query param surfaces in the page.
func TestTasksList_FlashRenders(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/tasks?flash=claimed", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Action: claimed") {
		t.Errorf("flash banner not rendered")
	}
}

// TestReportOTB_NoStore_RendersEmptyState — /reports/otb renders with
// empty-state copy when BillingStore is nil.
func TestReportOTB_NoStore_RendersEmptyState(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/reports/otb", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	for _, want := range []string{"Open-to-Buy", "No OTB budgets configured"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestSuggestedOrders_NoStore_RendersStub — /orders/suggested renders
// the placeholder list (no backing PO model today).
func TestSuggestedOrders_NoStore_RendersStub(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/orders/suggested", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No suggested orders") {
		t.Errorf("expected stub copy")
	}
}

// TestSuggestedOrderActions_RedirectWithFlash — approve / reject / send
// all 303-redirect to /orders/suggested with the action flash query param.
func TestSuggestedOrderActions_RedirectWithFlash(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	cases := []struct {
		path  string
		flash string
	}{
		{"/orders/suggested/" + uuid.NewString() + "/approve", "approved"},
		{"/orders/suggested/" + uuid.NewString() + "/reject", "rejected"},
		{"/orders/suggested/" + uuid.NewString() + "/send", "queued_for_vendor"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, c.path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("path=%s: expected 303 got %d", c.path, rr.Code)
			continue
		}
		loc := rr.Header().Get("Location")
		want := "/orders/suggested?flash=" + c.flash
		if loc != want {
			t.Errorf("path=%s: location=%q want %q", c.path, loc, want)
		}
	}
}

// TestReceivingClose_NoStore_RedirectsWithFlash — close POST against a
// nil InventoryStore redirects with no_store flash (does not 500).
func TestReceivingClose_NoStore_RedirectsWithFlash(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	docID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/receiving/"+docID+"/close", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/receiving/"+docID+"?flash=no_store" {
		t.Errorf("unexpected redirect location: %q", got)
	}
}

// TestReceivingDiscrepancy_NoStore_RedirectsWithFlash — same shape as
// the close POST.
func TestReceivingDiscrepancy_NoStore_RedirectsWithFlash(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	docID := uuid.NewString()
	lineID := uuid.NewString()
	form := url.Values{"reason": {"damage"}}
	req := httptest.NewRequest(http.MethodPost, "/receiving/"+docID+"/lines/"+lineID+"/discrepancy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/receiving/"+docID+"?flash=no_store" {
		t.Errorf("unexpected redirect: %q", got)
	}
}

// TestTaskActions_NoStore_RedirectsWithFlash — claim/complete/exception
// POSTs all redirect with no_store flash when TaskStore is nil.
func TestTaskActions_NoStore_RedirectsWithFlash(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	taskID := uuid.NewString()
	for _, path := range []string{
		"/tasks/" + taskID + "/claim",
		"/tasks/" + taskID + "/complete",
		"/tasks/" + taskID + "/exception",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("path=%s: expected 303 got %d", path, rr.Code)
			continue
		}
		if got := rr.Header().Get("Location"); got != "/tasks?flash=no_store" {
			t.Errorf("path=%s: unexpected redirect %q", path, got)
		}
	}
}

// TestOTBLock_NoStore_RedirectsWithFlash — same shape.
func TestOTBLock_NoStore_RedirectsWithFlash(t *testing.T) {
	h := New(withTestAuth(Deps{}), nil)
	r := chi.NewRouter()
	h.Mount(r)

	budgetID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/reports/otb/"+budgetID+"/lock", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 got %d", rr.Code)
	}
	if got := rr.Header().Get("Location"); got != "/reports/otb?flash=no_store" {
		t.Errorf("unexpected redirect %q", got)
	}
}
