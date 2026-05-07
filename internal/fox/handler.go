// internal/fox/handler.go
package fox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/db/types"
	"github.com/ruptiv/canary/internal/pagination"
	"github.com/ruptiv/canary/internal/party"
)

// Service is the slim interface the Handler depends on. Store
// satisfies it; tests can stub it for the unit-test pass.
type Service interface {
	EscalationStore
	SubjectResolver
	LoadDetection(ctx context.Context, id uuid.UUID) (*types.Detection, error)
	LoadCase(ctx context.Context, id uuid.UUID) (*types.Case, error)
	OpenCase(ctx context.Context, c *types.Case, linkDetection *uuid.UUID) (uuid.UUID, error)
	AppendEvidence(ctx context.Context, e *types.CaseEvidence) error
	AppendAction(ctx context.Context, a *types.CaseAction) error
	CloseCase(ctx context.Context, tenantID, caseID uuid.UUID, resolution string, closedBy *uuid.UUID, notes string) error
	ListEvidence(ctx context.Context, caseID uuid.UUID) ([]types.CaseEvidence, error)
	ListActions(ctx context.Context, caseID uuid.UUID) ([]types.CaseAction, error)
	ListCases(ctx context.Context, tenantID uuid.UUID, filter CaseFilter, limit, offset int) ([]types.Case, error)
}

// Handler wires HTTP endpoints onto a chi.Router.
type Handler struct {
	svc    Service
	party *party.Store // optional: party-based subject resolution
	cfg    EscalationConfig
	logger *zap.Logger
	now    func() time.Time
}

