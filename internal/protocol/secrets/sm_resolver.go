// sm_resolver.go
//
// SmResolver — GCP Secret Manager-backed Resolver.
//
// In v1 (PgxResolver), the per-(merchant, source) HMAC secret was
// stored as plaintext in protocol.source_secrets.secret. That is
// fine for dev; for production it makes a single accidental commit
// or DB dump a full key compromise. GRO-687 retires the plaintext
// path and routes secret values through Secret Manager while leaving
// the metadata (signature_algo, replay_window_seconds, status) in
// Postgres for fast lookup and policy enforcement.
//
// Architecture:
//
//   - protocol.source_secrets row carries identity + policy:
//     id, merchant_id, source_code, status, signature_algo,
//     replay_window_seconds, secret_sm_ref (full SM resource path).
//   - The actual secret bytes live in Secret Manager at the resource
//     path stored in secret_sm_ref. Naming convention:
//     projects/{project}/secrets/canary-source-{merchant_id}-{source_code}/versions/latest
//   - Lookup queries Postgres for the row, then asks SM for the value,
//     and returns a Secret matching the v1 shape.
//   - A short-TTL in-memory cache (default 60s) absorbs the hot-path
//     lookup pressure so SM isn't queried per webhook.
//
// Buy-vs-build discipline (platform-stack-commitment): Secret Manager
// is "buy". This file owns the wiring; rotation, encryption, audit,
// IAM, and durability are all SM's job.
//
// SECURITY: secret values must never appear in logs. Every zap call
// in this file passes only opaque identifiers (resource path is fine
// — the path identifies the secret, not its value).
package secrets

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultCacheTTL is the in-memory TTL for resolved secrets. Short
// enough that rotation latency is bounded; long enough that SM is
// not in the hot path per webhook. Configurable via WithCacheTTL.
const DefaultCacheTTL = 60 * time.Second

// secretManagerClient is the narrow interface that SmResolver depends
// on. Defining it here (rather than depending on the concrete SM client
// type directly) lets tests substitute a mock without touching the
// network. The single method matches secretmanager.Client.AccessSecretVersion.
type secretManagerClient interface {
	AccessSecretVersion(
		ctx context.Context,
		req *secretmanagerpb.AccessSecretVersionRequest,
		opts ...option.ClientOption,
	) (*secretmanagerpb.AccessSecretVersionResponse, error)
	Close() error
}

// smClientAdapter wraps the real *secretmanager.Client so its variadic
// option signature matches our interface. The real client's
// AccessSecretVersion takes gax.CallOption, not option.ClientOption;
// we drop the variadic at the boundary because we don't pass call
// options at lookup time.
type smClientAdapter struct {
	c *secretmanager.Client
}

func (a *smClientAdapter) AccessSecretVersion(
	ctx context.Context,
	req *secretmanagerpb.AccessSecretVersionRequest,
	_ ...option.ClientOption,
) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	return a.c.AccessSecretVersion(ctx, req)
}

func (a *smClientAdapter) Close() error { return a.c.Close() }

// SmResolver implements Resolver against GCP Secret Manager + Postgres.
//
// Postgres holds metadata (one row per active secret); Secret Manager
// holds the value at the resource path on the row. A small in-memory
// cache absorbs hot-path lookup pressure.
type SmResolver struct {
	pool      *pgxpool.Pool
	sm        secretManagerClient
	logger    *zap.Logger
	projectID string
	cacheTTL  time.Duration

	mu    sync.RWMutex
	cache map[string]cacheEntry // key = merchantID|sourceCode
}

type cacheEntry struct {
	secret    Secret
	expiresAt time.Time
}

// SmResolverOption configures NewSmResolver.
type SmResolverOption func(*SmResolver)

// WithCacheTTL overrides the default cache TTL.
func WithCacheTTL(ttl time.Duration) SmResolverOption {
	return func(r *SmResolver) {
		if ttl > 0 {
			r.cacheTTL = ttl
		}
	}
}

// WithLogger attaches a structured logger. Defaults to zap.NewNop().
func WithLogger(l *zap.Logger) SmResolverOption {
	return func(r *SmResolver) {
		if l != nil {
			r.logger = l
		}
	}
}

// NewSmResolver constructs an SmResolver with a real Secret Manager
// client. projectID is the GCP project that owns the canary-source-*
// secrets. Returns an error if the SM client cannot be constructed
// (typically: ADC not configured).
func NewSmResolver(ctx context.Context, pool *pgxpool.Pool, projectID string, opts ...SmResolverOption) (*SmResolver, error) {
	if pool == nil {
		return nil, errors.New("secrets: NewSmResolver: pool is nil")
	}
	if projectID == "" {
		return nil, errors.New("secrets: NewSmResolver: projectID is empty")
	}
	r := &SmResolver{
		pool:      pool,
		logger:    zap.NewNop(),
		projectID: projectID,
		cacheTTL:  DefaultCacheTTL,
		cache:     make(map[string]cacheEntry),
	}
	for _, o := range opts {
		o(r)
	}
	if r.sm == nil {
		client, err := secretmanager.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("secrets: secretmanager.NewClient: %w", err)
		}
		r.sm = &smClientAdapter{c: client}
	}
	return r, nil
}

