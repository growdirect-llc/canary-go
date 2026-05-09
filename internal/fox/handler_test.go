// internal/fox/handler_test.go
package fox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/db/types"
	"github.com/ruptiv/canary/internal/identity"
)

// stubService is a fully in-memory implementation of fox.Service for
// handler tests. It tracks the most recent inputs so tests can assert
// the handler called the right method with the right shape.
type stubService struct {
	// Plumbing
	detections map[uuid.UUID]*types.Detection
	cases      map[uuid.UUID]*types.Case
	evidence   []types.CaseEvidence
	actions    []types.CaseAction
	openCases  map[uuid.UUID]*types.Case // keyed by primary_subject_id

	// Hooks
	openCaseErr error
}

func newStubService() *stubService {
	return &stubService{
		detections: map[uuid.UUID]*types.Detection{},
		cases:      map[uuid.UUID]*types.Case{},
		openCases:  map[uuid.UUID]*types.Case{},
	}
}

func (s *stubService) FindOpenCaseBySubject(_ context.Context, tenantID, subjectID uuid.UUID) (*types.Case, error) {
	c := s.openCases[subjectID]
	if c == nil || c.TenantID != tenantID {
		return nil, nil
	}
	return c, nil
}

// ResolveSubject — stub: deterministic UUID derived from refID so the
// stub's behavior matches the production resolver's idempotency
// (same input → same output id).
func (s *stubService) ResolveSubject(_ context.Context, _ uuid.UUID, _ SubjectKind, refID uuid.UUID) (uuid.UUID, error) {
	return refID, nil
}

func (s *stubService) LoadDetection(_ context.Context, id uuid.UUID) (*types.Detection, error) {
	d, ok := s.detections[id]
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *stubService) LoadCase(_ context.Context, id uuid.UUID) (*types.Case, error) {
	c, ok := s.cases[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (s *stubService) LoadDetectionScoped(_ context.Context, tenantID, id uuid.UUID) (*types.Detection, error) {
	d, ok := s.detections[id]
	if !ok || d.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *stubService) LoadCaseScoped(_ context.Context, tenantID, id uuid.UUID) (*types.Case, error) {
	c, ok := s.cases[id]
	if !ok || c.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return c, nil
}

func (s *stubService) OpenCase(_ context.Context, c *types.Case, link *uuid.UUID) (uuid.UUID, error) {
	if s.openCaseErr != nil {
		return uuid.Nil, s.openCaseErr
	}
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	s.cases[c.ID] = c
	if c.PrimarySubjectID != nil {
		s.openCases[*c.PrimarySubjectID] = c
	}
	if link != nil {
		if d := s.detections[*link]; d != nil {
			id := c.ID
			d.CaseID = &id
			d.Status = "escalated_to_case"
		}
	}
	return c.ID, nil
}

func (s *stubService) AppendEvidence(_ context.Context, e *types.CaseEvidence) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	s.evidence = append(s.evidence, *e)
	return nil
}

func (s *stubService) AppendAction(_ context.Context, a *types.CaseAction) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	s.actions = append(s.actions, *a)
	return nil
}

func (s *stubService) CloseCase(_ context.Context, tenantID, caseID uuid.UUID, resolution string, by *uuid.UUID, notes string) error {
	c, ok := s.cases[caseID]
	if !ok || c.TenantID != tenantID {
		return ErrNotFound
	}
	c.Status = string(CaseStatusClosed)
	c.ResolutionType = &resolution
	return nil
}

