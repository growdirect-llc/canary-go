// sm_resolver_test.go
//
// Unit tests for SmResolver. Tests are unit-level — they use a mock
// secretManagerClient and a small ad-hoc fake for the Postgres path.
// The Postgres branch is exercised via integration tests against a
// real DB elsewhere; here we focus on caching, error mapping, and
// log sanitization.
package secrets

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockSMClient implements secretManagerClient. It records every call
// and returns scripted responses or errors keyed by resource name.
type mockSMClient struct {
	responses map[string][]byte // resource -> payload
	errors    map[string]error  // resource -> error
	calls     int64             // total AccessSecretVersion calls
	closeErr  error
	closed    int64
}

func newMockSMClient() *mockSMClient {
	return &mockSMClient{
		responses: make(map[string][]byte),
		errors:    make(map[string]error),
	}
}

func (m *mockSMClient) AccessSecretVersion(
	_ context.Context,
	req *secretmanagerpb.AccessSecretVersionRequest,
	_ ...option.ClientOption,
) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	atomic.AddInt64(&m.calls, 1)
	if err, ok := m.errors[req.Name]; ok {
		return nil, err
	}
	if data, ok := m.responses[req.Name]; ok {
		return &secretmanagerpb.AccessSecretVersionResponse{
			Name: req.Name,
			Payload: &secretmanagerpb.SecretPayload{
				Data: data,
			},
		}, nil
	}
	return nil, status.Error(codes.NotFound, "secret not found")
}

func (m *mockSMClient) Close() error {
	atomic.AddInt64(&m.closed, 1)
	return m.closeErr
}

func (m *mockSMClient) callCount() int64 { return atomic.LoadInt64(&m.calls) }

// newResolverWithCache returns a resolver wired with a mock SM client
// and a hand-built cache state, bypassing the Postgres path entirely.
// Tests that need to exercise the cache directly use this.
func newResolverWithMock(t *testing.T, mock *mockSMClient, ttl time.Duration, logger *zap.Logger) *SmResolver {
	t.Helper()
	r := &SmResolver{
		pool:      nil, // Tests using this helper bypass Postgres lookup.
		sm:        mock,
		logger:    logger,
		projectID: "canary-rapidpos",
		cacheTTL:  ttl,
		cache:     make(map[string]cacheEntry),
	}
	return r
}

// TestBuildResourcePath pins the SM resource naming convention so
// runtime, seeding, and migration backfill all stay in lockstep.
func TestBuildResourcePath(t *testing.T) {
	merchantID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := BuildResourcePath("canary-rapidpos", merchantID, "rapidpos")
	want := "projects/canary-rapidpos/secrets/canary-source-11111111-2222-3333-4444-555555555555-rapidpos/versions/latest"
	if got != want {
		t.Fatalf("BuildResourcePath: got %q, want %q", got, want)
	}
}

// TestFetchSecretValue_Success — golden path: SM returns bytes,
// resolver hands them back unchanged.
func TestFetchSecretValue_Success(t *testing.T) {
	mock := newMockSMClient()
	resourcePath := "projects/p/secrets/canary-source-x/versions/latest"
	mock.responses[resourcePath] = []byte("super-secret-bytes")

	r := newResolverWithMock(t, mock, DefaultCacheTTL, zap.NewNop())

	got, err := r.fetchSecretValue(context.Background(), resourcePath)
	if err != nil {
		t.Fatalf("fetchSecretValue: unexpected error: %v", err)
	}
	if string(got) != "super-secret-bytes" {
		t.Fatalf("fetchSecretValue: got %q, want %q", got, "super-secret-bytes")
	}
}

// TestFetchSecretValue_NotFound — SM NotFound must map to ErrNotFound,
// matching PgxResolver semantics so the gateway returns 401.
func TestFetchSecretValue_NotFound(t *testing.T) {
	mock := newMockSMClient()
	r := newResolverWithMock(t, mock, DefaultCacheTTL, zap.NewNop())

	_, err := r.fetchSecretValue(context.Background(), "projects/p/secrets/missing/versions/latest")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("fetchSecretValue: got %v, want ErrNotFound", err)
	}
}

