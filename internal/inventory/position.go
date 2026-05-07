// internal/inventory/position.go
//
// Read-side service logic. Thin pass-through over Store; lives as a
// separate file so handler tests can swap a stub Store via the
// PositionReader interface.
package inventory

import (
	"context"

	"github.com/google/uuid"
)

// PositionReader is the read-side surface used by the handler. Allows
// the handler unit tests to inject a stub without standing up pgx.
type PositionReader interface {
	GetPosition(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (*PositionDTO, error)
	ListPositions(ctx context.Context, tenantID uuid.UUID, locationID, itemID *uuid.UUID, limit, offset int) ([]PositionDTO, error)
}

// Compile-time guard: *Store satisfies PositionReader.
var _ PositionReader = (*Store)(nil)
