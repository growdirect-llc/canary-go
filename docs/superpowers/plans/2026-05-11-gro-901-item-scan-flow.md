# GRO-901 Item Scan Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the GRO-901 mobile-first scan-to-lookup item-create flow with final-save-only persistence.

**Architecture:** Add a tenant-bound signed scan-flow token, wire the existing barcode lookup resolver into the web dependencies, and implement the four Go SSR screens under `/items/scan`. The handlers check the local catalog first, carry state without draft rows, validate operator fields, and create the final item plus barcode only on confirm.

**Tech Stack:** Go 1.22+, Chi v5, existing `internal/web` templates/components, `internal/item.Store`, `internal/catalog/barcodelookup`, gorilla/csrf, HMAC-SHA256.

---

## Source Design

Implement from:

- `docs/superpowers/specs/2026-05-11-gro-901-item-scan-flow-design.md`
- `docs/decisions/ui-retail-vocabulary.md`
- `docs/decisions/ui-status-taxonomy.md`
- `docs/conventions/ui-pr-review-checklist.md`

Do not start Phase 5 Flow B, Phase 5 Flow C completion, or Phase 10 RBAC.

## File Structure

### Lane A - State, Dependency, Gateway Wiring

Owned files:

- Create `internal/web/scanflow_token.go`
- Create `internal/web/scanflow_token_test.go`
- Create `internal/web/barcode_lookup.go`
- Modify `internal/web/deps.go`
- Modify `cmd/gateway/main.go`

Responsibilities:

- Encode/decode signed scan state.
- Bind flow state to tenant.
- Add `BarcodeLookup` and `ScanFlowSecret` dependencies.
- Wire `ItemStore`, barcode resolver, and scan-flow secret in gateway.

### Lane B - Scan Handlers and Handler Tests

Owned files:

- Create `internal/web/handler_items_scan.go`
- Create `internal/web/handler_items_scan_test.go`
- Modify `internal/web/handler.go`

Responsibilities:

- Register scan routes and parse templates.
- Implement scan, lookup, review, operational, and confirm handlers.
- Prove duplicate, fallback, token, validation, and create behavior.

### Lane C - Templates and List Entry Point

Owned files:

- Create `internal/web/templates/items/scan.html`
- Create `internal/web/templates/items/scan_review.html`
- Create `internal/web/templates/items/scan_operational.html`
- Create `internal/web/templates/items/scan_confirm.html`
- Modify `internal/web/templates/items/list.html`

Responsibilities:

- Render the four-screen operator flow.
- Use existing component conventions and accessible status text.
- Add `Scan` beside `New item` on the item list.

### Lane D - Evidence and Closeout

Owned files:

- Modify only documentation or Linear comments if the operator asks for them.

Responsibilities:

- Run the named acceptance checks.
- Record failures that pre-exist on `main`.
- Report changed files and evidence.

## Sequencing

Run Task 1 first. Tasks 2 and 3 may run after Task 1. Tasks 4, 5, and 6 must run in order because each builds on handler state. Task 7 runs after behavior is green. Task 8 closes the ticket.

When using a swarm, keep the ownership above disjoint. A worker may read any file, but must only edit files owned by its lane.

## Task 1: Signed Scan-Flow Token

**Files:**

- Create: `internal/web/scanflow_token.go`
- Create: `internal/web/scanflow_token_test.go`

- [ ] **Step 1: Write failing token tests**

Create `internal/web/scanflow_token_test.go`:

```go
package web

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestScanFlowToken_RoundTrip(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	state := scanFlowState{
		Barcode:       "012345678905",
		Source:        "Open Food Facts",
		Confidence:    0.85,
		PartialFields: []string{"brand"},
		Product: scanProductFields{
			Name:     "Organic Whole Milk",
			Brand:    "Clover",
			ImageURL: "https://example.test/milk.png",
		},
		Operational: scanOperationalFields{
			SKU:           "012345678905",
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}

	token, err := codec.Encode(tenantID, state)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if token == "" || strings.Count(token, ".") != 1 {
		t.Fatalf("token shape = %q, want payload.signature", token)
	}

	got, err := codec.Decode(token, tenantID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Version != scanFlowTokenVersion {
		t.Errorf("Version = %d, want %d", got.Version, scanFlowTokenVersion)
	}
	if got.Barcode != "012345678905" {
		t.Errorf("Barcode = %q", got.Barcode)
	}
	if got.Product.Name != "Organic Whole Milk" {
		t.Errorf("Product.Name = %q", got.Product.Name)
	}
	if got.ExpiresAt <= got.IssuedAt {
		t.Errorf("ExpiresAt = %d must be greater than IssuedAt = %d", got.ExpiresAt, got.IssuedAt)
	}
}

func TestScanFlowToken_RejectsWrongTenant(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	token, err := codec.Encode(uuid.MustParse("00000000-0000-0000-0000-000000000001"), scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, err = codec.Decode(token, uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}

func TestScanFlowToken_RejectsExpired(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	codec.ttl = time.Minute

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	codec.now = func() time.Time { return time.Unix(1062, 0).UTC() }
	_, err = codec.Decode(token, tenantID)
	if !errors.Is(err, errScanFlowExpired) {
		t.Fatalf("Decode err = %v, want errScanFlowExpired", err)
	}
}

func TestScanFlowToken_RejectsTamper(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	parts := strings.Split(token, ".")
	tampered := parts[0] + ".AAAA" + parts[1]
	_, err = codec.Decode(tampered, tenantID)
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}

func TestScanFlowToken_RejectsUnsupportedVersion(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Version: 99, Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, err = codec.Decode(token, tenantID)
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}
```

- [ ] **Step 2: Run token tests and verify failure**

Run:

```bash
go test ./internal/web -run TestScanFlowToken
```

Expected: fail to compile because `newScanFlowTokenCodec`, `scanFlowState`, and token errors are undefined.

- [ ] **Step 3: Implement token codec**

Create `internal/web/scanflow_token.go`:

```go
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	scanFlowTokenVersion = 1
	defaultScanFlowTTL   = 15 * time.Minute
)

var (
	errScanFlowInvalid = errors.New("scan flow token invalid")
	errScanFlowExpired = errors.New("scan flow token expired")
)

type scanFlowTokenCodec struct {
	secret []byte
	now    func() time.Time
	ttl    time.Duration
}

type scanFlowState struct {
	Version       int                   `json:"v"`
	TenantID      string                `json:"tenant_id"`
	Barcode       string                `json:"barcode"`
	Source        string                `json:"source,omitempty"`
	Confidence    float64               `json:"confidence,omitempty"`
	PartialFields []string              `json:"partial_fields,omitempty"`
	Product       scanProductFields     `json:"product"`
	Operational   scanOperationalFields `json:"operational"`
	IssuedAt      int64                 `json:"iat"`
	ExpiresAt     int64                 `json:"exp"`
}

type scanProductFields struct {
	Name               string `json:"name,omitempty"`
	Brand              string `json:"brand,omitempty"`
	Size               string `json:"size,omitempty"`
	ImageURL           string `json:"image_url,omitempty"`
	CategorySuggestion string `json:"category_suggestion,omitempty"`
}

type scanOperationalFields struct {
	SKU           string `json:"sku,omitempty"`
	CategoryID    string `json:"category_id,omitempty"`
	VendorID      string `json:"vendor_id,omitempty"`
	UnitOfMeasure string `json:"unit_of_measure,omitempty"`
	UnitCost      string `json:"unit_cost,omitempty"`
	SellingPrice  string `json:"selling_price,omitempty"`
	CasePack      string `json:"case_pack,omitempty"`
	Status        string `json:"status,omitempty"`
}

func newScanFlowTokenCodec(secret []byte) *scanFlowTokenCodec {
	return &scanFlowTokenCodec{
		secret: append([]byte(nil), secret...),
		now:    func() time.Time { return time.Now().UTC() },
		ttl:    defaultScanFlowTTL,
	}
}

func (c *scanFlowTokenCodec) Encode(tenantID uuid.UUID, state scanFlowState) (string, error) {
	if len(c.secret) < 32 {
		return "", fmt.Errorf("%w: short secret", errScanFlowInvalid)
	}
	now := c.now().UTC()
	if state.Version == 0 {
		state.Version = scanFlowTokenVersion
	}
	state.TenantID = tenantID.String()
	state.IssuedAt = now.Unix()
	state.ExpiresAt = now.Add(c.ttl).Unix()

	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("scan flow token marshal: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := c.sign(encodedPayload)
	return encodedPayload + "." + signature, nil
}

func (c *scanFlowTokenCodec) Decode(token string, tenantID uuid.UUID) (scanFlowState, error) {
	if len(c.secret) < 32 {
		return scanFlowState{}, fmt.Errorf("%w: short secret", errScanFlowInvalid)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return scanFlowState{}, errScanFlowInvalid
	}
	expected := c.sign(parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return scanFlowState{}, errScanFlowInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return scanFlowState{}, fmt.Errorf("%w: payload decode", errScanFlowInvalid)
	}
	var state scanFlowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return scanFlowState{}, fmt.Errorf("%w: payload json", errScanFlowInvalid)
	}
	if state.Version != scanFlowTokenVersion {
		return scanFlowState{}, errScanFlowInvalid
	}
	if state.TenantID != tenantID.String() {
		return scanFlowState{}, errScanFlowInvalid
	}
	if state.ExpiresAt < c.now().UTC().Unix() {
		return scanFlowState{}, errScanFlowExpired
	}
	return state, nil
}

func (c *scanFlowTokenCodec) sign(encodedPayload string) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(encodedPayload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
```

