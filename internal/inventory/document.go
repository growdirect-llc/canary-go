// internal/inventory/document.go
//
// Read access to inventory.inventory_documents + inventory_document_lines.
// inventory_documents is a single table type-discriminated by document_type
// (goods_receipt | transfer_out | transfer_in | rtv | stock_count |
// adjustment_batch). The portal uses these reads to render the Transfers
// list/detail/variance views (W2b / GRO-816), the Receiving list (W2d),
// the Returns list (W2d), and the distribution variance report (W2b /
// W2e).
//
// Writes are out of scope for W2b — transfer creation is GRO-824 (W5).

package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrDocumentNotFound is returned by GetDocument when the (tenant_id, id)
// pair has no row.
var ErrDocumentNotFound = errors.New("inventory: document not found")

// Document type discriminators stored in inventory_documents.document_type.
const (
	DocumentTypeGoodsReceipt    = "goods_receipt"
	DocumentTypeTransferOut     = "transfer_out"
	DocumentTypeTransferIn      = "transfer_in"
	DocumentTypeRTV             = "rtv"
	DocumentTypeStockCount      = "stock_count"
	DocumentTypeAdjustmentBatch = "adjustment_batch"
)

// TransferTypes is the set of document types that represent inter-location
// movement. Used by ListDocuments callers that want both legs of a transfer.
var TransferTypes = []string{DocumentTypeTransferOut, DocumentTypeTransferIn}

// DocumentDTO mirrors a row in inventory.inventory_documents.
type DocumentDTO struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	DocumentType          string
	DocumentNumber        string
	SourceLocationID      *uuid.UUID
	DestinationLocationID *uuid.UUID
	VendorID              *uuid.UUID
	RelatedOrderID        *uuid.UUID
	Status                string
	ExpectedAt            *time.Time
	CompletedAt           *time.Time
	TotalQuantity         *string
	TotalCost             *string
	PerformedByUserID     *uuid.UUID
	Attributes            json.RawMessage
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// DocumentLineDTO mirrors a row in inventory.inventory_document_lines.
// VarianceQuantity is a generated column computed at the DB.
type DocumentLineDTO struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	DocumentID       uuid.UUID
	LineNumber       int
	ItemID           uuid.UUID
	ExpectedQuantity *string
	ActualQuantity   *string
	VarianceQuantity string
	VarianceReason   *string
	UnitCost         *string
	LotID            *uuid.UUID
	Attributes       json.RawMessage
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ListDocumentsFilter scopes a ListDocuments call. Empty types means
// "all types"; empty status means "any status".
type ListDocumentsFilter struct {
	TenantID uuid.UUID
	Types    []string
	Status   string
	Limit    int
}

