// Package squareauth implements Square OAuth + minimal Square API client
// for the May 12 demo. Provides:
//
//   - OAuth 2.0 authorization-code grant against Square Connect
//   - Token storage in app.pos_tenant_credentials with AES-256-GCM encryption
//   - Square API calls: merchant info, locations, recent payments
//   - Chi handler that mounts at /demo/* on the gateway
//
// Sandbox-only by default. Set SQUARE_ENVIRONMENT=production to switch.
//
// Environment variables required:
//
//   SQUARE_APPLICATION_ID          OAuth client_id
//   SQUARE_APPLICATION_SECRET      OAuth client_secret
//   SQUARE_REDIRECT_URI            full callback URL (e.g. https://demo.growdirect.io/auth/square/callback)
//   CANARY_ENCRYPTION_KEY          base64-encoded 32 bytes for AES-256-GCM (optional in sandbox; warns if missing)
//
// Optional:
//   SQUARE_ENVIRONMENT             "sandbox" (default) or "production"
package squareauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// ─── Service ───────────────────────────────────────────────────────────────

// Service holds OAuth + API client + token storage dependencies.
type Service struct {
	pool       *pgxpool.Pool
	logger     *zap.Logger
	cfg        Config
	encKey     []byte // 32 bytes; nil if not configured (sandbox only — see production)
	sessionKey []byte // SESSION_SECRET bytes; signs the demo session cookie. nil = unsigned dev fallback
	httpClient *http.Client
	// production is true when ENV=production or SQUARE_ENVIRONMENT=production
	// at construction time. When true, encrypt() refuses to write the
	// PLAIN: sentinel — a defense-in-depth check on top of the startup
	// fatal in New(). GRO-933.
	production bool
}

// Config holds the OAuth + API configuration. Loaded from env via LoadConfig.
type Config struct {
	ApplicationID     string
	ApplicationSecret string
	RedirectURI       string
	Environment       string // "sandbox" or "production"
}

// LoadConfig reads env vars and returns a Config. Falls back to sandbox.
func LoadConfig() Config {
	env := os.Getenv("SQUARE_ENVIRONMENT")
	if env == "" {
		env = "sandbox"
	}
	return Config{
		ApplicationID:     os.Getenv("SQUARE_APPLICATION_ID"),
		ApplicationSecret: os.Getenv("SQUARE_APPLICATION_SECRET"),
		RedirectURI:       os.Getenv("SQUARE_REDIRECT_URI"),
		Environment:       env,
	}
}

// New constructs a Service. Calls logger.Fatal if running under
// production (ENV=production or SQUARE_ENVIRONMENT=production) without
// a valid CANARY_ENCRYPTION_KEY — silent plaintext fallback is
// forbidden once real merchant tokens are at stake. GRO-933.
func New(pool *pgxpool.Pool, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg := LoadConfig()
	production := isProduction(cfg)
	encKey, err := loadEncryptionKey(os.Getenv("CANARY_ENCRYPTION_KEY"))
	if err != nil {
		if production {
			logger.Fatal("squareauth: CANARY_ENCRYPTION_KEY required in production",
				zap.Error(err),
				zap.String("hint", "set CANARY_ENCRYPTION_KEY to a base64-encoded 32-byte key"))
		}
		logger.Warn("squareauth: CANARY_ENCRYPTION_KEY invalid — tokens stored plaintext (sandbox only)",
			zap.Error(err))
	} else if encKey == nil {
		if production {
			logger.Fatal("squareauth: CANARY_ENCRYPTION_KEY required in production",
				zap.String("hint", "set CANARY_ENCRYPTION_KEY to a base64-encoded 32-byte key"))
		}
		logger.Warn("squareauth: CANARY_ENCRYPTION_KEY not set — tokens stored plaintext (sandbox only)")
	}
	sessionKey := []byte(os.Getenv("SESSION_SECRET"))
	if len(sessionKey) == 0 {
		// SESSION_SECRET is required by config.Load (same name) for the
		// gateway service. Squareauth reads it directly here because the
		// constructor doesn't take a Config. If empty, demo cookies fall
		// back to unsigned — logged loud at first use. Production
		// deployments must set SESSION_SECRET.
		logger.Warn("squareauth: SESSION_SECRET not set; demo session cookies will be unsigned (dev fallback)")
	}
	return &Service{
		pool:       pool,
		logger:     logger,
		cfg:        cfg,
		encKey:     encKey,
		sessionKey: sessionKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		production: production,
	}
}

