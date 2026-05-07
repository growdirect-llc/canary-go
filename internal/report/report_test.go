package report

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/identity"
)

func TestHandlerMount_RegistersRoutes(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)

	want := map[string]bool{
		"POST /v1/reports":              true,
		"GET /v1/reports":               true,
		"GET /v1/reports/{job_id}":      true,
	}
	got := map[string]bool{}
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	})
	for k := range want {
		if !got[k] {
			t.Errorf("missing route: %s", k)
		}
	}
}

func TestHandler_NoAuth_Returns401(t *testing.T) {
	endpoints := []struct{ method, path string }{
		{http.MethodPost, "/v1/reports"},
		{http.MethodGet, "/v1/reports"},
		{http.MethodGet, "/v1/reports/" + uuid.New().String()},
	}
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", ep.method, ep.path, w.Code)
		}
	}
}

func TestHandlerCreate_ValidRequest(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()

	body, _ := json.Marshal(ReportRequest{
		ReportType: ReportTypeSalesSummary,
		From:       "2026-01-01",
		To:         "2026-01-31",
		Format:     "csv",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	var job ReportJob
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.JobID == uuid.Nil {
		t.Error("job_id should not be nil")
	}
	if job.Status != JobStatusPending {
		t.Errorf("status = %q, want pending", job.Status)
	}
}

func TestHandlerCreate_InvalidReportType(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()

	body, _ := json.Marshal(ReportRequest{ReportType: "bogus"})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandlerCreate_MissingReportType(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()

	body, _ := json.Marshal(ReportRequest{From: "2026-01-01"})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
		TenantID:   tid,
		AuthMethod: "apikey",
	}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["code"] != "missing_report_type" {
		t.Errorf("code = %q, want missing_report_type", resp["code"])
	}
}

func TestHandlerGetJob_RoundTrip(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()
	claims := identity.Claims{TenantID: tid, AuthMethod: "apikey"}

	// Create
	body, _ := json.Marshal(ReportRequest{ReportType: ReportTypeReturnDetail, From: "2026-01-01", To: "2026-01-31"})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), claims))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create: status = %d", w.Code)
	}
	var job ReportJob
	_ = json.NewDecoder(w.Body).Decode(&job)

	// Fetch by ID
	req2 := httptest.NewRequest(http.MethodGet, "/v1/reports/"+job.JobID.String(), nil)
	req2 = req2.WithContext(identity.InjectClaims(req2.Context(), claims))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("get: status = %d, want 200", w2.Code)
	}
	var fetched ReportJob
	_ = json.NewDecoder(w2.Body).Decode(&fetched)
	if fetched.JobID != job.JobID {
		t.Errorf("job_id mismatch: got %v want %v", fetched.JobID, job.JobID)
	}
}

func TestHandlerGetJob_WrongTenant(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()

	// Create under tid
	body, _ := json.Marshal(ReportRequest{ReportType: ReportTypeShrink, From: "2026-01-01", To: "2026-01-31"})
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{TenantID: tid, AuthMethod: "apikey"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var job ReportJob
	_ = json.NewDecoder(w.Body).Decode(&job)

	// Fetch under a different tenant
	req2 := httptest.NewRequest(http.MethodGet, "/v1/reports/"+job.JobID.String(), nil)
	req2 = req2.WithContext(identity.InjectClaims(req2.Context(), identity.Claims{TenantID: uuid.New(), AuthMethod: "apikey"}))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("cross-tenant fetch: status = %d, want 404", w2.Code)
	}
}

func TestHandlerGetJob_MalformedJobID(t *testing.T) {
	r := chi.NewRouter()
	h := New(NewStore(), nil)
	h.Mount(r)
	tid := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/reports/not-a-uuid", nil)
	req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{TenantID: tid, AuthMethod: "apikey"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