// ListDocuments returns documents matching the filter, ordered by
// created_at DESC. limit defaults to 100, max 500.
func (s *Store) ListDocuments(ctx context.Context, f ListDocumentsFilter) ([]DocumentDTO, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	const baseQuery = `
		SELECT id, tenant_id, document_type, document_number,
		       source_location_id, destination_location_id, vendor_id,
		       related_order_id, status, expected_at, completed_at,
		       total_quantity::text, total_cost::text, performed_by_user_id,
		       attributes, created_at, updated_at
		FROM inventory.inventory_documents
		WHERE tenant_id = $1
	`

	args := []any{f.TenantID}
	q := baseQuery
	if len(f.Types) > 0 {
		args = append(args, f.Types)
		q += fmt.Sprintf(" AND document_type = ANY($%d)", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("inventory: list documents: %w", err)
	}
	defer rows.Close()

	out := make([]DocumentDTO, 0, limit)
	for rows.Next() {
		var d DocumentDTO
		if err := rows.Scan(
			&d.ID, &d.TenantID, &d.DocumentType, &d.DocumentNumber,
			&d.SourceLocationID, &d.DestinationLocationID, &d.VendorID,
			&d.RelatedOrderID, &d.Status, &d.ExpectedAt, &d.CompletedAt,
			&d.TotalQuantity, &d.TotalCost, &d.PerformedByUserID,
			&d.Attributes, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("inventory: list documents scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDocument returns one document by id (tenant-scoped).
// Returns ErrDocumentNotFound if no row.
func (s *Store) GetDocument(ctx context.Context, tenantID, id uuid.UUID) (*DocumentDTO, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, document_type, document_number,
		       source_location_id, destination_location_id, vendor_id,
		       related_order_id, status, expected_at, completed_at,
		       total_quantity::text, total_cost::text, performed_by_user_id,
		       attributes, created_at, updated_at
		FROM inventory.inventory_documents
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)

	var d DocumentDTO
	if err := row.Scan(
		&d.ID, &d.TenantID, &d.DocumentType, &d.DocumentNumber,
		&d.SourceLocationID, &d.DestinationLocationID, &d.VendorID,
		&d.RelatedOrderID, &d.Status, &d.ExpectedAt, &d.CompletedAt,
		&d.TotalQuantity, &d.TotalCost, &d.PerformedByUserID,
		&d.Attributes, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDocumentNotFound
		}
		return nil, fmt.Errorf("inventory: get document: %w", err)
	}
	return &d, nil
}

// ListDocumentLines returns all lines for a document (tenant-scoped),
// ordered by line_number ASC.
func (s *Store) ListDocumentLines(ctx context.Context, tenantID, documentID uuid.UUID) ([]DocumentLineDTO, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, document_id, line_number, item_id,
		       expected_quantity::text, actual_quantity::text, variance_quantity::text,
		       variance_reason, unit_cost::text, lot_id,
		       attributes, created_at, updated_at
		FROM inventory.inventory_document_lines
		WHERE tenant_id = $1 AND document_id = $2
		ORDER BY line_number ASC`,
		tenantID, documentID)
	if err != nil {
		return nil, fmt.Errorf("inventory: list document lines: %w", err)
	}
	defer rows.Close()

	out := make([]DocumentLineDTO, 0, 32)
	for rows.Next() {
		var l DocumentLineDTO
		if err := rows.Scan(
			&l.ID, &l.TenantID, &l.DocumentID, &l.LineNumber, &l.ItemID,
			&l.ExpectedQuantity, &l.ActualQuantity, &l.VarianceQuantity,
			&l.VarianceReason, &l.UnitCost, &l.LotID,
			&l.Attributes, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("inventory: list document lines scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DistributionLane is one row in the distribution variance report —
// aggregate variance across documents for a (source, destination) pair.
type DistributionLane struct {
	SourceLocationID      *uuid.UUID
	DestinationLocationID *uuid.UUID
	DocumentCount         int
	TotalShipped          string
	TotalReceived         string
	TotalVariance         string
}

// ListDistributionLanes aggregates transfer documents by lane, computing
// shipped vs received variance across each (source, destination) pair.
// Used by /reports/distribution.
func (s *Store) ListDistributionLanes(ctx context.Context, tenantID uuid.UUID, limit int) ([]DistributionLane, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		    d.source_location_id,
		    d.destination_location_id,
		    COUNT(DISTINCT d.id)::int           AS doc_count,
		    COALESCE(SUM(l.expected_quantity), 0)::text AS total_shipped,
		    COALESCE(SUM(l.actual_quantity), 0)::text   AS total_received,
		    COALESCE(SUM(l.variance_quantity), 0)::text AS total_variance
		FROM inventory.inventory_documents d
		LEFT JOIN inventory.inventory_document_lines l
		    ON l.document_id = d.id AND l.tenant_id = d.tenant_id
		WHERE d.tenant_id = $1
		  AND d.document_type = ANY($2)
		GROUP BY d.source_location_id, d.destination_location_id
		ORDER BY total_variance DESC
		LIMIT $3`,
		tenantID, TransferTypes, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory: list distribution lanes: %w", err)
	}
	defer rows.Close()

	out := make([]DistributionLane, 0, limit)
	for rows.Next() {
		var l DistributionLane
		if err := rows.Scan(
			&l.SourceLocationID, &l.DestinationLocationID,
			&l.DocumentCount, &l.TotalShipped, &l.TotalReceived, &l.TotalVariance,
		); err != nil {
			return nil, fmt.Errorf("inventory: list distribution lanes scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
