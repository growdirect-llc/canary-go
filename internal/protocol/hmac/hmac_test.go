package hmac

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// memNonceStore is a minimal in-memory NonceStore for tests. Real
// production uses a Valkey-backed implementation in
// internal/protocol/publisher.
type memNonceStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newMemNonceStore() *memNonceStore { return &memNonceStore{seen: make(map[string]time.Time)} }

func (m *memNonceStore) SeenOnce(_ context.Context, nonce string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if at, ok := m.seen[nonce]; ok && now.Sub(at) < ttl {
		return false, nil
	}
	m.seen[nonce] = now
	return true, nil
}

func TestVerify_HappyPath(t *testing.T) {
	secret := []byte("a-test-secret-with-enough-entropy")
	payload := []byte(`{"event":"order.created","id":"o_123"}`)
	ts := time.Now()
	nonce := "n_abc123"

	sigHex, _ := Sign(secret, ts, nonce, payload)

	v := New(secret, 5*time.Minute, newMemNonceStore())
	err := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts,
		Nonce:     nonce,
		HexHMAC:   sigHex,
	}, ts)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestVerify_TimestampOutOfWindow(t *testing.T) {
	secret := []byte("secret")
	payload := []byte(`{}`)
	ts := time.Now().Add(-10 * time.Minute)
	sigHex, _ := Sign(secret, ts, "n", payload)

	v := New(secret, 5*time.Minute, nil)
	err := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts, Nonce: "n", HexHMAC: sigHex,
	}, time.Now())
	if !errors.Is(err, ErrTimestampOutOfWindow) {
		t.Fatalf("expected ErrTimestampOutOfWindow, got: %v", err)
	}
}

func TestVerify_TimestampMissing(t *testing.T) {
	v := New([]byte("secret"), 5*time.Minute, nil)
	err := v.Verify(context.Background(), []byte("p"), Signature{}, time.Now())
	if !errors.Is(err, ErrTimestampMissing) {
		t.Fatalf("expected ErrTimestampMissing, got: %v", err)
	}
}

func TestVerify_SignatureMismatch_TamperedPayload(t *testing.T) {
	secret := []byte("secret")
	original := []byte(`{"amount":100}`)
	tampered := []byte(`{"amount":999}`)
	ts := time.Now()
	sigHex, _ := Sign(secret, ts, "n", original)

	v := New(secret, 5*time.Minute, nil)
	err := v.Verify(context.Background(), tampered, Signature{
		Timestamp: ts, Nonce: "n", HexHMAC: sigHex,
	}, ts)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
	}
}

func TestVerify_SignatureMismatch_WrongSecret(t *testing.T) {
	correct := []byte("correct-secret")
	wrong := []byte("wrong-secret")
	payload := []byte("p")
	ts := time.Now()
	sigHex, _ := Sign(correct, ts, "n", payload)

	v := New(wrong, 5*time.Minute, nil)
	err := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts, Nonce: "n", HexHMAC: sigHex,
	}, ts)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got: %v", err)
	}
}

func TestVerify_SignatureMissing(t *testing.T) {
	v := New([]byte("secret"), 5*time.Minute, nil)
	ts := time.Now()
	err := v.Verify(context.Background(), []byte("p"), Signature{
		Timestamp: ts, Nonce: "n",
	}, ts)
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("expected ErrSignatureMissing, got: %v", err)
	}
}

func TestVerify_SignatureMalformed(t *testing.T) {
	v := New([]byte("secret"), 5*time.Minute, nil)
	ts := time.Now()
	err := v.Verify(context.Background(), []byte("p"), Signature{
		Timestamp: ts, Nonce: "n", HexHMAC: "ZZZ-not-hex",
	}, ts)
	if !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("expected ErrSignatureMalformed, got: %v", err)
	}
}

func TestVerify_NonceReplay(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("p")
	ts := time.Now()
	sigHex, _ := Sign(secret, ts, "n_unique", payload)

	store := newMemNonceStore()
	v := New(secret, 5*time.Minute, store)

	first := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts, Nonce: "n_unique", HexHMAC: sigHex,
	}, ts)
	if first != nil {
		t.Fatalf("expected first call to succeed, got: %v", first)
	}

	second := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts, Nonce: "n_unique", HexHMAC: sigHex,
	}, ts)
	if !errors.Is(second, ErrNonceReplay) {
		t.Fatalf("expected ErrNonceReplay on second call, got: %v", second)
	}
}

func TestVerify_NonceMissing_WhenStoreConfigured(t *testing.T) {
	secret := []byte("secret")
	payload := []byte("p")
	ts := time.Now()
	// Sign with empty nonce; verifier with store should reject it
	sigHex, _ := Sign(secret, ts, "", payload)

	v := New(secret, 5*time.Minute, newMemNonceStore())
	err := v.Verify(context.Background(), payload, Signature{
		Timestamp: ts, Nonce: "", HexHMAC: sigHex,
	}, ts)
	if !errors.Is(err, ErrNonceMissing) {
		t.Fatalf("expected ErrNonceMissing, got: %v", err)
	}
}

func TestParseHeaders(t *testing.T) {
	now := time.Now().Unix()
	headers := map[string]string{
		HeaderTimestamp: itoa(now),
		HeaderNonce:     "nonce-1",
		HeaderSignature: "abcdef0123",
	}
	get := func(k string) string { return headers[k] }
	sig, err := ParseHeaders(get)
	if err != nil {
		t.Fatalf("ParseHeaders error: %v", err)
	}
	if sig.Timestamp.Unix() != now {
		t.Errorf("Timestamp: got %d want %d", sig.Timestamp.Unix(), now)
	}
	if sig.Nonce != "nonce-1" {
		t.Errorf("Nonce: got %q", sig.Nonce)
	}
	if sig.HexHMAC != "abcdef0123" {
		t.Errorf("HexHMAC: got %q", sig.HexHMAC)
	}
}

func TestParseHeaders_TimestampMalformed(t *testing.T) {
	headers := map[string]string{
		HeaderTimestamp: "not-a-number",
	}
	get := func(k string) string { return headers[k] }
	_, err := ParseHeaders(get)
	if !errors.Is(err, ErrTimestampMalformed) {
		t.Fatalf("expected ErrTimestampMalformed, got: %v", err)
	}
}

func TestSign_DeterministicAndStable(t *testing.T) {
	secret := []byte("secret")
	ts := time.Unix(1700000000, 0)
	payload := []byte(`{"id":"abc"}`)
	a, _ := Sign(secret, ts, "nonce", payload)
	b, _ := Sign(secret, ts, "nonce", payload)
	if a != b {
		t.Fatalf("Sign should be deterministic, got %s vs %s", a, b)
	}
	// Cross-check expected length (sha256 = 32 bytes = 64 hex chars)
	if len(a) != 64 {
		t.Fatalf("expected 64-char hex signature, got %d (%q)", len(a), a)
	}
	// And that it's valid hex
	if strings.IndexFunc(a, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}) >= 0 {
		t.Fatalf("expected lowercase hex, got %q", a)
	}
}

// itoa is used by tests only — avoids importing strconv into the test
// file's main namespace, keeps the test file self-contained.
func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