- [ ] **Step 4: Run token tests and verify pass**

Run:

```bash
go test ./internal/web -run TestScanFlowToken
```

Expected: pass.

- [ ] **Step 5: Commit Task 1**

Run:

```bash
git add internal/web/scanflow_token.go internal/web/scanflow_token_test.go
git commit -m "feat: add item scan flow token"
```

## Task 2: Web Dependencies and Gateway Wiring

**Files:**

- Create: `internal/web/barcode_lookup.go`
- Modify: `internal/web/deps.go`
- Modify: `cmd/gateway/main.go`

- [ ] **Step 1: Add web barcode lookup interface**

Create `internal/web/barcode_lookup.go`:

```go
package web

import (
	"context"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
)

type BarcodeLookup interface {
	Lookup(ctx context.Context, barcode string) (barcodelookup.Result, error)
}
```

- [ ] **Step 2: Extend web dependencies**

In `internal/web/deps.go`, add the fields near `ItemStore`:

```go
	ItemStore     item.Store    // interface
	BarcodeLookup BarcodeLookup // interface
	ScanFlowSecret []byte
	PricingStore  pricing.Store // interface
```

Keep `gofmt` alignment authoritative; do not rearrange unrelated dependencies.

- [ ] **Step 3: Wire gateway imports**

In `cmd/gateway/main.go`, add imports:

```go
	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
	itemPkg "github.com/ruptiv/canary/internal/item"
```

- [ ] **Step 4: Wire ItemStore, BarcodeLookup, and ScanFlowSecret**

In `cmd/gateway/main.go`, before `webDeps := web.Deps{...}`, add:

```go
	barcodeLookup := barcodelookup.NewResolver(
		[]barcodelookup.Source{
			barcodelookup.NewOpenFoodFacts(http.DefaultClient),
			barcodelookup.NewUPCItemDB(http.DefaultClient),
		},
		barcodelookup.WithLogger(logger),
	)
	scanFlowSecret := buildScanFlowSecret(logger)
```

Inside `webDeps`, add:

```go
		ItemStore:        itemPkg.NewPgxStore(pool),
		BarcodeLookup:    barcodeLookup,
		ScanFlowSecret:   scanFlowSecret,
```

- [ ] **Step 5: Add scan-flow secret builder**

In `cmd/gateway/main.go`, place this near `buildCSRFKey`:

```go
func buildScanFlowSecret(logger *zap.Logger) []byte {
	isProd := os.Getenv("ENV") == "production"
	key := make([]byte, 32)
	keyHex := os.Getenv("SCAN_FLOW_SECRET")
	if keyHex != "" {
		decoded, err := hex.DecodeString(keyHex)
		if err != nil || len(decoded) != 32 {
			if isProd {
				logger.Fatal("SCAN_FLOW_SECRET invalid in production; must be 64-char hex (32 bytes)",
					zap.Error(err))
			}
			logger.Warn("SCAN_FLOW_SECRET invalid; generating random key (dev fallback)")
		} else {
			copy(key, decoded)
			return key
		}
	}
	if isProd {
		logger.Fatal("SCAN_FLOW_SECRET required in production (ENV=production); set to a 64-char hex string")
	}
	_, _ = cryptoRand.Read(key)
	logger.Warn("SCAN_FLOW_SECRET not set; using ephemeral random key (dev only)")
	return key
}
```

- [ ] **Step 6: Run compile check**

Run:

```bash
gofmt -w internal/web/barcode_lookup.go internal/web/deps.go cmd/gateway/main.go
go test ./internal/web -run TestScanFlowToken
go test ./cmd/gateway
```

Expected: pass.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add internal/web/barcode_lookup.go internal/web/deps.go cmd/gateway/main.go
git commit -m "feat: wire item scan dependencies"
```

## Task 3: Route Skeleton, Scan Page, and List Entry

**Files:**

- Create: `internal/web/handler_items_scan.go`
- Create: `internal/web/handler_items_scan_test.go`
- Create: `internal/web/templates/items/scan.html`
- Modify: `internal/web/templates/items/list.html`
- Modify: `internal/web/handler.go`

- [ ] **Step 1: Write route/render tests**

Create `internal/web/handler_items_scan_test.go` with the shared stubs:

```go
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
	"github.com/ruptiv/canary/internal/item"
)

type scanItemStoreStub struct {
	getByBarcodeFn   func(ctx context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error)
	createFn         func(ctx context.Context, req item.CreateRequest) (*item.Item, error)
	listCategoriesFn func(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error)
	listVendorsFn    func(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error)
}

func (s *scanItemStoreStub) GetByID(context.Context, uuid.UUID, uuid.UUID) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) GetBySKU(context.Context, uuid.UUID, string) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) GetByBarcode(ctx context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error) {
	if s.getByBarcodeFn != nil {
		return s.getByBarcodeFn(ctx, tenantID, barcode)
	}
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) Create(ctx context.Context, req item.CreateRequest) (*item.Item, error) {
	if s.createFn != nil {
		return s.createFn(ctx, req)
	}
	return &item.Item{ID: uuid.New(), TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
}
func (s *scanItemStoreStub) Update(context.Context, uuid.UUID, uuid.UUID, item.PatchRequest) (*item.Item, error) {
	return nil, item.ErrNotFound
}
func (s *scanItemStoreStub) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *scanItemStoreStub) List(context.Context, item.ListFilters) ([]item.Item, error) {
	return nil, nil
}
func (s *scanItemStoreStub) ListCategories(ctx context.Context, tenantID uuid.UUID) ([]item.Category, error) {
	if s.listCategoriesFn != nil {
		return s.listCategoriesFn(ctx, tenantID)
	}
	return nil, nil
}
func (s *scanItemStoreStub) AggregateByCategory(context.Context, uuid.UUID) ([]item.CategoryAggregate, error) {
	return nil, nil
}
func (s *scanItemStoreStub) ListVendors(ctx context.Context, tenantID uuid.UUID) ([]item.Vendor, error) {
	if s.listVendorsFn != nil {
		return s.listVendorsFn(ctx, tenantID)
	}
	return nil, nil
}

var _ item.Store = (*scanItemStoreStub)(nil)

type scanLookupStub struct {
	called bool
	result barcodelookup.Result
	err    error
}