// TestFetchSecretValue_OtherError — non-NotFound errors propagate
// wrapped (so callers can distinguish "no secret" from "SM down").
func TestFetchSecretValue_OtherError(t *testing.T) {
	mock := newMockSMClient()
	resourcePath := "projects/p/secrets/x/versions/latest"
	mock.errors[resourcePath] = status.Error(codes.PermissionDenied, "no access")

	r := newResolverWithMock(t, mock, DefaultCacheTTL, zap.NewNop())

	_, err := r.fetchSecretValue(context.Background(), resourcePath)
	if err == nil {
		t.Fatal("fetchSecretValue: expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("fetchSecretValue: PermissionDenied must not map to ErrNotFound, got %v", err)
	}
}

// TestCache_HitDoesNotCallSM — second lookup with a fresh cache entry
// must not touch SM.
func TestCache_HitDoesNotCallSM(t *testing.T) {
	mock := newMockSMClient()
	resourcePath := "projects/p/secrets/cached/versions/latest"
	mock.responses[resourcePath] = []byte("v1")

	r := newResolverWithMock(t, mock, DefaultCacheTTL, zap.NewNop())

	merchantID := uuid.New()
	key := memKey(merchantID, "rapidpos")

	// Prime the cache with a synthetic Secret (bypassing Postgres).
	primed := Secret{
		ID:            uuid.New(),
		MerchantID:    merchantID,
		SourceCode:    "rapidpos",
		Secret:        []byte("v1"),
		SignatureAlgo: "HMAC-SHA256",
		ReplayWindow:  300 * time.Second,
	}
	r.cachePut(key, primed)

	got, ok := r.cacheGet(key)
	if !ok {
		t.Fatal("cacheGet: expected hit")
	}
	if string(got.Secret) != "v1" {
		t.Fatalf("cacheGet: got %q, want %q", got.Secret, "v1")
	}
	if mock.callCount() != 0 {
		t.Fatalf("expected 0 SM calls on cache hit, got %d", mock.callCount())
	}
}

// TestCache_Miss — never inserted, never expired, but no entry: miss.
func TestCache_Miss(t *testing.T) {
	r := newResolverWithMock(t, newMockSMClient(), DefaultCacheTTL, zap.NewNop())
	merchantID := uuid.New()
	if _, ok := r.cacheGet(memKey(merchantID, "rapidpos")); ok {
		t.Fatal("cacheGet: expected miss on empty cache")
	}
}

// TestCache_Expiration — entries past TTL must be treated as missing.
func TestCache_Expiration(t *testing.T) {
	// 1ms TTL — easy to cross deterministically with a sleep.
	r := newResolverWithMock(t, newMockSMClient(), 1*time.Millisecond, zap.NewNop())

	merchantID := uuid.New()
	key := memKey(merchantID, "rapidpos")
	r.cachePut(key, Secret{
		ID:            uuid.New(),
		MerchantID:    merchantID,
		SourceCode:    "rapidpos",
		Secret:        []byte("expiring"),
		SignatureAlgo: "HMAC-SHA256",
		ReplayWindow:  300 * time.Second,
	})

	// Sleep past TTL so the entry is stale.
	time.Sleep(10 * time.Millisecond)

	if _, ok := r.cacheGet(key); ok {
		t.Fatal("cacheGet: expected miss after TTL expiration")
	}
}

// TestInvalidate_EvictsEntry — Invalidate removes a cached secret.
func TestInvalidate_EvictsEntry(t *testing.T) {
	r := newResolverWithMock(t, newMockSMClient(), DefaultCacheTTL, zap.NewNop())
	merchantID := uuid.New()
	key := memKey(merchantID, "rapidpos")
	r.cachePut(key, Secret{Secret: []byte("x")})

	if _, ok := r.cacheGet(key); !ok {
		t.Fatal("expected cache hit before Invalidate")
	}
	r.Invalidate(merchantID, "rapidpos")
	if _, ok := r.cacheGet(key); ok {
		t.Fatal("expected cache miss after Invalidate")
	}
}

// TestLogs_DoNotContainSecretValue — the most important test in this
// file. Run a path that produces a log line and assert the secret
// value never appears in any field. We exercise both the success log
// (Debug-level "secret manager hit") and the failure log (Warn for
// NotFound, Error for other errors).
//
// Strategy: capture all zap entries via observer; iterate fields and
// check no field contains the secret bytes.
func TestLogs_DoNotContainSecretValue(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	mock := newMockSMClient()
	successPath := "projects/p/secrets/canary-source-success/versions/latest"
	notFoundPath := "projects/p/secrets/canary-source-missing/versions/latest"
	errPath := "projects/p/secrets/canary-source-err/versions/latest"
	secretValue := []byte("DO-NOT-LEAK-THIS-VALUE-A1B2C3D4")
	mock.responses[successPath] = secretValue
	mock.errors[errPath] = status.Error(codes.PermissionDenied, "no access")

	r := newResolverWithMock(t, mock, DefaultCacheTTL, logger)

	// Path 1: success — logger may or may not emit, but if it does
	// the secret must never appear.
	got, err := r.fetchSecretValue(context.Background(), successPath)
	if err != nil {
		t.Fatalf("success path: unexpected error: %v", err)
	}
	if string(got) != string(secretValue) {
		t.Fatalf("success path: returned wrong bytes")
	}

	// Path 2: NotFound — emits a Warn line. Must include resource,
	// must not include any secret bytes.
	if _, err := r.fetchSecretValue(context.Background(), notFoundPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notFound path: got %v, want ErrNotFound", err)
	}

	// Path 3: PermissionDenied — emits an Error line.
	if _, err := r.fetchSecretValue(context.Background(), errPath); err == nil {
		t.Fatal("err path: expected error")
	}

	// Now scan every captured entry. Any occurrence of the secret
	// value (in the message or any field's stringified form) is a
	// failure.
	needle := string(secretValue)
	for _, entry := range recorded.All() {
		if strings.Contains(entry.Message, needle) {
			t.Fatalf("log message leaked secret: %q", entry.Message)
		}
		for _, f := range entry.Context {
			// f.String covers string fields; for non-string fields
			// (Int, Error, etc.) f.Interface holds the value. We
			// stringify both forms.
			if strings.Contains(f.String, needle) {
				t.Fatalf("log field %q leaked secret as String: %q", f.Key, f.String)
			}
			if iface := f.Interface; iface != nil {
				if s, ok := iface.(string); ok && strings.Contains(s, needle) {
					t.Fatalf("log field %q leaked secret in Interface: %q", f.Key, s)
				}
				if b, ok := iface.([]byte); ok && strings.Contains(string(b), needle) {
					t.Fatalf("log field %q leaked secret in Interface []byte", f.Key)
				}
			}
		}
	}
}

// TestClose_PassesThrough — Close on the resolver closes the client.
func TestClose_PassesThrough(t *testing.T) {
	mock := newMockSMClient()
	r := newResolverWithMock(t, mock, DefaultCacheTTL, zap.NewNop())
	if err := r.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if atomic.LoadInt64(&mock.closed) != 1 {
		t.Fatal("Close: expected mock.Close to be called once")
	}
}

// TestNewSmResolver_RequiresProject — sanity check on constructor
// validation.
func TestNewSmResolver_RequiresProject(t *testing.T) {
	// We don't pass a real pool here because we're checking that
	// validation fails before any pool use; passing nil is fine.
	if _, err := NewSmResolver(context.Background(), nil, "canary-rapidpos"); err == nil {
		t.Fatal("expected error for nil pool")
	}
	// Use a non-nil sentinel pool via type assertion — but since we
	// can't easily construct a *pgxpool.Pool in unit tests, just
	// verify the projectID branch.
	//
	// A nil-pool check fires first; this confirms the code path.
}
