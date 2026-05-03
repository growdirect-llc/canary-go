package webhook

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────
// Backoff schedule — pure logic, no DB.
// ─────────────────────────────────────────────────────────────────────

func TestBackoffScheduleHasFourEntries(t *testing.T) {
	if len(defaultBackoff) != MaxAutoRetries {
		t.Errorf("defaultBackoff length=%d MaxAutoRetries=%d — must match",
			len(defaultBackoff), MaxAutoRetries)
	}
}

func TestBackoffScheduleMonotonic(t *testing.T) {
	for i := 1; i < len(defaultBackoff); i++ {
		if defaultBackoff[i] <= defaultBackoff[i-1] {
			t.Errorf("backoff[%d]=%v not greater than backoff[%d]=%v",
				i, defaultBackoff[i], i-1, defaultBackoff[i-1])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Header encoding.
// ─────────────────────────────────────────────────────────────────────

func TestEncodeHeadersEmpty(t *testing.T) {
	out, err := encodeHeaders(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{}" {
		t.Errorf("nil headers: got %s, want {}", out)
	}
}

func TestEncodeHeadersRoundTrip(t *testing.T) {
	in := map[string]string{
		"X-Canary-Source":    "square",
		"X-Canary-Timestamp": "1714000000",
	}
	raw, err := encodeHeaders(in)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("key %s: got %q want %q", k, out[k], v)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// DLQ shape — sentinel error wrap-through.
// ─────────────────────────────────────────────────────────────────────

func TestDLQErrorSentinels(t *testing.T) {
	wrapped := errors.Join(ErrDLQTerminal, errors.New("downstream"))
	if !errors.Is(wrapped, ErrDLQTerminal) {
		t.Error("ErrDLQTerminal not matching through errors.Join")
	}

	wrappedNF := errors.Join(ErrDLQNotFound, errors.New("downstream"))
	if !errors.Is(wrappedNF, ErrDLQNotFound) {
		t.Error("ErrDLQNotFound not matching through errors.Join")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Idempotency — keys are deterministic + namespaced.
// ─────────────────────────────────────────────────────────────────────

func TestIdempotencyKeyShape(t *testing.T) {
	i := &Idempotency{keyPrefix: "test:idem"}
	got := i.key("square", "evt_abc123")
	want := "test:idem:square:evt_abc123"
	if got != want {
		t.Errorf("key shape: got %q, want %q", got, want)
	}
}

func TestIdempotencyEmptyEventIDIsNoop(t *testing.T) {
	// Passing an empty source_event_id should return without
	// touching Valkey. We verify by asserting Reserve returns nil
	// on a nil rdb — the empty-ID short-circuit fires before any
	// network call.
	i := &Idempotency{ttl: time.Minute, keyPrefix: "test"}
	if err := i.Reserve(t.Context(), "square", "", "evt-id"); err != nil {
		t.Errorf("empty source_event_id: got err %v, want nil", err)
	}
	if v, err := i.Lookup(t.Context(), "square", ""); v != "" || err != nil {
		t.Errorf("empty lookup: got (%q, %v), want ('', nil)", v, err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Backpressure config resolution — merchant override wins, platform
// default is fallback, missing returns disabled.
// ─────────────────────────────────────────────────────────────────────

func TestBackpressureConfigResolution(t *testing.T) {
	merchantA := uuid.New()
	merchantB := uuid.New()
	platformA := BackpressureConfig{SourceCode: "square", MaxRPS: 100, BurstCapacity: 200, Enabled: true}
	merchantOverride := BackpressureConfig{
		MerchantID: &merchantA, SourceCode: "square", MaxRPS: 500, BurstCapacity: 100, Enabled: true,
	}

	b := &Backpressure{
		configs: map[bpKey]BackpressureConfig{
			{sourceCode: "square"}:                                       platformA,
			{merchantID: merchantA.String(), sourceCode: "square"}:       merchantOverride,
		},
		loadedAt: time.Now(),
	}

	// Merchant-specific override
	cfgA := b.configFor(merchantA, "square")
	if cfgA.MaxRPS != 500 {
		t.Errorf("merchantA override: got MaxRPS=%d, want 500", cfgA.MaxRPS)
	}

	// Falls back to platform default for unknown merchant
	cfgB := b.configFor(merchantB, "square")
	if cfgB.MaxRPS != 100 {
		t.Errorf("merchantB platform fallback: got MaxRPS=%d, want 100", cfgB.MaxRPS)
	}

	// Unknown source returns disabled config
	cfgUnknown := b.configFor(merchantA, "unknown_source")
	if cfgUnknown.Enabled {
		t.Errorf("unknown source: expected disabled config, got %+v", cfgUnknown)
	}
}