func (s *scanLookupStub) Lookup(context.Context, string) (barcodelookup.Result, error) {
	s.called = true
	return s.result, s.err
}

func newItemScanRouter(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	if len(deps.ScanFlowSecret) == 0 {
		deps.ScanFlowSecret = []byte("01234567890123456789012345678901")
	}
	deps = withTestAuth(deps)
	h := New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func postScanForm(r http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestItemScan_RendersScanForm_NoTenantField(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Scan item", `action="/items/scan/lookup"`, `name="barcode"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "tenant_id") {
		t.Error("scan form must not expose tenant_id")
	}
}

func TestItemList_HasScanButton(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/items/scan"`) {
		t.Error("items list missing scan link")
	}
	if !strings.Contains(body, "Scan") {
		t.Error("items list missing Scan button text")
	}
}

func fixedScanState() scanFlowState {
	return scanFlowState{
		Barcode:    "012345678905",
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Product: scanProductFields{
			Name:  "Organic Whole Milk",
			Brand: "Clover",
		},
		Operational: scanOperationalFields{
			SKU:           "012345678905",
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}
}

func fixedScanToken(t *testing.T, state scanFlowState) string {
	t.Helper()
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	token, err := codec.Encode(testTenant, state)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return token
}
```

- [ ] **Step 2: Run render tests and verify failure**

Run:

```bash
go test ./internal/web -run 'TestItemScan_RendersScanForm_NoTenantField|TestItemList_HasScanButton'
```

Expected: fail because `/items/scan` and the list link do not exist.

- [ ] **Step 3: Add template parsing and routes**

In `internal/web/handler.go`, add after existing item template parsing:

```go
	h.mustParse("items_scan", "templates/items/scan.html")
	h.mustParse("items_scan_review", "templates/items/scan_review.html")
	h.mustParse("items_scan_operational", "templates/items/scan_operational.html")
	h.mustParse("items_scan_confirm", "templates/items/scan_confirm.html")
```

In the item route group, register scan routes before `/items/{id}`:

```go
		r.Get("/items/scan", h.itemScanPage)
		r.Post("/items/scan/lookup", h.itemScanLookupAction)
		r.Get("/items/scan/review", h.itemScanReviewPage)
		r.Post("/items/scan/operational", h.itemScanOperationalAction)
		r.Post("/items/scan/confirm", h.itemScanConfirmAction)
```

- [ ] **Step 4: Add handler skeleton**

Create `internal/web/handler_items_scan.go`:

```go
package web

import (
	"net/http"
)

func (h *Handler) itemScanPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "items_scan", "items", map[string]any{
		"Flash": r.URL.Query().Get("flash"),
		"Form": map[string]string{
			"barcode": r.URL.Query().Get("barcode"),
		},
	})
}

func (h *Handler) itemScanLookupAction(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=no_store", http.StatusSeeOther)
}

func (h *Handler) itemScanReviewPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
}

func (h *Handler) itemScanOperationalAction(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
}

func (h *Handler) itemScanConfirmAction(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
}
```

- [ ] **Step 5: Add initial scan template**

Create `internal/web/templates/items/scan.html`:

```html
{{template "base.html" .}}

{{define "title"}}Scan item - Canary{{end}}

{{define "content"}}
{{with .Data}}
{{$csrf := $.CSRFField}}
{{$form := .Form}}
<div class="page-header">
  <h1>Scan item</h1>
  <p class="page-subtitle">Scan a barcode or enter it manually.</p>
</div>

{{if .Flash}}
<div class="ops-card" style="margin-bottom:16px;border-left:3px solid var(--signal-yellow);">
  <div style="font-size:13px;color:var(--text-primary);">
    {{if eq .Flash "missing_barcode"}}Barcode is required.
    {{else if eq .Flash "barcode_not_found"}}Barcode was not found. Continue with manual item entry.
    {{else if eq .Flash "lookup_failed"}}Lookup is unavailable. You can retry or use manual entry.
    {{else if eq .Flash "scan_expired"}}Scan session expired. Scan the item again.
    {{else if eq .Flash "no_store"}}Item store not wired in this environment.
    {{else}}Something went wrong: {{.Flash}}
    {{end}}
  </div>
</div>
{{end}}

<form method="POST" action="/items/scan/lookup" class="ops-card" style="max-width:640px;">
  {{$csrf}}
  <label style="display:flex;flex-direction:column;gap:6px;font-size:13px;color:var(--text-primary);">
    Barcode
    <input
      type="text"
      name="barcode"
      value="{{index $form "barcode"}}"
      inputmode="numeric"
      autocomplete="off"
      autofocus
      maxlength="64"
      placeholder="Scan or type barcode"
      style="padding:10px 12px;background:var(--bg-surface);border:1px solid var(--border-subtle);border-radius:6px;color:var(--text-primary);font-size:16px;outline:none;font-family:'Space Grotesk',monospace;"
    />
  </label>
  <div style="display:flex;gap:12px;align-items:center;margin-top:20px;padding-top:18px;border-top:1px solid var(--border-subtle);">
    <button type="submit" style="padding:10px 20px;background:var(--signal-yellow);color:#000;border:none;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer;">
      Look up barcode
    </button>
    <a href="/items/new" style="padding:10px 16px;color:var(--text-muted);text-decoration:none;font-size:13px;">Manual entry</a>
  </div>
</form>
{{end}}
{{end}}
```

Create minimal empty templates so parsing succeeds; later tasks fill them:

`internal/web/templates/items/scan_review.html`

```html
{{template "base.html" .}}
{{define "title"}}Review item - Canary{{end}}
{{define "content"}}<div class="page-header"><h1>Review item</h1></div>{{end}}
```

`internal/web/templates/items/scan_operational.html`

```html
{{template "base.html" .}}
{{define "title"}}Item fields - Canary{{end}}
{{define "content"}}<div class="page-header"><h1>Item fields</h1></div>{{end}}
```

`internal/web/templates/items/scan_confirm.html`

```html
{{template "base.html" .}}
{{define "title"}}Confirm item - Canary{{end}}
{{define "content"}}<div class="page-header"><h1>Confirm item</h1></div>{{end}}
```

- [ ] **Step 6: Add Scan action to item list**

In `internal/web/templates/items/list.html`, replace the single `+ New item` link with:

```html
  <div style="display:flex;gap:8px;align-items:center;align-self:center;">
    <a href="/items/scan" style="padding:8px 16px;background:var(--bg-surface);color:var(--text-primary);border:1px solid var(--border-subtle);border-radius:6px;font-size:13px;font-weight:600;text-decoration:none;">Scan</a>
    <a href="/items/new" style="padding:8px 16px;background:var(--signal-yellow);color:#000;border-radius:6px;font-size:13px;font-weight:600;text-decoration:none;">+ New item</a>
  </div>
