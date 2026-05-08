// Package mint produces JWT pairs (access + refresh) signed by the
// keystore-backed active key. Used by:
//
//   - T-1's /auth/login endpoint (initial pair on authentication)
//   - T-1's /auth/refresh endpoint (rotation produces a new pair)
//   - T-6 JIT provisioning (first SSO mints the user's pair)
//
// Distinct from the verifier in tokenverify: this writes JWTs;
// that reads them. Both share the keystore for symmetric crypto
// — minted-here verifies-there with no shared secret beyond what
// the JWKS endpoint publishes.
//
// T-1 / GRO-861.
package mint

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/identity/keystore"
	"github.com/ruptiv/canary/internal/identity/tokenverify"
)

// Default TTLs per GRO-848 surface 1. Caller can override via
// MinterConfig but most production paths use the contract values.
const (
	DefaultAccessTTL  = 30 * time.Minute
	DefaultRefreshTTL = 12 * time.Hour
)

// Pair is the tuple a successful mint produces.
type Pair struct {
	AccessToken  string
	RefreshToken string
	// Surfaces of the underlying claims so callers (refresh
	// rotation, audit) can record them without re-parsing the
	// signed strings.
	AccessJTI  string
	RefreshJTI string
	FamilyID   string
	AccessExp  time.Time
	RefreshExp time.Time
}

// ActiveKeyReader is the keystore surface the minter depends on.
// Held as an interface so tests stub a single key without a pgx
// pool. *keystore.Store satisfies it.
type ActiveKeyReader interface {
	Active(ctx context.Context) (*keystore.SigningKey, error)
}

// Config bundles minter parameters. Audience is intentionally a
// slice — the contract supports audience-narrowing tokens (a single
// JWT valid for both atlasview and canary).
type Config struct {
	Issuer     string        // pinned in iss claim — matches the verifier's expected issuer
	Audience   []string      // pinned in aud claim
	AccessTTL  time.Duration // default DefaultAccessTTL if zero
	RefreshTTL time.Duration // default DefaultRefreshTTL if zero
}

// Minter holds minter state.
type Minter struct {
	store ActiveKeyReader
	cfg   Config
	now   func() time.Time // injectable for tests
}

// New constructs a Minter. now is set to time.Now; tests use
// NewWithClock.
func New(store ActiveKeyReader, cfg Config) *Minter {
	return NewWithClock(store, cfg, time.Now)
}

// NewWithClock allows tests to control time.
func NewWithClock(store ActiveKeyReader, cfg Config, now func() time.Time) *Minter {
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = DefaultAccessTTL
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = DefaultRefreshTTL
	}
	return &Minter{store: store, cfg: cfg, now: now}
}

// Subject is the actor a Pair is being minted for. Caller pulls
// these from the user store (login flow) or from the validated
// previous refresh token (rotation flow).
type Subject struct {
	UserID   uuid.UUID // → sub claim
	OrgID    uuid.UUID // → org_id claim (tenant)
	PersonID uuid.UUID // → person_id claim (AtlasView person UUID)
	UserType string    // → user_type claim (read_only|regular|power|admin|system)
	Scopes   []string  // → scopes claim
}

// MintPair mints an access+refresh pair for s. familyID can be
// uuid.Nil for a brand-new family (login); pass an existing familyID
// during rotation to keep the family chain intact (refresh).
//
// Both tokens are signed by the keystore's active key. If no active
// key exists, returns keystore.ErrNoActiveKey — operational alert
// territory.
func (m *Minter) MintPair(ctx context.Context, s Subject, familyID uuid.UUID) (*Pair, error) {
	if familyID == uuid.Nil {
		familyID = uuid.New()
	}

	sk, err := m.store.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("mint: active key: %w", err)
	}
	priv, err := parseRSAPrivateKey(sk.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("mint: parse private key: %w", err)
	}

	now := m.now()
	accessJTI := uuid.New().String()
	refreshJTI := uuid.New().String()
	accessExp := now.Add(m.cfg.AccessTTL)
	refreshExp := now.Add(m.cfg.RefreshTTL)

	// Both tokens use tokenverify.Claims — only the aud differs.
	// Access tokens get the configured audience set; refresh tokens
	// get aud="refresh", a deliberate split so a captured refresh
	// can never be presented as an access token (and vice versa).
	common := func() tokenverify.Claims {
		return tokenverify.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    m.cfg.Issuer,
				Subject:   s.UserID.String(),
				IssuedAt:  jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
			},
			OrgID:    s.OrgID.String(),
			PersonID: s.PersonID.String(),
			UserType: s.UserType,
			Scopes:   s.Scopes,
			FamilyID: familyID.String(),
		}
	}

	accessClaims := common()
	accessClaims.Audience = jwt.ClaimStrings(m.cfg.Audience)
	accessClaims.ID = accessJTI
	accessClaims.ExpiresAt = jwt.NewNumericDate(accessExp)
	accessSigned, err := signWithKid(accessClaims, sk.Kid, priv)
	if err != nil {
		return nil, fmt.Errorf("mint: sign access: %w", err)
	}

	refreshClaims := common()
	refreshClaims.Audience = jwt.ClaimStrings{"refresh"}
	refreshClaims.ID = refreshJTI
	refreshClaims.ExpiresAt = jwt.NewNumericDate(refreshExp)
	refreshSigned, err := signWithKid(refreshClaims, sk.Kid, priv)
	if err != nil {
		return nil, fmt.Errorf("mint: sign refresh: %w", err)
	}

	return &Pair{
		AccessToken:  accessSigned,
		RefreshToken: refreshSigned,
		AccessJTI:    accessJTI,
		RefreshJTI:   refreshJTI,
		FamilyID:     familyID.String(),
		AccessExp:    accessExp,
		RefreshExp:   refreshExp,
	}, nil
}

// signWithKid wraps jwt.Token construction so the kid header is
// always set. Forgetting kid would make the token unverifiable
// against a multi-key JWKS — every minted token MUST carry it.
func signWithKid(claims jwt.Claims, kid string, priv any) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(priv)
}

// parseRSAPrivateKey decodes the PEM that the keystore stores. RSA-
// only today; ES256 + EdDSA paths plug in here when those algs are
// adopted.
func parseRSAPrivateKey(pemStr string) (any, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("mint: failed to decode PEM")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mint: parse PKCS1: %w", err)
	}
	return priv, nil
}