// isProduction returns true when either ENV=production or
// SQUARE_ENVIRONMENT=production. Both flag the same fatal: real
// merchant tokens may pass through and silent plaintext storage is
// forbidden either way.
func isProduction(cfg Config) bool {
	if os.Getenv("ENV") == "production" {
		return true
	}
	if cfg.Environment == "production" {
		return true
	}
	return false
}

// loadEncryptionKey parses CANARY_ENCRYPTION_KEY (base64-encoded 32
// bytes). Returns one of:
//
//   - (nil, nil)  — env var unset (caller decides: sandbox warns,
//     production fatals).
//   - (nil, err)  — env var present but malformed (always an error;
//     caller decides whether to fatal or warn).
//   - (key, nil)  — valid 32-byte AES-256 key.
//
// Pure function: no env reads, no logging. Tests drive it directly
// with synthetic input.
func loadEncryptionKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("CANARY_ENCRYPTION_KEY base64 decode failed: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("CANARY_ENCRYPTION_KEY length != 32 bytes after base64 (got %d)", len(key))
	}
	return key, nil
}

// ─── Square base URLs ──────────────────────────────────────────────────────

func (s *Service) connectBaseURL() string {
	if s.cfg.Environment == "sandbox" {
		return "https://connect.squareupsandbox.com"
	}
	return "https://connect.squareup.com"
}

// AuthorizeURL builds the Square OAuth authorize URL with the given CSRF state.
// Scopes match identity.md SDD §Onboarding.
func (s *Service) AuthorizeURL(state string) string {
	scopes := strings.Join([]string{
		"MERCHANT_PROFILE_READ",
		"PAYMENTS_READ",
		"ORDERS_READ",
		"ITEMS_READ",
	}, " ")
	q := url.Values{}
	q.Set("client_id", s.cfg.ApplicationID)
	q.Set("scope", scopes)
	q.Set("session", "false")
	q.Set("state", state)
	q.Set("response_type", "code")
	return fmt.Sprintf("%s/oauth2/authorize?%s", s.connectBaseURL(), q.Encode())
}

// ─── OAuth code exchange ───────────────────────────────────────────────────

// TokenResponse mirrors Square's /oauth2/token response.
type TokenResponse struct {
	AccessToken           string    `json:"access_token"`
	TokenType             string    `json:"token_type"`
	ExpiresAt             time.Time `json:"expires_at"`
	MerchantID            string    `json:"merchant_id"`
	RefreshToken          string    `json:"refresh_token"`
	ShortLivedAccessToken bool      `json:"short_lived,omitempty"`
}

// ExchangeCode swaps an authorization code for tokens.
func (s *Service) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	body := map[string]any{
		"client_id":     s.cfg.ApplicationID,
		"client_secret": s.cfg.ApplicationSecret,
		"code":          code,
		"grant_type":    "authorization_code",
		"redirect_uri":  s.cfg.RedirectURI,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("squareauth: marshal token request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.connectBaseURL()+"/oauth2/token", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("squareauth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Square-Version", "2024-09-19")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("squareauth: token exchange http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("squareauth: token exchange status %d: %s",
			resp.StatusCode, string(raw))
	}

	var tr TokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("squareauth: parse token response: %w", err)
	}
	return &tr, nil
}

// RefreshToken exchanges a refresh token for a new access + refresh token pair.
// Square revokes the old refresh token on success — the caller must persist
// the returned TokenResponse immediately via StoreToken.
func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	body := map[string]any{
		"client_id":     s.cfg.ApplicationID,
		"client_secret": s.cfg.ApplicationSecret,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("squareauth: marshal refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.connectBaseURL()+"/oauth2/token", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("squareauth: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Square-Version", "2024-09-19")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("squareauth: refresh http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("squareauth: refresh status %d: %s",
			resp.StatusCode, string(raw))
	}
	var tr TokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("squareauth: parse refresh response: %w", err)
	}
	return &tr, nil
}

// ─── Token storage (app.pos_tenant_credentials) ────────────────────────────

// StoredCredentials is what we write into the credentials_enc column as
// JSON-then-encrypted. Mirrors pos-adapter-substrate.md "Square shape".
type StoredCredentials struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	ExpiresAt        time.Time `json:"expires_at"`
	MerchantIDSquare string    `json:"merchant_id_square"`
	StoredAt         time.Time `json:"stored_at"`
}

