package namespace

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// ─── stubInserter ─────────────────────────────────────────────────────────────

// stubInserter is an in-memory inserter used by unit tests in place of
// *Store so no DB is required.
type stubInserter struct {
	names map[string]struct{}
}

func newStubInserter() *stubInserter {
	return &stubInserter{names: make(map[string]struct{})}
}

func (s *stubInserter) Insert(_ context.Context, reg Registration) error {
	if _, exists := s.names[reg.Name]; exists {
		return ErrNameTaken
	}
	s.names[reg.Name] = struct{}{}
	return nil
}

// ─── Name validation tests ───────────────────────────────────────────────────

func TestValidateName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "abc.jeffe", false},
		{"valid with hyphen", "acme-hardware.jeffe", false},
		{"valid digits", "shop42.jeffe", false},
		{"valid max length label (63 chars)", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jeffe", false},
		{"no suffix", "acme-hardware", true},
		{"wrong suffix", "acme-hardware.jeff", true},
		{"uppercase label", "Acme.jeffe", true},
		{"uppercase mid", "acMe.jeffe", true},
		{"leading hyphen", "-acme.jeffe", true},
		{"trailing hyphen", "acme-.jeffe", true},
		{"too short label (2 chars)", "ab.jeffe", true},
		{"too long label (64 chars)", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jeffe", true},
		{"special chars", "acme!.jeffe", true},
		{"space in label", "acme hardware.jeffe", true},
		{"empty label", ".jeffe", true},
		{"underscore not allowed", "acme_hardware.jeffe", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateName(%q) error=%v wantErr=%v", tc.input, err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrInvalidName) {
				t.Errorf("expected ErrInvalidName, got %T: %v", err, err)
			}
		})
	}
}

// ─── Register unit tests ─────────────────────────────────────────────────────

func TestRegister_Valid(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	reg, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "acme-hardware.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
		Network:   "signet",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if reg.Name != "acme-hardware.jeffe" {
		t.Errorf("name mismatch: got %q", reg.Name)
	}
	if reg.RegStatus != "pending" {
		t.Errorf("status mismatch: got %q", reg.RegStatus)
	}
	if reg.PayloadHash == "" {
		t.Error("payload_hash must not be empty")
	}
	if reg.InscriptionID == "" {
		t.Error("inscription_id must not be empty after stub inscribe")
	}
	if reg.Network != "signet" {
		t.Errorf("network mismatch: got %q", reg.Network)
	}
}

func TestRegister_DefaultsNetworkToSignet(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	reg, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "nonetwork.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "user",
		Network:   "", // should default to signet
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Network != "signet" {
		t.Errorf("expected signet default, got %q", reg.Network)
	}
}

func TestRegister_InvalidName_NoSuffix(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "no-suffix",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_InvalidName_TooShort(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "ab.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_InvalidName_TooLong(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	// 64-char label (one over max).
	longLabel := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jeffe"
	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      longLabel,
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_InvalidName_Uppercase(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "Acme.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_InvalidName_LeadingHyphen(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "-acme.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_InvalidName_TrailingHyphen(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "acme-.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "merchant",
	})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestRegister_DuplicateNameReturnsErrNameTaken(t *testing.T) {
	t.Parallel()
	ins := newStubInserter()
	inscriber := &sub3.StubInscriber{}
	ownerID := uuid.New()

	_, err := register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "first-reg.jeffe",
		OwnerID:   ownerID,
		OwnerType: "merchant",
		Network:   "signet",
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = register(context.Background(), ins, inscriber, RegisterRequest{
		Name:      "first-reg.jeffe",
		OwnerID:   uuid.New(),
		OwnerType: "user",
		Network:   "signet",
	})
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("expected ErrNameTaken, got %v", err)
	}
}
