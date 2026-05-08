// Package keystore manages the JWT signing key set backed by
// app.signing_keys. T-2 / GRO-862.
//
// Two-key rotation model:
//
//   - Exactly one row has status='active' at any time. That row is
//     used to mint new JWTs.
//   - Zero or one row has status='retiring'. If present, it stays
//     valid for verification until the rotation runbook flips it to
//     'expired' (≥24h after it entered 'retiring', enforced by ops
//     discipline + an alert rule outside this package).
//   - The verify set = active ∪ retiring. The JWKS endpoint
//     publishes both. JWT verification accepts tokens signed by
//     either key.
//
// State must survive process restarts (Postgres holds the rows) AND
// horizontal scale-out (Cloud Run instances re-read on TTL cache).
// The cache is intentionally short — a stale read costs at most a
// brief window of "instance verifies against an outdated key set",
// which is recoverable; a long-stale read could mean a rotated-out
// key still being trusted, which is not.
package keystore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values mirror the CHECK constraint on app.signing_keys.status.
const (
	StatusActive   = "active"
	StatusRetiring = "retiring"
	StatusExpired  = "expired"
)

// Algorithm whitelist mirrors the CHECK constraint on
// app.signing_keys.alg. The minter (T-1) consumes the same constants.
const (
	AlgRS256 = "RS256"
	AlgES256 = "ES256"
	AlgEdDSA = "EdDSA"
)

// DefaultCacheTTL is the verify-set cache window. 60s balances
// rotation responsiveness against per-request DB load; the rotation
// runbook's ≥24h overlap window is two orders of magnitude larger,
// so a 60s lag never lets a key leak past its retirement.
const DefaultCacheTTL = 60 * time.Second

// ErrNoActiveKey is returned when the keystore has zero active rows.
// This is a fatal operational state — the rotation runbook should
// page on it.
var ErrNoActiveKey = errors.New("keystore: no active signing key")

// SigningKey is the in-memory shape returned by the store. Public
// callers (JWKS handler, JWT verifier) only ever read this struct.
// The minter receives PrivateKeyPEM via Active() — separate path so
// verifiers cannot accidentally see private material.
type SigningKey struct {
	ID            uuid.UUID
	Kid           string
	Alg           string
	PublicJWK     json.RawMessage // raw JWK JSON; JWKS handler emits as-is
	PrivateKeyPEM string          // empty unless caller used Active()
	Status        string
	CreatedAt     time.Time
	RetiringAt    *time.Time
	RetiredAt     *time.Time
}

// Store is the keystore client. New instances share the underlying
// pgx pool but maintain independent caches; callers should hold one
// per service binary (gateway, identity).
type Store struct {
	pool     *pgxpool.Pool
	cacheTTL time.Duration

	mu        sync.RWMutex
	cachedAt  time.Time
	verifySet []SigningKey // active + retiring; PrivateKeyPEM cleared
}

// New constructs a Store with the default cache TTL.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, cacheTTL: DefaultCacheTTL}
}

// NewWithCacheTTL constructs a Store with an explicit cache TTL.
// Tests use a short TTL or zero (always-fresh).
func NewWithCacheTTL(pool *pgxpool.Pool, ttl time.Duration) *Store {
	return &Store{pool: pool, cacheTTL: ttl}
}

// Active returns the single active signing key including its private
// key material. Used by the minter (T-1) only — never by verifiers.
// Returns ErrNoActiveKey if no active row exists.
//
// Bypasses the cache because the minter reads it on every request and
// wants the current key, not a stale one. The "one active row"
// invariant is enforced by a partial unique index on the table.
func (s *Store) Active(ctx context.Context) (*SigningKey, error) {
	const q = `
		SELECT id, kid, alg, public_jwk, private_key_pem,
		       status, created_at, retiring_at, retired_at
		FROM app.signing_keys
		WHERE status = 'active'
	`
	row := s.pool.QueryRow(ctx, q)
	var sk SigningKey
	var pubRaw []byte
	if err := row.Scan(
		&sk.ID, &sk.Kid, &sk.Alg, &pubRaw, &sk.PrivateKeyPEM,
		&sk.Status, &sk.CreatedAt, &sk.RetiringAt, &sk.RetiredAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoActiveKey
		}
		return nil, fmt.Errorf("keystore active: %w", err)
	}
	sk.PublicJWK = pubRaw
	return &sk, nil
}

