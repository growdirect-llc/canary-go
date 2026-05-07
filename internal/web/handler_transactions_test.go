package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/growdirect-llc/rapidpos/internal/protocol/validate"
	"github.com/growdirect-llc/rapidpos/internal/testutil"
	"github.com/growdirect-llc/rapidpos/internal/transaction"
	"github.com/growdirect-llc/rapidpos/internal/web"
)

// seedTxnTenant inserts an org/tenant + a location.locations row and returns
// the tenant + location ids. Uses the location.locations table (tenant-scoped)
// because transaction.transactions.location_id FK targets it.
func seedTxnTenant(t *testing.T, ctx context.Context) (uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := testutil.MustConnect(t)
	orgID := uuid.New()
	tenantID := uuid.New()
	locID := uuid.New()

	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)`,
		orgID, "txn-test-org-"+orgID.String()[:8]); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $2, $3, $4, $5)`,
		tenantID, orgID,
		"txn-t-"+tenantID.String()[:8],
		"Txn Test Tenant",
		"txn_t_"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO location.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locID, tenantID, "LOC-"+locID.String()[:6], "Test Location"); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	return tenantID, locID
}

// seedNilTenantLocation creates a uuid.Nil-tenant org/tenant + location row
// so transactions can be inserted under the nil tenant (matches the pre-
// identity-middleware web request path). Idempotent: returns the existing row
// if a uuid.Nil tenant has already been seeded.
func seedNilTenantLocation(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	pool := testutil.MustConnect(t)

	// Org row with id = uuid.Nil — primary key conflict means it already exists.
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.organizations (id, org_name) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`,
		uuid.Nil, "nil-tenant-org"); err != nil {
		t.Fatalf("seed nil org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app.tenants (id, organization_id, tenant_code, name, schema_name)
		 VALUES ($1, $1, $2, $3, $4)
		 ON CONFLICT (id) DO NOTHING`,
		uuid.Nil, "nil-tenant", "Nil Tenant (pre-identity)", "nil_tenant"); err != nil {
		t.Fatalf("seed nil tenant: %v", err)
	}

	locID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO location.locations (id, tenant_id, location_code, name)
		 VALUES ($1, $2, $3, $4)`,
		locID, uuid.Nil, "NIL-"+locID.String()[:6], "Nil-tenant Location"); err != nil {
		t.Fatalf("seed nil-tenant location: %v", err)
	}
	return locID
}

// seedTxn creates one canonical transaction row + a single line item.
// Tenders are omitted because tender_type_id is FK-required and seeding
// the tender_types table is out of scope for these handler smoke tests.
func seedTxn(t *testing.T, ctx context.Context, store *transaction.Store, tenantID, locationID uuid.UUID) *transaction.TransactionDTO {
	t.Helper()
	now := time.Now().UTC()
	dto, err := store.Create(ctx, transaction.CreateRequest{
		TenantID:          tenantID,
		TransactionNumber: "T-" + uuid.NewString()[:8],
		TransactionType:   "sale",
		LocationID:        locationID,
		BusinessDate:      now.Format("2006-01-02"),
		StartedAt:         now.Add(-2 * time.Minute),
		EndedAt:           now,
		Status:            "completed",
		Currency:          "USD",
		Channel:           "in_store",
		LineItems: []transaction.LineItemRequest{{
			LineNumber:  1,
			Description: "Test SKU",
			Quantity:    decimal.NewFromInt(1),
			UnitPrice:   decimal.NewFromFloat(9.99),
			LineTotal:   decimal.NewFromFloat(9.99),
		}},
	})
	if err != nil {
		t.Fatalf("seed txn: %v", err)
	}
	return dto
}

// TestTransactionDetail_Renders_NoStore returns the stub view when no store is wired.
func TestTransactionDetail_Renders_NoStore(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	id := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/transactions/"+id, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 stub render, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), id[:8]) {
		t.Errorf("body missing short id %q", id[:8])
	}
}

// TestTransactionDetail_BadID_Returns404 verifies a non-UUID id returns 404.
func TestTransactionDetail_BadID_Returns404(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transactions/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

// TestTransactionDetail_NotFound_Returns404 returns 404 when the txn is missing
// even though the store is wired.
func TestTransactionDetail_NotFound_Returns404(t *testing.T) {
	pool := testutil.MustConnect(t)
	deps := web.Deps{TransactionStore: transaction.NewStore(pool)}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transactions/"+uuid.New().String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}

// TestTransactionDetail_RealData seeds a transaction under a real tenant
// + matching location, then queries via the handler. The web request runs
// with tenantIDFromCtx → uuid.Nil so the GetByID returns NotFound (tenant
// scoping). This documents the current state: real data wiring works, but
// the renderer can only show data once identity middleware (GRO-769) lands
// and forwards a real tenant. The 404 IS the expected behavior pre-identity.
func TestTransactionDetail_RealData(t *testing.T) {
	ctx := context.Background()
	pool := testutil.MustConnect(t)

	tenantID, locID := seedTxnTenant(t, ctx)
	store := transaction.NewStore(pool)
	dto := seedTxn(t, ctx, store, tenantID, locID)

	// Sanity: store can fetch the row when given the real tenant.
	if got, err := store.GetByID(ctx, tenantID, dto.ID); err != nil {
		t.Fatalf("store.GetByID under real tenant: %v", err)
	} else if got.ID != dto.ID {
		t.Errorf("got id %v, want %v", got.ID, dto.ID)
	}

	deps := web.Deps{TransactionStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Web request — tenantIDFromCtx returns uuid.Nil → expect 404 due to
	// tenant isolation. This is the current design, not a bug.
	req := httptest.NewRequest(http.MethodGet, "/transactions/"+dto.ID.String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 (tenant isolation pre-identity), got %d", rr.Code)
	}
}

var _ = seedNilTenantLocation // future-use helper

// TestTransactionDetail_TenantIsolation_Returns404 — txn belongs to a real
// tenant; web request as the nil tenant should 404, not leak.
func TestTransactionDetail_TenantIsolation_Returns404(t *testing.T) {
	ctx := context.Background()
	pool := testutil.MustConnect(t)
	tenantID, locID := seedTxnTenant(t, ctx)

	store := transaction.NewStore(pool)
	dto := seedTxn(t, ctx, store, tenantID, locID)

	deps := web.Deps{TransactionStore: store}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	// Web handler fetches with tenantIDFromCtx → uuid.Nil — different tenant.
	req := httptest.NewRequest(http.MethodGet, "/transactions/"+dto.ID.String(), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 (tenant isolation), got %d", rr.Code)
	}
}

// stubValidateStore lets us return canned anchor proofs without a real anchor row.
type stubValidateStore struct {
	proofs map[string]*validate.AnchorProof
}

func (s *stubValidateStore) InsertToken(_ context.Context, _ string, _ int64) (*validate.VerificationToken, error) {
	return nil, nil
}
func (s *stubValidateStore) GetToken(_ context.Context, _ uuid.UUID) (*validate.VerificationToken, error) {
	return nil, validate.ErrNotFound
}
func (s *stubValidateStore) ConsumeToken(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubValidateStore) GetAnchorProof(_ context.Context, eventHash string) (*validate.AnchorProof, error) {
	if p, ok := s.proofs[eventHash]; ok {
		return p, nil
	}
	return nil, validate.ErrNotAnchored
}

func deriveHashForTest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// TestTransactionProof_Pending_NoAnchor — proof page renders pending banner
// when no anchor exists for the txn yet.
func TestTransactionProof_Pending_NoAnchor(t *testing.T) {
	stub := &stubValidateStore{proofs: map[string]*validate.AnchorProof{}}
	deps := web.Deps{ValidateStore: stub}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	id := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/transactions/"+id+"/proof", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "PENDING") {
		t.Errorf("expected pending banner")
	}
}

// TestTransactionProof_Valid_RendersAnchor — proof page renders anchored
// banner + Merkle path when the validate store returns a real proof.
func TestTransactionProof_Valid_RendersAnchor(t *testing.T) {
	id := uuid.New().String()
	eventHash := deriveHashForTest(id)
	merkleNodes := []map[string]any{
		{"index": 0, "hash": "aa11"},
		{"index": 1, "hash": "bb22"},
	}
	rawPath, _ := json.Marshal(merkleNodes)
	rootHash := "rootabcdef0123456789"
	inscriptionID := "insc-XYZ"
	stub := &stubValidateStore{proofs: map[string]*validate.AnchorProof{
		eventHash: {
			EventHash:     eventHash,
			AnchorID:      uuid.New(),
			MerkleRoot:    rootHash,
			InscriptionID: &inscriptionID,
			Network:       "bitcoin-signet",
			AnchorStatus:  "anchored",
			LeafIndex:     0,
			MerkleProof:   rawPath,
			AnchoredAt:    time.Now().UTC(),
		},
	}}

	deps := web.Deps{ValidateStore: stub}
	h := web.New(deps, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transactions/"+id+"/proof", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "VALID") {
		t.Errorf("expected VALID banner")
	}
	if !strings.Contains(body, rootHash) {
		t.Errorf("expected root hash %q in body", rootHash)
	}
	if !strings.Contains(body, inscriptionID) {
		t.Errorf("expected inscription id in anchor ref")
	}
	if !strings.Contains(body, "aa11") || !strings.Contains(body, "bb22") {
		t.Errorf("expected Merkle path nodes in body")
	}
}

// TestTransactionProof_BadID_Returns404
func TestTransactionProof_BadID_Returns404(t *testing.T) {
	h := web.New(web.Deps{}, nil)
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/transactions/not-uuid/proof", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 got %d", rr.Code)
	}
}
