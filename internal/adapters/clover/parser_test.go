package clover

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/adapters"
	"github.com/ruptiv/canary/internal/protocol/publisher"
)

func TestCloverAdapter_SourceCodeIsClover(t *testing.T) {
	if New().SourceCode() != "clover" {
		t.Errorf("SourceCode = %q, want clover", New().SourceCode())
	}
}

func TestCloverParse_AlwaysReturnsErrNotImplemented(t *testing.T) {
	env := publisher.Event{
		EventID:    uuid.New(),
		SourceCode: "clover",
		Payload:    json.RawMessage(`{"anything":"goes"}`),
		IngestedAt: time.Now().UTC(),
	}
	_, err := New().Parse(env)
	if !errors.Is(err, adapters.ErrNotImplemented) {
		t.Errorf("want ErrNotImplemented; got %v", err)
	}
}