// VerifySet returns active + retiring keys with PrivateKeyPEM
// cleared. Cached for cacheTTL. Used by the JWT verifier (T-3) and
// the JWKS handler (T-2 follow-on).
func (s *Store) VerifySet(ctx context.Context) ([]SigningKey, error) {
	s.mu.RLock()
	if time.Since(s.cachedAt) < s.cacheTTL && s.verifySet != nil {
		out := make([]SigningKey, len(s.verifySet))
		copy(out, s.verifySet)
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	// Cache miss; refresh under write lock. Double-check after
	// acquiring write lock so concurrent callers don't all hit DB.
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.cachedAt) < s.cacheTTL && s.verifySet != nil {
		out := make([]SigningKey, len(s.verifySet))
		copy(out, s.verifySet)
		return out, nil
	}

	const q = `
		SELECT id, kid, alg, public_jwk,
		       status, created_at, retiring_at, retired_at
		FROM app.signing_keys
		WHERE status IN ('active', 'retiring')
		ORDER BY created_at DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("keystore verifyset: %w", err)
	}
	defer rows.Close()

	var out []SigningKey
	for rows.Next() {
		var sk SigningKey
		var pubRaw []byte
		if err := rows.Scan(
			&sk.ID, &sk.Kid, &sk.Alg, &pubRaw,
			&sk.Status, &sk.CreatedAt, &sk.RetiringAt, &sk.RetiredAt,
		); err != nil {
			return nil, fmt.Errorf("keystore verifyset scan: %w", err)
		}
		sk.PublicJWK = pubRaw
		out = append(out, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keystore verifyset rows: %w", err)
	}

	s.verifySet = out
	s.cachedAt = time.Now()

	// Return a copy so concurrent callers can't see in-flight cache
	// updates.
	cp := make([]SigningKey, len(out))
	copy(cp, out)
	return cp, nil
}

// FindByKid returns a key by `kid` from the verify set. JWT verifier
// uses this to dispatch tokens to the right public key.
func (s *Store) FindByKid(ctx context.Context, kid string) (*SigningKey, error) {
	set, err := s.VerifySet(ctx)
	if err != nil {
		return nil, err
	}
	for i := range set {
		if set[i].Kid == kid {
			return &set[i], nil
		}
	}
	return nil, fmt.Errorf("keystore: kid %q not in verify set", kid)
}

// Insert publishes a new active key. If an active row already exists,
// the caller should call Retire() first or use Rotate() (which does
// both atomically). The "one active row" invariant is also enforced
// at the DB layer by a partial unique index.
func (s *Store) Insert(ctx context.Context, k SigningKey) error {
	const q = `
		INSERT INTO app.signing_keys
			(id, kid, alg, public_jwk, private_key_pem, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
	`
	if _, err := s.pool.Exec(ctx, q, k.ID, k.Kid, k.Alg, []byte(k.PublicJWK), k.PrivateKeyPEM); err != nil {
		return fmt.Errorf("keystore insert: %w", err)
	}
	s.invalidateCache()
	return nil
}

// Retire flips an active row to retiring. After this the key serves
// verification only; minting goes to whichever row Insert(Rotate)
// promotes next. Idempotent — re-retiring a retiring row is a no-op.
func (s *Store) Retire(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE app.signing_keys
		SET status = 'retiring', retiring_at = NOW()
		WHERE id = $1 AND status = 'active'
	`
	if _, err := s.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("keystore retire: %w", err)
	}
	s.invalidateCache()
	return nil
}

// Expire flips a retiring row to expired. After this the key no
// longer serves verification; the JWKS endpoint omits it. The
// rotation runbook calls this ≥24h after Retire().
func (s *Store) Expire(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE app.signing_keys
		SET status = 'expired', retired_at = NOW()
		WHERE id = $1 AND status = 'retiring'
	`
	if _, err := s.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("keystore expire: %w", err)
	}
	s.invalidateCache()
	return nil
}

// Rotate is the single-call rotation primitive: retire the current
// active key (if any) and insert the new key as active. Atomic via
// a transaction so concurrent Rotate() calls are serialized by the
// partial unique index on status='active'.
func (s *Store) Rotate(ctx context.Context, newKey SigningKey) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("keystore rotate begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE app.signing_keys
		SET status = 'retiring', retiring_at = NOW()
		WHERE status = 'active'
	`); err != nil {
		return fmt.Errorf("keystore rotate retire: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.signing_keys
			(id, kid, alg, public_jwk, private_key_pem, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
	`, newKey.ID, newKey.Kid, newKey.Alg, []byte(newKey.PublicJWK), newKey.PrivateKeyPEM); err != nil {
		return fmt.Errorf("keystore rotate insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("keystore rotate commit: %w", err)
	}
	s.invalidateCache()
	return nil
}

func (s *Store) invalidateCache() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.verifySet = nil
	s.mu.Unlock()
}

// GenerateRSA creates a new RS256 SigningKey ready for Insert/Rotate.
// The key id is a fresh UUID; the public JWK encodes the modulus and
// exponent per RFC 7517. RSA 2048 — production minimum.
func GenerateRSA() (SigningKey, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return SigningKey{}, fmt.Errorf("rsa gen: %w", err)
	}
	id := uuid.New()
	kid := id.String()

	pubJWK, err := rsaPublicJWK(kid, &priv.PublicKey)
	if err != nil {
		return SigningKey{}, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})

	return SigningKey{
		ID:            id,
		Kid:           kid,
		Alg:           AlgRS256,
		PublicJWK:     pubJWK,
		PrivateKeyPEM: string(pemBytes),
		Status:        StatusActive,
	}, nil
}

// rsaPublicJWK encodes the public modulus and exponent as a JWK per
// RFC 7517 §4 + RFC 7518 §6.3. Output is canonical-ordered JSON so
// the JWKS endpoint emits stable bytes for caching.
func rsaPublicJWK(kid string, pub *rsa.PublicKey) (json.RawMessage, error) {
	jwk := struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	}{
		Kty: "RSA",
		Use: "sig",
		Alg: AlgRS256,
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	return json.Marshal(jwk)
}
