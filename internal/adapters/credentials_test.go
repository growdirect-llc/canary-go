package adapters

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrNoCredential_IsSentinel(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", ErrNoCredential)
	if !errors.Is(wrapped, ErrNoCredential) {
		t.Error("ErrNoCredential should unwrap through errors.Is")
	}
}

func TestCredentialStore_NewNotNil(t *testing.T) {
	// Nil-pool construction returns non-nil store (pool is never
	// dereferenced until a query runs; boot-time safety only).
	s := NewCredentialStore(nil)
	if s == nil {
		t.Fatal("NewCredentialStore returned nil")
	}
}
