---
title: GRO-901 Item Setup Flow A - Scan-to-Lookup Design
date: 2026-05-11
status: draft
owners: product, design, engineering
related:
  - docs/superpowers/specs/2026-05-08-canary-go-unified-dispatch.md
  - docs/architecture/component-led-ui-vision.md
  - docs/decisions/ui-retail-vocabulary.md
  - docs/decisions/ui-status-taxonomy.md
  - docs/conventions/ui-pr-review-checklist.md
  - internal/catalog/barcodelookup/doc.go
  - internal/web/handler_items_create.go
---

# GRO-901 Item Setup Flow A - Scan-to-Lookup Design

## Decision

Build GRO-901 as a mobile-first, four-screen item-create flow:

1. Scan or enter barcode.
2. Review lookup result.
3. Add store-operational fields.
4. Confirm and create.

The flow saves no draft item records. The item is written only after
the final confirmation submit. State between screens is carried in a
short-lived signed flow token bound to the authenticated tenant.

## Context

Phase 5 Item Setup is unblocked by the component substrate and data
foundation. GRO-901 is the first UI flow in the phase and sits beside
the existing manual item form from GRO-886.

The barcode lookup adapter substrate already exists in
`internal/catalog/barcodelookup`. That package resolves the best
source result under the Flow A three-second overall timeout. GRO-901
should wire that resolver into the merchant web UI without changing
the catalog data model for drafts.

The design also incorporates the GRO-978 UI standards review:

- Canary merchant screens say Item, SKU, barcode/GTIN, Supplier, and
  Operator rather than generic platform nouns.
- SKU and barcode remain distinct identifiers even when the barcode is
  used as the initial SKU suggestion.
- Source/confidence/provenance metadata is shown as support detail, not
  as ordinary navigation.
- The flow remains useful without AtlasView at runtime.

## Goals

- Let a store-floor operator create a new item from a scanned barcode
  in under a minute when lookup data is good.
- Support phone camera scanning first, with hardware scanner and manual
  entry as equal fallbacks.
- Detect duplicate barcodes before external lookup work.
- Keep all writes tenant-scoped from request context. No HTML form may
  expose `tenant_id`.
- Reuse existing Go SSR templates, item store APIs, CSRF protection,
  audit middleware, and UI components.
- Add focused acceptance evidence that proves lookup, duplicate,
  fallback, validation, token, and create behavior.

## Non-goals

- No Phase 5 Flow B CSV import work.
- No Phase 5 Flow C enrichment, PLU generation, or label printing.
- No C4 variant matrix or persistent parent/child item relationship.
- No Phase 10 RBAC/PII-scope work.
- No React, SPA router, or new frontend runtime.
- No licensed GS1/GDSN implementation beyond showing adapter metadata
  when a configured source returns it.

## UX Flow

### Screen 1: Scan

Route: `GET /items/scan`  
Template: `internal/web/templates/items/scan.html`

Primary action is a camera viewfinder on mobile browsers that support
barcode detection. The same screen also has a visible barcode input so
USB/Bluetooth scanners and manual entry work without camera support.

The operator submits a barcode to `POST /items/scan/lookup`. That
handler trims scanner noise, rejects empty or unreasonably long input,
and never rewrites meaningful GTIN digits.

### Lookup Action

Route: `POST /items/scan/lookup`

The handler:

1. Reads tenant from `tenantIDFromCtx`.
2. Calls `ItemStore.GetByBarcode(ctx, tenantID, barcode)`.
3. If a match exists, renders a duplicate state with:
   - Open existing item.
   - Create related item, which continues to the manual item form with
     the duplicate barcode omitted. Persistent variant relationships
     remain deferred to C4.
4. If no match exists, calls the barcode lookup resolver with the
   existing three-second overall budget.
5. If no source knows the barcode, redirects to
   `/items/new?barcode=<value>&flash=barcode_not_found`.
6. If a source returns data, normalizes the fields into signed scan
   state and redirects to `GET /items/scan/review?flow=<token>`.