func (s *stubService) ListEvidence(_ context.Context, caseID uuid.UUID) ([]types.CaseEvidence, error) {
	out := make([]types.CaseEvidence, 0)
	for _, e := range s.evidence {
		if e.CaseID == caseID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *stubService) ListActions(_ context.Context, caseID uuid.UUID) ([]types.CaseAction, error) {
	out := make([]types.CaseAction, 0)
	for _, a := range s.actions {
		if a.CaseID == caseID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubService) ListCases(_ context.Context, tenantID uuid.UUID, filter CaseFilter, limit, offset int) ([]types.Case, error) {
	out := make([]types.Case, 0)
	for _, c := range s.cases {
		if c.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && c.Status != filter.Status {
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}

// ───────────────────────── helpers ──────────────────────────────

func newTestRouter(svc Service) chi.Router {
	h := New(svc, DefaultEscalationConfig(), nil)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// doJSON sends a request as the given tenant. Pass uuid.Nil to send
// without claims (used to assert the 401 path).
func doJSON(t *testing.T, r chi.Router, tenantID uuid.UUID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if tenantID != uuid.Nil {
		req = req.WithContext(identity.InjectClaims(req.Context(), identity.Claims{
			TenantID:   tenantID,
			AgentName:  "test-agent",
			AuthMethod: identity.AuthMethodAPIKey,
		}))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// ───────────────────────── tests ────────────────────────────────

func TestHandler_FromDetection_OpensNew(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	cashier := uuid.New()
	det := &types.Detection{
		ID:                uuid.New(),
		TenantID:          tenantID,
		Severity:          "high",
		Status:            "new",
		CashierEmployeeID: &cashier,
		Evidence:          json.RawMessage(`{"k":"v"}`),
	}
	svc.detections[det.ID] = det
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/from-detection",
		map[string]string{"detection_id": det.ID.String()})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Opened {
		t.Errorf("expected opened=true, got %+v", resp)
	}
	if resp.CaseID == "" {
		t.Errorf("case_id should be set")
	}
	if len(svc.evidence) != 1 {
		t.Errorf("expected 1 seed evidence row, got %d", len(svc.evidence))
	}
}

func TestHandler_FromDetection_AttachesToOpen(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	cashier := uuid.New()
	existing := &types.Case{
		ID:               uuid.New(),
		TenantID:         tenantID,
		Status:           "open",
		PrimarySubjectID: &cashier,
	}
	svc.cases[existing.ID] = existing
	svc.openCases[cashier] = existing
	det := &types.Detection{
		ID:                uuid.New(),
		TenantID:          tenantID,
		Severity:          "high",
		Status:            "new",
		CashierEmployeeID: &cashier,
		Evidence:          json.RawMessage(`{}`),
	}
	svc.detections[det.ID] = det
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/from-detection",
		map[string]string{"detection_id": det.ID.String()})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.AttachedToExisting {
		t.Errorf("expected attached=true, got %+v", resp)
	}
	if resp.CaseID != existing.ID.String() {
		t.Errorf("case id mismatch: got %s want %s", resp.CaseID, existing.ID)
	}
	if len(svc.evidence) != 1 {
		t.Errorf("expected 1 attached evidence row, got %d", len(svc.evidence))
	}
}

func TestHandler_FromDetection_NoActionLow(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	det := &types.Detection{
		ID:       uuid.New(),
		TenantID: tenantID,
		Severity: "low",
		Status:   "new",
		Evidence: json.RawMessage(`{}`),
	}
	svc.detections[det.ID] = det
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/from-detection",
		map[string]string{"detection_id": det.ID.String()})

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var resp fromDetectionResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Opened || resp.AttachedToExisting {
		t.Errorf("expected no action, got %+v", resp)
	}
	if len(svc.cases) != 0 {
		t.Errorf("no case should have been opened")
	}
}

func TestHandler_FromDetection_NotFound(t *testing.T) {
	svc := newStubService()
	r := newTestRouter(svc)
	rec := doJSON(t, r, uuid.New(), http.MethodPost, "/v1/fox/cases/from-detection",
		map[string]string{"detection_id": uuid.New().String()})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rec.Code)
	}
}

func TestHandler_FromDetection_MalformedID(t *testing.T) {
	svc := newStubService()
	r := newTestRouter(svc)
	rec := doJSON(t, r, uuid.New(), http.MethodPost, "/v1/fox/cases/from-detection",
		map[string]string{"detection_id": "not-a-uuid"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rec.Code)
	}
}

func TestHandler_CreateCase_Manual(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	r := newTestRouter(svc)

	// merchant_id in body matches the authenticated tenant — should pass.
	body := map[string]any{
		"merchant_id": tenantID.String(),
		"severity":    "high",
		"title":       "Investigator-opened",
		"notes":       "Patterned drawer variance",
	}
	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(svc.cases))
	}
}

func TestHandler_CreateCase_InvalidSeverity(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	r := newTestRouter(svc)
	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases",
		map[string]any{"merchant_id": tenantID.String(), "severity": "extreme"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rec.Code)
	}
}