// StoreToken upserts the token blob into app.pos_tenant_credentials. The
// internal merchant_id is derived from the Square merchant_id deterministically
// for the demo (UUIDv5 of "square:<merchant_id_square>") — production would
// use a real onboarding flow that creates an app.merchants row first.
func (s *Service) StoreToken(ctx context.Context, tr *TokenResponse) (uuid.UUID, error) {
	internalMerchantID := deriveDemoMerchantID(tr.MerchantID)

	creds := StoredCredentials{
		AccessToken:      tr.AccessToken,
		RefreshToken:     tr.RefreshToken,
		TokenType:        tr.TokenType,
		ExpiresAt:        tr.ExpiresAt,
		MerchantIDSquare: tr.MerchantID,
		StoredAt:         time.Now().UTC(),
	}
	plaintext, err := json.Marshal(creds)
	if err != nil {
		return uuid.Nil, fmt.Errorf("squareauth: marshal creds: %w", err)
	}

	ciphertext, err := s.encrypt(plaintext)
	if err != nil {
		return uuid.Nil, fmt.Errorf("squareauth: encrypt creds: %w", err)
	}

	const q = `
		INSERT INTO app.pos_tenant_credentials
			(id, merchant_id, source_code, company_alias, credentials_enc, status, expires_at, last_tested_at, created_at, updated_at)
		VALUES
			($1, $2, 'square', NULL, $3, 'active', $4, NOW(), NOW(), NOW())
		ON CONFLICT (merchant_id, source_code, company_alias) DO UPDATE
		SET credentials_enc = EXCLUDED.credentials_enc,
		    status = 'active',
		    expires_at = EXCLUDED.expires_at,
		    last_tested_at = NOW(),
		    updated_at = NOW()
	`
	expiresAt := creds.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(30 * 24 * time.Hour) // Square sandbox default
	}
	_, err = s.pool.Exec(ctx, q, uuid.New(), internalMerchantID, ciphertext, expiresAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("squareauth: upsert credentials: %w", err)
	}

	s.logger.Info("squareauth token stored",
		zap.String("merchant_id", internalMerchantID.String()),
		zap.String("merchant_id_square", tr.MerchantID),
	)
	return internalMerchantID, nil
}

// LoadToken decrypts and returns the stored credentials for an internal merchant_id.
func (s *Service) LoadToken(ctx context.Context, internalMerchantID uuid.UUID) (*StoredCredentials, error) {
	const q = `
		SELECT credentials_enc
		FROM app.pos_tenant_credentials
		WHERE merchant_id = $1 AND source_code = 'square' AND company_alias IS NULL
		LIMIT 1
	`
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, q, internalMerchantID).Scan(&ciphertext)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("squareauth: load credentials: %w", err)
	}

	plaintext, err := s.decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("squareauth: decrypt credentials: %w", err)
	}
	var creds StoredCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, fmt.Errorf("squareauth: parse credentials: %w", err)
	}
	return &creds, nil
}

// DeleteToken removes the stored row for a merchant.
func (s *Service) DeleteToken(ctx context.Context, internalMerchantID uuid.UUID) error {
	const q = `
		DELETE FROM app.pos_tenant_credentials
		WHERE merchant_id = $1 AND source_code = 'square' AND company_alias IS NULL
	`
	_, err := s.pool.Exec(ctx, q, internalMerchantID)
	return err
}

// ErrTokenNotFound is returned when no stored credentials match.
var ErrTokenNotFound = errors.New("squareauth: token not found")

// IsExpiring returns true when the access token expires within threshold.
// A zero ExpiresAt is treated as non-expiring (Square sandbox tokens have
// a 30-day expiry so they will always carry a non-zero value after the
// initial code exchange).
func (c *StoredCredentials) IsExpiring(threshold time.Duration) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(c.ExpiresAt) < threshold
}

// deriveDemoMerchantID generates a stable UUIDv5 from a Square merchant_id
// for demo purposes. Production onboarding would create a real app.merchants
// row and use its UUID.
func deriveDemoMerchantID(squareMerchantID string) uuid.UUID {
	// UUIDv5 with a fixed namespace = stable, deterministic
	ns := uuid.MustParse("a8e6f9d1-1234-5678-9abc-def012345678")
	return uuid.NewSHA1(ns, []byte("square:"+squareMerchantID))
}

// ─── AES-256-GCM helpers ───────────────────────────────────────────────────
// Self-contained for now; consolidate with a platform-wide internal/security
// package post-demo (see GRO-802 out-of-scope).

