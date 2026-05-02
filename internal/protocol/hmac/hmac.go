// Package hmac verifies HMAC-SHA256 webhook signatures for the Canary
// protocol gateway (GRO-746, patent Node 2).
//
// Signature scheme:
//
//	X-Canary-Timestamp: <unix-seconds>
//	X-Canary-Nonce:     <opaque-string>
//	X-Canary-Signature: <hex-encoded HMAC-SHA256>
//
// The signed string is the canonical concatenation:
//
//	<timestamp>.<nonce>.<raw-request-body>
//
// using the dot byte (0x2E) as the field separator. The HMAC key is the
// per-source secret stored in protocol.source_secrets (look-up by
// merchant_id + source_code).
//
// Replay protection has two layers:
//
//  1. Timestamp window — requests outside ±replayWindow from server time
//     are rejected without a nonce check. Default window: 5 minutes.
//
//  2. Nonce single-use — a NonceStore (typically Valkey-backed) records
//     each nonce with a TTL equal to the timestamp window. The second
//     submission of the same nonce within the window is rejected.
//
// The package is dependency-free (stdlib only) so the verifier can be
// tested in isolation. The Valkey-backed NonceStore lives in
// internal/protocol/publisher.
package hmac

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Header names as the gateway accepts them. Lowercase canonical form;
// HTTP libraries fold case automatically.
const (
	HeaderTimestamp = "X-Canary-Timestamp"
	HeaderNonce     = "X-Canary-Nonce"
	HeaderSignature = "X-Canary-Signature"
)

// Common error sentinels — handlers map these to HTTP status codes.
var (
	ErrSignatureMissing      = errors.New("hmac: signature header missing")
	ErrTimestampMissing      = errors.New("hmac: timestamp header missing")
	ErrTimestampMalformed    = errors.New("hmac: timestamp header malformed")
	ErrTimestampOutOfWindow  = errors.New("hmac: timestamp outside replay window")
	ErrSignatureMalformed    = errors.New("hmac: signature header malformed")
	ErrSignatureMismatch     = errors.New("hmac: signature does not match")
	ErrNonceMissing          = errors.New("hmac: nonce header missing")
	ErrNonceReplay           = errors.New("hmac: nonce has already been used within the replay window")
)

// Signature is the parsed contents of the three Canary signature headers.
type Signature struct {
	Timestamp time.Time
	Nonce     string
	HexHMAC   string
}

// NonceStore records nonce strings with a TTL. The first call to
// SeenOnce for a given nonce returns (firstSeen=true, nil); subsequent
// calls within the TTL return (firstSeen=false, nil) so the verifier
// can reject the replay.
//
// Backends implement this with Valkey SET-NX-EX (atomic, distributed)
// or with an in-memory map (single-process tests).
type NonceStore interface {
	SeenOnce(ctx context.Context, nonce string, ttl time.Duration) (firstSeen bool, err error)
}

// Verifier holds a single source's HMAC secret and verification policy.
// Build one Verifier per (merchant_id, source_code) pair on the hot
// path; secrets and policy come from protocol.source_secrets.
type Verifier struct {
	secret       []byte
	replayWindow time.Duration
	nonces       NonceStore
}

// New constructs a Verifier. replayWindow must be positive; nonces may
// be nil to skip the replay-store check (timestamp-only protection).
func New(secret []byte, replayWindow time.Duration, nonces NonceStore) *Verifier {
	if replayWindow <= 0 {
		replayWindow = 5 * time.Minute
	}
	return &Verifier{
		secret:       secret,
		replayWindow: replayWindow,
		nonces:       nonces,
	}
}

// ReplayWindow exposes the configured window — handlers use it for
// telemetry and to compute nonce TTLs symmetric with the verifier.
func (v *Verifier) ReplayWindow() time.Duration { return v.replayWindow }

// Verify validates the signature against the payload using the configured
// secret. It returns nil iff every check passes:
//
//  1. timestamp parses cleanly and is within ±replayWindow of now
//  2. signature parses cleanly and matches the expected HMAC-SHA256
//  3. (if nonces is non-nil) the nonce is the first sighting in the window
//
// Steps run in the order above so timestamp drift never bills the nonce
// store, and signature failures never bill it either.
func (v *Verifier) Verify(ctx context.Context, payload []byte, sig Signature, now time.Time) error {
	// 1. Timestamp window
	if sig.Timestamp.IsZero() {
		return ErrTimestampMissing
	}
	delta := now.Sub(sig.Timestamp)
	if delta < 0 {
		delta = -delta
	}
	if delta > v.replayWindow {
		return fmt.Errorf("%w: drift=%s window=%s", ErrTimestampOutOfWindow, delta, v.replayWindow)
	}

	// 2. Signature match (constant-time)
	if sig.HexHMAC == "" {
		return ErrSignatureMissing
	}
	provided, err := hex.DecodeString(sig.HexHMAC)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureMalformed, err)
	}
	expected := computeHMAC(v.secret, sig.Timestamp, sig.Nonce, payload)
	if !hmac.Equal(provided, expected) {
		return ErrSignatureMismatch
	}

	// 3. Nonce single-use (only if a store is wired)
	if v.nonces != nil {
		if sig.Nonce == "" {
			return ErrNonceMissing
		}
		firstSeen, err := v.nonces.SeenOnce(ctx, sig.Nonce, v.replayWindow)
		if err != nil {
			return fmt.Errorf("hmac: nonce store: %w", err)
		}
		if !firstSeen {
			return ErrNonceReplay
		}
	}

	return nil
}

// ParseHeaders extracts a Signature from the three Canary headers.
// Lookup is a function rather than a map so callers can pass the
// http.Header type directly (or any equivalent). Empty results for
// individual fields are returned without erroring; the verifier
// itself enforces what's required.
func ParseHeaders(get func(string) string) (Signature, error) {
	tsRaw := get(HeaderTimestamp)
	sig := Signature{
		Nonce:   get(HeaderNonce),
		HexHMAC: get(HeaderSignature),
	}
	if tsRaw == "" {
		return sig, nil
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return sig, fmt.Errorf("%w: %v", ErrTimestampMalformed, err)
	}
	sig.Timestamp = time.Unix(ts, 0).UTC()
	return sig, nil
}

// Sign computes the canonical HMAC for a given payload and returns the
// hex-encoded signature plus the headers a client should send. Used by
// tests and by client SDKs that produce signatures.
func Sign(secret []byte, ts time.Time, nonce string, payload []byte) (sigHex string, headers map[string]string) {
	mac := computeHMAC(secret, ts, nonce, payload)
	sigHex = hex.EncodeToString(mac)
	headers = map[string]string{
		HeaderTimestamp: strconv.FormatInt(ts.Unix(), 10),
		HeaderNonce:     nonce,
		HeaderSignature: sigHex,
	}
	return
}

// computeHMAC builds the canonical signed string and returns the
// HMAC-SHA256 digest.
func computeHMAC(secret []byte, ts time.Time, nonce string, payload []byte) []byte {
	h := hmac.New(sha256.New, secret)
	// Canonical signed-string format: <unix_ts>.<nonce>.<payload>
	// Field separator is the dot byte (0x2E). Nonce may be empty.
	h.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	h.Write([]byte{'.'})
	h.Write([]byte(nonce))
	h.Write([]byte{'.'})
	h.Write(payload)
	return h.Sum(nil)
}
