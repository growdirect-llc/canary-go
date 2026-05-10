// internal/identity/apikey.go
//
// Per-agent scoped API key middleware. Spec:
// docs/sdds/canary-go/identity-auth-tenant.md.
//
// Wire format: header `X-Canary-API-Key: <plaintext>`.
// Storage:     argon2id(plaintext, salt) per RFC 9106.
// Lookup:      app.api_keys table; full table-scan on tenant_id is
//              acceptable up to ~10⁴ keys per tenant. The verify path
//              does NOT trust caller-supplied tenant_id — the middleware
//              first authenticates the key, then sets tenant_id from
//              the key row.
//
// Hashing: argon2id with parameters time=1, memory=64MB, threads=4,
// hashLen=32, saltLen=16. Recommended in RFC 9106 §4 for passwords;
// used here for opaque API key tokens with comparable threat model
// (online brute-force resistance, server CPU pressure).

package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

// HeaderAPIKey is the wire-format header for API key authentication.
const HeaderAPIKey = "X-Canary-API-Key"

// KeyPlaintextPrefix is the visible-on-wire identifier so users
// recognize the token type.
const KeyPlaintextPrefix = "cy_"

// argon2id parameter set per RFC 9106 §4 recommendations. Bumped
// memory to 64 MiB. Each param can be retuned per the migration
// pattern in §A.5 of the SDD.
const (
	argon2Time    uint32 = 1
	argon2Memory  uint32 = 64 * 1024
	argon2Threads uint8  = 4
	argon2KeyLen  uint32 = 32
	argon2SaltLen        = 16
)

// Errors surfaced by the API key path. Handlers map these to status
// codes via the package-level renderAuthError helper (cmd/identity).
var (
	ErrAPIKeyMissing  = errors.New("identity: api key missing")
	ErrAPIKeyInvalid  = errors.New("identity: api key invalid")
	ErrAPIKeyRevoked  = errors.New("identity: api key revoked")
	ErrAPIKeyExpired  = errors.New("identity: api key expired")
	ErrInvalidHashFmt = errors.New("identity: malformed key_hash format")
)

// APIKeyAuthClaims is the per-request shape produced by
// APIKeyMiddleware on a successful auth. Mirrors JWTClaims at the
// fields the rest of the platform depends on; AuthMethod
// distinguishes them downstream.
type APIKeyAuthClaims struct {
	KeyID        uuid.UUID
	TenantID     *uuid.UUID // nil for platform-scope keys
	AgentName    string
	Scopes       []string
	RateLimitRPM int // requests-per-minute cap from app.api_keys.rate_limit_rpm; 0 ⇒ unlimited
}

// APIKeyMiddlewareOpts configures the chi middleware.
type APIKeyMiddlewareOpts struct {
	// Pool is the pgxpool used to look up keys.
	Pool *pgxpool.Pool

	// OnAuthenticated is called with the resolved claims on a
	// successful auth. Defaults to InjectAPIKeyClaims.
	OnAuthenticated func(ctx context.Context, claims APIKeyAuthClaims) context.Context

	// Required, when true, returns 401 when the header is missing.
	// When false (default), missing-header passes to the next
	// handler — useful for routes that accept either JWT or API
	// key.
	Required bool

	// Limiter, when non-nil, is consulted for brute-force lockout
	// (pre-auth) and per-key rate limiting (post-auth). nil ⇒ no
	// rate limiting, preserving the pre-GRO-912 behavior.
	Limiter *RateLimiter

	// OnRateLimitError, when non-nil, is called with errors raised by
	// Limiter calls. Used to wire structured logs / metrics. The
	// middleware itself fails open: a Limiter error is treated as
	// "not limited" so a Valkey blip cannot break authentication.
	OnRateLimitError func(stage string, err error)
}