// TestHandler_CreateCase_BodyTenantMismatch verifies the
// AssertBodyTenantMatches defense: a body whose merchant_id names a
// different tenant than the authenticated one is rejected with 403.
func TestHandler_CreateCase_BodyTenantMismatch(t *testing.T) {
	svc := newStubService()
	tenantA := uuid.New()
	tenantB := uuid.New()
	r := newTestRouter(svc)

	body := map[string]any{
		"merchant_id": tenantB.String(), // body says B
		"severity":    "high",
	}
	rec := doJSON(t, r, tenantA, http.MethodPost, "/v1/fox/cases", body) // auth says A
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d body=%s want 403", rec.Code, rec.Body.String())
	}
	if len(svc.cases) != 0 {
		t.Errorf("expected no case to be created, got %d", len(svc.cases))
	}
}

// TestHandler_Unauthenticated_401 verifies every protected endpoint
// rejects requests with no claims attached.
func TestHandler_Unauthenticated_401(t *testing.T) {
	svc := newStubService()
	r := newTestRouter(svc)

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"from-detection", http.MethodPost, "/v1/fox/cases/from-detection",
			map[string]string{"detection_id": uuid.New().String()}},
		{"createCase", http.MethodPost, "/v1/fox/cases",
			map[string]any{"severity": "high"}},
		{"listCases", http.MethodGet, "/v1/fox/cases", nil},
		{"getCase", http.MethodGet, "/v1/fox/cases/" + uuid.New().String(), nil},
		{"appendAction", http.MethodPost, "/v1/fox/cases/" + uuid.New().String() + "/actions",
			map[string]any{"action_type": "note"}},
		{"closeCase", http.MethodPost, "/v1/fox/cases/" + uuid.New().String() + "/close",
			map[string]any{"resolution": "substantiated"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doJSON(t, r, uuid.Nil, c.method, c.path, c.body)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s: got %d body=%s want 401", c.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandler_GetCase(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c := &types.Case{ID: uuid.New(), TenantID: tenantID, CaseNumber: "FOX-1", Severity: "high", Status: "open"}
	svc.cases[c.ID] = c
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodGet, "/v1/fox/cases/"+c.ID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var resp caseDetailResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Case.ID != c.ID {
		t.Errorf("id mismatch")
	}
}

func TestHandler_GetCase_NotFound(t *testing.T) {
	svc := newStubService()
	r := newTestRouter(svc)
	rec := doJSON(t, r, uuid.New(), http.MethodGet, "/v1/fox/cases/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d", rec.Code)
	}
}

func TestHandler_ListCases_Filtered(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c1 := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "open"}
	c2 := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "closed"}
	svc.cases[c1.ID] = c1
	svc.cases[c2.ID] = c2
	r := newTestRouter(svc)

	// Tenant comes from claims, not the query string.
	rec := doJSON(t, r, tenantID, http.MethodGet, "/v1/fox/cases?status=open", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Cases []types.Case `json:"cases"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(resp.Cases))
	}
}

func TestHandler_AppendAction(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "open"}
	svc.cases[c.ID] = c
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/"+c.ID.String()+"/actions",
		map[string]any{"action_type": "note", "notes": "follow-up tomorrow"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(svc.actions))
	}
}

func TestHandler_AppendAction_RequiresActionType(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "open"}
	svc.cases[c.ID] = c
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/"+c.ID.String()+"/actions",
		map[string]any{"notes": "missing type"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rec.Code)
	}
}

func TestHandler_CloseCase(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "open"}
	svc.cases[c.ID] = c
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/"+c.ID.String()+"/close",
		map[string]any{"resolution": "substantiated", "notes": "termination filed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if c.Status != string(CaseStatusClosed) {
		t.Errorf("case status: got %s want closed", c.Status)
	}
}

func TestHandler_CloseCase_RequiresResolution(t *testing.T) {
	svc := newStubService()
	tenantID := uuid.New()
	c := &types.Case{ID: uuid.New(), TenantID: tenantID, Status: "open"}
	svc.cases[c.ID] = c
	r := newTestRouter(svc)

	rec := doJSON(t, r, tenantID, http.MethodPost, "/v1/fox/cases/"+c.ID.String()+"/close",
		map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d", rec.Code)
	}
}