func (s *Service) encrypt(plaintext []byte) ([]byte, error) {
	if s.encKey == nil {
		// GRO-933 defense-in-depth: even if startup somehow proceeded
		// without a key in production mode, refuse to write the PLAIN:
		// sentinel. The startup fatal in New() should already have
		// stopped this case; this is the second line.
		if s.production {
			return nil, errors.New("squareauth: refusing to write PLAIN: ciphertext in production — CANARY_ENCRYPTION_KEY missing")
		}
		// sandbox fallback: prepend a sentinel so we know to skip decryption
		out := append([]byte("PLAIN:"), plaintext...)
		return out, nil
	}
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return append([]byte("GCM:"), out...), nil
}

func (s *Service) decrypt(ciphertext []byte) ([]byte, error) {
	if bytes.HasPrefix(ciphertext, []byte("PLAIN:")) {
		return ciphertext[len("PLAIN:"):], nil
	}
	if !bytes.HasPrefix(ciphertext, []byte("GCM:")) {
		return nil, errors.New("squareauth: unknown ciphertext format")
	}
	if s.encKey == nil {
		return nil, errors.New("squareauth: GCM ciphertext but no key configured")
	}
	body := ciphertext[len("GCM:"):]
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(body) < gcm.NonceSize() {
		return nil, errors.New("squareauth: ciphertext too short")
	}
	nonce, payload := body[:gcm.NonceSize()], body[gcm.NonceSize():]
	return gcm.Open(nil, nonce, payload, nil)
}

// ─── CSRF state ────────────────────────────────────────────────────────────

// NewState generates a 32-byte random hex string.
func NewState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HashState returns a deterministic short hash for cookie storage.
func HashState(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ─── Square API client ─────────────────────────────────────────────────────

func (s *Service) apiBaseURL() string {
	if s.cfg.Environment == "sandbox" {
		return "https://connect.squareupsandbox.com"
	}
	return "https://connect.squareup.com"
}

// Merchant is a partial view of Square's merchant object.
type Merchant struct {
	ID           string `json:"id"`
	BusinessName string `json:"business_name"`
	Country      string `json:"country"`
	Currency     string `json:"currency"`
	LanguageCode string `json:"language_code"`
	Status       string `json:"status"`
}

// Location is a partial view of Square's location object.
type Location struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Type         string `json:"type"`
	Country      string `json:"country"`
	Address      struct {
		Locality string `json:"locality"`
		Region   string `json:"region"`
	} `json:"address"`
}

// Payment is a partial view of Square's payment object.
type Payment struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Amount     struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	} `json:"amount_money"`
	Status     string `json:"status"`
	SourceType string `json:"source_type"`
	CardDetails struct {
		Card struct {
			CardBrand string `json:"card_brand"`
			Last4     string `json:"last_4"`
		} `json:"card"`
	} `json:"card_details"`
	LocationID string `json:"location_id"`
}

// GetMerchant fetches the merchant info for the OAuth-connected token.
func (s *Service) GetMerchant(ctx context.Context, accessToken, squareMerchantID string) (*Merchant, error) {
	var resp struct {
		Merchant Merchant `json:"merchant"`
	}
	if err := s.apiGet(ctx, accessToken, "/v2/merchants/"+squareMerchantID, &resp); err != nil {
		return nil, err
	}
	return &resp.Merchant, nil
}

// ListLocations fetches all locations for the connected merchant.
func (s *Service) ListLocations(ctx context.Context, accessToken string) ([]Location, error) {
	var resp struct {
		Locations []Location `json:"locations"`
	}
	if err := s.apiGet(ctx, accessToken, "/v2/locations", &resp); err != nil {
		return nil, err
	}
	return resp.Locations, nil
}

// ListPayments fetches the most recent N payments.
func (s *Service) ListPayments(ctx context.Context, accessToken string, limit int) ([]Payment, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("sort_order", "DESC")
	var resp struct {
		Payments []Payment `json:"payments"`
	}
	if err := s.apiGet(ctx, accessToken, "/v2/payments?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return resp.Payments, nil
}

func (s *Service) apiGet(ctx context.Context, accessToken, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBaseURL()+path, nil)
	if err != nil {
		return fmt.Errorf("squareauth: build api request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Square-Version", "2024-09-19")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("squareauth: api http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("squareauth: api %s status %d: %s",
			path, resp.StatusCode, string(raw))
	}
	return json.Unmarshal(raw, out)
}
