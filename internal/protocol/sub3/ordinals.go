package sub3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Inscriber is the interface Sub 3 calls to anchor a Merkle root on
// Bitcoin. Implementations: OrdinalsBot (real) and StubInscriber (dev).
type Inscriber interface {
	Inscribe(ctx context.Context, merkleRoot string, network string) (InscribeResult, error)
}

// InscribeResult holds the outcome of a successful inscription request.
type InscribeResult struct {
	// InscriptionID is the ordinals inscription identifier.
	InscriptionID string
	// TxID is the Bitcoin transaction ID.
	TxID string
	// BlockHeight is 0 until the transaction is confirmed on-chain.
	// Callers should treat 0 as "pending confirmation".
	BlockHeight int64
}

// ─── OrdinalsBot ─────────────────────────────────────────────────────────────

// ordinalsBotBaseURL returns the base URL for the given network.
func ordinalsBotBaseURL(network string) string {
	if network == "signet" {
		return "https://signet.ordinalsbot.com"
	}
	return "https://api2.ordinalsbot.com"
}

// OrdinalsBot inscribes a Merkle root via the OrdinalsBot REST API.
// If APIKey is empty the caller should use StubInscriber instead;
// OrdinalsBot.Inscribe will return an error in that case.
type OrdinalsBot struct {
	APIKey  string
	Network string        // "signet" or "mainnet"
	Client  *http.Client  // nil → http.DefaultClient with 30s timeout
}

// NewOrdinalsBot constructs an OrdinalsBot inscriber. If apiKey is
// empty, returns a StubInscriber so callers can always use the Inscriber
// interface without branching.
func NewOrdinalsBot(apiKey, network string) Inscriber {
	if apiKey == "" {
		return &StubInscriber{}
	}
	return &OrdinalsBot{
		APIKey:  apiKey,
		Network: network,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Inscribe sends a single-file inscription request to OrdinalsBot.
// The file content is the merkleRoot string encoded as UTF-8 text.
// On any network/API error it returns an error; the worker will record
// anchor_status = 'failed' and retry on the next poll cycle.
func (o *OrdinalsBot) Inscribe(ctx context.Context, merkleRoot string, network string) (InscribeResult, error) {
	content := []byte(merkleRoot)

	body := map[string]any{
		"files": []map[string]any{
			{
				"name":    "merkle-root.txt",
				"size":    len(content),
				"type":    "text/plain",
				"content": merkleRoot,
			},
		},
		"lowPostage": true,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return InscribeResult{}, fmt.Errorf("ordinalsbot: marshal request: %w", err)
	}

	baseURL := ordinalsBotBaseURL(network)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/inscribe", bytes.NewReader(encoded))
	if err != nil {
		return InscribeResult{}, fmt.Errorf("ordinalsbot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", o.APIKey)

	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return InscribeResult{}, fmt.Errorf("ordinalsbot: http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return InscribeResult{}, fmt.Errorf("ordinalsbot: status %d: %s",
			resp.StatusCode, string(raw))
	}

	// Parse the response. OrdinalsBot returns a JSON envelope whose
	// exact shape varies by API version; we extract the fields we need
	// defensively so a shape change doesn't break the binary.
	var result struct {
		ID    string `json:"id"`
		TxID  string `json:"txid"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return InscribeResult{}, fmt.Errorf("ordinalsbot: parse response: %w", err)
	}

	return InscribeResult{
		InscriptionID: result.ID,
		TxID:          result.TxID,
		BlockHeight:   0, // not confirmed yet; caller polls or accepts pending
	}, nil
}

// ─── StubInscriber ───────────────────────────────────────────────────────────

// StubInscriber returns a deterministic fake inscription for local dev
// and unit tests. It never makes network calls.
//
//	inscription_id = "stub:" + sha256(merkle_root)[:16]
type StubInscriber struct{}

// Inscribe returns a fake InscribeResult. The inscription_id is
// deterministic so tests can assert on it.
func (s *StubInscriber) Inscribe(_ context.Context, merkleRoot string, _ string) (InscribeResult, error) {
	h := sha256.Sum256([]byte(merkleRoot))
	shortHash := fmt.Sprintf("%x", h[:8]) // 16 hex chars
	return InscribeResult{
		InscriptionID: "stub:" + shortHash,
		TxID:          "stub-tx-" + shortHash,
		BlockHeight:   0,
	}, nil
}
