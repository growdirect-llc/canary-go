// internal/replenishment/trigger_panic_test.go
//
// Unit test for handleSafely's panic-recovery contract (GRO-909).
//
// The point of GRO-909 is that a panic in handle() — e.g. a nil-pointer in
// taskStore.Create — must NOT escape and kill the trigger goroutine, taking
// down replenishment processing for the whole bull binary. The contract:
//
//   1. handleSafely catches the panic.
//   2. Subsequent messages in the batch still run.
//   3. The panicking message is NOT acked (poison-pill stays in PEL for
//      redelivery / XPENDING inspection).
//
// We test this directly against handleSafely (rather than driving processBatch
// with a stubbed Redis client) because handleSafely's panic-recovery semantics
// are the critical contract; whether processBatch loops correctly is exercised
// by integration tests against real Valkey.
package replenishment

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// TestHandleSafely_RecoverPanic_ContinuesBatch verifies that handleSafely
// catches a panic from the inner handler so a poison-pill message in a batch
// does not abort the trigger goroutine, and that subsequent messages still
// process.
func TestHandleSafely_RecoverPanic_ContinuesBatch(t *testing.T) {
	tr := &Trigger{
		logger: zap.NewNop(),
	}

	msg1 := redis.XMessage{ID: "1-0", Values: map[string]any{"poison": "yes"}}
	msg2 := redis.XMessage{ID: "2-0", Values: map[string]any{"poison": "no"}}

	processed := []string{}
	handler := func(ctx context.Context, m redis.XMessage) {
		if m.Values["poison"] == "yes" {
			// Simulate the realistic bug: a nil-pointer dereference in
			// taskStore.Create or similar.
			var nilPtr *int
			_ = *nilPtr // panics
		}
		processed = append(processed, m.ID)
	}

	// Drive a "batch" of two messages through handleSafely the same way
	// processBatch does. The first must panic-and-recover; the second must
	// still run.
	ctx := context.Background()
	for _, m := range []redis.XMessage{msg1, msg2} {
		tr.handleSafely(ctx, m, handler)
	}

	if len(processed) != 1 {
		t.Fatalf("expected 1 processed message, got %d: %v", len(processed), processed)
	}
	if processed[0] != "2-0" {
		t.Errorf("expected msg 2-0 to process after panic in 1-0, got %q", processed[0])
	}
}

// TestHandleSafely_NoPanic_PassesThrough verifies the happy path: handleSafely
// is a no-op wrapper when the inner function does not panic.
func TestHandleSafely_NoPanic_PassesThrough(t *testing.T) {
	tr := &Trigger{logger: zap.NewNop()}

	called := false
	handler := func(ctx context.Context, m redis.XMessage) {
		called = true
	}

	tr.handleSafely(context.Background(), redis.XMessage{ID: "1-0"}, handler)

	if !called {
		t.Error("inner handler was not invoked")
	}
}

// TestHandleSafely_PanicWithStringValue verifies recover handles non-error
// panic values (e.g. a bare string passed to panic()), since recover() returns
// any.
func TestHandleSafely_PanicWithStringValue(t *testing.T) {
	tr := &Trigger{logger: zap.NewNop()}

	handler := func(ctx context.Context, m redis.XMessage) {
		panic("synthetic panic with string payload")
	}

	// If recovery is broken, this defer-test pattern would be unreachable.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped handleSafely: %v", r)
		}
	}()

	tr.handleSafely(context.Background(), redis.XMessage{ID: "1-0"}, handler)
}