// New constructs a Handler. cfg may be the zero EscalationConfig — the
// constructor substitutes DefaultEscalationConfig() in that case so a
// caller can pass &fox.Handler{} and still get a sensible policy.
func New(svc Service, cfg EscalationConfig, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.MinSeverity == "" {
		cfg = DefaultEscalationConfig()
	}
	return &Handler{
		svc:    svc,
		cfg:    cfg,
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// WithPartyResolver attaches a party.Store so case-open routes
// resolve subjects through party.parties instead of the legacy
// employee_id / customer_id lookup. Per Wave A canonical-data-model-
// party-edits §D and
//
// When set: subjectFromDetection uses
// party.ResolveFromDetection → party.ResolveSubject and falls back
// to the legacy SubjectResolver on party-side error.
// When nil: legacy SubjectResolver path runs unchanged (Loop 2
// behavior, preserved for tests + backward-compat).
func (h *Handler) WithPartyResolver(p *party.Store) *Handler {
	h.party = p
	return h
}

// Mount registers all fox routes under their final URLs.
func (h *Handler) Mount(r chi.Router) {
	r.Route("/v1/fox", func(r chi.Router) {
		r.Post("/cases/from-detection", h.fromDetection)
		r.Post("/cases", h.createCase)
		r.Get("/cases", h.listCases)
		r.Get("/cases/{id}", h.getCase)
		r.Post("/cases/{id}/actions", h.appendAction)
		r.Post("/cases/{id}/close", h.closeCase)
	})
}

// ───────────────────────── request bodies ─────────────────────────

type fromDetectionReq struct {
	DetectionID string `json:"detection_id"`
}

type fromDetectionResp struct {
	CaseID             string `json:"case_id"`
	Opened             bool   `json:"opened"`
	AttachedToExisting bool   `json:"attached_to_existing"`
	Reason             string `json:"reason"`
}

type createCaseReq struct {
	MerchantID string `json:"merchant_id"` // interpreted as tenant_id
	SubjectID    string   `json:"subject_id,omitempty"`
	LocationID   string   `json:"location_id,omitempty"`
	DetectionIDs []string `json:"detection_ids,omitempty"`
	Severity     string   `json:"severity"`
	Title        string   `json:"title,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	OpenedBy     string   `json:"opened_by,omitempty"`
}

type appendActionReq struct {
	ActionType string          `json:"action_type"`
	TakenBy    string          `json:"taken_by,omitempty"`
	Notes      string          `json:"notes,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type closeCaseReq struct {
	Resolution string `json:"resolution"`
	ClosedBy   string `json:"closed_by,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

type caseDetailResp struct {
	Case     *types.Case          `json:"case"`
	Evidence []types.CaseEvidence `json:"evidence"`
	Actions  []types.CaseAction   `json:"actions"`
}

// ───────────────────────── handlers ───────────────────────────────

// fromDetection is the auto-escalation path. POST a single detection
// id; fox decides whether to open a case, attach it to an open one,
// or do nothing.
func (h *Handler) fromDetection(w http.ResponseWriter, r *http.Request) {
	var body fromDetectionReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	detID, err := uuid.Parse(body.DetectionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_detection_id", err.Error())
		return
	}

	det, err := h.svc.LoadDetection(r.Context(), detID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "detection_not_found", "")
			return
		}
		h.logger.Error("load detection", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "load_failed", "")
		return
	}

	decision, err := EvaluateForEscalation(r.Context(), h.svc, det, h.cfg)
	if err != nil {
		h.logger.Error("evaluate", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "evaluate_failed", "")
		return
	}

	switch {
	case decision.OpenNew:
		newCase := &types.Case{
			TenantID:          det.TenantID,
			CaseNumber:        CaseNumber(h.now()),
			CaseType:          "investigation",
			Title:             "Auto-escalated detection " + detID.String()[:8],
			Severity:          det.Severity,
			Status:            string(CaseStatusOpen),
			PrimarySubjectID:  h.subjectFromDetection(r.Context(), det),
			PrimaryLocationID: det.LocationID,
		}
		caseID, err := h.svc.OpenCase(r.Context(), newCase, &detID)
		if err != nil {
			h.logger.Error("open case", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "open_failed", "")
			return
		}
		// Snapshot the triggering detection as the seed evidence so
		// the case has a starting payload + hash chain anchor.
		ev := &types.CaseEvidence{
			TenantID:         det.TenantID,
			CaseID:           caseID,
			EvidenceType:     "transaction_snapshot",
			SourceEntityType: ptrString("q.detections"),
			SourceEntityID:   &detID,
			Payload:          det.Evidence,
		}
		if err := h.svc.AppendEvidence(r.Context(), ev); err != nil {
			// Non-fatal — case is open, evidence retry can come later.
			h.logger.Warn("seed evidence failed", zap.Error(err), zap.String("case_id", caseID.String()))
		}
		writeJSON(w, http.StatusOK, fromDetectionResp{
			CaseID: caseID.String(),
			Opened: true,
			Reason: decision.Reason,
		})

	case decision.AttachToExisting:
		ev := &types.CaseEvidence{
			TenantID:         det.TenantID,
			CaseID:           decision.CaseID,
			EvidenceType:     "transaction_snapshot",
			SourceEntityType: ptrString("q.detections"),
			SourceEntityID:   &detID,
			Payload:          det.Evidence,
		}
		if err := h.svc.AppendEvidence(r.Context(), ev); err != nil {
			h.logger.Error("append evidence", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "evidence_failed", "")
			return
		}
		writeJSON(w, http.StatusOK, fromDetectionResp{
			CaseID:             decision.CaseID.String(),
			AttachedToExisting: true,
			Reason:             decision.Reason,
		})

	default:
		writeJSON(w, http.StatusOK, fromDetectionResp{
			Reason: decision.Reason,
		})
	}
}

// createCase is the manual creation path. Investigator supplies tenant,
// optional subject, optional list of detections to seed evidence from,
// severity, title, notes.
func (h *Handler) createCase(w http.ResponseWriter, r *http.Request) {
	var body createCaseReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	tenantID, err := uuid.Parse(body.MerchantID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
		return
	}
	sev := Severity(body.Severity)
	if !sev.IsValid() {
		writeError(w, http.StatusBadRequest, "invalid_severity", "severity must be low|medium|high|critical")
		return
	}

	var subjectPtr *uuid.UUID
	if body.SubjectID != "" {
		s, err := uuid.Parse(body.SubjectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_subject_id", err.Error())
			return
		}
		subjectPtr = &s
	}
	var locationPtr *uuid.UUID
	if body.LocationID != "" {
		l, err := uuid.Parse(body.LocationID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_location_id", err.Error())
			return
		}
		locationPtr = &l
	}
	var openedByPtr *uuid.UUID
	if body.OpenedBy != "" {
		u, err := uuid.Parse(body.OpenedBy)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_opened_by", err.Error())
			return
		}
		openedByPtr = &u
	}

	title := body.Title
	if title == "" {
		title = "Manual case"
	}
	var notesPtr *string
	if body.Notes != "" {
		notesPtr = &body.Notes
	}
	newCase := &types.Case{
		TenantID:          tenantID,
		CaseNumber:        CaseNumber(h.now()),
		CaseType:          "investigation",
		Title:             title,
		Description:       notesPtr,
		Severity:          string(sev),
		Status:            string(CaseStatusOpen),
		PrimarySubjectID:  subjectPtr,
		PrimaryLocationID: locationPtr,
		AssignedTo:        openedByPtr,
	}
	caseID, err := h.svc.OpenCase(r.Context(), newCase, nil)
	if err != nil {
		h.logger.Error("open case", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "open_failed", "")
		return
	}

	// Seed evidence from each referenced detection. We pull the
	// detection to preserve its payload + link. Failures here are
	// logged but don't fail the request — the case is already open.
	for _, idStr := range body.DetectionIDs {
		detID, err := uuid.Parse(idStr)
		if err != nil {
			h.logger.Warn("skip malformed detection id", zap.String("id", idStr))
			continue
		}
		det, err := h.svc.LoadDetection(r.Context(), detID)
		if err != nil {
			h.logger.Warn("skip missing detection", zap.String("id", idStr), zap.Error(err))
			continue
		}
		ev := &types.CaseEvidence{
			TenantID:         tenantID,
			CaseID:           caseID,
			EvidenceType:     "transaction_snapshot",
			SourceEntityType: ptrString("q.detections"),
			SourceEntityID:   &detID,
			Payload:          det.Evidence,
		}
		if err := h.svc.AppendEvidence(r.Context(), ev); err != nil {
			h.logger.Warn("seed evidence failed",
				zap.String("case_id", caseID.String()),
				zap.String("detection_id", idStr),
				zap.Error(err))
		}
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"case_id":     caseID.String(),
		"case_number": newCase.CaseNumber,
	})
}

// getCase returns the case + its evidence + actions in one round trip.
func (h *Handler) getCase(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	c, err := h.svc.LoadCase(r.Context(), caseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "case_not_found", "")
			return
		}
		h.logger.Error("load case", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "load_failed", "")
		return
	}
	evs, err := h.svc.ListEvidence(r.Context(), caseID)
	if err != nil {
		h.logger.Error("list evidence", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_evidence_failed", "")
		return
	}
	acts, err := h.svc.ListActions(r.Context(), caseID)
	if err != nil {
		h.logger.Error("list actions", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_actions_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, caseDetailResp{Case: c, Evidence: evs, Actions: acts})
}

// listCases is the paginated index. merchant_id is required.
func (h *Handler) listCases(w http.ResponseWriter, r *http.Request) {
	tenantStr := r.URL.Query().Get("merchant_id")
	if tenantStr == "" {
		writeError(w, http.StatusBadRequest, "missing_merchant_id", "merchant_id query param is required")
		return
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
		return
	}
	filter := CaseFilter{Status: r.URL.Query().Get("status")}
	if from := r.URL.Query().Get("from"); from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_from", "from must be RFC3339")
			return
		}
		filter.From = &t
	}
	if to := r.URL.Query().Get("to"); to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_to", "to must be RFC3339")
			return
		}
		filter.To = &t
	}
	page := pagination.FromRequest(r)
	cases, err := h.svc.ListCases(r.Context(), tenantID, filter, page.Limit, page.Offset)
	if err != nil {
		h.logger.Error("list cases", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cases":  cases,
		"limit":  page.Limit,
		"offset": page.Offset,
	})
}

// appendAction is the investigator log endpoint. Tenant is derived
// from the case so callers don't need to pass it.
func (h *Handler) appendAction(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var body appendActionReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if body.ActionType == "" {
		writeError(w, http.StatusBadRequest, "missing_action_type", "")
		return
	}
	c, err := h.svc.LoadCase(r.Context(), caseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "case_not_found", "")
			return
		}
		h.logger.Error("load case", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "load_failed", "")
		return
	}
	var byPtr *uuid.UUID
	if body.TakenBy != "" {
		u, err := uuid.Parse(body.TakenBy)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_taken_by", err.Error())
			return
		}
		byPtr = &u
	}
	// Merge notes + metadata into details JSON. notes wins on key collision.
	details := map[string]any{}
	if len(body.Metadata) > 0 {
		_ = json.Unmarshal(body.Metadata, &details)
	}
	if body.Notes != "" {
		details["notes"] = body.Notes
	}
	detailsJSON, _ := json.Marshal(details)

	a := &types.CaseAction{
		TenantID:    c.TenantID,
		CaseID:      caseID,
		ActionType:  body.ActionType,
		PerformedBy: byPtr,
		Details:     detailsJSON,
	}
	if err := h.svc.AppendAction(r.Context(), a); err != nil {
		h.logger.Error("append action", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "append_failed", "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"action_id": a.ID.String()})
}

// closeCase transitions the case to status='closed' and records the
// resolution.
func (h *Handler) closeCase(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	var body closeCaseReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_body", err.Error())
		return
	}
	if body.Resolution == "" {
		writeError(w, http.StatusBadRequest, "missing_resolution", "resolution is required")
		return
	}
	c, err := h.svc.LoadCase(r.Context(), caseID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "case_not_found", "")
			return
		}
		h.logger.Error("load case", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "load_failed", "")
		return
	}
	var byPtr *uuid.UUID
	if body.ClosedBy != "" {
		u, err := uuid.Parse(body.ClosedBy)
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed_closed_by", err.Error())
			return
		}
		byPtr = &u
	}
	if err := h.svc.CloseCase(r.Context(), c.TenantID, caseID, body.Resolution, byPtr, body.Notes); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "case_not_found", "")
			return
		}
		h.logger.Error("close case", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "close_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"case_id": caseID.String(), "status": "closed"})
}

// ───────────────────────── helpers ────────────────────────────────

// subjectFromDetection resolves a detection's signal subject (cashier
// employee or customer) into a q.subjects.id via the SubjectResolver.
// Returns nil when the detection has no resolvable subject ref or
// when resolution fails — the case's primary_subject_id is nullable
// in the schema, so a missed resolution degrades cleanly.
//
// LAZY mode is the default — this method runs at case-escalation time,
// not on chirp detection write. Detection volume is 100×–1000× case
// volume; eager resolve would burden the hot path with FK lookups for
// signals that 99% never escalate. EAGER mode is reserved for tenants
// with explicit clustering needs on the detection stream itself
// (per-tenant override via app.tenants.attributes->>'subjects_resolve_mode').
//
// Subject precedence: cashier_employee_id wins over customer_id —
// LP cases skew employee-driven per the detection-rule design. When
// neither is present, returns nil and
// the case opens with primary_subject_id NULL.
func (h *Handler) subjectFromDetection(ctx context.Context, det *types.Detection) *uuid.UUID {
	// C.2: when a party.Store is wired, route through party.
	// Per docs/sdds/go-handoff/canonical-data-model-party-edits.md §D.
	if h.party != nil {
		partyID, err := h.party.ResolveFromDetection(ctx, det)
		if err != nil {
			h.logger.Warn("party.ResolveFromDetection failed; falling back to legacy",
				zap.String("tenant", det.TenantID.String()), zap.Error(err))
		} else if partyID != nil {
			subjectID, err := h.party.ResolveSubject(ctx, det.TenantID, *partyID)
			if err != nil {
				h.logger.Warn("party.ResolveSubject failed; falling back to legacy",
					zap.String("tenant", det.TenantID.String()),
					zap.String("party", partyID.String()),
					zap.Error(err))
			} else {
				return &subjectID
			}
		}
	}

	// Legacy path — Loop 2 SubjectResolver behavior. cashier wins
	// over customer (LP cases skew employee-driven).
	switch {
	case det.CashierEmployeeID != nil:
		id, err := h.svc.ResolveSubject(ctx, det.TenantID, SubjectEmployee, *det.CashierEmployeeID)
		if err != nil {
			h.logger.Warn("ResolveSubject(employee) failed; opening case without primary_subject_id",
				zap.String("tenant", det.TenantID.String()),
				zap.String("employee", det.CashierEmployeeID.String()),
				zap.Error(err))
			return nil
		}
		return &id
	case det.CustomerID != nil:
		id, err := h.svc.ResolveSubject(ctx, det.TenantID, SubjectCustomer, *det.CustomerID)
		if err != nil {
			h.logger.Warn("ResolveSubject(customer) failed; opening case without primary_subject_id",
				zap.String("tenant", det.TenantID.String()),
				zap.String("customer", det.CustomerID.String()),
				zap.Error(err))
			return nil
		}
		return &id
	default:
		return nil
	}
}

func ptrString(s string) *string { return &s }

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
