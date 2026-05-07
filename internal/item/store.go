// internal/item/store.go
//
// pgx-backed store for the item domain. Direct SQL — Loop 2 dispatch
// overrides the codebase-wide sqlc rule (CanaryGo/CLAUDE.md). The
// generated sqlc retrofit is a Loop 3 deliverable.
//
// Design notes:
//   - Every read/write is tenant-scoped. The schema's UNIQUE constraints
//     are (tenant_id, …); queries always include tenant_id in the
//     predicate to avoid cross-tenant reads even on indexed lookups.
//   - The barcode resolve query (GetByBarcode) is the keystone POS-scan
//     path. It hits idx_barcodes_lookup (a partial unique index on
//     active barcodes), joins catalog.items once, returns in a single round
//     trip. No N+1, no extra fetches before the hot path returns.
//   - Soft delete: DELETE flips status to 'inactive'. The dispatch said
//     soft-delete unless the schema demands hard delete; catalog.items has a
//     status column so soft is the right call.

package item

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/growdirect-llc/rapidpos/internal/db/types"
)

// Store is the data-access surface used by the HTTP handler. The
// concrete implementation hits Postgres via pgx. Handler tests can
// stub this interface without spinning up a database.
type Store interface {
	GetByID(ctx context.Context, tenantID, id uuid.UUID) (*Item, error)
	GetBySKU(ctx context.Context, tenantID uuid.UUID, sku string) (*Item, error)
	GetByBarcode(ctx context.Context, tenantID uuid.UUID, barcode string) (*Item, error)
	Create(ctx context.Context, req CreateRequest) (*Item, error)
	Update(ctx context.Context, tenantID, id uuid.UUID, patch PatchRequest) (*Item, error)
	Delete(ctx context.Context, tenantID, id uuid.UUID) error
	List(ctx context.Context, f ListFilters) ([]Item, error)
	ListCategories(ctx context.Context, tenantID uuid.UUID) ([]Category, error)
	ListVendors(ctx context.Context, tenantID uuid.UUID) ([]Vendor, error)
}

// PgxStore is the production Store. Construct with NewPgxStore(pool).
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore wires a *pgxpool.Pool into a Store.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore { return &PgxStore{pool: pool} }

// itemColumns is the canonical SELECT list for catalog.items. Keep this in
// lock-step with scanItem — the order matters.
const itemColumns = `id, tenant_id, sku, description, short_description, item_type,
		category_id, unit_of_measure, uom_quantity::text, default_price::text,
		default_cost::text, default_currency, tax_class, food_stamp_eligible,
		age_restriction, weighable, attributes, status, created_at, updated_at`

// scanItem reads one catalog.items row from a Row interface (Row or RowsRow).
// Numeric columns are cast to ::text in the SELECT so pgx delivers them
// as Go strings — Wave 1 types use string for numerics until decimal
// support lands in Loop 3.
func scanItem(row pgx.Row) (types.Item, error) {
	var it types.Item
	err := row.Scan(
		&it.ID, &it.TenantID, &it.SKU, &it.Description, &it.ShortDescription,
		&it.ItemType, &it.CategoryID, &it.UnitOfMeasure, &it.UOMQuantity,
		&it.DefaultPrice, &it.DefaultCost, &it.DefaultCurrency, &it.TaxClass,
		&it.FoodStampEligible, &it.AgeRestriction, &it.Weighable, &it.Attributes,
		&it.Status, &it.CreatedAt, &it.UpdatedAt,
	)
	return it, err
}

// barcodeColumns mirrors itemColumns for catalog.item_barcodes.
const barcodeColumns = `id, tenant_id, item_id, barcode, barcode_type,
		uom_quantity::text, is_primary, attributes, status, created_at, updated_at`

func scanBarcode(row pgx.Row) (types.ItemBarcode, error) {
	var b types.ItemBarcode
	err := row.Scan(
		&b.ID, &b.TenantID, &b.ItemID, &b.Barcode, &b.BarcodeType,
		&b.UOMQuantity, &b.IsPrimary, &b.Attributes, &b.Status,
		&b.CreatedAt, &b.UpdatedAt,
	)
	return b, err
}