The same route should return JSON when the request asks for JSON so
camera-enhanced UI can avoid a full-page transition. The non-JS form
path remains canonical and fully usable.

### Screen 2: Review Lookup

Route: `GET /items/scan/review?flow=<token>`  
Template: `internal/web/templates/items/scan_review.html`

The review screen shows:

- Barcode/GTIN.
- Suggested item name.
- Brand, size, image URL, category suggestion, and source fields when
  available.
- Lookup source, confidence, partial-field warnings, and latency in a
  compact support/provenance area.

Fields are editable. The action posts to
`POST /items/scan/operational` with the updated signed flow token.

### Screen 3: Operational Fields

Route: `POST /items/scan/operational`  
Template: `internal/web/templates/items/scan_operational.html`

This screen captures store-specific data:

- SKU, defaulting to the barcode only as a suggestion.
- Supplier when available.
- Unit of measure.
- Unit cost.
- Selling price.
- Case pack.
- Status, defaulting to active for this scan-create path.
- Category, using the same active category dropdown convention as the
  manual item form.

The handler validates required fields before rendering the confirm
screen. Validation errors re-render the operational screen with the
operator's input intact.

### Screen 4: Confirm

Route: `POST /items/scan/confirm`  
Template: `internal/web/templates/items/scan_confirm.html`

`POST /items/scan/confirm` has two intents:

- `intent=preview`: render the confirm screen after operational
  validation.
- `intent=create`: revalidate the token and fields, perform a final
  duplicate-barcode check, create the item and barcode in one store
  call, then redirect to `/items/{id}?flash=created`.

The confirm screen clearly separates merchant-facing item fields from
lookup provenance so operators know what will become the item record
and what is support metadata.

## State Model

Use a compact signed flow token instead of a draft table. The token is
carried as `flow` in the review URL and as a hidden form field on POST
steps.

Token contents:

- Version.
- Tenant ID or tenant hash.
- Barcode.
- Lookup source.
- Lookup confidence.
- Partial fields.
- Normalized product fields.
- Operator-entered operational fields.
- Issued-at and expires-at timestamps.

Token rules:

- HMAC-SHA256 over base64url JSON is enough for this slice.
- Token expires quickly, with a target lifetime of 15 minutes.
- Every handler verifies signature, expiry, and tenant match before
  rendering or accepting POST data.
- Token failure restarts at `/items/scan?flash=scan_expired`.
- User-editable fields are still revalidated server-side. The token is
  continuity protection, not permission to trust form values blindly.

Implementation files for the token should be isolated:

- `internal/web/scanflow_token.go`
- `internal/web/scanflow_token_test.go`

## Handler and Dependency Shape

Add a small interface to `internal/web` rather than coupling handlers
directly to the concrete resolver:

```go
type BarcodeLookup interface {
    Lookup(ctx context.Context, barcode string) (barcodelookup.Result, error)
}
```

Extend `web.Deps` with:

- `BarcodeLookup BarcodeLookup`
- `ScanFlowSecret []byte`

Gateway wiring should construct the existing resolver from configured
sources and pass it into `web.Deps`. Tests can provide a stub lookup.

New handler file:

- `internal/web/handler_items_scan.go`

Route registration belongs beside the existing item routes in
`internal/web/handler.go`:

- `GET /items/scan`
- `POST /items/scan/lookup`
- `GET /items/scan/review`
- `POST /items/scan/operational`
- `POST /items/scan/confirm`

Template parsing adds:

- `items_scan`
- `items_scan_review`
- `items_scan_operational`
- `items_scan_confirm`

## Persistence

Final creation uses `ItemStore.Create(ctx, item.CreateRequest)` and
does not introduce raw SQL in the web handler.

Create request mapping:

