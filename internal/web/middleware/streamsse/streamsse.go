// Package streamsse provides Server-Sent Events for the stream tier.
// Wraps Valkey XREAD-tail subscribers as SSE streams to browser/agent
// consumers.
//
// Per spec §"Per-tier infrastructure" stream row:
//   - Protocol: SSE / WebSocket / Valkey XREAD tail
//   - Auth:     Long-lived token (handled by route's API-key middleware)
//   - Health:   Heartbeat (no msg in N sec)
//   - Recovery: Replay from queue (Last-Event-ID → cursor)
//
// SSE was chosen over WebSocket as primary because:
//   - Native browser support (EventSource), no library required
//   - HTTP/2-friendly, plays well with intermediaries
//   - Simpler half-duplex model — server pushes, client doesn't talk back
//
// WebSocket support is a follow-on; this package leaves room for it via
// the Source abstraction, which doesn't bind to SSE.
//
// T3A.3 / GRO-900.
package streamsse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Event is one item delivered to a stream consumer. Type and ID match SSE
// "event:" and "id:" fields verbatim. Data is JSON-encoded into the
// "data:" field on write.
type Event struct {
	ID   string      // SSE event id — typically the Valkey stream ordinal
	Type string      // SSE event type — e.g. "chirp.detection", "alert.opened"
	Data interface{} // arbitrary serializable payload
}

// Source is the upstream event source. Implementations subscribe to a
// stream key (e.g. "chirp.detections" or "tenant:abc123.alerts") and
// deliver events through the returned channel. The returned channel must
// close when ctx is cancelled.
//
// Cursor is opaque — implementations interpret it (Valkey: stream ID;
// memory: index). Empty cursor means "from latest" — clients explicitly
// request replay via Last-Event-ID header.
type Source interface {
	Subscribe(ctx context.Context, key, cursor string) (<-chan Event, error)
}

// Handler streams events from a Source as SSE to HTTP clients.
type Handler struct {
	source    Source
	heartbeat time.Duration
	keyFn     func(r *http.Request) string
	logger    *zap.Logger
}

// New constructs the SSE handler. heartbeat = 0 disables heartbeats (not
// recommended in production — proxies idle-kill connections without them).
// keyFn defaults to r.URL.Path.
func New(source Source, heartbeat time.Duration, keyFn func(r *http.Request) string, logger *zap.Logger) *Handler {
	if keyFn == nil {
		keyFn = func(r *http.Request) string { return r.URL.Path }
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{source: source, heartbeat: heartbeat, keyFn: keyFn, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// SSE headers — set BEFORE first write.
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx response buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		// ResponseWriter doesn't support flushing — SSE can't function.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	key := h.keyFn(r)
	// SSE protocol: client uses Last-Event-ID header to resume after a
	// dropped connection. We pass that through as the cursor.
	cursor := r.Header.Get("Last-Event-ID")

	events, err := h.source.Subscribe(r.Context(), key, cursor)
	if err != nil {
		h.logger.Warn("streamsse: subscribe failed",
			zap.String("key", key), zap.Error(err))
		http.Error(w, "subscribe failed", http.StatusInternalServerError)
		return
	}

	// Write a leading retry-hint so EventSource clients reconnect quickly.
	_, _ = fmt.Fprintf(w, "retry: 3000\n\n")
	flusher.Flush()

	var heartbeatTick <-chan time.Time
	if h.heartbeat > 0 {
		t := time.NewTicker(h.heartbeat)
		defer t.Stop()
		heartbeatTick = t.C
	}

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected — return cleanly so the source's ctx
			// cancellation can also propagate.
			return
		case <-heartbeatTick:
			if _, err := fmt.Fprintf(w, ":heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, ok := <-events:
			if !ok {
				return // source closed
			}
			if err := writeSSE(w, e); err != nil {
				h.logger.Warn("streamsse: write failed", zap.Error(err))
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE serializes an Event into the SSE wire format. Each frame is:
//   id: <id>\n
//   event: <type>\n
//   data: <json>\n
//   \n
//
// Empty fields are skipped — id and event are optional per the SSE spec.
func writeSSE(w http.ResponseWriter, e Event) error {
	if e.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", e.ID); err != nil {
			return err
		}
	}
	if e.Type != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", e.Type); err != nil {
			return err
		}
	}
	if e.Data != nil {
		raw, err := json.Marshal(e.Data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n", raw); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n"); err != nil {
		return err
	}
	return nil
}

// Wire returns next unchanged when the flag is off; otherwise the SSE
// handler. Same shape as refcache.Wire / changefeed.Wire.
func Wire(enabled bool, sse *Handler, next http.Handler) http.Handler {
	if !enabled || sse == nil {
		return next
	}
	return sse
}

// MemorySource is an in-memory Source for tests. Push events via
// Push(); they're delivered to all current subscribers on the matching key.
type MemorySource struct {
	mu       sync.Mutex
	channels map[string][]chan Event
}

func NewMemorySource() *MemorySource {
	return &MemorySource{channels: make(map[string][]chan Event)}
}

func (m *MemorySource) Subscribe(ctx context.Context, key, _ string) (<-chan Event, error) {
	ch := make(chan Event, 16)
	m.mu.Lock()
	m.channels[key] = append(m.channels[key], ch)
	m.mu.Unlock()

	// On context cancel: remove the channel from the slice and close it.
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()
		subs := m.channels[key]
		for i, c := range subs {
			if c == ch {
				m.channels[key] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		close(ch)
	}()
	return ch, nil
}

// Push sends an event to all subscribers on the given key. Non-blocking
// per subscriber (skips if their buffer is full — slow consumer protection).
func (m *MemorySource) Push(key string, e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.channels[key] {
		select {
		case ch <- e:
		default:
			// Drop on slow consumer — production would surface this via
			// metrics; tests assert Pushed events arrive promptly.
		}
	}
}

// SubscriberCount returns the live subscriber count for a key — handy
// for test assertions.
func (m *MemorySource) SubscriberCount(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.channels[key])
}