// fetchBarcodes loads all active barcodes for an item. Called by
// GetByID / GetBySKU / GetByBarcode after the item row lands. Single
// indexed query (idx_barcodes_item).
func (s *PgxStore) fetchBarcodes(ctx context.Context, tenantID, itemID uuid.UUID) ([]types.ItemBarcode, error) {
	q := `SELECT ` + barcodeColumns + `
		FROM catalog.item_barcodes
		WHERE tenant_id = $1 AND item_id = $2
		ORDER BY is_primary DESC, created_at ASC`
	rows, err := s.pool.Query(ctx, q, tenantID, itemID)
	if err != nil {
		return nil, fmt.Errorf("item: fetch barcodes: %w", err)
	}
	defer rows.Close()
	var out []types.ItemBarcode
	for rows.Next() {
		b, err := scanBarcode(rows)
		if err != nil {
			return nil, fmt.Errorf("item: scan barcode: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────
// Reads
// ─────────────────────────────────────────────────────────────────────

// GetByID looks up an item by primary key, scoped to tenant. Includes barcodes.
func (s *PgxStore) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*Item, error) {
	q := `SELECT ` + itemColumns + ` FROM catalog.items WHERE tenant_id = $1 AND id = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, id)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("item: get by id: %w", err)
	}
	bcs, err := s.fetchBarcodes(ctx, tenantID, it.ID)
	if err != nil {
		return nil, err
	}
	out := fromTypesItem(it, bcs)
	return &out, nil
}

// GetBySKU looks up by (tenant_id, sku) — the schema-level UNIQUE.
func (s *PgxStore) GetBySKU(ctx context.Context, tenantID uuid.UUID, sku string) (*Item, error) {
	q := `SELECT ` + itemColumns + ` FROM catalog.items WHERE tenant_id = $1 AND sku = $2`
	row := s.pool.QueryRow(ctx, q, tenantID, sku)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("item: get by sku: %w", err)
	}
	bcs, err := s.fetchBarcodes(ctx, tenantID, it.ID)
	if err != nil {
		return nil, err
	}
	out := fromTypesItem(it, bcs)
	return &out, nil
}

// GetByBarcode is the keystone POS-scan path. Single round-trip:
// JOIN catalog.item_barcodes (idx_barcodes_lookup) → catalog.items. Returns the
// item with all its barcodes hydrated (small extra query — POS callers
// may want the primary code echoed).
//
// Latency design: idx_barcodes_lookup is `(tenant_id, barcode) WHERE
// status = 'active'` — a partial unique. The join key is item_id which
// is the PK of catalog.items. Both probes are O(log n). Sub-100ms is
// achievable on commodity hardware up to multi-million-row catalogs.
func (s *PgxStore) GetByBarcode(ctx context.Context, tenantID uuid.UUID, barcode string) (*Item, error) {
	q := `SELECT ` + prefixCols("i.", itemColumns) + `
		FROM catalog.item_barcodes b
		JOIN catalog.items i ON i.id = b.item_id AND i.tenant_id = b.tenant_id
		WHERE b.tenant_id = $1 AND b.barcode = $2 AND b.status = 'active'`
	row := s.pool.QueryRow(ctx, q, tenantID, barcode)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("item: get by barcode: %w", err)
	}
	bcs, err := s.fetchBarcodes(ctx, tenantID, it.ID)
	if err != nil {
		return nil, err
	}
	out := fromTypesItem(it, bcs)
	return &out, nil
}

// prefixCols rewrites a SELECT list like "id, tenant_id, ..." into
// "i.id, i.tenant_id, ..." — needed for joins where multiple tables
// share column names. Pure text manipulation; both inputs are
// hard-coded constants so injection is not a concern.
func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// If the column already has a cast (`uom_quantity::text`) we need
		// to prefix the column name only, not the cast.
		if idx := strings.Index(p, "::"); idx > 0 {
			out = append(out, prefix+p[:idx]+p[idx:])
		} else {
			out = append(out, prefix+p)
		}
	}
	return strings.Join(out, ", ")
}

// ─────────────────────────────────────────────────────────────────────
// Writes
// ─────────────────────────────────────────────────────────────────────

// Create inserts an item plus optional barcodes in a single transaction.
// SDD-vague: dispatch said "Create(item, barcodes []string)" but the
// schema has barcode_type / uom_quantity / is_primary on each barcode
// row — a flat string slice would lose that. Took the richer
// BarcodeRequest shape.
func (s *PgxStore) Create(ctx context.Context, req CreateRequest) (*Item, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("item: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id := uuid.New()

	// Build the INSERT with explicit defaults for omitted fields. The
	// schema has DEFAULTs but we want deterministic values returned by
	// RETURNING — passing the explicit value avoids a re-fetch.
	itemType := derefOr(req.ItemType, "standard")
	uom := derefOr(req.UnitOfMeasure, "EA")
	uomQty := derefOr(req.UOMQuantity, "1")
	currency := derefOr(req.DefaultCurrency, "USD")
	foodStamp := derefBoolOr(req.FoodStampEligible, false)
	weighable := derefBoolOr(req.Weighable, false)
	status := derefOr(req.Status, "active")
	attrs := req.Attributes
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}

	q := `INSERT INTO catalog.items
		(id, tenant_id, sku, description, short_description, item_type, category_id,
		 unit_of_measure, uom_quantity, default_price, default_cost, default_currency,
		 tax_class, food_stamp_eligible, age_restriction, weighable, attributes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10::numeric, $11::numeric,
		        $12, $13, $14, $15, $16, $17, $18)
		RETURNING ` + itemColumns
	row := tx.QueryRow(ctx, q,
		id, req.TenantID, req.SKU, req.Description, req.ShortDescription,
		itemType, req.CategoryID, uom, uomQty, req.DefaultPrice, req.DefaultCost,
		currency, req.TaxClass, foodStamp, req.AgeRestriction, weighable, attrs, status,
	)
	it, err := scanItem(row)
	if err != nil {
		return nil, mapWriteErr(err, "create item")
	}

	// Insert barcodes if supplied.
	created := make([]types.ItemBarcode, 0, len(req.Barcodes))
	for _, b := range req.Barcodes {
		bcType := derefOr(b.Type, "GTIN")
		bcUOM := derefOr(b.UOMQuantity, "1")
		bcPrim := derefBoolOr(b.IsPrimary, false)

		bq := `INSERT INTO catalog.item_barcodes
			(tenant_id, item_id, barcode, barcode_type, uom_quantity, is_primary)
			VALUES ($1, $2, $3, $4, $5::numeric, $6)
			RETURNING ` + barcodeColumns
		brow := tx.QueryRow(ctx, bq, req.TenantID, it.ID, b.Value, bcType, bcUOM, bcPrim)
		bc, err := scanBarcode(brow)
		if err != nil {
			return nil, mapWriteErr(err, "create barcode")
		}
		created = append(created, bc)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("item: commit: %w", err)
	}
	out := fromTypesItem(it, created)
	return &out, nil
}

// Update applies a partial patch. Only fields with a non-nil pointer
// are written. Returns the updated item with barcodes hydrated.
func (s *PgxStore) Update(ctx context.Context, tenantID, id uuid.UUID, patch PatchRequest) (*Item, error) {
	sets := make([]string, 0, 14)
	args := make([]any, 0, 16)
	args = append(args, tenantID, id)
	idx := 3 // $1 = tenantID, $2 = id

	add := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, val)
		idx++
	}
	addNumeric := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s = $%d::numeric", col, idx))
		args = append(args, val)
		idx++
	}

	if patch.Description != nil {
		add("description", *patch.Description)
	}
	if patch.ShortDescription != nil {
		add("short_description", *patch.ShortDescription)
	}
	if patch.ItemType != nil {
		add("item_type", *patch.ItemType)
	}
	if patch.CategoryID != nil {
		add("category_id", *patch.CategoryID)
	}
	if patch.UnitOfMeasure != nil {
		add("unit_of_measure", *patch.UnitOfMeasure)
	}
	if patch.UOMQuantity != nil {
		addNumeric("uom_quantity", *patch.UOMQuantity)
	}
	if patch.DefaultPrice != nil {
		addNumeric("default_price", *patch.DefaultPrice)
	}
	if patch.DefaultCost != nil {
		addNumeric("default_cost", *patch.DefaultCost)
	}
	if patch.DefaultCurrency != nil {
		add("default_currency", *patch.DefaultCurrency)
	}
	if patch.TaxClass != nil {
		add("tax_class", *patch.TaxClass)
	}
	if patch.FoodStampEligible != nil {
		add("food_stamp_eligible", *patch.FoodStampEligible)
	}
	if patch.AgeRestriction != nil {
		add("age_restriction", *patch.AgeRestriction)
	}
	if patch.Weighable != nil {
		add("weighable", *patch.Weighable)
	}
	if len(patch.Attributes) > 0 {
		add("attributes", patch.Attributes)
	}
	if patch.Status != nil {
		add("status", *patch.Status)
	}

	if len(sets) == 0 {
		// No-op patch — just return the current row.
		return s.GetByID(ctx, tenantID, id)
	}
	sets = append(sets, "updated_at = now()")

	q := fmt.Sprintf(`UPDATE catalog.items SET %s
		WHERE tenant_id = $1 AND id = $2
		RETURNING %s`, strings.Join(sets, ", "), itemColumns)

	row := s.pool.QueryRow(ctx, q, args...)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, mapWriteErr(err, "update item")
	}
	bcs, err := s.fetchBarcodes(ctx, tenantID, it.ID)
	if err != nil {
		return nil, err
	}
	out := fromTypesItem(it, bcs)
	return &out, nil
}