- `TenantID`: from context only.
- `SKU`: operator field; barcode may prefill but remains editable.
- `Description`: reviewed item name.
- `CategoryID`: selected category.
- `UnitOfMeasure`: selected or defaulted value.
- `DefaultCost`: unit cost.
- `DefaultPrice`: selling price.
- `Status`: active by default for scan-created items.
- `Barcodes`: one scanned barcode entry.
- `Attributes`: lookup metadata, including source, confidence, partial
  fields, latency, and original source field keys.

Before final create, run `GetByBarcode` again to handle races. Map
`item.ErrConflict` to a duplicate state rather than a generic error.

Audit behavior follows the existing protected-route middleware. If an
audit action-tag helper exists by implementation time, use
`item.create.scan`; otherwise document that per-action scan labeling is
a follow-on, matching the manual item form's current convention.

## UI Standards

Use existing component-led patterns where available:

- `form-field` for text/select/money inputs.
- `status-pill` for confidence, duplicate, not-found, and saved states.
- Compact cards for each screen's main workflow area.
- Accessible text labels for every status, not color-only badges.

Copy rules:

- Use Item, SKU, barcode/GTIN, Supplier, Operator, and Source.
- Do not call barcode "product id" or collapse it into SKU.
- Source/confidence details are support metadata, not headline copy.
- No visible tenant or internal source-system IDs in normal fields.

The list page may add a `Scan` action next to `New item`, but it must
remain a practical operator action, not a marketing hero.

## Error Handling

- Missing barcode: re-render scan with `missing_barcode`.
- Duplicate barcode: render duplicate state with existing item link and
  related-item/manual fallback.
- Barcode not found: redirect to manual form with barcode preserved.
- All lookup sources failed: render retry/manual fallback; do not block
  manual creation.
- Token expired/tampered: restart scan flow with clear expired message.
- Missing required operational fields: re-render operational screen with
  operator input intact.
- Final duplicate race: render duplicate state and do not create.
- Store unavailable: match existing item form behavior and surface
  `no_store`.

## Acceptance Evidence

Add `internal/web/handler_items_scan_test.go` with at least these
covered cases:

1. `GET /items/scan` renders the scan form and exposes no `tenant_id`
   input.
2. Duplicate barcode lookup short-circuits before external lookup and
   shows Open existing / Create related item choices.
3. Successful lookup creates a signed flow state and reaches review.
4. Barcode not found redirects to the manual item form with the barcode
   preserved.
5. All lookup sources failed renders retry/manual fallback.
6. Review rejects tampered flow tokens.
7. Review rejects expired flow tokens.
8. Operational validation preserves input and reports missing required
   fields.
9. Confirm create writes exactly one item and one barcode with tenant
   from context.
10. Final duplicate race maps to duplicate UI, not a generic 500.

Add `internal/web/scanflow_token_test.go` for encode/decode, tenant
binding, expiry, tamper, and version handling.

Run and capture:

- `go test ./internal/web -run 'TestItemScan|TestScanFlow'`
- `go test ./internal/catalog/barcodelookup/...`
- `go test ./internal/web ./internal/catalog/barcodelookup/...`
- `go test ./...`
- `go vet ./...`

If full-suite failures pre-exist on `main`, record the exact failing
tests and also show the targeted GRO-901 evidence above.

## Implementation Lanes

Future implementation can be split into disjoint ownership:

- Lane A: token state, `web.Deps`, resolver wiring, and route parsing.
- Lane B: scan handlers, validation, duplicate and fallback behavior.
- Lane C: four item templates and list-page scan entry point.
- Lane D: handler/token tests and acceptance evidence.

Each lane owns only its files and reports changed files. Do not start
Phase 5 Flow B, Phase 5 Flow C completion, or Phase 10 RBAC as part of
GRO-901.

## Open Follow-ups

- Persistent variant relationships stay deferred with C4. GRO-901 can
  offer a "Create related item" escape hatch but must not invent a
  hidden variant schema.
- If product decides lookup provenance needs an operator-facing audit
  action name immediately, add a small audit-tag helper before wiring
  `item.create.scan`.
- Native mobile scanner integration can replace or enhance the browser
  camera path later; this design keeps manual and hardware scanner
  paths first-class.
