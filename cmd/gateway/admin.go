// cmd/gateway/admin.go
//
// Admin-scoped endpoints under /v1/webhooks/*. Authenticated via
// X-Canary-API-Key against app.api_keys. The
// dlq:replay scope is required for replay calls; dlq:read for list.
//
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/protocol/publisher"
	"github.com/ruptiv/canary/internal/webhook"
)

// dlqStore is the slice of *webhook.DLQ the admin endpoints actually
// touch. Held as an interface so handler tests can stub it without
// pulling in a real Postgres pool.
type dlqStore interface {
	Get(ctx context.Context, id uuid.UUID) (*webhook.DLQRow, error)
	List(ctx context.Context, f webhook.ListFilters) ([]webhook.DLQRow, error)
	MarkReplayed(ctx context.Context, id uuid.UUID) error
	MarkRetryFailed(ctx context.Context, id uuid.UUID, lastErr string) (*webhook.DLQRow, error)
}

// adminHandlers binds the DLQ + replay endpoints to the gateway's
// pgxpool + publisher.
type adminHandlers struct {
	dlq       dlqStore
	publisher publisher.Publisher
}

func newAdminHandlers(dlq dlqStore, pub publisher.Publisher) *adminHandlers {
	return &adminHandlers{dlq: dlq, publisher: pub}
}

// Mount registers the admin routes on a chi router. Caller is
// responsible for placing the API-key middleware on the router group
// so unauthenticated callers get 401.
func (h *adminHandlers) Mount(r chi.Router) {
	r.Get("/v1/webhooks/dlq", h.list)
	r.Get("/v1/webhooks/dlq/{id}", h.get)
	r.Post("/v1/webhooks/replay/{id}", h.replay)
}

// list handles GET /v1/webhooks/dlq?source_code=&status=&limit=&offset=
func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "dlq:read") &&
		!identity.RequireScope(r.Context(), "dlq:replay") {
		writeAdminErr(w, http.StatusForbidden, "forbidden",
			"caller missing dlq:read scope")
		return
	}
	q := r.URL.Query()
	f := webhook.ListFilters{
		SourceCode: q.Get("source_code"),
		Status:     q.Get("status"),
	}
	// T-H: tenant-scoped keys see only their own DLQ rows.
	// Platform-scope keys (claims.TenantID == uuid.Nil) keep the
	// merchant_id query param as a free filter — they're cross-tenant
	// by design.
	claims, _ := identity.ClaimsFromContext(r.Context())
	if claims.TenantID != uuid.Nil {
		f.MerchantID = &claims.TenantID
	} else if v := q.Get("merchant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeAdminErr(w, http.StatusBadRequest, "malformed_merchant_id", err.Error())
			return
		}
		f.MerchantID = &id
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}

	rows, err := h.dlq.List(r.Context(), f)
	if err != nil {
		writeAdminErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"items": rows,
		"count": len(rows),
	})
}

// get handles GET /v1/webhooks/dlq/{id}
func (h *adminHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "dlq:read") &&
		!identity.RequireScope(r.Context(), "dlq:replay") {
		writeAdminErr(w, http.StatusForbidden, "forbidden",
			"caller missing dlq:read scope")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeAdminErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}
	row, err := h.dlq.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, webhook.ErrDLQNotFound) {
			writeAdminErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeAdminErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	// T-H: tenant-scoped key fetching another tenant's row
	// gets 404 (not 403) — same response shape as a true miss to
	// avoid leaking row existence across tenants.
	claims, _ := identity.ClaimsFromContext(r.Context())
	if claims.TenantID != uuid.Nil && row.MerchantID != claims.TenantID {
		writeAdminErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeAdminJSON(w, http.StatusOK, row)
}

// replay handles POST /v1/webhooks/replay/{id}.
//
// Flow:
//  1. Fetch DLQ row (must be in 'pending' status — terminal rows
//     return 409).
//  2. Re-mint a canonical event_id (replays are independent events
//     from a chain-hash perspective; this is the ops-driven reset).
//  3. Re-publish the stored payload to the same Valkey stream the
//     ingest path uses.
//  4. Success → MarkReplayed; failure → MarkRetryFailed (which
//     advances retry_count + next_retry_at).
//
// Auth: dlq:replay scope required.
func (h *adminHandlers) replay(w http.ResponseWriter, r *http.Request) {
	if !identity.RequireScope(r.Context(), "dlq:replay") {
		writeAdminErr(w, http.StatusForbidden, "forbidden",
			"caller missing dlq:replay scope")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeAdminErr(w, http.StatusBadRequest, "malformed_id", err.Error())
		return
	}

	row, err := h.dlq.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, webhook.ErrDLQNotFound) {
			writeAdminErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeAdminErr(w, http.StatusInternalServerError, "get_failed", err.Error())
		return
	}
	// T-H: tenant-scoped key replaying another tenant's
	// row — return 404 to avoid existence leak. Replay would also
	// re-publish under row.MerchantID, which is the foreign tenant.
	claims, _ := identity.ClaimsFromContext(r.Context())
	if claims.TenantID != uuid.Nil && row.MerchantID != claims.TenantID {
		writeAdminErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if row.Status != "pending" {
		writeAdminErr(w, http.StatusConflict, "terminal_status",
			fmt.Sprintf("row status is %s; cannot replay", row.Status))
		return
	}

	// Re-mint event_id and republish.
	newEventID := uuid.New()
	evt := publisher.Event{
		EventID:    newEventID,
		EventHash:  "", // recomputed downstream by sub1
		SourceCode: row.SourceCode,
		MerchantID: row.MerchantID,
		Timestamp:  time.Now().UTC(),
		IngestedAt: time.Now().UTC(),
		Payload:    row.Payload,
	}
	pubCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.publisher.Publish(pubCtx, evt); err != nil {
		// Record the failed attempt; surface a 502 to the caller so
		// they know to retry or escalate.
		_, _ = h.dlq.MarkRetryFailed(r.Context(), id, err.Error())
		writeAdminErr(w, http.StatusBadGateway, "republish_failed", err.Error())
		return
	}
	if err := h.dlq.MarkReplayed(r.Context(), id); err != nil {
		writeAdminErr(w, http.StatusInternalServerError, "mark_replayed_failed", err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"replayed":     true,
		"new_event_id": newEventID.String(),
		"dlq_id":       id.String(),
	})
}

func writeAdminErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": msg,
	})
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