// APIKeyMiddleware returns a chi-compatible middleware that
// authenticates X-Canary-API-Key. See the package doc for the full
// flow.
func APIKeyMiddleware(opts APIKeyMiddlewareOpts) func(http.Handler) http.Handler {
	if opts.Pool == nil {
		panic("identity: APIKeyMiddleware requires a non-nil pgxpool.Pool")
	}
	if opts.OnAuthenticated == nil {
		opts.OnAuthenticated = InjectAPIKeyClaims
	}

	notify := func(stage string, err error) {
		if opts.OnRateLimitError != nil && err != nil {
			opts.OnRateLimitError(stage, err)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plaintext := r.Header.Get(HeaderAPIKey)
			if plaintext == "" {
				if opts.Required {
					writeAuthError(w, http.StatusUnauthorized, ErrAPIKeyMissing)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Pre-auth lockout check. Cheap (one Valkey GET) so it fronts
			// the expensive argon2id verify. Fail-open on Valkey errors —
			// a blip should not block the auth path.
			ctx := r.Context()
			prefix := extractPrefix(plaintext)
			ip := SourceIP(r)
			if opts.Limiter != nil {
				status, err := opts.Limiter.IsLockedOut(ctx, prefix, ip)
				if err != nil {
					notify("lockout_check", err)
				} else if status.Locked {
					writeRateLimitError(w, status.RetryAfter, "rate_limited",
						"too many failed authentication attempts; try again later")
					return
				}
			}

			claims, err := AuthenticateAPIKey(ctx, opts.Pool, plaintext)
			if err != nil {
				// Record this as a brute-force signal. ErrAPIKeyRevoked /
				// ErrAPIKeyExpired are NOT counted — those are credentials
				// the system already knows about and chooses to reject.
				// Only ErrAPIKeyInvalid (and unknown errors that fall to
				// the same bucket) accrue toward lockout.
				if opts.Limiter != nil && errors.Is(err, ErrAPIKeyInvalid) {
					if _, lerr := opts.Limiter.RecordFailure(ctx, prefix, ip); lerr != nil {
						notify("record_failure", lerr)
					}
				}
				switch {
				case errors.Is(err, ErrAPIKeyRevoked), errors.Is(err, ErrAPIKeyExpired):
					writeAuthError(w, http.StatusForbidden, err)
				default:
					writeAuthError(w, http.StatusUnauthorized, err)
				}
				return
			}

			// Auth succeeded. Clear any failure-count residue from prior
			// mistakes (legitimate clients sometimes typo-then-succeed).
			if opts.Limiter != nil {
				if cerr := opts.Limiter.ClearFailures(ctx, prefix, ip); cerr != nil {
					notify("clear_failures", cerr)
				}
			}

			// Per-key throttle. INCR + check.
			if opts.Limiter != nil {
				ts, terr := opts.Limiter.AllowSuccess(ctx, claims.KeyID, claims.RateLimitRPM)
				if terr != nil {
					notify("throttle", terr)
				} else if !ts.Allowed {
					writeRateLimitError(w, ts.RetryAfter, "rate_limited",
						fmt.Sprintf("rate limit %d/min exceeded", ts.Limit))
					return
				}
			}

			ctx = opts.OnAuthenticated(ctx, *claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// keyPrefixLen is the number of plaintext characters (after the cy_
// brand prefix) used to bucket keys for indexed verify-loop selection.
// 8 base32 characters = 40 bits of entropy in the prefix space, more
// than enough to filter to ~1 candidate per request without leaking
// the secret material.
const keyPrefixLen = len(KeyPlaintextPrefix) + 8

// extractPrefix returns the indexable prefix of a plaintext API key
// (the brand prefix + first 8 random characters). Returns "" if the
// plaintext is too short to have a prefix — callers fall back to the
// full scan, which is also the path for legacy NULL-prefix rows.
func extractPrefix(plaintext string) string {
	if len(plaintext) < keyPrefixLen {
		return ""
	}
	return plaintext[:keyPrefixLen]
}

// AuthenticateAPIKey verifies plaintext against app.api_keys and
// returns the claim set on success. Updates last_used_at as a
// side-effect — fire-and-forget UPDATE outside the request hot-path.
//
// Lookup strategy (post-T-L):
//  1. Extract the indexable prefix from the plaintext (cy_<8 chars>).
//  2. Filter candidate rows by `WHERE key_prefix = $1` using the
//     idx_api_keys_key_prefix index. Normal case: 1 candidate.
//  3. Run argon2id verify on the candidate(s).
//  4. If no rows match the prefix (legacy NULL-prefix rows from before
//     the T-L migration), fall back to the full active scan + verify
//     loop so legacy keys keep working until they're rotated.
//
// argon2id is non-deterministic (salt-per-row), so we cannot index on
// the hash directly — but the plaintext prefix gives us a deterministic
// filter that's safe to expose (it's already on the wire) and reduces
// the verify-loop cost from O(n) to O(1) for new keys.
func AuthenticateAPIKey(ctx context.Context, pool *pgxpool.Pool, plaintext string) (*APIKeyAuthClaims, error) {
	if !strings.HasPrefix(plaintext, KeyPlaintextPrefix) {
		return nil, ErrAPIKeyInvalid
	}

	// Fast path: prefix-indexed candidate selection.
	if prefix := extractPrefix(plaintext); prefix != "" {
		if claims, err := authenticateByPrefix(ctx, pool, plaintext, prefix); err == nil {
			return claims, nil
		} else if !errors.Is(err, ErrAPIKeyInvalid) {
			return nil, err
		}
		// ErrAPIKeyInvalid from prefix path → fall through to legacy scan
		// in case the row predates the prefix migration.
	}

	const q = `
		SELECT id, tenant_id, agent_name, key_hash, scopes, rate_limit_rpm, status, expires_at
		  FROM app.api_keys
		 WHERE status = 'active'
		   AND (expires_at IS NULL OR expires_at > now())
		   AND key_prefix IS NULL`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("identity: query api_keys (legacy): %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id           uuid.UUID
			tenantID     *uuid.UUID
			agentName    string
			keyHash      string
			scopes       []string
			rateLimitRPM int
			status       string
			expiresAt    *time.Time
		)
		if err := rows.Scan(&id, &tenantID, &agentName, &keyHash, &scopes, &rateLimitRPM, &status, &expiresAt); err != nil {
			return nil, fmt.Errorf("identity: scan api_keys: %w", err)
		}
		ok, verr := VerifyAPIKey(plaintext, keyHash)
		if verr != nil {
			// Malformed hash row — skip, don't expose the error
			continue
		}
		if !ok {
			continue
		}
		// matched — best-effort last_used_at update
		go func(keyID uuid.UUID) {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx2,
				`UPDATE app.api_keys SET last_used_at = now() WHERE id = $1`,
				keyID,
			)
		}(id)
		return &APIKeyAuthClaims{
			KeyID:        id,
			TenantID:     tenantID,
			AgentName:    agentName,
			Scopes:       scopes,
			RateLimitRPM: rateLimitRPM,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: iter api_keys: %w", err)
	}
	return nil, ErrAPIKeyInvalid
}

// authenticateByPrefix is the indexed-fast-path of AuthenticateAPIKey.
// Filters candidate rows by the deterministic plaintext prefix and
// runs argon2id verify on the (typically single) match.
func authenticateByPrefix(ctx context.Context, pool *pgxpool.Pool, plaintext, prefix string) (*APIKeyAuthClaims, error) {
	const q = `
		SELECT id, tenant_id, agent_name, key_hash, scopes, rate_limit_rpm, status, expires_at
		  FROM app.api_keys
		 WHERE status = 'active'
		   AND key_prefix = $1
		   AND (expires_at IS NULL OR expires_at > now())`
	rows, err := pool.Query(ctx, q, prefix)
	if err != nil {
		return nil, fmt.Errorf("identity: query api_keys (prefix): %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id           uuid.UUID
			tenantID     *uuid.UUID
			agentName    string
			keyHash      string
			scopes       []string
			rateLimitRPM int
			status       string
			expiresAt    *time.Time
		)
		if err := rows.Scan(&id, &tenantID, &agentName, &keyHash, &scopes, &rateLimitRPM, &status, &expiresAt); err != nil {
			return nil, fmt.Errorf("identity: scan api_keys (prefix): %w", err)
		}
		ok, verr := VerifyAPIKey(plaintext, keyHash)
		if verr != nil || !ok {
			continue
		}
		go func(keyID uuid.UUID) {
			ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = pool.Exec(ctx2,
				`UPDATE app.api_keys SET last_used_at = now() WHERE id = $1`,
				keyID,
			)
		}(id)
		return &APIKeyAuthClaims{
			KeyID:        id,
			TenantID:     tenantID,
			AgentName:    agentName,
			Scopes:       scopes,
			RateLimitRPM: rateLimitRPM,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: iter api_keys (prefix): %w", err)
	}
	return nil, ErrAPIKeyInvalid
}

// HashAPIKey produces an argon2id self-describing hash string for the
// given plaintext. Format: argon2id$<saltB64>$<hashB64>.
func HashAPIKey(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("identity: empty plaintext")
	}
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: salt: %w", err)
	}
	h := argon2.IDKey([]byte(plaintext), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return fmt.Sprintf(
		"argon2id$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(h),
	), nil
}

// VerifyAPIKey constant-time-compares plaintext against a stored
// hash string in the format produced by HashAPIKey. Returns
// (true, nil) on match, (false, nil) on mismatch,
// (_, ErrInvalidHashFmt) on a parse failure.
func VerifyAPIKey(plaintext, stored string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false, ErrInvalidHashFmt
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false, ErrInvalidHashFmt
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, ErrInvalidHashFmt
	}
	got := argon2.IDKey([]byte(plaintext), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// GenerateAPIKeyPlaintext returns a fresh `cy_<base32>` token. 32
// bytes of crypto/rand entropy → 56-character base32 → ~160 bits of
// effective key material after the 'cy_' prefix.
func GenerateAPIKeyPlaintext() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: rand: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return KeyPlaintextPrefix + enc.EncodeToString(buf), nil
}

// CreateAPIKeyRow inserts a new app.api_keys row and returns the
// plaintext token + the row id. The plaintext is shown ONCE — caller
// must surface it to the end user; subsequent reads expose only the
// hash.
func CreateAPIKeyRow(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID *uuid.UUID,
	agentName string,
	scopes []string,
	rateLimitRPM int,
	expiresAt *time.Time,
) (plaintext string, id uuid.UUID, err error) {
	plaintext, err = GenerateAPIKeyPlaintext()
	if err != nil {
		return "", uuid.Nil, err
	}
	hash, err := HashAPIKey(plaintext)
	if err != nil {
		return "", uuid.Nil, err
	}
	if rateLimitRPM <= 0 {
		rateLimitRPM = 600
	}
	const insertQ = `
		INSERT INTO app.api_keys
		    (tenant_id, agent_name, key_hash, key_prefix, scopes, rate_limit_rpm, expires_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)
		RETURNING id`
	prefix := extractPrefix(plaintext)
	row := pool.QueryRow(ctx, insertQ, tenantID, agentName, hash, prefix, scopes, rateLimitRPM, expiresAt)
	if err := row.Scan(&id); err != nil {
		return "", uuid.Nil, fmt.Errorf("identity: insert api_key: %w", err)
	}
	return plaintext, id, nil
}

// RevokeAPIKey marks the row revoked. Idempotent — re-revoking a
// revoked key is a no-op (returns nil error).
func RevokeAPIKey(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	const q = `UPDATE app.api_keys SET status = 'revoked', updated_at = now()
	            WHERE id = $1 AND status = 'active'`
	tag, err := pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("identity: revoke api_key: %w", err)
	}
	_ = tag // RowsAffected may be 0 on idempotent re-revoke; caller doesn't care
	return nil
}

// ListAPIKeys returns active+revoked rows for the given tenant, never
// the key_hash. Ordered by created_at DESC.
type APIKeyRow struct {
	ID           uuid.UUID
	TenantID     *uuid.UUID
	AgentName    string
	Scopes       []string
	RateLimitRPM int
	Status       string
	ExpiresAt    *time.Time
	LastUsedAt   *time.Time
	CreatedAt    time.Time
}

// ListAPIKeysByTenant returns rows for tenantID. Pass uuid.Nil to
// list platform-scope keys.
func ListAPIKeysByTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]APIKeyRow, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if tenantID == uuid.Nil {
		rows, err = pool.Query(ctx, `
			SELECT id, tenant_id, agent_name, scopes, rate_limit_rpm,
			       status, expires_at, last_used_at, created_at
			  FROM app.api_keys
			 WHERE tenant_id IS NULL
			 ORDER BY created_at DESC`)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT id, tenant_id, agent_name, scopes, rate_limit_rpm,
			       status, expires_at, last_used_at, created_at
			  FROM app.api_keys
			 WHERE tenant_id = $1
			 ORDER BY created_at DESC`, tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("identity: list api_keys: %w", err)
	}
	defer rows.Close()
	out := []APIKeyRow{}
	for rows.Next() {
		var r APIKeyRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.AgentName, &r.Scopes,
			&r.RateLimitRPM, &r.Status, &r.ExpiresAt, &r.LastUsedAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("identity: scan api_keys: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeAuthError writes a standard JSON error envelope per the
// convention in docs/conventions.md. Used by APIKeyMiddleware.
func writeAuthError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	code := "auth_failed"
	switch {
	case errors.Is(err, ErrAPIKeyMissing):
		code = "missing_api_key"
	case errors.Is(err, ErrAPIKeyRevoked):
		code = "key_revoked"
	case errors.Is(err, ErrAPIKeyExpired):
		code = "key_expired"
	case errors.Is(err, ErrAPIKeyInvalid):
		code = "invalid_api_key"
	}
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, err.Error())
}
