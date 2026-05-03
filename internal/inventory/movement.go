// internal/inventory/movement.go
//
// Movement-side service logic — type validation and the MovementWriter
// interface used by the handler. Append-only by design; there is no
// UpdateMovement or DeleteMovement on this interface.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidMovementType is returned when a request carries a movement_type
// that is not in the canonical schema enum (see schema line 124).
var ErrInvalidMovementType = errors.New("inventory: invalid movement_type")

// ErrInvalidQuantity is returned when the quantity field is empty, not
// numeric, or zero. A zero-delta movement is meaningless and rejected.
var ErrInvalidQuantity = errors.New("inventory: invalid quantity")

// ErrMissingField is returned when a required UUID field on the request
// is the zero UUID.
var ErrMissingField = errors.New("inventory: missing required field")

// MovementWriter is the append-only write surface used by the handler.
// Note the absence of UpdateMovement / DeleteMovement methods. This is
// deliberate (dispatch line 67): the type itself rules out the wrong
// semantics so the handler cannot be wired to a violating store.
type MovementWriter interface {
	AppendMovement(ctx context.Context, req AppendMovementRequest, movementAt time.Time) (*MovementDTO, *PositionDTO, error)
	ListMovements(ctx context.Context, tenantID, itemID, locationID uuid.UUID, from, to *time.Time, limit, offset int) ([]MovementDTO, error)
}

// Compile-time guard.
var _ MovementWriter = (*Store)(nil)

// ValidateAppendRequest performs handler-boundary validation. Returns
// the canonical error so the handler can map to a status code, and a
// trimmed/normalised request copy ready for the store.
func ValidateAppendRequest(req AppendMovementRequest) (AppendMovementRequest, error) {
	if req.MerchantID == uuid.Nil {
		return req, fmt.Errorf("%w: merchant_id", ErrMissingField)
	}
	if req.ItemID == uuid.Nil {
		return req, fmt.Errorf("%w: item_id", ErrMissingField)
	}
	if req.LocationID == uuid.Nil {
		return req, fmt.Errorf("%w: location_id", ErrMissingField)
	}

	mt := strings.TrimSpace(req.MovementType)
	if _, ok := MovementTypes[mt]; !ok {
		return req, fmt.Errorf("%w: %q (valid: %s)", ErrInvalidMovementType, mt, validTypeList())
	}
	req.MovementType = mt

	q := strings.TrimSpace(req.Quantity)
	if q == "" {
		return req, fmt.Errorf("%w: empty", ErrInvalidQuantity)
	}
	v, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return req, fmt.Errorf("%w: %q is not numeric", ErrInvalidQuantity, q)
	}
	if v == 0 {
		return req, fmt.Errorf("%w: zero delta is not a movement", ErrInvalidQuantity)
	}
	req.Quantity = q
	return req, nil
}

// validTypeList renders the schema enum into a stable comma-separated
// string for use in error messages. Sorted for determinism.
func validTypeList() string {
	keys := make([]string, 0, len(MovementTypes))
	for k := range MovementTypes {
		keys = append(keys, k)
	}
	// Insertion sort — list is small and we don't want to import sort.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return strings.Join(keys, ", ")
}