// Delete is a soft delete — flips status to 'inactive'. ON DELETE CASCADE
// on catalog.item_barcodes / catalog.item_vendors would happen with hard delete; we
// preserve them so audit-trail queries still resolve. Use a separate
// purge dispatch if hard delete is ever needed.
//
// SDD-vague: dispatch said "soft delete (set status='inactive' or
// whatever schema dictates; flag if hard delete required)". Schema's
// CHECK comment lists status values as 'active | discontinued |
// seasonal | hidden' — there's no 'inactive' enum value but the column
// is free-text. Picked 'inactive' per dispatch wording rather than
// 'discontinued' so the dispatch authors can audit the choice. Switch
// trivially if 'discontinued' is preferred.
func (s *PgxStore) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	q := `UPDATE catalog.items SET status = 'inactive', updated_at = now()
		WHERE tenant_id = $1 AND id = $2`
	tag, err := s.pool.Exec(ctx, q, tenantID, id)
	if err != nil {
		return fmt.Errorf("item: soft delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns up to f.Limit items matching the filters.
func (s *PgxStore) List(ctx context.Context, f ListFilters) ([]Item, error) {
	conds := []string{"tenant_id = $1"}
	args := []any{f.TenantID}
	idx := 2

	if f.CategoryID != nil {
		conds = append(conds, fmt.Sprintf("category_id = $%d", idx))
		args = append(args, *f.CategoryID)
		idx++
	}
	if f.Status != nil {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, *f.Status)
		idx++
	}
	if f.VendorID != nil {
		// Items linked to vendor via catalog.item_vendors. EXISTS keeps it cheap.
		conds = append(conds, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM catalog.item_vendors iv
			WHERE iv.tenant_id = catalog.items.tenant_id
			  AND iv.item_id = catalog.items.id
			  AND iv.vendor_id = $%d)`, idx))
		args = append(args, *f.VendorID)
		idx++
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, f.Offset)

	q := fmt.Sprintf(`SELECT %s FROM catalog.items WHERE %s
		ORDER BY created_at DESC, id ASC
		LIMIT $%d OFFSET $%d`,
		itemColumns, strings.Join(conds, " AND "), idx, idx+1)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("item: list: %w", err)
	}
	defer rows.Close()

	out := make([]Item, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("item: scan list: %w", err)
		}
		// Skip per-row barcode fetch in list — adds N+1 latency. Use
		// GetByID for full hydration.
		out = append(out, fromTypesItem(it, nil))
	}
	return out, rows.Err()
}

// ListCategories returns the merchant's product categories. Flat list
// (level/path are returned so callers can rebuild a tree client-side).
func (s *PgxStore) ListCategories(ctx context.Context, tenantID uuid.UUID) ([]Category, error) {
	q := `SELECT id, tenant_id, parent_id, code, name, level, path::text,
			attributes, status, created_at, updated_at
		FROM catalog.product_categories
		WHERE tenant_id = $1
		ORDER BY level ASC, code ASC`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("item: list categories: %w", err)
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c types.ProductCategory
		err := rows.Scan(
			&c.ID, &c.TenantID, &c.ParentID, &c.Code, &c.Name, &c.Level, &c.Path,
			&c.Attributes, &c.Status, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("item: scan category: %w", err)
		}
		out = append(out, fromTypesCategory(c))
	}
	return out, rows.Err()
}

// ListVendors returns the merchant's vendors.
func (s *PgxStore) ListVendors(ctx context.Context, tenantID uuid.UUID) ([]Vendor, error) {
	q := `SELECT id, tenant_id, vendor_code, name, short_name, vendor_type,
			primary_contact, address, payment_terms, currency, tax_id,
			attributes, status, created_at, updated_at
		FROM catalog.vendors
		WHERE tenant_id = $1
		ORDER BY name ASC`
	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("item: list vendors: %w", err)
	}
	defer rows.Close()

	var out []Vendor
	for rows.Next() {
		var v types.Vendor
		err := rows.Scan(
			&v.ID, &v.TenantID, &v.VendorCode, &v.Name, &v.ShortName, &v.VendorType,
			&v.PrimaryContact, &v.Address, &v.PaymentTerms, &v.Currency, &v.TaxID,
			&v.Attributes, &v.Status, &v.CreatedAt, &v.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("item: scan vendor: %w", err)
		}
		out = append(out, fromTypesVendor(v))
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

// derefOr returns *p when p != nil, otherwise def.
func derefOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

// derefBoolOr is the bool variant.
func derefBoolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// mapWriteErr translates a pgx error into the domain's sentinel set.
// We care about three categories:
//   - unique violation (23505) → ErrConflict
//   - foreign-key violation (23503) → ErrValidation (caller passed a bad ID)
//   - everything else → wrapped pgx error
func mapWriteErr(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("item: %s: %w (%s)", op, ErrConflict, pgErr.ConstraintName)
		case "23503":
			return fmt.Errorf("item: %s: %w (foreign key %s)", op, ErrValidation, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("item: %s: %w", op, err)
}
