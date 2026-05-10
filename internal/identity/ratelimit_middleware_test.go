package identity

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWriteRateLimitError_EnvelopeShape locks down the 429 response
// shape so handlers can match on it.
func TestWriteRateLimitError_EnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRateLimitError(rec, 90*time.Second, "rate_limited", "slow down")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After: got %q, want %q", got, "90")
	}
	body := rec.Body.Bytes()
	if !bytes.Contains(body, []byte(`"rate_limited"`)) {
		t.Errorf("envelope missing code: %s", body)
	}
	if !bytes.Contains(body, []byte(`"slow down"`)) {
		t.Errorf("envelope missing message: %s", body)
	}
}

func TestWriteRateLimitError_RetryAfterClampsToOne(t *testing.T) {
	// Sub-second RetryAfter rounds up to 1 — never emit Retry-After: 0.
	rec := httptest.NewRecorder()
	writeRateLimitError(rec, 100*time.Millisecond, "rate_limited", "msg")
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After: got %q, want %q", got, "1")
	}
}

func TestWriteRateLimitError_ZeroRetryAfter_OmitsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRateLimitError(rec, 0, "rate_limited", "msg")
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After should be omitted when 0; got %q", got)
	}
}
