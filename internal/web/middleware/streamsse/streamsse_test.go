package streamsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// flushRecorder wraps httptest.ResponseRecorder + http.Flusher so the
// SSE handler's Flush() calls work in tests.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
	mu      sync.Mutex
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (f *flushRecorder) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

func (f *flushRecorder) Body() string {
	return f.ResponseRecorder.Body.String()
}

func TestStreamSSE_WritesEventToClient(t *testing.T) {
	src := NewMemorySource()
	h := New(src, 0, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := httptest.NewRequest(http.MethodGet, "/v1/chirp/stream", nil).WithContext(ctx)
	w := newFlushRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, r)
		close(done)
	}()

	// Wait until the subscriber registers.
	deadline := time.Now().Add(time.Second)
	for src.SubscriberCount("/v1/chirp/stream") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if src.SubscriberCount("/v1/chirp/stream") != 1 {
		t.Fatalf("subscriber did not register within timeout")
	}

	// Push and wait for delivery.
	src.Push("/v1/chirp/stream", Event{ID: "100", Type: "chirp.detection", Data: map[string]any{"id": "abc"}})

	// Allow the goroutine to write the frame.
	deadline = time.Now().Add(time.Second)
	for !strings.Contains(w.Body(), "id: 100") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	body := w.Body()

	for _, want := range []string{
		"retry: 3000",
		"id: 100",
		"event: chirp.detection",
		`data: {"id":"abc"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q. Got:\n%s", want, body)
		}
	}

	// Headers
	if got := w.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/event-stream; charset=utf-8", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestStreamSSE_HeartbeatOnIdle(t *testing.T) {
	src := NewMemorySource()
	h := New(src, 30*time.Millisecond, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	w := newFlushRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, r)
		close(done)
	}()
	<-done

	if !strings.Contains(w.Body(), ":heartbeat") {
		t.Errorf("expected at least one :heartbeat frame in idle stream. Got:\n%s", w.Body())
	}
}

func TestStreamSSE_ClosesOnContextCancel(t *testing.T) {
	src := NewMemorySource()
	h := New(src, 0, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	w := newFlushRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, r)
		close(done)
	}()

	// Wait for subscription to register, then cancel.
	deadline := time.Now().Add(time.Second)
	for src.SubscriberCount("/x") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(time.Second):
		t.Fatal("handler did not return after context cancel")
	}
}

func TestStreamSSE_LastEventIDIsCursor(t *testing.T) {
	src := &cursorCapturingSource{}
	h := New(src, 0, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/v1/alerts/stream", nil).WithContext(ctx)
	r.Header.Set("Last-Event-ID", "1700000000000-0")
	w := newFlushRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, r)
		close(done)
	}()

	// Give Subscribe time to be invoked.
	deadline := time.Now().Add(time.Second)
	for src.cursor() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got := src.cursor(); got != "1700000000000-0" {
		t.Errorf("cursor passed to Subscribe = %q, want 1700000000000-0", got)
	}
}

// minimalWriter — never satisfies http.Flusher.
type minimalWriter struct {
	headers http.Header
	body    strings.Builder
	status  int
}

func newMinimalWriter() *minimalWriter {
	return &minimalWriter{headers: make(http.Header)}
}

func (m *minimalWriter) Header() http.Header { return m.headers }
func (m *minimalWriter) Write(p []byte) (int, error) {
	return m.body.Write(p)
}
func (m *minimalWriter) WriteHeader(s int) { m.status = s }

func TestStreamSSE_FlusherUnsupported_Properly(t *testing.T) {
	src := NewMemorySource()
	h := New(src, 0, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := newMinimalWriter()
	h.ServeHTTP(w, r)
	if w.status != http.StatusInternalServerError {
		t.Errorf("expected 500 when ResponseWriter is not a Flusher, got %d", w.status)
	}
}

func TestStreamSSE_NotMountedWhenFlagOff(t *testing.T) {
	src := NewMemorySource()
	sse := New(src, 0, nil, nil)
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("base"))
	})

	gotOff := Wire(false, sse, base)
	w1 := httptest.NewRecorder()
	gotOff.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !strings.Contains(w1.Body.String(), "base") {
		t.Errorf("flag-off Wire should pass through to base, got %q", w1.Body.String())
	}

	// Flag-on returns the SSE handler — verify the SSE Content-Type is set.
	gotOn := Wire(true, sse, base)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r2 := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	w2 := newFlushRecorder()
	gotOn.ServeHTTP(w2, r2)
	if got := w2.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("flag-on Wire should mount SSE handler, got Content-Type %q", got)
	}
}

func TestMemorySource_PushDeliversToSubscriber(t *testing.T) {
	src := NewMemorySource()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Subscribe(ctx, "k", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	src.Push("k", Event{ID: "1", Type: "test", Data: "hello"})
	select {
	case e := <-ch:
		if e.ID != "1" || e.Type != "test" {
			t.Errorf("got %+v, want {ID:1 Type:test ...}", e)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("event not delivered within timeout")
	}
}

func TestMemorySource_SubscriberCountTracksLifecycle(t *testing.T) {
	src := NewMemorySource()
	if got := src.SubscriberCount("k"); got != 0 {
		t.Errorf("initial count = %d, want 0", got)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	_, _ = src.Subscribe(ctx1, "k", "")
	ctx2, cancel2 := context.WithCancel(context.Background())
	_, _ = src.Subscribe(ctx2, "k", "")

	if got := src.SubscriberCount("k"); got != 2 {
		t.Errorf("after two subscribes: count = %d, want 2", got)
	}

	cancel1()
	// Cleanup is via goroutine — give it a moment.
	deadline := time.Now().Add(time.Second)
	for src.SubscriberCount("k") != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := src.SubscriberCount("k"); got != 1 {
		t.Errorf("after one cancel: count = %d, want 1", got)
	}

	cancel2()
}

// cursorCapturingSource records the cursor passed to Subscribe so the
// LastEventID test can assert on it.
type cursorCapturingSource struct {
	mu      sync.Mutex
	captured string
}

func (s *cursorCapturingSource) Subscribe(ctx context.Context, key, cursor string) (<-chan Event, error) {
	s.mu.Lock()
	s.captured = cursor
	s.mu.Unlock()
	ch := make(chan Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (s *cursorCapturingSource) cursor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captured
}
