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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/ruptiv/canary/internal/auth"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/identity"
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
//
// Per GRO-931, every lifecycle endpoint requires an explicit
// `identity:keys:*` scope and confines its writes/reads to the
// caller's tenant unless the caller holds `identity:keys:admin`. Both
// the scope vocabulary and the tenant-confinement guards live here so
// the auth posture for the identity control plane is one block, not
// scattered across handlers.
//
// Scope vocabulary (literal strings until they migrate into the
// identity.Scope* constants on PR#9 / GRO-906):
//
//	identity:keys:read    — list / get / introspect
//	identity:keys:create  — mint a new key
//	identity:keys:revoke  — revoke an existing key
//	identity:keys:admin   — cross-tenant operations (revoke a key
//	                        outside the caller's tenant; mint scopes
//	                        the caller does not personally hold)
//
// Each handler runs three checks in order:
//   1. claims present (401 if not)
//   2. required scope present (403 insufficient_scope if not)
//   3. requested target visible to the caller's tenant
//      (404 not_found on cross-tenant — never 403, no existence leak)
// ─────────────────────────────────────────────────────────────────────

const (
	scopeKeysRead   = "identity:keys:read"
	scopeKeysCreate = "identity:keys:create"
	scopeKeysRevoke = "identity:keys:revoke"
	scopeKeysAdmin  = "identity:keys:admin"
)

// requireKeysScope returns claims if the caller has authenticated AND
// holds at least one of the named scopes. On failure it writes the
// appropriate error envelope and returns ok=false; callers exit early.
func requireKeysScope(w http.ResponseWriter, r *http.Request, anyOf ...string) (identity.Claims, bool) {
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		writeKeyError(w, http.StatusUnauthorized, "unauthorized", "auth required")
		return identity.Claims{}, false
	}
	for _, s := range anyOf {
		if identity.RequireScope(r.Context(), s) {
			return claims, true
		}
	}
	writeKeyError(w, http.StatusForbidden, "insufficient_scope",
		fmt.Sprintf("one of %v required", anyOf))
	return identity.Claims{}, false
}

// hasAdminScope is the predicate used to decide cross-tenant fan-out.
// Equivalent to RequireScope(ctx, scopeKeysAdmin) — wrapped here so
// the handler call sites read uniformly.
func hasAdminScope(claims identity.Claims) bool {
	for _, s := range claims.Scopes {
		if s == scopeKeysAdmin {
			return true
		}
	}
	return false
}

// containsAllScopes returns true when every scope in want is present
// in granted. Used by keysCreate to refuse minting a scope the caller
// does not personally hold (unless they're an admin).
func containsAllScopes(granted, want []string) bool {
	g := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		g[s] = struct{}{}
	}
	for _, s := range want {
		if _, ok := g[s]; !ok {
			return false
		}
	}
	return true
}

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
//
// Authorization (GRO-931):
//   - identity:keys:create required (or identity:keys:admin).
//   - tenant_id in body must match caller's tenant unless caller has
//     identity:keys:admin (which permits cross-tenant + platform key
//     creation).
//   - Requested scopes must be a subset of the caller's own scopes
//     unless the caller has identity:keys:admin. Prevents privilege
//     escalation from a low-scope tenant key to a high-scope key.
func (h *handlers) keysCreate(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireKeysScope(w, r, scopeKeysCreate, scopeKeysAdmin)
	if !ok {
		return
	}
	isAdmin := hasAdminScope(claims)

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
		// Admins are exempt: they can mint keys for any tenant.
		if !isAdmin && claims.TenantID != uuid.Nil && claims.TenantID != t {
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

	// Privilege-escalation defense (GRO-931): non-admin callers cannot
	// mint scopes they do not personally hold. Admin path is exempt.
	if !isAdmin && len(req.Scopes) > 0 && !containsAllScopes(claims.Scopes, req.Scopes) {
		writeKeyError(w, http.StatusForbidden, "scope_escalation",
			"requested scopes exceed caller's own scopes; identity:keys:admin required")
		return
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
//
// Authorization (GRO-931):
//   - identity:keys:read required (or identity:keys:admin).
//   - The store-level filter ListAPIKeysByTenant already scopes the
//     read by claims.TenantID; admin scope does not currently
//     enable cross-tenant listing here (a follow-up could add an
//     admin-only "list all" surface, but that's out of GRO-931).
func (h *handlers) keysList(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireKeysScope(w, r, scopeKeysRead, scopeKeysAdmin)
	if !ok {
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
//
// Authorization (GRO-931):
//   - identity:keys:revoke required (or identity:keys:admin).
//   - Tenant-scoped callers can only revoke rows that belong to their
//     tenant. Cross-tenant revoke attempts return 404, never 403,
//     so the existence of a key in another tenant is not leaked.
//   - Admin-scoped callers bypass the tenant filter and can revoke
//     any row.
func (h *handlers) keysRevoke(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireKeysScope(w, r, scopeKeysRevoke, scopeKeysAdmin)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeKeyError(w, http.StatusBadRequest, "malformed_id", "id must be a UUID")
		return
	}

	// Build the tenant filter the data layer will apply.
	//   - admin scope: nil (no filter)
	//   - tenant key (claims.TenantID != uuid.Nil): claims.TenantID
	//   - platform key without admin: refuse — platform keys must hold
	//     identity:keys:admin to operate on the lifecycle endpoints
	//     cross-tenant.
	var tenantFilter *uuid.UUID
	switch {
	case hasAdminScope(claims):
		tenantFilter = nil
	case claims.TenantID != uuid.Nil:
		t := claims.TenantID
		tenantFilter = &t
	default:
		writeKeyError(w, http.StatusForbidden, "insufficient_scope",
			"platform-scope keys must hold identity:keys:admin to revoke")
		return
	}

	if err := identity.RevokeAPIKey(r.Context(), h.pool, id, tenantFilter); err != nil {
		if errors.Is(err, identity.ErrAPIKeyNotFound) {
			writeKeyError(w, http.StatusNotFound, "not_found", "no such key")
			return
		}
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
