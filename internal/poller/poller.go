// Package poller implements the NCR Counterpoint REST polling loop for
// the edge service. Edge runs on-premise alongside Counterpoint + SQL
// Server and emits sale document events to the Canary protocol pipeline.
//
// Poll cycle (per active merchant, every 60 seconds):
//  1. Load credentials from app.bull_api_credentials
//  2. GET {endpoint_url}/Documents?since={cursor} with Basic Auth
//  3. Wrap each document in a publisher.Event (source_code=counterpoint)
//  4. Publish to Valkey stream "protocol:events" for Sub1 hash-seal
//  5. Advance cursor to the latest DocumentDate in the batch
//
// Cursor is stored in Valkey at key "counterpoint:cursor:{merchant_id}".
// Per-merchant errors are logged and skipped — one bad credential does
// not stall the whole poll cycle.
//
// Loop 4 — poll → wrap → publish. Parse logic stays in
// internal/adapters/counterpoint (consumed downstream by sub2-parse-route).
package poller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/adapters"
	"github.com/ruptiv/canary/internal/protocol/publisher"
)

const (
	streamName   = "protocol:events"
	cursorPrefix = "counterpoint:cursor:"
	sourceCode   = "counterpoint"
	// fetchLimit caps documents per poll cycle per merchant to bound
	// memory and avoid overwhelming sub1 on a backfill.
	fetchLimit = 500
)

// Poller drives the Counterpoint REST poll cycle.
type Poller struct {
	pool         *pgxpool.Pool
	credStore    *adapters.CredentialStore
	pub          publisher.Publisher
	valkey       *redis.Client
	httpClient   *http.Client
	pollInterval time.Duration
	logger       *zap.Logger
}

// Config holds Poller construction parameters.
type Config struct {
	Pool         *pgxpool.Pool
	Valkey       *redis.Client
	Publisher    publisher.Publisher
	PollInterval time.Duration // defaults to 60s
	HTTPTimeout  time.Duration // defaults to 20s
}

// New constructs a Poller. Panics if required fields are nil.
func New(cfg Config, logger *zap.Logger) *Poller {
	if cfg.Pool == nil || cfg.Valkey == nil || cfg.Publisher == nil {
		panic("poller: Pool, Valkey, and Publisher are required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60 * time.Second
	}
	httpTimeout := cfg.HTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = 20 * time.Second
	}
	return &Poller{
		pool:         cfg.Pool,
		credStore:    adapters.NewCredentialStore(cfg.Pool),
		pub:          cfg.Publisher,
		valkey:       cfg.Valkey,
		httpClient:   &http.Client{Timeout: httpTimeout},
		pollInterval: cfg.PollInterval,
		logger:       logger,
	}
}

// Run starts the poll loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	p.logger.Info("counterpoint poller starting", zap.Duration("interval", p.pollInterval))

	// Run once immediately so the first cycle doesn't wait a full interval.
	p.tick(ctx)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("counterpoint poller stopping")
			return ctx.Err()
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// tick runs one poll cycle across all active merchants.
func (p *Poller) tick(ctx context.Context) {
	creds, err := p.listAllActive(ctx)
	if err != nil {
		p.logger.Error("poller: list credentials", zap.Error(err))
		return
	}
	if len(creds) == 0 {
		p.logger.Debug("poller: no active credentials")
		return
	}

	for _, cred := range creds {
		if err := p.pollMerchant(ctx, cred); err != nil {
			p.logger.Error("poller: merchant poll failed",
				zap.String("merchant_id", cred.MerchantID.String()),
				zap.Error(err),
			)
			// Continue — one bad merchant does not block others.
		}
	}
}

// pollMerchant fetches new documents for a single merchant and publishes them.
func (p *Poller) pollMerchant(ctx context.Context, cred adapters.Credential) error {
	since, err := p.loadCursor(ctx, cred.MerchantID)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}

	docs, err := p.fetchDocuments(ctx, cred, since)
	if err != nil {
		return fmt.Errorf("fetch documents: %w", err)
	}
	if len(docs) == 0 {
		return nil
	}

	p.logger.Info("poller: fetched documents",
		zap.String("merchant_id", cred.MerchantID.String()),
		zap.Int("count", len(docs)),
	)

	var latestDate time.Time
	for _, raw := range docs {
		evt, docDate, err := p.wrapDocument(cred.MerchantID, raw)
		if err != nil {
			p.logger.Warn("poller: wrap failed, skipping document",
				zap.String("merchant_id", cred.MerchantID.String()),
				zap.Error(err),
			)
			continue
		}
		if err := p.pub.Publish(ctx, evt); err != nil {
			return fmt.Errorf("publish event: %w", err)
		}
		if docDate.After(latestDate) {
			latestDate = docDate
		}
	}

	if !latestDate.IsZero() {
		if err := p.saveCursor(ctx, cred.MerchantID, latestDate); err != nil {
			// Non-fatal — worst case we re-fetch and sub1 deduplicates by event_hash.
			p.logger.Warn("poller: save cursor failed",
				zap.String("merchant_id", cred.MerchantID.String()),
				zap.Error(err),
			)
		}
	}
	return nil
}