// Close releases the underlying SM client. Safe to call multiple
// times; only the first call has effect.
func (r *SmResolver) Close() error {
	if r.sm == nil {
		return nil
	}
	return r.sm.Close()
}

// const sm_lookupSQL fetches metadata + the SM resource ref.
//
// secret_sm_ref carries the full SM resource path. The legacy
// `secret` column may still hold a plaintext fallback during the
// transition; SmResolver intentionally ignores it.
const smLookupSQL = `
SELECT id, merchant_id, source_code, secret_sm_ref, signature_algo, replay_window_seconds
FROM protocol.source_secrets
WHERE merchant_id = $1
  AND source_code = $2
  AND status = 'active'
  AND secret_sm_ref IS NOT NULL
LIMIT 1
`

// Lookup satisfies the Resolver interface. It checks the cache, then
// queries Postgres for the row, then asks Secret Manager for the
// value. The returned Secret has the same shape as the PgxResolver
// flavor, so the webhook handler doesn't know which backend served
// it.
func (r *SmResolver) Lookup(ctx context.Context, merchantID uuid.UUID, sourceCode string) (Secret, error) {
	key := memKey(merchantID, sourceCode)

	if cached, ok := r.cacheGet(key); ok {
		return cached, nil
	}

	var (
		s             Secret
		smRef         string
		replaySeconds int
	)
	row := r.pool.QueryRow(ctx, smLookupSQL, merchantID, sourceCode)
	if err := row.Scan(&s.ID, &s.MerchantID, &s.SourceCode, &smRef, &s.SignatureAlgo, &replaySeconds); err != nil {
		if err.Error() == "no rows in result set" || errors.Is(err, errNoRows) {
			return Secret{}, ErrNotFound
		}
		return Secret{}, fmt.Errorf("secrets: lookup: %w", err)
	}
	s.ReplayWindow = time.Duration(replaySeconds) * time.Second

	value, err := r.fetchSecretValue(ctx, smRef)
	if err != nil {
		return Secret{}, err
	}
	s.Secret = value

	r.cachePut(key, s)
	return s, nil
}

// fetchSecretValue calls SM AccessSecretVersion and maps NotFound to
// secrets.ErrNotFound (matching PgxResolver semantics for unknown
// secrets). Other errors propagate wrapped. Secret value is never
// logged.
func (r *SmResolver) fetchSecretValue(ctx context.Context, resourcePath string) ([]byte, error) {
	req := &secretmanagerpb.AccessSecretVersionRequest{Name: resourcePath}
	resp, err := r.sm.AccessSecretVersion(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			r.logger.Warn("secret manager NotFound",
				zap.String("resource", resourcePath),
			)
			return nil, ErrNotFound
		}
		r.logger.Error("secret manager access failed",
			zap.String("resource", resourcePath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("secrets: SM access: %w", err)
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("secrets: SM returned empty payload for %s", resourcePath)
	}
	// resp.Payload.Data is the secret bytes. Caller (webhook handler)
	// uses this for HMAC keying — never log it.
	return resp.Payload.Data, nil
}

// BuildResourcePath returns the canonical SM resource path for a
// (merchant, source) pair. Centralized here so seeding tooling and
// migration backfill use the same convention as runtime lookup.
//
//	projects/{project}/secrets/canary-source-{merchant_id}-{source_code}/versions/latest
func BuildResourcePath(projectID string, merchantID uuid.UUID, sourceCode string) string {
	return fmt.Sprintf(
		"projects/%s/secrets/canary-source-%s-%s/versions/latest",
		projectID, merchantID.String(), sourceCode,
	)
}

// cacheGet returns a non-expired cached Secret, if any.
func (r *SmResolver) cacheGet(key string) (Secret, bool) {
	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if !ok {
		return Secret{}, false
	}
	if time.Now().After(entry.expiresAt) {
		// Lazy eviction; another caller will replace the entry.
		return Secret{}, false
	}
	return entry.secret, true
}

// cachePut stores a Secret with the configured TTL.
func (r *SmResolver) cachePut(key string, s Secret) {
	r.mu.Lock()
	r.cache[key] = cacheEntry{
		secret:    s,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.mu.Unlock()
}

// Invalidate forcibly evicts a cache entry. Useful for rotation
// callbacks; not used today but exposed for future wiring.
func (r *SmResolver) Invalidate(merchantID uuid.UUID, sourceCode string) {
	r.mu.Lock()
	delete(r.cache, memKey(merchantID, sourceCode))
	r.mu.Unlock()
}
