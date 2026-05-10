package changefeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func okHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func TestChangeFeed_AddsWatermarkHeader(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000000000)
	mw := New(wm, time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	r := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if got := w.Header().Get("X-Watermark"); got != "1700000000000" {
		t.Errorf("X-Watermark = %q, want 1700000000000", got)
	}
	// No cursor → zero lag.
	if got := w.Header().Get("X-Lag"); got != "0s" {
		t.Errorf("X-Lag = %q, want 0s (no cursor)", got)
	}
	if got := w.Header().Get("X-Lag-Exceeded"); got != "" {
		t.Errorf("X-Lag-Exceeded should not fire when lag is zero, got %q", got)
	}
}

func TestChangeFeed_ComputesLagFromCursor(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000060000) // 60s after cursor
	mw := New(wm, 5*time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	cursor := strconv.FormatInt(1700000000000, 10) // 60s before watermark
	r := httptest.NewRequest(http.MethodGet, "/v1/alerts?cursor="+cursor, nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if got := w.Header().Get("X-Lag"); got != "1m0s" {
		t.Errorf("X-Lag = %q, want 1m0s", got)
	}
	// 60s is well below the 5m threshold — should not trip.
	if got := w.Header().Get("X-Lag-Exceeded"); got != "" {
		t.Errorf("X-Lag-Exceeded should not fire below threshold, got %q", got)
	}
}

func TestChangeFeed_LagExceededHeader(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000600000) // 600s after cursor (10m)
	mw := New(wm, 5*time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	cursor := strconv.FormatInt(1700000000000, 10)
	r := httptest.NewRequest(http.MethodGet, "/v1/alerts?cursor="+cursor, nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if got := w.Header().Get("X-Lag"); got != "10m0s" {
		t.Errorf("X-Lag = %q, want 10m0s", got)
	}
	if got := w.Header().Get("X-Lag-Exceeded"); got != "1" {
		t.Errorf("X-Lag-Exceeded = %q, want 1", got)
	}
}

func TestChangeFeed_NoCursorIsZeroLag(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000600000)
	mw := New(wm, 5*time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	r := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if got := w.Header().Get("X-Lag"); got != "0s" {
		t.Errorf("X-Lag without cursor should be 0s, got %q", got)
	}
}

func TestChangeFeed_NoWatermarkYetIsZero(t *testing.T) {
	wm := NewMemoryWatermark()
	mw := New(wm, 5*time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	// Empty watermark store — first poll, watermark is 0, lag is 0.
	r := httptest.NewRequest(http.MethodGet, "/v1/alerts", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if got := w.Header().Get("X-Watermark"); got != "0" {
		t.Errorf("missing watermark should emit 0, got %q", got)
	}
}

func TestChangeFeed_NotMountedWhenFlagOff(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000060000)
	mw := New(wm, time.Minute, nil, nil)
	base := okHandler("ok")

	gotOff := Wire(false, mw, base)
	w := httptest.NewRecorder()
	gotOff.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/alerts", nil))
	if w.Header().Get("X-Watermark") != "" {
		t.Errorf("flag-off Wire should not set X-Watermark")
	}

	gotOn := Wire(true, mw, base)
	w2 := httptest.NewRecorder()
	gotOn.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/v1/alerts", nil))
	if w2.Header().Get("X-Watermark") == "" {
		t.Errorf("flag-on Wire should set X-Watermark")
	}
}

func TestChangeFeed_StreamKeyFunc(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("alerts", 12345)
	mw := New(wm, time.Minute, func(r *http.Request) string {
		// Trim path-id parameter — share watermark across collection + item.
		return "alerts"
	}, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	r := httptest.NewRequest(http.MethodGet, "/v1/alerts/abc-123", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if got := w.Header().Get("X-Watermark"); got != "12345" {
		t.Errorf("custom keyFn should resolve watermark by alias, got %q", got)
	}
}

func TestMemoryWatermark_AdvanceAndRead(t *testing.T) {
	wm := NewMemoryWatermark()
	if v, ok, _ := wm.Get(context.Background(), "x"); v != 0 || ok {
		t.Errorf("empty watermark should return (0, false, nil), got (%d, %v)", v, ok)
	}
	wm.Advance("x", 100)
	v, ok, err := wm.Get(context.Background(), "x")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !ok || v != 100 {
		t.Errorf("after Advance(100): got (%d, %v), want (100, true)", v, ok)
	}
	wm.Advance("x", 200)
	v, _, _ = wm.Get(context.Background(), "x")
	if v != 200 {
		t.Errorf("Advance should overwrite: got %d, want 200", v)
	}
}

func TestChangeFeed_CursorBeforeWatermarkIsClampedNonnegative(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1000)
	mw := New(wm, time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	// Cursor AFTER watermark (clock skew or replay) — should clamp to 0,
	// not produce negative lag.
	r := httptest.NewRequest(http.MethodGet, "/v1/alerts?cursor=2000", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if got := w.Header().Get("X-Lag"); got != "0s" {
		t.Errorf("cursor > watermark should clamp lag to 0s, got %q", got)
	}
}

func TestChangeFeed_MalformedCursorIsZeroLag(t *testing.T) {
	wm := NewMemoryWatermark()
	wm.Advance("/v1/alerts", 1700000060000)
	mw := New(wm, time.Minute, nil, nil)
	wrapped := mw.Wrap(okHandler("ok"))

	r := httptest.NewRequest(http.MethodGet, "/v1/alerts?cursor=not-a-number", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	// Malformed cursor → ignored → lag is 0 (treat as no cursor).
	if got := w.Header().Get("X-Lag"); got != "0s" {
		t.Errorf("malformed cursor should yield 0s lag, got %q", got)
	}
}