```

- [ ] **Step 7: Run render tests and verify pass**

Run:

```bash
gofmt -w internal/web/handler_items_scan.go internal/web/handler.go internal/web/handler_items_scan_test.go
go test ./internal/web -run 'TestItemScan_RendersScanForm_NoTenantField|TestItemList_HasScanButton'
```

Expected: pass.

- [ ] **Step 8: Commit Task 3**

Run:

```bash
git add internal/web/handler.go internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go internal/web/templates/items/scan.html internal/web/templates/items/scan_review.html internal/web/templates/items/scan_operational.html internal/web/templates/items/scan_confirm.html internal/web/templates/items/list.html
git commit -m "feat: add item scan route shell"
```

## Task 4: Lookup, Duplicate, Fallback, and Review

**Files:**

- Modify: `internal/web/handler_items_scan.go`
- Modify: `internal/web/handler_items_scan_test.go`
- Modify: `internal/web/templates/items/scan.html`
- Modify: `internal/web/templates/items/scan_review.html`

- [ ] **Step 1: Add lookup tests**

Append to `internal/web/handler_items_scan_test.go`:

```go
func TestItemScanLookup_DuplicateShortCircuitsExternalLookup(t *testing.T) {
	existingID := uuid.New()
	lookup := &scanLookupStub{}
	store := &scanItemStoreStub{
		getByBarcodeFn: func(_ context.Context, tenantID uuid.UUID, barcode string) (*item.Item, error) {
			if tenantID != testTenant {
				t.Errorf("tenantID = %s, want %s", tenantID, testTenant)
			}
			if barcode != "012345678905" {
				t.Errorf("barcode = %q", barcode)
			}
			return &item.Item{ID: existingID, SKU: "MILK-1", Description: "Milk"}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store, BarcodeLookup: lookup})

	form := url.Values{"barcode": {" 012345678905 "}}
	rec := postScanForm(r, "/items/scan/lookup", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if lookup.called {
		t.Fatal("external lookup must not run when local duplicate exists")
	}
	body := rec.Body.String()
	for _, want := range []string{"Barcode already exists", "/items/" + existingID.String(), "Create related item"} {
		if !strings.Contains(body, want) {
			t.Errorf("duplicate body missing %q", want)
		}
	}
}

func TestItemScanLookup_NotFoundRedirectsToManualForm(t *testing.T) {
	lookup := &scanLookupStub{err: barcodelookup.ErrBarcodeNotFound}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	for _, want := range []string{"/items/new?", "barcode=012345678905", "flash=barcode_not_found"} {
		if !strings.Contains(loc, want) {
			t.Errorf("redirect %q missing %q", loc, want)
		}
	}
}

func TestItemScanLookup_AllSourcesFailedRendersRetry(t *testing.T) {
	lookup := &scanLookupStub{err: barcodelookup.ErrAllSourcesFailed}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Lookup is unavailable", "Manual entry"} {
		if !strings.Contains(body, want) {
			t.Errorf("failure body missing %q", want)
		}
	}
}

func TestItemScanLookup_FoundRedirectsToReview(t *testing.T) {
	lookup := &scanLookupStub{result: barcodelookup.Result{
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Fields: map[string]any{
			"name":      "Organic Whole Milk",
			"brand":     "Clover",
			"image_url": "https://example.test/milk.png",
		},
		PartialFields: []string{"size"},
	}}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	rec := postScanForm(r, "/items/scan/lookup", url.Values{"barcode": {"012345678905"}})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/items/scan/review?flow=") {
		t.Fatalf("Location = %q, want review flow", loc)
	}
}

func TestItemScanLookup_JSONFoundReturnsReviewURL(t *testing.T) {
	lookup := &scanLookupStub{result: barcodelookup.Result{
		Source:     "Open Food Facts",
		Confidence: 0.85,
		Fields: map[string]any{
			"name":  "Organic Whole Milk",
			"brand": "Clover",
		},
	}}
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}, BarcodeLookup: lookup})

	form := url.Values{"barcode": {"012345678905"}}
	req := httptest.NewRequest(http.MethodPost, "/items/scan/lookup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	body := rec.Body.String()
	for _, want := range []string{`"status":"found"`, `"review_url":"/items/scan/review?flow=`} {
		if !strings.Contains(body, want) {
			t.Errorf("JSON body missing %q: %s", want, body)
		}
	}
}

func TestItemScanReview_RendersLookupFields(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	req := httptest.NewRequest(http.MethodGet, "/items/scan/review?flow="+url.QueryEscape(token), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Review item", "Organic Whole Milk", "Clover", "Open Food Facts", `name="name"`} {
		if !strings.Contains(body, want) {
			t.Errorf("review body missing %q", want)
		}
	}
	if strings.Contains(body, "tenant_id") {
		t.Error("review form must not expose tenant_id")
	}
}
```

- [ ] **Step 2: Run lookup tests and verify failure**

Run:

```bash
go test ./internal/web -run 'TestItemScanLookup|TestItemScanReview'
```

Expected: fail because lookup behavior and review rendering are not implemented.

- [ ] **Step 3: Implement lookup helpers and action**

Replace `internal/web/handler_items_scan.go` with:

```go
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/catalog/barcodelookup"
	"github.com/ruptiv/canary/internal/item"
)

func (h *Handler) itemScanPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "items_scan", "items", map[string]any{
		"Flash": r.URL.Query().Get("flash"),
		"Form": map[string]string{
			"barcode": r.URL.Query().Get("barcode"),
		},
	})
}

func (h *Handler) itemScanLookupAction(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no_store"})
			return
		}
		http.Redirect(w, r, "/items/scan?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusBadRequest, map[string]string{"status": "invalid_form"})
			return
		}
		http.Redirect(w, r, "/items/scan?flash=invalid_form", http.StatusSeeOther)
		return
	}
	barcode := normalizeScanBarcode(r.PostFormValue("barcode"))
	if barcode == "" {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusBadRequest, map[string]string{"status": "missing_barcode"})
			return
		}
		http.Redirect(w, r, "/items/scan?flash=missing_barcode", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	existing, err := h.deps.ItemStore.GetByBarcode(ctx, tenantID, barcode)
	if err == nil && existing != nil {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusOK, map[string]string{
				"status":   "duplicate",
				"item_url": "/items/" + existing.ID.String(),
			})
			return
		}
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "duplicate_barcode",
			"Form":  map[string]string{"barcode": barcode},
			"Duplicate": map[string]string{
				"ID":          existing.ID.String(),
				"SKU":         existing.SKU,
				"Description": existing.Description,
			},
		})
		return
	}
	if err != nil && !errors.Is(err, item.ErrNotFound) {
		h.logger.Error("itemScanLookupAction: GetByBarcode", zap.Error(err))
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusBadGateway, map[string]string{"status": "lookup_failed"})
			return
		}
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "lookup_failed",
			"Form":  map[string]string{"barcode": barcode},
		})
		return
	}
	if h.deps.BarcodeLookup == nil {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "lookup_unavailable"})
			return
		}
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "lookup_failed",
			"Form":  map[string]string{"barcode": barcode},
		})
		return
	}

	result, err := h.deps.BarcodeLookup.Lookup(ctx, barcode)
	if errors.Is(err, barcodelookup.ErrBarcodeNotFound) {
		q := url.Values{}
		q.Set("barcode", barcode)
		q.Set("flash", "barcode_not_found")
		manualURL := "/items/new?" + q.Encode()
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusNotFound, map[string]string{
				"status":     "not_found",
				"manual_url": manualURL,
			})
			return
		}
		http.Redirect(w, r, manualURL, http.StatusSeeOther)
		return
	}
	if err != nil {
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusBadGateway, map[string]string{
				"status": "lookup_failed",
			})
			return
		}
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "lookup_failed",
			"Form":  map[string]string{"barcode": barcode},
		})
		return
	}

	state := scanFlowState{
		Barcode:       barcode,
		Source:        result.Source,
		Confidence:    result.Confidence,
		PartialFields: result.PartialFields,
		Product:       productFieldsFromLookup(result.Fields),
		Operational: scanOperationalFields{
			SKU:           barcode,
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}
	token, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantID, state)
	if err != nil {
		h.logger.Error("itemScanLookupAction: encode flow", zap.Error(err))
		if wantsScanJSON(r) {
			writeScanJSON(w, http.StatusInternalServerError, map[string]string{"status": "lookup_failed"})
			return
		}
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "lookup_failed",
			"Form":  map[string]string{"barcode": barcode},
		})
		return
	}
	reviewURL := "/items/scan/review?flow=" + url.QueryEscape(token)
	if wantsScanJSON(r) {
		writeScanJSON(w, http.StatusOK, map[string]string{
			"status":     "found",
			"review_url": reviewURL,
		})
		return
	}
	http.Redirect(w, r, reviewURL, http.StatusSeeOther)
}

func (h *Handler) itemScanReviewPage(w http.ResponseWriter, r *http.Request) {
	state, token, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	h.render(w, r, "items_scan_review", "items", map[string]any{
		"Flow":  token,
		"State": scanStateView(state),
	})
}

