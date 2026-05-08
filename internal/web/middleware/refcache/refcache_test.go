package refcache

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// echoHandler returns a handler that increments callCount on each invocation
// and writes a deterministic body so we can detect cache hits.
func echoHandler(callCount *int32, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

func TestRefCache_HitReturnsCachedResponse(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `{"id":1}`))

	// First request — miss, populates cache.
	r1 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call: status %d, want 200", w1.Code)
	}
	if got := w1.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("first call: X-Cache = %q, want MISS", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("first call: handler invocations = %d, want 1", got)
	}

	// Second request — hit, handler not invoked again.
	r2 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second call: status %d, want 200", w2.Code)
	}
	if got := w2.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("second call: X-Cache = %q, want HIT", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("second call: handler invocations = %d, want 1 (cached)", got)
	}
	if w2.Body.String() != `{"id":1}` {
		t.Errorf("second call: body mismatch %q", w2.Body.String())
	}
	// Content-Type should round-trip from the cache.
	if got := w2.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("second call: Content-Type = %q, want application/json", got)
	}
}

func TestRefCache_MissPopulatesCache(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `ok`))

	r := httptest.NewRequest(http.MethodGet, "/v1/m/items/42", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if cache.Len() != 1 {
		t.Errorf("cache should have 1 entry after first GET, got %d", cache.Len())
	}
}

func TestRefCache_TTLExpires(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cache := NewMemoryCache(func() time.Time { return now })
	mw := New(cache, 30*time.Second, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `body`))

	// Populate
	r1 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	wrapped.ServeHTTP(httptest.NewRecorder(), r1)

	// Advance past TTL
	now = now.Add(31 * time.Second)

	// Second request should be a miss (entry expired) — handler invoked again.
	r2 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, r2)
	if got := w2.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("after TTL: X-Cache = %q, want MISS", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("after TTL: handler invocations = %d, want 2", got)
	}
}

func TestRefCache_NonGETPasses(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `ok`))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := httptest.NewRequest(method, "/v1/m/items/1", nil)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if got := w.Header().Get("X-Cache"); got != "BYPASS" {
			t.Errorf("%s: X-Cache = %q, want BYPASS", method, got)
		}
	}
	if cache.Len() != 0 {
		t.Errorf("non-GET methods should not populate cache, got %d entries", cache.Len())
	}
}

func TestRefCache_AuthorizationHeaderInKey(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `ok`))

	mkReq := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return r
	}

	// Two different tokens — should produce two distinct cache entries.
	wrapped.ServeHTTP(httptest.NewRecorder(), mkReq("token-a"))
	wrapped.ServeHTTP(httptest.NewRecorder(), mkReq("token-b"))
	if cache.Len() != 2 {
		t.Errorf("different Authorization headers should produce 2 entries, got %d", cache.Len())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("handler should have been invoked twice for distinct auth, got %d", got)
	}

	// Third request with token-a — should hit.
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, mkReq("token-a"))
	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("repeated token-a: X-Cache = %q, want HIT", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("handler should not be invoked for cached token-a, got %d", got)
	}
}

func TestRefCache_AcceptHeaderInKey(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `ok`))

	r1 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	r1.Header.Set("Accept", "application/json")
	wrapped.ServeHTTP(httptest.NewRecorder(), r1)

	r2 := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	r2.Header.Set("Accept", "application/yaml")
	wrapped.ServeHTTP(httptest.NewRecorder(), r2)

	if cache.Len() != 2 {
		t.Errorf("different Accept headers should produce 2 entries, got %d", cache.Len())
	}
}

func TestRefCache_QueryStringInKey(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	var calls int32
	wrapped := mw.Wrap(echoHandler(&calls, `ok`))

	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/m/items?limit=10", nil))
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/m/items?limit=20", nil))

	if cache.Len() != 2 {
		t.Errorf("different query strings should produce 2 entries, got %d", cache.Len())
	}
}

func TestRefCache_NonOKResponseNotCached(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	wrapped := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/m/items/1", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status should propagate as 500, got %d", w.Code)
	}
	if cache.Len() != 0 {
		t.Errorf("500 response should not populate cache, got %d entries", cache.Len())
	}
}

func TestRefCache_PrefixIsolatesServices(t *testing.T) {
	cache := NewMemoryCache(nil)
	mwItems := New(cache, time.Minute, "items:")
	mwLocs := New(cache, time.Minute, "locations:")

	wrapped1 := mwItems.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("items"))
	}))
	wrapped2 := mwLocs.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("locations"))
	}))

	// Same path, different prefix — two cache entries.
	wrapped1.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	wrapped2.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if cache.Len() != 2 {
		t.Errorf("different prefixes should produce 2 entries, got %d", cache.Len())
	}
}

func TestRefCache_NotMountedWhenFlagOff(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("base"))
	})

	// Wire with flag off — should return the base handler unchanged.
	gotOff := Wire(false, mw, base)
	w := httptest.NewRecorder()
	gotOff.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Header().Get("X-Cache") != "" {
		t.Errorf("flag-off Wire should not set X-Cache, got %q", w.Header().Get("X-Cache"))
	}

	// Wire with flag on — should attach middleware.
	gotOn := Wire(true, mw, base)
	w2 := httptest.NewRecorder()
	gotOn.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w2.Header().Get("X-Cache") != "MISS" {
		t.Errorf("flag-on Wire should set X-Cache: MISS, got %q", w2.Header().Get("X-Cache"))
	}
}

func TestRefCache_HopByHopHeadersExcluded(t *testing.T) {
	cache := NewMemoryCache(nil)
	mw := New(cache, time.Minute, "test:")
	wrapped := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", "ignored")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=should-not-cache")
		_, _ = w.Write([]byte("ok"))
	}))

	// Populate cache.
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	// Re-fetch — should be HIT, but Set-Cookie / Date should not have been replayed.
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected HIT, got %q", w.Header().Get("X-Cache"))
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Errorf("Set-Cookie should not be replayed from cache, got %q", w.Header().Get("Set-Cookie"))
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type should be replayed, got %q", w.Header().Get("Content-Type"))
	}
}
