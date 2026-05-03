// cmd/identity/handlers.go
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/auth"
	"github.com/growdirect-llc/rapidpos/internal/config"
	"github.com/growdirect-llc/rapidpos/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type handlers struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
	cfg  *config.Config
	jwt  *identity.JWTValidator
}

type healthResponse struct {
	OK      bool              `json:"ok"`
	Service string            `json:"service"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks"`
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{
		"database": "ok",
		"valkey":   "ok",
	}
	statusCode := http.StatusOK

	if err := h.pool.Ping(r.Context()); err != nil {
		checks["database"] = "error: " + err.Error()
		statusCode = http.StatusServiceUnavailable
	}
	if err := h.rdb.Ping(r.Context()).Err(); err != nil {
		checks["valkey"] = "error: " + err.Error()
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(healthResponse{
		OK:      statusCode == http.StatusOK,
		Service: h.cfg.ServiceName,
		Version: "1.0.0",
		Checks:  checks,
	})
}

type validateRequest struct {
	Token string `json:"token"`
}

type validateResponse struct {
	Valid      bool     `json:"valid"`
	MerchantID string   `json:"merchant_id,omitempty"`
	UserID     string   `json:"user_id,omitempty"`
	Roles      []string `json:"roles,omitempty"`
}

func (h *handlers) sessionsValidate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	claims, err := auth.VerifyToken(h.cfg.SessionSecret, req.Token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	// Revocation check — token must exist in Valkey
	key := fmt.Sprintf("session:%x", sha256.Sum256([]byte(req.Token)))
	if exists, err := h.rdb.Exists(r.Context(), key).Result(); err != nil || exists == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(validateResponse{Valid: false})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(validateResponse{
		Valid:      true,
		MerchantID: claims.MerchantID.String(),
		UserID:     claims.UserID.String(),
		Roles:      claims.Roles,
	})
}

// ─────────────────────────────────────────────────────────────────────
// /v1/identity/* — API key lifecycle + caller introspection
// (GRO-763 Phase C.6 / folds GRO-688)
// ─────────────────────────────────────────────────────────────────────

type createKeyRequest struct {
	TenantID     *string  `json:"tenant_id,omitempty"`
	AgentName    string   `json:"agent_name"`
	Scopes       []string `json:"scopes"`
	RateLimitRPM int      `json:"rate_limit_rpm,omitempty"`
	ExpiresAt    *string  `json:"expires_at,omitempty"` // RFC3339
}

type createKeyResponse struct {
	ID        string   `json:"id"`
	Plaintext string   `json:"plaintext"`
	TenantID  *string  `json:"tenant_id,omitempty"`
	AgentName string   `json:"agent_name"`
	Scopes    []string `json:"scopes"`
}

// keysCreate handles POST /v1/identity/keys. Returns the plaintext
// once at create-time; subsequent reads expose only the hash.
func (h *handlers) keysCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		writeKeyError(w, http.StatusUnauthorized, "unauthorized", "auth required")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
	if err != nil {
		writeKeyError(w, http.StatusBadRequest, "body_read_failed", err.Error())
		return
	}
	var req createKeyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeKeyError(w, http.StatusBadRequest, "malformed_json", err.Error())
		return
	}
	if req.AgentName == "" {
		writeKeyError(w, http.StatusBadRequest, "missing_agent_name", "agent_name is required")
		return
	}

	var tenantID *uuid.UUID
	if req.TenantID != nil && *req.TenantID != "" {
		t, err := uuid.Parse(*req.TenantID)
		if err != nil {
			writeKeyError(w, http.StatusBadRequest, "malformed_tenant_id", "tenant_id must be a UUID")
			return
		}
		// Cross-tenant defense — body tenant_id must match auth tenant_id.
		if claims.TenantID != uuid.Nil && claims.TenantID != t {
			writeKeyError(w, http.StatusForbidden, "tenant_mismatch",
				"tenant_id in body does not match authenticated tenant")
			return
		}
		tenantID = &t
	} else if claims.TenantID != uuid.Nil {
		// no body tenant — default to caller's tenant
		t := claims.TenantID
		tenantID = &t
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			writeKeyError(w, http.StatusBadRequest, "malformed_expires_at",
				"expires_at must be RFC3339")
			return
		}
		expiresAt = &t
	}

	plaintext, id, err := identity.CreateAPIKeyRow(
		r.Context(), h.pool, tenantID, req.AgentName, req.Scopes, req.RateLimitRPM, expiresAt,
	)
	if err != nil {
		writeKeyError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}

	resp := createKeyResponse{
		ID:        id.String(),
		Plaintext: plaintext,
		AgentName: req.AgentName,
		Scopes:    req.Scopes,
	}
	if tenantID != nil {
		s := tenantID.String()
		resp.TenantID = &s
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

type listKeysResponse struct {
	Items []listKeysRow `json:"items"`
	Count int           `json:"count"`
}

type listKeysRow struct {
	ID           string     `json:"id"`
	TenantID     *string    `json:"tenant_id,omitempty"`
	AgentName    string     `json:"agent_name"`
	Scopes       []string   `json:"scopes"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// keysList handles GET /v1/identity/keys. Returns rows for the
// caller's tenant only (uuid.Nil claim → platform-scope keys).
func (h *handlers) keysList(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		writeKeyError(w, http.StatusUnauthorized, "unauthorized", "auth required")
		return
	}
	rows, err := identity.ListAPIKeysByTenant(r.Context(), h.pool, claims.TenantID)
	if err != nil {
		writeKeyError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := listKeysResponse{Items: make([]listKeysRow, 0, len(rows)), Count: len(rows)}
	for _, r := range rows {
		row := listKeysRow{
			ID:           r.ID.String(),
			AgentName:    r.AgentName,
			Scopes:       r.Scopes,
			RateLimitRPM: r.RateLimitRPM,
			Status:       r.Status,
			ExpiresAt:    r.ExpiresAt,
			LastUsedAt:   r.LastUsedAt,
			CreatedAt:    r.CreatedAt,
		}
		if r.TenantID != nil {
			s := r.TenantID.String()
			row.TenantID = &s
		}
		out.Items = append(out.Items, row)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// keysRevoke handles POST /v1/identity/keys/{id}/revoke. Idempotent.
func (h *handlers) keysRevoke(w http.ResponseWriter, r *http.Request) {
	if _, ok := identity.ClaimsFromContext(r.Context()); !ok {
		writeKeyError(w, http.StatusUnauthorized, "unauthorized", "auth required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeKeyError(w, http.StatusBadRequest, "malformed_id", "id must be a UUID")
		return
	}
	if err := identity.RevokeAPIKey(r.Context(), h.pool, id); err != nil {
		writeKeyError(w, http.StatusInternalServerError, "revoke_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// whoami returns the decoded auth context for the caller. Useful for
// debugging — operators can hit this to confirm what their JWT or API
// key resolves to.
func (h *handlers) whoami(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		writeKeyError(w, http.StatusUnauthorized, "unauthorized", "auth required")
		return
	}
	resp := map[string]any{
		"auth_method": claims.AuthMethod,
		"agent_name":  claims.AgentName,
		"scopes":      claims.Scopes,
	}
	if claims.TenantID != uuid.Nil {
		resp["tenant_id"] = claims.TenantID.String()
	}
	if claims.UserID != uuid.Nil {
		resp["user_id"] = claims.UserID.String()
	}
	if claims.KeyID != uuid.Nil {
		resp["key_id"] = claims.KeyID.String()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeKeyError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, msg)
}

// renderIdentityErr maps internal/identity sentinels to HTTP status
// codes. Used by the API key authentication middleware mounted on
// the v1/identity routes.
func renderIdentityErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrAPIKeyMissing):
		writeKeyError(w, http.StatusUnauthorized, "missing_api_key", err.Error())
	case errors.Is(err, identity.ErrAPIKeyInvalid):
		writeKeyError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
	case errors.Is(err, identity.ErrAPIKeyRevoked):
		writeKeyError(w, http.StatusForbidden, "key_revoked", err.Error())
	case errors.Is(err, identity.ErrAPIKeyExpired):
		writeKeyError(w, http.StatusForbidden, "key_expired", err.Error())
	default:
		writeKeyError(w, http.StatusInternalServerError, "auth_error", err.Error())
	}
}
