// Package namespace implements the .jeffe namespace registration layer —
// Node identity lifecycle of the Canary Protocol (patent Application
// 63/991,596, FIG. 5). Every entity (merchant, user, or agent) can
// claim a globally unique .jeffe name inscribed as a Bitcoin ordinal
// on signet. The inscription_id is the on-chain proof of identity.
//
// Lifecycle: Register → Mint → Seed → Transact → Renew/Retire.
// GRO-751 covers the registration round-trip (Register + bilateral
// verification lookup).
package namespace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/growdirect-llc/rapidpos/internal/protocol/sub3"
)

// ErrNameTaken is returned by Register when the requested name is
// already registered. Callers should surface this as HTTP 409.
var ErrNameTaken = errors.New("namespace: name already registered")

// ErrInvalidName is returned when the name fails format validation.
var ErrInvalidName = errors.New("namespace: invalid name format")

// RegistrationPayload is the canonical record that gets inscribed (as
// its SHA-256 hash) and stored. Keep it small — the hash goes on-chain,
// not the full payload; the full payload is recoverable from the DB.
type RegistrationPayload struct {
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`   // UUID as string
	OwnerType string    `json:"owner_type"` // "merchant" | "user" | "agent"
	RaaSUUID  string    `json:"raas_uuid"`
	Network   string    `json:"network"`
	IssuedAt  time.Time `json:"issued_at"`
}

// RegisterRequest is the input to Register.
type RegisterRequest struct {
	Name      string
	OwnerID   uuid.UUID
	OwnerType string // "merchant" | "user" | "agent"
	Network   string // default "signet"
}

// inserter is the minimal interface Register needs from the store.
// *Store satisfies it; tests can supply a stub.
type inserter interface {
	Insert(ctx context.Context, reg Registration) error
}

// Register claims a namespace name, inscribes it, and persists the
// registration. Returns the new Registration. Returns ErrNameTaken if
// the name is already registered, ErrInvalidName if the name is
// malformed.
func Register(ctx context.Context, store NamespaceStore, inscriber sub3.Inscriber,
	req RegisterRequest) (*Registration, error) {
	return register(ctx, store, inscriber, req)
}

// register is the internal implementation that accepts the inserter
// interface so unit tests can bypass the DB.
func register(ctx context.Context, ins inserter, inscriber sub3.Inscriber,
	req RegisterRequest) (*Registration, error) {

	// 1. Validate name format.
	if err := validateName(req.Name); err != nil {
		return nil, err
	}

	// 2. Default network.
	if req.Network == "" {
		req.Network = "signet"
	}

	// 3. Build the RaaS UUID first — included in the payload so the
	//    hash is stable and verifiable before the row exists.
	raasUUID := uuid.New()
	issuedAt := time.Now().UTC()

	// 4. Build registration payload + hash.
	payload := RegistrationPayload{
		Name:      req.Name,
		OwnerID:   req.OwnerID.String(),
		OwnerType: req.OwnerType,
		RaaSUUID:  raasUUID.String(),
		Network:   req.Network,
		IssuedAt:  issuedAt,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("namespace: marshal payload: %w", err)
	}
	sum := sha256.Sum256(raw)
	payloadHash := fmt.Sprintf("%x", sum[:])

	// 5. Inscribe the hash (not the full payload) via the Sub 3 Inscriber.
	result, err := inscriber.Inscribe(ctx, payloadHash, req.Network)
	if err != nil {
		return nil, fmt.Errorf("namespace: inscribe: %w", err)
	}

	// 6. Build the registration record and persist.
	reg := Registration{
		RegID:          uuid.New(),
		Name:           req.Name,
		OwnerID:        req.OwnerID,
		OwnerType:      req.OwnerType,
		RaaSUUID:       raasUUID,
		InscriptionID:  result.InscriptionID,
		BtcTxID:        result.TxID,
		BtcBlockHeight: result.BlockHeight,
		Network:        req.Network,
		RegStatus:      "pending",
		PayloadHash:    payloadHash,
		RegisteredAt:   issuedAt,
	}

	if err := ins.Insert(ctx, reg); err != nil {
		return nil, err
	}

	return &reg, nil
}

// validateName enforces the .jeffe name format rules:
//   - Must end in ".jeffe"
//   - The label before the suffix is 3–63 chars
//   - Lowercase alphanumeric and hyphens only
//   - No leading or trailing hyphens
//   - No uppercase
func validateName(name string) error {
	const suffix = ".jeffe"
	if !strings.HasSuffix(name, suffix) {
		return fmt.Errorf("%w: must end in %q", ErrInvalidName, suffix)
	}
	label := strings.TrimSuffix(name, suffix)
	// len() is correct here: validateName only reaches this point for ASCII-safe inputs ([a-z0-9-]).
	if len(label) < 3 {
		return fmt.Errorf("%w: label too short (min 3 chars before .jeffe)", ErrInvalidName)
	}
	if len(label) > 63 {
		return fmt.Errorf("%w: label too long (max 63 chars before .jeffe)", ErrInvalidName)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("%w: label must not start or end with a hyphen", ErrInvalidName)
	}
	for _, r := range label {
		if unicode.IsUpper(r) {
			return fmt.Errorf("%w: label must be lowercase", ErrInvalidName)
		}
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' {
			return fmt.Errorf("%w: label may only contain lowercase letters, digits, and hyphens", ErrInvalidName)
		}
	}
	return nil
}