// cpDocumentHeader extracts the DocumentDate for cursor advancement.
// Full parse is done downstream in sub2-parse-route.
type cpDocumentHeader struct {
	DocumentDate time.Time `json:"DocumentDate"`
}

// wrapDocument converts raw Counterpoint JSON into a publisher.Event.
// Returns the event and the document's date (for cursor advancement).
func (p *Poller) wrapDocument(merchantID uuid.UUID, raw json.RawMessage) (publisher.Event, time.Time, error) {
	var hdr cpDocumentHeader
	if err := json.Unmarshal(raw, &hdr); err != nil {
		return publisher.Event{}, time.Time{}, fmt.Errorf("unmarshal header: %w", err)
	}

	h := sha256.Sum256(raw)
	evt := publisher.Event{
		EventID:    uuid.New(),
		EventHash:  fmt.Sprintf("%x", h),
		SourceCode: sourceCode,
		MerchantID: merchantID,
		Timestamp:  hdr.DocumentDate,
		IngestedAt: time.Now().UTC(),
		Payload:    raw,
	}
	return evt, hdr.DocumentDate, nil
}

// fetchDocuments calls the Counterpoint REST API and returns raw document JSON.
// The api_key_encrypted field is used directly as the Basic Auth password;
// decryption (KMS) is a Loop 5 hardening step — test seeds store plaintext.
func (p *Poller) fetchDocuments(ctx context.Context, cred adapters.Credential, since time.Time) ([]json.RawMessage, error) {
	url := strings.TrimRight(cred.EndpointURL, "/") + "/Documents"
	if !since.IsZero() {
		url += "?since=" + since.UTC().Format(time.RFC3339)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Basic Auth: username is merchant_id, password is the API key.
	req.SetBasicAuth(cred.MerchantID.String(), cred.APIKeyEncrypted)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB cap
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Counterpoint returns either a JSON array or a {"Documents":[...]} envelope.
	// Try array first; fall back to envelope shape.
	var docs []json.RawMessage
	if err := json.Unmarshal(body, &docs); err == nil {
		return limitDocs(docs), nil
	}
	var envelope struct {
		Documents []json.RawMessage `json:"Documents"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return limitDocs(envelope.Documents), nil
}

// loadCursor returns the last-polled timestamp for a merchant.
// Returns zero time when no cursor exists (first poll — fetches all available).
func (p *Poller) loadCursor(ctx context.Context, merchantID uuid.UUID) (time.Time, error) {
	key := cursorPrefix + merchantID.String()
	val, err := p.valkey.Get(ctx, key).Result()
	if err == redis.Nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("valkey get cursor: %w", err)
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cursor %q: %w", val, err)
	}
	return t, nil
}

// saveCursor persists the latest document date as the poll cursor.
// TTL of 30 days — stale cursors reset to zero (full fetch) automatically.
func (p *Poller) saveCursor(ctx context.Context, merchantID uuid.UUID, t time.Time) error {
	key := cursorPrefix + merchantID.String()
	return p.valkey.Set(ctx, key, t.UTC().Format(time.RFC3339), 30*24*time.Hour).Err()
}

// listAllActive returns credentials for all merchants with an active
// Counterpoint credential row.
func (p *Poller) listAllActive(ctx context.Context) ([]adapters.Credential, error) {
	const q = `
		SELECT merchant_id, api_key_encrypted, endpoint_url
		  FROM app.bull_api_credentials
		 WHERE is_active = true
		 ORDER BY merchant_id`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all active: %w", err)
	}
	defer rows.Close()
	var out []adapters.Credential
	for rows.Next() {
		var c adapters.Credential
		if err := rows.Scan(&c.MerchantID, &c.APIKeyEncrypted, &c.EndpointURL); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func limitDocs(docs []json.RawMessage) []json.RawMessage {
	if len(docs) > fetchLimit {
		return docs[:fetchLimit]
	}
	return docs
}