func (h *Handler) itemScanOperationalAction(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
}

func (h *Handler) itemScanConfirmAction(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
}

func (h *Handler) decodeScanFlow(w http.ResponseWriter, r *http.Request) (scanFlowState, string, bool) {
	token := r.URL.Query().Get("flow")
	if token == "" {
		token = r.PostFormValue("flow")
	}
	state, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Decode(token, tenantIDFromCtx(r.Context()))
	if err != nil {
		http.Redirect(w, r, "/items/scan?flash=scan_expired", http.StatusSeeOther)
		return scanFlowState{}, "", false
	}
	return state, token, true
}

func normalizeScanBarcode(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "\r\n\t")
	if len(v) > 64 {
		return ""
	}
	return v
}

func productFieldsFromLookup(fields map[string]any) scanProductFields {
	return scanProductFields{
		Name:               stringField(fields, "name"),
		Brand:              stringField(fields, "brand"),
		Size:               stringField(fields, "size"),
		ImageURL:           stringField(fields, "image_url"),
		CategorySuggestion: stringField(fields, "category"),
	}
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	if v, ok := fields[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func wantsScanJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeScanJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func scanStateView(state scanFlowState) map[string]any {
	confidence := "0%"
	if state.Confidence > 0 {
		confidence = fmt.Sprintf("%.0f%%", state.Confidence*100)
	}
	return map[string]any{
		"Barcode":       state.Barcode,
		"Source":        state.Source,
		"Confidence":    confidence,
		"PartialFields": state.PartialFields,
		"Product":       state.Product,
		"Operational":   state.Operational,
	}
}
```

- [ ] **Step 4: Render duplicate and retry in scan template**

In `internal/web/templates/items/scan.html`, inside the flash block add:

```html
    {{else if eq .Flash "duplicate_barcode"}}Barcode already exists.
```

After the flash block, add:

```html
{{with .Duplicate}}
<div class="ops-card" style="margin-bottom:16px;border-left:3px solid var(--signal-yellow);">
  <h2 style="margin:0 0 8px;font-size:18px;">Barcode already exists</h2>
  <p style="margin:0 0 12px;color:var(--text-secondary);font-size:13px;">{{.Description}} - SKU {{.SKU}}</p>
  <div style="display:flex;gap:10px;align-items:center;">
    <a href="/items/{{.ID}}" style="padding:8px 14px;background:var(--signal-yellow);color:#000;border-radius:6px;font-size:13px;font-weight:600;text-decoration:none;">Open existing item</a>
    <a href="/items/new" style="padding:8px 14px;color:var(--text-muted);text-decoration:none;font-size:13px;">Create related item</a>
  </div>
</div>
{{end}}
```

- [ ] **Step 5: Implement review template**

Replace `internal/web/templates/items/scan_review.html` with:

```html
{{template "base.html" .}}

{{define "title"}}Review item - Canary{{end}}

{{define "content"}}
{{with .Data}}
{{$csrf := $.CSRFField}}
{{$state := .State}}
{{$product := index $state "Product"}}
<div class="page-header">
  <h1>Review item</h1>
  <p class="page-subtitle">Confirm lookup details before adding store fields.</p>
</div>

<form method="POST" action="/items/scan/operational" class="ops-card" style="max-width:760px;">
  {{$csrf}}
  <input type="hidden" name="flow" value="{{.Flow}}" />

  <div style="display:flex;justify-content:space-between;gap:16px;align-items:flex-start;margin-bottom:18px;">
    <div>
      <div style="font-size:12px;color:var(--text-muted);margin-bottom:4px;">Barcode/GTIN</div>
      <div style="font-family:'Space Grotesk',monospace;font-size:14px;color:var(--text-primary);">{{index $state "Barcode"}}</div>
    </div>
    <div style="display:flex;gap:8px;align-items:center;">
      {{template "components/status-pill" (dict "label" (index $state "Source") "tone" "info")}}
      {{template "components/status-pill" (dict "label" (index $state "Confidence") "tone" "success")}}
    </div>
  </div>

  <div style="display:grid;grid-template-columns:1fr;gap:16px;">
    {{template "components/form-field" (dict "name" "name" "label" "Item name" "value" $product.Name "required" true)}}
    {{template "components/form-field" (dict "name" "brand" "label" "Brand" "value" $product.Brand)}}
    {{template "components/form-field" (dict "name" "size" "label" "Size" "value" $product.Size)}}
    {{template "components/form-field" (dict "name" "image_url" "label" "Image URL" "type" "url" "value" $product.ImageURL)}}
    {{template "components/form-field" (dict "name" "category_suggestion" "label" "Category suggestion" "value" $product.CategorySuggestion)}}
  </div>

  <div style="display:flex;gap:12px;align-items:center;margin-top:24px;padding-top:20px;border-top:1px solid var(--border-subtle);">
    <button type="submit" style="padding:10px 20px;background:var(--signal-yellow);color:#000;border:none;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer;">Continue</button>
    <a href="/items/scan" style="padding:10px 16px;color:var(--text-muted);text-decoration:none;font-size:13px;">Start over</a>
  </div>
</form>
{{end}}
{{end}}
```

- [ ] **Step 6: Run lookup tests**

Run:

```bash
gofmt -w internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go
go test ./internal/web -run 'TestItemScanLookup|TestItemScanReview'
```

Expected: pass.

- [ ] **Step 7: Commit Task 4**

Run:

```bash
git add internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go internal/web/templates/items/scan.html internal/web/templates/items/scan_review.html
git commit -m "feat: add item scan lookup review"
```

## Task 5: Operational Fields and Confirm Preview

**Files:**

- Modify: `internal/web/handler_items_scan.go`
- Modify: `internal/web/handler_items_scan_test.go`
- Modify: `internal/web/templates/items/scan_operational.html`
- Modify: `internal/web/templates/items/scan_confirm.html`

- [ ] **Step 1: Add operational tests**

Append to `internal/web/handler_items_scan_test.go`:

```go
func TestItemScanReview_TamperedFlowRedirectsToScan(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan/review?flow=tampered", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/items/scan?flash=scan_expired" {
		t.Fatalf("Location = %q, want scan_expired", loc)
	}
}

func TestItemScanOperational_ValidationPreservesInput(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	form := url.Values{
		"flow":  {token},
		"name":  {"Organic Whole Milk"},
		"brand": {"Clover"},
	}
	rec := postScanForm(r, "/items/scan/operational", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Store fields", "SKU is required", "Organic Whole Milk"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestItemScanOperational_ValidRendersConfirm(t *testing.T) {
	token := fixedScanToken(t, fixedScanState())
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})

	form := url.Values{
		"flow":            {token},
		"name":            {"Organic Whole Milk"},
		"brand":           {"Clover"},
		"sku":             {"MILK-001"},
		"unit_of_measure": {"EA"},
		"unit_cost":       {"2.49"},
		"selling_price":   {"4.99"},
		"status":          {"active"},
	}
	rec := postScanForm(r, "/items/scan/operational", form)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Confirm item", "MILK-001", "Organic Whole Milk", `name="intent" value="create"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run operational tests and verify failure**

Run:

```bash
go test ./internal/web -run 'TestItemScanReview_Tampered|TestItemScanOperational'
```

Expected: fail because operational handling is still a redirect stub.

- [ ] **Step 3: Implement operational parsing and validation**

In `internal/web/handler_items_scan.go`, replace `itemScanOperationalAction` with:

```go
func (h *Handler) itemScanOperationalAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/items/scan?flash=invalid_form", http.StatusSeeOther)
		return
	}
	state, _, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	state.Product = scanProductFields{
		Name:               strings.TrimSpace(r.PostFormValue("name")),
		Brand:              strings.TrimSpace(r.PostFormValue("brand")),
		Size:               strings.TrimSpace(r.PostFormValue("size")),
		ImageURL:           strings.TrimSpace(r.PostFormValue("image_url")),
		CategorySuggestion: strings.TrimSpace(r.PostFormValue("category_suggestion")),
	}
	state.Operational = scanOperationalFields{
		SKU:           strings.TrimSpace(r.PostFormValue("sku")),
		CategoryID:    strings.TrimSpace(r.PostFormValue("category_id")),
		VendorID:      strings.TrimSpace(r.PostFormValue("vendor_id")),
		UnitOfMeasure: defaultIfEmpty(strings.TrimSpace(r.PostFormValue("unit_of_measure")), "EA"),
		UnitCost:      strings.TrimSpace(r.PostFormValue("unit_cost")),
		SellingPrice:  strings.TrimSpace(r.PostFormValue("selling_price")),
		CasePack:      strings.TrimSpace(r.PostFormValue("case_pack")),
		Status:        defaultIfEmpty(strings.TrimSpace(r.PostFormValue("status")), "active"),
	}

	if missing := firstMissingScanOperational(state); missing != "" {
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "missing_"+missing))
		return
	}

	token, err := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantIDFromCtx(r.Context()), state)
	if err != nil {
		h.logger.Error("itemScanOperationalAction: encode", zap.Error(err))
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "scan_expired"))
		return
	}
	h.render(w, r, "items_scan_confirm", "items", map[string]any{
		"Flow":  token,
		"State": scanStateView(state),
	})
}

func firstMissingScanOperational(state scanFlowState) string {
	switch {
	case state.Product.Name == "":
		return "name"
	case state.Operational.SKU == "":
		return "sku"
	case state.Operational.UnitCost == "":
		return "unit_cost"
	case state.Operational.SellingPrice == "":
		return "selling_price"
	default:
		return ""
	}
}
```

Add helper:

```go
func (h *Handler) scanOperationalView(r *http.Request, state scanFlowState, flash string) map[string]any {
	token, _ := newScanFlowTokenCodec(h.deps.ScanFlowSecret).Encode(tenantIDFromCtx(r.Context()), state)
	view := map[string]any{
		"Flash":      flash,
		"Flow":       token,
		"State":      scanStateView(state),
		"Categories": []map[string]any{},
		"Vendors":    []map[string]any{},
	}
	if h.deps.ItemStore != nil {
		ctx := r.Context()
		tenantID := tenantIDFromCtx(ctx)
		if cats, err := h.deps.ItemStore.ListCategories(ctx, tenantID); err == nil {
			view["Categories"] = categoryDropdownView(cats)
		}
		if vendors, err := h.deps.ItemStore.ListVendors(ctx, tenantID); err == nil {
			view["Vendors"] = vendorDropdownView(vendors)
		}
	}
	return view
}
```

- [ ] **Step 4: Update review form to send defaults**

In `internal/web/templates/items/scan_review.html`, before the submit button add hidden defaults:

```html
  <input type="hidden" name="sku" value="{{index $state "Barcode"}}" />
  <input type="hidden" name="unit_of_measure" value="EA" />
  <input type="hidden" name="status" value="active" />
```

- [ ] **Step 5: Implement operational template**

Replace `internal/web/templates/items/scan_operational.html` with:

```html
{{template "base.html" .}}

{{define "title"}}Store fields - Canary{{end}}

{{define "content"}}
{{with .Data}}
{{$csrf := $.CSRFField}}
{{$state := .State}}
{{$product := index $state "Product"}}
{{$op := index $state "Operational"}}
<div class="page-header">
  <h1>Store fields</h1>
  <p class="page-subtitle">Add the fields Canary needs to sell and report this item.</p>
</div>

{{if .Flash}}
<div class="ops-card" style="margin-bottom:16px;border-left:3px solid var(--signal-yellow);">
  <div style="font-size:13px;color:var(--text-primary);">
    {{if eq .Flash "missing_name"}}Item name is required.
    {{else if eq .Flash "missing_sku"}}SKU is required.
    {{else if eq .Flash "missing_unit_cost"}}Unit cost is required.
    {{else if eq .Flash "missing_selling_price"}}Selling price is required.
    {{else}}Check the fields and try again.
    {{end}}
  </div>
</div>
{{end}}

<form method="POST" action="/items/scan/operational" class="ops-card" style="max-width:760px;">
  {{$csrf}}
  <input type="hidden" name="flow" value="{{.Flow}}" />
  <input type="hidden" name="name" value="{{$product.Name}}" />
  <input type="hidden" name="brand" value="{{$product.Brand}}" />
  <input type="hidden" name="size" value="{{$product.Size}}" />
  <input type="hidden" name="image_url" value="{{$product.ImageURL}}" />
  <input type="hidden" name="category_suggestion" value="{{$product.CategorySuggestion}}" />

  <div style="display:grid;grid-template-columns:1fr;gap:16px;">
    {{template "components/form-field" (dict "name" "sku" "label" "SKU" "value" $op.SKU "required" true)}}
    <label style="display:flex;flex-direction:column;gap:6px;font-size:13px;color:var(--text-primary);">
      Category
      <select name="category_id" style="padding:8px 12px;background:var(--bg-surface);border:1px solid var(--border-subtle);border-radius:6px;color:var(--text-primary);font-size:13px;outline:none;">
        <option value="">No category</option>
        {{range .Categories}}
        <option value="{{.ID}}" {{if eq $op.CategoryID .ID}}selected{{end}}>{{.Name}}</option>
        {{end}}
      </select>
    </label>
    <label style="display:flex;flex-direction:column;gap:6px;font-size:13px;color:var(--text-primary);">
      Supplier
      <select name="vendor_id" style="padding:8px 12px;background:var(--bg-surface);border:1px solid var(--border-subtle);border-radius:6px;color:var(--text-primary);font-size:13px;outline:none;">
        <option value="">No supplier yet</option>
        {{range .Vendors}}
        <option value="{{.ID}}" {{if eq $op.VendorID .ID}}selected{{end}}>{{.Name}}</option>
        {{end}}
      </select>
    </label>
    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px;">
      {{template "components/form-field" (dict "name" "unit_cost" "label" "Unit cost" "type" "number" "value" $op.UnitCost "required" true)}}
      {{template "components/form-field" (dict "name" "selling_price" "label" "Selling price" "type" "number" "value" $op.SellingPrice "required" true)}}
    </div>
    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px;">
      <label style="display:flex;flex-direction:column;gap:6px;font-size:13px;color:var(--text-primary);">
        Unit of measure
        <select name="unit_of_measure" style="padding:8px 12px;background:var(--bg-surface);border:1px solid var(--border-subtle);border-radius:6px;color:var(--text-primary);font-size:13px;outline:none;">
          <option value="EA" {{if or (eq $op.UnitOfMeasure "EA") (eq $op.UnitOfMeasure "")}}selected{{end}}>EA - Each</option>
          <option value="LB" {{if eq $op.UnitOfMeasure "LB"}}selected{{end}}>LB - Pound</option>
          <option value="OZ" {{if eq $op.UnitOfMeasure "OZ"}}selected{{end}}>OZ - Ounce</option>
          <option value="KG" {{if eq $op.UnitOfMeasure "KG"}}selected{{end}}>KG - Kilogram</option>
          <option value="L" {{if eq $op.UnitOfMeasure "L"}}selected{{end}}>L - Liter</option>
        </select>
      </label>
      {{template "components/form-field" (dict "name" "case_pack" "label" "Case pack" "type" "number" "value" $op.CasePack)}}
    </div>
    <label style="display:flex;flex-direction:column;gap:6px;font-size:13px;color:var(--text-primary);">
      Status
      <select name="status" style="padding:8px 12px;background:var(--bg-surface);border:1px solid var(--border-subtle);border-radius:6px;color:var(--text-primary);font-size:13px;outline:none;">
        <option value="active" {{if or (eq $op.Status "active") (eq $op.Status "")}}selected{{end}}>Active - start selling</option>
        <option value="hidden" {{if eq $op.Status "hidden"}}selected{{end}}>Hidden - set up but not yet at POS</option>
      </select>
    </label>
  </div>

  <div style="display:flex;gap:12px;align-items:center;margin-top:24px;padding-top:20px;border-top:1px solid var(--border-subtle);">
    <button type="submit" style="padding:10px 20px;background:var(--signal-yellow);color:#000;border:none;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer;">Review final item</button>
    <a href="/items/scan" style="padding:10px 16px;color:var(--text-muted);text-decoration:none;font-size:13px;">Start over</a>
  </div>
</form>
{{end}}
{{end}}
```

- [ ] **Step 6: Implement confirm template**

Replace `internal/web/templates/items/scan_confirm.html` with:

```html
{{template "base.html" .}}

{{define "title"}}Confirm item - Canary{{end}}

{{define "content"}}
{{with .Data}}
{{$csrf := $.CSRFField}}
{{$state := .State}}
{{$product := index $state "Product"}}
{{$op := index $state "Operational"}}
<div class="page-header">
  <h1>Confirm item</h1>
  <p class="page-subtitle">Create this item and barcode in Canary.</p>
</div>

<form method="POST" action="/items/scan/confirm" class="ops-card" style="max-width:760px;">
  {{$csrf}}
  <input type="hidden" name="flow" value="{{.Flow}}" />
  <input type="hidden" name="intent" value="create" />

  <div style="display:grid;grid-template-columns:140px 1fr;gap:10px;font-size:13px;">
    <div style="color:var(--text-muted);">Item name</div><div>{{$product.Name}}</div>
    <div style="color:var(--text-muted);">SKU</div><div>{{$op.SKU}}</div>
    <div style="color:var(--text-muted);">Barcode/GTIN</div><div>{{index $state "Barcode"}}</div>
    <div style="color:var(--text-muted);">Unit cost</div><div>{{$op.UnitCost}}</div>
    <div style="color:var(--text-muted);">Selling price</div><div>{{$op.SellingPrice}}</div>
    <div style="color:var(--text-muted);">Source</div><div>{{index $state "Source"}} {{template "components/status-pill" (dict "label" (index $state "Confidence") "tone" "info")}}</div>
  </div>

  <div style="display:flex;gap:12px;align-items:center;margin-top:24px;padding-top:20px;border-top:1px solid var(--border-subtle);">
    <button type="submit" style="padding:10px 20px;background:var(--signal-yellow);color:#000;border:none;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer;">Create item</button>
    <a href="/items/scan" style="padding:10px 16px;color:var(--text-muted);text-decoration:none;font-size:13px;">Start over</a>
  </div>
</form>
{{end}}
{{end}}
```

- [ ] **Step 7: Run operational tests**

Run:

```bash
gofmt -w internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go
go test ./internal/web -run 'TestItemScanReview_Tampered|TestItemScanOperational'
```

Expected: pass.

- [ ] **Step 8: Commit Task 5**

Run:

```bash
git add internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go internal/web/templates/items/scan_review.html internal/web/templates/items/scan_operational.html internal/web/templates/items/scan_confirm.html
git commit -m "feat: add item scan operational step"
```

## Task 6: Final Confirm Create and Metadata

**Files:**

- Modify: `internal/web/handler_items_scan.go`
- Modify: `internal/web/handler_items_scan_test.go`

- [ ] **Step 1: Add final create tests**

Append to `internal/web/handler_items_scan_test.go`:

```go
func TestItemScanConfirmCreate_CreatesItemAndBarcode(t *testing.T) {
	state := fixedScanState()
	state.Operational = scanOperationalFields{
		SKU:           "MILK-001",
		UnitOfMeasure: "EA",
		UnitCost:      "2.49",
		SellingPrice:  "4.99",
		Status:        "active",
	}
	token := fixedScanToken(t, state)
	createdID := uuid.New()
	var captured item.CreateRequest
	store := &scanItemStoreStub{
		createFn: func(_ context.Context, req item.CreateRequest) (*item.Item, error) {
			captured = req
			return &item.Item{ID: createdID, TenantID: req.TenantID, SKU: req.SKU, Description: req.Description}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store})

	rec := postScanForm(r, "/items/scan/confirm", url.Values{
		"flow":   {token},
		"intent": {"create"},
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/items/"+createdID.String()+"?flash=created" {
		t.Fatalf("Location = %q", loc)
	}
	if captured.TenantID != testTenant {
		t.Errorf("TenantID = %s, want %s", captured.TenantID, testTenant)
	}
	if captured.SKU != "MILK-001" || captured.Description != "Organic Whole Milk" {
		t.Errorf("captured item = %#v", captured)
	}
	if len(captured.Barcodes) != 1 || captured.Barcodes[0].Value != "012345678905" {
		t.Fatalf("Barcodes = %#v, want scanned barcode", captured.Barcodes)
	}
	if len(captured.Attributes) == 0 || !strings.Contains(string(captured.Attributes), "Open Food Facts") {
		t.Fatalf("Attributes = %s, want lookup metadata", string(captured.Attributes))
	}
}

func TestItemScanConfirmCreate_FinalDuplicateRace(t *testing.T) {
	state := fixedScanState()
	state.Operational.SKU = "MILK-001"
	state.Operational.UnitCost = "2.49"
	state.Operational.SellingPrice = "4.99"
	token := fixedScanToken(t, state)
	existingID := uuid.New()
	store := &scanItemStoreStub{
		getByBarcodeFn: func(_ context.Context, _ uuid.UUID, _ string) (*item.Item, error) {
			return &item.Item{ID: existingID, SKU: "EXISTING", Description: "Existing item"}, nil
		},
	}
	r := newItemScanRouter(t, Deps{ItemStore: store})

	rec := postScanForm(r, "/items/scan/confirm", url.Values{
		"flow":   {token},
		"intent": {"create"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Barcode already exists", "/items/" + existingID.String()} {
		if !strings.Contains(body, want) {
			t.Errorf("duplicate race body missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run final create tests and verify failure**

Run:

```bash
go test ./internal/web -run 'TestItemScanConfirmCreate'
```

Expected: fail because confirm create is still a redirect stub.

- [ ] **Step 3: Implement confirm create**

In `internal/web/handler_items_scan.go`, replace `itemScanConfirmAction` with:

Update the import block to include:

```go
	"encoding/json"

	"github.com/google/uuid"
```

```go
func (h *Handler) itemScanConfirmAction(w http.ResponseWriter, r *http.Request) {
	if h.deps.ItemStore == nil {
		http.Redirect(w, r, "/items/scan?flash=no_store", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/items/scan?flash=invalid_form", http.StatusSeeOther)
		return
	}
	state, _, ok := h.decodeScanFlow(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("intent") != "create" {
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":  r.PostFormValue("flow"),
			"State": scanStateView(state),
		})
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromCtx(ctx)
	existing, err := h.deps.ItemStore.GetByBarcode(ctx, tenantID, state.Barcode)
	if err == nil && existing != nil {
		h.render(w, r, "items_scan", "items", map[string]any{
			"Flash": "duplicate_barcode",
			"Form":  map[string]string{"barcode": state.Barcode},
			"Duplicate": map[string]string{
				"ID":          existing.ID.String(),
				"SKU":         existing.SKU,
				"Description": existing.Description,
			},
		})
		return
	}
	if err != nil && !errors.Is(err, item.ErrNotFound) {
		h.logger.Error("itemScanConfirmAction: final GetByBarcode", zap.Error(err))
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":  r.PostFormValue("flow"),
			"State": scanStateView(state),
			"Flash": "create_failed",
		})
		return
	}

	req, err := createRequestFromScanState(tenantID, state)
	if err != nil {
		h.render(w, r, "items_scan_operational", "items", h.scanOperationalView(r, state, "validation_failed"))
		return
	}
	created, err := h.deps.ItemStore.Create(ctx, req)
	if err != nil {
		if errors.Is(err, item.ErrConflict) {
			h.render(w, r, "items_scan", "items", map[string]any{
				"Flash": "duplicate_barcode",
				"Form":  map[string]string{"barcode": state.Barcode},
			})
			return
		}
		h.logger.Error("itemScanConfirmAction: Create", zap.Error(err))
		h.render(w, r, "items_scan_confirm", "items", map[string]any{
			"Flow":  r.PostFormValue("flow"),
			"State": scanStateView(state),
			"Flash": "create_failed",
		})
		return
	}
	http.Redirect(w, r, "/items/"+created.ID.String()+"?flash=created", http.StatusSeeOther)
}
```

Add helpers:

```go
func createRequestFromScanState(tenantID uuid.UUID, state scanFlowState) (item.CreateRequest, error) {
	if firstMissingScanOperational(state) != "" {
		return item.CreateRequest{}, item.ErrValidation
	}
	attrs, err := json.Marshal(map[string]any{
		"scan_lookup": map[string]any{
			"source":         state.Source,
			"confidence":     state.Confidence,
			"partial_fields": state.PartialFields,
			"brand":          state.Product.Brand,
			"size":           state.Product.Size,
			"image_url":      state.Product.ImageURL,
		},
	})
	if err != nil {
		return item.CreateRequest{}, err
	}
	req := item.CreateRequest{
		TenantID:      tenantID,
		SKU:           state.Operational.SKU,
		Description:   state.Product.Name,
		Attributes:    attrs,
		Barcodes:      []item.BarcodeRequest{{Value: state.Barcode, IsPrimary: boolPtr(true)}},
	}
	if state.Operational.CategoryID != "" {
		if id, err := uuid.Parse(state.Operational.CategoryID); err == nil {
			req.CategoryID = &id
		}
	}
	if state.Operational.UnitOfMeasure != "" {
		req.UnitOfMeasure = &state.Operational.UnitOfMeasure
	}
	if state.Operational.UnitCost != "" {
		req.DefaultCost = &state.Operational.UnitCost
	}
	if state.Operational.SellingPrice != "" {
		req.DefaultPrice = &state.Operational.SellingPrice
	}
	if state.Operational.Status != "" {
		req.Status = &state.Operational.Status
	}
	return req, nil
}

func boolPtr(v bool) *bool {
	return &v
}
```

- [ ] **Step 4: Add confirm flash rendering**

In `internal/web/templates/items/scan_confirm.html`, after the page header add:

```html
{{if .Flash}}
<div class="ops-card" style="margin-bottom:16px;border-left:3px solid var(--signal-yellow);">
  <div style="font-size:13px;color:var(--text-primary);">
    {{if eq .Flash "create_failed"}}Could not create the item. Try again or use manual entry.
    {{else}}Check the fields and try again.
    {{end}}
  </div>
</div>
{{end}}
```

- [ ] **Step 5: Run final create tests**

Run:

```bash
gofmt -w internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go
go test ./internal/web -run 'TestItemScanConfirmCreate'
```

Expected: pass.

- [ ] **Step 6: Commit Task 6**

Run:

```bash
git add internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go internal/web/templates/items/scan_confirm.html
git commit -m "feat: create item from scan confirm"
```

## Task 7: UI Polish and Standards Pass

**Files:**

- Modify: `internal/web/templates/items/scan.html`
- Modify: `internal/web/templates/items/scan_review.html`
- Modify: `internal/web/templates/items/scan_operational.html`
- Modify: `internal/web/templates/items/scan_confirm.html`
- Modify: `internal/web/handler_items_scan_test.go`

- [ ] **Step 1: Add UI standards smoke test**

Append to `internal/web/handler_items_scan_test.go`:

```go
func TestItemScanFlow_UIVocabulary(t *testing.T) {
	r := newItemScanRouter(t, Deps{ItemStore: &scanItemStoreStub{}})
	req := httptest.NewRequest(http.MethodGet, "/items/scan", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Scan item", "Barcode", "Manual entry"} {
		if !strings.Contains(body, want) {
			t.Errorf("scan UI missing approved copy %q", want)
		}
	}
	for _, forbidden := range []string{"Product ID", "tenant_id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("scan UI contains forbidden copy %q", forbidden)
		}
	}
}
```

- [ ] **Step 2: Run UI test and verify current state**

Run:

```bash
go test ./internal/web -run TestItemScanFlow_UIVocabulary
```

Expected: pass after prior tasks. If it fails, adjust visible copy to use Item, SKU, barcode/GTIN, Supplier, Operator, and Source.

- [ ] **Step 3: Polish responsive layout**

Apply these rules directly in the four scan templates:

```html
style="max-width:760px;"
```

for the main form cards, and:

```html
style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px;"
```

for paired field groups. Avoid nested cards. Keep action buttons in one row when width allows and wrapping when narrow.

- [ ] **Step 4: Run targeted web tests**

Run:

```bash
gofmt -w internal/web/handler_items_scan_test.go
go test ./internal/web -run 'TestItemScan'
```

Expected: pass.

- [ ] **Step 5: Commit Task 7**

Run:

```bash
git add internal/web/templates/items/scan.html internal/web/templates/items/scan_review.html internal/web/templates/items/scan_operational.html internal/web/templates/items/scan_confirm.html internal/web/handler_items_scan_test.go
git commit -m "feat: polish item scan UI"
```

## Task 8: Acceptance Verification and Handoff

**Files:**

- No code edits unless verification exposes a defect in GRO-901-owned files.

- [ ] **Step 1: Run targeted tests**

Run:

```bash
go test ./internal/web -run 'TestItemScan|TestScanFlow'
go test ./internal/catalog/barcodelookup/...
go test ./internal/web ./internal/catalog/barcodelookup/...
```

Expected: pass.

- [ ] **Step 2: Run broad checks**

Run:

```bash
go test ./...
go vet ./...
git diff --check
```

Expected: pass. If a broad check fails because of a pre-existing `main` issue, rerun the same check on clean `main`, record the failing package/test, and keep the targeted GRO-901 checks green.

- [ ] **Step 3: Acceptance evidence**

Collect this evidence in the final report or Linear comment:

```text
GRO-901 acceptance evidence:
- GET /items/scan renders scan form with no tenant_id field.
- Duplicate barcode short-circuits external lookup and offers Open existing / Create related item.
- Lookup found path reaches review with source and confidence.
- Barcode not found redirects to /items/new with barcode preserved.
- All sources failed renders retry/manual fallback.
- Tampered/expired tokens restart the scan flow.
- Operational validation preserves input.
- Confirm creates one item and one barcode with tenant from context.
- go test ./internal/web -run 'TestItemScan|TestScanFlow' passed.
- go test ./internal/catalog/barcodelookup/... passed.
- go test ./internal/web ./internal/catalog/barcodelookup/... passed.
```

- [ ] **Step 4: Final commit if verification fixes were needed**

If Task 8 required edits, run:

```bash
git status --short
git add internal/web/handler_items_scan.go internal/web/handler_items_scan_test.go internal/web/templates/items/scan.html internal/web/templates/items/scan_review.html internal/web/templates/items/scan_operational.html internal/web/templates/items/scan_confirm.html internal/web/templates/items/list.html internal/web/scanflow_token.go internal/web/scanflow_token_test.go internal/web/barcode_lookup.go internal/web/deps.go cmd/gateway/main.go
git commit -m "fix: close item scan acceptance gaps"
```

If no edits were needed, do not create an empty commit.

## Completion Criteria

- GRO-901 routes exist and are protected by the existing tenant middleware.
- No scan flow form exposes `tenant_id`.
- ItemStore is wired in gateway so the item UI is usable outside tests.
- Barcode lookup resolver is wired with Open Food Facts and UPC Item DB.
- No draft catalog rows are created.
- Final confirm writes the item and barcode in one `ItemStore.Create` call.
- At least eight scan-flow tests are present; this plan creates more than eight.
- Targeted web and barcode lookup tests pass.
- Full-suite failures, if any, are proven pre-existing before handoff.
